"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ChannelsPage = lazyNamedRoute(
  () => import("@multica/views/channels"),
  "ChannelsPage",
);

/**
 * Optional catch-all so `/channels` (list) and `/channels/[id]` (selected
 * channel or DM) resolve to the same persistent page instance — switching
 * the selection must not remount the shell (sidebar, search, drafts) the way
 * navigating between two distinct page.tsx files would. `channelPath[1]` is
 * reserved for #300's thread sub-route (`/channels/[id]/[threadId]`).
 */
export default function ChannelsRoutePage({
  params,
}: {
  params: Promise<{ channelPath?: string[] }>;
}) {
  const { channelPath } = use(params);
  return <ChannelsPage channelId={channelPath?.[0]} />;
}
