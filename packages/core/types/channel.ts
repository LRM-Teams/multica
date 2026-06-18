export interface Channel {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  lark_chat_id: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
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

export interface ChannelTypingPayload {
  channel_id: string;
  actor_type: "user" | "agent" | "lark" | "system";
  actor_id?: string;
  actor_name: string;
  is_typing: boolean;
  expires_in_ms?: number;
}
