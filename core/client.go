package core

import (
	"encoding/base64"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// DialOption 拨号选项
type DialOption func(*Client)

// WithSSRFCheck 启用 SSRF 检查（默认启用），设置为 false 可关闭
func WithSSRFCheck(enable bool) DialOption {
	return func(c *Client) {
		c.disableSSRFCheck = !enable
	}
}

// Client 并发安全的 RCON 客户端内核
//
// 仅提供 TCP 连接 + 握手 + 命令收发，不包含重连/重试/熔断逻辑。
// Connect/Close 可分别调用，供上层实现重连。
//
// password 仅在 Connect 时作为参数传入，不持久化存储。生命周期由上层管理。
type Client struct {
	mu               sync.RWMutex
	conn             net.Conn
	key              []byte
	authToken        string
	requestID        uint32
	host             string
	port             string
	closed           atomic.Bool
	disableSSRFCheck bool
	proxyAddr        string // HTTP CONNECT 代理地址（可选）
}

// Dial 创建并连接 RCON 客户端
func Dial(host, port, password string, timeout time.Duration, opts ...DialOption) (*Client, error) {
	client := &Client{
		host: host,
		port: port,
	}
	for _, opt := range opts {
		opt(client)
	}
	if err := client.connect(password, timeout); err != nil {
		return nil, err
	}
	return client, nil
}

// NewClient 创建未连接的 RCON 客户端（需手动调用 Connect）
//
// 可选传入 DialOption 来配置客户端行为（如 WithSSRFCheck、WithHTTPProxy）。
func NewClient(host, port string, opts ...DialOption) *Client {
	c := &Client{
		host: host,
		port: port,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Connect 建立 RCON 连接并完成 ServerConnect + Login 握手
func (c *Client) Connect(password string, timeout time.Duration) error {
	return c.connect(password, timeout)
}

func (c *Client) connect(password string, timeout time.Duration) error {
	// timeout<=0 时使用默认值，防止 handshake 的 writeRequest() 因 deadline=now 立即超时
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// 快速检查：不持锁判断已关闭状态
	if c.closed.Load() {
		return fmt.Errorf("client already closed")
	}

	// SSRF 检查：纯内存操作，无需持锁
	if !c.disableSSRFCheck {
		if err := validateDialTarget(c.host); err != nil {
			return fmt.Errorf("dial target rejected: %w", err)
		}
	}

	// TCP 拨号：在锁外执行，避免长时间阻塞 Close()
	var conn net.Conn
	var err error
	if c.proxyAddr != "" {
		conn, err = c.connectWithProxy(timeout)
	} else {
		dialer := net.Dialer{Timeout: timeout}
		conn, err = dialer.Dial("tcp4", net.JoinHostPort(c.host, c.port))
	}
	if err != nil {
		return fmt.Errorf("RCON connect failed: %w", err)
	}

	// 设置 TCP_NODELAY（禁用 Nagle 算法，降低延迟）
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	// 取得锁后再次检查关闭状态 —— 可能在上方拨号期间被 Close()
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		conn.Close()
		return fmt.Errorf("client closed during connection")
	}

	c.conn = conn
	c.requestID = 0
	c.key = nil
	c.authToken = ""

	if err := c.handshake(password, timeout); err != nil {
		conn.Close()
		c.conn = nil
		return err
	}

	return nil
}

func (c *Client) handshake(password string, timeout time.Duration) error {
	// ServerConnect
	_, rconErr := c.writeRequest("ServerConnect", "", nil, timeout)
	if rconErr != nil {
		return fmt.Errorf("ServerConnect failed: %w", rconErr)
	}
	connectResp, err := c.readResponse(nil, timeout, c.requestID)
	if err != nil {
		return fmt.Errorf("ServerConnect read failed: %w", err)
	}
	if connectResp.StatusCode != StatusSuccess {
		return fmt.Errorf("ServerConnect failed [%d]: %s", connectResp.StatusCode, connectResp.StatusMessage)
	}

	key, err := base64.StdEncoding.DecodeString(connectResp.ContentBody)
	if err != nil {
		return fmt.Errorf("XOR key decode failed: %w", err)
	}
	c.key = key

	// Login
	_, rconErr = c.writeRequest("Login", password, c.key, timeout)
	if rconErr != nil {
		return fmt.Errorf("Login failed: %w", rconErr)
	}
	loginResp, err := c.readResponse(c.key, timeout, c.requestID)
	if err != nil {
		return fmt.Errorf("Login read failed: %w", err)
	}
	if loginResp.StatusCode == StatusUnauthorized {
		return fmt.Errorf("RCON auth failed: %s", loginResp.StatusMessage)
	}
	if loginResp.StatusCode != StatusSuccess {
		return fmt.Errorf("Login failed [%d]: %s", loginResp.StatusCode, loginResp.StatusMessage)
	}

	c.authToken = loginResp.ContentBody
	return nil
}

// Close 关闭连接
func (c *Client) Close() error {
	c.closed.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// killConn 在已持有 mu 的情况下关闭 TCP 连接并标记 Client 失效。
//
// 用于 Send/SendFireAndForget 传输层错误时的"物理清理"——
// 这些方法已持有 mu，不能调用 Close()（会死锁），所以用此方法直接操作。
// 关闭连接后内核丢弃接收缓冲区，保证下一次 Send() 必然是新连接，不会读到脏数据。
func (c *Client) killConn() {
	c.closed.Store(true)
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// IsClosed 检查连接是否已关闭
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

// Addr 返回目标地址
func (c *Client) Addr() string {
	return net.JoinHostPort(c.host, c.port)
}
