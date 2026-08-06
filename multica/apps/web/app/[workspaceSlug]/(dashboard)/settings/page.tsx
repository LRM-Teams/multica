"use client";

import { lazyNamedRoute } from "@/lib/lazy-route";

const SettingsPage = lazyNamedRoute(
  () => import("@multica/views/settings"),
  "SettingsPage",
);

export default function Page() {
  return <SettingsPage />;
}
