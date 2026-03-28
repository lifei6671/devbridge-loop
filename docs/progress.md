# Progress Log

## Session: 2026-03-14

### Phase 1: 需求与现状梳理
- **Status:** complete
- **Started:** 2026-03-14 16:xx
- Actions taken:
  - 阅读 `BridgeAdminBackendTechnicalProposal.md` 全文并提取模块/API/安全/阶段要点。
  - 对比当前代码：确认只读 admin API 已具备，写接口与配置并发控制缺失。
  - 汇总为执行导向结论写入 `findings.md`。
- Files created/modified:
  - `docs/findings.md`（created）
  - `docs/task_plan.md`（created）

### Phase 2: 执行清单落盘
- **Status:** complete
- Actions taken:
  - 新增 `docs/BridgeAdminBackendExecutionChecklist.md`，按 `M1~M4` 拆解为任务编号、代码入口和验收标准。
  - 把方案阶段一/二已落地与待办状态回填到可勾选项。
- Files created/modified:
  - `docs/BridgeAdminBackendExecutionChecklist.md`（created）
  - `docs/progress.md`（updated）

### Phase 3: 第二阶段能力落地（写接口）
- **Status:** complete
- Actions taken:
  - `adminapi` 新增写接口与角色约束：`reload`、`session drain`、`connector drain`、`config update`、`diagnose export/download`。
  - 增加导出脱敏策略（敏感键 + 连接串口令）与短时下载令牌缓存。
  - `app` 层新增 `adminRuntimeConfigStore` 与 drain 回调，实现配置并发版本控制和 registry 收敛副作用。
- Files created/modified:
  - `cloud-bridge/runtime/bridge/adminapi/server.go`（modified）
  - `cloud-bridge/runtime/bridge/adminapi/ops.go`（created）
  - `cloud-bridge/runtime/bridge/adminapi/export_store.go`（created）
  - `cloud-bridge/runtime/bridge/adminapi/export_sanitize.go`（created）
  - `cloud-bridge/runtime/bridge/app/admin_runtime_ops.go`（created）
  - `cloud-bridge/runtime/bridge/app/bootstrap.go`（modified）

### Phase 4: Admin UI 联调增强（BMA-15）
- **Status:** in progress
- Actions taken:
  - 六类页面持续绑定真实 API 数据，不使用占位内容。
  - 完成详情抽屉与表格联动增强：当前行高亮、抽屉切换自动定位、整行点击打开详情。
  - 新增键盘可达性（`Enter/Space` 打开详情）与顶部详情上下文条（支持“跳回源页面 / 关闭详情”）。
- Files created/modified:
  - `cloud-bridge/web/src/App.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/BridgeAdminBackendExecutionChecklist.md`（modified）

### Phase 5: 单二进制联调验证（BMA-16）
- **Status:** in progress
- Actions taken:
  - 在 `bootstrap_test.go` 新增 `TestBootstrapServesAdminUIAndAPIOnSingleServer`。
  - 覆盖同一 admin server 的三段链路：`/console/` UI 可达、`/api/admin/bridge/overview` 可读、`/api/admin/ops/config/reload` 可写。
  - 已对齐默认配置语义（`admin.enabled=false`、`admin.ui_enabled=false`），`app` 包全量测试恢复通过。
- Files created/modified:
  - `cloud-bridge/runtime/bridge/app/bootstrap_test.go`（modified）
  - `cloud-bridge/runtime/bridge/app/config.go`（modified）
  - `docs/BridgeAdminBackendExecutionChecklist.md`（modified）

### Phase 6: YAML 配置文件启动能力
- **Status:** in progress
- Actions taken:
  - 新增 `app.LoadConfigFromYAMLFile` / `app.ParseConfigYAML`，支持从 YAML 文件加载配置并严格校验未知字段。
  - `cmd/cloud-bridge` 增加 `-config <path>` 启动参数；未指定时保持默认配置启动。
  - 新增配置加载单测与示例配置文件 `cloud-bridge/config.example.yaml`。
- Files created/modified:
  - `cloud-bridge/runtime/bridge/app/config_yaml.go`（created）
  - `cloud-bridge/runtime/bridge/app/config_yaml_test.go`（created）
  - `cloud-bridge/cmd/cloud-bridge/main.go`（modified）
  - `cloud-bridge/config.example.yaml`（created）
  - `cloud-bridge/go.mod`（modified）
  - `cloud-bridge/go.sum`（modified）

### Phase 7: SSE 实时刷新协议与联调（BMA-15/BMA-16）
- **Status:** in progress
- Actions taken:
  - 新增 SSE 协议文档，明确事件模型、查询参数、payload 契约与重连降级语义。
  - 后端新增 `GET /api/admin/events/stream`，实现 `bridge.ready / bridge.snapshot / bridge.heartbeat` 推送。
  - 鉴权增强：SSE 与普通 Admin API 统一复用浏览器登录会话，不再依赖 `access_token` query 或静态 Bearer Token。
  - 前端接入 `EventSource`，实现 SSE 优先 + 轮询兜底；顶部状态文案可区分连接中/已连接/回退轮询。
  - 新增 SSE 单测与 bootstrap 级联调测试，覆盖协议解析、路由鉴权、单实例 server SSE 输出能力。
  - 完成真实运行态 smoke test：`go run` 启动 bridge 后，`curl -N` 成功接收 `bridge.ready` 与连续 `bridge.snapshot`。
- Files created/modified:
  - `docs/BridgeAdminSSEProtocol.md`（created）
  - `cloud-bridge/runtime/bridge/adminapi/sse.go`（created）
  - `cloud-bridge/runtime/bridge/adminapi/sse_test.go`（created）
  - `cloud-bridge/runtime/bridge/adminapi/server.go`（modified）
  - `cloud-bridge/web/src/App.tsx`（modified）
  - `cloud-bridge/runtime/bridge/app/bootstrap_test.go`（modified）
  - `docs/BridgeAdminBackendExecutionChecklist.md`（modified）

### Phase 8: 安全收口实现（BMA-12/BMA-13/BMA-14）
- **Status:** complete
- Actions taken:
  - 将管理面鉴权从静态 token 重构为 `auth_providers + session_cookie_name + csrf_cookie_name + csrf_header_name + allowed_origins`，首版落地本地账号密码登录。
  - 补充兼容迁移：历史 `auth_tokens/auth_mode/cookie_token_name` 配置在加载阶段自动折算到新认证结构，避免升级后直接启动失败。
  - 管理台前端改为从 `auth/session` / `auth/login` 响应读取 `csrf_header_name`，并在会话漂移导致的 CSRF 403 后自动刷新一次会话再重试写请求。
  - 增加 `admin.allow_shared_listener` 与默认隔离校验，禁止管理面与控制面/业务面/指标面监听地址冲突（可显式放开）。
  - 导出下载链路增强为“发起人绑定 + 一次性下载链接 + no-store 响应头”，并补齐对应用例。
  - 补充 `adminapi/app` 单测覆盖浏览器登录、session+csrf、防重放下载、网络隔离配置校验。
- Files created/modified:
  - `cloud-bridge/runtime/bridge/adminapi/server.go`（modified）
  - `cloud-bridge/runtime/bridge/adminapi/auth.go`（created）
  - `cloud-bridge/runtime/bridge/adminapi/export_store.go`（modified）
  - `cloud-bridge/runtime/bridge/adminapi/ops.go`（modified）
  - `cloud-bridge/runtime/bridge/adminapi/ops_test.go`（modified）
  - `cloud-bridge/runtime/bridge/app/config.go`（modified）
  - `cloud-bridge/runtime/bridge/app/bootstrap.go`（modified）
  - `cloud-bridge/runtime/bridge/app/admin_runtime_ops.go`（modified）
  - `cloud-bridge/runtime/bridge/app/bootstrap_test.go`（modified）
  - `docs/BridgeAdminBackendExecutionChecklist.md`（modified）

### Phase 9: BMA-15/BMA-16 收口完成
- **Status:** complete
- Actions taken:
  - 前端自动刷新增强：SSE 握手失败回退轮询后，增加定时重连机制，自动尝试恢复 SSE 实时流。
  - 补齐单实例集成测试：新增导出下载一次性令牌回归测试与 cookie+csrf 写接口校验测试。
  - 回写跨文档状态：同步勾选 `BridgeAdminBackendExecutionChecklist`、`Agent-and-Bridge-ExecutionChecklist`、`UI-Agent-Bridge-Unimplemented-Checklist`。
- Files created/modified:
  - `cloud-bridge/web/src/App.tsx`（modified）
  - `cloud-bridge/runtime/bridge/app/bootstrap_test.go`（modified）
  - `docs/BridgeAdminBackendExecutionChecklist.md`（modified）
  - `docs/Agent-and-Bridge-ExecutionChecklist.md`（modified）
  - `docs/UI-Agent-Bridge-Unimplemented-Checklist.md`（modified）
  - `docs/task_plan.md`（modified）
  - `docs/progress.md`（updated）

## Session: 2026-03-22

### Phase 10: 运维配置组件化与分层配置持久化
- **Status:** complete
- Actions taken:
  - 新增运行时配置分层加载：`显式 -config > 环境变量 > 程序运行目录 bridge.yaml > 用户目录 > 系统目录 > 默认值`，并按 Linux XDG / Windows Known Folders 解析配置路径。
  - `adminRuntimeConfigStore` 现在会把配置写回当前已加载且可编辑的最高优先级文件（显式 `-config`、程序运行目录、用户目录；若用户层尚不存在则优先创建用户目录 override，仅在用户配置路径不可用时才退回系统目录），`/api/admin/config/snapshot` 同步返回 `config_file_path`、`config_file_source`、`base_config_file_path`、`field_sources`、`editable_file_patch`、`field_restore_preview`。
  - `PUT /api/admin/config` 支持用 `null` 删除当前写回目标文件中的字段，恢复继承更低优先级配置。
  - 运维页组件化表单与 YAML patch 入口同步切换为“当前可编辑配置文件”语义：保存提示、环境变量提示、回填 patch、恢复继承和 Managed CA 默认路径都会跟随实际写回目标层变化；未显式填写 CA 路径时，默认 Root CA 证书与私钥会直接落在配置文件同目录。
- Files created/modified:
  - `cloud-bridge/runtime/bridge/app/runtime_config_loader.go`（created）
  - `cloud-bridge/runtime/bridge/app/runtime_config_loader_test.go`（created）
  - `cloud-bridge/runtime/bridge/app/admin_runtime_config_store_test.go`（created）
  - `cloud-bridge/runtime/bridge/app/admin_runtime_ops.go`（modified）
  - `cloud-bridge/cmd/cloud-bridge/main.go`（modified）
  - `cloud-bridge/web/src/admin/components/pages/OpsPage.tsx`（modified）
  - `cloud-bridge/web/src/admin/components/ops/ConfigFieldRow.tsx`（modified）
  - `cloud-bridge/web/src/admin/components/ops/ConfigSectionCard.tsx`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminOpsActions.ts`（modified）
  - `cloud-bridge/web/src/admin/model/ops-config.ts`（created）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/BridgeAdminBackendTechnicalProposal.md`（modified）
  - `docs/progress.md`（updated）

### Phase 11: 管理台吸顶头部精简
- **Status:** complete
- Actions taken:
  - 管理台统一头部重构为吸顶布局，滚动页面时始终固定在顶部。
  - 移除顶栏搜索框、Bearer Token 应用入口、自动刷新开关与刷新间隔选择，只保留页面标题、实时状态和手动刷新。
  - 清理前端 view model 中已不再使用的快速跳页、token 草稿与相关动作出口，并避免旧 localStorage 自动刷新偏好导致隐藏状态残留。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/AdminShell.tsx`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminConsole.ts`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminConsoleActions.ts`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminConsoleState.ts`（modified）
  - `cloud-bridge/web/src/admin/model/pages.ts`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/BridgeAdminBackendTechnicalProposal.md`（modified）
  - `docs/progress.md`（updated）

### Phase 12: 实时状态提示改为徽章 Tooltip
- **Status:** complete
- Actions taken:
  - 移除顶栏实时状态旁的常显说明文本，避免和页面标题区争抢视觉焦点。
  - 将实时状态徽章改为 Tooltip 触发器，鼠标悬浮时再展示状态说明。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/AdminShell.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/BridgeAdminBackendTechnicalProposal.md`（modified）
  - `docs/progress.md`（updated）

### Phase 13: 隧道流量摘要头部信息分离
- **Status:** complete
- Actions taken:
  - 将 `Tunnel Pool 摘要` 右侧的视图来源与更新时间拆成两个独立信息块，避免视觉黏连。
  - 为流量页摘要头部补充独立布局样式，并在较窄宽度下允许自然换行。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/pages/TrafficPage.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 14: 侧边栏当前视图卡片移除
- **Status:** complete
- Actions taken:
  - 移除左上角“当前视图”卡片，让侧边栏从品牌区直接进入菜单分组。
  - 清理壳层派生状态里的 `sidebarContext` 以及对应无用样式，避免留下悬空结构。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/AdminShell.tsx`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminConsole.ts`（modified）
  - `cloud-bridge/web/src/admin/hooks/useAdminConsoleDerived.ts`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 15: YAML 预览自动换行与行号
- **Status:** complete
- Actions taken:
  - 将配置运维页的 YAML 示例与当前快照预览改成自动换行显示，避免长行触发横向滚动。
  - 为预览区域增加稳定行号列，帮助用户在自动换行后仍然明确定位内容。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/ops/LineNumberCodeBlock.tsx`（created）
  - `cloud-bridge/web/src/admin/components/pages/OpsPage.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 16: 配置运维页横向溢出收口
- **Status:** complete
- Actions taken:
  - 定位到整页横向滚动来自配置运维页嵌套 grid 子项默认 `min-width: auto`，导致 YAML 卡片与快照容器会把外层页面一起撑宽。
  - 为 `panel`、`snapshot-box`、`ops-yaml-grid`、`ops-yaml-card` 等关键容器补充 `min-width: 0` / `max-width: 100%` 约束，让长内容只在预期容器内换行或滚动，不再触发整页横向滚动条。
- Files created/modified:
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 17: 配置卡片头部换行收缩
- **Status:** complete
- Actions taken:
  - 进一步定位到横向滚动条由配置字段卡片头部触发：左侧标题说明与右侧来源 badge / 重启 badge / 恢复按钮在 section 双列布局下共同撑宽页面。
  - 为 `ui-card`、`config-section-*`、`config-field-*` 一整条容器链补充 `min-width: 0`，并让字段头部与 section 头部支持内部换行，确保右侧状态块只在卡片内折行，不再把整页撑出横向滚动条。
- Files created/modified:
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 18: Tooltip 隐藏态溢出修复
- **Status:** complete
- Actions taken:
  - 继续定位发现配置卡片里的说明 Tooltip 在未显示时仍以绝对定位节点存在，浏览器会把它计入页面可滚动宽度，导致整页仍出现横向滚动条。
  - 将 Tooltip 隐藏态改为默认 `display: none`，只在 hover / focus 时显示；同时将配置项说明 Tooltip 改为 `start` 对齐，减少显示态越过卡片边界的概率。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/ops/ConfigFieldRow.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 19: 配置 section 网格自动回落
- **Status:** complete
- Actions taken:
  - 结合用户反馈，进一步把运维配置 section 网格从固定双列改为按可读宽度自动回落的布局，避免某一张 section 卡片在宽度不足时继续横向挤压整页。
  - `config-section-grid` 现在要求单张卡片至少保留更宽的阅读宽度；主内容区不足以同时容纳两张卡片时会自动退回单列。
- Files created/modified:
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 20: Switch 隐藏输入宽度收口
- **Status:** complete
- Actions taken:
  - 根据用户排查结果，确认横向滚动条的直接来源是 `Switch` 组件中的隐藏 checkbox：`ui-switch-input` 仅做了透明处理，但没有收成真正不占布局宽度的视觉隐藏态。
  - 为 `ui-switch` 增加相对定位与固定轨道尺寸，并把 `ui-switch-input` 改成标准的无障碍隐藏写法（1px + clip / clip-path），确保隐藏输入不会再参与页面宽度计算。
- Files created/modified:
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

### Phase 21: 隧道流量摘要卡片自适应换列
- **Status:** complete
- Actions taken:
  - 定位到 DevTools 打开后的卡片挤压来自隧道流量页摘要区域仍在硬撑固定 6 列的紧凑 KPI 网格。
  - 为 traffic 页面单独引入 `traffic-kpi-grid`，改用 `auto-fit + minmax(210px, 1fr)` 的自适应列数，让摘要卡片按可用宽度自动换列，不再在中间宽度下挤成一团。
- Files created/modified:
  - `cloud-bridge/web/src/admin/components/pages/TrafficPage.tsx`（modified）
  - `cloud-bridge/web/src/index.css`（modified）
  - `docs/progress.md`（updated）

## Test Results
| Test | Input | Expected | Actual | Status |
|------|-------|----------|--------|--------|
| Admin API + App 关键包测试 | `cd cloud-bridge && go test ./runtime/bridge/adminapi ./runtime/bridge/app` | 通过 | 通过 | ✓ |
| Cloud Bridge 全量测试 | `cd cloud-bridge && go test ./...` | 通过 | 通过 | ✓ |
| Admin UI 构建测试 | `cd cloud-bridge/web && npm run build` | 通过 | 通过 | ✓ |
| Admin UI 类型检查 | `cd cloud-bridge/web && npx tsc --noEmit` | 通过 | 通过 | ✓ |
| BMA-16 目标用例 | `cd cloud-bridge && go test ./runtime/bridge/app -run TestBootstrapServesAdminUIAndAPIOnSingleServer -count=1` | 通过 | 通过 | ✓ |
| app 包全量测试 | `cd cloud-bridge && go test ./runtime/bridge/app -count=1` | 通过 | 通过 | ✓ |
| YAML 配置加载测试 | `cd cloud-bridge && go test ./runtime/bridge/app -run 'Test(ParseConfigYAMLAppliesDefaultsAndOverrides|ParseConfigYAMLRejectsUnknownFields|LoadConfigFromYAMLFile|LoadConfigFromYAMLFileRejectsEmptyPath)' -count=1` | 通过 | 通过 | ✓ |
| cloud-bridge 命令编译校验 | `cd cloud-bridge && go test ./cmd/cloud-bridge -count=1` | 通过 | 通过 | ✓ |
| Admin API SSE 测试 | `cd cloud-bridge && go test ./runtime/bridge/adminapi -count=1` | 通过 | 通过 | ✓ |
| BMA-16 SSE 目标用例 | `cd cloud-bridge && go test ./runtime/bridge/app -run TestBootstrapServesAdminSSEOnSingleServer -count=1` | 通过 | 通过 | ✓ |
| SSE 真实链路烟雾测试 | `go run ./cmd/cloud-bridge -config ./config.example.yaml` + `curl -N /api/admin/events/stream?...` | 收到 `bridge.ready/bridge.snapshot` | 通过 | ✓ |
| 安全收口回归测试 | `cd cloud-bridge && go test ./runtime/bridge/adminapi ./runtime/bridge/app -count=1` | 通过 | 通过 | ✓ |
| BMA-16 导出链路集成测试 | `cd cloud-bridge && go test ./runtime/bridge/app -run TestAdminDiagnoseExportDownloadLifecycleOnSingleServer -count=1` | 通过 | 通过 | ✓ |
| BMA-16 Cookie+CSRF 集成测试 | `cd cloud-bridge && go test ./runtime/bridge/app -run TestBootstrapCookieAuthWriteRequiresCSRFOnSingleServer -count=1` | 通过 | 通过 | ✓ |
| Admin 配置 YAML patch 回归 | `cd cloud-bridge && go test -timeout 60s ./runtime/bridge/adminapi -run TestConfigUpdateAcceptsYAMLBody -count=1` | 通过 | 通过 | ✓ |
| 用户 override 恢复继承与预览回归 | `cd cloud-bridge && go test -timeout 60s ./runtime/bridge/app -run TestAdminRuntimeConfigStoreUpdateNullPatchRemovesUserOverrideAndExposesEditablePatch -count=1` | 通过 | 通过 | ✓ |
| 分层配置加载与用户 override 回归 | `cd cloud-bridge && go test -timeout 60s ./runtime/bridge/adminapi ./runtime/bridge/app/... ./cmd/cloud-bridge/...` | 通过 | 通过 | ✓ |
| cloud-bridge 全量编译校验 | `cd cloud-bridge && go build ./...` | 通过 | 通过 | ✓ |
| 运维配置页类型检查 | `cd cloud-bridge/web && ./node_modules/.bin/tsc --noEmit -p tsconfig.json` | 通过 | 通过 | ✓ |
| 运维配置页构建校验 | `cd cloud-bridge/web && npm run build` | 通过 | 通过 | ✓ |

## Error Log
| Timestamp | Error | Attempt | Resolution |
|-----------|-------|---------|------------|
| 2026-03-14 | 暂无阻断错误 | 1 | - |

## 5-Question Reboot Check
| Question | Answer |
|----------|--------|
| Where am I? | Phase 9（BMA-15/BMA-16）已完成 |
| Where am I going? | 进入验收与发布准备阶段（文档冻结 + 构建发布） |
| What's the goal? | 形成“可安全上线 + 可实时联调 + 可单二进制交付”的已验收管理后台能力 |
| What have I learned? | 见 `findings.md` |
| What have I done? | 见本文件阶段记录 |
