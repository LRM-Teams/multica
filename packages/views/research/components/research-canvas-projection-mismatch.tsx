"use client";

import { useEffect } from "react";
import { AlertCircle } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { ResearchPendingRetryButton } from "./research-pending-retry-button";

export function ResearchCanvasProjectionMismatch({
  sessionId,
  snapshotNodeCount,
  typedNodeCount,
  graphVersion = null,
  onRetry,
  retryPending = false,
}: {
  sessionId?: string;
  snapshotNodeCount: number;
  typedNodeCount: number;
  graphVersion?: number | null;
  onRetry: () => void;
  retryPending?: boolean;
}) {
  const { t } = useT("research");

  useEffect(() => {
    console.warn("[research-d5-projection-mismatch]", {
      sessionId,
      snapshotNodeCount,
      typedNodeCount,
      graphVersion,
    });
  }, [graphVersion, sessionId, snapshotNodeCount, typedNodeCount]);

  return (
    <div
      role="alert"
      data-testid="research-projection-mismatch"
      className="flex h-full flex-col items-center justify-center gap-3 px-6 py-12 text-center"
    >
      <AlertCircle className="size-6 text-destructive" aria-hidden />
      <p className="text-sm font-medium text-foreground">
        {t(($) => $.d5.canvas.projection_mismatch_title)}
      </p>
      <p className="max-w-md text-sm text-muted-foreground">
        {t(($) => $.d5.canvas.projection_mismatch_body)}
      </p>
      <p
        className="font-mono text-xs text-muted-foreground"
        data-testid="research-projection-mismatch-diagnostics"
      >
        {t(($) => $.d5.canvas.projection_mismatch_diagnostics, {
          snapshotCount: snapshotNodeCount,
          typedCount: typedNodeCount,
        })}
      </p>
      <ResearchPendingRetryButton
        label={t(($) => $.session_page.retry)}
        pendingLabel={t(($) => $.interrupt.retrying)}
        pending={retryPending}
        onRetry={onRetry}
      />
    </div>
  );
}
