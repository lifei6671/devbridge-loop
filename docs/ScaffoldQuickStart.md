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
├── examples/                   # Demo 服务（order/user/inventory）
│   ├── cmd/order-service/
│   ├── cmd/user-service/
│   └── cmd/inventory-service/
├── apps/
│   └── dev-agent/
│       ├── src/                # React + shadcn/ui
│       └── src-tauri/          # Tauri v2 Rust Host
└── go.work                     # 多模块工作区
```

## 版本约束

- Go：`1.26.0`（`agent-core/go.mod`、`cloud-bridge/go.mod` 已固定）
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
  - `POST /api/v1/egress/http`
  - `POST /api/v1/egress/grpc`
- 状态面 API：
  - `GET /api/v1/state/summary`
  - `GET /api/v1/state/tunnel`
  - `GET /api/v1/state/intercepts`
  - `GET /api/v1/state/errors`
  - `GET /api/v1/state/requests`
  - `GET /api/v1/state/diagnostics`（聚合 summary/errors/requests）
  - `POST /api/v1/control/reconnect`
- 内存态注册表 + TTL 过期摘除扫描
- 注册键模型索引：`ServiceKey / InstanceKey / EndpointKey`
- 事件幂等骨架：`x-event-id` 去重（重复 `register/unregister` 返回成功语义，不重复增加 `resourceVersion`）
- 已接入 tunnel client 同步链路（agent -> bridge）：
  - 启动自动 `HELLO -> FULL_SYNC_REQUEST -> FULL_SYNC_SNAPSHOT`
  - 注册/注销与 TTL 摘除自动投递 `REGISTER_UPSERT/REGISTER_DELETE`
  - 定时发送 `TUNNEL_HEARTBEAT`
  - 断线后按退避重连并复用事件幂等语义
- 已接入本地回流转发入口：`POST /api/v1/backflow/http`
  - 接收 bridge 回流请求并转发到本地 `targetHost:targetPort`
  - 将回流转发超时/不可达归一为 `UPSTREAM_TIMEOUT`、`LOCAL_ENDPOINT_UNREACHABLE`
- 已接入本地 gRPC 回流入口：`POST /api/v1/backflow/grpc`
  - 接收 bridge gRPC 回流请求并对本地目标执行 Health Check
  - 回流失败统一映射为 `UPSTREAM_TIMEOUT`、`LOCAL_ENDPOINT_UNREACHABLE`
- 已接入 HTTP 出口代理：`POST /api/v1/egress/http`
  - 先执行 `discover`（dev 优先、base fallback）
  - 命中 dev 时自动代理到 bridge ingress（并透传 `x-env/X-Env`）
  - 未命中 dev 时按请求中的 `baseUrl` 走 base fallback
  - 请求摘要写入 `GET /api/v1/state/requests`，供诊断与 UI 展示
- 已接入 gRPC 出口代理：`POST /api/v1/egress/grpc`
  - 与 HTTP 出口链路完全分离，独立处理 `discover` 与目标选择
  - 当前 MVP 支持 gRPC Health Check 调用（`Health/Check`）
  - 命中 dev 时走本地 discover 目标；未命中时按 `baseAddress` fallback
  - 同样透传 `x-env/X-Env` metadata，并写入 `state/requests` 摘要
- 协议扩展接口占位：`ProtocolAdapter` / `IngressResolver` / `Forwarder` / `EgressProxy`
- 新增业务侧 registry 适配层（Go）：
  - 包路径：`agent-core/pkg/registry`
  - 提供 `registry type=agent` 工厂与适配器
  - 已实现：`Register / Heartbeat / Unregister / Discover`

2. `cloud-bridge`
- 状态 API：`sessions/intercepts/routes/errors`
- tunnel 事件入口占位：`POST /api/v1/tunnel/events`
- 路由提取抽象：`RouteExtractor`、`RouteExtractPipeline`
- 内置提取器：`host/header/sni`
- 已接入 HTTP ingress 骨架：`/api/v1/ingress/http/**`
  - 先按提取器链路解析 `(env, serviceName)`，再匹配 `(env, serviceName, protocol=http)` 路由
  - 命中后回调 agent `POST /api/v1/backflow/http`，完成本地服务回流
  - 回流异常统一输出：`ROUTE_NOT_FOUND/ROUTE_EXTRACT_FAILED/TUNNEL_OFFLINE`
- 已接入 gRPC ingress MVP：`/api/v1/ingress/grpc`
  - 先按提取器链路解析 `(env, serviceName)`，再匹配 `(env, serviceName, protocol=grpc)` 路由
  - 通过 agent `POST /api/v1/backflow/grpc` 执行本地 gRPC Health Check 回流
- 已实现同步幂等主干：`sessionEpoch/resourceVersion/eventId`
  - 重复事件：返回 `ACK + duplicate` 语义
  - 旧 epoch 事件：返回 `ERROR + STALE_EPOCH_EVENT`
  - 新 epoch 增量事件：要求先 `HELLO` 建连
  - `FULL_SYNC_SNAPSHOT`：清空 tunnel 旧状态后重建 intercept/route
- 新增错误统计：`GET /api/v1/state/errors`
  - 返回错误总量、按错误码聚合统计、最近错误列表（倒序）

3. `dev-agent` 桌面端
- Rust Host：
  - 主窗口关闭时隐藏到系统托盘（不直接退出进程）
  - 系统托盘菜单支持：显示主窗口、隐藏到托盘、退出应用
  - 启动时拉起 Go Agent Core 子进程（可通过环境变量覆盖）
  - 后台监督循环（2s）：进程退出检测 + 自动重启 + 退避重试
  - 向前端推送 `agent-runtime-changed` 事件，实现运行态实时刷新
  - 调用 Go 本地 HTTP API
  - 暴露 Tauri commands 给前端（含 `restart_agent_process` 手动重启）
- React Dashboard：
  - Agent/Bridge/Tunnel 状态卡片
  - 多页面导航：`Dashboard / Services / Intercepts / Logs / Config`
  - 服务列表页：实例详情查看 + 手动注销
  - ActiveIntercept 列表页（实时读取 `/api/v1/state/intercepts`）
  - 日志页（优先读取 `/api/v1/state/diagnostics`，兼容回退到旧接口）
  - 配置页支持桌面配置加载与保存
  - 刷新 + 手动重连 + 手动重启
  - 新增命令：
    - `get_diagnostics`
    - `get_desktop_config`
    - `save_desktop_config`

4. 业务系统 `registry=agent` 接入（Go）
- 适配器包：`agent-core/pkg/registry`
- 示例：

```go
adapter, err := registry.NewAdapter(registry.Options{
    Type:       registry.RegistryTypeAgent,
    AgentAddr:  "127.0.0.1:19090",
    RuntimeEnv: "dev-alice",
})
if err != nil {
    panic(err)
}

_, _ = adapter.Register(ctx, registry.Registration{
    ServiceName: "user-service",
    Endpoints: []registry.Endpoint{
        {Protocol: "http", TargetHost: "127.0.0.1", TargetPort: 18080},
    },
})
```

5. `examples` Demo 服务
- `order-service`：同时提供 HTTP 与 gRPC（Health）能力，并提供调用 `user-service` 的 HTTP/gRPC demo 接口
- `user-service`：同时提供 HTTP 与 gRPC（Health）能力，并提供调用 `inventory-service` 的 HTTP/gRPC demo 接口
- `inventory-service`：可选基础依赖服务，同时提供 HTTP 与 gRPC（Health）能力，用于 `base fallback` 联调

## 本地开发命令

### Go 侧

```bash
# 在各模块目录运行测试
cd agent-core && go test ./...
cd cloud-bridge && go test ./...

# 启动 agent-core
cd agent-core && go run ./cmd/agent-core

# 启动 cloud-bridge
cd cloud-bridge && go run ./cmd/cloud-bridge
```

### Makefile（推荐）

```bash
# 运行 Go 测试
make test

# 编译 Go 模块
make build-go

# 编译 demo 服务（order/user/inventory）
make build-demos

# 编译前端
make build-dev-agent-ui

# 一键编译（Go + 前端）
make build-all
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

### Demo 服务（P8）

```bash
# 可选：统一指定 agent-core 地址与 demo env
export DEMO_AGENT_ADDR=127.0.0.1:19090
export DEMO_ENV_NAME=dev-alice

# 启动 inventory（可选，用于 user->inventory fallback 场景）
make run-demo-inventory

# 启动 user-service
make run-demo-user

# 启动 order-service
make run-demo-order
```

## 环境变量（已支持）

- `DEVLOOP_AGENT_HTTP_ADDR`：Go Agent Core HTTP 地址，默认 `127.0.0.1:19090`
- `DEVLOOP_RD_NAME`：当前研发人员标识
- `DEVLOOP_ENV_NAME`：当前默认 env
- `DEVLOOP_ENV_RESOLVE_ORDER`：env 解析优先级（默认 `requestHeader,payload,runtimeDefault,baseFallback`）
- `DEVLOOP_DEFAULT_TTL_SECONDS`：注册默认 TTL
- `DEVLOOP_SCAN_INTERVAL_SECONDS`：TTL 扫描周期（秒）
- `DEVLOOP_AGENT_API_BASE`：Rust Host 调用 agent-core 的 base URL
- `DEVLOOP_AGENT_BINARY`：Rust Host 直接拉起的 agent-core 二进制路径
- `DEVLOOP_AGENT_CORE_DIR`：Rust Host 用 `go run` 拉起 agent-core 时的工作目录
- `DEVLOOP_AGENT_AUTO_RESTART`：是否自动重启（默认 `true`）
- `DEVLOOP_AGENT_RESTART_BACKOFF_MS`：自动重启退避毫秒数组（默认 `500,1000,2000,5000`）
- `DEVLOOP_TUNNEL_BRIDGE_ADDRESS`：agent 同步到 bridge 的地址（默认 `http://127.0.0.1:18080`）
- `DEVLOOP_TUNNEL_HEARTBEAT_INTERVAL_SEC`：tunnel 心跳间隔秒数（默认 `10`）
- `DEVLOOP_TUNNEL_RECONNECT_BACKOFF_MS`：tunnel 重连退避毫秒数组（默认 `500,1000,2000,5000`）
- `DEVLOOP_TUNNEL_REQUEST_TIMEOUT_SEC`：agent 到 bridge 请求超时秒数（默认 `5`）
- `DEVLOOP_TUNNEL_BACKFLOW_BASE_URL`：agent 在 HELLO 中上报的回流地址（默认按 `DEVLOOP_AGENT_HTTP_ADDR` 自动推导）
- `DEVLOOP_BRIDGE_PUBLIC_HOST`：bridge 对外路由域名（默认 `bridge.example.internal`）
- `DEVLOOP_BRIDGE_PUBLIC_PORT`：bridge 对外路由端口（默认 `443`）
- `DEVLOOP_BRIDGE_FALLBACK_BACKFLOW_URL`：bridge 侧回流地址兜底值（默认 `http://127.0.0.1:19090`）
- `DEVLOOP_BRIDGE_INGRESS_TIMEOUT_SEC`：bridge 调用 agent 回流接口的超时秒数（默认 `10`）

## 桌面配置持久化

- Rust Host 启动后会自动创建配置目录与日志目录，并加载 `desktop-config.json`
- 配置文件路径通过 `get_desktop_config` 返回：
  - `configDir`
  - `logDir`
  - `configFile`
- 当前实现优先级：环境变量 > 配置文件 > 默认值

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

## Tunnel 事件回包

- `POST /api/v1/tunnel/events` 返回统一 `ACK/ERROR` 结构：
  - `type`: `ACK` 或 `ERROR`
  - `status`: `accepted/duplicate/rejected`
  - `eventId/sessionEpoch/resourceVersion`
  - `deduplicated`
  - `errorCode/message`（仅 `ERROR`）
