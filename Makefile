GO ?= go
NPM ?= npm
CARGO ?= cargo

BIN_DIR := bin
AGENT_CORE_BIN := $(BIN_DIR)/agent-core
CLOUD_BRIDGE_BIN := $(BIN_DIR)/cloud-bridge
TAURI_EXTRA_ARGS ?=

.PHONY: help test test-go build-agent-core build-cloud-bridge build-go build-dev-agent-ui build-dev-agent-tauri build-dev-agent-tauri-cross build-all clean

help:
	@echo "可用命令:"
	@echo "  make test                 # 运行 Go 单元测试"
	@echo "  make build-agent-core     # 编译 agent-core"
	@echo "  make build-cloud-bridge   # 编译 cloud-bridge"
	@echo "  make build-go             # 编译全部 Go 模块"
	@echo "  make build-dev-agent-ui   # 编译 React 前端"
	@echo "  make build-dev-agent-tauri# 编译 Tauri Rust Host（依赖系统库）"
	@echo "  make build-dev-agent-tauri-cross TAURI_TARGET=<triple> [TAURI_EXTRA_ARGS='--no-bundle']"
	@echo "  make build-all            # 编译 Go 模块 + 前端"
	@echo "  make clean                # 清理构建产物"

test: test-go

test-go:
	cd agent-core && $(GO) test ./...
	cd cloud-bridge && $(GO) test ./...

build-agent-core:
	@mkdir -p $(BIN_DIR)
	cd agent-core && $(GO) build -o ../$(AGENT_CORE_BIN) ./cmd/agent-core

build-cloud-bridge:
	@mkdir -p $(BIN_DIR)
	cd cloud-bridge && $(GO) build -o ../$(CLOUD_BRIDGE_BIN) ./cmd/cloud-bridge

build-go: build-agent-core build-cloud-bridge

build-dev-agent-ui:
	cd apps/dev-agent && $(NPM) run build

# 注意：在 Linux 环境编译 Tauri 需要 pkg-config/gtk/webkit 等系统依赖。
build-dev-agent-tauri:
	$(CARGO) build --manifest-path apps/dev-agent/src-tauri/Cargo.toml

# 跨平台构建示例：
# make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-msvc
# make build-dev-agent-tauri-cross TAURI_TARGET=aarch64-apple-darwin TAURI_EXTRA_ARGS="--no-bundle"
build-dev-agent-tauri-cross:
	@if [ -z "$(TAURI_TARGET)" ]; then \
		echo "缺少 TAURI_TARGET。示例：make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-msvc"; \
		exit 1; \
	fi
	@if [ "$$(uname -s)" != "MINGW64_NT" ] && [ "$$(uname -s)" != "MSYS_NT" ] && [ "$$(uname -s)" != "CYGWIN_NT" ] && [ "$(findstring windows-msvc,$(TAURI_TARGET))" != "" ]; then \
		echo "当前宿主不是 Windows/MSVC 环境，$(TAURI_TARGET) 需要 Visual Studio 工具链（lib.exe/llvm-rc）。"; \
		echo "建议改用：make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-gnu TAURI_EXTRA_ARGS='--no-bundle'"; \
		exit 1; \
	fi
	cd apps/dev-agent && $(NPM) run tauri:build -- --target $(TAURI_TARGET) $(TAURI_EXTRA_ARGS)

build-all: build-go build-dev-agent-ui

clean:
	rm -rf $(BIN_DIR)
	rm -rf apps/dev-agent/dist
