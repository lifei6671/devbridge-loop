package main

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

// TestApplyControlPlaneTLSEnvOverridesManagedCA 验证环境变量可覆盖 control_plane TLS 配置并切换到 managed_ca。
func TestApplyControlPlaneTLSEnvOverridesManagedCA(testingObject *testing.T) {
	config := app.DefaultConfig()
	config.ControlPlane.TLSMode = "required"
	config.ControlPlane.TLSCertSource = "external"
	config.ControlPlane.TLSCertFile = "/tmp/from-yaml-cert.pem"
	config.ControlPlane.TLSKeyFile = "/tmp/from-yaml-key.pem"

	testingObject.Setenv(envControlPlaneTLSCertSource, "managed_ca")
	testingObject.Setenv(envControlPlaneTLSCACertFile, "/tmp/managed-ca.crt")
	testingObject.Setenv(envControlPlaneTLSCAKeyFile, "/tmp/managed-ca.key")
	testingObject.Setenv(envControlPlaneTLSServerCommonName, "bridge.internal.example")
	testingObject.Setenv(envControlPlaneTLSServerSANDNS, "bridge.internal.example, bridge.internal.svc")
	testingObject.Setenv(envControlPlaneTLSServerSANIPs, "127.0.0.1,10.20.30.40")
	testingObject.Setenv(envControlPlaneTLSServerCertTTL, "72h")
	testingObject.Setenv(envControlPlaneTLSServerCertRenewBefore, "12h")

	if err := applyControlPlaneTLSEnvOverrides(&config); err != nil {
		testingObject.Fatalf("apply tls env overrides failed: %v", err)
	}
	if config.ControlPlane.TLSCertSource != "managed_ca" {
		testingObject.Fatalf("unexpected tls cert source: got=%s want=%s", config.ControlPlane.TLSCertSource, "managed_ca")
	}
	if config.ControlPlane.TLSCACertFile != "/tmp/managed-ca.crt" {
		testingObject.Fatalf("unexpected tls ca cert file: got=%s", config.ControlPlane.TLSCACertFile)
	}
	if config.ControlPlane.TLSCAKeyFile != "/tmp/managed-ca.key" {
		testingObject.Fatalf("unexpected tls ca key file: got=%s", config.ControlPlane.TLSCAKeyFile)
	}
	if len(config.ControlPlane.TLSServerSANDNS) != 2 {
		testingObject.Fatalf("unexpected san dns count: got=%d want=2", len(config.ControlPlane.TLSServerSANDNS))
	}
	if len(config.ControlPlane.TLSServerSANIPs) != 2 {
		testingObject.Fatalf("unexpected san ip count: got=%d want=2", len(config.ControlPlane.TLSServerSANIPs))
	}
	if config.ControlPlane.TLSServerCertTTL != 72*time.Hour {
		testingObject.Fatalf("unexpected tls server cert ttl: got=%s want=%s", config.ControlPlane.TLSServerCertTTL, 72*time.Hour)
	}
	if config.ControlPlane.TLSServerCertRenewBefore != 12*time.Hour {
		testingObject.Fatalf(
			"unexpected tls server cert renew before: got=%s want=%s",
			config.ControlPlane.TLSServerCertRenewBefore,
			12*time.Hour,
		)
	}
}

// TestApplyControlPlaneTLSEnvOverridesClearSANList 验证显式传空字符串时可清空 SAN 覆盖值。
func TestApplyControlPlaneTLSEnvOverridesClearSANList(testingObject *testing.T) {
	config := app.DefaultConfig()
	config.ControlPlane.TLSServerSANDNS = []string{"bridge.internal.example"}
	config.ControlPlane.TLSServerSANIPs = []string{"127.0.0.1"}

	testingObject.Setenv(envControlPlaneTLSServerSANDNS, "  ")
	testingObject.Setenv(envControlPlaneTLSServerSANIPs, "")

	if err := applyControlPlaneTLSEnvOverrides(&config); err != nil {
		testingObject.Fatalf("apply tls env overrides failed: %v", err)
	}
	if len(config.ControlPlane.TLSServerSANDNS) != 0 {
		testingObject.Fatalf("expected san dns cleared, got=%v", config.ControlPlane.TLSServerSANDNS)
	}
	if len(config.ControlPlane.TLSServerSANIPs) != 0 {
		testingObject.Fatalf("expected san ips cleared, got=%v", config.ControlPlane.TLSServerSANIPs)
	}
}

// TestApplyRuntimeConfigEnvOverridesValidate 验证环境变量覆盖后仍会执行结构化校验。
func TestApplyRuntimeConfigEnvOverridesValidate(testingObject *testing.T) {
	config := app.DefaultConfig()
	config.ControlPlane.TLSMode = "required"
	config.ControlPlane.TLSCertSource = "external"
	config.ControlPlane.TLSCertFile = "/tmp/bridge.crt"
	config.ControlPlane.TLSKeyFile = "/tmp/bridge.key"

	testingObject.Setenv(envControlPlaneTLSCertSource, "managed_ca")
	// 仅切换 cert source 但不补 ca 文件时应被 validate 拦截。
	if _, err := applyRuntimeConfigEnvOverrides(config); err == nil {
		testingObject.Fatalf("expected apply runtime config env overrides validation error")
	}
}

// TestDurationEnvOrDefaultRejectsInvalidValue 验证非法 duration 环境变量会返回解析错误。
func TestDurationEnvOrDefaultRejectsInvalidValue(testingObject *testing.T) {
	testingObject.Setenv(envControlPlaneTLSServerCertTTL, "not-a-duration")
	if _, err := durationEnvOrDefault(envControlPlaneTLSServerCertTTL, time.Hour); err == nil {
		testingObject.Fatalf("expected duration env parse error")
	}
}
