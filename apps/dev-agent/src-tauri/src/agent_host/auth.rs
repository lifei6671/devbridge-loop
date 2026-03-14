use getrandom::getrandom;
use hmac::{Hmac, Mac};
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

/// 本地鉴权协议版本：用于 challenge-response 协商。
pub const AUTH_PROTOCOL_VERSION: &str = "lrpc-auth-v1";
/// HMAC-SHA256 十六进制摘要长度（32 字节 -> 64 个 hex 字符）。
pub const PROOF_HEX_LEN: usize = 64;

/// 生成随机字节并编码为小写十六进制字符串。
pub fn generate_random_hex(byte_len: usize) -> Result<String, String> {
    let mut random_buf = vec![0_u8; byte_len];
    // 使用操作系统随机源，避免使用可预测伪随机数。
    getrandom(&mut random_buf).map_err(|err| format!("生成随机数失败: {err}"))?;
    Ok(bytes_to_hex(&random_buf))
}

/// 计算 agent 侧 proof：HMAC(secret, client_nonce || agent_nonce || version || "agent")。
pub fn compute_agent_proof(
    session_secret: &str,
    client_nonce: &str,
    agent_nonce: &str,
    protocol_version: &str,
) -> Result<String, String> {
    compute_hmac_hex(
        session_secret,
        client_nonce,
        agent_nonce,
        protocol_version,
        "agent",
    )
}

/// 计算 host 侧 proof：HMAC(secret, client_nonce || agent_nonce || version || "host")。
pub fn compute_client_proof(
    session_secret: &str,
    client_nonce: &str,
    agent_nonce: &str,
    protocol_version: &str,
) -> Result<String, String> {
    compute_hmac_hex(
        session_secret,
        client_nonce,
        agent_nonce,
        protocol_version,
        "host",
    )
}

/// 常量时字符串比较：用于避免 proof 校验泄露时序信息。
pub fn constant_time_eq(left: &str, right: &str) -> bool {
    if left.len() != right.len() {
        return false;
    }
    let mut diff: u8 = 0;
    for (lhs, rhs) in left.as_bytes().iter().zip(right.as_bytes().iter()) {
        // 逐字节累积异或差值，避免在首个不匹配处提前返回。
        diff |= lhs ^ rhs;
    }
    diff == 0
}

/// 校验 hex token 格式，约束最小长度并拒绝非十六进制字符。
pub fn validate_hex_token(token: &str, min_len: usize, field_name: &str) -> Result<(), String> {
    if token.len() < min_len {
        return Err(format!("{field_name} 长度不足，至少需要 {min_len} 个字符"));
    }
    if token.len() % 2 != 0 {
        return Err(format!("{field_name} 必须是偶数长度的十六进制字符串"));
    }
    if !token.chars().all(|ch| ch.is_ascii_hexdigit()) {
        return Err(format!("{field_name} 不是合法十六进制字符串"));
    }
    Ok(())
}

/// 统一 HMAC 计算入口，避免两端拼接顺序不一致。
fn compute_hmac_hex(
    session_secret: &str,
    client_nonce: &str,
    agent_nonce: &str,
    protocol_version: &str,
    role: &str,
) -> Result<String, String> {
    if session_secret.trim().is_empty() {
        return Err("session_secret 不能为空".to_string());
    }
    let mut mac = HmacSha256::new_from_slice(session_secret.as_bytes())
        .map_err(|err| format!("初始化 HMAC 失败: {err}"))?;
    // 拼接顺序必须固定，保证 host/agent 计算结果可对齐。
    mac.update(client_nonce.as_bytes());
    mac.update(agent_nonce.as_bytes());
    mac.update(protocol_version.as_bytes());
    mac.update(role.as_bytes());
    Ok(bytes_to_hex(&mac.finalize().into_bytes()))
}

/// 将字节数组编码为小写十六进制字符串。
fn bytes_to_hex(bytes: &[u8]) -> String {
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        // 使用固定宽度两位编码，避免前导零丢失。
        let _ = std::fmt::Write::write_fmt(&mut output, format_args!("{byte:02x}"));
    }
    output
}

#[cfg(test)]
mod tests {
    use super::*;

    /// 验证 host/agent proof 角色隔离，不会得到同一摘要。
    #[test]
    fn proofs_are_role_scoped() {
        let session_secret = "00112233445566778899aabbccddeeff";
        let client_nonce = "aabbccddeeff0011";
        let agent_nonce = "1122334455667788";
        let agent_proof = compute_agent_proof(
            session_secret,
            client_nonce,
            agent_nonce,
            AUTH_PROTOCOL_VERSION,
        )
        .expect("计算 agent_proof 失败");
        let client_proof = compute_client_proof(
            session_secret,
            client_nonce,
            agent_nonce,
            AUTH_PROTOCOL_VERSION,
        )
        .expect("计算 client_proof 失败");
        assert_ne!(agent_proof, client_proof);
    }

    /// 验证 proof 固定长度为 64 个 hex 字符。
    #[test]
    fn proof_length_is_expected() {
        let proof = compute_agent_proof(
            "secret-test-value",
            "client-nonce",
            "agent-nonce",
            AUTH_PROTOCOL_VERSION,
        )
        .expect("计算 proof 失败");
        assert_eq!(proof.len(), PROOF_HEX_LEN);
    }

    /// 验证常量时比较在相同输入下返回 true。
    #[test]
    fn constant_time_compare_works() {
        assert!(constant_time_eq("abcd", "abcd"));
        assert!(!constant_time_eq("abcd", "abce"));
        assert!(!constant_time_eq("abcd", "abc"));
    }
}
