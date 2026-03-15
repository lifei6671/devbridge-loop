package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
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
func loadRuntimeConfigFromFlags() (app.Config, error) {
	configFilePathFlag := flag.String("config", "", "Bridge YAML 配置文件路径")
	flag.Parse()

	normalizedConfigFilePath := strings.TrimSpace(*configFilePathFlag)
	if normalizedConfigFilePath == "" {
		// 未显式指定配置文件时，回退默认配置，保证历史启动方式兼容。
		return app.DefaultConfig(), nil
	}
	return app.LoadConfigFromYAMLFile(normalizedConfigFilePath)
}
