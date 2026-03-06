# 一、目标

构建一套面向研发联调的 **DevLoop 开发闭环系统**，包含：

1. **Win11 本地桌面端 `dev-agent`**

   * 使用 **Tauri** 实现桌面 UI
   * 负责本地服务注册、服务发现、出口代理、入口回流接收
   * 与 `cloud-bridge` 建立长连接

2. **云端 `cloud-bridge`**

   * 负责 tunnel 管理
   * 负责有效接管关系维护
   * 作为 base 环境访问 dev 服务的云端入口

3. **业务系统 `agent registry` 接入**

   * 在现有 `nacos / local` 基础上新增 `agent`
   * 本地服务先注册到 `dev-agent`
   * 由 `dev-agent` 同步已接管服务到 `cloud-bridge`

4. **支持 HTTP / gRPC 双协议**

   * 两者独立端口
   * 两条代理链路分开实现

---

# 二、最新架构结论

## 1. 核心模型

正确模型是：

```text
本地应用 -> dev-agent -> cloud-bridge -> bridge入口/注册中心
```

而不是：

```text
本地应用 -> 直接注册 nacos
```

也不是：

```text
dev-agent 启动时预先声明会代理哪些服务
```

因为：

* `dev-agent` 启动时并不知道本地最终会启动哪些服务
* 只有本地应用实际注册到 `dev-agent` 后，`dev-agent` 才知道当前“已接管哪些服务”

---

## 2. 角色划分

### 1）本地业务服务

职责：

* 启动后注册到 `dev-agent`
* 通过 `dev-agent` 做服务发现
* 通过 `dev-agent` 做 HTTP / gRPC 出口访问

### 2）Win11 Tauri 桌面 `dev-agent`

职责：

* 提供桌面 UI
* 提供本地 agent 后端服务
* 建立到 `cloud-bridge` 的长连接
* 接收本地服务注册/反注册
* 维护本地已接管服务表
* 同步已接管服务到 `cloud-bridge`
* 提供出口代理
* 接收 bridge 回流流量并转发到 localhost 服务
* 提供状态查看、日志、调试与操作入口

### 3）云端 `cloud-bridge`

职责：

* 维护 tunnel 会话
* 维护有效接管关系
* 接收 base 环境入口流量
* 根据 `(env, serviceName, protocol)` 路由到对应 `dev-agent`

---

# 三、组件拆分

---

## A. Win11 Tauri dev-agent

建议拆成 3 层：

### A1. Tauri 前端 UI

建议技术：

* Tauri
* React
* TypeScript
* 可选 UI：shadcn/ui 或 Material 风格组件

职责：

* 展示 agent 状态
* 展示 tunnel 状态
* 展示本地已接管服务
* 展示同步到 cloud-bridge 的有效接管关系
* 查看日志
* 管理配置
* 手动重连、刷新、摘除某个注册项
* 展示 HTTP / gRPC 请求调试信息

### A2. Tauri Rust Host

职责：

* 承载桌面应用能力
* 管理窗口、托盘、开机启动、配置文件路径、日志目录
* 调用后端 agent 进程
* 暴露 Tauri command 给前端 UI

### A3. Agent Core

建议优先用 **Go** 实现核心 agent 服务，原因是你业务侧本身是 Go 生态，更容易复用：

职责：

* register/unregister 接口
* discovery 接口
* http/grpc proxy
* tunnel client
* state store
* sync worker
* local forwarder

推荐方式：

* `Tauri UI + Rust Host + Go Agent Core`
* Rust 负责桌面壳与调用
* Go 负责真正 agent 功能

---

## B. cloud-bridge

建议 Go 实现，职责：

* tunnel server
* active intercept store
* ingress http
* ingress grpc
* route lookup
* metrics / admin api

---

## C. 业务系统 agent registry adapter

职责：

* 在现有服务发现抽象层增加 `agent`
* 本地应用注册到 `dev-agent`
* 通过 `dev-agent` 做发现
* 通过 `dev-agent` 做出口代理

---

# 四、核心对象定义

---

## 1. LocalRegistration

表示本地应用注册到 dev-agent 的信息。

字段建议：

* `serviceName`
* `env`
* `instanceId`
* `httpPort`
* `grpcPort`
* `metadata`
* `weight`
* `healthy`
* `registerTime`

---

## 2. ActiveIntercept

表示 dev-agent 已同步到 bridge 的有效接管关系。

字段建议：

* `env`
* `serviceName`
* `protocol`
* `tunnelId`
* `rdName`
* `targetPort`
* `status`
* `updatedAt`

---

## 3. BridgeRoute

表示 bridge 中最终入口路由。

字段建议：

* `env`
* `serviceName`
* `protocol`
* `bridgeHost`
* `bridgePort`
* `tunnelId`
* `targetPort`

---

## 4. TunnelSession

表示 `cloud-bridge` 与 `dev-agent` 之间的连接状态。

字段建议：

* `tunnelId`
* `rdName`
* `connId`
* `status`
* `lastHeartbeatAt`
* `connectedAt`

---

## 5. DesktopAppState

表示 Tauri UI 展示用状态。

字段建议：

* `agentStatus`
* `bridgeStatus`
* `tunnelStatus`
* `currentEnv`
* `rdName`
* `localRegistrations`
* `activeIntercepts`
* `recentRequests`
* `lastError`

---

# 五、一期任务清单（MVP）

一期目标：

> 跑通“Win11 Tauri dev-agent + cloud-bridge + 两个 demo 服务 + HTTP/gRPC 双协议 + base/dev 闭环”的最小版本。

---

## A. 总体设计与原型

### A1. 输出总体设计文档

让 AI 先生成：

* 系统架构图
* dev-agent 分层图（Tauri UI / Rust Host / Go Core）
* 本地服务注册时序图
* cloud-bridge 回流时序图
* dev 出口调用时序图
* UI 页面结构草图

### A2. 定义核心对象与接口

输出：

* `LocalRegistration`
* `ActiveIntercept`
* `BridgeRoute`
* `TunnelSession`
* `DesktopAppState`

### A3. 定义前后端通信方式

明确：

* Tauri 前端如何调用 Rust
* Rust 如何调用 Go Agent Core
* 推荐方案：

  * Tauri 调 Rust command
  * Rust 管理 Go 子进程
  * UI 通过本地 HTTP API 或 IPC 获取 agent 状态

---

## B. Tauri dev-agent 桌面端基础

### B1. 创建 Tauri 项目骨架

要求：

* 支持 Win11
* 前端 React + TypeScript
* 后端 Rust Host
* 能打包为桌面程序

### B2. 实现桌面壳能力

包括：

* 主窗口
* 系统托盘
* 最小化到托盘
* 开机自启动开关
* 配置文件目录管理
* 日志目录管理

### B3. 集成 Go Agent Core

实现：

* Rust 拉起 Go Agent Core 进程
* 进程生命周期管理
* 崩溃检测与重启
* 前端显示 agent 运行状态

### B4. 配置页

支持配置：

* `bridgeAddr`
* `rdName`
* `envName`
* `authToken`
* `httpProxyListen`
* `grpcProxyListen`
* 日志级别
* 开机启动
* 自动重连开关

---

## C. dev-agent Core 基础能力

### C1. 实现 Go Agent Core 进程骨架

包含：

* 配置加载
* 日志
* 生命周期管理
* 本地状态存储

### C2. 实现到 cloud-bridge 的长连接

包含：

* 启动即连接
* 心跳
* 断线重连
* 重连后恢复状态

### C3. 实现本地 register/unregister 接口

供本地业务服务调用：

* `register`
* `unregister`
* `discover`
* `list registrations`

### C4. 实现本地已接管服务表

维护：

```text
(serviceName, env) -> LocalRegistration
```

支持：

* 新增
* 更新
* 删除
* 查询

---

## D. cloud-bridge 基础能力

### D1. 实现 cloud-bridge 骨架

包含：

* 配置加载
* 日志
* 生命周期
* 管理接口基础

### D2. 实现 tunnel session 管理

维护：

```text
tunnelId -> TunnelSession
```

支持：

* 连接建立
* 心跳刷新
* 断开清理
* 重连覆盖

### D3. 实现 ActiveIntercept 管理

维护：

```text
(env, serviceName, protocol) -> ActiveIntercept
```

支持：

* 新增
* 更新
* 删除
* 查询

---

## E. 注册驱动模型实现

### E1. 本地应用注册到 dev-agent

业务服务启动时：

* 不直接去 nacos
* 先调用 dev-agent 的 `register`

### E2. dev-agent 同步到 cloud-bridge

收到本地注册后：

* 更新 LocalRegistration
* 同步到 bridge
* 由 bridge 生成 ActiveIntercept

### E3. 本地应用下线同步删除

本地服务停止时：

* 调用 `unregister`
* agent 删除本地注册项
* 同步删除 bridge 中的 intercept

### E4. 重连后全量重同步

agent 与 bridge 重连后，自动重新同步全部 LocalRegistration

---

## F. 入口流量回流

### F1. 实现 HTTP ingress

bridge 提供 HTTP 入口：

* base 请求到 bridge
* 根据 `(env, serviceName, http)` 找到 tunnel
* 转发到对应 dev-agent
* dev-agent 转发到 localhost httpPort

### F2. 实现 gRPC ingress

bridge 提供 gRPC 入口：

* base 请求到 bridge
* 根据 `(env, serviceName, grpc)` 找到 tunnel
* 转发到对应 dev-agent
* dev-agent 转发到 localhost grpcPort

### F3. 实现入口错误分类

至少区分：

* 未找到 intercept
* tunnel 不在线
* 本地服务不可达
* 上游超时

---

## G. 出口发现与代理

### G1. 业务系统新增 `agent` 类型

现有：

* `nacos`
* `local`

新增：

* `agent`

### G2. 实现 discover 逻辑

规则：

* 优先同 env 的 dev
* 没有 dev 则 fallback 到 base

### G3. 实现 HTTP 出口代理

本地服务的 HTTP 调用全部走 dev-agent。

### G4. 实现 gRPC 出口代理

本地服务的 gRPC 调用全部走 dev-agent。

### G5. HTTP / gRPC 两套出口分离

分别实现：

* `httpProxy`
* `grpcProxy`

---

## H. 环境上下文传播

### H1. 定义统一 env 透传

* HTTP：`X-Env`
* gRPC：`x-env` metadata

### H2. 自动透传

在 agent 与 bridge 层都自动透传。

### H3. 默认回退

无 env 时默认走 base。

---

## I. Tauri UI 一期页面

### I1. 首页 Dashboard

展示：

* agent 在线状态
* bridge 连接状态
* tunnel 状态
* 当前 env
* 当前 rdName
* 已接管服务数量
* 最近错误

### I2. 本地服务页

展示：

* 当前已注册本地服务
* 服务名
* env
* httpPort
* grpcPort
* 健康状态
* 注册时间

支持操作：

* 刷新
* 手动注销
* 查看详情

### I3. ActiveIntercept 页

展示：

* 已同步到 cloud-bridge 的接管关系
* service / env / protocol / tunnelId / targetPort / status

### I4. 日志页

展示：

* agent 日志
* 连接日志
* 注册同步日志
* 代理转发日志
* 错误日志

### I5. 配置页

支持修改并保存：

* bridge 地址
* env
* rd 名称
* 代理监听端口
* token
* 自动启动
* 日志级别

---

## J. Demo 与联调验证

### J1. 准备 demo 服务

例如：

* `order-service`
* `user-service`

每个服务提供：

* HTTP
* gRPC

### J2. 验证场景

至少验证：

1. `order(base) -> user(base)`
2. `order(dev) -> user(base)`
3. `order(base, env=dev-lifeilin) -> user(dev-lifeilin)`
4. `order(dev-lifeilin) -> user(dev-lifeilin) -> inventory(base)`
5. HTTP 正常
6. gRPC 正常
7. 本地服务停止后自动摘除
8. UI 可正确展示状态变化

---

# 六、二期任务清单（增强）

---

## K. Tauri UI 增强

### K1. 请求调试页

展示最近请求：

* 时间
* 来源
* service
* protocol
* env
* 命中 dev/base
* 耗时
* 结果

### K2. 可视化链路页

展示一次请求经过：

* base / bridge / tunnel / agent / localhost

### K3. 桌面通知

在以下场景通知：

* bridge 断开
* tunnel 重连
* 服务注册成功
* 服务摘除
* 错误告警

### K4. 快捷操作

支持：

* 一键重连 bridge
* 一键清空本地注册项
* 一键导出日志
* 一键复制诊断信息

---

## L. 健康检查与自动摘除

### L1. 本地服务健康检查

dev-agent 周期探测：

* http health
* grpc health

### L2. 自动摘除

服务不健康时自动：

* 从 LocalRegistration 中移除
* 同步删除 bridge intercept

### L3. UI 展示健康变化

桌面 UI 实时刷新状态。

---

## M. gRPC 连接治理

### M1. 连接池隔离

按 `(service, env)` 建立独立连接池。

### M2. 长连接刷新

在 dev/base 切换时主动刷新旧连接。

### M3. gRPC 错误可视化

在 UI 中单独展示：

* 建连失败
* 流中断
* metadata 缺失
* 路由错误

---

## N. 多 RD 并发支持

### N1. 支持多个 env

如：

* `dev-lifeilin`
* `dev-zhangsan`

### N2. UI 增加多环境状态视图

查看不同 env 下的接管情况。

### N3. 冲突处理

处理同 env 同服务重复注册。

---

## O. 安全与权限控制

### O1. bridge 与 agent 鉴权

至少支持：

* token
* tunnel 身份校验

### O2. 接管权限校验

限制某 RD 只能接管允许的 env/service。

### O3. 敏感配置保护

Tauri 本地配置中的 token 不明文裸奔，至少做基础保护。

---

## P. 可观测性与管理接口

### P1. bridge 管理接口

查看：

* tunnel session
* ActiveIntercept
* 路由表
* 错误统计

### P2. agent 本地诊断接口

查看：

* LocalRegistration
* 当前发现结果
* 最近请求
* 当前出口连接状态

### P3. UI 诊断面板

支持一键展示：

* 当前配置
* 当前连接
* 当前路由命中
* 最近错误

---

# 七、建议的 AI 执行顺序

---

## 第一步：先设计

1. 设计系统总体架构
2. 设计 Tauri dev-agent 分层
3. 设计对象模型与消息协议
4. 设计 UI 页面结构与状态模型

## 第二步：先跑通桌面壳

5. 创建 Tauri Win11 项目
6. 实现托盘、配置页、日志页
7. 集成 Go Agent Core 进程管理

## 第三步：实现基础通信

8. 实现 dev-agent 与 bridge 长连接
9. 实现心跳、重连、状态同步
10. 实现 tunnel session 管理

## 第四步：实现注册驱动

11. 实现本地服务 register/unregister
12. 实现 LocalRegistration 表
13. 实现同步到 bridge 的 ActiveIntercept

## 第五步：实现入口回流

14. 实现 bridge HTTP ingress
15. 实现 bridge gRPC ingress
16. 实现 agent 落到 localhost 的转发

## 第六步：实现出口代理

17. 实现业务系统 `registry=agent`
18. 实现 discover 规则
19. 实现 HTTP 出口代理
20. 实现 gRPC 出口代理

## 第七步：做 UI 联动

21. UI 展示 tunnel 状态
22. UI 展示本地注册服务
23. UI 展示 active intercept
24. UI 展示日志与最近请求

## 第八步：联调与增强

25. 写 demo 服务
26. 跑闭环验证
27. 补健康检查、诊断、通知、导出日志

---

# 八、可直接交给 AI 的任务标题清单

你可以直接一条条发给 AI：

1. 设计 Win11 Tauri dev-agent + cloud-bridge 的总体架构
2. 设计 dev-agent 的分层：Tauri UI、Rust Host、Go Agent Core
3. 设计 LocalRegistration / ActiveIntercept / BridgeRoute / TunnelSession / DesktopAppState
4. 设计 dev-agent 与 cloud-bridge 的通信协议
5. 创建 Tauri + React + TypeScript 的 Win11 桌面项目骨架
6. 实现 Tauri 主窗口、托盘、配置持久化、日志目录管理
7. 实现 Rust Host 拉起并管理 Go Agent Core 进程
8. 实现 Go Agent Core 的配置加载、日志与生命周期
9. 实现 dev-agent 到 cloud-bridge 的长连接、心跳与重连
10. 实现 cloud-bridge 的 TunnelSession 管理
11. 实现本地应用向 dev-agent 的 register/unregister 接口
12. 实现 dev-agent 的 LocalRegistration 管理
13. 实现 dev-agent 将 LocalRegistration 同步到 cloud-bridge
14. 实现 cloud-bridge 的 ActiveIntercept 管理
15. 实现 cloud-bridge 的 HTTP ingress 回流到 dev-agent
16. 实现 cloud-bridge 的 gRPC ingress 回流到 dev-agent
17. 实现 dev-agent 将入口流量转发到 localhost http/grpc 服务
18. 在业务系统中新增 `agent` 类型的注册与发现实现
19. 实现基于 env 的发现优先 dev、否则 fallback base
20. 实现 dev-agent 的 HTTP 出口代理
21. 实现 dev-agent 的 gRPC 出口代理
22. 实现 HTTP `X-Env` 与 gRPC `x-env` 的自动透传
23. 实现 Tauri Dashboard 页面
24. 实现本地服务列表页面
25. 实现 ActiveIntercept 列表页面
26. 实现日志页面
27. 实现配置页面
28. 实现最近请求与调试面板
29. 编写 demo 服务验证 base/dev 闭环
30. 实现本地服务停止后的自动摘除
31. 实现断线重连后的全量重同步
32. 增加健康检查与 UI 状态联动
33. 增加桌面通知、日志导出与诊断信息复制
34. 增加多 RD env 并发隔离
35. 增加鉴权、权限控制与敏感配置保护

---

# 九、一期验收标准

---

## 功能验收

1. Win11 上可启动 Tauri 桌面端
2. 桌面端可成功拉起 Go Agent Core
3. agent 可连接 cloud-bridge
4. 本地服务可注册到 dev-agent
5. dev-agent 可同步接管关系到 cloud-bridge
6. base 服务可通过 bridge 访问到本地 dev 服务
7. dev 服务可通过 agent 实现 dev 优先、base fallback
8. HTTP 可用
9. gRPC 可用
10. 本地服务停止后接管关系自动摘除
11. agent 重连后状态可恢复
12. UI 可正确展示注册、连接、接管、错误状态

---

## 工程验收

1. Tauri UI、Rust Host、Go Core 模块边界清晰
2. HTTP 与 gRPC 路径分离
3. 配置清晰
4. 日志可追踪完整链路
5. 错误码与错误信息明确

