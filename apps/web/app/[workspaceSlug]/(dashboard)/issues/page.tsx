"use client";

import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { lazyNamedRoute } from "@/lib/lazy-route";

const IssuesPage = lazyNamedRoute(
  () => import("@multica/views/issues/components"),
  "IssuesPage",
);

export default function Page() {
  return (
    <ErrorBoundary>
      <IssuesPage />
    </ErrorBoundary>
  );
}
