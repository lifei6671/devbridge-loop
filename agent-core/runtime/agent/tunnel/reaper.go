package tunnel

import (
	"fmt"
	"strings"
	"time"
)

// Reaper 负责摘除 closed/broken 终态 tunnel 记录。
type Reaper struct {
	registry *Registry
	nowFn    func() time.Time
}

// NewReaper 创建终态回收器。
func NewReaper(registry *Registry, nowFn func() time.Time) *Reaper {
	normalizedNowFn := nowFn
	if normalizedNowFn == nil {
		// 默认使用 UTC now，便于日志与测试对齐。
		normalizedNowFn = func() time.Time { return time.Now().UTC() }
	}
	return &Reaper{
		registry: registry,
		nowFn:    normalizedNowFn,
	}
}

// MarkBrokenAndRemove 将 tunnel 标记为 broken 并立即摘除。
func (reaper *Reaper) MarkBrokenAndRemove(tunnelID string, reason string) (*Record, error) {
	if reaper.registry == nil {
		// registry 缺失时无法执行任何回收。
		return nil, fmt.Errorf("mark broken and remove: %w", ErrTunnelNotFound)
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 tunnelID 直接返回 not found。
		return nil, ErrTunnelNotFound
	}
	now := reaper.nowFn()
	if err := reaper.registry.MarkBroken(now, normalizedTunnelID, reason); err != nil {
		return nil, err
	}
	removedRecord, err := reaper.registry.RemoveTerminal(normalizedTunnelID)
	if err != nil {
		return nil, err
	}
	return removedRecord, nil
}

// RemoveClosedOnly 摘除已进入 closed 终态的 tunnel。
func (reaper *Reaper) RemoveClosedOnly(tunnelID string) (*Record, error) {
	if reaper.registry == nil {
		// registry 缺失时无法回收记录。
		return nil, fmt.Errorf("remove closed only: %w", ErrTunnelNotFound)
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 tunnelID 视为未找到。
		return nil, ErrTunnelNotFound
	}
	record, exists := reaper.registry.Get(normalizedTunnelID)
	if !exists {
		return nil, ErrTunnelNotFound
	}
	if record.State != StateClosed {
		// 只允许摘除 closed，防止误删运行中 tunnel。
		return nil, ErrInvalidStateTransition
	}
	removedRecord, err := reaper.registry.RemoveTerminal(normalizedTunnelID)
	if err != nil {
		return nil, err
	}
	return removedRecord, nil
}
