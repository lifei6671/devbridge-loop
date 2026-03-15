# Bridge Admin SSE 协议（v1）

## 1. 目标

用于 Admin Web 与 Bridge 后端之间的实时数据同步，减少手动刷新和轮询开销。

- 协议版本：`v1`
- 传输方式：HTTP SSE（`text/event-stream`）
- 推送模型：服务端按 `topic` 周期推送快照 + 心跳保活

## 2. 接口定义

- 方法：`GET`
- 路径：`/api/admin/events/stream`
- 权限：`viewer` 及以上

鉴权方式：

1. `Authorization: Bearer <token>`（推荐）
2. `access_token` query（仅 SSE 场景使用，用于浏览器 `EventSource` 无法自定义 Header 的限制）

> 注意：`access_token` query 仅对该 SSE 路由生效，其他 API 仍要求 Bearer Header。

## 3. 查询参数

通用参数：

- `topics`：订阅主题，支持单值、逗号分隔、多值，或 `all`
- `interval_ms`：服务端推送周期（毫秒）
  - 最小 `1000`
  - 最大 `30000`
  - 默认 `5000`

过滤参数（按 topic 生效）：

- `session_state`：用于 `connectors`（如 `ACTIVE/DRAINING/STALE/CLOSED`）
- `tunnel_state`：用于 `traffic`（如 `idle/reserved/active/closed/broken`）
- `connector_id`：用于 `traffic`，按指定 Agent/Connector 聚合和过滤 tunnel 数据
- `time_range_minutes`：用于 `observability`，默认 `30`，最大 `1440`

## 4. Topic 列表

- `dashboard`
- `routes`
- `connectors`
- `traffic`
- `ops`
- `observability`

## 5. 事件类型

服务端会发送以下 SSE 事件名：

1. `bridge.ready`
2. `bridge.snapshot`
3. `bridge.heartbeat`

同时会发送 `retry: <interval_ms>`，用于客户端断线重连等待策略。

## 6. 通用 Envelope

`bridge.ready / bridge.snapshot / bridge.heartbeat` 的 `data` 统一采用 JSON envelope：

```json
{
  "version": "v1",
  "type": "ready|snapshot|heartbeat",
  "topic": "dashboard",
  "server_time_ms": 1773491430762,
  "sequence": 12,
  "interval_ms": 5000,
  "topics": ["dashboard"],
  "payload": {}
}
```

字段说明：

- `version`：协议版本
- `type`：事件类型
- `topic`：快照所属主题（仅 `snapshot` 必填）
- `server_time_ms`：服务端生成事件时间
- `sequence`：连接内递增序号
- `interval_ms`：服务端确认的推送周期（常见于 `ready`）
- `topics`：连接实际生效的订阅主题（常见于 `ready`）
- `payload`：业务快照数据（仅 `snapshot`）

## 7. Snapshot Payload 约定

### 7.1 `dashboard`

```json
{
  "overview": {},
  "tunnel_summary": {},
  "traffic_summary": {},
  "diagnose_summary": {}
}
```

### 7.2 `routes`

```json
{
  "items": []
}
```

### 7.3 `connectors`

```json
{
  "connectors": [],
  "sessions": [],
  "session_state_filter": "ALL"
}
```

### 7.4 `traffic`

```json
{
  "tunnel_summary": {},
  "agent_pool_summary": {},
  "tunnels": [],
  "connectors": [],
  "traffic_summary": {},
  "tunnel_state_filter": "ALL",
  "tunnel_connector_filter": "ALL"
}
```

### 7.5 `ops`

```json
{
  "snapshot": {},
  "connectors": [],
  "sessions": []
}
```

### 7.6 `observability`

```json
{
  "from_ms": 1773490000000,
  "to_ms": 1773491800000,
  "logs": [],
  "metrics": [],
  "diagnose_summary": {},
  "time_range_minutes": 30
}
```

## 8. 重连与降级建议

前端建议策略：

1. 首选 SSE。
2. 若 `ready/snapshot` 未成功到达即出现 `error`，回退到轮询。
3. SSE 已建立后短暂波动，保持连接等待自动重连。
4. 轮询作为兜底，间隔与 `interval_ms` 一致。

## 9. 示例

```text
GET /api/admin/events/stream?topics=traffic&interval_ms=5000&tunnel_state=active&connector_id=agent-local&access_token=devbridge-viewer-token
```

## 10. 本地联调步骤（Smoke Test）

1. 启动 Bridge（示例）：

```bash
cd cloud-bridge
go run ./cmd/cloud-bridge -config ./config.example.yaml
```

2. 终端验证 SSE 首帧事件（推荐 `curl -N`）：

```bash
curl -N "http://127.0.0.1:39081/api/admin/events/stream?topics=dashboard&interval_ms=1000&access_token=devbridge-viewer-token"
```

期望输出包含：

- `event: bridge.ready`
- `event: bridge.snapshot`
- `\"topic\":\"dashboard\"`

3. 前端页面验证：

- 打开 Admin UI。
- 应用可读 token（`viewer`）。
- 顶部自动刷新状态应显示 `实时流已连接（SSE）`。
- 若服务端不可达或握手失败，应自动切换到轮询提示。
