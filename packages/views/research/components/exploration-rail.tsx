"use client";

import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ExplorationDimension, DimensionStatus } from "../lib/m2-visibility";

const dotClass: Record<DimensionStatus, string> = {
  open: "bg-brand",
  covered: "bg-success",
  gap: "bg-warning",
  dead: "bg-muted-foreground/50",
};

export function ExplorationRail({
  dimensions,
  selectedFamily,
  selectedQuestionId,
  onSelectFamily,
  onSelectQuestion,
  className,
}: {
  dimensions: ExplorationDimension[];
  selectedFamily?: string | null;
  selectedQuestionId?: string | null;
  onSelectFamily?: (family: string) => void;
  onSelectQuestion?: (nodeId: string) => void;
  className?: string;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState<Record<string, boolean>>({});

  if (dimensions.length === 0) {
    return (
      <aside
        data-testid="exploration-rail"
        className={cn(
          "flex w-[280px] shrink-0 flex-col border-r bg-background",
          className,
        )}
      >
        <div className="border-b px-3 py-2.5">
          <h3 className="text-xs font-semibold tracking-wide uppercase text-foreground">
            {t(($) => $.m2.rail_title)}
          </h3>
          <p className="text-[10px] text-muted-foreground">{t(($) => $.m2.rail_hint)}</p>
        </div>
        <p className="px-3 py-6 text-sm text-muted-foreground">{t(($) => $.m2.rail_empty)}</p>
      </aside>
    );
  }

  return (
    <aside
      data-testid="exploration-rail"
      className={cn(
        "flex w-[280px] shrink-0 flex-col overflow-hidden border-r bg-background",
        className,
      )}
    >
      <div className="border-b px-3 py-2.5">
        <h3 className="text-xs font-semibold tracking-wide uppercase text-foreground">
          {t(($) => $.m2.rail_title)}
        </h3>
        <p className="text-[10px] text-muted-foreground">{t(($) => $.m2.rail_hint)}</p>
      </div>
      <div className="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
        {dimensions.map((dim) => {
          const expanded = open[dim.family] ?? dim.family === selectedFamily;
          const statusLabel = t(($) => $.m2.status[dim.status]);
          return (
            <div
              key={dim.family}
              className={cn(
                "rounded-lg border transition-colors duration-150",
                dim.family === selectedFamily
                  ? "border-brand/40 bg-brand/5"
                  : "border-transparent bg-muted/20 hover:bg-muted/40",
              )}
            >
              <button
                type="button"
                className="flex w-full items-center gap-2 px-2.5 py-2 text-left"
                onClick={() => {
                  setOpen((s) => ({ ...s, [dim.family]: !expanded }));
                  onSelectFamily?.(dim.family);
                }}
              >
                <span
                  className={cn("size-2 shrink-0 rounded-full", dotClass[dim.status])}
                  aria-hidden
                />
                <span className="min-w-0 flex-1 truncate text-sm font-medium">{dim.title}</span>
                {dim.required ? (
                  <span className="rounded-md border px-1.5 py-0.5 text-[10px] text-muted-foreground">
                    {t(($) => $.m2.required)}
                  </span>
                ) : null}
                <span className="text-[10px] text-muted-foreground">{statusLabel}</span>
              </button>
              {expanded && dim.questions.length > 0 ? (
                <ul className="space-y-0.5 px-2 pb-2">
                  {dim.questions.map((q) => (
                    <li key={q.id}>
                      <button
                        type="button"
                        className={cn(
                          "w-full truncate rounded-md px-2 py-1.5 text-left text-xs transition-colors duration-150",
                          q.id === selectedQuestionId || q.active
                            ? "bg-background font-medium text-foreground shadow-sm"
                            : "text-muted-foreground hover:bg-background/80 hover:text-foreground",
                        )}
                        onClick={() => onSelectQuestion?.(q.id)}
                      >
                        {q.title}
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
              {expanded && dim.findingSummary ? (
                <p className="border-t px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
                  {dim.findingSummary}
                </p>
              ) : null}
            </div>
          );
        })}
      </div>
    </aside>
  );
}
