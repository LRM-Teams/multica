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

export interface NoteAIEditResult {
  action: NoteAIEditAction;
  markdown: string;
  title?: string | null;
  rationale?: string | null;
}

export interface CreateNoteAIJobRequest {
  agent_id: string;
  prompt: string;
  title?: string;
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
  created_at: string;
  updated_at?: string;
}
