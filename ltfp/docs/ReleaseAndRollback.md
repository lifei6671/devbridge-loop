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

