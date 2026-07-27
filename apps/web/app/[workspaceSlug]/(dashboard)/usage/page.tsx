"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const DashboardPage = lazyNamedRoute(
  () => import("@multica/views/dashboard"),
  "DashboardPage",
);

export default function UsageRoute() {
  return <DashboardPage />;
}
