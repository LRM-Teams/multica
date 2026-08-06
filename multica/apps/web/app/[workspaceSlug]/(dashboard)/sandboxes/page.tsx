"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const SandboxesPage = lazyNamedRoute(
  () => import("@multica/views/sandboxes"),
  "SandboxesPage",
);

export default function Page() {
  return <SandboxesPage />;
}
