"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const RuntimesPage = lazyNamedRoute(
  () => import("@multica/views/runtimes"),
  "RuntimesPage",
);

const cloudRuntimeEnabled =
  process.env.NEXT_PUBLIC_ENABLE_CLOUD_RUNTIME === "true";

export default function ComputersRoute() {
  return <RuntimesPage cloudRuntimeEnabled={cloudRuntimeEnabled} />;
}
