"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const SkillsPage = lazyNamedRoute(
  () => import("@multica/views/skills"),
  "SkillsPage",
);

export default function SkillsRoute() {
  return <SkillsPage />;
}
