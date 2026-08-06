"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const AutopilotDetailPage = lazyNamedRoute(
  () => import("@multica/views/autopilots/components"),
  "AutopilotDetailPage",
);

export default function Page({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <AutopilotDetailPage autopilotId={id} />;
}
