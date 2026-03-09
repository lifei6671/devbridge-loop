GO ?= go
NPM ?= npm
CARGO ?= cargo

BIN_DIR := bin
AGENT_CORE_BIN := $(BIN_DIR)/agent-core
CLOUD_BRIDGE_BIN := $(BIN_DIR)/cloud-bridge

.PHONY: help test test-go build-agent-core build-cloud-bridge build-go build-dev-agent-ui build-dev-agent-tauri build-all clean

help:
	@echo "可用命令:"
	@echo "  make test                 # 运行 Go 单元测试"
	@echo "  make build-agent-core     # 编译 agent-core"
	@echo "  make build-cloud-bridge   # 编译 cloud-bridge"
	@echo "  make build-go             # 编译全部 Go 模块"
	@echo "  make build-dev-agent-ui   # 编译 React 前端"
	@echo "  make build-dev-agent-tauri# 编译 Tauri Rust Host（依赖系统库）"
	@echo "  make build-all            # 编译 Go 模块 + 前端"
	@echo "  make clean                # 清理构建产物"

test: test-go

test-go:
	$(GO) test ./agent-core/... ./cloud-bridge/...

build-agent-core:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(AGENT_CORE_BIN) ./agent-core/cmd/agent-core

build-cloud-bridge:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CLOUD_BRIDGE_BIN) ./cloud-bridge/cmd/cloud-bridge

build-go: build-agent-core build-cloud-bridge

build-dev-agent-ui:
	cd apps/dev-agent && $(NPM) run build

# 注意：在 Linux 环境编译 Tauri 需要 pkg-config/gtk/webkit 等系统依赖。
build-dev-agent-tauri:
	$(CARGO) build --manifest-path apps/dev-agent/src-tauri/Cargo.toml

build-all: build-go build-dev-agent-ui

clean:
	rm -rf $(BIN_DIR)
	rm -rf apps/dev-agent/dist
