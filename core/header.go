// Package core 实现 HLL RCONv2 协议客户端内核
//
// 本包提供最基础的 TCP 连接 + XOR 加密 + 命令收发能力。
// 不包含重连、重试、熔断、连接池等高阶逻辑 — 这些由上层包负责。
//
// 本包零业务依赖，零示例代码，纯库。
package core

import (
	"encoding/json"
)

// MagicNumber RCONv2 协议魔数（12 字节报文头前 4 字节，小端序）
//
// 白皮书未提及此字段，但实测所有 HLL 服务器均使用此值。
const MagicNumber uint32 = 0xDE450508

// MaxPayloadSize 响应 Body 最大长度（16 MiB）
// 防止恶意或异常大包导致内存耗尽
const MaxPayloadSize uint32 = 16 * 1024 * 1024

// StatusCode RCON 响应 HTTP 风格状态码
type StatusCode int

const (
	StatusSuccess       StatusCode = 200
	StatusUnauthorized  StatusCode = 401
	StatusBadRequest    StatusCode = 400
	StatusInternalError StatusCode = 500
)

// Header RCONv2 报文头（12 字节，小端序）
type Header struct {
	Magic     uint32
	RequestID uint32
	Length    uint32
}

// rawRequest RCONv2 请求 JSON
//
// HLL 服务器期望 camelCase 字段名（authToken 除外，它是 PascalCase）。
// 这与 Python 版本 (switch_map/utils.py) 的行为一致。
type rawRequest struct {
	AuthToken   string `json:"authToken"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	ContentBody string `json:"contentBody"`
}

// RconResponse RCONv2 响应
type RconResponse struct {
	StatusCode    StatusCode
	StatusMessage string
	Version       int
	Name          string
	ContentBody   string
}

// IsSuccess 判断响应是否成功
func (r *RconResponse) IsSuccess() bool {
	return r.StatusCode == StatusSuccess
}

// UnmarshalJSON 单次解析 RCON 响应 JSON
//
// 先尝试标准 PascalCase（RCONv2 官方格式），若关键字段为空则回退到 camelCase。
// 替代原来的 decodeRconResponse + parseStringField + parseIntField（三次 Unmarshal）。
func (r *RconResponse) UnmarshalJSON(data []byte) error {
	// 尝试 PascalCase
	type pascal struct {
		StatusCode    int    `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
		Version       int    `json:"Version"`
		Name          string `json:"Name"`
		ContentBody   string `json:"ContentBody"`
	}
	var p pascal
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	r.StatusCode = StatusCode(p.StatusCode)
	r.StatusMessage = p.StatusMessage
	r.Version = p.Version
	r.Name = p.Name
	r.ContentBody = p.ContentBody

	// 如果关键字段为空，回退到 camelCase
	if r.StatusCode == 0 && r.Name == "" {
		type camel struct {
			StatusCode    int    `json:"statusCode"`
			StatusMessage string `json:"statusMessage"`
			Version       int    `json:"version"`
			Name          string `json:"name"`
			ContentBody   string `json:"contentBody"`
		}
		var c camel
		if err := json.Unmarshal(data, &c); err != nil {
			return err
		}
		r.StatusCode = StatusCode(c.StatusCode)
		r.StatusMessage = c.StatusMessage
		r.Version = c.Version
		r.Name = c.Name
		r.ContentBody = c.ContentBody
	}
	return nil
}
