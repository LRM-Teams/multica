import type { Issue } from "./issue";

export interface NotePage {
  id: string;
  workspace_id: string;
  parent_id: string | null;
  owner_user_id: string;
  title: string;
  content: string;
  sort_key: string;
  share_user_ids: string[];
  can_manage_shares: boolean;
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
  /** Structured note→issue refs (S1-R3). Present on detail; may be empty on list. */
  refs?: NotePageIssueRef[];
}

export interface NotePageListResponse {
  pages: NotePage[];
}

export interface CreateNotePageRequest {
  parent_id?: string | null;
  title?: string;
}

export interface UpdateNotePageRequest {
  title?: string;
  content?: string;
}

export interface MoveNotePageRequest {
  parent_id: string | null;
  sort_key: string;
}

export interface DuplicateNotePageRequest {
  title?: string;
}

export interface UpdateNotePageSharesRequest {
  user_ids: string[];
}

export type NoteAIJobStatus = "queued" | "dispatched" | "running" | "completed" | "failed" | "cancelled";
export type NoteAIEditAction = "insert" | "replace_selection" | "replace_page" | "patch";
export type NoteAIJobFailureCode = "invalid_structured_output" | "assistant_failure" | "task_failure" | "task_error";
export type NoteAIJobRepairCode = "repaired_selected_output" | "repaired_page_output";

export interface NoteAIEditResult {
  action: NoteAIEditAction;
  markdown: string;
  /** Exact current Markdown fragment to replace when action is patch. */
  target?: string | null;
  title?: string | null;
  rationale?: string | null;
}

export interface CreateNoteAIJobRequest {
  agent_id: string;
  prompt: string;
  title?: string;
  /** Must be omitted or "editor". "worker" is rejected (S2-C3). */
  intent?: NoteIntent;
}

export interface NoteAIJob {
  id: string;
  workspace_id: string;
  page_id: string;
  agent_id: string;
  chat_session_id: string;
  task_id: string;
  status: NoteAIJobStatus;
  result?: NoteAIEditResult | null;
  failure_reason?: string | null;
  failure_code?: NoteAIJobFailureCode | null;
  repair_code?: NoteAIJobRepairCode | null;
  created_at: string;
  updated_at?: string;
}

/** Editor = rewrite page; Worker = platform work briefed by the note (S2-C3). */
export type NoteIntent = "editor" | "worker";

export type NoteWorkerJobStatus =
  | "pending"
  | "dispatched"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface CreateNoteWorkerJobRequest {
  agent_id: string;
  /** Trusted user directive. Note body is loaded under ACL at dispatch (S2-C1). */
  instruction: string;
  intent?: NoteIntent;
  /**
   * Optional group channel destination. When omitted, the Worker posts into
   * the caller's 1:1 agent DM (Messages timeline).
   */
  channel_id?: string;
}

export interface NoteWorkerJob {
  id: string;
  workspace_id: string;
  page_id: string;
  agent_id: string;
  instruction: string;
  status: NoteWorkerJobStatus | string;
  intent: NoteIntent | string;
  task_id?: string | null;
  /** Messages channel where the Worker run was posted. */
  channel_id?: string | null;
  channel_message_id?: string | null;
  /** Legacy chat_session id when present (channel-only wakes usually omit this). */
  chat_session_id?: string | null;
  failure_reason?: string | null;
  created_at: string;
  updated_at: string;
}

/** Stable note → target link (S1-R1 / S1-R3 / S2-R1). */
export type NotePageRefType = "issue" | "agent" | "run";

export interface NotePageIssueRef {
  type: NotePageRefType | string;
  /** Target UUID. Always present. */
  id: string;
  /** Identifier label when accessible; omitted when inaccessible. */
  label?: string | null;
  accessible: boolean;
  page_id?: string;
  /** @deprecated Prefer `id`; kept for older clients during transition. */
  issue_id?: string;
  /** Present on accessible run refs (S2-R1). */
  agent_id?: string;
  workspace_id?: string;
  identifier?: string;
  title?: string;
  number?: number;
  created_at?: string;
}

export interface NotePageIssueRefListResponse {
  refs: NotePageIssueRef[];
}

export interface CreateNotePageIssueRefRequest {
  issue_id: string;
}

export interface CreateNotePageAgentRefRequest {
  agent_id: string;
}

export interface CreateNotePageRunRefRequest {
  run_id: string;
}

export interface CreateNotePageIssueRequest {
  title?: string;
  description?: string;
}

export interface CreateNotePageIssueResponse {
  issue: Issue;
  ref: NotePageIssueRef;
}

/** Pending note writeback proposal (S1-W1 / D1). */
export type NoteWritebackAction = "append" | "patch" | "replace_page";
export type NoteWritebackStatus = "pending" | "applied" | "rejected";

export interface NoteWritebackEvidence {
  type: string;
  id: string;
  label?: string | null;
}

export interface NoteWriteback {
  id: string;
  workspace_id: string;
  page_id: string;
  action: NoteWritebackAction | string;
  content: string;
  target?: string | null;
  evidence: NoteWritebackEvidence[];
  status: NoteWritebackStatus | string;
  created_by_type: string;
  created_by_id: string;
  resolved_by?: string | null;
  resolved_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface NoteWritebackListResponse {
  writebacks: NoteWriteback[];
}

export interface CreateNoteWritebackRequest {
  action: NoteWritebackAction;
  content: string;
  target?: string;
  evidence: NoteWritebackEvidence[];
}
