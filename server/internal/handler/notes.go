package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type NotePageResponse struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspace_id"`
	ParentID        *string  `json:"parent_id"`
	OwnerUserID     string   `json:"owner_user_id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	SortKey         string   `json:"sort_key"`
	ShareUserIDs    []string `json:"share_user_ids"`
	CanManageShares bool     `json:"can_manage_shares"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	DeletedAt       *string  `json:"deleted_at"`
}

type notePageRow struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	ParentID    pgtype.UUID
	OwnerUserID pgtype.UUID
	Title       string
	Content     string
	SortKey     string
	CreatedAt   pgtype.Timestamptz
	UpdatedAt   pgtype.Timestamptz
	DeletedAt   pgtype.Timestamptz
}

type noteCreateRequest struct {
	ParentID *string `json:"parent_id"`
	Title    string  `json:"title"`
}

type noteUpdateRequest struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
}

type noteShareRequest struct {
	UserIDs []string `json:"user_ids"`
}

type noteMoveRequest struct {
	ParentID *string `json:"parent_id"`
	SortKey  string  `json:"sort_key"`
}

type noteDuplicateRequest struct {
	Title *string `json:"title"`
}

type noteAIJobCreateRequest struct {
	AgentID string `json:"agent_id"`
	Prompt  string `json:"prompt"`
	Title   string `json:"title"`
}

type NoteAIEditResult struct {
	Action    string  `json:"action"`
	Markdown  string  `json:"markdown"`
	Target    *string `json:"target,omitempty"`
	Title     *string `json:"title,omitempty"`
	Rationale *string `json:"rationale,omitempty"`
}

type NoteAIJobResponse struct {
	ID            string            `json:"id"`
	WorkspaceID   string            `json:"workspace_id"`
	PageID        string            `json:"page_id"`
	AgentID       string            `json:"agent_id"`
	ChatSessionID string            `json:"chat_session_id"`
	TaskID        string            `json:"task_id"`
	Status        string            `json:"status"`
	Result        *NoteAIEditResult `json:"result,omitempty"`
	FailureReason *string           `json:"failure_reason,omitempty"`
	FailureCode   *string           `json:"failure_code,omitempty"`
	RepairCode    *string           `json:"repair_code,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
}

func (h *Handler) notesWorkspaceAndUser(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, string, bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = h.resolveWorkspaceID(r)
	}
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return pgtype.UUID{}, pgtype.UUID{}, "", false
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return pgtype.UUID{}, pgtype.UUID{}, "", false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, "", false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, "", false
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, "", false
	}
	return wsUUID, userUUID, userID, true
}

func normalizeNoteTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Untitled"
	}
	runes := []rune(title)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return title
}

func normalizeNoteSortKey(sortKey string) string {
	sortKey = strings.TrimSpace(sortKey)
	if sortKey == "" {
		return fmt.Sprintf("%020d", time.Now().UnixMicro())
	}
	runes := []rune(sortKey)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return sortKey
}

func scanNotePage(row pgx.Row) (notePageRow, error) {
	var p notePageRow
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.ParentID, &p.OwnerUserID, &p.Title, &p.Content, &p.SortKey, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt)
	return p, err
}

func notePageToResponse(p notePageRow, currentUserID pgtype.UUID, shareUserIDs []string) NotePageResponse {
	return NotePageResponse{
		ID:              uuidToString(p.ID),
		WorkspaceID:     uuidToString(p.WorkspaceID),
		ParentID:        uuidToPtr(p.ParentID),
		OwnerUserID:     uuidToString(p.OwnerUserID),
		Title:           p.Title,
		Content:         p.Content,
		SortKey:         p.SortKey,
		ShareUserIDs:    shareUserIDs,
		CanManageShares: uuidToString(p.OwnerUserID) == uuidToString(currentUserID),
		CreatedAt:       timestampToString(p.CreatedAt),
		UpdatedAt:       timestampToString(p.UpdatedAt),
		DeletedAt:       timestampToPtr(p.DeletedAt),
	}
}

func (h *Handler) noteShareUserIDs(ctx context.Context, pageID pgtype.UUID) ([]string, error) {
	rows, err := h.DB.Query(ctx, `
SELECT user_id
FROM note_page_share
WHERE page_id = $1
ORDER BY created_at ASC`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, uuidToString(id))
	}
	return ids, rows.Err()
}

func (h *Handler) noteAccess(ctx context.Context, pageID, workspaceID, userID pgtype.UUID) (accessible bool, owner bool, err error) {
	err = h.DB.QueryRow(ctx, `
WITH RECURSIVE chain AS (
    SELECT id, workspace_id, parent_id, owner_user_id
    FROM note_page
    WHERE id = $1
      AND deleted_at IS NULL
      AND (workspace_id = $2 OR owner_user_id = $3)
  UNION
    SELECT parent.id, parent.workspace_id, parent.parent_id, parent.owner_user_id
    FROM note_page parent
    JOIN chain child ON child.parent_id = parent.id
    WHERE parent.deleted_at IS NULL
)
SELECT
  EXISTS (
    SELECT 1
    FROM chain c
    WHERE c.owner_user_id = $3
       OR (
         c.workspace_id = $2
         AND EXISTS (SELECT 1 FROM note_page_share s WHERE s.page_id = c.id AND s.user_id = $3)
       )
  ) AS accessible,
  EXISTS (
    SELECT 1
    FROM note_page p
    WHERE p.id = $1 AND p.deleted_at IS NULL AND p.owner_user_id = $3
  ) AS owner`, pageID, workspaceID, userID).Scan(&accessible, &owner)
	return accessible, owner, err
}

func (h *Handler) loadAccessibleNote(w http.ResponseWriter, r *http.Request, pageID string, workspaceID, userID pgtype.UUID) (notePageRow, bool, bool) {
	pageUUID, ok := parseUUIDOrBadRequest(w, pageID, "note page id")
	if !ok {
		return notePageRow{}, false, false
	}
	accessible, owner, err := h.noteAccess(r.Context(), pageUUID, workspaceID, userID)
	if err != nil || !accessible {
		writeError(w, http.StatusNotFound, "note page not found")
		return notePageRow{}, false, false
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page
WHERE id = $1 AND deleted_at IS NULL`, pageUUID))
	if err != nil {
		writeError(w, http.StatusNotFound, "note page not found")
		return notePageRow{}, false, false
	}
	return page, owner, true
}

func (h *Handler) ListNotePages(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
WITH RECURSIVE visible AS (
    SELECT p.id, p.workspace_id, p.parent_id, p.owner_user_id, p.title, p.content, p.sort_key, p.created_at, p.updated_at, p.deleted_at
    FROM note_page p
    WHERE p.deleted_at IS NULL
      AND (
        p.owner_user_id = $2
        OR (
          p.workspace_id = $1
          AND EXISTS (SELECT 1 FROM note_page_share s WHERE s.page_id = p.id AND s.user_id = $2)
        )
      )
  UNION
    SELECT child.id, child.workspace_id, child.parent_id, child.owner_user_id, child.title, child.content, child.sort_key, child.created_at, child.updated_at, child.deleted_at
    FROM note_page child
    JOIN visible parent ON child.parent_id = parent.id
    WHERE child.deleted_at IS NULL
)
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM visible
ORDER BY parent_id NULLS FIRST, sort_key, created_at`, workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list notes")
		return
	}
	defer rows.Close()
	collected := []notePageRow{}
	for rows.Next() {
		var p notePageRow
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.ParentID, &p.OwnerUserID, &p.Title, &p.Content, &p.SortKey, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list notes")
			return
		}
		collected = append(collected, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list notes")
		return
	}
	pages := make([]NotePageResponse, 0, len(collected))
	for _, p := range collected {
		shares, err := h.noteShareUserIDs(r.Context(), p.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list notes")
			return
		}
		pages = append(pages, notePageToResponse(p, userID, shares))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (h *Handler) ListDeletedNotePages(w http.ResponseWriter, r *http.Request) {
	_, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT p.id, p.workspace_id, p.parent_id, p.owner_user_id, p.title, p.content, p.sort_key, p.created_at, p.updated_at, p.deleted_at
FROM note_page p
LEFT JOIN note_page parent ON parent.id = p.parent_id AND parent.workspace_id = p.workspace_id
WHERE p.owner_user_id = $1
  AND p.deleted_at IS NOT NULL
  AND (p.parent_id IS NULL OR parent.deleted_at IS NULL)
ORDER BY p.deleted_at DESC, p.updated_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deleted notes")
		return
	}
	defer rows.Close()
	collected := []notePageRow{}
	for rows.Next() {
		var p notePageRow
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.ParentID, &p.OwnerUserID, &p.Title, &p.Content, &p.SortKey, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list deleted notes")
			return
		}
		collected = append(collected, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deleted notes")
		return
	}
	pages := make([]NotePageResponse, 0, len(collected))
	for _, p := range collected {
		shares, err := h.noteShareUserIDs(r.Context(), p.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list deleted notes")
			return
		}
		pages = append(pages, notePageToResponse(p, userID, shares))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (h *Handler) CreateNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	var req noteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	parentID := pgtype.UUID{}
	pageWorkspaceID := workspaceID
	if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
		parent, _, ok := h.loadAccessibleNote(w, r, *req.ParentID, workspaceID, userID)
		if !ok {
			return
		}
		parentID = parent.ID
		pageWorkspaceID = parent.WorkspaceID
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, pageWorkspaceID, parentID, userID, normalizeNoteTitle(req.Title)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note page")
		return
	}
	writeJSON(w, http.StatusCreated, notePageToResponse(page, userID, []string{}))
}

func (h *Handler) GetNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	shares, err := h.noteShareUserIDs(r.Context(), page.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note page")
		return
	}
	writeJSON(w, http.StatusOK, notePageToResponse(page, userID, shares))
}

func (h *Handler) UpdateNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	var req noteUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := page.Title
	if req.Title != nil {
		title = normalizeNoteTitle(*req.Title)
	}
	content := page.Content
	if req.Content != nil {
		content = *req.Content
	}
	updated, err := scanNotePage(h.DB.QueryRow(r.Context(), `
UPDATE note_page
SET title = $4, content = $5, updated_by = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, page.ID, page.WorkspaceID, userID, title, content))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update note page")
		return
	}
	shares, err := h.noteShareUserIDs(r.Context(), updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update note page")
		return
	}
	writeJSON(w, http.StatusOK, notePageToResponse(updated, userID, shares))
}

func (h *Handler) MoveNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, owner, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	if !owner {
		writeError(w, http.StatusForbidden, "only the owner can move this note page")
		return
	}
	var req noteMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	parentID := pgtype.UUID{}
	pageWorkspaceID := page.WorkspaceID
	if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
		parent, parentOwner, ok := h.loadAccessibleNote(w, r, *req.ParentID, workspaceID, userID)
		if !ok {
			return
		}
		if !parentOwner {
			writeError(w, http.StatusForbidden, "only the owner can move notes under this parent")
			return
		}
		if uuidToString(parent.ID) == uuidToString(page.ID) {
			writeError(w, http.StatusBadRequest, "cannot move a note under itself")
			return
		}
		var parentInSubtree bool
		if err := h.DB.QueryRow(r.Context(), `
WITH RECURSIVE subtree AS (
    SELECT id FROM note_page WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NULL
  UNION ALL
    SELECT child.id
    FROM note_page child
    JOIN subtree parent ON child.parent_id = parent.id
    WHERE child.owner_user_id = $2 AND child.deleted_at IS NULL
)
SELECT EXISTS (SELECT 1 FROM subtree WHERE id = $3)`, page.ID, userID, parent.ID).Scan(&parentInSubtree); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to move note page")
			return
		}
		if parentInSubtree {
			writeError(w, http.StatusBadRequest, "cannot move a note under one of its child pages")
			return
		}
		parentID = parent.ID
		pageWorkspaceID = parent.WorkspaceID
	}
	updated, err := scanNotePage(h.DB.QueryRow(r.Context(), `
WITH RECURSIVE subtree AS (
    SELECT id FROM note_page WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NULL
  UNION ALL
    SELECT child.id
    FROM note_page child
    JOIN subtree parent ON child.parent_id = parent.id
    WHERE child.owner_user_id = $2 AND child.deleted_at IS NULL
), moved AS (
    UPDATE note_page
    SET workspace_id = $3,
        parent_id = CASE WHEN id = $1 THEN $4 ELSE parent_id END,
        sort_key = CASE WHEN id = $1 THEN $5 ELSE sort_key END,
        updated_by = $2,
        updated_at = now()
    WHERE id IN (SELECT id FROM subtree)
    RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
)
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM moved
WHERE id = $1`, page.ID, userID, pageWorkspaceID, parentID, normalizeNoteSortKey(req.SortKey)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move note page")
		return
	}
	shares, err := h.noteShareUserIDs(r.Context(), updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move note page")
		return
	}
	writeJSON(w, http.StatusOK, notePageToResponse(updated, userID, shares))
}

func (h *Handler) DeleteNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, owner, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	if !owner {
		writeError(w, http.StatusForbidden, "only the owner can delete this note page")
		return
	}
	_, err := h.DB.Exec(r.Context(), `
WITH RECURSIVE subtree AS (
    SELECT id FROM note_page WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
  UNION
    SELECT child.id
    FROM note_page child
    JOIN subtree parent ON child.parent_id = parent.id
    WHERE child.workspace_id = $2 AND child.deleted_at IS NULL
)
UPDATE note_page
SET deleted_at = now(), updated_by = $3, updated_at = now()
WHERE id IN (SELECT id FROM subtree)`, page.ID, page.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete note page")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DuplicateNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, owner, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	if !owner {
		writeError(w, http.StatusForbidden, "only the owner can duplicate this note page")
		return
	}
	var req noteDuplicateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	type duplicateSource struct {
		ID       pgtype.UUID
		ParentID pgtype.UUID
		Title    string
		Content  string
		SortKey  string
	}
	rows, err := h.DB.Query(r.Context(), `
WITH RECURSIVE subtree AS (
    SELECT id, parent_id, title, content, sort_key, created_at, 0 AS depth
    FROM note_page
    WHERE id = $1 AND workspace_id = $2 AND owner_user_id = $3 AND deleted_at IS NULL
  UNION ALL
    SELECT child.id, child.parent_id, child.title, child.content, child.sort_key, child.created_at, parent.depth + 1
    FROM note_page child
    JOIN subtree parent ON child.parent_id = parent.id
    WHERE child.workspace_id = $2 AND child.owner_user_id = $3 AND child.deleted_at IS NULL
)
SELECT id, parent_id, title, content, sort_key
FROM subtree
ORDER BY depth, parent_id NULLS FIRST, sort_key, created_at`, page.ID, page.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
		return
	}
	defer rows.Close()
	sources := []duplicateSource{}
	for rows.Next() {
		var source duplicateSource
		if err := rows.Scan(&source.ID, &source.ParentID, &source.Title, &source.Content, &source.SortKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
			return
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
		return
	}
	if len(sources) == 0 {
		writeError(w, http.StatusNotFound, "note page not found")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
		return
	}
	defer tx.Rollback(r.Context())
	newIDs := map[string]pgtype.UUID{}
	copied := make([]NotePageResponse, 0, len(sources))
	for index, source := range sources {
		parentID := page.ParentID
		if uuidToString(source.ID) != uuidToString(page.ID) {
			mappedParent, ok := newIDs[uuidToString(source.ParentID)]
			if !ok {
				writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
				return
			}
			parentID = mappedParent
		}
		title := source.Title
		if uuidToString(source.ID) == uuidToString(page.ID) && req.Title != nil {
			title = normalizeNoteTitle(*req.Title)
		}
		sortKey := source.SortKey
		if uuidToString(source.ID) == uuidToString(page.ID) {
			sortKey = fmt.Sprintf("%020d", time.Now().UnixMicro()+int64(index))
		}
		inserted, err := scanNotePage(tx.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, page.WorkspaceID, parentID, userID, title, source.Content, sortKey))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
			return
		}
		newIDs[uuidToString(source.ID)] = inserted.ID
		copied = append(copied, notePageToResponse(inserted, userID, []string{}))
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate note page")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"pages": copied})
}

func (h *Handler) PermanentlyDeleteNotePage(w http.ResponseWriter, r *http.Request) {
	_, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	pageUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "note page id")
	if !ok {
		return
	}
	result, err := h.DB.Exec(r.Context(), `
DELETE FROM note_page
WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NOT NULL`, pageUUID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to permanently delete note page")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "note page not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RestoreNotePage(w http.ResponseWriter, r *http.Request) {
	_, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	pageUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "note page id")
	if !ok {
		return
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page
WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NOT NULL`, pageUUID, userID))
	if err != nil {
		writeError(w, http.StatusNotFound, "note page not found")
		return
	}
	_, err = h.DB.Exec(r.Context(), `
WITH RECURSIVE subtree AS (
    SELECT id FROM note_page WHERE id = $1 AND workspace_id = $2 AND owner_user_id = $3
  UNION
    SELECT child.id
    FROM note_page child
    JOIN subtree parent ON child.parent_id = parent.id
    WHERE child.workspace_id = $2 AND child.owner_user_id = $3
)
UPDATE note_page
SET deleted_at = NULL, updated_by = $3, updated_at = now()
WHERE id IN (SELECT id FROM subtree)`, page.ID, page.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore note page")
		return
	}
	restored, err := scanNotePage(h.DB.QueryRow(r.Context(), `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page
WHERE id = $1 AND workspace_id = $2`, page.ID, page.WorkspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore note page")
		return
	}
	shares, err := h.noteShareUserIDs(r.Context(), restored.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore note page")
		return
	}
	writeJSON(w, http.StatusOK, notePageToResponse(restored, userID, shares))
}

func (h *Handler) UpdateNotePageShares(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, owner, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	if !owner {
		writeError(w, http.StatusForbidden, "only the owner can share this note page")
		return
	}
	var req noteShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `DELETE FROM note_page_share WHERE page_id = $1`, page.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return
	}
	seen := map[string]bool{}
	for _, rawID := range req.UserIDs {
		targetID, ok := parseUUIDOrBadRequest(w, rawID, "share user id")
		if !ok {
			return
		}
		key := uuidToString(targetID)
		if key == uuidToString(userID) || seen[key] {
			continue
		}
		seen[key] = true
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2)`, page.WorkspaceID, targetID).Scan(&exists); err != nil || !exists {
			writeError(w, http.StatusBadRequest, "share user must be a workspace member")
			return
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO note_page_share (page_id, user_id, created_by) VALUES ($1, $2, $3)`, page.ID, targetID, userID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update shares")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return
	}
	shares, err := h.noteShareUserIDs(r.Context(), page.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return
	}
	writeJSON(w, http.StatusOK, notePageToResponse(page, userID, shares))
}

func normalizeNoteAIJobTitle(title, noteTitle string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Note AI: " + normalizeNoteTitle(noteTitle)
	}
	runes := []rune(title)
	if len(runes) > chatSessionTitleMaxLen {
		return string(runes[:chatSessionTitleMaxLen])
	}
	return title
}

func stripNoteAIJSONFences(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		return trimmed
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

var noteAIJSONActionRE = regexp.MustCompile(`(?s)"action"\s*:\s*"([^"]+)"`)
var noteAIJSONTargetRE = regexp.MustCompile(`(?s)"target"\s*:\s*"([^"]*)"`)
var noteAIJSONTitleRE = regexp.MustCompile(`(?s)"title"\s*:\s*"([^"]*)"`)
var noteAIJSONRationaleRE = regexp.MustCompile(`(?s)"rationale"\s*:\s*"([^"]*)"`)

func parseLooseNoteAIEditResult(content string) (*NoteAIEditResult, error) {
	actionMatch := noteAIJSONActionRE.FindStringSubmatch(content)
	if len(actionMatch) < 2 {
		return nil, fmt.Errorf("note AI edit action is missing")
	}
	markdown, err := extractLooseNoteAIStringField(content, "markdown")
	if err != nil {
		return nil, err
	}
	result := &NoteAIEditResult{
		Action:   actionMatch[1],
		Markdown: markdown,
	}
	if targetMatch := noteAIJSONTargetRE.FindStringSubmatch(content); len(targetMatch) >= 2 {
		target := strings.TrimSpace(strings.ReplaceAll(targetMatch[1], `\"`, `"`))
		if target != "" {
			result.Target = &target
		}
	}
	if titleMatch := noteAIJSONTitleRE.FindStringSubmatch(content); len(titleMatch) >= 2 {
		title := strings.TrimSpace(strings.ReplaceAll(titleMatch[1], `\"`, `"`))
		if title != "" {
			result.Title = &title
		}
	}
	if rationaleMatch := noteAIJSONRationaleRE.FindStringSubmatch(content); len(rationaleMatch) >= 2 {
		rationale := strings.TrimSpace(strings.ReplaceAll(rationaleMatch[1], `\"`, `"`))
		if rationale != "" {
			result.Rationale = &rationale
		}
	}
	return result, nil
}

func extractLooseNoteAIStringField(content, field string) (string, error) {
	fieldName := `"` + field + `"`
	fieldIndex := strings.Index(content, fieldName)
	if fieldIndex < 0 {
		return "", fmt.Errorf("note AI edit %s is missing", field)
	}
	colon := strings.Index(content[fieldIndex+len(fieldName):], ":")
	if colon < 0 {
		return "", fmt.Errorf("note AI edit %s is invalid", field)
	}
	valueStart := fieldIndex + len(fieldName) + colon + 1
	for valueStart < len(content) && (content[valueStart] == ' ' || content[valueStart] == '\n' || content[valueStart] == '\r' || content[valueStart] == '\t') {
		valueStart++
	}
	if valueStart >= len(content) || content[valueStart] != '"' {
		return "", fmt.Errorf("note AI edit %s must be a string", field)
	}
	valueStart++
	for i := valueStart; i < len(content); i++ {
		if content[i] != '"' {
			continue
		}
		j := i + 1
		for j < len(content) && (content[j] == ' ' || content[j] == '\n' || content[j] == '\r' || content[j] == '\t') {
			j++
		}
		if j < len(content) && content[j] == ',' {
			k := j + 1
			for k < len(content) && (content[k] == ' ' || content[k] == '\n' || content[k] == '\r' || content[k] == '\t') {
				k++
			}
			if strings.HasPrefix(content[k:], `"action"`) || strings.HasPrefix(content[k:], `"markdown"`) || strings.HasPrefix(content[k:], `"target"`) || strings.HasPrefix(content[k:], `"title"`) || strings.HasPrefix(content[k:], `"rationale"`) {
				return strings.TrimSpace(strings.ReplaceAll(content[valueStart:i], `\"`, `"`)), nil
			}
		}
		if j < len(content) && content[j] == '}' {
			return strings.TrimSpace(strings.ReplaceAll(content[valueStart:i], `\"`, `"`)), nil
		}
	}
	return "", fmt.Errorf("note AI edit %s is invalid", field)
}

func validateNoteAIEditResult(result *NoteAIEditResult) (*NoteAIEditResult, error) {
	result.Action = strings.TrimSpace(result.Action)
	result.Markdown = strings.TrimSpace(result.Markdown)
	switch result.Action {
	case "insert", "replace_selection", "replace_page", "patch":
	default:
		return nil, fmt.Errorf("unsupported note AI edit action %q", result.Action)
	}
	if result.Markdown == "" {
		return nil, fmt.Errorf("note AI edit markdown is empty")
	}
	if result.Action == "patch" {
		if result.Target == nil || strings.TrimSpace(*result.Target) == "" {
			return nil, fmt.Errorf("note AI patch target is required")
		}
		target := strings.TrimSpace(*result.Target)
		result.Target = &target
	} else {
		result.Target = nil
	}
	if result.Title != nil {
		title := strings.TrimSpace(*result.Title)
		if title == "" {
			result.Title = nil
		} else {
			result.Title = &title
		}
	}
	if result.Rationale != nil {
		rationale := strings.TrimSpace(*result.Rationale)
		if rationale == "" {
			result.Rationale = nil
		} else {
			result.Rationale = &rationale
		}
	}
	return result, nil
}

func parseNoteAIEditResult(content string) (*NoteAIEditResult, error) {
	trimmed := stripNoteAIJSONFences(content)
	var result NoteAIEditResult
	if err := json.Unmarshal([]byte(trimmed), &result); err == nil {
		return validateNoteAIEditResult(&result)
	}
	loose, err := parseLooseNoteAIEditResult(trimmed)
	if err != nil {
		return nil, err
	}
	return validateNoteAIEditResult(loose)
}

func noteAISelectedEditPrompt(prompt string) bool {
	return strings.Contains(prompt, `action MUST be "replace_selection"`) ||
		strings.Contains(prompt, "Selected Markdown excerpt to replace:")
}

func noteAIPageEditPrompt(prompt string) bool {
	hasPageMarker := strings.Contains(prompt, "Full current page Markdown:")
	hasLegacyIntro := strings.Contains(prompt, "You are editing a user's Notion-style note page.")
	hasAssistantIntro := strings.Contains(prompt, "You are the in-note AI assistant for a user's Notion-style note page.")
	return hasPageMarker && (hasLegacyIntro || hasAssistantIntro)
}

func repairSelectedNoteAIEditResult(content, prompt string) (*NoteAIEditResult, error) {
	if !noteAISelectedEditPrompt(prompt) {
		return nil, fmt.Errorf("note AI output repair is only supported for selected edits")
	}
	markdown := stripNoteAIJSONFences(content)
	if extracted, err := extractLooseNoteAIStringField(markdown, "markdown"); err == nil && strings.TrimSpace(extracted) != "" {
		markdown = extracted
	}
	return validateNoteAIEditResult(&NoteAIEditResult{
		Action:   "replace_selection",
		Markdown: markdown,
	})
}

func noteAIInstruction(prompt string) string {
	start := strings.Index(prompt, "<instruction>")
	end := strings.Index(prompt, "</instruction>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	start += len("<instruction>")
	return strings.TrimSpace(prompt[start:end])
}

func noteAIInferPageRepairAction(prompt string, target *string) string {
	if target != nil && strings.TrimSpace(*target) != "" {
		return "patch"
	}
	instruction := strings.ToLower(noteAIInstruction(prompt))
	replacePageTerms := []string{
		"rewrite", "translate", "summarize", "summarise", "reorganize", "reorganise",
		"polish", "whole page", "entire page", "full page", "current page",
		"\u6574\u9875", "\u5168\u6587", "\u91cd\u5199", "\u7ffb\u8bd1", "\u603b\u7ed3", "\u6da6\u8272",
	}
	for _, term := range replacePageTerms {
		if strings.Contains(instruction, term) {
			return "replace_page"
		}
	}
	replaceSelectionTerms := []string{"replace the empty line", "replace this line", "replace cursor block", "\u66ff\u6362\u7a7a\u884c", "\u66ff\u6362\u8fd9\u4e00\u884c"}
	for _, term := range replaceSelectionTerms {
		if strings.Contains(instruction, term) {
			return "replace_selection"
		}
	}
	return "insert"
}

func noteAIOptionalLooseStringField(content, field string) *string {
	value, err := extractLooseNoteAIStringField(content, field)
	if err != nil || strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

func repairPageNoteAIEditResult(content, prompt string) (*NoteAIEditResult, error) {
	if !noteAIPageEditPrompt(prompt) || noteAISelectedEditPrompt(prompt) {
		return nil, fmt.Errorf("note AI page output repair is only supported for page edits")
	}
	trimmed := stripNoteAIJSONFences(content)
	markdown := trimmed
	if extracted, err := extractLooseNoteAIStringField(trimmed, "markdown"); err == nil && strings.TrimSpace(extracted) != "" {
		markdown = extracted
	}
	result := &NoteAIEditResult{
		Markdown:  markdown,
		Target:    noteAIOptionalLooseStringField(trimmed, "target"),
		Title:     noteAIOptionalLooseStringField(trimmed, "title"),
		Rationale: noteAIOptionalLooseStringField(trimmed, "rationale"),
	}
	if actionMatch := noteAIJSONActionRE.FindStringSubmatch(trimmed); len(actionMatch) >= 2 {
		switch strings.TrimSpace(actionMatch[1]) {
		case "insert", "replace_selection", "replace_page", "patch":
			result.Action = strings.TrimSpace(actionMatch[1])
		case "append", "continue", "draft", "add":
			result.Action = "insert"
		case "rewrite", "replace", "page", "edit_page":
			result.Action = "replace_page"
		}
	}
	if result.Action == "" {
		result.Action = noteAIInferPageRepairAction(prompt, result.Target)
	}
	return validateNoteAIEditResult(result)
}

type noteAIParseOutcome struct {
	Result     *NoteAIEditResult
	RepairCode *string
}

const (
	noteAIRepairSelectedOutput = "repaired_selected_output"
	noteAIRepairPageOutput     = "repaired_page_output"
	noteAIFailureInvalidOutput = "invalid_structured_output"
	noteAIFailureEmptyOutput   = "empty_structured_output"
	noteAIFailureAssistant     = "assistant_failure"
	noteAIFailureTask          = "task_failure"
	noteAIFailureTaskError     = "task_error"
)

func parseNoteAIEditResultWithRepairOutcome(content, prompt string) (noteAIParseOutcome, error) {
	result, err := parseNoteAIEditResult(content)
	if err == nil {
		return noteAIParseOutcome{Result: result}, nil
	}
	if repaired, repairErr := repairSelectedNoteAIEditResult(content, prompt); repairErr == nil {
		code := noteAIRepairSelectedOutput
		return noteAIParseOutcome{Result: repaired, RepairCode: &code}, nil
	}
	if repaired, repairErr := repairPageNoteAIEditResult(content, prompt); repairErr == nil {
		code := noteAIRepairPageOutput
		return noteAIParseOutcome{Result: repaired, RepairCode: &code}, nil
	}
	return noteAIParseOutcome{}, err
}

func parseNoteAIEditResultWithRepair(content, prompt string) (*NoteAIEditResult, error) {
	outcome, err := parseNoteAIEditResultWithRepairOutcome(content, prompt)
	return outcome.Result, err
}

func noteAIStatusFromTask(status string, terminalOutcome pgtype.Text, startedAt pgtype.Timestamptz, result *NoteAIEditResult, failure *string) string {
	switch status {
	case "pending", "failed":
		return "queued"
	case "draining":
		if startedAt.Valid {
			return "running"
		}
		return "dispatched"
	case "suppressed":
		return "cancelled"
	case "acked":
		if terminalOutcome.Valid {
			switch terminalOutcome.String {
			case "failed", "cancelled":
				return terminalOutcome.String
			}
		}
		if failure != nil {
			return "failed"
		}
		if result != nil {
			return "completed"
		}
		// Terminal task with neither a parseable edit nor an explicit failure —
		// e.g. completion output was dropped before chat_message persistence.
		// Surface failed so the UI does not treat an empty completed job as success.
		return "failed"
	default:
		return status
	}
}

func (h *Handler) noteAIJobResponse(ctx context.Context, workspaceID, userID, jobID pgtype.UUID) (NoteAIJobResponse, error) {
	var resp NoteAIJobResponse
	var taskStatus string
	var terminalOutcome pgtype.Text
	var taskStartedAt pgtype.Timestamptz
	var taskUpdatedAt pgtype.Timestamptz
	var taskError pgtype.Text
	var taskFailure pgtype.Text
	var assistantContent pgtype.Text
	var assistantFailure pgtype.Text
	var createdAt pgtype.Timestamptz
	var pageID, agentID, chatSessionID, taskID pgtype.UUID
	var prompt string
	var completionOutput pgtype.Text
	err := h.DB.QueryRow(ctx, `
SELECT j.id, j.workspace_id, j.page_id, j.agent_id, j.chat_session_id, j.task_id, j.created_at, j.prompt,
       e.status, e.terminal_outcome, e.started_at, e.updated_at, e.error, e.failure_reason,
       m.content, m.failure_reason, e.result->>'output'
FROM note_ai_job j
JOIN agent_inbox_event e ON e.id = j.task_id
LEFT JOIN LATERAL (
    SELECT content, failure_reason
    FROM chat_message
    WHERE chat_session_id = j.chat_session_id
      AND role = 'assistant'
      AND task_id = j.task_id
    ORDER BY created_at DESC
    LIMIT 1
) m ON true
WHERE j.id = $1 AND j.workspace_id = $2 AND j.creator_id = $3`, jobID, workspaceID, userID).Scan(
		&jobID, &workspaceID, &pageID, &agentID, &chatSessionID, &taskID, &createdAt, &prompt,
		&taskStatus, &terminalOutcome, &taskStartedAt, &taskUpdatedAt, &taskError, &taskFailure,
		&assistantContent, &assistantFailure, &completionOutput,
	)
	if err != nil {
		return resp, err
	}
	var result *NoteAIEditResult
	var failure *string
	var failureCode *string
	var repairCode *string
	outputContent := ""
	if assistantContent.Valid && strings.TrimSpace(assistantContent.String) != "" {
		outputContent = assistantContent.String
	} else if completionOutput.Valid && strings.TrimSpace(completionOutput.String) != "" {
		// Fallback when completion output was stored on the inbox event but never
		// persisted as an assistant chat_message (historical unwrap-drop bug).
		outputContent = completionOutput.String
	}
	if strings.TrimSpace(outputContent) != "" {
		outcome, err := parseNoteAIEditResultWithRepairOutcome(outputContent, prompt)
		if err != nil {
			value := "agent returned invalid structured note AI edit"
			code := noteAIFailureInvalidOutput
			failure = &value
			failureCode = &code
			slog.Warn("note AI structured output invalid",
				"workspace_id", uuidToString(workspaceID),
				"page_id", uuidToString(pageID),
				"job_id", uuidToString(jobID),
				"task_id", uuidToString(taskID),
				"agent_id", uuidToString(agentID),
				"failure_code", code,
				"error", err,
			)
		} else {
			result = outcome.Result
			repairCode = outcome.RepairCode
			if repairCode != nil {
				slog.Info("note AI output repaired",
					"workspace_id", uuidToString(workspaceID),
					"page_id", uuidToString(pageID),
					"job_id", uuidToString(jobID),
					"task_id", uuidToString(taskID),
					"agent_id", uuidToString(agentID),
					"repair_code", *repairCode,
					"action", result.Action,
				)
			}
		}
	}
	switch {
	case assistantFailure.Valid && assistantFailure.String != "":
		value := assistantFailure.String
		code := noteAIFailureAssistant
		failure = &value
		failureCode = &code
	case taskFailure.Valid && taskFailure.String != "":
		value := taskFailure.String
		code := noteAIFailureTask
		failure = &value
		failureCode = &code
	case taskError.Valid && taskError.String != "":
		value := taskError.String
		code := noteAIFailureTaskError
		failure = &value
		failureCode = &code
	case result == nil && failure == nil && taskStatus == "acked":
		value := "agent returned no note AI edit"
		code := noteAIFailureEmptyOutput
		failure = &value
		failureCode = &code
	}
	resp = NoteAIJobResponse{
		ID:            uuidToString(jobID),
		WorkspaceID:   uuidToString(workspaceID),
		PageID:        uuidToString(pageID),
		AgentID:       uuidToString(agentID),
		ChatSessionID: uuidToString(chatSessionID),
		TaskID:        uuidToString(taskID),
		Status:        noteAIStatusFromTask(taskStatus, terminalOutcome, taskStartedAt, result, failure),
		Result:        result,
		FailureReason: failure,
		FailureCode:   failureCode,
		RepairCode:    repairCode,
		CreatedAt:     timestampToString(createdAt),
		UpdatedAt:     timestampToString(taskUpdatedAt),
	}
	return resp, nil
}

func (h *Handler) CreateNoteAIJob(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	var req noteAIJobCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: page.WorkspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	session, err := h.Queries.CreateChatSession(r.Context(), db.CreateChatSessionParams{
		WorkspaceID: page.WorkspaceID,
		AgentID:     agentID,
		CreatorID:   userID,
		Title:       normalizeNoteAIJobTitle(req.Title, page.Title),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note AI job")
		return
	}
	msg, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       prompt,
		Parts:         []byte("[]"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note AI job")
		return
	}
	task, err := h.TaskService.EnqueueChatTask(r.Context(), session, parseUUID(userIDString))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue note AI job: "+err.Error())
		return
	}
	if err := h.Queries.LinkChatMessageToTask(r.Context(), db.LinkChatMessageToTaskParams{ID: msg.ID, TaskID: task.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link note AI job")
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_ai_job (id, workspace_id, page_id, creator_id, agent_id, chat_session_id, task_id, prompt, title)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, task.ID, page.WorkspaceID, page.ID, userID, agentID, session.ID, task.ID, prompt, normalizeNoteAIJobTitle(req.Title, page.Title)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note AI job")
		return
	}
	_, _ = h.Queries.UpdateChatSessionStatus(r.Context(), db.UpdateChatSessionStatusParams{ID: session.ID, Status: "archived"})
	resp, err := h.noteAIJobResponse(r.Context(), page.WorkspaceID, userID, task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note AI job")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) GetNoteAIJob(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	jobID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "jobId"), "note AI job id")
	if !ok {
		return
	}
	resp, err := h.noteAIJobResponse(r.Context(), workspaceID, userID, jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "note AI job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load note AI job")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CancelNoteAIJob(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	jobID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "jobId"), "note AI job id")
	if !ok {
		return
	}
	var taskID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
SELECT task_id
FROM note_ai_job
WHERE id = $1 AND workspace_id = $2 AND creator_id = $3`, jobID, workspaceID, userID).Scan(&taskID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "note AI job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load note AI job")
		return
	}
	if _, err := h.TaskService.CancelTaskWithResult(r.Context(), taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := h.noteAIJobResponse(r.Context(), workspaceID, userID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note AI job")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
