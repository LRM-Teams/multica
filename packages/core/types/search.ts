/**
 * Workspace-level global search contract (LRM-605 BE ↔ LRM-606 FE).
 *
 * This is the collaboration-content search surface (Lock A / Slack-style):
 * scope tabs 全部 / Messages / Channels / DMs / People. It is distinct from the
 * single-channel `messages/search` (ChannelMessageSearch*) — that in-channel
 * search is NOT a terminal Search surface per LRM-454.
 *
 * Contract proposal for BE (LRM-605). FE consumes it via `api.searchWorkspace`.
 *
 * Endpoint (proposed): `GET /api/workspaces/{workspace_id}/search`
 *   ?q=<query>&scope=all|messages|channels|dms|people&limit=<n>
 *
 * Authorization rules (must hold, no silent fallback — LRM-238):
 *  - Only return channels/DMs/messages/people the viewer can see.
 *  - When the query matches ONLY content the viewer cannot see, return
 *    `denied: true` so FE renders the explicit 无权限 state (never fakes an
 *    empty list that hides the permission boundary).
 *  - Missing fields / server errors must surface as an error code the FE can
 *    retry, not an empty 200.
 */

export type WorkspaceSearchScope = "all" | "messages" | "channels" | "dms" | "people";

/** Per-scope totals, used to render scope-tab counts (e.g. `Messages 18`). */
export interface WorkspaceSearchCounts {
  messages: number;
  channels: number;
  dms: number;
  people: number;
}

/**
 * A matched message. `snippet` is plain text carrying the match; FE highlights
 * via `<mark>` (HighlightText). When several hits collapse into one thread,
 * `thread_hit_count` > 1 and the row renders `THREAD + N 命中`.
 */
export interface WorkspaceSearchMessage {
  message_id: string;
  channel_id: string;
  /** Display name of the containing conversation, e.g. `multica` (no `#`). */
  channel_name: string;
  channel_kind: "group" | "dm";
  thread_root_message_id?: string | null;
  /** >1 when this row aggregates multiple hits in one thread. */
  thread_hit_count?: number;
  author_id?: string | null;
  author_type?: "user" | "agent" | "lark" | "system" | null;
  author_name: string;
  author_avatar_url?: string | null;
  snippet: string;
  created_at: string;
}

export interface WorkspaceSearchChannel {
  id: string;
  name: string;
  description?: string | null;
  member_count?: number;
  /** Whether the viewer is currently a member (for "你也在其中" hint). */
  joined?: boolean;
}

export interface WorkspaceSearchDM {
  /** DM channel id — navigate via `paths.channelDetail(id)`. */
  id: string;
  peer_id: string;
  peer_type: "user" | "agent";
  peer_name: string;
  peer_avatar_url?: string | null;
  snippet?: string | null;
  last_message_at?: string | null;
}

export interface WorkspaceSearchPerson {
  /** user_id for members; agent id for agents. */
  id: string;
  type: "user" | "agent";
  display_name: string;
  handle?: string | null;
  email?: string | null;
  avatar_url?: string | null;
}

export interface WorkspaceSearchResponse {
  query: string;
  scope: WorkspaceSearchScope;
  counts: WorkspaceSearchCounts;
  messages: WorkspaceSearchMessage[];
  channels: WorkspaceSearchChannel[];
  dms: WorkspaceSearchDM[];
  people: WorkspaceSearchPerson[];
  /**
   * Explicit permission denial. When true the query matched only content the
   * viewer cannot see; FE shows the 无权限 state instead of an empty list.
   */
  denied?: boolean;
}
