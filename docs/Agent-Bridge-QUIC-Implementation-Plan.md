# Agent / Bridge QUIC 接入开发计划与执行清单

## 1. 文档目标

本文档用于把 Agent 与 Bridge 之间新增 `quic_native` transport binding 的工作，
拆解为可执行、可验收、可灰度发布的工程计划。

本计划基于以下现有真相源，不重复定义上层业务协议语义：

- `docs/Agent‑and‑Bridge‑Implementation‑Technical‑Design.md`
- `docs/Agent-and-Bridge-ExecutionChecklist.md`
- `docs/LTFP-TransportAbstraction.md`
- `docs/LTFP-TransportExecutionChecklist.md`

---

## 2. 目标与范围

### 2.1 本次目标

在不改变现有 LTFP 上层控制面与数据面语义的前提下，为 Agent 与 Bridge 增加
一个新的 transport binding：`quic_native`。

新增能力包括：

- Agent 通过 QUIC 与 Bridge 建立长期 session
- 控制面通过长期 QUIC stream 承载
- 数据面 tunnel 通过 Agent 主动打开的 QUIC bidirectional stream 承载
- 现有 `supported_bindings / selected_binding` 握手协商链路支持 `quic_native`
- Agent / Bridge 运行时支持通过配置切换 `grpc_h2 / tcp_framed / quic_native`
- 补齐 QUIC binding 的测试、观测与灰度发布策略

### 2.2 明确不做

本轮不实现以下能力：

- `h3_stream`
- datagram
- session resume / 0-RTT
- mid-stream failover
- 多 Bridge 负载均衡
- tunnel 并发多路复用语义变更
- 把现有 token 认证直接升级为 mTLS 双向认证

### 2.3 关键约束

- 不修改 `TrafficOpen / OpenAck / Data / Close / Reset / TunnelRecycle` 的业务语义
- 不改变“Agent 主动建 tunnel、Bridge 消费 idle tunnel”的池化模型
- 不改变“单 tunnel 单并发 traffic，可串行复用”的传输层约束
- QUIC 分支必须保持对现有管理面、日志、指标口径的兼容

---

## 3. 实施决策

### 3.1 binding 选型

采用 `quic-go` 实现 `quic_native` binding，首版不走 “gRPC over QUIC”。

原因：

- 当前仓库的 transport 抽象已经明确预留 `quic_native`
- 现有 `grpc_h2` 与 `tcp_framed` 都是 binding 级实现，新增 QUIC 更适合沿用同一抽象
- 直接实现原生 QUIC stream 到 `transport.ControlChannel / Tunnel` 的映射，边界更清晰

### 3.2 首版连接模型

一个 Agent session 对应一条 QUIC connection：

- 1 条长期双向 control stream
- 0..N 条 Agent 主动打开的 bidirectional tunnel streams

stream 映射说明：

- 首条长期双向 stream 固定作为 control stream，承载 `ConnectorHello / ConnectorAuth / Heartbeat / Publish / TunnelPoolReport` 等控制面帧
- 后续由 Agent 主动打开的双向 stream 固定作为 tunnel stream，每条 stream 对应一条独立 tunnel
- control stream 使用现有 control frame fragment / reassemble 语义，读写两侧串行化，避免并发写帧与分片重组互相踩踏
- tunnel stream 的 `Reset` 映射到现有 `TrafficReset / broken` 收敛语义，`CloseWrite -> EOF` 映射到半关闭读尽语义
- 首版不引入 uni stream、datagram、0-RTT、session resume 等额外分支，避免 transport 抽象提前扩容

Bridge 继续复用现有 tunnel pool 逻辑：

1. Agent 建立 QUIC 连接
2. Agent 打开 control stream 完成握手和认证
3. Agent 主动打开 bidirectional tunnel stream
4. Bridge 接收 stream 并写入 idle pool
5. Bridge 为 traffic 分配 idle tunnel
6. traffic 结束后走现有 recycle / close 规则

### 3.3 安全策略

QUIC 首版默认要求 TLS 1.3 加密。

保留现有上层握手认证：

- 传输层：QUIC + TLS 1.3
- 协议层：现有 `ConnectorHello / ConnectorAuth / ConnectorAuthAck`

首版不把 QUIC 强绑到 mTLS，但配置结构应为后续 mTLS 留扩展位。

---

## 4. 当前代码基线

### 4.1 已具备的基础

- `ltfp/transport/types.go` 已有 `BindingTypeQUICNative`
- `docs/LTFP-TransportAbstraction.md` 已定义 `quic_native` 的最低能力要求
- `ConnectorHello.supported_bindings` 与 `ConnectorWelcome.selected_binding` 已存在
- Bridge 握手阶段已有 `selectWelcomeBinding(...)` 逻辑
- `grpcbinding` 与 `tcpbinding` 已提供可复用的 binding 组织模板

### 4.2 当前缺口

- 尚无 `ltfp/transport/quicbinding/`
- Agent runtime 配置校验仍只接受 `tcp_framed / grpc_h2`
- Bridge `control_plane` 配置尚无 QUIC 监听入口
- Bridge / Agent 尚无 QUIC dial / accept / stream lifecycle 管理代码
- 测试矩阵、压测脚本、管理面展示尚未覆盖 QUIC

---

## 5. 目录与模块落点

### 5.1 `ltfp/`

主要新增或修改：

- `ltfp/transport/quicbinding/transport.go`
- `ltfp/transport/quicbinding/session.go`
- `ltfp/transport/quicbinding/control_channel.go`
- `ltfp/transport/quicbinding/tunnel.go`
- `ltfp/transport/quicbinding/tunnel_producer.go`
- `ltfp/transport/quicbinding/tunnel_acceptor.go`
- `ltfp/transport/quicbinding/*_test.go`
- `ltfp/Makefile`
- `ltfp/docs/TestMatrix.md`

### 5.2 `agent-core/`

主要新增或修改：

- `agent-core/runtime/agent/app/config.go`
- `agent-core/runtime/agent/app/bootstrap.go`
- `agent-core/runtime/agent/app/runtime_bridge.go`
- `agent-core/cmd/agent-core/main.go`
- Agent 侧 QUIC transport opener / producer 接入点
- Agent 相关单测和集成测试

### 5.3 `cloud-bridge/`

主要新增或修改：

- `cloud-bridge/runtime/bridge/app/config.go`
- `cloud-bridge/runtime/bridge/app/bootstrap.go`
- `cloud-bridge/runtime/bridge/app/control_plane.go`
- Bridge 侧 QUIC listener / accept loop / stream dispatch
- `cloud-bridge/config.example.yaml`
- Bridge 相关单测和集成测试

### 5.4 `docs/`

需要同步或新增：

- 本文档
- `docs/LTFP-TransportExecutionChecklist.md`
- `docs/Agent-and-Bridge-ExecutionChecklist.md`
- 必要时补充 QUIC 设计补充说明文档

---

## 6. 分阶段开发计划

## Phase Q0：方案冻结与最小 POC

### 目标

先验证 QUIC 连接模型在当前抽象下可行，再进入正式开发。

### 清单

- [x] 确认依赖选型为 `quic-go`
- [x] 输出 QUIC stream 映射说明：control stream / tunnel stream / reset / close_write
- [x] 完成最小 POC：
  - Agent 侧建立一条 QUIC connection
  - 打开 1 条 control stream
  - 连续打开多条 bidirectional stream
  - Bridge 侧成功 accept 并双向读写
- [x] 明确首版不做 0-RTT / datagram / session resume
- [x] 明确 QUIC 配置项与现有 `tls_mode` 的兼容策略

### 验收标准

- 已证明“一条 QUIC 连接 + 多条 stream”的模型可稳定运行
- 已证明 stream reset / close 能映射到 transport 抽象要求

## Phase Q1：`ltfp/transport/quicbinding` 落地

### 目标

在 `ltfp` 层实现独立可测的 QUIC binding。

### 清单

- [x] 新建 `ltfp/transport/quicbinding/` 目录
- [x] 实现 `TransportConfig`
  - TLS 配置
  - idle timeout
  - keepalive period
  - max incoming streams
  - stream open timeout
- [x] 实现 `QUICControlChannel`
- [x] 实现 `QUICTunnel`
- [x] 实现 `TunnelProducer`
- [x] 实现 `TunnelAcceptor`
- [x] 实现 `Session` 聚合根
- [x] 保证 `BindingInfo()` 返回 `quic_native`
- [x] 对齐 `KeepalivePolicy` 输出口径
- [x] 确保 QUIC 私有类型不泄露到 `transport` 公共接口

### 验收标准

- `quicbinding` 可独立通过单元测试
- 能力语义与 `grpcbinding / tcpbinding` 保持一致

## Phase Q2：Bridge 侧控制面与监听接入

### 目标

让 Bridge 能接受 QUIC session，并把 tunnel streams 纳入现有 session/tunnel registry。

### 清单

- [x] 在 `cloud-bridge` 配置中新增 QUIC 监听配置
- [x] 新增 QUIC listener 初始化和关闭逻辑
- [x] 新增 QUIC accept loop
- [x] 为每条 QUIC 连接建立 Bridge 侧 transport session
- [x] 固定 control stream 建立流程
- [x] 接收 Agent 打开的 tunnel stream 并写入 idle pool
- [x] 在握手阶段支持 `selected_binding=quic_native`
- [x] 保持现有 auth / heartbeat / publish / pool report 逻辑不变
- [x] 为 QUIC 连接和 stream 增加结构化日志

### 验收标准

- Bridge 可同时保留现有 `tcp_framed / grpc_h2` 能力
- QUIC 路径可完成 Hello/Auth/Heartbeat 闭环

## Phase Q3：Agent 侧 runtime 接入

### 目标

让 Agent 可通过配置切换到 `quic_native` 并稳定维持 tunnel pool。

### 清单

- [x] 放开 Agent `bridge_transport` 校验，支持 `quic_native`
- [x] 新增 QUIC dialer 与 TLS 配置加载
- [x] Agent session opener 支持 QUIC
- [x] 使用 control stream 完成握手与认证
- [x] tunnel maintainer 改为通过 QUIC bidirectional stream 建 tunnel
- [x] refill controller 在 QUIC 下复用现有节流与并发限制
- [x] 保证 Agent 上报的 `SupportedBindings` 可包含 `quic_native`
- [x] 补齐 runtime 单测

### 验收标准

- Agent 使用 `quic_native` 时可稳定建立 session
- idle tunnel 池可维持目标水位

## Phase Q4：数据面语义对齐与并发治理

### 目标

确保 QUIC 下的 traffic 行为与现有 binding 语义一致。

### 清单

- [x] 对齐 `TrafficOpen / OpenAck / Data / Close / Reset`
- [x] 对齐 deadline / cancel / reset 语义
- [x] 对齐 tunnel recycle 语义
- [x] 校验单 tunnel 单并发 traffic 约束
- [x] 校验 stream reset 后 tunnel 状态是否正确收敛到 `broken/closed`
- [x] 校验 idle tunnel 静默断开清理逻辑
- [x] 校验 control stream 与大流量数据流并存时 heartbeat 不被饿死

### 验收标准

- QUIC 下的控制面与数据面错误语义稳定
- 与现有 binding 不产生额外的 runtime 特判分叉

## Phase Q5：测试、观测与管理面补齐

### 目标

把 QUIC 纳入仓库既有测试矩阵与可观测体系。

### 清单

- [x] `ltfp/transport/quicbinding/*_test.go` 单测
- [x] `ltfp` parity 测试新增 QUIC
- [ ] 集成测试覆盖：
  - 握手
  - 认证
  - 心跳
  - pool refill
  - open/ack/close/recycle（app 层 HTTP ingress + connector path 已补齐 QUIC 变体）
  - late ack（app 层 open timeout drain 已补齐 QUIC 变体）
  - data / reset（HTTP ingress + connector path 已补齐 QUIC 变体；更广的跨协议/end-to-end 场景仍待补齐）
  - recycle
  - session stale
- [ ] 压测覆盖：
  - [x] 突发开 stream
  - [x] stream limit 打满
  - [ ] 弱网丢包
  - [x] 空闲超时
  - [x] 慢读回压
- [x] 增加 QUIC 维度指标
- [x] 管理台展示 `binding=quic_native`
- [x] 更新测试矩阵文档

### 验收标准

- QUIC 已进入自动化回归矩阵
- 关键故障模式具备指标和日志证据

## Phase Q6：灰度与发布

### 目标

安全上线，不影响现有 `grpc_h2 / tcp_framed` 路径。

### 清单

- [x] 通过配置开关启用 QUIC，不替换默认 binding
- [x] 提供回退方案：失败时可快速切回 `grpc_h2`
- [ ] 在开发环境完成单 Agent / 单 Bridge canary
- [ ] 在预发布环境完成弱网与长连稳定性验证
- [x] 输出发布说明、回退说明、运维检查项

### 验收标准

- QUIC 可灰度启用
- 回退路径简单明确

---

## 7. 按模块拆分的详细任务清单

## 7.1 `ltfp` 任务

- [x] 新增 QUIC binding 包骨架
- [x] 实现 QUIC connection 到 session 的聚合
- [x] 实现 stream 到 tunnel 的状态映射
- [x] 实现 QUIC control write/read queue
- [x] 实现 keepalive / idle timeout 配置归一化
- [x] 对齐 `BindingInfo.KeepalivePolicy`
- [x] 更新 `Makefile`：
  - [x] `test-binding`
  - [x] `test-parity`
  - [x] `test-pressure`
- [x] 更新 `ltfp/docs/TestMatrix.md`

## 7.2 `agent-core` 任务

- [x] 扩展配置校验枚举
- [x] 复用既有 `bridge_addr / bridge_tls / dial_timeout` 完成 QUIC 配置接线
- [x] QUIC session opener 接入
- [x] QUIC tunnel producer 接入
- [x] runtime diagnostics 增加 QUIC 字段
- [x] 补齐 QUIC 场景单测与集测

## 7.3 `cloud-bridge` 任务

- [x] 增加 QUIC 监听配置
- [x] 初始化 QUIC listener
- [x] 接入 QUIC 连接 accept loop
- [x] 控制面握手支持 `selected_binding=quic_native`
- [x] 接入 QUIC tunnel stream acceptor
- [x] registry / observability 与 QUIC 对齐
- [x] 补齐 Bridge 侧集成测试

## 7.4 文档与运维任务

- [x] 更新 transport 执行清单
- [x] 更新 Agent/Bridge 执行清单
- [x] 补充 QUIC 配置示例
- [x] 补充灰度与回退步骤
- [ ] 如形成新的长期约束，再回写 `AGENTS.md`

---

## 8. 关键设计点与风险清单

### 8.1 依赖变更风险

引入 `quic-go` 会修改 `go.mod / go.sum`。

说明：

- 该项属于仓库规则中的“依赖变更”，正式实施前需要明确确认
- 本文档先做计划，不直接改依赖

### 8.2 配置语义风险

现有 `control_plane.tls_mode` 有 `plaintext / optional / required`，
但 QUIC 天然依赖 TLS 1.3，不能直接照搬该语义。

处理原则：

- QUIC 分支单独定义监听与 TLS 必填项
- 不让“伪明文 QUIC”进入实现
- Bridge 当前实现仅在 `control_plane.tls_mode != plaintext` 且服务端 TLS 配置已就绪时启动 QUIC listener；默认明文控制面不会尝试拉起 QUIC 监听

当前状态：

- `cloud-bridge` 与 `agent-core` 已分别执行 `go mod tidy`，补齐了 `quic-go` 相关 transitive checksum
- `ltfp/transport/quicbinding` 当前已用真实 integration test 证明最小 POC 可行：客户端建立 QUIC connection、打开 control stream、继续打开多条 bidirectional tunnel stream，服务端可稳定 accept 并双向读写
- `ltfp/transport/quicbinding` 当前仅在 binding 私有目录内持有 `quic-go` 连接与 stream 类型；对 `transport` 公共层暴露的仍然是 `ControlChannel / Tunnel / TunnelProducer / TunnelAcceptor / Session` 统一接口
- `ltfp/transport/quicbinding/QUICControlChannel` 当前已通过 `readMutex / writeMutex` 和 fragment/reassemble 串行化 control 帧收发，首版不再额外引入独立 goroutine queue 抽象
- Agent 当前实现复用了现有 `bridge_addr / bridge_tls / control_channel.dial_timeout` 配置；`quic_native` 下额外约束为必须启用 `bridge_tls.enabled=true`
- `apps/dev-agent` 当前已放开 `bridge_transport=quic_native` 的 UI 选择与宿主配置校验；设置页除 `tcp_framed / grpc_h2 / quic_native` 外，还可直接保存 `bridge_tls.enabled / bridge_tls.root_ca_file / bridge_tls.server_name`，并提示 QUIC 依赖 Bridge TLS、QUIC 监听端口以及 Root CA（`managed_ca` 下需带外分发）
- `cloud-bridge` 当前已修复 QUIC listener 复用控制面通用 TLS 配置时的 ALPN 冲突：运行时会为 QUIC 分支克隆 TLS 配置并清空 `NextProtos`，交由 `ltfp/transport/quicbinding` 注入 `devbridge-ltfp-quic/v1`，避免 `tls: no application protocol`
- Bridge 管理台当前已放开 `control_plane.quic_listen_addr / tls_mode` 基础字段，以及 `tls_cert_source / tls_cert_file / tls_key_file / tls_ca_cert_file / tls_ca_key_file / tls_server_common_name / tls_server_san_dns / tls_server_san_ips / tls_server_cert_ttl_ms / tls_server_cert_renew_before_ms` 高级字段；后台可直接 staged 保存 QUIC 监听地址、TLS 模式和 Bridge 证书来源/签发参数到当前可编辑配置文件，其中首次切换到 `managed_ca` 时会自动补齐与配置文件同目录的默认 Root CA 路径以及本地联调 SAN，避免保存阶段立即因缺参失败；当最终生效配置为 `managed_ca + tls_mode!=plaintext`，且 CA 路径仍是默认同目录路径时，管理后台保存后还会立即初始化 Root CA 文件，避免用户还要额外手工触发一次 reload 才能拿到 `root-ca.crt / root-ca.key`；若手动指定自定义 CA 路径，则仍沿用 reload/启动阶段加载或初始化的语义，避免后台保存阶段因为越权写系统路径而失败；Bridge 启动和默认路径 Root CA 初始化时，控制台日志会显式打印 `ca_cert_directory / ca_cert_file / ca_key_directory / ca_key_file`，便于直接定位证书目录；前端配置页也会在 `managed_ca` 选中时显式提示这组自动补齐与 Root CA 初始化规则，并根据当前 `listen_addr / grpc_h2_listen_addr / quic_listen_addr` 动态推导预计写入的 SAN/CN；证书区会根据 `tls_cert_source` 只展示当前模式所需字段，并提示是否存在已修改但暂时隐藏的另一侧字段，切换模式时已填值不会丢失
- Agent 的 `agent.snapshot / session.snapshot / diagnose.snapshot` 已补齐 QUIC 维度，当前可观测到是否启用 QUIC、控制连接状态、tunnel producer 就绪态及本地/远端地址
- Bridge 当前已把 QUIC binding 写入 session/tunnel registry，并通过 adminview/admin API 暴露 connector/session/tunnel 的 `binding=quic_native` 与 QUIC listener 概览；管理台前端已在 Session/Tunnel 列表与详情中展示 QUIC binding
- Bridge 当前要求每条 QUIC 连接必须先在控制流上完成 `Hello -> Auth -> AuthAck`，随后才会启动 tunnel stream accept loop；未认证连接即使提前打开 data stream，也不会进入 session/tunnel registry
- Bridge 当前已为每条已认证 QUIC 连接建立 `quicbinding.SessionRoleServer` transport session，并在连接关闭/失败时同步推进 `authenticated -> closed/failed` 状态，结构化日志可直接看到 `session_state / protocol_state`
- Bridge 当前对“等待首个 QUIC control stream”显式施加超时，避免客户端只完成 QUIC/TLS 建连却长期不打开控制流，导致未认证连接长期占用 goroutine 与连接资源
- Bridge 当前已补齐 QUIC 关键路径结构化日志，覆盖 listener 启动、连接接入、control stream 就绪、认证完成、tunnel accept loop 启动以及 QUIC tunnel 入库，排障时可直接按 `peer_addr / connector_id / session_id / session_epoch / registered_tunnel_id` 关联
- Bridge 当前已补齐 QUIC 维度指标，覆盖 `quic_connection_accept_total / quic_connection_active / quic_connection_authenticated_total / quic_tunnel_registered_total`，并通过 adminview / admin API / SSE 只读链路暴露
- Bridge 侧现有 QUIC 集成覆盖已包含握手、认证、transport heartbeat 刷新 session、`TunnelPoolReport -> TunnelRefillRequest`、idle tunnel recycle、session stale sweep、admin API 只读链路对 `binding=quic_native` / QUIC listener 的读取，以及 app 层 HTTP ingress + connector path 下的 `open/ack/close/recycle`、`open timeout + late ack drain`、`data/reset` QUIC 变体；更广的跨协议 end-to-end 语义与压测场景仍待补齐
- Bridge connector path 现已补齐 QUIC 下的“单 tunnel 单并发 traffic”约束回归，验证 active 中的 quic tunnel 不会被第二条 traffic 并发复用；控制面也补了真实 QUIC control+tunnel 压载场景，验证单条大流量 data stream 持续写入时 heartbeat pong 仍能按时返回
- `ltfp` 当前已把 QUIC 纳入 parity smoke：`grpc_h2 / tcp_framed / quic_native` 共用同一组 `open/ack/data/close` 与 `open/ack/data/reset` 场景断言；`make test-binding` 也已纳入 `transport/quicbinding`
- `ltfp` 的 QUIC session / tunnel 现在都显式断言 `BindingInfo().KeepalivePolicy == DefaultKeepalivePolicyForBinding(quic_native)`，避免后续回归把保活策略默默漂移
- `ltfp/transport/quicbinding/tunnel_test.go` 已补齐 tunnel 级 QUIC 语义回归，覆盖 `read deadline` 超时不破坏 tunnel、本地 reset、对端 stream reset 后状态收敛到 `broken`、idle peer close 后清理收敛到 `closed`，以及 `CloseWrite -> EOF`
- `ltfp` 的 `make test-pressure` 现已纳入 QUIC benchmark smoke，覆盖 `TunnelSmallPayload / TunnelLargePayload / TunnelIdleDeadline / BurstRefill / StreamLimitSaturation / BurstOpenStreams / TunnelSlowReadBackpressure`；同时 `transport/quicbinding` 已补齐 `TestTunnelProducerRespectsMaxIncomingStreams`，验证控制流 + 单条数据流占满 stream 配额时额外开流会按预期超时，释放后可恢复；运行时会出现一条 `quic-go` 的本地 UDP buffer 提示，但 benchmark 已正常通过
- 2026-03-28 已完成一次真实本地端到端联调：Bridge 以 `tls_mode=required + quic_listen_addr=:49183` 启动，Agent 通过 `bridge_transport=quic_native + bridge_tls.*` 建连；Bridge 日志已出现 `bridge quic control channel ready / connection authenticated / transport session opened / tunnel accept loop started`，Agent 本地 `diagnose.snapshot` 也已确认 `state=ACTIVE`、`quic.connected=true`、`quic.tunnel_producer_ready=true`

### 8.3 UDP 网络风险

QUIC 基于 UDP，需额外验证：

- 防火墙
- Kubernetes / 容器网络
- 云 LB / NAT 行为
- UDP 空闲回收

### 8.4 stream 配额风险

如果 `MaxIncomingStreams` 太低，会导致：

- tunnel refill 看似成功但实际建池不足
- 高并发时 Acquire 退化为超时

### 8.5 控制面干扰风险

虽然 control stream 与 tunnel stream 在逻辑上分离，但它们共享同一 QUIC
connection，仍需重点验证：

- heartbeat 延迟
- 大流量下控制消息优先级
- reset 风暴对控制流的影响

---

## 9. 验证计划

### 9.1 最小必要验证顺序

1. `ltfp` binding 单测
2. `ltfp` parity / integration
3. `agent-core` 局部测试
4. `cloud-bridge` 局部测试
5. Agent / Bridge 端到端联调
6. 压测与弱网验证

### 9.2 建议执行命令

按仓库现有模块边界执行：

```bash
cd ltfp && go test ./...
cd agent-core && go test ./...
cd cloud-bridge && go test ./...
```

如补齐 `Makefile` 后，优先使用：

```bash
cd ltfp && make test-binding
cd ltfp && make test-parity
cd ltfp && make test-pressure
```

### 9.3 必测场景

- QUIC 握手失败
- 证书校验失败
- control stream 建立成功但 tunnel stream 打开失败
- 大量 idle tunnel 建立与回收
- `TrafficOpenAck` 超时与迟到
- tunnel recycle 失败
- session stale / draining
- Bridge 重启后 Agent 重连

### 9.4 QUIC 配置示例

以下示例用于本地或开发环境联调，基于当前已接线的真实字段整理，
不直接修改仓库默认配置文件。

Bridge 侧最小 QUIC 配置示例：

```yaml
admin:
  listen_addr: ":39080"
control_plane:
  listen_addr: ":39081"
  grpc_h2_listen_addr: ":39082"
  quic_listen_addr: ":39083"
  heartbeat_timeout: "30s"
  tls_mode: "required"
  tls_cert_file: "/etc/devbridge/bridge-server.crt"
  tls_key_file: "/etc/devbridge/bridge-server.key"
```

说明：

- 仓库当前默认端口顺序为：`admin=39080`、`tcp_framed=39081`、`grpc_h2=39082`、`quic_native=39083`
- `quic_listen_addr` 必须与 `listen_addr`、`grpc_h2_listen_addr` 不同
- `tls_mode` 不能为 `plaintext`，否则 QUIC listener 不会启动
- `tls_cert_file` / `tls_key_file` 必须与 Agent 侧 `bridge_tls` 校验链匹配

Agent 侧最小 QUIC 启动示例：

```bash
export DEV_AGENT_CFG_AGENT_ID="agent-quic-canary-1"
export DEV_AGENT_CFG_BRIDGE_ADDR="127.0.0.1:39083"
export DEV_AGENT_CFG_BRIDGE_TRANSPORT="quic_native"
export DEV_AGENT_CFG_BRIDGE_TLS_ENABLED="true"
export DEV_AGENT_CFG_BRIDGE_TLS_ROOT_CA_FILE="/etc/devbridge/root-ca.crt"
export DEV_AGENT_CFG_BRIDGE_TLS_SERVER_NAME="bridge.internal.example"
export DEV_AGENT_CFG_BRIDGE_AUTH_METHOD="token"
export DEV_AGENT_CFG_BRIDGE_AUTH_TOKEN="dbt_agent-quic-canary-1.agent-dev-secret"

cd agent-core && go run ./cmd/agent-core
```

说明：

- `DEV_AGENT_CFG_BRIDGE_ADDR` 必须指向 Bridge 的 `quic_listen_addr`
- `DEV_AGENT_CFG_BRIDGE_TRANSPORT=quic_native` 是启用 QUIC 的显式开关
- `quic_native` 下必须同时设置 `DEV_AGENT_CFG_BRIDGE_TLS_ENABLED=true`
- 若未显式提供 `DEV_AGENT_CFG_BRIDGE_TLS_SERVER_NAME`，Agent 会回退到 `bridge_addr` 的 host 做证书校验；开发环境建议显式填写，避免 SAN 不匹配

默认关闭说明：

- Agent 默认 `bridge_transport=tcp_framed`，默认 `bridge_addr=127.0.0.1:39081`
- `cloud-bridge/config.example.yaml` 默认 `control_plane.tls_mode=plaintext`
- 因此仓库默认运行路径不会自动切到 QUIC，只有显式改配置才会启用

### 9.5 灰度、回退与运维检查项

推荐灰度步骤：

1. 先保持 Bridge 现网主路径不变，仅为 Bridge 补齐 `quic_listen_addr`、TLS 证书和 UDP 端口放通。
2. 确认管理面或只读接口已出现 `control_plane_quic` listener，再挑选单个 Agent canary。
3. 仅对 canary Agent 切换 `DEV_AGENT_CFG_BRIDGE_TRANSPORT=quic_native`，并把 `DEV_AGENT_CFG_BRIDGE_ADDR` 指向 QUIC 端口。
4. 观察 `session/tunnel binding=quic_native`、心跳刷新、idle pool refill、实际业务流量闭环是否正常。
5. 若 canary 稳定，再按批次逐步放大；每批次只改 Agent 侧 transport，Bridge 维持多 binding 并行接入。

快速回退步骤：

1. 将目标 Agent 的 `DEV_AGENT_CFG_BRIDGE_TRANSPORT` 从 `quic_native` 改回 `grpc_h2`。
2. 将 `DEV_AGENT_CFG_BRIDGE_ADDR` 改回 Bridge 的 `grpc_h2_listen_addr`。
3. 保留 `bridge_tls` 相关参数不变，重启 Agent，使其重新通过 gRPC H2 建连。
4. 确认管理面中新 session/tunnel 的 `binding` 已回到 `grpc_h2`，且 QUIC 会话自然收敛退出。

说明：

- 最快回退路径是“只切 Agent，不重配 Bridge”
- Bridge 可以继续保留 QUIC listener，但不再分配新的 QUIC Agent
- 若确认后续完全停用 QUIC，再安排单独窗口回收 Bridge 的 QUIC 监听与 UDP 端口策略

运维检查项：

- UDP 端口、宿主机防火墙、Kubernetes Service / LB、云侧安全组已放通 `quic_listen_addr`
- Bridge 证书 SAN 与 Agent 使用的 `bridge_tls.server_name` 一致
- 管理面或只读 API 中可看到 `control_plane_quic` listener
- canary Agent 建连后，session / tunnel 记录出现 `binding=quic_native`
- 心跳、`TunnelPoolReport -> TunnelRefillRequest`、open/ack/data/close 主链路可闭环
- 未出现持续性的 heartbeat timeout、open timeout、late ack 异常抬升
- 若压测日志持续出现 `quic-go` UDP buffer 警告，需要联动宿主机内核参数做缓冲区调优

---

## 10. 交付物清单

完成 QUIC 首版接入时，应至少交付：

- [ ] `ltfp/transport/quicbinding/` 完整实现
- [x] Agent QUIC 接入代码
- [x] Bridge QUIC 接入代码
- [x] QUIC 配置样例
- [ ] 自动化测试补齐
- [x] 测试矩阵更新
- [x] 发布 / 回退说明
- [x] 管理面与日志指标对齐

---

## 11. 完成定义

满足以下条件，才可视为本任务完成：

1. `quic_native` binding 已可编译、可联调
2. Agent 与 Bridge 可通过 QUIC 建立稳定 session
3. tunnel pool 与 traffic 主链路可闭环
4. 自动化测试与关键压测通过
5. 文档、配置样例、回退说明已同步
6. 默认路径仍可回退到现有 `grpc_h2`

---

## 12. 建议执行顺序

建议按以下顺序推进，避免大面积返工：

1. 先做 Q0，冻结连接模型与配置语义
2. 再做 Q1，把 `ltfp` binding 打磨到可独立测试
3. 然后 Bridge 侧接入，再接 Agent 侧
4. 最后补齐测试矩阵、管理面展示与灰度发布

这样可以把高风险问题尽量前置在 transport 层解决，减少业务 runtime 被迫跟着改。

---

## 13. 更新时间

- 版本：v0.3
- 日期：2026-03-28
- 状态：in_progress
