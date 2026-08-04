"use client";

import { useId, useState, type ReactNode } from "react";
import { AlertCircle, ChevronDown, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type {
  ExplorationDimension,
  ExplorationRailMode,
  DimensionStatus,
} from "../lib/m2-visibility";
import { resolveExplorationRailMode } from "../lib/m2-visibility";

const statusDot: Record<DimensionStatus, string> = {
  open: "bg-brand",
  covered: "bg-success",
  gap: "bg-warning",
  dead: "bg-destructive",
};

const statusChip: Record<DimensionStatus, string> = {
  open: "border-brand/35 bg-brand/10 text-brand",
  covered: "border-success/35 bg-success/10 text-success-strong",
  gap: "border-warning/40 bg-warning/10 text-foreground",
  dead: "border-destructive/35 bg-destructive/10 text-destructive",
};

function isSessionCompleted(sessionStatus?: string | null): boolean {
  return (
    sessionStatus === "completed" ||
    sessionStatus === "archived" ||
    sessionStatus === "done"
  );
}

function RailHeader({
  title,
  hint,
  titleId,
}: {
  title: string;
  hint: string;
  /** LRM-1172: id for <aside aria-labelledby>. */
  titleId: string;
}) {
  return (
    <div className="border-b border-border/55 px-3 py-2.5">
      <h3
        id={titleId}
        className="text-xs font-semibold tracking-wide text-foreground uppercase"
      >
        {title}
      </h3>
      <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
        {hint}
      </p>
    </div>
  );
}

function RailShell({
  className,
  labelledBy,
  mode,
  liveText,
  children,
}: {
  className?: string;
  labelledBy: string;
  /** LRM-1201: busy flag + polite region must persist across mode swaps. */
  mode: ExplorationRailMode;
  liveText: string;
  children: ReactNode;
}) {
  return (
    <aside
      data-testid="exploration-rail"
      aria-labelledby={labelledBy}
      aria-busy={mode === "loading"}
      className={cn(
        "relative z-[1] flex w-[300px] max-w-full shrink-0 flex-col overflow-hidden border-r border-border/55 bg-background/55 backdrop-blur-sm",
        className,
      )}
    >
      {/* Stays empty in error mode — that text already sits in role=alert. */}
      <p
        data-testid="exploration-rail-live"
        className="sr-only"
        aria-live="polite"
      >
        {liveText}
      </p>
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

  const dimensionResultText = (dim: ExplorationDimension): string => {
    switch (dim.status) {
      case "open":
        return dim.findingSummary?.trim()
          ? dim.findingSummary
          : t(($) => $.m2.rail_result_open);
      case "covered":
        return dim.findingSummary?.trim()
          ? dim.findingSummary
          : t(($) => $.m2.rail_result_covered_fallback);
      case "gap":
        return dim.findingSummary?.trim()
          ? dim.findingSummary
          : t(($) => $.m2.rail_result_gap);
      case "dead":
        return t(($) => $.m2.rail_result_dead);
      default:
        return "";
    }
  };

  const nextStepHint = (dim: ExplorationDimension): string | null => {
    const count = dim.questions.length;
    if (count <= 0) return null;
    switch (dim.status) {
      case "covered":
        return t(($) => $.m2.rail_next_expand_covered, { count });
      case "gap":
        return t(($) => $.m2.rail_next_expand_gap, { count });
      case "dead":
        return t(($) => $.m2.rail_next_expand_dead, { count });
      default:
        return t(($) => $.m2.rail_question_count, { count });
    }
  };

  const buildTrailSummary = (
    dims: ExplorationDimension[],
    done: boolean,
  ): string => {
    const n = dims.length;
    const adopted = dims.filter((d) => d.status === "covered").length;
    const dead = dims.filter((d) => d.status === "dead").length;
    if (done) {
      const parts: string[] = [];
      if (n > 0) {
        parts.push(t(($) => $.m2.rail_completed_directions, { count: n }));
      }
      if (adopted > 0) {
        parts.push(t(($) => $.m2.rail_completed_findings, { count: adopted }));
      }
      return parts.join(t(($) => $.m2.rail_summary_joiner));
    }
    const parts: string[] = [];
    if (n > 0) {
      parts.push(t(($) => $.m2.rail_summary_verified, { count: n }));
    }
    if (adopted > 0) {
      parts.push(t(($) => $.m2.rail_summary_adopted, { count: adopted }));
    }
    if (dead > 0) {
      parts.push(t(($) => $.m2.rail_summary_dead, { count: dead }));
    }
    return parts.join(t(($) => $.m2.rail_summary_joiner));
  };

  const titleId = useId();
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const mode = resolveExplorationRailMode(dimensions, sessionStatus, error);
  const completed = isSessionCompleted(sessionStatus);
  const title = t(($) => $.m2.rail_title);
  const hint = t(($) => $.m2.rail_hint);
  const liveText =
    mode === "loading"
      ? t(($) => $.m2.rail_loading)
      : mode === "ready"
        ? completed
          ? t(($) => $.m2.rail_completed_banner)
          : t(($) => $.m2.rail_ready_live)
        : mode === "empty"
          ? t(($) => $.m2.rail_empty_title)
          : "";

  // LRM-1281: raw error (incl. inbox_task_failed) never enters DOM / aria / title / tooltip.
  if (mode === "error") {
    return (
      <RailShell
        className={className}
        labelledBy={titleId}
        mode={mode}
        liveText={liveText}
      >
        <RailHeader title={title} hint={hint} titleId={titleId} />
        <div
          role="alert"
          data-testid="exploration-rail-error"
          className="flex flex-1 flex-col items-start gap-3 px-3 py-6"
        >
          <AlertCircle className="size-5 text-destructive" aria-hidden />
          <div className="space-y-1">
            <p className="text-sm font-medium text-destructive">
              {t(($) => $.m2.rail_error_title)}
            </p>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {t(($) => $.m2.rail_error_body)}
            </p>
          </div>
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
      <RailShell
        className={className}
        labelledBy={titleId}
        mode={mode}
        liveText={liveText}
      >
        <RailHeader title={title} hint={hint} titleId={titleId} />
        <div
          data-testid="exploration-rail-loading"
          className="flex flex-1 flex-col gap-2 p-2"
        >
          <div className="mb-1 space-y-1 px-1.5 py-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2
                className="size-3.5 animate-spin text-brand motion-reduce:animate-none"
                aria-hidden
              />
              <span>{t(($) => $.m2.rail_loading)}</span>
            </div>
            <p className="text-[10px] leading-snug text-muted-foreground">
              {t(($) => $.m2.rail_loading_hint)}
            </p>
          </div>
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-3 motion-reduce:animate-none"
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
      <RailShell
        className={className}
        labelledBy={titleId}
        mode={mode}
        liveText={liveText}
      >
        <RailHeader title={title} hint={hint} titleId={titleId} />
        <div
          data-testid="exploration-rail-empty"
          className="flex flex-1 flex-col gap-3 px-3 py-6"
        >
          <p className="text-sm font-medium text-foreground">
            {t(($) => $.m2.rail_empty_title)}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.m2.rail_empty_body)}
          </p>
          <ul
            data-testid="exploration-rail-empty-expect"
            className="mt-1 space-y-2"
          >
            {[
              t(($) => $.m2.rail_empty_expect_verified),
              t(($) => $.m2.rail_empty_expect_gap),
              t(($) => $.m2.rail_empty_expect_reuse),
            ].map((label) => (
              <li
                key={label}
                className="rounded-lg border border-border/50 bg-card/60 px-2.5 py-2 text-[11px] leading-snug text-muted-foreground"
              >
                {label}
              </li>
            ))}
          </ul>
        </div>
      </RailShell>
    );
  }

  const summaryLine = buildTrailSummary(dimensions, completed);

  return (
    <RailShell
      className={className}
      labelledBy={titleId}
      mode={mode}
      liveText={liveText}
    >
      <RailHeader title={title} hint={hint} titleId={titleId} />
      {summaryLine || completed ? (
        <div
          data-testid="exploration-rail-summary"
          className="border-b border-border/45 px-3 py-2"
        >
          {completed ? (
            <p className="text-xs font-medium text-foreground">
              {t(($) => $.m2.rail_completed_banner)}
            </p>
          ) : null}
          {summaryLine ? (
            <p
              className={cn(
                "text-[11px] leading-snug text-muted-foreground",
                completed && "mt-0.5",
              )}
            >
              {summaryLine}
            </p>
          ) : null}
        </div>
      ) : null}
      <div
        data-testid="exploration-rail-cards"
        className="min-h-0 flex-1 space-y-2 overflow-x-hidden overflow-y-auto p-2"
      >
        {dimensions.map((dim) => {
          const expanded = open[dim.family] ?? dim.family === selectedFamily;
          const statusLabel = t(($) => $.m2.status[dim.status]);
          const selected = dim.family === selectedFamily;
          const resultText = dimensionResultText(dim);
          const nextHint = nextStepHint(dim);
          const accessibleName = `${dim.title}, ${statusLabel}, ${resultText}`;
          return (
            <article
              key={dim.family}
              data-testid="exploration-result-card"
              data-status={dim.status}
              className={cn(
                "rounded-xl border bg-card/90 p-0 shadow-sm backdrop-blur-sm transition-[border-color,box-shadow] duration-150",
                selected
                  ? "border-brand/45 shadow-md ring-1 ring-brand/20"
                  : "border-border/60 hover:border-border",
              )}
            >
              <button
                type="button"
                className="flex w-full items-start gap-2.5 px-3 py-3 text-left"
                onClick={() => {
                  setOpen((s) => ({ ...s, [dim.family]: !expanded }));
                  onSelectFamily?.(dim.family);
                }}
                aria-expanded={expanded}
                aria-label={accessibleName}
              >
                <span
                  className={cn(
                    "mt-1.5 size-2 shrink-0 rounded-full",
                    statusDot[dim.status],
                    dim.status === "open" &&
                      expanded &&
                      "animate-pulse motion-reduce:animate-none",
                  )}
                  aria-hidden
                />
                <span className="min-w-0 flex-1">
                  {/* Direction → result → next */}
                  <span className="flex items-start gap-1.5">
                    <span
                      className={cn(
                        "text-sm font-semibold text-foreground",
                        expanded ? "whitespace-normal" : "line-clamp-2",
                      )}
                    >
                      {dim.title}
                    </span>
                    {dim.required ? (
                      <span className="mt-0.5 shrink-0 rounded-md border border-border/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">
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
                  </span>
                  <span
                    data-testid="exploration-result-body"
                    className="mt-2 block text-[11px] leading-relaxed text-muted-foreground"
                  >
                    <span className="font-medium text-foreground/80">
                      {t(($) => $.m2.rail_result_prefix)}
                    </span>
                    {resultText}
                    {dim.status === "dead" && dim.findingSummary?.trim() ? (
                      <>
                        {" "}
                        {t(($) => $.m2.rail_result_dead_reason, {
                          reason: dim.findingSummary.trim(),
                        })}
                      </>
                    ) : null}
                  </span>
                  {nextHint && !expanded ? (
                    <span className="mt-1.5 block text-[10px] leading-snug text-muted-foreground">
                      {nextHint}
                    </span>
                  ) : null}
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
                        <span className="block line-clamp-2 text-xs font-medium">
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
              {expanded && dim.status === "dead" ? (
                <div className="border-t border-border/50 px-3 py-2">
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs text-muted-foreground"
                    data-testid="exploration-rail-collapse"
                    onClick={() =>
                      setOpen((s) => ({ ...s, [dim.family]: false }))
                    }
                  >
                    {t(($) => $.m2.rail_collapse)}
                  </Button>
                </div>
              ) : null}
            </article>
          );
        })}
      </div>
    </RailShell>
  );
}
