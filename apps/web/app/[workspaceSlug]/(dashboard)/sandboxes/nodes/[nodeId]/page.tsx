"use client";

import { use } from "react";
import { SandboxNodeSetupPage } from "@multica/views/sandboxes";

export default function SandboxNodeSetupRoute({
  params,
}: {
  params: Promise<{ nodeId: string }>;
}) {
  const { nodeId } = use(params);
  return <SandboxNodeSetupPage nodeId={nodeId} />;
}
