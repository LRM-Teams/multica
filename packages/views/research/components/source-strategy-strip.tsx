"use client";

import type { ReactNode } from "react";
import { AlertCircle, Loader2 } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type {
  SourceStrategyChip,
  SourceStrategyMode,
  SourceStrategyModel,
} from "../lib/m2-visibility";
import { resolveSourceStrategyMode } from "../lib/m2-visibility";
import { ResearchPendingRetryButton } from "./research-pending-retry-button";

/** 360px drawer → one column; wider sheets can dual-column via auto-fit. */
const CARD_GRID =
  "grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(15rem,1fr))]";

function StripHeader({
  title,
  term,
  hint,
  status,
}: {
  title: string;
  term: string;
  hint: string;
  status?: string;
}) {
  return (
    <div className="mb-2 space-y-1">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <h3 className="text-sm font-semibold tracking-tight text-foreground">
          {title}
        </h3>
        <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {term}
        </span>
      </div>
      <p className="text-[11px] leading-snug text-muted-foreground">{hint}</p>
      {status ? (
        <p
          data-testid="source-strategy-status"
          className="text-[11px] font-medium text-foreground/85"
        >
          {status}
        </p>
      ) : null}
    </div>
  );
}

function layerAccent(layer: SourceStrategyChip["layer"]) {
  return layer === "general"
    ? "border-[color:color-mix(in_oklab,var(--source-general)_45%,var(--border))]"
    : "border-[color:color-mix(in_oklab,var(--source-domain)_45%,var(--border))]";
}

function layerChipClass(layer: SourceStrategyChip["layer"]) {
  return layer === "general"
    ? "border-[color:color-mix(in_oklab,var(--source-general)_35%,transparent)] bg-[color:color-mix(in_oklab,var(--source-general)_12%,transparent)] text-[color:var(--source-general)]"
    : "border-[color:color-mix(in_oklab,var(--source-domain)_35%,transparent)] bg-[color:color-mix(in_oklab,var(--source-domain)_12%,transparent)] text-[color:var(--source-domain)]";
}

function ExpectedOutcomes({ items }: { items: string[] }) {
  return (
    <ul
      data-testid="source-strategy-expected"
      className="mt-1 space-y-1.5 rounded-xl border border-border/55 bg-card/70 px-3 py-2.5"
    >
      {items.map((item) => (
        <li
          key={item}
          className="flex gap-2 text-[11px] leading-snug text-muted-foreground"
        >
          <span
            className="mt-1.5 size-1 shrink-0 rounded-full bg-brand/70"
            aria-hidden
          />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  );
}

/**
 * Persistent shell (LRM-1201) — same contract as HumanBoundaryCard: the root and
 * the polite live region must not be replaced when the mode flips.
 */
function StripShell({
  mode,
  shell,
  title,
  term,
  hint,
  status,
  liveText,
  children,
}: {
  mode: SourceStrategyMode;
  shell: string;
  title: string;
  term: string;
  hint: string;
  status?: string;
  liveText: string;
  children: ReactNode;
}) {
  return (
    <div
      data-testid="source-strategy-strip"
      role={mode === "error" ? "alert" : undefined}
      aria-busy={mode === "loading"}
      className={shell}
    >
      <StripHeader title={title} term={term} hint={hint} status={status} />
      {/* Silent in error mode — role=alert already carries that text. */}
      <p data-testid="source-strategy-live" className="sr-only" aria-live="polite">
        {liveText}
      </p>
      {children}
    </div>
  );
}

function StrategyCard({
  chip,
  layerLabel,
  sampleCountLabel,
}: {
  chip: SourceStrategyChip;
  layerLabel: string;
  sampleCountLabel: string | null;
}) {
  return (
    <article
      data-testid="source-strategy-card"
      className={cn(
        "min-w-0 rounded-xl border bg-card/90 p-3 shadow-sm backdrop-blur-sm",
        layerAccent(chip.layer),
      )}
    >
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
        <span
          className={cn(
            "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
            layerChipClass(chip.layer),
          )}
        >
          {layerLabel}
        </span>
        <span className="min-w-0 truncate text-sm font-semibold text-foreground">
          {chip.label}
        </span>
        {sampleCountLabel ? (
          <span className="text-[10px] text-muted-foreground">{sampleCountLabel}</span>
        ) : null}
      </div>
      {chip.why ? (
        <p className="line-clamp-3 text-[11px] leading-relaxed text-muted-foreground">
          {chip.why}
        </p>
      ) : null}
      {chip.samples.length > 0 ? (
        <ul className="mt-2 space-y-1 border-t border-border/50 pt-2">
          {chip.samples.map((s) => (
            <li key={s.id} className="min-w-0 truncate text-[11px]">
              {s.url ? (
                <a
                  href={s.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="font-medium text-brand underline-offset-2 hover:underline"
                >
                  {s.title}
                </a>
              ) : (
                <span className="text-muted-foreground">{s.title}</span>
              )}
            </li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}

export function SourceStrategyStrip({
  model,
  sessionStatus,
  error,
  onRetry,
  retryPending = false,
  className,
}: {
  model: SourceStrategyModel;
  /** Session status drives empty vs in-flight loading (LRM-977). */
  sessionStatus?: string | null;
  error?: string | null;
  onRetry?: () => void;
  /** Disable retry while a refetch is in flight (LRM-1282). */
  retryPending?: boolean;
  className?: string;
}) {
  const { t } = useT("research");
  const mode = resolveSourceStrategyMode(model, sessionStatus, error);

  const shell = cn(
    "relative z-[1] border-b border-border/55 bg-background/55 px-3 py-2.5 backdrop-blur-sm",
    className,
  );

  const title = t(($) => $.m2.strategy_title);
  const term = t(($) => $.m2.strategy_label);
  const hint = t(($) => $.m2.strategy_hint);

  const statusText =
    mode === "loading"
      ? t(($) => $.m2.strategy_loading)
      : mode === "partial"
        ? t(($) => $.m2.strategy_partial)
        : mode === "ready"
          ? t(($) => $.m2.strategy_ready_status)
          : undefined;

  const liveText =
    mode === "loading"
      ? t(($) => $.m2.strategy_loading)
      : mode === "partial"
        ? t(($) => $.m2.strategy_partial)
        : mode === "ready"
          ? t(($) => $.m2.strategy_ready_live)
          : mode === "empty"
            ? t(($) => $.m2.strategy_empty_title)
            : "";

  const expected = [
    t(($) => $.m2.strategy_expect_1),
    t(($) => $.m2.strategy_expect_2),
    t(($) => $.m2.strategy_expect_3),
  ];

  const frame = (children: ReactNode) => (
    <StripShell
      mode={mode}
      shell={shell}
      title={title}
      term={term}
      hint={hint}
      status={statusText}
      liveText={liveText}
    >
      {children}
    </StripShell>
  );

  if (mode === "error") {
    return frame(
      <div
        data-testid="source-strategy-error"
        className="flex flex-col items-start gap-2 py-2"
      >
        <AlertCircle className="size-4 text-destructive" aria-hidden />
        <p className="text-sm text-destructive">
          {t(($) => $.m2.strategy_error)}
          {error ? (
            <span className="mt-1 block text-xs text-destructive/90">{error}</span>
          ) : null}
        </p>
        {onRetry ? (
          <ResearchPendingRetryButton
            label={t(($) => $.session_page.retry)}
            pendingLabel={t(($) => $.connectivity.retrying)}
            pending={retryPending}
            onRetry={onRetry}
          />
        ) : null}
      </div>,
    );
  }

  if (mode === "loading") {
    return frame(
      <div data-testid="source-strategy-loading" className="flex flex-col gap-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
          <span>{t(($) => $.m2.strategy_loading)}</span>
        </div>
        <ExpectedOutcomes items={expected} />
      </div>,
    );
  }

  if (mode === "empty") {
    return frame(
      <div data-testid="source-strategy-empty" className="flex flex-col gap-1 py-1">
        <p className="text-sm font-medium text-foreground">
          {t(($) => $.m2.strategy_empty_title)}
        </p>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.m2.strategy_empty_body)}
        </p>
      </div>,
    );
  }

  // ready | partial — keep real facts; partial never invents completeness.
  return frame(
    <>
      {model.whyLine ? (
        <p className="mb-2 line-clamp-2 text-[11px] leading-relaxed text-muted-foreground">
          <span className="text-foreground/80">{t(($) => $.m2.why_label)} </span>
          {model.whyLine}
        </p>
      ) : null}
      <div data-testid="source-strategy-cards" className={CARD_GRID}>
        {model.chips.map((chip) => (
          <StrategyCard
            key={chip.id}
            chip={chip}
            layerLabel={
              chip.layer === "general"
                ? t(($) => $.m2.layer_general)
                : t(($) => $.m2.layer_domain)
            }
            sampleCountLabel={
              chip.samples.length > 0
                ? t(($) => $.m2.strategy_sample_count, {
                    count: chip.samples.length,
                  })
                : null
            }
          />
        ))}
      </div>
    </>,
  );
}
