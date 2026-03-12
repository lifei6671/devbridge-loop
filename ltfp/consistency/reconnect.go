package consistency

// ReconnectSyncPlan 描述重连后采用 full-sync 或增量同步的策略。
type ReconnectSyncPlan struct {
	NeedFullSync         bool
	Reason               string
	SinceResourceVersion uint64
}

// BuildReconnectSyncPlan 根据会话信息生成重连同步计划。
func BuildReconnectSyncPlan(previousSessionID string, previousSessionEpoch uint64, currentSessionID string, currentSessionEpoch uint64, currentResourceVersion uint64) ReconnectSyncPlan {
	// 新会话 ID 与旧会话 ID 不一致时，直接执行 full-sync。
	if previousSessionID != "" && currentSessionID != "" && previousSessionID != currentSessionID {
		return ReconnectSyncPlan{
			NeedFullSync:         true,
			Reason:               "session_id_changed",
			SinceResourceVersion: 0,
		}
	}
	// epoch 升级说明连接代际变化，需 full-sync 重建一致性。
	if previousSessionEpoch > 0 && currentSessionEpoch > previousSessionEpoch {
		return ReconnectSyncPlan{
			NeedFullSync:         true,
			Reason:               "session_epoch_advanced",
			SinceResourceVersion: 0,
		}
	}
	// 无上次会话信息时首次连入，执行 full-sync 获取基线快照。
	if previousSessionEpoch == 0 {
		return ReconnectSyncPlan{
			NeedFullSync:         true,
			Reason:               "initial_sync",
			SinceResourceVersion: 0,
		}
	}
	// 同会话且同 epoch 时允许从当前资源版本走增量同步。
	return ReconnectSyncPlan{
		NeedFullSync:         false,
		Reason:               "delta_sync",
		SinceResourceVersion: currentResourceVersion,
	}
}
