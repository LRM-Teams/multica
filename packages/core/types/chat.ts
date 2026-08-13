import type { AgentTask } from "./agent";
import type { MessagePart } from "./message-part";

export interface RuntimeTokenStats {
  provider?: string;
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  total_tokens?: number;
  cost_usd?: number;
  context_tokens?: number;
  context_window?: number;
  context_percent?: number;
  auto_compaction_enabled?: boolean;
  updated_at?: string;
}

export interface ChatSession {
  id: string;
  workspace_id: string;
  agent_id: string;
  creator_id: string;
  title: string;
  status: "active" | "archived";
  /**
   * The project this chat session is bound to, if any. When set, the agent
   * works inside that project's directory. Null / absent means unbound.
   */
  project_id?: string | null;
  /** Latest provider-native token/context stats when the runtime reports them. */
  runtime_stats?: RuntimeTokenStats | null;
  /** True when the session has any unread assistant replies. List-only. */
  has_unread: boolean;
  created_at: string;
  updated_at: string;
}

export interface PendingChatTaskItem {
  chat_session_id: string;
  pending?: boolean;
  status?: string;
  delivery_id?: string;
  created_at?: string;
}

export interface PendingChatTasksResponse {
  tasks: PendingChatTaskItem[];
}

export interface ChatMessage {
  id: string;
  chat_session_id: string;
  role: "user" | "assistant";
  content: string;
  parts?: MessagePart[];
  task_id: string | null;
  created_at: string;
  /**
   * Attachments linked to this message via the attachment table's
   * chat_message_id FK. Populated by ListChatMessages. UI renders these
   * as file/image cards inside the bubble; the markdown URL inline in
   * `content` may have an expiring signature, while attachment metadata
   * here is stable and the source of truth for click-time download.
   */
  attachments?: import("./attachment").Attachment[];
  /**
   * When set, this is an assistant message synthesized by the server's
   * FailTask fallback (mirrors the issue path's failure system comment).
   * `content` carries the raw daemon-reported errMsg; the front-end maps
   * `failure_reason` (an enum like "agent_error" / "connection_error" /
   * "timeout") to a user-facing label and renders a destructive bubble.
   * Null on success messages and on user messages.
   */
  failure_reason?: string | null;
  /**
   * Wall-clock duration from `task.created_at` (user hit send) to terminal
   * state (completed/failed). Set by the server on assistant messages
   * synthesized by CompleteTask/FailTask. UI renders it as "Replied in
   * 38s" / "Failed after 12s" beneath the bubble. Null on user messages
   * and on legacy assistant messages predating migration 063.
   */
  elapsed_ms?: number | null;
}

export interface ChatMessagesCursor {
  created_at: string;
  id: string;
}

export interface ChatMessagesPage {
  messages: ChatMessage[];
  limit: number;
  has_more: boolean;
  next_cursor?: ChatMessagesCursor | null;
}

export interface SendChatMessageResponse {
  message_id: string;
  /**
   * Raft `agent:deliver` id when the Computer was woken with the user line.
   * Empty on the greeting sticker fast path and when delivery persist failed.
   */
  delivery_id?: string;
  /**
   * True while a standalone reply is still outstanding. Greeting is the
   * only send that returns pending=false.
   */
  pending?: boolean;
  /**
   * Server-authoritative task creation time. Optimistic StatusPill seed
   * uses this as its anchor so the timer starts from the real `0s` —
   * without it the front-end falls back to its local clock and the
   * timer "snaps backwards" later when WS events update the cache.
   */
  created_at: string;
}

export interface CancelledChatMessage {
  chat_session_id: string;
  message_id: string;
  content: string;
  restore_to_input: boolean;
}

export interface CancelTaskResponse extends AgentTask {
  cancelled_chat_message?: CancelledChatMessage;
}

/**
 * Response from GET /api/chat/sessions/{id}/pending-task.
 * Outstanding is `pending: true` for this chat_session_id.
 */
export interface ChatPendingTask {
  pending?: boolean;
  delivery_id?: string;
  status?: string;
  created_at?: string;
}
