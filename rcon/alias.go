// Package rcon 提供 HLL RCONv2 客户端和连接池
//
// 本文件将 core 包的常用类型重新导出，避免外部调用者同时导入 core 和 rcon。
// 仅导出构成公共 API 的类型；纯内部使用的类型（DialOption, Header 等）不在此导出。
package rcon

import (
	"github.com/simon7073/hll-rcon-client/core"
)

// ─── 类型别名（来自 core 包） ───

// RconResponse RCON 响应
type RconResponse = core.RconResponse

// RconError RCON 错误
type RconError = core.RconError

// StatusCode RCON 状态码
type StatusCode = core.StatusCode

// ─── 常量别名 ───

const (
	StatusSuccess       = core.StatusSuccess
	StatusUnauthorized  = core.StatusUnauthorized
	StatusBadRequest    = core.StatusBadRequest
	StatusInternalError = core.StatusInternalError
)
