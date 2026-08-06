import {
  Activity,
  Check,
  CircleDashed,
  CircleOff,
  Clock3,
  RefreshCw,
  TriangleAlert,
  X,
  type LucideIcon,
} from "lucide-react";

/**
 * Execution overlay 8-state model (LRM-1473 / LRM-1479).
 *
 * Derived strictly from the backend Projection (Presence contract v2 +
 * run snapshot tasks/attempts/results). No state is inferred from chat
 * summaries, animations or captions; a row only shows `running` when the
 * projection carries an unexpired running signal.
 */
export type ExecutionStatus =
  | "running"
  | "waiting"
  | "done"
  | "failed"
  | "retrying"
  | "stale"
  | "offline"
  | "unknown";

export type ExecutionActionKey =
  | "waiting"
  | "working"
  | "recent_done"
  | "recent_failed"
  | "retrying"
  | "stale"
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
  running: {
    labelKey: "running",
    Icon: Activity,
    badgeClass: "bg-brand/10 text-brand",
    markerClass: "border-brand/35 bg-brand/10 text-brand",
    textClass: "text-brand",
  },
  waiting: {
    labelKey: "waiting",
    Icon: Clock3,
    badgeClass: "bg-muted text-muted-foreground",
    markerClass: "border-dashed border-muted-foreground/55 text-muted-foreground",
    textClass: "text-muted-foreground",
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
  running: "working",
  waiting: "waiting",
  done: "recent_done",
  failed: "recent_failed",
  retrying: "retrying",
  stale: "stale",
  offline: "offline",
  unknown: "unknown",
};
