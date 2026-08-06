"use client";

import { use } from "react";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ActorProfilePage = lazyNamedRoute(
  () => import("@multica/views/common/actor-profile-page"),
  "ActorProfilePage",
);

export default function ActorProfileRoute({
  params,
}: {
  params: Promise<{ memberType: string; memberId: string }>;
}) {
  const { memberType, memberId } = use(params);
  // The path builder only ever emits "agent" | "user"; coerce anything else to
  // "user" so the profile query resolves (or renders "Member unavailable")
  // instead of tripping a type error.
  return (
    <ActorProfilePage
      memberType={memberType === "agent" ? "agent" : "user"}
      memberId={memberId}
    />
  );
}
