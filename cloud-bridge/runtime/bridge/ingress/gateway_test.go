package ingress

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestGatewayBuildRouteLookupRequest 验证三类入口都能输出统一路由请求。
func TestGatewayBuildRouteLookupRequest(testingObject *testing.T) {
	testingObject.Parallel()
	tests := []struct {
		name         string
		buildRequest func() (RouteLookupRequest, error)
		assert       func(testingObject *testing.T, request RouteLookupRequest)
	}{
		{
			name: "http_l7_shared",
			buildRequest: func() (RouteLookupRequest, error) {
				return NewHTTPGateway(80).BuildRouteLookupRequest(HTTPGatewayRequest{
					Host:        "api.dev.example.com:443",
					Path:        "orders/1",
					Namespace:   "dev",
					Environment: "alice",
				})
			},
			assert: func(testingObject *testing.T, request RouteLookupRequest) {
				if request.IngressMode != pb.IngressModeL7Shared {
					testingObject.Fatalf("unexpected ingress mode: %s", request.IngressMode)
				}
				if request.Protocol != "http" {
					testingObject.Fatalf("unexpected protocol: %s", request.Protocol)
				}
				if request.Host != "api.dev.example.com" || request.Authority != "api.dev.example.com:443" {
					testingObject.Fatalf("unexpected host/authority: host=%s authority=%s", request.Host, request.Authority)
				}
				if request.Path != "/orders/1" {
					testingObject.Fatalf("unexpected path: %s", request.Path)
				}
			},
		},
		{
			name: "grpc_l7_shared",
			buildRequest: func() (RouteLookupRequest, error) {
				return NewGRPCGateway(443).BuildRouteLookupRequest(GRPCGatewayRequest{
					Authority: "order.dev.example.com:443",
					Path:      "/pkg.Service/Method",
				})
			},
			assert: func(testingObject *testing.T, request RouteLookupRequest) {
				if request.IngressMode != pb.IngressModeL7Shared {
					testingObject.Fatalf("unexpected ingress mode: %s", request.IngressMode)
				}
				if request.Protocol != "grpc" {
					testingObject.Fatalf("unexpected protocol: %s", request.Protocol)
				}
				if request.Host != "order.dev.example.com" {
					testingObject.Fatalf("unexpected host: %s", request.Host)
				}
			},
		},
		{
			name: "tls_sni_shared",
			buildRequest: func() (RouteLookupRequest, error) {
				return NewTLSSNIGateway(443).BuildRouteLookupRequest(TLSSNIGatewayRequest{
					SNI: "Order.Dev.Example.com",
				})
			},
			assert: func(testingObject *testing.T, request RouteLookupRequest) {
				if request.IngressMode != pb.IngressModeTLSSNIShared {
					testingObject.Fatalf("unexpected ingress mode: %s", request.IngressMode)
				}
				if request.SNI != "order.dev.example.com" {
					testingObject.Fatalf("unexpected sni: %s", request.SNI)
				}
				if request.ListenPort != 443 {
					testingObject.Fatalf("unexpected listen port: %d", request.ListenPort)
				}
			},
		},
		{
			name: "l4_dedicated_port",
			buildRequest: func() (RouteLookupRequest, error) {
				return NewTCPPortGateway().BuildRouteLookupRequest(TCPPortGatewayRequest{
					ListenPort:  18081,
					Namespace:   "dev",
					Environment: "alice",
				})
			},
			assert: func(testingObject *testing.T, request RouteLookupRequest) {
				if request.IngressMode != pb.IngressModeL4DedicatedPort {
					testingObject.Fatalf("unexpected ingress mode: %s", request.IngressMode)
				}
				if request.ListenPort != 18081 {
					testingObject.Fatalf("unexpected listen port: %d", request.ListenPort)
				}
				if request.Protocol != "tcp" {
					testingObject.Fatalf("unexpected protocol: %s", request.Protocol)
				}
			},
		},
	}
	for _, testCase := range tests {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			request, err := testCase.buildRequest()
			if err != nil {
				testingObject.Fatalf("build request failed: %v", err)
			}
			testCase.assert(testingObject, request)
		})
	}
}

// TestGatewayBuildRouteLookupRequestRejectInvalidInput 验证入口校验失败场景。
func TestGatewayBuildRouteLookupRequestRejectInvalidInput(testingObject *testing.T) {
	testingObject.Parallel()
	if _, err := NewTLSSNIGateway(443).BuildRouteLookupRequest(TLSSNIGatewayRequest{}); err == nil {
		testingObject.Fatalf("expected tls-sni request validation error")
	}
	if _, err := NewTCPPortGateway().BuildRouteLookupRequest(TCPPortGatewayRequest{}); err == nil {
		testingObject.Fatalf("expected dedicated port validation error")
	}
}

// TestValidateSharedTLSListenerConstraint 验证 https 与 tlsSni 共享 listener 约束。
func TestValidateSharedTLSListenerConstraint(testingObject *testing.T) {
	testingObject.Parallel()
	if err := ValidateSharedTLSListenerConstraint(SharedTLSListenerConfig{
		HTTPSListenAddr:  ":443",
		TLSSNIListenAddr: ":443",
	}); err != nil {
		testingObject.Fatalf("expected shared listener accepted, got: %v", err)
	}
	if err := ValidateSharedTLSListenerConstraint(SharedTLSListenerConfig{
		HTTPSListenAddr:  ":443",
		TLSSNIListenAddr: ":8443",
	}); err == nil {
		testingObject.Fatalf("expected mismatch listener rejected")
	}
}
