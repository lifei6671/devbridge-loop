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
- 注册键模型索引：`ServiceKey / InstanceKey / EndpointKey`
- 事件幂等骨架：`x-event-id` 去重（重复 `register/unregister` 返回成功语义，不重复增加 `resourceVersion`）
- 协议扩展接口占位：`ProtocolAdapter` / `IngressResolver` / `Forwarder` / `EgressProxy`

2. `cloud-bridge`
- 状态 API：`sessions/intercepts/routes`
- tunnel 事件入口占位：`POST /api/v1/tunnel/events`
- 路由提取抽象：`RouteExtractor`、`RouteExtractPipeline`
- 内置提取器：`host/header/sni`

3. `dev-agent` 桌面端
- Rust Host：
  - 启动时拉起 Go Agent Core 子进程（可通过环境变量覆盖）
  - 后台监督循环（2s）：进程退出检测 + 自动重启 + 退避重试
  - 向前端推送 `agent-runtime-changed` 事件，实现运行态实时刷新
  - 调用 Go 本地 HTTP API
  - 暴露 Tauri commands 给前端（含 `restart_agent_process` 手动重启）
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
- `DEVLOOP_AGENT_AUTO_RESTART`：是否自动重启（默认 `true`）
- `DEVLOOP_AGENT_RESTART_BACKOFF_MS`：自动重启退避毫秒数组（默认 `500,1000,2000,5000`）

## 事件头与 env 解析

- `POST /api/v1/registrations`、`DELETE /api/v1/registrations/{instanceId}` 支持请求头：`x-event-id`（兼容 `X-Event-Id`）
- 响应头：
  - `X-Event-Id`
  - `X-Event-Deduplicated`（`true/false`）
- `POST /api/v1/discover` 的 env 解析顺序：
  1. `x-env` / `X-Env`
  2. 请求体 `env`
  3. 运行时默认 `DEVLOOP_ENV_NAME`
  4. `base`

## 前端运行态事件

- 事件名：`agent-runtime-changed`
- 触发时机：首次启动、监督循环检测到状态变化、手动重启
- 事件负载：`status/pid/lastError/restartCount/restartAttempt/nextRestartAt` 等运行态字段
