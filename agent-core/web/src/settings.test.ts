import { describe, expect, it } from "vitest";

import {
  buildConfigFromSettingsDraft,
  type ConfigSnapshot,
  type EditableAgentConfigDocument,
  parseNonNegativeSecondsToMillis,
  parsePositiveFloat,
  settingsDraftIsDirty,
  shouldHydrateSettingsDraft,
  toSettingsDraft,
  validateSettingsDraft,
} from "@/settings";

function buildConfigDocument(): EditableAgentConfigDocument {
  return {
    agent_id: "agent-local",
    bridge_addr: "127.0.0.1:39081",
    bridge_transport: "tcp_framed",
    bridge_tls: {
      enabled: false,
      root_ca_file: "",
      server_name: "",
    },
    session: {
      heartbeat_interval: "5s",
      auth_timeout: "5s",
      auth_method: "token",
      auth_token: "keep-me",
      client_cap_version: "agent-core/v1",
    },
    tunnel_pool: {
      min_idle: 8,
      max_idle: 32,
      max_inflight: 4,
      ttl: "10m0s",
      max_reuse: 256,
      recycle_ack_timeout: "3s",
      open_rate: 10,
      open_burst: 20,
      reconcile_gap: "1s",
    },
    observability: {
      metrics_addr: "127.0.0.1:39090",
      log_level: "info",
    },
    control_channel: {
      dial_timeout: "5s",
    },
    ui: {
      web: {
        enabled: true,
        listen_addr: "127.0.0.1:39082",
        base_path: "/agent",
        session_cookie_name: "devbridge_agent_session",
        auth: {
          username: "admin",
          password: "change-me",
        },
      },
    },
  };
}

function buildSnapshot(document: EditableAgentConfigDocument): ConfigSnapshot {
  return {
    config_version: 3,
    config_file_path: "/tmp/agent.yaml",
    config_file_source: "user",
    base_config_file_path: "/tmp/agent.yaml",
    runtime_config_file_path: "/tmp/agent.yaml",
    runtime_local_config_path: "",
    runtime_system_config_path: "/etc/devbridge/agent.yaml",
    runtime_explicit_config_path: "",
    updated_at_ms: Date.now(),
    updated_by: "admin",
    reload_required: true,
    applied_to_runtime: false,
    source: "agent.runtime.config.store",
    config: document,
    agent_id: document.agent_id,
    bridge_addr: document.bridge_addr,
    bridge_transport: document.bridge_transport,
    tunnel_pool_min_idle: document.tunnel_pool.min_idle,
    tunnel_pool_max_idle: document.tunnel_pool.max_idle,
    tunnel_pool_max_inflight: document.tunnel_pool.max_inflight,
    tunnel_pool_ttl_ms: 600000,
    tunnel_pool_max_reuse: document.tunnel_pool.max_reuse,
    tunnel_pool_recycle_ack_ms: 3000,
    tunnel_pool_open_rate: document.tunnel_pool.open_rate,
    tunnel_pool_open_burst: document.tunnel_pool.open_burst,
    tunnel_pool_reconcile_gap_ms: 1000,
    ipc_transport: "",
    ipc_endpoint: "",
  };
}

describe("settings helpers", () => {
  it("maps config snapshot to tauri-aligned settings draft", () => {
    const snapshot = buildSnapshot(buildConfigDocument());

    const draft = toSettingsDraft(snapshot);

    expect(draft.agentId).toBe("agent-local");
    expect(draft.bridgeAddr).toBe("127.0.0.1:39081");
    expect(draft.transport).toBe("tcp_framed");
    expect(draft.bridgeTLSEnabled).toBe(false);
    expect(draft.sessionAuthToken).toBe("keep-me");
    expect(draft.tunnelPoolTtlSecText).toBe("600");
    expect(draft.tunnelPoolReconcileGapMsText).toBe("1000");
  });

  it("defaults token field to blank when snapshot does not echo token", () => {
    const document = buildConfigDocument();
    document.session.auth_token = "";
    const snapshot = buildSnapshot(document);

    const draft = toSettingsDraft(snapshot);

    expect(draft.sessionAuthToken).toBe("");
    expect(settingsDraftIsDirty(draft, snapshot)).toBe(false);
  });

  it("builds updated config while preserving non-tauri fields", () => {
    const baseDocument = buildConfigDocument();
    const snapshot = buildSnapshot(baseDocument);
    const draft = {
      ...toSettingsDraft(snapshot),
      bridgeAddr: "127.0.0.1:49081",
      transport: "quic_native",
      bridgeTLSEnabled: true,
      bridgeTLSRootCAFile: "/etc/devbridge/root-ca.crt",
      bridgeTLSServerName: "bridge.internal.example",
      sessionAuthToken: "dbt_agent-local.new-secret",
      tunnelPoolMinIdleText: "10",
      tunnelPoolTtlSecText: "30",
      tunnelPoolOpenRateText: "12.5",
    };

    const nextDocument = buildConfigFromSettingsDraft(baseDocument, draft);

    expect(nextDocument.bridge_addr).toBe("127.0.0.1:49081");
    expect(nextDocument.bridge_transport).toBe("quic_native");
    expect(nextDocument.bridge_tls.enabled).toBe(true);
    expect(nextDocument.bridge_tls.root_ca_file).toBe("/etc/devbridge/root-ca.crt");
    expect(nextDocument.bridge_tls.server_name).toBe("bridge.internal.example");
    expect(nextDocument.session.auth_token).toBe("dbt_agent-local.new-secret");
    expect(nextDocument.tunnel_pool.min_idle).toBe(10);
    expect(nextDocument.tunnel_pool.ttl).toBe("30s");
    expect(nextDocument.tunnel_pool.open_rate).toBe(12.5);
    expect(nextDocument.ui.web.auth.password).toBe("change-me");
  });

  it("keeps existing token when saving draft with blank token", () => {
    const baseDocument = buildConfigDocument();
    const snapshot = buildSnapshot({
      ...baseDocument,
      session: {
        ...baseDocument.session,
        auth_token: "",
      },
    });
    const draft = {
      ...toSettingsDraft(snapshot),
      bridgeAddr: "127.0.0.1:49081",
      sessionAuthToken: "   ",
    };

    const nextDocument = buildConfigFromSettingsDraft(baseDocument, draft);

    expect(nextDocument.bridge_addr).toBe("127.0.0.1:49081");
    expect(nextDocument.session.auth_token).toBe("keep-me");
  });

  it("detects whether tauri-aligned settings changed", () => {
    const snapshot = buildSnapshot(buildConfigDocument());
    const originalDraft = toSettingsDraft(snapshot);

    expect(settingsDraftIsDirty(originalDraft, snapshot)).toBe(false);

    const changedDraft = {
      ...originalDraft,
      sessionAuthToken: "dbt_agent-local.changed",
    };
    expect(settingsDraftIsDirty(changedDraft, snapshot)).toBe(true);
  });

  it("hydrates latest snapshot when current draft was never edited by user", () => {
    const emptyDocument = buildConfigDocument();
    emptyDocument.agent_id = "";
    emptyDocument.bridge_addr = "";
    emptyDocument.bridge_tls.root_ca_file = "";
    emptyDocument.tunnel_pool.min_idle = 0;
    emptyDocument.tunnel_pool.max_idle = 0;
    emptyDocument.tunnel_pool.max_inflight = 0;
    emptyDocument.tunnel_pool.ttl = "0s";
    emptyDocument.tunnel_pool.open_rate = 0;
    emptyDocument.tunnel_pool.open_burst = 0;
    emptyDocument.tunnel_pool.reconcile_gap = "0s";

    const staleDraft = toSettingsDraft(buildSnapshot(emptyDocument));
    const latestSnapshot = buildSnapshot(buildConfigDocument());

    expect(shouldHydrateSettingsDraft(staleDraft, latestSnapshot, false)).toBe(true);
  });

  it("keeps current draft when user has local unsaved edits", () => {
    const snapshot = buildSnapshot(buildConfigDocument());
    const editedDraft = {
      ...toSettingsDraft(snapshot),
      bridgeAddr: "127.0.0.1:49081",
    };

    expect(shouldHydrateSettingsDraft(editedDraft, snapshot, true)).toBe(false);
  });

  it("returns field-level validation errors for invalid tauri-aligned draft", () => {
    const snapshot = buildSnapshot(buildConfigDocument());
    const draft = {
      ...toSettingsDraft(snapshot),
      transport: "quic_native",
      bridgeTLSEnabled: false,
      sessionAuthToken: "",
      tunnelPoolMinIdleText: "40",
      tunnelPoolMaxIdleText: "20",
      tunnelPoolOpenRateText: "0",
    };

    const errors = validateSettingsDraft(draft);

    expect(errors.bridgeTLSEnabled).toContain("quic_native");
    expect(errors.tunnelPoolMinIdleText).toContain("不能大于");
    expect(errors.tunnelPoolOpenRateText).toContain("必须大于 0");
    expect(errors.sessionAuthToken).toBeUndefined();
  });

  it("rejects trailing garbage and accepts trimmed decimal text in numeric parsers", () => {
    const cases = [
      {
        name: "parsePositiveFloat trims whitespace",
        run: () => parsePositiveFloat(" 12.5 ", "tunnel_pool_open_rate"),
        expected: 12.5,
      },
      {
        name: "parsePositiveFloat rejects trailing garbage",
        run: () => parsePositiveFloat("12abc", "tunnel_pool_open_rate"),
        error: "tunnel_pool_open_rate 必须是数字",
      },
      {
        name: "parseNonNegativeSecondsToMillis trims whitespace",
        run: () => parseNonNegativeSecondsToMillis(" 1.5 ", "tunnel_pool_ttl_s"),
        expected: 1500,
      },
      {
        name: "parseNonNegativeSecondsToMillis rejects trailing garbage",
        run: () => parseNonNegativeSecondsToMillis("1.5ms", "tunnel_pool_ttl_s"),
        error: "tunnel_pool_ttl_s 必须是数字",
      },
    ] as const;

    for (const testCase of cases) {
      if ("expected" in testCase) {
        expect(testCase.run()).toBe(testCase.expected);
      } else {
        expect(testCase.run).toThrow(testCase.error);
      }
    }
  });
});
