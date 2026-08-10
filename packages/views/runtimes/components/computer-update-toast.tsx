"use client";

import type { ReactNode } from "react";
import { ArrowUpCircle, Loader2, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

export type ComputerUpdateToastPhase =
  | "prompt"
  | "updating"
  | "success"
  | "failed";

export const computerUpdateToastOptions = {
  duration: Infinity,
  position: "top-right" as const,
  unstyled: true,
};

export const computerUpdateSuccessToastOptions = {
  duration: 4000,
  position: "top-right" as const,
  unstyled: true,
};

export function ComputerUpdateToast({
  phase,
  title,
  versionLine,
  progressLabel,
  errorLabel,
  updateLabel,
  laterLabel,
  retryLabel,
  dismissLabel,
  onUpdate,
  onLater,
  onRetry,
  onDismiss,
  busy = false,
}: {
  phase: ComputerUpdateToastPhase;
  title: string;
  versionLine?: string | null;
  progressLabel?: string | null;
  errorLabel?: string | null;
  updateLabel: string;
  laterLabel: string;
  retryLabel: string;
  dismissLabel: string;
  onUpdate?: () => void;
  onLater?: () => void;
  onRetry?: () => void;
  onDismiss?: () => void;
  busy?: boolean;
}) {
  const showActions = phase === "prompt" || phase === "failed";
  const meta: ReactNode =
    phase === "updating"
      ? progressLabel
      : phase === "failed"
        ? errorLabel
        : versionLine;

  return (
    <output
      data-testid="computer-update-toast"
      data-phase={phase}
      className="relative flex w-[min(360px,calc(100vw-1.5rem))] flex-col gap-2.5 overflow-hidden rounded-xl border border-border/80 bg-popover/95 p-3 pr-10 text-popover-foreground shadow-[0_16px_48px_-20px_rgba(15,23,42,0.45)] backdrop-blur-xl"
    >
      <div className="flex items-start gap-3">
        <span
          className={cn(
            "grid size-10 shrink-0 place-items-center rounded-xl border border-border/70 bg-muted/65",
            phase === "failed" && "border-destructive/40 bg-destructive/10",
            phase === "success" && "border-success/40 bg-success/10",
          )}
        >
          {phase === "updating" ? (
            <Loader2
              className="size-5 animate-spin text-brand"
              aria-hidden="true"
            />
          ) : (
            <ArrowUpCircle
              className={cn(
                "size-5 text-warning",
                phase === "success" && "text-success",
                phase === "failed" && "text-destructive",
              )}
              aria-hidden="true"
            />
          )}
        </span>

        <span className="min-w-0 flex-1 pt-0.5">
          <span className="block text-sm font-semibold leading-5">{title}</span>
          {meta ? (
            <span className="mt-0.5 block text-[11px] leading-4 text-muted-foreground">
              {meta}
            </span>
          ) : null}
        </span>
      </div>

      {showActions ? (
        <div className="flex items-center justify-end gap-2">
          {phase === "prompt" ? (
            <>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 px-2.5 text-xs"
                onClick={onLater}
                disabled={busy}
              >
                {laterLabel}
              </Button>
              <Button
                type="button"
                size="sm"
                className="h-7 px-2.5 text-xs"
                onClick={onUpdate}
                disabled={busy}
              >
                {updateLabel}
              </Button>
            </>
          ) : (
            <>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 px-2.5 text-xs"
                onClick={onLater}
                disabled={busy}
              >
                {laterLabel}
              </Button>
              <Button
                type="button"
                size="sm"
                className="h-7 px-2.5 text-xs"
                onClick={onRetry}
                disabled={busy}
              >
                {retryLabel}
              </Button>
            </>
          )}
        </div>
      ) : null}

      {onDismiss && phase !== "updating" ? (
        <button
          type="button"
          aria-label={dismissLabel}
          onClick={onDismiss}
          className="absolute right-2 top-2 grid size-6 place-items-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="size-3.5" aria-hidden="true" />
        </button>
      ) : null}
    </output>
  );
}
