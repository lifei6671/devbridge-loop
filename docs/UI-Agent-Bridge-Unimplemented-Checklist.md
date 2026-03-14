# UI -> Agent -> Bridge 未实现功能清单（启动开发版）

## 1. 文档目的

本清单用于聚焦 `UI -> Agent -> Bridge` 三者联动链路中尚未完成的实现项，作为下一阶段开发执行入口。

说明：

- 状态基线：截至 `2026-03-14` 的仓库实现状态。
- 本清单只覆盖“链路打通”与“运行态真相源”相关事项，不替代完整总清单。
- 每个任务均附带“开发参考文档”和“建议代码入口”。

---

## 2. 开发参考文档（真相源）

主规范（开发前建议通读）：

- [Agent‑and‑Bridge‑Implementation‑Technical‑Design.md](./Agent‑and‑Bridge‑Implementation‑Technical‑Design.md)
- [LTFP-TransportAbstraction.md](./LTFP-TransportAbstraction.md)
- [LTFP-v1-Draft.md](./LTFP-v1-Draft.md)

执行与边界清单（开发时交叉核对）：

- [Agent-and-Bridge-ExecutionChecklist.md](./Agent-and-Bridge-ExecutionChecklist.md)
- [Agent-TauriConnectionModelExecutionChecklist.md](./Agent-TauriConnectionModelExecutionChecklist.md)

---

## 3. 未实现清单（按优先级）

### P1：控制面业务消息闭环（Agent <-> Bridge）

- [x] `UAB-C1` 补齐控制面业务消息类型与编解码（不仅 heartbeat）
  - 目标：补齐 `ConnectorHello/Welcome/Auth/AuthAck/PublishService/Health/TunnelPoolReport/TunnelRefillRequest/ControlError` 的帧类型与处理约定。
  - 参考文档：`LTFP-TransportAbstraction` 的 `7. Control Channel 语义`、`17.2 ControlFrame`；`LTFP-v1-Draft` 的 `18.2 关键控制消息`。
  - 建议代码入口：`ltfp/transport/`、`ltfp/pb/`、`agent-core/runtime/agent/app/runtime_bridge.go`、`cloud-bridge/runtime/bridge/app/control_plane.go`。
  - 完成记录（2026-03-14）：已新增控制消息类型（含 `TunnelPoolReport/TunnelRefillRequest`）、业务控制帧类型映射与 `ControlEnvelope <-> ControlFrame` 编解码函数，并补齐单元测试。

- [x] `UAB-C2` 接入 Agent 控制面消息分发循环
  - 目标：在 Agent runtime 主循环中处理业务控制消息，而非仅处理 ping/pong。
  - 参考文档：`技术方案` 的 `7.4 Agent SessionManager`、`10. TunnelPoolReport 与 TunnelRefillRequest`、`11. 控制面 HOL 治理`。
  - 建议代码入口：`agent-core/runtime/agent/app/runtime_bridge.go`、`agent-core/runtime/agent/control/`。
  - 完成记录（2026-03-14）：已在 `waitForActiveExit` 接入业务控制帧分发；新增 `TunnelRefillRequest` 解析与 `RefillHandler` 调用链；新增 `ControlError` 入库到 runtime 最近错误字段并补齐单测。

- [x] `UAB-C3` 接入 Bridge 控制面业务处理器
  - 目标：把 Bridge 侧 `control/` 目录下的 handler 接到 control plane runtime，不再只回 heartbeat。
  - 参考文档：`技术方案` 的 `8.5/8.6`、`10`、`12`；`LTFP-v1-Draft` 的 `10.2/10.3`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/app/control_plane.go`、`cloud-bridge/runtime/bridge/control/`、`cloud-bridge/runtime/bridge/registry/`。
  - 完成记录（2026-03-14）：Bridge 控制面已新增业务分发器，TCP/gRPC 控制通道可处理 `PublishService/UnpublishService/RouteAssign/RouteRevoke` 并返回 ACK，`ConnectorAuthAck` 会刷新会话视图；新增 TCP/gRPC 业务回包单测。

- [x] `UAB-C4` 完成控制面高优先级与 HOL 规避落地
  - 目标：保证心跳、鉴权、refill、error 在大消息场景下不被饿死。
  - 参考文档：`技术方案` 的 `11. 控制面 HOL 治理`；`LTFP-TransportAbstraction` 的 `7.5 控制面优先级与 HOL 规避`。
  - 建议代码入口：`agent-core/runtime/agent/control/priority_dispatcher.go`、`cloud-bridge/runtime/bridge/app/control_plane.go`、`ltfp/transport/tcpbinding/control_channel.go`。
  - 完成记录（2026-03-14）：新增控制帧优先级策略 `RecommendControlFramePriority`（心跳/鉴权/refill/error 高优，发布与池上报低优），并接入 Bridge 控制面回包发送路径，补齐单测。

### P1：数据面主路径闭环（Bridge 分流执行 + Agent 接流量）

- [x] `UAB-D1` Bridge runtime 接入 routing + connector proxy 主路径
  - 目标：将 ingress 命中流量实际交给 route resolver、selector 与 connector 执行器。
  - 参考文档：`技术方案` 的 `8.3~8.7`、`9`、`12.1`；`LTFP-v1-Draft` 的 `15.1`、`17.1`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/app/bootstrap.go`、`cloud-bridge/runtime/bridge/routing/`、`cloud-bridge/runtime/bridge/connectorproxy/`。
  - 完成记录（2026-03-14）：Bridge `app` 层已新增 `Dispatch*Ingress` 统一分发入口，打通 `ingress lookup -> routing resolver -> connector dispatcher` 执行链；`bootstrap` 已将控制面与数据面绑定到共享 `session/service/route/tunnel` 注册表，新增 app 层集成测试覆盖 connector 主路径闭环与 tunnel 回收行为。

- [x] `UAB-D2` Bridge runtime 接入 direct proxy 与 hybrid fallback
  - 目标：补齐 `external_service` 直连路径与 `hybrid_group` 的 `pre_open_only` fallback 执行链。
  - 参考文档：`技术方案` 的 `8.8/8.9`、`3.6`；`LTFP-v1-Draft` 的 `15.2/15.3`、`17.2/17.3`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/directproxy/`、`cloud-bridge/runtime/bridge/routing/hybrid.go`、`cloud-bridge/runtime/bridge/routing/executor.go`。
  - 完成记录（2026-03-14）：Bridge `app` 层已将 `PathExecutor` 从 direct 占位实现切换为真实 `directproxy.Executor`，并新增默认静态 discovery provider（支持 `external_service.selector.endpoint/endpoints`）与默认 TCP upstream dialer；新增 app 层集成测试覆盖 `external_service` 直连成功链路与 `hybrid_group` 在 `pre_open_no_tunnel` 场景回退到 direct path 的执行闭环。

- [x] `UAB-D3` 打通 Agent 侧 `TrafficOpen -> OpenAck -> Relay` 实际消费链
  - 目标：将 Agent 已有 `traffic` 模块接入 tunnel 生命周期，确保 idle tunnel 能被消费执行一次 traffic。
  - 参考文档：`技术方案` 的 `7.6`、`7.9`、`3.3/3.4`；`LTFP-TransportAbstraction` 的 `11`、`12`。
  - 建议代码入口：`agent-core/runtime/agent/tunnel/`、`agent-core/runtime/agent/traffic/`、`agent-core/runtime/agent/app/bootstrap.go`。
  - 完成记录（2026-03-14）：Agent `app` 层已新增 data-plane runtime：通过 `trafficAcceptor` worker 将 idle tunnel 首帧 `TrafficOpen` 移交给 `traffic.Opener` 执行 `OpenAck -> Relay -> Close/Reset`；新增 `transport.Tunnel -> traffic.TunnelIO` payload 适配层并接入 `bridgeTunnelOpener`；`tunnel.Manager/Registry` 新增按 tunnelID 激活 idle 能力（`idle -> reserved -> active` 原子迁移）；新增 app/tunnel 单测覆盖成功链路与首帧协议错误回收。

- [x] `UAB-D4` 收口 timeout/cancel/late-ack 语义到端到端路径
  - 目标：`open_ack` 超时后取消、迟到 ack 丢弃、traffic 结束关 tunnel 的行为在真实链路可复现。
  - 参考文档：`技术方案` 的 `8.7.4/8.7.5`、`12`；`LTFP-TransportAbstraction` 的 `14/15`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/connectorproxy/open_handshake.go`、`cloud-bridge/runtime/bridge/connectorproxy/cancel.go`、`agent-core/runtime/agent/traffic/`。
  - 完成记录（2026-03-14）：Agent `traffic.Opener` 已新增 tunnel 关闭信号监听（可选 `Done()` 接口），对端取消/关 tunnel 可主动中止 pre-open 拨号与 relay 上下文；`runtimeTrafficTunnel` 已透传 `Done/Err`；Bridge `app` 层新增 connector timeout/late-ack 端到端测试，覆盖 `DispatchHTTPIngress -> connector path -> open_ack timeout -> reset -> tunnel 回收 -> late-ack 丢弃指标` 闭环；并支持在 app 测试注入 connector open timeout/late-ack drain 参数与指标容器。

### P2：资源同步与注册表真相源

- [x] `UAB-S1` 接入 Agent 服务发布与健康上报
  - 目标：把 `service`、`health_reporter` 运行态接入控制面上报，Bridge 可维护服务状态。
  - 参考文档：`技术方案` 的 `7.7/7.8`、`3.2`；`LTFP-v1-Draft` 的 `7.3/7.6/16`。
  - 建议代码入口：`agent-core/runtime/agent/service/`、`agent-core/runtime/agent/control/publisher.go`、`agent-core/runtime/agent/control/health_reporter.go`。
  - 完成记录（2026-03-14）：Agent 已落地 `ServiceCatalog` 与 `HealthReporter` 运行态，Bridge 会话 ACTIVE 后可自动发送 `PublishService` 与 `ServiceHealthReport`；`service.list` 已从本地目录返回真实服务条目；Bridge 控制面已接入 `ServiceHealthReport` 分发与 `HealthHandler`，可将健康状态写入 `ServiceRegistry`；新增 Agent/Bridge 单测覆盖发布、健康上报与 registry 更新闭环。

- [x] `UAB-S2` 接入 TunnelPoolReport 与 RefillRequest 闭环
  - 目标：Agent 侧 report 与 Bridge 侧 refill 控制形成闭环驱动，而不只靠本地周期纠偏。
  - 参考文档：`技术方案` 的 `10`、`7.5`、`8.7.2`；`LTFP-TransportAbstraction` 的 `9/10`。
  - 建议代码入口：`agent-core/runtime/agent/control/tunnel_reporter.go`、`agent-core/runtime/agent/control/refill_handler.go`、`cloud-bridge/runtime/bridge/control/refill_controller.go`。
  - 完成记录（2026-03-14）：Agent runtime 已接入 `TunnelReporter`（事件驱动 + 周期纠偏）并在会话 ACTIVE 后立即上报 `TunnelPoolReport`；`Runtime.NotifyEvent` 已同时扇出到 traffic reconcile 与 tunnel report；Bridge 控制面已接入 `TunnelPoolReport` 处理器与 `RefillController`，可按低水位阈值生成 `TunnelRefillRequest` 并通过控制通道回推 Agent；新增 Agent/Bridge 单测覆盖上报发送、补池决策、会话代际校验与控制面分发回包。

- [x] `UAB-S3` Bridge runtime 接入 session/service/route/tunnel 注册表生命周期
  - 目标：将 registry 从“有模块”变成“被 runtime 实际消费与更新”的真相源。
  - 参考文档：`技术方案` 的 `8.4~8.6`、`4`；`LTFP-v1-Draft` 的 `8.2 Runtime Traffic Registry`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/registry/`、`cloud-bridge/runtime/bridge/control/`、`cloud-bridge/runtime/bridge/routing/`。
  - 完成记录（2026-03-14）：Bridge `control_plane` 已接入 session 生命周期收敛循环（`ACTIVE -> STALE -> CLOSED`），支持基于 `heartbeat_timeout` 的周期 sweep；同 connector 新 `session_epoch` 接管时会把旧 session 标记为 `DRAINING` 并联动收敛运行态；`ServiceRegistry` 新增按 connector 生命周期批量标记（`INACTIVE/STALE`），`TunnelRegistry` 新增按 session 批量摘除并关闭底层 tunnel，避免旧会话 tunnel 被误分配；`PublishHandler` 发布服务时可落库 `connector_id`（支持 envelope/session 回填）；新增 Bridge/Registry 单测覆盖切代收敛、超时收敛与批量清理行为。

### P2：UI 可观测数据真实化（避免占位返回）

- [x] `UAB-U1` `service.list` 返回真实服务数据
  - 目标：去掉空数组占位，展示真实 `service_id/status/endpoint_count`。
  - 参考文档：`技术方案` 的 `7.7`、`13`；`LTFP-v1-Draft` 的 `7.3/7.4`。
  - 建议代码入口：`agent-core/runtime/agent/app/localrpc_server.go`、`agent-core/runtime/agent/app/runtime_bridge.go`、`apps/dev-agent/src-tauri/src/commands/service.rs`。
  - 完成记录（2026-03-14）：Agent `service.list` 已输出真实服务条目（含 `service_id/status/endpoint_count`）；Tauri `service_list_snapshot` 已兼容 `service_type/health_status/endpoints` 字段并补齐解析单测；前端服务表已改为直接展示 `service_id/名称/协议/endpoint 数/状态/更新时间`，去除原空占位与 tunnel 侧推断依赖。

- [x] `UAB-U2` `tunnel.list` 返回真实 tunnel 关联信息
  - 目标：去掉 `service_id/local_addr/latency` 占位字段，接入真实运行态数据。
  - 参考文档：`技术方案` 的 `5.2/6`、`13`。
  - 建议代码入口：`agent-core/runtime/agent/tunnel/registry.go`、`agent-core/runtime/agent/app/runtime_bridge.go`、`apps/dev-agent/src-tauri/src/commands/tunnel.rs`。
  - 完成记录（2026-03-14）：Agent runtime 已新增 tunnel 关联运行态缓存（`tunnel_id -> traffic_id/service_id/local_addr/open_ack_latency/upstream_dial_latency`），在 `trafficAcceptor -> Opener.Handle` 链路中写入并在 tunnel 回收时清理；`tunnel.list` 改为返回真实关联字段（不再固定 `--/0` 占位），并保留 `remote_addr` 为当前 Bridge 地址；新增单测覆盖关联缓存写入覆盖/清理与 `tunnel.list` 真实字段输出；前端隧道表对未知延迟改为展示 `--`，避免把 0 误读为真实时延。

- [x] `UAB-U3` `traffic.stats.snapshot` 接入 runtime 真实链路指标
  - 目标：将 Agent runtime 流量指标作为主来源；宿主网卡采样保留为“主机视角”指标，不混淆语义。
  - 参考文档：`技术方案` 的 `13`、`9`；`LTFP-TransportAbstraction` 的 `11.5`。
  - 建议代码入口：`agent-core/runtime/agent/obs/`、`agent-core/runtime/agent/app/localrpc_server.go`、`apps/dev-agent/src-tauri/src/commands/traffic.rs`。
  - 完成记录（2026-03-14）：Agent runtime 已在 `traffic relay` 路径累计真实上/下行字节并通过 `traffic.stats.snapshot` 输出速率与累计值（`upload/download_bytes_per_sec` + totals）；`localrpc` 已移除占位 0 返回；Tauri `traffic_stats_snapshot` 改为优先读取 Agent RPC 快照，宿主网卡采样仅在 RPC 不可用时回退，并保留 `source` 字段区分来源，避免语义混淆；新增单测覆盖 runtime 快照与 localrpc 分发路径。

- [ ] `UAB-U4` `diagnose.logs` 与诊断快照接入 runtime 事件源
  - 目标：去掉空日志占位，至少提供控制面状态变化、错误、重连、补池事件。
  - 参考文档：`技术方案` 的 `13.3`、`12`；`Agent-TauriConnectionModelExecutionChecklist` 的事件/诊断相关条目。
  - 建议代码入口：`agent-core/runtime/agent/app/localrpc_server.go`、`agent-core/runtime/agent/app/runtime_bridge.go`、`apps/dev-agent/src/App.tsx`。

### P3：Bridge 管理后台能力收口（独立于主链路）

- [ ] `UAB-A1` 落地 `admin.enabled` 默认关闭与管理面独立开关
  - 目标：`admin.enabled=false` 时不注册 Admin API/UI 路由，不初始化管理中间件。
  - 参考文档：`Agent-and-Bridge-ExecutionChecklist` 的 `A16`；`BridgeAdminBackendTechnicalProposal.md`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/app/config.go`、`cloud-bridge/runtime/bridge/app/bootstrap.go`。

- [ ] `UAB-A2` 分阶段补齐 Admin API 的鉴权、权限、审计与导出脱敏
  - 目标：先做只读域，再做受控写接口，最终满足 A16 验收标准。
  - 参考文档：`Agent-and-Bridge-ExecutionChecklist` 的 `A16` 全项；`BridgeAdminBackendTechnicalProposal.md`。
  - 建议代码入口：`cloud-bridge/runtime/bridge/app/`（后续可拆 `adminapi/` 与 `adminview/`）。

---

## 4. 推荐开发顺序

1. 先完成 `P1 控制面 + 数据面` 的最小闭环（不做 UI 美化，先打通真实链路）。
2. 再完成 `P2 资源同步 + UI 真相源`（消除占位数据）。
3. 最后处理 `P3 Admin`（与主链路解耦并行推进）。

---

## 5. 进度记录（可持续更新）

- [x] 第一轮：控制面业务消息闭环
- [x] 第二轮：数据面 connector/direct/hybrid 路径闭环
- [ ] 第三轮：UI 真实数据接入与诊断收口
- [ ] 第四轮：Admin 能力收口
