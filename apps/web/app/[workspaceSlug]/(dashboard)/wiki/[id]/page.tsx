"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const KnowledgePageView = lazyNamedRoute(
  () => import("@multica/views/knowledge"),
  "KnowledgePageView",
);

export default function WikiDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <KnowledgePageView pageId={id} />;
}
