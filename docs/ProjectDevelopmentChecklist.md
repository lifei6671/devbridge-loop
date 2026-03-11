# DevBridge Loop 执行清单（共享协议库版）

## 一、文档目的

本清单用于承接 [ProjectDevelopmentPlan.md](./ProjectDevelopmentPlan.md) 与 [LTFP-v1-Draft.md](./LTFP-v1-Draft.md)，在“协议必须抽成共享库，由 `agent-core` 与 `cloud-bridge` 复用”这一原则下，将任务拆成可执行、可验收、可提交的工作项。

---

## 二、已冻结设计前提

### 1. LTFP 协议必须沉淀为独立共享库

本期冻结原则：

- `agent-core` 与 `cloud-bridge` 之间的线协议、错误码、协商字段、消息类型、校验规则，不允许各自维护一份副本。
- 协议相关代码必须下沉到独立模块，建议目录固定为 `ltfp/`。
- 共享库优先承载“线协议与语义”，而不是承载业务运行态实现。

要求：

- 共享库至少同时被 `agent-core` 和 `cloud-bridge` 引用。
- 共享库不能反向依赖 `agent-core/internal` 或 `cloud-bridge/internal`。
- 所有跨进程/跨模块消息定义都必须以共享库为单一真相源。

### 2. 共享库与业务运行态必须分层

共享库承载：

- 控制面消息模型
- 数据面消息模型
- 协商模型
- 错误码、状态码、feature 常量
- 编解码与校验辅助
- 兼容性测试夹具

业务模块本地保留：

- `agent-core` 的 `LocalRegistration`、本地发现、forwarder、runtime store
- `cloud-bridge` 的 ingress listener、discovery provider、runtime registry、admin config
- UI、配置文件、服务发现 provider 的具体实现

要求：

- `PortMappingConfig` 属于 bridge 本地配置模型，不强行下沉到共享库。
- `ForwardIntent`、`ForwardDecision`、`TrafficOpen`、`PublishService` 这类跨模块契约必须在共享库。
- 本地运行态结构若需要上行或下行，必须通过 adapter 映射到共享库消息，而不是直接复用内部 struct。

### 3. 共享库优先采用“协议定义 + 绑定适配”结构

建议目录：

```text
ltfp/
  go.mod
  README.md
  proto/
  pb/
  codec/
  errors/
  negotiation/
  validate/
  testkit/
```

说明：

- `proto/`：LTFP 的 schema 源文件
- `pb/`：生成后的 Go 绑定或统一暴露层
- `codec/`：当前 `http/masque` tunnel 对协议消息的封装与解析
- `errors/`：错误码、状态码、feature 名称
- `negotiation/`：会话级与请求级协商模型、特性集合、能力交集计算
- `validate/`：字段合法性、版本与幂等检查的纯函数校验
- `testkit/`：golden payload、兼容性夹具、共享断言

### 4. 本期优先复用现有 transport binding

本期不先做全新 tunnel 协议栈，先复用现有 binding。

要求：

- 协议消息统一由 `ltfp` 定义。
- `http` 与 `masque` 只负责承载，不再各自维护独立消息语义。
- 协商流程与普通同步流程共用同一条 tunnel 连接。

### 5. v2.1 核心路径保持 server 侧 route resolve

要求：

- `connector_service`、`external_service`、`hybrid_group` 的 route resolve 主逻辑在 `cloud-bridge`。
- `agent-core` 核心路径只消费 `TrafficOpen`、数据流与请求级 `forwardIntent`。
- `RouteAssign / RouteRevoke` 在本期仍视为可选扩展，不作为首批闭环阻塞项。

### 6. 幂等、一致性、协商语义必须先于功能扩展落地

要求：

- 所有资源级消息固定具备 `session_epoch + event_id + resource_version`。
- `HELLO / ACK / ERROR` 固定支持会话级协商字段。
- `requiredFeatures` 不满足时握手必须失败。
- `TrafficOpenAck success` 之后不得触发 hybrid fallback。

---

## 三、执行清单

### C0. 共享协议库骨架

- [x] 冻结原则：LTFP 协议抽成独立共享库，由 `agent-core` 与 `cloud-bridge` 复用
- [ ] 新建 `ltfp/` 目录与独立 `go.mod`
- [ ] 约定模块路径，例如 `github.com/lifei6671/devbridge-loop/ltfp`
- [ ] 在仓库根目录新增 `go.work`，纳入 `ltfp`、`agent-core`、`cloud-bridge`、`examples`、`service-registry`
- [ ] 编写 `ltfp/README.md`，明确共享库边界、包结构、禁止项
- [ ] 约定 codegen 命令、生成目录与更新流程
- [ ] 约定版本策略：共享库变更必须先更新 schema，再更新使用方

验收标准：

- `agent-core` 与 `cloud-bridge` 可以通过 workspace 同时引用本地 `ltfp`
- 共享库目录职责明确，不再依赖后续口头约定

### C1. 协议 schema 与常量收口

- [ ] 将 LTFP 文档中的控制面消息整理为实际 schema：`ControlEnvelope`、`ConnectorHello`、`ConnectorWelcome`、`ConnectorAuthAck`、`PublishService`、`UnpublishService`、`ServiceHealthReport`
- [ ] 将数据面消息整理为实际 schema：`TrafficOpen`、`TrafficOpenAck`、`TrafficClose`、`TrafficReset`
- [ ] 将协商相关消息字段整理为统一 schema：`NegotiationProfile`、`NegotiationResult`、`ForwardIntent`、`ForwardDecision`
- [ ] 在 `ltfp/errors` 中定义统一错误码：同步错误、协商错误、入口错误、traffic 错误
- [ ] 在 `ltfp/errors` 中定义统一状态常量：`accepted / duplicate / rejected`、`ACTIVE / INACTIVE / STALE` 等
- [ ] 在 `ltfp/negotiation` 中定义 feature 常量：如 `port_mapping_forward`、`client_auto_negotiation`
- [ ] 明确哪些字段是 canonical identity，哪些字段是 lookup key
- [ ] 补齐注释：`service_id` 为空首发时复用既有 `service_key` 对应 identity

验收标准：

- 文档中的核心消息都能在 `ltfp` 中找到一一对应定义
- 不再出现 agent 和 bridge 各自定义一份同名但不同义的 struct

### C2. 编解码、校验与兼容性夹具

- [ ] 在 `ltfp/codec` 中实现当前 tunnel 消息的统一编码/解码入口
- [ ] 支持当前 `http` sync path 的 envelope 编解码
- [ ] 支持当前 `masque` sync path 的 envelope 编解码
- [ ] 在 `ltfp/validate` 中实现纯函数校验：必填字段、feature 集合、版本号、幂等字段、scope 约束
- [ ] 在 `ltfp/testkit` 中建立 golden payload
- [ ] 为 `HELLO`、`PublishService`、`TrafficOpen`、`TrafficOpenAck`、`ForwardIntent` 建立跨模块共享断言
- [ ] 为兼容升级预留 schema 版本测试

验收标准：

- 同一条消息在 `http` 与 `masque` 承载下解码得到同一语义对象
- 错误 payload 能在共享库层被稳定拒绝，而不是交给使用方各自兜底

### C3. 会话协商与握手清单

- [ ] 在 `ltfp/negotiation` 中实现能力集合、required/optional features、交集计算逻辑
- [ ] 固定会话级协商字段进入 `HELLO / ACK / ERROR`
- [ ] 固定请求级协商字段进入 `ForwardIntent / ForwardDecision`
- [ ] 明确 `ConnectorWelcome.assigned_session_epoch` 与 `ConnectorAuthAck.session_epoch` 的权威关系
- [ ] 实现 `NEGOTIATION_UNSUPPORTED_VERSION`、`NEGOTIATION_UNSUPPORTED_FEATURE` 等错误码
- [ ] 提供共享校验：缺少 required feature 时直接拒绝握手
- [ ] 建立协商成功、部分降级、失败拒绝三类 golden case

验收标准：

- 协商行为由共享库定义，agent 与 bridge 只消费结果
- `syncProtocol=http/masque` 下协商行为一致

### C4. 控制面一致性语义清单

- [ ] 在共享库中固定资源级消息必带字段：`session_id`、`session_epoch`、`event_id`、`resource_version`
- [ ] 固定 ACK 契约：`accepted`、`accepted_resource_version`、`current_resource_version`、`error_code`、`error_message`
- [ ] 固定 `PublishServiceAck`、`UnpublishServiceAck`、`RouteAssignAck`、`RouteRevokeAck` schema
- [ ] 提供共享幂等辅助：事件去重键生成、版本比较辅助函数
- [ ] 提供共享 reject reason 常量：旧 epoch、版本回退、重复事件、非法 scope、缺失 feature
- [ ] 为 full-sync 与增量 sync 建立 schema 与测试样例

验收标准：

- agent 和 bridge 不再手写各自的 ACK 语义
- 所有资源级处理链路都能按共享库规则做幂等判断

### C5. 数据面与回流协议清单

- [ ] 在共享库中固定 `1 traffic = 1 stream` 的协议约束与注释
- [ ] 固定 `TrafficOpen` 只用于 connector proxy path
- [ ] 固定 `TrafficOpen` 中 `service_id` 与 `endpoint_selection_hint` 的语义
- [ ] 固定 `ForwardIntent.mode=port_mapping` 的严格转发语义
- [ ] 固定 `ForwardIntent.mode=auto_negotiation` 的协商转发语义
- [ ] 固定 `ForwardDecision` 返回字段与错误语义
- [ ] 明确 `TrafficOpenAck success` 前后的状态边界，用于 hybrid fallback

验收标准：

- connector proxy path 的线协议完全来自共享库
- port mapping 与 auto negotiation 都走同一套请求级协商消息

### C6. `agent-core` 接入共享库

- [ ] 盘点 `agent-core/internal/domain` 中哪些 struct 属于线协议，列出迁移清单
- [ ] 将 `sync.go`、`backflow.go`、tunnel 相关消息中的共享协议对象替换为 `ltfp` 类型
- [ ] 保留 `LocalRegistration`、本地诊断、runtime store 等 agent 本地模型，不强行迁移
- [ ] 在 `agent-core/internal/tunnel` 增加 adapter：本地运行态 -> `ltfp` 消息
- [ ] 在 `agent-core/internal/httpapi` 增加 adapter：`ltfp` 回流请求 -> forwarder 输入
- [ ] 在 `manager.go` 中改为使用共享库处理握手、协商、ACK 校验
- [ ] 在 agent 测试中引入 `ltfp/testkit`，复用 golden payload
- [ ] 删除 agent 侧重复定义且已迁移到共享库的协议 struct

验收标准：

- agent 的线协议定义不再散落在 `internal/domain` 各文件中
- handshake、publish、backflow、traffic open 的解析和校验都走共享库

### C7. `cloud-bridge` 接入共享库

- [ ] 盘点 `cloud-bridge/internal/domain` 中哪些 struct 属于线协议，列出迁移清单
- [ ] 将 tunnel event、reply、backflow 契约中的共享协议对象替换为 `ltfp` 类型
- [ ] 保留 bridge 本地配置模型、listener 配置、discovery endpoint、本地 runtime registry，不强行迁移
- [ ] 在 `sync_processor.go` 中改为使用共享库完成消息校验、ACK 组装、错误码回填
- [ ] 在 ingress / backflow handler 中接入共享的 `ForwardIntent / ForwardDecision`
- [ ] 在 `http` 与 `masque` 入口共用同一套共享协议解码结果
- [ ] 在 bridge 测试中引入 `ltfp/testkit`
- [ ] 删除 bridge 侧重复定义且已迁移到共享库的协议 struct

验收标准：

- bridge 的 sync path 和 backflow path 共享同一套协议对象
- `http` 和 `masque` 只剩 transport 差异，不再有协议语义差异

### C8. 端口映射与专属端口入口清单

- [ ] 在 `cloud-bridge` 本地配置模型中定义 `PortMappingConfig`
- [ ] 明确 `PortMappingConfig` 不进入共享库，只在 bridge 本地配置与 listener 生命周期中使用
- [ ] 将端口映射请求转换为共享库 `ForwardIntent(mode=port_mapping)`
- [ ] 命中 dedicated port / port mapping 后绕过 `RouteExtractPipeline`
- [ ] 支持 `sourceOrder=tunnel/local_route/service_discovery`
- [ ] 禁止请求方覆盖映射绑定的 `env/serviceName`
- [ ] 补齐端口冲突、监听失败、热更新、关闭清理测试

验收标准：

- 端口映射配置仍由 bridge 本地维护
- 端口映射转发行为通过共享库消息与 agent 协同

### C9. External Service、Export、Hybrid 清单

- [ ] `external_service` 路径继续保持 bridge 自己查、自己连、自己转发
- [ ] connector path 与 direct proxy path 共用 route 抽象，但不共用数据面控制语义
- [ ] export 判断逻辑引用共享库中的 service status / health status 常量
- [ ] hybrid fallback 判断引用共享库中的 traffic 边界与错误码
- [ ] 补齐 pre-open fallback 测试：resolve miss、service unavailable、open ack fail、pre-open timeout
- [ ] 明确 post-open 不 fallback：已收到 `TrafficOpenAck success`、已写出数据、partial response、mid-stream reset

验收标准：

- direct proxy 与 connector proxy 的边界清晰
- hybrid 行为在代码、测试、共享常量上保持一致

### C10. 清理、迁移与发布门槛

- [ ] 删除双端重复的协议常量、错误码、消息 struct
- [ ] 清理注释与命名不一致的历史字段
- [ ] 将 `docs/ProjectDevelopmentPlan.md`、`docs/LTFP-v1-Draft.md`、`docs/ProjectDevelopmentChecklist.md` 之间的术语统一
- [ ] 为共享库建立升级说明：新增字段、向后兼容、必填变更的处理规则
- [ ] 建立最小发布门槛：共享库变更必须伴随 agent 和 bridge 兼容性测试通过
- [ ] 建立回归清单：握手、full-sync、publish/unpublish、port mapping、external proxy、hybrid fallback

验收标准：

- 协议语义的真相源只有 `ltfp`
- 新增协议字段时，变更路径可追踪、可测试、可回滚

---

## 四、建议执行顺序

1. 先做 `C0-C2`，把共享库立起来。
2. 再做 `C3-C5`，先冻结协商、幂等、traffic 语义。
3. 然后并行推进 `C6-C7`，让 agent 与 bridge 都切到共享库。
4. 共享库接入稳定后，再做 `C8-C9`，补端口映射、direct proxy、export、hybrid。
5. 最后做 `C10`，清重复代码、补回归与升级规则。

---

## 五、本期明确不做

- 不在首批闭环里实现完整 `RouteAssign / RouteRevoke` 运行时下发
- 不在本期实现跨 server 集群一致性
- 不在本期实现 session resume
- 不在本期实现 mid-stream failover
- 不在本期实现完整鉴权与权限体系

---

## 六、本期验收口径

### 功能验收

- `agent-core` 与 `cloud-bridge` 都通过 `ltfp` 复用同一套协议对象
- handshake、publish/unpublish、backflow、traffic open 全部使用共享协议库
- dedicated port / port mapping 可通过共享 `ForwardIntent` 正常转发
- `external_service` 不向 agent 发送 `TrafficOpen`
- hybrid fallback 严格符合 `pre_open_only`

### 工程验收

- 仓库存在独立 `ltfp` 模块
- 根目录存在 `go.work`，本地联调不依赖临时 replace 手改
- 双端重复协议定义已清理
- golden payload 与兼容性测试可运行

### 非功能验收

- `http` 与 `masque` 在共享协议层行为一致
- 重复事件、旧 epoch、非法 feature 能稳定拒绝
- 共享库变更不会要求双端手工同步两套 struct
