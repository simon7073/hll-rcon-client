package core

import (
	"encoding/json"
	"testing"
)

func TestRconResponse_UnmarshalJSON_PascalCase(t *testing.T) {
	// 标准 RCONv2 协议格式（PascalCase），一次 Unmarshal 完成
	input := `{"StatusCode":200,"StatusMessage":"OK","Version":2,"Name":"GetPlayers","ContentBody":"[{\"name\":\"BaiZe\"}]"}`
	var resp RconResponse
	if err := json.Unmarshal([]byte(input), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.StatusCode != StatusSuccess {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, StatusSuccess)
	}
	if resp.Name != "GetPlayers" {
		t.Errorf("Name = %q, want GetPlayers", resp.Name)
	}
	if resp.ContentBody != `[{"name":"BaiZe"}]` {
		t.Errorf("ContentBody = %q", resp.ContentBody)
	}
}

func TestRconResponse_UnmarshalJSON_CamelCase(t *testing.T) {
	// 兼容老版本服务器（camelCase），回退到第二次 Unmarshal
	input := `{"statusCode":500,"statusMessage":"Internal Error","version":2,"name":"GetPlayers","contentBody":"error"}`
	var resp RconResponse
	if err := json.Unmarshal([]byte(input), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.StatusCode != StatusInternalError {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, StatusInternalError)
	}
	if resp.Name != "GetPlayers" {
		t.Errorf("Name = %q, want GetPlayers", resp.Name)
	}
}

func TestRconResponse_UnmarshalJSON_EmptyBody(t *testing.T) {
	input := `{}`
	var resp RconResponse
	if err := json.Unmarshal([]byte(input), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.StatusCode != 0 || resp.Name != "" {
		t.Errorf("expected zero-valued response, got StatusCode=%d Name=%q", resp.StatusCode, resp.Name)
	}
}

func TestRconResponse_UnmarshalJSON_Malformed(t *testing.T) {
	input := `not json`
	var resp RconResponse
	err := json.Unmarshal([]byte(input), &resp)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestRconResponse_UnmarshalJSON_OnlyStatusCode(t *testing.T) {
	// Unauthorized 响应通常只有 StatusCode 和 StatusMessage
	input := `{"StatusCode":401,"StatusMessage":"Not authenticated"}`
	var resp RconResponse
	if err := json.Unmarshal([]byte(input), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.StatusCode != StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, StatusUnauthorized)
	}
	// ContentBody 不触发 camelCase 回退（StatusCode != 0）
	if resp.Name != "" {
		t.Errorf("Name should be empty, got %q", resp.Name)
	}
}

func TestRconResponse_IsSuccess(t *testing.T) {
	tests := []struct {
		code    StatusCode
		success bool
	}{
		{StatusSuccess, true},
		{StatusUnauthorized, false},
		{StatusBadRequest, false},
		{StatusInternalError, false},
		{StatusCode(0), false},
	}
	for _, tt := range tests {
		r := &RconResponse{StatusCode: tt.code}
		if got := r.IsSuccess(); got != tt.success {
			t.Errorf("IsSuccess() with StatusCode=%d: got %v, want %v", tt.code, got, tt.success)
		}
	}
}
