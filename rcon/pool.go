package rcon

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"golang.org/x/sync/singleflight"
)

// idleConn 包装空闲连接及其归还时间
type idleConn struct {
	client     *core.Client
	returnedAt time.Time
}

// Pool RCON 连接池
// 每个服务器维护多个 TCP 连接，支持动态弹性伸缩（min~max 之间）
type Pool struct {
	mu             sync.RWMutex
	serverPools    map[uint]*serverPool
	circuitBreaker map[uint]*CircuitBreaker
	singleflight   singleflight.Group
	stopChan       chan struct{}
	wg             sync.WaitGroup
	config         PoolConfig

	// Metrics（可选）
	metrics       *PoolMetrics
	metricsStopCh chan struct{}
}

// serverPool 单个服务器的连接池
type serverPool struct {
	closeMu       sync.Mutex
	params       connParams
	avail        chan *idleConn // 空闲连接队列（带归还时间戳）
	minConns     int            // 最少连接数
	maxConns     int            // 最多连接数
	activeConns  atomic.Int64   // 当前总连接数（空闲 + 使用中）
	closed       bool
	waitingCount atomic.Int64 // 当前等待命令数
}

// connParams 连接参数
type connParams struct {
	host     string
	port     string
	password string
	dialOpts []core.DialOption // 拨号选项（如代理配置）
}

// PoolConfig 连接池配置
type PoolConfig struct {
	ConnectTimeout    time.Duration
	ReadTimeout       time.Duration
	HealthInterval    time.Duration
	MaxRetries        int
	BackoffBase       time.Duration
	BackoffMax        time.Duration
	BreakerThreshold  int64
	BreakerOpenTime   time.Duration
	MinConnsPerServer int           // 最少连接数（默认 2）
	MaxConnsPerServer int           // 最多连接数（默认 10）
	ScaleUpWait       time.Duration // 排队超过此时间才扩容（默认 500ms）
	IdleTimeout       time.Duration // 空闲超过此时间才缩容（默认 60s）
	DisableSSRFCheck  bool          // 禁用 SSRF 检查（仅调试/内网环境）
}

// DefaultPoolConfig 默认连接池配置
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		ConnectTimeout:    15 * time.Second,
		ReadTimeout:       30 * time.Second,
		HealthInterval:    30 * time.Second,
		MaxRetries:        10,
		BackoffBase:       1 * time.Second,
		BackoffMax:        60 * time.Second,
		BreakerThreshold:  5,
		BreakerOpenTime:   5 * time.Minute,
		MinConnsPerServer: 2,
		MaxConnsPerServer: 10,
		ScaleUpWait:       500 * time.Millisecond,
		IdleTimeout:       60 * time.Second,
	}
}

// NewPool 创建 RCON 连接池
func NewPool(config PoolConfig) *Pool {
	if config.MinConnsPerServer <= 0 {
		config.MinConnsPerServer = 2
	}
	if config.MaxConnsPerServer <= 0 {
		config.MaxConnsPerServer = 10
	}
	if config.MinConnsPerServer > config.MaxConnsPerServer {
		config.MinConnsPerServer = config.MaxConnsPerServer
	}
	if config.ScaleUpWait <= 0 {
		config.ScaleUpWait = 500 * time.Millisecond
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = 60 * time.Second
	}
	if config.HealthInterval <= 0 {
		config.HealthInterval = 30 * time.Second
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 15 * time.Second
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = 30 * time.Second
	}
	p := &Pool{
		serverPools:    make(map[uint]*serverPool),
		circuitBreaker: make(map[uint]*CircuitBreaker),
		stopChan:       make(chan struct{}),
		config:         config,
	}
	go p.healthCheckLoop()
	return p
}

// SetMetricsPersister 设置指标持久化器并启动采集
func (p *Pool) SetMetricsPersister(persister MetricsPersister) {
	if p.metrics == nil {
		p.metrics = NewPoolMetrics()
		p.metrics.SetPersister(persister)
		p.startMetricsCollector()
	} else {
		p.metrics.SetPersister(persister)
	}
}

// Acquire 从连接池获取一个可用连接
//
// 返回带自动重连/重试的 Layer 1 客户端（*Client），不再是裸 *core.Client。
//
// 动态弹性策略（方案 C）：
//   - 优先从空闲池取
//   - 当前连接数 < minConns：立即创建（保底）
//   - minConns <= 当前 < maxConns：等待 ScaleUpWait，仍无空闲则扩容 +1
//   - 当前 >= maxConns：阻塞等待空闲连接
func (p *Pool) Acquire(ctx context.Context, serverID uint, host, port, password string, opts ...core.DialOption) (*Client, error) {
	cb := p.getOrCreateBreaker(serverID)
	if !cb.Allow() {
		return nil, fmt.Errorf("circuit breaker open for server %d", serverID)
	}

	params := connParams{host: host, port: port, password: password, dialOpts: opts}
	sp := p.getOrCreateServerPool(serverID, params)

	sp.waitingCount.Add(1)
	defer sp.waitingCount.Add(-1)

	for {
		// 1. 快速路径：从空闲池取
		select {
		case ic := <-sp.avail:
			if ic != nil && !ic.client.IsClosed() {
				cb.Success()
				return newClientWithCore(params.host, params.port, params.password, ic.client), nil
			}
			if ic != nil {
				ic.client.Close()
				sp.activeConns.Add(-1)
			}
		default:
		}

		current := sp.activeConns.Load()

		// 2. 低于最小连接数：立即创建（保底）
		if current < int64(sp.minConns) {
			if sp.activeConns.CompareAndSwap(current, current+1) {
				cc, err := p.createConnection(ctx, serverID, params)
				if err != nil {
					sp.activeConns.Add(-1)
					cb.Failure()
					return nil, err
				}
				cb.Success()
				return newClientWithCore(params.host, params.port, params.password, cc), nil
			}
			continue // CAS 失败，重试
		}

		// 3. 在 min~max 之间：等待 ScaleUpWait 后扩容
		if current < int64(sp.maxConns) {
			timer := time.NewTimer(p.config.ScaleUpWait)
			select {
			case ic := <-sp.avail:
				timer.Stop()
				if ic != nil && !ic.client.IsClosed() {
					cb.Success()
					return newClientWithCore(params.host, params.port, params.password, ic.client), nil
				}
				if ic != nil {
					ic.client.Close()
					sp.activeConns.Add(-1)
				}
				continue
			case <-timer.C:
				// 等待超时，尝试扩容
				if sp.activeConns.CompareAndSwap(current, current+1) {
					cc, err := p.createConnection(ctx, serverID, params)
					if err != nil {
						sp.activeConns.Add(-1)
						cb.Failure()
						return nil, err
					}
					cb.Success()
					log.Printf("[RCON] scale-up: server=%d, active=%d->%d", serverID, current, current+1)
					return newClientWithCore(params.host, params.port, params.password, cc), nil
				}
				continue // CAS 失败，重试
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		// 4. 已达最大连接数：阻塞等待空闲连接
		select {
		case ic := <-sp.avail:
			if ic != nil && !ic.client.IsClosed() {
				cb.Success()
				return newClientWithCore(params.host, params.port, params.password, ic.client), nil
			}
			if ic != nil {
				ic.client.Close()
				sp.activeConns.Add(-1)
			}
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Release 归还连接到池中
//
// 缩容策略：
//   - 当前连接数 > minConns 且空闲池已有连接 -> 直接关闭（快速缩容）
//   - 否则归还到空闲池，由健康检查在空闲超时后关闭（渐进缩容）
//
// 线程安全：向 sp.avail 发送数据时持有 closeMu，防止与 serverPool.Close()
// 的 close(sp.avail) 竞态导致 send on closed channel panic。
func (p *Pool) Release(serverID uint, client *Client) {
	if client == nil || client.core == nil {
		return
	}
	cc := client.core
	p.mu.RLock()
	sp, ok := p.serverPools[serverID]
	p.mu.RUnlock()
	if !ok || sp.closed {
		cc.Close()
		return
	}

	// 连接已关闭（Layer 0 传输错误后 killConn），清理计数后返回
	if cc.IsClosed() {
		sp.closeMu.Lock()
		if !sp.closed {
			sp.activeConns.Add(-1)
		}
		sp.closeMu.Unlock()
		return
	}

	// 快速缩容：超过最小连接数且空闲池已有连接 -> 直接关闭
	// 使用 CAS 循环防止 TOCTOU 竞态（并发 Release 可能同时通过 active > min 检查）
	for {
		current := sp.activeConns.Load()
		if current <= int64(sp.minConns) || len(sp.avail) == 0 {
			break
		}
		if sp.activeConns.CompareAndSwap(current, current-1) {
			cc.Close()
			log.Printf("[RCON] scale-down: server=%d, active=%d", serverID, current-1)
			return
		}
	}

	// 归还到空闲池（closeMu 保护，防止与 serverPool.Close() 竞态）
	sp.closeMu.Lock()
	if sp.closed {
		sp.closeMu.Unlock()
		cc.Close()
		return
	}
	select {
	case sp.avail <- &idleConn{client: cc, returnedAt: time.Now()}:
		sp.closeMu.Unlock()
	default:
		sp.closeMu.Unlock()
		cc.Close()
		sp.activeConns.Add(-1)
		log.Printf("[RCON] pool overflow: server=%d, discarding extra connection", serverID)
	}
}

// RemoveServer 彻底移除服务器
func (p *Pool) RemoveServer(serverID uint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sp, ok := p.serverPools[serverID]; ok {
		sp.Close()
		delete(p.serverPools, serverID)
	}
	delete(p.circuitBreaker, serverID)
}

// ResetBreaker 重置指定服务器的熔断器
func (p *Pool) ResetBreaker(serverID uint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cb, ok := p.circuitBreaker[serverID]; ok {
		cb.Reset()
	}
}

// Close 关闭所有连接
func (p *Pool) Close() {
	close(p.stopChan)
	p.stopMetricsCollector()
	p.wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	for id, sp := range p.serverPools {
		sp.Close()
		delete(p.serverPools, id)
	}
}

// MaxWaitingCount 返回所有服务器当前等待命令数的最大值
func (p *Pool) MaxWaitingCount() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var max int64
	for _, sp := range p.serverPools {
		if wc := sp.waitingCount.Load(); wc > max {
			max = wc
		}
	}
	return max
}

// --- 内部方法 ---

func (p *Pool) getOrCreateServerPool(serverID uint, params connParams) *serverPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sp, ok := p.serverPools[serverID]; ok && !sp.closed {
		return sp
	}
	sp := &serverPool{
		params:   params,
		avail:    make(chan *idleConn, p.config.MaxConnsPerServer),
		minConns: p.config.MinConnsPerServer,
		maxConns: p.config.MaxConnsPerServer,
	}
	p.serverPools[serverID] = sp
	return sp
}

func (p *Pool) getOrCreateBreaker(serverID uint) *CircuitBreaker {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cb, ok := p.circuitBreaker[serverID]; ok {
		return cb
	}
	cb := NewCircuitBreaker(p.config.BreakerThreshold, p.config.BreakerOpenTime)
	p.circuitBreaker[serverID] = cb
	return cb
}

func (p *Pool) createConnection(ctx context.Context, serverID uint, params connParams) (*core.Client, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	result, err, _ := p.singleflight.Do(fmt.Sprintf("connect-%d", serverID), func() (interface{}, error) {
		opts := []core.DialOption{}
		opts = append(opts, params.dialOpts...)
		if p.config.DisableSSRFCheck {
			opts = append(opts, core.WithSSRFCheck(false))
		}
		client, err := core.Dial(params.host, params.port, params.password, p.config.ConnectTimeout, opts...)
		if err != nil {
			return nil, fmt.Errorf("RCON connect failed (server %d): %w", serverID, err)
		}
		log.Printf("[RCON] connected: server=%d, addr=%s:%s", serverID, params.host, params.port)
		return client, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*core.Client), nil
}

func (sp *serverPool) Close() {
	sp.closeMu.Lock()
	defer sp.closeMu.Unlock()
	sp.closed = true
	close(sp.avail)
	for ic := range sp.avail {
		if ic != nil && ic.client != nil {
			ic.client.Close()
		}
	}
	sp.activeConns.Store(0)
	sp.avail = make(chan *idleConn, sp.maxConns)
}

// healthCheckLoop 定期健康检查 + 缩容 + 保底补连
func (p *Pool) healthCheckLoop() {
	p.wg.Add(1)
	defer p.wg.Done()
	ticker := time.NewTicker(p.config.HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.checkHealth()
		}
	}
}

func (p *Pool) checkHealth() {
	p.mu.RLock()
	ids := make([]uint, 0, len(p.serverPools))
	for id := range p.serverPools {
		ids = append(ids, id)
	}
	p.mu.RUnlock()

	for _, id := range ids {
		p.mu.RLock()
		sp, spOk := p.serverPools[id]
		p.mu.RUnlock()
		if !spOk || sp.closed {
			continue
		}
		params := sp.params

		// 1. 健康检查 + 空闲超时缩容
		select {
		case ic := <-sp.avail:
			if ic != nil && !ic.client.IsClosed() {
				// 检查是否空闲超时 -> 缩容
				if time.Since(ic.returnedAt) > p.config.IdleTimeout && sp.activeConns.Load() > int64(sp.minConns) {
					ic.client.Close()
					sp.activeConns.Add(-1)
					log.Printf("[RCON] idle-timeout scale-down: server=%d, active=%d", id, sp.activeConns.Load())
				} else {
					// 放回空闲池（closeMu 保护，防止与 serverPool.Close() 竞态）
					sp.closeMu.Lock()
					if sp.closed {
						sp.closeMu.Unlock()
						ic.client.Close()
						sp.activeConns.Add(-1)
					} else {
						select {
						case sp.avail <- ic:
							sp.closeMu.Unlock()
						default:
							sp.closeMu.Unlock()
							ic.client.Close()
							sp.activeConns.Add(-1)
						}
					}
				}
			} else if ic != nil {
				ic.client.Close()
				sp.activeConns.Add(-1)
			}
		default:
		}

		// 2. 确保最小连接数
		p.wg.Add(1)
		go p.ensureMinConnections(context.Background(), id, sp, params)
	}
}

// ensureMinConnections 补充连接到最小值
func (p *Pool) ensureMinConnections(ctx context.Context, serverID uint, sp *serverPool, params connParams) {
	defer p.wg.Done()
	for {
		// 检查停止信号（Pool.Close() 已调用）
		select {
		case <-p.stopChan:
			return
		default:
		}

		current := sp.activeConns.Load()
		if current >= int64(sp.minConns) {
			return
		}
		if sp.closed {
			return
		}
		if sp.activeConns.CompareAndSwap(current, current+1) {
			p.wg.Add(1)
			go p.reconnectWithBackoff(ctx, serverID, sp, params)
			return // 每次只补一个，下次健康检查再补
		}
	}
}

// reconnectWithBackoff 指数退避重连（由 ensureMinConnections 调用）
// 注意：调用前 activeConns 已 +1，本函数负责在失败时 -1
func (p *Pool) reconnectWithBackoff(ctx context.Context, serverID uint, sp *serverPool, params connParams) {
	defer p.wg.Done()
	backoff := p.config.BackoffBase
	for i := 0; i < p.config.MaxRetries; i++ {
		// 等待退避期间，同时监听停止信号和 context 取消
		select {
		case <-p.stopChan:
			sp.activeConns.Add(-1)
			return
		case <-ctx.Done():
			sp.activeConns.Add(-1)
			return
		case <-time.After(backoff):
		}
		sp.closeMu.Lock()
		if sp.closed {
			sp.closeMu.Unlock()
			sp.activeConns.Add(-1)
			return
		}
		sp.closeMu.Unlock()

		opts := []core.DialOption{}
		opts = append(opts, params.dialOpts...)
		if p.config.DisableSSRFCheck {
			opts = append(opts, core.WithSSRFCheck(false))
		}
		client, err := core.Dial(params.host, params.port, params.password, p.config.ConnectTimeout, opts...)
		if err == nil {
			// 归还前再次检查 closed 状态（拨号期间 serverPool 可能已被 Close）
			sp.closeMu.Lock()
			if sp.closed {
				sp.closeMu.Unlock()
				client.Close()
				sp.activeConns.Add(-1)
				return
			}
			select {
			case sp.avail <- &idleConn{client: client, returnedAt: time.Now()}:
				sp.closeMu.Unlock()
				log.Printf("[RCON] reconnected: server=%d (attempt %d)", serverID, i+1)
				p.ResetBreaker(serverID)
				return // 成功，activeConns 保持不变
			default:
				sp.closeMu.Unlock()
				client.Close()
				sp.activeConns.Add(-1)
				log.Printf("[RCON] reconnect: avail full, discarded, server=%d", serverID)
				return
			}
		}
		backoff *= 2
		if backoff > p.config.BackoffMax {
			backoff = p.config.BackoffMax
		}
	}
	sp.activeConns.Add(-1)
	log.Printf("[RCON] reconnect failed: server=%d (max retries %d)", serverID, p.config.MaxRetries)
}
