# LTFP 传输层规范 v2.1（最终版）

## 1. 文档说明

### 1.1 文档目的

本文档定义 LTFP（Loop Tunnel Forwarding Protocol）在 Agent 与 Server 之间的传输层模型、协议边界、状态机、错误语义、binding 约束以及 Go 语言接口抽象。

本文档用于为以下工作提供统一依据：

* 协议设计
* 运行时实现
* binding 实现
* 技术评审
* 管理面状态与日志口径统一
* 后续 QUIC 等 binding 演进

对应执行清单见 [LTFP-TransportExecutionChecklist.md](./LTFP-TransportExecutionChecklist.md)。

---

### 1.2 适用范围

本文档适用于以下场景：

* Agent 与 Server 之间的长期会话建立
* 控制面消息承载
* 数据面 tunnel 建立、分配与销毁
* 单次 traffic 在 tunnel 上的协议生命周期
* `grpc_h2` 及后续 `quic_native` / `h3_stream` / `tcp_framed` 的统一抽象

---

### 1.3 非目标

本文档不定义以下内容：

* 业务路由决策规则
* 服务发现实现细节
* 权限平台或控制台实现
* UI 展示与审计页面
* 具体 protobuf 业务字段定义
* datagram
* session resume
* mid-stream failover
* tunnel 多次复用

---

## 2. 术语定义

### 2.1 Session

Agent 与 Server 之间的一次长期传输会话。一个 Session 包含：

* 1 条长期控制通道
* 0..N 条空闲 tunnel
* 0..N 条使用中的 tunnel
* 1 组 session 级治理能力

---

### 2.2 Control Channel

控制面长期双向通道，用于承载认证、心跳、服务发布、能力同步、错误通知、池容量事件等控制类消息。

---

### 2.3 Tunnel

由 Agent 主动建立的数据面双向字节通道。Tunnel 创建后先进入空闲池，待 Server 分配给某次实际 traffic 后承载该次 traffic 的数据传输。

Tunnel 在 Transport 层仅表示**字节管道**，不理解 `TrafficOpen / TrafficOpenAck / TrafficClose / TrafficReset / TrafficData` 的业务语义。

---

### 2.4 Tunnel Pool

由 Session 维护的 tunnel 集合，包含 idle tunnel 与 in-use tunnel。其职责是：

* 接收新 tunnel
* 分配 tunnel
* 移除损坏 tunnel
* 维持池容量
* 提供最小可观测状态

---

### 2.5 Traffic

一次实际代理请求在某条 tunnel 上的协议生命周期。Traffic 不是 transport 层对象，而是 protocol/runtime 层对象。

---

### 2.6 Binding

底层传输实现方式，例如：

* `grpc_h2`
* `quic_native`
* `h3_stream`
* `tcp_framed`

---

### 2.7 LTFP Protocol State

已冻结的 LTFP 协议层会话状态机，对外的唯一真相源，当前包括：

* `CONNECTING`
* `AUTHENTICATING`
* `ACTIVE`
* `DRAINING`
* `STALE`
* `CLOSED`

管理面、审计、对外日志与协议级错误归类，必须以该状态机为准。

---

## 3. 设计目标与原则

### 3.1 设计目标

本方案的设计目标如下：

1. 明确控制面与数据面的分层边界
2. 明确 Agent 预建 tunnel pool 的工作模式
3. 消除“Server 主动开流”在 gRPC/HTTP2 下的实现歧义
4. 为协议层与 runtime 层提供稳定 transport 抽象
5. 保持 `grpc_h2` 首版可落地，同时为后续 QUIC 演进留出空间
6. 保证 transport 文档不与已冻结 LTFP 协议真相源冲突

---

### 3.2 设计原则

#### 3.2.1 协议层与传输层解耦

传输层只负责：

* 会话
* 控制通道
* tunnel
* 字节流
* 生命周期
* close / reset / deadline
* binding 能力暴露

传输层不负责：

* service 路由
* 权限判断
* `service_key` 查找
* namespace / env 逻辑
* traffic 协议状态机
* `TrafficOpen/Ack/Close/Reset/Data` 字段语义

---

#### 3.2.2 控制面与数据面分离

控制面负责：

* 握手
* 认证
* 心跳
* 服务发布
* 健康状态同步
* session 级错误上报
* tunnel 池容量同步与补充事件

数据面负责：

* 单次 traffic 的承载
* 数据面协议帧承载
* tunnel 级关闭与 reset

---

#### 3.2.3 Tunnel Pool 模型

数据面采用 Agent 预建 tunnel pool 模型，而不是 Server 临时主动新建 stream。

约束如下：

1. tunnel 必须由 Agent 主动创建
2. tunnel 创建后先进入空闲池
3. Server 在真实请求到来时，从空闲池中分配 tunnel
4. Server 在 tunnel 上先发送 `TrafficOpen`
5. Agent 返回 `TrafficOpenAck`
6. 之后进入数据转发阶段
7. traffic 结束后 tunnel 关闭，由 Agent 再补新 tunnel

---

#### 3.2.4 单 Tunnel 单 Traffic

这是本方案的强约束。

即：

* tunnel 建立后可以空闲等待
* tunnel 一旦被某次 traffic 占用，就不得再服务第二次 traffic
* traffic 结束后 tunnel 必须关闭
* Agent 负责补充新的空闲 tunnel

---

#### 3.2.5 数据面采用 Framed All The Way

这是本方案的强约束。

同一条 tunnel 上承载的所有数据面内容，均必须通过 runtime/protocol 层统一 framing。
也就是说，以下内容都属于数据面协议帧：

* `TrafficOpen`
* `TrafficOpenAck`
* `TrafficData`
* `TrafficClose`
* `TrafficReset`

Transport 不定义这些帧类型，但 runtime/protocol 必须定义并统一编码。

本方案**不采用**“Open/Ack 后切原始 raw byte stream”的模式。

---

## 4. 体系结构

### 4.1 分层结构

本方案分为四层：

#### Layer 1：Protocol Model Layer

定义控制面与数据面协议对象、帧类型和消息语义。

---

#### Layer 2：Transport Abstraction Layer

定义统一 transport 抽象，例如：

* `Session`
* `ControlChannel`
* `Tunnel`
* `TunnelPool`
* `TunnelProducer`
* `TunnelAcceptor`

---

#### Layer 3：Binding Layer

定义具体 binding 实现，例如：

* `grpcbinding`
* `quicbinding`
* `tcpbinding`

---

#### Layer 4：Runtime Layer

负责运行逻辑，例如：

* Agent 侧 tunnel 预建
* Server 侧 tunnel 分配
* `TrafficOpen / TrafficOpenAck / TrafficData / TrafficClose / TrafficReset`
* 数据转发
* 心跳
* session 生命周期治理

---

### 4.2 依赖方向

依赖关系必须保持为：

`protocol/runtime -> transport abstraction <- binding implementation`

含义如下：

* runtime 与 protocol 只能依赖 `transport` 抽象接口
* binding 负责实现 `transport` 抽象
* 具体 binding 必须在组合根（如 main/factory/wire）注入给 runtime
* 禁止 runtime/protocol 直接 import `transport/grpcbinding` 一类具体实现包

---

## 5. 与 LTFP 协议层的映射关系

这一节为规范强制内容。

### 5.1 唯一外部真相源

LTFP 协议层状态机是**唯一外部真相源**。
下列场景必须使用协议层状态，而不是 transport 内部状态：

* 管理面展示
* 审计
* 对外日志
* 协议级错误处理
* 状态统计与告警

Transport 层状态仅用于：

* binding 内部治理
* runtime 内部调度
* 连接建立过程中的局部里程碑

Transport 状态**不得**直接作为对外协议状态暴露。

---

### 5.2 Transport SessionState 与 LTFP Protocol State 映射

Transport `SessionState` 不是协议态，但必须能映射到协议态。

建议映射如下：

| Transport SessionState | LTFP Protocol State | 说明                                  |
| ---------------------- | ------------------- | ----------------------------------- |
| `idle`                 | `CONNECTING`        | 尚未进入有效传输阶段                          |
| `connecting`           | `CONNECTING`        | 正在建立底层连接                            |
| `connected`            | `AUTHENTICATING`    | 物理连接已建立，控制面未完成初始化                   |
| `control_ready`        | `AUTHENTICATING`    | 控制面可用，但认证未完成                        |
| `authenticated`        | `ACTIVE`            | 可收发业务控制消息并调度 tunnel                 |
| `draining`             | `DRAINING`          | 不再接新任务，进行排空                         |
| `failed`               | 见下方判定规则            | `failed` 只是 transport 内部失败态，不能直接作为对外协议态         |
| `closed`               | `CLOSED`            | 明确关闭                                |

规范要求：

1. binding/runtime 必须提供该映射
2. `failed` 的对外协议态归属必须按下述固定规则判定，不得由 binding 各自定义
3. 任意管理面若读取的是 transport `SessionState`，必须显式标记为“内部态”

`failed` 的固定映射规则如下：

1. 若 session 尚未进入 `authenticated` 即失败，但实现仍处于自动重试/退避重连阶段，且尚未宣布放弃本次 session，则对外仍映射为 `CONNECTING`
2. 对于前述持续重试场景，运行时必须在内部元信息、日志或指标中暴露最近一次失败原因，禁止把错误静默吞掉
3. 若 session 尚未进入 `authenticated` 即失败，且实现已放弃本次建立流程，则对外映射为 `CLOSED`
4. 若 session 已进入 `authenticated`，随后因 heartbeat 超时、控制面 transport error、对端异常失联而失败，则对外映射为 `STALE`
5. 显式本地关闭应走 `draining -> closed`，不得先进入 `failed` 再映射 `CLOSED`
6. runtime 不得将 transport `failed` 原样透传为外部协议状态

---

### 5.3 `service_key` 与 `service_id` 边界

本规范采用以下硬约束：

1. `service_key` 仅用于 Server 侧 route resolve 输入
2. route resolve 完成后，runtime/traffic 只允许使用 `service_id`
3. transport 层不得依赖 `service_key`
4. 数据面 tunnel 分配后，不得重新触发基于 `service_key` 的 lookup 语义

结论：

* `service_key` 属于控制面 / 路由决策层输入
* `service_id` 属于 resolve 结果，也是 runtime/traffic 的唯一服务标识

---

## 6. Session 语义

### 6.1 Session 定义

Session 表示 Agent 与 Server 之间的一次长期传输会话，是控制面和数据面的共同上层上下文，也是 transport 侧聚合根。

---

### 6.2 Session 组成

一个 Session 包含：

* 1 条活动 Control Channel
* 若干条 idle tunnel
* 若干条 in-use tunnel
* tunnel 生产 / 接收 / 池管理能力
* 1 个 session 级生命周期状态机

---

### 6.3 Session 状态

状态定义如下：

* `idle`
* `connecting`
* `connected`
* `control_ready`
* `authenticated`
* `draining`
* `closed`
* `failed`

---

### 6.4 Session 状态说明

#### idle

尚未建立底层传输连接。

#### connecting

正在建立底层连接与 binding 所需握手。

#### connected

底层连接已建立，但控制面尚未就绪。

#### control_ready

控制通道已建立，可以交换控制帧。

#### authenticated

认证和初始化已完成，可维护 tunnel pool 并分配实际 traffic。

#### draining

会话进入排空状态，不再接收新的 traffic 分配，但允许已有 traffic 收尾。

#### closed

正常关闭。

#### failed

异常终止。

---

### 6.5 Session 状态转换

```text
idle -> connecting -> connected -> control_ready -> authenticated
authenticated -> draining -> closed
authenticated -> failed
control_ready -> failed
connected -> failed
connecting -> failed
```

---

### 6.6 Session 状态约束

1. 未进入 `authenticated` 前，不得分配实际 traffic
2. `draining` 状态下，不得再分配新的 tunnel
3. `failed` 后必须终止控制面并清理全部 tunnel
4. 同一 session 不得从 `failed` 回到 `authenticated`
5. 对外状态展示必须转换为 LTFP 协议态

---

## 7. Control Channel 语义

### 7.1 定义

Control Channel 是 Session 唯一长期控制通道。

---

### 7.2 职责

* 交换握手和认证消息
* 交换 heartbeat
* 同步服务发布与状态
* 上报 session 级错误
* 协调 tunnel pool 状态与补充事件

---

### 7.3 控制面典型顺序

```text
底层会话建立
-> ControlChannel ready
-> ConnectorHello
-> ConnectorWelcome
-> ConnectorAuth
-> ConnectorAuthAck
-> PublishService / Heartbeat / TunnelPoolReport / TunnelRefillRequest / ControlError ...
```

---

### 7.4 控制面约束

1. `AuthAck(success=true)` 前不得发布业务服务
2. 心跳只能在 control ready 后开始
3. 控制面断开通常视为 session 不可用
4. 控制面不承载业务字节流

### 7.5 控制面优先级与 HOL 规避

控制面虽然逻辑上是单一长期通道，但实现必须显式规避大消息导致的队头阻塞（Head-of-Line Blocking）。

规范建议：

1. `Heartbeat`、认证相关消息、`TunnelRefillRequest`、`ControlError` 应视为高优先级控制消息
2. `PublishService`、`TunnelPoolReport` 若体积较大，应支持分页、分块或增量同步，避免单条消息长期占满发送队列
3. heartbeat 判死阈值必须考虑控制面队列抖动与大消息发送延迟，禁止把单次排队延迟直接当成对端失活
4. 对于串行发送的 binding，实现应在 runtime/control loop 中提供优先级调度或独立发送队列，以降低 HOL 风险

---

## 8. Tunnel 语义

### 8.1 定义

Tunnel 是一条由 Agent 主动建立的数据面双向字节通道。

在 Transport 层，Tunnel 只表示底层流对象，不感知 Traffic 协议状态。

---

### 8.2 Tunnel 状态

状态定义如下：

* `opening`
* `idle`
* `reserved`
* `active`
* `closing`
* `closed`
* `broken`

---

### 8.3 Tunnel 状态说明

#### opening

Agent 正在创建底层 tunnel。

#### idle

Tunnel 已建立，处于空闲池中，尚未分配给任何 traffic。

#### reserved

Tunnel 已被 Server 分配，不能再被其他请求抢占，但尚未完成整个使用过程。

#### active

Tunnel 正在被本次 traffic 使用。Transport 只知道它已被使用，不区分 `open_sent` 还是 `established`。

#### closing

Tunnel 正在关闭过程中。

#### closed

Tunnel 已正常关闭。

#### broken

Tunnel 因底层异常、reset 或其他错误而不可继续使用。

---

### 8.4 Tunnel 状态转换

```text
opening -> idle
idle -> reserved
reserved -> active
reserved -> closed
reserved -> broken
active -> closing
active -> closed
active -> broken
closing -> closed
idle -> broken
```

---

### 8.5 Tunnel 状态约束

1. 只有 `idle` tunnel 才能被分配
2. `reserved` 表示该 tunnel 已被独占，不得再次分配
3. `active` 仅表示 tunnel 正在被使用，不表示协议层一定已完成 `OpenAck`
4. `closed` 和 `broken` 的 tunnel 不得重回池中复用
5. 一条 tunnel 只能承载一次 traffic
6. 同一条 tunnel 允许“一侧读、一侧写”的并发使用
7. 同一条 tunnel 上不保证多写方并发安全；若存在多个写入来源，必须由更高层串行化
8. 同一条 tunnel 上不保证多读方并发安全；调用方应维持单 reader 模型

---

## 9. Tunnel Pool 语义

### 9.1 Agent 侧职责

Agent 负责：

1. 在 session 认证成功后主动创建若干空闲 tunnel
2. 在 tunnel 被消费或损坏后补充新 tunnel
3. 定期清理过期 idle tunnel
4. 通过控制面上报池状态

---

### 9.2 Server 侧职责

Server 负责：

1. 接收 Agent 建好的 idle tunnel
2. 从池中分配 tunnel 给实际 traffic
3. 跟踪 tunnel 生命周期
4. 在 traffic 结束后回收本地状态

---

### 9.3 容量管理建议

建议配置以下参数：

* `min_idle_tunnels`
* `max_idle_tunnels`
* `idle_tunnel_ttl`
* `acquire_timeout`
* `max_inflight_tunnel_opens`
* `tunnel_open_rate_limit`
* `tunnel_open_burst`

---

### 9.4 容量不足处理

当 Server 没有可用 idle tunnel 时，可选策略为：

1. 立即失败
2. 短暂等待新 tunnel 到位
3. 通过控制面请求 Agent 增补 tunnel

首版建议：

* 允许短等待
* 超时后快速失败
* 不允许无限期阻塞
* `TunnelRefillRequest` 只能作为“水位提示”，不得被实现成瞬时硬命令
* Agent 应通过平滑控制循环逐步逼近目标容量，而不是收到 `delta=N` 就立即并发创建 N 条 tunnel
* Agent 必须限制并发建连数与建连速率，避免在流量突刺下形成惊群效应

---

### 9.5 服务级隔离说明

首版 tunnel pool 与 session 绑定，这意味着同一 session 下的多个服务默认共享同一个 idle pool。

这会带来一个现实约束：高流量服务可能抢占低流量但高优先级服务的可用 tunnel。

首版规范不强制实现按服务拆分的独立 tunnel pool，但要求：

1. 调度层必须为后续按 `service_id`、QoS 或优先级做资源隔离保留演进空间
2. 文档与运维配置必须明确“同 session 服务默认共享 pool”的语义
3. 若存在强优先级服务，建议通过独立 session、预留容量或实现定义的优先级策略做隔离

---

## 10. Tunnel Pool 最小事件模型

这一节为规范强制内容。

### 10.1 最小语义要求

无论具体控制面消息名如何定义，实现必须满足以下最小事件语义：

1. Agent 必须能够上报当前 tunnel 池状态
2. Server 必须能够表达“当前 idle tunnel 不足”
3. 双方必须能基于该事件触发 tunnel 增补
4. 这些事件必须走控制面，而不是数据面 tunnel

---

### 10.2 推荐事件对象

首版推荐以下控制面事件对象名称：

* `TunnelPoolReport`
* `TunnelRefillRequest`

若首版暂未冻结消息名称，则允许使用等价实现，但必须满足 10.1 的语义要求。

---

### 10.3 最小字段建议

#### TunnelPoolReport

最小建议字段：

* `session_id`
* `session_epoch`
* `idle_count`
* `in_use_count`
* `target_idle_count`
* `timestamp`

#### TunnelRefillRequest

最小建议字段：

* `session_id`
* `session_epoch`
* `request_id`
* `requested_idle_delta`
* `reason`
* `timestamp`

若后续协议正式冻结字段，以冻结版本为准。

`TunnelRefillRequest` 的最小语义要求：

1. `request_id` 必须作为幂等键使用
2. `requested_idle_delta` 表示“建议补充的最小空闲增量”，而不是强制绝对值命令
3. Agent 可以合并多个 refill request，但不得因重复请求无限制超量补 tunnel
4. Server 重试同一 refill request 时，Agent 必须按 `request_id` 去重
5. `TunnelRefillRequest` 只能表达容量意图，Agent 不得将其解释为“立刻并发创建 delta 条 tunnel”的硬指令
6. Agent 必须结合 `max_inflight_tunnel_opens`、速率限制和当前负载，平滑地逼近目标 idle 容量

---

## 11. Traffic 协议边界

### 11.1 定义

Traffic 不是 transport 层对象，而是 protocol/runtime 层对象。

Traffic 表示：

> 一次实际代理请求在某条 tunnel 上的协议生命周期。

---

### 11.2 Traffic 状态

建议在 runtime 层定义如下状态：

* `reserved`
* `open_sent`
* `established`
* `closing`
* `closed`
* `reset`
* `rejected`

---

### 11.3 Traffic 状态说明

#### reserved

已分配 tunnel，准备发送 `TrafficOpen`。

#### open_sent

已发送 `TrafficOpen`，等待 `TrafficOpenAck`。

#### established

已收到 `TrafficOpenAck(success=true)`，进入双向数据转发阶段。

#### closing

正在进行协议性收尾。

#### closed

正常结束。

#### reset

异常终止。

#### rejected

收到 `TrafficOpenAck(success=false)`，本次 traffic 被拒绝。

---

### 11.4 数据面典型顺序

```text
Tunnel 建立并空闲
-> Server 分配 tunnel
-> Server 发送 TrafficOpen
-> Agent 返回 TrafficOpenAck
-> 双向 TrafficData
-> TrafficClose / TrafficReset
-> Tunnel 关闭
```

---

### 11.5 数据面约束

1. tunnel 被分配后的首个协议动作必须是 `TrafficOpen`
2. 未收到 `TrafficOpenAck(success=true)` 前不得进入业务数据转发
3. `TrafficOpenAck(success=false)` 后当前 tunnel 必须关闭
4. 收到 `TrafficReset` 后必须立即终止该 traffic
5. traffic 结束后 tunnel 关闭，不回池

---

## 12. 数据面 Framing 模型

这一节为规范强制内容。

### 12.1 规范选择

LTFP 数据面采用 **framed all the way** 模型。

也就是说，同一条 tunnel 上承载的所有内容都必须被视为 runtime/protocol 层的数据面协议帧，至少包括：

* `TrafficOpen`
* `TrafficOpenAck`
* `TrafficData`
* `TrafficClose`
* `TrafficReset`

---

### 12.2 为什么不采用 Raw Stream After Ack

本规范不采用“Open/Ack 成功后切换为原始字节流”的模式，原因如下：

1. `TrafficClose/Reset` 无法自然插回同一条 tunnel
2. gRPC bidi stream 本质上是消息流，不是真裸 TCP
3. 不同 binding 下容易出现语义分叉
4. 统一 framed 模型更利于协议一致性

---

### 12.3 Transport 与 Runtime 分工

#### Transport 层

* 只提供 tunnel 字节读写
* 不定义数据面协议帧类型
* 不解释 tunnel 内字节含义
* 通过 `SetReadDeadline / SetWriteDeadline / SetDeadline` 提供超时控制

#### Runtime / Protocol 层

* 定义数据面帧结构
* 负责 `TrafficOpen/Ack/Data/Close/Reset` 的编码与解码
* 负责数据面状态机推进
* 负责将普通流式 I/O 适配为 framed data plane

---

### 12.4 数据面最小帧集合

首版 runtime/protocol 必须定义最小数据面帧集合：

* `Open`
* `OpenAck`
* `Data`
* `Close`
* `Reset`

注意：

* 前一版 transport 文档中删除 `DataFrame` 是正确的
* 但这不等于“数据面没有 frame”
* 正确结论是：**数据面 frame 属于 runtime/protocol，而不属于 transport abstraction**

### 12.5 Framed Adapter 与标准 I/O 的边界

这一节为规范强制内容。

由于数据面采用 **framed all the way** 模型，Runtime 层在进行实际流量转发时：

* 禁止直接调用 `io.Copy(tunnel, clientConn)`
* 禁止直接调用 `io.Copy(serverConn, tunnel)`

原因是：

* `tunnel` 只提供底层字节管道
* `TrafficData` 必须先经过 runtime/protocol 编码为数据面 frame
* 若绕过 `TrafficProtocol` 直接向 `tunnel` 写裸字节，对端 `ReadFrame()` 将无法解码

Runtime 层必须自行封装一个 `TrafficDataStream` Adapter，供代理逻辑使用标准 `io.Copy`：

* 该适配器实现标准的 `io.Reader` 和 `io.Writer`
* 其 `Write(p)` 必须将输入按 `max_data_frame_size` 拆分，再逐帧调用 `TrafficProtocol.WriteFrame(Tunnel, TrafficFrame{Type: Data, Data: chunk})`
* 其 `Read(p)` 必须循环调用 `TrafficProtocol.ReadFrame()`，仅提取 `Type=data` 的载荷
* `Write(p)` 必须保证拆包后分片顺序与原始字节流顺序一致，且在全部分片写完前不得静默丢弃尾部数据
* 单帧 `Data` 最大尺寸必须由 runtime/binding 协商或配置化；若未显式配置，`grpc_h2` 首版建议不超过 `1 MiB`
* 禁止将数 MiB 甚至更大的单次 `Write(p)` 直接封装成单个 `TrafficData` frame，以避免触发底层消息大小限制和大对象分配
* 当单个 `Data` frame 大于 `p` 时，适配器必须维护内部缓冲，禁止截断丢数据
* 收到 `Close` 时，适配器应向上层返回 `io.EOF`
* 收到 `Reset` 时，适配器应返回显式错误，不得伪装成普通 EOF
* 适配器必须支持“一侧读、一侧写”的并发用法，以兼容双向 `io.Copy`

结论：

* `Tunnel` 面向 transport/runtime 内核，不面向直接业务代理
* 上层代理逻辑若要继续使用 `io.Copy`，必须对 `TrafficDataStream` Adapter 使用，而不是直接对 `Tunnel` 使用

---

## 13. 错误模型

### 13.1 错误分类

必须区分三类错误：

* `Transport Error`
* `Protocol Error`
* `Application Reject`

---

### 13.2 Transport Error

由底层传输或 binding 引起，例如：

* gRPC stream cancel
* QUIC reset
* TCP 断开
* TLS 握手失败
* frame 读取失败
* deadline 超时

处理规则：

* 控制面上的 transport error 通常升级为 session 失败
* 单 tunnel 上的 transport error 只影响该 tunnel 对应 traffic

---

### 13.3 Protocol Error

由协议顺序或帧格式不合法引起，例如：

* 未 open 就发 data
* open 后未 ack 就写业务流
* 必填字段缺失
* 非法状态流转

处理规则：

* 控制面协议错误：可上报 `ControlError`，必要时关闭 session
* 数据面协议错误：reset 当前 tunnel

---

### 13.4 Application Reject

由业务逻辑拒绝引起，例如：

* service 不存在
* 权限不足
* namespace 不匹配
* agent 本地连接目标失败

处理规则：

* 在 `TrafficOpenAck(success=false)` 中返回拒绝信息
* 当前 traffic 结束
* tunnel 关闭
* 不影响整个 session

---

## 14. 超时与保活

### 14.1 Session 建立超时

`Session.Open()` 必须受连接建立超时约束。

建议区分：

* connect timeout
* TLS timeout
* control ready timeout
* auth timeout

任一阶段超时均可使 session 进入 `failed`。

---

### 14.2 Control Heartbeat

控制面必须有显式 heartbeat 机制。

原因：

* 底层 keepalive 只能表明链路是否还活着
* 协议层 heartbeat 才能表明对端协议栈是否仍可用

建议：

* 固定周期发送 heartbeat
* 连续 N 次无响应则标记 session 不可用
* session 不可用后进入 `draining` 或 `failed`
* heartbeat 超时阈值必须覆盖控制面队列抖动与大消息发送延迟，避免出现虚假超时

---

### 14.3 Tunnel Idle TTL

空闲 tunnel 可以设置最大空闲寿命，以避免：

* 中间网络设备回收连接
* 长期占用无效资源

超时后：

* Agent 主动关闭旧 tunnel
* 再补充新 tunnel

此外，`idle_tunnel_ttl` 不能替代僵尸 tunnel 探测。

对于长期驻留 idle 池的 tunnel，binding/runtime 应按底层能力启用传输层保活或探活机制，例如：

* gRPC keepalive / ping
* TCP keep-alive
* 实现定义的轻量探测

若探活失败、keepalive 超时或 binding 明确报告链路不可用，则该 tunnel 必须标记为 `broken` 并从池中移除，禁止继续分配。

---

### 14.4 Traffic Open Timeout

Server 发送 `TrafficOpen` 后必须等待 `TrafficOpenAck`。

若超时未收到 ack：

* 当前 tunnel 标记 `broken`
* 当前 traffic 失败
* tunnel 从池中移除
* Agent 补新 tunnel

---

### 14.5 Data Idle Timeout

数据转发期间可配置读写 idle timeout。

超时后：

* 优先协议性 close
* 无法收尾则 reset

---

## 15. 关闭与终止语义

### 15.1 正常关闭

正常关闭表示本次 traffic 已完成，双方一致结束。

语义：

1. 一方发送 `TrafficClose`
2. 对端完成收尾
3. 双方释放资源
4. tunnel 关闭
5. Agent 后续补充新 tunnel

---

### 15.2 异常终止

异常终止表示本次 traffic 无法继续。

语义：

1. 一方发送 `TrafficReset`
2. 对端立即停止转发
3. tunnel 标记损坏或直接关闭
4. 当前 traffic 失败
5. Agent 补新 tunnel

---

### 15.3 半关闭

若 binding 支持 half-close，则允许一侧停止写、另一侧继续读。

若 binding 不支持，则由 runtime 使用 `TrafficClose` 语义实现替代。

首版建议：

* gRPC binding：通过协议 close 替代真正 socket half-close
* QUIC binding：可支持 `CloseWrite()`

首版规范**不**额外引入 `CloseRead()` 抽象。

若本地需要主动放弃继续接收对端数据，并要求对端立即停止发送，应使用 `Reset()`，而不是引入额外的单边读关闭接口。

---

## 16. Binding 能力矩阵与兼容规则

这一节为规范强制内容。

### 16.1 支持级别分类

Transport/Tunnel 能力分为三类：

#### 必须支持

* `Reset`
* `Done`
* `Err`

#### 可选支持

* `CloseWrite`
* `SetDeadline`
* `SetReadDeadline`
* `SetWriteDeadline`

#### 不支持时的统一行为

必须返回 `ErrUnsupported`，不得 silent ignore。

---

### 16.2 `ErrUnsupported`

建议定义：

```go
var ErrUnsupported = errors.New("unsupported capability")
```

规范要求：

1. binding 不支持某项可选能力时，必须返回 `ErrUnsupported`
2. 不允许空实现
3. 不允许表面返回成功但实际无效

---

### 16.3 grpc_h2 最低要求

对于 `grpc_h2` binding：

* `Reset`：必须支持，可映射为 stream cancel/reset
* `Done/Err`：必须支持
* `CloseWrite`：可选；若不支持返回 `ErrUnsupported`
* `SetDeadline*`：可选；若不支持返回 `ErrUnsupported`，或由 binding 文档明确声明采用 goroutine + timer 模拟

---

### 16.4 QUIC 最低要求

对于 `quic_native` binding：

* `Reset`：必须支持
* `Done/Err`：必须支持
* `CloseWrite`：建议支持
* `SetDeadline*`：建议支持

---

## 17. Go 接口定义

### 17.1 基础类型

```go
package transport

import (
	"context"
	"errors"
	"io"
	"time"
)

type BindingType string

const (
	BindingGRPC BindingType = "grpc_h2"
	BindingQUIC BindingType = "quic_native"
	BindingH3   BindingType = "h3_stream"
	BindingTCP  BindingType = "tcp_framed"
)

type SessionState string

const (
	SessionStateIdle          SessionState = "idle"
	SessionStateConnecting    SessionState = "connecting"
	SessionStateConnected     SessionState = "connected"
	SessionStateControlReady  SessionState = "control_ready"
	SessionStateAuthenticated SessionState = "authenticated"
	SessionStateDraining      SessionState = "draining"
	SessionStateClosed        SessionState = "closed"
	SessionStateFailed        SessionState = "failed"
)

type TunnelState string

const (
	TunnelStateOpening  TunnelState = "opening"
	TunnelStateIdle     TunnelState = "idle"
	TunnelStateReserved TunnelState = "reserved"
	TunnelStateActive   TunnelState = "active"
	TunnelStateClosing  TunnelState = "closing"
	TunnelStateClosed   TunnelState = "closed"
	TunnelStateBroken   TunnelState = "broken"
)

type Endpoint struct {
	Network string
	Address string
}

type BindingInfo struct {
	Type                 BindingType
	Local                Endpoint
	Remote               Endpoint
	SupportsHalfClose    bool
	SupportsStreamReset  bool
	SupportsDeadline     bool
	MaxConcurrentStreams int64
}

type SessionMeta struct {
	SessionID    string
	SessionEpoch uint64
	NodeID       string
	Labels       map[string]string
}

type TunnelMeta struct {
	TunnelID     string
	SessionID    string
	SessionEpoch uint64
	CreatedAt    time.Time
	Labels       map[string]string
}
```

---

### 17.2 ControlFrame

控制面 transport 接口直接承载 `ControlFrame`。

```go
package transport

type ControlFrame struct {
	Type    uint16
	Payload []byte
}
```

说明：

* `Payload` 由上层 protocol 编解码
* Transport 不解释 payload 的业务含义

---

### 17.3 ControlChannel

```go
package transport

type ControlChannel interface {
	WriteControlFrame(ctx context.Context, frame ControlFrame) error
	ReadControlFrame(ctx context.Context) (ControlFrame, error)

	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}
```

---

### 17.4 Tunnel

`tunnel` 是数据面底层字节流对象，应尽可能贴近 `net.Conn` 风格。

```go
package transport

type Tunnel interface {
	io.Reader
	io.Writer
	io.Closer

	ID() string
	Meta() TunnelMeta
	State() TunnelState
	BindingInfo() BindingInfo

	CloseWrite() error
	Reset(cause error) error

	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error

	Done() <-chan struct{}
	Err() error
}
```

说明：

1. `Read/Write` 不带 context，便于与 `io.Copy` 集成
2. `Tunnel` 只传输字节，不认 `TrafficOpen/Ack/Close/Reset/Data`
3. 数据面 framing 由 runtime/protocol 负责
4. 可选能力不支持时必须返回 `ErrUnsupported`
5. 同一条 tunnel 的多 goroutine 并发写入不保证安全；若存在多写方，上层必须自行串行化

---

### 17.5 TunnelProducer / TunnelAcceptor / TunnelPool

```go
package transport

type TunnelProducer interface {
	OpenTunnel(ctx context.Context) (Tunnel, error)
}

type TunnelAcceptor interface {
	AcceptTunnel(ctx context.Context) (Tunnel, error)
}

type TunnelPool interface {
	PutIdle(t Tunnel) error
	Acquire(ctx context.Context) (Tunnel, error)
	Remove(id string) error

	IdleCount() int
	InUseCount() int
}
```

---

### 17.6 Session（聚合根）

`Session` 必须作为 transport 聚合根，统一暴露 control / tunnel 相关能力。

```go
package transport

type Session interface {
	ID() string
	Meta() SessionMeta
	State() SessionState
	BindingInfo() BindingInfo

	Open(ctx context.Context) error
	Close(ctx context.Context, reason error) error

	Control() (ControlChannel, error)

	TunnelProducer() (TunnelProducer, error)
	TunnelAcceptor() (TunnelAcceptor, error)
	TunnelPool() (TunnelPool, error)

	Done() <-chan struct{}
	Err() error
}
```

规范要求：

1. Agent 侧 Session 必须提供 `TunnelProducer`
2. Server 侧 Session 必须提供 `TunnelAcceptor` 和 `TunnelPool`
3. 角色不支持的能力必须返回 `ErrUnsupported`
4. 能力受生命周期影响暂不可用时，必须返回显式错误，禁止返回 `(nil, nil)`
5. 不允许绕开 Session 再单独引入脱离生命周期的 tunnel 根对象

推荐约束：

* `Control()` 在 session 尚未 ready 时可返回 `ErrNotReady`
* `TunnelProducer/TunnelAcceptor/TunnelPool` 若该 session 角色不具备该能力，必须返回 `ErrUnsupported`
* 若 session 已关闭或失败，应返回 `ErrSessionClosed`、`ErrClosed` 或实现定义的显式错误

---

## 18. Runtime 层推荐抽象

### 18.1 TrafficMeta / TrafficState

`TrafficMeta` 必须只使用 `service_id`，不得再使用 `service_key`。

```go
package runtime

import "time"

type TrafficMeta struct {
	TrafficID    string
	SessionID    string
	SessionEpoch uint64
	ServiceID    string
	Labels       map[string]string
	DeadlineAt   *time.Time
}

type TrafficState string

const (
	TrafficStateReserved    TrafficState = "reserved"
	TrafficStateOpenSent    TrafficState = "open_sent"
	TrafficStateEstablished TrafficState = "established"
	TrafficStateClosing     TrafficState = "closing"
	TrafficStateClosed      TrafficState = "closed"
	TrafficStateReset       TrafficState = "reset"
	TrafficStateRejected    TrafficState = "rejected"
)
```

---

### 18.2 TrafficFrame 与 TrafficProtocol

数据面 framing 与编解码属于 runtime/protocol 层。

```go
package runtime

import (
	"your_project/transport"
)

type TrafficFrameType string

const (
	TrafficFrameOpen    TrafficFrameType = "open"
	TrafficFrameOpenAck TrafficFrameType = "open_ack"
	TrafficFrameData    TrafficFrameType = "data"
	TrafficFrameClose   TrafficFrameType = "close"
	TrafficFrameReset   TrafficFrameType = "reset"
)

type TrafficOpenAck struct {
	Accepted bool
	Code     string
	Message  string
}

type TrafficClose struct {
	Code    string
	Message string
}

type TrafficReset struct {
	Code    string
	Message string
}

type TrafficFrame struct {
	Type    TrafficFrameType
	Open    *TrafficMeta
	OpenAck *TrafficOpenAck
	Data    []byte
	Close   *TrafficClose
	Reset   *TrafficReset
}

type TrafficProtocol interface {
	WriteFrame(t transport.Tunnel, frame TrafficFrame) error
	ReadFrame(t transport.Tunnel) (TrafficFrame, error)
}
```

规范要求：

1. `ReadFrame` 必须作为统一“读取下一帧”入口
2. 调用方不得在未知下一帧类型的情况下猜测性调用 `ReadOpen/ReadData/ReadReset` 一类接口
3. 如需 typed helper，可在 `ReadFrame/WriteFrame` 之上封装，但不得替代统一帧入口
4. `TrafficData` 既然属于 framed all the way，就必须通过 `TrafficFrame{Type=data}` 承载
5. `TrafficProtocol` 不得在高频数据面 `ReadFrame/WriteFrame` 上重新引入 `context.Context`
6. 数据面超时控制必须通过 `Tunnel.SetReadDeadline / SetWriteDeadline / SetDeadline` 完成
7. 同一条 tunnel 上，允许一个 goroutine 调用 `ReadFrame`，另一个 goroutine 调用 `WriteFrame` 并发工作
8. 同一条 tunnel 上，多个并发 `WriteFrame` 调用必须被串行化，确保单个 frame 的字节写出保持原子连续，不得发生 frame 字节交错
9. 同一条 tunnel 上，不保证多个并发 `ReadFrame` 调用安全；调用方应维持单 reader 模型

说明：

* 本版删除了多组 `ReadOpen/ReadData/ReadClose/...` 分散读取接口，避免调用方无法预知下一帧类型
* typed helper 可以作为实现细节存在，但规范最小接口必须是统一 frame 模型
* `TrafficProtocol` 既然建立在 `io.Reader/io.Writer` 风格的 `Tunnel` 之上，就应保持一致的纯流式接口
* 若实现方需要支持取消，应在 runtime 更外层控制 goroutine 生命周期，而不是在每次 frame I/O 上强塞 `ctx`
* 推荐在 `TrafficProtocol` 实现内部使用 mutex、单 writer loop 或等价机制序列化同一 tunnel 上的 frame 写出

---

## 19. Binding 映射约束

### 19.1 grpc_h2

#### 控制面

* 一条长期 bidi stream 对应一个 `ControlChannel`

示例：

```proto
rpc ControlChannel(stream ControlEnvelope) returns (stream ControlEnvelope);
```

---

#### 数据面

* Agent 主动发起大量 `TunnelStream`
* 每个 `TunnelStream` 对应一条 tunnel
* Server 接收到后放入 idle pool
* 实际请求到来时由 Server 从 idle pool 分配 tunnel
* 在该 tunnel 上发送 `TrafficOpen`

传输层眼中，数据面只有纯字节 payload，不承载业务字段。

建议 protobuf 定义如下：

```proto
message TunnelEnvelope {
  bytes payload = 1;
}

rpc TunnelStream(stream TunnelEnvelope) returns (stream TunnelEnvelope);
```

规范要求：

1. `TunnelEnvelope` 只能作为 transport 载体
2. 不得在 `TunnelEnvelope` 中直接定义 `TrafficOpen/OpenAck/Close/Reset/Data` 等业务字段
3. 这些业务对象必须由 runtime/protocol 层先编码为二进制，再写入 `payload`

#### 性能与拷贝优化建议

`grpc_h2` 自身已经是 message-framed 传输，叠加 LTFP runtime/protocol frame 后，必然存在多层 framing 与额外内存拷贝成本。

这属于首版为保持抽象边界清晰而接受的 trade-off，但实现方必须主动做性能治理。

建议如下：

1. 优先复用编码缓冲区、`bytes.Buffer` 或对象池，减少每帧临时分配
2. `TrafficDataStream.Write` 必须配合 `max_data_frame_size` 做分片，避免触发 gRPC 单消息大小限制与大对象分配
3. binding 可以在不破坏标准抽象的前提下实现私有 fast path，以减少一次中间拷贝或优化批量发送
4. 任何 fast path 都必须是可选优化；标准 `Tunnel` + `TrafficProtocol` 路径必须始终可用
5. fast path 不得把 gRPC protobuf 类型或 binding 私有对象泄露到 runtime/protocol 公共接口中

---

### 19.2 quic_native

* 控制面可使用一条长期 QUIC stream
* 数据面仍建议沿用 tunnel pool 模型
* 虽然 QUIC 支持双方主动开 stream，但首版协议抽象仍以共同能力下界为准，不改变池化模型

---

### 19.3 tcp_framed

* 控制面使用长期 framed TCP 连接
* 数据面 tunnel 仍以一条 TCP 连接对应一条 tunnel 的方式承载
* 同样遵循 tunnel 一次一用原则

---

## 20. 首版强约束

首版强制要求如下：

1. 必须实现长期 control channel
2. 必须实现 Agent 主动创建 tunnel
3. 必须实现 Server 侧 idle tunnel pool
4. 必须实现 tunnel 一次一用
5. 必须实现 `TrafficOpen / TrafficOpenAck / TrafficData / TrafficClose / TrafficReset`
6. 必须实现控制面 heartbeat
7. 必须实现 tunnel 补充机制
8. 可以暂不实现 tunnel 多次复用
9. 可以暂不实现 datagram
10. 可以暂不实现 session resume

---

## 21. 错误定义建议

```go
package transport

import "errors"

type ErrorKind string

const (
	ErrorKindTransport ErrorKind = "transport"
	ErrorKindProtocol  ErrorKind = "protocol"
	ErrorKindReject    ErrorKind = "reject"
	ErrorKindTimeout   ErrorKind = "timeout"
	ErrorKindClosed    ErrorKind = "closed"
	ErrorKindInternal  ErrorKind = "internal"
)

type Error struct {
	Kind      ErrorKind
	Op        string
	Message   string
	Temporary bool
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return string(e.Kind) + ": " + e.Op + ": " + e.Message + ": " + e.Cause.Error()
	}
	return string(e.Kind) + ": " + e.Op + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var (
	ErrClosed        = errors.New("closed")
	ErrNotReady      = errors.New("not ready")
	ErrSessionClosed = errors.New("session closed")
	ErrTunnelClosed  = errors.New("tunnel closed")
	ErrTunnelBroken  = errors.New("tunnel broken")
	ErrTimeout       = errors.New("timeout")
	ErrProtocol      = errors.New("protocol violation")
	ErrUnsupported   = errors.New("unsupported capability")
)
```

---

## 22. 推荐目录结构

```text
ltfp/
  protocol/
    control/
      codec.go
      frames.go
      messages.go
    traffic/
      codec.go
      frames.go
      messages.go
      state.go

  transport/
    types.go
    errors.go
    session.go
    control_channel.go
    tunnel.go
    tunnel_pool.go

  transport/grpcbinding/
    transport.go
    session.go
    control_channel.go
    tunnel_producer.go
    tunnel_acceptor.go
    stream_adapter.go

  transport/quicbinding/
    transport.go
    session.go
    control_channel.go
    tunnel_producer.go
    tunnel_acceptor.go
    stream_adapter.go

  runtime/
    traffic_meta.go
    traffic_state.go
    traffic_protocol.go
    connector/
      session_manager.go
      tunnel_manager.go
      control_loop.go
    server/
      session_registry.go
      tunnel_pool.go
      dispatcher.go
```

---

## 23. 实施顺序建议

建议按以下顺序落地：

1. 固化 transport abstraction 接口
2. 先实现 `grpc_h2` binding
3. 实现 Agent 侧 tunnel maintainer
4. 实现 Server 侧 tunnel pool 与 dispatcher
5. 在 runtime 层完成 `TrafficOpen/Ack/Data/Close/Reset`
6. 最后再评估 QUIC binding

---

## 24. 规范性结论

本规范的结论如下：

1. LTFP 传输层采用“长期控制通道 + Agent 预建 Tunnel Pool + 单 Tunnel 单 Traffic”的统一模型
2. Transport 层只负责 session、control channel、tunnel 与字节流生命周期
3. `TrafficOpen / TrafficOpenAck / TrafficData / TrafficClose / TrafficReset` 属于 protocol/runtime 层，不属于 transport abstraction
4. Server 不需要在 gRPC/HTTP2 上主动新建 stream，而是消费 Agent 预先建立的 tunnel
5. 一条 tunnel 只承载一次 traffic，traffic 结束后关闭并由 Agent 补充新 tunnel
6. Tunnel 在 transport 层不感知 `open_sent / established` 等业务握手状态
7. 数据面采用 **framed all the way**，`Data` 也属于 runtime/protocol frame
8. Transport 数据面不定义 `DataFrame`，所有数据面协议帧由 runtime/protocol 层自行定义与编解码
9. LTFP 协议层状态机是唯一外部真相源，transport `SessionState` 仅为内部态，必须通过映射表转换
10. runtime/traffic 只允许使用 `service_id`，不得重新依赖 `service_key`
11. `Session` 必须作为 transport 聚合根，统一暴露 control / tunnel producer / acceptor / pool 能力
12. binding 对可选能力若不支持，必须返回 `ErrUnsupported`
13. Session 级能力查询不得使用 `nil` 表示“不支持”或“未就绪”，必须返回显式错误
14. gRPC 数据面 `TunnelEnvelope` 必须是纯字节载体，不得直接承载业务协议字段
15. `TrafficProtocol` 的高频 frame I/O 采用无 `context` 的纯流式接口，超时由 `Tunnel` deadline 控制
16. Runtime 若要复用 `io.Copy`，必须使用 `TrafficDataStream` 一类 framed adapter，而不是直接对 `Tunnel` 做裸字节转发
17. 首版以 `grpc_h2` 为默认 binding，但抽象层必须为后续 QUIC 演进保留空间
18. 控制面必须规避大消息对 heartbeat、认证与 refill 请求造成的队头阻塞，可通过优先级、分块或增量同步降低 HOL 风险
19. `TunnelRefillRequest` 是容量水位提示，不是硬命令；Agent 建 tunnel 必须受速率限制与 inflight 上限约束
20. `TrafficDataStream.Write` 必须按 `max_data_frame_size` 拆包，禁止把超大 payload 直接封成单帧
21. 同一条 tunnel 允许单读单写并发，但多写方必须串行化，避免 frame 字节交错
22. 长驻 idle 池的 tunnel 应结合 keepalive/probe 与 TTL 一起治理僵尸连接
