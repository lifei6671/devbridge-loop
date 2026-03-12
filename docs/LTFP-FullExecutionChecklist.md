# DevBridge Loop LTFP 全量执行清单（v2.1 落地版）

## 一、文档目的

本清单用于承接 [LTFP-v1-Draft.md](./LTFP-v1-Draft.md) 与 [ProjectDevelopmentPlan.md](./ProjectDevelopmentPlan.md)，将 LTFP v2.1 从“协议草案”推进到“可编码、可联调、可验收、可发布”的落地执行项。

本文档与 [ProjectDevelopmentChecklist.md](./ProjectDevelopmentChecklist.md) 的关系如下：

- `ProjectDevelopmentChecklist.md` 只承载“共享协议库迁移”工作。
- 本文档承载 LTFP 全量落地工作，包括共享协议库、agent、bridge、ingress、discovery、direct proxy、export、hybrid、测试与发布门槛。

本文默认以 `LTFP-v1-Draft.md` 为 v2.1 协议语义真相源；若当前产品线仍需要 `ForwardIntent`、`ForwardDecision`、`NegotiationProfile`、`NegotiationResult` 等额外契约，必须先明确它们是：

- 进入 LTFP 正式 schema；或
- 仅保留为 bridge/agent 本地 adapter 契约，不作为共享线协议首批阻塞项。

---

## 二、本期范围

### 1. 必须交付

- `ltfp/` 独立共享模块与 schema 真相源
- 控制面握手、认证、心跳、full-sync、幂等 ACK
- `PublishService / UnpublishService / ServiceHealthReport`
- `connector_service` 路由解析与 connector proxy path
- ingress 三分层：`l7_shared`、`tls_sni_shared`、`l4_dedicated_port`
- `external_service` 的 discovery import + direct proxy
- export 基础能力
- `hybrid_group` 的 `pre_open_only` fallback
- 管理接口、诊断能力、兼容性测试与发布门槛

### 2. 第二批交付

- 可选的 `RouteAssign / RouteRevoke / RouteStatusReport`
- 更标准的 `grpc_h2` / `quic_native` binding
- datagram 预研
- `apps/dev-agent` 的可视化增强

### 3. 明确不做

- 跨 server 集群一致性
- session resume
- mid-stream failover
- 跨 scope 引用
- 完整多租户与复杂 RBAC
- 第三方服务发现系统的全部高级特性一次性接齐

---

## 三、已冻结设计前提

- `namespace + environment` 是强制作用域，所有核心资源都必须带 scope。
- `service_key` 是 lookup key，`service_id` 是 identity key，且 `service_key -> service_id` 在当前系统中一对一映射。
- `1 traffic = 1 原生独立数据流`，禁止在单个大流上做应用层二次复用。
- ingress 必须分为 `l7_shared`、`tls_sni_shared`、`l4_dedicated_port` 三类。
- `connector_service` 与 `external_service` 共用 route 抽象，但不共用数据面控制语义。
- v2.1 核心路径中，route match / resolve 发生在 server 侧，agent 只消费 `TrafficOpen` 与数据流。
- `hybrid_group` 只允许 `pre_open_only` fallback，收到 `TrafficOpenAck success` 后绝不 fallback。
- 首版不允许跨 scope 引用，也不允许跨 environment fallback。
- 第三方 discovery 不是配置真相源，只是外部目标来源和公开投影视图。

---

## 四、全量执行清单

### L0. 真相源、术语与边界冻结

- [ ] 明确 `docs/LTFP-v1-Draft.md` 为 v2.1 协议语义真相源
- [ ] 明确 `docs/ProjectDevelopmentChecklist.md` 仅用于共享协议库迁移
- [ ] 明确本清单为 LTFP 全量落地清单
- [ ] 统一术语：`connector/agent`、`server/cloud-bridge`、`canonical config registry`、`runtime traffic registry`
- [ ] 统一术语：`connector_service`、`external_service`、`hybrid_group`、`l7_shared`、`tls_sni_shared`、`l4_dedicated_port`
- [ ] 统一术语：`service_id`、`service_key`、`session_epoch`、`resource_version`、`event_id`
- [ ] 决议 `ForwardIntent / ForwardDecision / NegotiationProfile / NegotiationResult` 的归属：进入 LTFP schema 或降级为本地 adapter
- [ ] 将 docs、proto、代码中的命名统一，避免“正文一套、proto 一套、代码一套”

验收标准：

- 协议真相源只有一份，不再出现跨文档冲突
- 所有未决对象都有归属，不再带着语义歧义进入实现阶段

### L1. `ltfp/` 模块骨架与 workspace

- [x] 新建 `ltfp/` 目录与独立 `go.mod`
- [x] 在仓库根目录新增 `go.work`，纳入 `ltfp`、`agent-core`、`cloud-bridge`、`examples`、`service-registry`
- [x] 编写 `ltfp/README.md`，明确共享库职责、禁止项、升级方式
- [x] 固定包结构：`proto/`、`pb/`、`codec/`、`errors/`、`validate/`、`testkit/`
- [x] 约定 codegen 命令、生成目录、提交流程与更新规则
- [x] 固定共享库版本策略：schema 变更优先于使用方改造
- [x] 明确 `ltfp` 不得反向依赖 `agent-core/internal` 或 `cloud-bridge/internal`
- [x] 建立最小示例，验证 agent 与 bridge 可同时引用本地 `ltfp`

验收标准：

- 本地 workspace 下可无 `replace` 临时手改地联调
- `ltfp` 可作为唯一共享协议入口被双端引用

### L2. 资源模型、proto 与常量收口

- [x] 将资源模型整理为 typed schema：`Connector`、`Session`、`Service`、`ServiceEndpoint`、`ServiceExposure`、`HealthCheckConfig`、`Route`、`RouteMatch`、`RouteTarget`
- [x] 将控制面消息整理为 typed schema：`ControlEnvelope`、`ConnectorHello`、`ConnectorWelcome`、`ConnectorAuth`、`ConnectorAuthAck`、`Heartbeat`、`PublishService`、`PublishServiceAck`、`UnpublishService`、`UnpublishServiceAck`、`ServiceHealthReport`、`ControlError`
- [x] 将可选扩展消息整理为 typed schema：`RouteAssign`、`RouteAssignAck`、`RouteRevoke`、`RouteRevokeAck`、`RouteStatusReport`
- [x] 将数据面消息整理为 typed schema：`TrafficOpen`、`TrafficOpenAck`、`TrafficClose`、`TrafficReset`、`StreamPayload`
- [x] 固定状态常量：session state、service status、health status、ACK 状态、fallback policy、ingress mode
- [x] 固定统一错误码：认证错误、协商错误、scope 错误、幂等错误、ingress 错误、route resolve 错误、traffic 错误、proxy 错误
- [x] 在 schema 注释中明确 `service_id` 为空首发时的复用规则
- [x] 在 schema 注释中明确 `TrafficOpen` 只携带 `service_id` 与非权威 `endpoint_selection_hint`
- [x] 保留 `policy_json` 仅作为实验字段，不允许其成为长期扩展主通道
- [ ] 若保留 `ForwardIntent / ForwardDecision`，则在此阶段补齐正式 schema 与注释

验收标准：

- 文档中的核心对象都能在 `ltfp` 中找到一一对应的定义
- 不再存在同名不同义的双端 struct 与错误码

### L3. 编解码、校验与兼容性夹具

- [x] 在 `ltfp/codec` 中实现共享控制面消息的编码/解码入口
- [x] 在 `ltfp/codec` 中实现共享数据面消息的编码/解码入口
- [x] 支持现有 `http` tunnel 的控制面/数据面承载
- [x] 支持现有 `masque` tunnel 的控制面/数据面承载
- [x] 在 `ltfp/validate` 中实现纯函数校验：必填字段、枚举值、scope、版本号、事件幂等字段、兼容性字段
- [x] 校验 `Route` scope 必须等于 target scope
- [x] 校验首版不允许跨 scope 引用与跨 environment fallback
- [x] 校验 `TrafficOpen` 不得携带权威 `target_addr`
- [x] 为 `policy_json` 建立过渡期校验与警告规则
- [x] 在 `ltfp/testkit` 中建立 golden payload、round-trip 断言、错误 payload 断言
- [x] 为 schema 版本演进建立兼容性夹具与升级样例

验收标准：

- 同一条消息在不同 binding 下解码得到同一语义对象
- 非法 payload 能在共享库层稳定拒绝，而不是交由使用方各自兜底

### L4. 握手、认证、心跳与 session 状态机

- [x] 在共享库中固定 `HELLO -> WELCOME -> AUTH -> AUTH_ACK -> HEARTBEAT` 主链路
- [x] 明确 `ConnectorWelcome.assigned_session_epoch` 与 `ConnectorAuthAck.session_epoch` 的权威关系
- [x] 固定 session 状态机：`CONNECTING -> AUTHENTICATING -> ACTIVE -> DRAINING/STALE -> CLOSED`
- [x] 实现 heartbeat 超时后的 `STALE` 流转规则
- [x] 实现新 `session_epoch` 接管后旧 session 进入 `DRAINING` 或 `STALE`
- [x] 旧 session 禁止继续修改资源状态
- [x] 固定 `ControlError.scope/code/message/retryable` 的语义
- [x] 建立握手失败、认证失败、心跳超时、旧 epoch 晚到消息的测试样例

验收标准：

- session 代际切换规则清晰，旧会话不会污染新状态
- 断线、重连、超时、认证失败都能落到可观测状态与错误码

### L5. 控制面一致性、幂等 ACK 与 full-sync

- [x] 固定资源级消息必带 `session_id`、`session_epoch`、`event_id`、`resource_version`
- [x] 固定 ACK 契约：`accepted`、`accepted_resource_version`、`current_resource_version`、`error_code`、`error_message`
- [x] 为 `PublishServiceAck`、`UnpublishServiceAck`、`RouteAssignAck`、`RouteRevokeAck` 固定幂等语义
- [x] 提供共享去重键生成与版本比较辅助函数
- [x] 固定 reject reason：旧 epoch、重复事件、版本回退、非法 scope、缺失依赖资源
- [x] 设计 full-sync 与增量 sync 的消息边界和回放规则
- [x] 实现重连后的全量重同步流程
- [x] 建立重复事件安全 ACK、版本回退拒绝、重连恢复一致性的测试

验收标准：

- 重放同一事件不会产生重复副作用
- 断线重连后，agent 与 bridge 最终能恢复一致状态

### L6. Canonical Config Registry 与 Runtime Traffic Registry

- [ ] 在 bridge 侧建立 `Canonical Config Registry`，存储 `connector`、`session`、`service`、`route`、`discovery projection metadata`
- [ ] 在 bridge 侧建立 `Runtime Traffic Registry`，存储 `active traffic`、`connector proxy traffic`、`direct proxy traffic`、`bytes/state/trace`
- [ ] 建立 `service_key -> service_id` 的稳定映射与查询接口
- [ ] 为 `connector/session/service/route/traffic` 建立最小索引与状态查询能力
- [ ] 将 connector proxy 与 direct proxy 的运行态明确区分
- [ ] 为 runtime registry 接入 trace_id、错误码、路径类型、fallback 原因
- [ ] 为 registry 建立最小审计字段与状态快照能力

验收标准：

- 配置真相源与高频运行态分层明确
- 任一 traffic 都能追溯到 route、service、session、path type

### L7. Agent 本地服务模型与健康检查

- [ ] 盘点 `agent-core` 本地模型，明确哪些属于运行态、哪些需要映射到共享协议
- [ ] 为 `LocalRegistration` 建立到 `PublishService` 的 adapter
- [ ] 为本地摘除、下线、重注册建立到 `UnpublishService` 的 adapter
- [ ] 在 agent 侧实现 endpoint 粒度健康探测
- [ ] 在 agent 侧聚合 service 粒度健康状态
- [ ] 通过 `ServiceHealthReport` 上报 endpoint 粒度与 service 粒度结果
- [ ] 明确 `HEALTHY / UNHEALTHY / UNKNOWN` 的上报时机和降噪策略
- [ ] 建立探测失败、探测恢复、endpoint 多实例聚合的测试

验收标准：

- connector published service 的健康来源明确来自 agent
- server 不需要也不得主动探测 agent 本地 upstream 地址

### L8. `connector_service` 路由解析与 connector proxy path

- [ ] 在 bridge 侧实现 `connector_service` 的 route resolve
- [ ] route resolve 过滤条件至少覆盖：scope 不匹配、service 不健康、connector 离线、session 非 `ACTIVE`
- [ ] bridge 只选择 connector 与 service，不选择权威 upstream endpoint
- [ ] `TrafficOpen` 只携带 `service_id`、`route_id`、`trace_id`、`endpoint_selection_hint`
- [ ] agent 在收到 `TrafficOpen` 后自行选择 `ServiceEndpoint`
- [ ] agent 实现 upstream dial、双向转发、正常关闭、异常 reset
- [ ] 明确 pre-open 与 post-open 的错误边界
- [ ] `TrafficOpenAck success` 前后的状态边界可被代码与测试准确识别
- [ ] 每个 traffic 使用一条独立底层 stream，不做应用层二次复用
- [ ] 为单 endpoint、多 endpoint、dial failure、timeout、reset 建立回归用例

验收标准：

- 外部请求经 bridge 命中 `connector_service` 后，可稳定转发到 agent 本地服务
- `TrafficOpenAck success` 前后的失败语义清晰且可观测

### L9. Ingress 三分层与专属端口入口

- [ ] 实现 `l7_shared` 的 Host / `:authority` / `path_prefix` 路由
- [ ] 实现 `tls_sni_shared` 的 SNI 提取、路由与错误分类
- [ ] 实现 `l4_dedicated_port` 的监听器生命周期管理
- [ ] 固定裸 TCP 场景“一服务一端口”，禁止多裸 TCP 服务共用导出端口
- [ ] 建立 route / service / exposure / listener 的可追踪映射
- [ ] 补齐端口冲突检测、配置校验、热更新、关闭清理、启动局部失败策略
- [ ] 为 `l7_shared`、`tls_sni_shared`、`l4_dedicated_port` 建立独立诊断输出
- [ ] 如保留 `PortMappingConfig`，明确其只是 `l4_dedicated_port` 的 bridge 本地落地方式，不进入共享库
- [ ] 建立 Host 路由、SNI 路由、listen_port 路由互不串扰的测试

验收标准：

- 三类 ingress 都能被独立验证
- 裸 TCP 与 dedicated port 行为符合方案约束

### L10. `external_service`、Discovery Import 与 Direct Proxy

- [ ] 定义 `DiscoveryProvider` 抽象与最小 mock provider
- [ ] 实现基础查询模式：`cache_first`、`refresh_on_miss`、`stale_if_error`
- [ ] 实现 external endpoint 过滤、选路、拨号、超时、错误分类
- [ ] 实现 provider allowlist、namespace allowlist、service allowlist
- [ ] 实现 endpoint 网段 allowlist / denylist
- [ ] 实现 direct proxy 连接超时与并发限制
- [ ] 保证 `external_service` 路径由 bridge 自己查、自己连、自己转发
- [ ] 保证 `external_service` 不向 agent 发送 `TrafficOpen`
- [ ] 将 direct proxy traffic 写入 runtime registry 并与 connector proxy 明确区分
- [ ] 建立 provider miss、cache stale、endpoint unhealthy、dial failure、provider down 的测试

验收标准：

- route 命中 `external_service` 时，bridge 可独立完成代理
- discovery 缓存、安全约束、超时与并发限制都可被验证

### L11. Export 与 Discovery Projection

- [ ] 实现 export 准入判断：connector 在线、存在有效 session、service `ACTIVE`、health `HEALTHY`、`discovery_policy.enabled=true`、`exposure.allow_export=true`、server 已生成可达入口
- [ ] 基于 ingress mode 生成 export endpoint
- [ ] `l7_shared` 导出共享域名/端口
- [ ] `tls_sni_shared` 导出 server reachable address，并附带 SNI metadata
- [ ] `l4_dedicated_port` 导出 `gateway_host:dedicated_port`
- [ ] export 导出的必须是 server reachable address，而不是 agent 本地 upstream 地址
- [ ] 建立 discovery export reconciler，负责 create/update/delete
- [ ] 当 session 失效、service 失活、health 下降时及时撤销或更新 export
- [ ] 建立 export endpoint 生成、健康变化、session 变化、撤销清理测试

验收标准：

- Export 行为严格符合 LTFP 文档规则
- 第三方 discovery 中看到的是 server 入口，不是 agent 内网地址

### L12. `hybrid_group` 与 `pre_open_only` fallback

- [ ] 实现 `primary_connector_service + fallback_external_service` 模型
- [ ] 可 fallback 的阶段仅限：route resolve miss、service unavailable、`TrafficOpenAck` 失败、agent pre-open timeout
- [ ] 固定 fallback 截止点为“收到 `TrafficOpenAck success` 之前”
- [ ] 收到 `TrafficOpenAck success` 后绝不 fallback
- [ ] 已向 upstream 写出业务数据、mid-stream reset、partial response、任意 post-open 失败都绝不 fallback
- [ ] 为 fallback 记录原因、命中路径、原始 route、最终 target
- [ ] 建立 pre-open fallback 成功、post-open 禁止 fallback 的完整测试集

验收标准：

- fallback 行为严格符合 `pre_open_only`
- 不会对有副作用请求做 post-open 重试

### L13. Agent 与 Bridge 接入共享协议

- [ ] 盘点 `agent-core/internal/domain` 中属于线协议的 struct，迁移到 `ltfp`
- [ ] 盘点 `cloud-bridge/internal/domain` 中属于线协议的 struct，迁移到 `ltfp`
- [ ] `agent-core` 的握手、发布、健康上报、traffic open 解析与 ACK 校验切到共享协议
- [ ] `cloud-bridge` 的 sync path、reply、route resolve 输入输出切到共享协议
- [ ] 保留 `LocalRegistration`、forwarder、listener 配置、provider 实现、runtime store 等本地模型
- [ ] 在 agent 与 bridge 中建立“本地运行态 <-> ltfp message” 的 adapter 层
- [ ] 删除双端重复的协议常量、错误码、消息 struct
- [ ] 在双端测试中引入 `ltfp/testkit`

验收标准：

- 双端共享同一套协议对象和错误码
- 迁移完成后，协议语义变更只需在 `ltfp` 维护一处

### L14. 管理接口、诊断与可观测性

- [ ] 输出 service、session、route、traffic、health、error 的管理/状态接口
- [ ] 为握手失败、旧 epoch、重复事件、scope 拒绝、fallback、direct proxy 失败提供结构化日志
- [ ] 接入 trace_id，从 ingress 贯穿到 traffic、upstream、审计日志
- [ ] 统计 connector proxy 与 direct proxy 的运行态指标
- [ ] 输出 dedicated port listener 状态、discovery provider 状态、export 状态
- [ ] 输出 reject reason、error code、fallback reason 的查询能力

验收标准：

- 核心控制面与数据面失败都可定位
- 运行态可区分 connector proxy、direct proxy、hybrid fallback 三条路径

### L15. 测试矩阵、发布门槛与回滚

- [ ] 建立单元测试矩阵：schema、validate、codec、registry、selector、health、ingress、provider
- [ ] 建立集成测试矩阵：握手、full-sync、publish/unpublish、health report、connector proxy、direct proxy、export、hybrid
- [ ] 建立 binding parity 测试：`http` 与 `masque` 控制面/数据面行为一致
- [ ] 建立兼容性测试：共享库变更必须同时验证 agent 与 bridge
- [ ] 建立回归清单：握手、认证、心跳、旧 epoch、重复事件、scope 校验、三类 ingress、external proxy、export、hybrid fallback
- [ ] 建立升级说明：新增字段、默认值、向后兼容、必填变更处理规则
- [ ] 建立最小发布门槛：共享库、agent、bridge 兼容性测试均通过
- [ ] 建立最小回滚方案：可识别新增字段、可回退二进制、可保留运行态数据一致性

验收标准：

- LTFP 变更路径可追踪、可测试、可回滚
- 发布时不会要求双端手工同步两套协议定义

### L16. 第二批扩展能力

- [ ] 如启用 edge route sync，再实现 `RouteAssign / RouteRevoke / RouteStatusReport`
- [ ] 将 `policy_json` 中进入稳定范围的字段转为 typed schema
- [ ] 抽象统一 transport binding 接口
- [ ] 评估并接入 `grpc_h2` 或 `quic_native` 作为标准 binding
- [ ] 为 datagram 和 future binding 预留能力位与实验计划

验收标准：

- 第二批能力不阻塞 v2.1 首批闭环
- 新 binding 不改变上层 route / service / traffic 语义

---

## 五、建议执行顺序

1. 先做 `L0-L3`，冻结真相源、模块骨架、schema、校验与兼容性夹具。
2. 再做 `L4-L6`，把握手、幂等、registry、健康上报这条控制面主链路做稳。
3. 然后做 `L7-L9`，跑通 `connector_service` 与 ingress 三分层。
4. 接着做 `L10-L12`，补齐 `external_service`、export、hybrid。
5. 再做 `L13-L15`，完成双端迁移、观测性、测试与发布门槛。
6. 最后做 `L16`，处理 edge route sync、标准 binding、QUIC、datagram 等第二批能力。

---

## 六、分阶段交付物

### 阶段一：协议与控制面闭环

- `ltfp/` 模块、schema、codec、validate、testkit
- 稳定的握手、认证、心跳、full-sync、ACK 语义
- registry 与 service identity 规则

### 阶段二：最小可用数据路径

- `connector_service` 路由解析
- `TrafficOpen / Ack / Close / Reset`
- agent 本地 endpoint 选择与回流转发
- ingress 三分层

### 阶段三：外部目标与公开投影

- discovery import + direct proxy
- export reconciler
- service health 与 export gating

### 阶段四：稳定性与扩展边界

- hybrid `pre_open_only`
- 观测性、诊断、回归矩阵
- 发布门槛与回滚策略

---

## 七、本期验收口径

### 功能验收

- `agent-core` 与 `cloud-bridge` 通过 `ltfp` 共享同一套协议对象
- handshake、heartbeat、publish/unpublish、health report、traffic open 全部使用共享协议库
- `connector_service`、`external_service`、`hybrid_group` 三条路径都可独立验证
- `external_service` 不向 agent 发送 `TrafficOpen`
- export 只导出 server reachable address
- hybrid fallback 严格符合 `pre_open_only`

### 工程验收

- 仓库存在独立 `ltfp` 模块与 `go.work`
- 双端重复协议定义已清理
- golden payload、兼容性夹具、binding parity 测试可运行
- 关键管理/状态接口可查询 service、session、route、traffic、health、error

### 非功能验收

- `http` 与 `masque` 在共享协议层行为一致
- 重复事件、旧 epoch、非法 scope、非法 payload 能稳定拒绝
- connector proxy 与 direct proxy 运行态可区分、可追踪、可审计
- 新增协议字段时，变更路径可追踪、可测试、可回滚
