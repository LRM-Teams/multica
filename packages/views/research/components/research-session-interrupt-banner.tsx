"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { SessionInterrupt } from "../lib/session-interrupt";

export type InterruptBannerPhase = "idle" | "pending" | "retry_failed";

/** LRM-823 — top session interrupt / wake_failed recovery banner. */
export function ResearchSessionInterruptBanner({
  interrupt,
  phase,
  onRetry,
  className,
}: {
  interrupt: SessionInterrupt;
  phase: InterruptBannerPhase;
  onRetry: () => void;
  className?: string;
}) {
  const { t } = useT("research");
  const reasonLabel = t(($) => {
    const reasons = $.interrupt.reasons;
    return reasons[interrupt.reason as keyof typeof reasons] ?? reasons.unknown;
  });
  const showSecondary = phase === "retry_failed";
  const pending = phase === "pending";

  return (
    <div
      role="alert"
      data-testid="research-session-interrupt-banner"
      data-reason={interrupt.reason}
      data-phase={phase}
      className={cn(
        "relative z-[2] shrink-0 border-b px-3 py-2.5 sm:px-4",
        showSecondary
          ? "border-destructive/35 bg-destructive/8"
          : "border-warning/35 bg-warning/10",
        className,
      )}
    >
      <div className="flex flex-col gap-2.5 sm:flex-row sm:items-start sm:justify-between sm:gap-3">
        <div className="flex min-w-0 gap-2">
          <AlertTriangle
            className={cn(
              "mt-0.5 size-4 shrink-0",
              showSecondary ? "text-destructive" : "text-warning",
            )}
            aria-hidden
          />
          <div className="min-w-0 space-y-0.5">
            <p className="text-sm font-semibold text-foreground">
              {showSecondary
                ? t(($) => $.interrupt.retry_failed_title)
                : t(($) => $.interrupt.title)}
            </p>
            <p
              data-testid="research-session-interrupt-reason"
              className="text-xs leading-relaxed text-foreground/90"
            >
              <span className="font-medium">{reasonLabel}</span>
              {interrupt.headline ? (
                <>
                  {" · "}
                  <span className="text-muted-foreground">{interrupt.headline}</span>
                </>
              ) : null}
            </p>
            {showSecondary ? (
              <p
                data-testid="research-session-interrupt-secondary"
                className="text-xs leading-relaxed text-muted-foreground"
              >
                {interrupt.recoveryHint?.trim()
                  ? interrupt.recoveryHint
                  : t(($) => $.interrupt.retry_failed_hint)}
              </p>
            ) : interrupt.recoveryHint?.trim() ? (
              <p className="text-xs leading-relaxed text-muted-foreground">
                {interrupt.recoveryHint}
              </p>
            ) : (
              <p className="text-xs leading-relaxed text-muted-foreground">
                {t(($) => $.interrupt.hint)}
              </p>
            )}
          </div>
        </div>
        <Button
          type="button"
          size="sm"
          variant={showSecondary ? "outline" : "default"}
          disabled={pending}
          data-testid="research-session-interrupt-retry"
          onClick={onRetry}
          className="shrink-0 self-start sm:self-auto"
        >
          <RefreshCw
            className={cn("size-3.5", pending && "animate-spin")}
            aria-hidden
          />
          {pending
            ? t(($) => $.interrupt.retrying)
            : showSecondary
              ? t(($) => $.interrupt.retry_again)
              : t(($) => $.interrupt.retry)}
        </Button>
      </div>
    </div>
  );
}
