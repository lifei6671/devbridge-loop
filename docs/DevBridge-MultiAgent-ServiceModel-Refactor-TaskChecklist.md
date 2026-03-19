# DevBridge 多 Agent 服务模型重构任务清单

**文档状态**：Draft for Execution  
**基线文档**：`docs/DevBridge-MultiAgent-ServiceModel-TechDesign.md`（v1.5）  
**编制日期**：2026-03-19

---

## 1. 范围与原则

- 以 `DevBridge-MultiAgent-ServiceModel-TechDesign.md` 为唯一协议与实现基线。
- 本次是**彻底重构**，不做 `service_key` 兼容路径，不做双栈。
- Bridge 与 Agent 控制面、路由层、数据面、观测面一次性收敛到新语义。

## 2. 当前代码现状（项目实际情况）

当前实现仍以旧模型为主，关键差距如下：

- 协议与类型：`ltfp/proto/devbridge/loop/v2/ltfp.proto`、`ltfp/pb/types.go` 仍是 `service_id/service_key/hybrid_group` 语义。
- 校验与适配：`ltfp/validate/validator.go`、`ltfp/adapter/local_service_adapter.go` 仍强绑定 `service_key`。
- Bridge 服务注册：`cloud-bridge/runtime/bridge/registry/service_registry.go` 仍以 `byServiceID + byServiceKey` 为中心，未落地 `LogicalService + ServiceInstance(instance_id)`。
- Bridge 发布与路由：
  - `cloud-bridge/runtime/bridge/control/publish_handler.go` 仍按 `service_key -> service_id` 复用。
  - `cloud-bridge/runtime/bridge/routing/resolver.go` 仍按 `connector_service.service_key` 解析，并保留 `hybrid_group`。
  - `cloud-bridge/runtime/bridge/routing/matcher.go` 仅支持 `header_matches(map[string]string)` 精确匹配，不支持 `queries`、`prefix/regex/present`。
- 数据面：`TrafficOpen` 仍以 `service_id` 为主，`instance_id` 未进入权威字段。
- Agent：`agent-core/runtime/agent/app/runtime_bridge.go`、`agent-core/runtime/agent/control/health_reporter.go` 仍围绕 `service_id/service_key`。
- 管理面与观测：`adminview/snapshot.go`、`web/src/App.tsx` 仍展示 `service_key` 与 `hybrid_fallback_total`。

## 3. 优先级定义

- `P0`：阻塞新协议上线的核心路径，必须先完成。
- `P1`：保证可运维、可观测、可配置的关键配套。
- `P2`：增强项，非首批上线阻塞。

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

- [ ] C01 已确认本次为彻底重构，不引入任何 `service_key` 兼容逻辑。
- [ ] C02 当前阶段代码改动仅覆盖本阶段任务，不混入下一阶段能力。
- [ ] C03 当前阶段单测已补齐并通过（至少覆盖新增规则与拒绝路径）。
- [ ] C04 当前阶段关键日志与指标字段已同步更新。
- [ ] C05 当前阶段文档（字段、流程图、错误码）已同步。
- [ ] C06 阶段出口验收完成并记录结果后，再进入下一阶段。

### 7.2 每日推进要求（执行纪律）

- 每日开始前：确认当天仅推进一个执行阶段的剩余项。
- 每日结束前：更新阶段状态、阻塞项、次日计划。
- 出现跨阶段依赖冲突时：先回到上阶段补齐出口条件，再继续推进。

### 7.3 建议的阶段状态记录模板

| 日期 | 执行阶段 | 状态（NotStarted/InProgress/Done/Blocked） | 完成项 | 阻塞项 | 下一步 |
|---|---|---|---|---|---|
| YYYY-MM-DD | EXX | InProgress | - | - | - |
