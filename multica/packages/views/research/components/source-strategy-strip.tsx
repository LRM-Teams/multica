"use client";

import { AlertCircle, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { SourceStrategyChip, SourceStrategyModel } from "../lib/m2-visibility";
import { resolveSourceStrategyMode } from "../lib/m2-visibility";

function StripHeader({ title, hint }: { title: string; hint: string }) {
  return (
    <div className="mb-2 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <h3 className="text-xs font-semibold tracking-wide text-foreground uppercase">
        {title}
      </h3>
      <p className="text-[10px] leading-snug text-muted-foreground">{hint}</p>
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

export function SourceStrategyStrip({
  model,
  sessionStatus,
  error,
  onRetry,
  className,
}: {
  model: SourceStrategyModel;
  /** Session status drives empty vs in-flight loading (LRM-977). */
  sessionStatus?: string | null;
  error?: string | null;
  onRetry?: () => void;
  className?: string;
}) {
  const { t } = useT("research");
  const mode = resolveSourceStrategyMode(model, sessionStatus, error);

  const shell = cn(
    "relative z-[1] border-b border-border/55 bg-background/55 px-3 py-2.5 backdrop-blur-sm",
    className,
  );

  if (mode === "error") {
    return (
      <div
        data-testid="source-strategy-strip"
        role="alert"
        className={shell}
      >
        <StripHeader
          title={t(($) => $.m2.strategy_label)}
          hint={t(($) => $.m2.strategy_hint)}
        />
        <div
          data-testid="source-strategy-error"
          className="flex flex-col items-start gap-2 py-2"
        >
          <AlertCircle className="size-4 text-destructive" aria-hidden />
          <p className="text-sm text-destructive">
            {error || t(($) => $.m2.strategy_error)}
          </p>
          {onRetry ? (
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              {t(($) => $.session_page.retry)}
            </Button>
          ) : null}
        </div>
      </div>
    );
  }

  if (mode === "loading") {
    return (
      <div
        data-testid="source-strategy-strip"
        className={shell}
        aria-busy
        aria-live="polite"
      >
        <StripHeader
          title={t(($) => $.m2.strategy_label)}
          hint={t(($) => $.m2.strategy_hint)}
        />
        <div
          data-testid="source-strategy-loading"
          className="flex flex-col gap-2"
        >
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
            <span>{t(($) => $.m2.strategy_loading)}</span>
          </div>
          <div className="grid gap-2 sm:grid-cols-3">
            {[0, 1, 2].map((i) => (
              <div
                key={i}
                className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-3"
                style={{ animationDelay: `${i * 80}ms` }}
              >
                <div className="mb-2 h-3 w-[40%] rounded bg-muted/70" />
                <div className="mb-1.5 h-2.5 w-full rounded bg-muted/50" />
                <div className="h-2.5 w-[70%] rounded bg-muted/40" />
              </div>
            ))}
          </div>
        </div>
      </div>
    );
  }

  if (mode === "empty") {
    return (
      <div data-testid="source-strategy-strip" className={shell}>
        <StripHeader
          title={t(($) => $.m2.strategy_label)}
          hint={t(($) => $.m2.strategy_hint)}
        />
        <div data-testid="source-strategy-empty" className="flex flex-col gap-1 py-1">
          <p className="text-sm font-medium text-foreground">
            {t(($) => $.m2.strategy_empty_title)}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.m2.strategy_empty_body)}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div data-testid="source-strategy-strip" className={shell}>
      <StripHeader
        title={t(($) => $.m2.strategy_label)}
        hint={t(($) => $.m2.strategy_hint)}
      />
      {model.whyLine ? (
        <p className="mb-2 line-clamp-2 text-[11px] leading-relaxed text-muted-foreground">
          <span className="text-foreground/80">{t(($) => $.m2.why_label)} </span>
          {model.whyLine}
        </p>
      ) : null}
      <div
        data-testid="source-strategy-cards"
        className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3"
      >
        {model.chips.map((chip) => (
          <article
            key={chip.id}
            data-testid="source-strategy-card"
            className={cn(
              "rounded-xl border bg-card/90 p-3 shadow-sm backdrop-blur-sm",
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
                {chip.layer === "general"
                  ? t(($) => $.m2.layer_general)
                  : t(($) => $.m2.layer_domain)}
              </span>
              <span className="truncate text-sm font-semibold text-foreground">
                {chip.label}
              </span>
              {chip.samples.length > 0 ? (
                <span className="text-[10px] text-muted-foreground">
                  {t(($) => $.m2.strategy_sample_count, {
                    count: chip.samples.length,
                  })}
                </span>
              ) : null}
            </div>
            <p className="line-clamp-3 text-[11px] leading-relaxed text-muted-foreground">
              {chip.why || t(($) => $.m2.strategy_summary_pending)}
            </p>
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
        ))}
      </div>
    </div>
  );
}
