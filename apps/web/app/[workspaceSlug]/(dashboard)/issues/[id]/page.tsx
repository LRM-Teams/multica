"use client";

import { use } from "react";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { lazyNamedRoute } from "@/lib/lazy-route";

const IssueDetail = lazyNamedRoute(
  () => import("@multica/views/issues/components"),
  "IssueDetail",
);

export default function IssueDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <ErrorBoundary resetKeys={[id]}>
      <IssueDetail issueId={id} />
    </ErrorBoundary>
  );
}
