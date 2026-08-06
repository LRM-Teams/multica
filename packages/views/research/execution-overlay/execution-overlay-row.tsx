"use client";

import { useId, useState } from "react";
import {
  ChevronDown,
  LocateFixed,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import {
  EXECUTION_STATUS_ACTION_KEY,
  EXECUTION_STATUS_PRESENTATION,
  type ExecutionStatus,
} from "./execution-status";
import type { ExecutionRow } from "./execution-adapter";

export function formatElapsed(
  ms: number,
  fmt: (count: number) => string,
): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return fmt(seconds);
}

/** Pick seconds / minutes / hours formatter based on magnitude. */
export function formatElapsedDuration(
  ms: number,
  f: { sec: (count: number) => string; min: (count: number) => string; hour: (count: number) => string },
): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return f.sec(seconds);
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return f.min(minutes);
  const hours = Math.floor(minutes / 60);
  return f.hour(hours);
}

export function formatClock(ts: number, fmt: (time: string) => string): string {
  const d = new Date(ts);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return fmt(`${hh}:${mm}`);
}

export type ExecutionCopy = {
  status: Record<ExecutionStatus, string>;
  action: Record<string, string>;
  recentResult: string;
  startLabel: string;
  updatedLabel: string;
  durationLabel: string;
  stageLabel: string;
  waitLabel: string;
  staleLabel: string;
  taskLabel: string;
  attemptLabel: string;
  failedReason: string;
  waitingReason: string;
  offlineReason: string;
  unknownReason: string;
  locatable: string;
  locate: string;
  unavailable: string;
  locateAria: (name: string) => string;
  viewAria: (name: string) => string;
  expandAria: (name: string) => string;
  clock_time: (time: string) => string;
  elapsed_sec: (count: number) => string;
  elapsed_min: (count: number) => string;
  elapsed_hour: (count: number) => string;
};

export function ExecutionOverlayRow({
  agent,
  highlighted,
  onLocate,
  copy,
}: {
  agent: ExecutionRow;
  highlighted?: boolean;
  onLocate?: (agent: ExecutionRow) => void;
  copy: ExecutionCopy;
}) {
  const [expanded, setExpanded] = useState(false);
  const detailId = useId();
  const presentation = EXECUTION_STATUS_PRESENTATION[agent.status];
  const StatusIcon = presentation.Icon;
  const canLocate = Boolean(agent.currentNodeId && onLocate);
  const actionText =
    agent.action ?? copy.action[EXECUTION_STATUS_ACTION_KEY[agent.status]] ?? agent.action;
  const active = agent.status === "running" || agent.status === "waiting" || agent.status === "retrying";
  const activate = () => {
    if (canLocate) onLocate?.(agent);
    else setExpanded(true);
  };

  const reasonText =
    agent.status === "failed"
      ? copy.failedReason
      : agent.status === "waiting"
        ? copy.waitingReason
        : agent.status === "offline"
          ? copy.offlineReason
          : agent.status === "unknown"
            ? copy.unknownReason
            : undefined;

  return (
    <article
      data-testid="execution-overlay-row"
      data-status={agent.status}
      data-highlighted={highlighted ? "true" : "false"}
      className={cn(
        "relative min-w-0 overflow-hidden border-b border-border/60 last:border-b-0 transition-colors",
        highlighted && "bg-brand/5",
      )}
    >
      {agent.status === "running" ? (
        <span
          aria-hidden="true"
          className="absolute inset-x-0 top-0 h-px overflow-hidden bg-brand/15"
        >
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
        {agent.avatarUrl ? (
          // Plain <img> on purpose: packages/views is framework-agnostic and
          // must not reference next/image (or Next-only lint rules — the
          // @next/next plugin does not exist in this package's eslint config).
          <img src={agent.avatarUrl} alt="" className="size-8 shrink-0 rounded-lg object-cover" />
        ) : (
          <span
            aria-hidden="true"
            className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-[11px] font-semibold text-foreground"
          >
            {agent.initials}
          </span>
        )}
        <span className="min-w-0">
          <span className="flex min-w-0 items-baseline gap-1.5">
            <span className={cn("truncate text-xs font-semibold", presentation.textClass)}>
              {agent.name}
            </span>
            <span className="truncate text-[11px] text-muted-foreground">{agent.role}</span>
          </span>
          <span className="mt-0.5 block truncate text-xs leading-5 text-foreground" title={actionText}>
            {actionText}
          </span>
          {/* Time line: start · duration · last update (tabular-nums). */}
          <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11px] tabular-nums text-muted-foreground">
            {agent.startedAt != null ? (
              <span>{copy.startLabel} {formatClock(agent.startedAt, copy.clock_time)}</span>
            ) : null}
            {active && agent.elapsedMs != null ? (
              <span>
                {copy.durationLabel}{" "}
                {formatElapsedDuration(agent.elapsedMs, {
                  sec: copy.elapsed_sec,
                  min: copy.elapsed_min,
                  hour: copy.elapsed_hour,
                })}
              </span>
            ) : null}
            <span>{copy.updatedLabel} {formatClock(agent.updatedAt, copy.clock_time)}</span>
            {agent.locationLabel ? (
              <>
                <span aria-hidden="true">·</span>
                <span className="truncate">{copy.locatable}</span>
              </>
            ) : null}
          </span>
          {/* Recent accepted output. */}
          {agent.recentResult ? (
            <span className="mt-1 flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground">
              <span className="size-1 shrink-0 rounded-full bg-success" aria-hidden="true" />
              <span className="truncate" title={copy.recentResult}>
                {copy.recentResult}: {agent.recentResult.title}
              </span>
            </span>
          ) : null}
        </span>
        <span className="flex items-center gap-1">
          <span
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-[10px] font-semibold",
              presentation.badgeClass,
            )}
            data-status-badge
          >
            <span
              className={cn(
                "inline-flex size-4 items-center justify-center rounded-full border",
                presentation.markerClass,
              )}
            >
              <StatusIcon className="size-2.5" strokeWidth={2.4} />
            </span>
            {copy.status[agent.status]}
          </span>
          <ChevronDown
            aria-hidden="true"
            className={cn(
              "mt-1 size-3.5 text-muted-foreground transition-transform motion-reduce:transition-none",
              expanded && "rotate-180",
            )}
          />
        </span>
      </button>
      {expanded ? (
        <div
          id={detailId}
          className="mx-3 mb-2.5 ml-13 min-w-0 rounded-lg border border-border/70 bg-muted/35 p-2.5"
        >
          {agent.actionDetail ? (
            <p className="break-words text-xs leading-5 text-foreground">{agent.actionDetail}</p>
          ) : null}
          {reasonText ? (
            <p className="mt-1.5 flex gap-1.5 text-xs leading-5 text-destructive-strong">
              <TriangleAlert className="mt-1 size-3 shrink-0" aria-hidden="true" />
              <span className="break-words">{reasonText}</span>
            </p>
          ) : null}
          {/* Binding detail from the Projection only. */}
          <dl className="mt-1.5 space-y-0.5 text-[11px] text-muted-foreground">
            {agent.stage ? (
              <div className="flex gap-1.5">
                <dt className="shrink-0">{copy.stageLabel}</dt>
                <dd className="min-w-0 break-words">{agent.stage}</dd>
              </div>
            ) : null}
            {agent.waitingReason && agent.status === "waiting" ? (
              <div className="flex gap-1.5">
                <dt className="shrink-0">{copy.waitLabel}</dt>
                <dd className="min-w-0 break-words">{agent.waitingReason}</dd>
              </div>
            ) : null}
            {agent.staleReason && agent.status === "stale" ? (
              <div className="flex gap-1.5">
                <dt className="shrink-0">{copy.staleLabel}</dt>
                <dd className="min-w-0 break-words">{agent.staleReason}</dd>
              </div>
            ) : null}
            {agent.taskId ? (
              <div className="flex gap-1.5">
                <dt className="shrink-0">{copy.taskLabel}</dt>
                <dd className="min-w-0 break-words">{agent.taskId}</dd>
              </div>
            ) : null}
            {agent.attemptId ? (
              <div className="flex gap-1.5">
                <dt className="shrink-0">{copy.attemptLabel}</dt>
                <dd className="min-w-0 break-words">{agent.attemptId}</dd>
              </div>
            ) : null}
          </dl>
          {!canLocate ? (
            <p className="mt-1.5 text-xs text-muted-foreground">{copy.unavailable}</p>
          ) : null}
          {canLocate ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="mt-2 min-h-9 max-w-full gap-1.5 px-2 text-xs"
              onClick={() => onLocate?.(agent)}
            >
              <LocateFixed className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="truncate">{copy.locate}</span>
            </Button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}
