/**
 * Lightweight identity carried from a row that already has agent display
 * fields (message author, channel member, …). Used only for optimistic
 * panel chrome while GET /api/agents/:id loads — never as the panel body
 * authority (LRM-292 / Frank: open by id, do not ListAgents.find).
 */
export type AgentPanelIdentitySnapshot = {
  name?: string;
  display_name?: string | null;
  avatar_url?: string | null;
};

/** Unified open(agent) entry — agentId required; snapshot optional. */
export type OpenAgentPanelFn = (
  agentId: string,
  snapshot?: AgentPanelIdentitySnapshot,
) => void;
