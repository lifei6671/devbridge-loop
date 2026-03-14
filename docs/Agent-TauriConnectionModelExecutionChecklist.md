# Agent-Tauri 连接模型可执行清单（7.3 + 8）

## 1. 目标与范围

本清单对齐《[Agent 与 Tauri 本地通信技术方案](./Agent–TauriLocalCommunicationDesignProposal.md)》以下章节，并拆成可执行任务：

- 7.3 连接模型：单长期连接 + 多路复用 `request/response/event`
- 8.1 边界约束：禁止 UI / Host 侵入 Agent 运行时内部
- 8.2 可视数据范围：仅快照、聚合事件、诊断、指标、结构化日志
- 8.3 Rust 角色边界：只做宿主层，不成为第二个 Runtime Core

本清单只覆盖连接模型与边界落实，不替代 A15 的 IPC 鉴权、Windows Named Pipe、跨平台权限矩阵。

---

## 2. 可执行清单

### C1. 单长期连接骨架

- [x] Host 建立并保持单条本地 IPC 长连接
- [x] 启动链路采用 `reconnecting -> resyncing -> connected`
- [x] 连接建立后执行 `app.bootstrap` 与 `agent.snapshot` 对账

落点：

- `apps/dev-agent/src-tauri/src/agent_host/supervisor.rs`
- `apps/dev-agent/src-tauri/src/agent_host/ipc_client.rs`

验收：

- `cargo check` 通过
- 启动后 `host_logs_snapshot` 可见 `RPC_CONNECTED`

### C2. 帧头 `RequestId` 关联规则

- [x] request/response 使用非零 `RequestId` 关联
- [x] event/ping/pong 强制全零 `RequestId`
- [x] JSON body 禁止出现 `request_id` 字段

落点：

- `apps/dev-agent/src-tauri/src/agent_host/frame.rs`
- `apps/dev-agent/src-tauri/src/agent_host/codec.rs`

验收：

- `cargo test --bin dev-agent local_rpc_tests -- --nocapture` 通过

### C3. Agent 事件转发到前端（event bridge）

- [x] `ipc_client` 缓存服务端 event
- [x] `supervisor` 周期事件泵（ping + drain）
- [x] 事件通过 `agent-runtime-changed` 统一转发给前端

落点：

- `apps/dev-agent/src-tauri/src/agent_host/ipc_client.rs`
- `apps/dev-agent/src-tauri/src/agent_host/supervisor.rs`
- `apps/dev-agent/src-tauri/src/agent_host/event_bridge.rs`

验收：

- 运行时可在宿主日志看到 `AGENT_EVENT_FORWARDED`
- 前端 `eventFeed` 收到 `reason=agent.event.*`

### C4. 帧安全约束（协议防御）

- [x] `BodyLen <= 1MiB`
- [x] `ping/pong` 必须空 body
- [x] 先验头后分配 body，非法帧拒绝

落点：

- `apps/dev-agent/src-tauri/src/agent_host/codec.rs`

验收：

- `local_rpc_tests` 中 5 个用例通过

### C5. 边界防越权（禁止接口）

- [x] 方法域白名单：`app/agent/session/service/tunnel/traffic/config/diagnose`
- [x] 拒绝低层越界方法：`traffic.open/reset`、`tunnel.read/write`、`relay.inject`、`runtime.takeover`

落点：

- `apps/dev-agent/src-tauri/src/agent_host/router.rs`

验收：

- 非法 method 调用返回错误，不进入后续帧发送

### C6. UI 数据边界

- [x] 前端仅消费快照、聚合事件、结构化日志、宿主指标
- [x] 不暴露 Tunnel/Traffic 原始读写接口
- [x] 白色主题控制台改版：落地总览/运行控制/配置管理/服务列表/通道列表/诊断中心，全部映射现有 command 能力
- [x] 配置管理直连真实 Go runtime：`agent_id/bridge_addr/tunnel_pool_*` 可编辑并透传 `agent-core`
- [x] IPC transport 改为平台绑定只读展示（Windows=`named_pipe`，Unix=`uds`）
- [x] 右侧主内容区移除重复“当前视图”标题，仅保留卡片内容

落点：

- `apps/dev-agent/src/App.tsx`
- `apps/dev-agent/src-tauri/src/commands/*.rs`

验收：

- `npm run build` 通过
- 前端展示数据均为 snapshot/event/logs 视图模型

### C7. 宿主层职责收敛（非 Runtime Core）

- [x] `main.rs` 收敛为组装入口，不承载业务运行时状态机
- [x] 宿主逻辑按 `commands/agent_host/state` 分层

落点：

- `apps/dev-agent/src-tauri/src/main.rs`
- `apps/dev-agent/src-tauri/src/commands/`
- `apps/dev-agent/src-tauri/src/agent_host/`
- `apps/dev-agent/src-tauri/src/state/`

验收：

- 模块职责与方案 10.1 目录建议一致

### C8. 当前遗留（下一阶段）

- [x] Windows Named Pipe 实装（当前 Linux UDS 为主）
- [x] `session_secret` challenge-response（HMAC-SHA256）
- [x] OS 对端身份校验（Linux `SO_PEERCRED` / Windows 对端令牌 SID + PID）
- [x] 跨平台权限测试矩阵（Linux/Windows）

说明：

- C8 项已全部完成；A15 总清单以 `Agent-and-Bridge-ExecutionChecklist.md` 为最终勾选口径。

---

## 3. 执行记录（2026-03-13 / 2026-03-14）

- 已完成 C1~C7 的代码落地与编译验证
- 已完成 C8-1（Windows Named Pipe 实装）：`ipc_client` 已支持 `uds/named_pipe` 双分支，mock runtime 新增 Named Pipe 服务端分支
- 已完成 C8-2（`session_secret` challenge-response）：`ipc_client.connect` 接入 `app.auth.begin/app.auth.complete` 双向 HMAC 校验，mock runtime 在鉴权成功前拒绝业务方法
- 已完成 C8-3（OS 对端身份校验）：Linux 使用 `SO_PEERCRED(uid/pid)` 校验，Windows 使用 `GetNamedPipeServerProcessId + Token SID` 校验
- 已完成 C8-4（跨平台权限测试矩阵）：新增 `.github/workflows/dev-agent-a15-matrix.yml`，Linux/Windows 双平台执行 `cargo check + cargo test + npm run build`，Linux 增补 `x86_64-pc-windows-gnu` 交叉检查
- 已完成 C6 白色版 UI 重设计：`App.tsx/index.css` 重构为“左侧导航 + 顶部状态栏 + 白色卡片仪表盘”，并保持仅展示 snapshot/event/logs/metrics 视图模型
- 已校正配置管理能力：`host_config_update` 与前端配置页仅保留真实生效字段（`runtime_program/runtime_args/ipc_transport/ipc_endpoint`），移除 mock/虚构 Bridge 三项配置（重启 Agent 生效）
- 已完成控制台视觉重构：采用 shadcn 风格 `Card/Button/Input/Badge` 组件与浅灰桌面壳布局，保持左侧导航/顶部搜索/指标卡/表格分区，不新增任何未实现命令
- 已完成 Win11 启动稳态修复：单实例锁改为 OS 文件锁（`fs2::FileExt::try_lock_exclusive`），并新增启动失败日志 `startup.log` 与 Windows 错误弹窗，避免“闪退无提示”
- 已补齐端点安全策略：UDS 目录权限与 owner 校验、反 symlink、防共享目录回落；Windows Named Pipe 采用 SID 作用域命名与 DACL（current user + LocalSystem）并拒绝远程客户端
- 已完成配置链路增强：前端配置页新增 `agent_id/bridge_addr/tunnel_pool_*`，`host_config_update` 与 `launcher` 同步透传到 `DEV_AGENT_CFG_*`，`agent-core` 启动入口读取环境变量并通过 `BootstrapWithOptions` 应用
- 已完成 UI 细节收敛：右侧主内容区移除“当前视图”大标题；IPC 传输方式固定为平台绑定只读项，不再允许自定义
- 已新增模块化目录并完成实现迁移，不是空文件占位
- 已执行验证：
  - `cd apps/dev-agent/src-tauri && cargo check`
  - `cd apps/dev-agent/src-tauri && cargo check --target x86_64-pc-windows-gnu`
  - `cd apps/dev-agent/src-tauri && cargo test --bin dev-agent -- --nocapture`
  - `make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-gnu`
  - `cd apps/dev-agent && npm run build`
