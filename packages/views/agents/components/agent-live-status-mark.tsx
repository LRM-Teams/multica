"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import type { AgentLiveStatusView } from "../resolve-agent-live-status";

/**
 * Canonical name-row live status chip: coloured dot + localized word.
 *
 * Used by the profile hover card, DM header, agent side panel, and live
 * peek so every surface paints the same mark from `AgentLiveStatusView`
 * (dot + text-xs label). Icons / pills live elsewhere.
 */
export function AgentLiveStatusMark({
  status,
  className,
  showSkeleton = false,
}: {
  status: AgentLiveStatusView | null;
  className?: string;
  /** When status is still resolving, render a width-stable skeleton. */
  showSkeleton?: boolean;
}) {
  if (!status) {
    return showSkeleton ? (
      <Skeleton className={cn("h-3 w-14", className)} data-testid="presence-skeleton" />
    ) : null;
  }

  return (
    <span
      className={cn(
        "inline-flex min-w-0 items-center gap-1 text-xs",
        status.textClass,
        className,
      )}
      data-testid="agent-live-status"
    >
      <span
        className={cn("size-1.5 shrink-0 rounded-full", status.dotClass)}
        aria-hidden
      />
      <span className="truncate">{status.label}</span>
    </span>
  );
}
