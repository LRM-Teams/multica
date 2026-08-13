"use client";

import { WifiOff, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { OfflineBannerMode } from "../lib/network-status";

/** LRM-833 — top offline / reconnect banner (keeps page content mounted). */
export function ResearchOfflineBanner({
  mode,
  onRetry,
  className,
}: {
  mode: OfflineBannerMode;
  onRetry?: () => void;
  className?: string;
}) {
  const { t } = useT("research");
  const failed = mode === "failed";
  const reconnecting = mode === "reconnecting";
  const showRetry = (failed || reconnecting) && !!onRetry;

  const body = (
    <div className="flex flex-col gap-2.5 sm:flex-row sm:items-start sm:justify-between sm:gap-3">
      <div className="flex min-w-0 gap-2">
        <WifiOff
          className={cn(
            "mt-0.5 size-4 shrink-0",
            failed ? "text-destructive" : "text-warning",
          )}
          aria-hidden
        />
        <div className="min-w-0 space-y-0.5">
          <p className="text-sm font-semibold text-foreground">
            {failed
              ? t(($) => $.connectivity.reconnect_failed_title)
              : reconnecting
                ? t(($) => $.connectivity.reconnecting_title)
                : t(($) => $.connectivity.offline_title)}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {failed
              ? t(($) => $.connectivity.reconnect_failed_hint)
              : reconnecting
                ? t(($) => $.connectivity.reconnecting_hint)
                : t(($) => $.connectivity.offline_hint)}
          </p>
        </div>
      </div>
      {showRetry ? (
        <Button
          type="button"
          size="sm"
          variant={failed ? "outline" : "default"}
          // LRM-1345 A-2 — the reconnecting control must stay a real focus target.
          // A native `disabled` on the button the user just activated drops focus to
          // <body> in Chromium and never gives it back, so keyboard / screen reader
          // users lose their place and never hear the retry outcome. Same frozen
          // pattern as LRM-1213 `research-session-interrupt-banner`: keep it
          // focusable, guard the handler.
          aria-disabled={reconnecting || undefined}
          data-testid="research-offline-banner-retry"
          onClick={() => {
            if (reconnecting) return;
            onRetry?.();
          }}
          className={cn(
            "shrink-0 self-start sm:self-auto",
            reconnecting && "opacity-50 cursor-not-allowed",
          )}
        >
          <RefreshCw
            className={cn("size-3.5", reconnecting && "animate-spin motion-reduce:animate-none")}
            aria-hidden
          />
          {reconnecting
            ? t(($) => $.connectivity.retrying)
            : t(($) => $.connectivity.retry)}
        </Button>
      ) : null}
    </div>
  );

  const shellClass = cn(
    "relative z-[3] block shrink-0 border-b px-3 py-2.5 sm:px-4",
    failed
      ? "border-destructive/35 bg-destructive/8"
      : "border-warning/35 bg-warning/10",
    className,
  );

  // LRM-1192: failed reconnect is an alert; offline/reconnecting stay polite status.
  // LRM-1232: reconnecting declares aria-busy so AT can distinguish “offline notice” vs “in progress”.
  // LRM-1345 A-1: one single return so the shell element identity is stable across
  // mode changes. The old code returned a `<div role="alert">` for failed and an
  // `<output>` otherwise; React sees a different tag, unmounts the whole subtree and
  // the focused Retry button disappears, dropping focus to <body> exactly when the
  // user retries. `<output>` maps to role="status" natively (react-doctor
  // prefer-tag-over-role), and failed upgrades the same node to role="alert".
  return (
    <output
      role={failed ? "alert" : undefined}
      data-testid="research-offline-banner"
      data-mode={mode}
      aria-busy={reconnecting || undefined}
      className={shellClass}
    >
      {body}
    </output>
  );
}
