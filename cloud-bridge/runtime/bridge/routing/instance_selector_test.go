package routing

import (
	"reflect"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

// TestRoundRobinServiceInstanceSelectorSelect 验证轮询选择器会按顺序轮转候选实例。
func TestRoundRobinServiceInstanceSelectorSelect(testingObject *testing.T) {
	testingObject.Parallel()

	selector := NewRoundRobinServiceInstanceSelector()
	candidates := []ConnectorResolution{
		{Session: sessionRuntimeForSelectorTest("connector-a")},
		{Session: sessionRuntimeForSelectorTest("connector-b")},
		{Session: sessionRuntimeForSelectorTest("connector-c")},
	}
	observed := make([]string, 0, 6)
	for index := 0; index < 6; index++ {
		selected := selector.Select(candidates)
		observed = append(observed, selected.Session.ConnectorID)
	}
	expected := []string{
		"connector-a",
		"connector-b",
		"connector-c",
		"connector-a",
		"connector-b",
		"connector-c",
	}
	for index := range expected {
		if observed[index] != expected[index] {
			testingObject.Fatalf(
				"unexpected round robin order at index=%d: got=%s want=%s full=%v",
				index,
				observed[index],
				expected[index],
				observed,
			)
		}
	}
}

// TestRandomServiceInstanceSelectorSelect 验证随机选择器会在候选集合中返回合法实例。
func TestRandomServiceInstanceSelectorSelect(testingObject *testing.T) {
	testingObject.Parallel()

	selector := NewRandomServiceInstanceSelector()
	candidates := []ConnectorResolution{
		{Session: sessionRuntimeForSelectorTest("connector-a")},
		{Session: sessionRuntimeForSelectorTest("connector-b")},
	}
	observed := map[string]struct{}{}
	for index := 0; index < 128; index++ {
		selected := selector.Select(candidates)
		if selected.Session.ConnectorID != "connector-a" && selected.Session.ConnectorID != "connector-b" {
			testingObject.Fatalf("random selector returns unexpected connector: %s", selected.Session.ConnectorID)
		}
		observed[selected.Session.ConnectorID] = struct{}{}
	}
	// 多次采样后应同时出现两个候选，证明策略具备随机分布特征。
	if len(observed) != 2 {
		testingObject.Fatalf("random selector did not cover all candidates, observed=%v", observed)
	}
}

// TestNewServiceInstanceSelectorByAlgorithm 验证按算法名工厂可返回预期选择器实现。
func TestNewServiceInstanceSelectorByAlgorithm(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name             string
		algorithm        string
		wantSelectorType string
	}{
		{
			name:             "random",
			algorithm:        ServiceInstanceSelectorAlgorithmRandom,
			wantSelectorType: "*routing.RandomServiceInstanceSelector",
		},
		{
			name:             "round_robin",
			algorithm:        ServiceInstanceSelectorAlgorithmRoundRobin,
			wantSelectorType: "*routing.RoundRobinServiceInstanceSelector",
		},
		{
			name:             "fallback_unknown",
			algorithm:        "unknown",
			wantSelectorType: "*routing.RoundRobinServiceInstanceSelector",
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			selector := NewServiceInstanceSelectorByAlgorithm(testCase.algorithm)
			if selector == nil {
				testingObject.Fatalf("selector should not be nil")
			}
			if actualType := toSelectorTypeName(selector); actualType != testCase.wantSelectorType {
				testingObject.Fatalf(
					"unexpected selector type: got=%s want=%s",
					actualType,
					testCase.wantSelectorType,
				)
			}
		})
	}
}

// sessionRuntimeForSelectorTest 构造最小会话快照，避免在测试里重复样板代码。
func sessionRuntimeForSelectorTest(connectorID string) registry.SessionRuntime {
	return registry.SessionRuntime{ConnectorID: connectorID}
}

// toSelectorTypeName 返回选择器的具体类型名，便于测试断言工厂行为。
func toSelectorTypeName(selector ServiceInstanceSelector) string {
	return reflect.TypeOf(selector).String()
}
