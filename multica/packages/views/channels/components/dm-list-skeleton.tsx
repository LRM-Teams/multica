"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-459 — DIRECT MESSAGES region body while `dmListOptions` is pending.
 * Use `isPending` (not `isLoading`) at call sites to avoid empty-CTA flash.
 */
export function DmListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div
      className="px-0 py-0.5"
      data-testid="dm-list-skeleton"
      aria-busy="true"
      aria-label="Loading direct messages"
    >
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          className="mb-0.5 flex items-center gap-2.5 rounded-lg px-2 py-2"
        >
          <Skeleton className="size-10 shrink-0 rounded-full" />
          <div className="min-w-0 flex-1 space-y-1.5">
            <div className="flex items-center justify-between gap-2">
              <Skeleton className="h-3.5 w-24" />
              <Skeleton className="h-2.5 w-8 shrink-0" />
            </div>
            <Skeleton className="h-3 w-4/5 max-w-[11rem]" />
          </div>
        </div>
      ))}
    </div>
  );
}
