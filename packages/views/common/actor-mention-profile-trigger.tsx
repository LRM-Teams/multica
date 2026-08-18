"use client";

import type { ReactNode } from "react";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useMemberPanelStore } from "@multica/core/workspace";
import { useOpenAgentPanel } from "./agent-panel-context";
import { useOpenMemberPanel } from "./member-panel-context";
import { ActorProfileTrigger } from "./actor-profile-popover";

/** Shared hover and panel-opening behavior for person mention tokens. */
export function ActorMentionProfileTrigger({
  actorType,
  actorId,
  children,
}: {
  actorType: "member" | "agent";
  actorId: string;
  children: ReactNode;
}) {
  const openAgentFromContext = useOpenAgentPanel();
  const openAgentFromStore = useAgentPanelStore((state) => state.open);
  const closeAgentPanel = useAgentPanelStore((state) => state.close);
  const openMemberFromContext = useOpenMemberPanel();
  const openMemberFromStore = useMemberPanelStore((state) => state.open);

  const openProfilePanel =
    actorType === "agent"
      ? () => (openAgentFromContext ?? openAgentFromStore)(actorId)
      : () => {
          closeAgentPanel();
          (openMemberFromContext ?? openMemberFromStore)(actorId);
        };

  return (
    <ActorProfileTrigger
      memberType={actorType === "agent" ? "agent" : "user"}
      memberId={actorId}
      triggerElement="span"
      onClickCapture={openProfilePanel}
    >
      {children}
    </ActorProfileTrigger>
  );
}
