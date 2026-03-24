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
- [ ] 输出 QUIC stream 映射说明：control stream / tunnel stream / reset / close_write
- [ ] 完成最小 POC：
  - Agent 侧建立一条 QUIC connection
  - 打开 1 条 control stream
  - 连续打开多条 bidirectional stream
  - Bridge 侧成功 accept 并双向读写
- [ ] 明确首版不做 0-RTT / datagram / session resume
- [ ] 明确 QUIC 配置项与现有 `tls_mode` 的兼容策略

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
- [ ] 对齐 `KeepalivePolicy` 输出口径
- [ ] 确保 QUIC 私有类型不泄露到 `transport` 公共接口

### 验收标准

- `quicbinding` 可独立通过单元测试
- 能力语义与 `grpcbinding / tcpbinding` 保持一致

## Phase Q2：Bridge 侧控制面与监听接入

### 目标

让 Bridge 能接受 QUIC session，并把 tunnel streams 纳入现有 session/tunnel registry。

### 清单

- [x] 在 `cloud-bridge` 配置中新增 QUIC 监听配置
- [ ] 新增 QUIC listener 初始化和关闭逻辑
- [ ] 新增 QUIC accept loop
- [ ] 为每条 QUIC 连接建立 Bridge 侧 transport session
- [ ] 固定 control stream 建立流程
- [ ] 接收 Agent 打开的 tunnel stream 并写入 idle pool
- [ ] 在握手阶段支持 `selected_binding=quic_native`
- [ ] 保持现有 auth / heartbeat / publish / pool report 逻辑不变
- [ ] 为 QUIC 连接和 stream 增加结构化日志

### 验收标准

- Bridge 可同时保留现有 `tcp_framed / grpc_h2` 能力
- QUIC 路径可完成 Hello/Auth/Heartbeat 闭环

## Phase Q3：Agent 侧 runtime 接入

### 目标

让 Agent 可通过配置切换到 `quic_native` 并稳定维持 tunnel pool。

### 清单

- [x] 放开 Agent `bridge_transport` 校验，支持 `quic_native`
- [ ] 新增 QUIC dialer 与 TLS 配置加载
- [ ] Agent session opener 支持 QUIC
- [ ] 使用 control stream 完成握手与认证
- [ ] tunnel maintainer 改为通过 QUIC bidirectional stream 建 tunnel
- [ ] refill controller 在 QUIC 下复用现有节流与并发限制
- [ ] 保证 Agent 上报的 `SupportedBindings` 可包含 `quic_native`
- [ ] 补齐 runtime 单测

### 验收标准

- Agent 使用 `quic_native` 时可稳定建立 session
- idle tunnel 池可维持目标水位

## Phase Q4：数据面语义对齐与并发治理

### 目标

确保 QUIC 下的 traffic 行为与现有 binding 语义一致。

### 清单

- [ ] 对齐 `TrafficOpen / OpenAck / Data / Close / Reset`
- [ ] 对齐 deadline / cancel / reset 语义
- [ ] 对齐 tunnel recycle 语义
- [ ] 校验单 tunnel 单并发 traffic 约束
- [ ] 校验 stream reset 后 tunnel 状态是否正确收敛到 `broken/closed`
- [ ] 校验 idle tunnel 静默断开清理逻辑
- [ ] 校验 control stream 与大流量数据流并存时 heartbeat 不被饿死

### 验收标准

- QUIC 下的控制面与数据面错误语义稳定
- 与现有 binding 不产生额外的 runtime 特判分叉

## Phase Q5：测试、观测与管理面补齐

### 目标

把 QUIC 纳入仓库既有测试矩阵与可观测体系。

### 清单

- [x] `ltfp/transport/quicbinding/*_test.go` 单测
- [ ] `ltfp` parity 测试新增 QUIC
- [ ] 集成测试覆盖：
  - 握手
  - 认证
  - 心跳
  - pool refill
  - open/ack/data/close/reset
  - late ack
  - recycle
  - session stale
- [ ] 压测覆盖：
  - 突发开 stream
  - stream limit 打满
  - 弱网丢包
  - 空闲超时
  - 慢读回压
- [ ] 增加 QUIC 维度指标
- [ ] 管理台展示 `binding=quic_native`
- [ ] 更新测试矩阵文档

### 验收标准

- QUIC 已进入自动化回归矩阵
- 关键故障模式具备指标和日志证据

## Phase Q6：灰度与发布

### 目标

安全上线，不影响现有 `grpc_h2 / tcp_framed` 路径。

### 清单

- [ ] 通过配置开关启用 QUIC，不替换默认 binding
- [ ] 提供回退方案：失败时可快速切回 `grpc_h2`
- [ ] 在开发环境完成单 Agent / 单 Bridge canary
- [ ] 在预发布环境完成弱网与长连稳定性验证
- [ ] 输出发布说明、回退说明、运维检查项

### 验收标准

- QUIC 可灰度启用
- 回退路径简单明确

---

## 7. 按模块拆分的详细任务清单

## 7.1 `ltfp` 任务

- [x] 新增 QUIC binding 包骨架
- [x] 实现 QUIC connection 到 session 的聚合
- [x] 实现 stream 到 tunnel 的状态映射
- [ ] 实现 QUIC control write/read queue
- [x] 实现 keepalive / idle timeout 配置归一化
- [ ] 对齐 `BindingInfo.KeepalivePolicy`
- [ ] 更新 `Makefile`：
  - `test-binding`
  - `test-parity`
  - `test-pressure`
- [ ] 更新 `ltfp/docs/TestMatrix.md`

## 7.2 `agent-core` 任务

- [ ] 扩展配置校验枚举
- [ ] 增加 QUIC 地址 / TLS / ServerName / timeout 配置
- [ ] QUIC session opener 接入
- [ ] QUIC tunnel producer 接入
- [ ] runtime diagnostics 增加 QUIC 字段
- [ ] 补齐 QUIC 场景单测与集测

## 7.3 `cloud-bridge` 任务

- [ ] 增加 QUIC 监听配置
- [ ] 初始化 QUIC listener
- [ ] 接入 QUIC 连接 accept loop
- [ ] 控制面握手支持 `selected_binding=quic_native`
- [ ] 接入 QUIC tunnel stream acceptor
- [ ] registry / observability 与 QUIC 对齐
- [ ] 补齐 Bridge 侧集成测试

## 7.4 文档与运维任务

- [ ] 更新 transport 执行清单
- [ ] 更新 Agent/Bridge 执行清单
- [ ] 补充 QUIC 配置示例
- [ ] 补充灰度与回退步骤
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

---

## 10. 交付物清单

完成 QUIC 首版接入时，应至少交付：

- [ ] `ltfp/transport/quicbinding/` 完整实现
- [ ] Agent QUIC 接入代码
- [ ] Bridge QUIC 接入代码
- [ ] QUIC 配置样例
- [ ] 自动化测试补齐
- [ ] 测试矩阵更新
- [ ] 发布 / 回退说明
- [ ] 管理面与日志指标对齐

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

- 版本：v0.1
- 日期：2026-03-24
- 状态：in_progress
