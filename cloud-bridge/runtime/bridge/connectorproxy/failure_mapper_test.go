package connectorproxy

import (
	"errors"
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestFailureMapperMap 验证 connector path 失败映射语义。
func TestFailureMapperMap(testingObject *testing.T) {
	testingObject.Parallel()
	mapper := NewFailureMapper()
	testCases := []struct {
		name           string
		err            error
		wantHTTPStatus int
		wantCode       string
	}{
		{
			name:           "no_idle_tunnel",
			err:            ErrNoIdleTunnel,
			wantHTTPStatus: 503,
			wantCode:       FailureCodeNoIdleTunnel,
		},
		{
			name: "connector_dial_failed",
			err: &OpenRejectedError{
				Ack: pb.TrafficOpenAck{
					Success:      false,
					ErrorCode:    ltfperrors.CodeConnectorDialFailed,
					ErrorMessage: "dial upstream failed",
				},
			},
			wantHTTPStatus: 502,
			wantCode:       FailureCodeDialFailed,
		},
		{
			name: "open_rejected",
			err: &OpenRejectedError{
				Ack: pb.TrafficOpenAck{
					Success:      false,
					ErrorCode:    ltfperrors.CodeTrafficOpenRejected,
					ErrorMessage: "endpoint rejected",
				},
			},
			wantHTTPStatus: 503,
			wantCode:       FailureCodeOpenRejected,
		},
		{
			name:           "open_timeout",
			err:            ErrOpenAckTimeout,
			wantHTTPStatus: 504,
			wantCode:       FailureCodeOpenAckTimeout,
		},
		{
			name:           "relay_reset",
			err:            ErrRelayReset,
			wantHTTPStatus: 502,
			wantCode:       FailureCodeRelayFailed,
		},
		{
			name:           "default_failed",
			err:            errors.New("connector failed"),
			wantHTTPStatus: 502,
			wantCode:       FailureCodeConnectorProxyFailed,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			mappedFailure := mapper.Map(testCase.err)
			if mappedFailure.HTTPStatus != testCase.wantHTTPStatus {
				testingObject.Fatalf("unexpected http status: got=%d want=%d", mappedFailure.HTTPStatus, testCase.wantHTTPStatus)
			}
			if mappedFailure.Code != testCase.wantCode {
				testingObject.Fatalf("unexpected error code: got=%s want=%s", mappedFailure.Code, testCase.wantCode)
			}
		})
	}
}
