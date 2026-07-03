"use client";

import { use } from "react";
import { SandboxDetailPage } from "@multica/views/sandboxes";

export default function SandboxDetailRoute({
  params,
}: {
  params: Promise<{ instanceId: string }>;
}) {
  const { instanceId } = use(params);
  return <SandboxDetailPage instanceId={instanceId} />;
}
