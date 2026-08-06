"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const EvolutionCenterPage = lazyNamedRoute(
  () => import("@multica/views/evolution"),
  "EvolutionCenterPage",
);

export default function EvolutionRoute() {
  return <EvolutionCenterPage />;
}
