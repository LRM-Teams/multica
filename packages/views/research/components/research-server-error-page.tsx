"use client";

import { AlertCircle, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** LRM-833 — full-page 5xx surface with a clear retry CTA (no white screen). */
export function ResearchServerErrorPage({
  onRetry,
  message,
  retrying,
  className,
}: {
  onRetry: () => void;
  message?: string | null;
  retrying?: boolean;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <div
      role="alert"
      data-testid="research-server-error-page"
      className={cn(
        "flex h-full min-h-[320px] flex-col items-center justify-center gap-4 px-6 py-12 text-center",
        className,
      )}
    >
      <div className="flex size-12 items-center justify-center rounded-full bg-destructive/10">
        <AlertCircle className="size-6 text-destructive" aria-hidden />
      </div>
      <div className="max-w-md space-y-1.5">
        <h2 className="text-base font-medium text-foreground">
          {t(($) => $.connectivity.server_error_title)}
        </h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {t(($) => $.connectivity.server_error_hint)}
        </p>
        {message?.trim() ? (
          <details
            data-testid="research-server-error-diagnostics"
            className="pt-1 text-left text-xs text-muted-foreground"
          >
            <summary className="cursor-pointer text-center">
              {t(($) => $.connectivity.technical_details)}
            </summary>
            <code
              lang="en"
              dir="ltr"
              className="mt-2 block max-h-24 overflow-auto rounded-md bg-muted/60 p-2 whitespace-pre-wrap break-words"
            >
              {message}
            </code>
          </details>
        ) : null}
      </div>
      <Button
        type="button"
        variant="default"
        size="sm"
        aria-disabled={retrying || undefined}
        className={retrying ? "cursor-not-allowed opacity-50" : undefined}
        data-testid="research-server-error-retry"
        onClick={() => {
          if (retrying) return;
          onRetry();
        }}
      >
        <RefreshCw
          className={cn("size-3.5", retrying && "animate-spin motion-reduce:animate-none")}
          aria-hidden
        />
        {retrying
          ? t(($) => $.connectivity.retrying)
          : t(($) => $.connectivity.retry)}
      </Button>
    </div>
  );
}
