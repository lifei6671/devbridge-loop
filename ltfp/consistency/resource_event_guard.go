package consistency

import (
	"strings"
	"sync"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// ResourceEventMeta 描述资源级事件的一致性元信息。
type ResourceEventMeta struct {
	SessionID       string
	SessionEpoch    uint64
	EventID         string
	ResourceType    string
	ResourceID      string
	ResourceVersion uint64
}

// ResourceEventDecision 描述资源事件一致性判定结果。
type ResourceEventDecision struct {
	Status                  pb.EventStatus
	DedupKey                string
	ResourceKey             string
	AcceptedResourceVersion uint64
	CurrentResourceVersion  uint64
	ErrorCode               string
	ErrorMessage            string
}

// ResourceEventGuard 维护资源级去重窗口与版本视图。
type ResourceEventGuard struct {
	mu               sync.Mutex
	replayWindow     *ReplayWindow
	resourceVersions map[string]uint64
}

// NewResourceEventGuard 创建资源事件守卫。
func NewResourceEventGuard(replayCapacity int) *ResourceEventGuard {
	return &ResourceEventGuard{
		replayWindow:     NewReplayWindow(replayCapacity),
		resourceVersions: make(map[string]uint64),
	}
}

// Evaluate 对单条资源事件执行去重与版本判定。
func (guard *ResourceEventGuard) Evaluate(meta ResourceEventMeta) ResourceEventDecision {
	guard.mu.Lock()
	defer guard.mu.Unlock()

	decision, valid := guard.validateAndBuildKeys(meta)
	if !valid {
		// 元信息不合法时直接拒绝，避免污染版本视图。
		return decision
	}

	// 同一会话代际下 eventID 重复，按幂等 duplicate 处理。
	if guard.replayWindow.SeenOrAdd(decision.DedupKey) {
		decision.Status = pb.EventStatusDuplicate
		decision.CurrentResourceVersion = guard.resourceVersions[decision.ResourceKey]
		decision.AcceptedResourceVersion = decision.CurrentResourceVersion
		return decision
	}

	current := guard.resourceVersions[decision.ResourceKey]
	relation := CompareResourceVersion(current, meta.ResourceVersion)
	if relation == VersionRelationOlder {
		// 旧版本事件一律拒绝，防止状态回滚。
		err := ltfperrors.New(ltfperrors.CodeVersionRollback, "incoming resourceVersion is older than current resourceVersion")
		decision.Status = pb.EventStatusRejected
		decision.CurrentResourceVersion = current
		decision.AcceptedResourceVersion = current
		decision.ErrorCode = ltfperrors.ExtractCode(err)
		decision.ErrorMessage = err.Error()
		return decision
	}
	if relation == VersionRelationEqual {
		// 同版本视作重复提交，不再重复执行副作用。
		decision.Status = pb.EventStatusDuplicate
		decision.CurrentResourceVersion = current
		decision.AcceptedResourceVersion = current
		return decision
	}

	// 新版本事件可接受，并推进本地版本视图。
	guard.resourceVersions[decision.ResourceKey] = meta.ResourceVersion
	decision.Status = pb.EventStatusAccepted
	decision.CurrentResourceVersion = meta.ResourceVersion
	decision.AcceptedResourceVersion = meta.ResourceVersion
	return decision
}

// SetVersion 强制设置某资源的当前版本（用于 full-sync 对账）。
func (guard *ResourceEventGuard) SetVersion(resourceType string, resourceID string, version uint64) {
	if version == 0 {
		// 版本号为 0 时不写入，避免破坏版本单调性。
		return
	}
	resourceKey, ok := buildResourceKey(resourceType, resourceID)
	if !ok {
		// 资源键不合法时直接忽略。
		return
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	// full-sync 后使用快照中的权威版本覆盖本地。
	guard.resourceVersions[resourceKey] = version
}

// ReplaceAllVersions 用快照全量替换当前版本视图。
func (guard *ResourceEventGuard) ReplaceAllVersions(snapshot map[string]uint64) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	// 先清空旧视图，确保 full-sync 后无遗留数据。
	guard.resourceVersions = make(map[string]uint64, len(snapshot))
	for key, version := range snapshot {
		if strings.TrimSpace(key) == "" {
			// 空键不写入，避免脏数据污染。
			continue
		}
		if version == 0 {
			// 版本为 0 的条目不写入。
			continue
		}
		guard.resourceVersions[key] = version
	}
}

// SnapshotVersions 返回当前版本视图的副本。
func (guard *ResourceEventGuard) SnapshotVersions() map[string]uint64 {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	result := make(map[string]uint64, len(guard.resourceVersions))
	for key, version := range guard.resourceVersions {
		// 返回副本，避免外部修改内部状态。
		result[key] = version
	}
	return result
}

// validateAndBuildKeys 校验元信息并生成去重键与资源键。
func (guard *ResourceEventGuard) validateAndBuildKeys(meta ResourceEventMeta) (ResourceEventDecision, bool) {
	decision := ResourceEventDecision{}
	if err := validate.ValidateResourceMeta(meta.SessionID, meta.SessionEpoch, meta.EventID, meta.ResourceVersion); err != nil {
		// 元信息校验失败时返回 rejected。
		decision.Status = pb.EventStatusRejected
		decision.ErrorCode = ltfperrors.ExtractCode(err)
		decision.ErrorMessage = err.Error()
		return decision, false
	}
	dedupKey, err := BuildDedupKey(meta.SessionID, meta.SessionEpoch, meta.EventID)
	if err != nil {
		// 去重键构造失败同样按 rejected 处理。
		decision.Status = pb.EventStatusRejected
		decision.ErrorCode = ltfperrors.ExtractCode(err)
		decision.ErrorMessage = err.Error()
		return decision, false
	}
	resourceKey, ok := buildResourceKey(meta.ResourceType, meta.ResourceID)
	if !ok {
		// 资源键不合法会导致版本比较失效，直接拒绝。
		err = ltfperrors.New(ltfperrors.CodeMissingRequiredField, "resourceType and resourceID are required")
		decision.Status = pb.EventStatusRejected
		decision.ErrorCode = ltfperrors.ExtractCode(err)
		decision.ErrorMessage = err.Error()
		return decision, false
	}
	decision.DedupKey = dedupKey
	decision.ResourceKey = resourceKey
	return decision, true
}

// buildResourceKey 生成资源版本索引键。
func buildResourceKey(resourceType string, resourceID string) (string, bool) {
	normalizedType := strings.TrimSpace(resourceType)
	normalizedID := strings.TrimSpace(resourceID)
	if normalizedType == "" || normalizedID == "" {
		// type/id 任一缺失都无法唯一标识资源。
		return "", false
	}
	return normalizedType + ":" + normalizedID, true
}
