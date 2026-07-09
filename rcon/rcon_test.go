package rcon

import (
	"testing"
	"time"
)

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(5, 5*time.Minute)
	if cb.State() != StateClosed {
		t.Error("expected StateClosed")
	}
	if !cb.Allow() {
		t.Error("expected Allow=true")
	}

	// trigger failures
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Error("expected StateOpen after 5 failures")
	}
	if cb.Allow() {
		t.Error("expected Allow=false in open state")
	}
}

func TestPoolConfig(t *testing.T) {
	cfg := DefaultPoolConfig()
	if cfg.MinConnsPerServer != 2 {
		t.Errorf("expected MinConnsPerServer=2, got %d", cfg.MinConnsPerServer)
	}
	if cfg.MaxConnsPerServer != 10 {
		t.Errorf("expected MaxConnsPerServer=10, got %d", cfg.MaxConnsPerServer)
	}
	if cfg.ScaleUpWait != 500*time.Millisecond {
		t.Errorf("expected ScaleUpWait=500ms, got %v", cfg.ScaleUpWait)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("expected IdleTimeout=60s, got %v", cfg.IdleTimeout)
	}
	if cfg.ConnectTimeout == 0 {
		t.Error("expected non-zero ConnectTimeout")
	}
}
