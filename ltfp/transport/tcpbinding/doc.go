// Package tcpbinding 提供 tcp_framed 传输绑定实现。
//
// 线格式约定如下：
// 1. 控制面使用独立长期 TCP 连接，单帧格式为 `[2B frame_type][4B payload_len][payload]`。
// 2. 数据面每条 tunnel 使用独立 TCP 连接，单帧格式为 `[4B payload_len][payload]`。
// 3. 控制面与数据面不复用同一 TCP 连接，避免在 transport 层引入额外 mux 语义。
package tcpbinding
