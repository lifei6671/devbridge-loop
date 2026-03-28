import test from "node:test";
import assert from "node:assert/strict";

import {
  DEFAULT_BRIDGE_ADDR_BY_TRANSPORT,
  applyBridgeTransportSelection,
  rememberBridgeAddressForTransport,
} from "../src/bridge_transport_memory.js";

test("applyBridgeTransportSelection should fall back to per-transport defaults when no memory exists", () => {
  const result = applyBridgeTransportSelection({
    currentTransport: "tcp_framed",
    currentBridgeAddr: "127.0.0.1:39081",
    nextTransport: "grpc_h2",
    memory: {},
  });

  assert.equal(result.bridgeAddr, DEFAULT_BRIDGE_ADDR_BY_TRANSPORT.grpc_h2);
  assert.deepEqual(result.memory, {
    tcp_framed: "127.0.0.1:39081",
  });
});

test("applyBridgeTransportSelection should restore remembered address for the selected transport", () => {
  const result = applyBridgeTransportSelection({
    currentTransport: "grpc_h2",
    currentBridgeAddr: "bridge-a.internal:39082",
    nextTransport: "tcp_framed",
    memory: {
      tcp_framed: "bridge-a.internal:39081",
      quic_native: "bridge-a.internal:39083",
    },
  });

  assert.equal(result.bridgeAddr, "bridge-a.internal:39081");
  assert.deepEqual(result.memory, {
    tcp_framed: "bridge-a.internal:39081",
    grpc_h2: "bridge-a.internal:39082",
    quic_native: "bridge-a.internal:39083",
  });
});

test("rememberBridgeAddressForTransport should update current transport memory with the latest draft value", () => {
  const result = rememberBridgeAddressForTransport("quic_native", "bridge.example.com:443", {
    tcp_framed: "127.0.0.1:39081",
  });

  assert.deepEqual(result, {
    tcp_framed: "127.0.0.1:39081",
    quic_native: "bridge.example.com:443",
  });
});

test("rememberBridgeAddressForTransport should ignore blank addresses", () => {
  const result = rememberBridgeAddressForTransport("grpc_h2", "   ", {
    tcp_framed: "127.0.0.1:39081",
  });

  assert.deepEqual(result, {
    tcp_framed: "127.0.0.1:39081",
  });
});
