# LTFP 发布门槛与回滚策略（协议库层）

## 升级说明约定

每次协议库变更都必须更新以下内容：

1. schema 变更说明：新增/删除/重命名字段。
2. 兼容性说明：主版本兼容策略、minor 版本支持范围。
3. 必填变更说明：新增必填字段的默认值策略与迁移窗口。
4. 错误码变更说明：新增错误码和行为影响范围。

## 最小发布门槛

发布前必须通过：

1. `make -C ltfp proto`
2. `make -C ltfp test-regression`
3. `make -C ltfp verify-release`

跨模块门槛：

- 当准备对接或发布给 `agent-core` / `cloud-bridge` 使用时，开启 `LTFP_REQUIRE_CROSS_MODULE=1` 执行：

```bash
cd ltfp
LTFP_REQUIRE_CROSS_MODULE=1 ./scripts/verify_release.sh
```

说明：

- 默认不强制执行跨模块测试，是为了保持协议库独立迭代效率。
- 进入联调阶段后，跨模块测试必须成为发布阻塞项。

## 回滚策略

发布失败或联调回归时按以下顺序回滚：

1. 回滚 `proto/` 与 `pb/gen/` 到上一稳定版本。
2. 回滚 `validate/` 与 `consistency/` 的行为变更，保留兼容错误码。
3. 保留向后兼容解析：允许读取新字段但不依赖新字段决策。
4. 重新执行 `make -C ltfp test-regression` 验证回滚后行为稳定。

回滚验收：

- 旧版本调用方可无代码改动继续编译与运行。
- 现网已发送 payload 不因字段回退导致解码失败。
- 关键错误码语义保持稳定，不出现错误分类漂移。

## QUIC 接入灰度与回退补充（跨模块）

当 `ltfp` 的 `quic_native` binding 与 `agent-core` / `cloud-bridge` 联动发布时，
补充遵循以下规则：

1. 默认路径保持不变，只允许通过显式配置把单个 Agent 切到 `quic_native`。
2. Bridge 侧先开 QUIC listener，再逐个切 Agent；不要先全量切 Agent 再补 Bridge。
3. 快速回退优先走 Agent 侧：把 `bridge_transport` 从 `quic_native` 改回 `grpc_h2`，
   并把目标地址切回 `grpc_h2_listen_addr`，避免为回退再改 Bridge 运行参数。
4. 是否彻底关闭 Bridge QUIC listener 应作为单独运维动作处理，不耦合到单 Agent 回退。

详细运行示例与操作步骤以
[Agent-Bridge-QUIC-Implementation-Plan.md](../../docs/Agent-Bridge-QUIC-Implementation-Plan.md)
中的“QUIC 配置示例”“灰度、回退与运维检查项”为准。
