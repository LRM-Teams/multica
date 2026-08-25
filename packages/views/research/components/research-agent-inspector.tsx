"use client";

import {
  useEffect,
  useId,
  useRef,
  type RefObject,
} from "react";
import { ChevronDown, UserRound, X } from "lucide-react";
import type { TypedGraphNode } from "@multica/core/research";
import {
  EXECUTION_STATUS_PRESENTATION,
  type ExecutionRow,
} from "../execution-overlay";
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
import { ActorAvatar } from "../../common/actor-avatar";
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

function normalizedText(value: string | null | undefined): string {
  return (value ?? "").trim().replace(/\s+/g, " ").toLocaleLowerCase();
}

function distinctText(
  value: string | null | undefined,
  comparedWith: Array<string | null | undefined>,
): string | null {
  const normalized = normalizedText(value);
  if (!normalized) return null;
  return comparedWith.some((candidate) => normalizedText(candidate) === normalized)
    ? null
    : (value ?? "").trim();
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
  titleId: string;
}) {
  const { t } = useT("research");
  const payloadObjective = objectiveFromTypedNode(typedNode);
  const payloadInput = inputFromTypedNode(typedNode);
  const objective =
    row.taskObjective ||
    payloadObjective ||
    t(($) => $.d5.inspector.no_task);
  const statusPresentation = EXECUTION_STATUS_PRESENTATION[row.status];
  const StatusIcon = statusPresentation.Icon;
  const statusLabel = t(($) => $.panel.execution.status[row.status]);
  const action = distinctText(row.action ?? row.actionDetail, [objective, statusLabel]);
  const currentAction = row.status === "done" || row.status === "failed" ? null : action;
  const input = distinctText(payloadInput, [objective, currentAction]);
  const recentResult = distinctText(
    row.recentResult?.title ?? (row.status === "done" ? action : null),
    [objective, currentAction, input],
  );
  const failureReason = row.reason ?? (row.status === "failed" ? row.actionDetail : null);
  const executionFacts = [
    row.taskId,
    row.attemptId,
    row.branchId,
    row.startedAt,
    row.updatedAt,
    row.elapsedMs,
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
    <div
      data-testid="research-agent-inspector-content"
      className="min-w-0 [overflow-wrap:anywhere]"
    >
      <header className="flex items-start gap-3 border-b border-border p-4">
        <ActorAvatar
          actorType="agent"
          actorId={row.id}
          name={row.name}
          size={40}
          profileLink={false}
        />
        <div className="min-w-0 flex-1 pt-0.5">
          <h2 id={titleId} className="truncate text-sm font-medium text-foreground">
            {row.name}
          </h2>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            <span className={cn("inline-flex items-center gap-1.5", statusPresentation.textClass)}>
              <StatusIcon className="size-3.5" aria-hidden="true" />
              {statusLabel}
            </span>
            {row.stage ? (
              <span className="truncate text-muted-foreground">
                {t(($) => $.d5.inspector.phase, { phase: row.stage })}
              </span>
            ) : null}
          </div>
        </div>
        <Button
          ref={closeButtonRef}
          type="button"
          size="icon-sm"
          variant="ghost"
          className="-mr-1 -mt-1 text-muted-foreground"
          onClick={onClose}
          aria-label={t(($) => $.d5.inspector.close)}
        >
          <X aria-hidden="true" />
        </Button>
      </header>
      <div className="space-y-4 p-4">
        <section aria-labelledby={`${titleId}-objective`}>
          <h3
            id={`${titleId}-objective`}
            className="text-xs font-medium text-muted-foreground"
          >
            {t(($) => $.d5.inspector.objective)}
          </h3>
          <p className="mt-1.5 text-sm leading-relaxed text-foreground">{objective}</p>
        </section>
        {currentAction ? (
          <section className="border-t border-border pt-4">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t(($) => $.d5.inspector.current)}
            </h3>
            <p className="mt-1.5 text-sm leading-relaxed text-foreground">
              {currentAction}
            </p>
          </section>
        ) : null}
        {recentResult ? (
          <section className="border-t border-border pt-4">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t(($) => $.d5.inspector.completed)}
            </h3>
            <p className="mt-1.5 text-sm leading-relaxed text-foreground">
              {recentResult}
            </p>
          </section>
        ) : null}
        {input ? (
          <section className="border-t border-border pt-4">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t(($) => $.node.input)}
            </h3>
            <p className="mt-1.5 text-sm leading-relaxed text-foreground">{input}</p>
          </section>
        ) : null}
        {failureReason ? (
          <section className="border-t border-destructive/30 pt-4">
            <h3 className="text-xs font-medium text-destructive-strong">
              {t(($) => $.d5.inspector.reason)}
            </h3>
            <p className="mt-1.5 text-sm leading-relaxed text-foreground">
              {failureReason}
            </p>
          </section>
        ) : null}
        {executionFacts ? (
          <details className="group border-t border-border pt-3">
            <summary className="flex cursor-pointer list-none items-center justify-between rounded-lg py-1 text-xs font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
              {t(($) => $.d5.inspector.execution_details)}
              <ChevronDown
                className="size-4 transition-transform group-open:rotate-180"
                aria-hidden="true"
              />
            </summary>
            <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs">
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
              {row.updatedAt != null ? (
                <ExecutionFact label={t(($) => $.panel.execution.updated)} value={clock(row.updatedAt)} />
              ) : null}
              {elapsed ? (
                <ExecutionFact label={t(($) => $.panel.execution.duration)} value={elapsed} />
              ) : null}
            </dl>
          </details>
        ) : null}
      </div>
      {onOpenAgentConfig ? (
        <footer className="border-t border-border p-3">
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="w-full"
            onClick={onOpenAgentConfig}
          >
            <UserRound data-icon="inline-start" aria-hidden="true" />
            {t(($) => $.d5.inspector.open_agent)}
          </Button>
        </footer>
      ) : null}
    </div>
  );
}

function ExecutionFact({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all font-mono text-foreground">{value}</dd>
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
          showCloseButton={false}
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
            titleId={titleId}
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
