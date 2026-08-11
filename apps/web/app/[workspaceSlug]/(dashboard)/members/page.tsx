"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const MembersDirectoryPage = lazyNamedRoute(
  () => import("@multica/views/members"),
  "MembersDirectoryPage",
);

export default function MembersRoute() {
  return <MembersDirectoryPage />;
}
