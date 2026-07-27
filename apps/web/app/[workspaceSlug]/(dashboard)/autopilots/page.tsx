"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const AutopilotsPage = lazyNamedRoute(
  () => import("@multica/views/autopilots/components"),
  "AutopilotsPage",
);

export default function Page() {
  return <AutopilotsPage />;
}
