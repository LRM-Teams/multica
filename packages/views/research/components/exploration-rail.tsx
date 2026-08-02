"use client";

import { useState, type ReactNode } from "react";
import { AlertCircle, ChevronDown, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ExplorationDimension, DimensionStatus } from "../lib/m2-visibility";
import { resolveExplorationRailMode } from "../lib/m2-visibility";

const statusDot: Record<DimensionStatus, string> = {
  open: "bg-brand",
  covered: "bg-success",
  gap: "bg-warning",
  dead: "bg-muted-foreground/55",
};

const statusChip: Record<DimensionStatus, string> = {
  open: "border-brand/35 bg-brand/10 text-brand",
  covered: "border-success/35 bg-success/10 text-success",
  gap: "border-warning/40 bg-warning/10 text-foreground",
  dead: "border-border bg-muted/50 text-muted-foreground",
};

function RailHeader({
  title,
  hint,
}: {
  title: string;
  hint: string;
}) {
  return (
    <div className="border-b border-border/55 px-3 py-2.5">
      <h3 className="text-xs font-semibold tracking-wide text-foreground uppercase">
        {title}
      </h3>
      <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{hint}</p>
    </div>
  );
}

function RailShell({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <aside
      data-testid="exploration-rail"
      className={cn(
        "relative z-[1] flex w-[300px] shrink-0 flex-col overflow-hidden border-r border-border/55 bg-background/55 backdrop-blur-sm",
        className,
      )}
    >
      {children}
    </aside>
  );
}

export function ExplorationRail({
  dimensions,
  sessionStatus,
  error,
  onRetry,
  selectedFamily,
  selectedQuestionId,
  onSelectFamily,
  onSelectQuestion,
  className,
}: {
  dimensions: ExplorationDimension[];
  /** Session status drives empty vs in-flight loading (LRM-975). */
  sessionStatus?: string | null;
  error?: string | null;
  onRetry?: () => void;
  selectedFamily?: string | null;
  selectedQuestionId?: string | null;
  onSelectFamily?: (family: string) => void;
  onSelectQuestion?: (nodeId: string) => void;
  className?: string;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const mode = resolveExplorationRailMode(dimensions, sessionStatus, error);

  if (mode === "error") {
    return (
      <RailShell className={className}>
        <RailHeader
          title={t(($) => $.m2.rail_title)}
          hint={t(($) => $.m2.rail_hint)}
        />
        <div
          role="alert"
          data-testid="exploration-rail-error"
          className="flex flex-1 flex-col items-start gap-3 px-3 py-6"
        >
          <AlertCircle className="size-5 text-destructive" aria-hidden />
          <p className="text-sm text-destructive">
            {error || t(($) => $.m2.rail_error)}
          </p>
          {onRetry ? (
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              {t(($) => $.session_page.retry)}
            </Button>
          ) : null}
        </div>
      </RailShell>
    );
  }

  if (mode === "loading") {
    return (
      <RailShell className={className}>
        <RailHeader
          title={t(($) => $.m2.rail_title)}
          hint={t(($) => $.m2.rail_hint)}
        />
        <div
          data-testid="exploration-rail-loading"
          className="flex flex-1 flex-col gap-2 p-2"
          aria-busy
          aria-live="polite"
        >
          <div className="mb-1 flex items-center gap-2 px-1.5 py-1 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
            <span>{t(($) => $.m2.rail_loading)}</span>
          </div>
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-3"
              style={{ animationDelay: `${i * 80}ms` }}
            >
              <div className="mb-2 h-3 w-[66%] rounded bg-muted/70" />
              <div className="mb-1.5 h-2.5 w-full rounded bg-muted/50" />
              <div className="h-2.5 w-[80%] rounded bg-muted/40" />
            </div>
          ))}
        </div>
      </RailShell>
    );
  }

  if (mode === "empty") {
    return (
      <RailShell className={className}>
        <RailHeader
          title={t(($) => $.m2.rail_title)}
          hint={t(($) => $.m2.rail_hint)}
        />
        <div
          data-testid="exploration-rail-empty"
          className="flex flex-1 flex-col gap-2 px-3 py-6"
        >
          <p className="text-sm font-medium text-foreground">
            {t(($) => $.m2.rail_empty_title)}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.m2.rail_empty_body)}
          </p>
        </div>
      </RailShell>
    );
  }

  return (
    <RailShell className={className}>
      <RailHeader
        title={t(($) => $.m2.rail_title)}
        hint={t(($) => $.m2.rail_hint)}
      />
      <div
        data-testid="exploration-rail-cards"
        className="min-h-0 flex-1 space-y-2 overflow-y-auto p-2"
      >
        {dimensions.map((dim) => {
          const expanded = open[dim.family] ?? dim.family === selectedFamily;
          const statusLabel = t(($) => $.m2.status[dim.status]);
          const selected = dim.family === selectedFamily;
          return (
            <article
              key={dim.family}
              data-testid="exploration-result-card"
              className={cn(
                "rounded-xl border bg-card/90 shadow-sm backdrop-blur-sm transition-[border-color,box-shadow] duration-150",
                selected
                  ? "border-brand/45 shadow-md ring-1 ring-brand/20"
                  : "border-border/60 hover:border-border",
              )}
            >
              <button
                type="button"
                className="flex w-full items-start gap-2.5 px-3 py-2.5 text-left"
                onClick={() => {
                  setOpen((s) => ({ ...s, [dim.family]: !expanded }));
                  onSelectFamily?.(dim.family);
                }}
                aria-expanded={expanded}
              >
                <span
                  className={cn(
                    "mt-1.5 size-2 shrink-0 rounded-full",
                    statusDot[dim.status],
                  )}
                  aria-hidden
                />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-sm font-semibold text-foreground">
                      {dim.title}
                    </span>
                    {dim.required ? (
                      <span className="shrink-0 rounded-md border border-border/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">
                        {t(($) => $.m2.required)}
                      </span>
                    ) : null}
                  </span>
                  <span className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    <span
                      className={cn(
                        "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
                        statusChip[dim.status],
                      )}
                    >
                      {statusLabel}
                    </span>
                    {dim.questions.length > 0 ? (
                      <span className="text-[10px] text-muted-foreground">
                        {t(($) => $.m2.rail_question_count, {
                          count: dim.questions.length,
                        })}
                      </span>
                    ) : null}
                  </span>
                  {dim.findingSummary ? (
                    <span className="mt-2 line-clamp-3 block text-[11px] leading-relaxed text-muted-foreground">
                      {dim.findingSummary}
                    </span>
                  ) : (
                    <span className="mt-2 block text-[11px] text-muted-foreground/80">
                      {t(($) => $.m2.rail_summary_pending)}
                    </span>
                  )}
                </span>
                <ChevronDown
                  className={cn(
                    "mt-1 size-3.5 shrink-0 text-muted-foreground transition-transform duration-150",
                    expanded && "rotate-180",
                  )}
                  aria-hidden
                />
              </button>
              {expanded && dim.questions.length > 0 ? (
                <ul className="space-y-1 border-t border-border/50 px-2 py-2">
                  {dim.questions.map((q) => (
                    <li key={q.id}>
                      <button
                        type="button"
                        className={cn(
                          "w-full rounded-lg px-2.5 py-2 text-left transition-colors duration-150",
                          q.id === selectedQuestionId || q.active
                            ? "bg-background/90 text-foreground shadow-sm ring-1 ring-border/60"
                            : "text-muted-foreground hover:bg-background/60 hover:text-foreground",
                        )}
                        onClick={() => onSelectQuestion?.(q.id)}
                      >
                        <span className="block truncate text-xs font-medium">
                          {q.title}
                        </span>
                        {q.summary ? (
                          <span className="mt-0.5 line-clamp-2 block text-[10px] leading-snug text-muted-foreground">
                            {q.summary}
                          </span>
                        ) : null}
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
            </article>
          );
        })}
      </div>
    </RailShell>
  );
}
