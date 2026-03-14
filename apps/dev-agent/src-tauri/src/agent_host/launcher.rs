use fs2::FileExt;
use std::fs::{self, OpenOptions};
use std::io::Write;
#[cfg(unix)]
use std::os::unix::fs::FileTypeExt;
#[cfg(unix)]
use std::os::unix::fs::MetadataExt;
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Arc;

use super::auth::{generate_random_hex, validate_hex_token};
use crate::state::app_state::{now_ms, AppRuntimeState, SingleInstanceGuard};

/// 构造用户作用域字符串：优先使用用户标识，失败时回退 default。
fn fallback_user_scope() -> String {
    std::env::var("USER")
        .or_else(|_| std::env::var("USERNAME"))
        .unwrap_or_else(|_| "default".to_string())
        .replace(['\\', '/', ' '], "_")
}

/// Windows 上优先使用 SID 作为作用域，避免仅凭用户名造成碰撞。
#[cfg(windows)]
fn resolve_windows_user_scope() -> String {
    fn current_user_sid_string() -> Result<String, String> {
        type Handle = *mut std::ffi::c_void;
        type Dword = u32;
        type Bool = i32;

        const TOKEN_QUERY: Dword = 0x0008;
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
            fn GetCurrentProcess() -> Handle;
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
            fn ConvertSidToStringSidW(
                sid: *mut std::ffi::c_void,
                string_sid: *mut *mut u16,
            ) -> Bool;
            fn LocalFree(handle: *mut std::ffi::c_void) -> *mut std::ffi::c_void;
            fn CloseHandle(handle: Handle) -> Bool;
        }

        fn close_handle_silent(handle: Handle) {
            if !handle.is_null() {
                unsafe {
                    let _ = CloseHandle(handle);
                }
            }
        }

        fn utf16_ptr_to_string(wide_ptr: *const u16) -> String {
            let mut text_len = 0_usize;
            unsafe {
                while *wide_ptr.add(text_len) != 0 {
                    text_len += 1;
                }
                String::from_utf16_lossy(std::slice::from_raw_parts(wide_ptr, text_len))
            }
        }

        let mut token_handle: Handle = std::ptr::null_mut();
        let open_token_ok =
            unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token_handle) };
        if open_token_ok == 0 {
            return Err(format!(
                "读取当前用户令牌失败: {}",
                std::io::Error::last_os_error()
            ));
        }

        let mut required_len: Dword = 0;
        let first_read_ok = unsafe {
            GetTokenInformation(
                token_handle,
                TOKEN_INFORMATION_CLASS_USER,
                std::ptr::null_mut(),
                0,
                &mut required_len,
            )
        };
        if first_read_ok != 0 {
            close_handle_silent(token_handle);
            return Err("读取当前用户 SID 长度返回异常".to_string());
        }
        let first_read_error = std::io::Error::last_os_error();
        if first_read_error.raw_os_error().unwrap_or_default() != ERROR_INSUFFICIENT_BUFFER {
            close_handle_silent(token_handle);
            return Err(format!("读取当前用户 SID 长度失败: {first_read_error}"));
        }

        let mut token_user_buffer = vec![0_u8; required_len as usize];
        let second_read_ok = unsafe {
            GetTokenInformation(
                token_handle,
                TOKEN_INFORMATION_CLASS_USER,
                token_user_buffer.as_mut_ptr().cast::<std::ffi::c_void>(),
                required_len,
                &mut required_len,
            )
        };
        if second_read_ok == 0 {
            close_handle_silent(token_handle);
            return Err(format!(
                "读取当前用户 SID 失败: {}",
                std::io::Error::last_os_error()
            ));
        }

        let token_user = unsafe { &*(token_user_buffer.as_ptr().cast::<TokenUser>()) };
        if token_user.user.sid.is_null() {
            close_handle_silent(token_handle);
            return Err("当前用户 SID 为空".to_string());
        }
        let mut sid_wide_ptr: *mut u16 = std::ptr::null_mut();
        let convert_ok = unsafe { ConvertSidToStringSidW(token_user.user.sid, &mut sid_wide_ptr) };
        if convert_ok == 0 || sid_wide_ptr.is_null() {
            close_handle_silent(token_handle);
            return Err(format!(
                "SID 转字符串失败: {}",
                std::io::Error::last_os_error()
            ));
        }

        let sid_text = utf16_ptr_to_string(sid_wide_ptr);
        unsafe {
            let _ = LocalFree(sid_wide_ptr.cast::<std::ffi::c_void>());
        }
        close_handle_silent(token_handle);
        Ok(sid_text)
    }

    current_user_sid_string()
        .map(|sid| sid.replace(['\\', '/', ' '], "_"))
        .unwrap_or_else(|_| fallback_user_scope())
}

/// 非 Windows 平台不需要 SID，复用通用作用域字符串。
#[cfg(not(windows))]
fn resolve_windows_user_scope() -> String {
    fallback_user_scope()
}

/// 根据传输类型生成默认 IPC 端点地址。
pub fn default_ipc_endpoint(transport: &str) -> String {
    if transport == "named_pipe" {
        let user_scope = resolve_windows_user_scope();
        return format!(r"\\.\pipe\agent-ui-{user_scope}");
    }
    let runtime_dir = resolve_runtime_dir();
    runtime_dir
        .join("agent-ui")
        .join("agent.sock")
        .to_string_lossy()
        .to_string()
}

/// 解析运行目录：优先 XDG_RUNTIME_DIR，其次用户私有目录。
pub fn resolve_runtime_dir() -> PathBuf {
    if let Ok(xdg_runtime_dir) = std::env::var("XDG_RUNTIME_DIR") {
        if !xdg_runtime_dir.trim().is_empty() {
            return PathBuf::from(xdg_runtime_dir);
        }
    }
    #[cfg(windows)]
    {
        if let Ok(user_profile) = std::env::var("USERPROFILE") {
            if !user_profile.trim().is_empty() {
                return PathBuf::from(user_profile).join(".dev-agent").join("run");
            }
        }
    }
    if let Ok(home) = std::env::var("HOME") {
        if !home.trim().is_empty() {
            return PathBuf::from(home).join(".dev-agent").join("run");
        }
    }
    // 避免退回 /tmp 等共享目录：兜底到当前目录下的私有子目录。
    std::env::current_dir()
        .unwrap_or_else(|_| PathBuf::from("."))
        .join(".dev-agent")
        .join("run")
}

/// 确保目录存在且权限收敛（Unix 下为 0700）。
pub fn ensure_secure_dir(path: &Path) -> Result<(), String> {
    fs::create_dir_all(path).map_err(|err| format!("创建运行目录失败: {err}"))?;
    #[cfg(unix)]
    {
        if let Ok(meta) = fs::symlink_metadata(path) {
            if meta.file_type().is_symlink() {
                return Err("运行目录不能是符号链接".to_string());
            }
        }
        // 按文档要求将目录权限收敛到 owner-only。
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))
            .map_err(|err| format!("设置运行目录权限失败: {err}"))?;
        unsafe extern "C" {
            fn geteuid() -> u32;
        }
        let metadata =
            fs::metadata(path).map_err(|err| format!("读取运行目录元数据失败: {err}"))?;
        let actual_mode = metadata.permissions().mode() & 0o777;
        if actual_mode != 0o700 {
            return Err(format!("运行目录权限必须为 0700，当前为 {:o}", actual_mode));
        }
        let current_uid = unsafe { geteuid() };
        if metadata.uid() != current_uid {
            return Err(format!(
                "运行目录 owner 必须是当前用户，expected_uid={current_uid}, actual_uid={}",
                metadata.uid()
            ));
        }
    }
    Ok(())
}

/// 准备 IPC 端点：执行目录权限、反 symlink 与 stale socket 清理。
pub fn prepare_ipc_endpoint(transport: &str, endpoint: &str) -> Result<(), String> {
    if transport == "named_pipe" {
        validate_named_pipe_endpoint(endpoint)?;
        // Named Pipe 端点由系统 API 管理，不需要预建路径。
        return Ok(());
    }
    if transport != "uds" {
        return Err(format!("不支持的 IPC transport={transport}"));
    }
    let endpoint_path = PathBuf::from(endpoint);
    let parent_dir = endpoint_path
        .parent()
        .ok_or_else(|| "UDS 端点路径缺少父目录".to_string())?;
    ensure_secure_dir(parent_dir)?;

    #[cfg(unix)]
    {
        if let Ok(meta) = fs::symlink_metadata(&endpoint_path) {
            if meta.file_type().is_symlink() {
                return Err("UDS 端点不能是符号链接".to_string());
            }
            if meta.file_type().is_socket() {
                // 历史崩溃遗留 socket 文件可安全清理。
                fs::remove_file(&endpoint_path)
                    .map_err(|err| format!("清理陈旧 UDS 端点失败: {err}"))?;
            } else {
                return Err("UDS 端点已存在且不是 socket 文件".to_string());
            }
        }
    }
    Ok(())
}

/// 校验 Named Pipe 端点格式，避免误用普通路径导致越权或不可用行为。
fn validate_named_pipe_endpoint(endpoint: &str) -> Result<(), String> {
    let normalized = endpoint.trim();
    if normalized.is_empty() {
        return Err("Named Pipe 端点不能为空".to_string());
    }
    if !normalized.starts_with(r"\\.\pipe\") {
        return Err(format!(
            "Named Pipe 端点格式非法，应以 \\\\.\\pipe\\ 开头: {normalized}"
        ));
    }
    if normalized.contains('/') {
        // 明确拒绝 Unix 风格路径分隔符，减少跨平台配置误填。
        return Err("Named Pipe 端点不能包含 '/'，请使用 Windows 路径格式".to_string());
    }
    let pipe_name = normalized.trim_start_matches(r"\\.\pipe\");
    if pipe_name.is_empty() {
        return Err("Named Pipe 名称不能为空".to_string());
    }
    if pipe_name.contains('\\') {
        return Err("Named Pipe 名称不能包含额外 '\\' 分隔层级".to_string());
    }
    if !pipe_name.starts_with("agent-ui-") {
        return Err("Named Pipe 名称必须以 agent-ui- 开头".to_string());
    }
    Ok(())
}

/// 生成本次 Agent 进程专属 `session_secret`（至少 32 字节随机熵）。
pub fn generate_session_secret() -> Result<String, String> {
    // 按方案要求使用高熵 secret，避免可预测字符串被重放伪造。
    generate_random_hex(32)
}

/// 根据配置创建子进程命令并拉起 Agent runtime。
pub fn spawn_agent_process(
    config: &crate::state::app_state::HostRuntimeConfig,
    session_secret: &str,
) -> Result<Child, String> {
    validate_hex_token(session_secret, 64, "session_secret")?;
    let mut command = Command::new(&config.runtime_program);
    command.args(&config.runtime_args);
    // 注入会话密钥到子进程环境变量，供本地 IPC challenge-response 使用。
    command.env("DEV_AGENT_SESSION_SECRET", session_secret);
    // 保留启动时间戳字段，便于日志关联本次拉起链路。
    command.env("DEV_AGENT_BOOTSTRAP_AT_MS", now_ms().to_string());
    // 注入 agent-core 核心配置，确保真实 Go runtime 直接读取并生效。
    command.env("DEV_AGENT_CFG_AGENT_ID", &config.agent_id);
    command.env("DEV_AGENT_CFG_BRIDGE_ADDR", &config.bridge_addr);
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_MIN_IDLE",
        config.tunnel_pool_min_idle.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_MAX_IDLE",
        config.tunnel_pool_max_idle.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_MAX_INFLIGHT",
        config.tunnel_pool_max_inflight.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_TTL_MS",
        config.tunnel_pool_ttl_ms.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_RATE",
        config.tunnel_pool_open_rate.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_BURST",
        config.tunnel_pool_open_burst.to_string(),
    );
    command.env(
        "DEV_AGENT_CFG_TUNNEL_POOL_RECONCILE_GAP_MS",
        config.tunnel_pool_reconcile_gap_ms.to_string(),
    );
    command.env("DEV_AGENT_IPC_TRANSPORT", &config.ipc_transport);
    command.env("DEV_AGENT_IPC_ENDPOINT", &config.ipc_endpoint);
    command.stdin(Stdio::null());
    command.stdout(Stdio::null());
    command.stderr(Stdio::null());
    command
        .spawn()
        .map_err(|err| format!("启动 Agent 进程失败: {err}"))
}

/// 创建并持有单实例锁，避免宿主多开。
fn acquire_single_instance_guard() -> Result<SingleInstanceGuard, String> {
    let runtime_dir = resolve_runtime_dir();
    ensure_secure_dir(&runtime_dir)?;
    let lock_path = runtime_dir.join("agent-host.lock");
    let mut lock_file = OpenOptions::new()
        .create(true)
        .read(true)
        .write(true)
        .open(&lock_path)
        .map_err(|err| format!("打开单实例锁文件失败: {err}"))?;
    // 使用操作系统文件锁约束单实例，避免仅靠“锁文件存在”导致崩溃后残留问题。
    lock_file
        .try_lock_exclusive()
        .map_err(|err| format!("获取单实例锁失败，可能已有实例在运行: {err}"))?;
    // 每次持锁后重写内容，便于排障时确认当前持有者 PID。
    lock_file
        .set_len(0)
        .map_err(|err| format!("重置单实例锁文件失败: {err}"))?;
    lock_file
        .write_all(format!("pid={}\n", std::process::id()).as_bytes())
        .map_err(|err| format!("写入单实例锁文件失败: {err}"))?;
    #[cfg(unix)]
    {
        // 锁文件权限进一步收敛到 0600。
        fs::set_permissions(&lock_path, fs::Permissions::from_mode(0o600))
            .map_err(|err| format!("设置锁文件权限失败: {err}"))?;
    }
    Ok(SingleInstanceGuard {
        _file: lock_file,
        path: lock_path,
    })
}

/// 保证单实例锁已就绪，供 bootstrap/setup 复用。
pub fn ensure_single_instance_guard(state: &Arc<AppRuntimeState>) -> Result<(), String> {
    let mut guard = state
        .single_instance
        .lock()
        .map_err(|_| "初始化单实例锁失败：single_instance 锁异常".to_string())?;
    if guard.is_none() {
        // 仅在首次时创建锁；后续复用已有句柄。
        *guard = Some(acquire_single_instance_guard()?);
    }
    Ok(())
}
