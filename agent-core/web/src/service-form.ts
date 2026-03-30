import type { ServiceItem } from "@/console-shared";

export type ServiceFormState = {
  instanceID: string;
  namespace: string;
  environment: string;
  serviceName: string;
  protocol: string;
  host: string;
  port: string;
  ingressMode: string;
  ingressHost: string;
  pathPrefix: string;
  listenPort: string;
  sniName: string;
  healthCheckMode: string;
  healthCheckPath: string;
  healthInterval: string;
  allowExport: boolean;
  notes: string;
};

export type ServiceFormFieldKey = keyof ServiceFormState;
export type ServiceFormErrors = Partial<Record<ServiceFormFieldKey, string>>;

export type ServicePayload = {
  instance_id: string;
  scope: {
    namespace: string;
    environment: string;
  };
  service_name: string;
  protocol: string;
  host: string;
  port: number;
  sni_name: string;
  exposure: {
    ingress_mode: string;
    host: string;
    listen_port: number;
    sni_name: string;
    path_prefix: string;
    allow_export: boolean;
  };
  health_check_interval_sec: number;
  health_check_mode: string;
  health_check_path: string;
  route_hint: Record<string, never>;
};

export const defaultServiceForm: ServiceFormState = {
  instanceID: "",
  namespace: "default",
  environment: "prod",
  serviceName: "",
  protocol: "http",
  host: "127.0.0.1",
  port: "8080",
  ingressMode: "l7_shared",
  ingressHost: "",
  pathPrefix: "",
  listenPort: "",
  sniName: "",
  healthCheckMode: "http",
  healthCheckPath: "/",
  healthInterval: "30",
  allowExport: false,
  notes: "",
};

const supportedServiceProtocols = new Set(["http", "https", "tcp", "grpc"]);
const supportedIngressModes = new Set(["l7_shared", "tls_sni_shared", "l4_dedicated_port"]);
const supportedHealthCheckModes = new Set(["http", "https", "tcp"]);

export function validateServiceForm(form: ServiceFormState): ServiceFormErrors {
  const errors: ServiceFormErrors = {};

  if (!form.namespace.trim()) {
    errors.namespace = "命名空间不能为空";
  }
  if (!form.environment.trim()) {
    errors.environment = "环境不能为空";
  }
  if (!form.serviceName.trim()) {
    errors.serviceName = "服务名不能为空";
  }
  if (!form.host.trim()) {
    errors.host = "主机不能为空";
  }
  if (!supportedServiceProtocols.has(form.protocol.trim())) {
    errors.protocol = "协议仅支持 http / https / tcp / grpc";
  }
  if (!supportedIngressModes.has(form.ingressMode.trim())) {
    errors.ingressMode = "入口模式仅支持 l7_shared / tls_sni_shared / l4_dedicated_port";
  }
  if (!supportedHealthCheckModes.has(form.healthCheckMode.trim())) {
    errors.healthCheckMode = "检查模式仅支持 http / https / tcp";
  }

  const portError = validatePortField(form.port, "端口");
  if (portError) {
    errors.port = portError;
  }

  const healthIntervalError = validatePositiveIntegerField(form.healthInterval, "检查间隔");
  if (healthIntervalError) {
    errors.healthInterval = healthIntervalError;
  }

  if (form.ingressMode === "l7_shared") {
    if (!form.pathPrefix.trim()) {
      errors.pathPrefix = "l7_shared 模式下必须提供路径前缀";
    } else if (!form.pathPrefix.trim().startsWith("/")) {
      errors.pathPrefix = "路径前缀必须以 / 开头";
    }
  }

  if (form.ingressMode === "tls_sni_shared" && !form.sniName.trim()) {
    errors.sniName = "tls_sni_shared 模式下必须提供 SNI 名称";
  }

  if (form.ingressMode === "l4_dedicated_port") {
    if (!form.listenPort.trim()) {
      errors.listenPort = "l4_dedicated_port 模式下必须提供监听端口";
    } else {
      const listenPortError = validatePortField(form.listenPort, "监听端口");
      if (listenPortError) {
        errors.listenPort = listenPortError;
      }
    }
  } else if (form.listenPort.trim()) {
    const listenPortError = validatePortField(form.listenPort, "监听端口");
    if (listenPortError) {
      errors.listenPort = listenPortError;
    }
  }

  if ((form.healthCheckMode === "http" || form.healthCheckMode === "https") && !form.healthCheckPath.trim()) {
    errors.healthCheckPath = "HTTP/HTTPS 检查模式下必须提供健康路径";
  } else if (
    (form.healthCheckMode === "http" || form.healthCheckMode === "https") &&
    !form.healthCheckPath.trim().startsWith("/")
  ) {
    errors.healthCheckPath = "健康路径必须以 / 开头";
  }

  return errors;
}

export function toServiceForm(service: ServiceItem): ServiceFormState {
  const primaryEndpoint = service.endpoints[0];
  return {
    instanceID: service.instance_id || "",
    namespace: service.scope.namespace?.trim() || defaultServiceForm.namespace,
    environment: service.scope.environment?.trim() || defaultServiceForm.environment,
    serviceName: service.service_name?.trim() || "",
    protocol: service.protocol?.trim() || defaultServiceForm.protocol,
    host: primaryEndpoint?.host?.trim() || "",
    port: primaryEndpoint?.port ? String(primaryEndpoint.port) : "",
    ingressMode: service.exposure?.ingress_mode?.trim() || defaultServiceForm.ingressMode,
    ingressHost: service.exposure?.host?.trim() || "",
    pathPrefix: service.exposure?.path_prefix?.trim() || "",
    listenPort: service.exposure?.listen_port ? String(service.exposure.listen_port) : "",
    sniName: service.exposure?.sni_name?.trim() || service.sni_name?.trim() || primaryEndpoint?.sni_name?.trim() || "",
    healthCheckMode: service.health_check_mode?.trim() || defaultServiceForm.healthCheckMode,
    healthCheckPath: service.health_check_path?.trim() || "",
    healthInterval: service.health_check_interval_sec ? String(service.health_check_interval_sec) : defaultServiceForm.healthInterval,
    allowExport: service.exposure?.allow_export === true,
    notes: "",
  };
}

export function buildServicePayload(form: ServiceFormState): ServicePayload {
  return {
    instance_id: form.instanceID.trim(),
    scope: {
      namespace: form.namespace.trim(),
      environment: form.environment.trim(),
    },
    service_name: form.serviceName.trim(),
    protocol: form.protocol.trim(),
    host: form.host.trim(),
    port: Number(form.port),
    sni_name: form.sniName.trim(),
    exposure: {
      ingress_mode: form.ingressMode.trim(),
      host: form.ingressHost.trim(),
      listen_port: Number(form.listenPort || "0"),
      sni_name: form.sniName.trim(),
      path_prefix: form.pathPrefix.trim(),
      allow_export: form.allowExport,
    },
    health_check_interval_sec: Number(form.healthInterval || defaultServiceForm.healthInterval),
    health_check_mode: form.healthCheckMode.trim(),
    health_check_path: form.healthCheckPath.trim(),
    route_hint: {},
  };
}

function validatePortField(text: string, label: string): string | undefined {
  const normalized = text.trim();
  if (!/^\d+$/.test(normalized)) {
    return `${label}必须是 1-65535 的整数`;
  }
  const value = Number.parseInt(normalized, 10);
  if (!Number.isFinite(value) || value < 1 || value > 65535) {
    return `${label}必须介于 1 到 65535`;
  }
  return undefined;
}

function validatePositiveIntegerField(text: string, label: string): string | undefined {
  const normalized = text.trim();
  if (!/^\d+$/.test(normalized)) {
    return `${label}必须是正整数`;
  }
  const value = Number.parseInt(normalized, 10);
  if (!Number.isFinite(value) || value <= 0) {
    return `${label}必须大于 0`;
  }
  return undefined;
}
