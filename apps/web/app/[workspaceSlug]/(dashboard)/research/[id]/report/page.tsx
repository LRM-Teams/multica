"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ResearchV6ReportPage = lazyNamedRoute(
  () => import("@multica/views/research"),
  "ResearchV6ReportPage",
);

export default function ResearchReportRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <ResearchV6ReportPage sessionId={id} />;
}
