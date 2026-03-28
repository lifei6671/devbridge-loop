import assert from "node:assert/strict";
import test from "node:test";

import {
  deriveManagedCAIdentitySuggestion,
  parseManagedCAHostFromListenAddr,
} from "../src/admin/model/managed_ca_hint.js";

test("parseManagedCAHostFromListenAddr splits bare host:port without keeping the port", () => {
  assert.equal(parseManagedCAHostFromListenAddr("bridge:39081"), "bridge");
  assert.equal(parseManagedCAHostFromListenAddr("127.0.0.1:39081"), "127.0.0.1");
  assert.equal(parseManagedCAHostFromListenAddr("[::1]:39083"), "::1");
});

test("deriveManagedCAIdentitySuggestion keeps SAN DNS free of bare host ports", () => {
  const suggestion = deriveManagedCAIdentitySuggestion({
    controlPlaneListenAddr: "bridge:39081",
    controlPlaneGRPCH2ListenAddr: "",
    controlPlaneQUICListenAddr: "",
  });

  assert.equal(suggestion.sanDNS, "bridge");
  assert.equal(suggestion.sanIPsAndCN, "- / CN=bridge");
});
