"use client";

import type { ResearchMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { LocateFixed } from "lucide-react";
import { useT } from "../../i18n/use-t";

const CANVAS_CHANGE_OPS: Readonly<Record<string, string>> = {
  goal_steered: "goal_steered",
  goal_modified: "goal_modified",
  graph_merge: "integration_formed",
  integration_formed: "integration_formed",
  node_retired: "node_retired",
  node_command_retry: "task_restarted",
  task_restarted: "task_restarted",
  node_command_fork: "frontier_created",
  node_command_reassign: "agent_reassigned",
  run_completed: "run_completed",
};

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function metaNumber(meta: unknown, key: string): number | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function metaArrayLength(meta: unknown, key: string): number | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return Array.isArray(value) ? value.length : null;
}

function metaStringArray(meta: unknown, key: string): string[] {
  if (!meta || typeof meta !== "object") return [];
  const value = (meta as Record<string, unknown>)[key];
  if (!Array.isArray(value)) return [];
  return value.flatMap((entry) =>
    typeof entry === "string" && entry.trim() ? [entry.trim()] : [],
  );
}

/**
 * Returns only node identifiers explicitly committed by the process message.
 * The successor/result node stays first so a merge receipt focuses the newly
 * formed higher-tier result instead of one of its absorbed inputs.
 */
export function canvasChangeTargetNodeIds(message: ResearchMessage): string[] {
  if (!isCanvasChangeProcessMessage(message)) return [];
  const candidates = [
    metaString(message.meta, "node_id"),
    metaString(message.meta, "result_node_id"),
    metaString(message.meta, "source_node_id"),
    metaString(message.meta, "target_node_id"),
    ...metaStringArray(message.meta, "affected_node_ids"),
    ...metaStringArray(message.meta, "input_node_ids"),
  ];
  return Array.from(
    new Set(candidates.filter((value): value is string => value != null)),
  );
}

export function canvasChangeKind(message: ResearchMessage): string | null {
  if (message.card_kind !== "process") return null;
  const op = metaString(message.meta, "op");
  return op ? CANVAS_CHANGE_OPS[op] ?? null : null;
}

export function isCanvasChangeProcessMessage(message: ResearchMessage): boolean {
  return canvasChangeKind(message) != null;
}

export function ResearchCanvasChangeCard({
  message,
  className,
  onFocusNode,
}: {
  message: ResearchMessage;
  className?: string;
  onFocusNode?: (nodeId: string) => void;
}) {
  const { t } = useT("research");
  const op = metaString(message.meta, "op") ?? "process";
  const changeKind = canvasChangeKind(message) ?? op;
  const title = metaString(message.meta, "title") || message.body.trim();
  const detail =
    metaString(message.meta, "summary") ||
    metaString(message.meta, "goal") ||
    metaString(message.meta, "reason") ||
    (message.body.trim() !== title ? message.body.trim() : null);
  const inputCount = metaArrayLength(message.meta, "input_node_ids");
  const conclusionCount = metaNumber(message.meta, "conclusion_count");
  const graphVersion = metaNumber(message.meta, "graph_version");
  const targetNodeId = canvasChangeTargetNodeIds(message)[0] ?? null;
  const facts = [
    inputCount == null
      ? null
      : t(($) => $.d5.change_receipt.input_count, { count: inputCount }),
    conclusionCount == null
      ? null
      : t(($) => $.d5.change_receipt.conclusion_count, { count: conclusionCount }),
    graphVersion == null
      ? null
      : t(($) => $.d5.change_receipt.graph_version, { version: graphVersion }),
  ].filter((value): value is string => Boolean(value));

  const opLabel = t(($) => {
    const ops = $.d5.change_receipt.ops as Record<string, string> | undefined;
    return ops?.[changeKind] ?? changeKind;
  });

  return (
    <div
      data-testid="research-canvas-change-card"
      data-canvas-change-op={op}
      data-canvas-change-kind={changeKind}
      className={cn(
        "rounded-xl border border-brand/25 bg-brand/5 px-3 py-2.5 text-[12px]",
        changeKind === "integration_formed" && "motion-safe:animate-pulse",
        className,
      )}
    >
      <div className="font-semibold text-brand">
        {t(($) => $.d5.change_receipt.title, { label: opLabel })}
      </div>
      {title ? <div className="mt-1 font-medium text-foreground">{title}</div> : null}
      {detail ? <div className="mt-1 text-muted-foreground">{detail}</div> : null}
      {facts.length ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {facts.map((fact) => (
            <span
              key={fact}
              className="rounded-md border border-brand/20 bg-background/35 px-1.5 py-0.5 text-[10px] text-muted-foreground"
            >
              {fact}
            </span>
          ))}
        </div>
      ) : null}
      {targetNodeId && onFocusNode ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="mt-2 min-h-9 gap-1.5 px-2 text-[11px] text-brand hover:bg-brand/10 hover:text-brand focus-visible:ring-brand"
          onClick={() => onFocusNode(targetNodeId)}
        >
          <LocateFixed className="size-3.5" aria-hidden="true" />
          {t(($) => $.d5.change_receipt.show_on_canvas)}
        </Button>
      ) : null}
    </div>
  );
}
