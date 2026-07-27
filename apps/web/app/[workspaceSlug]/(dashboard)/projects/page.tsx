"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const ProjectsPage = lazyNamedRoute(
  () => import("@multica/views/projects/components"),
  "ProjectsPage",
);

export default function Page() {
  return <ProjectsPage />;
}
