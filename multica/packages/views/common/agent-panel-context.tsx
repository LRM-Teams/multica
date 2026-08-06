"use client";

import { createContext, use } from "react";
import type { OpenAgentPanelFn } from "@multica/core/agents";

const AgentPanelContext = createContext<OpenAgentPanelFn | null>(null);

/**
 * Makes "open this agent's side panel" reachable from anywhere an agent
 * identity renders inside the provider's subtree — not just the message
 * bubble avatar/name, which already gets it via a direct prop. Needed for
 * `MentionView` (a TipTap NodeView several layers removed from
 * channels-page.tsx's own agent-panel state) to open the same panel on
 * @mention click.
 *
 * LRM-292: callers pass agentId (optional identity snapshot). Hosts resolve
 * via GET /api/agents/:id — never ListAgents.find.
 */
export function AgentPanelProvider({
  onOpenAgent,
  children,
}: {
  onOpenAgent: OpenAgentPanelFn;
  children: React.ReactNode;
}) {
  return (
    <AgentPanelContext.Provider value={onOpenAgent}>
      {children}
    </AgentPanelContext.Provider>
  );
}

/** Returns null outside a provider (e.g. issue/task editors) — callers must handle that. */
export function useOpenAgentPanel(): OpenAgentPanelFn | null {
  return use(AgentPanelContext);
}
