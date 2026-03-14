# Task Plan: Bridge 管理后台方案拆解与落地

## Goal
将 `docs/BridgeAdminBackendTechnicalProposal.md` 拆解成可执行、可验收、可持续跟踪的任务清单，并完成一批可运行的后端落地实现与测试。

## Current Phase
Phase 5

## Phases

### Phase 1: 需求与现状梳理
- [x] 提取方案中的模块、接口、权限、安全与阶段目标
- [x] 对齐当前仓库已实现能力与缺口
- [x] 记录关键发现到 `findings.md`
- **Status:** complete

### Phase 2: 执行清单落盘
- [x] 新增 Bridge Admin 可执行清单文档（任务编号/验收标准/状态）
- [x] 把已完成项与未完成项映射到里程碑
- [x] 回填与 UI-Agent 总清单的交叉引用
- **Status:** complete

### Phase 3: 第二阶段能力落地（写接口）
- [x] 落地受控运维接口（reload/session drain/connector drain）
- [x] 落地 `PUT /api/admin/config` 的 `if_match_version` 并发校验
- [x] 落地诊断导出（admin 限制 + 脱敏 + 短时下载链接）
- [x] 增加写操作审计参数摘要
- **Status:** complete

### Phase 4: 测试与验证
- [x] 补齐 adminapi 单测（鉴权、权限矩阵、冲突语义、导出链路）
- [x] 补齐 app 集成测试（drain 副作用、config 版本冲突）
- [x] 运行 `go test ./...` 通过
- **Status:** complete

### Phase 5: 文档回写与交付
- [x] 更新 `UI-Agent-Bridge-Unimplemented-Checklist.md` 的 A2 阶段记录
- [x] 输出本轮“已落地 + 剩余任务”说明
- **Status:** complete

## Key Questions
1. 本轮“落地”优先覆盖哪些可验证能力（写接口/安全/导出）？
2. 动态配置更新采用“立即生效”还是“分阶段快照生效 + 重启生效提示”？

## Decisions Made
| Decision | Rationale |
|----------|-----------|
| 先做可执行清单再做代码实现 | 先统一任务边界，避免开发范围漂移 |
| 本轮优先落地 A16 中可独立验证的写接口与并发控制 | 风险最低、收益高、可快速形成可测成果 |

## Errors Encountered
| Error | Attempt | Resolution |
|-------|---------|------------|
| 暂无 | 1 | - |
