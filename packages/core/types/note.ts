export interface NotePage {
  id: string;
  workspace_id: string;
  parent_id: string | null;
  owner_user_id: string;
  title: string;
  icon?: string | null;
  content: string;
  sort_key: string;
  share_user_ids: string[];
  share_agent_ids?: string[];
  share_channel_ids?: string[];
  can_manage_shares: boolean;
  /** True when the current user was newly granted a direct share and has not opened the page yet. */
  share_unread?: boolean;
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
  icon?: string | null;
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
  agent_ids?: string[];
  channel_ids?: string[];
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

/** Stable note → target link (S1-R1 / S1-R3 / S2-R1 / N2-A1). */
export type NotePageRefType = "issue" | "agent" | "run" | "channel";

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
  /**
   * Issue identifier (e.g. MUL-12), or for channel refs the kind
   * (`worker` | `coordination`).
   */
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

export interface CreateNotePageChannelRefRequest {
  channel_id: string;
  /** Defaults to worker on the server. */
  kind?: "worker" | "coordination";
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

/** S4-S1/S4-S3 on-demand day/week/month retrospective. */
export type NoteRetrospectiveWindow = "day" | "week" | "month";

/** Period Brief window — calendar presets or inclusive custom range. */
export type NotePeriodBriefWindow = NoteRetrospectiveWindow | "custom";
export type NoteRetrospectiveSource = "issue_activity" | "touched_notes" | "agent_runs";
export type NoteRetrospectiveComposition = "day_raw" | "layered_summaries";

export interface CreateNoteRetrospectiveRequest {
  window: NoteRetrospectiveWindow;
  date?: string;
  timezone?: string;
  sources?: NoteRetrospectiveSource[];
}

export interface NoteRetrospectiveWindowInfo {
  kind: string;
  timezone: string;
  start: string;
  end: string;
  label: string;
}

export interface CreateNoteRetrospectiveResponse {
  page: NotePage;
  window: NoteRetrospectiveWindowInfo;
  sources_used: string[];
  sources_empty: string[];
  sources_skipped: string[];
  fact_count: number;
  /** day_raw for day windows; layered_summaries for week/month (S4-S3). */
  composition?: NoteRetrospectiveComposition | string;
  layers_used?: string[];
  child_pages_used?: string[];
}

/** Period Work Brief synthesis (ADR 0019 / K0). */
export interface CreateNotePeriodBriefRequest {
  window: NotePeriodBriefWindow;
  /** Anchor date for day|week|month (YYYY-MM-DD in timezone). */
  date?: string;
  /** Inclusive start calendar day for `window: "custom"` (YYYY-MM-DD). */
  start_date?: string;
  /** Inclusive end calendar day for `window: "custom"` (YYYY-MM-DD). */
  end_date?: string;
  timezone?: string;
  /** Synthesizer Agent (defaults to Period Brief Agent / 周报 in the UI). */
  agent_id: string;
  /** Dedicated per-Computer collector Agents (`period-collect-*`). At least one. */
  collector_agent_ids: string[];
  sources?: NoteRetrospectiveSource[];
  channel_id?: string;
  /** Optional scoped request (paths / topics / aspects). Empty = full-scope default. */
  focus?: string;
  /** Note page whose bubble started this run; the finished brief inserts as its child. */
  context_note_page_id?: string;
  /** Existing notes-bubble chat session to continue. */
  chat_session_id?: string;
}

export interface CreateNotePeriodBriefResponse {
  page: NotePage;
  job: NoteWorkerJob;
  window: NoteRetrospectiveWindowInfo;
  sources_used: string[];
  sources_empty: string[];
  sources_skipped: string[];
  fact_count: number;
  /** Echo of accepted collector Agent ids (order preserved, deduped). */
  collector_agent_ids?: string[];
  /** One Note Worker job per collector (pack page + wake). */
  collector_jobs?: NoteWorkerJob[];
  /** Notes-bubble session that received the user turn and progress. */
  chat_session_id?: string;
}

export type NotePeriodBriefRunStatus =
  | "planning"
  | "collecting"
  | "synthesizing"
  | "awaiting_confirm"
  | "done";

export interface NotePeriodBriefActiveRun {
  id: string;
  status: NotePeriodBriefRunStatus | string;
  chat_session_id?: string;
  source_page_id?: string;
  draft_page_id: string;
}

export interface NotePeriodBriefActiveResponse {
  run: NotePeriodBriefActiveRun | null;
}

export type NotePeriodBriefInsertMode = "append" | "child";

export interface InsertNotePeriodBriefRequest {
  mode: NotePeriodBriefInsertMode;
}

export interface InsertNotePeriodBriefResponse {
  mode: NotePeriodBriefInsertMode;
  title?: string;
}
