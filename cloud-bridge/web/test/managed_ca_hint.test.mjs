import { describe, expect, it } from "vitest";

import {
  deriveManagedCAIdentitySuggestion,
  parseManagedCAHostFromListenAddr,
} from "../src/admin/model/managed_ca_hint.js";

describe("managed CA hint helpers", () => {
  it("splits bare host:port without keeping the port", () => {
    expect(parseManagedCAHostFromListenAddr("bridge:39081")).toBe("bridge");
    expect(parseManagedCAHostFromListenAddr("127.0.0.1:39081")).toBe("127.0.0.1");
    expect(parseManagedCAHostFromListenAddr("[::1]:39083")).toBe("::1");
  });

  it("keeps SAN DNS free of bare host ports", () => {
    const suggestion = deriveManagedCAIdentitySuggestion({
      controlPlaneListenAddr: "bridge:39081",
      controlPlaneGRPCH2ListenAddr: "",
      controlPlaneQUICListenAddr: "",
    });

    expect(suggestion.sanDNS).toBe("bridge");
    expect(suggestion.sanIPsAndCN).toBe("- / CN=bridge");
  });
});
