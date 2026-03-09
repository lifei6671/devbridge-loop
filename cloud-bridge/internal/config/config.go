package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 包含 cloud-bridge 运行配置。
type Config struct {
	HTTPAddr            string
	RouteExtractorOrder []string
	BridgePublicHost    string
	BridgePublicPort    int
	FallbackBackflowURL string
	IngressTimeout      time.Duration
}

// LoadFromEnv 按环境变量加载配置，并提供默认值。
func LoadFromEnv() Config {
	order := strings.Split(getenv("DEVLOOP_ROUTE_EXTRACT_ORDER", "host,header,sni"), ",")
	for i := range order {
		order[i] = strings.TrimSpace(order[i])
	}

	return Config{
		HTTPAddr:            getenv("DEVLOOP_BRIDGE_HTTP_ADDR", "0.0.0.0:38080"),
		RouteExtractorOrder: order,
		BridgePublicHost:    getenv("DEVLOOP_BRIDGE_PUBLIC_HOST", "bridge.example.internal"),
		BridgePublicPort:    getenvInt("DEVLOOP_BRIDGE_PUBLIC_PORT", 443),
		FallbackBackflowURL: getenv("DEVLOOP_BRIDGE_FALLBACK_BACKFLOW_URL", "http://127.0.0.1:39090"),
		IngressTimeout:      time.Duration(getenvInt("DEVLOOP_BRIDGE_INGRESS_TIMEOUT_SEC", 10)) * time.Second,
	}
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(getenv(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
