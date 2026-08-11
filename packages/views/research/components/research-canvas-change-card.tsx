"use client";

import type { ResearchMessage } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

const CANVAS_CHANGE_OPS = new Set([
  "goal_steered",
  "integration_formed",
  "node_retired",
  "task_restarted",
  "run_completed",
]);

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

export function isCanvasChangeProcessMessage(message: ResearchMessage): boolean {
  if (message.card_kind !== "process") return false;
  const op = metaString(message.meta, "op");
  return op != null && CANVAS_CHANGE_OPS.has(op);
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
  const title = metaString(message.meta, "title") || message.body.trim();
  const detail =
    metaString(message.meta, "summary") ||
    metaString(message.meta, "goal") ||
    metaString(message.meta, "reason");

  return (
    <div
      data-testid="research-canvas-change-card"
      data-canvas-change-op={op}
      className={cn(
        "rounded-xl border border-brand/25 bg-brand/5 px-3 py-2.5 text-[12px]",
        className,
      )}
    >
      <div className="font-semibold text-brand">
        {t(($) => $.d5.change_receipt.title, { op })}
      </div>
      {title ? <div className="mt-1 font-medium text-foreground">{title}</div> : null}
      {detail ? <div className="mt-1 text-muted-foreground">{detail}</div> : null}
    </div>
  );
}
