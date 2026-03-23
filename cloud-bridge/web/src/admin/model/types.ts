export type AdminPageKey =
  | "dashboard"
  | "routes"
  | "services"
  | "connectors"
  | "traffic"
  | "ops"
  | "observability";

export type ApiRecord = Record<string, unknown>;

export type StateTone = "normal" | "ok" | "warn" | "danger";

export type DetailDomain = "route" | "connector" | "session" | "tunnel";

export type DetailSelection = {
  domain: DetailDomain;
  index: number;
};

export type ChartDatum = {
  label: string;
  value: number;
  tone?: StateTone;
};

export type TrendDatum = {
  label: string;
  value: number;
};

export type DetailSummaryRow = {
  label: string;
  hint: string;
  value: string;
};

export type NavSection = {
  title: string;
  items: AdminPageKey[];
};

export type DashboardMetricCard = {
  label: string;
  value: string;
  hint: string;
  tone?: StateTone;
};

export type TrendSeriesTone = "blue" | "green" | "orange";

export type MultiTrendSeries = {
  label: string;
  tone: TrendSeriesTone;
  items: TrendDatum[];
  latestValue: number;
};

export type TunnelRingSegment = {
  label: string;
  value: number;
  color: string;
};

export type RefreshPageOptions = {
  silentError?: boolean;
};

export type RealtimeMode = "off" | "sse" | "polling";

export type SSEConnectionState = "idle" | "connecting" | "live" | "error";

export type AuthStatus = "loading" | "authenticated" | "anonymous";

export type AdminAuthProvider = {
  name: string;
  type: string;
  label: string;
  login_flow: string;
};

export type AdminSessionRecord = {
  username: string;
  display_name: string;
  role: string;
  provider: string;
  csrf_token: string;
  csrf_header_name: string;
  expires_at_ms: number;
};

export type SSEEnvelope = {
  version?: string;
  type?: string;
  topic?: string;
  server_time_ms?: number;
  sequence?: number;
  payload?: unknown;
};
