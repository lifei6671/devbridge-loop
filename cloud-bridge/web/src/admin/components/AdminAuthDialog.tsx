import type { FormEvent } from "react";

import type { AdminConsoleViewModel } from "../hooks/useAdminConsole";

type AdminAuthDialogProps = {
  vm: AdminConsoleViewModel;
};

export function AdminAuthDialog(props: AdminAuthDialogProps) {
  const {
    authError,
    authProviders,
    authStatus,
    isAuthenticating,
    login,
    loginPassword,
    loginUsername,
    selectedProvider,
    setLoginPassword,
    setLoginUsername,
    setSelectedProvider,
  } = props.vm;

  if (authStatus === "authenticated") {
    return null;
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void login(selectedProvider, loginUsername, loginPassword);
  };

  return (
    <div className="auth-overlay" role="dialog" aria-modal="true" aria-labelledby="admin-auth-title">
      <div className="auth-backdrop" />
      <form className="auth-dialog panel" onSubmit={handleSubmit}>
        <div className="auth-dialog-head">
          <p className="auth-kicker">Bridge Admin Login</p>
          <h2 id="admin-auth-title">登录管理界面</h2>
          <p className="auth-sub">
            管理面已切换为浏览器登录态。输入用户名和密码后，页面会建立受控会话并自动续用到 API 与 SSE。
          </p>
        </div>

        <div className="auth-field-grid">
          <label className="auth-field">
            <span>登录方式</span>
            <select
              value={selectedProvider}
              onChange={(event) => setSelectedProvider(event.target.value)}
              disabled={isAuthenticating || authProviders.length <= 1}
            >
              {authProviders.map((provider) => (
                <option key={provider.name} value={provider.name}>
                  {provider.label}
                </option>
              ))}
            </select>
          </label>

          <label className="auth-field">
            <span>用户名</span>
            <input
              autoFocus
              autoComplete="username"
              placeholder="例如：admin"
              value={loginUsername}
              onChange={(event) => setLoginUsername(event.target.value)}
            />
          </label>

          <label className="auth-field">
            <span>密码</span>
            <input
              type="password"
              autoComplete="current-password"
              placeholder="输入密码"
              value={loginPassword}
              onChange={(event) => setLoginPassword(event.target.value)}
            />
          </label>
        </div>

        {authError.trim() !== "" ? <p className="auth-error">{authError}</p> : null}

        <div className="auth-actions">
          <button type="submit" className="solid-btn auth-submit-btn" disabled={isAuthenticating}>
            {authStatus === "loading" || isAuthenticating ? "登录中..." : "登录"}
          </button>
        </div>
      </form>
    </div>
  );
}
