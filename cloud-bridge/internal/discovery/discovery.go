package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Query 描述一次服务发现查询参数。
type Query struct {
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
}

// Normalize 将查询参数标准化，避免大小写或空白导致误判。
func (q Query) Normalize() Query {
	return Query{
		Env:         normalizeToken(q.Env),
		ServiceName: normalizeToken(q.ServiceName),
		Protocol:    normalizeProtocol(q.Protocol),
	}
}

// Validate 校验查询参数是否完整。
func (q Query) Validate() error {
	normalized := q.Normalize()
	if normalized.Env == "" {
		return errors.New("env is empty")
	}
	if normalized.ServiceName == "" {
		return errors.New("serviceName is empty")
	}
	if normalized.Protocol == "" {
		return errors.New("protocol is empty")
	}
	return nil
}

// Endpoint 表示服务发现命中的目标地址。
type Endpoint struct {
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Source   string            `json:"source,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate 校验发现结果是否可用于转发。
func (e Endpoint) Validate() error {
	if strings.TrimSpace(e.Host) == "" {
		return errors.New("endpoint host is empty")
	}
	if e.Port <= 0 {
		return errors.New("endpoint port must be positive")
	}
	return nil
}

// Target 返回 host:port 形式的目标地址。
func (e Endpoint) Target() string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(e.Host), e.Port)
}

// Resolver 定义可插拔服务发现能力。
type Resolver interface {
	Name() string
	Resolve(ctx context.Context, query Query) (Endpoint, bool, error)
}

// NoopResolver 代表关闭发现能力时的空实现。
type NoopResolver struct{}

// Name 返回 resolver 名称。
func (NoopResolver) Name() string { return "noop" }

// Resolve 空实现永远返回未命中。
func (NoopResolver) Resolve(_ context.Context, _ Query) (Endpoint, bool, error) {
	return Endpoint{}, false, nil
}

// Chain 将多个 resolver 按顺序组合为一个发现链。
type Chain struct {
	resolvers []Resolver
}

// NewChain 创建按优先级依次执行的服务发现链。
func NewChain(resolvers ...Resolver) *Chain {
	trimmed := make([]Resolver, 0, len(resolvers))
	for _, resolver := range resolvers {
		if resolver == nil {
			continue
		}
		trimmed = append(trimmed, resolver)
	}
	return &Chain{resolvers: trimmed}
}

// Name 返回链式发现器标识。
func (c *Chain) Name() string { return "chain" }

// Resolve 按顺序调用 resolver，命中即返回；若某个 resolver 失败会继续后续并聚合错误。
func (c *Chain) Resolve(ctx context.Context, query Query) (Endpoint, bool, error) {
	if c == nil || len(c.resolvers) == 0 {
		return Endpoint{}, false, nil
	}
	if err := query.Validate(); err != nil {
		return Endpoint{}, false, fmt.Errorf("invalid discovery query: %w", err)
	}

	normalizedQuery := query.Normalize()
	var joinedErr error
	for _, resolver := range c.resolvers {
		endpoint, matched, err := resolver.Resolve(ctx, normalizedQuery)
		if err != nil {
			joinedErr = errors.Join(joinedErr, fmt.Errorf("%s resolve failed: %w", resolver.Name(), err))
			continue
		}
		if !matched {
			continue
		}
		if err := endpoint.Validate(); err != nil {
			joinedErr = errors.Join(joinedErr, fmt.Errorf("%s resolve invalid endpoint: %w", resolver.Name(), err))
			continue
		}
		if strings.TrimSpace(endpoint.Source) == "" {
			endpoint.Source = resolver.Name()
		}
		return endpoint, true, nil
	}
	return Endpoint{}, false, joinedErr
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeProtocol(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grpc":
		return "grpc"
	case "http":
		return "http"
	default:
		return "http"
	}
}

func copyMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func renderServiceName(pattern string, query Query) string {
	normalizedPattern := strings.TrimSpace(pattern)
	if normalizedPattern == "" {
		normalizedPattern = "${service}"
	}
	replacer := strings.NewReplacer(
		"${env}", query.Env,
		"${service}", query.ServiceName,
		"${protocol}", query.Protocol,
	)
	return strings.TrimSpace(replacer.Replace(normalizedPattern))
}
