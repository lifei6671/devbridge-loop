# LTFP 协议共享库

`ltfp` 是 DevBridge Loop 的协议源模块，负责承载线协议模型与通用能力。

## 模块边界

- 承载控制面与数据面的协议对象（schema）
- 承载协议状态常量与错误码
- 承载协议层编解码与字段校验
- 承载协商算法与兼容性测试夹具
- 承载协议库级 registry / resolver / discovery / export / fallback / observe 辅助能力

## 非目标

- 不承载 `agent-core` 或 `cloud-bridge` 的业务运行态持久化实现
- 不承载 ingress listener、provider 实现、forwarder 实现
- 不反向依赖 `agent-core/internal` 或 `cloud-bridge/internal`

## 目录约定

- `pb/`：协议对象定义
- `errors/`：错误码与协议错误封装
- `codec/`：协议编解码实现
- `validate/`：字段与语义校验
- `negotiation/`：协商算法与能力交集
- `session/`：握手链路与会话状态机
- `consistency/`：幂等键、版本比较、ACK 构造与重连同步计划
- `registry/`：canonical/runtime 注册表与快照查询
- `routing/`：connector/external/hybrid 路由解析
- `discovery/`：provider 查询、缓存、安全策略、路由抉择与拨号守卫
- `export/`：export 准入、endpoint 生成与 projection reconcile
- `fallback/`：hybrid pre-open fallback 策略判定
- `ingress/`：L7/SNI/listen_port 匹配与 dedicated 端口校验
- `observe/`：结构化事件记录与最小指标聚合
- `adapter/`：本地运行态到 LTFP 消息转换辅助
- `testkit/`：golden fixtures 与跨模块测试夹具
- `proto/`：协议 `.proto` 定义
- `pb/gen/`：由 `proto/` 生成的 Go 绑定
- `docs/TestMatrix.md`：协议库测试矩阵
- `docs/ReleaseAndRollback.md`：发布门槛与回滚策略

## 开发约束

- 共享库变更优先更新协议对象，再更新使用方
- 新增字段必须补兼容性说明与测试
- 导出函数必须有中文函数级注释

## Codegen 流程

1. 在 `proto/` 修改或新增 `.proto` 文件。
2. 在 `ltfp/` 目录执行 `make proto` 生成 Go 绑定。
3. 执行 `go test ./...` 校验协议对象、校验器与编解码器兼容性。

常用命令：

```bash
cd ltfp
make proto
make test
make test-regression
make verify-release
```
