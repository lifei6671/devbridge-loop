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

- [ ] 增加 connector token 领域模型（`connector_id/token_id/token_secret_hash/status/issued_at/expires_at/rotated_at/metadata`）
- [ ] 引入抗暴力破解哈希（优先 `argon2id`）
- [ ] 支持状态机：`active/grace/revoked/expired`
- [ ] 实现 token 解析规则 `dbt_<token_id>.<token_secret>`（按第一个 `.` 分割）

验收标准：

- Bridge 侧不存储 token 明文
- 认证流程可基于 token 状态与过期时间做正确拒绝

### S4. TLS 接入模式落地（required/optional/plaintext）

- [ ] 在 Bridge 配置层新增并校验 `tls_mode`
- [ ] 在控制面接入层执行模式判定：
- [ ] `required` 仅允许 TLS
- [ ] `optional` 同时允许 TLS/明文
- [ ] `plaintext` 仅允许明文
- [ ] 为拒绝场景补齐明确日志与指标

代码落点建议：

- `cloud-bridge/runtime/bridge/app/config.go`
- `cloud-bridge/runtime/bridge/app/bootstrap.go`
- `cloud-bridge/runtime/bridge/app/control_plane.go`

验收标准：

- 三种模式行为与方案定义一致
- 模式切换后可通过自动化测试稳定验证

### S5. Agent TLS 校验与连接配置

- [ ] 为 Agent 增加 Bridge TLS 配置（Root CA、ServerName、开关）
- [ ] gRPC 通道移除默认 `insecure` 依赖，按配置启用 TLS 凭据
- [ ] TCP binding 在 TLS 模式下支持证书链、SAN、有效期校验
- [ ] 显式禁用 TLS 1.3 Early Data（0-RTT）

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

- [ ] 落地 `assigned_session_epoch`（候选）与 `auth_ack.session_epoch`（最终权威）的一致性约束
- [ ] 以 `connector_id` 粒度实现原子提交（事务/CAS/锁）
- [ ] 成功抢占限流（如 60s 内最多 3 次）
- [ ] 对并发失败握手统一返回 `auth_session_superseded`

验收标准：

- 无法出现同 connector 双 ACTIVE 会话
- 低 epoch 事件不会覆盖高 epoch 权威状态

### S8. 数据面关闭与回收语义对齐

- [ ] 固化 simultaneous close 规则：必要时回 `TrafficCloseAck`，且仅一次
- [ ] `TunnelRecycle` 发起权保持在 Bridge
- [ ] Server 无法确认安全回收时直接关闭 tunnel
- [ ] 双端并发 close 场景补齐时序回归测试

代码落点建议：

- `agent-core/runtime/agent/app/data_plane_runtime.go`
- `cloud-bridge/runtime/bridge/app/tunnel_runtime_adapter.go`
- `cloud-bridge/runtime/bridge/connectorproxy/*`

验收标准：

- 双端 close 不会出现 ACK 互等卡死
- recycle 失败路径统一降级为关闭

### S9. 回收错误码与告警口径统一

- [ ] 对齐 `ltfp/errors/codes.go` 与协议文档错误码集合
- [ ] 在 Agent/Bridge 回收拒绝分支统一填充错误码
- [ ] 同步告警、诊断、日志字段映射

代码落点建议：

- `ltfp/errors/codes.go`
- `agent-core/runtime/agent/app/data_plane_runtime.go`
- `cloud-bridge/runtime/bridge/connectorproxy/*`

验收标准：

- 同一失败原因在双端呈现相同错误码
- 诊断与告警规则可稳定命中

### S10. 观测与审计增强

- [ ] 新增认证与握手指标：成功率、错误码分布、supersede 次数、rate limit 次数
- [ ] 新增 TLS 模式拒绝指标：`required` 拒绝明文、`plaintext` 拒绝 TLS
- [ ] 审计日志保留 `connector_id/token_id(脱敏)/session_id/session_epoch/source_ip/error_code`

验收标准：

- 安全失败路径均可通过指标和日志追溯
- 日志中无高敏感凭证明文

### S11. 测试矩阵与发布门槛

- [ ] 单测：proto/validator/auth parser/hash/tls mode 判定/epoch 原子提交
- [ ] 集测：三种 TLS 模式 + 握手全链路 + 并发抢占 + token 状态切换
- [ ] 数据面：simultaneous close + recycle 成功/失败 + 错误码断言
- [ ] 回归：`grpc_h2` 与 `tcp_framed` 两种 binding parity

建议执行命令：

- `cd ltfp && go test ./...`
- `cd agent-core && go test ./...`
- `cd cloud-bridge && go test ./...`

验收标准：

- 关键链路测试全部通过后才允许合入主线
- 无高优先级已知缺陷遗留

### S12. 交付核对（Definition of Done）

- [ ] 方案文档、协议定义、实现代码、测试用例四者一致
- [ ] `auth_payload` 已完全移除
- [ ] TLS 三模式在配置、运行时、测试、观测中全部闭环
- [ ] §7.11 认证流程具备可复现实验与自动化回归

---

## 五、建议执行顺序

1. 先做 `S0/S6`，冻结协议结构与校验边界。
2. 再做 `S2/S3/S7`，完成 Bridge 认证核心能力与会话权威提交。
3. 接着做 `S4/S5`，打通 TLS 模式与 Agent 连接安全。
4. 然后做 `S8/S9`，对齐数据面关闭回收与错误码。
5. 最后做 `S10/S11/S12`，完成观测、测试与交付封板。

---

## 六、里程碑建议

- M1（协议冻结）：完成 `S0/S6`
- M2（认证可用）：完成 `S2/S3/S7`
- M3（TLS 可控）：完成 `S4/S5`
- M4（数据面一致）：完成 `S8/S9`
- M5（可发布）：完成 `S10/S11/S12`
