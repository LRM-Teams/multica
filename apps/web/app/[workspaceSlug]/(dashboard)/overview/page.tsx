"use client";

import { OverviewPage } from "@multica/views/overview";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <OverviewPage />
    </ErrorBoundary>
  );
}
