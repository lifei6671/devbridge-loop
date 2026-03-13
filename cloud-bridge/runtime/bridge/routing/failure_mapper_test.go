package routing

import (
	"errors"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// TestFailureMapperMap 验证 route + path 失败映射规则。
func TestFailureMapperMap(testingObject *testing.T) {
	testingObject.Parallel()
	mapper := NewFailureMapper()
	testCases := []struct {
		name           string
		err            error
		result         PathExecuteResult
		wantHTTPStatus int
		wantCode       string
	}{
		{
			name:           "route_miss",
			err:            ltfperrors.New(ltfperrors.CodeIngressRouteMismatch, "route miss"),
			wantHTTPStatus: 404,
			wantCode:       ltfperrors.CodeIngressRouteMismatch,
		},
		{
			name:           "service_unavailable",
			err:            ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "service unavailable"),
			wantHTTPStatus: 503,
			wantCode:       ltfperrors.CodeResolveServiceUnavailable,
		},
		{
			name: "connector_result_preferred",
			err:  connectorproxy.ErrNoIdleTunnel,
			result: PathExecuteResult{
				ConnectorResult: &connectorproxy.DispatchResult{
					HTTPStatus: 503,
					ErrorCode:  connectorproxy.FailureCodeNoIdleTunnel,
				},
			},
			wantHTTPStatus: 503,
			wantCode:       connectorproxy.FailureCodeNoIdleTunnel,
		},
		{
			name: "external_result_preferred",
			err:  ltfperrors.New(ltfperrors.CodeDirectProxyDialFailed, "dial failed"),
			result: PathExecuteResult{
				DirectResult: &directproxy.ExecuteResult{
					HTTPStatus: 502,
					ErrorCode:  ltfperrors.CodeDirectProxyDialFailed,
				},
			},
			wantHTTPStatus: 502,
			wantCode:       ltfperrors.CodeDirectProxyDialFailed,
		},
		{
			name: "hybrid_fallback_failure_prefers_direct_result",
			err:  errors.New("fallback failed"),
			result: PathExecuteResult{
				ConnectorResult: &connectorproxy.DispatchResult{
					HTTPStatus: 503,
					ErrorCode:  connectorproxy.FailureCodeNoIdleTunnel,
				},
				DirectResult: &directproxy.ExecuteResult{
					HTTPStatus: 502,
					ErrorCode:  ltfperrors.CodeDirectProxyDialFailed,
				},
			},
			wantHTTPStatus: 502,
			wantCode:       ltfperrors.CodeDirectProxyDialFailed,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			mappedFailure := mapper.Map(testCase.err, testCase.result)
			if mappedFailure.HTTPStatus != testCase.wantHTTPStatus {
				testingObject.Fatalf("unexpected http status: got=%d want=%d", mappedFailure.HTTPStatus, testCase.wantHTTPStatus)
			}
			if mappedFailure.Code != testCase.wantCode {
				testingObject.Fatalf("unexpected code: got=%s want=%s", mappedFailure.Code, testCase.wantCode)
			}
		})
	}
}
