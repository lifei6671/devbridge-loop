package registry

import (
	"fmt"
	"strings"
)

// NewAdapter 根据业务配置创建 registry 适配器。
func NewAdapter(options Options) (Registry, error) {
	switch normalizeRegistryType(options.Type) {
	case RegistryTypeAgent:
		return NewAgentAdapter(AgentOptions{
			AgentAddr:  options.AgentAddr,
			RuntimeEnv: options.RuntimeEnv,
			HTTPClient: options.HTTPClient,
			WatchEvery: options.WatchEvery,
		})
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", strings.TrimSpace(string(options.Type)))
	}
}

func normalizeRegistryType(value RegistryType) RegistryType {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case string(RegistryTypeAgent):
		return RegistryTypeAgent
	case string(RegistryTypeNacos):
		return RegistryTypeNacos
	case string(RegistryTypeLocal):
		return RegistryTypeLocal
	default:
		return RegistryType(strings.TrimSpace(string(value)))
	}
}
