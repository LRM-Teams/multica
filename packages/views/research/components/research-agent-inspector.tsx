"use client";

import type { TypedGraphNode } from "@multica/core/research";
import type { ExecutionRow } from "../execution-overlay";
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
}: {
  row: ExecutionRow;
  typedNode?: TypedGraphNode | null;
  onClose: () => void;
  onOpenAgentConfig?: () => void;
}) {
  const { t } = useT("research");
  const payloadObjective = objectiveFromTypedNode(typedNode);
  const payloadInput = inputFromTypedNode(typedNode);
  const objective =
    row.taskObjective ||
    payloadObjective ||
    row.action ||
    t(($) => $.d5.inspector.no_task);

  return (
    <>
      <header className="agent-head">
        <button
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
            <b>{row.name}</b>
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
            "research-agent-inspector-sheet max-h-[min(72dvh,560px)] gap-0 overflow-y-auto rounded-t-2xl border-t border-border p-0 text-foreground",
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
            onOpenAgentConfig={onOpenAgentConfig}
          />
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <aside
      data-testid="research-agent-inspector"
      data-placement="overlay"
      className={cn("research-agent-inspector open", className)}
    >
      <ResearchAgentInspectorBody
        row={row}
        typedNode={typedNode}
        onClose={onClose}
        onOpenAgentConfig={onOpenAgentConfig}
      />
    </aside>
  );
}
