# Agent Web UI / LocalRPC / HTTP 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不破坏现有 Tauri + LocalRPC 链路的前提下，为 Agent 模块抽象出稳定的 `hostapi` 层，并逐步补齐基于 HTTP 的 Web UI 接口与简单账号密码登录能力。

**Architecture:** 现有 `localrpc` 继续承担 UDS / Named Pipe、帧协议和本地握手职责，但不再直接承载业务方法实现；业务方法下沉到 `hostapi`，由 `localrpc` 与未来 `httpapi` 共同复用。HTTP 首版采用独立管理面监听地址 + 会话 Cookie 登录模型，账号密码从 Agent YAML 配置文件读取，避免把浏览器认证状态混入现有 IPC 临时环境变量链路。

**Tech Stack:** Go, net/http, Unix Domain Socket / Named Pipe, YAML (`gopkg.in/yaml.v3`), 现有 Agent runtime / LocalRPC / Tauri 宿主链路

---

## 当前状态（2026-03-29）

当前这份计划对应的四个阶段已经全部落地：

- `hostapi` 抽象已完成，`localrpc` 只保留 transport / 本地握手 / 帧协议职责。
- Agent 已支持 `-config <yaml_path>`，`ui.web.auth` 从 YAML 配置读取用户名和密码。
- Agent 配置加载与保存语义现已与 Bridge 对齐：
  - `-config <yaml_path>` 仍为最高优先级
  - 未显式传入时，按“环境变量 > 程序运行目录 `agent.yaml` > 用户目录 > 系统目录 > 默认值”叠加构建运行配置
  - `SaveConfigToYAMLFile` 使用创建父目录、继承原文件权限、临时文件写入后原子替换的落盘方式
- HTTP 管理接口已完成，登录态使用独立 Cookie，会话不复用 `DEV_AGENT_SESSION_SECRET`。
- 未知 `<base_path>/api/*` 路径现在会返回 `404`，不再错误回退到嵌入式 SPA HTML，便于前后端契约错配时快速定位。
- HTTP 管理接口现已补充聚合 SSE 流：
  - `GET <base_path>/api/events/stream`
  - 复用现有 Web 登录 Cookie，不额外引入第二套浏览器鉴权材料
  - 首帧发送 `agent.ready`，随后推送聚合 `agent.snapshot`，并用 `agent.heartbeat` 保活
- Agent 配置管理接口已支持“读当前生效配置快照 + 保存到可编辑 YAML”：
  - `GET  <base_path>/api/app/config` 返回运行配置快照、配置来源路径和可编辑的完整配置文档
  - `PUT  <base_path>/api/app/config` 可将新的 Agent 配置持久化回显式配置文件 / 本地配置文件 / 用户配置文件
  - 保存时只更新当前可编辑配置层，不再把用户目录 / 系统目录继承而来的字段整份固化进目标 YAML
  - 当前保存仅更新配置快照与 YAML 文件，不会热更新已运行的 Agent 传输与会话参数，仍需重启生效
  - Agent Web 控制台已嵌入 `agent-core`：
  - 新增 `agent-core/web/` 前端工程，使用 React + Tailwind + shadcn 风格组件实现页面。
  - HTTP 服务在 `ui.web.base_path` 下同时承载静态 UI 和 `/api/*` 接口。
  - 裸 `base_path` 会自动重定向到带尾斜杠的地址，保证相对静态资源可稳定加载。
  - 页面导航采用 hash 路由，避免构建产物与可配置 `base_path` 强耦合。
  - 控制台数据同步已改为 `EventSource` 优先：
    - 正常情况下通过 `/api/events/stream` 接收聚合实时快照
    - 若浏览器不支持 SSE 或首次握手失败，则回退到现有定时轮询
  - 登录页视觉已对齐 `docs/stitch/login/code.html` 的 split layout 风格：
    - 顶部品牌条
    - 左侧蓝色品牌叙事区
    - 右侧高密度登录表单
    - 登录流程仍复用现有 `/api/login`
    - 字体、留白、输入焦点态和品牌信息块已进一步按 Stitch 原型收口，避免出现明显“通用后台”风格偏差
    - 纯前端静态预览场景下，若未接入 Agent HTTP API，不再把 `/api/session` 缺失渲染为红色解析错误，便于独立做 UI 校准
  - 设置页现已支持编辑与 Tauri 对齐的共享运行字段：
    - `agent_id`
    - `bridge_addr`
    - `bridge_transport`
    - `bridge_tls_*`
    - `tunnel_pool_*`
  - 设置页现已额外开放 `session.auth_token`：
    - 可直接在 Web 控制台中修改 Bridge 认证 token
    - 不再提供本地“随机生成”按钮，避免生成 Bridge 未登记的无效凭证
    - 页面不会回显已有 token 明文；留空表示保持当前 token 不变
    - 新 token 需要先在 Bridge 后台创建或轮换，再粘贴到 Agent 后台保存
    - 保存后仍需重启 Agent，新的握手认证才会真正生效
  - Web 设置页保存时只提交这组共享字段，其余 YAML 配置保持原值不变。
  - 设置页已补字段级前端校验和重启引导，不再只依赖顶部全局错误提示。
  - 登录后的控制台壳层已调整为“左侧菜单固定、右侧工作区独立滚动”，避免长页面滚动时导航区一并滑走。
  - 总览页与服务页已继续按同一套 tonal / editorial 风格收口：
    - 总览页核心卡片、指标和桥接会话区块统一为中文口径与同层次视觉语气
    - 服务页已从“左表右侧常驻登记表单”改为“单列表 + 头部注册按钮 + 模态窗”
    - 服务列表每行支持 `详情 / 编辑 / 删除`
    - `注册服务` 与 `编辑服务` 共用同一套模态表单；编辑时会按实例当前登记内容回显
    - 服务详情使用独立只读模态，避免在主列表里堆积过多说明信息
  - 隧道页、流量页与诊断页也已同步收口：
    - 核心标题、指标标签与上下文字段统一为中文口径
    - 卡片层级与日志块/表格区域采用与总览页一致的轻分层风格
  - 控制台可见状态词与界面品牌文案已继续中文化：
    - `idle / active / error / healthy / degraded` 等运行态会在前端映射为中文展示
    - 页头、侧栏和页脚品牌文案已统一为 `Agent 控制台`
    - 服务暴露路径、日志级别、运行态摘要等零散英文标签已一并收口
  - 表单下拉组件已从原生 `<select>` 包装切换为 shadcn/ui 风格实现：
    - 基于 Radix Select 弹层式交互
    - 服务登记页和设置页的枚举字段已统一使用同一套下拉视觉与焦点态
  - 复选框与多行输入区也已继续向 shadcn/ui 风格统一：
    - 服务登记页的 `allow_export` 已切换为弹性更好的 checkbox 组件
    - `Textarea` 的圆角、底色和焦点态已与其他表单控件统一
  - 设置页输入区已切换为更接近 Stitch 设计语言的 tonal / no-line 风格分组表单，减少硬边框和割裂感。
- Agent 启动入口已支持显式区分运行模式：
  - 直接启动且 `ui.web.enabled=true` 时，默认进入 Web 模式
  - 传 `-tauri` 时显式启用 Tauri / LocalRPC
  - 同时传 `-tauri -web` 时启用双入口
  - `apps/dev-agent` 宿主会自动补齐 `-tauri` 参数
- 仓库 `Makefile` 已新增 `build-agent-web-ui`，并把 `build-agent-core` / `build-win11-go` 调整为先打包 Agent Web，再编译并嵌入 Go 二进制。

本轮实际验证：

```bash
cd agent-core && npm install --prefix web
cd agent-core/web && npm run test
cd agent-core/web && npm run build
cd agent-core && gofmt -w runtime/agent/httpapi/server.go runtime/agent/httpapi/server_test.go runtime/agent/app/http_server.go runtime/agent/app/http_server_test.go web/embed.go
cd agent-core && gofmt -w cmd/agent-core/main.go cmd/agent-core/main_test.go runtime/agent/app/bootstrap.go runtime/agent/app/bootstrap_test.go
cd agent-core && gofmt -w runtime/agent/app/config.go runtime/agent/app/config_yaml.go runtime/agent/app/config_yaml_test.go runtime/agent/app/runtime_config_loader.go runtime/agent/app/runtime_config_store.go runtime/agent/app/runtime_config_store_test.go runtime/agent/app/hostapi_adapter.go runtime/agent/hostapi/service.go runtime/agent/hostapi/types.go
cd agent-core && go test -timeout 60s ./...
cd agent-core && go test -race -timeout 60s ./...
cargo test --manifest-path apps/dev-agent/src-tauri/Cargo.toml runtime_args_should_append_tauri_flag_once
```

未覆盖项：

- `golangci-lint run ./...` 未执行，当前环境缺少 `golangci-lint`
- 还未做真实浏览器跨内核验证
- 还未做 HTTPS / 反向代理 / 跨域部署场景验证

---

## 1. 背景与边界

### 1.1 本次要完成的三项能力

1. 为 `localrpc` 抽象一层稳定的业务接口，避免后续 HTTP 再复制一套分发逻辑。
2. 为 Agent 补一套 Web 管理接口，供后续浏览器版 UI 使用。
3. HTTP 管理接口使用简单账号密码登录，账号密码配置在配置文件中。

### 1.2 本计划明确不做

- 不把现有 Tauri Rust Host 直接替换成 HTTP。
- 不暴露 tunnel/traffic 原始帧控制接口。
- 不做复杂 RBAC、OAuth、LDAP、多用户租户模型。
- 不在本轮引入数据库、外部 Session Store、刷新令牌体系。
- 不顺手重构 Agent 运行时其他无关模块。

### 1.3 关键设计决策

- **抽象方向：** 复用既有设计文档中的分层，把业务能力沉到 `agent-core/runtime/agent/hostapi/`，而不是继续堆在 `localrpc_server.go`。
- **兼容策略：** LocalRPC 对 Tauri 保持兼容；HTTP 是新增入口，不改变现有 UDS / Named Pipe 行为。
- **认证模型：** Web 首版采用“用户名 + 密码登录 -> 服务端签发会话 Cookie”。
- **配置来源：** 账号密码不走启动环境变量，统一走 Agent YAML 配置文件；环境变量仅保留现有 Tauri / IPC 启动注入职责。
- **三套鉴权隔离：**
  - LocalRPC challenge-response 仅服务 Tauri Host <-> Agent IPC。
  - HTTP session/cookie 仅服务浏览器 <-> Agent Web UI。
  - `DEV_AGENT_SESSION_SECRET` 绝不复用为 HTTP 登录态或 Cookie 签名材料。

---

## 2. 文件结构与职责拆分

### 2.1 新增目录

- `agent-core/runtime/agent/hostapi/`
  - 对外提供 Agent 本地管理面可复用的稳定业务接口。
- `agent-core/runtime/agent/httpapi/`
  - 对外提供 HTTP 路由、登录、会话校验、中间件与响应适配。

### 2.2 重点文件落点

**Modify**

- `agent-core/runtime/agent/app/config.go`
- `agent-core/runtime/agent/app/bootstrap.go`
- `agent-core/runtime/agent/app/localrpc_server.go`
- `agent-core/cmd/agent-core/main.go`
- `agent-core/runtime/agent/app/config_test.go`
- `agent-core/runtime/agent/app/localrpc_server_test.go`
- `docs/Agent–TauriLocalCommunicationDesignProposal.md`

**Create**

- `agent-core/runtime/agent/hostapi/service.go`
- `agent-core/runtime/agent/hostapi/types.go`
- `agent-core/runtime/agent/hostapi/service_test.go`
- `agent-core/runtime/agent/httpapi/server.go`
- `agent-core/runtime/agent/httpapi/auth.go`
- `agent-core/runtime/agent/httpapi/handlers.go`
- `agent-core/runtime/agent/httpapi/server_test.go`
- `agent-core/runtime/agent/app/config_yaml.go`
- `agent-core/runtime/agent/app/config_yaml_test.go`
- `agent-core/runtime/agent/app/http_server.go`

**Optional Create**

- `agent-core/config.example.yaml`
  - 如果当前仓库缺少 Agent 独立示例配置，则补一份最小 YAML 样例。

### 2.3 结构目标

```text
cmd/agent-core/main.go
  ├── 解析 YAML / env
  ├── Bootstrap runtime
  ├── 启动 localrpc server
  └── 启动 httpapi server（可选开启）

runtime/agent/app/
  ├── 运行时装配
  ├── 配置校验
  ├── localrpc transport adapter
  └── http server lifecycle

runtime/agent/hostapi/
  ├── 快照/命令统一入口
  ├── 输入输出 DTO
  ├── 能力接口（由 app/runtime 提供实现）
  └── transport 无关的参数校验与错误码

runtime/agent/httpapi/
  ├── login/logout/session
  ├── auth middleware
  └── hostapi -> HTTP 响应映射
```

### 2.4 方法归属约束

**保留在 `localrpc` 的能力**

- 帧协议编解码
- UDS / Named Pipe listener
- 本地 challenge-response 握手
- connection 级鉴权状态
- LocalRPC request / response frame 适配

**下沉到 `hostapi` 的能力**

- `app.shutdown`
- `config.snapshot`
- `session.snapshot`
- `service.list`
- `service.add`
- `service.delete`
- `tunnel.list`
- `traffic.stats.snapshot`
- `diagnose.snapshot`
- `diagnose.logs`

**保留在 `httpapi` 的能力**

- 登录 / 登出 / session 查询
- Cookie 签发与校验
- HTTP 状态码映射
- `base_path` 路由装配

约束：

- `hostapi` 不感知 LocalRPC frame、HTTP request/response、cookie、listener。
- `localrpc` / `httpapi` 只能消费 `hostapi` 的 DTO 与能力接口，不能直接拼 runtime 私有状态。

---

## 3. 分阶段开发计划

### Task 1: 把 `localrpc` 业务方法下沉到 `hostapi`

**目标**

先完成“传输层”和“业务方法层”解耦，这是后续 HTTP 复用的基础，也是整个计划的最低风险切入点。

**Files:**

- Create: `agent-core/runtime/agent/hostapi/service.go`
- Create: `agent-core/runtime/agent/hostapi/types.go`
- Create: `agent-core/runtime/agent/hostapi/service_test.go`
- Modify: `agent-core/runtime/agent/app/localrpc_server.go`
- Modify: `agent-core/runtime/agent/app/localrpc_server_test.go`

- [ ] **Step 1: 先写 `hostapi` 行为测试**

聚焦已有稳定方法，优先覆盖：

- `app.shutdown`
- `traffic.stats.snapshot`
- `diagnose.logs`
- `service.list`
- `service.add`
- `service.delete`

测试目标不是覆盖 transport，而是验证 `hostapi.Service` 对 runtime 的受控调用和响应结构。

- [ ] **Step 2: 定义 `hostapi` 的统一输入输出模型**

建议最小接口：

```go
type RuntimeHost interface {
    Shutdown(ctx context.Context) error
    BuildConfigSnapshot(ctx context.Context) (ConfigSnapshot, error)
    BuildTrafficStatsSnapshot(ctx context.Context) (TrafficStatsSnapshot, error)
    ListServices(ctx context.Context) ([]ServiceView, error)
    AddService(ctx context.Context, input AddServiceInput) (ServiceView, error)
    DeleteService(ctx context.Context, input DeleteServiceInput) error
    BuildDiagnoseLogs(ctx context.Context) ([]DiagnoseEventView, error)
}

type Method string

const (
    MethodAppShutdown Method = "app.shutdown"
    MethodServiceList Method = "service.list"
    // ...
)

type Request struct {
    Method  Method
    Payload json.RawMessage
}

type Handler interface {
    Handle(ctx context.Context, request Request) (Response, *Failure)
}

type Service struct {
    runtime RuntimeHost
}

type Response struct {
    Method  string
    Payload any
}

type Failure struct {
    Code    string
    Message string
}

func NewService(runtime RuntimeHost) *Service
func (service *Service) Handle(ctx context.Context, request Request) (Response, *Failure)
```

要求：

- `hostapi` 不感知 UDS / Named Pipe / HTTP。
- `hostapi` 不返回 `http.ResponseWriter`、socket、frame、cookie 等 transport 对象。
- `hostapi` 只定义 transport 无关 DTO；LocalRPC 如需继续兼容既有 payload 结构，由 adapter 层负责做兼容映射。
- `hostapi` 不直接持有 `*app.Runtime`，而是依赖最小能力接口，避免把运行时私有细节扩散到 Web 层。

- [ ] **Step 3: 把 `localrpc_server.go` 改成 transport adapter**

改造后职责应变成：

- 读取帧
- 做 LocalRPC challenge-response 鉴权
- 把 `method + payload` 转给 `hostapi`
- 把 `hostapi` 的成功/失败结果编码成 LocalRPC response frame

明确不要把 HTTP 认证逻辑混进 `localrpc`。

- [ ] **Step 4: 跑最小测试验证“抽象不改行为”**

Run:

```bash
cd agent-core && go test ./runtime/agent/app ./runtime/agent/hostapi
```

Expected:

- 原有 `localrpc_server_test.go` 通过
- 新增 `hostapi` 单测通过

- [ ] **Step 5: 文档回写分层约束**

把 `docs/Agent–TauriLocalCommunicationDesignProposal.md` 中 `localrpc/` 与 `hostapi/` 的职责，与真实目录/实现对齐，写清楚：

- LocalRPC = transport + local auth
- HostAPI = 受控业务方法层

---

### Task 2: 给 Agent 增加 YAML 配置入口，并定义 Web UI 配置结构

**目标**

在真正写 HTTP 之前，先把“账号密码在配置文件里”这件事落到真实启动链路中，避免后面又从 env 回填一遍。

**Files:**

- Modify: `agent-core/runtime/agent/app/config.go`
- Create: `agent-core/runtime/agent/app/config_yaml.go`
- Create: `agent-core/runtime/agent/app/config_yaml_test.go`
- Modify: `agent-core/cmd/agent-core/main.go`
- Modify: `agent-core/cmd/agent-core/main_test.go`
- Optional Create: `agent-core/config.example.yaml`

- [ ] **Step 1: 先补配置结构与校验测试**

建议新增：

```go
type LocalUIConfig struct {
    Web WebUIConfig `yaml:"web"`
}

type Config struct {
    // ... existing fields ...
    UI LocalUIConfig `yaml:"ui"`
}

type WebUIConfig struct {
    Enabled           bool   `yaml:"enabled"`
    ListenAddr        string `yaml:"listen_addr"`
    BasePath          string `yaml:"base_path"`
    SessionCookieName string `yaml:"session_cookie_name"`
    Auth              WebUIAuthConfig `yaml:"auth"`
}

type WebUIAuthConfig struct {
    Username string `yaml:"username"`
    Password string `yaml:"password"`
}
```

校验点：

- `enabled=false` 时允许缺省
- `enabled=true` 时必须有 `listen_addr`
- `enabled=true` 时 `auth.username` / `auth.password` 必填
- `base_path` 归一化为 `/agent` 或 `/`

- [ ] **Step 2: 复用 Bridge 的 YAML 解析模式给 Agent 增加 `-config`**

新增：

- `LoadConfigFromYAMLFile`
- `ParseConfigYAML`

要求：

- 使用 `yaml.Decoder.KnownFields(true)`
- 先套 `DefaultConfig()` 再 decode
- 解析后统一走 `Validate()`
- 保证 `Config.UI` 真正进入 runtime，而不是只停留在 YAML 解析结构里

启动入口建议支持：

```bash
agent-core -config ./agent-core/config.example.yaml
```

启动契约建议写死为：

- 未传 `-config` 时，维持当前默认行为：`DefaultConfig() + 必要 env`，保证 Tauri 现有链路不被强制破坏。
- 传入 `-config` 且文件不存在 / 解析失败 / 校验失败时，启动直接失败。
- 本轮 **不做** 隐式默认搜索路径，避免不同运行目录下出现歧义；只支持显式 `-config`。

- [ ] **Step 3: 明确 env 与 YAML 的优先级**

推荐顺序：

1. `DefaultConfig()`
2. `-config` YAML
3. 仅保留必要 env 覆盖（当前 Tauri 启动链路依赖的字段）

需要在计划执行时明确哪些字段仍允许 env 覆盖，哪些字段必须只读 YAML。

推荐：

- `DEV_AGENT_IPC_*`、`DEV_AGENT_SESSION_SECRET` 继续走 env
- Web 登录账号密码只读 YAML，不从 env 覆盖
- Tauri 启动链路本轮不强制切到 YAML；如后续要统一配置来源，再单独立项迁移

- [ ] **Step 4: 跑配置加载测试**

Run:

```bash
cd agent-core && go test ./cmd/agent-core ./runtime/agent/app
```

Expected:

- YAML 成功加载
- 非法字段被拒绝
- `enabled=true` 但缺少认证配置时失败

- [ ] **Step 5: 补最小样例配置**

如果新增 `agent-core/config.example.yaml`，至少包含：

```yaml
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:39082
    base_path: /agent
    session_cookie_name: devbridge_agent_session
    auth:
      username: admin
      password: change-me
```

注意：

- 样例值必须明显提示替换
- 不得提交真实密码

---

### Task 3: 落地 HTTP 管理面与简单登录

**目标**

在 `hostapi` 已稳定、YAML 已可读的前提下，新增 HTTP 服务骨架和会话认证。

**Files:**

- Create: `agent-core/runtime/agent/httpapi/server.go`
- Create: `agent-core/runtime/agent/httpapi/auth.go`
- Create: `agent-core/runtime/agent/httpapi/handlers.go`
- Create: `agent-core/runtime/agent/httpapi/server_test.go`
- Create: `agent-core/runtime/agent/app/http_server.go`
- Modify: `agent-core/runtime/agent/app/bootstrap.go`

- [ ] **Step 1: 先写 HTTP 路由和认证测试**

首版至少覆盖：

- `POST <base_path>/api/login`
- `POST <base_path>/api/logout`
- `GET  <base_path>/api/session`
- `GET  <base_path>/api/app/config`
- `PUT  <base_path>/api/app/config`
- `GET  <base_path>/api/traffic/stats`
- `GET  <base_path>/api/diagnose/logs`
- `GET  <base_path>/api/services`
- `POST <base_path>/api/services`
- `DELETE <base_path>/api/services`

其中除 `login` 外全部需要登录态。

- [ ] **Step 2: 实现首版认证模型**

建议最小实现：

```go
POST /api/login
{ "username": "...", "password": "..." }
```

服务端行为：

- 使用常量时间比较校验用户名/密码
- 成功后签发 `HttpOnly + SameSite=Lax` Cookie
- 服务端内存维护 session token -> expiry
- `logout` 主动删除 cookie 和内存 session

Cookie 约束：

- `Path` 绑定到 `<base_path>/`
- 不显式设置 `Domain`，默认仅当前监听主机可用
- 本机开发默认不加 `Secure`，但要在代码结构上给后续 HTTPS 场景留开关
- HTTP session token 与 `DEV_AGENT_SESSION_SECRET` 完全隔离，不复用生成逻辑或存储字段

注意：

- 用户名/密码比较也要走常量时间路径
- 首版先做单账户即可，结构上允许未来扩展多账户
- 不在日志中打印明文密码

- [ ] **Step 3: 复用 `hostapi`，不要复制业务分发**

HTTP handler 只做：

- HTTP 请求解码
- 登录态校验
- URL / JSON -> `hostapi.Request`
- `hostapi.Response` -> JSON 输出

不得：

- 在 `httpapi` 重写 `service.add` / `diagnose.logs` 的业务逻辑
- 在 handler 内直接拼 runtime 私有状态

- [ ] **Step 4: 把 HTTP server 接到 runtime 生命周期**

运行策略：

- `ui.web.enabled=false` 时不启动 HTTP server
- `ui.web.enabled=true` 时与 `localrpc` 并行启动
- runtime shutdown 时统一关闭 HTTP listener 和 session 清理协程

需要检查：

- context cancel 时是否退出
- HTTP 启动失败是否阻断 runtime 启动
- 不引入 goroutine 泄漏
- 不把 HTTP 登录态注入 LocalRPC connection auth state
- 不要求 Tauri WebView 复用当前 IPC 登录态；后续若桌面端也要嵌 Web UI，应单独处理 WebView Cookie 初始化问题

- [ ] **Step 5: 跑 HTTP 单测与模块测试**

Run:

```bash
cd agent-core && go test ./runtime/agent/httpapi ./runtime/agent/app
```

Expected:

- 未登录访问返回 401
- 登录成功后可读取快照
- 错误密码返回 401
- logout 后会话失效

---

### Task 4: 补 Web UI 接入预留、回归验证与文档收口

**目标**

把这轮能力收敛成“浏览器可接、Tauri 不坏、文档可追踪”的可交付状态。

**Files:**

- Modify: `docs/Agent–TauriLocalCommunicationDesignProposal.md`
- Create: `agent-core/web/*`
- Create: `agent-core/web/embed.go`
- Modify: `docs/task_plan.md`
- Optional Modify: `apps/dev-agent` 相关文档或 TODO（只在需要记录兼容策略时）

- [ ] **Step 1: 明确 Web UI 首版接口契约**

文档中补清：

- 登录/登出/session 接口
- 只读快照接口
- 服务增删接口
- 返回码约定（`401/400/409/500`）

- [ ] **Step 2: 明确 LocalRPC 与 HTTP 的职责差异**

需要写清：

- LocalRPC 面向 Tauri Host，本机强信任、短链路、双向握手
- HTTP 面向浏览器 UI，弱信任、Cookie 会话、显式登录
- 两者共享 `hostapi`，但认证与 transport 不共享实现

- [ ] **Step 3: 执行 agent-core 模块回归**

Run:

```bash
cd agent-core && gofmt -w .
cd agent-core && go test -timeout 60s ./...
cd agent-core && go test -race -timeout 60s ./...
```

如环境允许再跑：

```bash
cd agent-core && golangci-lint run ./...
```

- [ ] **Step 4: 明确未覆盖联调项**

即便本轮已经完成 React Web UI，也仍需在交付说明中明确：

- 还未验证跨浏览器 Cookie 行为
- 还未做 HTTPS / 反向代理场景验证

- [ ] **Step 5: 回写总计划状态**

在 `docs/task_plan.md` 或新的专项清单中记录：

- 当前阶段完成到哪一步
- 下一阶段是否进入 Web UI 页面开发

---

## 4. 推荐执行顺序

```text
Phase A: hostapi 抽象
    ↓
Phase B: YAML 配置入口 + web.auth 配置
    ↓
Phase C: HTTP 登录 / 会话 / 路由
    ↓
Phase D: 回归验证 + 文档收口
```

原因：

- 先抽象 `hostapi`，能确保后续 HTTP 与 LocalRPC 共用一套真相源。
- 先补 YAML，能避免 HTTP 做完后再返工认证配置来源。
- 认证和 HTTP 生命周期比 `hostapi` 风险高，放在后面更稳。

---

## 5. 风险与兼容性提示

### 5.1 兼容性

- LocalRPC 对 Tauri 的 payload 字段必须保持兼容。
- 现有 `DEV_AGENT_IPC_*` / `DEV_AGENT_SESSION_SECRET` 环境变量链路不能被 HTTP 配置改坏。
- `hostapi` 抽象时不能把 runtime 私有对象泄漏给 transport 层。

### 5.2 安全

- 配置文件中的密码为高敏感信息，样例文件只能放占位值。
- 首版账号密码登录只适用于本机管理面，不应默认暴露到公网监听地址。
- 后续若要支持公网访问，必须追加 HTTPS、CSRF、登录限速与审计。

### 5.3 生命周期

- HTTP server 与 LocalRPC server 并行运行时，必须统一受 runtime context 管理。
- 不能出现 HTTP 启动失败但 runtime 假装成功运行的状态。
- 不能因内存 session 清理协程导致 goroutine 泄漏。

---

## 6. 完成标准

满足以下条件，才算本计划落地完成：

1. `localrpc` 已变成 transport adapter，核心业务逻辑下沉到 `hostapi`
2. Agent 支持从 YAML 配置读取 Web UI 开关、监听地址、账号密码
3. HTTP 登录、登出、会话校验、基础只读接口和服务管理接口可用
4. 现有 `localrpc` 相关测试不回归
5. 明确保持 “Tauri Host 继续走 LocalRPC，浏览器 / 外部 Web UI 走 HTTP” 的双链路并存模型
6. `agent-core` 模块测试通过，文档已同步

---

## 7. 建议的首个实现切片

第一轮实现建议只做：

1. `hostapi` 抽象
2. `localrpc` 改为调用 `hostapi`
3. 补 `hostapi` 单测

这一步做完后，我们再进入第二轮：

1. Agent YAML 配置入口
2. Web 认证配置结构

最后第三轮再做：

1. HTTP server
2. 登录 / 会话
3. Web API

这样能把风险拆成三个独立可验收的阶段，最适合一步一步推进。
