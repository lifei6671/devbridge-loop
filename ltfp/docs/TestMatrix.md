# LTFP 测试矩阵（协议库层）

本文档只覆盖 `ltfp` 协议库本身，不包含 `agent-core` 与 `cloud-bridge` 业务联调实现。

## 单元测试矩阵

| 维度 | 当前覆盖包 | 说明 |
| --- | --- | --- |
| schema | `pb` | 协议对象、枚举常量、forward/full-sync 结构 |
| validate | `validate` | 字段合法性、scope、policy_json、version、forward/full-sync 校验 |
| codec | `codec` | 控制面/数据面 JSON 编解码、binding 承载一致性 |
| consistency | `consistency` | 去重键、版本比较、ACK 构造、重连计划 |
| negotiation | `negotiation` | 特性交集与 pb 适配器 |
| session | `session` | 握手顺序、epoch 权威、状态机、心跳超时 |
| registry | `registry` | canonical/runtime registry、索引、审计、快照 |
| routing | `routing` | connector/external/hybrid route resolve 与 `TrafficOpen` 构造 |
| discovery | `discovery` | provider 查询、缓存模式、安全策略、endpoint 选路与拨号守卫 |
| export | `export` | export 准入、endpoint 生成、projection reconcile |
| ingress | `ingress` | Host/Authority/Path/SNI/listen_port 匹配与端口冲突校验 |
| fallback | `fallback` | `pre_open_only` 判定与 post-open 拒绝策略 |
| health | `health` | endpoint -> service 健康聚合 |
| observe | `observe` | trace/error/reject/fallback 事件记录与指标聚合 |
| adapter | `adapter` | 本地运行态到 `Publish/Unpublish/HealthReport` 适配 |
| errors | `errors` | 错误码封装、错误链匹配 |
| testkit | `testkit` | golden fixtures（通过其它包测试间接验证） |

说明：

- 以上覆盖仍然是“协议库层”验证，不替代 `agent-core` / `cloud-bridge` 端到端网络行为测试。
- 双端接入后需要新增兼容性矩阵，验证共享协议库升级不会破坏实际联调链路。

## 集成测试矩阵（协议库内）

| 流程 | 当前覆盖 |
| --- | --- |
| 握手链路（HELLO/WELCOME/AUTH/AUTH_ACK/HEARTBEAT） | `examples/interop/interop_test.go` |
| full-sync 请求与快照 | `examples/interop/sync_flow_test.go` |
| publish/unpublish ACK 语义 | `examples/interop/sync_flow_test.go` + `consistency` |
| health report | `examples/interop/sync_flow_test.go` |
| route resolve（connector/external/hybrid） | `routing/resolver_test.go` |
| discovery cache 与安全策略 | `discovery/manager_test.go` + `discovery/*_test.go` |
| export 准入与 reconcile | `export/export_test.go` |
| fallback pre-open only | `fallback/policy_test.go` + `routing/resolver_test.go` |
| binding parity（http/masque） | `codec/binding_codec_test.go` |
| schema 版本兼容 | `validate/versioning_test.go` |

说明：

- 协议库层只验证消息语义与决策逻辑，不验证真实网络代理、listener 生命周期与 provider 实例可用性。

## 执行入口

```bash
cd ltfp
make test-unit
make test-integration
make test-parity
make test-compat
make test-regression
```
