GO ?= go
NPM ?= npm
CARGO ?= cargo

BIN_DIR := bin
AGENT_CORE_BIN := $(BIN_DIR)/agent-core
CLOUD_BRIDGE_BIN := $(BIN_DIR)/cloud-bridge
CLOUD_BRIDGE_WEB_DIR := cloud-bridge/web
DEMO_ORDER_BIN := $(BIN_DIR)/order-service
DEMO_USER_BIN := $(BIN_DIR)/user-service
DEMO_INVENTORY_BIN := $(BIN_DIR)/inventory-service
WIN11_BIN_DIR := $(BIN_DIR)/win11
WIN_GOOS ?= windows
WIN_GOARCH ?= amd64
WIN_CGO_ENABLED ?= 0
WIN11_TAURI_TARGET ?= x86_64-pc-windows-gnu
WIN11_TAURI_ARGS ?= --no-bundle
TAURI_EXTRA_ARGS ?= --no-bundle
UNAME_S := $(shell uname -s 2>/dev/null || echo unknown)
IS_WINDOWS_HOST := $(if $(filter MINGW% MSYS% CYGWIN%,$(UNAME_S)),1,0)

.PHONY: help test test-go build-agent-core build-cloud-bridge-ui build-cloud-bridge build-go build-dev-agent-ui build-dev-agent-tauri build-dev-agent-tauri-cross build-demo-order build-demo-user build-demo-inventory build-demos run-demo-order run-demo-user run-demo-inventory build-win11 build-win11-go build-win11-dev-agent build-all clean

help:
	@echo "可用命令:"
	@echo "  make test                 # 运行 Go 单元测试"
	@echo "  make build-agent-core     # 编译 agent-core"
	@echo "  make build-cloud-bridge-ui# 构建 Bridge 管理页面静态资源"
	@echo "  make build-cloud-bridge   # 编译 cloud-bridge"
	@echo "  make build-go             # 编译全部 Go 模块"
	@echo "  make build-demos          # 编译 demo 服务（order/user/inventory）"
	@echo "  make build-win11          # 在 WSL 一键构建 Win11 可执行（含 Go 模块 + dev-agent + 前端依赖）"
	@echo "  make build-dev-agent-ui   # 编译 React 前端"
	@echo "  make build-dev-agent-tauri# 编译 Tauri Rust Host（依赖系统库）"
	@echo "  make build-dev-agent-tauri-cross TAURI_TARGET=<triple> [TAURI_EXTRA_ARGS='--no-bundle']"
	@echo "  make run-demo-order       # 启动 order-service demo"
	@echo "  make run-demo-user        # 启动 user-service demo"
	@echo "  make run-demo-inventory   # 启动 inventory-service demo"
	@echo "  make build-all            # 编译 Go 模块 + 前端"
	@echo "  make clean                # 清理构建产物"

test: test-go

# 运行 Go 测试前先构建 Bridge UI，避免 go:embed 因缺失 dist 目录编译失败。
test-go: build-cloud-bridge-ui
	cd agent-core && $(GO) test ./...
	cd cloud-bridge && $(GO) test ./...

build-agent-core:
	@mkdir -p $(BIN_DIR)
	cd agent-core && $(GO) build -o ../$(AGENT_CORE_BIN) ./cmd/agent-core

# 构建 Bridge Admin 前端静态资源（用于 go:embed 打包）。
build-cloud-bridge-ui:
	@if ! command -v $(NPM) >/dev/null 2>&1; then \
		echo "未检测到 npm，请先安装 Node.js/npm。"; \
		exit 1; \
	fi
	cd $(CLOUD_BRIDGE_WEB_DIR) && $(NPM) install
	cd $(CLOUD_BRIDGE_WEB_DIR) && $(NPM) run build

build-cloud-bridge: build-cloud-bridge-ui
	@mkdir -p $(BIN_DIR)
	cd cloud-bridge && $(GO) build -o ../$(CLOUD_BRIDGE_BIN) ./cmd/cloud-bridge

build-go: build-agent-core build-cloud-bridge

build-demo-order:
	@mkdir -p $(BIN_DIR)
	cd examples && $(GO) build -o ../$(DEMO_ORDER_BIN) ./cmd/order-service

build-demo-user:
	@mkdir -p $(BIN_DIR)
	cd examples && $(GO) build -o ../$(DEMO_USER_BIN) ./cmd/user-service

build-demo-inventory:
	@mkdir -p $(BIN_DIR)
	cd examples && $(GO) build -o ../$(DEMO_INVENTORY_BIN) ./cmd/inventory-service

build-demos: build-demo-order build-demo-user build-demo-inventory

run-demo-order:
	cd examples && $(GO) run ./cmd/order-service

run-demo-user:
	cd examples && $(GO) run ./cmd/user-service

run-demo-inventory:
	cd examples && $(GO) run ./cmd/inventory-service

# 在 WSL 下交叉编译 Win11 可执行文件（Go 模块 + demo 模块）。
# 默认生成 x86_64 Windows 可执行（.exe），可通过 WIN_GOARCH 覆盖为 arm64。
build-win11-go:
	@mkdir -p $(WIN11_BIN_DIR)
	@echo ">> build windows binaries: GOOS=$(WIN_GOOS) GOARCH=$(WIN_GOARCH) CGO_ENABLED=$(WIN_CGO_ENABLED)"
	cd agent-core && CGO_ENABLED=$(WIN_CGO_ENABLED) GOOS=$(WIN_GOOS) GOARCH=$(WIN_GOARCH) $(GO) build -trimpath -ldflags="-s -w" -o ../$(WIN11_BIN_DIR)/agent-core.exe ./cmd/agent-core
	cd cloud-bridge && CGO_ENABLED=$(WIN_CGO_ENABLED) GOOS=$(WIN_GOOS) GOARCH=$(WIN_GOARCH) $(GO) build -trimpath -ldflags="-s -w" -o ../$(WIN11_BIN_DIR)/cloud-bridge.exe ./cmd/cloud-bridge

# 在 WSL 下构建 Windows 版 dev-agent（包含前端依赖安装和前端打包）。
# 默认使用 windows-gnu 目标，避免 WSL 下依赖 msvc 工具链。
build-win11-dev-agent:
	@mkdir -p $(WIN11_BIN_DIR)
	@echo ">> build dev-agent for Win11: target=$(WIN11_TAURI_TARGET) args=$(WIN11_TAURI_ARGS)"
	@if [ "$(findstring windows-msvc,$(WIN11_TAURI_TARGET))" != "" ]; then \
		echo "当前是 WSL/Linux 宿主，WIN11_TAURI_TARGET 不建议使用 msvc：$(WIN11_TAURI_TARGET)"; \
		echo "请改用 windows-gnu（例如 x86_64-pc-windows-gnu）。"; \
		exit 1; \
	fi
	@if ! command -v rustup >/dev/null 2>&1; then \
		echo "未检测到 rustup，请先安装 Rust 工具链。"; \
		exit 1; \
	fi
	@if ! rustup target list --installed | grep -q "^$(WIN11_TAURI_TARGET)$$"; then \
		echo ">> install rust target: $(WIN11_TAURI_TARGET)"; \
		rustup target add $(WIN11_TAURI_TARGET); \
	fi
	@if [ "$(findstring x86_64-pc-windows-gnu,$(WIN11_TAURI_TARGET))" != "" ] && ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then \
		echo "缺少 mingw 工具链：x86_64-w64-mingw32-gcc"; \
		echo "请执行：sudo apt-get install -y gcc-mingw-w64-x86-64"; \
		exit 1; \
	fi
	cd apps/dev-agent && $(NPM) install
	cd apps/dev-agent && $(NPM) run tauri:build -- --target $(WIN11_TAURI_TARGET) $(WIN11_TAURI_ARGS)
	@if [ -f apps/dev-agent/src-tauri/target/$(WIN11_TAURI_TARGET)/release/dev-agent.exe ]; then \
		cp apps/dev-agent/src-tauri/target/$(WIN11_TAURI_TARGET)/release/dev-agent.exe $(WIN11_BIN_DIR)/dev-agent.exe; \
		echo ">> copy dev-agent.exe => $(WIN11_BIN_DIR)/dev-agent.exe"; \
		if [ -f apps/dev-agent/src-tauri/target/$(WIN11_TAURI_TARGET)/release/WebView2Loader.dll ]; then \
			cp apps/dev-agent/src-tauri/target/$(WIN11_TAURI_TARGET)/release/WebView2Loader.dll $(WIN11_BIN_DIR)/WebView2Loader.dll; \
			echo ">> copy WebView2Loader.dll => $(WIN11_BIN_DIR)/WebView2Loader.dll"; \
		fi; \
	else \
		echo "未找到 dev-agent.exe，请检查 Tauri 产物目录。"; \
		exit 1; \
	fi

# 在 WSL 下一键构建 Win11 可执行文件（Go 模块 + dev-agent + 前端依赖）。
build-win11: build-win11-go build-win11-dev-agent

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
	@if ! command -v $(NPM) >/dev/null 2>&1; then \
		echo "未检测到 npm，请先安装 Node.js/npm。"; \
		exit 1; \
	fi
	@if ! command -v rustup >/dev/null 2>&1; then \
		echo "未检测到 rustup，请先安装 Rust 工具链。"; \
		exit 1; \
	fi
	@if [ "$(IS_WINDOWS_HOST)" != "1" ] && [ "$(findstring windows-msvc,$(TAURI_TARGET))" != "" ]; then \
		echo "当前宿主不是 Windows/MSVC 环境，$(TAURI_TARGET) 需要 Visual Studio 工具链（lib.exe/llvm-rc）。"; \
		echo "建议改用：make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-gnu TAURI_EXTRA_ARGS='--no-bundle'"; \
		exit 1; \
	fi
	@if ! rustup target list --installed | grep -q "^$(TAURI_TARGET)$$"; then \
		echo ">> install rust target: $(TAURI_TARGET)"; \
		rustup target add $(TAURI_TARGET); \
	fi
	@if [ "$(findstring x86_64-pc-windows-gnu,$(TAURI_TARGET))" != "" ] && ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then \
		echo "缺少 mingw 工具链：x86_64-w64-mingw32-gcc"; \
		echo "请执行：sudo apt-get install -y gcc-mingw-w64-x86-64"; \
		exit 1; \
	fi
	@if [ "$(findstring aarch64-pc-windows-gnu,$(TAURI_TARGET))" != "" ] && ! command -v aarch64-w64-mingw32-gcc >/dev/null 2>&1; then \
		echo "缺少 mingw 工具链：aarch64-w64-mingw32-gcc"; \
		echo "请执行：sudo apt-get install -y gcc-mingw-w64-aarch64"; \
		exit 1; \
	fi
	cd apps/dev-agent && $(NPM) install
	cd apps/dev-agent && $(NPM) run tauri:build -- --target $(TAURI_TARGET) $(TAURI_EXTRA_ARGS)

build-all: build-go build-demos build-dev-agent-ui

clean:
	rm -rf $(BIN_DIR)
	rm -rf apps/dev-agent/dist
