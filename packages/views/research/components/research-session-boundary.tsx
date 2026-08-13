"use client";

import { Component, Fragment, type ErrorInfo, type ReactNode } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { AlertCircle } from "lucide-react";
import { useT } from "../../i18n/use-t";

type ResearchSessionBoundaryProps = {
  sessionId: string;
  children: ReactNode;
  title: string;
  hint: string;
  retryLabel: string;
};

type ResearchSessionBoundaryState = {
  error: Error | null;
  retryKey: number;
};

/**
 * Session id is an ownership boundary for every local hook below it. Changing
 * the id remounts descendants so drafts, refs, mutation observers, and overlays
 * from one Research session cannot act on the next session.
 */
class ResearchSessionErrorBoundary extends Component<
  ResearchSessionBoundaryProps,
  ResearchSessionBoundaryState
> {
  state: ResearchSessionBoundaryState = { error: null, retryKey: 0 };

  static getDerivedStateFromError(error: Error): Partial<ResearchSessionBoundaryState> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Research session render failed", {
      sessionId: this.props.sessionId,
      error,
      componentStack: info.componentStack,
    });
  }

  private retry = () => {
    this.setState(({ retryKey }) => ({ error: null, retryKey: retryKey + 1 }));
  };

  render() {
    if (this.state.error) {
      return (
        <div
          role="alert"
          data-testid="research-session-render-error"
          className="flex h-full flex-col items-center justify-center gap-3 px-6 py-12 text-center"
        >
          <AlertCircle className="size-6 text-destructive" aria-hidden />
          <div className="max-w-md space-y-1.5">
            <h2 className="text-sm font-medium text-destructive">
              {this.props.title}
            </h2>
            <p className="text-sm text-muted-foreground">
              {this.props.hint}
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={this.retry}>
            {this.props.retryLabel}
          </Button>
        </div>
      );
    }

    return (
      <Fragment key={`${this.props.sessionId}:${this.state.retryKey}`}>
        {this.props.children}
      </Fragment>
    );
  }
}

export function ResearchSessionBoundary({
  sessionId,
  children,
}: Pick<ResearchSessionBoundaryProps, "sessionId" | "children">) {
  const { t } = useT("research");
  return (
    <ResearchSessionErrorBoundary
      key={sessionId}
      sessionId={sessionId}
      title={t(($) => $.session_page.load_failed)}
      hint={t(($) => $.session_page.load_failed_hint)}
      retryLabel={t(($) => $.session_page.retry)}
    >
      {children}
    </ResearchSessionErrorBoundary>
  );
}
