# DevBridge Loop 开发计划（LTFP v2.1 对齐版）

## 一、计划目标

本计划用于将 [LTFP-v1-Draft.md](./LTFP-v1-Draft.md) 落地到当前代码库，形成一条可分阶段交付的实现路径。

本期总目标：

1. 将 `agent-core` 与 `cloud-bridge` 收口到同一套控制面/数据面语义。
2. 先跑通 server 侧 route resolve 的核心路径，再逐步补齐 external direct proxy、export、hybrid。
3. 保持 tunnel 传输层可插拔，优先复用当前已存在的 `http/masque` 基线，协议语义按 LTFP v2.1 收敛。
4. 输出可实现、可验收、可切分提交的阶段计划，指导后续编码、联调和测试。

---

## 二、实施前提

### 1. 本计划以当前仓库为基线

当前主要实现落点：

- `agent-core/`
- `cloud-bridge/`
- `apps/dev-agent/`（桌面壳，不作为本计划主阻塞项）

本计划优先推动：

- `agent-core` 的注册、健康、tunnel、forwarder、backflow 能力
- `cloud-bridge` 的 session、route resolve、ingress、direct proxy、discovery 能力
- 协议模型、错误码、测试矩阵、文档的一致性

### 2. 当前与目标状态的关系

LTFP v2.1 是目标协议模型；当前代码里已经存在部分先行实现。

因此本计划分两类工作：

- `语义收口`：把现有实现调整到 LTFP 定义的行为上
- `能力补齐`：实现当前文档已冻结、但代码尚未完整具备的功能

### 3. 核心实施原则

- v2.1 核心路径中，`route match / resolve` 在 server 侧完成。
- `connector_service` 与 `external_service` 共享 route 抽象，但数据路径严格分离。
- `service_key` 是 lookup key，`service_id` 是 identity key；二者映射规则必须稳定。
- 所有资源级控制消息都必须具备 `session_epoch + event_id + resource_version` 语义。
- `1 traffic = 1 原生数据流`，禁止在单条大流上做应用层二次复用。
- 当前先复用已存在的 tunnel binding；等语义稳定后再补齐更标准的 binding 抽象。

---

## 三、本期范围

### 1. 必须交付

- connector 接入、握手、认证、心跳、重连
- `PublishService / UnpublishService / ServiceHealthReport`
- session 代际与资源级幂等
- server 侧 route resolve
- connector proxy path
- ingress 三分层：`l7_shared`、`tls_sni_shared`、`l4_dedicated_port`
- external discovery import + direct proxy
- export 基础能力
- hybrid `pre_open_only` fallback
- 管理接口、状态接口、诊断与关键测试

### 2. 可选扩展

- agent 侧 route cache / `RouteAssign` 同步
- 更标准的 `grpc_h2` / `quic_native` binding
- datagram 预研
- `apps/dev-agent` 的可视化增强

### 3. 明确不做

- 跨 server 集群一致性
- session resume
- mid-stream failover
- 跨 scope 引用
- 完整多租户与 RBAC
- 全量鉴权体系

---

## 四、工作流拆分

### A. 协议与领域模型

目标：冻结消息模型、错误码、状态机、资源关系。

主要目录：

- `agent-core/internal/domain`
- `cloud-bridge/internal/domain`
- `docs/LTFP-v1-Draft.md`

### B. Agent 控制面与运行态

目标：维护本地服务视图、健康视图、session、tunnel 上报与回流转发。

主要目录：

- `agent-core/internal/store`
- `agent-core/internal/tunnel`
- `agent-core/internal/httpapi`
- `agent-core/internal/forwarder`
- `agent-core/internal/app`

### C. Bridge 控制面与路由

目标：接入 connector、维护 canonical/runtime state、完成 ingress 和 route resolve。

主要目录：

- `cloud-bridge/internal/store`
- `cloud-bridge/internal/httpapi`
- `cloud-bridge/internal/routing`
- `cloud-bridge/internal/app`

### D. Discovery 与 Direct Proxy

目标：打通 external service 查询、缓存、选 endpoint、直连代理。

主要目录：

- `cloud-bridge/internal/discovery`
- `cloud-bridge/internal/upstream`

### E. 质量与验证

目标：覆盖幂等、乱序、断线恢复、协议分层、错误分类和端到端链路。

主要目录：

- `agent-core/internal/**/*_test.go`
- `cloud-bridge/internal/**/*_test.go`
- `examples/`
- `docs/`

---

## 五、阶段计划

## M0. 协议收口与代码基线对齐

目标：把文档、领域模型、错误码、目录职责先收成一版，避免后续边开发边改语义。

主要任务：

- 对齐 `service_id / service_key / session_epoch / resource_version / event_id` 的字段语义。
- 对齐 `PublishServiceAck / UnpublishServiceAck / RouteAssignAck / RouteRevokeAck` 契约。
- 对齐 `TrafficOpen / TrafficOpenAck / TrafficClose / TrafficReset` 语义。
- 在 `agent-core` 与 `cloud-bridge` 各自 domain 层补齐缺失对象和错误码。
- 冻结 v2.1 核心路径不需要 `RouteAssign/RouteRevoke` 的约束。
- 补一版测试矩阵文档：控制面、数据面、ingress、discovery、fallback。

交付物：

- 对齐后的 `docs/LTFP-v1-Draft.md`
- 对齐后的 `docs/ProjectDevelopmentPlan.md`
- 双端统一的 domain 模型与错误码草案

完成标准：

- 文档中的核心对象都能在代码中找到对应结构。
- 不再存在“正文一套、proto 一套、代码一套”的冲突。

---

## M1. 控制面一致性与握手治理

目标：先把 session 生命周期、幂等 ACK、重连恢复这条主链路做稳。

主要任务：

### Agent 侧

- 在 `manager.go` 收口 `HELLO -> AUTH -> HEARTBEAT -> FULL_SYNC` 流程。
- 按 v2.1 规则持有并传播最终权威 `session_epoch`。
- 对所有资源级事件分配 `event_id` 与 `resource_version`。
- 实现断线重连后的全量重同步。

### Bridge 侧

- 在 session store 中实现同一 connector 的 epoch 递增与旧会话拒绝。
- 在 sync processor 中实现资源级幂等去重。
- 对重复 publish/unpublish 返回成功语义，但不污染当前状态。
- 回填 `accepted_resource_version / current_resource_version`。

### 测试

- 旧 epoch 晚到事件被拒绝。
- 同一 `event_id` 重放不产生副作用。
- 重连后全量重同步能恢复一致状态。
- `http` 与 `masque` 下握手行为一致。

交付物：

- 稳定的 tunnel handshake 与 full-sync 路径
- 资源级 ACK 契约实现
- 重连恢复测试集

完成标准：

- agent 异常重连后，bridge 状态最终一致。
- 重复事件不会产生重复资源或错误摘除。

---

## M2. Connector Service 核心闭环

目标：打通最小可用主链路，即 connector 发布服务后，可由 bridge 通过 tunnel 回流到 agent 本地 upstream。

主要任务：

### Agent 侧

- 完成 `PublishService / UnpublishService / ServiceHealthReport` 的实际发送。
- 实现本地 `ServiceEndpoint` 选择器。
- 完成 `TrafficOpen` 收到后的 dial、转发、关闭、reset 处理。
- 区分 pre-open 错误与 post-open 错误。

### Bridge 侧

- 建立 canonical service/session registry。
- 实现 `connector_service` 的 route resolve。
- 只把 `service_id` 与非权威 `endpoint_selection_hint` 放入 `TrafficOpen`。
- 补齐 runtime traffic registry 与 trace 关联。

### 测试

- 单 service 单 endpoint 回流成功。
- 单 service 多 endpoint 时由 agent 完成最终 endpoint 选择。
- upstream 不可达、超时、reset 的错误分类正确。
- 一个 traffic 对应一个独立数据流，不做二次复用。

交付物：

- connector proxy path MVP
- end-to-end 回流用例
- runtime traffic 状态可查询

完成标准：

- 外部请求经 bridge 命中 `connector_service` 后，可稳定转发到 agent 本地服务。
- `TrafficOpenAck success` 之前的失败与之后的失败可被系统正确区分。

---

## M3. Ingress 分层与专属端口入口

目标：按 LTFP 的三类 ingress 完成入口建模，并补齐裸 TCP/L4 的专属端口能力。

主要任务：

- 实现 `l7_shared` 的 Host / authority / path 路由。
- 实现 `tls_sni_shared` 的 SNI 路由和错误分类。
- 实现 `l4_dedicated_port` 的监听器生命周期管理。
- 将 dedicated port 与 route/service/exposure 建立可追踪映射。
- 补齐端口冲突检测、配置校验、重建与热更新。
- 补齐 dedicated port 的运行态状态查询。

和当前产品线对齐时，可将“端口映射入口”视为 `l4_dedicated_port` 的一类具体落地。

测试重点：

- Host 路由、SNI 路由、listen port 路由互不串扰。
- 裸 TCP 服务必须一服务一端口。
- 监听端口冲突时 bridge 启动失败或局部 listener 拒绝生效。

交付物：

- ingress 三分层实现
- dedicated port listener 管理
- 配置校验与运行态诊断

完成标准：

- HTTP、TLS-SNI、L4 三类入口都可独立验证。
- dedicated port 可稳定映射到目标 service。

---

## M4. External Service 与 Direct Proxy

目标：让 `external_service` 路径完全脱离 agent，形成 server 自己查、自己连、自己转发的独立链路。

主要任务：

- 定义 `DiscoveryProvider` 抽象。
- 实现基础缓存模型：`cache_first / refresh_on_miss / stale_if_error`。
- 对接至少一种 provider（建议先从 mock 或现有最小 provider 开始）。
- 在 bridge 侧实现 endpoint 过滤、选路、dial、超时、错误分类。
- 实现 allowlist / denylist / 并发限制 / timeout 等安全约束。
- 让 runtime traffic registry 区分 connector proxy traffic 与 direct proxy traffic。

测试重点：

- `external_service` 不会向 agent 发送 `TrafficOpen`。
- provider miss、cache stale、endpoint unhealthy、dial failure 的错误分类准确。
- external 路径和 connector 路径不会互相污染状态。

交付物：

- discovery import + direct proxy MVP
- external route resolve 与 upstream client
- 基础 provider 测试集

完成标准：

- route 命中 `external_service` 时，bridge 可独立完成代理。
- direct proxy 路径不依赖 agent 在线。

---

## M5. 健康检查、Export 与管理接口

目标：补齐服务可用性判定与第三方公开投影视图。

主要任务：

### Agent 侧

- 实现 endpoint 粒度健康探测。
- 聚合成 service 粒度健康状态，并通过 `ServiceHealthReport` 上报。
- 区分 `HEALTHY / UNHEALTHY / UNKNOWN`。

### Bridge 侧

- 维护 connector service 的 server-side lifecycle `status`。
- 聚合 `status + health_status + session state` 决定是否可 export。
- 实现 export endpoint 生成逻辑，区分三类 ingress。
- 输出 admin/state API：service、session、route、traffic、error、health。

测试重点：

- server 不主动探测 agent 本地 upstream。
- export 导出的是 server reachable address，而不是 agent 本地地址。
- 只有 `ACTIVE + HEALTHY + allow_export + valid session` 才允许 export。

交付物：

- service health 路径
- export reconciler MVP
- 管理接口与状态接口

完成标准：

- Export 行为与文档规则一致。
- 健康状态变化会影响 route resolve 与 export 决策。

---

## M6. Hybrid Route 与 `pre_open_only` fallback

目标：实现 `hybrid_group` 的严格边界，不把 fallback 做成隐式重试黑洞。

主要任务：

- 实现 `primary_connector_service + fallback_external_service` 模型。
- 只允许在 `TrafficOpenAck success` 之前 fallback。
- 将 `route resolve miss / service unavailable / open ack fail / pre-open timeout` 视为可 fallback 点。
- 明确禁止 post-open fallback。
- 补齐审计字段，记录 fallback 原因与选中路径。

测试重点：

- 收到 `TrafficOpenAck success` 后绝不 fallback。
- 已写出任意业务字节后绝不 fallback。
- partial response / mid-stream reset 不触发 fallback。

交付物：

- hybrid resolver
- pre-open fallback 测试集
- fallback 审计与诊断输出

完成标准：

- fallback 边界与 LTFP 完全一致。
- 不会对有副作用请求做 post-open 重试。

---

## M7. Binding 抽象升级与传输协议扩展

目标：在核心语义稳定后，再处理绑定层统一和后续协议扩展。

主要任务：

- 将当前 `http/masque` tunnel 路径继续抽象到统一 transport binding 接口。
- 明确 control plane 与 data plane 对 binding 的能力要求。
- 验证 `http`、`masque` 两种 binding 下的控制面/数据面语义一致性。
- 评估是否引入 `grpc_h2` 或 `quic_native` 作为下一版标准 binding。
- 为 datagram 和 future binding 预留能力位，但不纳入首版交付。

测试重点：

- 连接建立、心跳、重连、回流、错误码在不同 binding 下保持同语义。
- transport 切换不影响上层 route / service / traffic 模型。

交付物：

- transport binding 抽象
- binding parity 测试
- 下一版 binding 演进建议

完成标准：

- 上层逻辑不再依赖某一种特定 tunnel 实现细节。
- `http/masque` 至少完成语义等价验证。

---

## 六、并行实施建议

### 流 1. 协议与领域模型流

优先级最高，负责：

- domain struct
- 错误码
- ACK 契约
- docs/proto 对齐

### 流 2. Agent 流

负责：

- session 与 tunnel manager
- forwarder
- health report
- backflow handler

### 流 3. Bridge 流

负责：

- session store
- route resolve
- ingress
- runtime/canonical registry

### 流 4. Discovery 与 Direct Proxy 流

负责：

- provider
- cache
- upstream client
- external route tests

### 流 5. 测试与联调流

负责：

- 幂等/重连测试
- e2e demo
- 压测
- 文档回写

并行原则：

- `M0/M1` 不建议并行拆得太散，先统一协议语义。
- 从 `M2` 开始，agent 与 bridge 可并行推进。
- `M4/M5/M6` 可在 `M2/M3` 稳定后部分并行。

---

## 七、推荐提交顺序

建议按以下顺序拆 PR：

1. domain / error code / docs 对齐
2. handshake / session_epoch / ACK 契约
3. publish/unpublish/full-sync 幂等
4. connector proxy path MVP
5. ingress 三分层
6. dedicated port / port mapping
7. external discovery + direct proxy
8. health + export
9. hybrid fallback
10. transport binding 抽象与 parity

---

## 八、阶段验收门槛

### 1. 协议验收

- 资源级消息具备幂等和版本语义
- 握手期 `session_epoch` 权威规则一致
- `service_id / service_key` 规则一致

### 2. 功能验收

- connector published service 可稳定回流
- external service 可由 bridge 独立代理
- dedicated port 可稳定提供 L4 入口
- hybrid fallback 行为符合 `pre_open_only`
- export 仅在允许条件下发生

### 3. 工程验收

- 核心模块均有单元测试
- 关键链路具备集成测试
- 关键错误具备可观察输出
- 文档、配置、代码字段命名一致

### 4. 非功能验收

- 重连恢复时间可量化
- 重复事件不会放大状态污染
- ingress 错误分类可观测
- direct proxy 与 connector proxy 的运行态可区分

---

## 九、风险与前置约束

### 1. 最大风险

- 现有代码里的先行语义与 LTFP v2.1 不完全一致
- 过早做 binding 扩展会放大协议未冻结的问题
- external discovery 接入如果一开始就对真实 provider，调试成本会很高

### 2. 建议控制方式

- 先做 `M0/M1`，再做功能扩展
- 先用 mock provider 跑通 direct proxy，再接真实 provider
- 先锁定 v2.1 核心路径，不把 edge route sync 混进首批闭环
- 所有阶段都要求文档和测试同步更新

---

## 十、建议的下一步

在本计划基础上，建议立刻补两份文档：

1. 将 [ProjectDevelopmentChecklist.md](./ProjectDevelopmentChecklist.md) 按本计划同步改写为阶段任务清单。
2. 将 LTFP 里的 proto 草案拆成实际 `.proto` 文件或 `domain` 结构草案，作为编码入口。
