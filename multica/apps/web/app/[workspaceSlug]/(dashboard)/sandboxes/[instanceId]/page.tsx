"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const SandboxDetailPage = lazyNamedRoute(
  () => import("@multica/views/sandboxes"),
  "SandboxDetailPage",
);

export default function SandboxDetailRoute({
  params,
}: {
  params: Promise<{ instanceId: string }>;
}) {
  const { instanceId } = use(params);
  return <SandboxDetailPage instanceId={instanceId} />;
}
