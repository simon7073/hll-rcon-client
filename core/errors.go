package core

import (
	"errors"
	"fmt"
)

// 哨兵错误 —— 供上层用 errors.Is() 判断错误类型，替代字符串匹配
var (
	ErrMagicMismatch    = errors.New("magic mismatch")
	ErrHeaderReadFailed = errors.New("header read failed")
	ErrBodyReadFailed   = errors.New("body read failed")
)

// ErrorClass 错误类别，供上层决定如何处理
type ErrorClass int

const (
	// ErrorClassTransport 传输层错误
	//
	// TCP 连接可能已损坏（如超时、协议解析失败）。
	// Layer 0 会自动关闭连接（killConn），上层应重建连接。
	ErrorClassTransport ErrorClass = iota

	// ErrorClassApplication 应用层错误
	//
	// 服务器返回了完整响应，但业务逻辑失败（如 400/500，非认证错误）。
	// TCP 连接仍然可用，可继续复用。
	ErrorClassApplication

	// ErrorClassAuth 认证错误
	//
	// 服务器返回 401 Unauthorized，意味着 authToken 过期或失效。
	// Layer 0 已自动关闭连接（authToken 已无效）。
	// 上层应重建连接（重新握手获取新 authToken）。
	ErrorClassAuth

	// ErrorClassLocal 本地错误
	//
	// 客户端本地错误（如 JSON 序列化失败），与服务器和连接无关。
	// TCP 连接不受影响，可继续复用。上层不需要重连。
	ErrorClassLocal
)

// RconError RCON 错误详情，供上层判断错误类别和处理方式
type RconError struct {
	Class         ErrorClass  // 错误类别：传输层 or 应用层
	StatusCode    StatusCode  // 服务器返回的状态码（应用层错误时有值）
	StatusMessage string      // 服务器返回的状态消息（应用层错误时有值）
	Command       string      // 出错的命令名
	Cause         error       // 底层错误（若有）
}

func (e *RconError) Error() string {
	switch e.Class {
	case ErrorClassApplication:
		return fmt.Sprintf("RCON command error [%d]: %s", e.StatusCode, e.StatusMessage)
	case ErrorClassAuth:
		return fmt.Sprintf("RCON auth error [%d]: %s (token expired, reconnection required)", e.StatusCode, e.StatusMessage)
	case ErrorClassLocal:
		if e.Cause != nil {
			return fmt.Sprintf("RCON local error: %v", e.Cause)
		}
		return "RCON local error"
	default: // ErrorClassTransport
		if e.Cause != nil {
			return fmt.Sprintf("RCON transport error: %v", e.Cause)
		}
		return "RCON transport error"
	}
}

func (e *RconError) Unwrap() error {
	return e.Cause
}
