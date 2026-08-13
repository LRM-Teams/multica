"use client";

import { AlertCircle, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { EvidenceOverviewMode } from "../lib/m2-visibility";

/**
 * LRM-1329 — drawer-level evidence overview bar.
 *
 * Left: coverage/readiness only. Right: verification slot — without a BE
 * verification_status contract this is always the neutral
 * 「核验信息未提供」 string (never inferred from chips/count/session).
 *
 * Ready/revision sweep is a remount-keyed CSS class (no prop→state effect).
 */
export function ResearchEvidencePulse({
  mode,
  revisionKey,
  errorSummary,
  onRetry,
  retryPending = false,
  className,
}: {
  mode: EvidenceOverviewMode;
  /** Changes when real evidence facts revise — remounts one-shot sweep. */
  revisionKey: string;
  errorSummary?: string | null;
  onRetry?: () => void;
  retryPending?: boolean;
  className?: string;
}) {
  const { t } = useT("research");

  const statusText =
    mode === "loading"
      ? t(($) => $.m2.evidence_loading)
      : mode === "partial"
        ? t(($) => $.m2.evidence_partial)
        : mode === "ready"
          ? t(($) => $.m2.evidence_ready)
          : mode === "empty"
            ? t(($) => $.m2.evidence_empty_title)
            : mode === "permission"
              ? t(($) => $.m2.evidence_permission)
              : t(($) => $.m2.evidence_error);

  const verification = t(($) => $.m2.evidence_verification_unavailable);
  const liveText = mode === "error" || mode === "permission" ? "" : statusText;

  const expected = [
    t(($) => $.m2.evidence_expect_1),
    t(($) => $.m2.evidence_expect_2),
    t(($) => $.m2.evidence_expect_3),
  ];

  // Remount when ready revision changes → CSS one-shot 420ms sweep.
  const sweepMountKey =
    mode === "ready" && revisionKey.length > 0
      ? `ready:${revisionKey}`
      : "idle";

  return (
    <section
      data-testid="research-evidence-pulse"
      data-mode={mode}
      data-sweep-key={sweepMountKey}
      role={mode === "error" || mode === "permission" ? "alert" : undefined}
      aria-busy={mode === "loading" || undefined}
      className={cn(
        "relative min-w-0 overflow-hidden rounded-xl border border-border/60 bg-card/90 p-3 shadow-sm",
        mode === "ready" &&
          "border-[color:color-mix(in_oklab,var(--brand)_35%,var(--border))]",
        mode === "error" || mode === "permission"
          ? "border-destructive/40"
          : null,
        className,
      )}
    >
      <div
        key={sweepMountKey}
        aria-hidden
        data-testid="research-evidence-pulse-sweep"
        className={cn(
          "pointer-events-none absolute inset-0",
          mode === "ready" && "animate-evidence-sweep",
          mode === "ready" &&
            "shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--brand)_22%,transparent)]",
        )}
      />
      <div className="relative flex min-w-0 flex-wrap items-start justify-between gap-x-3 gap-y-1.5">
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex min-w-0 items-center gap-1.5">
            {mode === "loading" ? (
              <Loader2
                className="size-3.5 shrink-0 animate-spin text-brand motion-reduce:animate-none"
                aria-hidden
              />
            ) : mode === "error" || mode === "permission" ? (
              <AlertCircle
                className="size-3.5 shrink-0 text-destructive"
                aria-hidden
              />
            ) : (
              <span
                className={cn(
                  "mt-0.5 size-2 shrink-0 rounded-full",
                  mode === "ready" && "bg-brand",
                  mode === "partial" && "bg-brand/55",
                  mode === "empty" && "bg-muted-foreground/45",
                )}
                aria-hidden
              />
            )}
            <p
              data-testid="research-evidence-pulse-status"
              className={cn(
                "min-w-0 break-words text-[12px] font-semibold leading-snug",
                mode === "error" || mode === "permission"
                  ? "text-destructive"
                  : "text-foreground",
              )}
            >
              {statusText}
            </p>
          </div>
          {mode === "empty" ? (
            <p className="text-[11px] leading-snug text-muted-foreground">
              {t(($) => $.m2.evidence_empty_body)}
            </p>
          ) : null}
          {mode === "error" && errorSummary ? (
            <p className="text-[11px] leading-snug text-destructive/90">
              {errorSummary}
            </p>
          ) : null}
        </div>
        <p
          data-testid="research-evidence-pulse-verification"
          className="shrink-0 text-[10px] font-medium tracking-wide text-muted-foreground"
        >
          {verification}
        </p>
      </div>

      {/* Persistent polite live region — never owns error (alert does). */}
      <output
        data-testid="research-evidence-pulse-live"
        className="sr-only"
        aria-live="polite"
      >
        {liveText}
      </output>

      {mode === "loading" ? (
        <ExpectedList items={expected} />
      ) : null}

      {mode === "error" && onRetry ? (
        <div className="relative mt-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={retryPending}
            onClick={onRetry}
          >
            {t(($) => $.session_page.retry)}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

function ExpectedList({ items }: { items: string[] }) {
  return (
    <ul
      data-testid="research-evidence-pulse-expected"
      className="relative mt-2 space-y-1.5 rounded-lg border border-border/55 bg-background/60 px-2.5 py-2"
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
