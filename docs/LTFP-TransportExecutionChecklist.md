# LTFP 传输层执行清单（v2.1 一期）

## 一、文档目的

本清单用于承接 [LTFP-TransportAbstraction.md](./LTFP-TransportAbstraction.md)，把传输层规范从“抽象定义”推进到“可编码、可联调、可验收”的工程执行项。

本文档与 [LTFP-FullExecutionChecklist.md](./LTFP-FullExecutionChecklist.md) 的关系如下：

- `LTFP-FullExecutionChecklist.md` 承载 LTFP 全量能力落地。
- 本文档只承载传输层专项工作，包括 transport abstraction、runtime framed data plane、binding、session/tunnel control loop、测试与发布门槛。

口径说明：

- 本轮勾选以 `ltfp` 传输层库与 binding 实现完成为准。
- `agent-core` / `cloud-bridge` 的深度业务接入，不作为本清单的一期阻塞项。

---

## 二、本期范围

### 1. 一期必须交付

- `ltfp/transport` 抽象层与统一错误模型
- `ltfp/runtime` 最小数据面 frame 与 `TrafficDataStream` 适配器
- `grpc_h2` binding
- `tcp_framed` binding
- Agent 侧 session opener、control loop、tunnel producer、tunnel maintainer
- Server 侧 session registry、control loop、tunnel acceptor、tunnel pool、dispatcher
- 观测性、诊断、binding parity 测试与最小联调样例

### 2. 二期交付

- `quic_native` binding
- `h3_stream` binding
- datagram
- session resume
- tunnel 多次复用与更复杂的 pool QoS
- 更激进的 zero-copy / fast-path 优化

### 3. 明确不做

- 业务路由、discovery、export、hybrid 等上层能力细节
- mid-stream failover
- 跨 server 集群一致性
- 首版按服务拆分独立 tunnel pool

---

## 三、已冻结设计前提

- [x] `docs/LTFP-TransportAbstraction.md` 是传输层真相源。
- [x] 首版 binding 范围固定为 `grpc_h2` 与 `tcp_framed`。
- [x] `quic_native`、`h3_stream`、datagram、session resume 统一进入二期。
- [x] 传输层采用“长期 control channel + Agent 预建 tunnel pool + 单 tunnel 单 traffic”模型。
- [x] 数据面采用 **framed all the way**，`TrafficData` 也属于 runtime/protocol frame。
- [x] `TrafficOpen / OpenAck / Data / Close / Reset` 属于 runtime/protocol，不属于 transport abstraction。
- [x] `Session` 是 transport 聚合根；能力查询必须返回显式错误，禁止 `(nil, nil)`。
- [x] `TunnelRefillRequest` 是容量水位提示，不是“立刻并发建 delta 条 tunnel”的硬命令。

---

## 四、执行清单

### T0. 真相源、命名与目录冻结

- [x] 明确本清单以 [LTFP-TransportAbstraction.md](./LTFP-TransportAbstraction.md) 为唯一规范依据
- [x] 明确 binding 优先级：一期 `grpc_h2`、`tcp_framed`；二期 `quic_native`、`h3_stream`
- [x] 冻结目录命名：`ltfp/transport/`、`ltfp/runtime/`、`ltfp/transport/grpcbinding/`、`ltfp/transport/tcpbinding/`
- [x] 冻结 binding type 常量：`grpc_h2`、`tcp_framed`、`quic_native`、`h3_stream`
- [x] 建立“规范章节 -> 代码包 -> 测试用例”映射表，避免文档与代码脱节

映射表（首版）：

| 规范章节 | 代码包 / 文件 | 测试用例 |
| --- | --- | --- |
| Session 聚合根与状态机 | `ltfp/transport/session.go` | `ltfp/transport/session_test.go` |
| ControlChannel 抽象与语义 | `ltfp/transport/control_channel.go` | `ltfp/transport/grpcbinding/control_channel_test.go`、`ltfp/transport/tcpbinding/control_channel_test.go` |
| Tunnel 抽象与语义 | `ltfp/transport/tunnel.go`、`ltfp/transport/grpcbinding/tunnel.go`、`ltfp/transport/tcpbinding/tunnel.go` | `ltfp/transport/grpcbinding/tunnel_test.go`、`ltfp/transport/tcpbinding/tunnel_test.go` |
| TunnelPool / Refill / 僵尸治理 | `ltfp/transport/tunnel_pool.go`、`ltfp/transport/refill_controller.go` | `ltfp/transport/tunnel_pool_test.go`、`ltfp/transport/refill_controller_test.go` |
| Runtime 数据面帧协议 | `ltfp/runtime/protocol.go`、`ltfp/runtime/types.go` | `ltfp/runtime/protocol_test.go` |
| TrafficDataStream 适配器 | `ltfp/runtime/stream.go` | `ltfp/runtime/stream_test.go` |
| `grpc_h2` binding | `ltfp/transport/grpcbinding/*.go` | `ltfp/transport/grpcbinding/*_test.go` |
| `tcp_framed` binding | `ltfp/transport/tcpbinding/*.go` | `ltfp/transport/tcpbinding/*_test.go` |

验收标准：

- 目录、包名、binding 名称一次定清
- 后续实现不再出现“文档一套、代码一套”的命名漂移

### T1. `ltfp/transport` 抽象层骨架

- [x] 新建 `ltfp/transport/` 包及最小文件骨架：`types.go`、`errors.go`、`session.go`、`control_channel.go`、`tunnel.go`、`tunnel_pool.go`
- [x] 落地 `BindingType`、`BindingInfo`、`SessionMeta`、`TunnelMeta`、`SessionState`、`TunnelState`
- [x] 落地 `ControlFrame`、`ControlChannel`、`Tunnel`、`TunnelProducer`、`TunnelAcceptor`、`TunnelPool`、`Session`
- [x] 固定错误模型：`ErrUnsupported`、`ErrNotReady`、`ErrClosed`、`ErrSessionClosed`、`ErrTunnelClosed`、`ErrTunnelBroken`、`ErrTimeout`
- [x] 用编译期断言与最小示例验证接口依赖方向不反转

验收标准：

- 传输层公共接口稳定可引用
- runtime 与 binding 能只依赖 `ltfp/transport`

### T2. Session 聚合根与状态机

- [x] 实现 `Session` 生命周期：`idle -> connecting -> connected -> control_ready -> authenticated -> draining/failed/closed`
- [x] 实现 transport `SessionState` 到 LTFP 协议态的固定映射规则
- [x] 实现 `Control()`、`TunnelProducer()`、`TunnelAcceptor()`、`TunnelPool()` 的显式错误返回
- [x] 落地 `Open(ctx)`、`Close(ctx, reason)`、`Done()`、`Err()` 的最小行为约束
- [x] 为重试期 `connecting -> failed -> connecting` 场景保留 `last_error` 观测口径

验收标准：

- 外部只看协议态，内部仍可保留 transport 细分状态
- session 失败、关闭、重试的边界一致且可观测

### T3. Control Channel 与控制面治理

- [x] 实现 `ControlChannel` 的读写约束与关闭语义
- [x] 实现控制面消息优先级队列或等价调度机制，避免 heartbeat、认证、refill 被大消息 HOL 阻塞
- [x] 支持大体积 `PublishService` / `TunnelPoolReport` 的分块、分页或增量发送策略
- [x] 为 heartbeat 建立发送周期、丢失阈值、判死逻辑与超时口径
- [x] 为控制面错误建立统一日志字段：`session_id`、`session_epoch`、`binding`、`last_error`、`retryable`

验收标准：

- heartbeat 不会因控制面大消息排队产生虚假超时
- 控制面断开、认证失败、重试退避都能结构化观测

### T4. Tunnel、Pool、Refill 与背压控制

- [x] 实现 `Tunnel` 状态机：`opening -> idle -> reserved -> active -> closing/closed/broken`
- [x] 实现 `TunnelPool`：`PutIdle`、`Acquire`、`Remove`、`IdleCount`、`InUseCount`
- [x] 实现 `TunnelPoolReport` 与 `TunnelRefillRequest` 的最小事件模型
- [x] 实现 `min_idle_tunnels`、`max_idle_tunnels`、`idle_tunnel_ttl`、`acquire_timeout`
- [x] 实现 `max_inflight_tunnel_opens`、`tunnel_open_rate_limit`、`tunnel_open_burst`
- [x] 实现 Agent 侧平滑补池控制循环，禁止把 refill delta 解释为瞬时硬命令
- [x] 实现僵尸 tunnel 治理：TTL、keepalive/probe、broken 剔除

验收标准：

- tunnel pool 在高并发下不会因 refill 形成惊群
- idle tunnel 不会因僵尸连接长期污染池状态

### T5. `ltfp/runtime` 最小数据面协议

- [x] 新建 `ltfp/runtime/` 包并落地 `TrafficMeta`、`TrafficState`、`TrafficFrameType`、`TrafficFrame`、`TrafficProtocol`
- [x] 固定最小帧集合：`Open`、`OpenAck`、`Data`、`Close`、`Reset`
- [x] 保证 runtime 只使用 `service_id`，不重新依赖 `service_key`
- [x] 为 `TrafficProtocol` 实现统一 `ReadFrame` / `WriteFrame` 入口
- [x] 固定 `TrafficProtocol` 无 `context.Context`，超时统一走 `Tunnel.SetDeadline*`
- [x] 落地单 tunnel 并发边界：单读单写允许，多写必须串行化，多读不保证安全

验收标准：

- 数据面 frame 语义不泄露到 transport 抽象层
- runtime framed I/O 语义与 transport 字节流语义稳定对齐

### T6. `TrafficDataStream` Framed Adapter

- [x] 实现 `TrafficDataStream`，对上提供标准 `io.Reader` / `io.Writer`
- [x] `Write(p)` 按 `max_data_frame_size` 强制拆包，并保持原始字节顺序
- [x] `Read(p)` 在 `Data` frame 大于 `p` 时维护内部缓冲，禁止截断
- [x] 收到 `Close` 时返回 `io.EOF`
- [x] 收到 `Reset` 时返回显式错误
- [x] 适配器支持一侧读、一侧写并发，以兼容双向 `io.Copy`
- [x] `grpc_h2` 首版默认 `max_data_frame_size` 控制在安全值内，避免触发单消息大小限制

验收标准：

- 上层代理逻辑可通过适配器安全复用 `io.Copy`
- 超大 payload 不会被封成单帧导致 gRPC / TCP framed 崩溃

### T7. `grpc_h2` Binding 一期实现

- [x] 定义 `ControlChannel` 与 `TunnelStream` 的 gRPC proto / service
- [x] 定义 `TunnelEnvelope { bytes payload = 1; }`，禁止在 envelope 中直接暴露业务协议字段
- [x] 实现 `ltfp/transport/grpcbinding/transport.go`
- [x] 实现 `session.go`、`control_channel.go`、`tunnel_producer.go`、`tunnel_acceptor.go`、`stream_adapter.go`
- [x] 实现 Agent 主动打开 `TunnelStream`，Server 侧接收后写入 idle pool
- [x] 实现 gRPC keepalive、最大消息大小、deadline 与 cancel 语义映射
- [x] 实现对象池、缓冲复用与可选 fast-path，但不得把 gRPC 私有类型泄露到公共接口

验收标准：

- `grpc_h2` 可跑通 control + data + pool refill 的完整闭环
- 在不破坏抽象边界的前提下具备首版可接受的性能

### T8. `tcp_framed` Binding 一期实现

- [x] 定义 `tcp_framed` 的线格式：长度前缀、帧边界、控制面与数据面承载方式
- [x] 实现 `ltfp/transport/tcpbinding/transport.go`
- [x] 实现 `session.go`、`control_channel.go`、`tunnel_producer.go`、`tunnel_acceptor.go`、`stream_adapter.go`
- [x] 实现 TCP listener / dialer 与连接生命周期治理
- [x] 实现 `CloseWrite`、`Reset`、`SetDeadline*` 的能力映射与 `ErrUnsupported` 兜底
- [x] 与 `grpc_h2` 对齐控制面、pool、traffic 的语义，不得出现 binding 特判分叉

验收标准：

- `tcp_framed` 与 `grpc_h2` 在传输层语义上保持一致
- 同一组 runtime / testkit 能同时覆盖两种 binding

### T9. Agent 侧运行环

- [ ] 实现 session opener、认证后 tunnel maintainer 与自动补池循环
- [ ] 实现控制面 heartbeat、pool report、refill request 处理
- [ ] 实现 tunnel 建连速率限制、inflight 限制与退避重试
- [ ] 实现数据面 `Open / OpenAck / Data / Close / Reset` 的最小处理链路
- [ ] 实现 idle tunnel TTL、keepalive/probe、broken 清理

验收标准：

- Agent 能在 session 建立后稳定维持 idle pool
- 高峰期不会因暴力建 tunnel 造成瞬时资源冲击

### T10. Server 侧运行环

- [ ] 实现 session registry 与 Server 侧协议态视图
- [ ] 实现 tunnel acceptor、idle pool 管理与 dispatcher
- [ ] 实现 `Acquire(ctx)`、短等待、超时快速失败与 refill 触发
- [ ] 实现 `TrafficOpen` 发送、`OpenAck` 等待、超时 broken、traffic 清理
- [ ] 实现 control channel 断开、epoch 切换、session stale/draining 对 pool 的影响

验收标准：

- Server 能稳定消费 Agent 预建 tunnel
- traffic 打开失败、session 失效、pool 紧张时都能按规范处理

### T11. 观测性、诊断与基准

- [x] 输出 session、control、tunnel、pool、traffic 的结构化日志
- [x] 输出关键指标：heartbeat RTT、idle/in-use 数量、refill 速率、open timeout、reset 数量、broken 数量
- [x] 输出 `last_error`、`binding`、`session_epoch`、`tunnel_id`、`traffic_id` 等定位字段
- [x] 为 `grpc_h2` 与 `tcp_framed` 建立最小 benchmark：小包流量、大包流量、空闲维持、突发 refill
- [x] 记录 HOL、fragmentation、并发写串行化、keepalive 行为的诊断样例（见 [LTFP-TransportDiagnosticsSamples.md](./LTFP-TransportDiagnosticsSamples.md)）

验收标准：

- 出现超时、掉线、僵尸 tunnel、池耗尽时可快速定位
- 两种 binding 的性能与稳定性差异可被量化

### T12. 测试矩阵与发布门槛

- [x] 单元测试：state machine、error model、capability query、pool、rate limit、fragmentation、adapter 缓冲
- [x] 绑定测试：`grpc_h2` / `tcp_framed` 的 control channel、tunnel、deadline、reset、close 语义
- [x] parity 测试：同一组 traffic 场景在两种 binding 下得到一致结果
- [x] 集成测试：握手、认证、heartbeat、pool refill、open/ack/data/close/reset、session stale、idle ttl、zombie cleanup
- [x] 压测场景：突发 acquire、refill 惊群、超大 payload 拆包、控制面 HOL
- [x] 发布门槛：单测、集测、parity、压测样例全部通过后方可进入接入阶段

验收标准：

- `grpc_h2` 与 `tcp_framed` 语义一致、测试覆盖完整
- 一期上线前已经覆盖主要故障模式，而不是靠接入阶段兜底

### T13. 二期预留项

- [ ] 实现 `quic_native` binding
- [ ] 实现 `h3_stream` binding
- [ ] 评估 datagram 抽象与能力位
- [ ] 评估 session resume
- [ ] 评估 tunnel 多次复用、按服务 QoS 保留池、更多 fast-path 优化

验收标准：

- 二期能力不影响一期抽象稳定性
- 新 binding 接入时不需要推翻一期 transport/runtime 设计

---

## 五、建议执行顺序

1. 先做 `T0-T2`，冻结抽象、目录、状态机与错误模型。
2. 再做 `T3-T6`，把 control / tunnel / pool / traffic framed adapter 的核心语义做稳。
3. 优先完成 `T7`，先跑通 `grpc_h2` 一期闭环。
4. 再完成 `T8`，建立 `tcp_framed` 与 `grpc_h2` 的 parity。
5. 接着做 `T9-T12`，补齐 agent/server 运行环、观测性、测试与发布门槛。
6. 最后推进 `T13`，处理 `quic_native`、`h3_stream`、datagram、resume 等二期能力。

---

## 六、分阶段交付物

### 阶段一：抽象与公共库

- `ltfp/transport`
- `ltfp/runtime`
- 公共错误模型、状态机、pool 与 framed adapter

### 阶段二：首个标准 binding

- `grpc_h2` binding
- Agent / Server 最小联调闭环

### 阶段三：第二个一期 binding

- `tcp_framed` binding
- binding parity 测试与 benchmark

### 阶段四：稳定性与发布门槛

- 观测性、诊断、压测、回归矩阵
- 进入 agent / bridge 深度接入前的发布基线

---

## 七、本期验收口径

### 功能验收

- `grpc_h2` 与 `tcp_framed` 都能跑通完整 session/control/tunnel/traffic 闭环
- transport abstraction 与 runtime framed data plane 边界清晰，未发生 binding 污染
- Agent 与 Server 都能稳定维护 pool、处理 refill、清理 broken tunnel

### 工程验收

- 仓库存在独立 `ltfp/transport` 与 `ltfp/runtime` 包
- 两种一期 binding 均有独立实现与统一测试入口
- 文档、代码、测试三者对命名、状态机、错误码的口径一致

### 非功能验收

- 控制面不会因大消息产生明显 HOL 误判
- 超大 payload 会被安全拆包，不会触发单帧灾难性失败
- 并发写不会发生 frame 字节交错
- idle tunnel 能通过 TTL + keepalive/probe 识别并清理僵尸连接
