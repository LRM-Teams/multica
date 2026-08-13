"use client";

import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

export function ResearchPendingRetryButton({
  label,
  pendingLabel,
  pending = false,
  onRetry,
  className,
  testId,
}: {
  label: string;
  pendingLabel: string;
  pending?: boolean;
  onRetry: () => void;
  className?: string;
  testId?: string;
}) {
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      aria-disabled={pending || undefined}
      aria-busy={pending || undefined}
      className={cn(pending && "cursor-not-allowed opacity-50", className)}
      data-testid={testId}
      onClick={() => {
        if (pending) return;
        onRetry();
      }}
    >
      {pending ? pendingLabel : label}
    </Button>
  );
}
