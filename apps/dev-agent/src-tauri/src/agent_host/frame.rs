/// 本地 RPC 固定头长度：Magic(4)+Version(2)+Type(2)+Flags(4)+RequestId(16)+BodyLen(4)。
pub const LOCAL_RPC_HEADER_LEN: usize = 32;
/// 本地 RPC 单帧 body 上限：1MiB。
pub const LOCAL_RPC_MAX_BODY_LEN: u32 = 1_048_576;
/// 本地 RPC 协议魔数。
pub const LOCAL_RPC_MAGIC: [u8; 4] = *b"LRPC";
/// 本地 RPC 协议版本。
pub const LOCAL_RPC_VERSION: u16 = 1;

/// 本地 RPC 帧类型：严格限制为 request/response/event/ping/pong 五类。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LocalRpcFrameType {
    Request,
    Response,
    Event,
    Ping,
    Pong,
}

impl LocalRpcFrameType {
    /// 将帧类型转换为线上协议的 u16 编码。
    pub fn to_wire(self) -> u16 {
        match self {
            Self::Request => 1,
            Self::Response => 2,
            Self::Event => 3,
            Self::Ping => 4,
            Self::Pong => 5,
        }
    }

    /// 将线上协议编码还原为枚举值，非法值直接拒绝。
    pub fn from_wire(value: u16) -> Result<Self, String> {
        match value {
            1 => Ok(Self::Request),
            2 => Ok(Self::Response),
            3 => Ok(Self::Event),
            4 => Ok(Self::Ping),
            5 => Ok(Self::Pong),
            _ => Err(format!("PROTOCOL_ERROR: 未知帧类型 {value}")),
        }
    }
}

/// 本地 RPC 完整帧：头部字段与消息体统一封装。
#[derive(Debug, Clone)]
pub struct LocalRpcFrame {
    pub frame_type: LocalRpcFrameType,
    pub flags: u32,
    pub request_id: [u8; 16],
    pub body: Vec<u8>,
}

/// 生成 16 字节 RequestId，作为 request/response 唯一关联键。
pub fn build_request_id(now_ms: u64, sequence: u64) -> [u8; 16] {
    let mut request_id = [0_u8; 16];
    // 前 8 字节写入毫秒时间戳，便于日志排查请求时序。
    request_id[..8].copy_from_slice(&now_ms.to_be_bytes());
    // 后 8 字节写入自增序列，避免同毫秒碰撞。
    request_id[8..].copy_from_slice(&sequence.to_be_bytes());
    request_id
}

/// 判断 RequestId 是否全零，用于执行协议强约束。
pub fn is_zero_request_id(request_id: &[u8; 16]) -> bool {
    request_id.iter().all(|item| *item == 0)
}
