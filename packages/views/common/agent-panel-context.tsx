"use client";

import { createContext, use } from "react";

type OpenAgentPanel = (agentId: string) => void;

const AgentPanelContext = createContext<OpenAgentPanel | null>(null);

/**
 * Makes "open this agent's side panel" reachable from anywhere an agent
 * identity renders inside the provider's subtree — not just the message
 * bubble avatar/name, which already gets it via a direct prop. Needed for
 * `MentionView` (a TipTap NodeView several layers removed from
 * channels-page.tsx's own agent-panel state) to open the same panel on
 * @mention click.
 */
export function AgentPanelProvider({
  onOpenAgent,
  children,
}: {
  onOpenAgent: OpenAgentPanel;
  children: React.ReactNode;
}) {
  return (
    <AgentPanelContext.Provider value={onOpenAgent}>
      {children}
    </AgentPanelContext.Provider>
  );
}

/** Returns null outside a provider (e.g. issue/task editors) — callers must handle that. */
export function useOpenAgentPanel(): OpenAgentPanel | null {
  return use(AgentPanelContext);
}
