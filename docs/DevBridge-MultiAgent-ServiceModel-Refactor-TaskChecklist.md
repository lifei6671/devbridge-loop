# DevBridge 多 Agent 服务模型重构任务清单

**文档状态**：Completed  
**基线文档**：`docs/DevBridge-MultiAgent-ServiceModel-TechDesign.md`（v1.5）  
**编制日期**：2026-03-19

---

## 1. 范围与原则

- 以 `DevBridge-MultiAgent-ServiceModel-TechDesign.md` 为唯一协议与实现基线。
- 本次是**彻底重构**，不做 `service_key` 兼容路径，不做双栈。
- Bridge 与 Agent 控制面、路由层、数据面、观测面一次性收敛到新语义。
- 代码必须符合Golang官方代码规范，必须增加函数级别和行内级别中文注释。

### 1.1 开工冻结决策（与 TechDesign 第12章一致）

- `instance_id` 持久化：本版仅进程内持久化，跨进程重启不保证复用。
- 全部实例不健康默认行为：`allow_degraded`，可通过策略改为 `reject`。
- Header 匹配规则：Header 名称不区分大小写，值区分大小写；本版不引入 `case_insensitive`。
- Admission 模式：同步拦截，不引入异步校验。
- scope Header 缺失行为：使用 `default_scope`，本版不默认返回 400。
- ScopeFallbackPolicy：本版为“本级 miss 即降级”，不引入条件触发器。
- 范围边界：`match_labels`、高级 `sticky_by`、host 模板扩展均在 P2，不阻塞 P0/P1 开工。

## 2. 当前交付状态（2026-03-19）

当前清单对应改造已完成，交付状态如下：

- 协议与类型：`ltfp` 已切换到 `logical_service_id`、`instance_id`、`Scope`、`ServiceSelector`、`RouteMatch.headers/queries` 新模型。
- 校验与错误码：旧 `service_key/service_id/namespace/environment` 主路径请求会被显式拒绝，新增实例归属与 session_epoch 相关错误码已接通。
- Bridge 控制面与注册表：已完成 `LogicalService + ServiceInstance` 两层注册表、Publish/Unpublish/Health 新语义切换。
- 路由与数据面：已完成 scope 降级链、external fallback gating、Header/Query matcher、regex cache、Admission 冲突检测、shadow warning、`TrafficOpen(logical_service_id + instance_id)` 全链路切换。
- Agent：已完成 `instance_id` 持久化复用、TrafficOpen 实例归属校验、自动路由切换到 `ServiceSelector`。
- 管理面与观测：已完成 `logical_service_id/instance_id/scope` 展示、`scope_fallback_total`/`bridge_host_derive_total`/`bridge_instance_selector_pick_total`/`bridge_route_conflict_rejection_total` 指标接通。
- 运行验证：`cloud-bridge`、`agent-core`、`ltfp` 已完成 `go test ./... -timeout 60s`，`cloud-bridge/web` 已完成 `npm run build`。

## 3. 优先级定义

- `P0`：阻塞新协议上线的核心路径，必须先完成。
- `P1`：保证可运维、可观测、可配置的关键配套。
- `P2`：增强项，非首批上线阻塞。

### 3.1 2026-03-19 修复补丁（控制面与入口严格化）

- 控制面实例归属：`UnpublishService(instance_id)` 仅允许删除当前 `connector/session` 归属实例；跨 connector/session 请求返回 `INSTANCE_OWNERSHIP_MISMATCH`。
- 入口 scope header：不再接受历史 header（`X-DevBridge-*`/`X-Namespace`/`X-Env`），检测到即返回 `UNSUPPORTED_LEGACY_PROTOCOL`，仅允许 `X-Bridge-Namespace` 与 `X-Bridge-Environment`。
- RouteAssign 目标类型：`route.target.type` 改为强约束，必须显式为 `connector_service` 或 `external_service`，缺失或非法值直接拒绝（`UNSUPPORTED_VALUE`）。
- 验证记录：`cloud-bridge/runtime/bridge/...` 已完成 `go test -timeout 120s ./runtime/bridge/...` 全绿。

---

## 4. 改造任务清单

| 任务ID | 优先级 | 改造点 | 主要改动路径 | 验收标准 |
|---|---|---|---|---|
| T01 | P0 | 协议字段整体切换到新模型（`logical_service_id`、`instance_id`、`Scope`、`ServiceSelector`、`RouteMatch.headers/queries`） | `ltfp/proto/devbridge/loop/v2/ltfp.proto`, `ltfp/pb/gen/...`, `ltfp/pb/types.go`, `ltfp/pb/resources.go` | 1) `service_key` 不再出现在核心控制面消息定义；2) `TrafficOpen` 含 `logical_service_id + instance_id`；3) 项目可编译通过 |
| T02 | P0 | 错误码与校验器重构，新增拒绝旧协议路径 | `ltfp/errors/codes.go`, `ltfp/validate/validator.go` | 1) 新增 `UNSUPPORTED_LEGACY_PROTOCOL`、`INSTANCE_OWNERSHIP_MISMATCH`、`STALE_SESSION_EPOCH`、`INSTANCE_NOT_FOUND` 等；2) 收到旧 `service_key` 字段请求时明确拒绝；3) 单测覆盖拒绝路径 |
| T03 | P0 | Bridge 注册表重构为两层模型（LogicalService + ServiceInstance） | `cloud-bridge/runtime/bridge/registry/service_registry.go`（建议拆分 logical/instance 子模块） | 1) 建立 `(service_name, scope)->logical_service_id` 唯一索引；2) `instance_id` 作为实例主键；3) 支持按 `logical_service_id` 查询可路由实例；4) 旧 `byServiceKey` 索引移除 |
| T04 | P0 | Publish/Unpublish/Health 控制面处理器切换新语义 | `cloud-bridge/runtime/bridge/control/publish_handler.go`, `health_handler.go` | 1) Publish 执行 R1/R2/R3 规则；2) Ack 返回 `logical_service_id + instance_id`；3) HealthReport 以 `instance_id` 定位；4) 相关审计字段切换完成 |
| T05 | P0 | Route Resolver 重构为“scope解析 -> 降级链 -> 路由匹配 -> 实例选择” | `cloud-bridge/runtime/bridge/routing/resolver.go`, `selector.go`, `instance_selector.go` | 1) 使用 `ServiceSelector` 解析目标；2) 实现 `ScopeFallbackPolicy`（可禁用）；3) 本地 miss 后可选 external fallback；4) 移除 `hybrid_group` 依赖 |
| T06 | P0 | RouteMatch 能力补齐（headers/queries + exact/prefix/regex/present） | `cloud-bridge/runtime/bridge/routing/matcher.go` | 1) 支持 Header 与 Query 组合 AND 匹配；2) regex 使用 RE2 并缓存编译结果；3) `path_prefix` 最长前缀 + priority 规则保持确定性 |
| T07 | P0 | Admission Pipeline：Route 冲突检测与 shadow warning | 新增 `cloud-bridge/runtime/bridge/admission/*`，接入 `control/route_handler.go` | 1) `host + path_prefix(完全相同) + headers + priority + target不同` 时拒绝；2) priority 不同仅 warning；3) 返回冲突 route_id |
| T08 | P0 | 数据面 `TrafficOpen`/Dispatcher/ConnectorProxy 全链路改造 | `cloud-bridge/runtime/bridge/connectorproxy/*.go`, `routing/executor.go`, `app/ingress_http_server.go` | 1) 下发 `logical_service_id + instance_id`；2) Agent 返回 `INSTANCE_NOT_FOUND` 时 Bridge 正确处理；3) 不再依赖 `service_key` 元数据路由 |
| T09 | P0 | Agent ServiceCatalog 与 PublishAck 回写改造（实例持久化） | `agent-core/runtime/agent/app/runtime_bridge.go`, `agent-core/runtime/agent/...catalog...` | 1) 记录 `instance_id` 并重连复用；2) Ack 以 `instance_id` 为权威更新本地缓存；3) 不再以 `service_key` 回填主标识 |
| T10 | P0 | Agent TrafficOpen 处理改造（校验实例归属） | `agent-core/runtime/agent/traffic/*`, `agent-core/runtime/agent/control/health_reporter.go` | 1) 按 `instance_id` 找本地实例；2) 不属于本 connector 返回 `INSTANCE_NOT_FOUND`；3) 健康上报按 `instance_id` |
| T11 | P0 | 移除旧 `hybrid_group/pre_open_only` 核心逻辑，统一为 ScopeFallbackPolicy | `cloud-bridge/runtime/bridge/routing/hybrid.go`, `routing/executor.go`, `ltfp/fallback/*`, `ltfp/routing/resolver.go` | 1) 运行主路径不再依赖 hybrid fallback policy；2) 指标从 `bridge_hybrid_fallback_total` 收敛到 scope fallback 指标；3) 旧路径测试替换为新策略测试 |
| T12 | P0 | 端到端回归与断言基线建设（新协议验收闸门） | `cloud-bridge/runtime/bridge/*_test.go`, `agent-core/runtime/agent/*_test.go`, `ltfp/*_test.go` | 1) 覆盖文档第10章边界场景；2) 增加“旧字段请求被拒绝”自动化测试；3) `go test ./...` 通过 |
| T13 | P1 | Bridge 配置模型增加 `default_scope` 与 `fallback_policies` | `cloud-bridge/runtime/bridge/app/config/config.go`, `config_yaml.go`, `config_yaml_test.go` | 1) 支持 namespace 级启停与链路配置；2) 配置合法性校验（去重、禁止空 scope、防环）；3) 配置热加载/重载策略明确 |
| T14 | P1 | scope Header 标准化与透传统一 | `app/ingress_http_server.go`, `ingress/*.go`, Agent upstream 转发链路 | 1) 统一使用 `X-Bridge-Namespace`、`X-Bridge-Environment`；2) Header 缺失时按 `default_scope`；3) 请求链路保持透传不改写 |
| T15 | P1 | 外部注册中心查询接口改为 scope 驱动 | `cloud-bridge/runtime/bridge/directproxy/discovery_adapter.go`, `ltfp/discovery/*` | 1) 外部查询输入包含 request_scope；2) 仅在本地降级链 miss 后触发；3) 观测字段可区分 local miss 与 external hit |
| T16 | P1 | 管理面快照与 UI 字段重构 | `cloud-bridge/runtime/bridge/adminview/snapshot.go`, `adminapi/server.go`, `web/src/App.tsx` | 1) 服务列表展示 `logical_service_id/instance_id/scope`；2) 路由目标展示 selector；3) 移除 UI 对 `service_key/hybrid_fallback_total` 的硬依赖 |
| T17 | P1 | 观测与审计字段迁移 | `cloud-bridge/runtime/bridge/obs/metrics.go`, `obs/logs.go`, 相关日志打点 | 1) 指标新增/重命名与文档8章一致；2) 日志字段包含 `request_scope/matched_scope/is_external_fallback`；3) 原关键指标可对账 |
| T18 | P1 | Route 自动生成逻辑从 `service_key` 切换到 `ServiceSelector` | `agent-core/runtime/agent/app/runtime_bridge.go`（`buildAutoRouteAssignPayload`） | 1) 自动路由 payload 不再写 `connector_service.service_key`；2) 使用 `service_name + scope` selector；3) 新增自动路由单测 |
| T19 | P2 | Host 自动派生能力落地（模板可配置） | Bridge 新增 `host_deriver` 模块，接入发布/路由路径 | 1) `exposure.host` 为空时按模板派生；2) 冲突时可被 Admission 拦截；3) 指标 `bridge_host_derive_total` 生效 |
| T20 | P2 | Selector 增强项（label selector、sticky_by、weighted） | `routing/selector.go`, `routing/instance_selector.go`, policy 结构 | 1) `match_labels`/`instance_labels` 可选启用；2) sticky_by（header/cookie/client_ip）可配置；3) weighted 策略有压测或统计验证 |

---

## 5. 里程碑建议

| 里程碑 | 包含任务 | 目标产出 |
|---|---|---|
| M1 协议与模型冻结 | T01-T04 | 新协议可收发，发布/下线/健康链路完成主字段切换 |
| M2 路由与数据面打通 | T05-T12 | scope 降级路由 + 实例选择 + 数据面全链路可运行，回归通过 |
| M3 运维与管理面收敛 | T13-T18 | 配置、观测、UI、自动路由全部与新模型一致 |
| M4 增强能力 | T19-T20 | 提升可用性与策略扩展能力 |

## 6. 总体验收门槛（Release Gate）

- 协议门槛：控制面与数据面不再接受旧 `service_key` 协议主路径。
- 功能门槛：`ScopeFallbackPolicy` 可配置生效，`RouteMatch` 扩展能力生效。
- 正确性门槛：边界场景（文档第10章）自动化测试全通过。
- 可观测门槛：新指标与新日志字段齐全，可定位 `logical_service_id + instance_id + scope`。
- 稳定性门槛：`go test ./...` 在 `cloud-bridge`、`agent-core`、`ltfp` 全绿。

**当前验收结论**：以上 Release Gate 已全部满足，本清单对应重构任务完成。

---

## 7. 执行任务清单（开发按单推进）

本节用于实际开发推进，要求按阶段执行，不跳步。每个阶段完成后，必须满足对应出口条件再进入下一阶段。

| 执行阶段 | 对应任务 | 入口条件 | 关键执行项 | 出口条件（验收） |
|---|---|---|---|---|
| E00 基线冻结 | - | 文档版本冻结（TechDesign v1.5） | 1) 冻结字段基线；2) 明确禁用兼容策略；3) 建立任务跟踪看板 | 1) 基线文档版本号固定；2) 执行清单评审通过 |
| E01 协议与类型改造 | T01 | E00 完成 | 1) 修改 proto 与 pb 类型；2) 移除控制面 `service_key` 主字段；3) 更新资源模型 | 1) 编译通过；2) 协议字段切换完成；3) 无旧字段主路径引用 |
| E02 校验与错误码改造 | T02 | E01 完成 | 1) 增加新错误码；2) 校验器拒绝旧字段请求；3) 补充拒绝路径单测 | 1) 旧协议请求可稳定拒绝；2) 单测通过 |
| E03 Bridge 注册与控制面 | T03, T04 | E02 完成 | 1) 注册表改为 LogicalService/ServiceInstance 两层；2) Publish/Unpublish/Health 切换到 `instance_id` | 1) PublishAck 返回 `logical_service_id + instance_id`；2) Health 可按 `instance_id` 定位 |
| E04 路由核心改造 | T05, T06, T07 | E03 完成 | 1) Resolver 引入 scope 降级链；2) Matcher 支持 headers/queries 与 exact/prefix/regex/present；3) Admission 冲突检测上线 | 1) 路由匹配确定性通过；2) 冲突检测生效（含 warning/deny） |
| E05 数据面链路改造 | T08 | E04 完成 | 1) Dispatcher/ConnectorProxy/Ingress 全链路切换新字段；2) 错误回传路径处理 `INSTANCE_NOT_FOUND` | 1) 数据面不再依赖 `service_key`；2) 新错误码处理正确 |
| E06 Agent 侧改造 | T09, T10, T18 | E05 完成 | 1) ServiceCatalog 记录并复用 `instance_id`；2) TrafficOpen 按 `instance_id` 找实例；3) 自动路由改为 `ServiceSelector` | 1) Agent 重连后实例标识稳定；2) 自动路由 payload 新语义正确 |
| E07 旧语义清理与回归 | T11, T12 | E06 完成 | 1) 移除 `hybrid_group/pre_open_only` 主路径；2) 完成边界场景自动化测试 | 1) `go test ./...` 全绿；2) 不再存在旧 fallback 主路径依赖 |
| E08 运维与管理面收敛 | T13, T14, T15, T16, T17 | E07 完成 | 1) 配置模型上线；2) scope header 标准化；3) 外部发现接口切 scope；4) UI/指标/日志切新模型 | 1) 管理面字段全部切换；2) 新观测字段可定位完整链路 |
| E09 增强能力 | T19, T20 | E08 完成 | 1) Host 自动派生；2) selector 高级能力（label/sticky/weighted） | 1) 增强能力通过专项验证；2) 不影响主链路稳定性 |

### 7.1 每阶段执行 Checklist（可直接勾选）

- [x] C01 已确认本次为彻底重构，不引入任何 `service_key` 兼容逻辑。
- [x] C02 当前阶段代码改动仅覆盖本阶段任务，不混入下一阶段能力。
- [x] C03 当前阶段单测已补齐并通过（至少覆盖新增规则与拒绝路径）。
- [x] C04 当前阶段关键日志与指标字段已同步更新。
- [x] C05 当前阶段文档（字段、流程图、错误码）已同步。
- [x] C06 阶段出口验收完成并记录结果后，再进入下一阶段。

### 7.2 每日推进要求（执行纪律）

- 每日开始前：确认当天仅推进一个执行阶段的剩余项。
- 每日结束前：更新阶段状态、阻塞项、次日计划。
- 出现跨阶段依赖冲突时：先回到上阶段补齐出口条件，再继续推进。

### 7.3 建议的阶段状态记录模板

| 日期 | 执行阶段 | 状态（NotStarted/InProgress/Done/Blocked） | 完成项 | 阻塞项 | 下一步 |
|---|---|---|---|---|---|
| 2026-03-19 | E04 | Done | 已补齐 `RouteMatch.queries`、regex cache、Admission 冲突检测与 shadow warning；`bridge_route_conflict_rejection_total` 指标已接通；路由冲突可返回 `conflict_route_id` | 无 | 继续完成剩余阶段并收口 Release Gate |
| 2026-03-19 | E07 | Done | 已移除 `hybrid_group/pre_open_only` 主路径；已删除 `ltfp/fallback/*` 与 Bridge hybrid resolver/executor 路径；旧路径测试已替换，`ltfp/cloud-bridge/agent-core` 均已完成 `go test ./... -timeout 60s` | 无 | 进入 E08，推进 `default_scope/fallback_policies`、scope header 标准化、external discovery scope 化 |
| 2026-03-19 | E08 | Done | 已完成 E01-E07；已完成 `default_scope/fallback_policies` 配置模型、合法性校验与 YAML/管理面快照回写；HTTP/gRPC ingress 已统一收敛到 `X-Bridge-Namespace/X-Bridge-Environment` 并在缺失时使用 `default_scope`；resolver 已改为“本地 connector 优先、local miss 后再落 external”，并新增按 namespace policy 的 `external.enabled` 显式 gating；`request_scope/matched_scope/is_external_fallback` 已进入 direct path 日志字段、traffic ownership/admin API 与 Web UI 查询面板；Ops UI 已支持 `default_scope.namespace/default_scope.environment` 配置补丁 | 无 | 进入 E09，开始推进 Host 自动派生与 selector 增强能力 |
| 2026-03-19 | E09 | Done | 已完成 T19-T20：新增 `ingress.base_domain` 配置与 `hostderiver` 模块；`PublishService` 与 `RouteAssign` 在空 host 时可按 `service_name + scope + base_domain` 自动派生；resolver 已支持 label-only `ServiceSelector.match_labels` 解析、`instance_labels` 实例过滤、`load_balance_policy=sticky/weighted`；`sticky_by` 已支持 `client_ip/header:/cookie:`；`bridge_host_derive_total` 与 `bridge_instance_selector_pick_total` 指标已接通；`cloud-bridge` 已完成 `go test ./... -timeout 60s` 与 `cloud-bridge/web` `npm run build` | 无 | 进入后续收尾/发布闸门或继续补全跨模块专项回归 |
| 2026-03-19 | Release Gate | Done | 已完成 `cloud-bridge`、`agent-core`、`ltfp` 全量 `go test ./... -timeout 60s`；`cloud-bridge/web` `npm run build` 通过；协议、功能、正确性、可观测、稳定性门槛全部达成 | 无 | 本任务清单关闭 |

---

## 8. 子任务级拆分（WBS）

本节将 `E00-E09` 进一步拆成可直接开工的子任务，建议每个子任务独立提交 PR，避免跨阶段混改。

| 子任务ID | 所属阶段 | 具体改造点 | 主要文件/模块 | 前置依赖 | 完成定义（DoD） |
|---|---|---|---|---|---|
| W00-01 | E00 | 冻结字段词典（logical_service_id/instance_id/scope/selector） | `docs/DevBridge-MultiAgent-ServiceModel-TechDesign.md` | 无 | 词典章节冻结，评审结论记录 |
| W00-02 | E00 | 输出“禁用兼容”决议与约束清单 | 本文档 + TechDesign | W00-01 | 明确写入“不接受 service_key 兼容路径” |
| W01-01 | E01 | 修改 proto：替换核心字段模型 | `ltfp/proto/devbridge/loop/v2/ltfp.proto` | W00-02 | proto 字段切换完成、无 service_key 主路径 |
| W01-02 | E01 | 重新生成 pb 并修正编译断点 | `ltfp/pb/gen/*`, `ltfp/pb/types.go` | W01-01 | `go build` 通过，pb 类型与 proto 一致 |
| W01-03 | E01 | 资源模型同步（RouteMatch/ServiceSelector/Scope） | `ltfp/pb/resources.go` | W01-02 | 资源类型字段与设计文档一致 |
| W02-01 | E02 | 新增错误码（legacy/instance/session） | `ltfp/errors/codes.go` | W01-03 | 新错误码可被桥与 agent 引用 |
| W02-02 | E02 | 校验器拒绝旧字段请求 | `ltfp/validate/validator.go` | W02-01 | 旧字段请求返回明确错误码 |
| W02-03 | E02 | 增加拒绝路径单测 | `ltfp/validate/*_test.go` | W02-02 | 单测覆盖旧请求拒绝与边界条件 |
| W03-01 | E03 | 注册表拆分 logical/instance 结构 | `cloud-bridge/runtime/bridge/registry/*` | W02-03 | 支持 `(service_name,scope)->logical_service_id` 唯一索引 |
| W03-02 | E03 | 发布处理改造（R1/R2/R3） | `control/publish_handler.go` | W03-01 | PublishAck 返回 `logical_service_id+instance_id` |
| W03-03 | E03 | 健康处理改造（按 instance_id） | `control/health_handler.go` | W03-02 | HealthReport 按实例定位与更新 |
| W04-01 | E04 | Resolver 接入 scope 解析与降级链 | `routing/resolver.go` | W03-03 | 支持可配置降级链且可禁用 |
| W04-02 | E04 | Matcher 支持 headers/queries 四种操作符 | `routing/matcher.go` | W04-01 | exact/prefix/regex/present 全可用 |
| W04-03 | E04 | Admission 冲突检测（相同 path_prefix 拒绝） | `routing/admission/*`, `control/route_handler.go` | W04-02 | 冲突 route 被拒绝并返回冲突标识 |
| W04-04 | E04 | priority 差异仅告警 | 同 W04-03 | W04-03 | 生成 warning，不阻断发布 |
| W05-01 | E05 | TrafficOpen 字段切换到 logical+instance | `connectorproxy/*`, `routing/executor.go` | W04-04 | 数据面下发字段完成替换 |
| W05-02 | E05 | 处理 `INSTANCE_NOT_FOUND` 回传策略 | `connectorproxy/*`, `executor.go` | W05-01 | Bridge 对该错误的处理符合文档定义 |
| W05-03 | E05 | ingress 不再依赖 service_key 元数据 | `app/ingress_http_server.go` | W05-02 | 链路中不再读取 service_key 路由 |
| W06-01 | E06 | Agent catalog 持久化 instance_id | `agent-core/runtime/agent/app/runtime_bridge.go` | W05-03 | 重连复用 instance_id 生效 |
| W06-02 | E06 | Agent TrafficOpen 按 instance_id 定位 | `agent-core/runtime/agent/traffic/*` | W06-01 | 非本 connector 返回 `INSTANCE_NOT_FOUND` |
| W06-03 | E06 | 健康上报按 instance_id | `agent-core/runtime/agent/control/health_reporter.go` | W06-02 | 健康上报与实例绑定一致 |
| W06-04 | E06 | 自动路由 payload 切换为 selector | `runtime_bridge.go` (`buildAutoRouteAssignPayload`) | W06-03 | 不再写 `connector_service.service_key` |
| W07-01 | E07 | 清理 hybrid_group/pre_open_only 主路径 | `routing/hybrid.go`, `ltfp/fallback/*` | W06-04 | 主执行链不再引用旧语义 |
| W07-02 | E07 | 新增边界回归用例（第10章） | `cloud-bridge/*_test.go`, `agent-core/*_test.go` | W07-01 | 边界用例齐全并稳定通过 |
| W07-03 | E07 | 全仓测试收敛 | `cloud-bridge`, `agent-core`, `ltfp` | W07-02 | `go test ./...` 全绿 |
| W08-01 | E08 | 配置模型落地（default_scope/fallback_policies） | `app/config/config.go`, `config_yaml.go` | W07-03 | 配置加载、校验、重载策略明确 |
| W08-02 | E08 | 标准 scope header 接入与透传 | `app/ingress_http_server.go`, `ingress/*` | W08-01 | `X-Bridge-Namespace/Environment` 全链路可用 |
| W08-03 | E08 | 外部 discovery 接口 scope 化 | `directproxy/discovery_adapter.go`, `ltfp/discovery/*` | W08-02 | 仅本地 miss 后触发外部查询 |
| W08-04 | E08 | Admin Snapshot/API/UI 字段切换 | `adminview/snapshot.go`, `adminapi/server.go`, `web/src/App.tsx` | W08-03 | UI 展示 logical/instance/scope 新字段 |
| W08-05 | E08 | 指标与审计字段切换 | `obs/metrics.go`, `obs/logs.go` | W08-04 | 可观测链路可按 logical+instance+scope 定位 |
| W09-01 | E09 | Host 自动派生（模板化） | 新增 `host_deriver` 模块 | W08-05 | 空 host 可自动派生且冲突可拦截 |
| W09-02 | E09 | Selector 增强（labels/sticky/weighted） | `routing/selector.go`, `instance_selector.go` | W09-01 | 增强策略可配置并通过验证 |

### 8.1 建议 PR 拆分顺序（按可合并单元）

| PR序号 | 建议包含子任务 | 目标 |
|---|---|---|
| PR-01 | W00-01, W00-02 | 冻结基线与执行约束 |
| PR-02 | W01-01 ~ W01-03 | 协议与类型切换 |
| PR-03 | W02-01 ~ W02-03 | 校验与错误码 |
| PR-04 | W03-01 ~ W03-03 | Bridge 注册与控制面 |
| PR-05 | W04-01 ~ W04-04 | 路由核心能力 |
| PR-06 | W05-01 ~ W05-03 | 数据面全链路 |
| PR-07 | W06-01 ~ W06-04 | Agent 侧能力 |
| PR-08 | W07-01 ~ W07-03 | 旧语义清理与回归 |
| PR-09 | W08-01 ~ W08-05 | 运维与管理面 |
| PR-10 | W09-01, W09-02 | 增强能力 |

### 8.2 每个子任务的执行闭环

- 开工前：确认前置依赖子任务状态为 Done。
- 开发中：仅修改子任务声明的文件范围，避免扩散改动。
- 提交前：补充对应测试与文档，确保 DoD 可验证。
- 合并前：在阶段看板标记状态与风险，更新阻塞项。

---

## 9. 按周执行排期（建议）

说明：以下按 1-2 人并行开发估算，单位为“人天（PD）”。若单人推进，周数顺延。

| 周次 | 目标阶段 | 计划子任务 | 预估PD | 周验收标准 |
|---|---|---|---|---|
| W1 | E00-E01 | W00-01, W00-02, W01-01, W01-02, W01-03 | 5-7 PD | 协议与 pb 类型切换完成，主干可编译 |
| W2 | E02-E03 | W02-01, W02-02, W02-03, W03-01, W03-02, W03-03 | 6-8 PD | 旧字段拒绝生效，Publish/Health 改造可跑通 |
| W3 | E04 | W04-01, W04-02, W04-03, W04-04 | 5-7 PD | scope 降级与冲突检测生效，匹配规则稳定 |
| W4 | E05-E06 | W05-01, W05-02, W05-03, W06-01, W06-02, W06-03, W06-04 | 7-9 PD | 数据面与 Agent 全链路切换完成 |
| W5 | E07-E08 | W07-01, W07-02, W07-03, W08-01, W08-02, W08-03, W08-04, W08-05 | 8-10 PD | 旧语义清理完成，测试全绿，管理面/观测收敛 |
| W6 | E09 + 收尾 | W09-01, W09-02 + 缺陷修复 + 文档收口 | 4-6 PD | 增强能力可用，满足 Release Gate |

### 9.1 周推进节奏（固定动作）

- 周一：冻结本周子任务范围，确认依赖已满足。
- 周三：中期检查，只处理阻塞项，不扩新范围。
- 周五：完成周验收标准，更新阶段状态与风险台账。

### 9.2 子任务优先执行顺序（当周内）

1. 先做“字段与模型定义”（proto/pb/config）。
2. 再做“控制面状态写入”（publish/health/registry）。
3. 再做“路由与数据面消费字段”。
4. 最后做“UI/观测/增强项”。

---

## 10. 人天估算与缓冲策略

| 类型 | 建议比例 | 说明 |
|---|---|---|
| 开发实现 | 60% | 代码重构与联调 |
| 测试与回归 | 25% | 单测、集成测试、边界场景 |
| 文档与评审 | 10% | 设计同步、变更记录 |
| 风险缓冲 | 5% | 处理跨模块耦合与返工 |

| 风险ID | 风险描述 | 触发信号 | 应对策略 |
|---|---|---|---|
| R-01 | proto 切换导致大面积编译断点 | pb 生成后断点>20处 | 拆 PR，先过类型层再做行为层 |
| R-02 | 路由新旧语义混用 | resolver/matcher 出现双字段判断 | 强制删除旧字段入口并加 lint 检查 |
| R-03 | Agent/Bridge 字段不一致 | TrafficOpen/PublishAck 互认失败 | 增加端到端契约测试先行 |
| R-04 | 观测字段滞后导致无法定位问题 | 指标/日志缺 logical+instance+scope | 每阶段完成即补观测字段，不后置 |
| R-05 | 范围蔓延导致延期 | 同周出现跨阶段任务 | 执行“阶段出口未达成不得跳步” |

---

## 11. 开工即用清单（首周）

| 序号 | 动作 | 输出物 | 完成判定 |
|---|---|---|---|
| D1 | 冻结词典与禁用兼容结论 | 文档变更记录 | 评审通过且已合并 |
| D2 | 完成 proto 字段切换 | proto diff + 评审记录 | 不再出现控制面 `service_key` 主字段 |
| D3 | 生成 pb 并修复类型编译 | pb 更新提交 | `go build` 通过 |
| D4 | 补充基础拒绝测试样例 | `*_test.go` | 旧字段请求拒绝测试通过 |
| D5 | 建立阶段看板状态 | 周报/看板快照 | E01 状态为 Done |

### 11.1 每日站会最小模板

| 项目 | 内容 |
|---|---|
| 昨日完成 | 完成的子任务ID（如 W01-01） |
| 今日计划 | 今日要完成的子任务ID |
| 当前阻塞 | 具体阻塞点与需要支持 |
| 风险变化 | 新增风险或风险解除 |
