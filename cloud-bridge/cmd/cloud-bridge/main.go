package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

const (
	envControlPlaneTLSMode                  = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_MODE"
	envControlPlaneTLSCertSource            = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CERT_SOURCE"
	envControlPlaneTLSCertFile              = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CERT_FILE"
	envControlPlaneTLSKeyFile               = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_KEY_FILE"
	envControlPlaneTLSCACertFile            = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CA_CERT_FILE"
	envControlPlaneTLSCAKeyFile             = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CA_KEY_FILE"
	envControlPlaneTLSServerCommonName      = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_COMMON_NAME"
	envControlPlaneTLSServerSANDNS          = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_SAN_DNS"
	envControlPlaneTLSServerSANIPs          = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_SAN_IPS"
	envControlPlaneTLSServerCertTTL         = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_CERT_TTL"
	envControlPlaneTLSServerCertRenewBefore = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_CERT_RENEW_BEFORE"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := loadRuntimeConfigFromFlags()
	if err != nil {
		log.Fatalf("load bridge config failed: %v", err)
	}
	runtime, err := app.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("bridge bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bridge runtime stopped: %v", err)
	}
}

// loadRuntimeConfigFromFlags 解析命令行参数，并委托 app 层按统一优先级加载运行配置。
func loadRuntimeConfigFromFlags() (app.Config, error) {
	configFilePathFlag := flag.String("config", "", "Bridge YAML 配置文件路径")
	flag.Parse()
	return app.LoadRuntimeConfig(strings.TrimSpace(*configFilePathFlag))
}

// applyRuntimeConfigEnvOverrides 将环境变量覆盖到运行配置，并再次执行结构化校验。
func applyRuntimeConfigEnvOverrides(runtimeConfig app.Config) (app.Config, error) {
	return app.ApplyRuntimeConfigEnvOverrides(runtimeConfig)
}

// applyControlPlaneTLSEnvOverrides 处理 control_plane TLS 相关环境变量覆盖（环境变量优先）。
func applyControlPlaneTLSEnvOverrides(runtimeConfig *app.Config) error {
	return app.ApplyControlPlaneTLSEnvOverrides(runtimeConfig)
}

// stringEnvOrDefault 读取字符串环境变量，空值时回退到默认值。
func stringEnvOrDefault(key string, defaultValue string) string {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue
	}
	return normalizedValue
}

// commaSeparatedEnvList 读取逗号分隔环境变量；返回值中的布尔表示该变量是否显式存在。
func commaSeparatedEnvList(key string) ([]string, bool) {
	rawValue, exists := os.LookupEnv(key)
	if !exists {
		return nil, false
	}
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return nil, true
	}
	rawParts := strings.Split(normalizedValue, ",")
	normalizedParts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		normalizedPart := strings.TrimSpace(rawPart)
		if normalizedPart == "" {
			continue
		}
		normalizedParts = append(normalizedParts, normalizedPart)
	}
	return normalizedParts, true
}

// durationEnvOrDefault 读取 duration 环境变量，空值时回退默认值。
func durationEnvOrDefault(key string, defaultValue time.Duration) (time.Duration, error) {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue, nil
	}
	parsedDuration, err := time.ParseDuration(normalizedValue)
	if err != nil {
		return 0, fmt.Errorf("parse %s failed: %w", key, err)
	}
	return parsedDuration, nil
}
