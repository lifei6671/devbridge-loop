package consistency

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestResourceEventGuardEvaluate 验证资源事件守卫在乱序与重放场景下的行为。
func TestResourceEventGuardEvaluate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		meta         ResourceEventMeta
		prepare      func(*ResourceEventGuard)
		expectStatus pb.EventStatus
		expectCode   string
		expectCurVer uint64
	}{
		{
			name: "accept newer version",
			meta: ResourceEventMeta{
				SessionID:       "session-1",
				SessionEpoch:    1,
				EventID:         "evt-1",
				ResourceType:    "service",
				ResourceID:      "svc-1",
				ResourceVersion: 1,
			},
			expectStatus: pb.EventStatusAccepted,
			expectCurVer: 1,
		},
		{
			name: "duplicate by replay window",
			meta: ResourceEventMeta{
				SessionID:       "session-1",
				SessionEpoch:    1,
				EventID:         "evt-1",
				ResourceType:    "service",
				ResourceID:      "svc-1",
				ResourceVersion: 2,
			},
			prepare: func(guard *ResourceEventGuard) {
				// 先写入同 event，模拟重放。
				guard.Evaluate(ResourceEventMeta{
					SessionID:       "session-1",
					SessionEpoch:    1,
					EventID:         "evt-1",
					ResourceType:    "service",
					ResourceID:      "svc-1",
					ResourceVersion: 1,
				})
			},
			expectStatus: pb.EventStatusDuplicate,
			expectCurVer: 1,
		},
		{
			name: "duplicate by equal version",
			meta: ResourceEventMeta{
				SessionID:       "session-1",
				SessionEpoch:    1,
				EventID:         "evt-2",
				ResourceType:    "service",
				ResourceID:      "svc-1",
				ResourceVersion: 3,
			},
			prepare: func(guard *ResourceEventGuard) {
				// 先推进到版本 3，再发送不同 event 但同版本。
				guard.Evaluate(ResourceEventMeta{
					SessionID:       "session-1",
					SessionEpoch:    1,
					EventID:         "evt-1",
					ResourceType:    "service",
					ResourceID:      "svc-1",
					ResourceVersion: 3,
				})
			},
			expectStatus: pb.EventStatusDuplicate,
			expectCurVer: 3,
		},
		{
			name: "reject older version",
			meta: ResourceEventMeta{
				SessionID:       "session-1",
				SessionEpoch:    1,
				EventID:         "evt-2",
				ResourceType:    "service",
				ResourceID:      "svc-1",
				ResourceVersion: 1,
			},
			prepare: func(guard *ResourceEventGuard) {
				// 当前版本先推进到 2。
				guard.Evaluate(ResourceEventMeta{
					SessionID:       "session-1",
					SessionEpoch:    1,
					EventID:         "evt-1",
					ResourceType:    "service",
					ResourceID:      "svc-1",
					ResourceVersion: 2,
				})
			},
			expectStatus: pb.EventStatusRejected,
			expectCode:   ltfperrors.CodeVersionRollback,
			expectCurVer: 2,
		},
		{
			name: "reject missing session id",
			meta: ResourceEventMeta{
				SessionID:       "",
				SessionEpoch:    1,
				EventID:         "evt-1",
				ResourceType:    "service",
				ResourceID:      "svc-1",
				ResourceVersion: 1,
			},
			expectStatus: pb.EventStatusRejected,
			expectCode:   ltfperrors.CodeMissingRequiredField,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			guard := NewResourceEventGuard(64)
			if testCase.prepare != nil {
				// 每个子用例先准备前置状态。
				testCase.prepare(guard)
			}

			decision := guard.Evaluate(testCase.meta)
			if decision.Status != testCase.expectStatus {
				t.Fatalf("unexpected status: got=%s want=%s", decision.Status, testCase.expectStatus)
			}
			if testCase.expectCode != "" && decision.ErrorCode != testCase.expectCode {
				t.Fatalf("unexpected error code: got=%s want=%s", decision.ErrorCode, testCase.expectCode)
			}
			if testCase.expectCurVer != 0 && decision.CurrentResourceVersion != testCase.expectCurVer {
				t.Fatalf("unexpected current version: got=%d want=%d", decision.CurrentResourceVersion, testCase.expectCurVer)
			}
		})
	}
}

// TestResourceEventGuardReplaceAllVersions 验证 full-sync 版本快照覆盖行为。
func TestResourceEventGuardReplaceAllVersions(t *testing.T) {
	t.Parallel()

	guard := NewResourceEventGuard(8)
	guard.SetVersion("service", "svc-1", 3)
	guard.SetVersion("route", "route-1", 5)

	guard.ReplaceAllVersions(map[string]uint64{
		"service:svc-2": 9,
		"route:route-2": 7,
	})

	snapshot := guard.SnapshotVersions()
	if len(snapshot) != 2 {
		t.Fatalf("unexpected snapshot size: got=%d want=2", len(snapshot))
	}
	if snapshot["service:svc-2"] != 9 {
		t.Fatalf("unexpected service version: got=%d want=9", snapshot["service:svc-2"])
	}
	if snapshot["route:route-2"] != 7 {
		t.Fatalf("unexpected route version: got=%d want=7", snapshot["route:route-2"])
	}
}
