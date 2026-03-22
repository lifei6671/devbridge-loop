import { asRecord, isRecord, readNumber, readText } from "./records";
import type { ApiRecord } from "./types";

export type OpsConfigFieldKind = "text" | "boolean" | "select" | "number";
export type OpsConfigSource = "default" | "env" | "system" | "user";
export type OpsConfigSourceBadgeVariant =
  | "default"
  | "secondary"
  | "outline"
  | "success"
  | "warning"
  | "danger";

export type OpsConfigFieldOption = {
  label: string;
  value: string;
};

export type OpsConfigField = {
  key: string;
  kind: OpsConfigFieldKind;
  label: string;
  description: string;
  impact: string;
  placeholder?: string;
  restartRequired?: boolean;
  unit?: string;
  options?: OpsConfigFieldOption[];
};

export type OpsConfigSection = {
  key: string;
  title: string;
  description: string;
  fields: OpsConfigField[];
};

const sourceLabelMap: Record<OpsConfigSource, string> = {
  default: "默认值",
  env: "环境变量",
  system: "系统目录",
  user: "用户目录",
};

const sourceBadgeVariantMap: Record<OpsConfigSource, OpsConfigSourceBadgeVariant> = {
  default: "outline",
  env: "warning",
  system: "secondary",
  user: "success",
};

export const opsConfigSections: OpsConfigSection[] = [
  {
    key: "ingress",
    title: "接入配置",
    description: "入口监听和域名推导相关的常用项。",
    fields: [
      {
        key: "ingress.http_addr",
        kind: "text",
        label: "HTTP 监听地址",
        description: "控制 HTTP 入口的监听地址。",
        impact: "修改后会影响外部 HTTP 访问入口，通常需要重启生效。",
        placeholder: ":38080",
        restartRequired: true,
      },
      {
        key: "ingress.grpc_addr",
        kind: "text",
        label: "gRPC 监听地址",
        description: "控制 ingress gRPC 服务的监听地址。",
        impact: "修改后会影响 gRPC 接入流量，通常需要重启生效。",
        placeholder: ":38081",
        restartRequired: true,
      },
      {
        key: "ingress.https_addr",
        kind: "text",
        label: "HTTPS 监听地址",
        description: "控制 HTTPS 入口的监听地址。",
        impact: "会影响 TLS/HTTPS 暴露端口，通常需要重启生效。",
        placeholder: ":8443",
        restartRequired: true,
      },
      {
        key: "ingress.tls_sni_addr",
        kind: "text",
        label: "TLS SNI 监听地址",
        description: "控制基于 SNI 的共享 TLS 监听地址。",
        impact: "会影响共享 TLS 入口和证书握手流量，通常需要重启生效。",
        placeholder: ":8443",
        restartRequired: true,
      },
      {
        key: "ingress.tcp_port_range",
        kind: "text",
        label: "TCP 端口范围",
        description: "控制动态分配给 TCP 入口的端口池范围。",
        impact: "端口池变化会影响后续分配结果，通常需要重启生效。",
        placeholder: "9000-9100",
        restartRequired: true,
      },
      {
        key: "ingress.base_domain",
        kind: "text",
        label: "基础域名",
        description: "用于派生入口域名和服务暴露地址。",
        impact: "修改后会影响路由域名生成与访问地址，通常需要重启生效。",
        placeholder: "example.com",
        restartRequired: true,
      },
    ],
  },
  {
    key: "admin",
    title: "管理后台",
    description: "后台监听、UI 暴露和共享监听策略。",
    fields: [
      {
        key: "admin.enabled",
        kind: "boolean",
        label: "启用管理后台",
        description: "控制管理 API 和后台整体开关。",
        impact: "关闭后将不再暴露后台入口，通常需要重启生效。",
        restartRequired: true,
      },
      {
        key: "admin.listen_addr",
        kind: "text",
        label: "后台监听地址",
        description: "控制管理后台监听端口。",
        impact: "修改后会影响后台访问地址，通常需要重启生效。",
        placeholder: ":39081",
        restartRequired: true,
      },
      {
        key: "admin.allow_shared_listener",
        kind: "boolean",
        label: "允许共享监听",
        description: "控制后台是否允许和其他监听器共享端口。",
        impact: "会影响监听冲突和端口复用策略，通常需要重启生效。",
        restartRequired: true,
      },
      {
        key: "admin.ui_enabled",
        kind: "boolean",
        label: "启用后台 UI",
        description: "控制静态管理页面是否暴露。",
        impact: "关闭后 API 仍可按配置保留，但 UI 路由会停止提供，通常需要重启生效。",
        restartRequired: true,
      },
      {
        key: "admin.base_path",
        kind: "text",
        label: "后台挂载路径",
        description: "控制后台 UI 路由的挂载前缀。",
        impact: "修改后会影响后台 URL 路径，通常需要重启生效。",
        placeholder: "/admin",
        restartRequired: true,
      },
    ],
  },
  {
    key: "control-plane",
    title: "控制面",
    description: "Bridge 控制面监听和心跳策略。",
    fields: [
      {
        key: "control_plane.listen_addr",
        kind: "text",
        label: "控制面监听地址",
        description: "控制 LTFP 控制面监听地址。",
        impact: "会影响 Agent/Bridge 建链入口，通常需要重启生效。",
        placeholder: ":39080",
        restartRequired: true,
      },
      {
        key: "control_plane.grpc_h2_listen_addr",
        kind: "text",
        label: "gRPC H2 监听地址",
        description: "控制控制面 gRPC/H2 入口。",
        impact: "会影响使用 gRPC/H2 的控制链路，通常需要重启生效。",
        placeholder: ":39082",
        restartRequired: true,
      },
      {
        key: "control_plane.heartbeat_timeout_ms",
        kind: "number",
        label: "心跳超时",
        description: "控制会话失活前允许的心跳超时窗口。",
        impact: "窗口越小故障收敛越快，但误判风险也更高，通常需要重启生效。",
        placeholder: "30000",
        restartRequired: true,
        unit: "ms",
      },
    ],
  },
  {
    key: "observability",
    title: "观测配置",
    description: "日志级别和指标暴露地址。",
    fields: [
      {
        key: "observability.log_level",
        kind: "select",
        label: "日志级别",
        description: "控制运行日志输出级别。",
        impact: "级别越低日志越详细，但会带来更多输出和存储压力。",
        options: [
          { label: "Debug", value: "debug" },
          { label: "Info", value: "info" },
          { label: "Warn", value: "warn" },
          { label: "Error", value: "error" },
        ],
        restartRequired: true,
      },
      {
        key: "observability.metrics_addr",
        kind: "text",
        label: "指标监听地址",
        description: "控制 Prometheus 指标端点监听地址。",
        impact: "会影响采集器访问 `/metrics` 的地址，通常需要重启生效。",
        placeholder: ":39090",
        restartRequired: true,
      },
    ],
  },
  {
    key: "scope",
    title: "默认作用域",
    description: "未显式指定 scope 时使用的默认命名空间与环境。",
    fields: [
      {
        key: "default_scope.namespace",
        kind: "text",
        label: "默认命名空间",
        description: "控制缺省请求和路由计算时的默认 namespace。",
        impact: "会影响未显式带 scope 的默认归属，通常需要重启生效。",
        placeholder: "default",
        restartRequired: true,
      },
      {
        key: "default_scope.environment",
        kind: "text",
        label: "默认环境",
        description: "控制缺省请求和路由计算时的默认 environment。",
        impact: "会影响未显式带 scope 的默认归属，通常需要重启生效。",
        placeholder: "base",
        restartRequired: true,
      },
    ],
  },
];

export function listOpsConfigFields() {
  return opsConfigSections.flatMap((section) => section.fields);
}

export function readOpsConfigValue(snapshot: ApiRecord, fieldKey: string): unknown {
  if (fieldKey === "control_plane.heartbeat_timeout_ms") {
    return readNumber(asRecord(snapshot.control_plane), "heartbeat_timeout_ms", 0);
  }
  return readNestedConfigValue(snapshot, fieldKey);
}

export function buildOpsConfigDraft(snapshot: ApiRecord): Record<string, unknown> {
  const draft: Record<string, unknown> = {};
  for (const field of listOpsConfigFields()) {
    draft[field.key] = readOpsConfigValue(snapshot, field.key);
  }
  return draft;
}

export function readOpsConfigSource(snapshot: ApiRecord, fieldKey: string): string {
  const sourceRecord = asRecord(snapshot.field_sources);
  return readText(sourceRecord, fieldKey, "default");
}

export function readEditableUserPatch(snapshot: ApiRecord): Record<string, unknown> {
  return asRecord(snapshot.editable_user_patch);
}

export type OpsConfigRestorePreview = {
  source: string;
  value: unknown;
};

export type OpsConfigChangePreview = {
  currentText: string;
  nextText: string;
  nextLabel: string;
  note: string;
};

export function readOpsConfigRestorePreview(
  snapshot: ApiRecord,
  fieldKey: string
): OpsConfigRestorePreview | null {
  const previewRecord = asRecord(asRecord(snapshot.field_restore_preview)[fieldKey]);
  if (Object.prototype.hasOwnProperty.call(previewRecord, "value") === false) {
    return null;
  }
  return {
    source: readText(previewRecord, "source", "default"),
    value: previewRecord.value,
  };
}

export function formatOpsConfigSource(source: string): string {
  return sourceLabelMap[normalizeOpsConfigSource(source)] ?? sourceLabelMap.default;
}

export function getOpsConfigSourceBadgeVariant(source: string): OpsConfigSourceBadgeVariant {
  return sourceBadgeVariantMap[normalizeOpsConfigSource(source)] ?? sourceBadgeVariantMap.default;
}

export function getOpsConfigHelpText(field: OpsConfigField): string {
  const effectLabel = field.restartRequired === true ? "通常需要重启生效" : "通常可即时生效";
  return `${field.description} 影响：${field.impact} 生效方式：${effectLabel} 优先级：环境变量 > 用户目录 > 系统目录 > 默认值。`;
}

export function isOpsConfigFieldDirty(
  snapshot: ApiRecord,
  draft: Record<string, unknown>,
  field: OpsConfigField
): boolean {
  return areOpsConfigValuesEqual(readOpsConfigValue(snapshot, field.key), draft[field.key]) === false;
}

export function buildOpsConfigPatch(
  snapshot: ApiRecord,
  draft: Record<string, unknown>
): Record<string, unknown> {
  const patch: Record<string, unknown> = {};
  for (const field of listOpsConfigFields()) {
    if (isOpsConfigFieldDirty(snapshot, draft, field) === true) {
      patch[field.key] = normalizeOpsConfigValue(field, draft[field.key]);
    }
  }
  return patch;
}

export function buildOpsConfigChangePreview(
  field: OpsConfigField,
  source: string,
  currentValue: unknown,
  nextValue: unknown
): OpsConfigChangePreview | null {
  if (areOpsConfigValuesEqual(currentValue, nextValue) === true) {
    return null;
  }
  const normalizedSource = normalizeOpsConfigSource(source);
  const normalizedNextValue = normalizeOpsConfigValue(field, nextValue);
  if (normalizedSource === "env") {
    return {
      currentText: formatOpsConfigFieldValue(field, currentValue),
      nextText: formatOpsConfigFieldValue(field, normalizedNextValue),
      nextLabel: "用户目录",
      note: "环境变量仍会覆盖当前生效值，这次保存只会写入用户目录 override。",
    };
  }
  return {
    currentText: formatOpsConfigFieldValue(field, currentValue),
    nextText: formatOpsConfigFieldValue(field, normalizedNextValue),
    nextLabel: "保存后",
    note: "保存后生效值会按用户目录 override 更新。",
  };
}

export function formatOpsConfigFieldValue(field: OpsConfigField, value: unknown): string {
  if (field.kind === "boolean") {
    return value === true ? "开启" : "关闭";
  }
  if (field.kind === "number") {
    const parsedValue = Number(value);
    if (Number.isFinite(parsedValue) === false) {
      return "-";
    }
    const normalizedValue = String(Math.trunc(parsedValue));
    return field.unit ? `${normalizedValue} ${field.unit}` : normalizedValue;
  }
  if (field.kind === "select") {
    const normalizedValue = String(value ?? "").trim();
    if (normalizedValue === "") {
      return "-";
    }
    const matchedOption = (field.options ?? []).find((option) => option.value === normalizedValue);
    return matchedOption?.label ?? normalizedValue;
  }
  const normalizedValue = String(value ?? "").trim();
  return normalizedValue === "" ? "-" : normalizedValue;
}

export function validateOpsConfigPatch(patch: Record<string, unknown>): string | null {
  for (const field of listOpsConfigFields()) {
    if (Object.prototype.hasOwnProperty.call(patch, field.key) === false) {
      continue;
    }
    const patchValue = patch[field.key];
    if (field.kind === "boolean") {
      continue;
    }
    const normalizedText = String(patchValue ?? "").trim();
    if (normalizedText === "") {
      return `${field.label} 不能为空`;
    }
    if (field.kind === "number") {
      const parsedValue = Number(patchValue);
      if (Number.isFinite(parsedValue) === false || parsedValue <= 0) {
        return `${field.label} 仅支持正整数`;
      }
      continue;
    }
    if (field.kind === "select") {
      const isKnownOption = (field.options ?? []).some((option) => option.value === normalizedText);
      if (isKnownOption === false) {
        return `${field.label} 取值不合法`;
      }
    }
  }
  return null;
}

function normalizeOpsConfigValue(field: OpsConfigField, rawValue: unknown): unknown {
  if (field.kind === "boolean") {
    return rawValue === true;
  }
  if (field.kind === "number") {
    const parsedValue = Number(rawValue);
    if (Number.isFinite(parsedValue) === true) {
      return Math.trunc(parsedValue);
    }
    return 0;
  }
  return String(rawValue ?? "").trim();
}

function areOpsConfigValuesEqual(left: unknown, right: unknown): boolean {
  if (typeof left === "boolean" || typeof right === "boolean") {
    return left === right;
  }
  if (typeof left === "number" || typeof right === "number") {
    return Number(left) === Number(right);
  }
  return String(left ?? "") === String(right ?? "");
}

function readNestedConfigValue(record: ApiRecord, dottedPath: string): unknown {
  const segments = dottedPath.split(".");
  let current: unknown = record;
  for (const segment of segments) {
    const currentRecord = asRecord(current);
    current = currentRecord[segment];
  }
  return current;
}

function normalizeOpsConfigSource(source: string): OpsConfigSource {
  if (source === "env" || source === "system" || source === "user") {
    return source;
  }
  return "default";
}

export function buildConfigUpdateYAMLDocument(
  configVersion: number,
  patch: Record<string, unknown>
): string {
  const normalizedVersion = Number.isFinite(configVersion) === true && configVersion > 0
    ? Math.trunc(configVersion)
    : 0;
  return stringifyYAML({
    if_match_version: normalizedVersion,
    patch: expandDottedRecord(patch),
  });
}

export function formatConfigSnapshotAsYAML(snapshot: ApiRecord): string {
  return stringifyYAML(snapshot);
}

function expandDottedRecord(patch: Record<string, unknown>): Record<string, unknown> {
  const expandedRecord: Record<string, unknown> = {};
  for (const [patchKey, patchValue] of Object.entries(patch)) {
    if (patchValue === undefined) {
      continue;
    }
    setExpandedRecordValue(expandedRecord, patchKey, patchValue);
  }
  return expandedRecord;
}

function setExpandedRecordValue(
  targetRecord: Record<string, unknown>,
  dottedPath: string,
  value: unknown
) {
  const pathSegments = dottedPath
    .split(".")
    .map((segment) => segment.trim())
    .filter((segment) => segment !== "");
  if (pathSegments.length === 0) {
    return;
  }
  let currentRecord = targetRecord;
  for (const [segmentIndex, segment] of pathSegments.entries()) {
    const isLeafSegment = segmentIndex === pathSegments.length - 1;
    if (isLeafSegment === true) {
      currentRecord[segment] = value;
      return;
    }
    const nextValue = currentRecord[segment];
    if (isRecord(nextValue) === true) {
      currentRecord = nextValue;
      continue;
    }
    const nestedRecord: Record<string, unknown> = {};
    currentRecord[segment] = nestedRecord;
    currentRecord = nestedRecord;
  }
}

function stringifyYAML(value: unknown, indentLevel = 0): string {
  if (Array.isArray(value) === true) {
    if (value.length === 0) {
      return "[]";
    }
    const indent = "  ".repeat(indentLevel);
    return value
      .map((item) => {
        if (isYAMLScalar(item) === true) {
          return `${indent}- ${formatYAMLScalar(item)}`;
        }
        return `${indent}-\n${stringifyYAML(item, indentLevel + 1)}`;
      })
      .join("\n");
  }

  if (isRecord(value) === true) {
    const recordEntries = Object.entries(value);
    if (recordEntries.length === 0) {
      return "{}";
    }
    const indent = "  ".repeat(indentLevel);
    return recordEntries
      .map(([recordKey, recordValue]) => {
        const yamlKey = formatYAMLKey(recordKey);
        if (isYAMLScalar(recordValue) === true) {
          return `${indent}${yamlKey}: ${formatYAMLScalar(recordValue)}`;
        }
        return `${indent}${yamlKey}:\n${stringifyYAML(recordValue, indentLevel + 1)}`;
      })
      .join("\n");
  }

  return formatYAMLScalar(String(value ?? ""));
}


type YAMLScalar = boolean | number | string | null | undefined;

function isYAMLScalar(value: unknown): value is YAMLScalar {
  return (
    value === null ||
    value === undefined ||
    typeof value === "boolean" ||
    typeof value === "number" ||
    typeof value === "string"
  );
}

function formatYAMLScalar(value: YAMLScalar): string {
  if (value === null || value === undefined) {
    return "null";
  }
  if (typeof value === "boolean") {
    return value === true ? "true" : "false";
  }
  if (typeof value === "number") {
    return Number.isFinite(value) === true ? String(value) : "null";
  }
  return JSON.stringify(value);
}

function formatYAMLKey(key: string): string {
  const safeKeyPattern = /^[A-Za-z0-9_.-]+$/;
  return safeKeyPattern.test(key) === true ? key : JSON.stringify(key);
}

