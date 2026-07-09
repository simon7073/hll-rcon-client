package rcon

import (
	"context"
	"log"
	"sync"
	"time"
)

// MetricsPersister 指标持久化接口
// 由调用方实现，将周期快照写入外部存储（如 PostgreSQL）
type MetricsPersister interface {
	SaveSnapshot(ctx context.Context, snap MetricsSnapshot) error
}

// MetricsSnapshot 一个 10 分钟周期的连接池峰值快照
type MetricsSnapshot struct {
	Time         time.Time
	Servers      map[uint]ServerMetrics
	TotalWaiting int64
}

// ServerMetrics 单个服务器指标
type ServerMetrics struct {
	ServerID        uint
	MaxConns        int
	ActiveConns     int64 // 当前总连接数（空闲 + 使用中）
	MaxWaitingCount int64
}

// PoolMetrics 连接池指标收集器
type PoolMetrics struct {
	mu         sync.RWMutex
	cycleStart time.Time
	cyclePeaks map[uint]int64
	snapshots  []MetricsSnapshot
	idx        int
	max        int // default 144 (24h)
	persister  MetricsPersister
}

// NewPoolMetrics 创建指标收集器
func NewPoolMetrics() *PoolMetrics {
	return &PoolMetrics{
		cyclePeaks: make(map[uint]int64),
		max:        144,
	}
}

// SetPersister 设置持久化器
func (pm *PoolMetrics) SetPersister(p MetricsPersister) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.persister = p
}

func (pm *PoolMetrics) updatePeaks(serverPeaks map[uint]int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.cycleStart.IsZero() {
		pm.cycleStart = time.Now()
	}
	for sid, wc := range serverPeaks {
		if wc > pm.cyclePeaks[sid] {
			pm.cyclePeaks[sid] = wc
		}
	}
}

func (pm *PoolMetrics) cycleEnd(now time.Time) MetricsSnapshot {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	snap := MetricsSnapshot{
		Time:    now,
		Servers: make(map[uint]ServerMetrics),
	}
	for sid, peak := range pm.cyclePeaks {
		snap.Servers[sid] = ServerMetrics{
			ServerID:        sid,
			MaxWaitingCount: peak,
		}
		snap.TotalWaiting += peak
	}

	if len(pm.snapshots) < pm.max {
		pm.snapshots = append(pm.snapshots, snap)
	} else {
		pm.snapshots[pm.idx] = snap
		pm.idx = (pm.idx + 1) % pm.max
	}

	pm.cyclePeaks = make(map[uint]int64)
	pm.cycleStart = now
	return snap
}

func (pm *PoolMetrics) persisterIfSet(snap MetricsSnapshot) {
	if pm.persister == nil {
		return
	}
	go func() {
		if err := pm.persister.SaveSnapshot(context.Background(), snap); err != nil {
			log.Printf("[PoolMetrics] persist snapshot failed: %v", err)
		}
	}()
}

// GetHistory 返回内存中的历史快照
func (pm *PoolMetrics) GetHistory() []MetricsSnapshot {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]MetricsSnapshot, 0, len(pm.snapshots))
	for i := 0; i < len(pm.snapshots); i++ {
		pos := (pm.idx + i) % len(pm.snapshots)
		if !pm.snapshots[pos].Time.IsZero() {
			result = append(result, pm.snapshots[pos])
		}
	}
	return result
}

// --- Pool methods for metrics ---

// startMetricsCollector 启动指标采集后台任务（1 秒 tick）
func (p *Pool) startMetricsCollector() {
	p.metricsStopCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.metricsStopCh:
				return
			case now := <-ticker.C:
				p.metricsTick(now)
			}
		}
	}()
}

func (p *Pool) stopMetricsCollector() {
	if p.metricsStopCh != nil {
		close(p.metricsStopCh)
		p.metricsStopCh = nil
	}
}

func (p *Pool) metricsTick(now time.Time) {
	if p.metrics == nil {
		return
	}

	serverPeaks := p.collectWaitingCounts()
	p.metrics.updatePeaks(serverPeaks)

	p.metrics.mu.RLock()
	cycleStart := p.metrics.cycleStart
	p.metrics.mu.RUnlock()
	if cycleStart.IsZero() {
		return
	}
	if now.Sub(cycleStart) >= 10*time.Minute {
		snap := p.metrics.cycleEnd(now)
		p.metrics.persisterIfSet(snap)
		log.Printf("[PoolMetrics] 10m snapshot: totalMaxWaiting=%d, servers=%d",
			snap.TotalWaiting, len(snap.Servers))
	}
}

func (p *Pool) collectWaitingCounts() map[uint]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[uint]int64, len(p.serverPools))
	for id, sp := range p.serverPools {
		result[id] = sp.waitingCount.Load()
	}
	return result
}

// MetricsNow 返回当前周期的实时指标快照
func (p *Pool) MetricsNow() MetricsSnapshot {
	if p.metrics == nil {
		return MetricsSnapshot{Time: time.Now()}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	snap := MetricsSnapshot{
		Time:    time.Now(),
		Servers: make(map[uint]ServerMetrics),
	}

	p.metrics.mu.RLock()
	cyclePeaks := make(map[uint]int64, len(p.metrics.cyclePeaks))
	for k, v := range p.metrics.cyclePeaks {
		cyclePeaks[k] = v
	}
	p.metrics.mu.RUnlock()

	for id, sp := range p.serverPools {
		peak := cyclePeaks[id]
		if wc := sp.waitingCount.Load(); wc > peak {
			peak = wc
		}
		snap.Servers[id] = ServerMetrics{
			ServerID:        id,
			MaxConns:        sp.maxConns,
			ActiveConns:     sp.activeConns.Load(),
			MaxWaitingCount: peak,
		}
		snap.TotalWaiting += peak
	}
	return snap
}

// MetricsHistory 返回内存中的历史快照
func (p *Pool) MetricsHistory() []MetricsSnapshot {
	if p.metrics == nil {
		return nil
	}
	return p.metrics.GetHistory()
}

// ActiveServers 返回活跃服务器数量
func (p *Pool) ActiveServers() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.serverPools)
}
