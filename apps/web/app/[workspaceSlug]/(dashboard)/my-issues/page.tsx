"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const MyIssuesPage = lazyNamedRoute(
  () => import("@multica/views/my-issues"),
  "MyIssuesPage",
);

export default function Page() {
  return <MyIssuesPage />;
}
