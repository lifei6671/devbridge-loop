# Golang 编码规范

## 目录

1. [错误处理](#1-错误处理)
2. [Context 使用](#2-context-使用)
3. [Goroutine 与并发](#3-goroutine-与并发)
4. [defer 使用](#4-defer-使用)
5. [接口与类型设计](#5-接口与类型设计)
6. [包与命名](#6-包与命名)
7. [内存与性能](#7-内存与性能)
8. [初始化与构造](#8-初始化与构造)
9. [测试](#9-测试)
10. [注释与文档](#10-注释与文档)

---

## 1. 错误处理

### 1.1 不能丢弃错误

```go
// ❌ 错误：显式丢弃可能有意义的错误
_ = os.Remove(tmpFile)

// ✅ 正确：处理或记录
if err := os.Remove(tmpFile); err != nil {
    log.Printf("failed to remove temp file: %v", err)
}
```

唯一例外：经过充分论证确实无需处理时，必须加注释说明原因。

### 1.2 错误要向上传递，不要静默吞掉

```go
// ❌ 错误：吞掉错误，上层无法感知
func loadConfig(path string) *Config {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil // 调用方不知道是"没有配置"还是"读取失败"
    }
    // ...
}

// ✅ 正确：返回错误
func loadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("loadConfig: %w", err)
    }
    // ...
}
```

### 1.3 使用 `%w` 包装错误，保留可检查性

```go
// ❌ 错误：用 %v 格式化，丢失错误类型
return fmt.Errorf("query failed: %v", err)

// ✅ 正确：用 %w 包装，调用方可以用 errors.Is / errors.As 检查
return fmt.Errorf("query failed: %w", err)
```

### 1.4 不要用 panic 替代错误返回

```go
// ❌ 错误：用 panic 处理可预期的错误
func divide(a, b int) int {
    if b == 0 {
        panic("division by zero")
    }
    return a / b
}

// ✅ 正确：返回错误
func divide(a, b int) (int, error) {
    if b == 0 {
        return 0, errors.New("division by zero")
    }
    return a / b, nil
}
```

panic 只用于真正不可恢复的程序状态（如初始化失败、不变量被破坏）。

### 1.5 哨兵错误用 `var` 声明，不要用字符串比较

```go
// ❌ 错误：字符串比较脆弱
if err.Error() == "not found" { }

// ✅ 正确：声明哨兵错误
var ErrNotFound = errors.New("not found")

if errors.Is(err, ErrNotFound) { }
```

---

## 2. Context 使用

### 2.1 永远不要传 nil context

```go
// ❌ 错误
DoSomething(nil)

// ✅ 正确：不确定时用 context.TODO()
DoSomething(context.TODO())

// ✅ 正确：明确无需取消时用 context.Background()
DoSomething(context.Background())
```

### 2.2 context 必须是函数的第一个参数，命名为 ctx

```go
// ❌ 错误
func Process(data []byte, ctx context.Context) error

// ✅ 正确
func Process(ctx context.Context, data []byte) error
```

### 2.3 不要把 context 存进结构体

```go
// ❌ 错误：context 生命周期与结构体耦合
type Worker struct {
    ctx context.Context
}

// ✅ 正确：通过方法参数传递
type Worker struct{}

func (w *Worker) Do(ctx context.Context) error { ... }
```

例外：实现标准库接口（如 `http.Request`）时可以存储，但这是库层面的特殊设计。

### 2.4 收到 context 取消信号时要及时退出

```go
// ❌ 错误：忽略取消信号，导致 goroutine 泄漏
for {
    process()
}

// ✅ 正确：监听取消
for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        process()
    }
}
```

### 2.5 不要在 context 中存储函数参数，只存请求级别的横切数据

```go
// ❌ 错误：把业务参数塞进 context
ctx = context.WithValue(ctx, "userID", 123)

// ✅ 正确：context 只存横切关注点（trace ID、认证令牌等）
ctx = context.WithValue(ctx, traceIDKey{}, traceID)

// 业务参数直接传参
func GetUser(ctx context.Context, userID int64) (*User, error)
```

---

## 3. Goroutine 与并发

### 3.1 启动 goroutine 时必须明确它何时结束

```go
// ❌ 错误：goroutine 生命周期不明确，可能泄漏
go process(data)

// ✅ 正确：用 WaitGroup 或 channel 跟踪生命周期
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    process(ctx, data)
}()
wg.Wait()
```

### 3.2 goroutine 内部的 panic 不会传播到外部，必须自行 recover

```go
// ❌ 错误：goroutine 内 panic 会导致整个程序崩溃，且无法被外部 recover
go func() {
    riskyOperation()
}()

// ✅ 正确：在 goroutine 内部 recover
go func() {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("recovered panic: %v", r)
        }
    }()
    riskyOperation()
}()
```

### 3.3 channel 的发送方负责关闭

```go
// ❌ 错误：接收方关闭 channel，发送方可能向已关闭的 channel 发送
go func() {
    close(ch) // 接收方不应关闭
}()

// ✅ 正确：只有发送方关闭 channel
go func() {
    defer close(ch)
    for _, v := range data {
        ch <- v
    }
}()
```

### 3.4 不要在持有锁时进行 I/O 或长时间操作

```go
// ❌ 错误：持锁期间做 I/O，其他 goroutine 长时间阻塞
mu.Lock()
result, err := http.Get(url) // 可能阻塞数秒
mu.Unlock()

// ✅ 正确：先完成 I/O，再获取锁写入结果
result, err := http.Get(url)
mu.Lock()
cache[url] = result
mu.Unlock()
```

### 3.5 使用 sync.Once 保证初始化只执行一次

```go
// ❌ 错误：双重检查锁定在 Go 中不可靠
if instance == nil {
    mu.Lock()
    if instance == nil {
        instance = newInstance()
    }
    mu.Unlock()
}

// ✅ 正确
var (
    instance *MyService
    once     sync.Once
)

func getInstance() *MyService {
    once.Do(func() {
        instance = newInstance()
    })
    return instance
}
```

---

## 4. defer 使用

### 4.1 defer 注册顺序与执行顺序相反（LIFO），必须考虑依赖关系

```go
// ❌ 错误：cancel 在 Wait 之后执行，goroutine 无法被取消后再等待
defer cancel()
defer wg.Wait()

// ✅ 正确：先注册 Wait（后执行），再注册 cancel（先执行）
defer wg.Wait()
defer cancel()
// 执行顺序：cancel() → wg.Wait()
```

### 4.2 在资源获取后立即 defer 释放

```go
// ✅ 正确：紧跟资源获取语句
f, err := os.Open(path)
if err != nil {
    return err
}
defer f.Close()
```

### 4.3 不要在循环中使用 defer

```go
// ❌ 错误：defer 在函数返回时才执行，循环中使用会导致资源积累
for _, path := range paths {
    f, _ := os.Open(path)
    defer f.Close() // 所有文件在函数结束时才关闭
}

// ✅ 正确：用闭包或单独函数处理
for _, path := range paths {
    func() {
        f, err := os.Open(path)
        if err != nil {
            return
        }
        defer f.Close()
        process(f)
    }()
}
```

### 4.4 注意 defer 捕获变量的时机

```go
// ❌ 错误：defer 捕获的是变量引用，result 可能已被修改
func process() (result string) {
    defer fmt.Println(result) // 打印的是空字符串，不是最终值
    result = "done"
    return
}

// ✅ 正确：用具名返回值 + 闭包
func process() (result string) {
    defer func() {
        fmt.Println(result) // 打印的是最终值
    }()
    result = "done"
    return
}
```

---

## 5. 接口与类型设计

### 5.1 接口定义在使用方，不在实现方

```go
// ❌ 错误：实现方定义接口
// 在 storage 包中
type Storage interface {
    Save(data []byte) error
}

// ✅ 正确：使用方定义接口，依赖自己需要的行为
// 在 service 包中
type dataStore interface {
    Save(data []byte) error
}
```

### 5.2 接口越小越好，单方法接口最理想

```go
// ❌ 臃肿的接口
type UserRepository interface {
    Create(u User) error
    Update(u User) error
    Delete(id int64) error
    FindByID(id int64) (User, error)
    FindByEmail(email string) (User, error)
    List(page, size int) ([]User, error)
}

// ✅ 按需拆分小接口
type UserCreator interface {
    Create(u User) error
}

type UserFinder interface {
    FindByID(id int64) (User, error)
}
```

### 5.3 不要在方法内检查 nil receiver

```go
// ❌ 错误：掩盖调用方 bug，语义模糊
func (s *Server) Start() error {
    if s == nil {
        return errors.New("server is nil")
    }
    // ...
}

// ✅ 正确：在构造阶段保证非 nil，调用方传 nil 应当 panic
func NewServer(cfg Config) (*Server, error) {
    if cfg.Addr == "" {
        return nil, errors.New("addr is required")
    }
    return &Server{cfg: cfg}, nil
}
```

### 5.4 不要返回具体类型指针的接口，优先返回接口

```go
// ❌ 容易导致 nil 接口陷阱
func NewWriter() io.Writer {
    var w *bytes.Buffer // 具体类型的 nil 指针
    return w            // 返回的接口不为 nil！
}

// ✅ 明确返回 nil
func NewWriter() io.Writer {
    return nil
}
```

---

## 6. 包与命名

### 6.1 包名用小写单数名词，不要下划线或驼峰

```
// ❌
package stringUtils
package string_utils
package Strings

// ✅
package stringutil
package http
package sync
```

### 6.2 不要在名称中重复包名

```go
// ❌ 错误：包名已经是 http，方法名不需要重复
http.HTTPClient
http.NewHTTPServer()

// ✅ 正确
http.Client
http.NewServer()
```

### 6.3 接口名在有单一方法时，用方法名 + er 命名

```go
// ✅ Go 标准库惯例
type Reader interface { Read(...) }
type Writer interface { Write(...) }
type Stringer interface { String() string }
```

### 6.4 常量组用 iota，并给类型起别名

```go
// ❌ 裸 int 常量
const (
    StatusPending = 0
    StatusRunning = 1
    StatusDone    = 2
)

// ✅ 有类型的常量，编译器可检查错误赋值
type Status int

const (
    StatusPending Status = iota
    StatusRunning
    StatusDone
)
```

---

## 7. 内存与性能

### 7.1 预分配已知容量的 slice 和 map

```go
// ❌ 频繁扩容
var result []int
for _, v := range data {
    result = append(result, v*2)
}

// ✅ 预分配
result := make([]int, 0, len(data))
for _, v := range data {
    result = append(result, v*2)
}
```

### 7.2 字符串拼接用 strings.Builder

```go
// ❌ 每次拼接都分配新内存
var s string
for _, part := range parts {
    s += part
}

// ✅ 使用 Builder
var b strings.Builder
b.Grow(estimatedSize) // 可选：预估总长度
for _, part := range parts {
    b.WriteString(part)
}
result := b.String()
```

### 7.3 大结构体传指针，小结构体传值

```go
// ❌ 大结构体传值，每次调用都复制
func process(r BigRequest) Response { ... }

// ✅ 大结构体传指针
func process(r *BigRequest) Response { ... }

// ✅ 小结构体（如坐标、颜色）传值更清晰，无需指针
type Point struct{ X, Y float64 }
func distance(a, b Point) float64 { ... }
```

### 7.4 避免在热路径中使用 fmt.Sprintf 格式化日志

```go
// ❌ 每次都分配字符串，即使日志级别未开启
log.Debug(fmt.Sprintf("processing item %d", id))

// ✅ 使用结构化日志，懒求值
slog.Debug("processing item", "id", id)
```

---

## 8. 初始化与构造

### 8.1 使用函数式选项模式处理可选参数

```go
// ❌ 参数越加越多，调用方难以维护
func NewServer(addr string, timeout int, maxConn int, tls bool) *Server

// ✅ 函数式选项
type Option func(*Server)

func WithTimeout(d time.Duration) Option {
    return func(s *Server) { s.timeout = d }
}

func NewServer(addr string, opts ...Option) *Server {
    s := &Server{addr: addr, timeout: 30 * time.Second}
    for _, opt := range opts {
        opt(s)
    }
    return s
}

// 调用方：按需传入，清晰易读
srv := NewServer(":8080", WithTimeout(10*time.Second))
```

### 8.2 构造函数中完成所有校验，返回后对象始终有效

```go
// ✅ 构造时验证，构造成功即代表对象可用
func NewClient(cfg Config) (*Client, error) {
    if cfg.BaseURL == "" {
        return nil, errors.New("BaseURL is required")
    }
    if cfg.Timeout <= 0 {
        return nil, errors.New("Timeout must be positive")
    }
    return &Client{cfg: cfg}, nil
}
```

### 8.3 不要在 init() 中做复杂初始化

```go
// ❌ init 中的错误无法被调用方处理
func init() {
    db, _ = sql.Open("postgres", os.Getenv("DSN"))
}

// ✅ 显式初始化函数，错误可以被处理
func InitDB(dsn string) (*sql.DB, error) {
    return sql.Open("postgres", dsn)
}
```

---

## 9. 测试

### 9.1 表驱动测试覆盖边界条件

```go
func TestDivide(t *testing.T) {
    cases := []struct {
        name    string
        a, b    int
        want    int
        wantErr bool
    }{
        {"normal", 10, 2, 5, false},
        {"divide by zero", 10, 0, 0, true},
        {"negative", -10, 2, -5, false},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := divide(tc.a, tc.b)
            if (err != nil) != tc.wantErr {
                t.Fatalf("unexpected error: %v", err)
            }
            if got != tc.want {
                t.Errorf("got %d, want %d", got, tc.want)
            }
        })
    }
}
```

### 9.2 测试文件中用 `_test` 包名测试公开 API

```go
// 文件：parser_test.go
package parser_test  // 黑盒测试，只能访问导出符号

import "myproject/parser"

func TestParse(t *testing.T) {
    parser.Parse("input")
}
```

### 9.3 用 t.Cleanup 替代 defer 做测试清理

```go
func TestWithDB(t *testing.T) {
    db := setupTestDB(t)
    t.Cleanup(func() {
        db.Close() // 即使子测试也会执行
    })
    // ...
}
```

---

## 10. 注释与文档

### 10.1 所有导出符号必须有文档注释

```go
// ❌ 无注释
func Parse(input string) (*AST, error)

// ✅ 以符号名开头的完整句子
// Parse parses the input string and returns an AST.
// It returns an error if the input is malformed.
func Parse(input string) (*AST, error)
```

### 10.2 注释解释"为什么"，而不是"做了什么"

```go
// ❌ 重复代码本身的信息
// 将 i 加 1
i++

// ✅ 解释不显而易见的原因
// Skip the header byte; the protocol reserves it for future use.
i++
```

### 10.3 TODO 注释要包含可追踪的信息

```go
// ❌ 无法追踪
// TODO: fix this

// ✅ 包含责任人或 issue 引用
// TODO(zhang): remove after migration to v2 API, see issue #1234
```

---

## 快速检查清单

在提交代码前，逐项确认：

- [ ] 所有错误都被处理或有注释说明忽略原因
- [ ] 没有传递 nil context
- [ ] 启动的每个 goroutine 都有明确的退出条件
- [ ] defer 的注册顺序符合预期执行顺序
- [ ] 循环中没有使用 defer
- [ ] 接口定义在使用方包内
- [ ] 导出的函数、类型、常量都有文档注释
- [ ] 测试覆盖了正常路径和边界条件