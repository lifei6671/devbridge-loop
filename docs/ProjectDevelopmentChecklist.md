# DevLoop 开发清单

## 一、文档目的

本清单用于承接 [ProjectDevelopmentPlan.md](./ProjectDevelopmentPlan.md) 的技术方案，将已确认的设计约束冻结下来，并拆分为可执行、可验收、可排期的开发任务。

---

## 二、已冻结设计前提

### 1. 协议设计必须可扩展

一期先支持 `HTTP`、`gRPC`，但整体架构必须抽象为可扩展模型，后续可新增其他协议而不推翻现有实现。

建议在设计时固定以下抽象：

- `ProtocolAdapter`：协议适配器，定义入口解析、出口代理、回流转发、错误映射。
- `IngressResolver`：入口解析器，负责从请求上下文提取路由信息。
- `RouteTarget`：统一路由目标，屏蔽不同协议的目标表示差异。
- `Forwarder`：agent 侧本地转发器，负责将 bridge 回流流量落到本地实例。
- `EgressProxy`：出口代理器，负责本地服务对外调用时的协议处理。

要求：

- HTTP/gRPC 以插件化适配器方式接入。
- 协议无关的 tunnel、注册表、路由表、状态同步逻辑独立实现。
- 新协议接入时，优先通过新增适配器完成，不改 tunnel 主干模型。

### 2. 注册模型必须支持多实例、多端口

这是本地实例与服务端实例的注册模型，必须支持同一服务多开实例、同一实例暴露多个端口。

建议拆分三个层次的键：

- `ServiceKey = (env, serviceName)`
- `InstanceKey = (env, serviceName, instanceId)`
- `EndpointKey = (env, serviceName, instanceId, protocol, listenPort)`

建议对象模型：

- `LocalRegistration`
  - `serviceName`
  - `env`
  - `instanceId`
  - `metadata`
  - `healthy`
  - `registerTime`
  - `lastHeartbeatTime`
  - `ttlSeconds`
  - `endpoints[]`
- `LocalEndpoint`
  - `protocol`
  - `listenHost`
  - `listenPort`
  - `targetHost`
  - `targetPort`
  - `status`

要求：

- 不再用 `(serviceName, env)` 直接映射单个实例。
- 同一 `serviceName` 在同一 `env` 下允许多个实例并存。
- 同一实例允许同时注册 `httpPort`、`grpcPort` 或未来更多协议端口。
- `instanceId` 生成规则必须冻结（推荐 `UUID`，或 `hostname + pid + startTs`）。
- 注册模型必须支持心跳续期与 TTL 超时清理（被动摘除），避免异常退出后残留脏注册。

### 3. bridge 的路由提取必须高度抽象

bridge 需要支持从以下来源提取服务路由信息，并支持后续扩展：

- `Host`
- 请求头
- `SNI`

建议抽象：

- `RouteExtractor`：单一提取器接口。
- `RouteExtractResult`：统一提取结果，至少包含 `serviceName`、`env`、`protocol`、`source`。
- `RouteExtractPipeline`：提取器链路，按优先级顺序执行。

要求：

- 一期内置 `HostExtractor`、`HeaderExtractor`、`SNIExtractor`。
- 提取顺序可配置，不写死在业务代码中。
- 提取不到时进入统一兜底逻辑与错误分类。

### 4. 技术栈冻结

桌面端采用：

- 前端：`React` + `TypeScript` + `shadcn/ui`
- 宿主后端：`Rust`
- 核心通信与 bridge：`Go`

职责划分：

- Tauri 前端负责界面与交互。
- Rust Host 负责桌面能力、配置、日志、子进程管理、前端命令桥接。
- Go Agent Core 负责注册、发现、代理、tunnel、同步。
- Rust 负责拉起 Go 子进程，并通过本地 `HTTP API` 与 Go 通信（一期固定，`IPC` 留作二期优化项）。
- `cloud-bridge` 采用 Go 实现。

### 5. env 透传与优先级可配置

统一使用 `x-env` 语义，兼容 `X-Env`。

默认规则：

- 显式请求中的 `x-env` / `X-Env` 优先。
- 其次可使用运行时默认 `envName`。
- 最后回退到 `base`。

要求：

- HTTP 与 gRPC 统一走一套 env 解析配置。
- env 解析优先级做成配置项，而不是写死。
- bridge 与 agent 两侧都自动透传 env。

### 6. LocalRegistration 以运行时内存态为主

本期不依赖落盘的注册数据。agent 启动后实时接收本地服务的 `register` / `unregister` 请求维护内存态注册表。

要求：

- 配置和日志可以持久化。
- 注册表、发现结果、接管关系以运行时状态为主。
- agent 重启后不恢复旧注册，等待本地服务重新注册。
- tunnel 重连后对当前内存态 `LocalRegistration` 做全量重同步。
- 本地注册通过心跳维持存活；心跳超时后执行被动摘除并触发同步下线事件。

### 7. 本期不做鉴权

一期先不实现 agent 与 bridge 鉴权、权限校验、敏感配置保护。

说明：

- 接口与消息协议预留扩展位。
- 文档中标记为二期能力，不纳入本期交付阻塞项。

### 8. tunnel 同步模型必须幂等且可恢复

一期同步链路需要支持重试、乱序、重复投递场景，避免重连后出现重复接管或脏路由。

建议在握手与事件模型中固定以下字段：

- `sessionEpoch`：每次 tunnel 建连成功后递增，旧 epoch 事件必须丢弃。
- `resourceVersion`：注册表快照版本号，用于全量/增量同步衔接。
- `eventId`：事件唯一标识，用于去重与幂等处理。

要求：

- `register` / `unregister` / `full-sync` 事件都可幂等重放。
- bridge 对重复事件返回成功语义，不因重复报错。
- 重连后按 `sessionEpoch + resourceVersion` 恢复一致状态。

---

## 三、一期开发清单

### P0. 设计冻结

- [x] 输出系统架构图、分层图、注册时序图、入口回流时序图、出口代理时序图
- [x] 定义 `ProtocolAdapter`、`IngressResolver`、`Forwarder`、`EgressProxy` 抽象接口
- [x] 定义 `ServiceKey`、`InstanceKey`、`EndpointKey`、`RouteTarget`、`RouteExtractResult`
- [x] 冻结 `instanceId` 生成规则、注册心跳协议、TTL 超时被动摘除策略
- [x] 明确 Rust Host 与 Go Agent Core 的通信方式，固定为本地 `HTTP API`（一期不做 `IPC`）
- [x] 定义 agent 与 bridge 的 tunnel 消息模型、事件类型、错误码、重连与重同步机制，包含 `sessionEpoch`、`resourceVersion`、`eventId`
- [x] 定义 bridge 路由解析优先级配置模型，覆盖 `Host`、Header、`SNI`
- [x] 定义 env 解析优先级配置模型，默认支持 `x-env` / `X-Env`
- [x] 定义一期非功能指标（同步时延、重连恢复时间、支持注册规模、代理延迟增量）与压测口径

### P1. Tauri 桌面壳

- [x] 创建 `Tauri + React + TypeScript + shadcn/ui` 项目骨架
- [x] 搭建基础布局、导航与全局状态模型
- [ ] 实现主窗口、最小化到托盘、系统托盘菜单
- [ ] 实现配置目录、日志目录、基础配置加载与保存
- [x] 实现 Rust Host 命令桥接层
- [x] 实现 Rust Host 拉起 Go Agent Core 子进程
- [x] 实现 Go 子进程生命周期管理、崩溃检测、自动重启
- [x] 前端展示 Go Agent Core 运行状态

### P2. Go Agent Core 基础能力

- [x] 创建 Go Agent Core 工程骨架
- [x] 实现配置加载、日志、生命周期管理
- [x] 实现内存态状态存储，不持久化注册数据
- [x] 提供本地管理接口：`register`、`heartbeat`、`unregister`、`discover`、`list registrations`
- [x] 实现 `LocalRegistration` 管理
- [x] 实现 `LocalEndpoint` 管理，支持同服务多实例、多端口
- [x] 实现注册心跳续期与 TTL 超时摘除（被动摘除）
- [x] 实现本地状态查询接口，供 UI 和 Rust Host 获取状态
- [ ] 定义最近错误、最近请求、状态摘要等诊断输出模型

### P3. cloud-bridge 基础能力

- [x] 创建 `cloud-bridge` Go 工程骨架
- [x] 实现配置加载、日志、生命周期、基础管理接口
- [x] 实现 `TunnelSession` 管理
- [x] 实现 `ActiveIntercept` 管理
- [x] 实现 `BridgeRoute` 生成与查询
- [x] 实现 bridge 侧状态查询接口：session、intercept、route、错误统计

### P4. tunnel 与注册驱动同步

- [ ] 实现 agent 到 bridge 的长连接
- [x] 实现连接建立、心跳、断线检测、自动重连
- [x] 实现 agent 启动后的初次握手与状态上报（携带 `sessionEpoch`、快照版本）
- [x] 实现本地注册事件同步到 bridge
- [x] 实现本地下线事件同步到 bridge
- [x] 实现 bridge 根据注册事件生成/更新/删除 `ActiveIntercept`
- [x] 实现基于 `eventId` 的事件去重与幂等处理
- [x] 实现基于 `sessionEpoch` 的旧连接事件拒绝与乱序保护
- [x] 实现 tunnel 重连后的全量重同步
- [x] 验证多实例、多端口下同步模型正确
- [x] 验证重复注册、重复下线、乱序事件下状态一致性

### P5. 入口回流与路由抽象

- [x] 实现 `RouteExtractor` 抽象与提取器注册机制
- [x] 实现 `HostExtractor`
- [x] 实现 `HeaderExtractor`
- [x] 实现 `SNIExtractor`
- [x] 实现可配置的提取优先级链路
- [x] 实现 HTTP ingress
- [x] 实现 gRPC ingress
- [x] 实现 bridge 根据 `(env, serviceName, protocol)` 路由到目标 tunnel
- [x] 实现 agent 侧本地转发，将回流请求转发到目标 localhost endpoint
- [x] 实现统一错误分类，至少覆盖未命中路由、tunnel 离线、本地不可达、上游超时、提取失败

### P6. 出口发现与代理

- [x] 在业务系统现有 registry 抽象中新增 `agent` 类型
- [x] 实现 `discover` 规则：同 env 优先，未命中则 fallback 到 `base`
- [x] 实现 HTTP 出口代理
- [x] 实现 gRPC 出口代理
- [x] 实现 HTTP/gRPC 协议分离的出口链路
- [x] 实现 env 自动透传
- [x] 实现 env 优先级配置化
- [x] 将请求命中的 `dev/base` 结果暴露给诊断接口与 UI

### P7. Tauri UI 一期页面

- [x] 实现 Dashboard 页面
- [x] 展示 agent 状态、bridge 状态、tunnel 状态、当前 env、当前 rdName、接管数量、最近错误
- [x] 实现本地服务列表页
- [x] 展示服务名、env、instanceId、协议端口、健康状态、注册时间
- [x] 支持刷新、手动注销、查看详情
- [x] 实现 ActiveIntercept 列表页
- [x] 实现日志页面
- [x] 实现配置页面
- [x] 页面数据全部接入 Rust Host / Go Agent Core 的真实状态接口

### P8. Demo 与联调

- [ ] 准备 `order-service` demo
- [ ] 准备 `user-service` demo
- [ ] 两个 demo 同时提供 HTTP 与 gRPC 能力
- [ ] 验证 `order(base) -> user(base)`
- [ ] 验证 `order(dev) -> user(base)`
- [ ] 验证 `order(base, env=dev-xxx) -> user(dev-xxx)`
- [ ] 验证 `order(dev-xxx) -> user(dev-xxx) -> inventory(base)`
- [ ] 验证 HTTP 全链路正常
- [ ] 验证 gRPC 全链路正常
- [ ] 验证本地服务停止后可自动摘除
- [ ] 验证本地服务异常退出（未主动 `unregister`）后可通过心跳超时自动摘除
- [ ] 验证 tunnel 重连后可基于当前内存态注册表完成全量重同步
- [ ] 验证重复注册/重复注销不导致重复接管或错误摘除
- [ ] 验证同步事件乱序/重放下 bridge 与 agent 最终状态一致
- [ ] 验证 UI 可正确展示连接、注册、接管、错误状态变化

---

## 四、本期明确不做

- [ ] agent 与 bridge 鉴权
- [ ] 接管权限控制
- [ ] token 加密存储
- [ ] 桌面通知
- [ ] 请求可视化链路页
- [ ] 日志导出与诊断信息复制
- [ ] 多 RD 并发隔离
- [ ] 主动健康检查（HTTP/gRPC probe）自动摘除
- [ ] 路由来源可信级与 Header 防注入策略（企业内 VPN 场景下延期到二期）

说明：本期仅实现基于注册心跳与 tunnel 状态的被动摘除能力。

以上能力保留接口扩展点，但不纳入本期 MVP 交付。

---

## 五、本期验收口径

### 功能验收

- [ ] Win11 上可启动 Tauri 桌面端
- [ ] Rust Host 可稳定拉起 Go Agent Core
- [ ] agent 可与 `cloud-bridge` 建立长连接并维持心跳
- [ ] 本地服务可实时注册到 dev-agent
- [ ] 本地服务可实时从 dev-agent 注销
- [ ] dev-agent 可同步接管关系到 `cloud-bridge`
- [ ] bridge 可从 `Host`、Header、`SNI` 抽象链路中解析路由信息
- [ ] base 服务可通过 bridge 访问到本地 dev 服务
- [ ] dev 服务出口可实现 dev 优先、base fallback
- [ ] HTTP 链路可用
- [ ] gRPC 链路可用
- [ ] 多实例、多端口注册与转发正确
- [ ] tunnel 重连后状态恢复正确
- [ ] 本地服务异常退出后可在 TTL 时间窗内被动摘除并同步到 bridge
- [ ] 重复注册/重复注销/重放事件不会破坏最终接管状态
- [ ] UI 可正确展示注册、连接、接管、错误状态

### 工程验收

- [ ] Tauri UI、Rust Host、Go Agent Core、cloud-bridge 模块边界清晰
- [ ] 协议无关层与协议适配层边界清晰
- [ ] HTTP 与 gRPC 路径分离，扩展其他协议时不需要重构主干
- [ ] 路由提取链支持配置优先级
- [ ] env 解析优先级支持配置
- [ ] tunnel 同步模型具备幂等语义（`sessionEpoch`、`resourceVersion`、`eventId`）
- [ ] 配置项清晰、日志可追踪、错误码明确

### 非功能验收（一期目标）

- [ ] tunnel 断线重连后，P95 在 `10s` 内恢复有效路由
- [ ] 注册变更同步到 bridge 生效延迟 P95 不超过 `2s`
- [ ] 单 agent 运行时内存态支持至少 `200` 个 `LocalRegistration` 与 `400` 个 endpoint
- [ ] 入口回流代理链路引入的额外延迟 P95 不超过 `20ms`（同地域内网基准）
