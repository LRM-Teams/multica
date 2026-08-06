import {
  Activity,
  Check,
  CircleDashed,
  CircleOff,
  Clock3,
  ListTodo,
  PauseCircle,
  RefreshCw,
  TriangleAlert,
  X,
  type LucideIcon,
} from "lucide-react";

/**
 * Execution overlay state model (LRM-1473 / LRM-1479, contract PR #2415).
 *
 * States are derived strictly from the authoritative Projection (Presence
 * contract v2 + `ResearchRunSnapshot` attempts/tasks/results). No state is
 * inferred from chat summaries, animations or captions. A row only shows
 * `running` when the projection carries an unexpired running signal; a row
 * only shows `queued` when the presence phase is `queued` (task assigned but
 * not yet runtime-started); `cancelling` only when the attempt ledger shows an
 * in-flight cancellation before a terminal state.
 */
export type ExecutionStatus =
  | "queued"
  | "running"
  | "cancelling"
  | "done"
  | "failed"
  | "retrying"
  | "stale"
  | "idle"
  | "offline"
  | "unknown";

export type ExecutionActionKey =
  | "waiting"
  | "working"
  | "cancelling"
  | "recent_done"
  | "recent_failed"
  | "retrying"
  | "stale"
  | "idle"
  | "offline"
  | "unknown";

export type StatusPresentation = {
  /** i18n key suffix within `panel.execution.status.*`. */
  labelKey: string;
  Icon: LucideIcon;
  badgeClass: string;
  markerClass: string;
  /** Colored status text (third channel besides icon + badge color). */
  textClass: string;
};

export const EXECUTION_STATUS_PRESENTATION: Record<
  ExecutionStatus,
  StatusPresentation
> = {
  queued: {
    labelKey: "queued",
    Icon: ListTodo,
    badgeClass: "bg-muted text-muted-foreground",
    markerClass: "border-dashed border-muted-foreground/55 text-muted-foreground",
    textClass: "text-muted-foreground",
  },
  running: {
    labelKey: "running",
    Icon: Activity,
    badgeClass: "bg-brand/10 text-brand",
    markerClass: "border-brand/35 bg-brand/10 text-brand",
    textClass: "text-brand",
  },
  cancelling: {
    labelKey: "cancelling",
    Icon: PauseCircle,
    badgeClass: "bg-warning/10 text-warning",
    markerClass: "border-warning/35 bg-warning/10 text-warning",
    textClass: "text-warning",
  },
  done: {
    labelKey: "done",
    Icon: Check,
    badgeClass: "bg-success/10 text-success-strong",
    markerClass: "border-success/30 bg-success/10 text-success-strong",
    textClass: "text-success-strong",
  },
  failed: {
    labelKey: "failed",
    Icon: X,
    badgeClass: "bg-destructive/10 text-destructive-strong",
    markerClass: "border-destructive/30 bg-destructive/10 text-destructive-strong",
    textClass: "text-destructive-strong",
  },
  retrying: {
    labelKey: "retrying",
    Icon: RefreshCw,
    badgeClass: "bg-warning/10 text-warning",
    markerClass: "border-warning/35 bg-warning/10 text-warning",
    textClass: "text-warning",
  },
  stale: {
    labelKey: "stale",
    Icon: TriangleAlert,
    badgeClass: "bg-warning/10 text-warning",
    markerClass: "border-warning/35 bg-warning/10 text-warning",
    textClass: "text-warning",
  },
  idle: {
    labelKey: "idle",
    Icon: Clock3,
    badgeClass: "bg-muted text-muted-foreground",
    markerClass: "border-muted-foreground/40 text-muted-foreground",
    textClass: "text-muted-foreground",
  },
  offline: {
    labelKey: "offline",
    Icon: CircleOff,
    badgeClass: "bg-muted text-muted-foreground",
    markerClass: "border-muted-foreground/40 text-muted-foreground",
    textClass: "text-muted-foreground",
  },
  unknown: {
    labelKey: "unknown",
    Icon: CircleDashed,
    badgeClass: "bg-muted text-muted-foreground",
    markerClass: "border-dashed border-foreground/30 text-foreground/60",
    textClass: "text-foreground/60",
  },
};

export const EXECUTION_STATUS_ACTION_KEY: Record<
  ExecutionStatus,
  ExecutionActionKey
> = {
  queued: "waiting",
  running: "working",
  cancelling: "cancelling",
  done: "recent_done",
  failed: "recent_failed",
  retrying: "retrying",
  stale: "stale",
  idle: "idle",
  offline: "offline",
  unknown: "unknown",
};

/**
 * Deck priority for display ordering (agent-execution-spec §3).
 * Lower value sorts first; `blocking failed` rises above everything.
 */
export const EXECUTION_DECK_PRIORITY: Record<ExecutionStatus, number> = {
  failed: 0,
  cancelling: 1,
  running: 2,
  queued: 3,
  retrying: 4,
  stale: 5,
  idle: 6,
  done: 7,
  offline: 8,
  unknown: 9,
};
