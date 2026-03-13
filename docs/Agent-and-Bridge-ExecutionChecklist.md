# Agent 与 Bridge 实现执行清单（一期）

## 一、文档目的

本清单用于承接 [Agent‑and‑Bridge‑Implementation‑Technical‑Design.md](./Agent‑and‑Bridge‑Implementation‑Technical‑Design.md)，将技术方案拆解为可执行、可联调、可验收的工程任务。

说明：

- 本清单以 Agent/Bridge 运行时实现为主，不替代传输层专项清单 [LTFP-TransportExecutionChecklist.md](./LTFP-TransportExecutionChecklist.md)。
- 本清单默认全部未勾选；如已有实现，可按“验收标准”回填勾选状态。

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

- [ ] `trafficAcceptor` 独占 idle tunnel 的 `ReadFrame`
- [ ] `TrafficRuntime` 处理 `TrafficOpen`、本地 dial、ack、relay、close/reset
- [ ] `endpoint_selection_hint` 软引导 + 本地重选
- [ ] `TrafficOpenAck.metadata` 回传 `actual_endpoint_id/actual_endpoint_addr`
- [ ] 收到取消信号时中止 dial/relay 并清理 upstream

验收标准：

- 任意时刻同一 tunnel 只有一个 reader
- Bridge 可拿到最终实际 endpoint 作为观测真相

### A5. Bridge Ingress 与 RouteResolver

- [ ] 落地三类入口：`l7_shared`、`tls_sni_shared`、`l4_dedicated_port`
- [ ] RouteResolver 目标分类：`connector_service/external_service/hybrid_group`
- [ ] 过滤规则：scope 不匹配、service 不健康、connector 离线、session 非 active
- [ ] 明确 `https` 与 `tlsSni` 共享 listener 的实现约束

验收标准：

- 三类入口互不串扰，路由结果可解释
- 端口与监听行为无冲突

### A6. Bridge ConnectorProxy 主链路

- [ ] `AcquireIdleTunnel` 分配与状态流转：`idle -> reserved -> active`
- [ ] 发送 `TrafficOpen`，等待 `TrafficOpenAck` 成功后进入 relay
- [ ] 正常关闭后清理 tunnel registry，本地状态回收
- [ ] no-idle 场景：短等待 + refill + 超时失败（503）

验收标准：

- connector path 可稳定打通并闭环
- no-idle 时行为确定且可观测

### A7. 超时取消与迟到 Ack 规则

- [ ] 实现 `open_sent` 超时后的取消流程（reset 或直接关闭）
- [ ] tunnel 标记 `broken` 并从 registry 摘除
- [ ] 迟到 `TrafficOpenAck` 一律丢弃，不得恢复终态
- [ ] 增加 `bridge_traffic_open_ack_late_total` 指标

验收标准：

- 超时后不会出现状态回滚或幽灵恢复
- late-ack 有明确指标和日志证据

### A8. DirectProxy 与 Hybrid Fallback

- [ ] `external_service` 路径：discovery、endpoint cache、direct dial、relay
- [ ] `hybrid_group` 仅允许 `pre_open_only` fallback
- [ ] pre-open 失败分支区分：已分配 tunnel vs 未分配 tunnel
- [ ] post-open 失败禁止 fallback

验收标准：

- hybrid 仅在规定窗口 fallback
- connector/direct 路径边界清晰，错误语义不混淆

### A9. 数据面协议、并发与反压

- [ ] 落地统一帧：`Open/OpenAck/Data/Close/Reset`
- [ ] 落地并发约束：单读单写，多写串行，多读禁止
- [ ] relay pump 使用有界缓冲，禁止无界队列
- [ ] 下游阻塞时暂停继续 `ReadFrame`，依赖底层窗口流控回压
- [ ] 回压超阈值（deadline/timeout）按错误语义 close/reset

验收标准：

- 慢客户端场景不会导致 Bridge/Agent 内存无限增长
- 反压行为在 `grpc_h2/quic/h3` 绑定下保持一致原则

### A10. 观测性与诊断

- [ ] Agent 指标：session、pool、open_ack 延迟、upstream dial 延迟
- [ ] Bridge 指标：acquire 等待、open timeout/reject/late ack、hybrid fallback、actual endpoint override
- [ ] 统一日志字段：`trace_id/traffic_id/route_id/service_id/actual_endpoint_*/session_id/session_epoch/connector_id/tunnel_id`
- [ ] 关键错误路径补齐结构化日志模板

验收标准：

- 任意失败请求可在单次排查中追溯完整链路
- 指标可直接支持 SLO 与容量调参

### A11. 配置与初始化覆盖

- [ ] 保持当前默认值：`minIdle=8/maxIdle=32/rateLimit=10/burst=20/...`
- [ ] Agent 初始化支持外部传入 `tunnelPool` 参数覆盖默认值
- [ ] 未传字段回落默认值
- [ ] 首版不做运行时热更新（重启生效）

验收标准：

- 默认配置不变
- 启动参数可覆盖并稳定生效

### A12. 错误语义与终止行为

- [ ] connector path 错误分类：route miss/service unavailable/no idle/open reject/open timeout/dial 失败/relay reset
- [ ] external path 错误分类：discovery miss/provider down/refresh 失败/direct dial 失败/relay 失败
- [ ] idle tunnel 回收触发：TTL、超上限、session draining/stale
- [ ] close/reset 的状态与日志口径一致

验收标准：

- 错误码、HTTP 返回、日志字段三者一致
- 终止语义在双端对齐

### A13. 测试矩阵与发布门槛

- [ ] 单元测试：状态机、并发边界、timeout/cancel、ack 去重、参数回落
- [ ] 集成测试：握手、心跳、pool、open/ack/data/close/reset、late ack、hybrid pre-open fallback
- [ ] 压测：突发 no-idle、refill 节流、慢读回压、大包 relay
- [ ] 与 [ltfp/docs/TestMatrix.md](../ltfp/docs/TestMatrix.md) 对齐补齐 case 编号与结果
- [ ] 发布门槛：单测/集测/关键压测全部通过

验收标准：

- 高风险链路（超时、取消、回压、fallback）均有自动化回归
- 发布前可量化稳定性与容量余量

### A14. UI 层与内嵌打包

- [ ] Agent 桌面 UI 层采用 Tauri，并定义与 Agent Runtime 的本地交互边界（IPC/HTTP）
- [ ] Bridge 管理页面采用 React + shadcn/ui 实现
- [ ] 建立 Bridge 管理页面构建产物流程（如 `npm run build` 输出静态资源）
- [ ] Bridge 服务端实现静态资源内嵌（如 `go:embed`）与管理页面路由
- [ ] 发布与部署流程改造：Bridge 单包即含管理 UI，不依赖独立 UI 服务部署
- [ ] 补齐内嵌资源版本标识与缓存策略（静态资源 hash / cache-control）

验收标准：

- Agent 桌面端可独立启动并连接本地 Agent Runtime
- Bridge 二进制单独启动即可访问管理页面（不需要额外前端进程）
- 升级 Bridge 版本时，管理页面与后端 API 版本保持一致发布

---

## 五、按阶段推进建议（与技术方案第 15 节对齐）

### 阶段一：控制面与基础骨架

- A0、A1、A2、A3（不含高级调参）、A10（基础指标）

### 阶段二：connector path 打通

- A4、A5（connector 相关）、A6、A7、A9、A12（connector 部分）

### 阶段三：direct path 与 hybrid

- A8、A12（external/hybrid 部分）、A10（hybrid/direct 指标）

### 阶段四：稳态优化

- A3（调参）、A9（性能优化）、A11（配置治理）、A13（完整门槛）、A14（UI 打包与发布收口）

---

## 六、里程碑勾选模板

- [ ] M1：阶段一完成并通过基础联调
- [ ] M2：阶段二完成并通过 connector 回归
- [ ] M3：阶段三完成并通过 hybrid/direct 回归
- [ ] M4：阶段四完成并满足发布门槛
