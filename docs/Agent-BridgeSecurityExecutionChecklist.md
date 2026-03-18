# Agent 与 Bridge 安全接入与认证执行清单（最终态）

## 一、文档目的

本清单用于承接 [Agent‑BridgeSecurityArchitectureAndAuthDesign.md](./Agent‑BridgeSecurityArchitectureAndAuthDesign.md)，将安全接入与认证方案拆解为可执行、可联调、可验收的工程任务。

口径说明：

- 本清单按“开发阶段直接切最终态”执行，不设置兼容迁移窗口。
- 本清单覆盖 `ltfp` 协议层、`agent-core` 运行时、`cloud-bridge` 运行时三部分。
- 未在本清单内的能力（如多集群 HA、长期密钥平台化）不作为本轮阻塞项。

---

## 二、本期范围

### 1. 一期必须交付

- `ConnectorAuth` 强类型化（`token/client_cap_version`），移除 `auth_payload`
- Bridge 认证流程按 §7.11 单一权威落地
- TLS 接入模式三态（`required/optional/plaintext`）可配置并生效
- `ConnectorHello.namespace/environment` 改为可选校验
- Tunnel simultaneous close 与 recycle 语义和现有实现对齐
- `TunnelRecycleAck` 错误码与代码现状对齐
- 完整测试矩阵与发布门槛

### 2. 本期不做

- mTLS
- OCSP / CRL / OCSP Stapling
- session resume
- 多 Bridge 集群一致性治理

---

## 三、已冻结前提

- [x] 当前阶段未上线，直接切最终态，不保留协议双栈兼容。
- [x] TLS 由 Bridge 配置驱动：`required/optional/plaintext` 三模式。
- [x] Bridge 认证流程以方案 §7.11 为唯一实现基线。
- [x] `ConnectorHello.namespace/environment` 为可选，不参与认证成功判定。
- [x] simultaneous close 保留“必要时发送 `TrafficCloseAck`，避免互等卡死”的既有语义。
- [x] `TunnelRecycleAck` 错误码至少包含：`invalid_seq/close_ack_required/tunnel_unhealthy/buffer_dirty/tunnel_mismatch`，`deadline_hit` 作为可选扩展。

---

## 四、执行清单

### S0. 协议与模型冻结

- [x] 更新 `ltfp/proto/devbridge/loop/v2/ltfp.proto`：`ConnectorAuth` 改为强类型字段（`auth_method/token/client_cap_version/metadata`）
- [x] 重新生成 `ltfp/pb/gen/devbridge/loop/v2/ltfp.pb.go`
- [x] 同步 `ltfp/pb/types.go` 与 codec/testkit 中的结构定义与样例
- [x] 删除/禁用 `auth_payload` 相关引用与测试样例

验收标准：

- 仓库中不再出现可写入 `auth_payload` 的生产路径
- `ConnectorAuth.token` 缺失会被稳定拒绝

### S1. Agent 侧认证请求改造

- [x] 改造 Agent 握手发送逻辑，确保 `ConnectorAuth` 使用强类型字段
- [x] 补充 `client_cap_version` 填充规则（可选字段）
- [x] 认证相关日志统一脱敏，禁止输出 token 明文
- [x] 补齐 agent 侧握手单测与集成测试

代码落点建议：

- `agent-core/runtime/agent/app/runtime_bridge.go`
- `ltfp/testkit/fixtures.go`
- `ltfp/examples/interop/*`

验收标准：

- Agent 发出的 `ConnectorAuth` 仅包含新字段
- 明文 token 不进入日志与诊断快照

### S2. Bridge 认证流水线（按 §7.11）

- [x] 在 Bridge 控制面接入 `ConnectorHello -> ConnectorWelcome -> ConnectorAuth -> ConnectorAuthAck` 全链路处理
- [x] 固定校验顺序：method -> connector -> token 格式 -> token 查询 -> 归属校验 -> 状态/过期校验 -> 抢占限流 -> epoch 原子提交
- [x] 返回标准错误码：`auth_invalid_method/auth_invalid_token/auth_token_expired/auth_token_revoked/auth_connector_mismatch/auth_session_superseded/auth_rate_limited/auth_internal_error`
- [x] 明确“仅原子提交成功后才进入 authenticated 并允许业务流量”

代码落点建议：

- `cloud-bridge/runtime/bridge/app/control_plane.go`
- `cloud-bridge/runtime/bridge/control/*`
- `cloud-bridge/runtime/bridge/registry/*`

验收标准：

- 同一 `connector_id` 并发握手仅一个成功提交为 ACTIVE
- 失败握手不会污染会话权威视图或资源状态

### S3. Token 存储与校验能力

- [x] 增加 connector token 领域模型（`connector_id/token_id/token_secret_hash/status/issued_at/expires_at/rotated_at/metadata`）
- [x] 引入抗暴力破解哈希（优先 `argon2id`）
- [x] 支持状态机：`active/grace/revoked/expired`
- [x] 实现 token 解析规则 `dbt_<token_id>.<token_secret>`（按第一个 `.` 分割）

验收标准：

- Bridge 侧不存储 token 明文
- 认证流程可基于 token 状态与过期时间做正确拒绝

### S4. TLS 接入模式落地（required/optional/plaintext）

- [x] 在 Bridge 配置层新增并校验 `tls_mode`
- [x] 在控制面接入层执行模式判定：
- [x] `required` 仅允许 TLS
- [x] `optional` 同时允许 TLS/明文
- [x] `plaintext` 仅允许明文
- [x] 为拒绝场景补齐明确日志与指标

代码落点建议：

- `cloud-bridge/runtime/bridge/app/config.go`
- `cloud-bridge/runtime/bridge/app/bootstrap.go`
- `cloud-bridge/runtime/bridge/app/control_plane.go`

验收标准：

- 三种模式行为与方案定义一致
- 模式切换后可通过自动化测试稳定验证

### S5. Agent TLS 校验与连接配置

- [x] 为 Agent 增加 Bridge TLS 配置（Root CA、ServerName、开关）
- [x] gRPC 通道移除默认 `insecure` 依赖，按配置启用 TLS 凭据
- [x] TCP binding 在 TLS 模式下支持证书链、SAN、有效期校验
- [x] 显式禁用 TLS 1.3 Early Data（0-RTT）

代码落点建议：

- `agent-core/runtime/agent/app/runtime_bridge.go`
- `ltfp/transport/grpcbinding/*`
- `ltfp/transport/tcpbinding/*`

验收标准：

- TLS 模式下 Agent 证书校验失败会稳定中止握手
- 非 TLS 模式下行为不受 TLS 逻辑污染

### S6. Hello 可选 scope 校验改造

- [x] 放宽 `ValidateConnectorHello`：`namespace/environment` 允许为空
- [x] 保留 `connector_id` 必填约束
- [x] 同步 testkit/interop/validate 测试样例

代码落点建议：

- `ltfp/validate/validator.go`
- `ltfp/validate/validator_test.go`
- `ltfp/testkit/fixtures.go`

验收标准：

- 缺失 `namespace/environment` 的 `ConnectorHello` 不会被拒绝
- 其他必填字段校验仍有效

### S7. Session Epoch 权威与并发收敛

- [x] 落地 `assigned_session_epoch`（候选）与 `auth_ack.session_epoch`（最终权威）的一致性约束
- [x] 以 `connector_id` 粒度实现原子提交（事务/CAS/锁）
- [x] 成功抢占限流（如 60s 内最多 3 次）
- [x] 对并发失败握手统一返回 `auth_session_superseded`

验收标准：

- 无法出现同 connector 双 ACTIVE 会话
- 低 epoch 事件不会覆盖高 epoch 权威状态

### S8. 数据面关闭与回收语义对齐

- [x] 固化 simultaneous close 规则：必要时回 `TrafficCloseAck`，且仅一次
- [x] `TunnelRecycle` 发起权保持在 Bridge
- [x] Server 无法确认安全回收时直接关闭 tunnel
- [x] 双端并发 close 场景补齐时序回归测试

代码落点建议：

- `agent-core/runtime/agent/app/data_plane_runtime.go`
- `cloud-bridge/runtime/bridge/app/tunnel_runtime_adapter.go`
- `cloud-bridge/runtime/bridge/connectorproxy/*`

验收标准：

- 双端 close 不会出现 ACK 互等卡死
- recycle 失败路径统一降级为关闭

### S9. 回收错误码与告警口径统一

- [x] 对齐 `ltfp/errors/codes.go` 与协议文档错误码集合
- [x] 在 Agent/Bridge 回收拒绝分支统一填充错误码
- [x] 同步告警、诊断、日志字段映射

代码落点建议：

- `ltfp/errors/codes.go`
- `agent-core/runtime/agent/app/data_plane_runtime.go`
- `cloud-bridge/runtime/bridge/connectorproxy/*`

验收标准：

- 同一失败原因在双端呈现相同错误码
- 诊断与告警规则可稳定命中

### S10. 观测与审计增强

- [x] 新增认证与握手指标：成功率、错误码分布、supersede 次数、rate limit 次数
- [x] 新增 TLS 模式拒绝指标：`required` 拒绝明文、`plaintext` 拒绝 TLS
- [x] 审计日志保留 `connector_id/token_id(脱敏)/session_id/session_epoch/source_ip/error_code`

验收标准：

- 安全失败路径均可通过指标和日志追溯
- 日志中无高敏感凭证明文

### S11. 测试矩阵与发布门槛

- [x] 单测：proto/validator/auth parser/hash/tls mode 判定/epoch 原子提交
- [x] 集测：三种 TLS 模式 + 握手全链路 + 并发抢占 + token 状态切换
- [x] 数据面：simultaneous close + recycle 成功/失败 + 错误码断言
- [x] 回归：`grpc_h2` 与 `tcp_framed` 两种 binding parity

建议执行命令：

- `cd ltfp && go test ./...`
- `cd agent-core && go test ./...`
- `cd cloud-bridge && go test ./...`

验收标准：

- 关键链路测试全部通过后才允许合入主线
- 无高优先级已知缺陷遗留

### S12. 交付核对（Definition of Done）

- [x] 方案文档、协议定义、实现代码、测试用例四者一致
- [x] `auth_payload` 已完全移除
- [x] TLS 三模式在配置、运行时、测试、观测中全部闭环
- [x] §7.11 认证流程具备可复现实验与自动化回归

### S13. Bridge 自建 CA 与证书运维闭环

- [ ] 初始化生成 Root CA 私钥与证书，并独立持久化
- [ ] 由 Root CA 签发 Bridge 服务端证书，替代“仅加载外部 `tls_cert_file/tls_key_file`”路径
- [ ] Bridge 服务端证书支持短周期续签、替换与热加载
- [ ] Root CA 带外分发、轮换、紧急替换 runbook 与配置入口补齐
- [ ] Root CA 私钥与 Bridge 服务端私钥分开存储，最小权限访问并排除普通日志/通用备份

代码落点建议：

- `cloud-bridge/runtime/bridge/app/bootstrap.go`
- `cloud-bridge/runtime/bridge/app/config.go`
- `cloud-bridge/runtime/bridge/app/control_plane_tls.go`
- `docs/Agent‑BridgeSecurityArchitectureAndAuthDesign.md`

验收标准：

- Bridge 能在无预制 cert/key 的场景完成首次 CA 初始化和服务端证书签发
- Agent 仅信任 Bridge Root CA 时可成功完成 TLS 握手
- Root CA 轮换与紧急替换具备可演练的带外操作路径

### S14. 未认证入口限流、失败封禁与枚举抑制

- [ ] 对未认证 `ConnectorHello` 实现 `source_ip/connector_id` 双维度限流
- [ ] 对认证失败实现 `source_ip/connector_id` 双维度失败限流与短时封禁
- [ ] 对未知 `connector_id`、无效 token、吊销 token 等场景统一外显响应口径，降低枚举区分度
- [ ] 为握手洪泛增加连接预算与并发预算控制
- [ ] 补齐未认证入口限流、封禁、枚举抑制自动化测试

代码落点建议：

- `cloud-bridge/runtime/bridge/app/control_plane.go`
- `cloud-bridge/runtime/bridge/app/control_auth.go`
- `cloud-bridge/runtime/bridge/obs/*`

验收标准：

- 未认证洪泛不会无限消耗握手资源
- 外部请求无法通过错误差异稳定区分“未知 connector”和“已注册 connector”
- 限流与封禁命中后有统一指标、日志与可回归测试

### S15. Session 状态机与旧会话收敛

- [ ] 显式落地 `connecting -> connected -> control_ready -> authenticated -> draining/closed/failed` 状态机
- [ ] 未进入 `authenticated` 前禁止发布服务、分配实际 traffic、进入 tunnel pool 工作态
- [ ] 新 `session_epoch` 生效后，旧 session 必须进入 `draining/stale` 并冻结控制面资源写入
- [ ] `failed` 状态必须终止控制面并清理该 session 下全部 tunnel
- [ ] Agent 收到 `auth_session_superseded` 或 `auth_rate_limited` 后执行指数退避

代码落点建议：

- `ltfp/session/*`
- `agent-core/runtime/agent/app/runtime_bridge.go`
- `cloud-bridge/runtime/bridge/registry/*`
- `cloud-bridge/runtime/bridge/app/control_plane.go`

验收标准：

- 认证完成前无法发布服务或接收实际 traffic
- 新旧 session 切换时不会出现旧会话继续污染控制面状态
- supersede/rate limit 失败路径具有稳定退避行为

### S16. 控制面一致性与幂等语义

- [ ] 资源级控制消息统一校验 `session_epoch/event_id/resource_version`
- [ ] `event_id` 去重作用域固定为 `session_id + event_id`
- [ ] 为 `PublishServiceAck/UnpublishServiceAck/RouteAssignAck/RouteRevokeAck` 固定 accepted/current version 语义
- [ ] 补齐重复投递、乱序投递、低 epoch 覆盖、高 epoch 并发写入回归测试

代码落点建议：

- `ltfp/consistency/*`
- `cloud-bridge/runtime/bridge/control/*`
- `cloud-bridge/runtime/bridge/registry/*`
- `agent-core/runtime/agent/app/runtime_bridge.go`

验收标准：

- 相同 `session_id + event_id` 的重复消息不会重复生效
- 低 `session_epoch` 消息无法覆盖高权威状态
- 关键 ACK 的 accepted/current version 语义在双端一致

### S17. 资源身份与策略边界

- [ ] 固定 `service_key=<namespace>/<environment>/<service_name>` 为 canonical lookup key
- [ ] 固定 `service_id` 为全局 opaque identity，并在 runtime/traffic/ACK/audit 中统一使用
- [ ] 当 `PublishService.service_id` 为空时，若 `service_key` 已存在必须复用既有 `service_id`，仅首次出现时分配新值
- [ ] publish policy / route policy 与 token 绑定关系彻底解耦，避免 token 承载 scope 与资源治理策略
- [ ] 补齐 service republish、route target、audit 字段使用 `service_id/service_key` 的回归测试

代码落点建议：

- `cloud-bridge/runtime/bridge/control/*`
- `cloud-bridge/runtime/bridge/registry/*`
- `agent-core/runtime/agent/app/runtime_bridge.go`
- `docs/LTFP-v1-Draft.md`

验收标准：

- 同一 `service_key` 的重复发布不会无故漂移 `service_id`
- 路由层使用 `service_key`，运行时与审计使用 `service_id` 的职责边界清晰可测
- token 仅承担接入认证，不承载资源发布与路由治理策略

### S18. Token 生命周期与吊销治理

- [ ] 为 `token_secret_hash` 记录算法与参数版本，避免后续轮换失去判定依据
- [ ] `grace` 状态增加最长 24 小时上限，并支持自动转 `expired/revoked`
- [ ] 明确“会话期间 token 过期不主动中断现有 active session”的运行时语义与测试
- [ ] 支持正常轮换场景下的新旧 token 并存窗口与平滑切换
- [ ] 支持紧急吊销场景下对关联 active session 的强制 drain 或强制关闭路径
- [ ] 明文 token 仅展示一次，不允许二次展示，不得进入普通日志

代码落点建议：

- `cloud-bridge/runtime/bridge/app/control_auth.go`
- `cloud-bridge/runtime/bridge/registry/*`
- `cloud-bridge/runtime/bridge/adminapi/*`
- `agent-core/runtime/agent/app/runtime_bridge.go`

验收标准：

- `grace` token 不会无限期存活
- 正常轮换与紧急吊销场景均可通过自动化测试稳定复现
- token 过期、轮换、吊销对现有 active session 的影响符合方案定义

### S19. Agent 侧凭证安全存储

- [ ] 为桌面/主机场景接入 keyring / Secret Service / Windows DPAPI / macOS Keychain 等安全存储
- [ ] 为容器场景明确 Secret 管理与静态存储加密方案
- [ ] 环境变量仅保留为过渡载体，不作为长期高安全推荐方式
- [ ] 配置文件落盘权限、诊断快照、日志导出路径统一增加高敏感凭证审查
- [ ] 补齐 token 存储、读取、迁移、脱敏导出测试

代码落点建议：

- `agent-core/runtime/agent/app/config.go`
- `agent-core/cmd/agent-core/main.go`
- `agent-core/runtime/agent/app/localrpc_*`
- `docs/ScaffoldQuickStart.md`

验收标准：

- Agent 默认路径不再依赖明文 token 常驻环境变量
- 日志、配置、诊断导出均不会泄露 token 明文
- 主机与容器两类部署形态均有明确的凭证存储规范

### S20. Tunnel 复用边界与超时治理

- [ ] `recycle_seq` 严格限定为单条 tunnel 生命周期内单调递增
- [ ] `tunnel_id` 在同一 session 生命周期内不得复用
- [ ] idle tunnel 探活固定在 transport/binding 层，不得写入业务 payload 缓冲区
- [ ] `Session.Open()` 补齐 `connect/tls/control_ready/auth` 分阶段超时与失败收敛
- [ ] `TrafficOpenAck` 超时、`TunnelRecycleAck` 超时、idle probe 失败时统一将 tunnel 标记为 `broken` 并补池
- [ ] 补齐 `recycle_seq` 回退、`tunnel_id` 复用、探活污染缓冲区的回归测试

代码落点建议：

- `ltfp/transport/*`
- `agent-core/runtime/agent/app/data_plane_runtime.go`
- `cloud-bridge/runtime/bridge/app/tunnel_runtime_adapter.go`
- `cloud-bridge/runtime/bridge/connectorproxy/*`

验收标准：

- 任何超时或探活失败路径都不会留下可误复用 tunnel
- `recycle_seq` 与 `tunnel_id` 的作用域约束可通过自动化测试稳定验证
- idle tunnel 探活不会污染业务数据缓冲与回收判定

---

## 五、建议执行顺序

1. 先做 `S0/S6`，冻结协议结构与校验边界。
2. 再做 `S2/S3/S7/S14`，完成 Bridge 认证核心能力、未认证入口防护与会话权威提交。
3. 接着做 `S4/S5/S13`，打通 TLS 模式、Agent 校验与 Bridge 自建 CA 闭环。
4. 然后做 `S15/S16/S17`，收敛 session 状态机、控制面一致性与资源身份边界。
5. 再做 `S8/S9/S20`，对齐数据面关闭回收、超时治理与错误码。
6. 然后做 `S18/S19`，补齐 token 生命周期治理与 Agent 凭证安全存储。
7. 最后做 `S10/S11/S12`，完成观测、测试与交付封板。

---

## 六、里程碑建议

- M1（协议冻结）：完成 `S0/S6`
- M2（认证基线）：完成 `S2/S3/S7/S14`
- M3（TLS 与证书）：完成 `S4/S5/S13`
- M4（控制面收敛）：完成 `S15/S16/S17`
- M5（数据面边界）：完成 `S8/S9/S20`
- M6（凭证治理）：完成 `S18/S19`
- M7（可发布）：完成 `S10/S11/S12`
