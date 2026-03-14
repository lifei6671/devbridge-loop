# LTFP 测试矩阵（协议库 + 传输层）

本文档覆盖 `ltfp` 模块内协议库与一期 transport/runtime/binding 基线。
为对齐 `docs/Agent-and-Bridge-ExecutionChecklist.md` 的 A13 项，文末补充了 Agent/Bridge 对齐用例的 case 编号与结果索引。

## 单元测试矩阵

| 维度 | 当前覆盖包 | 说明 |
| --- | --- | --- |
| schema | `pb` | 协议对象、枚举常量、forward/full-sync 结构 |
| validate | `validate` | 字段合法性、scope、policy_json、version、forward/full-sync 校验 |
| codec | `codec` | 控制面/数据面 JSON 编解码、binding 承载一致性 |
| consistency | `consistency` | 去重键、版本比较、ACK 构造、重连计划 |
| negotiation | `negotiation` | 特性交集与 pb 适配器 |
| session | `session` | 握手顺序、epoch 权威、状态机、心跳超时 |
| runtime framed data plane | `runtime` | `Open/OpenAck/Data/Close/Reset` 帧语义、stream adapter 缓冲与并发读写 |
| transport abstraction | `transport` | session/tunnel 状态机、error model、capability query、pool/refill、fragmentation、heartbeat |

说明：

- `make test-unit` 会运行以上协议库与传输层核心单测。

## 绑定测试矩阵

| binding | 当前覆盖包 | 重点场景 |
| --- | --- | --- |
| `grpc_h2` | `transport/grpcbinding` | control/tunnel 读写、deadline/cancel、reset/close、keepalive、消息上限与 transport option |
| `tcp_framed` | `transport/tcpbinding` | 控制帧分块重组、优先级写队列、tunnel 生命周期、`SetDeadline*`/`Reset`/`CloseWrite` 语义 |

说明：

- `make test-binding` 运行两种 binding 的完整测试包。

## Parity、集成与压测

| 类型 | 当前入口 | 说明 |
| --- | --- | --- |
| parity smoke | `make test-parity` | 运行 `codec` parity 与两种 binding 的同层语义回归 |
| integration | `make test-integration` | `examples/interop` 握手、full-sync、publish/unpublish、health 等链路 |
| pressure smoke | `make test-pressure` | 基准场景烟测：小包/大包、空闲维持、突发 refill（`-benchtime=1x`） |

说明：

- parity 当前为 smoke 级别，双端接入后会继续补充真实端到端流量场景对齐用例。

## 发布门槛

`scripts/verify_release.sh` 默认按顺序执行：

1. `make test-unit`
2. `make test-binding`
3. `make test-integration`
4. `make test-parity`
5. `make test-pressure`
6. `make test-compat`

任一步失败即阻塞 release verification。

## Agent/Bridge A13 对齐用例（case 编号与结果）

执行日期：`2026-03-13`

| Case ID | 类型 | 场景 | 测试入口 | 结果 |
| --- | --- | --- | --- | --- |
| `AB-UT-001` | unit | 状态机（session draining/stale/active） | `agent-core/runtime/agent/session/manager_test.go:TestManagerPropagatesDrainingAndResumeToTunnelPool` | PASS |
| `AB-UT-002` | unit | 并发边界（max_inflight + reconcile 串行） | `agent-core/runtime/agent/tunnel/producer_test.go:TestProducerOpenBatchRespectsMaxInflight`、`agent-core/runtime/agent/tunnel/manager_test.go:TestManagerHandleSessionStateDrainingSerializesWithReconcile` | PASS |
| `AB-UT-003` | unit | timeout/cancel（dial cancel、open timeout） | `agent-core/runtime/agent/traffic/opener_test.go:TestOpenerHandleDialCanceled`、`cloud-bridge/runtime/bridge/connectorproxy/dispatcher_test.go:TestOpenHandshakeTimeoutFallbackClose` | PASS |
| `AB-UT-004` | unit | ack 去重（event_id/request_id） | `agent-core/runtime/agent/control/ack_deduper_test.go:TestAckDeduperSeenWithinTTL`、`agent-core/runtime/agent/control/refill_handler_test.go:TestRefillHandlerDeduplicateAndClamp` | PASS |
| `AB-UT-005` | unit | 参数回落（default + override） | `agent-core/runtime/agent/app/config_test.go:TestDefaultConfigTunnelPoolValues`、`agent-core/runtime/agent/app/bootstrap_test.go:TestBootstrapWithOptionsTunnelPoolOverride` | PASS |
| `AB-IT-001` | integration | 握手与控制面链路 | `ltfp/examples/interop`（`make test-integration`） | PASS |
| `AB-IT-002` | integration | 心跳调度与退出语义 | `agent-core/runtime/agent/session/heartbeat_test.go:TestHeartbeatSchedulerRunSendsUntilContextCanceled` | PASS |
| `AB-IT-003` | integration | pool（no-idle、wait、refill） | `cloud-bridge/runtime/bridge/connectorproxy/dispatcher_test.go:TestTunnelAcquirerNoIdleTimeoutAndRefill`、`TestTunnelAcquirerWaitsForIncomingIdle` | PASS |
| `AB-IT-004` | integration | open/ack/data/close/reset | `cloud-bridge/runtime/bridge/connectorproxy/dispatcher_test.go:TestDispatcherDispatchSuccessLifecycle`、`agent-core/runtime/agent/traffic/relay_test.go:TestStreamRelayBidirectionalAndClose` | PASS |
| `AB-IT-005` | integration | late ack 丢弃 | `cloud-bridge/runtime/bridge/connectorproxy/dispatcher_test.go:TestDispatcherOpenAckTimeoutDropsLateAck` | PASS |
| `AB-IT-006` | integration | hybrid pre-open fallback | `cloud-bridge/runtime/bridge/routing/executor_test.go:TestPathExecutorHybridFallbackPreOpenNoTunnel`、`TestPathExecutorHybridFallbackPreOpenWithTunnel` | PASS |
| `AB-PT-001` | pressure smoke | 突发 no-idle | `cloud-bridge/runtime/bridge/connectorproxy/dispatcher_test.go:TestTunnelAcquirerBurstNoIdleFastFail` | PASS |
| `AB-PT-002` | pressure smoke | refill 节流 | `agent-core/runtime/agent/tunnel/producer_test.go:TestTokenBucketConsumeOrDelayRespectsRateLimit` | PASS |
| `AB-PT-003` | pressure smoke | 慢读回压 | `agent-core/runtime/agent/traffic/relay_test.go:TestStreamRelayBackpressureTimeout` | PASS |
| `AB-PT-004` | pressure smoke | 大包 relay | `agent-core/runtime/agent/traffic/relay_test.go:TestStreamRelayLargePayloadFragmentation` | PASS |

验证命令（本次执行）：

```bash
cd agent-core && go test ./runtime/agent/control ./runtime/agent/session ./runtime/agent/tunnel ./runtime/agent/traffic -count=1
cd cloud-bridge && go test ./runtime/bridge/connectorproxy ./runtime/bridge/routing ./runtime/bridge/control ./runtime/bridge/registry -count=1
cd ltfp && make test-integration
```

## 执行入口

```bash
cd ltfp
make test-unit
make test-binding
make test-integration
make test-parity
make test-pressure
make test-compat
make test-regression
```
