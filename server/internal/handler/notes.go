package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

type noteDuplicateRequest struct {
	Title *string `json:"title"`
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
    SELECT id, parent_id, owner_user_id
    FROM note_page
    WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
  UNION
    SELECT parent.id, parent.parent_id, parent.owner_user_id
    FROM note_page parent
    JOIN chain child ON child.parent_id = parent.id
    WHERE parent.workspace_id = $2 AND parent.deleted_at IS NULL
)
SELECT
  EXISTS (
    SELECT 1
    FROM chain c
    WHERE c.owner_user_id = $3
       OR EXISTS (SELECT 1 FROM note_page_share s WHERE s.page_id = c.id AND s.user_id = $3)
  ) AS accessible,
  EXISTS (
    SELECT 1
    FROM note_page p
    WHERE p.id = $1 AND p.workspace_id = $2 AND p.deleted_at IS NULL AND p.owner_user_id = $3
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
WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`, pageUUID, workspaceID))
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
    WHERE p.workspace_id = $1
      AND p.deleted_at IS NULL
      AND (
        p.owner_user_id = $2
        OR EXISTS (SELECT 1 FROM note_page_share s WHERE s.page_id = p.id AND s.user_id = $2)
      )
  UNION
    SELECT child.id, child.workspace_id, child.parent_id, child.owner_user_id, child.title, child.content, child.sort_key, child.created_at, child.updated_at, child.deleted_at
    FROM note_page child
    JOIN visible parent ON child.parent_id = parent.id
    WHERE child.workspace_id = $1 AND child.deleted_at IS NULL
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
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT p.id, p.workspace_id, p.parent_id, p.owner_user_id, p.title, p.content, p.sort_key, p.created_at, p.updated_at, p.deleted_at
FROM note_page p
LEFT JOIN note_page parent ON parent.id = p.parent_id AND parent.workspace_id = p.workspace_id
WHERE p.workspace_id = $1
  AND p.owner_user_id = $2
  AND p.deleted_at IS NOT NULL
  AND (p.parent_id IS NULL OR parent.deleted_at IS NULL)
ORDER BY p.deleted_at DESC, p.updated_at DESC`, workspaceID, userID)
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
	if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, *req.ParentID, "parent id")
		if !ok {
			return
		}
		accessible, _, err := h.noteAccess(r.Context(), parsed, workspaceID, userID)
		if err != nil || !accessible {
			writeError(w, http.StatusNotFound, "parent note page not found")
			return
		}
		parentID = parsed
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, workspaceID, parentID, userID, normalizeNoteTitle(req.Title)))
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
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, page.ID, workspaceID, userID, title, content))
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
WHERE id IN (SELECT id FROM subtree)`, page.ID, workspaceID, userID)
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
ORDER BY depth, parent_id NULLS FIRST, sort_key, created_at`, page.ID, workspaceID, userID)
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
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`, workspaceID, parentID, userID, title, source.Content, sortKey))
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

func (h *Handler) RestoreNotePage(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
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
WHERE id = $1 AND workspace_id = $2 AND owner_user_id = $3 AND deleted_at IS NOT NULL`, pageUUID, workspaceID, userID))
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
WHERE id IN (SELECT id FROM subtree)`, page.ID, workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore note page")
		return
	}
	restored, err := scanNotePage(h.DB.QueryRow(r.Context(), `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page
WHERE id = $1 AND workspace_id = $2`, page.ID, workspaceID))
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
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2)`, workspaceID, targetID).Scan(&exists); err != nil || !exists {
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
