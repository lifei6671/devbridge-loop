export const DEFAULT_BRIDGE_ADDR_BY_TRANSPORT = Object.freeze({
  tcp_framed: "127.0.0.1:39081",
  grpc_h2: "127.0.0.1:39082",
  quic_native: "127.0.0.1:39083",
});

/**
 * @param {string} transport
 * @returns {"tcp_framed" | "grpc_h2" | "quic_native"}
 */
export function normalizeBridgeTransportKey(transport) {
  const normalized = transport.trim();
  if (normalized === "grpc_h2" || normalized === "quic_native") {
    return normalized;
  }
  return "tcp_framed";
}

/**
 * @param {string} transport
 * @returns {string}
 */
export function defaultBridgeAddrForTransport(transport) {
  const key = normalizeBridgeTransportKey(transport);
  return DEFAULT_BRIDGE_ADDR_BY_TRANSPORT[key];
}

/**
 * @param {{ bridge_addr: string; bridge_transport: string }} snapshot
 * @returns {Partial<Record<"tcp_framed" | "grpc_h2" | "quic_native", string>>}
 */
export function buildBridgeTransportAddressMemory(snapshot) {
  return rememberBridgeAddressForTransport(snapshot.bridge_transport, snapshot.bridge_addr, {});
}

/**
 * @param {string} transport
 * @param {string} bridgeAddr
 * @param {Partial<Record<"tcp_framed" | "grpc_h2" | "quic_native", string>>} [memory]
 * @returns {Partial<Record<"tcp_framed" | "grpc_h2" | "quic_native", string>>}
 */
export function rememberBridgeAddressForTransport(transport, bridgeAddr, memory = {}) {
  const normalizedBridgeAddr = bridgeAddr.trim();
  if (!normalizedBridgeAddr) {
    return { ...memory };
  }
  const key = normalizeBridgeTransportKey(transport);
  return {
    ...memory,
    [key]: normalizedBridgeAddr,
  };
}

/**
 * @param {{
 *   currentTransport: string;
 *   currentBridgeAddr: string;
 *   nextTransport: string;
 *   memory?: Partial<Record<"tcp_framed" | "grpc_h2" | "quic_native", string>>;
 * }} input
 * @returns {{
 *   bridgeAddr: string;
 *   memory: Partial<Record<"tcp_framed" | "grpc_h2" | "quic_native", string>>;
 * }}
 */
export function applyBridgeTransportSelection(input) {
  const nextMemory = rememberBridgeAddressForTransport(
    input.currentTransport,
    input.currentBridgeAddr,
    input.memory,
  );
  const nextTransport = normalizeBridgeTransportKey(input.nextTransport);
  return {
    bridgeAddr: nextMemory[nextTransport] ?? defaultBridgeAddrForTransport(nextTransport),
    memory: nextMemory,
  };
}
