# Bridge / Agent Token 管理设计

## 1. 背景

当前 Agent 与 Bridge 的控制面握手使用 connector token：

- Agent 侧通过 `session.auth_token` 保存并发送明文 token
- Bridge 侧在认证时解析 `dbt_<token_id>.<token_secret>`，按 `token_id` 查找记录并校验 `token_secret_hash`

现状存在三个问题：

1. Bridge 默认仍回退到内存 token store，重启后 token 丢失，不适合正常运维
2. Agent Web 后台允许本地随机生成 token，但 Bridge 若未同步登记对应记录，握手一定失败
3. 当前缺少 Bridge 管理后台的 token 创建、轮换、吊销与持久化能力

因此需要把 token 管理收敛为一套正常可运维的模型：Bridge 负责生成和持久化 token 记录，Agent 负责录入并保存明文 token。

## 2. 目标

### 2.1 本次要解决的问题

- Bridge 支持可切换的 connector token 存储方式
- 默认使用文件型 token store，重启后仍能完成认证
- Bridge 管理后台支持创建、轮换、吊销与查看 token 元数据
- Agent 后台保留 token 编辑能力，但不再本地随机生成 token
- Agent 后台不回显已有 token 明文，避免浏览器侧泄漏

### 2.2 非目标

- 本次不实现 `sqlite` token store，只预留 driver 扩展位
- 本次不实现多租户级 token 审批流或复杂 RBAC
- 本次不实现明文 token 的二次查看能力

### 2.3 当前落地状态（2026-03-30）

截至当前实现，设计已收敛为：

- Bridge 新增 `connector_auth.token_store.driver`，默认 `file`
- `driver=file` 时使用独立 YAML token 文件并原子写回；`driver=memory` 仅保留给开发联调
- Bridge Admin 已提供 `connector-tokens` 资源接口，并在 Ops 页承载 token 管理卡片
- Bridge SSE 的 `ops` 聚合快照会附带 `connector_tokens` 元数据数组
- Agent 侧已去掉本地随机生成 token 的能力，`session.auth_token` 改成只写不回显
- Bridge 首期轮换语义采用“立即替换旧 token”，`grace` 双 token 过渡仍作为后续增强项

## 3. 设计原则

### 3.1 职责边界

- Bridge 是 token 的签发方、存储方和认证校验方
- Agent 是 token 的使用方和本地配置持有方
- token 不应被视为普通运行参数，不和 Bridge 主配置 patch 混为同一资源

### 3.2 安全原则

- Bridge 永远不保存明文 `token_secret`
- 明文 token 仅在创建/轮换当次返回一次
- 日志、快照、导出、SSE 中禁止输出明文 token
- Agent 后台不回显已有 token 明文

### 3.3 扩展原则

- token store 通过 `driver` 配置切换
- 首期支持 `file` 与 `memory`
- 结构上预留 `sqlite` 与其他持久化后端

## 4. 配置设计

Bridge 配置新增一组 token store 配置，建议放入 `connector_auth` 域：

```yaml
connector_auth:
  token_store:
    driver: file
    file:
      path: ./bridge.tokens.yaml
```

### 4.1 字段定义

- `connector_auth.token_store.driver`
  - 可选值：`file | memory`
  - 默认值：`file`
- `connector_auth.token_store.file.path`
  - 当 `driver=file` 时必填
  - 表示 token 元数据文件路径
  - 若为相对路径，应按“声明该字段的配置文件所在目录”解析；若字段沿用默认值，则按当前加载的基础配置文件所在目录解析

### 4.2 兼容性

- 未显式配置时，Bridge 默认按 `file` 模式初始化
- 若 `driver=memory`，保持当前开发行为：token 只存在内存，重启即丢失
- `sqlite` 等后续驱动只新增配置分支，不改变当前字段语义

## 5. 数据模型

Bridge token 记录沿用现有认证领域模型：

- `connector_id`
- `token_id`
- `token_secret_hash`
- `hash_algorithm`
- `hash_version`
- `status`
- `issued_at`
- `expires_at`
- `rotated_at`
- `metadata`

文件型存储只保存以上元数据，不保存明文 token。

### 5.1 明文 token 结构

明文 token 格式保持不变：

```text
dbt_<token_id>.<token_secret>
```

- `token_id` 用于公开索引
- `token_secret` 仅在创建/轮换时生成并回传

## 6. Bridge 运行时设计

### 6.1 token store 抽象

在现有 `connectorTokenStore` 查询接口基础上，扩展出可管理型 token store 能力：

- 查询：`LookupByTokenID`
- 列表：`List`
- 创建：`Create`
- 轮换：`Rotate`
- 吊销：`Revoke`

为兼容现有认证路径，可以拆为：

1. 认证只依赖的只读接口
2. 管理后台依赖的可写接口

### 6.2 file store

文件型 token store 负责：

- 启动时从 YAML 文件加载 token 记录
- 写操作后原子落盘
- 并发读写保护
- 记录归一化与脏数据跳过

落盘策略与当前 Bridge 配置保存保持一致：

- 自动创建父目录
- 写入临时文件
- 继承合理权限
- 原子替换目标文件

### 6.3 memory store

内存 store 保留：

- 便于本地快速联调
- 无文件依赖
- 重启后状态丢失

默认开发 token 的注入逻辑需要重新收敛：

- 建议仅在 `driver=memory` 且未配置任何记录时注入开发 token
- `driver=file` 不应自动注入开发 token，避免生产误用

## 7. Bridge 管理后台 API 设计

新增一组独立 token 资源接口，不走 `/api/admin/config` 主配置更新链路。

### 7.1 列表

`GET /api/admin/connector-tokens`

返回：

- `connector_id`
- `token_id`
- `status`
- `issued_at_ms`
- `expires_at_ms`
- `rotated_at_ms`
- `metadata`

不返回：

- `token_secret`
- `token_secret_hash`

### 7.2 创建

`POST /api/admin/connector-tokens`

请求体示例：

```json
{
  "connector_id": "agent-local",
  "expires_at_ms": 0,
  "metadata": {
    "note": "local agent bootstrap"
  }
}
```

返回体示例：

```json
{
  "record": {
    "connector_id": "agent-local",
    "token_id": "agent-local-a1b2c3",
    "status": "active"
  },
  "plain_token": "dbt_agent-local-a1b2c3.xxxxx"
}
```

说明：

- `plain_token` 仅本次响应返回一次
- 响应后前端应引导复制，不再提供二次查看

### 7.3 轮换

`POST /api/admin/connector-tokens/:token_id/rotate`

返回新的 `plain_token`，旧 token 立即失效或按现有 `grace` 机制进入过渡态，首期建议直接失效，减少复杂度。

### 7.4 吊销

`POST /api/admin/connector-tokens/:token_id/revoke`

返回更新后的 token 元数据。

### 7.5 详情

`GET /api/admin/connector-tokens/:token_id`

返回元数据详情，不返回明文 secret。

## 8. Agent 后台设计

### 8.1 设置页行为

- 去掉“随机生成 token”按钮
- 保留 `session.auth_token` 的手工编辑入口
- 该字段改为“写入型字段”：
  - 页面加载时默认空白
  - 不从后端回显现有 token
  - 用户留空表示“不修改”
  - 用户输入新值才写回配置

### 8.2 Agent 后端接口语义

`GET /api/app/config`

- 不再向前端返回 `session.auth_token` 明文
- 可返回脱敏占位或根本不返回该字段

`PUT /api/app/config`

- 若请求中 token 字段为空字符串或缺失，则保持原值不变
- 若请求中提供新的非空 token，则覆盖保存

### 8.3 交互流程

1. 运维在 Bridge 后台创建 token
2. Bridge 返回一次性明文 token
3. 运维在 Agent 后台录入 token
4. Agent 保存到本地 YAML
5. Agent 重启或重连后使用新 token 握手

## 9. UI 设计

### 9.1 Bridge Web

新增 token 管理页或在 connector 详情页挂接 token 管理面板，至少包含：

- token 列表
- 创建 token 按钮
- 轮换按钮
- 吊销按钮
- 一次性明文 token 展示对话框

### 9.2 Agent Web

设置页中 token 输入框保留，但文案调整为：

- “由 Bridge 后台生成并分发”
- “留空表示保持当前 token 不变”

避免误导用户认为 Agent 可以自行生成合法 token。

## 10. 错误处理

### 10.1 Bridge

- `driver=file` 且文件不可读：启动失败，错误明确指向 token 文件
- 写入 token 文件失败：管理 API 返回失败，不更新内存索引
- token_id 冲突：创建接口返回参数冲突错误

### 10.2 Agent

- 保存空 token：不覆盖旧值
- 保存新 token 后若握手失败：继续沿用现有认证错误与限流日志
- 前端不应因为 token 不回显而误判为“配置丢失”

## 11. 测试设计

### 11.1 Bridge 单测

- `file` store 加载/保存/原子替换
- `memory` store 行为回归
- 创建/轮换/吊销 token API
- 重启后 `file` 模式 token 可继续认证
- 明文 token 不进入快照、列表、审计日志

### 11.2 Agent 单测

- `GET /api/app/config` 不回显 token
- `PUT /api/app/config` 留空不覆盖
- `PUT /api/app/config` 非空时覆盖保存
- 设置页加载后 token 输入框默认空白

### 11.3 联调验证

- Bridge 创建 token → Agent 录入 → 连接成功
- Bridge 轮换 token → Agent 未更新前连接失败 → 更新后恢复
- Bridge 重启后 `file` 模式连接仍成功

## 12. 实施顺序

1. 撤掉 Agent 侧随机生成 token
2. Agent token 字段改成只写不回显
3. Bridge 增加 `connector_auth.token_store` 配置
4. 落地 `memory/file` token store
5. Bridge 管理 API 接入 token 资源
6. Bridge Web 增加 token 管理 UI
7. 补文档与回归测试

## 13. 风险与取舍

### 13.1 为什么不把 token 混进 Bridge 主配置

- token 属于凭证资源，不是普通运行配置
- 若走 `/api/admin/config` patch 语义，快照、导出、回显与并发控制都会更复杂
- 独立资源更适合后续扩展到 `sqlite`

### 13.2 为什么 Agent 不回显 token

- 只隐藏 UI 不足以防止浏览器网络面板泄漏
- 后端不下发明文，才能真正降低暴露面

### 13.3 为什么默认 `file`

- 更符合“正常使用”预期
- 避免 Bridge 重启后 token 全失效
- `memory` 仍保留给开发联调

## 14. 预期结果

完成后，系统会形成以下稳定流程：

```text
Bridge 后台创建 token
   ↓
Bridge file store 持久化 token 记录（仅 hash）
   ↓
一次性向运维展示明文 token
   ↓
运维写入 Agent 配置
   ↓
Agent 使用 token 与 Bridge 握手
   ↓
Bridge 重启后仍可继续校验
```

这套模型既满足当前文件持久化诉求，也为后续 `sqlite` 等 driver 留出了平滑扩展空间。
