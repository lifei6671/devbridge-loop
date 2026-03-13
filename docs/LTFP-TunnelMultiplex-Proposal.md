# LTFP 单 Tunnel 多路复用技术方案

**文档状态**：Draft  
**版本**：v1.0  
**基于**：LTFP-TransportAbstraction v2.1 / LTFP-v1-Draft v2.1  
**变更范围**：Transport Abstraction Layer · Runtime Layer · grpc_h2 Binding · Protobuf 协议

---

## 目录

1. [背景与动机](#1-背景与动机)
2. [核心设计决策](#2-核心设计决策)
3. [协议层变更](#3-协议层变更)
4. [Transport 层接口变更](#4-transport-层接口变更)
5. [Runtime 层变更](#5-runtime-层变更)
6. [grpc_h2 Binding 特殊处理](#6-grpc_h2-binding-特殊处理)
7. [约束与边界条件](#7-约束与边界条件)
8. [Protobuf 新增定义](#8-protobuf-新增定义)
9. [Go 接口与骨架代码](#9-go-接口与骨架代码)
10. [需修订的文档章节](#10-需修订的文档章节)
11. [实施顺序建议](#11-实施顺序建议)
12. [规范性结论](#12-规范性结论)

---

## 1. 背景与动机

### 1.1 现状

LTFP 当前版本（TransportAbstraction v2.1）在 §3.2.4 中规定了"单 Tunnel 单 Traffic"强约束：

> tunnel 一旦被某次 traffic 占用，就不得再服务第二次 traffic；traffic 结束后 tunnel 必须关闭，由 Agent 负责补充新的空闲 tunnel。

在 `grpc_h2` binding 下，这意味着每次 traffic 对应一条完整的 gRPC bidi stream 生命周期，traffic 结束后 stream 关闭，Agent 必须重新建立新 stream 并放入 idle pool。

### 1.2 问题

上述设计在高频短连接场景下存在明显开销：

- 每次 traffic 结束都触发 gRPC stream teardown，涉及 HTTP/2 RST_STREAM 或 END_STREAM 帧交换
- Agent 侧需要持续补充 tunnel，即使请求量平稳，tunnel 建立速率仍与 traffic QPS 线性相关
- gRPC stream 创建本身不是零成本，涉及 HTTP/2 HEADERS 帧、流量控制窗口初始化等操作
- idle pool 水位抖动较大，在突发流量下容易出现 pool 空的瞬态

### 1.3 目标

将 tunnel 生命周期从"一次性"改为"可串行复用"：

```
旧：idle → in-use → closed（不可回收）
新：idle → in-use → [recycle 握手] → idle（回收入池）
```

同一条 tunnel 在一次 traffic 正常结束后，经过明确的回收握手，可重新进入 idle pool，承载下一次 traffic。

### 1.4 非目标

本方案**不实现**以下内容：

- **并发多路复用**：同一条 tunnel 上通过 `traffic_id` 并发承载多条流量。这被 LTFP-v1 §3.2 明确排除，本方案不改变该约束。
- **Session Resume**：仍属于非目标，与本方案正交。
- **Mid-stream Failover**：仍属于非目标，与本方案正交。
- **跨 tunnel 的 traffic 迁移**：traffic 一旦绑定 tunnel，不允许中途切换。

---

## 2. 核心设计决策

### 2.1 串行复用 vs 并发复用

本方案采用**串行复用（Sequential Reuse）**：同一时刻，一条 tunnel 仍然只承载一次 traffic，但 traffic 结束后 tunnel 不关闭，而是经回收握手后重新进入 idle pool。

并发复用（在单 tunnel 内通过 `traffic_id` 拆分帧）被排除，原因如下：

- LTFP-v1 §3.2 明确硬约束：`禁止在单个大 stream 里通过 traffic_id 手工复用多条流量`
- 并发复用会引入 Head-of-Line 阻塞问题，与采用多 stream 的初衷相悖
- 并发复用使帧边界管理、错误隔离、debug 复杂度显著上升
- gRPC/HTTP2 本身在 L4 已有多路复用，再在应用层叠加收益有限

### 2.2 回收握手是必须的

tunnel 不能在 traffic 结束后直接无条件放回 idle pool。必须经过显式的**回收握手（Recycle Handshake）**，原因如下：

- `TrafficClose` 只表示应用层"业务关闭意图"，不保证 tunnel 底层缓冲区已排空
- 需要防止上一次 traffic 的残留 bytes 污染下一次 traffic 的读取
- 需要双方就 tunnel 可复用状态达成一致，防止单方认为可复用而另一方已关闭
- 需要通过单调递增的 `recycle_seq` 防止断线重连后旧消息污染新状态

### 2.3 回收失败降级为关闭

回收握手失败（超时、`TrafficReset` 结束、底层 stream 异常）时，tunnel 必须直接关闭，不得强行回收。Server 将触发 Agent 补充新 tunnel 的标准流程。这保证了复用逻辑的引入不会降低协议的健壮性。

### 2.4 最大复用次数上限

每条 tunnel 必须有 `max_reuse_count` 上限。超过上限的 tunnel 在当次 traffic 结束后直接关闭，不再回收。此上限用于：

- 防止单条 tunnel 因长期使用积累状态（如 TCP 窗口收缩、流量控制窗口碎片化）
- 提供自然的 tunnel 轮换机制，分摊连接建立开销
- 配合监控指标提供可观测的生命周期数据

---

## 3. 协议层变更

### 3.1 Tunnel 生命周期状态机扩展

**旧状态机（单次使用）：**

```
created → idle → in-use → closed
```

**新状态机（串行复用）：**

```
created
  └─→ idle
        └─→ in-use
              ├─→ recycling          （traffic 正常关闭，发起回收握手）
              │     ├─→ idle         （回收成功，重新入池）
              │     └─→ closed       （回收失败或达到最大复用次数）
              └─→ closed             （TrafficReset / 底层错误，直接关闭）
```

状态说明：

| 状态 | 说明 |
|---|---|
| `created` | tunnel 刚建立，尚未进入 idle pool |
| `idle` | 在 idle pool 中等待分配 |
| `in-use` | 已被某次 traffic 占用 |
| `recycling` | traffic 已结束，正在执行回收握手 |
| `closed` | tunnel 关闭，底层 stream 已释放 |

### 3.2 Traffic 结束后的回收流程

**正常结束路径（TrafficClose → Recycle）：**

```
Server                              Agent
  │                                   │
  │──── TrafficClose ────────────────→│
  │←─── TrafficCloseAck ─────────────│  （现有协议已有）
  │                                   │
  │── TunnelRecycle ─────────────────→│  （新增）
  │←── TunnelRecycleAck ─────────────│  （新增）
  │                                   │
  │  [tunnel 重新进入 idle pool]       │
```

**异常结束路径（TrafficReset → 直接关闭）：**

```
Server                              Agent
  │                                   │
  │──── TrafficReset ────────────────→│
  │                                   │
  │  [tunnel 直接 Close，不进入 recycling]
  │  [Agent 收到通知后关闭本端 stream] │
```

**达到最大复用次数路径：**

```
Server                              Agent
  │                                   │
  │──── TrafficClose ────────────────→│
  │←─── TrafficCloseAck ─────────────│
  │                                   │
  │── TunnelRecycle (is_final=true) ─→│  （新增字段，表示本次回收后关闭）
  │←── TunnelRecycleAck ─────────────│
  │                                   │
  │  [tunnel 关闭，Agent 补充新 tunnel]│
```

### 3.3 回收握手语义约束

- `TunnelRecycle` 必须在 `TrafficClose` + `TrafficCloseAck` 完成后才能发送
- `TunnelRecycle` 只能由 **Server** 发起（因为 Server 拥有 TunnelPool，是决策方）
- Agent 收到 `TunnelRecycle` 后，必须验证 `recycle_seq` 严格递增，拒绝乱序消息
- `TunnelRecycleAck.accepted=false` 时，双方均必须关闭 tunnel，不得重试回收
- 回收握手超时（建议默认 3s）后，Server 必须将 tunnel 标记为 broken 并关闭

---

## 4. Transport 层接口变更

### 4.1 Tunnel 接口扩展

在现有 `Tunnel` 接口基础上新增以下方法：

```go
package transport

type Tunnel interface {
    // 现有接口（不变）
    ID() string
    Read(p []byte) (n int, err error)
    Write(p []byte) (n int, err error)
    Close() error
    Reset(err error) error
    SetDeadline(t time.Time) error
    SetReadDeadline(t time.Time) error
    SetWriteDeadline(t time.Time) error
    Done() <-chan struct{}
    Err() error

    // 新增：将 tunnel 底层缓冲区排空，为下次复用做准备
    // 若底层 stream 存在未读字节，必须返回 error
    // 对于 grpc_h2 binding，通常为 no-op（buffer 在 runtime 层控制）
    // 实现必须是幂等的
    Flush() error

    // 新增：当前 tunnel 已完成的 traffic 轮次数（0-based）
    // 第一次 traffic 占用前为 0，每次成功回收后递增
    ReuseCount() int

    // 新增：该 tunnel 是否满足回收前提条件
    // 实现需检查：无 pending bytes、无 deadline 触发历史、底层 stream 健康
    Recyclable() bool
}
```

### 4.2 TunnelPool 接口扩展

```go
package transport

type TunnelPool interface {
    // 现有接口（不变）
    PutIdle(t Tunnel) error
    Acquire(ctx context.Context) (Tunnel, error)
    Remove(id string) error
    IdleCount() int
    InUseCount() int

    // 新增：将使用完毕的 tunnel 归还到 idle pool
    // 实现必须在内部校验 Recyclable() 前提
    // 若 Recyclable() 为 false，实现应自动降级为 Remove()，调用方无需感知
    // 返回 RecycleResult 说明实际执行结果（recycled 或 closed）
    Recycle(ctx context.Context, t Tunnel) (RecycleResult, error)

    // 新增：监控指标
    RecycledCount() int   // 历史累计回收成功次数
    ClosedCount() int     // 历史累计关闭次数（含回收失败降级）
}

type RecycleResult string

const (
    RecycleResultRecycled RecycleResult = "recycled" // 成功回收入池
    RecycleResultClosed   RecycleResult = "closed"   // 降级关闭
)
```

### 4.3 TunnelConfig 扩展

新增 tunnel 复用相关配置，通过 Session 或 binding 配置注入：

```go
package transport

type TunnelReuseConfig struct {
    // 单条 tunnel 最大复用次数，超过后强制关闭
    // 默认建议值：100
    // 设为 0 表示禁用复用（退化为旧行为）
    MaxReuseCount int

    // 回收握手超时时间
    // 默认建议值：3s
    RecycleHandshakeTimeout time.Duration

    // idle 池中 tunnel 的最大闲置存活时间
    // 超过后主动关闭，由 Agent 补充新 tunnel
    // 默认建议值：5min
    IdleTTL time.Duration
}
```

---

## 5. Runtime 层变更

### 5.1 Traffic 轮次循环

当前 runtime dispatcher 在 `TrafficClose` 后直接释放 tunnel。引入复用后，dispatcher 必须实现 **traffic 轮次（round）循环**：

```go
// 伪代码：Server 侧 tunnel dispatcher 主循环
func (d *Dispatcher) RunTunnel(ctx context.Context, tunnel transport.Tunnel) {
    defer d.pool.Remove(tunnel.ID())

    for {
        // 等待下一次 traffic 分配
        trafficCtx, err := d.waitForTraffic(ctx, tunnel)
        if err != nil {
            // ctx 取消或 session 关闭，退出循环
            return
        }

        // 执行单次 traffic
        result := d.handleTraffic(trafficCtx, tunnel)

        // 根据结果决定是否回收
        if result.ShouldRecycle && tunnel.Recyclable() &&
            tunnel.ReuseCount() < d.cfg.MaxReuseCount {

            recycleResult, err := d.recycleHandshake(ctx, tunnel)
            if err != nil || recycleResult == transport.RecycleResultClosed {
                // 回收失败，退出循环（tunnel 已关闭）
                return
            }

            // 回收成功，将 tunnel 重新放回 idle pool
            if err := d.pool.PutIdle(tunnel); err != nil {
                return
            }
            // 继续下一轮
            continue
        }

        // 不可回收，关闭 tunnel，退出循环
        tunnel.Close()
        return
    }
}
```

### 5.2 TrafficMeta 扩展

`TrafficMeta` 需要记录当前 traffic 所在的 tunnel 轮次，用于审计和调试：

```go
package runtime

type TrafficMeta struct {
    TrafficID    string
    SessionID    string
    SessionEpoch uint64
    ServiceID    string
    Labels       map[string]string
    DeadlineAt   *time.Time

    // 新增：该 traffic 在 tunnel 上的轮次（从 0 开始）
    TunnelReuseRound int
    // 新增：所在 tunnel 的 ID
    TunnelID string
}
```

### 5.3 TrafficProtocol 接口不变

`TrafficProtocol` 的 `ReadFrame`/`WriteFrame` 接口本身无需修改。变化在调用方（runtime dispatcher）的循环逻辑，而非 protocol 层的帧读写接口。

这符合分层设计原则：protocol 层仍然只负责单次帧的编解码，复用逻辑属于 runtime 调度层的职责。

### 5.4 TunnelStats（Agent 侧）

Agent 侧需要维护每条 tunnel 的复用统计，用于决策和监控：

```go
package runtime

type TunnelStats struct {
    TunnelID        string
    CreatedAt       time.Time
    ReuseCount      int           // 当前已复用次数
    MaxReuseCount   int           // 上限（来自 TunnelReuseConfig）
    LastTrafficID   string        // 最近一次 traffic ID
    LastRecycleAt   time.Time     // 最近一次成功回收时间
    RecycleFailures int           // 累计回收失败次数
    DeadlineHit     bool          // 是否触发过 deadline（触发后不可回收）
}

func (s *TunnelStats) IsRecyclable() bool {
    return !s.DeadlineHit &&
        s.RecycleFailures == 0 &&
        s.ReuseCount < s.MaxReuseCount
}
```

### 5.5 控制面 TunnelRefillRequest 水位计算

`TunnelRefillRequest` 的水位计算逻辑需要相应调整：

**旧逻辑：**

```
需要补充数量 = target_idle_count - current_idle_count
（因为每次 traffic 必然消耗一条 tunnel）
```

**新逻辑：**

```
需要补充数量 = target_idle_count - current_idle_count - expected_recycle_count
（expected_recycle_count 为预计可回收回 idle pool 的 tunnel 数量）
```

**约束：** `expected_recycle_count` 只能作为优化参考，不得影响安全下界。若 `current_idle_count < min_idle_count`，必须立即触发补充，不依赖回收预测。

---

## 6. grpc_h2 Binding 特殊处理

### 6.1 gRPC Stream 生命周期与 Tunnel 复用的映射

gRPC bidi stream 一旦任一方发送 `END_STREAM`（`CloseSend`），stream 进入半关闭状态，无法继续用于下一次 traffic。

因此，**grpc_h2 binding 下的 tunnel 复用必须满足**：在 traffic 结束时，不调用 gRPC stream 的 `CloseSend`，而仅通过 LTFP runtime 层的 `TrafficClose` 帧表达业务关闭意图。

这与"禁止并发手工多路复用"的约束**不冲突**：

- 并发复用：同一时刻多个 traffic 共享 tunnel，frames 交错，被禁止
- 串行复用：同一时刻只有一个 traffic，frames 顺序连续，被允许

### 6.2 grpc_h2 Binding 实现要点

```
1. TunnelStream（gRPC bidi stream）不在 TrafficClose 后关闭
2. TrafficClose 帧通过 StreamPayload 发送，与其他帧一样走 LTFP framing
3. TunnelRecycle / TunnelRecycleAck 同样通过 StreamPayload 发送
4. 当 TunnelRecycle(is_final=true) 回收后，Server 侧调用 SendMsg 后
   再调用 CloseSend，Agent 侧读到 EOF 后关闭本端
5. 当 TrafficReset 时，Server 侧直接关闭 gRPC stream（触发 RST_STREAM）
```

### 6.3 StreamPayload 扩展

在现有 `StreamPayload` 的 `oneof payload` 中新增两个 case：

```protobuf
message StreamPayload {
  oneof payload {
    TrafficOpen     open_req       = 1;
    TrafficOpenAck  open_ack       = 2;
    bytes           data           = 3;
    TrafficClose    close          = 4;
    TrafficReset    reset          = 5;
    TunnelRecycle     recycle      = 6;  // 新增
    TunnelRecycleAck  recycle_ack  = 7;  // 新增
  }
}
```

### 6.4 HOL 阻塞风险

引入回收握手帧后，`TunnelRecycle` 和 `TunnelRecycleAck` 本身是轻量控制帧，不携带数据 payload，不会显著增加 HOL 风险。但需注意：

- 如果 Agent 侧在处理 `TunnelRecycleAck` 时存在排队（如 goroutine 调度延迟），可能导致 Server 侧 recycle 超时
- 建议 Agent 侧对 `TunnelRecycle` 消息实现**最高优先级处理**，不与数据面 frame 共用同一处理队列

---

## 7. 约束与边界条件

### 7.1 不可回收条件（满足任一即禁止回收）

以下任意条件成立时，tunnel 必须直接关闭，禁止回收：

| 条件 | 说明 |
|---|---|
| traffic 以 `TrafficReset` 结束 | 异常终止，状态不确定 |
| tunnel 触发过 `SetDeadline` 导致的超时 | deadline 状态可能影响下次读写 |
| `Tunnel.Recyclable()` 返回 false | binding 层认为 tunnel 不健康 |
| `TunnelRecycleAck.accepted = false` | Agent 拒绝回收 |
| 回收握手超时 | 双方状态不一致，不安全 |
| `ReuseCount >= MaxReuseCount` | 达到上限，强制轮换 |
| tunnel 底层 stream 报告任何 I/O 错误 | 底层不健康 |

### 7.2 `recycle_seq` 单调性约束

- `recycle_seq` 由 Server 维护，每条 tunnel 独立计数，从 1 开始
- Agent 必须拒绝 `recycle_seq <= last_accepted_seq` 的 `TunnelRecycle` 消息
- 拒绝时 `TunnelRecycleAck.accepted = false`，并附带 `error_code = "invalid_seq"`

### 7.3 Tunnel 从未被回收过也必须关闭

如果一条 tunnel 始终因不可回收条件而走关闭路径，其行为应与旧协议完全一致（Agent 补充新 tunnel）。复用机制的引入不改变这一兜底路径。

### 7.4 控制面不感知复用状态

- 控制面（ControlChannel）不承载 `TunnelRecycle` / `TunnelRecycleAck`，这两条消息走数据面（tunnel 本身）
- `TunnelRefillRequest` 的语义不变：依然是 Server 向 Agent 请求补充 idle tunnel
- 只是触发频率可能降低（因为部分 tunnel 可以回收，不需要每次都新建）

### 7.5 Session 级别的复用开关

建议在 `ConnectorWelcome` 中增加 `tunnel_reuse_enabled` 字段，允许 Server 在握手阶段告知 Agent 是否启用 tunnel 复用。这使得复用能力可以按 session 级别动态开关，便于灰度发布和降级。

---

## 8. Protobuf 新增定义

在 `LTFP-v1-Draft` protobuf 草案基础上新增以下 message 定义：

```protobuf
// TunnelRecycle：Server 发起，在 TrafficClose/TrafficCloseAck 完成后发送
// 表示 Server 认为 tunnel 可以回收，请求 Agent 确认
message TunnelRecycle {
  // tunnel 的唯一标识（由 binding 层分配）
  string tunnel_id = 1;

  // 单调递增序列号，防止乱序消息污染
  uint64 recycle_seq = 2;

  // 是否为最终回收（回收后关闭，不再入池）
  // 当 ruse_count >= max_reuse_count 时为 true
  bool is_final = 3;

  // 可选：当前该 tunnel 已完成的 traffic 次数（含本次）
  int32 completed_traffic_count = 4;

  map<string, string> metadata = 5;
}

// TunnelRecycleAck：Agent 响应 TunnelRecycle
message TunnelRecycleAck {
  string tunnel_id = 1;
  uint64 recycle_seq = 2;

  // true：Agent 确认 tunnel 状态正常，同意回收
  // false：Agent 认为 tunnel 不健康，拒绝回收（双方均应关闭 tunnel）
  bool accepted = 3;

  // 拒绝时的错误码
  // 已定义值："invalid_seq" | "tunnel_unhealthy" | "deadline_hit" | "buffer_dirty"
  string error_code = 4;
  string error_message = 5;

  map<string, string> metadata = 6;
}
```

`ConnectorWelcome` 扩展字段：

```protobuf
message ConnectorWelcome {
  // 现有字段不变
  string selected_binding = 1;
  uint32 version_major = 2;
  uint32 version_minor = 3;
  uint32 heartbeat_interval_sec = 4;
  repeated string capabilities = 5;
  uint64 assigned_session_epoch = 6;
  map<string, string> metadata = 7;

  // 新增：是否启用 tunnel 复用（Server 侧决策）
  bool tunnel_reuse_enabled = 8;

  // 新增：单条 tunnel 最大复用次数（Server 侧下发配置）
  // 0 表示使用 Agent 本地默认值
  int32 tunnel_max_reuse_count = 9;

  // 新增：回收握手超时（秒）
  uint32 tunnel_recycle_timeout_sec = 10;
}
```

`StreamPayload` 扩展（见第 6 节）：

```protobuf
message StreamPayload {
  oneof payload {
    TrafficOpen      open_req    = 1;
    TrafficOpenAck   open_ack    = 2;
    bytes            data        = 3;
    TrafficClose     close       = 4;
    TrafficReset     reset       = 5;
    TunnelRecycle    recycle     = 6;  // 新增
    TunnelRecycleAck recycle_ack = 7;  // 新增
  }
}
```

---

## 9. Go 接口与骨架代码

### 9.1 Tunnel 接口完整定义（含新增方法）

```go
package transport

import "time"

type Tunnel interface {
    // 标识
    ID() string

    // I/O（实现 io.ReadWriter）
    Read(p []byte) (n int, err error)
    Write(p []byte) (n int, err error)

    // 生命周期
    Close() error
    Reset(err error) error

    // Deadline 控制
    SetDeadline(t time.Time) error
    SetReadDeadline(t time.Time) error
    SetWriteDeadline(t time.Time) error

    // 状态感知
    Done() <-chan struct{}
    Err() error

    // 复用相关（新增）
    Flush() error
    ReuseCount() int
    Recyclable() bool
}
```

### 9.2 TunnelPool 接口完整定义（含新增方法）

```go
package transport

import "context"

type RecycleResult string

const (
    RecycleResultRecycled RecycleResult = "recycled"
    RecycleResultClosed   RecycleResult = "closed"
)

type TunnelPool interface {
    PutIdle(t Tunnel) error
    Acquire(ctx context.Context) (Tunnel, error)
    Remove(id string) error

    IdleCount() int
    InUseCount() int

    // 新增
    Recycle(ctx context.Context, t Tunnel) (RecycleResult, error)
    RecycledCount() int
    ClosedCount() int
}
```

### 9.3 Server 侧 Recycle 握手骨架

```go
package runtime

import (
    "context"
    "fmt"
    "time"

    "your_project/transport"
)

type RecycleHandshaker struct {
    proto   TrafficProtocol
    timeout time.Duration
}

func (h *RecycleHandshaker) Handshake(
    ctx context.Context,
    tunnel transport.Tunnel,
    seq uint64,
    isFinal bool,
) error {
    // 1. 发送 TunnelRecycle
    recycleFrame := TrafficFrame{
        Type: TrafficFrameRecycle,
        Recycle: &TunnelRecycle{
            TunnelID:   tunnel.ID(),
            RecycleSeq: seq,
            IsFinal:    isFinal,
        },
    }
    if err := h.proto.WriteFrame(tunnel, recycleFrame); err != nil {
        return fmt.Errorf("write TunnelRecycle: %w", err)
    }

    // 2. 等待 TunnelRecycleAck，带超时
    deadline := time.Now().Add(h.timeout)
    if err := tunnel.SetReadDeadline(deadline); err != nil {
        return fmt.Errorf("set read deadline: %w", err)
    }
    defer tunnel.SetReadDeadline(time.Time{}) // 无论结果，清除 deadline

    ackFrame, err := h.proto.ReadFrame(tunnel)
    if err != nil {
        return fmt.Errorf("read TunnelRecycleAck: %w", err)
    }
    if ackFrame.Type != TrafficFrameRecycleAck {
        return fmt.Errorf("unexpected frame type: %s", ackFrame.Type)
    }

    ack := ackFrame.RecycleAck
    if ack.RecycleSeq != seq {
        return fmt.Errorf("recycle_seq mismatch: got %d, want %d", ack.RecycleSeq, seq)
    }
    if !ack.Accepted {
        return fmt.Errorf("recycle rejected by agent: %s: %s", ack.ErrorCode, ack.ErrorMessage)
    }

    return nil
}
```

### 9.4 Server 侧 Dispatcher 主循环骨架

```go
package runtime

import (
    "context"

    "your_project/transport"
)

type TunnelDispatcher struct {
    pool    transport.TunnelPool
    proto   TrafficProtocol
    cfg     transport.TunnelReuseConfig
    recycler *RecycleHandshaker
    recycleSeqCounter map[string]uint64 // key: tunnelID
}

func (d *TunnelDispatcher) RunTunnel(ctx context.Context, tunnel transport.Tunnel) {
    defer func() {
        // 确保 tunnel 最终从 pool 移除
        d.pool.Remove(tunnel.ID())
    }()

    for {
        // 1. 将 tunnel 放入 idle pool，等待分配
        if err := d.pool.PutIdle(tunnel); err != nil {
            return
        }

        // 2. 等待 traffic 在此 tunnel 上开始（由 Acquire + TrafficOpen 触发）
        if err := d.waitForTrafficStart(ctx, tunnel); err != nil {
            return // ctx 取消或 session 关闭
        }

        // 3. 执行单次 traffic
        result := d.handleOneTraffic(ctx, tunnel)

        // 4. 决策：回收 or 关闭
        isFinal := !result.NormalClose ||
            !tunnel.Recyclable() ||
            tunnel.ReuseCount() >= d.cfg.MaxReuseCount

        seq := d.nextRecycleSeq(tunnel.ID())

        if result.NormalClose {
            // 发起回收握手
            err := d.recycler.Handshake(ctx, tunnel, seq, isFinal)
            if err != nil {
                // 握手失败，降级关闭
                tunnel.Close()
                return
            }
            if isFinal {
                // is_final=true，握手成功后关闭
                tunnel.Close()
                return
            }
            // 握手成功，继续循环（tunnel 重新 PutIdle）
            continue
        }

        // TrafficReset 或异常，直接关闭
        tunnel.Close()
        return
    }
}

func (d *TunnelDispatcher) nextRecycleSeq(tunnelID string) uint64 {
    d.recycleSeqCounter[tunnelID]++
    return d.recycleSeqCounter[tunnelID]
}
```

### 9.5 Agent 侧 TunnelRecycle 处理骨架

```go
package runtime

import (
    "context"
    "fmt"

    "your_project/transport"
)

type AgentTunnelHandler struct {
    proto TrafficProtocol
    stats map[string]*TunnelStats // key: tunnelID
}

func (h *AgentTunnelHandler) HandleRecycle(
    ctx context.Context,
    tunnel transport.Tunnel,
    frame TunnelRecycle,
) error {
    stats := h.stats[tunnel.ID()]
    if stats == nil {
        return h.sendRecycleAck(tunnel, frame.RecycleSeq, false, "unknown_tunnel", "")
    }

    // 验证 recycle_seq 单调性
    if frame.RecycleSeq <= stats.LastRecycleSeq {
        return h.sendRecycleAck(tunnel, frame.RecycleSeq, false, "invalid_seq",
            fmt.Sprintf("got %d, last accepted %d", frame.RecycleSeq, stats.LastRecycleSeq))
    }

    // 验证是否可回收
    if !stats.IsRecyclable() {
        return h.sendRecycleAck(tunnel, frame.RecycleSeq, false, "tunnel_unhealthy", "")
    }

    // Flush 底层缓冲区
    if err := tunnel.Flush(); err != nil {
        return h.sendRecycleAck(tunnel, frame.RecycleSeq, false, "buffer_dirty", err.Error())
    }

    // 更新统计
    stats.LastRecycleSeq = frame.RecycleSeq

    // 发送 RecycleAck
    if err := h.sendRecycleAck(tunnel, frame.RecycleSeq, true, "", ""); err != nil {
        return err
    }

    // 如果是 final，关闭本端
    if frame.IsFinal {
        return tunnel.Close()
    }

    return nil
}

func (h *AgentTunnelHandler) sendRecycleAck(
    tunnel transport.Tunnel,
    seq uint64,
    accepted bool,
    errCode, errMsg string,
) error {
    return h.proto.WriteFrame(tunnel, TrafficFrame{
        Type: TrafficFrameRecycleAck,
        RecycleAck: &TunnelRecycleAck{
            TunnelID:     tunnel.ID(),
            RecycleSeq:   seq,
            Accepted:     accepted,
            ErrorCode:    errCode,
            ErrorMessage: errMsg,
        },
    })
}
```

---

## 10. 需修订的文档章节

### 10.1 LTFP-TransportAbstraction.md

| 章节 | 原内容摘要 | 修订方向 |
|---|---|---|
| §1.3 非目标 | 列有"tunnel 多次复用" | 从非目标列表中移除 |
| §3.2.4 单 Tunnel 单 Traffic | 强约束：tunnel 一次性使用 | 改为"单 Tunnel 串行多 Traffic"，说明串行复用规则 |
| §3.2.5 Framed All The Way | 列举帧类型 | 补充 `TunnelRecycle` / `TunnelRecycleAck` 为数据面帧类型 |
| §17.4 Tunnel 接口 | 原接口定义 | 增加 `Flush()`、`ReuseCount()`、`Recyclable()` 方法 |
| §17.5 TunnelPool 接口 | 原接口定义 | 增加 `Recycle()`、`RecycledCount()`、`ClosedCount()` 方法 |
| §18.2 TrafficProtocol | 帧类型枚举 | 增加 `TrafficFrameRecycle`、`TrafficFrameRecycleAck` |
| §19.1 grpc_h2 Binding | 数据面描述 | 补充 grpc stream 复用语义，StreamPayload 扩展说明 |
| §20 首版强约束 | 第 8 条"可以暂不实现 tunnel 多次复用" | 改为"必须实现 tunnel 串行复用" |
| §22 推荐目录结构 | 现有结构 | 在 `runtime/connector/` 下增加 `tunnel_reuse.go`、`recycle_handshaker.go` |
| §24 规范性结论 | 第 5 条描述单次 traffic | 更新为串行复用语义 |

### 10.2 LTFP-v1-Draft.md

| 章节 | 原内容摘要 | 修订方向 |
|---|---|---|
| §3.2 数据面禁止应用层二次复用 | 禁止 traffic_id 手工复用 | 保留并发复用禁止约束；新增串行复用例外说明 |
| §22 protobuf 草案 | 现有 message 定义 | 增加 `TunnelRecycle`、`TunnelRecycleAck`；扩展 `ConnectorWelcome`；扩展 `StreamPayload` |
| §24 实施阶段 | 阶段一描述 | 在阶段一中增加"tunnel 串行复用"作为必须实现项 |

---

## 11. 实施顺序建议

建议按以下顺序落地，最小化风险：

### 阶段 0：协议冻结

- 确认 `TunnelRecycle` / `TunnelRecycleAck` 的 protobuf schema
- 确认 `ConnectorWelcome` 扩展字段
- 在 `ConnectorWelcome` 中增加 `tunnel_reuse_enabled=false` 作为默认值（特性开关，默认关闭）
- 更新两份规范文档

### 阶段 1：Transport 层接口扩展

- 在 `transport` 包中增加 `Flush()`、`ReuseCount()`、`Recyclable()` 接口定义
- 在 `transport` 包中增加 `TunnelPool.Recycle()` 接口定义
- 在 `grpcbinding` 中实现上述接口（可先实现 `Recyclable()` 返回 false，跑通接口层）

### 阶段 2：Runtime 层 Recycle 握手

- 实现 `RecycleHandshaker`（Server 侧）
- 实现 `AgentTunnelHandler.HandleRecycle`（Agent 侧）
- 实现 `TunnelDispatcher.RunTunnel` 中的复用循环逻辑
- 在 `tunnel_reuse_enabled=false` 时确保行为与旧版完全一致

### 阶段 3：集成与测试

- 单测：回收成功路径、回收失败降级路径、达到 max_reuse_count 路径、`TrafficReset` 直接关闭路径
- 集成测试：端对端验证 tunnel 在多次 traffic 后状态正确
- 压测：对比开启/关闭复用时的 tunnel 建立速率、连接数、内存占用

### 阶段 4：灰度启用

- 在测试环境将 `ConnectorWelcome.tunnel_reuse_enabled` 改为 true
- 观察监控指标：`RecycledCount`、`ClosedCount`、`ReuseCount` 分布
- 确认无异常后在生产环境逐步开启

---

## 12. 规范性结论

本方案的规范性结论如下：

1. LTFP 传输层采用"长期控制通道 + Agent 预建 Tunnel Pool + **单 Tunnel 串行多 Traffic**"的统一模型，替代原"单 Tunnel 单 Traffic"约束
2. **并发手工多路复用仍然被禁止**：不允许在单条 tunnel 上通过 `traffic_id` 并发承载多条流量
3. 串行复用通过**回收握手（TunnelRecycle / TunnelRecycleAck）** 实现，握手在数据面（tunnel 本身）进行，不经控制面
4. `TunnelRecycle` 只能由 **Server** 发起，`TunnelRecycleAck` 由 **Agent** 响应
5. 回收握手失败（任何原因）必须**降级为 tunnel 关闭**，不得重试回收
6. `recycle_seq` 必须单调递增，Agent 必须拒绝乱序消息
7. 以 `TrafficReset` 结束的 traffic，其 tunnel **不得回收**，必须关闭
8. 触发过 deadline 的 tunnel **不得回收**，必须关闭
9. 每条 tunnel 必须有 `max_reuse_count` 上限，由 Server 在 `ConnectorWelcome` 中下发
10. `tunnel_reuse_enabled` 作为 session 级特性开关，由 Server 在握手阶段控制
11. `TunnelRefillRequest` 水位计算可以参考预计回收数量，但安全下界不得依赖回收预测
12. Transport 层的 `Tunnel` 接口新增 `Flush()`、`ReuseCount()`、`Recyclable()` 方法
13. Transport 层的 `TunnelPool` 接口新增 `Recycle()` 方法，内部自动处理降级逻辑
14. `TunnelRecycle` / `TunnelRecycleAck` 属于数据面帧，纳入"Framed All The Way"范畴
15. grpc_h2 binding 在 tunnel 复用期间不调用 gRPC stream 的 `CloseSend`，仅在 `is_final=true` 回收或错误时才关闭 stream
16. LTFP 协议层状态机仍然是唯一外部真相源，tunnel 内部的 `recycling` 状态属于 transport 内部态，不对外暴露
