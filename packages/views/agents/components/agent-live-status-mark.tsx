"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import type { AgentLiveStatusView } from "../resolve-agent-live-status";

/**
 * Live status word next to a name (LRM-248).
 *
 * Profile / name rows: **text only** by default — the avatar already carries
 * the corner live dot; a second dot next to「在线」is the duplicate Frank rejected.
 * Pass `showDot` for Activity-style strips that are not sitting beside an avatar.
 */
export function AgentLiveStatusMark({
  status,
  className,
  showSkeleton = false,
  showDot = false,
}: {
  status: AgentLiveStatusView | null;
  className?: string;
  /** When status is still resolving, render a width-stable skeleton. */
  showSkeleton?: boolean;
  /** Include the coloured status dot (default false — LRM-248 no duplicate). */
  showDot?: boolean;
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
      {showDot ? (
        <span
          className={cn("size-1.5 shrink-0 rounded-full", status.dotClass)}
          aria-hidden
        />
      ) : null}
      <span className="truncate">{status.label}</span>
    </span>
  );
}
