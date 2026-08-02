"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";
import type { SourcesFailureMode } from "./report-source-degrade";

/** LRM-834 — all-failed / partial-failed banner above the source map. */
export function ReportSourcesFailureBanner({
  mode,
  failedCount,
  onRetry,
}: {
  mode: SourcesFailureMode;
  failedCount: number;
  onRetry?: () => void;
}) {
  const { t } = useT("research");
  if (mode === "none" || failedCount <= 0) return null;

  if (mode === "all") {
    return (
      <div
        data-testid="research-sources-all-failed"
        role="alert"
        className="flex flex-col gap-3 rounded-[10px] border border-destructive/40 bg-destructive/5 px-3 py-3 sm:flex-row sm:items-center sm:justify-between"
      >
        <div className="flex min-w-0 gap-2">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
          <div className="min-w-0 space-y-0.5">
            <p className="text-sm font-medium text-foreground">
              {t(($) => $.reader.sources_all_failed_title)}
            </p>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.reader.sources_all_failed_body)}
            </p>
          </div>
        </div>
        {onRetry ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            data-testid="research-sources-all-failed-retry"
            onClick={onRetry}
            className="shrink-0 self-start sm:self-auto"
          >
            <RefreshCw className="size-3.5" aria-hidden />
            {t(($) => $.reader.sources_all_failed_retry)}
          </Button>
        ) : null}
      </div>
    );
  }

  return (
    <div
      data-testid="research-sources-partial-failed"
      className="rounded-[10px] border border-warning/40 bg-warning/5 px-3 py-2 text-xs text-muted-foreground"
    >
      {t(($) => $.reader.sources_partial_failed_hint, { count: failedCount })}
    </div>
  );
}
