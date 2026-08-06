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

/**
 * LRM-877 — optional Dock Stack hint when opening Agent from a human Profile
 * (or hover card). Hosts that own a member→agent panel stack keep
 * `returnToMemberId` so `← {name}` can pop back to the human Profile.
 */
export type OpenAgentPanelOptions = {
  returnToMemberId?: string;
};

/** Unified open(agent) entry — agentId required; snapshot optional. */
export type OpenAgentPanelFn = (
  agentId: string,
  snapshot?: AgentPanelIdentitySnapshot,
  options?: OpenAgentPanelOptions,
) => void;
