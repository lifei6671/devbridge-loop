import { open } from "@tauri-apps/plugin-dialog";
import { CircleAlert, LoaderCircle } from "lucide-react";
import type { ReactElement } from "react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import {
  parseNumberList,
  parseStringList
} from "@/components/app/helpers";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type {
  DesktopConfigSaveRequest,
  DesktopConfigView
} from "@/types";

interface ConfigPageProps {
  agentHttpAddrValidationError: string | null;
  desktopConfig: DesktopConfigView | null;
  desktopConfigDraft: DesktopConfigSaveRequest | null;
  desktopConfigDraftDirty: boolean;
  masqueConfigVisible: boolean;
  masquePskVisible: boolean;
  onPatchDraft: (patch: Partial<DesktopConfigSaveRequest>) => void;
  onSaveConfig: () => void;
  savingConfig: boolean;
}

interface FieldLabelProps {
  text: string;
  tooltip?: string;
}

interface TooltipPosition {
  left: number;
  top: number;
}

function InfoTooltip({ text }: { text: string }): ReactElement {
  const triggerRef = useRef<HTMLSpanElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const [open, setOpen] = useState(false);
  const [positioned, setPositioned] = useState(false);
  const [position, setPosition] = useState<TooltipPosition>({ left: 0, top: 0 });

  const updatePosition = useCallback(() => {
    const trigger = triggerRef.current;
    const tooltip = tooltipRef.current;
    if (!trigger || !tooltip) {
      return;
    }

    const triggerRect = trigger.getBoundingClientRect();
    const tooltipRect = tooltip.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const gap = 8;
    const padding = 8;

    let nextLeft = triggerRect.left;
    if (nextLeft + tooltipRect.width + padding > viewportWidth) {
      nextLeft = viewportWidth - tooltipRect.width - padding;
    }
    if (nextLeft < padding) {
      nextLeft = padding;
    }

    let nextTop = triggerRect.bottom + gap;
    if (nextTop + tooltipRect.height + padding > viewportHeight) {
      const aboveTop = triggerRect.top - tooltipRect.height - gap;
      if (aboveTop >= padding) {
        nextTop = aboveTop;
      } else {
        nextTop = Math.max(padding, viewportHeight - tooltipRect.height - padding);
      }
    }

    setPosition({ left: nextLeft, top: nextTop });
    setPositioned(true);
  }, []);

  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    updatePosition();
  }, [open, updatePosition]);

  useEffect(() => {
    if (!open) {
      return;
    }

    const onViewportChanged = () => {
      updatePosition();
    };
    window.addEventListener("resize", onViewportChanged);
    window.addEventListener("scroll", onViewportChanged, true);
    return () => {
      window.removeEventListener("resize", onViewportChanged);
      window.removeEventListener("scroll", onViewportChanged, true);
    };
  }, [open, updatePosition]);

  useEffect(() => {
    if (!open) {
      setPositioned(false);
    }
  }, [open]);

  return (
    <>
      <span
        ref={triggerRef}
        className="inline-flex cursor-help items-center"
        tabIndex={0}
        aria-label={text}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setOpen(false);
          }
        }}
      >
        <CircleAlert className="h-3.5 w-3.5 text-amber-700/90" />
      </span>
      {open && typeof document !== "undefined"
        ? createPortal(
            <span
              ref={tooltipRef}
              role="tooltip"
              className="pointer-events-none fixed z-[1000] rounded-md border border-slate-700 bg-slate-900/95 px-2 py-1 text-[11px] leading-4 text-slate-100 shadow-lg"
              style={{
                left: position.left,
                top: position.top,
                maxWidth: "min(20rem, calc(100vw - 1rem))",
                visibility: positioned ? "visible" : "hidden"
              }}
            >
              {text}
            </span>,
            document.body
          )
        : null}
    </>
  );
}

function FieldLabel({ text, tooltip }: FieldLabelProps): ReactElement {
  return (
    <div className="flex items-center gap-1 text-xs text-muted-foreground">
      <span>{text}</span>
      {tooltip ? <InfoTooltip text={tooltip} /> : null}
    </div>
  );
}

export function ConfigPage({
  agentHttpAddrValidationError,
  desktopConfig,
  desktopConfigDraft,
  desktopConfigDraftDirty,
  masqueConfigVisible,
  masquePskVisible,
  onPatchDraft,
  onSaveConfig,
  savingConfig
}: ConfigPageProps): ReactElement {
  const pickAgentBinary = async (): Promise<void> => {
    try {
      const selected = await open({
        multiple: false,
        directory: false
      });
      if (!selected) {
        return;
      }

      const resolved = Array.isArray(selected) ? selected[0] : selected;
      if (typeof resolved === "string" && resolved.trim() !== "") {
        onPatchDraft({ agentBinary: resolved });
      }
    } catch (error) {
      console.error("pick agent binary failed", error);
    }
  };

  const pickAgentCoreDir = async (): Promise<void> => {
    try {
      const selected = await open({
        multiple: false,
        directory: true
      });
      if (!selected) {
        return;
      }

      const resolved = Array.isArray(selected) ? selected[0] : selected;
      if (typeof resolved === "string" && resolved.trim() !== "") {
        onPatchDraft({ agentCoreDir: resolved });
      }
    } catch (error) {
      console.error("pick agent core dir failed", error);
    }
  };

  return (
    <section className="grid gap-4 xl:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>桌面端信息</CardTitle>
          <CardDescription>本地配置目录与配置文件状态</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div>配置已加载: {desktopConfig?.configLoaded ? "是" : "否"}</div>
          <div>核心监听地址: {desktopConfig?.agentHttpAddr ?? "-"}</div>
          <div>核心 API 地址: {desktopConfig?.agentApiBase ?? "-"}</div>
          <div>默认环境: {desktopConfig?.envName ?? "-"}</div>
          <div>配置目录: {desktopConfig?.configDir ?? "-"}</div>
          <div>日志目录: {desktopConfig?.logDir ?? "-"}</div>
          <div>配置文件: {desktopConfig?.configFile ?? "-"}</div>
          <div className="h-px w-full bg-border/70" />
          <div>平台: {desktopConfig?.platform ?? "-"}</div>
          <div>架构: {desktopConfig?.arch ?? "-"}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>配置编辑器</CardTitle>
          <CardDescription>基础配置加载与保存（保存后重启核心进程生效）</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {desktopConfigDraftDirty ? (
            <p className="text-xs text-amber-900">
              检测到未保存的配置修改，后台轮询不会覆盖当前表单。
            </p>
          ) : null}

          <div className="space-y-1">
            <FieldLabel
              text="核心监听地址（agentHttpAddr）"
              tooltip="用于 agent-core 服务端监听（bind）。0.0.0.0 表示监听所有网卡，便于局域网接入。"
            />
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentHttpAddr ?? ""}
              onChange={(event) => onPatchDraft({ agentHttpAddr: event.target.value })}
            />
            {agentHttpAddrValidationError ? (
              <p className="text-xs text-amber-900">{agentHttpAddrValidationError}</p>
            ) : null}
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="核心 API 地址（agentApiBase）"
              tooltip="用于桌面端访问 agent-core。通常建议填 http://127.0.0.1:端口，不要把 0.0.0.0 当客户端目标地址使用。"
            />
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentApiBase ?? ""}
              onChange={(event) => onPatchDraft({ agentApiBase: event.target.value })}
            />
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="默认环境（envName）"
              tooltip="写入 DEVLOOP_ENV_NAME。注册时未指定 env、或按环境解析顺序命中 runtimeDefault 时会使用该值。"
            />
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.envName ?? ""}
              placeholder="dev-default"
              onChange={(event) => onPatchDraft({ envName: event.target.value })}
            />
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="核心可执行文件（agentBinary）"
              tooltip="高级启动项。仅在需要指定自定义核心二进制路径时填写；留空会自动探测。"
            />
            <div className="flex items-center gap-2">
              <input
                className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                placeholder="留空自动探测（Windows 优先同目录 agent-core.exe）"
                value={desktopConfigDraft?.agentBinary ?? ""}
                onChange={(event) =>
                  onPatchDraft({
                    agentBinary: event.target.value.trim() === "" ? null : event.target.value
                  })
                }
              />
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => {
                  void pickAgentBinary();
                }}
              >
                选择文件
              </Button>
            </div>
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="核心工作目录（agentCoreDir）"
              tooltip="高级启动项。仅在 go run ./cmd/agent-core 启动方式下需要指定工作目录；留空会自动推断。"
            />
            <div className="flex items-center gap-2">
              <input
                className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                placeholder="留空自动推断（开发态默认 ../../agent-core）"
                value={desktopConfigDraft?.agentCoreDir ?? ""}
                onChange={(event) =>
                  onPatchDraft({
                    agentCoreDir: event.target.value.trim() === "" ? null : event.target.value
                  })
                }
              />
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => {
                  void pickAgentCoreDir();
                }}
              >
                选择目录
              </Button>
            </div>
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">核心重启退避（毫秒，CSV）</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.agentRestartBackoffMs ?? []).join(",")}
              onChange={(event) =>
                onPatchDraft({
                  agentRestartBackoffMs: parseNumberList(event.target.value)
                })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">环境解析顺序（CSV）</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.envResolveOrder ?? []).join(",")}
              onChange={(event) =>
                onPatchDraft({
                  envResolveOrder: parseStringList(event.target.value)
                })
              }
            />
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="桥接服务地址（tunnelBridgeAddress）"
              tooltip="Agent 与 cloud-bridge 的上行同步地址（注册同步、接管同步）。"
            />
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBridgeAddress ?? ""}
              onChange={(event) =>
                onPatchDraft({ tunnelBridgeAddress: event.target.value })
              }
            />
          </div>

          <div className="space-y-1">
            <FieldLabel
              text="回流地址（tunnelBackflowBaseUrl）"
              tooltip="bridge 回调 agent 的入口地址。默认通常与 agentApiBase 一致，但在 NAT、容器或反向代理场景可单独配置。"
            />
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBackflowBaseUrl ?? ""}
              onChange={(event) =>
                onPatchDraft({ tunnelBackflowBaseUrl: event.target.value })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">隧道协议（tunnelSyncProtocol）</label>
            <select
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelSyncProtocol ?? "http"}
              onChange={(event) =>
                onPatchDraft({ tunnelSyncProtocol: event.target.value })
              }
            >
              <option value="http">HTTP（兼容模式）</option>
              <option value="masque">MASQUE（高性能）</option>
            </select>
          </div>

          {masqueConfigVisible ? (
            <>
              <div className="space-y-1">
                <label className="text-xs text-muted-foreground">MASQUE 鉴权模式</label>
                <select
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  value={desktopConfigDraft?.tunnelMasqueAuthMode ?? "psk"}
                  onChange={(event) =>
                    onPatchDraft({
                      tunnelMasqueAuthMode: event.target.value
                    })
                  }
                >
                  <option value="psk">PSK（预共享密钥）</option>
                  <option value="ecdh">ECDH（密钥协商）</option>
                </select>
              </div>

              {masquePskVisible ? (
                <div className="space-y-1">
                  <label className="text-xs text-muted-foreground">MASQUE 预共享密钥（PSK）</label>
                  <input
                    className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                    value={desktopConfigDraft?.tunnelMasquePsk ?? ""}
                    onChange={(event) =>
                      onPatchDraft({
                        tunnelMasquePsk: event.target.value
                      })
                    }
                  />
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">
                  当前为 ECDH 模式，`tunnelMasquePsk` 不参与鉴权。
                </p>
              )}

              <div className="space-y-1">
                <label className="text-xs text-muted-foreground">MASQUE 代理模板地址</label>
                <input
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  placeholder="留空按 tunnelBridgeAddress 自动推导"
                  value={desktopConfigDraft?.tunnelMasqueProxyUrl ?? ""}
                  onChange={(event) =>
                    onPatchDraft({
                      tunnelMasqueProxyUrl: event.target.value
                    })
                  }
                />
              </div>

              <div className="space-y-1">
                <FieldLabel
                  text="MASQUE 目标地址（bridge 的 MASQUE UDP 入口）"
                  tooltip="该地址是 MASQUE 隧道的目标端点，不一定等于 bridge HTTP 地址，端口也可以不同。"
                />
                <input
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  value={desktopConfigDraft?.tunnelMasqueTargetAddr ?? ""}
                  onChange={(event) =>
                    onPatchDraft({
                      tunnelMasqueTargetAddr: event.target.value
                    })
                  }
                />
              </div>
            </>
          ) : null}

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.agentAutoRestart ?? true}
              onChange={(event) =>
                onPatchDraft({ agentAutoRestart: event.target.checked })
              }
            />
            自动拉起核心进程
          </label>

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.closeToTrayOnClose ?? true}
              onChange={(event) =>
                onPatchDraft({ closeToTrayOnClose: event.target.checked })
              }
            />
            关闭窗口时最小化到托盘
          </label>

          <p className="text-xs text-muted-foreground">
            勾选后点击关闭按钮会直接最小化到托盘；不勾选则每次弹窗确认“退出/最小化”。
          </p>

          <Button
            variant="outline"
            size="sm"
            disabled={savingConfig || !desktopConfigDraft || Boolean(agentHttpAddrValidationError)}
            onClick={onSaveConfig}
          >
            <LoaderCircle className={cn("mr-2 h-4 w-4", savingConfig && "animate-spin")} />
            {savingConfig ? "保存中..." : "保存配置"}
          </Button>
        </CardContent>
      </Card>
    </section>
  );
}
