"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const AgentDetailPage = lazyNamedRoute(
  () => import("@multica/views/agents"),
  "AgentDetailPage",
);

export default function AgentDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <AgentDetailPage agentId={id} />;
}
