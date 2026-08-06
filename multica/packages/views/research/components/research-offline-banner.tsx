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

  return (
    // `<output>` maps to role="status" — keep native tag (react-doctor prefer-tag-over-role).
    <output
      data-testid="research-offline-banner"
      data-mode={mode}
      className={cn(
        "relative z-[3] block shrink-0 border-b px-3 py-2.5 sm:px-4",
        failed
          ? "border-destructive/35 bg-destructive/8"
          : "border-warning/35 bg-warning/10",
        className,
      )}
    >
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
            disabled={reconnecting}
            data-testid="research-offline-banner-retry"
            onClick={onRetry}
            className="shrink-0 self-start sm:self-auto"
          >
            <RefreshCw
              className={cn("size-3.5", reconnecting && "animate-spin")}
              aria-hidden
            />
            {reconnecting
              ? t(($) => $.connectivity.retrying)
              : t(($) => $.connectivity.retry)}
          </Button>
        ) : null}
      </div>
    </output>
  );
}
