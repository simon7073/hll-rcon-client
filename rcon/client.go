// Package rcon 提供 HLL RCONv2 客户端和连接池
//
// 本包在 core 包之上添加自动重连/重试、连接池、熔断器等高阶能力。
// core 包提供纯 TCP 收发，本包负责错误分类和恢复策略。
package rcon

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
)

// fireAndForgetCommands 列出 RCON 协议中不返回响应的命令。
// Send() 检测到这些命令时，内部路由到 SendFireAndForget 路径，
// 成功后返回 synthetic 200 OK 响应，让调用方可以统一使用 Send()。
var fireAndForgetCommands = map[string]bool{
	"ServerBroadcast": true,
}

// Client Layer 1 RCON 客户端 — 在 core.Client 之上添加自动重连
//
// 契约：
//   - Layer 0 传输错误时已自动关闭连接（物理清理），返回 ErrorClassTransport
//   - Layer 1 收到 ErrorClassTransport 后只需"重建连接 + 重试"，不需要判断要不要关
//   - ErrorClassApplication（400/500）连接仍可用，直接返回
type Client struct {
	core             *core.Client
	host             string
	port             string
	password         string
	disableReconnect bool               // 池管理模式下禁用自动重连，由 Pool 的健康检查和 Acquire 负责
	dialOpts         []core.DialOption  // 拨号选项（如代理配置）
	reconnectMu      sync.Mutex         // 保护 reconnect 临界区，防止并发 Send 同时重建连接导致 TCP 泄漏
}

// NewClient 创建 Layer 1 客户端（需要手动 Connect）
func NewClient(host, port, password string, opts ...core.DialOption) *Client {
	return &Client{
		host:     host,
		port:     port,
		password: password,
		dialOpts: opts,
	}
}

// Connect 建立连接（委托给 core.Client.Connect）
//
// 失败时关闭底层的 core.Client 并将 c.core 置 nil，确保 IsClosed() 返回 true，
// 避免上层持有一个不可用的半初始化连接。
func (c *Client) Connect(timeout time.Duration) error {
	if c.core != nil {
		c.core.Close()
	}
	c.core = core.NewClient(c.host, c.port)
	// 应用拨号选项（如代理配置）
	for _, opt := range c.dialOpts {
		opt(c.core)
	}
	if err := c.core.Connect(c.password, timeout); err != nil {
		c.core.Close()
		c.core = nil
		return err
	}
	return nil
}

// Close 关闭连接
func (c *Client) Close() error {
	if c.core != nil {
		return c.core.Close()
	}
	return nil
}

// IsClosed 检查连接是否已关闭
func (c *Client) IsClosed() bool {
	return c.core == nil || c.core.IsClosed()
}

// Addr 返回目标地址（不依赖连接状态）
func (c *Client) Addr() string {
	return net.JoinHostPort(c.host, c.port)
}

// newClientWithCore 使用已有 core.Client 创建 Layer 1 客户端
//
// 仅供连接池内部使用。外部调用者应通过 NewClient + Connect 或 Pool.Acquire 获取客户端。
func newClientWithCore(host, port, password string, cc *core.Client) *Client {
	return &Client{
		core:             cc,
		host:             host,
		port:             port,
		password:         password,
		disableReconnect: true, // 池管理模式下禁止自动重连，由 Pool 负责
	}
}

// Send 发送命令（独立客户端自动重建连接 + 重试一次）
//
// 契约：
//   - Layer 0 传输错误时已自动关闭连接（killConn），返回 ErrorClassTransport
//   - Layer 0 认证错误（authToken 过期）返回 ErrorClassAuth
//   - Layer 1 收到 ErrorClassTransport 或 ErrorClassAuth 后：
//     → 独立客户端：创建新 core.Client，重连（重新握手），重试一次
//     → 池管理模式（disableReconnect=true）：直接返回错误，由 Pool 负责恢复
//   - 连接在调用前已断开（如上次传输错误被 killConn）：
//     → 独立客户端：自动重连后发送，无需调用方预检查 IsClosed()
//     → 池管理模式：直接返回错误
//   - 其他错误（ErrorClassApplication、ErrorClassLocal等）→ 直接返回
//
// ContentBody 原样透传，不做任何 JSON 解析、字段过滤或截断。
func (c *Client) Send(command, params string, timeout time.Duration) (*core.RconResponse, error) {
	// Fire-and-forget 命令：不等待响应，成功后返回 synthetic 200
	if fireAndForgetCommands[command] {
		if err := c.SendFireAndForget(command, params, timeout); err != nil {
			return nil, err
		}
		return &core.RconResponse{
			StatusCode:    core.StatusSuccess,
			StatusMessage: "OK",
			ContentBody:   "",
		}, nil
	}

	// 连接未建立或已断开
	if c.core == nil || c.core.IsClosed() {
		if c.disableReconnect {
			return nil, &core.RconError{
				Class:   core.ErrorClassTransport,
				Command: command,
				Cause:   fmt.Errorf("connection not established"),
			}
		}
		// 独立模式：自动重连后继续发送
		if err := c.reconnectLocked(timeout); err != nil {
			return nil, &core.RconError{
				Class:   core.ErrorClassTransport,
				Command: command,
				Cause:   fmt.Errorf("reconnect failed: %w", err),
			}
		}
	}

	resp, err := c.core.Send(command, params, timeout)
	if err == nil {
		return resp, nil
	}

	// 提取 RconError（core.Send 始终返回 *RconError）
	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		// 理论上不会走到这里，core.Send 始终返回 *RconError
		return nil, err
	}

	// 不需要重连的错误（应用层、本地错误等）→ 直接返回
	if rconErr.Class != core.ErrorClassTransport && rconErr.Class != core.ErrorClassAuth {
		return nil, rconErr
	}

	// 传输层/认证错误 → Layer 0 已关闭连接（killConn）
	// 池管理模式：直接返回错误，Pool 的 Release() 会通过 IsClosed() 检查自动清理
	if c.disableReconnect {
		return nil, rconErr
	}

	// 独立模式：重建连接 + 重试一次
	if err := c.reconnectLocked(timeout); err != nil {
		return nil, &core.RconError{
			Class:   core.ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("reconnect failed: %w", err),
		}
	}

	// 重试
	resp, retryErr := c.core.Send(command, params, timeout)
	if retryErr != nil {
		var retryRconErr *core.RconError
		if errors.As(retryErr, &retryRconErr) {
			return nil, retryRconErr
		}
		return nil, &core.RconError{
			Class:   core.ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("command failed after reconnect: %w", retryErr),
		}
	}
	return resp, nil
}

// reconnect 重建连接（创建新 core.Client + 握手）。
//
// 旧 core.Client 已被 Layer 0 killConn 或已 IsClosed，无需再关闭。
func (c *Client) reconnect(timeout time.Duration) error {
	c.core = core.NewClient(c.host, c.port)
	for _, opt := range c.dialOpts {
		opt(c.core)
	}
	if err := c.core.Connect(c.password, timeout); err != nil {
		c.core.Close()
		c.core = nil
		return err
	}
	return nil
}

// reconnectLocked 加锁保护的重连，防止并发 Send/SendFireAndForget 同时重建连接导致 TCP 泄漏。
//
// Double-check 模式：拿到锁后再次检查连接状态，可能另一个 goroutine 已经完成了重连。
func (c *Client) reconnectLocked(timeout time.Duration) error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	// Double-check：可能另一个 goroutine 已经完成了重连
	if c.core != nil && !c.core.IsClosed() {
		return nil
	}

	return c.reconnect(timeout)
}

// SendFireAndForget 发送不等待响应的命令（独立客户端自动重建连接 + 重试一次）
//
// 行为与 Send() 一致：
//   - 连接断开时自动重连（独立模式）或返回错误（池模式）
//   - 传输错误后重建连接 + 重试一次（独立模式）
func (c *Client) SendFireAndForget(command, params string, timeout time.Duration) error {
	// 连接未建立或已断开
	if c.core == nil || c.core.IsClosed() {
		if c.disableReconnect {
			return &core.RconError{
				Class:   core.ErrorClassTransport,
				Command: command,
				Cause:   fmt.Errorf("connection not established"),
			}
		}
		// 独立模式：自动重连后继续发送
		if err := c.reconnectLocked(timeout); err != nil {
			return &core.RconError{
				Class:   core.ErrorClassTransport,
				Command: command,
				Cause:   fmt.Errorf("reconnect failed: %w", err),
			}
		}
	}

	err := c.core.SendFireAndForget(command, params, timeout)
	if err == nil {
		return nil
	}

	// 提取 RconError
	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		return err
	}

	// 非传输错误 → 直接返回
	if rconErr.Class != core.ErrorClassTransport {
		return rconErr
	}

	// 传输错误 → Layer 0 已关闭连接（killConn）
	// 池管理模式：直接返回错误，Pool 的 Release() 会通过 IsClosed() 检查自动清理
	if c.disableReconnect {
		return rconErr
	}

	// 独立模式：重建连接 + 重试一次
	if err := c.reconnectLocked(timeout); err != nil {
		return &core.RconError{
			Class:   core.ErrorClassTransport,
			Command: command,
			Cause:   fmt.Errorf("reconnect failed: %w", err),
		}
	}

	return c.core.SendFireAndForget(command, params, timeout)
}
