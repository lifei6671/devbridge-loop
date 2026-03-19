package obs

import "testing"

// TestMetricsServiceDimensions 验证服务池/实例维度指标的计数与快照覆盖语义。
func TestMetricsServiceDimensions(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := NewMetrics()

	// 发布维度：服务池和实例都应累计。
	metrics.ObserveBridgeServicePublish("svc-1", "inst-1")
	metrics.ObserveBridgeServicePublish("svc-1", "inst-1")
	metrics.ObserveBridgeServicePublish("svc-1", "inst-2")
	if metrics.BridgeServicePublishTotal("svc-1") != 3 {
		testingObject.Fatalf("unexpected service publish total: got=%d want=3", metrics.BridgeServicePublishTotal("svc-1"))
	}
	if metrics.BridgeServiceInstancePublishTotal("svc-1", "inst-1") != 2 {
		testingObject.Fatalf(
			"unexpected instance publish total: got=%d want=2",
			metrics.BridgeServiceInstancePublishTotal("svc-1", "inst-1"),
		)
	}

	// 可用实例维度：Set 使用快照覆盖语义，重复实例会去重。
	metrics.SetBridgeServiceAvailableInstances("svc-1", []string{"inst-1", "inst-2", "inst-2"})
	if metrics.BridgeServiceAvailableInstanceTotal("svc-1") != 2 {
		testingObject.Fatalf(
			"unexpected available instance total: got=%d want=2",
			metrics.BridgeServiceAvailableInstanceTotal("svc-1"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-1", "inst-2") != 1 {
		testingObject.Fatalf(
			"expected instance available metric is 1, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-1", "inst-2"),
		)
	}
	metrics.SetBridgeServiceAvailableInstances("svc-1", nil)
	if metrics.BridgeServiceAvailableInstanceTotal("svc-1") != 0 {
		testingObject.Fatalf(
			"expected available instance total reset to 0, got=%d",
			metrics.BridgeServiceAvailableInstanceTotal("svc-1"),
		)
	}

	// 路由命中维度：服务池和实例分别计数。
	metrics.ObserveBridgeRouteHit("svc-1", "inst-1")
	metrics.ObserveBridgeRouteHit("svc-1", "inst-1")
	if metrics.BridgeServiceRouteHitTotal("svc-1") != 2 {
		testingObject.Fatalf("unexpected route hit total: got=%d want=2", metrics.BridgeServiceRouteHitTotal("svc-1"))
	}
	if metrics.BridgeServiceInstanceRouteHitTotal("svc-1", "inst-1") != 2 {
		testingObject.Fatalf(
			"unexpected instance route hit total: got=%d want=2",
			metrics.BridgeServiceInstanceRouteHitTotal("svc-1", "inst-1"),
		)
	}
	metrics.ObserveBridgeInstanceSelectorPick("inst-1", "sticky")
	metrics.ObserveBridgeInstanceSelectorPick("inst-1", "sticky")
	if metrics.BridgeInstanceSelectorPickTotal("inst-1", "sticky") != 2 {
		testingObject.Fatalf(
			"unexpected instance selector pick total: got=%d want=2",
			metrics.BridgeInstanceSelectorPickTotal("inst-1", "sticky"),
		)
	}

	// 失败原因维度：空 reason 会归一化为 unknown。
	metrics.ObserveBridgeRouteFailureReason("svc-1", "inst-1", "")
	metrics.ObserveBridgeRouteFailureReason("svc-1", "inst-1", "resolve_service_unavailable")
	if metrics.BridgeServiceRouteFailureReasonTotal("svc-1", "unknown") != 1 {
		testingObject.Fatalf(
			"unexpected unknown failure reason total: got=%d want=1",
			metrics.BridgeServiceRouteFailureReasonTotal("svc-1", "unknown"),
		)
	}
	if metrics.BridgeServiceInstanceRouteFailureReasonTotal("svc-1", "inst-1", "resolve_service_unavailable") != 1 {
		testingObject.Fatalf(
			"unexpected instance failure reason total: got=%d want=1",
			metrics.BridgeServiceInstanceRouteFailureReasonTotal("svc-1", "inst-1", "resolve_service_unavailable"),
		)
	}

	metrics.ObserveBridgeHostDerive(true)
	metrics.ObserveBridgeHostDerive(false)
	if metrics.BridgeHostDeriveTotal(true) != 1 || metrics.BridgeHostDeriveTotal(false) != 1 {
		testingObject.Fatalf(
			"unexpected host derive totals: success=%d failure=%d",
			metrics.BridgeHostDeriveTotal(true),
			metrics.BridgeHostDeriveTotal(false),
		)
	}

	metrics.IncBridgeRouteConflictRejectionTotal()
	if metrics.BridgeRouteConflictRejectionTotal() != 1 {
		testingObject.Fatalf(
			"unexpected route conflict rejection total: got=%d want=1",
			metrics.BridgeRouteConflictRejectionTotal(),
		)
	}
}
