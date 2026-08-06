"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const PlanBillingPage = lazyNamedRoute(
  () => import("@multica/views/plan-billing"),
  "PlanBillingPage",
);

export default function PlanBillingRoute() {
  return <PlanBillingPage />;
}
