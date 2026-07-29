"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const ResearchListPage = lazyNamedRoute(
  () => import("@multica/views/research"),
  "ResearchListPage",
);

export default function ResearchRoute() {
  return <ResearchListPage />;
}
