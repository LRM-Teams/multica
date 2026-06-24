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
  description: string | null;
  lark_chat_id: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  /** List-only enrichments (absent on create/update/get responses). */
  unread_count?: number;
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
  /**
   * Attachments linked to this message via the attachment table's
   * channel_message_id FK. Populated by ListChannelMessages. The bubble
   * renders these as file/image cards; the markdown URL inline in `content`
   * may carry an expiring signature, while this metadata is stable.
   */
  attachments?: import("./attachment").Attachment[];
  created_at: string;
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
