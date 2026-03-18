package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

const (
	defaultRuntimeConfigFileName = "bridge.yaml"

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

// loadRuntimeConfigFromFlags 解析命令行参数，并按需从 YAML 文件加载配置。
// 规则：
// 1) 显式传入 -config 时，按该路径加载并作为后续后台持久化目标；
// 2) 未传 -config 时，先使用默认配置，再尝试自动加载 ./bridge.yaml；
// 3) 若 ./bridge.yaml 不存在，保留默认配置并把该路径作为后台保存目标。
func loadRuntimeConfigFromFlags() (app.Config, error) {
	configFilePathFlag := flag.String("config", "", "Bridge YAML 配置文件路径")
	flag.Parse()

	normalizedConfigFilePath := strings.TrimSpace(*configFilePathFlag)
	if normalizedConfigFilePath != "" {
		runtimeConfig, err := app.LoadConfigFromYAMLFile(normalizedConfigFilePath)
		if err != nil {
			return app.Config{}, err
		}
		return applyRuntimeConfigEnvOverrides(runtimeConfig)
	}
	defaultConfigFilePath, err := filepath.Abs(defaultRuntimeConfigFileName)
	if err != nil {
		return app.Config{}, err
	}
	if _, statErr := os.Stat(defaultConfigFilePath); statErr == nil {
		runtimeConfig, loadErr := app.LoadConfigFromYAMLFile(defaultConfigFilePath)
		if loadErr != nil {
			return app.Config{}, loadErr
		}
		return applyRuntimeConfigEnvOverrides(runtimeConfig)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return app.Config{}, statErr
	}
	defaultConfig := app.DefaultConfig()
	defaultConfig.RuntimeConfigFilePath = defaultConfigFilePath
	return applyRuntimeConfigEnvOverrides(defaultConfig)
}

// applyRuntimeConfigEnvOverrides 将环境变量覆盖到运行配置，并再次执行结构化校验。
func applyRuntimeConfigEnvOverrides(runtimeConfig app.Config) (app.Config, error) {
	resolvedConfig := runtimeConfig
	if err := applyControlPlaneTLSEnvOverrides(&resolvedConfig); err != nil {
		return app.Config{}, err
	}
	// 环境变量覆盖后统一走 Validate，确保与 YAML 路径保持同一校验语义。
	if err := resolvedConfig.Validate(); err != nil {
		return app.Config{}, err
	}
	return resolvedConfig, nil
}

// applyControlPlaneTLSEnvOverrides 处理 control_plane TLS 相关环境变量覆盖（环境变量优先）。
func applyControlPlaneTLSEnvOverrides(runtimeConfig *app.Config) error {
	if runtimeConfig == nil {
		return errors.New("apply control plane tls env overrides: nil config")
	}
	runtimeConfig.ControlPlane.TLSMode = stringEnvOrDefault(envControlPlaneTLSMode, runtimeConfig.ControlPlane.TLSMode)
	runtimeConfig.ControlPlane.TLSCertSource = stringEnvOrDefault(
		envControlPlaneTLSCertSource,
		runtimeConfig.ControlPlane.TLSCertSource,
	)
	runtimeConfig.ControlPlane.TLSCertFile = stringEnvOrDefault(
		envControlPlaneTLSCertFile,
		runtimeConfig.ControlPlane.TLSCertFile,
	)
	runtimeConfig.ControlPlane.TLSKeyFile = stringEnvOrDefault(
		envControlPlaneTLSKeyFile,
		runtimeConfig.ControlPlane.TLSKeyFile,
	)
	runtimeConfig.ControlPlane.TLSCACertFile = stringEnvOrDefault(
		envControlPlaneTLSCACertFile,
		runtimeConfig.ControlPlane.TLSCACertFile,
	)
	runtimeConfig.ControlPlane.TLSCAKeyFile = stringEnvOrDefault(
		envControlPlaneTLSCAKeyFile,
		runtimeConfig.ControlPlane.TLSCAKeyFile,
	)
	runtimeConfig.ControlPlane.TLSServerCommonName = stringEnvOrDefault(
		envControlPlaneTLSServerCommonName,
		runtimeConfig.ControlPlane.TLSServerCommonName,
	)

	if sanDNSList, hasValue := commaSeparatedEnvList(envControlPlaneTLSServerSANDNS); hasValue {
		// 显式设置空字符串时清空列表，便于运维在覆盖层执行回滚。
		runtimeConfig.ControlPlane.TLSServerSANDNS = sanDNSList
	}
	if sanIPList, hasValue := commaSeparatedEnvList(envControlPlaneTLSServerSANIPs); hasValue {
		runtimeConfig.ControlPlane.TLSServerSANIPs = sanIPList
	}

	serverCertTTL, err := durationEnvOrDefault(
		envControlPlaneTLSServerCertTTL,
		runtimeConfig.ControlPlane.TLSServerCertTTL,
	)
	if err != nil {
		return err
	}
	runtimeConfig.ControlPlane.TLSServerCertTTL = serverCertTTL

	serverCertRenewBefore, err := durationEnvOrDefault(
		envControlPlaneTLSServerCertRenewBefore,
		runtimeConfig.ControlPlane.TLSServerCertRenewBefore,
	)
	if err != nil {
		return err
	}
	runtimeConfig.ControlPlane.TLSServerCertRenewBefore = serverCertRenewBefore
	return nil
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
