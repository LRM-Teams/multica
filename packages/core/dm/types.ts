import type { ChannelLastMessage, RuntimeTokenStats } from "../types";

/**
 * The other party in a 1-on-1 direct message. `type` discriminates a human
 * workspace member from an agent; `id` is the member (user) id or agent id.
 * JSON field names mirror the backend (`server/internal/handler/dm.go`).
 */
export interface DMPeer {
  type: "user" | "agent";
  id: string;
  name: string;
  avatar_url?: string;
  /**
   * True when the peer agent has been archived (the product-facing "delete
   * agent" action is a soft archive — history is never hidden). The DM stays
   * fully readable; the client shows a read-only banner and blocks new sends
   * instead of losing the conversation (2026-07-31 Wendy DM incident, B1).
   */
  archived?: boolean;
}

/**
 * Owner control actions on a supervised agent_pair DM (#692). Sent as the
 * `action` to `POST /api/dm/channels/{id}/a2a-control`, except `view_dm` which
 * is a client-only navigation affordance and is never sent to the endpoint.
 * Mirrors the backend (`server/internal/handler/agent_dm_a2a.go`).
 */
export type AgentDMControlAction =
  | "view_dm"
  | "grant_rounds"
  | "pause_pair"
  | "resume_pair"
  | "pause_global"
  | "resume_global";

/** Live gate/round state of a supervised agent↔agent DM. Mirrors the server
 *  `AgentDMControlResponse`. The viewer is always a read-only owner supervisor. */
export interface AgentDMControl {
  state:
    | "active"
    | "paused_budget"
    | "paused_frequency"
    | "paused_pair"
    | "paused_global";
  exchange_id?: string;
  matter_id?: string;
  round: number;
  round_limit: number;
  pause_reason?: string;
  can_grant_rounds: boolean;
  can_pause_pair: boolean;
  can_pause_global: boolean;
  actions: AgentDMControlAction[];
}

/**
 * Workspace-level agent↔agent DM control (#692). Read/written at the
 * independent `/api/dm/a2a-control` endpoint (NOT derived from any single DM
 * channel — a per-channel control's `can_pause_global`/`actions` are only a
 * hint, this is the source of truth). Mirrors the server
 * `AgentDMGlobalControlResponse`. Manageable only by a workspace user who owns
 * at least one non-archived agent.
 */
export interface AgentDMGlobalControl {
  state: "active" | "paused_global";
  paused: boolean;
  can_pause_global: boolean;
  actions: Array<"pause_global" | "resume_global">;
}

/** The five A2A pause/resume system-event kinds — the `event` on a message's
 *  `system_event` part and the owner `agent_dm_paused` inbox item. */
export type AgentDMSystemEvent =
  | "agent_dm_paused_budget"
  | "agent_dm_paused_frequency"
  | "agent_dm_paused_pair"
  | "agent_dm_paused_global"
  | "agent_dm_resumed";

/**
 * Params carried by an A2A pause/resume `system_event` message part (FE-5 DM
 * row) and the owner-private `agent_dm_paused` inbox item (FE-6 Activity).
 * Mirrors the server `agentDMSystemEventParams`. `matter` is a ≤120-rune source
 * summary (fallback「当前事项」); `round_limit` already includes any granted rounds.
 */
export interface AgentDMPauseEventParams {
  exchange_id: string;
  dm_channel_id: string;
  source_channel_id?: string;
  matter_id: string;
  matter: string;
  state: string;
  pause_reason?: string;
  round: number;
  round_limit: number;
  agent_a_id: string;
  agent_a_handle: string;
  agent_a_name: string;
  agent_b_id: string;
  agent_b_handle: string;
  agent_b_name: string;
  actions: AgentDMControlAction[];
}

/**
 * One row in the unified DM list and the create-or-find response.
 *
 * `source` is always `"dm_channel"` for the visible Messages surface. Legacy
 * standalone chat_sessions may still exist for history migration, but they are
 * not returned from `/api/dm`.
 */
export interface DMItem {
  /** dm channel id. */
  id: string;
  source: "dm_channel";
  /**
   * `"direct"` for a 1-on-1 DM; `"agent_pair"` for a supervised agent↔agent DM
   * (#692) the viewer owns one end of. Absent → treat as `"direct"`.
   */
  mode?: "direct" | "agent_pair";
  peer: DMPeer;
  /** The two agents of an `agent_pair` DM. Absent for `direct` DMs (use `peer`). */
  participants?: DMPeer[];
  /** True when the viewer is a read-only owner supervisor (owns one end), not a
   *  channel member — drives the read-only + supervision affordances. */
  supervised?: boolean;
  /** Live gate/round control for a supervised `agent_pair` DM (owner surface). */
  a2a_control?: AgentDMControl;
  last_message?: ChannelLastMessage;
  unread: number;
  updated_at: string;
  /**
   * Set when the conversation is in the unified Messages sidebar PINNED
   * section (Slack-style starred/pinned group). Absent/undefined = not pinned.
   */
  pinned_at?: string;
  /** Set when the conversation is muted for the current user. */
  muted_at?: string;
  muted?: boolean;
  /**
   * True when the user manually marked the conversation unread (peer-level
   * `dm_peer_state.manual_unread_at`). The server also bumps `unread` to at
   * least 1 in this case so the existing unread pill renders; this flag lets
   * the row distinguish a manual unread from real unread messages if needed.
   */
  manually_unread?: boolean;
  /**
   * True unread message count excluding the manually-unread boost. Added by
   * BE task #24. Falls back to `unread` when absent so old API responses work.
   * Use this (not `unread`) to decide number-badge vs dot.
   */
  real_unread?: number;
  /** An unread message in this DM @-mentions the viewer (BE #301); drives the
   *  independent @-mention red dot (coexists with the count). */
  has_mention?: boolean;
  /**
   * Viewer's read cursor for this DM (`conversation_member.last_read_seq`).
   * Drives the "N new messages" divider pinned on entry; omitted → no divider.
   */
  last_read_seq?: number;
  /** Latest provider-native token/context stats for agent DMs when available. */
  runtime_stats?: RuntimeTokenStats | null;
}

/** Body for POST /api/dm — idempotent create-or-find of a 1-on-1 DM. */
export interface CreateOrFindDMBody {
  peer_type: "user" | "agent";
  peer_id: string;
}
