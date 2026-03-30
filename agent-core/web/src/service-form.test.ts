import { describe, expect, it } from "vitest";

import { buildServicePayload, defaultServiceForm, toServiceForm, validateServiceForm } from "@/service-form";

describe("service form helpers", () => {
  it("requires key identity and endpoint fields", () => {
    const errors = validateServiceForm({
      ...defaultServiceForm,
      namespace: "",
      environment: "",
      serviceName: "",
      host: "",
      port: "0",
    });

    expect(errors.namespace).toContain("不能为空");
    expect(errors.environment).toContain("不能为空");
    expect(errors.serviceName).toContain("不能为空");
    expect(errors.host).toContain("不能为空");
    expect(errors.port).toContain("1 到 65535");
  });

  it("validates ingress-mode-specific fields", () => {
    const l7Errors = validateServiceForm({
      ...defaultServiceForm,
      ingressMode: "l7_shared",
      pathPrefix: "api/orders",
    });
    const tlsErrors = validateServiceForm({
      ...defaultServiceForm,
      ingressMode: "tls_sni_shared",
      sniName: "",
    });
    const l4Errors = validateServiceForm({
      ...defaultServiceForm,
      ingressMode: "l4_dedicated_port",
      listenPort: "",
    });

    expect(l7Errors.pathPrefix).toContain("/ 开头");
    expect(tlsErrors.sniName).toContain("SNI");
    expect(l4Errors.listenPort).toContain("监听端口");
  });

  it("accepts a complete valid service form", () => {
    const errors = validateServiceForm({
      ...defaultServiceForm,
      serviceName: "orders",
      ingressMode: "l7_shared",
      pathPrefix: "/api/orders",
      healthCheckMode: "http",
      healthCheckPath: "/healthz",
      healthInterval: "15",
    });

    expect(errors).toEqual({});
  });

  it("maps a service item back into editable form fields", () => {
    const form = toServiceForm({
      logical_service_id: "svc_orders",
      instance_id: "orders-01",
      scope: {
        namespace: "default",
        environment: "prod",
      },
      service_name: "orders",
      protocol: "http",
      exposure: {
        ingress_mode: "l7_shared",
        host: "api.internal.example",
        path_prefix: "/api/orders",
        allow_export: true,
      },
      health_check_mode: "http",
      health_check_interval_sec: 15,
      health_check_path: "/healthz",
      status: "active",
      endpoints: [
        {
          endpoint_id: "ep-orders",
          protocol: "http",
          host: "127.0.0.1",
          port: 8080,
        },
      ],
      endpoint_count: 1,
      updated_at_ms: Date.now(),
    });

    expect(form.instanceID).toBe("orders-01");
    expect(form.namespace).toBe("default");
    expect(form.environment).toBe("prod");
    expect(form.serviceName).toBe("orders");
    expect(form.protocol).toBe("http");
    expect(form.host).toBe("127.0.0.1");
    expect(form.port).toBe("8080");
    expect(form.ingressMode).toBe("l7_shared");
    expect(form.ingressHost).toBe("api.internal.example");
    expect(form.pathPrefix).toBe("/api/orders");
    expect(form.healthCheckMode).toBe("http");
    expect(form.healthCheckPath).toBe("/healthz");
    expect(form.healthInterval).toBe("15");
    expect(form.allowExport).toBe(true);
  });

  it("builds the exact service payload from editable form state", () => {
    const payload = buildServicePayload({
      ...defaultServiceForm,
      instanceID: "orders-01",
      namespace: "default",
      environment: "prod",
      serviceName: "orders",
      protocol: "https",
      host: "127.0.0.1",
      port: "8443",
      ingressMode: "tls_sni_shared",
      ingressHost: "orders.example.com",
      sniName: "orders.example.com",
      healthCheckMode: "https",
      healthCheckPath: "/healthz",
      healthInterval: "20",
      allowExport: true,
    });

    expect(payload).toEqual({
      instance_id: "orders-01",
      scope: {
        namespace: "default",
        environment: "prod",
      },
      service_name: "orders",
      protocol: "https",
      host: "127.0.0.1",
      port: 8443,
      sni_name: "orders.example.com",
      exposure: {
        ingress_mode: "tls_sni_shared",
        host: "orders.example.com",
        listen_port: 0,
        sni_name: "orders.example.com",
        path_prefix: "",
        allow_export: true,
      },
      health_check_interval_sec: 20,
      health_check_mode: "https",
      health_check_path: "/healthz",
      route_hint: {},
    });
  });
});
