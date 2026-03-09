# DevBridge

> 一套面向微服务联调的开发环境闭环工具。  
> 支持本地接管部分服务，其余流量自动回退共享环境。

DevBridge 主要解决这样一类问题：

- 微服务系统运行在云服务器、虚拟机或裸机环境中
- 开发时通常只修改其中少量服务
- 本地无法完整运行整套环境
- 共享 base 环境虽然可用，但无法满足每个开发者的独立联调需求
- 传统内网穿透工具只能解决“连通性”，无法解决“服务发现、流量回流、环境闭环”问题

DevBridge 的目标，就是让开发者只在本地运行当前需要开发的服务，同时继续复用共享环境中的其余服务，并保持整个调用链的闭环一致性。

---

## 目录

- [项目简介](#项目简介)
- [要解决的问题](#要解决的问题)
- [核心能力](#核心能力)
- [典型工作方式](#典型工作方式)
- [核心架构](#核心架构)
- [流量模型](#流量模型)
- [适用场景](#适用场景)
- [不是什么](#不是什么)
- [模块划分](#模块划分)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [工作原理](#工作原理)
- [界面规划](#界面规划)
- [路线图](#路线图)
- [适配与扩展](#适配与扩展)
- [贡献指南](#贡献指南)
- [许可证](#许可证)

---

## 项目简介

DevBridge 是一套用于微服务研发联调的 **开发环境闭环系统**。

它允许开发者：

- 只在本地运行自己正在开发的少量服务
- 让未改动的依赖服务继续运行在共享的 base 环境
- 让 base 环境能够访问本地服务
- 让本地服务继续使用统一的服务发现机制访问其他服务
- 在整个链路中保持统一的环境上下文与路由逻辑

DevBridge 不是单纯的“内网穿透工具”，而是一个面向研发联调场景的 **本地 Agent + 云端 Bridge + 服务接管与流量回流** 系统。

---

## 要解决的问题

在很多中小型微服务团队中，经常会遇到下面这些问题：

#### 1. 本地无法完整运行整套环境

一套微服务系统往往包含很多服务、数据库、缓存、中间件和配置依赖。  
开发者通常只修改其中一个或几个服务，但为了联调却不得不面对整套环境的复杂性。

#### 2. 共享环境不具备开发者隔离能力

公司通常会准备一个或多个共享联调环境，但这些环境往往是公共的：

- 多人同时开发时容易相互影响
- 某些需求需要独占流量
- 某些未发布功能不适合直接合并到公共环境

#### 3. 本地服务加入调用链后，链路会断裂

假设本地运行了 `user-service`：

- base 环境中的 `order-service` 无法直接访问开发机
- 本地 `user-service` 再调用其他服务时，可能又跳回共享环境
- 最终导致“入口链路”和“出口链路”不一致

#### 4. 传统隧道工具解决不了服务级闭环

frp、ngrok、rathole 这类工具主要解决的是：

- 本地服务如何被远端访问
- 网络连通性如何建立

但它们通常不负责：

- 服务注册
- 服务发现
- dev 优先 / base 回退
- 服务级别的环境隔离
- HTTP / gRPC 双协议协同路由
- 开发者桌面端状态管理

DevBridge 就是为了解决这些更上层的问题。

---

## 核心能力

DevBridge 提供以下核心能力：

#### 1. 本地服务注册到 DevBridge Agent

本地服务启动后，不再直接注册到共享注册中心，而是先注册到本地 `DevBridge Agent`。

#### 2. 本地已接管服务自动同步到云端 Bridge

`DevBridge Agent` 会把当前本地已接管的服务同步到 `DevBridge Bridge`，形成云端可访问的接管关系。

#### 3. base 环境入口流量可回流到本地服务

共享环境中的服务可以通过 `DevBridge Bridge` 访问当前开发者本地运行的服务。

#### 4. 本地服务出口流量支持 dev 优先、base 回退

本地服务调用其他服务时：

- 优先命中当前开发环境中的 dev 实例
- 如果没有 dev 实例，则自动回退到 base 环境

#### 5. 同时支持 HTTP 和 gRPC

DevBridge 支持：

- HTTP
- gRPC

并且这两类协议采用独立端口、独立路径处理，避免混用带来的复杂性与不稳定。

#### 6. 提供 Win11 桌面端管理界面

DevBridge 计划提供基于 **Tauri** 的 Win11 桌面端，用于：

- 配置管理
- 连接状态展示
- 本地服务注册展示
- 接管关系展示
- 日志与诊断
- 请求调试

---

## 典型工作方式

一个典型的开发联调流程如下：

1. 开发者在本地启动 `DevBridge Desktop`
2. `DevBridge Agent` 自动连接到云端 `DevBridge Bridge`
3. 本地业务服务启动后，注册到 `DevBridge Agent`
4. `DevBridge Agent` 将当前已接管服务同步到 `DevBridge Bridge`
5. base 环境中的服务调用这些已接管服务时，请求会通过 Bridge 回流到本地
6. 本地服务调用其他服务时，通过 Agent 做统一发现与路由
7. 如果目标服务在当前 dev 环境中有实例，则优先走 dev
8. 如果没有，则自动回退到共享 base 环境

最终形成一条完整的开发环境闭环。

---

## 核心架构

DevBridge 由三个核心部分组成：

#### 1. DevBridge Desktop

运行在 Win11 上的桌面应用，基于 Tauri 实现。

职责：

- 提供桌面界面
- 管理 Agent 生命周期
- 管理配置
- 展示连接状态
- 展示本地服务与接管关系
- 提供日志与调试能力

#### 2. DevBridge Agent

运行在开发机上的本地 Agent 进程。

职责：

- 接收本地服务注册 / 反注册
- 维护本地已接管服务表
- 与 DevBridge Bridge 保持长连接
- 同步本地已接管服务到 Bridge
- 提供服务发现能力
- 提供 HTTP / gRPC 出口代理
- 接收来自 Bridge 的入口流量并转发到 localhost 服务

#### 3. DevBridge Bridge

运行在云端的桥接服务。

职责：

- 维护与各个开发机 Agent 的 tunnel 连接
- 维护当前有效接管关系
- 作为 base 环境访问本地服务的统一入口
- 根据 `(env, serviceName, protocol)` 将请求路由到正确的开发机 Agent

---

## 流量模型

DevBridge 同时处理 **入口流量** 和 **出口流量**。

#### 入口流量：base -> dev

当 base 环境中的服务需要访问某个已被开发者本地接管的服务时：

1. base 服务通过服务发现拿到 Bridge 地址
2. 请求发往云端 `DevBridge Bridge`
3. Bridge 根据环境、服务名、协议查找有效接管关系
4. 找到对应的开发机 Agent tunnel
5. 将请求转发到本地 Agent
6. Agent 再将请求转发到 localhost 的本地服务

#### 出口流量：dev -> dev/base

当本地服务需要访问其他服务时：

1. 本地服务通过 `DevBridge Agent` 做服务发现
2. Agent 优先选择同一开发环境中的 dev 实例
3. 如果没有 dev 实例，则回退到 base 实例
4. 如果目标是 dev，则走 Bridge
5. 如果目标是 base，则走直连

---

## 适用场景

DevBridge 特别适合以下场景：

- 微服务运行在 **VM / 裸机 / 云服务器**，而不是 Kubernetes
- 服务发现依赖 **Nacos** 或类似注册中心
- 团队需要共享 base 环境做联调
- 开发者通常只需本地启动少量服务
- 服务同时使用 **HTTP 和 gRPC**
- 开发机主要运行在 **Windows 11**
- 需要一个比传统隧道工具更贴近研发联调的解决方案

---

## 不是什么

为了避免误解，这里明确说明 DevBridge **不是什么**：

#### 不是通用公网隧道工具

DevBridge 的目标不是把本地服务暴露到公网，也不是替代 ngrok / frp / rathole 这类通用隧道产品。

#### 不是 Kubernetes 原生 Service Mesh

DevBridge 不是 Istio、Linkerd 这类面向生产集群的 Service Mesh，也不依赖 Kubernetes 才能工作。

#### 不是生产流量治理平台

DevBridge 的重点是研发联调阶段的环境闭环，而不是生产流量治理、灰度发布、服务网格策略编排。

#### 不是全量环境复制方案

DevBridge 的核心理念不是在本地复制整套环境，而是：

> 本地只运行需要开发的部分，未改动部分继续复用共享环境。

---

## 模块划分

当前建议的仓库模块结构如下：

```text
devbridge-loop/
├── devbridge-desktop   ## Win11 Tauri 桌面端
├── devbridge-agent     ## 本地 Agent 核心
├── devbridge-bridge    ## 云端 Bridge 服务
├── devbridge-sdk-go    ## Go 侧接入层（注册/发现/代理适配）
├── docs                ## 设计文档、架构图、时序图
└── examples            ## demo 服务与示例配置
```

---

## 快速开始

> 当前项目仍处于设计与开发阶段，下面的步骤表示目标使用方式。

#### 1. 启动云端 Bridge

先在云服务器上部署并启动 `DevBridge Bridge`：

```bash
devbridge-bridge --config ./bridge.yaml
```

#### 2. 启动本地桌面端

在 Win11 上启动 `DevBridge Desktop`。

桌面端会：

* 加载本地配置
* 启动 Agent Core
* 建立到 Bridge 的连接
* 展示当前连接状态

#### 3. 本地服务接入 DevBridge

本地业务服务启动时，不再直接使用 `nacos`，而是改为使用 `agent` 类型：

```yaml
registry:
  type: agent
  agent_addr: 127.0.0.1:19000
```

#### 4. 服务注册到 Agent

服务启动后，会向本地 Agent 注册：

* 服务名
* env
* HTTP 端口
* gRPC 端口
* metadata

Agent 接收后会自动同步到 Bridge。

#### 5. 验证联调闭环

验证以下几种场景：

* base -> base
* dev -> base
* base -> dev
* dev -> dev
* dev -> base fallback

---

## 配置说明

下面是一个示例配置，仅用于说明结构。

#### Desktop / Agent 配置示例

```yaml
agent:
  rd_name: lifeilin
  env_name: dev-lifeilin
  bridge_addr: bridge.example.com:38080
  auth_token: your-token
  http_proxy_listen: 127.0.0.1:16080
  grpc_proxy_listen: 127.0.0.1:39090
  auto_reconnect: true
  log_level: info
```

#### Bridge 配置示例

```yaml
bridge:
  listen_addr: 0.0.0.0:38080
  admin_addr: 0.0.0.0:18081
  heartbeat_timeout: 30s
  idle_timeout: 60s
  log_level: info
```

---

## 工作原理

DevBridge 使用“注册驱动的接管模型”，而不是“静态声明模型”。

#### 1. Agent 启动时并不知道要接管什么服务

`DevBridge Agent` 启动时只做这些事：

* 建立到 Bridge 的连接
* 准备本地注册、发现、代理接口
* 等待本地服务注册

此时 Agent 在线，但尚未接管任何服务。

#### 2. 本地服务注册驱动接管关系生成

当本地服务真正启动并注册到 Agent 后：

1. Agent 更新本地注册表
2. Agent 将当前服务同步到 Bridge
3. Bridge 更新有效接管关系
4. base 环境此时才可以访问该本地服务

#### 3. 本地服务下线会自动摘除接管关系

当本地服务停止或反注册时：

1. Agent 删除本地注册信息
2. 同步通知 Bridge
3. Bridge 删除对应接管关系
4. 后续流量自动回退到 base

---

## 界面规划

DevBridge Desktop 计划提供以下页面：

#### 1. Dashboard

展示：

* Agent 在线状态
* Bridge 连接状态
* tunnel 状态
* 当前 env
* 当前开发者名称
* 已接管服务数量
* 最近错误

#### 2. 本地服务页

展示：

* 当前本地已注册服务
* HTTP / gRPC 端口
* 健康状态
* 注册时间
* metadata

支持：

* 手动刷新
* 手动摘除
* 查看详情

#### 3. 接管关系页

展示同步到 Bridge 的有效接管关系：

* env
* serviceName
* protocol
* tunnelId
* targetPort
* 状态

#### 4. 日志页

展示：

* Agent 日志
* Bridge 连接日志
* 注册同步日志
* 转发日志
* 错误日志

#### 5. 调试页

展示：

* 最近请求
* 命中 dev / base
* 请求耗时
* 错误原因
* 链路诊断信息

#### 6. 配置页

支持配置：

* Bridge 地址
* env 名称
* RD 名称
* token
* HTTP / gRPC 监听端口
* 自动启动
* 自动重连
* 日志级别

---

## 路线图

#### 一期目标

* [ ] 实现 Agent 与 Bridge 长连接
* [ ] 实现本地服务 register / unregister
* [ ] 实现本地已接管服务同步到 Bridge
* [ ] 实现 Bridge 的有效接管关系管理
* [ ] 实现 base -> dev 的 HTTP 回流
* [ ] 实现 base -> dev 的 gRPC 回流
* [ ] 实现 dev -> dev/base 的服务发现与出口代理
* [ ] 支持 HTTP / gRPC 双协议
* [ ] 实现 Win11 Tauri 桌面端基本界面

#### 二期目标

* [ ] 健康检查与自动摘除
* [ ] tunnel 多路复用
* [ ] 多开发者并发隔离
* [ ] gRPC 连接治理
* [ ] 请求调试与链路可视化
* [ ] 诊断信息导出
* [ ] 鉴权与权限控制
* [ ] 更完善的管理接口与指标

---

## 适配与扩展

DevBridge 当前优先面向以下组合：

* 开发机：Windows 11
* 桌面端：Tauri
* Agent / Bridge：Go
* 服务调用：HTTP + gRPC
* 服务发现：支持在现有 `nacos / local` 之外扩展 `agent`
* 部署环境：VM / 裸机 / 云服务器

未来可以考虑扩展：

* Linux / macOS 桌面支持
* 更多服务发现后端
* 更多语言 SDK
* 更细粒度的流量调试能力

---

## 贡献指南

欢迎参与 DevBridge 的设计、开发与讨论。

你可以通过以下方式参与：

* 提交 Issue
* 提交 Pull Request
* 参与架构设计讨论
* 提供真实业务场景反馈
* 补充文档、示例与测试

在提交代码前，请尽量确保：

* 保持模块边界清晰
* HTTP 与 gRPC 路径分离
* 日志和错误信息可追踪
* 配置项具备清晰命名
* 对开发环境闭环这个核心目标保持一致

---

## 许可证

Apache-2.0
