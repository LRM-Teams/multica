import type { ChannelLastMessage } from "../types";

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
}

/**
 * One row in the unified DM list and the create-or-find response.
 *
 * `source` routes everything downstream:
 *  - `"dm_channel"`  — the DM is a kind='dm' channel; use the channel message
 *    stack (GET/POST /api/channels/{id}/messages, channel:message WS).
 *  - `"legacy_session"` — a pre-existing standalone chat_session (the old Chat
 *    FAB conversations); use the chat stack (GET /api/chat/sessions/{id}/...).
 *
 * `unread` is a count for dm_channel items and 0/1 for legacy_session items
 * (legacy sessions only track a per-session has-unread flag, not a count).
 */
export interface DMItem {
  /** dm channel id (source=dm_channel) OR legacy chat_session id (source=legacy_session). */
  id: string;
  source: "dm_channel" | "legacy_session";
  peer: DMPeer;
  last_message?: ChannelLastMessage;
  unread: number;
  updated_at: string;
  /**
   * Set when the conversation is pinned to the top of DIRECT MESSAGES. Backed by
   * peer-level state (`dm_peer_state.pinned_at`) so pinning dedupes across a
   * peer's dm_channel + legacy_session sources. Absent/undefined = not pinned.
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
}

/** Body for POST /api/dm — idempotent create-or-find of a 1-on-1 DM. */
export interface CreateOrFindDMBody {
  peer_type: "user" | "agent";
  peer_id: string;
}
