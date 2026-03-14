package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/app"
)

const (
	envAgentID                  = "DEV_AGENT_CFG_AGENT_ID"
	envBridgeAddr               = "DEV_AGENT_CFG_BRIDGE_ADDR"
	envBridgeTransport          = "DEV_AGENT_CFG_BRIDGE_TRANSPORT"
	envTunnelPoolMinIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MIN_IDLE"
	envTunnelPoolMaxIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_IDLE"
	envTunnelPoolMaxInflight    = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_INFLIGHT"
	envTunnelPoolTTLMS          = "DEV_AGENT_CFG_TUNNEL_POOL_TTL_MS"
	envTunnelPoolOpenRate       = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_RATE"
	envTunnelPoolOpenBurst      = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_BURST"
	envTunnelPoolReconcileGapMS = "DEV_AGENT_CFG_TUNNEL_POOL_RECONCILE_GAP_MS"
)

// main 负责启动 agent-runtime，并处理系统退出信号。
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := app.DefaultConfig()
	resolvedConfig, bootstrapOptions, err := loadRuntimeConfigFromEnv(cfg)
	if err != nil {
		log.Fatalf("load runtime config from env failed: %v", err)
	}

	// 通过 BootstrapWithOptions 应用 tunnel pool 覆盖，确保配置真实进入 runtime。
	runtime, err := app.BootstrapWithOptions(ctx, resolvedConfig, bootstrapOptions)
	if err != nil {
		log.Fatalf("agent bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent runtime stopped: %v", err)
	}
}

// loadRuntimeConfigFromEnv 从环境变量加载 agent-core 真实配置并做启动前校验。
func loadRuntimeConfigFromEnv(defaultConfig app.Config) (app.Config, app.BootstrapOptions, error) {
	resolvedConfig := defaultConfig
	resolvedConfig.AgentID = stringEnvOrDefault(envAgentID, defaultConfig.AgentID)
	resolvedConfig.BridgeAddr = stringEnvOrDefault(envBridgeAddr, defaultConfig.BridgeAddr)
	resolvedConfig.BridgeTransport = stringEnvOrDefault(
		envBridgeTransport,
		defaultConfig.BridgeTransport,
	)

	minIdle, err := intEnvOrDefault(envTunnelPoolMinIdle, defaultConfig.TunnelPool.MinIdle)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	maxIdle, err := intEnvOrDefault(envTunnelPoolMaxIdle, defaultConfig.TunnelPool.MaxIdle)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	maxInflight, err := intEnvOrDefault(envTunnelPoolMaxInflight, defaultConfig.TunnelPool.MaxInflight)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	ttlMS, err := int64EnvOrDefault(envTunnelPoolTTLMS, defaultConfig.TunnelPool.TTL.Milliseconds())
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	openRate, err := float64EnvOrDefault(envTunnelPoolOpenRate, defaultConfig.TunnelPool.OpenRate)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	openBurst, err := intEnvOrDefault(envTunnelPoolOpenBurst, defaultConfig.TunnelPool.OpenBurst)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	reconcileGapMS, err := int64EnvOrDefault(
		envTunnelPoolReconcileGapMS,
		defaultConfig.TunnelPool.ReconcileGap.Milliseconds(),
	)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}

	// 先做一轮显式校验，尽量把错误信息定位到具体字段。
	if strings.TrimSpace(resolvedConfig.AgentID) == "" {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 不能为空", envAgentID)
	}
	if strings.TrimSpace(resolvedConfig.BridgeAddr) == "" {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 不能为空", envBridgeAddr)
	}
	if strings.TrimSpace(resolvedConfig.BridgeTransport) == "" {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 不能为空", envBridgeTransport)
	}
	if minIdle < 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须大于等于 0", envTunnelPoolMinIdle)
	}
	if maxIdle <= 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须大于 0", envTunnelPoolMaxIdle)
	}
	if minIdle > maxIdle {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 不能大于 %s", envTunnelPoolMinIdle, envTunnelPoolMaxIdle)
	}
	if maxInflight <= 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须大于 0", envTunnelPoolMaxInflight)
	}
	if ttlMS < 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 不能小于 0", envTunnelPoolTTLMS)
	}
	if math.IsNaN(openRate) || math.IsInf(openRate, 0) || openRate <= 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须是有限且大于 0 的数值", envTunnelPoolOpenRate)
	}
	if openBurst <= 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须大于 0", envTunnelPoolOpenBurst)
	}
	if reconcileGapMS <= 0 {
		return app.Config{}, app.BootstrapOptions{}, fmt.Errorf("%s 必须大于 0", envTunnelPoolReconcileGapMS)
	}

	tunnelPoolTTL := time.Duration(ttlMS) * time.Millisecond
	tunnelPoolReconcileGap := time.Duration(reconcileGapMS) * time.Millisecond
	// 通过 override 结构注入 tunnel pool，保持与设计方案一致。
	bootstrapOptions := app.BootstrapOptions{
		TunnelPoolOverride: &app.TunnelPoolOverride{
			MinIdle:      &minIdle,
			MaxIdle:      &maxIdle,
			MaxInflight:  &maxInflight,
			TTL:          &tunnelPoolTTL,
			OpenRate:     &openRate,
			OpenBurst:    &openBurst,
			ReconcileGap: &tunnelPoolReconcileGap,
		},
	}

	return resolvedConfig, bootstrapOptions, nil
}

// stringEnvOrDefault 读取字符串环境变量，空值时回退默认值。
func stringEnvOrDefault(key string, defaultValue string) string {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue
	}
	return normalizedValue
}

// intEnvOrDefault 读取 int 环境变量，空值时回退默认值。
func intEnvOrDefault(key string, defaultValue int) (int, error) {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue, nil
	}
	parsedValue, err := strconv.Atoi(normalizedValue)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, nil
}

// int64EnvOrDefault 读取 int64 环境变量，空值时回退默认值。
func int64EnvOrDefault(key string, defaultValue int64) (int64, error) {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue, nil
	}
	parsedValue, err := strconv.ParseInt(normalizedValue, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, nil
}

// float64EnvOrDefault 读取 float64 环境变量，空值时回退默认值。
func float64EnvOrDefault(key string, defaultValue float64) (float64, error) {
	rawValue := os.Getenv(key)
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return defaultValue, nil
	}
	parsedValue, err := strconv.ParseFloat(normalizedValue, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, nil
}
