"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-459 — CHANNELS region body while `channelsOptions` is pending.
 * Use `isPending` (not `isLoading`) at call sites to avoid empty-CTA flash.
 */
export function ChannelListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div
      className="px-0 py-0.5"
      data-testid="channel-list-skeleton"
      aria-busy="true"
      aria-label="Loading channels"
    >
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          className="mb-0.5 flex items-center gap-2.5 rounded-lg px-2 py-2"
        >
          <div className="flex min-w-0 flex-1 items-center gap-1">
            <Skeleton className="size-3.5 shrink-0 rounded-sm" />
            <Skeleton className="h-3.5 w-28" />
          </div>
        </div>
      ))}
    </div>
  );
}
