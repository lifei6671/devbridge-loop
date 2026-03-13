package ingress

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TCPPortGatewayRequest 描述 L4 dedicated port 入口请求上下文。
type TCPPortGatewayRequest struct {
	ListenPort  uint32
	Namespace   string
	Environment string
	Metadata    map[string]string
}

// TCPPortGateway 负责把裸 TCP 请求转换为统一路由请求。
type TCPPortGateway struct{}

// NewTCPPortGateway 创建 L4 dedicated port 入口适配器。
func NewTCPPortGateway() *TCPPortGateway {
	return &TCPPortGateway{}
}

// BuildRouteLookupRequest 将 TCP 入口请求转换为统一路由请求。
func (gateway *TCPPortGateway) BuildRouteLookupRequest(request TCPPortGatewayRequest) (RouteLookupRequest, error) {
	_ = gateway
	if request.ListenPort == 0 {
		return RouteLookupRequest{}, fmt.Errorf("build tcp route lookup request: empty listen port")
	}
	return RouteLookupRequest{
		IngressMode: pb.IngressModeL4DedicatedPort,
		Protocol:    "tcp",
		ListenPort:  request.ListenPort,
		Namespace:   strings.TrimSpace(request.Namespace),
		Environment: strings.TrimSpace(request.Environment),
		Metadata:    copyStringMap(request.Metadata),
	}, nil
}
