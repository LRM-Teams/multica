"use client";

import dynamic from "next/dynamic";
import type { ComponentType } from "react";
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

/**
 * Route-level code-split helper (LRM-639). Wraps `next/dynamic` so each
 * dashboard page pulls its view module in a separate async chunk instead of
 * a static sync import into the route entry.
 *
 * Loader modules may mix components with helpers (barrels); we only require
 * the named export to be a component at runtime — missing export throws
 * (no silent whole-bundle fallback, LRM-238).
 */
// Props vary per view export; callers keep normal JSX checking at use sites
// via the concrete props they pass (dynamic() erases named-export generics).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function lazyNamedRoute(
  loader: () => Promise<Record<string, unknown>>,
  exportName: string,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
): ComponentType<any> {
  return dynamic(
    () =>
      loader().then((mod) => {
        const Comp = mod[exportName];
        if (typeof Comp !== "function") {
          throw new Error(
            `lazyNamedRoute: "${exportName}" missing from module — no silent whole-bundle fallback`,
          );
        }
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        return { default: Comp as ComponentType<any> };
      }),
    { loading: () => <RouteChunkFallback /> },
  );
}
