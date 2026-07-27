"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * Suspense fallback while a route-level chunk loads.
 * Aligns LRM-628: first paint may show an explicit skeleton, never a blank
 * white panel, and never silently fall back to a sync whole-app bundle.
 */
export function RouteChunkFallback() {
  return (
    <div
      className="flex h-full min-h-0 w-full flex-col gap-2 p-4"
      aria-busy="true"
      data-testid="route-chunk-fallback"
    >
      {Array.from({ length: 8 }).map((_, i) => (
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  );
}
