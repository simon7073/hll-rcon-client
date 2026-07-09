package rcon

import (
	"sync"
	"time"
)

// CircuitBreakerState 熔断器状态
type CircuitBreakerState int

const (
	StateClosed   CircuitBreakerState = 0
	StateOpen     CircuitBreakerState = 1
	StateHalfOpen CircuitBreakerState = 2
)

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	mu              sync.RWMutex
	state           CircuitBreakerState
	failureCount    int64
	successCount    int64
	lastFailureTime time.Time

	threshold   int64
	halfOpenMax int64
	openTimeout time.Duration
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(threshold int64, openTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:       StateClosed,
		threshold:   threshold,
		halfOpenMax: 3,
		openTimeout: openTimeout,
	}
	return cb
}

// State 返回当前状态
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Allow 检查是否允许请求通过
//
// StateClosed → 直接放行（无锁读取 state）。
// StateOpen   → 持写锁检查超时，超时则转为 HalfOpen。
//               双重检查 state 值，防止与 Reset() 的 TOCTOU 竞态。
// HalfOpen    → 放行前 successCount 个请求用于探活。
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.RLock()
	state := cb.state
	cb.mu.RUnlock()

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		cb.mu.Lock()
		// 双重检查：RLock 释放与 Lock 获取之间，Reset() 可能已把状态改为 Closed
		if cb.state != StateOpen {
			cb.mu.Unlock()
			return cb.Allow()
		}
		if time.Since(cb.lastFailureTime) > cb.openTimeout {
			cb.state = StateHalfOpen
			cb.successCount = 0
			cb.mu.Unlock()
			return true
		}
		cb.mu.Unlock()
		return false
	case StateHalfOpen:
		cb.mu.RLock()
		count := cb.successCount
		cb.mu.RUnlock()
		return count < cb.halfOpenMax
	default:
		// 防御性：未知状态默认放行
		return true
	}
}

// Success 记录成功
func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case StateHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.halfOpenMax {
			cb.state = StateClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	case StateClosed:
		cb.failureCount = 0
	}
}

// Failure 记录失败
func (cb *CircuitBreaker) Failure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.threshold {
			cb.state = StateOpen
		}
	case StateHalfOpen:
		cb.state = StateOpen
	}
}

// Reset 重置熔断器
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
}
