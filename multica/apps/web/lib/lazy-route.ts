"use client";

import dynamic from "next/dynamic";
import type { ComponentType } from "react";
import { RouteChunkFallback } from "./route-chunk-fallback";

/**
 * Route-level code-split helper (LRM-639). Wraps `next/dynamic` so each
 * dashboard page pulls its view module in a separate async chunk instead of
 * a static sync import into the route entry.
 *
 * Loader modules may mix components with helpers (barrels); we only require
 * the named export to be a component at runtime — missing export throws
 * (no silent whole-bundle fallback, LRM-238).
 *
 * Props vary per view export; callers keep normal JSX checking at use sites
 * via the concrete props they pass (dynamic() erases named-export generics).
 */
export function lazyNamedRoute(
  loader: () => Promise<Record<string, unknown>>,
  exportName: string,
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
        return { default: Comp as ComponentType<any> };
      }),
    { loading: RouteChunkFallback },
  );
}
