"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const SkillDetailPage = lazyNamedRoute(
  () => import("@multica/views/skills"),
  "SkillDetailPage",
);

export default function SkillDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <SkillDetailPage skillId={id} />;
}
