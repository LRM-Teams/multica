"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const InboxPage = lazyNamedRoute(
  () => import("@multica/views/inbox"),
  "InboxPage",
);

export default function InboxRoute() {
  return <InboxPage />;
}
