"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const SandboxNodeSetupPage = lazyNamedRoute(
  () => import("@multica/views/sandboxes"),
  "SandboxNodeSetupPage",
);

export default function SandboxNodeSetupRoute({
  params,
}: {
  params: Promise<{ nodeId: string }>;
}) {
  const { nodeId } = use(params);
  return <SandboxNodeSetupPage nodeId={nodeId} />;
}
