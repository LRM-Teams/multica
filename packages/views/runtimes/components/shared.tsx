import { Cloud, Monitor, Wifi, WifiHigh, WifiOff } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import type { RuntimeHealth, RuntimeHealthPresentation } from "@multica/core/runtimes";
import { ProviderLogo } from "./provider-logo";
import { useT } from "../../i18n/use-t";

/**
 * Computers list leading glyph: Monitor for a local/remote machine, Cloud for
 * a cloud computer. One-glance local-vs-cloud distinction on the Computers page.
 */
export function ComputerIcon({
  kind,
  className,
}: {
  /** `cloud` → cloud computer; anything else → regular computer (Monitor). */
  kind: "local" | "cloud" | string;
  className?: string;
}) {
  const isCloud = kind === "cloud";
  const Icon = isCloud ? Cloud : Monitor;
  return (
    <Icon
      className={cn("h-3.5 w-3.5", className)}
      data-testid={isCloud ? "computer-icon-cloud" : "computer-icon-local"}
      aria-hidden
    />
  );
}

// Compact provider tag: small logo square + provider name. Used in dense
// list rows to identify which CLI / model provider a runtime is wired to.
export function ProviderChip({ provider }: { provider: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border bg-muted/40 px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
      <ProviderLogo provider={provider} className="h-3 w-3" />
      <span className="capitalize">{provider}</span>
    </span>
  );
}

// Maps each derived 4-state runtime health to a semantic colour class.
// `dot`  — bare dot fill (sidebar rows, connectivity status).
// `tone` — badge bg+text (HealthBadge / RuntimeHealthStateBadge).
// `text` — solid text tone for the borderless connectivity status
//          (LRM-624 Plan A: title row shows connectivity once, no chip wall).
// Labels flow through useT — see useHealthLabel below.
const HEALTH_VISUAL: Record<
  RuntimeHealth,
  { dot: string; tone: string; text: string }
> = {
  online: {
    dot: "bg-success",
    tone: "bg-success/10 text-success-strong",
    text: "text-success",
  },
  recently_lost: {
    dot: "bg-warning",
    tone: "bg-warning/10 text-warning",
    text: "text-warning",
  },
  offline: {
    dot: "bg-muted-foreground/40",
    tone: "bg-muted text-muted-foreground",
    text: "text-muted-foreground",
  },
  about_to_gc: {
    dot: "bg-destructive",
    tone: "bg-destructive/10 text-destructive",
    text: "text-destructive",
  },
};

export function HealthDot({
  health,
  className = "",
}: {
  health: RuntimeHealth | "loading";
  className?: string;
}) {
  if (health === "loading") {
    return (
      <span
        className={`inline-block h-2 w-2 rounded-full bg-muted ${className}`}
      />
    );
  }
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full ${HEALTH_VISUAL[health].dot} ${className}`}
    />
  );
}

// Wifi-style runtime health indicator. The icon shape carries the rough
// state ("can it talk to us?") and the colour carries severity. Used
// wherever a richer signal than the bare dot is appropriate (agent
// hover-card runtime row, runtime list health column).
//
//   online        → Wifi (full bars, success)
//   recently_lost → WifiHigh (fewer bars, warning) — transient hiccup
//   offline       → WifiOff (slashed, muted) — long unreachable
//   about_to_gc   → WifiOff (slashed, destructive) — sweeper coming
const HEALTH_ICON: Record<
  RuntimeHealth,
  { Icon: typeof Wifi; tone: string }
> = {
  online: { Icon: Wifi, tone: "text-success" },
  recently_lost: { Icon: WifiHigh, tone: "text-warning" },
  offline: { Icon: WifiOff, tone: "text-muted-foreground" },
  about_to_gc: { Icon: WifiOff, tone: "text-destructive" },
};

export function HealthIcon({
  health,
  className = "h-3 w-3",
}: {
  health: RuntimeHealth | "loading";
  className?: string;
}) {
  if (health === "loading") {
    return <Wifi className={`${className} text-muted-foreground/40`} />;
  }
  const { Icon, tone } = HEALTH_ICON[health];
  return <Icon className={`${className} ${tone}`} />;
}

// English-only fallback. Pure function form for non-component callers
// (e.g. column factory builders). Translated call sites should use the
// `useHealthLabel` hook below instead.
const HEALTH_LABEL_EN: Record<RuntimeHealth, string> = {
  online: "Online",
  recently_lost: "Recently lost",
  offline: "Offline",
  about_to_gc: "About to GC",
};

export function healthLabel(health: RuntimeHealth | "loading"): string {
  if (health === "loading") return "—";
  return HEALTH_LABEL_EN[health];
}

// Hook form: usable inside React components (preferred for new call sites
// that aren't running in non-component contexts).
export function useHealthLabel(): (health: RuntimeHealth | "loading") => string {
  const { t } = useT("runtimes");
  return (health) => {
    if (health === "loading") return "—";
    return t(($) => $.health[health].label);
  };
}

/**
 * Machine-level connectivity only: Connected / Disconnected, using the same
 * agents.inspector copy as ComputerInfoRow (Frank/Iris 2026-08-02). Binary —
 * anything other than online reads as disconnected. Do not pair with a
 * secondary runtimeHealth badge on the same line.
 */
export function MachineConnectedStatus({
  health,
  className,
}: {
  health: RuntimeHealth | "loading";
  className?: string;
}) {
  const { t } = useT("agents");
  if (health === "loading") {
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              className={cn(
                "inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground",
                className,
              )}
            />
          }
        >
          <span className="h-2 w-2 rounded-full bg-muted-foreground/40" />
          —
        </TooltipTrigger>
        <TooltipContent side="top">—</TooltipContent>
      </Tooltip>
    );
  }
  const connected = health === "online";
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={cn(
              "inline-flex items-center gap-1.5 text-xs font-medium",
              connected ? "text-success" : "text-muted-foreground",
              className,
            )}
          />
        }
      >
        <span
          className={cn(
            "h-2 w-2 rounded-full",
            connected ? "bg-success" : "bg-muted-foreground/40",
          )}
        />
        {connected
          ? t(($) => $.inspector.computer_connected)
          : t(($) => $.inspector.computer_disconnected)}
      </TooltipTrigger>
      <TooltipContent side="top">
        {connected
          ? t(($) => $.inspector.computer_connected)
          : t(($) => $.inspector.computer_disconnected)}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * Borderless single-point connectivity status: a coloured dot + label, no
 * bordered chip / wifi-icon wall. Plan A (LRM-624) for the machine-detail
 * title row — connectivity is expressed exactly once. The secondary
 * runtimeHealth badge alongside it is reserved for incremental update
 * states only (see `headerRuntimeHealthBadge`), never a second "Offline".
 *
 * Prefer {@link MachineConnectedStatus} for machine-level surfaces (computers
 * page) — that one uses Connected/Disconnected vocabulary. This component
 * keeps Online/Offline for per-runtime health rows.
 */
export function RuntimeConnectivityStatus({
  health,
  className,
}: {
  health: RuntimeHealth | "loading";
  className?: string;
}) {
  const labelOf = useHealthLabel();
  if (health === "loading") {
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              className={cn(
                "inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground",
                className,
              )}
            />
          }
        >
          <span className="h-2 w-2 rounded-full bg-muted-foreground/40" />
          —
        </TooltipTrigger>
        <TooltipContent side="top">—</TooltipContent>
      </Tooltip>
    );
  }
  const v = HEALTH_VISUAL[health];
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={cn(
              "inline-flex items-center gap-1.5 text-xs font-medium",
              v.text,
              className,
            )}
          />
        }
      >
        <span className={cn("h-2 w-2 rounded-full", v.dot)} />
        {labelOf(health)}
      </TooltipTrigger>
      <TooltipContent side="top">{labelOf(health)}</TooltipContent>
    </Tooltip>
  );
}

export function HealthBadge({
  health,
}: {
  health: RuntimeHealth | "loading";
}) {
  const labelOf = useHealthLabel();
  if (health === "loading") {
    return (
      <Badge variant="secondary" className="bg-muted text-muted-foreground">
        —
      </Badge>
    );
  }
  const v = HEALTH_VISUAL[health];
  return (
    <Badge variant="secondary" className={v.tone}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dot}`} />
      {labelOf(health)}
    </Badge>
  );
}

const RUNTIME_HEALTH_STATE_VISUAL: Record<
  RuntimeHealthPresentation,
  { dot: string; text: string; tone: string }
> = {
  ok: { dot: "bg-success", text: "text-success-strong", tone: "bg-success/10 text-success-strong" },
  update_available: { dot: "bg-warning", text: "text-warning", tone: "bg-warning/10 text-warning" },
  // Staged: downloaded, applies when idle — brand tone like "updating", since the
  // work is effectively done and just waiting, not a pending user action.
  ready_to_apply: { dot: "bg-brand", text: "text-brand", tone: "bg-brand/10 text-brand" },
  updating: { dot: "bg-brand", text: "text-brand", tone: "bg-brand/10 text-brand" },
  failed: { dot: "bg-destructive", text: "text-destructive", tone: "bg-destructive/10 text-destructive" },
  offline: { dot: "bg-muted-foreground/40", text: "text-muted-foreground", tone: "bg-muted text-muted-foreground" },
};

export function useRuntimeHealthStateLabel(): (
  health: RuntimeHealthPresentation,
) => string {
  const { t } = useT("runtimes");
  return (health) => t(($) => $.runtime_health[health]);
}

export function RuntimeHealthStateBadge({
  health,
}: {
  health: RuntimeHealthPresentation;
}) {
  const labelOf = useRuntimeHealthStateLabel();
  const v = RUNTIME_HEALTH_STATE_VISUAL[health];
  return (
    <Badge variant="secondary" className={v.tone}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dot}`} />
      {labelOf(health)}
    </Badge>
  );
}

/**
 * Borderless small-text sibling of `RuntimeHealthStateBadge`, for placing
 * next to the version number instead of competing with the connectivity
 * dot+text for primary visual weight (Iris 07-31: "不跟连通性文字并排抢视觉
 * 权重——放在版本号旁边，弱化成小字级"). Only ever rendered for a real
 * update/issue state — callers already gate on `headerRuntimeHealthBadge`
 * returning non-null, so this never sits as an empty placeholder.
 */
export function RuntimeHealthStateInline({
  health,
}: {
  health: RuntimeHealthPresentation;
}) {
  const labelOf = useRuntimeHealthStateLabel();
  const v = RUNTIME_HEALTH_STATE_VISUAL[health];
  return (
    <span className={`inline-flex items-center gap-1 text-[11px] font-medium ${v.text}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dot}`} />
      {labelOf(health)}
    </span>
  );
}

export function InfoField({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div
        className={`mt-0.5 text-sm truncate ${mono ? "font-mono text-xs" : ""}`}
      >
        {value}
      </div>
    </div>
  );
}

export function TokenCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 text-sm font-semibold tabular-nums">{value}</div>
    </div>
  );
}

// KPI tile used in the Runtime detail "story numbers" row. The big number
// is the visual anchor of the whole left column — sized large enough that
// it dominates over the chart hierarchy below it. Label sits as a small
// caps eyebrow; hint is a thin caption beneath the number for deltas /
// ratios / savings context.
export function KpiCard({
  label,
  value,
  hint,
  accent,
}: {
  label: string;
  value: string;
  hint?: React.ReactNode;
  accent?: "brand" | "success" | "default";
}) {
  const valueClass =
    accent === "brand"
      ? "text-brand"
      : accent === "success"
        ? "text-success"
        : "";
  return (
    <div className="flex flex-col gap-2 p-5">
      <div className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className={`text-3xl font-semibold leading-none tabular-nums ${valueClass}`}>
        {value}
      </div>
      {hint != null && (
        <div className="text-xs text-muted-foreground">{hint}</div>
      )}
    </div>
  );
}
