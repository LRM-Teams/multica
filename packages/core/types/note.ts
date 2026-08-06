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

export interface DuplicateNotePageRequest {
  title?: string;
}

export interface UpdateNotePageSharesRequest {
  user_ids: string[];
}
