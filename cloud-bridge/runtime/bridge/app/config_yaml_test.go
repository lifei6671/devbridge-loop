package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptls "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/tls"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestParseConfigYAMLAppliesDefaultsAndOverrides 验证 YAML 仅覆盖显式字段，其余字段保持默认值。
func TestParseConfigYAMLAppliesDefaultsAndOverrides(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
admin:
  enabled: true
  ui_enabled: true
  listen_addr: ":49081"
  auth_providers:
    - name: local-password
      type: password
      enabled: true
      password:
        accounts:
          - username: viewer
            password: viewer-pass
            role: viewer
          - username: operator
            password: operator-pass
            role: operator
          - username: admin
            password: admin-pass
            role: admin
control_plane:
  listen_addr: ":49080"
  grpc_h2_listen_addr: ":49082"
  heartbeat_timeout: 45s
  tls_mode: plaintext
observability:
  log_level: debug
ingress:
  base_domain: dev.example.internal
default_scope:
  namespace: default
  environment: shared
fallback_policies:
  - policy_id: fallback-dev
    namespace: dev
    enabled: true
    chain:
      - target_scope:
          namespace: dev
          environment: base
    external:
      enabled: true
`
	config, err := ParseConfigYAML([]byte(configYAML))
	if err != nil {
		testingObject.Fatalf("parse config yaml failed: %v", err)
	}
	if !config.Admin.Enabled {
		testingObject.Fatalf("expected admin enabled from yaml")
	}
	if !config.Admin.UIEnabled {
		testingObject.Fatalf("expected admin ui enabled from yaml")
	}
	if config.Admin.ListenAddr != ":49081" {
		testingObject.Fatalf("unexpected admin listen addr: got=%s want=%s", config.Admin.ListenAddr, ":49081")
	}
	if config.ControlPlane.HeartbeatTimeout != 45*time.Second {
		testingObject.Fatalf("unexpected heartbeat timeout: got=%s want=%s", config.ControlPlane.HeartbeatTimeout, 45*time.Second)
	}
	if config.ControlPlane.TLSMode != string(apptls.ModePlaintext) {
		testingObject.Fatalf("unexpected tls mode: got=%s want=%s", config.ControlPlane.TLSMode, apptls.ModePlaintext)
	}
	if config.ControlPlane.TLSCertSource != string(apptls.CertSourceExternal) {
		testingObject.Fatalf(
			"unexpected tls cert source default: got=%s want=%s",
			config.ControlPlane.TLSCertSource,
			apptls.CertSourceExternal,
		)
	}
	// 未在 YAML 中配置的字段应继续沿用默认值。
	if config.Ingress.HTTPAddr != ":38080" {
		testingObject.Fatalf("unexpected ingress http addr default: got=%s want=%s", config.Ingress.HTTPAddr, ":38080")
	}
	if config.Ingress.GRPCAddr != ":38081" {
		testingObject.Fatalf("unexpected ingress grpc addr default: got=%s want=%s", config.Ingress.GRPCAddr, ":38081")
	}
	if config.Ingress.BaseDomain != "dev.example.internal" {
		testingObject.Fatalf("unexpected ingress base_domain: got=%s want=%s", config.Ingress.BaseDomain, "dev.example.internal")
	}
	if len(config.Admin.AuthProviders) != 1 {
		testingObject.Fatalf("unexpected admin auth provider count: got=%d want=%d", len(config.Admin.AuthProviders), 1)
	}
	if len(config.Admin.AuthProviders[0].Password.Accounts) != 3 {
		testingObject.Fatalf(
			"unexpected admin account count: got=%d want=%d",
			len(config.Admin.AuthProviders[0].Password.Accounts),
			3,
		)
	}
	if config.DefaultScope.Namespace != "default" || config.DefaultScope.Environment != "shared" {
		testingObject.Fatalf("unexpected default scope: %+v", config.DefaultScope)
	}
	if len(config.FallbackPolicies) != 1 {
		testingObject.Fatalf("unexpected fallback policy count: got=%d want=1", len(config.FallbackPolicies))
	}
	if config.FallbackPolicies[0].Namespace != "dev" || !config.FallbackPolicies[0].External.Enabled {
		testingObject.Fatalf("unexpected fallback policy: %+v", config.FallbackPolicies[0])
	}
}

// TestParseConfigYAMLMigratesLegacyAdminAuthFields 验证旧版 admin 鉴权字段会自动迁移到新结构。
func TestParseConfigYAMLMigratesLegacyAdminAuthFields(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
admin:
  enabled: true
  listen_addr: ":49081"
  auth_mode: cookie
  cookie_token_name: bridge_admin_token
  auth_tokens:
    - name: viewer
      token: viewer-token
      role: viewer
    - name: admin
      token: admin-token
      role: admin
control_plane:
  listen_addr: ":49080"
  grpc_h2_listen_addr: ":49082"
`
	config, err := ParseConfigYAML([]byte(configYAML))
	if err != nil {
		testingObject.Fatalf("parse legacy config yaml failed: %v", err)
	}
	if config.Admin.SessionCookieName != "bridge_admin_token" {
		testingObject.Fatalf(
			"unexpected migrated session cookie name: got=%s want=%s",
			config.Admin.SessionCookieName,
			"bridge_admin_token",
		)
	}
	if len(config.Admin.AuthProviders) != 1 {
		testingObject.Fatalf("unexpected migrated auth provider count: got=%d want=1", len(config.Admin.AuthProviders))
	}
	if config.Admin.AuthProviders[0].Name != "legacy-token-compat" {
		testingObject.Fatalf(
			"unexpected migrated provider name: got=%s want=%s",
			config.Admin.AuthProviders[0].Name,
			"legacy-token-compat",
		)
	}
	if len(config.Admin.AuthProviders[0].Password.Accounts) != 2 {
		testingObject.Fatalf(
			"unexpected migrated account count: got=%d want=2",
			len(config.Admin.AuthProviders[0].Password.Accounts),
		)
	}
	if config.Admin.AuthProviders[0].Password.Accounts[0].Username != "viewer" {
		testingObject.Fatalf(
			"unexpected migrated viewer username: got=%s want=%s",
			config.Admin.AuthProviders[0].Password.Accounts[0].Username,
			"viewer",
		)
	}
	if config.Admin.AuthProviders[0].Password.Accounts[0].Password != "viewer-token" {
		testingObject.Fatalf(
			"unexpected migrated viewer password: got=%s want=%s",
			config.Admin.AuthProviders[0].Password.Accounts[0].Password,
			"viewer-token",
		)
	}
	if config.Admin.LegacyAuthMode != "" || len(config.Admin.LegacyAuthTokens) != 0 || config.Admin.LegacyCookieTokenName != "" {
		testingObject.Fatalf("expected legacy admin auth fields cleared after migration: %+v", config.Admin)
	}
}

// TestParseConfigYAMLRejectsUnknownFields 验证未知配置字段会被严格校验拒绝。
func TestParseConfigYAMLRejectsUnknownFields(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
unknown_root: true
`
	_, err := ParseConfigYAML([]byte(configYAML))
	if err == nil {
		testingObject.Fatalf("expected parse yaml config to fail for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown_root") {
		testingObject.Fatalf("unexpected parse error: %v", err)
	}
}

// TestParseConfigYAMLRejectsInvalidFallbackPolicies 验证 scope 降级配置会执行去重与空 scope 校验。
func TestParseConfigYAMLRejectsInvalidFallbackPolicies(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
default_scope:
  namespace: default
  environment: base
fallback_policies:
  - policy_id: fallback-dev
    namespace: dev
    enabled: true
    chain:
      - target_scope:
          namespace: dev
          environment: base
      - target_scope:
          namespace: dev
          environment: base
`
	_, err := ParseConfigYAML([]byte(configYAML))
	if err == nil {
		testingObject.Fatalf("expected invalid fallback policies to fail")
	}
	if !strings.Contains(err.Error(), "duplicated target_scope") {
		testingObject.Fatalf("unexpected fallback policy error: %v", err)
	}
}

// TestLoadConfigFromYAMLFile 验证可从指定 YAML 文件路径加载配置。
func TestLoadConfigFromYAMLFile(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	configFilePath := filepath.Join(tempDir, "bridge.yaml")
	configYAML := `
admin:
  enabled: false
control_plane:
  listen_addr: ":59080"
  grpc_h2_listen_addr: ":59082"
`
	if err := os.WriteFile(configFilePath, []byte(configYAML), 0o600); err != nil {
		testingObject.Fatalf("write temp yaml file failed: %v", err)
	}
	config, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load yaml config file failed: %v", err)
	}
	if config.ControlPlane.ListenAddr != ":59080" {
		testingObject.Fatalf("unexpected control plane listen addr: got=%s want=%s", config.ControlPlane.ListenAddr, ":59080")
	}
	if config.Admin.Enabled {
		testingObject.Fatalf("expected admin disabled from yaml")
	}
	if config.RuntimeConfigFilePath != configFilePath {
		testingObject.Fatalf("unexpected runtime config file path: got=%s want=%s", config.RuntimeConfigFilePath, configFilePath)
	}
}

// TestLoadConfigFromYAMLFileRejectsEmptyPath 验证空路径会被配置加载入口拒绝。
func TestLoadConfigFromYAMLFileRejectsEmptyPath(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := LoadConfigFromYAMLFile("   ")
	if err == nil {
		testingObject.Fatalf("expected empty yaml path to fail")
	}
}

// TestSaveConfigToYAMLFileRoundTrip 验证配置可写回 YAML 并可再次加载。
func TestSaveConfigToYAMLFileRoundTrip(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	configFilePath := filepath.Join(tempDir, "bridge.yaml")
	config := DefaultConfig()
	config.Ingress.HTTPAddr = ":18080"
	config.Ingress.GRPCAddr = ":18081"
	config.Ingress.TCPPortRange = "9100-9200"
	config.ControlPlane.ListenAddr = ":19080"
	config.ControlPlane.GRPCH2ListenAddr = ":19082"
	config.ControlPlane.HeartbeatTimeout = 45 * time.Second
	config.ControlPlane.TLSMode = string(apptls.ModeRequired)
	config.ControlPlane.TLSCertSource = string(apptls.CertSourceManagedCA)
	config.ControlPlane.TLSCACertFile = "/tmp/managed-root-ca.crt"
	config.ControlPlane.TLSCAKeyFile = "/tmp/managed-root-ca.key"
	config.ControlPlane.TLSServerCommonName = "bridge.internal.example"
	config.ControlPlane.TLSServerSANDNS = []string{"bridge.internal.example"}
	config.ControlPlane.TLSServerSANIPs = []string{"127.0.0.1"}
	config.ControlPlane.TLSServerCertTTL = 72 * time.Hour
	config.ControlPlane.TLSServerCertRenewBefore = 12 * time.Hour
	config.Admin.BasePath = "/console"
	config.Observability.LogLevel = "debug"
	config.DefaultScope.Namespace = "tenant"
	config.DefaultScope.Environment = "shared"
	config.FallbackPolicies = []pb.ScopeFallbackPolicy{
		{
			PolicyID:  "fallback-tenant",
			Namespace: "tenant",
			Enabled:   true,
			Chain: []pb.FallbackStep{
				{
					TargetScope: pb.Scope{Namespace: "tenant", Environment: "base"},
				},
			},
			External: pb.ExternalFallbackConfig{Enabled: true},
		},
	}

	if err := SaveConfigToYAMLFile(config, configFilePath); err != nil {
		testingObject.Fatalf("save config yaml failed: %v", err)
	}
	loadedConfig, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config yaml failed: %v", err)
	}
	if loadedConfig.Ingress.HTTPAddr != config.Ingress.HTTPAddr {
		testingObject.Fatalf("unexpected ingress.http_addr: got=%s want=%s", loadedConfig.Ingress.HTTPAddr, config.Ingress.HTTPAddr)
	}
	if loadedConfig.Ingress.BaseDomain != config.Ingress.BaseDomain {
		testingObject.Fatalf("unexpected ingress.base_domain: got=%s want=%s", loadedConfig.Ingress.BaseDomain, config.Ingress.BaseDomain)
	}
	if loadedConfig.ControlPlane.HeartbeatTimeout != config.ControlPlane.HeartbeatTimeout {
		testingObject.Fatalf(
			"unexpected control_plane.heartbeat_timeout: got=%s want=%s",
			loadedConfig.ControlPlane.HeartbeatTimeout,
			config.ControlPlane.HeartbeatTimeout,
		)
	}
	if loadedConfig.ControlPlane.TLSMode != config.ControlPlane.TLSMode {
		testingObject.Fatalf("unexpected control_plane.tls_mode: got=%s want=%s", loadedConfig.ControlPlane.TLSMode, config.ControlPlane.TLSMode)
	}
	if loadedConfig.ControlPlane.TLSCertSource != config.ControlPlane.TLSCertSource {
		testingObject.Fatalf(
			"unexpected control_plane.tls_cert_source: got=%s want=%s",
			loadedConfig.ControlPlane.TLSCertSource,
			config.ControlPlane.TLSCertSource,
		)
	}
	if loadedConfig.ControlPlane.TLSCACertFile != config.ControlPlane.TLSCACertFile {
		testingObject.Fatalf(
			"unexpected control_plane.tls_ca_cert_file: got=%s want=%s",
			loadedConfig.ControlPlane.TLSCACertFile,
			config.ControlPlane.TLSCACertFile,
		)
	}
	if loadedConfig.ControlPlane.TLSCAKeyFile != config.ControlPlane.TLSCAKeyFile {
		testingObject.Fatalf(
			"unexpected control_plane.tls_ca_key_file: got=%s want=%s",
			loadedConfig.ControlPlane.TLSCAKeyFile,
			config.ControlPlane.TLSCAKeyFile,
		)
	}
	if loadedConfig.ControlPlane.TLSServerCertTTL != config.ControlPlane.TLSServerCertTTL {
		testingObject.Fatalf(
			"unexpected control_plane.tls_server_cert_ttl: got=%s want=%s",
			loadedConfig.ControlPlane.TLSServerCertTTL,
			config.ControlPlane.TLSServerCertTTL,
		)
	}
	if loadedConfig.Admin.BasePath != config.Admin.BasePath {
		testingObject.Fatalf("unexpected admin.base_path: got=%s want=%s", loadedConfig.Admin.BasePath, config.Admin.BasePath)
	}
	if loadedConfig.Observability.LogLevel != config.Observability.LogLevel {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=%s", loadedConfig.Observability.LogLevel, config.Observability.LogLevel)
	}
	if loadedConfig.DefaultScope != config.DefaultScope {
		testingObject.Fatalf("unexpected default_scope: got=%+v want=%+v", loadedConfig.DefaultScope, config.DefaultScope)
	}
	if len(loadedConfig.FallbackPolicies) != 1 || loadedConfig.FallbackPolicies[0].PolicyID != "fallback-tenant" {
		testingObject.Fatalf("unexpected fallback_policies round trip: %+v", loadedConfig.FallbackPolicies)
	}
}
