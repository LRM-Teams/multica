"use client";

import { useMemo, useCallback, useState } from "react";
import { CircleDashed, ChevronsUpDown } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ExecutionRow } from "./execution-adapter";
import { ExecutionOverlayRow, type ExecutionCopy } from "./execution-overlay-row";
import { ExecutionOverlaySyncIndicator } from "./execution-overlay-sync-indicator";
import { EXECUTION_DECK_PRIORITY, type ExecutionStatus } from "./execution-status";

export type ExecutionOverlaySyncState = {
  disconnected?: boolean;
  expired?: boolean;
  lastSyncedAt?: number;
  onRetry?: () => void;
  isRetrying?: boolean;
};

/**
 * LRM-1473 / LRM-1479 — the reusable execution overlay panel. It is the single
 * implementation behind both placements:
 *  - desktop floating overlay on the research canvas (right-top), and
 *  - narrow / side panel inside the aux drawer.
 * Rendering + ordering is a pure display concern: rows arrive pre-derived from
 * the Projection via `buildExecutionOverlayRows` and are never written back.
 */
export function ExecutionOverlayPanel({
  rows,
  title,
  className,
  onLocate,
  sync,
  highlightAgentId,
  groups = true,
}: {
  rows: readonly ExecutionRow[];
  title?: string;
  className?: string;
  onLocate?: (agent: ExecutionRow) => void;
  sync?: ExecutionOverlaySyncState;
  /** Bidirectional locate: id of the agent whose row is highlighted because
   *  the user selected that agent's node on the canvas. */
  highlightAgentId?: string | null;
  groups?: boolean;
}) {
  const { t } = useT("research");
  const resolvedTitle = title ?? t(($) => $.panel.execution.title);

  const copy: ExecutionCopy = useMemo(
    () => ({
      status: {
        queued: t(($) => $.panel.execution.status.queued),
        running: t(($) => $.panel.execution.status.running),
        cancelling: t(($) => $.panel.execution.status.cancelling),
        done: t(($) => $.panel.execution.status.done),
        failed: t(($) => $.panel.execution.status.failed),
        retrying: t(($) => $.panel.execution.status.retrying),
        stale: t(($) => $.panel.execution.status.stale),
        idle: t(($) => $.panel.execution.status.idle),
        offline: t(($) => $.panel.execution.status.offline),
        unknown: t(($) => $.panel.execution.status.unknown),
      },
      action: {
        waiting: t(($) => $.panel.execution.action.waiting),
        working: t(($) => $.panel.execution.action.working),
        cancelling: t(($) => $.panel.execution.action.cancelling),
        recent_done: t(($) => $.panel.execution.action.recent_done),
        recent_failed: t(($) => $.panel.execution.action.recent_failed),
        retrying: t(($) => $.panel.execution.action.retrying),
        stale: t(($) => $.panel.execution.action.stale),
        idle: t(($) => $.panel.execution.action.idle),
        offline: t(($) => $.panel.execution.action.offline),
        unknown: t(($) => $.panel.execution.action.unknown),
      },
      recentResult: t(($) => $.panel.execution.recent_result),
      startLabel: t(($) => $.panel.execution.started),
      updatedLabel: t(($) => $.panel.execution.updated),
      durationLabel: t(($) => $.panel.execution.duration),
      stageLabel: t(($) => $.panel.execution.stage),
      waitLabel: t(($) => $.panel.execution.wait_reason),
      staleLabel: t(($) => $.panel.execution.stale_reason),
      taskLabel: t(($) => $.panel.execution.task),
      attemptLabel: t(($) => $.panel.execution.attempt),
      taskObjectiveLabel: t(($) => $.panel.execution.task_objective),
      failedReason: t(($) => $.panel.execution.failed_reason),
      waitingReason: t(($) => $.panel.execution.waiting_reason),
      offlineReason: t(($) => $.panel.execution.offline_reason),
      unknownReason: t(($) => $.panel.execution.unknown_reason),
      locatable: t(($) => $.panel.execution.locatable, { location: "" }),
      locate: t(($) => $.panel.execution.locate, { location: "" }),
      unavailable: t(($) => $.panel.execution.unavailable),
      locateAria: (name: string) => t(($) => $.panel.execution.locate_aria, { name }),
      viewAria: (name: string) => t(($) => $.panel.execution.view_aria, { name }),
      expandAria: (name: string) => t(($) => $.panel.execution.expand_aria, { name }),
      clock_time: (time: string) => t(($) => $.panel.execution.clock_time, { time }),
      elapsed_sec: (count: number) => t(($) => $.panel.execution.elapsed_sec, { count }),
      elapsed_min: (count: number) => t(($) => $.panel.execution.elapsed_min, { count }),
      elapsed_hour: (count: number) => t(($) => $.panel.execution.elapsed_hour, { count }),
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t],
  );

  const anomalyCount = rows.filter(
    (r) => r.status === "failed" || r.status === "cancelling" || r.status === "stale",
  ).length;
  const runningCount = rows.filter((r) => r.status === "running").length;
  const queuedCount = rows.filter((r) => r.status === "queued").length;

  // Deck ordering (agent-execution-spec §3): blocking failed → cancelling →
  // running → queued → retrying → stale → idle → done → offline/unknown;
  // newest first within a status. Sorting is a pure display concern and never
  // mutates the underlying rows.
  const sortedRows = useMemo(
    () =>
      [...rows].sort((a, b) => {
        const pa = EXECUTION_DECK_PRIORITY[a.status] ?? 9;
        const pb = EXECUTION_DECK_PRIORITY[b.status] ?? 9;
        if (pa !== pb) return pa - pb;
        return (b.updatedAt ?? 0) - (a.updatedAt ?? 0);
      }),
    [rows],
  );

  // Grouped rows following the same deck priority order; labels are localized.
  const grouped = useMemo(() => {
    const buckets = new Map<ExecutionStatus, ExecutionRow[]>();
    const order: ExecutionStatus[] = [
      "failed", "cancelling", "running", "queued", "retrying",
      "stale", "idle", "done", "offline", "unknown",
    ];
    for (const row of sortedRows) {
      const list = buckets.get(row.status) ?? [];
      list.push(row);
      buckets.set(row.status, list);
    }
    return order
      .map((status) => ({ status, list: buckets.get(status) ?? [] }))
      .filter(({ list }) => list.length > 0);
  }, [sortedRows]);

  const renderList = (list: readonly ExecutionRow[]) =>
    list.map((agent) => (
      <ExecutionOverlayRow
        key={agent.id}
        agent={agent}
        copy={{ ...copy, locatable: t(($) => $.panel.execution.locatable, { location: agent.locationLabel ?? "" }), locate: t(($) => $.panel.execution.locate, { location: agent.locationLabel ?? "" }) }}
        highlighted={highlightAgentId != null && agent.id === highlightAgentId}
        onLocate={onLocate}
      />
    ));

  // Roving keyboard navigation across the row list (ArrowUp / ArrowDown).
  const onListKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLElement>) => {
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      const buttons = Array.from(
        (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>(
          '[data-testid="execution-overlay-row"] > button',
        ),
      );
      if (buttons.length === 0) return;
      const idx = buttons.indexOf(document.activeElement as HTMLButtonElement);
      if (idx === -1) return;
      event.preventDefault();
      const next = event.key === "ArrowDown" ? idx + 1 : idx - 1;
      const target = buttons[(next + buttons.length) % buttons.length];
      target?.focus();
    },
    [],
  );

  const [collapsed, setCollapsed] = useState(false);

  return (
    <section
      aria-label={resolvedTitle}
      data-testid="execution-overlay-panel"
      className={cn("min-w-0 overflow-hidden rounded-xl border border-border bg-card shadow-sm", className)}
    >
      <header className="flex min-h-10 min-w-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <CircleDashed className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <h2 className="truncate text-xs font-semibold text-foreground">{resolvedTitle}</h2>
        </div>
        <button
          type="button"
          aria-expanded={!collapsed}
          data-testid="execution-overlay-collapse-toggle"
          className="flex min-w-0 shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-[11px] tabular-nums text-muted-foreground outline-none hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-brand"
          onClick={() => setCollapsed((v) => !v)}
        >
          <ChevronsUpDown className="size-3 shrink-0" aria-hidden="true" />
          <span className="truncate">
            {t(($) => $.panel.execution.collapse_counts, {
              anomaly: anomalyCount,
              running: runningCount,
              queued: queuedCount,
              total: rows.length,
            })}
          </span>
        </button>
      </header>

      <ExecutionOverlaySyncIndicator
        disconnected={sync?.disconnected}
        expired={sync?.expired}
        lastSyncedAt={sync?.lastSyncedAt}
        onRetry={sync?.onRetry}
        isRetrying={sync?.isRetrying}
      />

      {collapsed ? (
        <p className="p-3 text-center text-xs text-muted-foreground">
          {t(($) => $.panel.execution.collapsed_hint)}
        </p>
      ) : rows.length === 0 ? (
        <p className="p-4 text-center text-xs text-muted-foreground">
          {t(($) => $.panel.execution.empty)}
        </p>
      ) : !groups ? (
        <div className="min-w-0" onKeyDown={onListKeyDown}>
          {renderList(sortedRows)}
        </div>
      ) : (
        <div className="min-w-0" onKeyDown={onListKeyDown}>
          {grouped.map(({ status, list }) => (
            <div key={status} className="min-w-0">
              <div className="px-3 pt-2.5 pb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {copy.status[status]}
              </div>
              {renderList(list)}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
