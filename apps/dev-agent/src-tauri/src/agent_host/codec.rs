use serde_json::Value;
use std::io::{ErrorKind, Read, Write};

use super::frame::{
    is_zero_request_id, LocalRpcFrame, LocalRpcFrameType, LOCAL_RPC_HEADER_LEN, LOCAL_RPC_MAGIC,
    LOCAL_RPC_MAX_BODY_LEN, LOCAL_RPC_VERSION,
};

/// 将 JSON body 解析为 Value，失败时统一返回协议错误。
pub fn parse_json_body(body: &[u8]) -> Result<Value, String> {
    serde_json::from_slice::<Value>(body)
        .map_err(|err| format!("PROTOCOL_ERROR: JSON 解码失败: {err}"))
}

/// 校验帧头与帧体约束，避免非法包进入业务处理分支。
pub fn validate_local_rpc_frame(frame: &LocalRpcFrame) -> Result<(), String> {
    if frame.body.len() > LOCAL_RPC_MAX_BODY_LEN as usize {
        return Err("FRAME_TOO_LARGE: BodyLen 超过 1MiB".to_string());
    }
    match frame.frame_type {
        LocalRpcFrameType::Request | LocalRpcFrameType::Response => {
            if is_zero_request_id(&frame.request_id) {
                return Err("PROTOCOL_ERROR: request/response 的 RequestId 不能为零".to_string());
            }
        }
        LocalRpcFrameType::Event | LocalRpcFrameType::Ping | LocalRpcFrameType::Pong => {
            if !is_zero_request_id(&frame.request_id) {
                return Err("PROTOCOL_ERROR: event/ping/pong 的 RequestId 必须为全零".to_string());
            }
        }
    }
    if matches!(
        frame.frame_type,
        LocalRpcFrameType::Ping | LocalRpcFrameType::Pong
    ) && !frame.body.is_empty()
    {
        return Err("PROTOCOL_ERROR: ping/pong 的 BodyLen 必须为 0".to_string());
    }
    if !frame.body.is_empty() {
        let json_value = parse_json_body(&frame.body)?;
        if json_value.get("request_id").is_some() {
            return Err("PROTOCOL_ERROR: JSON body 不允许出现 request_id".to_string());
        }
    }
    Ok(())
}

/// 将帧编码到连接上，遵守固定头 + body 的写入顺序。
pub fn write_local_rpc_frame(writer: &mut impl Write, frame: &LocalRpcFrame) -> Result<(), String> {
    validate_local_rpc_frame(frame)?;
    let body_len_u32 =
        u32::try_from(frame.body.len()).map_err(|_| "FRAME_TOO_LARGE: BodyLen 溢出".to_string())?;
    let mut header = [0_u8; LOCAL_RPC_HEADER_LEN];
    header[0..4].copy_from_slice(&LOCAL_RPC_MAGIC);
    header[4..6].copy_from_slice(&LOCAL_RPC_VERSION.to_be_bytes());
    header[6..8].copy_from_slice(&frame.frame_type.to_wire().to_be_bytes());
    header[8..12].copy_from_slice(&frame.flags.to_be_bytes());
    header[12..28].copy_from_slice(&frame.request_id);
    header[28..32].copy_from_slice(&body_len_u32.to_be_bytes());
    writer
        .write_all(&header)
        .map_err(|err| format!("写入帧头失败: {err}"))?;
    if !frame.body.is_empty() {
        writer
            .write_all(&frame.body)
            .map_err(|err| format!("写入帧体失败: {err}"))?;
    }
    writer
        .flush()
        .map_err(|err| format!("刷新帧写缓冲失败: {err}"))?;
    Ok(())
}

/// 从连接解码一帧，严格按“先验头、后分配 body”顺序执行。
pub fn read_local_rpc_frame(reader: &mut impl Read) -> Result<LocalRpcFrame, String> {
    let mut header = [0_u8; LOCAL_RPC_HEADER_LEN];
    reader.read_exact(&mut header).map_err(|err| {
        if matches!(err.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) {
            "读取本地 RPC 帧超时".to_string()
        } else {
            format!("读取本地 RPC 帧头失败: {err}")
        }
    })?;

    let magic = [header[0], header[1], header[2], header[3]];
    if magic != LOCAL_RPC_MAGIC {
        return Err("PROTOCOL_ERROR: 帧魔数不匹配".to_string());
    }
    let version = u16::from_be_bytes([header[4], header[5]]);
    if version != LOCAL_RPC_VERSION {
        return Err(format!("PROTOCOL_ERROR: 不支持协议版本 {version}"));
    }
    let frame_type = LocalRpcFrameType::from_wire(u16::from_be_bytes([header[6], header[7]]))?;
    let flags = u32::from_be_bytes([header[8], header[9], header[10], header[11]]);
    let mut request_id = [0_u8; 16];
    request_id.copy_from_slice(&header[12..28]);
    let body_len = u32::from_be_bytes([header[28], header[29], header[30], header[31]]);
    if body_len > LOCAL_RPC_MAX_BODY_LEN {
        return Err("FRAME_TOO_LARGE: BodyLen 超过 1MiB".to_string());
    }

    // 通过 BodyLen 校验后才分配内存，避免恶意包触发大对象申请。
    let mut body = vec![0_u8; body_len as usize];
    if body_len > 0 {
        reader.read_exact(&mut body).map_err(|err| {
            if matches!(err.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) {
                "读取本地 RPC 帧体超时".to_string()
            } else {
                format!("读取本地 RPC 帧体失败: {err}")
            }
        })?;
    }
    let frame = LocalRpcFrame {
        frame_type,
        flags,
        request_id,
        body,
    };
    validate_local_rpc_frame(&frame)?;
    Ok(frame)
}

#[cfg(test)]
mod local_rpc_tests {
    use super::*;
    use crate::agent_host::frame::{build_request_id, LOCAL_RPC_MAX_BODY_LEN};
    use serde_json::json;
    use std::io::Cursor;

    /// 验证 ping/pong 帧必须为空 body。
    #[test]
    fn ping_body_must_be_empty() {
        let invalid_ping = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Ping,
            flags: 0,
            request_id: [0; 16],
            body: b"bad".to_vec(),
        };
        let result = validate_local_rpc_frame(&invalid_ping);
        assert!(result.is_err());
    }

    /// 验证 request/response 的 RequestId 不能为全零。
    #[test]
    fn request_id_rules_for_request_response() {
        let invalid_request = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Request,
            flags: 0,
            request_id: [0; 16],
            body: serde_json::to_vec(&json!({"method":"agent.snapshot","payload":{}}))
                .expect("序列化 request body 失败"),
        };
        let result = validate_local_rpc_frame(&invalid_request);
        assert!(result.is_err());
    }

    /// 验证 JSON body 不允许包含 request_id 字段。
    #[test]
    fn request_id_field_in_json_is_rejected() {
        let invalid_event = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Event,
            flags: 0,
            request_id: [0; 16],
            body: serde_json::to_vec(&json!({"request_id":"abc","event":"x"}))
                .expect("序列化 event body 失败"),
        };
        let result = validate_local_rpc_frame(&invalid_event);
        assert!(result.is_err());
    }

    /// 验证编解码 roundtrip 保持字段一致。
    #[test]
    fn frame_roundtrip_codec() {
        let request_id = build_request_id(1234, 7);
        let frame = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Response,
            flags: 0,
            request_id,
            body: serde_json::to_vec(&json!({"ok":true,"payload":{"x":1}}))
                .expect("序列化 response body 失败"),
        };

        let mut encoded = Vec::new();
        write_local_rpc_frame(&mut encoded, &frame).expect("写入帧失败");
        let mut cursor = Cursor::new(encoded);
        let decoded = read_local_rpc_frame(&mut cursor).expect("读取帧失败");

        assert_eq!(decoded.frame_type, LocalRpcFrameType::Response);
        assert_eq!(decoded.request_id, request_id);
    }

    /// 验证 BodyLen 超限时会直接拒绝，不继续读取 body。
    #[test]
    fn reject_too_large_body_len() {
        let mut header = [0_u8; LOCAL_RPC_HEADER_LEN];
        header[0..4].copy_from_slice(&LOCAL_RPC_MAGIC);
        header[4..6].copy_from_slice(&LOCAL_RPC_VERSION.to_be_bytes());
        header[6..8].copy_from_slice(&LocalRpcFrameType::Event.to_wire().to_be_bytes());
        header[8..12].copy_from_slice(&0_u32.to_be_bytes());
        header[12..28].copy_from_slice(&[0; 16]);
        // 直接声明超过 1MiB，触发 FRAME_TOO_LARGE。
        header[28..32].copy_from_slice(&(LOCAL_RPC_MAX_BODY_LEN + 1).to_be_bytes());

        let mut cursor = Cursor::new(header.to_vec());
        let result = read_local_rpc_frame(&mut cursor);
        assert!(result.is_err());
    }
}
