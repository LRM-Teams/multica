"use client";

import { createContext, use } from "react";

export type OpenMemberPanelFn = (userId: string) => void;

const MemberPanelContext = createContext<OpenMemberPanelFn | null>(null);

/**
 * LRM-619 — local "open human member profile dock" for channels/DM, mirroring
 * AgentPanelProvider so @mentions / avatars open the same exclusive right slot.
 */
export function MemberPanelProvider({
  onOpenMember,
  children,
}: {
  onOpenMember: OpenMemberPanelFn;
  children: React.ReactNode;
}) {
  return (
    <MemberPanelContext.Provider value={onOpenMember}>
      {children}
    </MemberPanelContext.Provider>
  );
}

/** Returns null outside a provider — callers must handle that. */
export function useOpenMemberPanel(): OpenMemberPanelFn | null {
  return use(MemberPanelContext);
}
