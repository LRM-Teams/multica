"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/** Row skeletons for Activity list — never replace the page chrome (LRM-424). */
export function ActivityListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div
      className="space-y-1 p-2"
      data-testid="activity-list-skeleton"
      aria-busy="true"
      aria-label="Loading activity"
    >
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-4 py-2.5">
          <Skeleton className="h-7 w-7 shrink-0 rounded-full" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </div>
      ))}
    </div>
  );
}
