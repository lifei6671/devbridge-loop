# Agent 与 Bridge 实现执行清单（一期）

## 一、文档目的

本清单用于承接 [Agent‑and‑Bridge‑Implementation‑Technical‑Design.md](./Agent‑and‑Bridge‑Implementation‑Technical‑Design.md)，将技术方案拆解为可执行、可联调、可验收的工程任务。

说明：

- 本清单以 Agent/Bridge 运行时实现为主，不替代传输层专项清单 [LTFP-TransportExecutionChecklist.md](./LTFP-TransportExecutionChecklist.md)。
- 本清单默认全部未勾选；如已有实现，可按“验收标准”回填勾选状态。
- 当前 `UI -> Agent -> Bridge` 链路未实现项可参考 [UI-Agent-Bridge-Unimplemented-Checklist.md](./UI-Agent-Bridge-Unimplemented-Checklist.md)。

---

## 二、本期范围

### 1. 一期必须交付

- Agent：session 管理、tunnel pool、traffic 接入与转发、健康上报
- Bridge：ingress、route resolve、session/tunnel registry、connector/direct/hybrid 三路径执行
- 控制面：心跳、鉴权、资源消息一致性字段、HOL 治理
- 数据面：`TrafficOpen/OpenAck/Data/Close/Reset`、单 reader 约束、反压与内存边界
- 稳定性：超时取消、迟到 Ack 丢弃、错误语义、观测性、测试矩阵
- UI：Agent 桌面 UI 采用 Tauri；Bridge 管理页面采用 React + shadcn/ui，并在打包时内嵌到 Bridge

### 2. 本期不做

- 多 Bridge 高可用与跨集群一致性
- session resume
- tunnel 多次复用
- mid-stream failover
- datagram

---

## 三、已冻结前提

- [ ] `single tunnel single traffic` 为硬约束
- [ ] `connector_service` / `external_service` / `hybrid_group` 三路径分治
- [ ] fallback 只允许 pre-open 阶段；收到 `TrafficOpenAck success=true` 后禁止 fallback
- [ ] 数据面保持 framed all the way，禁止切 raw stream
- [ ] idle tunnel 阶段 reader 归 `trafficAcceptor`，active 阶段 reader 归 `TrafficRuntime`
- [ ] `TrafficOpenAck` 超时后必须进入取消流程，迟到 Ack 必须丢弃
- [ ] Agent 桌面层固定 Tauri；Bridge 管理后台固定 React + shadcn/ui；发布包必须内嵌 UI 资源

---

## 四、执行清单

### A0. 真相源与目录冻结

- [ ] 明确方案真相源：技术方案文档 + transport 抽象文档
- [ ] 冻结 Agent/Bridge 运行时目录与模块命名，避免后续漂移
- [ ] 建立“方案章节 -> 代码模块 -> 测试用例”映射表

验收标准：

- 术语、目录、模块命名在评审后不再反复变更
- 每个核心章节都有对应代码落点和测试落点

### A1. 控制面骨架（Session/Auth/Heartbeat）

- [x] Agent `SessionManager`：建连、鉴权、心跳、重连、epoch 防污染
- [x] Bridge Session 生命周期视图：`ACTIVE / DRAINING / STALE / CLOSED`
- [x] 心跳超时判定与断链收敛行为
- [x] 控制面高优先级消息调度框架（heartbeat/auth/refill/control error）

验收标准：

- session 状态切换与 heartbeat 规则可复现、可观测
- 大消息下不会把 heartbeat 饿死

### A2. 控制面资源一致性与幂等

- [x] 资源级消息统一携带：`session_id/session_epoch/event_id/resource_version`
- [x] ACK 去重与版本比较规则实现
- [x] 重放与乱序场景下的幂等处理
- [x] full-sync/重连后的状态对账流程

验收标准：

- 重放同一事件不产生重复副作用
- 旧 epoch 消息不会污染新 session

### A3. Agent TunnelManager 与池治理

- [x] 实现 `minIdle/maxIdle/ttl/maxInflight/rateLimit/burst` 约束
- [x] 实现 idle tunnel 预建、消费后补建、broken 摘除
- [x] 实现 `TunnelPoolReport` 事件驱动上报 + 周期纠偏
- [x] 实现 `TunnelRefillRequest` 合并处理与平滑建连

验收标准：

- 高并发下 pool 不惊群、不雪崩
- idle 回收路径满足 `idle -> closing -> closed`

### A4. Agent traffic 接入与执行

- [x] `trafficAcceptor` 独占 idle tunnel 的 `ReadFrame`
- [x] `TrafficRuntime` 处理 `TrafficOpen`、本地 dial、ack、relay、close/reset
- [x] `endpoint_selection_hint` 软引导 + 本地重选
- [x] `TrafficOpenAck.metadata` 回传 `actual_endpoint_id/actual_endpoint_addr`
- [x] 收到取消信号时中止 dial/relay 并清理 upstream

验收标准：

- 任意时刻同一 tunnel 只有一个 reader
- Bridge 可拿到最终实际 endpoint 作为观测真相

### A5. Bridge Ingress 与 RouteResolver

- [x] 落地三类入口：`l7_shared`、`tls_sni_shared`、`l4_dedicated_port`
- [x] RouteResolver 目标分类：`connector_service/external_service/hybrid_group`
- [x] 过滤规则：scope 不匹配、service 不健康、connector 离线、session 非 active
- [x] 明确 `https` 与 `tlsSni` 共享 listener 的实现约束

验收标准：

- 三类入口互不串扰，路由结果可解释
- 端口与监听行为无冲突

### A6. Bridge ConnectorProxy 主链路

- [x] `AcquireIdleTunnel` 分配与状态流转：`idle -> reserved -> active`
- [x] 发送 `TrafficOpen`，等待 `TrafficOpenAck` 成功后进入 relay
- [x] 正常关闭后清理 tunnel registry，本地状态回收
- [x] no-idle 场景：短等待 + refill + 超时失败（503）

验收标准：

- connector path 可稳定打通并闭环
- no-idle 时行为确定且可观测

### A7. 超时取消与迟到 Ack 规则

- [x] 实现 `open_sent` 超时后的取消流程（reset 或直接关闭）
- [x] tunnel 标记 `broken` 并从 registry 摘除
- [x] 迟到 `TrafficOpenAck` 一律丢弃，不得恢复终态
- [x] 增加 `bridge_traffic_open_ack_late_total` 指标

验收标准：

- 超时后不会出现状态回滚或幽灵恢复
- late-ack 有明确指标和日志证据

### A8. DirectProxy 与 Hybrid Fallback

- [x] `external_service` 路径：discovery、endpoint cache、direct dial、relay
- [x] `hybrid_group` 仅允许 `pre_open_only` fallback
- [x] pre-open 失败分支区分：已分配 tunnel vs 未分配 tunnel
- [x] post-open 失败禁止 fallback

验收标准：

- hybrid 仅在规定窗口 fallback
- connector/direct 路径边界清晰，错误语义不混淆

### A9. 数据面协议、并发与反压

- [x] 落地统一帧：`Open/OpenAck/Data/Close/Reset`
- [x] 落地并发约束：单读单写，多写串行，多读禁止
- [x] relay pump 使用有界缓冲，禁止无界队列
- [x] 下游阻塞时暂停继续 `ReadFrame`，依赖底层窗口流控回压
- [x] 回压超阈值（deadline/timeout）按错误语义 close/reset

验收标准：

- 慢客户端场景不会导致 Bridge/Agent 内存无限增长
- 反压行为在 `grpc_h2/quic/h3` 绑定下保持一致原则

### A10. 观测性与诊断

- [x] Agent 指标：session、pool、open_ack 延迟、upstream dial 延迟
- [x] Bridge 指标：acquire 等待、open timeout/reject/late ack、hybrid fallback、actual endpoint override
- [x] 统一日志字段：`trace_id/traffic_id/route_id/service_id/actual_endpoint_*/session_id/session_epoch/connector_id/tunnel_id`
- [x] 关键错误路径补齐结构化日志模板

验收标准：

- 任意失败请求可在单次排查中追溯完整链路
- 指标可直接支持 SLO 与容量调参

### A11. 配置与初始化覆盖

- [x] 保持当前默认值：`minIdle=8/maxIdle=32/rateLimit=10/burst=20/...`
- [x] Agent 初始化支持外部传入 `tunnelPool` 参数覆盖默认值
- [x] 未传字段回落默认值
- [x] 首版不做运行时热更新（重启生效）
- [x] `bridge_transport` 支持按 Agent 配置切换 `tcp_framed/grpc_h2`（控制通路与 tunnel 通路均走同一 binding）
- [x] Bridge 控制面新增 `grpc_h2` 监听地址配置，与 `tcp_framed` 监听地址分离

验收标准：

- 默认配置不变
- 启动参数可覆盖并稳定生效

### A12. 错误语义与终止行为

- [x] connector path 错误分类：route miss/service unavailable/no idle/open reject/open timeout/dial 失败/relay reset
- [x] external path 错误分类：discovery miss/provider down/refresh 失败/direct dial 失败/relay 失败
- [x] idle tunnel 回收触发：TTL、超上限、session draining/stale
- [x] close/reset 的状态与日志口径一致

验收标准：

- 错误码、HTTP 返回、日志字段三者一致
- 终止语义在双端对齐

### A13. 测试矩阵与发布门槛

- [x] 单元测试：状态机、并发边界、timeout/cancel、ack 去重、参数回落
- [x] 集成测试：握手、心跳、pool、open/ack/data/close/reset、late ack、hybrid pre-open fallback
- [x] 压测：突发 no-idle、refill 节流、慢读回压、大包 relay
- [x] 与 [ltfp/docs/TestMatrix.md](../ltfp/docs/TestMatrix.md) 对齐补齐 case 编号与结果
- [x] 发布门槛：单测/集测/关键压测全部通过

执行记录（2026-03-13 / 2026-03-14）：

- `cd agent-core && go test ./runtime/agent/control ./runtime/agent/session ./runtime/agent/tunnel ./runtime/agent/traffic -count=1` 通过
- `cd cloud-bridge && go test ./runtime/bridge/connectorproxy ./runtime/bridge/routing ./runtime/bridge/control ./runtime/bridge/registry -count=1` 通过
- `cd ltfp && make test-integration` 通过
- case 编号与结果已同步到 [ltfp/docs/TestMatrix.md](../ltfp/docs/TestMatrix.md) 的 `Agent/Bridge A13 对齐用例` 章节

验收标准：

- 高风险链路（超时、取消、回压、fallback）均有自动化回归
- 发布前可量化稳定性与容量余量

### A14. UI 层与内嵌打包

注：按当前执行口径，前 3 项分别归属 A15（Agent-Tauri）与 A16（Bridge Admin 能力）跟踪，本节实际开发范围为第 4-7 项。

- [ ] Agent 桌面 UI 层采用 Tauri，并定义与 Agent Runtime 的本地交互边界（UDS / Named Pipe 长连接，支持Linux和Windows两种平台）
- [ ] Agent 桌面 UI 层通信协议详细方案： [Agent 与 Tauri 本地通信技术方案](./Agent–TauriLocalCommunicationDesignProposal.md)。
- [ ] Bridge 管理页面采用 React + shadcn/ui 实现，详细方案：[Bridge 管理后台技术方案](BridgeAdminBackendTechnicalProposal.md)
- [x] 建立 Bridge 管理页面构建产物流程（如 `npm run build` 输出静态资源）
- [x] Bridge 服务端实现静态资源内嵌（如 `go:embed`）与管理页面路由
- [x] 发布与部署流程改造：Bridge 单包即含管理 UI，不依赖独立 UI 服务部署
- [x] 补齐内嵌资源版本标识与缓存策略（静态资源 hash / cache-control）

执行记录（2026-03-13 / 2026-03-14）：

- `make build-cloud-bridge` 已串联 `cloud-bridge/web` 构建与 `cloud-bridge` 二进制打包（单包包含 UI）
- Bridge Admin UI 路由已挂载到独立管理端口的 `/admin` 与 `/admin/*`
- UI 响应新增 `X-Bridge-UI-Version`，并按 `HTML no-cache / hash 资源 immutable` 输出 `Cache-Control`

验收标准：

- Agent 桌面端可独立启动并连接本地 Agent Runtime
- Bridge 二进制单独启动即可访问管理页面（不需要额外前端进程）
- 升级 Bridge 版本时，管理页面与后端 API 版本保持一致发布

### A15. Agent-Tauri 本地通信与宿主集成

- [x] Rust Host 进程管理：Agent 启动/停止/守护、单实例检查、崩溃恢复框架
- [x] Host 状态机落地：`desired_state(running/stopped)` + `exit_kind(expected/unexpected)`，避免 `agent.stop` 被自动拉起覆盖
- [x] 跨平台 IPC 抽象：Linux UDS、Windows Named Pipe（统一客户端/服务端接口）
- [x] 长连接多路复用协议：`request/response/event/ping/pong` + 帧头 `RequestId` 关联
- [x] 帧协议安全约束：`BodyLen` 上限、`ping/pong` 空 body、先验头再分配内存、非法帧断链
- [x] `session_secret` challenge-response（HMAC-SHA256）：`app.auth.begin/app.auth.complete` 双向 proof 校验
- [x] OS 对端身份校验：Linux `SO_PEERCRED` / Windows Named Pipe 对端令牌 SID + PID
- [x] IPC 端点安全策略：UDS 路径/权限（0700/0600、反 symlink）与 Named Pipe DACL/拒绝远程客户端
- [x] Agent `localrpc` 服务端：`app/agent/session/service/tunnel/traffic/config/diagnose` 方法域落地
- [x] 禁止接口与边界校验：拒绝 `traffic.open/reset`、`tunnel.read/write`、`relay.inject` 等越界调用
- [x] Rust `event_bridge`：事件转发、节流/聚合、稳定前端事件模型
- [x] 断链重连流程：`disconnected -> reconnecting -> resyncing -> connected`，重连后强制全量 snapshot 对账
- [x] 生命周期流程联调：`app.bootstrap` 启动链路、优雅关闭链路、超时回收路径
- [x] 宿主观测性：`agent_host_ipc_connected/reconnect_total/rpc_latency_ms/supervisor_restart_total` 指标与结构化日志
- [x] 配置管理与真实 runtime 打通：`agent_id/bridge_addr/tunnel_pool_*` 可配置并直连 `agent-core`，IPC transport 平台绑定只读
- [x] 跨平台测试矩阵：Linux/Windows 上的握手、权限、断链重连、状态对账、停止语义

当前状态：已完成。

执行记录（2026-03-13 / 2026-03-14）：

- `apps/dev-agent/src-tauri/src/main.rs` 已落地 Supervisor 骨架：单实例锁、`desired_state + exit_kind` 状态机、崩溃恢复回拉与 `app_bootstrap/agent_start/agent_stop/agent_restart/app_shutdown` 命令链路
- 事件桥统一为 `agent-runtime-changed`，增加节流窗口与 dropped 聚合计数，前端订阅模型固定
- 宿主观测性已接入 `agent_host_ipc_connected/agent_host_ipc_reconnect_total/agent_host_rpc_latency_ms/agent_host_supervisor_restart_total` 快照字段与 `host_logs_snapshot` 结构化日志查询
- `apps/dev-agent/src/App.tsx` 与 `apps/dev-agent/src/index.css` 已按方案边界改造 UI（宿主控制/快照/事件/日志），并联通 Tauri command
- `apps/dev-agent/src/App.tsx` 与 `apps/dev-agent/src/index.css` 已完成白色主题重设计：总览/运行控制/配置管理/服务列表/通道列表/诊断中心均对齐当前可用 command，不展示越界数据面能力
- 已校正配置写入链路：`host_config_update` 仅支持真实可生效的宿主启动字段（`runtime_program/runtime_args/ipc_transport/ipc_endpoint`），前端配置页已去除虚构 Bridge 三项并提示“重启生效”
- 已完成 UI 风格升级：`apps/dev-agent/src/App.tsx` 与 `index.css` 切换为 shadcn 风格组件化布局（侧栏/顶栏/统计卡/表格），仅使用现有真实 command 与 snapshot/event/logs 数据
- Win11 启动稳态已增强：单实例锁改为 OS 文件锁（`fs2::FileExt::try_lock_exclusive`）避免残留锁文件导致秒退；启动失败落地 `startup.log` 并弹出 Windows 错误提示框
- 已补齐本地 RPC 帧协议实现：`Magic/Version/Type/Flags/RequestId/BodyLen/Body`，支持 `request/response/event/ping/pong` 并按 `RequestId` 关联
- 已落地帧安全约束：`BodyLen<=1MiB`、`ping/pong` 空 body、先验头后分配 body、JSON 禁止 `request_id` 字段、非法帧返回 `PROTOCOL_ERROR`
- 已接入本地握手 challenge-response：`ipc_client.connect` 启动 `app.auth.begin/app.auth.complete` 双向 HMAC 校验，mock runtime 在鉴权通过前拒绝 `app.bootstrap/agent.snapshot` 等业务方法
- 已接入 OS 对端身份校验：Linux `SO_PEERCRED` 强校验 `uid/pid`；Windows 强校验 `NamedPipe server pid + Token SID`
- 已补齐 IPC 端点安全策略：UDS 目录/锁文件权限强制收敛并校验 owner，默认运行目录不再回落共享 `/tmp`；Windows Named Pipe 端点作用域优先使用当前用户 SID，服务端采用 `PIPE_REJECT_REMOTE_CLIENTS + DACL(current user + LocalSystem)`
- mock runtime 已接入 UDS localrpc listener，启动链路改为真实 `app.bootstrap -> agent.snapshot -> ping` 对账，不再依赖纯 sleep 模拟
- 内置 localrpc 内核已补齐方法域落地：`session.snapshot/session.reconnect/session.drain`、`service.list`、`tunnel.list`、`config.snapshot`、`diagnose.snapshot/diagnose.logs`，Bridge 未接入时统一返回 `UNAVAILABLE` 或空快照，不再使用 `METHOD_NOT_ALLOWED`
- 已落地方法域白名单与越界拒绝：拒绝 `traffic.open/reset`、`tunnel.read/write`、`relay.inject`、`runtime.takeover`
- 已完成 `ipc_client` 跨平台传输抽象：Linux `UDS` 与 Windows `Named Pipe` 统一为 `LocalRpcStream`，mock runtime 已新增 Named Pipe 服务端分支
- `apps/dev-agent/src-tauri/src` 已按方案 10.1 调整目录结构：`commands/`、`agent_host/`、`state/`，并将 `main.rs` 收敛为入口组装层
- 已新增连接模型落地清单：[Agent-Tauri 连接模型可执行清单](./Agent-TauriConnectionModelExecutionChecklist.md)，对齐方案 7.3 与第 8 节边界并持续跟踪
- `supervisor` 已补齐事件泵：周期 `ping + drain_events`，将 Agent 原生事件聚合后转发为前端稳定事件 `agent-runtime-changed`
- 已完成真实配置链路：配置页新增 `agent_id/bridge_addr/tunnel_pool_*`，`host_config_update`、`launcher` 与 `agent-core main` 串联 `DEV_AGENT_CFG_*` 环境变量并在 runtime 启动阶段生效
- 已完成 UI 收敛：右侧主内容区移除“当前视图”标题，IPC 传输方式改为平台绑定只读展示（Windows `named_pipe` / Unix `uds`）
- 已按“UI↔Agent 内核（IPC）+ Agent↔Bridge（网络）”双层模型收敛状态口径：内核链路与 Bridge 服务状态拆分展示，Bridge 状态在内核未建链时显示“等待内核 IPC 建链”
- 已新增跨平台测试矩阵工作流：`.github/workflows/dev-agent-a15-matrix.yml`，在 Linux/Windows 双平台执行 `cargo check + cargo test + npm run build`，并在 Linux 额外执行 `x86_64-pc-windows-gnu` 交叉目标检查
- 验证记录：
  - `cd apps/dev-agent/src-tauri && cargo check` 通过
  - `cd apps/dev-agent/src-tauri && cargo test --bin dev-agent -- --nocapture` 通过
  - `cd apps/dev-agent/src-tauri && cargo check --target x86_64-pc-windows-gnu` 通过
  - `make build-dev-agent-tauri-cross TAURI_TARGET=x86_64-pc-windows-gnu` 通过
  - `cd apps/dev-agent && npm run build` 通过

验收标准：

- UI 可稳定完成六类能力：生命周期管理、配置管理、运行态快照、指标日志诊断、运维命令、崩溃恢复重连
- 未授权进程无法通过本地 IPC 握手与鉴权
- 断链恢复后 UI 状态与 Agent 真相源一致（无陈旧状态）
- `agent.stop` 后不会被 Supervisor 误判为崩溃并自动拉起

### A16. Bridge 管理后台（Admin API + Admin UI）

- [ ] Bridge 启动开关：显式支持 `admin.enabled`（默认关闭）与 `admin.listen_addr/admin.base_path` 配置
- [ ] `admin.enabled=false` 时不注册 `/api/admin/*` 路由、不提供管理静态资源、不初始化后台中间件
- [ ] 管理面网络隔离：Admin 独立监听地址/端口，禁止仅依赖路径前缀隔离
- [ ] 后台模块落地：`adminapi/`（鉴权、中间件、路由）+ `adminview/`（快照/事件/诊断视图模型）
- [ ] 权限模型落地：`viewer/operator/admin` 三角色与接口级权限矩阵
- [ ] 登录与认证落地：Cookie/Token 方案二选一并明确服务端校验策略
- [ ] CSRF 防护：Cookie 模式下写接口强制 `CSRF Token + Origin/Referer` 校验 + 安全 Cookie 属性
- [ ] 只读 API 落地：`bridge/routes/connectors/sessions/tunnels/traffic/config/logs/metrics/diagnose` 资源域
- [ ] 查询契约落地：列表检索统一 `cursor + limit`，服务端硬上限与日志/时序时间窗口限制
- [ ] 运维 API 收敛：仅支持 reload/drain/diagnose export 等受控命令，禁止任意脚本执行
- [ ] 配置并发控制：`config_version + if_match_version` 乐观并发，版本冲突返回 `409`
- [ ] 审计能力：所有写操作记录操作者、作用域、参数摘要、结果、trace_id、时间戳
- [ ] 导出安全：日志与诊断包统一脱敏（token/cookie/secret/password/private_key 等）且仅 `admin` 可导出
- [ ] 页面功能落地：Dashboard、Route、Connector/Session、Tunnel/Traffic、配置运维、日志指标诊断六类页面
- [ ] 内嵌部署联调：前端构建产物内嵌 Bridge，可独立二进制访问后台并完成端到端操作

验收标准：

- Admin 模块可在启动时显式开关，关闭时管理面不可访问且不占用管理监听
- 管理后台所有写操作均满足认证、权限、CSRF（Cookie 模式）、审计、幂等/防重约束
- 管理查询在高基数数据下无无界返回，不影响 Bridge 数据面稳定性
- 日志/诊断导出默认脱敏且可追溯导出审计记录

---

## 五、按阶段推进建议（与技术方案第 15 节对齐）

### 阶段一：控制面与基础骨架

- A0、A1、A2、A3（不含高级调参）、A10（基础指标）

### 阶段二：connector path 打通

- A4、A5（connector 相关）、A6、A7、A9、A12（connector 部分）

### 阶段三：direct path 与 hybrid

- A8、A12（external/hybrid 部分）、A10（hybrid/direct 指标）

### 阶段四：稳态优化

- A3（调参）、A9（性能优化）、A11（配置治理）、A13（完整门槛）、A14（UI 打包与发布收口）、A15（宿主稳态与安全收口）、A16（管理后台能力收口）

---

## 六、里程碑勾选模板

- [ ] M1：阶段一完成并通过基础联调
- [ ] M2：阶段二完成并通过 connector 回归
- [ ] M3：阶段三完成并通过 hybrid/direct 回归
- [ ] M4：阶段四完成并满足发布门槛
