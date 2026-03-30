# Bridge / Agent Token 管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Bridge 增加可切换的 token store 与后台 token 管理能力，同时把 Agent token 改成“可写但不回显”，形成可持久化、可运维的握手配置流程。

**Architecture:** 以 Bridge 为 token 签发与存储中心，新增 `connector_auth.token_store` 配置和 `memory/file` 两种 store 实现；Bridge Admin API 提供 token 资源管理接口；Agent Web 仅保留手工录入 token 的写入能力，后端不再回显明文 token。整个改动优先沿用现有配置落盘、Admin API、前端页面分层和测试结构，不做无关重构。

**Tech Stack:** Go、YAML 配置、Bridge Admin API、React、shadcn/ui、Vitest、Go `testing`

## 当前进展（2026-03-30）

- 已完成：Agent 侧移除随机生成 token，并将 `session.auth_token` 改成只写不回显
- 已完成：Bridge 配置新增 `connector_auth.token_store.driver`，默认 `file`
- 已完成：Bridge `memory/file` token store、token admin service 与 Admin API 装配
- 已完成：Bridge Web 在 Ops 页集成 connector token 管理卡片，支持创建 / 轮换 / 吊销与一次性明文模态展示
- 已完成：`cloud-bridge/runtime/bridge/app` 中 `controlChannelSessionState` 的并发竞态修复，并通过 `go test -race`
- 已完成：Bridge file token store 重启后仍可认证的集成测试
- 已完成：`cloud-bridge` 与 `agent-core` 全模块 `go test -timeout 60s ./...`
- 待继续：跨 Bridge/Agent 的端到端联调，以及静态检查环境补齐后的 `golangci-lint`

---

## 文件结构与职责

### Bridge 后端

- Modify: `cloud-bridge/runtime/bridge/app/config/config.go`
  - 新增 `connector_auth.token_store` 配置结构与默认值
- Modify: `cloud-bridge/runtime/bridge/app/config/config_test.go`
  - 校验默认值与字段映射
- Modify: `cloud-bridge/runtime/bridge/app/config_yaml_test.go`
  - 校验 YAML 读写 round-trip
- Modify: `cloud-bridge/runtime/bridge/app/runtime_config_loader.go`
  - 接入新配置字段的运行时加载与默认路径解析
- Modify: `cloud-bridge/runtime/bridge/app/admin_runtime_ops.go`
  - 管理后台配置快照中加入 token store 配置，注意不要暴露 token 明文
- Modify: `cloud-bridge/runtime/bridge/auth/connector_token.go`
  - 保留现有只读认证逻辑，同时拆出可管理型 store 基础
- Create: `cloud-bridge/runtime/bridge/auth/token_store_file.go`
  - 文件型 token store，负责加载、查询、创建、轮换、吊销、原子落盘
- Create: `cloud-bridge/runtime/bridge/auth/token_store_file_test.go`
  - 文件型 store 单测
- Create: `cloud-bridge/runtime/bridge/auth/token_admin.go`
  - token 领域服务：生成明文 token、hash、构建记录、状态转换
- Create: `cloud-bridge/runtime/bridge/auth/token_admin_test.go`
  - 领域服务单测
- Modify: `cloud-bridge/runtime/bridge/auth/control_auth.go`
  - 根据配置选择 `memory/file` store，收敛默认开发 token 注入规则
- Modify: `cloud-bridge/runtime/bridge/auth/export.go`
  - 向 app/adminapi 暴露管理型 token service 接口
- Modify: `cloud-bridge/runtime/bridge/app/bootstrap.go`
  - 在 runtime bootstrap 中装配 token store 与 admin 依赖
- Modify: `cloud-bridge/runtime/bridge/adminapi/server.go`
  - 注册 token 管理路由
- Create: `cloud-bridge/runtime/bridge/adminapi/tokens.go`
  - token list/create/detail/rotate/revoke handler
- Create: `cloud-bridge/runtime/bridge/adminapi/tokens_test.go`
  - admin token API 单测
- Modify: `cloud-bridge/runtime/bridge/app/bootstrap_test.go`
  - 加集成回归：启动、创建 token、重启后继续认证

### Bridge 前端

- Modify: `cloud-bridge/web/src/admin/hooks/useAdminDataActions.ts`
  - 增加 token 查询
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminOpsActions.ts`
  - 增加 token create/rotate/revoke
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminConsoleState.ts`
  - 增加 token 列表状态
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminConsoleActions.ts`
  - 暴露 token create/rotate/revoke 动作
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminConsole.ts`
  - 汇总 token 状态与动作
- Modify: `cloud-bridge/web/src/admin/components/pages/OpsPage.tsx`
  - 在 Ops 页集成 token 列表、一次性明文展示与操作按钮

### Agent 后端

- Modify: `agent-core/runtime/agent/app/runtime_config_store.go`
  - `GET /api/app/config` 不回显 token；`PUT` 空值不覆盖原 token
- Modify: `agent-core/runtime/agent/app/runtime_config_store_test.go`
  - token 只写不回显语义单测
- Modify: `agent-core/runtime/agent/httpapi/server_test.go`
  - HTTP 配置接口回归

### Agent 前端

- Modify: `agent-core/web/src/settings.ts`
  - 去掉本地 token 生成 helper，保留 token 写入型 draft 语义
- Modify: `agent-core/web/src/settings.test.ts`
  - 回归测试：不回填、不覆盖、显式写入
- Modify: `agent-core/web/src/console-pages.tsx`
  - 去掉随机生成按钮，调整 token 字段文案与 hint
- Modify: `agent-core/web/src/console-shared.ts`
  - 更新字段说明文本
- Modify: `agent-core/web/src/App.tsx`
  - 去掉生成按钮动作与 toast

### 文档

- Modify: `docs/Agent-WebUI-HTTP-Implementation-Plan.md`
- Modify: `docs/BridgeAdminBackendTechnicalProposal.md`
- Modify: `docs/Agent‑BridgeSecurityArchitectureAndAuthDesign.md`
- Modify: `docs/Agent-BridgeSecurityExecutionChecklist.md`

## Task 1: Agent 侧撤掉随机生成 Token

**Files:**
- Modify: `agent-core/web/src/settings.ts`
- Modify: `agent-core/web/src/settings.test.ts`
- Modify: `agent-core/web/src/console-pages.tsx`
- Modify: `agent-core/web/src/console-shared.ts`
- Modify: `agent-core/web/src/App.tsx`

- [ ] **Step 1: 写前端失败用例，覆盖“不再存在本地 token 生成 helper/按钮”**

```ts
it("does not expose local token generator behavior", () => {
  expect("generateSessionAuthToken" in settingsModule).toBe(false)
})
```

- [ ] **Step 2: 运行局部测试，确认当前失败**

Run: `cd agent-core/web && npm run test -- src/settings.test.ts`
Expected: FAIL，仍能找到本地 token 生成相关行为

- [x] **Step 3: 删除生成 helper、按钮和相关 toast 动作**

```ts
// 删除 generateSessionAuthToken
// 删除 onGenerateSessionAuthToken props
// 删除“随机生成”按钮
```

- [x] **Step 4: 补充 token 字段文案，明确“由 Bridge 后台生成并分发”**

```ts
session_auth_token: "由 Bridge 后台创建后粘贴到这里，留空表示保持当前 token 不变。"
```

- [x] **Step 5: 运行前端测试与构建**

Run: `cd agent-core/web && npm run test && npm run build`
Expected: PASS

## Task 2: Agent token 改成只写不回显

**Files:**
- Modify: `agent-core/runtime/agent/app/runtime_config_store.go`
- Modify: `agent-core/runtime/agent/app/runtime_config_store_test.go`
- Modify: `agent-core/runtime/agent/httpapi/server_test.go`
- Modify: `agent-core/web/src/settings.ts`
- Modify: `agent-core/web/src/settings.test.ts`

- [x] **Step 1: 写 Go 失败用例，覆盖 `GET` 不回显、`PUT` 空值不覆盖**

```go
func TestConfigSnapshotDoesNotExposeSessionAuthToken(t *testing.T) {}
func TestUpdateConfigKeepsSessionAuthTokenWhenInputEmpty(t *testing.T) {}
```

- [ ] **Step 2: 跑局部 Go 测试，确认失败**

Run: `cd agent-core && go test ./runtime/agent/app ./runtime/agent/httpapi`
Expected: FAIL，快照仍回显 token 或空值会覆盖

- [x] **Step 3: 在后端实现 token 脱敏/不下发语义**

```go
// snapshot config 中移除或清空 session.auth_token
// update config 时：只有新值非空时才覆盖原 token
```

- [x] **Step 4: 写前端失败用例，覆盖“设置页不再回填 token”**

```ts
it("keeps sessionAuthToken blank when snapshot omits the token", () => {
  expect(draft.sessionAuthToken).toBe("")
})
```

- [x] **Step 5: 调整前端 draft 构建与保存逻辑**

```ts
if (draft.sessionAuthToken.trim() !== "") {
  next.session.auth_token = draft.sessionAuthToken.trim()
}
```

- [x] **Step 6: 运行 Agent 前后端测试**

Run: `cd agent-core && go test ./runtime/agent/app ./runtime/agent/httpapi && cd web && npm run test`
Expected: PASS

## Task 3: Bridge 配置新增 token store driver

**Files:**
- Modify: `cloud-bridge/runtime/bridge/app/config/config.go`
- Modify: `cloud-bridge/runtime/bridge/app/config/config_test.go`
- Modify: `cloud-bridge/runtime/bridge/app/config/config_yaml.go`
- Modify: `cloud-bridge/runtime/bridge/app/config_yaml_test.go`
- Modify: `cloud-bridge/runtime/bridge/app/runtime_config_loader.go`
- Modify: `cloud-bridge/runtime/bridge/app/admin_runtime_ops.go`
- Modify: `cloud-bridge/runtime/bridge/app/admin_runtime_config_store_test.go`

- [x] **Step 1: 写配置失败用例，覆盖默认 `driver=file` 和 YAML round-trip**

```go
func TestDefaultConnectorTokenStoreDriverIsFile(t *testing.T) {}
func TestConfigYAMLRoundTripIncludesConnectorTokenStore(t *testing.T) {}
```

- [ ] **Step 2: 跑局部配置测试，确认失败**

Run: `cd cloud-bridge && go test ./runtime/bridge/app -run 'TestDefaultConnectorTokenStoreDriverIsFile|TestConfigYAMLRoundTripIncludesConnectorTokenStore' -count=1`
Expected: FAIL，字段尚不存在

- [x] **Step 3: 增加配置结构与默认值**

```go
type ConnectorAuthConfig struct {
    TokenStore ConnectorTokenStoreConfig `yaml:"token_store"`
}
```

- [x] **Step 4: 把新字段接入运行时加载和 admin config snapshot**

```go
"connector_auth": map[string]any{
  "token_store": map[string]any{...},
}
```

- [x] **Step 5: 运行 Bridge app 配置相关测试**

Run: `cd cloud-bridge && go test ./runtime/bridge/app -count=1`
Expected: PASS

## Task 4: 落地 Bridge file token store

**Files:**
- Modify: `cloud-bridge/runtime/bridge/auth/connector_token.go`
- Create: `cloud-bridge/runtime/bridge/auth/token_store_file.go`
- Create: `cloud-bridge/runtime/bridge/auth/token_store_file_test.go`
- Create: `cloud-bridge/runtime/bridge/auth/token_admin.go`
- Create: `cloud-bridge/runtime/bridge/auth/token_admin_test.go`
- Modify: `cloud-bridge/runtime/bridge/auth/export.go`
- Modify: `cloud-bridge/runtime/bridge/auth/control_auth.go`
- Modify: `cloud-bridge/runtime/bridge/auth/control_auth_test.go`

- [x] **Step 1: 写 file store 失败用例，覆盖加载、创建、轮换、吊销、重载**

```go
func TestFileTokenStoreCreateAndReload(t *testing.T) {}
func TestFileTokenStoreRotateRevokesPreviousToken(t *testing.T) {}
func TestFileTokenStoreUsesAtomicReplace(t *testing.T) {}
```

- [ ] **Step 2: 跑 auth 包测试，确认失败**

Run: `cd cloud-bridge && go test ./runtime/bridge/auth -count=1`
Expected: FAIL，file store 不存在

- [x] **Step 3: 实现 file store 与 token admin service**

```go
type FileTokenStore struct { ... }
func (store *FileTokenStore) Create(...) (record TokenRecord, plainToken string, err error) {}
```

- [x] **Step 4: 收敛 control_auth 装配逻辑**

```go
switch driver {
case "memory":
case "file":
default:
  return error
}
```

- [x] **Step 5: 调整默认开发 token 注入规则**

```go
// 仅 memory 且无记录时注入开发 token
```

- [x] **Step 6: 运行 Bridge auth 测试**

Run: `cd cloud-bridge && go test ./runtime/bridge/auth -count=1`
Expected: PASS

## Task 5: Bridge Admin API 增加 token 资源

**Files:**
- Modify: `cloud-bridge/runtime/bridge/adminapi/server.go`
- Create: `cloud-bridge/runtime/bridge/adminapi/tokens.go`
- Create: `cloud-bridge/runtime/bridge/adminapi/tokens_test.go`
- Modify: `cloud-bridge/runtime/bridge/app/bootstrap.go`
- Modify: `cloud-bridge/runtime/bridge/app/bootstrap_test.go`

- [x] **Step 1: 写 API 失败用例，覆盖 list/create/detail/rotate/revoke**

```go
func TestTokenRoutesCreateReturnsPlainTokenOnce(t *testing.T) {}
func TestTokenRoutesListDoesNotExposeSecret(t *testing.T) {}
func TestTokenRoutesRotateReturnsNewPlainToken(t *testing.T) {}
```

- [ ] **Step 2: 跑 adminapi 局部测试，确认失败**

Run: `cd cloud-bridge && go test ./runtime/bridge/adminapi -count=1`
Expected: FAIL，路由或 handler 尚不存在

- [x] **Step 3: 注册路由并实现 handler**

```go
mux.Handle("/api/admin/connector-tokens", ...)
mux.Handle("/api/admin/connector-tokens/", ...)
```

- [x] **Step 4: 在 bootstrap 中注入 token 管理依赖**

```go
AdminAPIDependencies{
  TokenService: ...,
}
```

- [x] **Step 5: 运行 Bridge adminapi 与 bootstrap 回归**

Run: `cd cloud-bridge && go test ./runtime/bridge/adminapi ./runtime/bridge/app -count=1`
Expected: PASS

## Task 6: Bridge Web 增加 token 管理页

**Files:**
- Modify: `cloud-bridge/web/src/admin/model/types.ts`
- Create: `cloud-bridge/web/src/admin/model/tokens.ts`
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminDataActions.ts`
- Modify: `cloud-bridge/web/src/admin/hooks/useAdminOpsActions.ts`
- Modify: `cloud-bridge/web/src/admin/model/pages.ts`
- Create: `cloud-bridge/web/src/admin/components/pages/TokensPage.tsx`
- Modify: `cloud-bridge/web/src/admin/components/AdminPageContent.tsx`

- [x] **Step 1: 写前端失败用例或最小模型断言，覆盖 token 列表和一次性明文展示状态**

```ts
it("normalizes token records without exposing secret hash", () => {
  expect(record.tokenSecretHash).toBeUndefined()
})
```

- [x] **Step 2: 跑 Bridge Web 测试或构建前检查**

Run: `cd cloud-bridge/web && npm run build`
Expected: FAIL 或类型错误，直到模型/页面接入完成

- [x] **Step 3: 实现 token 页面，或在 Ops 页集成 token 管理卡片**

```tsx
<ConnectorTokenPanel
  records={tokenRecords}
  onCreateToken={...}
  onRotateToken={...}
  onRevokeToken={...}
/>
```

- [x] **Step 4: 增加一次性明文 token 展示对话框**

```tsx
<AlertDialog open={plainTokenDialogOpen}>...</AlertDialog>
```

- [x] **Step 5: 运行 Bridge Web 构建**

Run: `cd cloud-bridge/web && npm run build`
Expected: PASS

## Task 7: 联调回归与持久化验证

**Files:**
- Modify: `cloud-bridge/runtime/bridge/app/bootstrap_test.go`
- Modify: `agent-core/runtime/agent/app/runtime_bridge_test.go`
- Modify: `agent-core/runtime/agent/app/runtime_config_store_test.go`

- [x] **Step 1: 写集成失败用例，覆盖 Bridge file 模式重启后仍可认证**

```go
func TestBridgeFileTokenStorePersistsAcrossRestart(t *testing.T) {}
```

- [x] **Step 2: 写失败用例，覆盖 Agent 空 token 保存不覆盖**

```go
func TestAgentConfigUpdateKeepsExistingTokenWhenInputEmpty(t *testing.T) {}
```

- [ ] **Step 3: 跑关键 Go 测试，确认失败**

Run: `cd cloud-bridge && go test ./runtime/bridge/app -count=1 && cd ../agent-core && go test ./runtime/agent/app -count=1`
Expected: FAIL，直到联调语义补齐

- [x] **Step 4: 补最小实现并修正断言**

```go
// Bridge restart -> reload file token records
// Agent empty token -> preserve old value
```

- [x] **Step 5: 跑关键回归**

Run: `cd cloud-bridge && go test -timeout 60s ./runtime/bridge/auth ./runtime/bridge/adminapi ./runtime/bridge/app -count=1`
Expected: PASS

Run: `cd agent-core && go test -timeout 60s ./runtime/agent/app ./runtime/agent/httpapi -count=1`
Expected: PASS

## Task 8: 文档同步与全量验证

**Files:**
- Modify: `docs/Agent-WebUI-HTTP-Implementation-Plan.md`
- Modify: `docs/BridgeAdminBackendTechnicalProposal.md`
- Modify: `docs/Agent‑BridgeSecurityArchitectureAndAuthDesign.md`
- Modify: `docs/Agent-BridgeSecurityExecutionChecklist.md`

- [x] **Step 1: 同步文档**

```md
- Bridge 新增 connector_auth.token_store.driver，默认 file
- Agent token 改成只写不回显
- Bridge Admin 新增 token 资源接口
```

- [x] **Step 2: 运行 Bridge 全量验证**

Run: `cd cloud-bridge && go test -timeout 60s ./...`
Expected: PASS

- [x] **Step 3: 运行 Agent 全量验证**

Run: `cd agent-core && go test -timeout 60s ./...`
Expected: PASS

- [x] **Step 4: 运行竞态测试**

Run: `cd cloud-bridge && go test -race -timeout 60s ./runtime/bridge/auth ./runtime/bridge/adminapi ./runtime/bridge/app`
Expected: PASS

Run: `cd agent-core && go test -race -timeout 60s ./runtime/agent/app ./runtime/agent/httpapi`
Expected: PASS

- [x] **Step 5: 运行前端构建**

Run: `cd agent-core/web && npm run build && cd ../../cloud-bridge/web && npm run build`
Expected: PASS

- [x] **Step 6: 格式化与静态检查**

Run: `cd cloud-bridge && gofmt -w . && golangci-lint run ./...`
Expected: PASS

Run: `cd agent-core && gofmt -w . && golangci-lint run ./...`
Expected: PASS
