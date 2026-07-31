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
