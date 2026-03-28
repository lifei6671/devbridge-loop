export type BridgeTransportKey = "tcp_framed" | "grpc_h2" | "quic_native";

export type BridgeTransportAddressMemory = Partial<Record<BridgeTransportKey, string>>;

export const DEFAULT_BRIDGE_ADDR_BY_TRANSPORT: Record<BridgeTransportKey, string>;

export function normalizeBridgeTransportKey(transport: string): BridgeTransportKey;

export function defaultBridgeAddrForTransport(transport: string): string;

export function buildBridgeTransportAddressMemory(snapshot: {
  bridge_addr: string;
  bridge_transport: string;
}): BridgeTransportAddressMemory;

export function rememberBridgeAddressForTransport(
  transport: string,
  bridgeAddr: string,
  memory?: BridgeTransportAddressMemory,
): BridgeTransportAddressMemory;

export function applyBridgeTransportSelection(input: {
  currentTransport: string;
  currentBridgeAddr: string;
  nextTransport: string;
  memory?: BridgeTransportAddressMemory;
}): {
  bridgeAddr: string;
  memory: BridgeTransportAddressMemory;
};
