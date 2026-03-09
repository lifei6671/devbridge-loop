package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// RouteExtractResult stores extracted route identity.
type RouteExtractResult struct {
	ServiceName string `json:"serviceName"`
	Env         string `json:"env"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
}

// RouteExtractor parses route metadata from an ingress request.
type RouteExtractor interface {
	Name() string
	Extract(ctx context.Context, r *http.Request) (RouteExtractResult, bool)
}

// Pipeline applies extractors in configured priority.
type Pipeline struct {
	extractors []RouteExtractor
}

// NewPipeline builds extraction pipeline by order.
func NewPipeline(order []string) *Pipeline {
	registry := map[string]RouteExtractor{
		"host":   HostExtractor{},
		"header": HeaderExtractor{},
		"sni":    SNIExtractor{},
	}
	result := make([]RouteExtractor, 0, len(order))
	for _, name := range order {
		extractor, ok := registry[strings.ToLower(strings.TrimSpace(name))]
		if ok {
			result = append(result, extractor)
		}
	}
	if len(result) == 0 {
		result = append(result, HostExtractor{}, HeaderExtractor{}, SNIExtractor{})
	}
	return &Pipeline{extractors: result}
}

// Resolve extracts route identity from request.
func (p *Pipeline) Resolve(ctx context.Context, r *http.Request) (RouteExtractResult, error) {
	for _, extractor := range p.extractors {
		if result, ok := extractor.Extract(ctx, r); ok {
			return result, nil
		}
	}
	return RouteExtractResult{}, errors.New("route extract failed")
}

// HostExtractor extracts service/env from host, e.g. user.dev-alice.internal.
type HostExtractor struct{}

func (HostExtractor) Name() string { return "host" }

func (HostExtractor) Extract(_ context.Context, r *http.Request) (RouteExtractResult, bool) {
	host := r.Host
	host = strings.Split(host, ":")[0]
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return RouteExtractResult{}, false
	}
	return RouteExtractResult{
		ServiceName: parts[0],
		Env:         parts[1],
		Protocol:    protocolFromRequest(r),
		Source:      "host",
	}, true
}

// HeaderExtractor extracts service/env from headers.
type HeaderExtractor struct{}

func (HeaderExtractor) Name() string { return "header" }

func (HeaderExtractor) Extract(_ context.Context, r *http.Request) (RouteExtractResult, bool) {
	service := strings.TrimSpace(r.Header.Get("x-service-name"))
	env := strings.TrimSpace(r.Header.Get("x-env"))
	if env == "" {
		env = strings.TrimSpace(r.Header.Get("X-Env"))
	}
	if service == "" || env == "" {
		return RouteExtractResult{}, false
	}
	return RouteExtractResult{
		ServiceName: service,
		Env:         env,
		Protocol:    protocolFromRequest(r),
		Source:      "header",
	}, true
}

// SNIExtractor is a placeholder for TLS SNI extraction in phase one scaffold.
type SNIExtractor struct{}

func (SNIExtractor) Name() string { return "sni" }

func (SNIExtractor) Extract(_ context.Context, r *http.Request) (RouteExtractResult, bool) {
	sni := strings.TrimSpace(r.Header.Get("x-sni-host"))
	if sni == "" {
		return RouteExtractResult{}, false
	}
	host := strings.Split(strings.Split(sni, ":")[0], ".")
	if len(host) < 2 {
		return RouteExtractResult{}, false
	}
	return RouteExtractResult{
		ServiceName: host[0],
		Env:         host[1],
		Protocol:    protocolFromRequest(r),
		Source:      "sni",
	}, true
}

func protocolFromRequest(r *http.Request) string {
	if strings.EqualFold(r.Header.Get("content-type"), "application/grpc") || strings.Contains(strings.ToLower(r.Header.Get("content-type")), "grpc") {
		return "grpc"
	}
	return "http"
}

// DebugString prints extractor order.
func (p *Pipeline) DebugString() string {
	names := make([]string, 0, len(p.extractors))
	for _, e := range p.extractors {
		names = append(names, e.Name())
	}
	return fmt.Sprintf("route extract order: %s", strings.Join(names, " -> "))
}
