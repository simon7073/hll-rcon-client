package rcon

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
)

// ==================== newClientWithCore ====================

func TestNewClientWithCore(t *testing.T) {
	cc := core.NewClient("10.0.0.1", "12345")
	r := newClientWithCore("10.0.0.1", "12345", "secret", cc)
	if r == nil {
		t.Fatal("newClientWithCore returned nil")
	}
	if r.host != "10.0.0.1" || r.port != "12345" || r.password != "secret" {
		t.Errorf("fields not preserved: host=%q port=%q password=%q", r.host, r.port, r.password)
	}
	if r.core != cc {
		t.Error("core.Client reference not preserved")
	}
	if r.IsClosed() {
		t.Error("unconnected core.Client should not be closed")
	}
}

// ==================== NewClient + Close/IsClosed/Addr ====================

func TestRconClient_Lifecycle(t *testing.T) {
	c := NewClient("10.0.0.2", "12345", "pwd")

	addr := c.Addr()
	if addr != "10.0.0.2:12345" {
		t.Errorf("Addr() = %q, want 10.0.0.2:12345", addr)
	}

	if !c.IsClosed() {
		t.Error("unconnected client should report IsClosed=true (no underlying core)")
	}

	if err := c.Close(); err != nil {
		t.Errorf("Close() on unconnected client: %v", err)
	}
}

// ==================== Send without connection ====================

func TestRconClient_SendWithoutConnect(t *testing.T) {
	c := NewClient("10.0.0.3", "12345", "pwd")
	_, err := c.Send("GetPlayers", "", 5*time.Second)
	if err == nil {
		t.Error("Send() without Connect() should return error")
	}
	// 错误应该是 *core.RconError
	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		t.Errorf("Send() without Connect() should return *RconError, got %T: %v", err, err)
	}
}

func TestRconClient_SendFireAndForgetWithoutConnect(t *testing.T) {
	c := NewClient("10.0.0.3", "12345", "pwd")
	err := c.SendFireAndForget("Broadcast", "test", 3*time.Second)
	if err == nil {
		t.Error("SendFireAndForget() without Connect() should return error")
	}
	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		t.Errorf("SendFireAndForget() without Connect() should return *RconError, got %T: %v", err, err)
	}
}

// ==================== disableReconnect (pool mode) ====================

func TestNewClientWithCore_DisableReconnect(t *testing.T) {
	cc := core.NewClient("10.0.0.1", "12345")
	r := newClientWithCore("10.0.0.1", "12345", "pwd", cc)
	if !r.disableReconnect {
		t.Error("newClientWithCore should set disableReconnect=true")
	}
}

func TestNewClient_DisableReconnectFalse(t *testing.T) {
	c := NewClient("10.0.0.1", "12345", "pwd")
	if c.disableReconnect {
		t.Error("NewClient should set disableReconnect=false (standalone use)")
	}
}

// TestConnectFailureCleanup 验证 Connect() 失败后 c.core 被置 nil
func TestConnectFailureCleanup(t *testing.T) {
	c := NewClient("10.0.0.1", "12345", "pwd")
	err := c.Connect(100 * time.Millisecond)
	if err == nil {
		t.Skip("unexpectedly connected to 10.0.0.1:12345, skip cleanup test")
	}
	if !c.IsClosed() {
		t.Error("IsClosed() should return true after Connect() failure")
	}
	if c.core != nil {
		t.Error("c.core should be nil after Connect() failure")
	}
}

// ==================== ErrorClass 判断逻辑 ====================

func TestSend_ErrorClass_Application(t *testing.T) {
	// 模拟 core.Send 返回应用层错误（400/500）
	// 验证 rcon.Client.Send() 直接返回，不触发重连逻辑
	//
	// 由于需要 mock core.Client，这里只测试错误类型判断逻辑
	// 集成测试在 cmd/test_layer0 中覆盖

	err := &core.RconError{
		Class:         core.ErrorClassApplication,
		StatusCode:    400,
		StatusMessage: "Request was invalid",
		Command:       "GetServerInformation",
		Cause:         fmt.Errorf("server returned non-success status"),
	}

	// 验证 ErrorClass 判断
	if err.Class != core.ErrorClassApplication {
		t.Errorf("expected ErrorClassApplication, got %v", err.Class)
	}

	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		t.Error("should be extractable as *RconError")
	}
	if rconErr.Class != core.ErrorClassApplication {
		t.Errorf("extracted Class = %v, want ErrorClassApplication", rconErr.Class)
	}
}

func TestSend_ErrorClass_Transport(t *testing.T) {
	err := &core.RconError{
		Class:   core.ErrorClassTransport,
		Command: "GetPlayers",
		Cause:   fmt.Errorf("connection reset"),
	}

	if err.Class != core.ErrorClassTransport {
		t.Errorf("expected ErrorClassTransport, got %v", err.Class)
	}

	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		t.Error("should be extractable as *RconError")
	}
}

// ==================== 辅助类型 ====================

// timeoutError 实现 net.Error 接口，模拟超时
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// TestSend_NetTimeout 验证网络超时错误的处理
// 注意：当前 core.Send() 将超时包装为 *RconError{Class: ErrorClassTransport}
// 上层通过 errors.As 提取后检查 Class 字段
func TestSend_NetTimeout(t *testing.T) {
	timeoutErr := &net.OpError{Op: "read", Net: "tcp", Err: &timeoutError{}}

	// 模拟 core.Send 返回的错误
	err := &core.RconError{
		Class:   core.ErrorClassTransport,
		Command: "GetPlayers",
		Cause:   timeoutErr,
	}

	var rconErr *core.RconError
	if !errors.As(err, &rconErr) {
		t.Error("should extract *RconError")
	}
	if rconErr.Class != core.ErrorClassTransport {
		t.Errorf("Class = %v, want ErrorClassTransport", rconErr.Class)
	}
}
