# LTFP 测试矩阵（协议库 + 传输层）

本文档覆盖 `ltfp` 模块内协议库与一期 transport/runtime/binding 基线，不包含 `agent-core` 与 `cloud-bridge` 业务联调实现。

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
