"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** Non-blocking recovery surface when a cached canvas outlives a failed refresh. */
export function ResearchCanvasStaleNotice({
  onRetry,
  retryPending = false,
  className,
}: {
  onRetry?: () => void;
  retryPending?: boolean;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <output
      role="alert"
      data-testid="research-canvas-stale-notice"
      className={cn(
        "absolute top-3 left-1/2 z-30 flex max-w-[min(34rem,calc(100%-1.5rem))] -translate-x-1/2 items-center gap-2 rounded-lg border border-warning/45 bg-background/95 px-3 py-2 text-xs shadow-lg backdrop-blur",
        className,
      )}
    >
      <AlertTriangle className="size-4 shrink-0 text-warning" aria-hidden />
      <span className="min-w-0 flex-1">
        <strong className="font-semibold text-foreground">
          {t(($) => $.d5.canvas.stale_title)}
        </strong>{" "}
        <span className="text-muted-foreground">
          {t(($) => $.d5.canvas.stale_body)}
        </span>
      </span>
      {onRetry ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 shrink-0 px-2 text-xs"
          disabled={retryPending}
          onClick={onRetry}
          data-testid="research-canvas-stale-retry"
        >
          <RefreshCw
            className={cn("size-3.5", retryPending && "animate-spin")}
            aria-hidden
          />
          {retryPending
            ? t(($) => $.interrupt.retrying)
            : t(($) => $.session_page.retry)}
        </Button>
      ) : null}
    </output>
  );
}
