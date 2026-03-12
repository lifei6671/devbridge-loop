# 一、新的传输方案定义

这个新方案可以直接命名为：

> **LTFP Transport Abstraction v2**

它的核心思想不是“先定 gRPC 接口”，而是：

> **先定义统一的传输抽象，再让 gRPC / QUIC / TCP 去实现这个抽象。**

也就是说，今后你的协议库依赖的是抽象接口，而不是依赖具体的 gRPC stream。

---

## 1.1 设计目标

新的传输方案解决四个问题：

### 第一，协议层不依赖具体传输

协议消息如：

* `ConnectorHello`
* `PublishService`
* `TrafficOpen`
* `TrafficOpenAck`
* `TrafficClose`
* `TrafficReset`

其语义必须独立于 gRPC、QUIC、TCP。

---

### 第二，控制面与数据面彻底分离

控制面只处理：

* 建链
* 鉴权
* 心跳
* 注册发布
* 控制类错误
* 路由协商

数据面只处理：

* 单个 traffic 的建立
* 单个 traffic 的字节流传输
* 单个 traffic 的关闭 / reset

---

### 第三，一个 traffic 对应一个原生独立流

这是新方案的硬约束。

即：

* gRPC：一个 traffic 对应一个 bidi stream
* QUIC：一个 traffic 对应一个 QUIC stream
* TCP fallback：一个 traffic 对应一个独立 TCP 连接

**禁止**在一个大 stream 内再用 `traffic_id` 二次复用多个逻辑流。

---

### 第四，传输层只做承载，不做业务决策

binding 层只关心：

* 开流
* 收发消息
* 收发数据
* 关闭
* reset
* 报告错误

binding 层不应关心：

* 哪个 service_key 应该路由去哪
* 某个 connector 是否有权限
* 某个 traffic 是不是命中某个服务实例

这些属于上层协议或路由层。

---

# 二、方案分层

新的方案建议拆成四层。

---

## 2.1 Layer 1：Protocol Model

定义纯协议对象，不关心底层如何传。

例如：

* `ControlEnvelope`
* `TrafficOpen`
* `TrafficOpenAck`
* `TrafficClose`
* `TrafficReset`
* `StreamFrame`

这一层只定义结构和语义。

---

## 2.2 Layer 2：Transport Abstraction

定义统一抽象接口：

* `Session`
* `ControlChannel`
* `TrafficChannel`
* `Transport`
* `Listener`

这一层是整个新方案的核心。

协议库只依赖这层。

---

## 2.3 Layer 3：Binding Implementation

具体实现：

* `grpcbinding`
* `quicbinding`
* `tcpbinding`

这一层负责把抽象接口映射到底层协议。

---

## 2.4 Layer 4：Runtime Orchestrator

这一层负责：

* connector 端会话管理
* server 端会话管理
* 心跳
* 重连
* service publish
* traffic 调度

这一层依赖协议层和 transport abstraction，但不能反过来污染抽象层。

---

# 三、新方案的核心对象

---

## 3.1 Session

`Session` 表示一次 connector 与 server 之间的传输会话。

它是控制面和数据面共同的上层锚点。

职责：

* 建立底层连接上下文
* 创建控制通道
* 创建数据通道
* 管理会话生命周期
* 提供对端 / 本端元信息
* 汇报 session 级错误

---

## 3.2 ControlChannel

`ControlChannel` 表示长期存在的控制面通道。

特点：

* 生命周期通常与 session 一致
* 双向收发控制消息
* 不承载业务字节流
* 一条 session 只能有一个活动 control channel

---

## 3.3 TrafficChannel

`TrafficChannel` 表示单个 traffic 对应的数据面通道。

特点：

* 一条 `TrafficChannel` 只服务一个 `traffic_id`
* 首消息必须是 `TrafficOpen`
* `OpenAck` 成功后才允许传字节流
* 关闭与 reset 只作用于该 traffic，不直接影响整个 session

---

# 四、统一语义约束

---

## 4.1 Session 级语义

一个 session 的生命周期建议定义为：

* `idle`
* `connecting`
* `connected`
* `control_ready`
* `authenticated`
* `draining`
* `closed`
* `failed`

规则：

* 未认证前不可分配新 traffic
* `draining` 后不可再开新 traffic
* `failed` 后必须释放所有资源
* 不允许在同一 `session_epoch` 内从 `failed` 恢复到 `authenticated`

---

## 4.2 Traffic 级语义

单个 traffic 生命周期建议定义为：

* `opening`
* `ack_wait`
* `established`
* `closing`
* `closed`
* `reset`

规则：

* `TrafficOpen` 必须是首个协议动作
* 只有 `OpenAck(success=true)` 后才能发数据
* `TrafficReset` 到达后该 traffic 必须立刻终止
* traffic 失败默认不等于 session 失败

---

## 4.3 错误分层

必须区分三类错误：

### transport error

底层网络或 binding 失败：

* TCP 断开
* TLS 握手失败
* gRPC stream cancel
* QUIC stream reset

### protocol error

协议顺序或字段不合法：

* 未 open 就发 data
* open 后没 ack 就继续写
* 必填字段缺失
* 非法状态流转

### application rejection

业务层拒绝：

* service 不存在
* 权限不匹配
* 本地目标连接失败
* namespace 不匹配

这三类错误必须在代码模型里分开，否则后续调试会很痛苦。

---

# 五、Go 抽象接口定义

下面这部分是核心。
我给你的不是 demo，而是一套适合长期演进的接口模型。

---

## 5.1 基础类型

```go
package transport

import (
	"context"
	"errors"
	"io"
	"time"
)
```

---

### 5.1.1 BindingType

```go
package transport

type BindingType string

const (
	BindingGRPC BindingType = "grpc_h2"
	BindingQUIC BindingType = "quic_native"
	BindingH3   BindingType = "h3_stream"
	BindingTCP  BindingType = "tcp_framed"
)
```

---

### 5.1.2 SessionState / TrafficState

```go
package transport

type SessionState string

const (
	SessionStateIdle          SessionState = "idle"
	SessionStateConnecting    SessionState = "connecting"
	SessionStateConnected     SessionState = "connected"
	SessionStateControlReady  SessionState = "control_ready"
	SessionStateAuthenticated SessionState = "authenticated"
	SessionStateDraining      SessionState = "draining"
	SessionStateClosed        SessionState = "closed"
	SessionStateFailed        SessionState = "failed"
)

type TrafficState string

const (
	TrafficStateOpening     TrafficState = "opening"
	TrafficStateAckWait     TrafficState = "ack_wait"
	TrafficStateEstablished TrafficState = "established"
	TrafficStateClosing     TrafficState = "closing"
	TrafficStateClosed      TrafficState = "closed"
	TrafficStateReset       TrafficState = "reset"
)
```

---

### 5.1.3 Endpoint / BindingInfo

```go
package transport

type Endpoint struct {
	Network string
	Address string
}

type BindingInfo struct {
	Type                 BindingType
	Local                Endpoint
	Remote               Endpoint
	SupportsHalfClose    bool
	SupportsStreamReset  bool
	MaxConcurrentStreams int64
}
```

---

### 5.1.4 Metadata

```go
package transport

type SessionMeta struct {
	SessionID    string
	SessionEpoch uint64
	NodeID       string
	Labels       map[string]string
}

type TrafficMeta struct {
	TrafficID    string
	SessionID    string
	SessionEpoch uint64
	ServiceKey   string
	Labels       map[string]string
	DeadlineAt   *time.Time
}
```

---

# 六、控制面与数据面消息接口

这里我建议不要在 transport 抽象层强绑定 protobuf 具体类型。
否则你的 transport 包会反向依赖协议定义，容易循环依赖。

更好的方式是：transport 层只认“控制消息对象”和“open/ack/close/reset 对象”的抽象。

---

## 6.1 ControlMessage / OpenMessage 等接口

```go
package transport

type ControlMessage interface {
	ControlMessageName() string
}

type TrafficOpenMessage interface {
	TrafficOpenMessageName() string
}

type TrafficOpenAckMessage interface {
	TrafficOpenAckMessageName() string
	Accepted() bool
}

type TrafficCloseMessage interface {
	TrafficCloseMessageName() string
}

type TrafficResetMessage interface {
	TrafficResetMessageName() string
}
```

实际项目中由你的 `protocol` 包去实现这些接口，例如：

```go
func (*ConnectorHello) ControlMessageName() string { return "ConnectorHello" }
func (*TrafficOpen) TrafficOpenMessageName() string { return "TrafficOpen" }
```

这样 transport 层不需要知道 protobuf 的字段细节。

---

# 七、Session 抽象

---

## 7.1 Session 接口

```go
package transport

type Session interface {
	ID() string
	Meta() SessionMeta
	State() SessionState
	BindingInfo() BindingInfo

	Open(ctx context.Context) error
	Close(ctx context.Context, reason error) error

	Control() (ControlChannel, error)

	OpenTraffic(ctx context.Context, meta TrafficMeta) (TrafficChannel, error)
	AcceptTraffic(ctx context.Context) (TrafficChannel, TrafficMeta, error)

	Done() <-chan struct{}
	Err() error
}
```

---

## 7.2 语义说明

### `Open`

建立底层连接和必要握手，但不等于已经完成业务认证。

### `Control`

返回该 session 唯一的 control channel。
如果 control channel 尚未建立，应返回错误或阻塞到 ready，具体可由实现约定。

### `OpenTraffic`

主动创建一个新的数据面通道。

### `AcceptTraffic`

被动接受对端发起的新数据面通道。

### `Close`

关闭整个 session。必须连带关闭：

* control channel
* 所有 active traffic channel

### `Done / Err`

用于统一监听 session 生命周期。

---

# 八、ControlChannel 抽象

---

## 8.1 ControlChannel 接口

```go
package transport

type ControlChannel interface {
	Send(ctx context.Context, msg ControlMessage) error
	Recv(ctx context.Context) (ControlMessage, error)

	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}
```

---

## 8.2 语义说明

### `Send`

消息被 transport 层接受发送，不代表业务已处理成功。

### `Recv`

读取一个控制消息。
返回 `io.EOF` 表示正常结束。
返回其他 error 表示异常断开或解码失败。

### `Close`

仅关闭控制通道，不强制要求立刻关闭 session。
但很多实现中 control channel 断开后 session 通常也会结束。

---

# 九、TrafficChannel 抽象

这部分最关键。

---

## 9.1 TrafficChannel 接口

```go
package transport

type TrafficChannel interface {
	ID() string
	Meta() TrafficMeta
	State() TrafficState

	SendOpen(ctx context.Context, msg TrafficOpenMessage) error
	RecvOpen(ctx context.Context) (TrafficOpenMessage, error)

	SendOpenAck(ctx context.Context, msg TrafficOpenAckMessage) error
	RecvOpenAck(ctx context.Context) (TrafficOpenAckMessage, error)

	Write(ctx context.Context, p []byte) (int, error)
	Read(ctx context.Context, p []byte) (int, error)

	SendClose(ctx context.Context, msg TrafficCloseMessage) error
	RecvClose(ctx context.Context) (TrafficCloseMessage, error)

	SendReset(ctx context.Context, msg TrafficResetMessage) error
	RecvReset(ctx context.Context) (TrafficResetMessage, error)

	CloseWrite(ctx context.Context) error
	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}
```

---

## 9.2 为什么不用直接继承 `io.ReadWriteCloser`

因为这个通道不是纯字节流，它有明确的协议阶段：

* open
* ack
* data
* close/reset

如果直接只暴露 `io.ReadWriteCloser`，调用者很容易绕过状态机，导致：

* 没 open 就 write
* 未 ack 就 read/write
* close/reset 混乱

所以这里显式定义更稳。

---

## 9.3 数据传输建议

虽然接口里是 `Read/Write`，但它承载的是**字节流语义**，不是消息语义。

也就是说：

* gRPC 下底层可能是 message stream
* QUIC 下底层可能是真正 stream
* transport 层应对上统一为“顺序字节流”

这样上层无需关心底层分片。

---

# 十、Listener / Dialer 抽象

为了让 server / connector 两端都统一，我建议再抽一层工厂接口。

---

## 10.1 Dialer

```go
package transport

type Dialer interface {
	Dial(ctx context.Context, target string, opts DialOptions) (Session, error)
}
```

---

## 10.2 Listener

```go
package transport

type Listener interface {
	Accept(ctx context.Context) (Session, error)
	Close() error
	Addr() Endpoint
}
```

---

## 10.3 Transport

```go
package transport

type Transport interface {
	Type() BindingType

	NewDialer(opts DialerOptions) (Dialer, error)
	NewListener(opts ListenerOptions) (Listener, error)
}
```

---

## 10.4 Options

```go
package transport

type TLSOptions struct {
	Enable            bool
	ServerName        string
	InsecureSkipVerify bool
	CAFile            string
	CertFile          string
	KeyFile           string
}

type DialOptions struct {
	SessionMeta SessionMeta
	TLS         TLSOptions
	Extra       map[string]any
}

type DialerOptions struct {
	TLS   TLSOptions
	Extra map[string]any
}

type ListenerOptions struct {
	Address string
	TLS     TLSOptions
	Extra   map[string]any
}
```

---

# 十一、错误抽象

我强烈建议把错误模型从一开始就独立出来。

---

## 11.1 错误分类

```go
package transport

type ErrorKind string

const (
	ErrorKindTransport  ErrorKind = "transport"
	ErrorKindProtocol   ErrorKind = "protocol"
	ErrorKindReject     ErrorKind = "reject"
	ErrorKindTimeout    ErrorKind = "timeout"
	ErrorKindClosed     ErrorKind = "closed"
	ErrorKindInternal   ErrorKind = "internal"
)
```

---

## 11.2 TransportError

```go
package transport

type Error struct {
	Kind      ErrorKind
	Op        string
	Message   string
	Temporary bool
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return string(e.Kind) + ": " + e.Op + ": " + e.Message + ": " + e.Cause.Error()
	}
	return string(e.Kind) + ": " + e.Op + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
```

---

## 11.3 哨兵错误

```go
package transport

var (
	ErrClosed        = errors.New("transport closed")
	ErrSessionClosed = errors.New("session closed")
	ErrTrafficClosed = errors.New("traffic closed")
	ErrReset         = errors.New("traffic reset")
	ErrTimeout       = errors.New("timeout")
	ErrProtocol      = errors.New("protocol violation")
)
```

---

# 十二、推荐的 Go 包结构

我建议直接按下面拆。

```text
ltfp/
  protocol/
    control.go
    traffic.go
    envelopes.go
    errors.go

  transport/
    types.go
    errors.go
    session.go
    control.go
    traffic.go
    factory.go
    options.go

  transport/grpcbinding/
    transport.go
    dialer.go
    listener.go
    session.go
    control_channel.go
    traffic_channel.go
    codec.go
    mapper.go

  transport/quicbinding/
    transport.go
    dialer.go
    listener.go
    session.go
    control_channel.go
    traffic_channel.go

  runtime/
    connector/
      client.go
      session_manager.go
      publisher.go
      heartbeat.go
    server/
      server.go
      session_registry.go
      router.go
      dispatcher.go
```

---

# 十三、实现建议：协议库依赖方向

依赖方向应当固定为：

```text
protocol <- transport abstraction <- binding implementation <- runtime
```

而不是：

```text
grpc实现 -> 直接定义协议 -> runtime 绑定 grpc
```

原因很简单：

如果一开始把协议直接做进 gRPC service 定义里，后面想加 QUIC 会非常痛苦。
你会发现很多“状态语义”和“流关闭语义”都被 gRPC 的调用模型绑死了。

所以正确做法是：

* `protocol` 定义消息
* `transport` 定义抽象
* `grpcbinding` 实现抽象
* `runtime` 组合使用

---

# 十四、一个更稳的落地方式

如果你问我工程上怎么分阶段，我建议这样。

---

## 阶段一：先落抽象层

先把这些接口和错误模型固定：

* `Session`
* `ControlChannel`
* `TrafficChannel`
* `Transport`
* `Listener`
* `Dialer`

这一步最重要。

---

## 阶段二：只做 grpcbinding

首版先只实现：

* `grpc_h2`

这样最现实。

---

## 阶段三：runtime 只依赖 transport

connector 和 server 的运行时层不得直接 import gRPC stream 类型。
只能依赖 `transport.Session` / `transport.TrafficChannel`。

这一步能保证后续可替换性。

---

# 十五、我给你的最终建议口径

如果你要对内做方案说明，我建议你这样表述：

> 传输层不再直接以 gRPC 接口作为协议标准，而是重构为一套独立的 Transport Abstraction。
> 上层协议只定义控制面与数据面消息语义，底层由不同 binding 实现统一抽象接口。
> 首版落地采用 grpc_h2 binding，但协议库与运行时均不直接绑定 gRPC 类型，从而为后续 QUIC / H3 演进保留空间。

---

# 十六、一版可直接拷贝的 Go 接口汇总

下面给你一份更完整、可直接放进代码库的版本。

```go
package transport

import (
	"context"
	"errors"
	"time"
)

type BindingType string

const (
	BindingGRPC BindingType = "grpc_h2"
	BindingQUIC BindingType = "quic_native"
	BindingH3   BindingType = "h3_stream"
	BindingTCP  BindingType = "tcp_framed"
)

type SessionState string

const (
	SessionStateIdle          SessionState = "idle"
	SessionStateConnecting    SessionState = "connecting"
	SessionStateConnected     SessionState = "connected"
	SessionStateControlReady  SessionState = "control_ready"
	SessionStateAuthenticated SessionState = "authenticated"
	SessionStateDraining      SessionState = "draining"
	SessionStateClosed        SessionState = "closed"
	SessionStateFailed        SessionState = "failed"
)

type TrafficState string

const (
	TrafficStateOpening     TrafficState = "opening"
	TrafficStateAckWait     TrafficState = "ack_wait"
	TrafficStateEstablished TrafficState = "established"
	TrafficStateClosing     TrafficState = "closing"
	TrafficStateClosed      TrafficState = "closed"
	TrafficStateReset       TrafficState = "reset"
)

type Endpoint struct {
	Network string
	Address string
}

type BindingInfo struct {
	Type                 BindingType
	Local                Endpoint
	Remote               Endpoint
	SupportsHalfClose    bool
	SupportsStreamReset  bool
	MaxConcurrentStreams int64
}

type SessionMeta struct {
	SessionID    string
	SessionEpoch uint64
	NodeID       string
	Labels       map[string]string
}

type TrafficMeta struct {
	TrafficID    string
	SessionID    string
	SessionEpoch uint64
	ServiceKey   string
	Labels       map[string]string
	DeadlineAt   *time.Time
}

type ControlMessage interface {
	ControlMessageName() string
}

type TrafficOpenMessage interface {
	TrafficOpenMessageName() string
}

type TrafficOpenAckMessage interface {
	TrafficOpenAckMessageName() string
	Accepted() bool
}

type TrafficCloseMessage interface {
	TrafficCloseMessageName() string
}

type TrafficResetMessage interface {
	TrafficResetMessageName() string
}

type Session interface {
	ID() string
	Meta() SessionMeta
	State() SessionState
	BindingInfo() BindingInfo

	Open(ctx context.Context) error
	Close(ctx context.Context, reason error) error

	Control() (ControlChannel, error)

	OpenTraffic(ctx context.Context, meta TrafficMeta) (TrafficChannel, error)
	AcceptTraffic(ctx context.Context) (TrafficChannel, TrafficMeta, error)

	Done() <-chan struct{}
	Err() error
}

type ControlChannel interface {
	Send(ctx context.Context, msg ControlMessage) error
	Recv(ctx context.Context) (ControlMessage, error)

	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}

type TrafficChannel interface {
	ID() string
	Meta() TrafficMeta
	State() TrafficState

	SendOpen(ctx context.Context, msg TrafficOpenMessage) error
	RecvOpen(ctx context.Context) (TrafficOpenMessage, error)

	SendOpenAck(ctx context.Context, msg TrafficOpenAckMessage) error
	RecvOpenAck(ctx context.Context) (TrafficOpenAckMessage, error)

	Write(ctx context.Context, p []byte) (int, error)
	Read(ctx context.Context, p []byte) (int, error)

	SendClose(ctx context.Context, msg TrafficCloseMessage) error
	RecvClose(ctx context.Context) (TrafficCloseMessage, error)

	SendReset(ctx context.Context, msg TrafficResetMessage) error
	RecvReset(ctx context.Context) (TrafficResetMessage, error)

	CloseWrite(ctx context.Context) error
	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}

type TLSOptions struct {
	Enable             bool
	ServerName         string
	InsecureSkipVerify bool
	CAFile             string
	CertFile           string
	KeyFile            string
}

type DialOptions struct {
	SessionMeta SessionMeta
	TLS         TLSOptions
	Extra       map[string]any
}

type DialerOptions struct {
	TLS   TLSOptions
	Extra map[string]any
}

type ListenerOptions struct {
	Address string
	TLS     TLSOptions
	Extra   map[string]any
}

type Dialer interface {
	Dial(ctx context.Context, target string, opts DialOptions) (Session, error)
}

type Listener interface {
	Accept(ctx context.Context) (Session, error)
	Close() error
	Addr() Endpoint
}

type Transport interface {
	Type() BindingType
	NewDialer(opts DialerOptions) (Dialer, error)
	NewListener(opts ListenerOptions) (Listener, error)
}

type ErrorKind string

const (
	ErrorKindTransport ErrorKind = "transport"
	ErrorKindProtocol  ErrorKind = "protocol"
	ErrorKindReject    ErrorKind = "reject"
	ErrorKindTimeout   ErrorKind = "timeout"
	ErrorKindClosed    ErrorKind = "closed"
	ErrorKindInternal  ErrorKind = "internal"
)

type Error struct {
	Kind      ErrorKind
	Op        string
	Message   string
	Temporary bool
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return string(e.Kind) + ": " + e.Op + ": " + e.Message + ": " + e.Cause.Error()
	}
	return string(e.Kind) + ": " + e.Op + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var (
	ErrClosed        = errors.New("transport closed")
	ErrSessionClosed = errors.New("session closed")
	ErrTrafficClosed = errors.New("traffic closed")
	ErrReset         = errors.New("traffic reset")
	ErrTimeout       = errors.New("timeout")
	ErrProtocol      = errors.New("protocol violation")
)
```
