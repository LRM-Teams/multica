"use client";

import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { lazyNamedRoute } from "@/lib/lazy-route";

const OverviewPage = lazyNamedRoute(
  () => import("@multica/views/overview"),
  "OverviewPage",
);

export default function Page() {
  return (
    <ErrorBoundary>
      <OverviewPage />
    </ErrorBoundary>
  );
}
