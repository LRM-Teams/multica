"use client";

import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";

/**
 * Session id is an ownership boundary for every local hook below it. Changing
 * the id remounts descendants so drafts, refs, mutation observers, and overlays
 * from one Research session cannot act on the next session.
 */
export function ResearchSessionBoundary({
  sessionId,
  children,
}: {
  sessionId: string;
  children: ReactNode;
}) {
  const { t } = useT("research");

  return (
    <ErrorBoundary
      key={sessionId}
      resetKeys={[sessionId]}
      fallback={({ error, reset }) => (
        <main
          className="flex h-full min-h-0 items-center justify-center px-6 py-12"
          data-testid="research-session-render-error"
        >
          <div
            role="alert"
            className="flex max-w-lg flex-col items-center gap-3 text-center"
          >
            <AlertTriangle className="size-7 text-destructive" aria-hidden />
            <div className="space-y-1.5">
              <h1 className="text-base font-semibold text-foreground">
                {t(($) => $.session_page.load_failed)}
              </h1>
              <p className="text-sm text-muted-foreground">
                {t(($) => $.session_page.load_failed_hint)}
              </p>
              {error.message ? (
                <details className="pt-1 text-left text-xs text-muted-foreground">
                  <summary className="cursor-pointer text-center">
                    {t(($) => $.session_page.technical_details)}
                  </summary>
                  <code
                    lang="en"
                    dir="ltr"
                    className="mt-2 block max-h-32 overflow-auto rounded-md bg-muted/60 p-2 whitespace-pre-wrap break-words"
                  >
                    {error.message}
                  </code>
                </details>
              ) : null}
            </div>
            <Button type="button" variant="outline" size="sm" onClick={reset}>
              {t(($) => $.session_page.retry)}
            </Button>
          </div>
        </main>
      )}
    >
      {children}
    </ErrorBoundary>
  );
}
