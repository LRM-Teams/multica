"use client";

import { RefreshCw, WifiOff, TriangleAlert } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { formatClock } from "./execution-overlay-row";

/**
 * LRM-1473 G5 — global sync indicator. Distinguishes three conditions:
 *  - `disconnected`: the presence feed is unreachable (fetch/WS failure) →
 *    show the last-sync time and a retry, keep the last real rows visible
 *    (marked stale downstream, never refresh into fake states).
 *  - `expired`: rows' data is authoritative but older than the lease window
 *    (last-sync stale) → amber "data may be stale" banner.
 * The banner is never derived from animations or chat; it reflects fetch/WS
 * state and the projection timestamps only.
 */
export function ExecutionOverlaySyncIndicator({
  disconnected = false,
  expired = false,
  lastSyncedAt,
  onRetry,
  isRetrying = false,
  className,
}: {
  disconnected?: boolean;
  expired?: boolean;
  lastSyncedAt?: number;
  onRetry?: () => void;
  isRetrying?: boolean;
  className?: string;
}) {
  const { t } = useT("research");
  const muted = !disconnected && !expired;
  return (
    <div
      data-testid="execution-overlay-sync-indicator"
      data-state={muted ? "ok" : disconnected ? "disconnected" : "expired"}
      role={disconnected ? "alert" : undefined}
      aria-live="polite"
      className={cn(
        "flex min-w-0 items-center gap-2 border-b border-border/60 px-3 py-1.5 text-[11px]",
        disconnected
          ? "bg-destructive/8 text-destructive-strong"
          : expired
            ? "bg-warning/8 text-warning"
            : "text-muted-foreground",
        className,
      )}
    >
      <span className="shrink-0">
        {disconnected ? (
          <WifiOff className="size-3.5" aria-hidden="true" />
        ) : expired ? (
          <TriangleAlert className="size-3.5" aria-hidden="true" />
        ) : (
          <span className="inline-block size-1.5 rounded-full bg-success" aria-hidden="true" />
        )}
      </span>
      <span className="min-w-0 flex-1 truncate">
        {disconnected
          ? t(($) => $.panel.execution.disconnected)
          : expired
            ? t(($) => $.panel.execution.data_expired)
            : t(($) => $.panel.execution.synced)}
        {lastSyncedAt != null && !muted
          ? ` · ${t(($) => $.panel.execution.last_sync, { time: formatClock(lastSyncedAt, (f) => f) })}`
          : null}
      </span>
      {onRetry ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 shrink-0 gap-1 px-1.5 text-[11px]"
          aria-disabled={isRetrying}
          onClick={() => {
            if (!isRetrying) onRetry();
          }}
        >
          <RefreshCw className={cn("size-3", isRetrying && "animate-spin")} aria-hidden="true" />
          <span className="sr-only">{t(($) => $.panel.execution.retry)}</span>
        </Button>
      ) : null}
    </div>
  );
}
