"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const KnowledgeListPage = lazyNamedRoute(
  () => import("@multica/views/knowledge"),
  "KnowledgeListPage",
);

export default function WikiRoute() {
  return <KnowledgeListPage />;
}
