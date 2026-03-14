use serde_json::{json, Value};
use std::collections::VecDeque;
#[cfg(windows)]
use std::fs::OpenOptions;
use std::io::{Read, Write};
use std::thread;
use std::time::{Duration, Instant};

#[cfg(unix)]
use std::os::unix::net::UnixStream;

use super::auth::{
    compute_agent_proof, compute_client_proof, constant_time_eq, generate_random_hex,
    validate_hex_token, AUTH_PROTOCOL_VERSION, PROOF_HEX_LEN,
};
use super::codec::{parse_json_body, read_local_rpc_frame, write_local_rpc_frame};
use super::frame::{build_request_id, LocalRpcFrame, LocalRpcFrameType};
use super::router::validate_localrpc_method;
use crate::state::app_state::now_ms;

/// 本地 IPC 建连超时时间。
const LOCAL_RPC_CONNECT_TIMEOUT_MS: u64 = 6000;
/// 本地 IPC 默认请求超时时间。
pub const LOCAL_RPC_DEFAULT_TIMEOUT_MS: u64 = 1200;

/// 本地 RPC 底层流抽象：屏蔽 UDS 与 Named Pipe 的平台差异。
enum LocalRpcStream {
    #[cfg(unix)]
    Uds(UnixStream),
    #[cfg(windows)]
    NamedPipe(std::fs::File),
}

impl Read for LocalRpcStream {
    /// 统一读取入口：上层协议不关心具体传输实现。
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        match self {
            #[cfg(unix)]
            LocalRpcStream::Uds(stream) => stream.read(buf),
            #[cfg(windows)]
            LocalRpcStream::NamedPipe(file) => file.read(buf),
        }
    }
}

impl Write for LocalRpcStream {
    /// 统一写入入口：保证帧协议可复用同一套编解码。
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        match self {
            #[cfg(unix)]
            LocalRpcStream::Uds(stream) => stream.write(buf),
            #[cfg(windows)]
            LocalRpcStream::NamedPipe(file) => file.write(buf),
        }
    }

    /// 刷新写缓冲，确保帧及时落到 IPC 通道。
    fn flush(&mut self) -> std::io::Result<()> {
        match self {
            #[cfg(unix)]
            LocalRpcStream::Uds(stream) => stream.flush(),
            #[cfg(windows)]
            LocalRpcStream::NamedPipe(file) => file.flush(),
        }
    }
}

impl LocalRpcStream {
    /// 设置读超时：Unix 直接使用 socket 超时；Windows 同步 Named Pipe 暂以默认阻塞模式运行。
    #[cfg(unix)]
    fn set_read_timeout_millis(&self, timeout_ms: u64) -> Result<(), String> {
        match self {
            LocalRpcStream::Uds(stream) => stream
                .set_read_timeout(Some(Duration::from_millis(timeout_ms)))
                .map_err(|err| format!("设置 IPC 读超时失败: {err}")),
        }
    }

    /// 设置读超时：Unix 直接使用 socket 超时；Windows 同步 Named Pipe 暂以默认阻塞模式运行。
    #[cfg(windows)]
    fn set_read_timeout_millis(&self, timeout_ms: u64) -> Result<(), String> {
        let _ = timeout_ms;
        match self {
            LocalRpcStream::NamedPipe(_) => {
                // Windows 首版使用同步句柄，先保证协议收发可用，超时策略在后续阶段增强。
                Ok(())
            }
        }
    }

    /// 设置读超时：Unix 直接使用 socket 超时；Windows 同步 Named Pipe 暂以默认阻塞模式运行。
    #[cfg(not(any(unix, windows)))]
    fn set_read_timeout_millis(&self, timeout_ms: u64) -> Result<(), String> {
        let _ = timeout_ms;
        Err("当前构建目标暂未实现读取超时配置".to_string())
    }

    /// 关闭底层连接，供 stop/shutdown 流程回收资源。
    #[cfg(unix)]
    fn close(&mut self) {
        match self {
            LocalRpcStream::Uds(stream) => {
                let _ = stream.shutdown(std::net::Shutdown::Both);
            }
        }
    }

    /// 关闭底层连接，供 stop/shutdown 流程回收资源。
    #[cfg(windows)]
    fn close(&mut self) {
        match self {
            LocalRpcStream::NamedPipe(file) => {
                // 同步 Named Pipe 通过 flush + drop 释放句柄。
                let _ = file.flush();
            }
        }
    }

    /// 关闭底层连接，供 stop/shutdown 流程回收资源。
    #[cfg(not(any(unix, windows)))]
    fn close(&mut self) {
        let _ = self;
    }
}

/// 根据传输类型建立本地 IPC 流。
fn connect_local_rpc_stream(transport: &str, endpoint: &str) -> Result<LocalRpcStream, String> {
    let deadline = Instant::now() + Duration::from_millis(LOCAL_RPC_CONNECT_TIMEOUT_MS);
    loop {
        #[cfg(unix)]
        {
            if transport == "uds" {
                match UnixStream::connect(endpoint) {
                    Ok(stream) => return Ok(LocalRpcStream::Uds(stream)),
                    Err(err) => {
                        if Instant::now() >= deadline {
                            return Err(format!("连接本地 IPC 端点失败: {err}"));
                        }
                        // 等待服务端 listener 就绪后再重试连接。
                        thread::sleep(Duration::from_millis(80));
                        continue;
                    }
                }
            }
            if transport == "named_pipe" {
                return Err("当前平台不支持 named_pipe，请使用 uds".to_string());
            }
            return Err(format!("不支持的 IPC transport={transport}"));
        }

        #[cfg(windows)]
        {
            if transport == "named_pipe" {
                match OpenOptions::new().read(true).write(true).open(endpoint) {
                    Ok(file) => return Ok(LocalRpcStream::NamedPipe(file)),
                    Err(err) => {
                        if Instant::now() >= deadline {
                            if err.raw_os_error() == Some(2) {
                                return Err(format!(
                                    "连接本地 Named Pipe 失败: {err}。\
 这表示 UI 与 Agent 内核的 IPC 尚未建立（endpoint={endpoint}）；\
 可能是内核进程未成功启动，或当前 runtime 尚未实现 localrpc listener。"
                                ));
                            }
                            return Err(format!("连接本地 Named Pipe 失败: {err}"));
                        }
                        // 让出时间片，等待 Agent 侧完成 pipe 监听创建。
                        thread::sleep(Duration::from_millis(80));
                        continue;
                    }
                }
            }
            if transport == "uds" {
                return Err("当前平台不支持 uds，请使用 named_pipe".to_string());
            }
            return Err(format!("不支持的 IPC transport={transport}"));
        }

        #[cfg(not(any(unix, windows)))]
        {
            let _ = (transport, endpoint, deadline);
            return Err("当前构建目标暂未实现本地 IPC 客户端".to_string());
        }
    }
}

/// 本地 RPC 客户端：维护一条长期连接并处理多路消息。
pub struct LocalRpcClient {
    transport: String,
    endpoint: String,
    stream: LocalRpcStream,
    next_request_seq: u64,
    event_cache: VecDeque<Value>,
}

/// 统一格式化 RPC 错误，保留 code 以便上层做能力降级判断。
fn format_rpc_error(response_value: &Value) -> String {
    let error_code = response_value
        .get("error")
        .and_then(|err| err.get("code"))
        .and_then(Value::as_str)
        .unwrap_or("UNKNOWN_RPC_ERROR");
    let error_message = response_value
        .get("error")
        .and_then(|err| err.get("message"))
        .and_then(Value::as_str)
        .unwrap_or("unknown rpc error");
    format!("RPC 调用失败[{error_code}]: {error_message}")
}

impl LocalRpcClient {
    /// 建立本地 IPC 长连接：Linux 走 UDS，Windows 走 Named Pipe。
    pub fn connect(
        transport: &str,
        endpoint: &str,
        session_secret: &str,
        expected_peer_pid: Option<u32>,
    ) -> Result<Self, String> {
        validate_hex_token(session_secret, 64, "session_secret")?;
        let stream = connect_local_rpc_stream(transport, endpoint)?;
        // 读超时用于 request/ping 超时控制，避免无限阻塞。
        stream.set_read_timeout_millis(LOCAL_RPC_DEFAULT_TIMEOUT_MS)?;
        let mut client = Self {
            transport: transport.to_string(),
            endpoint: endpoint.to_string(),
            stream,
            next_request_seq: 1,
            event_cache: VecDeque::new(),
        };
        client.verify_os_peer_identity(expected_peer_pid)?;
        client.perform_auth_handshake(session_secret)?;
        Ok(client)
    }

    /// 发送 request 并等待对应 request_id 的 response。
    pub fn request(
        &mut self,
        method: &str,
        payload: Value,
        timeout_ms: u64,
    ) -> Result<Value, String> {
        validate_localrpc_method(method)?;
        let request_id = build_request_id(now_ms(), self.next_request_seq);
        self.next_request_seq = self.next_request_seq.saturating_add(1);
        let request_body = json!({
            "type": "request",
            "method": method,
            "timeout_ms": timeout_ms,
            "payload": payload,
        });
        let body_bytes = serde_json::to_vec(&request_body)
            .map_err(|err| format!("序列化 request 失败: {err}"))?;
        let request_frame = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Request,
            flags: 0,
            request_id,
            body: body_bytes,
        };

        self.stream.set_read_timeout_millis(timeout_ms.max(100))?;
        write_local_rpc_frame(&mut self.stream, &request_frame)?;
        loop {
            let frame = read_local_rpc_frame(&mut self.stream)?;
            match frame.frame_type {
                LocalRpcFrameType::Response => {
                    if frame.request_id != request_id {
                        return Err(
                            "PROTOCOL_ERROR: response request_id 与当前请求不匹配".to_string()
                        );
                    }
                    let value = parse_json_body(&frame.body)?;
                    if !value.get("ok").and_then(Value::as_bool).unwrap_or(false) {
                        return Err(format_rpc_error(&value));
                    }
                    return Ok(value.get("payload").cloned().unwrap_or(Value::Null));
                }
                LocalRpcFrameType::Event => {
                    let event_value = parse_json_body(&frame.body)?;
                    // 事件不打断 request 流程，先缓存在本地等待上层消费。
                    self.event_cache.push_back(event_value);
                }
                LocalRpcFrameType::Ping => {
                    self.send_pong()?;
                }
                LocalRpcFrameType::Pong => {}
                LocalRpcFrameType::Request => {
                    return Err("PROTOCOL_ERROR: client 收到非法 request 帧".to_string())
                }
            }
        }
    }

    /// 发送 ping 并等待 pong，用于连接健康探测。
    pub fn ping(&mut self) -> Result<(), String> {
        let ping_frame = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Ping,
            flags: 0,
            request_id: [0; 16],
            body: Vec::new(),
        };
        self.stream.set_read_timeout_millis(600)?;
        write_local_rpc_frame(&mut self.stream, &ping_frame)?;
        loop {
            let frame = read_local_rpc_frame(&mut self.stream)?;
            match frame.frame_type {
                LocalRpcFrameType::Pong => return Ok(()),
                LocalRpcFrameType::Event => {
                    let event_value = parse_json_body(&frame.body)?;
                    // ping 期间收到事件时同样缓存，不丢弃。
                    self.event_cache.push_back(event_value);
                }
                LocalRpcFrameType::Ping => {
                    self.send_pong()?;
                }
                LocalRpcFrameType::Response | LocalRpcFrameType::Request => {
                    return Err("PROTOCOL_ERROR: ping 流程收到非法帧类型".to_string())
                }
            }
        }
    }

    /// 读取并清空事件缓存，交给 event_bridge 做统一转发。
    pub fn drain_events(&mut self) -> Vec<Value> {
        self.event_cache.drain(..).collect::<Vec<_>>()
    }

    /// 关闭 IPC 客户端连接，供 stop/shutdown 流程回收。
    pub fn close(&mut self) {
        self.stream.close();
    }

    /// 给上层暴露连接信息，便于日志排查。
    pub fn describe(&self) -> String {
        format!("transport={}, endpoint={}", self.transport, self.endpoint)
    }

    /// 在收到 ping 帧时立刻返回 pong，保持协议存活语义。
    fn send_pong(&mut self) -> Result<(), String> {
        let pong_frame = LocalRpcFrame {
            frame_type: LocalRpcFrameType::Pong,
            flags: 0,
            request_id: [0; 16],
            body: Vec::new(),
        };
        write_local_rpc_frame(&mut self.stream, &pong_frame)?;
        Ok(())
    }

    /// 执行操作系统级对端身份校验，防止连接到非预期进程。
    fn verify_os_peer_identity(&self, expected_peer_pid: Option<u32>) -> Result<(), String> {
        self.verify_expected_peer_pid(expected_peer_pid)
    }

    /// Linux 平台：通过 SO_PEERCRED 校验对端 uid/pid。
    #[cfg(all(unix, target_os = "linux"))]
    fn verify_expected_peer_pid(&self, expected_peer_pid: Option<u32>) -> Result<(), String> {
        use std::os::fd::AsRawFd;

        #[repr(C)]
        struct UCred {
            pid: i32,
            uid: u32,
            gid: u32,
        }

        type SockLen = u32;

        unsafe extern "C" {
            fn getsockopt(
                socket: i32,
                level: i32,
                option_name: i32,
                option_value: *mut std::ffi::c_void,
                option_len: *mut SockLen,
            ) -> i32;
            fn geteuid() -> u32;
        }

        const SOL_SOCKET: i32 = 1;
        const SO_PEERCRED: i32 = 17;

        let LocalRpcStream::Uds(stream) = &self.stream;
        let raw_fd = stream.as_raw_fd();
        let mut peer_cred = UCred {
            pid: 0,
            uid: 0,
            gid: 0,
        };
        let mut peer_cred_len = std::mem::size_of::<UCred>() as SockLen;
        let result_code = unsafe {
            // 先做 OS 级别身份核验，再进入应用层 challenge-response。
            getsockopt(
                raw_fd,
                SOL_SOCKET,
                SO_PEERCRED,
                (&mut peer_cred as *mut UCred).cast::<std::ffi::c_void>(),
                &mut peer_cred_len,
            )
        };
        if result_code != 0 {
            return Err(format!(
                "PEER_IDENTITY_CHECK_FAILED: SO_PEERCRED 获取失败: {}",
                std::io::Error::last_os_error()
            ));
        }
        if peer_cred_len < std::mem::size_of::<UCred>() as SockLen {
            return Err("PEER_IDENTITY_CHECK_FAILED: SO_PEERCRED 返回长度异常".to_string());
        }

        let current_uid = unsafe { geteuid() };
        if peer_cred.uid != current_uid {
            return Err(format!(
                "PEER_IDENTITY_MISMATCH: uid 不匹配，expected_uid={current_uid}, actual_uid={}",
                peer_cred.uid
            ));
        }
        if peer_cred.pid <= 0 {
            return Err(format!(
                "PEER_IDENTITY_MISMATCH: peer pid 非法，actual_pid={}",
                peer_cred.pid
            ));
        }
        let actual_pid = peer_cred.pid as u32;
        if let Some(expected_peer_pid) = expected_peer_pid {
            if actual_pid != expected_peer_pid {
                return Err(format!(
                    "PEER_IDENTITY_MISMATCH: pid 不匹配，expected_pid={expected_peer_pid}, actual_pid={actual_pid}"
                ));
            }
        }
        Ok(())
    }

    /// Windows 平台：校验 Named Pipe 服务端 PID 与进程令牌 SID。
    #[cfg(windows)]
    fn verify_expected_peer_pid(&self, expected_peer_pid: Option<u32>) -> Result<(), String> {
        use std::os::windows::io::AsRawHandle;

        type Handle = *mut std::ffi::c_void;
        type Dword = u32;
        type Bool = i32;

        const TOKEN_QUERY: Dword = 0x0008;
        const PROCESS_QUERY_LIMITED_INFORMATION: Dword = 0x1000;
        const TOKEN_INFORMATION_CLASS_USER: Dword = 1;
        const ERROR_INSUFFICIENT_BUFFER: i32 = 122;

        #[repr(C)]
        struct SidAndAttributes {
            sid: *mut std::ffi::c_void,
            attributes: Dword,
        }

        #[repr(C)]
        struct TokenUser {
            user: SidAndAttributes,
        }

        unsafe extern "system" {
            fn GetNamedPipeServerProcessId(pipe: Handle, server_process_id: *mut Dword) -> Bool;
            fn OpenProcess(
                desired_access: Dword,
                inherit_handle: Bool,
                process_id: Dword,
            ) -> Handle;
            fn OpenProcessToken(
                process_handle: Handle,
                desired_access: Dword,
                token_handle: *mut Handle,
            ) -> Bool;
            fn GetTokenInformation(
                token_handle: Handle,
                token_information_class: Dword,
                token_information: *mut std::ffi::c_void,
                token_information_length: Dword,
                return_length: *mut Dword,
            ) -> Bool;
            fn EqualSid(
                first_sid: *mut std::ffi::c_void,
                second_sid: *mut std::ffi::c_void,
            ) -> Bool;
            fn CloseHandle(object: Handle) -> Bool;
        }

        fn close_handle_silent(handle: Handle) {
            if !handle.is_null() {
                unsafe {
                    let _ = CloseHandle(handle);
                }
            }
        }

        fn read_process_sid_buffer(process_id: Dword) -> Result<Vec<u8>, String> {
            let process_handle =
                unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, process_id) };
            if process_handle.is_null() {
                return Err(format!(
                    "PEER_IDENTITY_CHECK_FAILED: 打开进程失败，pid={process_id}, error={}",
                    std::io::Error::last_os_error()
                ));
            }

            let mut token_handle: Handle = std::ptr::null_mut();
            let open_token_ok =
                unsafe { OpenProcessToken(process_handle, TOKEN_QUERY, &mut token_handle) };
            if open_token_ok == 0 {
                close_handle_silent(process_handle);
                return Err(format!(
                    "PEER_IDENTITY_CHECK_FAILED: 打开进程令牌失败，pid={process_id}, error={}",
                    std::io::Error::last_os_error()
                ));
            }

            let mut needed_len: Dword = 0;
            let first_read_ok = unsafe {
                GetTokenInformation(
                    token_handle,
                    TOKEN_INFORMATION_CLASS_USER,
                    std::ptr::null_mut(),
                    0,
                    &mut needed_len,
                )
            };
            if first_read_ok != 0 {
                close_handle_silent(token_handle);
                close_handle_silent(process_handle);
                return Err("PEER_IDENTITY_CHECK_FAILED: 读取 TokenUser 长度返回异常".to_string());
            }
            let first_read_error = std::io::Error::last_os_error();
            if first_read_error.raw_os_error().unwrap_or_default() != ERROR_INSUFFICIENT_BUFFER {
                close_handle_silent(token_handle);
                close_handle_silent(process_handle);
                return Err(format!(
                    "PEER_IDENTITY_CHECK_FAILED: 读取 TokenUser 长度失败: {first_read_error}"
                ));
            }
            if needed_len == 0 {
                close_handle_silent(token_handle);
                close_handle_silent(process_handle);
                return Err("PEER_IDENTITY_CHECK_FAILED: TokenUser 长度为 0".to_string());
            }

            let mut token_user_buffer = vec![0_u8; needed_len as usize];
            let second_read_ok = unsafe {
                GetTokenInformation(
                    token_handle,
                    TOKEN_INFORMATION_CLASS_USER,
                    token_user_buffer.as_mut_ptr().cast::<std::ffi::c_void>(),
                    needed_len,
                    &mut needed_len,
                )
            };
            close_handle_silent(token_handle);
            close_handle_silent(process_handle);
            if second_read_ok == 0 {
                return Err(format!(
                    "PEER_IDENTITY_CHECK_FAILED: 读取 TokenUser 失败，pid={process_id}, error={}",
                    std::io::Error::last_os_error()
                ));
            }
            Ok(token_user_buffer)
        }

        fn token_user_sid_ptr(token_user_buffer: &[u8]) -> Result<*mut std::ffi::c_void, String> {
            if token_user_buffer.len() < std::mem::size_of::<TokenUser>() {
                return Err("PEER_IDENTITY_CHECK_FAILED: TokenUser 缓冲区长度不足".to_string());
            }
            let token_user = unsafe { &*(token_user_buffer.as_ptr().cast::<TokenUser>()) };
            if token_user.user.sid.is_null() {
                return Err("PEER_IDENTITY_CHECK_FAILED: TokenUser.Sid 为空".to_string());
            }
            Ok(token_user.user.sid)
        }

        let LocalRpcStream::NamedPipe(pipe_file) = &self.stream;
        let pipe_handle = pipe_file.as_raw_handle().cast::<std::ffi::c_void>();
        let mut actual_peer_pid: Dword = 0;
        let get_pid_ok = unsafe { GetNamedPipeServerProcessId(pipe_handle, &mut actual_peer_pid) };
        if get_pid_ok == 0 {
            return Err(format!(
                "PEER_IDENTITY_CHECK_FAILED: 获取 Named Pipe 服务端 PID 失败: {}",
                std::io::Error::last_os_error()
            ));
        }
        if let Some(expected_peer_pid) = expected_peer_pid {
            if actual_peer_pid != expected_peer_pid {
                return Err(format!(
                    "PEER_IDENTITY_MISMATCH: pid 不匹配，expected_pid={expected_peer_pid}, actual_pid={actual_peer_pid}"
                ));
            }
        }

        let current_sid_buffer = read_process_sid_buffer(std::process::id())?;
        let peer_sid_buffer = read_process_sid_buffer(actual_peer_pid)?;
        let current_sid = token_user_sid_ptr(&current_sid_buffer)?;
        let peer_sid = token_user_sid_ptr(&peer_sid_buffer)?;
        let sid_equal = unsafe { EqualSid(current_sid, peer_sid) };
        if sid_equal == 0 {
            return Err("PEER_IDENTITY_MISMATCH: 服务端 SID 与当前用户 SID 不一致".to_string());
        }
        Ok(())
    }

    /// 其他平台先跳过对端身份校验（当前方案仅要求 Linux/Windows）。
    #[cfg(all(unix, not(target_os = "linux")))]
    fn verify_expected_peer_pid(&self, expected_peer_pid: Option<u32>) -> Result<(), String> {
        let _ = expected_peer_pid;
        Ok(())
    }

    /// 其他平台先跳过对端身份校验（当前方案仅要求 Linux/Windows）。
    #[cfg(not(any(unix, windows)))]
    fn verify_expected_peer_pid(&self, expected_peer_pid: Option<u32>) -> Result<(), String> {
        let _ = expected_peer_pid;
        Ok(())
    }

    /// 执行双向 challenge-response 握手，防止伪造进程冒充 Agent。
    fn perform_auth_handshake(&mut self, session_secret: &str) -> Result<(), String> {
        let client_nonce = generate_random_hex(16)?;
        let begin_payload = json!({
            "client_name": "tauri-host",
            "protocol_version": AUTH_PROTOCOL_VERSION,
            "client_nonce": client_nonce,
        });
        let begin_result = self.request(
            "app.auth.begin",
            begin_payload,
            LOCAL_RPC_DEFAULT_TIMEOUT_MS,
        )?;
        let protocol_version = begin_result
            .get("protocol_version")
            .and_then(Value::as_str)
            .unwrap_or(AUTH_PROTOCOL_VERSION);
        let agent_nonce = begin_result
            .get("agent_nonce")
            .and_then(Value::as_str)
            .ok_or_else(|| "握手失败：缺少 agent_nonce".to_string())?;
        let agent_proof = begin_result
            .get("agent_proof")
            .and_then(Value::as_str)
            .ok_or_else(|| "握手失败：缺少 agent_proof".to_string())?;
        validate_hex_token(agent_nonce, 16, "agent_nonce")?;
        validate_hex_token(agent_proof, PROOF_HEX_LEN, "agent_proof")?;
        let expected_agent_proof =
            compute_agent_proof(session_secret, &client_nonce, agent_nonce, protocol_version)?;
        if !constant_time_eq(agent_proof, &expected_agent_proof) {
            return Err("握手失败：agent_proof 校验不通过".to_string());
        }

        let client_proof =
            compute_client_proof(session_secret, &client_nonce, agent_nonce, protocol_version)?;
        let complete_payload = json!({
            "protocol_version": protocol_version,
            "client_nonce": client_nonce,
            "agent_nonce": agent_nonce,
            "client_proof": client_proof,
        });
        let complete_result = self.request(
            "app.auth.complete",
            complete_payload,
            LOCAL_RPC_DEFAULT_TIMEOUT_MS,
        )?;
        let authenticated = complete_result
            .get("authenticated")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        if !authenticated {
            return Err("握手失败：agent 未返回 authenticated=true".to_string());
        }
        // 完成双向 proof 后才允许后续业务 RPC。
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::format_rpc_error;
    use serde_json::json;

    #[test]
    fn format_rpc_error_keeps_error_code() {
        let response_value = json!({
            "ok": false,
            "error": {
                "code": "METHOD_NOT_ALLOWED",
                "message": "method service.list is not available",
            }
        });
        let formatted = format_rpc_error(&response_value);
        assert!(formatted.contains("METHOD_NOT_ALLOWED"));
        assert!(formatted.contains("service.list"));
    }

    #[test]
    fn format_rpc_error_uses_fallback_code_when_missing() {
        let response_value = json!({
            "ok": false,
            "error": {
                "message": "unknown failure",
            }
        });
        let formatted = format_rpc_error(&response_value);
        assert!(formatted.contains("UNKNOWN_RPC_ERROR"));
        assert!(formatted.contains("unknown failure"));
    }
}
