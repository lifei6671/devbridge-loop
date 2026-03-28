export type ManagedCAIdentitySuggestion = {
  behaviorSummary: string;
  sanDNS: string;
  sanIPsAndCN: string;
};

export function deriveManagedCAIdentitySuggestion(input: {
  controlPlaneGRPCH2ListenAddr: string;
  controlPlaneListenAddr: string;
  controlPlaneQUICListenAddr: string;
}): ManagedCAIdentitySuggestion;

export function parseManagedCAHostFromListenAddr(listenAddr: string): string;
