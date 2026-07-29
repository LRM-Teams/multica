"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ResearchSessionPage = lazyNamedRoute(
  () => import("@multica/views/research"),
  "ResearchSessionPage",
);

export default function ResearchSessionRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <ResearchSessionPage sessionId={id} />;
}
