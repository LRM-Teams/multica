"use client";

import { useId, useState } from "react";
import {
  Activity,
  Check,
  ChevronDown,
  CircleDashed,
  Clock3,
  LocateFixed,
  Pause,
  RefreshCw,
  TriangleAlert,
  X,
  type LucideIcon,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";
import { cn } from "@multica/ui/lib/utils";
import type {
  ResearchExecutionActionKey,
  ResearchExecutionAgent,
  ResearchExecutionStatus,
  ResearchExecutionTimeKey,
} from "../lib/research-execution-panel-fixture";

type StatusPresentation = {
  labelKey: string;
  Icon: LucideIcon;
  badgeClass: string;
  markerClass: string;
};

const STATUS_PRESENTATION: Record<ResearchExecutionStatus, StatusPresentation> = {
  queued: { labelKey: "queued", Icon: Clock3, badgeClass: "bg-muted text-muted-foreground", markerClass: "border-dashed border-muted-foreground/55 text-muted-foreground" },
  running: { labelKey: "running", Icon: Activity, badgeClass: "bg-brand/10 text-brand", markerClass: "border-brand/35 bg-brand/10 text-brand" },
  done: { labelKey: "done", Icon: Check, badgeClass: "bg-success/10 text-success-strong", markerClass: "border-success/30 bg-success/10 text-success-strong" },
  failed: { labelKey: "failed", Icon: X, badgeClass: "bg-destructive/10 text-destructive-strong", markerClass: "border-destructive/30 bg-destructive/10 text-destructive-strong" },
  stale: { labelKey: "stale", Icon: TriangleAlert, badgeClass: "bg-warning/10 text-warning", markerClass: "border-warning/35 bg-warning/10 text-warning" },
  idle: { labelKey: "idle", Icon: Pause, badgeClass: "bg-muted text-muted-foreground", markerClass: "border-muted-foreground/35 text-muted-foreground" },
};

function AgentAvatar({ agent }: { agent: ResearchExecutionAgent }) {
  return agent.avatarUrl ? (
    <img src={agent.avatarUrl} alt="" className="size-8 shrink-0 rounded-lg object-cover" />
  ) : (
    <span aria-hidden="true" className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-[11px] font-semibold text-foreground">
      {agent.initials}
    </span>
  );
}

type ExecutionCopy = {
  locatable: string;
  locate: string;
  unavailable: string;
  status: Record<ResearchExecutionStatus, string>;
  action: Record<ResearchExecutionActionKey, string>;
  time: Record<ResearchExecutionTimeKey, string>;
  failedReason: string;
  locateAria: (name: string) => string;
  viewAria: (name: string) => string;
};

function ExecutionRow({ agent, onLocate, copy }: {
  agent: ResearchExecutionAgent;
  onLocate?: (agent: ResearchExecutionAgent) => void;
  copy: ExecutionCopy;
}) {
  const [expanded, setExpanded] = useState(false);
  const detailId = useId();
  const presentation = STATUS_PRESENTATION[agent.status];
  const StatusIcon = presentation.Icon;
  const canLocate = Boolean(agent.currentNodeId && onLocate);
  const actionText = agent.action ?? copy.action[agent.actionKey ?? fallbackActionKey(agent.status)];
  const timeText = copy.time[agent.timeKey];
  const failureReasonText = agent.failureReasonKey ? copy.failedReason : undefined;
  const activate = () => {
    if (canLocate) onLocate?.(agent);
    else setExpanded(true);
  };

  return (
    <article data-testid="research-execution-row" data-status={agent.status} className="relative min-w-0 overflow-hidden border-b border-border/60 last:border-b-0">
      {agent.status === "running" ? (
        <span aria-hidden="true" className="absolute inset-x-0 top-0 h-px overflow-hidden bg-brand/15">
          <span className="block h-full w-1/2 bg-brand animate-nav-progress-sweep motion-reduce:hidden" />
        </span>
      ) : null}
      <button
        type="button"
        className="grid w-full min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-2 px-3 py-2.5 text-left outline-none hover:bg-muted/25 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-brand"
        aria-label={canLocate ? copy.locateAria(agent.name) : copy.viewAria(agent.name)}
        aria-expanded={expanded}
        aria-controls={detailId}
        onClick={activate}
      >
        <AgentAvatar agent={agent} />
        <span className="min-w-0">
          <span className="flex min-w-0 items-baseline gap-1.5">
            <span className="truncate text-xs font-semibold text-foreground">{agent.name}</span>
            <span className="truncate text-[11px] text-muted-foreground">{agent.role}</span>
          </span>
          <span className="mt-0.5 block truncate text-xs leading-5 text-foreground" title={actionText}>{actionText}</span>
          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
            <span className="truncate">{timeText}</span>
            {agent.locationLabel ? <><span aria-hidden="true">·</span><span className="truncate">{copy.locatable}</span></> : null}
          </span>
        </span>
        <span className="flex items-center gap-1">
          <span className={cn("inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-[10px] font-semibold", presentation.badgeClass)}>
            <span className={cn("inline-flex size-4 items-center justify-center rounded-full border", presentation.markerClass)}>
              <StatusIcon className="size-2.5" strokeWidth={2.4} />
            </span>
            {copy.status[agent.status]}
          </span>
          <ChevronDown aria-hidden="true" className={cn("mt-1 size-3.5 text-muted-foreground transition-transform motion-reduce:transition-none", expanded && "rotate-180")} />
        </span>
      </button>
      {expanded ? (
        <div id={detailId} className="mx-3 mb-2.5 ml-13 min-w-0 rounded-lg border border-border/70 bg-muted/35 p-2.5">
          {agent.actionDetail ? <p className="break-words text-xs leading-5 text-foreground">{agent.actionDetail}</p> : null}
          {failureReasonText ? <p className="mt-1.5 flex gap-1.5 text-xs leading-5 text-destructive-strong"><TriangleAlert className="mt-1 size-3 shrink-0" aria-hidden="true" /><span className="break-words">{failureReasonText}</span></p> : null}
          {!canLocate ? <p className="mt-1.5 text-xs text-muted-foreground">{copy.unavailable}</p> : null}
          {canLocate ? (
            <Button type="button" variant="outline" size="sm" className="mt-2 min-h-9 max-w-full gap-1.5 px-2 text-xs" onClick={() => onLocate?.(agent)}>
              <LocateFixed className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="truncate">{copy.locate}</span>
            </Button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function fallbackActionKey(status: ResearchExecutionStatus): ResearchExecutionActionKey {
  switch (status) {
    case "queued": return "waiting";
    case "running": return "working";
    case "done": return "recent_done";
    case "failed": return "recent_failed";
    case "stale": return "stale";
    case "idle": return "idle";
  }
}

export function ResearchExecutionPanel({ agents, title, className, onLocate, error, onRetry, isRetrying = false }: {
  agents: readonly ResearchExecutionAgent[];
  title?: string;
  className?: string;
  onLocate?: (agent: ResearchExecutionAgent) => void;
  error?: string | null;
  onRetry?: () => void;
  isRetrying?: boolean;
}) {
  const { t } = useT("research");
  const resolvedTitle = title ?? t(($) => $.panel.execution.title);
  const copy: ExecutionCopy = {
    locatable: t(($) => $.panel.execution.locatable, { location: "" }),
    locate: t(($) => $.panel.execution.locate, { location: "" }),
    unavailable: t(($) => $.panel.execution.unavailable),
    status: {
      queued: t(($) => $.panel.execution.status.queued),
      running: t(($) => $.panel.execution.status.running),
      done: t(($) => $.panel.execution.status.done),
      failed: t(($) => $.panel.execution.status.failed),
      stale: t(($) => $.panel.execution.status.stale),
      idle: t(($) => $.panel.execution.status.idle),
    },
    action: {
      waiting: t(($) => $.panel.execution.action.waiting),
      working: t(($) => $.panel.execution.action.working),
      recent_done: t(($) => $.panel.execution.action.recent_done),
      recent_failed: t(($) => $.panel.execution.action.recent_failed),
      stale: t(($) => $.panel.execution.action.stale),
      idle: t(($) => $.panel.execution.action.idle),
    },
    time: {
      queued: t(($) => $.panel.execution.time.queued),
      running: t(($) => $.panel.execution.time.running),
      recent: t(($) => $.panel.execution.time.recent),
      failed: t(($) => $.panel.execution.time.failed),
      stale: t(($) => $.panel.execution.time.stale),
      idle: t(($) => $.panel.execution.time.idle),
    },
    failedReason: t(($) => $.panel.execution.failed_reason),
    locateAria: (name: string) => t(($) => $.panel.execution.locate_aria, { name }),
    viewAria: (name: string) => t(($) => $.panel.execution.view_aria, { name }),
  };
  const runningCount = agents.filter((agent) => agent.status === "running").length;
  return (
    <section aria-label={resolvedTitle} className={cn("min-w-0 overflow-hidden rounded-xl border border-border bg-card shadow-sm", className)}>
      <header className="flex min-h-10 min-w-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2"><CircleDashed className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" /><h2 className="truncate text-xs font-semibold text-foreground">{resolvedTitle}</h2></div>
        <p className="shrink-0 text-[11px] tabular-nums text-muted-foreground">{runningCount > 0 ? t(($) => $.panel.execution.active_count, { count: runningCount }) : t(($) => $.panel.execution.no_active)}</p>
      </header>
      {error ? (
        <div className="p-3 text-center" role="alert"><p className="text-xs text-destructive-strong">{t(($) => $.panel.execution.load_failed)}</p>{onRetry ? <Button type="button" variant="outline" size="sm" className="mt-2 min-h-9 gap-1.5" aria-disabled={isRetrying} onClick={() => { if (!isRetrying) onRetry(); }}><RefreshCw className={cn("size-3.5", isRetrying && "animate-spin")} aria-hidden="true" /> {t(($) => $.panel.execution.retry)}</Button> : null}</div>
      ) : agents.length === 0 ? (
        <p className="p-4 text-center text-xs text-muted-foreground">{t(($) => $.panel.execution.empty)}</p>
      ) : (
        <div className="min-w-0">{agents.map((agent) => <ExecutionRow key={agent.id} agent={agent} onLocate={onLocate} copy={{ ...copy, locatable: t(($) => $.panel.execution.locatable, { location: agent.locationLabel ?? "" }), locate: t(($) => $.panel.execution.locate, { location: agent.locationLabel ?? "" }) }} />)}</div>
      )}
    </section>
  );
}
