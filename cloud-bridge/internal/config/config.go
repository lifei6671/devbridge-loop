package config

import (
	"os"
	"strings"
)

// Config contains cloud-bridge runtime settings.
type Config struct {
	HTTPAddr            string
	RouteExtractorOrder []string
}

// LoadFromEnv loads configuration with defaults matching phase-one constraints.
func LoadFromEnv() Config {
	order := strings.Split(getenv("DEVLOOP_ROUTE_EXTRACT_ORDER", "host,header,sni"), ",")
	for i := range order {
		order[i] = strings.TrimSpace(order[i])
	}
	return Config{
		HTTPAddr:            getenv("DEVLOOP_BRIDGE_HTTP_ADDR", "0.0.0.0:18080"),
		RouteExtractorOrder: order,
	}
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
