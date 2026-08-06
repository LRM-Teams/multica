import type { InboxItem } from "./inbox";

export type UserActivityTab = "all" | "unread" | "mentions";

export type UserActivityItemKind = "thread" | "inbox";

export interface UserActivityItem {
  kind: UserActivityItemKind;
  id: string;
  workspace_id: string;
  channel_id?: string | null;
  channel_name?: string | null;
  channel_kind?: string | null;
  updated_at: string;
  unread_count: number;
  preview_text: string;
  title: string;
  access_denied: boolean;
  thread_root_message_id?: string | null;
  reply_count?: number | null;
  last_reply_at?: string | null;
  followed?: boolean | null;
  mentioned_me?: boolean | null;
  participated?: boolean | null;
  /**
   * LRM-809: actor the row avatar represents. Threads: dm peer (agent for
   * user↔agent DMs) or root author; inbox rows: the inbox actor.
   * "system"/null → no profile affordance.
   */
  actor_type?: string | null;
  actor_id?: string | null;
  inbox?: InboxItem | null;
}

export interface UserActivityListResponse {
  items: UserActivityItem[];
  next_cursor?: string | null;
}
