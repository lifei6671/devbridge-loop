/// 本地 RPC 允许的一级方法域白名单。
pub const ALLOWED_METHOD_DOMAINS: [&str; 8] = [
    "app", "agent", "session", "service", "tunnel", "traffic", "config", "diagnose",
];

/// 本地 RPC 强制禁止的越界方法集合。
pub const DENIED_LOW_LEVEL_METHODS: [&str; 6] = [
    "traffic.open",
    "traffic.reset",
    "tunnel.read",
    "tunnel.write",
    "relay.inject",
    "runtime.takeover",
];

/// 校验 method 是否属于允许域且不触达越界接口。
pub fn validate_localrpc_method(method: &str) -> Result<(), String> {
    if DENIED_LOW_LEVEL_METHODS.contains(&method) {
        return Err(format!("越界调用被拒绝: {method}"));
    }
    let domain = method.split('.').next().unwrap_or_default();
    if !ALLOWED_METHOD_DOMAINS.contains(&domain) {
        return Err(format!("method domain 不在白名单内: {method}"));
    }
    Ok(())
}
