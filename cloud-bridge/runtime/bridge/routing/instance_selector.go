package routing

import (
	"math/rand"
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
)

// ServiceInstanceSelector 定义服务实例选择算法接口，便于后续扩展随机、P2C 等策略。
type ServiceInstanceSelector interface {
	// Select 从候选实例中选择一个目标实例。
	Select(candidates []ConnectorResolution) ConnectorResolution
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
func (selector *RoundRobinServiceInstanceSelector) Select(candidates []ConnectorResolution) ConnectorResolution {
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
func (selector *RandomServiceInstanceSelector) Select(candidates []ConnectorResolution) ConnectorResolution {
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

// NewServiceInstanceSelectorByAlgorithm 按算法名创建实例选择器，不识别时回退轮询。
func NewServiceInstanceSelectorByAlgorithm(algorithm string) ServiceInstanceSelector {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case ServiceInstanceSelectorAlgorithmRandom:
		return NewRandomServiceInstanceSelector()
	case "", ServiceInstanceSelectorAlgorithmRoundRobin:
		return NewRoundRobinServiceInstanceSelector()
	default:
		// 未识别算法时保守回退，避免初始化失败影响转发链路。
		return NewRoundRobinServiceInstanceSelector()
	}
}
