# LTFP Transport Diagnostics Samples

本文档记录传输层首版常见故障的最小诊断样例，便于快速定位。

## 1. Control HOL

场景：大体积 `PublishService` 排队，heartbeat 延迟上升但尚未判死。

```json
{
  "component": "control_channel",
  "event": "heartbeat_delayed",
  "session_id": "s-1001",
  "session_epoch": 7,
  "binding": "grpc_h2",
  "queue_depth_high": 23,
  "queue_depth_normal": 118,
  "heartbeat_rtt_ms": 842,
  "retryable": true
}
```

## 2. Fragmentation

场景：控制面大消息被切片重组，耗时明显升高。

```json
{
  "component": "control_fragmentation",
  "event": "reassemble_done",
  "session_id": "s-1002",
  "session_epoch": 3,
  "binding": "tcp_framed",
  "message_type": 1003,
  "fragment_count": 17,
  "payload_bytes": 1048576
}
```

## 3. Concurrent Write Serialization

场景：单 tunnel 多写方竞争，运行时做串行化保护。

```json
{
  "component": "runtime_protocol",
  "event": "write_serialized",
  "session_id": "s-1003",
  "session_epoch": 11,
  "binding": "grpc_h2",
  "tunnel_id": "t-7788",
  "traffic_id": "tr-911",
  "frame_type": "data"
}
```

## 4. Keepalive / Probe

场景：idle tunnel 探活超时，未立即判定 broken；连续失败后清理。

```json
{
  "component": "tunnel_pool",
  "event": "idle_probe_timeout",
  "session_id": "s-1004",
  "session_epoch": 5,
  "binding": "tcp_framed",
  "tunnel_id": "t-9911",
  "idle_count": 9,
  "in_use_count": 12,
  "broken_tunnel_count": 0
}
```

```json
{
  "component": "tunnel_pool",
  "event": "idle_tunnel_evicted",
  "session_id": "s-1004",
  "session_epoch": 5,
  "binding": "tcp_framed",
  "tunnel_id": "t-9911",
  "reason": "probe_failed",
  "broken_tunnel_count": 1
}
```
