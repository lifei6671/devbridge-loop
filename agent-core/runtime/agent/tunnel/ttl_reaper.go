package tunnel

import (
	"fmt"
	"time"
)

// TTLReaperConfig 定义 idle TTL 回收配置。
type TTLReaperConfig struct {
	IdleTTL time.Duration
}

// Normalize 校验并回填 TTL 配置。
func (config TTLReaperConfig) Normalize() TTLReaperConfig {
	normalizedConfig := config
	if normalizedConfig.IdleTTL <= 0 {
		// 未配置 TTL 时默认不开启 TTL 回收。
		normalizedConfig.IdleTTL = 0
	}
	return normalizedConfig
}

// TTLSweepResult 描述一次 TTL 扫描结果。
type TTLSweepResult struct {
	Candidates int
	Closed     int
	Failed     int
	FirstError error
}

// TTLReaper 负责将过期 idle tunnel 按规范回收。
type TTLReaper struct {
	registry *Registry
	reaper   *Reaper
	config   TTLReaperConfig
	nowFn    func() time.Time
}

// NewTTLReaper 创建 TTL 回收器。
func NewTTLReaper(registry *Registry, reaper *Reaper, config TTLReaperConfig, nowFn func() time.Time) *TTLReaper {
	normalizedNowFn := nowFn
	if normalizedNowFn == nil {
		// 默认使用 UTC now，便于跨模块时间对齐。
		normalizedNowFn = func() time.Time { return time.Now().UTC() }
	}
	return &TTLReaper{
		registry: registry,
		reaper:   reaper,
		config:   config.Normalize(),
		nowFn:    normalizedNowFn,
	}
}

// Sweep 扫描并回收超过 TTL 的 idle tunnel。
func (reaper *TTLReaper) Sweep() TTLSweepResult {
	result := TTLSweepResult{}
	if reaper == nil || reaper.registry == nil {
		// 依赖缺失时返回空结果，避免空指针。
		return result
	}
	if reaper.config.IdleTTL <= 0 {
		// 未开启 TTL 回收时直接返回。
		return result
	}
	now := reaper.nowFn()
	expiredTunnelIDs := reaper.registry.ExpiredIdleIDs(now, reaper.config.IdleTTL)
	result.Candidates = len(expiredTunnelIDs)

	for _, tunnelID := range expiredTunnelIDs {
		record, exists := reaper.registry.Get(tunnelID)
		if !exists {
			// 扫描和执行之间可能被其他流程回收，直接跳过。
			continue
		}
		if record.State != StateIdle {
			// 只有 idle 才允许进入 TTL 回收流程。
			continue
		}
		if err := reaper.registry.MarkClosing(now, tunnelID); err != nil {
			// 状态切换失败时保留首个错误，继续处理其它候选。
			result.recordFailure(fmt.Errorf("ttl sweep mark closing tunnel=%s: %w", tunnelID, err))
			continue
		}

		if record.Tunnel != nil {
			if err := record.Tunnel.Close(); err != nil {
				// close 失败仅记录错误，仍继续收敛到 closed。
				result.recordFailure(fmt.Errorf("ttl sweep close tunnel=%s: %w", tunnelID, err))
			}
		}
		if err := reaper.registry.MarkClosed(now, tunnelID); err != nil {
			result.recordFailure(fmt.Errorf("ttl sweep mark closed tunnel=%s: %w", tunnelID, err))
			continue
		}
		if reaper.reaper != nil {
			if _, err := reaper.reaper.RemoveClosedOnly(tunnelID); err != nil {
				result.recordFailure(fmt.Errorf("ttl sweep remove closed tunnel=%s: %w", tunnelID, err))
				continue
			}
		}
		// 正常路径遵循 idle -> closing -> closed，并最终摘除。
		result.Closed++
	}
	return result
}

// recordFailure 记录一次失败并保留首个错误。
func (result *TTLSweepResult) recordFailure(err error) {
	result.Failed++
	if result.FirstError == nil {
		// 仅保留首个错误，避免错误信息过载。
		result.FirstError = err
	}
}
