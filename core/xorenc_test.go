package core

import (
	"testing"
)

func TestXOREncrypt(t *testing.T) {
	key := []byte("testkey")
	data := []byte("hello world")
	encrypted := xorEncrypt(data, key)
	decrypted := xorEncrypt(encrypted, key)
	if string(decrypted) != string(data) {
		t.Errorf("XOR round-trip failed: got %q, want %q", decrypted, data)
	}
}
