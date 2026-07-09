// Package core 的 HTTP CONNECT 代理支持
//
// 使用 elastic/proxy-connect-dialer-go 实现优雅的代理拨号器
package core

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	pc "github.com/elastic/proxy-connect-dialer-go"
)

// WithHTTPProxy 配置客户端使用 HTTP CONNECT 代理
//
// proxyAddr 格式："host:port" 或 "http://host:port"
// 返回的 DialOption 会在 connect() 时替换默认拨号器。
func WithHTTPProxy(proxyAddr string) DialOption {
	return func(c *Client) {
		c.proxyAddr = proxyAddr
	}
}

// connectWithProxy 使用 HTTP CONNECT 代理建立 TCP 连接
func (c *Client) connectWithProxy(timeout time.Duration) (net.Conn, error) {
	addr := net.JoinHostPort(c.host, c.port)

	// 解析代理地址
	proxyURL, err := parseProxyURL(c.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address: %w", err)
	}

	// 创建代理拨号器，设置超时
	d, err := pc.NewProxyConnectDialer(proxyURL, &net.Dialer{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("create proxy dialer failed: %w", err)
	}

	// 拨号（会通过代理建立 TCP 连接）
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("proxy dial failed: %w", err)
	}

	// 设置 TCP_NODELAY（禁用 Nagle 算法，降低延迟）
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	// 不设 SetDeadline — 握手阶段的读写超时由 writeRequest() 的
	// SetWriteDeadline / readResponse() 的 SetReadDeadline 分别控制，此处设置会被覆盖。

	return conn, nil
}

// parseProxyURL 将字符串解析为 *url.URL
func parseProxyURL(addr string) (*url.URL, error) {
	if addr == "" {
		return nil, fmt.Errorf("empty proxy address")
	}
	// 如果已经是完整 URL，直接解析
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return url.Parse(addr)
	}
	// 否则补全为 http://host:port
	return url.Parse("http://" + addr)
}
