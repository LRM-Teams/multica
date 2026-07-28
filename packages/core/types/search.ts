/**
 * Workspace-level global search contract (LRM-605 BE ↔ LRM-606 FE).
 *
 * Consumes the real BE contract on dev: `GET /api/search`. The workspace id is
 * resolved from the auth context on the server (ctxWorkspaceID), NOT from the
 * URL path. Query: ?q=<query>&scope=all|messages|channels|dms|people&limit=<n>.
 *
 * This is the collaboration-content search surface (Lock A / Slack-style):
 * scope tabs 全部 / Messages / Channels / DMs / People. It is distinct from the
 * single-channel `messages/search` (ChannelMessageSearch*) — that in-channel
 * search is NOT a terminal Search surface per LRM-454.
 *
 * Authorization (LRM-238, AC#3 option b — aligned to Slack + shipping 605):
 * the server only returns channels/DMs/messages/people the viewer can see (SQL
 * `JOIN channel_member viewer`). A query that matches only content the viewer
 * cannot see returns an empty result set — an empty result is simply "no
 * visible matches", never faked as anything else. Whole-request auth failure
 * surfaces as a 401/403 error (retryable), never a silent empty 200. There is
 * intentionally no `denied` field.
 */

export type WorkspaceSearchScope = "all" | "messages" | "channels" | "dms" | "people";

/** Per-scope totals, used to render scope-tab counts (e.g. `Messages 18`). */
export interface WorkspaceSearchCounts {
  messages: number;
  channels: number;
  dms: number;
  people: number;
}

export interface WorkspaceSearchHighlightRange {
  start: number;
  end: number;
}

/**
 * A matched message. `snippet` is a plain-text excerpt carrying the match;
 * `content` is the full message text; `highlight_ranges` are char offsets into
 * `content` for precise `<mark>` highlighting (BE-supplied). When several hits
 * collapse into one thread, `hit_count` > 1 and the row renders `THREAD + N`.
 */
export interface WorkspaceSearchMessage {
  result_type: "message";
  message_id: string;
  channel_id: string;
  /** Display name of the containing conversation, e.g. `multica` (no `#`). */
  channel_name: string;
  channel_kind: "group" | "dm";
  thread_root_message_id?: string | null;
  /** >1 when this row aggregates multiple hits in one thread. */
  hit_count: number;
  author_type?: "user" | "agent" | "lark" | "system" | null;
  author_id?: string | null;
  author_name: string;
  content: string;
  snippet: string;
  highlight_ranges?: WorkspaceSearchHighlightRange[];
  created_at: string;
}

/**
 * Channel search result. BE returns the same `GlobalSearchChannelResult` shape
 * for both the `channels` and `dms` arrays.
 */
export interface WorkspaceSearchChannel {
  channel_id: string;
  name: string;
  kind: "group" | "dm";
  description?: string | null;
}

/**
 * DM search result — same server shape as channels (no peer payload). The DM
 * row renders `name`, falling back to a localized "私信" placeholder when the
 * server returns no DM name.
 */
export interface WorkspaceSearchDM {
  channel_id: string;
  name: string;
  kind: "dm";
  description?: string | null;
}

export interface WorkspaceSearchPerson {
  /** "user" for members; "agent" for agents. */
  actor_type: "user" | "agent";
  /** user_id for members; agent id for agents. */
  actor_id: string;
  name: string;
  display_name: string;
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
}
