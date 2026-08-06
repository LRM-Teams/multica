"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const MemberDetailPage = lazyNamedRoute(
  () => import("@multica/views/members"),
  "MemberDetailPage",
);

export default function MemberDetailRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <MemberDetailPage userId={id} />;
}
