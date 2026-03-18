# 

**文档状态**：Draft for Review  
**版本**：v1.1  
**依赖文档**：LTFP-v1-Draft.md (v2.1)、LTFP-TransportAbstraction.md (v2.1)、Agent_and_Bridge_Implementation_Technical_Design.md

---

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
6. 保持对现有单 Agent 场景的完全兼容（零感知升级路径）
7. 为后续跨 scope 引用、灰度发布、流量权重分配预留扩展空间

### 1.3 非目标

- 不实现完整 RBAC 与租户系统
- 不实现跨 scope 引用授权（预留扩展点，本版不落地）
- 不实现 mid-stream 级别的实例切换（failover 仍限于 pre-open 阶段）
- 不改变 Transport 层和 Binding 层的任何接口

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

这两个字段是**配置时字段**，不是**请求时字段**。浏览器等客户端发起请求时不携带、也不需要携带这两个字段。

```
配置阶段（Agent 注册时）：
  namespace + environment + service_name
      → 确定唯一性边界
      → 派生对外访问 host（可选）
      → 映射到第三方 Discovery 命名空间

运行时（请求路由时）：
  host + path + headers
      → Route Resolver 匹配
      → 找到绑定的 LogicalService
      → 路由到 ServiceInstance
```

**namespace/environment 的三个作用：**

| 作用 | 说明 |
|------|------|
| 唯一性边界 | 同名服务在不同 scope 下互相独立，不冲突 |
| Host 派生 | 按约定规则自动生成对外访问域名，编码进 DNS 而非请求头 |
| Discovery 映射 | 向 Nacos/Consul 导出时映射到第三方系统对应概念 |

**Host 派生规则（Bridge 侧可配置模板）：**

```
模板（默认）：{service_name}.{environment}.{namespace}.{base_domain}

示例：
  service_name = "order-service"
  scope = {namespace: "dev", environment: "alice"}
  base_domain = "example.com"

  →  host = "order-service.alice.dev.example.com"
```

Agent 发布时 `exposure.host` 留空，Bridge 自动派生并注册到 ingress。Agent 也可以显式指定 `exposure.host` 来覆盖派生规则，此时 scope 仅用于内部隔离，不影响外部域名。

**namespace/environment 不做的事：**
- 不出现在 HTTP 请求头里
- 不作为 runtime 路由的匹配字段
- 不需要客户端感知

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

处理：Admission Pipeline 在注册阶段检测冲突，拒绝后注册的 Route，返回明确错误。详见第 5.7 节。

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

| 场景 | host+path 唯一性 | 解决机制 |
|------|-----------------|----------|
| 多 Agent HA | 唯一（同一 LogicalService） | InstanceSelector |
| 不同服务抢占同一入口 | 冲突 | Admission 拦截，注册时报错 |
| 按请求内容路由到不同服务 | 不唯一，有意为之 | RouteMatch HeaderMatcher |
| 路由前缀重叠 | 不唯一，最长匹配 | Route priority + path 长度 |

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

**兼容说明：** 如果收到仍携带旧版 `service_key` 的消息，server 侧做解析兼容：

```go
// server 侧兼容逻辑（过渡期）
if msg.ServiceKey != "" && msg.ServiceName == "" {
    parts := strings.SplitN(msg.ServiceKey, "/", 3)
    if len(parts) == 3 {
        msg.Scope = &Scope{Namespace: parts[0], Environment: parts[1]}
        msg.ServiceName = parts[2]
    }
}
```

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

### 5.2 PublishService 处理流程

```
收到 PublishService（service_name="order-service", scope={dev,alice}, connector_id="agent-bob"）

Step 1: 查找或创建 LogicalService
  key = (service_name, scope.namespace, scope.environment)
  if service_index[key] exists:
      logical_service_id = service_index[key]
      logicalService = logical_services[logical_service_id]
  else:
      logical_service_id = newUUID()
      logicalService = LogicalService{...}
      service_index[key] = logical_service_id
      logical_services[logical_service_id] = logicalService

Step 2: 查找或创建 ServiceInstance
  instance_key = (connector_id, logical_service_id)
  if msg.instance_id != "" && instances[msg.instance_id] exists:
      // 重连复用
      instance = instances[msg.instance_id]
      instance.endpoints = msg.endpoints
      instance.session_id = current_session_id
      instance.session_epoch = current_session_epoch
      instance.instance_status = ACTIVE
      instance.resource_version++
  elif instance_index[instance_key] exists:
      // 同 connector 已有实例，更新
      instance_id = instance_index[instance_key]
      instance = instances[instance_id]
      // 更新字段...
  else:
      // 首次发布，创建新实例
      instance_id = newUUID()
      instance = ServiceInstance{...}
      instance_index[instance_key] = instance_id
      instances[instance_id] = instance

Step 3: 重新计算 LogicalService 派生状态
  recalculateLogicalServiceStatus(logical_service_id)

Step 4: 返回 PublishServiceAck
  ack.logical_service_id = logical_service_id
  ack.instance_id = instance.instance_id
  ack.accepted = true
  ack.accepted_resource_version = instance.resource_version
```

### 5.3 Session 断线处理

当 session 进入 STALE 时，仅影响该 connector 下的实例，不影响其他 connector 的实例：

```
session → STALE

for each instance owned by connector_id:
    instance.instance_status = STALE
    instance.health_status = UNKNOWN

recalculateLogicalServiceStatus(logical_service_id)
// 其他 connector 的实例仍然 ACTIVE，LogicalService 可能仍然 ACTIVE
```

### 5.4 LogicalService 状态派生（ServiceController）

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

### 5.5 Route Resolver 变更

路由分为两个独立步骤：**Route 匹配**（找到规则）和**实例选择**（找到 Agent）。

**Step 1：Route 匹配（多条件组合 + 优先级排序）**

```go
func (r *RouteResolver) MatchRoute(req IngressRequest) (*Route, error) {
    // 1. 候选集合：host 匹配 + path_prefix 匹配
    candidates := r.index.FindByHostAndPath(req.Host, req.Path)

    // 2. 精确过滤：Header/Query 条件全部满足（AND 关系）
    matched := make([]*Route, 0)
    for _, route := range candidates {
        if r.matchHeaders(route.Match.Headers, req.Headers) &&
           r.matchQueries(route.Match.Queries, req.QueryParams) {
            matched = append(matched, route)
        }
    }
    if len(matched) == 0 {
        return nil, ErrNoRouteMatch
    }

    // 3. 排序：priority 降序；priority 相同时 path_prefix 长度降序（最长前缀优先）
    sort.Slice(matched, func(i, j int) bool {
        if matched[i].Priority != matched[j].Priority {
            return matched[i].Priority > matched[j].Priority
        }
        return len(matched[i].Match.PathPrefix) > len(matched[j].Match.PathPrefix)
    })

    return matched[0], nil
}
```

`matchHeaders` 规则：
- `exact`：Header 值完全相同（名称大小写不敏感，值大小写敏感）
- `prefix`：Header 值以指定字符串开头
- `regex`：RE2 语法，编译结果缓存，防 ReDoS
- `present=true`：Header 存在即通过；`present=false`：Header 不存在才通过
- `Route.Match.Headers` 为空时无 Header 约束，匹配所有请求

**Step 2：实例选择（LogicalService → ServiceInstance）**

```go
func (r *RouteResolver) ResolveConnectorTarget(selector ServiceSelector) (ServiceInstance, error) {
    ls, err := r.findLogicalService(selector)
    if err != nil || ls.Status != ACTIVE {
        return nil, ErrServiceUnavailable
    }
    candidates := r.registry.GetEligibleInstances(ls.LogicalServiceID)
    if len(candidates) == 0 {
        return nil, ErrNoEligibleInstance
    }
    policy := r.getLoadBalancePolicy(selector)
    return r.instanceSelector.Select(candidates, policy)
}
```

### 5.6 InstanceSelector 接口

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

### 5.7 Admission Pipeline：Route 冲突检测

`RouteAssign` 消息写入 Registry 前，必须执行冲突检测，防止不同服务抢占同一入口（场景二）。

**冲突判定条件（以下全部满足时视为冲突）：**
- `protocol` 相同，或其中一条未指定 protocol
- `host` 完全相同，或其中一条通配符覆盖另一条
- `path_prefix` 存在包含关系（A 是 B 的前缀，或完全相同）
- `headers` 条件**完全相同**（均为空，或逐一相同）
- `target` 指向**不同的 LogicalService**

满足全部条件时，后注册的 Route 被拒绝，返回含冲突 route_id 的错误信息。

```go
type RouteConflictAdmissionHandler struct {
    registry RouteRegistry
}

func (h *RouteConflictAdmissionHandler) Handle(ctx context.Context, op AdmissionOp, resource any) (any, error) {
    if op != OpCreate && op != OpUpdate {
        return resource, nil
    }
    route := resource.(*Route)

    overlapping := h.registry.FindOverlapping(route.Match)
    for _, existing := range overlapping {
        if existing.RouteID == route.RouteID {
            continue
        }
        if headersConflict(existing.Match.Headers, route.Match.Headers) {
            existingTarget := h.registry.ResolveLogicalServiceID(existing.Target)
            newTarget      := h.registry.ResolveLogicalServiceID(route.Target)
            if existingTarget != newTarget {
                return nil, fmt.Errorf(
                    "route conflict: overlaps with existing route %s targeting different service %s",
                    existing.RouteID, existingTarget,
                )
            }
        }
    }
    return resource, nil
}
```

**不触发冲突检测的合法情况：**
- 同一入口指向同一 LogicalService（多 Agent HA，场景一）
- path_prefix 重叠但 Header 条件不同（场景三）
- path_prefix 完全不同（场景四）

**Host 自动派生时的前置检查：** Bridge 执行 host 派生时，若生成的 host 与已有 Route 的 host 相同且 path_prefix 重叠，直接在派生阶段报错，不进入 Admission 流程。

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

---

## 9. 数据模型迁移与兼容策略

### 9.1 字段兼容映射

| 旧字段 | 新字段 | 说明 |
|--------|--------|------|
| `service_key` | `service_name` + `scope` | server 侧自动解析旧格式 |
| `service_id` | `logical_service_id` | 语义提升：从实例 ID 升级为逻辑服务 ID |
| `connector_service.service_key` | `connector_service.selector` | Route 引用方式变更 |
| `TrafficOpen.service_id` | `TrafficOpen.logical_service_id` + `instance_id` | 新增 instance 维度 |

### 9.2 迁移阶段

**阶段 0（当前）：** 单 Agent，service_key 模式，保持不变。

**阶段一：server 侧双写（无 Agent 感知）**
- server 解析 `service_key` 时同时写入 `service_name + scope`
- 内部索引从 `service_key` 切换为 `(name, scope)` 二元组
- 对外 API 继续兼容 `service_key`
- LogicalService + ServiceInstance 两层结构落地，但 LogicalService 下只有 1 个实例
- `TrafficOpen` 暂不发送 `instance_id`（Agent 侧不做校验）

**阶段二：新版 Proto 发布（Agent 侧感知）**
- Agent 升级到新版：`PublishService` 发送 `service_name + scope`，接收 `instance_id`
- `TrafficOpen` 开始携带 `instance_id`，Agent 开始做实例校验
- 旧版 Agent 通过 server 侧兼容逻辑继续工作
- 此时多 Agent 发布同一服务的能力已完整

**阶段三：废弃 `service_key`（清理）**
- 明确 deprecation 期限
- 移除 server 侧 `service_key` 解析兼容代码
- 彻底清理 `service_key` 字段

### 9.3 Agent 版本协商

通过现有的 `ConnectorHello.capabilities` 字段声明版本能力：

```
旧版 Agent：capabilities 不含 "instance_aware"
  → server 不发送 instance_id 字段（TrafficOpen 保持旧格式）
  → server 内部仍维护实例，但 Agent 侧无感知

新版 Agent：capabilities 含 "instance_aware"
  → server 发送完整的 instance_id
  → Agent 执行实例校验逻辑
```

---

## 10. 边界情况与处理规则

### 10.1 同一 connector 重复发布同一服务

**场景：** Agent 重连后再次发送相同 `(service_name, scope)` 的 `PublishService`，且携带了上次的 `instance_id`。

**处理规则：**
- 校验 `instance_id` 确实归属于该 `connector_id`（防止伪造）
- 校验 `session_epoch` 不小于当前记录（防止旧 session 覆盖新状态）
- 满足条件则复用该 instance，更新 endpoints 和 session 信息，递增 `resource_version`

### 10.2 两个 connector 发布同名服务

**场景：** `agent-alice` 和 `agent-bob` 都发布了 `(order-service, {dev, alice})`。

**处理规则：**
- 同一 `(service_name, scope)` 对应同一个 `logical_service_id`（唯一）
- 两个 connector 各自获得独立的 `instance_id`
- LogicalService 下有两个 ServiceInstance
- 这是多 Agent HA 的标准场景，正常处理

### 10.3 TrafficOpen 中 instance_id 不属于接收 Agent

**场景：** Bridge 误发，或 instance 已被另一 connector 接管。

**处理规则：**
- Agent 返回 `TrafficOpenAck{success=false, error_code="INSTANCE_NOT_FOUND"}`
- Bridge 不进行 fallback（非 hybrid_group 场景），直接返回错误给 client
- Bridge 将该 tunnel 标记为 broken，从池中移除
- 记录 `bridge_instance_not_found_total` 指标并告警

### 10.4 LogicalService 下全部实例同时不健康

**场景：** 所有 Agent 的健康检查同时失败（如依赖的数据库挂了）。

**处理规则：**
- `LogicalService.healthy_instance_count = 0`，但 `active_instance_count > 0`
- 默认策略：**降级可用**（允许路由到 ACTIVE 但 UNHEALTHY 的实例，让 upstream 返回实际错误）
- 可通过 Route policy 字段配置为 **硬拒绝**（所有实例不健康时直接返回 503）
- 此行为通过 `RoutePolicy.unhealthy_instance_policy` 控制（`allow_degraded` | `reject`）

### 10.5 多 Agent 发布，endpoints 不同

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
| `server/route/resolver/` | **重大** | 路由匹配逻辑扩展为多条件组合 + 优先级排序 |
| `server/admission/` | **新增** | Admission Pipeline 框架及 RouteConflictAdmissionHandler |
| `server/selector/` | **新增** | InstanceSelector 接口及多种实现 |
| `server/ingress/host_deriver/` | **新增** | Host 自动派生逻辑，含前置冲突检查 |
| `server/control/` | **中等** | PublishService/Unpublish/HealthReport handler 适配新字段 |
| `server/proxy/connector_proxy/` | **中等** | 选实例逻辑，TrafficOpen 携带 instance_id |
| `server/ingress/` | **小** | IngressRequest 结构增加 Headers/QueryParams 字段供 Route Resolver 使用 |
| `server/discovery/` | **无** | 不变 |
| `agent/service/catalog.go` | **中等** | 增加 instance_id 管理 |
| `agent/control/publisher.go` | **中等** | 发送新版 PublishService 格式 |
| `agent/traffic/acceptor.go` | **小** | 增加 instance_id 校验 |
| `agent/control/health_reporter.go` | **小** | 上报时携带 instance_id |
| `proto/` | **中等** | 新增/修改 proto 字段；新增 HeaderMatcher、QueryMatcher、RouteMatch 扩展 |
| `server/metrics/` | **小** | 新增指标 |

---

## 12. 评审关注点

以下是本方案的主要设计决策点，建议评审重点讨论：

**决策点一：instance_id 的持久化范围**  
当前方案要求 instance_id 进程内持久化，重连时带回。是否需要写入本地文件（跨进程重启复用）？这会影响 Agent 状态管理的复杂度。

**决策点二：LogicalService 的全部实例不健康时的默认行为**  
方案中默认为"降级可用"（allow_degraded）。是否应该反过来，默认为"硬拒绝"，降级作为可选配置？

**决策点三：InstanceSelector 的 sticky 策略粒度**  
Sticky 路由基于 client IP hash，是否足够？某些场景可能需要基于 HTTP Cookie 或 Header 的粘性，这会要求 Ingress 层提前解析 L7 信息并传递给 Route Resolver。

**决策点四：label selector 的实现优先级**  
ServiceSelector 中的 `match_labels` 在本版标注为"预留，不强制实现"。是否有立即需要该能力的场景，需要提前落地？

**决策点五：跨 scope 引用的时机**  
当前方案仍然不允许跨 scope 引用（与现有文档保持一致），仅为此预留了 ServiceSelector 扩展点。如果有明确的跨 scope 需求，需在本版同时设计授权模型。

**决策点六：Header 匹配的大小写敏感性**  
当前方案中 Header 名称大小写不敏感，Header 值大小写敏感。是否需要提供 `case_insensitive` 选项（特别是 `exact` 和 `prefix` 匹配），以支持某些业务系统传递大小写不一致的 Header 值的场景？

**决策点七：host 自动派生的模板是否支持自定义**  
默认模板为 `{service_name}.{environment}.{namespace}.{base_domain}`。是否需要支持 namespace/environment 级别的自定义模板覆盖，以适配不同团队的命名规范？如果支持，模板配置的存储和同步需要额外设计。

**决策点八：Admission Pipeline 的同步/异步模式**  
当前设计是同步拦截（注册时立即返回冲突错误）。对于大批量 Route 注册场景（如 Operator 批量同步 K8s Ingress），是否需要支持异步校验模式（先接受，后校验，结果通过 RouteStatusReport 回调）？
