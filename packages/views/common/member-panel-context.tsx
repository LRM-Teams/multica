"use client";

import { createContext, use } from "react";
import type { OpenMemberPanelFn } from "@multica/core/members";

const MemberPanelContext = createContext<OpenMemberPanelFn | null>(null);

/**
 * Makes "open this human member's profile panel" reachable from anywhere a
 * member identity renders inside the provider's subtree — message bubbles,
 * @mentions, member list rows. Channels/DM host the panel inline in the
 * exclusive side slot; elsewhere GlobalMemberPanel uses the zustand store.
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

/** Returns null outside a provider — callers must handle that (fall back to store). */
export function useOpenMemberPanel(): OpenMemberPanelFn | null {
  return use(MemberPanelContext);
}
