package core

import (
	"errors"
	"fmt"
	"testing"
)

// ==================== 哨兵错误（供 errors.Is 判断）====================

func TestSentinelErrors_Is(t *testing.T) {
	if !errors.Is(ErrMagicMismatch, ErrMagicMismatch) {
		t.Error("ErrMagicMismatch should match itself")
	}
	if !errors.Is(ErrHeaderReadFailed, ErrHeaderReadFailed) {
		t.Error("ErrHeaderReadFailed should match itself")
	}
	if !errors.Is(ErrBodyReadFailed, ErrBodyReadFailed) {
		t.Error("ErrBodyReadFailed should match itself")
	}
}

func TestSentinelErrors_Wrapped(t *testing.T) {
	// 模拟 transport.go 中的 fmt.Errorf("%w: ...", sentinel) 包装
	wrappedMagic := fmt.Errorf("some context: %w", ErrMagicMismatch)
	if !errors.Is(wrappedMagic, ErrMagicMismatch) {
		t.Error("wrapped ErrMagicMismatch should match via errors.Is")
	}

	wrappedHeader := fmt.Errorf("response %w: connection reset", ErrHeaderReadFailed)
	if !errors.Is(wrappedHeader, ErrHeaderReadFailed) {
		t.Error("wrapped ErrHeaderReadFailed should match via errors.Is")
	}
}

func TestSentinelErrors_NotMatch(t *testing.T) {
	plain := errors.New("some random error")
	if errors.Is(plain, ErrMagicMismatch) {
		t.Error("plain error should not match ErrMagicMismatch")
	}
	if errors.Is(plain, ErrHeaderReadFailed) {
		t.Error("plain error should not match ErrHeaderReadFailed")
	}
}

func TestSentinelErrors_CrossoverNotMatch(t *testing.T) {
	if errors.Is(ErrMagicMismatch, ErrHeaderReadFailed) {
		t.Error("ErrMagicMismatch should not match ErrHeaderReadFailed")
	}
	if errors.Is(ErrHeaderReadFailed, ErrBodyReadFailed) {
		t.Error("ErrHeaderReadFailed should not match ErrBodyReadFailed")
	}
}

// ==================== ErrorClass ====================

func TestErrorClass_Values(t *testing.T) {
	if ErrorClassTransport != 0 {
		t.Errorf("ErrorClassTransport should be 0, got %d", ErrorClassTransport)
	}
	if ErrorClassApplication != 1 {
		t.Errorf("ErrorClassApplication should be 1, got %d", ErrorClassApplication)
	}
}

// ==================== RconError ====================

func TestRconError_Error_Transport(t *testing.T) {
	// 传输错误：显示 Cause
	e := &RconError{
		Class:   ErrorClassTransport,
		Command: "ServerConnect",
		Cause:   fmt.Errorf("connection refused"),
	}
	msg := e.Error()
	if msg != "RCON transport error: connection refused" {
		t.Errorf("Error() = %q", msg)
	}

	// 传输错误，无 Cause
	e2 := &RconError{
		Class:   ErrorClassTransport,
		Command: "Send",
	}
	msg2 := e2.Error()
	if msg2 != "RCON transport error" {
		t.Errorf("Error() without cause = %q", msg2)
	}
}

func TestRconError_Error_Application(t *testing.T) {
	// 应用层错误：显示 StatusCode 和 StatusMessage
	e := &RconError{
		Class:         ErrorClassApplication,
		StatusCode:    StatusUnauthorized,
		StatusMessage: "Not authenticated",
		Command:       "GetPlayers",
	}
	msg := e.Error()
	if msg != "RCON command error [401]: Not authenticated" {
		t.Errorf("Error() = %q", msg)
	}
}

func TestRconError_Unwrap(t *testing.T) {
	cause := errors.New("underlying io error")
	e := &RconError{
		Class:      ErrorClassTransport,
		StatusCode: StatusInternalError,
		Command:    "GetPlayers",
		Cause:      cause,
	}
	if !errors.Is(e, cause) {
		t.Error("Unwrap should expose the cause")
	}
}

func TestRconError_As(t *testing.T) {
	// 验证 errors.As 可以提取 *RconError
	e := &RconError{
		Class:   ErrorClassTransport,
		Command: "Test",
		Cause:   fmt.Errorf("test error"),
	}
	var rconErr *RconError
	if !errors.As(e, &rconErr) {
		t.Error("errors.As should extract *RconError")
	}
}
