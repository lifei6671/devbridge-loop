# LTFP 协议共享库

`ltfp` 是 DevBridge Loop 的协议真相源模块，负责承载线协议模型与通用能力。

## 模块边界

- 承载控制面与数据面的协议对象（schema）
- 承载协议状态常量与错误码
- 承载协议层编解码与字段校验
- 承载协商算法与兼容性测试夹具

## 非目标

- 不承载 `agent-core` 或 `cloud-bridge` 的运行态存储
- 不承载 ingress listener、provider 实现、forwarder 实现
- 不反向依赖 `agent-core/internal` 或 `cloud-bridge/internal`

## 目录约定

- `pb/`：协议对象定义
- `errors/`：错误码与协议错误封装
- `codec/`：协议编解码实现
- `validate/`：字段与语义校验
- `negotiation/`：协商算法与能力交集
- `testkit/`：golden fixtures 与跨模块测试夹具
- `proto/`：保留给后续 `.proto` 真正落地

## 开发约束

- 共享库变更优先更新协议对象，再更新使用方
- 新增字段必须补兼容性说明与测试
- 导出函数必须有中文函数级注释
