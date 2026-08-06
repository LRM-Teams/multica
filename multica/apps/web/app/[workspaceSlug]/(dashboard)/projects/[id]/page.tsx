"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ProjectDetail = lazyNamedRoute(
  () => import("@multica/views/projects/components"),
  "ProjectDetail",
);

export default function ProjectDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <ProjectDetail projectId={id} />;
}
