"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const RuntimeDetailPage = lazyNamedRoute(
  () => import("@multica/views/runtimes"),
  "RuntimeDetailPage",
);

export default function RuntimeDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <RuntimeDetailPage runtimeId={id} />;
}
