# Bridge 管理后台执行清单（可执行版）

## 1. 文档目标

本清单把 [Bridge 管理后台技术方案](./BridgeAdminBackendTechnicalProposal.md) 转成可执行任务项，供开发、联调、验收持续勾选。

说明：

- 状态基线：`2026-03-14`。
- 任务状态：`[x]` 已完成、`[~]` 进行中、`[ ]` 未开始。
- 每个任务都给出建议代码入口与最小验收标准。

---

## 2. 里程碑与任务

### M1. 管理面骨架与只读能力（阶段一）

- [x] `BMA-1` Admin 启动开关：`admin.enabled` 默认关闭，支持 `admin.listen_addr/admin.base_path`
  - 代码入口：`cloud-bridge/runtime/bridge/app/config.go`、`cloud-bridge/runtime/bridge/app/bootstrap.go`
  - 验收：`admin.enabled=false` 时不初始化管理面服务，不注册管理路由。

- [x] `BMA-2` 模块分层：落地 `adminapi/` + `adminview/`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/`、`cloud-bridge/runtime/bridge/adminview/`
  - 验收：后台输出不直接暴露 registry 内部结构。

- [x] `BMA-3` 鉴权与角色模型：本地账号密码登录 + 浏览器会话 + `viewer/operator/admin`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/server.go`
  - 验收：未登录拒绝；viewer 可读；越权请求拒绝；写接口必须带 CSRF。

- [x] `BMA-4` 只读资源域：`bridge/routes/connectors/sessions/tunnels/traffic/config/logs/metrics/diagnose`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/server.go`
  - 验收：各域接口可访问，返回稳定 JSON 结构。

- [x] `BMA-5` 查询契约：`cursor+limit`、分页硬上限、日志/指标时间窗口限制
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/server.go`
  - 验收：超限自动截断；缺失时间窗参数时报错。

---

### M2. 受控写接口与并发语义（阶段二）

- [x] `BMA-6` 运维接口：`POST /api/admin/ops/config/reload`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/`、`cloud-bridge/runtime/bridge/app/`
  - 验收：`operator/admin` 可调用；返回可审计结果。

- [x] `BMA-7` 运维接口：`POST /api/admin/ops/session/:sessionId/drain`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/`、`cloud-bridge/runtime/bridge/app/`、`cloud-bridge/runtime/bridge/registry/`
  - 验收：目标 session 进入 `DRAINING`；service/tunnel 发生对应收敛副作用。

- [x] `BMA-8` 运维接口：`POST /api/admin/ops/connector/:connectorId/drain`
  - 代码入口：同上
  - 验收：按 connector 当前会话执行 drain，具备幂等行为。

- [x] `BMA-9` 配置并发控制：`PUT /api/admin/config` + `if_match_version`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/`、`cloud-bridge/runtime/bridge/app/`
  - 验收：版本一致才更新；冲突返回 `409`。

- [x] `BMA-10` 导出安全：`POST /api/admin/ops/diagnose/export`
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/`
  - 验收：仅 `admin` 可导出；导出内容默认脱敏；短时下载链接有效期受限。

- [x] `BMA-11` 写操作审计增强
  - 代码入口：`cloud-bridge/runtime/bridge/adminapi/server.go`
  - 验收：审计日志包含操作者、作用域、参数摘要、结果、trace_id、时间戳。

---

### M3. 安全收口（阶段三）

- [x] `BMA-12` Cookie 模式 CSRF（如启用 Cookie）
  - 验收：写操作强制 `CSRF Token + Origin/Referer` 校验，Cookie 安全属性正确。

- [x] `BMA-13` 管理面网络隔离校验
  - 验收：默认独立监听，避免仅依赖路径前缀作为安全边界。

- [x] `BMA-14` 导出下载链路审计与访问控制加固
  - 验收：导出与下载都可追溯，过期链接不可访问。

---

### M4. 页面能力与联调收口（阶段四）

- [x] `BMA-15` 六类页面端到端联调
  - 范围：Dashboard、Route、Connector/Session、Tunnel/Traffic、配置运维、日志指标诊断
  - 验收：页面均调用真实 API，非占位数据。
  - 完成记录：六类页面均已接入真实 API；详情抽屉与表格联动支持行高亮、自动定位、行级点击与键盘导航；自动刷新已升级为 SSE 优先 + 轮询兜底，并在轮询模式下定时自动重试恢复 SSE。

- [x] `BMA-16` 内嵌部署联调
  - 验收：Bridge 单二进制可访问 Admin UI 并执行完整读写流程。
  - 完成记录：`bootstrap` 集成测试已覆盖单实例 admin server 下 UI 路由、浏览器登录、读接口、SSE、写接口（reload/config update/session drain/diagnose export-download）与 session+csrf 写入约束链路。

---

## 3. 本轮落地范围（当前执行）

- [x] 已完成 `BMA-6 ~ BMA-11` 与 `BMA-12 ~ BMA-14`（写接口 + 并发控制 + 安全收口）。
- [x] 已完成 `BMA-15 ~ BMA-16`（页面联调与单二进制端到端收口）。
- [ ] 完成后回写到：
  - `docs/UI-Agent-Bridge-Unimplemented-Checklist.md` 的 `UAB-A2` 阶段记录
  - `docs/Agent-and-Bridge-ExecutionChecklist.md` 的 `A16` 勾选状态
