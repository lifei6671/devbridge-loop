package hostderiver

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Deriver 根据 service_name + scope 派生共享入口 Host。
type Deriver struct {
	baseDomain string
	metrics    *obs.Metrics
}

// New 创建 Host 派生器。
func New(baseDomain string, metrics *obs.Metrics) *Deriver {
	return &Deriver{
		baseDomain: strings.TrimSpace(baseDomain),
		metrics:    metrics,
	}
}

// Derive 按 `{service}.{environment}.{namespace}.{base_domain}` 模板生成 Host。
func (deriver *Deriver) Derive(serviceName string, scope pb.Scope) (string, error) {
	if deriver == nil {
		return "", fmt.Errorf("derive host: nil deriver")
	}
	normalizedServiceName := sanitizeHostLabel(serviceName)
	normalizedNamespace := sanitizeHostLabel(scope.Namespace)
	normalizedEnvironment := sanitizeHostLabel(scope.Environment)
	normalizedBaseDomain, err := normalizeBaseDomain(deriver.baseDomain)
	if err != nil {
		deriver.observe(false)
		return "", err
	}
	if normalizedServiceName == "" {
		deriver.observe(false)
		return "", fmt.Errorf("derive host: empty service_name")
	}
	if normalizedNamespace == "" || normalizedEnvironment == "" {
		deriver.observe(false)
		return "", fmt.Errorf("derive host: empty scope label")
	}
	host := strings.Join(
		[]string{normalizedServiceName, normalizedEnvironment, normalizedNamespace, normalizedBaseDomain},
		".",
	)
	deriver.observe(true)
	return host, nil
}

func (deriver *Deriver) observe(success bool) {
	if deriver == nil || deriver.metrics == nil {
		return
	}
	deriver.metrics.ObserveBridgeHostDerive(success)
}

func normalizeBaseDomain(baseDomain string) (string, error) {
	normalizedBaseDomain := strings.Trim(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if normalizedBaseDomain == "" {
		return "", fmt.Errorf("derive host: empty ingress.base_domain")
	}
	labels := strings.Split(normalizedBaseDomain, ".")
	normalizedLabels := make([]string, 0, len(labels))
	for _, label := range labels {
		normalizedLabel := sanitizeHostLabel(label)
		if normalizedLabel == "" {
			return "", fmt.Errorf("derive host: invalid ingress.base_domain=%s", baseDomain)
		}
		normalizedLabels = append(normalizedLabels, normalizedLabel)
	}
	return strings.Join(normalizedLabels, "."), nil
}

func sanitizeHostLabel(rawLabel string) string {
	normalizedLabel := strings.ToLower(strings.TrimSpace(rawLabel))
	if normalizedLabel == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(normalizedLabel))
	previousHyphen := false
	for _, character := range normalizedLabel {
		switch {
		case character >= 'a' && character <= 'z':
			builder.WriteRune(character)
			previousHyphen = false
		case character >= '0' && character <= '9':
			builder.WriteRune(character)
			previousHyphen = false
		default:
			if previousHyphen {
				continue
			}
			builder.WriteByte('-')
			previousHyphen = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
