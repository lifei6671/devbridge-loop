package directproxy

import (
	"errors"
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// TestFailureMapperMap 验证 external path 各失败阶段映射语义。
func TestFailureMapperMap(testingObject *testing.T) {
	testingObject.Parallel()
	mapper := NewFailureMapper()
	testCases := []struct {
		name           string
		err            error
		kind           FailureKind
		wantHTTPStatus int
		wantCode       string
	}{
		{
			name:           "discovery_miss",
			err:            ErrDiscoveryNoEndpoint,
			kind:           FailureKindDiscoveryMiss,
			wantHTTPStatus: 503,
			wantCode:       ltfperrors.CodeDiscoveryNoEndpoint,
		},
		{
			name:           "provider_down",
			err:            errors.New("provider unavailable"),
			kind:           FailureKindDiscoveryMiss,
			wantHTTPStatus: 503,
			wantCode:       ltfperrors.CodeDiscoveryProviderUnavailable,
		},
		{
			name:           "endpoint_denied",
			err:            ltfperrors.New(ltfperrors.CodeDiscoveryEndpointDenied, "endpoint denied by policy"),
			kind:           FailureKindDiscoveryMiss,
			wantHTTPStatus: 403,
			wantCode:       ltfperrors.CodeDiscoveryEndpointDenied,
		},
		{
			name:           "refresh_failed",
			err:            errors.New("refresh timeout"),
			kind:           FailureKindDiscoveryRefresh,
			wantHTTPStatus: 503,
			wantCode:       ltfperrors.CodeDiscoveryRefreshFailed,
		},
		{
			name:           "direct_dial_failed",
			err:            ErrDirectDialFailed,
			kind:           FailureKindDirectDial,
			wantHTTPStatus: 502,
			wantCode:       ltfperrors.CodeDirectProxyDialFailed,
		},
		{
			name:           "relay_failed",
			err:            errors.New("relay broken"),
			kind:           FailureKindRelay,
			wantHTTPStatus: 502,
			wantCode:       ltfperrors.CodeDirectProxyRelayFailed,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			mappedFailure := mapper.Map(testCase.err, testCase.kind)
			if mappedFailure.HTTPStatus != testCase.wantHTTPStatus {
				testingObject.Fatalf("unexpected http status: got=%d want=%d", mappedFailure.HTTPStatus, testCase.wantHTTPStatus)
			}
			if mappedFailure.Code != testCase.wantCode {
				testingObject.Fatalf("unexpected code: got=%s want=%s", mappedFailure.Code, testCase.wantCode)
			}
		})
	}
}
