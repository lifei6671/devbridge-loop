package routing

import (
	"hash/fnv"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ServiceInstanceSelectorAlgorithmRoundRobin 表示轮询选择算法。
	ServiceInstanceSelectorAlgorithmRoundRobin = "round_robin"
	// ServiceInstanceSelectorAlgorithmRandom 表示随机选择算法。
	ServiceInstanceSelectorAlgorithmRandom = "random"
	// ServiceInstanceSelectorAlgorithmSticky 表示按粘性 key 稳定选择实例。
	ServiceInstanceSelectorAlgorithmSticky = "sticky"
	// ServiceInstanceSelectorAlgorithmWeighted 表示按实例权重执行加权轮询。
	ServiceInstanceSelectorAlgorithmWeighted = "weighted"
)

// ServiceInstanceSelectionRequest 描述一次实例选择的上下文。
type ServiceInstanceSelectionRequest struct {
	Policy    string
	StickyKey string
}

// ServiceInstanceSelector 定义服务实例选择算法接口，便于后续扩展随机、P2C 等策略。
type ServiceInstanceSelector interface {
	// Select 从候选实例中选择一个目标实例。
	Select(candidates []ConnectorResolution, request ServiceInstanceSelectionRequest) ConnectorResolution
}

// RoundRobinServiceInstanceSelector 基于轮询的实例选择器。
type RoundRobinServiceInstanceSelector struct {
	sequence uint64
}

// NewRoundRobinServiceInstanceSelector 创建轮询实例选择器。
func NewRoundRobinServiceInstanceSelector() *RoundRobinServiceInstanceSelector {
	return &RoundRobinServiceInstanceSelector{}
}

// Select 使用轮询算法在候选实例中选择目标实例。
func (selector *RoundRobinServiceInstanceSelector) Select(candidates []ConnectorResolution, _ ServiceInstanceSelectionRequest) ConnectorResolution {
	if len(candidates) == 0 {
		// 防御性兜底：上游已保证非空，这里仅避免越界。
		return ConnectorResolution{}
	}
	if len(candidates) == 1 || selector == nil {
		return candidates[0]
	}
	// 使用原子递增保证并发访问下轮询下标稳定。
	selectedIndex := int((atomic.AddUint64(&selector.sequence, 1) - 1) % uint64(len(candidates)))
	return candidates[selectedIndex]
}

// RandomServiceInstanceSelector 基于随机的实例选择器。
type RandomServiceInstanceSelector struct {
	mu  sync.Mutex
	rng *rand.Rand
}

// NewRandomServiceInstanceSelector 创建随机实例选择器。
func NewRandomServiceInstanceSelector() *RandomServiceInstanceSelector {
	return &RandomServiceInstanceSelector{
		// 使用纳秒时间初始化随机源，保证各进程实例起点差异。
		rng: rand.New(rand.NewSource(time.Now().UTC().UnixNano())),
	}
}

// Select 使用随机算法在候选实例中选择目标实例。
func (selector *RandomServiceInstanceSelector) Select(candidates []ConnectorResolution, _ ServiceInstanceSelectionRequest) ConnectorResolution {
	if len(candidates) == 0 {
		return ConnectorResolution{}
	}
	if len(candidates) == 1 || selector == nil || selector.rng == nil {
		return candidates[0]
	}
	selector.mu.Lock()
	// 使用互斥锁保护 rng，避免并发调用导致数据竞争。
	selectedIndex := selector.rng.Intn(len(candidates))
	selector.mu.Unlock()
	return candidates[selectedIndex]
}

// StickyServiceInstanceSelector 基于粘性 key 执行稳定实例选择。
type StickyServiceInstanceSelector struct {
	fallback *RoundRobinServiceInstanceSelector
}

// NewStickyServiceInstanceSelector 创建粘性实例选择器。
func NewStickyServiceInstanceSelector() *StickyServiceInstanceSelector {
	return &StickyServiceInstanceSelector{
		fallback: NewRoundRobinServiceInstanceSelector(),
	}
}

// Select 使用粘性 key 执行稳定实例选择；缺失 key 时回退轮询。
func (selector *StickyServiceInstanceSelector) Select(candidates []ConnectorResolution, request ServiceInstanceSelectionRequest) ConnectorResolution {
	if len(candidates) == 0 {
		return ConnectorResolution{}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	normalizedStickyKey := strings.TrimSpace(request.StickyKey)
	if normalizedStickyKey == "" {
		if selector == nil || selector.fallback == nil {
			return candidates[0]
		}
		return selector.fallback.Select(candidates, request)
	}
	sortedCandidates := append([]ConnectorResolution(nil), candidates...)
	sort.Slice(sortedCandidates, func(left, right int) bool {
		return strings.TrimSpace(sortedCandidates[left].Instance.InstanceID) < strings.TrimSpace(sortedCandidates[right].Instance.InstanceID)
	})
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(normalizedStickyKey))
	selectedIndex := int(hasher.Sum64() % uint64(len(sortedCandidates)))
	return sortedCandidates[selectedIndex]
}

// WeightedServiceInstanceSelector 基于实例权重执行加权轮询。
type WeightedServiceInstanceSelector struct {
	sequence uint64
}

// NewWeightedServiceInstanceSelector 创建加权实例选择器。
func NewWeightedServiceInstanceSelector() *WeightedServiceInstanceSelector {
	return &WeightedServiceInstanceSelector{}
}

// Select 按实例 weight 执行加权轮询；无效权重自动回退为 1。
func (selector *WeightedServiceInstanceSelector) Select(candidates []ConnectorResolution, _ ServiceInstanceSelectionRequest) ConnectorResolution {
	if len(candidates) == 0 {
		return ConnectorResolution{}
	}
	if len(candidates) == 1 || selector == nil {
		return candidates[0]
	}
	totalWeight := 0
	weights := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		weight := resolveCandidateWeight(candidate)
		weights = append(weights, weight)
		totalWeight += weight
	}
	if totalWeight <= 0 {
		return candidates[0]
	}
	selectedCursor := int((atomic.AddUint64(&selector.sequence, 1) - 1) % uint64(totalWeight))
	accumulatedWeight := 0
	for index, weight := range weights {
		accumulatedWeight += weight
		if selectedCursor < accumulatedWeight {
			return candidates[index]
		}
	}
	return candidates[len(candidates)-1]
}

// NewServiceInstanceSelectorByAlgorithm 按算法名创建实例选择器，不识别时回退轮询。
func NewServiceInstanceSelectorByAlgorithm(algorithm string) ServiceInstanceSelector {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case ServiceInstanceSelectorAlgorithmRandom:
		return NewRandomServiceInstanceSelector()
	case ServiceInstanceSelectorAlgorithmSticky:
		return NewStickyServiceInstanceSelector()
	case ServiceInstanceSelectorAlgorithmWeighted:
		return NewWeightedServiceInstanceSelector()
	case "", ServiceInstanceSelectorAlgorithmRoundRobin:
		return NewRoundRobinServiceInstanceSelector()
	default:
		// 未识别算法时保守回退，避免初始化失败影响转发链路。
		return NewRoundRobinServiceInstanceSelector()
	}
}

func resolveCandidateWeight(candidate ConnectorResolution) int {
	weight := parsePositiveWeight(candidate.Instance.Metadata["weight"])
	if weight > 0 {
		return weight
	}
	weight = parsePositiveWeight(candidate.Instance.Labels["weight"])
	if weight > 0 {
		return weight
	}
	return 1
}

func parsePositiveWeight(rawWeight string) int {
	normalizedWeight := strings.TrimSpace(rawWeight)
	if normalizedWeight == "" {
		return 0
	}
	parsedWeight, err := strconv.Atoi(normalizedWeight)
	if err != nil || parsedWeight <= 0 {
		return 0
	}
	return parsedWeight
}
