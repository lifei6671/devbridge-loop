# Findings & Decisions

## Requirements
- 用户目标：把 `BridgeAdminBackendTechnicalProposal.md` 拆成可执行任务清单，并直接推进代码落地。
- 需要同时满足：任务可执行、可验收、可追踪状态；不是纯文档。
- 项目已有约束：新增代码需包含中文函数级与行内注释。

## Research Findings
- 方案文档已经定义了完整资源域与分阶段顺序（阶段一到四），可直接映射为执行清单。
- 当前仓库已完成管理面第一阶段骨架：`adminapi/` 与 `adminview/` 已落地，读接口、Bearer 鉴权、角色模型、分页/时间窗约束已存在。
- 当前缺口集中在 A16 第二阶段：受控写接口、配置并发控制、导出安全与更细粒度审计。
- 当前 `adminapi/server.go` 仅注册只读路由，尚未注册 `/api/admin/ops/*` 与 `PUT /api/admin/config`。
- 本轮已补齐第二阶段核心接口：`ops/config/reload`、`ops/session/:id/drain`、`ops/connector/:id/drain`、`PUT /api/admin/config`、`ops/diagnose/export` 与下载链路。
- 导出链路已增加统一脱敏（敏感键 + 连接串口令）与 10 分钟短时令牌下载。
- 已新增 cookie 鉴权模式下的写接口 CSRF 防护：`Origin/Referer` 来源校验 + CSRF 双提交校验。
- 已新增管理面监听地址隔离校验：默认禁止与业务/控制/指标监听地址复用，需显式 `allow_shared_listener=true` 才允许。
- 导出下载链路已加固为“发起人绑定 + 一次性下载令牌 + no-store”。
- BMA-15 前端自动刷新已增强为“轮询兜底 + 定时重试恢复 SSE”，避免长时间停留在 polling 模式。
- BMA-16 单实例联调已补齐导出下载与 cookie+csrf 写接口测试覆盖，读写链路闭环完整。

## Technical Decisions
| Decision | Rationale |
|----------|-----------|
| 新增 `docs/BridgeAdminBackendExecutionChecklist.md` 作为执行清单 | 与技术方案解耦，便于按里程碑持续勾选 |
| 本轮优先落地写接口 + 并发控制 + 导出安全 | 对 A16 验收价值高，且可通过单测闭环 |
| 配置更新先采用“受控字段 + 版本冲突保护” | 在不引入大规模热更新复杂度前，先确保一致性语义 |
| 配置更新返回 `staged_requires_restart` | 明确告知当前更新为受控快照语义，不误导为完全热更新 |
| Cookie 鉴权仅在写请求启用 CSRF 校验 | 读请求无需额外负担，同时覆盖高风险写路径 |
| 导出下载采用“同操作者 + 一次性链接” | 降低分享/重放风险，提升可追溯性 |

## Issues Encountered
| Issue | Resolution |
|-------|------------|
| 工作区存在大量历史改动，需避免误触 | 仅改动 admin 相关文件并通过测试验证 |

## Resources
- `docs/BridgeAdminBackendTechnicalProposal.md`
- `docs/Agent-and-Bridge-ExecutionChecklist.md`（A16）
- `docs/UI-Agent-Bridge-Unimplemented-Checklist.md`（UAB-A2 阶段记录）
- `cloud-bridge/runtime/bridge/adminapi/server.go`
- `cloud-bridge/runtime/bridge/app/bootstrap.go`
- `cloud-bridge/runtime/bridge/adminapi/export_store.go`
- `cloud-bridge/runtime/bridge/app/config.go`

## Visual/Browser Findings
- 本轮未使用浏览器与图片工具。
