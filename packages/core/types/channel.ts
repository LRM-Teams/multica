export interface ChannelLastMessage {
  author_type: "user" | "agent" | "lark" | "system";
  author_name: string;
  content: string;
  created_at: string;
}

export interface ChannelMemberBrief {
  member_type: "user" | "agent";
  member_id: string;
  name: string;
}

export interface Channel {
  id: string;
  workspace_id: string;
  name: string;
  kind: "group" | "dm";
  description: string | null;
  lark_chat_id: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
  archived_by?: string | null;
  /** List-only enrichments (absent on create/update/get responses). */
  unread_count?: number;
  /** True unread count excluding the manually-unread boost. Added by BE task #24.
   *  Falls back to unread_count when absent so old API responses still work. */
  real_unread_count?: number;
  manually_unread?: boolean;
  pinned_at?: string | null;
  muted_at?: string | null;
  muted?: boolean;
  last_message?: ChannelLastMessage | null;
  members?: ChannelMemberBrief[];
}

export interface ChannelMember {
  member_type: "user" | "agent";
  member_id: string;
  name: string;
  created_at: string;
}

export interface ChannelMessage {
  id: string;
  channel_id: string;
  workspace_id: string;
  author_type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  source: "multica" | "lark";
  external_message_id: string | null;
  reply_to_message_id?: string | null;
  reply_to?: ChannelMessageReply | null;
  thread_root_message_id?: string | null;
  thread_reply_count?: number;
  thread_last_reply_at?: string | null;
  thread_unread_count?: number;
  thread_followed?: boolean;
  thread_id?: string | null;
  trigger_depth?: number;
  /**
   * Attachments linked to this message via the attachment table's
   * channel_message_id FK. Populated by ListChannelMessages. The bubble
   * renders these as file/image cards; the markdown URL inline in `content`
   * may carry an expiring signature, while this metadata is stable.
   */
  attachments?: import("./attachment").Attachment[];
  created_at: string;
}

export interface ChannelThreadMessagesCursor {
  before: string;
  before_id: string;
}

export interface ChannelThreadMessagesPage {
  messages: ChannelMessage[];
  next_cursor: ChannelThreadMessagesCursor | null;
}

export interface ChannelMessageReply {
  id: string;
  author_type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  created_at: string;
}

export interface ChannelMessageSearchResult {
  message_id: string;
  channel_id: string;
  thread_root_message_id?: string | null;
  author_type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  created_at: string;
}

export interface ChannelMessageSearchResponse {
  query: string;
  total: number;
  results: ChannelMessageSearchResult[];
}

export interface ChannelAuthorStat {
  author_type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  count: number;
}

export interface ChannelStats {
  total_messages: number;
  file_count: number;
  member_count: number;
  by_author: ChannelAuthorStat[];
}

export interface ChannelActiveTask {
  agent_id: string;
  agent_name: string;
  task_id: string;
  status: string;
}

/** One entry in a project workdir listing; `path` is relative to the workdir
 *  root (forward slashes). The tree is rebuilt client-side from these paths. */
export interface ChannelProjectFile {
  path: string;
  is_dir: boolean;
  size?: number;
}

export type ChannelProjectFilesStatus =
  | "ok"
  | "no_project"
  | "offline"
  | "missing"
  | "github_unlinked"
  | "error";

export interface ChannelProjectFiles {
  project_id: string;
  status: ChannelProjectFilesStatus;
  nodes: ChannelProjectFile[];
  truncated: boolean;
}

/** A single project file's preview content (read from the daemon workdir). For
 *  text, `content` is UTF-8 and `encoding` is empty; for media it's base64 with
 *  `encoding` "base64" and `mime_type` set so the client can render it. */
export interface ChannelProjectFileContent {
  content: string;
  encoding: string;
  mime_type: string;
  truncated: boolean;
  too_large: boolean;
  binary: boolean;
}

export interface ChannelTypingPayload {
  channel_id: string;
  actor_type: "user" | "agent" | "lark" | "system";
  actor_id?: string;
  actor_name: string;
  is_typing: boolean;
  expires_in_ms?: number;
}
