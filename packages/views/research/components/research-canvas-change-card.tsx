"use client";

import type { ResearchMessage } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
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
}: {
  message: ResearchMessage;
  className?: string;
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
    </div>
  );
}
