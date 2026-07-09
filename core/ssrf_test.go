package core

import (
	"testing"
)

func TestValidateDialTarget(t *testing.T) {
	tests := []struct {
		host    string
		wantErr bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"127.0.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"10.0.0.1", true},
		{"", true},
	}
	for _, tt := range tests {
		err := validateDialTarget(tt.host)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateDialTarget(%q) error=%v, wantErr=%v", tt.host, err, tt.wantErr)
		}
	}
}
