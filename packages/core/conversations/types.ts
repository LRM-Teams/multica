import type { Channel, ChannelLastMessage } from "../types";

/**
 * LRM-1399 — unified Messages read model returned by `GET /api/conversations`.
 * The backend deliberately keeps each side's native payload shape
 * (`ChannelResponse` / `DMItem`) so their detail, mutation, and permission
 * routes stay separate. On the client this is the SINGLE list data source for
 * the active (non-archived) CHANNELS + DIRECT MESSAGES sidebar regions.
 *
 * Mirrors `server/internal/handler/conversation.go`:
 * `ConversationListItem{Kind, Channel, DM}` and
 * `ConversationListResponse{Items, NextCursor}`.
 */
export interface ConversationListItem {
  kind: "channel" | "dm";
  channel?: Channel;
  /** DM payload — shape defined against `/api/dm` (see packages/core/dm/types.ts). */
  dm?: DMListItem;
}

/** Pinned/unpinned + recency sort key carried only inside the cursor. */
export interface ConversationListResponse {
  items: ConversationListItem[];
  next_cursor?: string;
}

/**
 * The DM side of a conversation row. Defined structurally here (rather than
 * re-exporting `DMItem`) so the unified module owns its contract without a
 * circular import; the runtime payload is identical to `/api/dm`.
 */
export interface DMListItem {
  id: string;
  source: "dm_channel";
  mode?: "direct" | "agent_pair";
  peer: { type: "user" | "agent"; id: string; name: string; avatar_url?: string; archived?: boolean };
  participants?: { type: "user" | "agent"; id: string; name: string; avatar_url?: string }[];
  supervised?: boolean;
  last_message?: ChannelLastMessage;
  unread: number;
  updated_at: string;
  pinned_at?: string;
  muted_at?: string;
  muted?: boolean;
  manually_unread?: boolean;
  real_unread?: number;
  has_mention?: boolean;
  last_read_seq?: number;
  runtime_stats?: unknown;
}
