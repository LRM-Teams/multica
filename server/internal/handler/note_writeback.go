package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type noteWritebackEvidence struct {
	Type  string  `json:"type"`
	ID    string  `json:"id"`
	Label *string `json:"label,omitempty"`
}

type NoteWritebackResponse struct {
	ID            string                  `json:"id"`
	WorkspaceID   string                  `json:"workspace_id"`
	PageID        string                  `json:"page_id"`
	Action        string                  `json:"action"`
	Content       string                  `json:"content"`
	Target        *string                 `json:"target,omitempty"`
	Evidence      []noteWritebackEvidence `json:"evidence"`
	Status        string                  `json:"status"`
	CreatedByType string                  `json:"created_by_type"`
	CreatedByID   string                  `json:"created_by_id"`
	ResolvedBy    *string                 `json:"resolved_by,omitempty"`
	ResolvedAt    *string                 `json:"resolved_at,omitempty"`
	CreatedAt     string                  `json:"created_at"`
	UpdatedAt     string                  `json:"updated_at"`
}

type NoteWritebackListResponse struct {
	Writebacks []NoteWritebackResponse `json:"writebacks"`
}

type noteWritebackCreateRequest struct {
	Action   string                  `json:"action"`
	Content  string                  `json:"content"`
	Target   *string                 `json:"target"`
	Evidence []noteWritebackEvidence `json:"evidence"`
}

type noteWritebackProposalRow struct {
	ID            pgtype.UUID
	WorkspaceID   pgtype.UUID
	PageID        pgtype.UUID
	Action        string
	Content       string
	Target        pgtype.Text
	Evidence      []byte
	Status        string
	CreatedByType string
	CreatedByID   pgtype.UUID
	ResolvedBy    pgtype.UUID
	ResolvedAt    pgtype.Timestamptz
	CreatedAt     pgtype.Timestamptz
	UpdatedAt     pgtype.Timestamptz
}

func validateNoteWritebackEvidence(items []noteWritebackEvidence) ([]noteWritebackEvidence, error) {
	if len(items) == 0 {
		return nil, errors.New("evidence is required")
	}
	out := make([]noteWritebackEvidence, 0, len(items))
	for _, item := range items {
		typ := strings.TrimSpace(item.Type)
		id := strings.TrimSpace(item.ID)
		if typ == "" || id == "" {
			return nil, errors.New("each evidence item requires type and id")
		}
		cleaned := noteWritebackEvidence{Type: typ, ID: id}
		if item.Label != nil {
			label := strings.TrimSpace(*item.Label)
			if label != "" {
				cleaned.Label = &label
			}
		}
		out = append(out, cleaned)
	}
	return out, nil
}

func applyNoteWritebackContent(current, action, content string, target *string) (string, error) {
	switch action {
	case "append":
		cur := strings.TrimRight(current, "\n")
		add := strings.TrimSpace(content)
		if add == "" {
			return "", errors.New("content is required")
		}
		if cur == "" {
			return add, nil
		}
		return cur + "\n\n" + add, nil
	case "replace_page":
		if strings.TrimSpace(content) == "" {
			return "", errors.New("content is required")
		}
		return content, nil
	case "patch":
		if target == nil || strings.TrimSpace(*target) == "" {
			return "", errors.New("target is required for patch")
		}
		needle := *target
		if !strings.Contains(current, needle) {
			loose := strings.TrimSpace(needle)
			if loose == "" || !strings.Contains(current, loose) {
				return "", errors.New("patch target not found in note content")
			}
			needle = loose
		}
		return strings.Replace(current, needle, content, 1), nil
	default:
		return "", errors.New("unsupported action")
	}
}

func noteWritebackToResponse(row noteWritebackProposalRow) (NoteWritebackResponse, error) {
	evidence := []noteWritebackEvidence{}
	if len(row.Evidence) > 0 {
		if err := json.Unmarshal(row.Evidence, &evidence); err != nil {
			return NoteWritebackResponse{}, err
		}
	}
	resp := NoteWritebackResponse{
		ID:            uuidToString(row.ID),
		WorkspaceID:   uuidToString(row.WorkspaceID),
		PageID:        uuidToString(row.PageID),
		Action:        row.Action,
		Content:       row.Content,
		Evidence:      evidence,
		Status:        row.Status,
		CreatedByType: row.CreatedByType,
		CreatedByID:   uuidToString(row.CreatedByID),
		CreatedAt:     timestampToString(row.CreatedAt),
		UpdatedAt:     timestampToString(row.UpdatedAt),
	}
	if row.Target.Valid {
		t := row.Target.String
		resp.Target = &t
	}
	if row.ResolvedBy.Valid {
		s := uuidToString(row.ResolvedBy)
		resp.ResolvedBy = &s
	}
	if row.ResolvedAt.Valid {
		s := timestampToString(row.ResolvedAt)
		resp.ResolvedAt = &s
	}
	return resp, nil
}

func scanNoteWriteback(row pgx.Row) (noteWritebackProposalRow, error) {
	var r noteWritebackProposalRow
	err := row.Scan(
		&r.ID, &r.WorkspaceID, &r.PageID, &r.Action, &r.Content, &r.Target, &r.Evidence,
		&r.Status, &r.CreatedByType, &r.CreatedByID, &r.ResolvedBy, &r.ResolvedAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	return r, err
}

const noteWritebackSelectCols = `
id, workspace_id, page_id, action, content, target, evidence,
status, created_by_type, created_by_id, resolved_by, resolved_at,
created_at, updated_at`

// CreateNotePageWriteback stores a pending writeback proposal (S1-W1 / D1).
// Does not mutate note_page.content until AcceptNotePageWriteback.
func (h *Handler) CreateNotePageWriteback(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDStr, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req noteWritebackCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "append", "patch", "replace_page":
	default:
		writeError(w, http.StatusBadRequest, "action must be append, patch, or replace_page")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if action == "patch" && (req.Target == nil || strings.TrimSpace(*req.Target) == "") {
		writeError(w, http.StatusBadRequest, "target is required for patch")
		return
	}
	evidence, err := validateNoteWritebackEvidence(req.Evidence)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode evidence")
		return
	}

	creatorType, creatorID := h.resolveActor(r, userIDStr, uuidToString(page.WorkspaceID))
	var target pgtype.Text
	if req.Target != nil {
		t := strings.TrimSpace(*req.Target)
		if t != "" {
			target = pgtype.Text{String: t, Valid: true}
		}
	}

	row, err := scanNoteWriteback(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page_writeback (
  workspace_id, page_id, action, content, target, evidence,
  created_by_type, created_by_id
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
RETURNING `+noteWritebackSelectCols, page.WorkspaceID, page.ID, action, req.Content, target, evidenceJSON, creatorType, parseUUID(creatorID)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create writeback")
		return
	}
	resp, err := noteWritebackToResponse(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create writeback")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) ListNotePageWritebacks(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	var rows pgx.Rows
	var err error
	if statusFilter == "" {
		rows, err = h.DB.Query(r.Context(), `
SELECT `+noteWritebackSelectCols+`
FROM note_page_writeback
WHERE page_id = $1
ORDER BY created_at DESC`, page.ID)
	} else {
		switch statusFilter {
		case "pending", "applied", "rejected":
		default:
			writeError(w, http.StatusBadRequest, "invalid status filter")
			return
		}
		rows, err = h.DB.Query(r.Context(), `
SELECT `+noteWritebackSelectCols+`
FROM note_page_writeback
WHERE page_id = $1 AND status = $2
ORDER BY created_at DESC`, page.ID, statusFilter)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list writebacks")
		return
	}
	defer rows.Close()

	out := make([]NoteWritebackResponse, 0)
	for rows.Next() {
		row, err := scanNoteWriteback(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list writebacks")
			return
		}
		resp, err := noteWritebackToResponse(row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list writebacks")
			return
		}
		out = append(out, resp)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list writebacks")
		return
	}
	writeJSON(w, http.StatusOK, NoteWritebackListResponse{Writebacks: out})
}

func (h *Handler) loadPendingWritebackForUser(w http.ResponseWriter, r *http.Request, writebackID string, workspaceID, userID pgtype.UUID) (noteWritebackProposalRow, notePageRow, bool) {
	wbUUID, ok := parseUUIDOrBadRequest(w, writebackID, "writeback id")
	if !ok {
		return noteWritebackProposalRow{}, notePageRow{}, false
	}
	row, err := scanNoteWriteback(h.DB.QueryRow(r.Context(), `
SELECT `+noteWritebackSelectCols+`
FROM note_page_writeback
WHERE id = $1`, wbUUID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "writeback not found")
			return noteWritebackProposalRow{}, notePageRow{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load writeback")
		return noteWritebackProposalRow{}, notePageRow{}, false
	}
	page, _, ok := h.loadAccessibleNote(w, r, uuidToString(row.PageID), workspaceID, userID)
	if !ok {
		return noteWritebackProposalRow{}, notePageRow{}, false
	}
	if row.Status != "pending" {
		writeError(w, http.StatusConflict, "writeback is not pending")
		return noteWritebackProposalRow{}, notePageRow{}, false
	}
	return row, page, true
}

func (h *Handler) AcceptNotePageWriteback(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	row, page, ok := h.loadPendingWritebackForUser(w, r, chi.URLParam(r, "writebackId"), workspaceID, userID)
	if !ok {
		return
	}

	var target *string
	if row.Target.Valid {
		t := row.Target.String
		target = &t
	}
	nextContent, err := applyNoteWritebackContent(page.Content, row.Action, row.Content, target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept writeback")
		return
	}
	defer tx.Rollback(context.Background())

	tag, err := tx.Exec(r.Context(), `
UPDATE note_page_writeback
SET status = 'applied',
    resolved_by = $2,
    resolved_at = now(),
    updated_at = now()
WHERE id = $1 AND status = 'pending'`, row.ID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept writeback")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "writeback is not pending")
		return
	}

	if _, err := tx.Exec(r.Context(), `
UPDATE note_page
SET content = $3, updated_by = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL`, page.ID, userID, nextContent); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply writeback to note")
		return
	}

	applied, err := scanNoteWriteback(tx.QueryRow(r.Context(), `
SELECT `+noteWritebackSelectCols+`
FROM note_page_writeback WHERE id = $1`, row.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept writeback")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept writeback")
		return
	}

	resp, err := noteWritebackToResponse(applied)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept writeback")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) RejectNotePageWriteback(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	row, _, ok := h.loadPendingWritebackForUser(w, r, chi.URLParam(r, "writebackId"), workspaceID, userID)
	if !ok {
		return
	}

	rejected, err := scanNoteWriteback(h.DB.QueryRow(r.Context(), `
UPDATE note_page_writeback
SET status = 'rejected',
    resolved_by = $2,
    resolved_at = now(),
    updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING `+noteWritebackSelectCols, row.ID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "writeback is not pending")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reject writeback")
		return
	}

	resp, err := noteWritebackToResponse(rejected)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reject writeback")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
