package health

import (
	"context"
	"sync"
	"time"
)

// Monitor 健康监控器
type Monitor struct {
	checker   Checker
	targets   map[string]Target
	statuses  map[string]Status
	reporter  HealthReporter
	mu        sync.RWMutex
	stopChan  chan struct{}
	interval  time.Duration
}

// NewMonitor 创建健康监控器
func NewMonitor(checker Checker, reporter HealthReporter) *Monitor {
	return &Monitor{
		checker:  checker,
		targets:  make(map[string]Target),
		statuses: make(map[string]Status),
		reporter: reporter,
		stopChan: make(chan struct{}),
		interval: checker.GetInterval(),
	}
}

// RegisterTarget 注册监控目标
func (m *Monitor) RegisterTarget(target Target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets[target.GetID()] = target
}

// UnregisterTarget 注销监控目标
func (m *Monitor) UnregisterTarget(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, targetID)
	delete(m.statuses, targetID)
}

// Start 启动健康监控
func (m *Monitor) Start(ctx context.Context) {
	// 立即执行一次检查
	m.checkAll(ctx)
	
	// 启动定期检查
	go m.run(ctx)
}

// Stop 停止健康监控
func (m *Monitor) Stop() {
	close(m.stopChan)
}

// run 运行健康检查循环
func (m *Monitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

// checkAll 检查所有目标
func (m *Monitor) checkAll(ctx context.Context) {
	m.mu.RLock()
	targets := make([]Target, 0, len(m.targets))
	for _, target := range m.targets {
		targets = append(targets, target)
	}
	m.mu.RUnlock()

	// 并发检查所有目标
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			m.checkTarget(ctx, t)
		}(target)
	}
	wg.Wait()
}

// checkTarget 检查单个目标
func (m *Monitor) checkTarget(ctx context.Context, target Target) {
	status := m.checker.Check(ctx, target)
	
	// 更新状态
	m.mu.Lock()
	m.statuses[target.GetID()] = status
	m.mu.Unlock()
	
	// 报告状态
	if m.reporter != nil {
		m.reporter.ReportHealth(target.GetID(), status)
	}
}

// GetStatus 获取指定目标的健康状态
func (m *Monitor) GetStatus(targetID string) (Status, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, ok := m.statuses[targetID]
	return status, ok
}

// GetAllStatuses 获取所有目标的健康状态
func (m *Monitor) GetAllStatuses() map[string]Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	result := make(map[string]Status, len(m.statuses))
	for id, status := range m.statuses {
		result[id] = status
	}
	return result
}

// IsHealthy 检查指定目标是否健康
func (m *Monitor) IsHealthy(targetID string) bool {
	status, ok := m.GetStatus(targetID)
	return ok && status.Healthy
}

// GetHealthyTargets 获取所有健康的目标
func (m *Monitor) GetHealthyTargets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var healthy []string
	for id, status := range m.statuses {
		if status.Healthy {
			healthy = append(healthy, id)
		}
	}
	return healthy
}

// GetUnhealthyTargets 获取所有不健康的目标
func (m *Monitor) GetUnhealthyTargets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var unhealthy []string
	for id, status := range m.statuses {
		if !status.Healthy {
			unhealthy = append(unhealthy, id)
		}
	}
	return unhealthy
}