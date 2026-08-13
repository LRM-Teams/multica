"use client";

import { AlertCircle, FileText, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { DeliveryMode } from "../lib/delivery-mode";

const modeChip: Record<DeliveryMode, string> = {
  empty: "border-border/70 bg-muted/40 text-muted-foreground",
  loading: "border-brand/35 bg-brand/10 text-brand",
  running: "border-brand/35 bg-brand/10 text-brand",
  error: "border-destructive/40 bg-destructive/10 text-destructive",
};

/** Mode chip for the Delivery modal header (LRM-993). */
export function ResearchDeliveryModeChip({ mode }: { mode: DeliveryMode }) {
  const { t } = useT("research");
  const label =
    mode === "empty"
      ? t(($) => $.panel.delivery_mode.empty)
      : mode === "loading"
        ? t(($) => $.panel.delivery_mode.loading)
        : mode === "error"
          ? t(($) => $.panel.delivery_mode.error)
          : t(($) => $.panel.delivery_mode.running);

  return (
    <>
      <span
        data-testid="research-delivery-mode"
        data-delivery-mode={mode}
        className={cn(
          "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
          modeChip[mode],
        )}
        aria-hidden
      >
        {label}
      </span>
      {/*
        LRM-1229 — delivery flips loading → running inside one mount, so the
        announcement cannot live on a mode-specific subtree (that node is
        unmounted exactly when the content becomes ready). This chip renders in
        every mode, so it hosts the one persistent live region for the modal
        (same shape as LRM-1225 chat + research-canvas). Use native <output>
        (prefer-tag-over-role) instead of role=status. Visible chip is
        aria-hidden to avoid speaking the label twice.
      */}
      <output
        data-testid="research-delivery-mode-live"
        className="sr-only"
        aria-live="polite"
        aria-atomic="true"
        aria-busy={mode === "loading"}
      >
        {label}
      </output>
    </>
  );
}

/**
 * Designed empty / loading / error bodies for Delivery (LRM-993).
 * Running mode is owned by ReportProse + source table.
 */
export function ResearchDeliveryModeBody({
  mode,
  errorMessage,
  onRetry,
}: {
  mode: Exclude<DeliveryMode, "running">;
  errorMessage?: string | null;
  onRetry?: () => void;
}) {
  const { t } = useT("research");

  if (mode === "loading") {
    return (
      <div
        data-testid="research-delivery-loading"
        className="space-y-3"
        aria-busy
      >
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin text-brand motion-reduce:animate-none" aria-hidden />
          <span>{t(($) => $.reader.loading_body)}</span>
        </div>
        {[0, 1].map((i) => (
          <div
            key={i}
            className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-4 motion-reduce:animate-none"
            style={{ animationDelay: `${i * 80}ms` }}
          >
            <div className="mb-3 h-3 w-[42%] rounded bg-muted/70" />
            <div className="mb-2 h-2.5 w-full rounded bg-muted/50" />
            <div className="mb-2 h-2.5 w-[88%] rounded bg-muted/45" />
            <div className="h-2.5 w-[64%] rounded bg-muted/40" />
          </div>
        ))}
      </div>
    );
  }

  if (mode === "error") {
    return (
      <div
        data-testid="research-delivery-error"
        role="alert"
        className="rounded-xl border border-destructive/35 bg-destructive/5 px-4 py-4"
      >
        <div className="flex items-start gap-2">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-destructive">
              {t(($) => $.reader.error_title)}
            </p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
              {errorMessage || t(($) => $.reader.error_body)}
            </p>
            {onRetry ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={onRetry}
              >
                {t(($) => $.session_page.retry)}
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      data-testid="research-delivery-empty"
      className="rounded-xl border border-border/55 bg-card/80 px-4 py-4"
    >
      <div className="mb-2 inline-flex size-9 items-center justify-center rounded-lg border border-border/55 bg-muted/40 text-muted-foreground">
        <FileText className="size-4" aria-hidden />
      </div>
      <p className="text-sm font-medium text-foreground">
        {t(($) => $.reader.empty_title)}
      </p>
      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
        {t(($) => $.reader.empty_body)}
      </p>
    </div>
  );
}
