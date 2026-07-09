package core

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// defaultTimeout 当调用方传入 timeout<=0 时使用的默认超时
const defaultTimeout = 3 * time.Second

// writeRequest 组装 RCON 请求报文并写入 TCP 连接。
//
// 这是 Send / SendFireAndForget / handshake 共享的公共写入逻辑，
// 消除了原有的 exchange() 与 SendFireAndForget() 之间的代码重复。
//
// 职责：requestID 自增 → rawRequest 组装 → JSON 序列化 → XOR 加密 →
// header+body 拼接 → 循环写入 TCP。
//
// 返回值：
//   - reqID：本次请求的编号（供 readResponse 记录，当前不校验但保留以备后用）
//   - *RconError：
//       ErrorClassLocal    — JSON 序列化失败，连接未受损，调用方不应 killConn
//       ErrorClassTransport — SetWriteDeadline 或 Write 失败，调用方应 killConn
func (c *Client) writeRequest(name, contentBody string, xorKey []byte, timeout time.Duration) (uint32, *RconError) {
	c.requestID++
	reqID := c.requestID

	rawReq := rawRequest{
		AuthToken:   c.authToken,
		Version:     2,
		Name:        name,
		ContentBody: contentBody,
	}

	body, err := json.Marshal(rawReq)
	if err != nil {
		return 0, &RconError{
			Class:   ErrorClassLocal,
			Command: name,
			Cause:   fmt.Errorf("request marshal failed: %w", err),
		}
	}

	payload := xorEncrypt(body, xorKey)

	// 组装 header + body 为一个连续字节切片
	fullReq := make([]byte, 12+len(payload))
	binary.LittleEndian.PutUint32(fullReq[0:], MagicNumber)
	binary.LittleEndian.PutUint32(fullReq[4:], reqID)
	binary.LittleEndian.PutUint32(fullReq[8:], uint32(len(payload)))
	copy(fullReq[12:], payload)

	if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return 0, &RconError{
			Class:   ErrorClassTransport,
			Command: name,
			Cause:   fmt.Errorf("set write deadline failed: %w", err),
		}
	}
	totalWritten := 0
	for totalWritten < len(fullReq) {
		n, err := c.conn.Write(fullReq[totalWritten:])
		if err != nil {
			return 0, &RconError{
				Class:   ErrorClassTransport,
				Command: name,
				Cause:   fmt.Errorf("write failed: %w", err),
			}
		}
		totalWritten += n
	}

	return reqID, nil
}

// Send 发送 RCON 命令并接收响应（纯发送，无重连/重试）
//
// 一次 Send() 只处理一条命令（互斥锁保证串行），是 Layer 0 的原子执行单元。
// 内部流程：writeRequest → readResponse → 状态码检查。
//
// 错误处理：
//   - ErrorClassTransport（超时/EOF/协议解析失败）→ killConn 关连接
//   - ErrorClassAuth（401，authToken 过期）→ killConn 关连接（authToken 已无效，连接不可复用）
//   - ErrorClassApplication（400/500）→ 不关连接，连接仍可用
//   - ErrorClassLocal（JSON 序列化失败）→ 不关连接
func (c *Client) Send(command, params string, timeout time.Duration) (*RconResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() || c.conn == nil {
		return nil, &RconError{
			Class:   ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("connection not established"),
		}
	}

	if timeout <= 0 {
		timeout = defaultTimeout
	}

	reqID, rconErr := c.writeRequest(command, params, c.key, timeout)
	if rconErr != nil {
		if rconErr.Class == ErrorClassTransport {
			c.killConn()
		}
		return nil, rconErr
	}

	resp, err := c.readResponse(c.key, timeout, reqID)
	if err != nil {
		c.killConn()
		return nil, &RconError{
			Class:   ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("response read failed: %w", err),
		}
	}

	if resp.StatusCode != StatusSuccess {
		rconErr := &RconError{
			StatusCode:    resp.StatusCode,
			StatusMessage: resp.StatusMessage,
			Command:       command,
		}
		if resp.StatusCode == StatusUnauthorized {
			rconErr.Class = ErrorClassAuth
			rconErr.Cause = fmt.Errorf("authentication token expired")
			c.killConn() // authToken 已失效，连接不可复用，关闭后让上层重建
		} else {
			rconErr.Class = ErrorClassApplication
			rconErr.Cause = fmt.Errorf("server returned non-success status")
		}
		return nil, rconErr
	}

	return resp, nil
}

// SendFireAndForget 发送不需要响应的命令
//
// 写完 TCP 报文后，调用 drainResponse 清空接收缓冲区，消除脏数据隐患：
//   - 读到数据 → 丢弃（命令有响应时，如 KickPlayer）
//   - 读超时   → 正常（命令无响应时，如 ServerBroadcast，此为预期情况）
//   - 读错误   → killConn（TCP 连接真断了）
func (c *Client) SendFireAndForget(command, params string, timeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() || c.conn == nil {
		return &RconError{
			Class:   ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("connection not established"),
		}
	}

	if timeout <= 0 {
		timeout = defaultTimeout
	}

	_, rconErr := c.writeRequest(command, params, c.key, timeout)
	if rconErr != nil {
		if rconErr.Class == ErrorClassTransport {
			c.killConn()
		}
		return rconErr
	}

	// drainResponse 用 1 秒短超时：ServerBroadcast 无响应 → 超时即正常
	if err := c.drainResponse(c.key, 1*time.Second); err != nil {
		c.killConn()
		return &RconError{
			Class:   ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("drain response failed: %w", err),
		}
	}

	return nil
}

// drainResponse 读取并丢弃 RCON 响应。
//
// 与 readResponse 使用相同的协议解析逻辑（header → body），但：
//   - 读超时 → 返回 nil（视为正常：命令无响应，如 ServerBroadcast）
//   - 读到有效报文 → 丢弃 body，返回 nil
//   - 非超时错误（EOF / 连接断开 / Magic 不匹配）→ 返回 error
func (c *Client) drainResponse(xorKey []byte, timeout time.Duration) error {
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}

	var header Header
	if err := binary.Read(c.conn, binary.LittleEndian, &header); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil // 命令无响应，正常
		}
		return fmt.Errorf("drain header read: %w", err)
	}

	if header.Magic != MagicNumber {
		return fmt.Errorf("drain: %w: expected %X, got %X", ErrMagicMismatch, MagicNumber, header.Magic)
	}

	if header.Length > MaxPayloadSize {
		return fmt.Errorf("drain: payload too large: %d", header.Length)
	}

	body := make([]byte, header.Length)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil
		}
		return fmt.Errorf("drain body read: %w", err)
	}

	return nil // 响应已丢弃
}

// readResponse 读取并解析 RCON 响应
func (c *Client) readResponse(xorKey []byte, timeout time.Duration, expectedRequestID uint32) (*RconResponse, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	var respHeader Header
	if err := binary.Read(c.conn, binary.LittleEndian, &respHeader); err != nil {
		return nil, fmt.Errorf("response %w: %v", ErrHeaderReadFailed, err)
	}
	if respHeader.Magic != MagicNumber {
		return nil, fmt.Errorf("%w: expected %X, got %X", ErrMagicMismatch, MagicNumber, respHeader.Magic)
	}
	// 不检查 RequestID —— 实测发现 HLL 服务器返回的 RequestID 不可靠
	_ = expectedRequestID

	if respHeader.Length > MaxPayloadSize {
		return nil, fmt.Errorf("payload too large: %d bytes (max %d)", respHeader.Length, MaxPayloadSize)
	}

	body := make([]byte, respHeader.Length)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, fmt.Errorf("response %w: %v", ErrBodyReadFailed, err)
	}

	decryptedBody := xorEncrypt(body, xorKey)

	var resp RconResponse
	if err := json.Unmarshal(decryptedBody, &resp); err != nil {
		return nil, fmt.Errorf("response parse failed: %w", err)
	}
	return &resp, nil
}
