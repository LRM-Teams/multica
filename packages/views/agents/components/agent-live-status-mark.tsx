"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import type { AgentLiveStatusView } from "../resolve-agent-live-status";

/**
 * Canonical live / activity status chip.
 *
 * Profile / DM / side-panel headers (LRM-248): pass `showDot={false}` so the
 * avatar badge is the only round indicator; the word is plain "Online" /
 * "Offline" text. Activity composer strip keeps its fact-derived dot.
 */
export function AgentLiveStatusMark({
  status,
  className,
  showSkeleton = false,
  showDot = true,
}: {
  status: AgentLiveStatusView | null;
  className?: string;
  /** When status is still resolving, render a width-stable skeleton. */
  showSkeleton?: boolean;
  /**
   * When false, render the label only (no second round indicator next to the
   * word). Use on profile surfaces where the avatar already carries the live
   * badge (LRM-248 — no duplicate indicators).
   */
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
