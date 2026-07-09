package rcon

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_Allow_Closed(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	if !cb.Allow() {
		t.Error("StateClosed: Allow() should return true")
	}
}

func TestCircuitBreaker_Allow_Open(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Hour)

	// Trip to StateOpen
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Fatal("expected StateOpen after failures")
	}
	if cb.Allow() {
		t.Error("StateOpen (not timed out): Allow() should return false")
	}
}

func TestCircuitBreaker_Allow_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Trip to StateOpen with very short openTimeout
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Fatal("expected StateOpen after failures")
	}

	// Wait for openTimeout to expire
	time.Sleep(20 * time.Millisecond)

	if !cb.Allow() {
		t.Error("StateOpen (timed out): Allow() should transition to HalfOpen and return true")
	}
	if cb.State() != StateHalfOpen {
		t.Error("expected transition to StateHalfOpen")
	}
}

func TestCircuitBreaker_Allow_HalfOpen_RateLimit(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Trip + wait + enter HalfOpen
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	time.Sleep(20 * time.Millisecond)

	// Allow() in HalfOpen always returns true until successCount reaches halfOpenMax.
	// The rate limiting is on the transition back to Closed (via Success()), not on Allow().
	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Errorf("HalfOpen request %d: Allow() should return true (unlimited until transition)", i+1)
		}
	}

	// After accumulating enough Success() calls, transitions to Closed
	for i := 0; i < 3; i++ {
		cb.Success()
	}
	if cb.State() != StateClosed {
		t.Error("expected Closed after enough Success() calls in HalfOpen")
	}
}

func TestCircuitBreaker_Allow_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Trip + wait + enter HalfOpen
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	time.Sleep(20 * time.Millisecond)

	// Record successes to transition back to Closed
	for i := 0; i < 3; i++ {
		cb.Allow() // enter HalfOpen allowance
		cb.Success()
	}

	if cb.State() != StateClosed {
		t.Error("expected transition back to StateClosed after enough successes in HalfOpen")
	}
}

func TestCircuitBreaker_Allow_ResetDuringOpen(t *testing.T) {
	// 验证 Reset() 在 Allow() 的 RUnlock → Lock 窗口之间修改状态时，
	// 双重检查能正确避免覆盖为 HalfOpen。
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Trip to StateOpen
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Fatal("expected StateOpen")
	}

	// 模拟 TOCTOU 场景：在 Allow() 读取 state=Open 后、获取写锁前，Reset() 把状态改为 Closed。
	// 我们直接并发执行 Allow() 和 Reset() 来触发这个窗口。
	time.Sleep(20 * time.Millisecond) // openTimeout expired
	var wg sync.WaitGroup
	resetHappened := false

	wg.Add(2)
	// Goroutine 1: Allow() - should handle the race correctly
	go func() {
		defer wg.Done()
		// 这个 Allow() 会进入 StateOpen 分支，获取 Lock，然后双重检查 state
		result := cb.Allow()
		// After Reset, since cb is now Closed, Allow() should return true
		if !result {
			t.Error("Allow() after Reset+timeout: should return true (Closed state)")
		}
	}()

	// Goroutine 2: Reset() - races with Allow()
	go func() {
		defer wg.Done()
		// 给 Allow 一点时间进入 StateOpen 分支
		time.Sleep(1 * time.Millisecond)
		cb.Reset()
		resetHappened = true
	}()

	wg.Wait()

	if !resetHappened {
		t.Error("Reset should have happened")
	}

	// 最终状态：由于 Reset 在 openTimeout 过期后调用，且 Allow 获取锁时
	// 双重检查发现 state != StateOpen（已被 Reset 改为 Closed），
	// 最终状态应为 Closed。
	finalState := cb.State()
	if finalState == StateHalfOpen {
		t.Error("TOCTOU bug: Allow() overwrote Reset()'s Closed state with HalfOpen")
	}
}

func TestCircuitBreaker_Failure_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Error("expected StateOpen after threshold failures")
	}
}

func TestCircuitBreaker_Failure_HalfOpenBackToOpen(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Trip + wait + enter HalfOpen
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // enter HalfOpen

	// A failure in HalfOpen should send back to Open
	cb.Failure()
	if cb.State() != StateOpen {
		t.Error("expected StateOpen after failure in HalfOpen")
	}
}

func TestCircuitBreaker_Success_ResetFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	cb.Failure()
	cb.Failure()
	cb.Success()

	// Success in Closed should reset failure count
	if cb.failureCount != 0 {
		t.Errorf("expected failureCount=0 after success, got %d", cb.failureCount)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Minute)

	// Trip + check
	for i := 0; i < 5; i++ {
		cb.Failure()
	}
	if cb.State() != StateOpen {
		t.Fatal("expected StateOpen")
	}

	cb.Reset()
	if cb.State() != StateClosed {
		t.Error("Reset() should restore StateClosed")
	}
	if cb.failureCount != 0 {
		t.Error("Reset() should clear failureCount")
	}
}
