# DevLoop 一期脚手架快速说明

本文档说明当前仓库已落地的 P1/P2 技术框架骨架（`Tauri v2 + React + shadcn/ui + Go 1.26`）。

## 目录结构

```text
.
├── agent-core/                 # Go Agent Core (本地管理面 + 状态面)
│   ├── cmd/agent-core/
│   └── internal/
├── cloud-bridge/               # Go cloud-bridge 骨架
│   ├── cmd/cloud-bridge/
│   └── internal/
├── apps/
│   └── dev-agent/
│       ├── src/                # React + shadcn/ui
│       └── src-tauri/          # Tauri v2 Rust Host
└── go.work                     # 多模块工作区
```

## 版本约束

- Go：`1.26.0`（`go.work`、`agent-core/go.mod`、`cloud-bridge/go.mod` 已固定）
- Tauri：`v2`
- UI：`React + TypeScript + shadcn/ui`

## 当前已实现的骨架能力

1. `agent-core`
- 本地管理面 API：
  - `POST /api/v1/registrations`
  - `POST /api/v1/registrations/{instanceId}/heartbeat`
  - `DELETE /api/v1/registrations/{instanceId}`
  - `GET /api/v1/registrations`
  - `POST /api/v1/discover`
- 状态面 API：
  - `GET /api/v1/state/summary`
  - `GET /api/v1/state/tunnel`
  - `GET /api/v1/state/intercepts`
  - `GET /api/v1/state/errors`
  - `POST /api/v1/control/reconnect`
- 内存态注册表 + TTL 过期摘除扫描
- 协议扩展接口占位：`ProtocolAdapter` / `IngressResolver` / `Forwarder` / `EgressProxy`

2. `cloud-bridge`
- 状态 API：`sessions/intercepts/routes`
- tunnel 事件入口占位：`POST /api/v1/tunnel/events`
- 路由提取抽象：`RouteExtractor`、`RouteExtractPipeline`
- 内置提取器：`host/header/sni`

3. `dev-agent` 桌面端
- Rust Host：
  - 启动时拉起 Go Agent Core 子进程（可通过环境变量覆盖）
  - 调用 Go 本地 HTTP API
  - 暴露 Tauri commands 给前端
- React Dashboard：
  - Agent/Bridge/Tunnel 状态卡片
  - 本地注册列表
  - 最近错误列表
  - 刷新 + 手动重连

## 本地开发命令

### Go 侧

```bash
# 在仓库根目录
go test ./agent-core/... ./cloud-bridge/...

# 启动 agent-core
cd agent-core && go run ./cmd/agent-core

# 启动 cloud-bridge
cd cloud-bridge && go run ./cmd/cloud-bridge
```

### Tauri + React 侧

```bash
cd apps/dev-agent
npm install
npm run build
npm run tauri:dev
```

> 在 Linux 环境编译 Tauri 需要系统依赖（如 `pkg-config`、`glib2.0`、`gtk3`、`webkit2gtk`）。
> 目标运行环境是 Win11 时，请在 Win11 开发机安装对应 Tauri 构建依赖后运行。

## 环境变量（已支持）

- `DEVLOOP_AGENT_HTTP_ADDR`：Go Agent Core HTTP 地址，默认 `127.0.0.1:19090`
- `DEVLOOP_RD_NAME`：当前研发人员标识
- `DEVLOOP_ENV_NAME`：当前默认 env
- `DEVLOOP_DEFAULT_TTL_SECONDS`：注册默认 TTL
- `DEVLOOP_SCAN_INTERVAL_SECONDS`：TTL 扫描周期（秒）
- `DEVLOOP_AGENT_API_BASE`：Rust Host 调用 agent-core 的 base URL
- `DEVLOOP_AGENT_BINARY`：Rust Host 直接拉起的 agent-core 二进制路径
- `DEVLOOP_AGENT_CORE_DIR`：Rust Host 用 `go run` 拉起 agent-core 时的工作目录
