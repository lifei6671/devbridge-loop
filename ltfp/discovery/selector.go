package discovery

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// SelectStrategy 定义 endpoint 选择策略。
type SelectStrategy string

const (
	// SelectStrategyRoundRobin 表示轮询选路。
	SelectStrategyRoundRobin SelectStrategy = "round_robin"
	// SelectStrategyRandom 表示随机选路。
	SelectStrategyRandom SelectStrategy = "random"
)

// EndpointSelector 提供 external endpoint 选择能力。
type EndpointSelector struct {
	mu      sync.Mutex
	counter uint64
	random  *rand.Rand
}

// NewEndpointSelector 创建 endpoint selector。
func NewEndpointSelector(seed int64) *EndpointSelector {
	return &EndpointSelector{
		// 固定随机种子用于测试时构造可复现实验。
		random: rand.New(rand.NewSource(seed)),
	}
}

// Select 从候选 endpoints 中按策略选择一个可用 endpoint。
func (selector *EndpointSelector) Select(strategy SelectStrategy, endpoints []Endpoint) (Endpoint, error) {
	usable := filterUsableEndpoints(endpoints)
	if len(usable) == 0 {
		return Endpoint{}, ltfperrors.New(ltfperrors.CodeDiscoveryNoEndpoint, "no usable endpoint for selection")
	}

	selector.mu.Lock()
	defer selector.mu.Unlock()
	switch strategy {
	case SelectStrategyRoundRobin:
		// 轮询模式优先用于稳定流量分摊。
		index := selector.counter % uint64(len(usable))
		selector.counter++
		return usable[index], nil
	case SelectStrategyRandom:
		// 随机模式用于简单抖动打散。
		index := selector.random.Intn(len(usable))
		return usable[index], nil
	default:
		return Endpoint{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported endpoint select strategy: %s", strategy))
	}
}

// filterUsableEndpoints 过滤可参与选路的 endpoint。
func filterUsableEndpoints(endpoints []Endpoint) []Endpoint {
	usable := make([]Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		host := strings.TrimSpace(endpoint.Host)
		if host == "" || endpoint.Port == 0 {
			// host/port 缺失的 endpoint 直接忽略。
			continue
		}
		if endpoint.HealthStatus == pb.HealthStatusUnhealthy {
			// UNHEALTHY endpoint 不参与 direct proxy 选路。
			continue
		}
		usable = append(usable, endpoint)
	}
	return usable
}
