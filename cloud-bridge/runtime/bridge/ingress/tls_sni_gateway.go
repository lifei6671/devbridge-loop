package ingress

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TLSSNIGatewayRequest 描述 TLS ClientHello 阶段可用的匹配信息。
type TLSSNIGatewayRequest struct {
	SNI         string
	Namespace   string
	Environment string
	Metadata    map[string]string
}

// SharedTLSListenerConfig 描述 HTTPS/TLS-SNI 共享监听配置。
type SharedTLSListenerConfig struct {
	HTTPSListenAddr  string
	TLSSNIListenAddr string
}

// TLSSNIGateway 负责把 TLS SNI 请求转换为统一路由请求。
type TLSSNIGateway struct {
	ListenPort uint32
}

// NewTLSSNIGateway 创建 TLS-SNI 入口适配器。
func NewTLSSNIGateway(listenPort uint32) *TLSSNIGateway {
	return &TLSSNIGateway{ListenPort: listenPort}
}

// BuildRouteLookupRequest 将 TLS SNI 请求转换为统一路由请求。
func (gateway *TLSSNIGateway) BuildRouteLookupRequest(request TLSSNIGatewayRequest) (RouteLookupRequest, error) {
	normalizedSNI := strings.ToLower(strings.TrimSpace(request.SNI))
	if normalizedSNI == "" {
		return RouteLookupRequest{}, fmt.Errorf("build tls-sni route lookup request: empty sni")
	}
	return RouteLookupRequest{
		IngressMode: pb.IngressModeTLSSNIShared,
		Protocol:    "tls",
		Host:        normalizedSNI,
		Authority:   normalizedSNI,
		SNI:         normalizedSNI,
		ListenPort:  gateway.ListenPort,
		Namespace:   strings.TrimSpace(request.Namespace),
		Environment: strings.TrimSpace(request.Environment),
		Metadata:    copyStringMap(request.Metadata),
	}, nil
}

// ValidateSharedTLSListenerConstraint 校验 HTTPS 与 TLS-SNI 必须复用同一监听地址。
func ValidateSharedTLSListenerConstraint(config SharedTLSListenerConfig) error {
	normalizedHTTPSAddr := strings.TrimSpace(config.HTTPSListenAddr)
	normalizedTLSSNIAddr := strings.TrimSpace(config.TLSSNIListenAddr)
	if normalizedHTTPSAddr == "" && normalizedTLSSNIAddr == "" {
		// 两者都未配置时不做约束校验。
		return nil
	}
	if normalizedHTTPSAddr == "" || normalizedTLSSNIAddr == "" {
		return fmt.Errorf(
			"validate shared tls listener constraint: https=%q tls_sni=%q must both be configured",
			normalizedHTTPSAddr,
			normalizedTLSSNIAddr,
		)
	}
	if normalizedHTTPSAddr != normalizedTLSSNIAddr {
		return fmt.Errorf(
			"validate shared tls listener constraint: https=%q tls_sni=%q must share same listener",
			normalizedHTTPSAddr,
			normalizedTLSSNIAddr,
		)
	}
	return nil
}
