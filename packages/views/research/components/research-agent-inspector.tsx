"use client";

import {
  useEffect,
  useId,
  useRef,
  type RefObject,
} from "react";
import type { TypedGraphNode } from "@multica/core/research";
import type { ExecutionRow } from "../execution-overlay";
import {
  formatClock,
  formatElapsedDuration,
} from "../execution-overlay/execution-overlay-row";
import { Button } from "@multica/ui/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

function payloadString(payload: unknown, key: string): string | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function objectiveFromTypedNode(node: TypedGraphNode | null | undefined): string | null {
  if (!node) return null;
  const root = node.payload;
  const details =
    root && typeof root === "object" && !Array.isArray(root)
      ? (root as Record<string, unknown>).details
      : null;
  const records = [
    details && typeof details === "object" && !Array.isArray(details)
      ? (details as Record<string, unknown>)
      : null,
    root && typeof root === "object" && !Array.isArray(root)
      ? (root as Record<string, unknown>)
      : null,
  ];
  for (const record of records) {
    if (!record) continue;
    for (const key of ["objective", "small_goal", "goal", "question"]) {
      const value = payloadString(record, key);
      if (value) return value;
    }
  }
  return null;
}

function inputFromTypedNode(node: TypedGraphNode | null | undefined): string | null {
  if (!node) return null;
  const root = node.payload;
  const details =
    root && typeof root === "object" && !Array.isArray(root)
      ? (root as Record<string, unknown>).details
      : null;
  const records = [
    details && typeof details === "object" && !Array.isArray(details)
      ? (details as Record<string, unknown>)
      : null,
    root && typeof root === "object" && !Array.isArray(root)
      ? (root as Record<string, unknown>)
      : null,
  ];
  for (const record of records) {
    if (!record) continue;
    for (const key of ["input", "task_input", "inputs"]) {
      const value = payloadString(record, key);
      if (value) return value;
    }
  }
  return null;
}

function ResearchAgentInspectorBody({
  row,
  typedNode,
  onClose,
  onOpenAgentConfig,
  closeButtonRef,
  titleId,
}: {
  row: ExecutionRow;
  typedNode?: TypedGraphNode | null;
  onClose: () => void;
  onOpenAgentConfig?: () => void;
  closeButtonRef?: RefObject<HTMLButtonElement | null>;
  titleId?: string;
}) {
  const { t } = useT("research");
  const payloadObjective = objectiveFromTypedNode(typedNode);
  const payloadInput = inputFromTypedNode(typedNode);
  const objective =
    row.taskObjective ||
    payloadObjective ||
    row.action ||
    t(($) => $.d5.inspector.no_task);
  const executionFacts = [
    row.taskId,
    row.attemptId,
    row.branchId,
    row.startedAt,
    row.updatedAt,
    row.elapsedMs,
    row.reason,
  ].some((value) => value != null && value !== "");
  const clock = (value: number) =>
    formatClock(value, (time) => t(($) => $.panel.execution.clock_time, { time }));
  const elapsed =
    row.elapsedMs == null
      ? null
      : formatElapsedDuration(row.elapsedMs, {
          sec: (count) => t(($) => $.panel.execution.elapsed_sec, { count }),
          min: (count) => t(($) => $.panel.execution.elapsed_min, { count }),
          hour: (count) => t(($) => $.panel.execution.elapsed_hour, { count }),
        });

  return (
    <>
      <header className="agent-head">
        <button
          ref={closeButtonRef}
          type="button"
          className="agent-close"
          onClick={onClose}
          aria-label={t(($) => $.d5.inspector.close)}
        >
          ×
        </button>
        <div className="who">
          <div className="agent-big-avatar">{row.initials || row.name.slice(0, 2).toUpperCase()}</div>
          <div>
            <b id={titleId}>{row.name}</b>
            <span>{row.action || row.actionDetail || row.status}</span>
          </div>
        </div>
      </header>
      <div className="agent-body">
        <div className="agent-objective">
          <small>{t(($) => $.d5.inspector.objective)}</small>
          <b>{objective}</b>
        </div>
        {payloadInput ? (
          <section className="work-block">
            <h4>{t(($) => $.node.input)}</h4>
            <div className="work-item done">{payloadInput}</div>
          </section>
        ) : null}
        {row.stage ? (
          <p className="mt-3 text-[11px] text-muted-foreground">
            {t(($) => $.d5.inspector.phase, { phase: row.stage })}
          </p>
        ) : null}
        {executionFacts ? (
          <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 rounded-lg border border-border/50 bg-background/30 p-3 text-[11px]">
            {row.taskId ? (
              <ExecutionFact label={t(($) => $.panel.execution.task)} value={row.taskId} />
            ) : null}
            {row.attemptId ? (
              <ExecutionFact label={t(($) => $.panel.execution.attempt)} value={row.attemptId} />
            ) : null}
            {row.branchId ? (
              <ExecutionFact label={t(($) => $.d5.inspector.branch)} value={row.branchId} />
            ) : null}
            {row.startedAt != null ? (
              <ExecutionFact label={t(($) => $.panel.execution.started)} value={clock(row.startedAt)} />
            ) : null}
            <ExecutionFact label={t(($) => $.panel.execution.updated)} value={clock(row.updatedAt)} />
            {elapsed ? (
              <ExecutionFact label={t(($) => $.panel.execution.duration)} value={elapsed} />
            ) : null}
            {row.reason ? (
              <ExecutionFact label={t(($) => $.d5.inspector.reason)} value={row.reason} />
            ) : null}
          </dl>
        ) : null}
        {row.recentResult ? (
          <section className="work-block">
            <h4>{t(($) => $.d5.inspector.completed)}</h4>
            <div className="work-item done">{row.recentResult.title}</div>
          </section>
        ) : null}
        {row.action ? (
          <section className="work-block">
            <h4>{t(($) => $.d5.inspector.current)}</h4>
            <div className="work-item live">{row.action}</div>
          </section>
        ) : null}
      </div>
      <footer className="agent-foot">
        {onOpenAgentConfig ? (
          <Button type="button" size="sm" variant="outline" onClick={onOpenAgentConfig}>
            {t(($) => $.d5.inspector.open_agent)}
          </Button>
        ) : null}
      </footer>
    </>
  );
}

function ExecutionFact({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words font-mono text-foreground">{value}</dd>
    </>
  );
}

export function ResearchAgentInspector({
  row,
  typedNode,
  open,
  onClose,
  onOpenAgentConfig,
  className,
}: {
  row: ExecutionRow | null;
  typedNode?: TypedGraphNode | null;
  open: boolean;
  onClose: () => void;
  onOpenAgentConfig?: () => void;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const titleId = useId();
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const restoreFocusRef = useRef(true);
  const rowId = row?.id ?? null;

  useEffect(() => {
    if (!open || !rowId || isMobile) return;
    restoreFocusRef.current = true;
    previousFocusRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const frame = requestAnimationFrame(() => {
      closeButtonRef.current?.focus({ preventScroll: true });
    });
    return () => {
      cancelAnimationFrame(frame);
      if (restoreFocusRef.current && previousFocusRef.current?.isConnected) {
        previousFocusRef.current.focus({ preventScroll: true });
      }
      previousFocusRef.current = null;
    };
  }, [isMobile, open, rowId]);

  const handleOpenAgentConfig = onOpenAgentConfig
    ? () => {
        restoreFocusRef.current = false;
        onOpenAgentConfig();
      }
    : undefined;

  if (!open || !row) return null;

  if (isMobile) {
    return (
      <Sheet
        open={open}
        onOpenChange={(next) => {
          if (!next) onClose();
        }}
      >
        <SheetContent
          side="bottom"
          data-testid="research-agent-inspector"
          data-placement="sheet"
          className={cn(
            "research-agent-inspector-sheet max-h-[min(72dvh,560px)] gap-0 overflow-y-auto rounded-t-2xl border-t border-border bg-canvas-bg p-0 text-foreground",
            className,
          )}
        >
          <SheetHeader className="sr-only">
            <SheetTitle>{row.name}</SheetTitle>
            <SheetDescription>{t(($) => $.d5.inspector.objective)}</SheetDescription>
          </SheetHeader>
          <ResearchAgentInspectorBody
            row={row}
            typedNode={typedNode}
            onClose={onClose}
            onOpenAgentConfig={handleOpenAgentConfig}
          />
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <aside
      role="dialog"
      aria-labelledby={titleId}
      data-testid="research-agent-inspector"
      data-placement="overlay"
      className={cn("research-agent-inspector open", className)}
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        event.preventDefault();
        event.stopPropagation();
        onClose();
      }}
    >
      <ResearchAgentInspectorBody
        row={row}
        typedNode={typedNode}
        onClose={onClose}
        onOpenAgentConfig={handleOpenAgentConfig}
        closeButtonRef={closeButtonRef}
        titleId={titleId}
      />
    </aside>
  );
}
