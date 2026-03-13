package directproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrDiscoveryAdapterDependencyMissing 表示 discovery 适配器依赖缺失。
	ErrDiscoveryAdapterDependencyMissing = errors.New("direct discovery adapter dependency missing")
	// ErrDiscoveryNoEndpoint 表示 discovery 未返回可用 endpoint。
	ErrDiscoveryNoEndpoint = errors.New("direct discovery has no endpoint")
)

// ExternalEndpoint 描述 direct proxy 使用的外部 endpoint。
type ExternalEndpoint struct {
	EndpointID string
	Address    string
	Metadata   map[string]string
}

// DiscoveryRequest 描述外部发现查询输入。
type DiscoveryRequest struct {
	Provider    string
	Namespace   string
	Environment string
	ServiceName string
	Group       string
	Selector    map[string]string
}

// DiscoveryProvider 定义底层 discovery provider 能力。
type DiscoveryProvider interface {
	Discover(ctx context.Context, request DiscoveryRequest) ([]ExternalEndpoint, error)
}

// DiscoveryAdapterOptions 定义 discovery 适配器构造参数。
type DiscoveryAdapterOptions struct {
	Provider DiscoveryProvider
}

// DiscoveryAdapter fetches external endpoints.
type DiscoveryAdapter struct {
	provider DiscoveryProvider
}

// NewDiscoveryAdapter 创建 discovery 适配器。
func NewDiscoveryAdapter(options DiscoveryAdapterOptions) (*DiscoveryAdapter, error) {
	if options.Provider == nil {
		return nil, ErrDiscoveryAdapterDependencyMissing
	}
	return &DiscoveryAdapter{
		provider: options.Provider,
	}, nil
}

// Discover 调用 provider 查询目标 service 的 endpoint 列表。
func (adapter *DiscoveryAdapter) Discover(ctx context.Context, target pb.ExternalServiceTarget) ([]ExternalEndpoint, error) {
	if adapter == nil || adapter.provider == nil {
		return nil, ErrDiscoveryAdapterDependencyMissing
	}
	normalizedServiceName := strings.TrimSpace(target.ServiceName)
	if normalizedServiceName == "" {
		return nil, ltfperrors.New(
			ltfperrors.CodeMissingRequiredField,
			"external_service.service_name is required",
		)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	endpoints, err := adapter.provider.Discover(normalizedContext, DiscoveryRequest{
		Provider:    strings.TrimSpace(target.Provider),
		Namespace:   strings.TrimSpace(target.Namespace),
		Environment: strings.TrimSpace(target.Environment),
		ServiceName: normalizedServiceName,
		Group:       strings.TrimSpace(target.Group),
		Selector:    copyStringMap(target.Selector),
	})
	if err != nil {
		return nil, fmt.Errorf("direct discovery failed: %w", err)
	}
	filtered := make([]ExternalEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Address) == "" {
			continue
		}
		filtered = append(filtered, ExternalEndpoint{
			EndpointID: strings.TrimSpace(endpoint.EndpointID),
			Address:    strings.TrimSpace(endpoint.Address),
			Metadata:   copyStringMap(endpoint.Metadata),
		})
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf(
			"direct discovery failed: %w: provider=%s namespace=%s environment=%s service=%s group=%s",
			ErrDiscoveryNoEndpoint,
			strings.TrimSpace(target.Provider),
			strings.TrimSpace(target.Namespace),
			strings.TrimSpace(target.Environment),
			normalizedServiceName,
			strings.TrimSpace(target.Group),
		)
	}
	return filtered, nil
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}
