# DevBridge 多 Agent 服务模型演进技术方案

**文档状态**：Draft for Review  
**版本**：v1.5  
**依赖文档**：LTFP-v1-Draft.md (v2.1)、LTFP-TransportAbstraction.md (v2.1)、Agent_and_Bridge_Implementation_Technical_Design.md

---

> 本文档为多 Agent 服务模型重构的最终态规范。若与上述依赖文档存在冲突，以本文为准。

## 1. 背景与动机

### 1.1 当前约束

现有资源模型存在两个核心限制：

**限制一：service_key 把作用域编码进了身份标识**

```
service_key = "dev/alice/order-service"
               ↑     ↑        ↑
             namespace env    name（三者耦合为单一字符串）
```

导致的问题：
- 服务的 namespace/environment 变化时，service_key 随之变化，所有引用该 key 的 Route 全部失效
- 跨 scope 引用需要在 key 里硬编码路径，无法通过授权模型优雅控制
- Route 隐含地绑定了 namespace/environment，policy 演进困难

**限制二：service_key → service_id 是 1:1 映射**

```
service_key → service_id → connector_id（单 Agent 持有）
```

导致的问题：
- 同一逻辑服务无法被多个 Agent 同时发布
- 某个 Agent 断线后该服务完全不可用，无法 HA
- 开发环境多人共享同一服务入口的场景无法支持

**限制三：namespace/environment 的路由语义不清晰**

浏览器等客户端发起请求时不携带 namespace/environment 信息，现有设计未明确这两个字段在运行时流量路由中扮演什么角色，导致以下问题：

- 不清楚 namespace/environment 如何映射到对外可访问的 host/path
- 同一 host+path 在不同 scope 下可能对应不同服务，路由优先级规则缺失
- RouteMatch 只支持 host/path，无法表达"同一入口、按请求内容分发到不同服务"的场景

### 1.2 目标

本方案解决以下问题：

1. 将 service 的**身份、作用域、归属**三个维度解耦
2. 支持同一逻辑服务被多个 Agent 同时发布（Multi-Agent HA）
3. 明确 namespace/environment 在配置阶段的作用（派生 host、隔离边界），与运行时路由完全解耦
4. 扩展 RouteMatch 支持 Header/Query 级别匹配条件，覆盖内容路由场景
5. 在 Admission Pipeline 增加 Route 冲突检测，防止多个服务抢占同一入口
6. 明确废弃 `service_key` 模型，不提供兼容路径，直接切换到新协议重构
7. 为后续跨 scope 引用、灰度发布、流量权重分配预留扩展空间
8. 重构 Transport/Binding 相关协议承载与适配实现，使其完整承载新模型字段与语义

### 1.3 非目标

- 不实现完整 RBAC 与租户系统
- 不实现跨 scope 引用授权（预留扩展点，本版不落地）
- 不实现 mid-stream 级别的实例切换（failover 仍限于 pre-open 阶段）
- 不提供旧协议兼容层（包括 `service_key` 解析兼容、双写、双栈协商）

---

## 2. 核心概念重新定义

### 2.1 三个维度分离

| 维度 | 定义 | 载体 | 示例 |
|------|------|------|------|
| **身份 (Identity)** | 服务是"谁" | `logical_service_id`（server 分配的 UUID） | `ls_01J8Z6C4X9` |
| **作用域 (Scope)** | 服务属于"哪里" | `Scope` 独立结构体 | `{namespace:"dev", environment:"alice"}` |
| **名称 (Name)** | 人类可读标识 | `service_name`（scope 内唯一） | `"order-service"` |

旧的 `service_key` 废弃，由 `(service_name, scope)` 二元组替代，内部索引使用 `logical_service_id`。

### 2.2 两层服务模型

引入 **LogicalService + ServiceInstance** 两层模型：

```
LogicalService（逻辑服务）
  ├── 对应一个"服务的意图"
  ├── 被 Route 引用
  ├── 聚合所有 Agent 的实例
  └── 状态由实例健康情况派生

ServiceInstance（服务实例）
  ├── 对应某个 Agent 发布的一次具体声明
  ├── 携带该 Agent 本地的 endpoints
  ├── 与具体 connector_id + session_epoch 绑定
  └── 独立维护健康状态
```

### 2.3 Scope 结构

```proto
message Scope {
  string namespace   = 1;  // 顶级隔离单元
  string environment = 2;  // namespace 内的子环境
  // 保留扩展：tenant, region, cluster 等
}
```

Scope 是独立字段，不编码进任何 name 或 key，始终通过结构化字段传递和比较。

### 2.4 namespace/environment 的职责边界

这两个字段在系统中承担**双重角色**：配置时用于服务注册和隔离，运行时由调用方通过 Header 携带，驱动 Bridge 的作用域降级查找。

```
配置阶段（Agent 注册时）：
  namespace + environment + service_name
      → 确定服务唯一性边界
      → 派生对外访问 host（可选）
      → 映射到第三方 Discovery 命名空间

运行时（请求路由时）：
  调用方在 Header 中携带 scope
      → Bridge Route Resolver 读取 scope
      → 按 ScopeFallbackPolicy 执行降级链查找
      → 找到匹配的 LogicalService 后路由到 ServiceInstance
      → scope Header 在整个链路透传，不修改
```

**namespace/environment 的作用：**

| 作用 | 阶段 | 说明 |
|------|------|------|
| 唯一性边界 | 配置时 | 同名服务在不同 scope 下互相独立，不冲突 |
| Host 派生 | 配置时 | 按约定规则自动生成对外访问域名 |
| Discovery 映射 | 配置时 | 向 Nacos/Consul 导出时映射到第三方系统对应概念 |
| 请求作用域 | 运行时 | 调用方声明自己的身份，驱动降级查找和链路追踪 |

**Host 派生规则（Bridge 侧可配置模板）：**

```
模板（默认）：{service_name}.{environment}.{namespace}.{base_domain}

示例：
  service_name = "order-service"
  scope = {namespace: "dev", environment: "alice"}
  base_domain = "example.com"

  →  host = "order-service.alice.dev.example.com"
```

Agent 发布时 `exposure.host` 留空则 Bridge 自动派生。Agent 也可以显式指定 `exposure.host` 覆盖派生规则，此时 scope 仅用于内部隔离，不影响外部域名。

### 2.5 Scope 传递机制

scope 由**第一个调用者**（浏览器、客户端、上游服务）在发起请求时携带，后续 Bridge 和 Agent 在转发时透传，不修改。

**默认传递方式：HTTP Header**

```
请求方设置：
  X-Bridge-Namespace: dev
  X-Bridge-Environment: alice

Bridge 收到后：
  → 解析 Header，得到 scope = {dev, alice}
  → 执行 ScopeFallbackPolicy 驱动的降级查找
  → 向 Agent 发 TrafficOpen 时在 metadata 中携带原始 scope

Agent 向本地 upstream 转发时：
  → 将 scope Header 原样透传给 upstream
  → upstream 服务可感知调用方身份
```

**透传规则：**
- Bridge 和 Agent 只读取、透传 scope，不修改
- scope 在整条链路上代表"第一个调用者的身份"，语义保持完整
- 若请求未携带 scope Header，Bridge 使用默认 scope（`default/base`），可在 Bridge 全局配置中覆盖此默认值

### 2.6 ScopeFallbackPolicy（管理员配置）

降级策略由 Bridge 管理员按 namespace 维度配置，不同 namespace 可以有独立策略，也可以完全禁用降级。

```proto
message ScopeFallbackPolicy {
  string   policy_id  = 1;  // 唯一标识
  string   namespace  = 2;  // 此策略生效的 namespace（必填）
  bool     enabled    = 3;  // 是否启用降级，false 则精确匹配失败直接返回 404

  // 降级链：按顺序逐级尝试，由管理员配置
  repeated FallbackStep chain = 4;

  // 外部注册中心兜底（仅在本地降级链全部 miss 后启用）
  ExternalFallbackConfig external = 5;
}

message FallbackStep {
  Scope target_scope = 1;  // 降级目标 scope
  // 预留：可扩展匹配条件，如 service_type 过滤等
}

message ExternalFallbackConfig {
  bool enabled = 1;  // 是否启用外部兜底
  // 具体注册中心由全局 DiscoveryProvider 配置决定
  // 此处仅控制是否在本地 miss 后触发外部查询
}
```

**配置示例：**

```yaml
# dev namespace 的降级策略
policy_id: fallback-dev
namespace: dev
enabled: true
chain:
  - target_scope: {namespace: dev, environment: base}      # 第一级：同 namespace 共享环境
  - target_scope: {namespace: default, environment: base}  # 第二级：全局兜底环境
external:
  enabled: true   # 本地全部 miss 后查外部注册中心

# prod namespace：不允许降级
policy_id: fallback-prod
namespace: prod
enabled: false
```

**默认行为（无配置时）：** 禁用降级，精确匹配失败直接返回 404。管理员需要显式配置才能启用降级链。

**scope "base" 和 "default" 的语义约定：**
- `environment=base`：该 namespace 下的共享环境，注册到此处的服务对同 namespace 所有 environment 隐式可见（通过降级链）
- `namespace=default`：全局兜底 namespace，注册到 `default/base` 的服务对所有 namespace 隐式可见
- 这是**约定语义**，不是系统强制——实际可见性由管理员配置的降级链决定，"base" 和 "default" 只是推荐命名规范

---

## 3. 资源模型设计

### 3.1 LogicalService

```proto
message LogicalService {
  string logical_service_id  = 1;  // server 分配，全局唯一，不可变
  string service_name        = 2;  // 人类可读名字，scope 内唯一
  Scope  scope               = 3;  // 独立作用域字段
  string status              = 4;  // ACTIVE | INACTIVE（从实例状态派生）
  int32  active_instance_count = 5;
  int32  healthy_instance_count = 6;
  map<string,string> labels  = 7;  // 可用于 selector 匹配
  uint64 resource_version    = 8;
  map<string,string> metadata = 9;
}
```

**status 派生规则：**

```
healthy_instance_count > 0  →  ACTIVE
healthy_instance_count == 0 且 active_instance_count > 0  →  ACTIVE（有实例但全部不健康，可降级处理）
active_instance_count == 0  →  INACTIVE
```

**唯一性约束：** `(service_name, scope.namespace, scope.environment)` 三元组在 server 侧唯一。此约束为内部约束，不对外暴露为字符串 key。

### 3.2 ServiceInstance

```proto
message ServiceInstance {
  string instance_id           = 1;  // server 分配，全局唯一
  string logical_service_id    = 2;  // 归属的逻辑服务
  string connector_id          = 3;  // 持有该实例的 Agent
  string session_id            = 4;  // 当前绑定的 session
  uint64 session_epoch         = 5;  // 防旧 session 污染
  string instance_status       = 6;  // ACTIVE | INACTIVE | STALE
  string health_status         = 7;  // HEALTHY | UNHEALTHY | UNKNOWN
  repeated ServiceEndpoint endpoints = 8;  // 该 Agent 本地的 endpoint 列表
  ServiceExposure exposure     = 9;
  HealthCheckConfig health_check = 10;
  DiscoveryPolicy discovery_policy = 11;
  uint64 resource_version      = 12;
  map<string,string> labels    = 13;
  map<string,string> metadata  = 14;
}
```

**instance_status 语义：**

| 状态 | 含义 |
|------|------|
| `ACTIVE` | 持有该实例的 connector 在线，session_epoch 有效，可参与路由 |
| `INACTIVE` | 主动下线或不满足接流条件 |
| `STALE` | session 失效，保留用于审计，不参与路由 |

**instance_status 与 health_status 正交：**
- `instance_status` 描述实例的生命周期状态（连接层面）
- `health_status` 描述实例后端服务的健康状态（业务层面）
- 只有 `instance_status=ACTIVE` 且 `health_status=HEALTHY` 的实例才进入路由候选池

### 3.3 ServiceSelector（Route 引用服务的方式）

Route 不再直接引用 service_key 字符串，改为 ServiceSelector：

```proto
message ServiceSelector {
  // 方式一：精确 ID 引用（最稳定，推荐用于机器生成的 Route）
  string logical_service_id = 1;

  // 方式二：name + scope 引用（推荐用于人工配置的 Route）
  string service_name = 2;
  Scope  scope        = 3;

  // 方式三：label selector（用于灰度/蓝绿场景，本版预留，不强制实现）
  map<string,string> match_labels = 4;

  // 实例级别过滤（可选）
  map<string,string> instance_labels = 5;  // 进一步过滤参与路由的实例
}
```

**解析优先级（server 侧 Route Resolver）：**
1. `logical_service_id` 非空 → 直接查找，忽略其他字段
2. `service_name + scope` 非空 → 通过 `(name, scope)` 索引查找
3. `match_labels` 非空 → 遍历匹配（性能较差，需限流）

三种方式互斥，不叠加。

### 3.4 RouteMatch 扩展

原有 RouteMatch 只支持 host/path/sni/port 级别的匹配，无法表达"同一入口、按请求内容分发到不同服务"的场景。本版扩展增加 Header 与 Query 参数匹配条件。

```proto
message RouteMatch {
  // 现有字段（不变）
  string protocol    = 1;
  string host        = 2;
  string authority   = 3;
  uint32 listen_port = 4;
  string path_prefix = 5;
  string sni         = 6;

  // 新增：请求级别匹配条件（仅 L7 协议生效）
  repeated HeaderMatcher  headers = 7;
  repeated QueryMatcher   queries = 8;
}

message HeaderMatcher {
  string name         = 1;  // Header 名（大小写不敏感）
  oneof match {
    string exact      = 2;  // 精确匹配
    string prefix     = 3;  // 前缀匹配
    string regex      = 4;  // 正则匹配
    bool   present    = 5;  // 仅判断存在性（值为 true 表示必须存在，false 表示必须不存在）
  }
}

message QueryMatcher {
  string name         = 1;  // Query 参数名
  oneof match {
    string exact      = 2;
    string prefix     = 3;
    string regex      = 4;
    bool   present    = 5;
  }
}
```

**约束：**
- `headers` 和 `queries` 中多个条件为 **AND 关系**（全部满足才匹配）
- Header/Query 匹配仅对 `l7_shared` ingress mode 生效；`tls_sni_shared` 和 `l4_dedicated_port` 忽略这两个字段
- `regex` 匹配需限制复杂度，防止 ReDoS，建议使用 RE2 语法

### 3.5 host+path 多服务映射的四种场景

同一 host+path 对应多个服务不只一种情况，需要明确区分处理方式：

**场景一：多 Agent HA（同一逻辑服务，预期行为）**

```
agent-alice 发布：order-service, scope{dev,alice}  →  order.alice.dev.example.com
agent-bob   发布：order-service, scope{dev,alice}  →  order.alice.dev.example.com

同一 host，对应同一个 LogicalService，两个 ServiceInstance。
```

处理：`InstanceSelector` 在候选实例中选一个，正常转发。

**场景二：不同服务抢占同一入口（配置冲突，应拒绝）**

```
agent-alice 发布：order-service,   scope{dev,alice}，exposure.host=api.example.com, path=/api/
agent-bob   发布：payment-service, scope{dev,bob},   exposure.host=api.example.com, path=/api/

两个不同 LogicalService 绑定到完全相同的 RouteMatch。
```

处理：Admission Pipeline 在注册阶段检测冲突，拒绝后注册的 Route，返回明确错误。详见第 5.8 节。

**场景三：同一入口按请求内容分发（主动设计，需 Header 匹配）**

```
Route A：host=api.example.com, path=/api/, header{X-Env=alice}  →  order-service(dev/alice)
Route B：host=api.example.com, path=/api/, header{X-Env=bob}   →  order-service(dev/bob)

相同 host+path，但 Header 不同，指向不同 LogicalService。
```

处理：RouteMatch 增加 HeaderMatcher，两条 Route 的匹配条件不冲突，Route Resolver 按精确度优先选择。

**场景四：路由前缀重叠（最长前缀优先，已有 priority 机制）**

```
Route A：host=api.example.com, path_prefix=/api/
Route B：host=api.example.com, path_prefix=/api/orders/

请求 GET /api/orders/123 两条都能匹配。
```

处理：Route Resolver 按 priority 字段排序，priority 相同时按 path_prefix 长度降序（最长前缀优先）。

**四种场景决策表：**

| 场景 | 冲突判定 | 解决机制 |
|------|---------|----------|
| 多 Agent HA（同一 LogicalService） | 不冲突（target 相同） | InstanceSelector |
| 不同服务，path/headers/priority 完全相同 | **冲突**，Admission 拒绝 | 注册时报错 |
| 相同 path，但 Header 条件不同 | 不冲突（条件可区分） | RouteMatch HeaderMatcher |
| path_prefix 存在包含关系 | 不冲突（最长前缀保证确定性） | Route priority + path 长度降序 |

### 3.6 与现有资源的关系

```
旧版：
  Route.target.connector_service.service_key  →  Service（含 connector_id）

新版：
  Route.target.connector_service.selector（ServiceSelector）
      → LogicalService
          → [ServiceInstance...]（由 InstanceSelector 选一个）
              → connector_id → session → idle tunnel
```

Route 的 RouteMatch 在现有字段基础上扩展了 headers/queries，其他字段（RoutePolicy、priority 等）保持不变。

---

## 4. 控制面协议变更

### 4.1 PublishService（Agent → Bridge）

**变更：** 废弃 `service_key` 字段，拆分为 `service_name` + `scope`；新增 `instance_id` 用于重连时复用。

```proto
message PublishService {
  // 身份字段
  string instance_id    = 1;  // 可空；首次发布时为空，重连时携带上次获得的 instance_id
  string service_name   = 2;  // 纯名字，不含 scope 信息（必填）
  Scope  scope          = 3;  // 独立作用域（必填）

  // 元数据
  map<string,string> labels    = 4;
  map<string,string> metadata  = 5;

  // 服务配置
  string service_type          = 6;
  repeated ServiceEndpoint endpoints = 7;
  ServiceExposure  exposure    = 8;
  HealthCheckConfig health_check = 9;
  DiscoveryPolicy  discovery_policy = 10;
}
```

**废弃字段：** `service_key`、`namespace`、`environment`（这两个字段的信息移入 `scope`）。

**重构约束：** 本版不做旧字段兼容。若收到仍携带旧版 `service_key` 的消息，server 直接拒绝并返回 `UNSUPPORTED_LEGACY_PROTOCOL`。

### 4.2 PublishServiceAck（Bridge → Agent）

```proto
message PublishServiceAck {
  bool   accepted               = 1;
  string logical_service_id    = 2;  // 逻辑服务 ID（新增，替代原 service_id）
  string instance_id           = 3;  // 本次实例 ID（新增；Agent 持久化，重连时带回）
  string service_name          = 4;
  Scope  scope                 = 5;
  uint64 accepted_resource_version = 6;
  uint64 current_resource_version  = 7;
  string error_code            = 8;
  string error_message         = 9;
}
```

**Agent 行为要求：** Agent 收到 Ack 后，必须将 `instance_id` 持久化（至少在进程内存中），重连时原样带回，让 server 能复用同一个 instance 记录而不是创建新的。

### 4.3 UnpublishService（Agent → Bridge）

```proto
message UnpublishService {
  string instance_id         = 1;  // 优先使用（精确指定）
  string logical_service_id  = 2;  // 次选（指定逻辑服务）
  string service_name        = 3;  // 辅助字段
  Scope  scope               = 4;
  string reason              = 5;
}
```

### 4.4 ServiceHealthReport（Agent → Bridge）

新增 `instance_id` 字段，明确上报的是哪个实例的健康状态：

```proto
message ServiceHealthReport {
  string instance_id           = 1;  // 新增（必填）
  string logical_service_id    = 2;  // 新增（辅助定位）
  string service_health_status = 3;  // HEALTHY | UNHEALTHY | UNKNOWN
  repeated EndpointHealthStatus endpoint_statuses = 4;
  int64  check_time_unix       = 5;
  string reason                = 6;
  map<string,string> metadata  = 7;
}
```

### 4.5 TrafficOpen（Bridge → Agent）

新增 `instance_id`，Agent 需校验该实例归属于自己，并据此选择本地 endpoint：

```proto
message TrafficOpen {
  string traffic_id              = 1;
  string route_id                = 2;
  string logical_service_id     = 3;  // 逻辑服务（替代原 service_id）
  string instance_id             = 4;  // 新增：指定由哪个实例处理
  string source_addr             = 5;
  string protocol_hint           = 6;
  string trace_id                = 7;
  map<string,string> endpoint_selection_hint = 8;  // 非权威，仅作 hint
  map<string,string> metadata    = 9;
}
```

**Agent 对 instance_id 的处理：**
1. 校验 `instance_id` 是否属于本 connector 的已发布实例
2. 如不属于，返回 `TrafficOpenAck{success=false, error_code="INSTANCE_NOT_FOUND"}`
3. 如属于，使用该实例的 endpoints 选择本地 upstream 地址

### 4.6 ConnectorServiceTarget（Route 配置）

```proto
message ConnectorServiceTarget {
  ServiceSelector selector = 1;  // 替代原 service_key 字段
  map<string,string> instance_selector = 2;  // 可选：进一步过滤实例
  string load_balance_policy = 3;  // round_robin | least_conn | random | sticky（可选）
}
```

---

## 5. Server 侧核心逻辑变更

### 5.1 Canonical Config Registry

在现有 Registry 基础上，增加 LogicalService 与 ServiceInstance 的存储层：

```
CanonicalConfigRegistry
  ├── logical_services: map[logical_service_id] → LogicalService
  ├── service_index:    map[(name, scope)] → logical_service_id   （唯一性保障）
  ├── instances:        map[instance_id] → ServiceInstance
  ├── instance_index:   map[(connector_id, logical_service_id)] → instance_id
  └── label_index:      map[label_key=label_val] → []logical_service_id  （可选，供 selector 查询）
```

### 5.2 instance_id 校验规则

`instance_id` 是 Agent 提供的**复用意图**，不是 server 无条件接受的令牌。所有权由 `(instance_id → connector_id)` 绑定关系决定，`connector_id` 来自经过认证的 session，不由 Agent 在 `PublishService` 消息里自行声明。

**四条强制校验规则（server 侧按顺序执行）：**

| 规则 | 条件 | 失败处理 |
|------|------|----------|
| R1 归属校验 | instance 记录中绑定的 `connector_id` 必须等于当前 session 的 `connector_id` | 拒绝，`INSTANCE_OWNERSHIP_MISMATCH`，不修改任何状态 |
| R2 存在性降级 | instance_id 非空但记录不存在（已被清理或从未存在） | 不报错，静默降级为 FindOrCreate 流程，返回新分配的 instance_id |
| R3 epoch 单调性 | 新 `session_epoch` 必须 ≥ 已记录的 `session_epoch` | 拒绝，`STALE_SESSION_EPOCH` |
| R4 Agent 响应要求 | 收到 `INSTANCE_OWNERSHIP_MISMATCH` 必须清空本地缓存，重新走首次发布流程；收到与提示不同的 instance_id 必须以 Ack 为准更新本地缓存 | — |

**规则说明：**

- R1 是安全边界。即使两个 Agent 使用相同的 `connector_id`（split-brain），其 `session_epoch` 不同，R3 会在 R1 之后拦截旧的一方
- R2 的静默降级保证了 Agent 进程重启后本地缓存失效时能平滑恢复，不需要特殊错误处理路径
- R3 依赖现有的 `session_epoch` 单调递增保证，无需额外机制
- instance_id 提示与最终分配结果可能不同（R2 触发时），Agent 不得假设两者一致

### 5.3 PublishService 完整处理流程

```
收到 PublishService{
  service_name = "order-service",
  scope        = {dev, alice},
  instance_id  = "si_001",          // 可能为空
  connector_id = (来自认证 session)  // 不由消息体声明
  session_id   = current_session_id
  session_epoch = current_session_epoch
}

━━━ Step 1: 查找或创建 LogicalService ━━━

  key = (service_name, scope.namespace, scope.environment)
  if service_index[key] exists:
      logical_service_id = service_index[key]
  else:
      logical_service_id = newUUID()
      logical_services[logical_service_id] = LogicalService{...}
      service_index[key] = logical_service_id

━━━ Step 2: instance_id 校验与实例解析 ━━━

  if msg.instance_id == "":
      // Case A：首次发布，走 FindOrCreate
      goto FIND_OR_CREATE

  existing = instances[msg.instance_id]

  if existing == nil:
      // Case B：记录不存在，静默降级（R2）
      // 记录日志：instance_id hint not found, fallback to FindOrCreate
      goto FIND_OR_CREATE

  if existing.connector_id != current_connector_id:
      // Case C：归属不匹配，拒绝（R1）
      return PublishServiceAck{
          accepted     = false,
          error_code   = "INSTANCE_OWNERSHIP_MISMATCH",
          error_message = "instance_id is owned by a different connector",
      }

  if existing.session_epoch > current_session_epoch:
      // Case D：epoch 倒退，拒绝（R3）
      return PublishServiceAck{
          accepted     = false,
          error_code   = "STALE_SESSION_EPOCH",
          error_message = "session_epoch must be monotonically increasing",
      }

  // Case E：校验通过，复用现有实例
  instance = existing
  instance.endpoints      = msg.endpoints
  instance.session_id     = current_session_id
  instance.session_epoch  = current_session_epoch
  instance.instance_status = ACTIVE
  instance.resource_version++
  goto RECONCILE

FIND_OR_CREATE:
  instance_key = (connector_id, logical_service_id)
  if instance_index[instance_key] exists:
      // 同 connector 在该逻辑服务下已有实例，直接更新
      instance = instances[instance_index[instance_key]]
      instance.endpoints      = msg.endpoints
      instance.session_id     = current_session_id
      instance.session_epoch  = current_session_epoch
      instance.instance_status = ACTIVE
      instance.resource_version++
  else:
      // 全新实例
      instance = ServiceInstance{
          instance_id       = newUUID(),
          logical_service_id = logical_service_id,
          connector_id      = current_connector_id,
          session_id        = current_session_id,
          session_epoch     = current_session_epoch,
          instance_status   = ACTIVE,
          health_status     = UNKNOWN,
          endpoints         = msg.endpoints,
          resource_version  = 1,
      }
      instances[instance.instance_id]   = instance
      instance_index[instance_key]       = instance.instance_id

RECONCILE:
━━━ Step 3: 重新计算 LogicalService 派生状态 ━━━

  recalculateLogicalServiceStatus(logical_service_id)

━━━ Step 4: 返回 PublishServiceAck ━━━

  return PublishServiceAck{
      accepted                 = true,
      logical_service_id       = logical_service_id,
      instance_id              = instance.instance_id,   // Agent 必须以此为准更新本地缓存
      service_name             = msg.service_name,
      scope                    = msg.scope,
      accepted_resource_version = instance.resource_version,
  }
```

**注意：** Ack 中返回的 `instance_id` 是 server 认定的最终值。当 Case B（静默降级）触发时，该值与 Agent 提示的 `instance_id` 不同，Agent 必须更新本地缓存。

### 5.4 Session 断线处理

当 session 进入 STALE 时，仅影响该 connector 下的实例，不影响其他 connector 的实例：

```
session → STALE

for each instance owned by connector_id:
    instance.instance_status = STALE
    instance.health_status = UNKNOWN

recalculateLogicalServiceStatus(logical_service_id)
// 其他 connector 的实例仍然 ACTIVE，LogicalService 可能仍然 ACTIVE
```

### 5.5 LogicalService 状态派生（ServiceController）

引入 **ServiceController**，持续 Reconcile LogicalService 的派生状态：

```go
func (c *ServiceController) Reconcile(logicalServiceID string) error {
    ls := c.registry.GetLogicalService(logicalServiceID)
    instances := c.registry.GetInstances(logicalServiceID)

    activeCount := 0
    healthyCount := 0
    for _, inst := range instances {
        if inst.InstanceStatus == ACTIVE {
            activeCount++
            if inst.HealthStatus == HEALTHY {
                healthyCount++
            }
        }
    }

    newStatus := INACTIVE
    if activeCount > 0 {
        newStatus = ACTIVE
    }

    if ls.Status != newStatus || ls.ActiveInstanceCount != activeCount {
        c.registry.UpdateLogicalServiceStatus(logicalServiceID, newStatus, activeCount, healthyCount)
        c.notifyRouteResolver(logicalServiceID)  // 通知 Route Resolver 更新候选池
    }
    return nil
}
```

**触发时机：**
- 任意实例的 `instance_status` 变化
- 任意实例的 `health_status` 变化
- session STALE 导致批量实例状态变更
- 定时兜底（每 30s 全量对账）

### 5.6 Route Resolver 变更

路由分为三个独立步骤：**scope 解析**、**Route 匹配**（含作用域降级）、**实例选择**。

**Step 0：从请求 Header 解析 scope**

```go
func (r *RouteResolver) ExtractScope(req IngressRequest) Scope {
    ns  := req.Headers.Get("X-Bridge-Namespace")
    env := req.Headers.Get("X-Bridge-Environment")
    if ns == "" && env == "" {
        return r.config.DefaultScope  // 管理员配置的全局默认 scope
    }
    return Scope{Namespace: ns, Environment: env}
}
```

**Step 1：构建作用域降级链**

```go
func (r *RouteResolver) BuildScopeChain(requestScope Scope) []Scope {
    policy := r.policyRegistry.GetFallbackPolicy(requestScope.Namespace)

    // 未配置策略或降级未启用：只尝试精确 scope
    if policy == nil || !policy.Enabled {
        return []Scope{requestScope}
    }

    // 降级链第一个始终是请求的原始 scope
    chain := []Scope{requestScope}
    for _, step := range policy.Chain {
        // 避免重复（如请求本身就是 dev/base）
        if step.TargetScope != requestScope {
            chain = append(chain, step.TargetScope)
        }
    }
    return chain
}
```

**Step 2：按降级链执行 Route 匹配**

```go
func (r *RouteResolver) MatchRoute(req IngressRequest) (*ResolvedRoute, error) {
    requestScope := r.ExtractScope(req)
    scopeChain   := r.BuildScopeChain(requestScope)

    // 按降级链逐级查找本地 LogicalService
    for _, scope := range scopeChain {
        route, ls, err := r.tryMatchInScope(req, scope)
        if err == nil {
            fallbackOccurred := scope != requestScope
            return &ResolvedRoute{
                Route:            route,
                LogicalService:   ls,
                MatchedScope:     scope,
                RequestScope:     requestScope,
                FallbackOccurred: fallbackOccurred,
            }, nil
        }
    }

    // 本地降级链全部 miss，尝试外部注册中心
    policy := r.policyRegistry.GetFallbackPolicy(requestScope.Namespace)
    if policy != nil && policy.External.Enabled {
        return r.tryExternalRegistry(req, requestScope)
    }

    return nil, ErrNoRouteMatch
}

func (r *RouteResolver) tryMatchInScope(req IngressRequest, scope Scope) (*Route, *LogicalService, error) {
    // 1. 按 host + path 找候选 Route
    candidates := r.index.FindByHostAndPath(req.Host, req.Path)

    // 2. 过滤 Header/Query 条件
    matched := filterByHeadersAndQueries(candidates, req)
    if len(matched) == 0 {
        return nil, nil, ErrNoRouteMatch
    }

    // 3. 优先级排序
    sortByPriority(matched)

    // 4. 按 scope 查找对应的 LogicalService
    for _, route := range matched {
        ls, err := r.registry.FindLogicalService(route.Target.Selector, scope)
        if err == nil && ls.Status == ACTIVE {
            return route, ls, nil
        }
    }
    return nil, nil, ErrNoRouteMatch
}
```

**Step 3：外部注册中心查询（本地全部 miss 后的统一兜底）**

```go
func (r *RouteResolver) tryExternalRegistry(req IngressRequest, requestScope Scope) (*ResolvedRoute, error) {
    // 外部注册中心查询使用原始 requestScope
    // 具体 provider、namespace、group 由管理员在 DiscoveryProvider 配置中指定
    result, err := r.discoveryManager.Query(req.ServiceName, requestScope)
    if err != nil || len(result.Endpoints) == 0 {
        return nil, ErrNoRouteMatch
    }
    return &ResolvedRoute{
        ExternalEndpoints: result.Endpoints,
        MatchedScope:      requestScope,
        RequestScope:      requestScope,
        IsExternalFallback: true,
    }, nil
}
```

**外部注册中心的位置说明：**
- 外部注册中心**不参与降级链本身**，只作为本地降级链全部 miss 后的统一兜底
- 查询时使用原始 `requestScope`，由管理员在 `DiscoveryProvider` 配置中指定具体的 provider、namespace、group 映射
- 外部注册中心查询成功后走 `direct proxy` 路径，不经过 ConnectorProxy

**matchHeaders 规则：**
- `exact`：Header 值完全相同（名称大小写不敏感，值大小写敏感）
- `prefix`：Header 值以指定字符串开头
- `regex`：RE2 语法，编译结果缓存，防 ReDoS
- `present=true`：Header 存在即通过；`present=false`：Header 不存在才通过
- `Route.Match.Headers` 为空时无 Header 约束，匹配所有请求

**Step 4：实例选择（LogicalService → ServiceInstance）**

```go
func (r *RouteResolver) ResolveConnectorTarget(ls *LogicalService) (ServiceInstance, error) {
    candidates := r.registry.GetEligibleInstances(ls.LogicalServiceID)
    if len(candidates) == 0 {
        return nil, ErrNoEligibleInstance
    }
    policy := r.getLoadBalancePolicy(ls)
    return r.instanceSelector.Select(candidates, policy)
}
```

### 5.7 InstanceSelector 接口

```go
type InstanceSelector interface {
    Select(ctx context.Context, candidates []ServiceInstance, policy LoadBalancePolicy) (ServiceInstance, error)
}

type LoadBalancePolicy string

const (
    PolicyRoundRobin LoadBalancePolicy = "round_robin"
    PolicyLeastConn  LoadBalancePolicy = "least_conn"
    PolicyRandom     LoadBalancePolicy = "random"
    PolicySticky     LoadBalancePolicy = "sticky"    // 基于 client IP hash，默认
    PolicyWeighted   LoadBalancePolicy = "weighted"  // 基于 instance.weight（预留）
)

type RoundRobinSelector struct{ counter atomic.Uint64 }
type LeastConnSelector  struct{ /* 跟踪每实例 active traffic 数 */ }
type RandomSelector     struct{}
type StickySelector     struct{ hashFunc func(TrafficMeta) uint64 }
```

**默认策略：** `round_robin`。

**sticky 扩展说明：** 默认基于 client IP hash。若需要基于 HTTP Cookie 或特定 Header 的粘性，需 Ingress 层在 `IngressRequest` 中携带粘性 key，由 Route policy 字段的 `sticky_by` 属性指定（`client_ip | header:X-Session-ID | cookie:session`）。本版预留字段，不强制实现。

### 5.8 Admission Pipeline：Route 冲突检测

`RouteAssign` 消息写入 Registry 前，必须执行冲突检测，防止不同服务在无法区分的条件下抢占同一入口。

**冲突的核心定义：** 对于任意一个可能的请求，两条 Route 都能匹配它，且现有规则（priority + 最长前缀 + header 条件）无法唯一确定选哪条，且两条 Route 指向不同的 LogicalService。

**冲突判定条件（以下全部满足时视为冲突）：**

| 字段 | 判定规则 | 说明 |
|------|---------|------|
| `host` | 完全相同，或通配符覆盖 | — |
| `path_prefix` | **完全相同**（不是包含关系，是逐字符相同） | path_prefix 不同时，最长前缀规则保证确定性，不构成冲突 |
| `headers` | 条件完全相同（均为空，或逐一相同） | headers 不同时，匹配条件可区分，不构成冲突 |
| `priority` | 完全相同 | priority 不同时，高优先级永远赢，不构成歧义冲突（但见下方 shadow warning） |
| `target` | 指向**不同的 LogicalService** | 同一 LogicalService 的多条路径不冲突（HA 场景） |

满足全部条件时，后注册的 Route 被拒绝，返回含冲突 `route_id` 的错误信息。

```go
func (h *RouteConflictAdmissionHandler) Handle(ctx context.Context, op AdmissionOp, resource any) (any, error) {
    if op != OpCreate && op != OpUpdate {
        return resource, nil
    }
    route := resource.(*Route)

    // 只查找 host + path_prefix 完全相同的 Route（不是包含关系）
    exactOverlapping := h.registry.FindExactPathOverlap(route.Match)
    for _, existing := range exactOverlapping {
        if existing.RouteID == route.RouteID {
            continue
        }
        if !headersConflict(existing.Match.Headers, route.Match.Headers) {
            continue  // headers 条件不同，可区分，不冲突
        }
        if existing.Priority != route.Priority {
            // priority 不同，不是冲突，但发出 shadow warning
            h.emitShadowWarning(route, existing)
            continue
        }
        existingTarget := h.registry.ResolveLogicalServiceID(existing.Target)
        newTarget      := h.registry.ResolveLogicalServiceID(route.Target)
        if existingTarget == newTarget {
            continue  // 同一 LogicalService，HA 场景，不冲突
        }
        return nil, fmt.Errorf(
            "route conflict: identical match conditions (host=%s path=%s headers=%v priority=%d) with existing route %s targeting different service %s",
            route.Match.Host, route.Match.PathPrefix, route.Match.Headers,
            route.Priority, existing.RouteID, existingTarget,
        )
    }
    return resource, nil
}
```

**Shadow Warning（Priority 遮蔽警告）：**

当两条 Route 的 host、path_prefix、headers 完全相同，但 priority 不同，且指向不同 LogicalService 时，不拒绝注册，但在 `RouteAssignAck` 的 `warnings` 字段中返回提示：

```proto
message RouteAssignAck {
  bool     accepted                 = 1;
  string   route_id                 = 2;
  uint64   accepted_resource_version = 3;
  uint64   current_resource_version  = 4;
  string   error_code               = 5;
  string   error_message            = 6;
  repeated string warnings          = 7;  // 新增：非致命警告
}
```

```
warning: "route rt_yyy has identical match conditions with higher priority route rt_xxx,
          this route may never be matched unless rt_xxx is removed or its priority is lowered"
```

**合法情况总结（不触发冲突检测）：**
- target 指向同一 LogicalService（HA 场景）
- path_prefix 不同（前缀重叠但不完全相同，由最长前缀规则处理）
- headers 条件不同（内容路由场景）
- priority 不同（发出 shadow warning，不拒绝）

---

## 6. Agent 侧变更

### 6.1 Service Catalog 变更

Agent 本地的 service catalog 增加 `instance_id` 管理：

```go
type ServiceCatalog struct {
    // service_name → ServiceEntry
    entries map[string]*ServiceEntry
}

type ServiceEntry struct {
    ServiceName string
    Scope       Scope
    InstanceID  string   // 从 PublishServiceAck 获得，持久化
    LogicalServiceID string
    Endpoints   []ServiceEndpoint
    HealthStatus string
    Labels      map[string]string
}
```

**持久化要求：** `instance_id` 至少在进程级别缓存，允许跨连接断线复用。如果 Agent 有本地配置文件，建议写入，支持进程重启后复用。

### 6.2 TrafficOpen 处理变更

`trafficAcceptor` 收到 `TrafficOpen` 后，增加 `instance_id` 校验环节：

```go
func (a *TrafficAcceptor) HandleTrafficOpen(open TrafficOpen) error {
    // 1. 校验 instance_id 属于本 connector
    entry := a.catalog.GetByInstanceID(open.InstanceID)
    if entry == nil {
        return a.rejectTraffic(open.TrafficID, "INSTANCE_NOT_FOUND", "instance not owned by this connector")
    }

    // 2. 校验 instance 状态（已主动 unpublish 的实例拒绝接流）
    if entry.Status != ACTIVE {
        return a.rejectTraffic(open.TrafficID, "INSTANCE_INACTIVE", "instance is not active")
    }

    // 3. 按 entry.Endpoints 选择本地 upstream（EndpointSelector 逻辑不变）
    endpoint, err := a.endpointSelector.Select(entry.Endpoints, open.EndpointSelectionHint)
    if err != nil {
        return a.rejectTraffic(open.TrafficID, "NO_ENDPOINT", err.Error())
    }

    // 4. 后续 dial + relay 逻辑不变
    return a.startRelay(open, endpoint)
}
```

### 6.3 健康上报变更

`health_reporter` 上报时携带 `instance_id`：

```go
func (r *HealthReporter) Report(entry ServiceEntry, status HealthStatus) {
    r.controlChannel.Send(ServiceHealthReport{
        InstanceID:        entry.InstanceID,
        LogicalServiceID:  entry.LogicalServiceID,
        ServiceHealthStatus: status.String(),
        EndpointStatuses:  status.EndpointStatuses,
        CheckTimeUnix:     time.Now().Unix(),
    })
}
```

---

## 7. ConnectorProxy 流程完整时序

多 Agent 场景下，从客户端请求到流量转发的完整路径（以场景三内容路由为例）：

```
Client（浏览器）
  GET /api/orders HTTP/1.1
  Host: api.example.com
  X-Env: alice
        │
        ▼
Bridge Ingress（L7 Shared）
  解析：host=api.example.com, path=/api/orders, headers={X-Env:alice}
        │
        ▼
Route Resolver: MatchRoute
  候选：[RouteA(path=/api/, header{X-Env=alice}),
         RouteB(path=/api/, header{X-Env=bob})]
  Header 过滤：RouteA 满足 X-Env=alice ✓，RouteB 不满足 ✗
  选中：RouteA → target: order-service(dev/alice)
        │
        ▼
Route Resolver: ResolveConnectorTarget
  LogicalService: ls_xxx [ACTIVE]
  instances: [si_001(agent-alice,HEALTHY), si_002(agent-alice2,HEALTHY), si_003(UNHEALTHY)]
        │
        ▼
InstanceSelector（RoundRobin）
  选中：si_001（connector_id = "agent-alice"）
        │
        ▼
Session Registry → Tunnel Pool → 分配 idle tunnel
        │
        ▼
TrafficOpen{
  traffic_id          = "tr_xxx",
  logical_service_id  = "ls_xxx",
  instance_id         = "si_001",
  source_addr         = "1.2.3.4:5678",
  protocol_hint       = "http"
}
        │（通过 idle tunnel 发送）
        ▼
Agent-Alice: TrafficAcceptor
  校验 instance_id=si_001 属于本 connector ✓
  查找对应 endpoints: [127.0.0.1:18080]
  Dial upstream
        │
        ▼
TrafficOpenAck{success=true}
        │
        ▼
双向 framed relay
        │
        ▼
TrafficClose → tunnel 关闭 → Agent 补池
```

---

## 8. 观测性扩展

### 8.1 新增 Bridge 指标

| 指标名 | 类型 | 说明 |
|--------|------|------|
| `bridge_logical_service_instance_count` | Gauge | 每个逻辑服务的实例总数，按 `logical_service_id`, `status` 分标签 |
| `bridge_logical_service_healthy_instance_count` | Gauge | 健康实例数 |
| `bridge_instance_selector_pick_total` | Counter | 实例选择次数，按 `instance_id`, `policy` 分标签 |
| `bridge_instance_not_found_total` | Counter | TrafficOpen 时 instance_id 校验失败次数 |
| `bridge_logical_service_status_transitions_total` | Counter | 逻辑服务状态变更次数（ACTIVE↔INACTIVE） |
| `bridge_route_match_total` | Counter | Route 匹配次数，按 `route_id`, `matched=true/false` 分标签 |
| `bridge_route_match_candidates_count` | Histogram | 每次请求的候选 Route 数量，用于评估索引效率 |
| `bridge_route_conflict_rejection_total` | Counter | Admission Pipeline 拒绝 Route 的次数 |
| `bridge_host_derive_total` | Counter | Host 自动派生次数，按 `success=true/false` 分标签 |
| `bridge_publish_instance_ownership_mismatch_total` | Counter | PublishService 时 instance_id 归属校验失败次数（R1），持续增长应告警 |
| `bridge_publish_instance_id_fallback_total` | Counter | PublishService 时 instance_id 提示不存在触发静默降级次数（R2） |
| `bridge_publish_stale_epoch_rejection_total` | Counter | PublishService 时 session_epoch 倒退被拒绝次数（R3） |
| `bridge_scope_fallback_total` | Counter | 作用域降级触发次数，按 `from_scope`, `to_scope` 分标签 |
| `bridge_scope_external_fallback_total` | Counter | 本地降级链全部 miss 后触发外部注册中心查询次数 |
| `bridge_scope_fallback_miss_total` | Counter | 降级链 + 外部兜底全部失败次数（最终 404/503） |

### 8.2 新增 Agent 指标

| 指标名 | 类型 | 说明 |
|--------|------|------|
| `agent_instance_publish_total` | Counter | 发布实例次数（按 service_name 分标签） |
| `agent_instance_id_reuse_total` | Counter | 重连时复用 instance_id 的次数 |
| `agent_traffic_open_instance_mismatch_total` | Counter | TrafficOpen 中 instance_id 不匹配次数 |

### 8.3 日志字段补充

在现有日志字段基础上，补充：

| 字段 | 说明 |
|------|------|
| `logical_service_id` | 替代/补充原 `service_id` |
| `instance_id` | 本次 traffic 选中的实例 |
| `instance_count` | route resolve 时的候选实例数 |
| `lb_policy` | 使用的负载均衡策略 |
| `route_id` | 本次请求匹配到的 Route ID |
| `route_match_header_count` | 匹配时生效的 Header 条件数量 |
| `derived_host` | 由 Bridge 自动派生的 host（如果是自动派生） |
| `request_scope` | 请求携带的原始 scope（namespace/environment） |
| `matched_scope` | 最终匹配到服务的 scope（降级后可能与 request_scope 不同） |
| `scope_fallback_steps` | 降级触发时经过的 scope 路径（如 `dev/alice → dev/base`） |
| `is_external_fallback` | 是否通过外部注册中心兜底路由 |

---

## 9. 重构实施策略（无兼容）

### 9.1 字段替换总览（最终态）

| 废弃字段 | 新字段 | 处理策略 |
|--------|--------|------|
| `service_key` | `service_name` + `scope` | 不兼容旧字段；收到旧字段直接拒绝 |
| `service_id` | `logical_service_id` | 语义升级为逻辑服务身份 |
| `connector_service.service_key` | `connector_service.selector` | Route 目标引用全面切换 |
| `TrafficOpen.service_id` | `TrafficOpen.logical_service_id` + `instance_id` | 数据面增加实例维度 |

### 9.2 重构原则

- 不提供双写、双读、灰度切换、版本协商等兼容路径
- 不保留旧协议 fallback 逻辑（包括 `service_key` 解析兼容）
- Bridge 与 Agent 必须同时切换到新协议
- 控制面、路由层、数据面、观测面一次性收敛到新字段语义

### 9.3 实施阶段（工程分解，不是兼容迁移）

**阶段 A：协议与数据模型重构**
- 更新 proto / pb / validate：移除旧字段，落地 `Scope`、`ServiceSelector`、`logical_service_id`、`instance_id`
- 控制面 ACK 与错误码对齐（包含 `UNSUPPORTED_LEGACY_PROTOCOL`）

**阶段 B：Bridge 核心重构**
- Registry 切换到 `LogicalService + ServiceInstance` 两层模型
- Route Resolver 切换到 `scope 解析 -> ScopeFallbackPolicy -> RouteMatch -> InstanceSelector`
- Admission Pipeline 落地冲突检测（`path_prefix` 完全相同才判冲突）

**阶段 C：Agent 与数据面重构**
- Agent catalog 增加 `instance_id` 生命周期管理与持久化
- TrafficOpen 按 `instance_id` 校验归属并选择本地 endpoint
- HealthReport/Unpublish 全量按实例维度上报

**阶段 D：联调与验收**
- 以本文档为唯一验收基线执行端到端回归
- 所有旧字段路径（发布、路由、开流、健康）必须不可达并返回明确错误

---

## 10. 边界情况与处理规则

### 10.1 同一 connector 重连，携带上次 instance_id（正常复用）

**场景：** Agent 断线重连后，携带上次 Ack 返回的 `instance_id` 重新发布同一服务。

**处理规则（对应 Case E）：**
- R1 归属校验通过（同一 connector_id）
- R3 epoch 单调性通过（重连后 epoch 更大）
- 复用该实例记录，更新 session 绑定和 endpoints，递增 resource_version
- Ack 返回相同的 instance_id，Agent 本地缓存不变

### 10.2 两个 connector 发布同名服务（多 Agent HA）

**场景：** `agent-alice` 和 `agent-bob` 都发布了 `(order-service, {dev, alice})`，各自未携带 instance_id 或携带属于自己的 instance_id。

**处理规则：**
- 同一 `(service_name, scope)` 对应同一个 `logical_service_id`
- 两个 connector 各自走 FIND_OR_CREATE，各自获得独立的 instance_id
- LogicalService 下挂两个 ServiceInstance，正常 HA 场景

### 10.3 Agent 携带不属于自己的 instance_id（归属冲突）

**场景：** `agent-bob` 发送 `PublishService{instance_id=si_001}`，但 `si_001` 归属于 `agent-alice`。可能来源：配置错误、本地缓存文件被错误复制、恶意伪造。

**处理规则（对应 Case C，R1 触发）：**
- server 校验 `instances[si_001].connector_id != agent-bob`，拒绝
- 返回 `PublishServiceAck{accepted=false, error_code="INSTANCE_OWNERSHIP_MISMATCH"}`
- `agent-alice` 的实例记录**完全不受影响**
- `agent-bob` 收到拒绝后，必须清空本地 instance_id 缓存，重新发送 `instance_id=""` 走首次发布流程
- 记录 `bridge_publish_instance_ownership_mismatch_total`，持续增长应触发告警（可能存在配置错误或攻击）

### 10.4 instance_id 提示存在但记录已被清理（静默降级）

**场景：** Agent 进程重启，本地缓存保留了旧的 instance_id，但 server 侧因 STALE 清理等原因已删除该记录。

**处理规则（对应 Case B，R2 触发）：**
- server 查找 instance_id 不存在，不报错
- 静默降级走 FIND_OR_CREATE 流程
- 若 `(connector_id, logical_service_id)` 下已有实例（旧记录未清理完），直接复用并更新
- 若无任何记录，分配新的 instance_id
- Ack 返回最终 instance_id，可能与 Agent 提示的不同
- Agent 必须以 Ack 为准更新本地缓存
- 记录 `bridge_publish_instance_id_fallback_total`

### 10.5 同一 Agent 进程的两个实例同时在线（split-brain）

**场景：** `agent-alice` 进程 A（旧，epoch=5）尚未退出，进程 B（新，epoch=6）已启动并重连，两者 connector_id 相同，都携带了 `si_001`。

**处理规则：**
- 进程 B 先连上：R1 通过（同 connector），R3 通过（epoch=6 ≥ 5），复用 si_001，session 绑定更新为进程 B 的 session
- 进程 A 后续发消息或重连：R3 触发（epoch=5 < 6），被拒绝，返回 `STALE_SESSION_EPOCH`
- 进程 A 的后续 heartbeat 失效，session 进入 STALE，不影响进程 B 持有的实例
- 依赖现有 `session_epoch` 单调递增保证，无需额外机制

### 10.6 TrafficOpen 中 instance_id 不属于接收 Agent

**场景：** Bridge 误发，或实例已被另一 connector 接管（极少数竞态）。

**处理规则：**
- Agent 返回 `TrafficOpenAck{success=false, error_code="INSTANCE_NOT_FOUND"}`
- Bridge 不进行 fallback（非 hybrid_group 场景），直接返回错误给 client
- Bridge 将该 tunnel 标记为 broken，从池中移除
- 记录 `bridge_instance_not_found_total` 指标并告警

### 10.7 LogicalService 下全部实例同时不健康

**场景：** 所有 Agent 的健康检查同时失败（如依赖的数据库挂了）。

**处理规则：**
- `LogicalService.healthy_instance_count = 0`，但 `active_instance_count > 0`
- 默认策略：**降级可用**（允许路由到 ACTIVE 但 UNHEALTHY 的实例，让 upstream 返回实际错误）
- 可通过 `RoutePolicy.unhealthy_instance_policy` 配置为 **硬拒绝**（`allow_degraded` | `reject`）

### 10.8 多 Agent 发布，endpoints 不同

**场景：** `agent-alice` 发布 `endpoints: [127.0.0.1:18080]`，`agent-bob` 发布 `endpoints: [192.168.1.5:8080]`，同为 `order-service`。

**处理规则：**
- endpoints 属于 ServiceInstance，不属于 LogicalService
- `TrafficOpen` 指定了 `instance_id`，收到 TrafficOpen 的 Agent 只使用自己实例的 endpoints
- Bridge 不感知、不持有 Agent 的本地 endpoint 地址
- 此为预期行为，文档明确说明

---

## 11. 需改动的模块范围

| 模块 | 改动类型 | 说明 |
|------|----------|------|
| `server/service/` | **重大** | 引入 LogicalService + ServiceInstance 两层模型，重写 PublishService 处理逻辑 |
| `server/registry/canonical/` | **重大** | 新增 logical_service、instance 的存储与索引 |
| `server/route/` | **重大** | RouteTarget 引用方式从 service_key 改为 ServiceSelector；RouteMatch 增加 headers/queries 字段 |
| `server/route/resolver/` | **重大** | 路由匹配逻辑扩展为 scope 解析 + 降级链 + 多条件过滤 + 优先级排序 |
| `server/scope/` | **新增** | ScopeFallbackPolicy 资源模型、存储、管理接口；scope chain 构建逻辑 |
| `server/admission/` | **新增** | Admission Pipeline 框架及 RouteConflictAdmissionHandler |
| `server/selector/` | **新增** | InstanceSelector 接口及多种实现 |
| `server/ingress/host_deriver/` | **新增** | Host 自动派生逻辑，含前置冲突检查 |
| `server/control/` | **中等** | PublishService/Unpublish/HealthReport handler 适配新字段 |
| `server/proxy/connector_proxy/` | **中等** | 选实例逻辑，TrafficOpen 携带 instance_id 和透传 scope |
| `server/proxy/direct_proxy/` | **小** | 外部注册中心兜底路径，透传 scope Header |
| `server/ingress/` | **小** | IngressRequest 结构增加 Headers/QueryParams 以及 scope 解析入口 |
| `server/discovery/` | **小** | DiscoveryProvider 查询接口增加 scope 参数，支持外部兜底查询 |
| `agent/service/catalog.go` | **中等** | 增加 instance_id 管理 |
| `agent/control/publisher.go` | **中等** | 发送新版 PublishService 格式 |
| `agent/traffic/acceptor.go` | **小** | 增加 instance_id 校验；透传 scope Header 给本地 upstream |
| `agent/control/health_reporter.go` | **小** | 上报时携带 instance_id |
| `proto/` | **中等** | 新增/修改 proto 字段；新增 HeaderMatcher、QueryMatcher、RouteMatch 扩展；新增 ScopeFallbackPolicy |
| `server/metrics/` | **小** | 新增指标 |

---

## 12. 评审结论（已冻结）

以下决策已冻结，作为本次重构（v1.5 基线）的强约束，开发按此执行，不再引入兼容分支。

### 12.1 开工阻塞项结论（已确认）

**决策一：instance_id 持久化范围**  
本版仅要求**进程内持久化**。Agent 断线重连可复用内存中的 `instance_id`；跨进程重启不要求复用本地文件缓存。重启后如无可用 `instance_id`，按 `instance_id=""` 走 FIND_OR_CREATE。

**决策二：全部实例不健康时的默认行为**  
默认策略固定为 `allow_degraded`。若业务需要硬拒绝，使用 `RoutePolicy.unhealthy_instance_policy=reject` 覆盖。

**决策三：Header 匹配大小写规则**  
Header 名称大小写不敏感，Header 值大小写敏感。本版不提供 `case_insensitive` 匹配选项。

**决策四：Admission Pipeline 模式**  
本版采用**同步拦截**。Route 注册冲突在写入前即时拒绝；异步校验模式不在本版范围内。

**决策五：scope Header 缺失默认行为**  
当请求缺失 `X-Bridge-Namespace` / `X-Bridge-Environment` 时，使用 `default_scope`；本版不改为默认 400。

**决策六：ScopeFallbackPolicy 触发条件**  
本版降级链保持“本级 miss 即降级”的无条件触发语义，不引入 `trigger` 条件表达式。

### 12.2 范围边界（本版固定，不阻塞开工）

**边界一：sticky 策略粒度**  
P0/P1 仅要求 `client_ip` 粘性能力；基于 cookie/header 的 `sticky_by` 作为 P2 增强项。

**边界二：label selector 优先级**  
`match_labels` 本版预留，不作为首批上线阻塞项；按 P2 能力项推进。

**边界三：跨 scope 引用**  
本版仍不允许跨 scope 引用，保持同 scope 路由约束不变。

**边界四：host 自动派生模板**  
本版使用默认模板 `{service_name}.{environment}.{namespace}.{base_domain}`；模板扩展能力按 P2 任务推进。
