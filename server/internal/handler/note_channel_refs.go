package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	notePageChannelRefKindWorker       = "worker"
	notePageChannelRefKindCoordination = "coordination"
)

func normalizeNotePageChannelRefKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case notePageChannelRefKindCoordination:
		return notePageChannelRefKindCoordination
	default:
		return notePageChannelRefKindWorker
	}
}

func accessibleChannelRef(pageID, channelID, workspaceID, createdAt, name, kind string) NotePageIssueRefResponse {
	label := name
	if strings.TrimSpace(label) == "" {
		label = channelID
	}
	return NotePageIssueRefResponse{
		Type:        "channel",
		ID:          channelID,
		Label:       &label,
		Accessible:  true,
		PageID:      pageID,
		WorkspaceID: workspaceID,
		Title:       name,
		Identifier:  kind,
		CreatedAt:   createdAt,
	}
}

func inaccessibleChannelRef(channelID string) NotePageIssueRefResponse {
	return NotePageIssueRefResponse{
		Type:       "channel",
		ID:         channelID,
		Accessible: false,
	}
}

// loadNotePageChannelRefs returns note→channel anchors (N2-A1).
// Inaccessible / non-member channels are stubbed without name (no title leak).
func (h *Handler) loadNotePageChannelRefs(
	ctx context.Context,
	pageID, pageWorkspaceID, userID pgtype.UUID,
) ([]NotePageIssueRefResponse, error) {
	rows, err := h.DB.Query(ctx, `
SELECT
  r.page_id,
  r.channel_id,
  r.workspace_id,
  r.kind,
  r.created_at,
  c.id IS NOT NULL
    AND c.workspace_id = r.workspace_id
    AND EXISTS (
      SELECT 1 FROM channel_member cm
      WHERE cm.channel_id = c.id
        AND cm.workspace_id = r.workspace_id
        AND cm.member_type = 'user'
        AND cm.member_id = $3
    ) AS accessible,
  c.name
FROM note_page_channel_ref r
LEFT JOIN channel c ON c.id = r.channel_id AND c.workspace_id = $2
WHERE r.page_id = $1
ORDER BY r.created_at ASC, r.channel_id ASC`, pageID, pageWorkspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make([]NotePageIssueRefResponse, 0)
	for rows.Next() {
		var (
			pageUUID, channelUUID, refWorkspaceID pgtype.UUID
			kind                                  string
			createdAt                             pgtype.Timestamptz
			accessible                            bool
			name                                  pgtype.Text
		)
		if err := rows.Scan(
			&pageUUID, &channelUUID, &refWorkspaceID, &kind, &createdAt,
			&accessible, &name,
		); err != nil {
			return nil, err
		}
		channelID := uuidToString(channelUUID)
		if !accessible {
			refs = append(refs, inaccessibleChannelRef(channelID))
			continue
		}
		refs = append(refs, accessibleChannelRef(
			uuidToString(pageUUID),
			channelID,
			uuidToString(refWorkspaceID),
			timestampToString(createdAt),
			name.String,
			kind,
		))
	}
	return refs, rows.Err()
}

type notePageChannelRefCreateRequest struct {
	ChannelID string `json:"channel_id"`
	Kind      string `json:"kind"`
}

func (h *Handler) ListNotePageChannelRefs(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	refs, err := h.loadNotePageChannelRefs(r.Context(), page.ID, page.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note channel refs")
		return
	}
	writeJSON(w, http.StatusOK, NotePageIssueRefListResponse{Refs: refs})
}

func (h *Handler) CreateNotePageChannelRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req notePageChannelRefCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	channelIDStr := strings.TrimSpace(req.ChannelID)
	if channelIDStr == "" {
		writeError(w, http.StatusBadRequest, "channel_id is required")
		return
	}
	channelUUID, ok := parseUUIDOrBadRequest(w, channelIDStr, "channel_id")
	if !ok {
		return
	}
	kind := normalizeNotePageChannelRefKind(req.Kind)

	var (
		channelName string
		channelKind string
	)
	err := h.DB.QueryRow(r.Context(), `
SELECT c.name, c.kind
FROM channel c
JOIN channel_member cm ON cm.channel_id = c.id
  AND cm.workspace_id = c.workspace_id
  AND cm.member_type = 'user'
  AND cm.member_id = $3
WHERE c.id = $1 AND c.workspace_id = $2`, channelUUID, page.WorkspaceID, userID).Scan(&channelName, &channelKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load channel")
		return
	}
	if channelKind == "dm" {
		writeError(w, http.StatusBadRequest, "dm channels cannot be note collaboration anchors")
		return
	}

	createdAt, err := h.upsertNotePageChannelRef(
		r.Context(), page.ID, channelUUID, page.WorkspaceID, userID, kind,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note channel ref")
		return
	}

	writeJSON(w, http.StatusCreated, accessibleChannelRef(
		uuidToString(page.ID),
		uuidToString(channelUUID),
		uuidToString(page.WorkspaceID),
		timestampToString(createdAt),
		channelName,
		kind,
	))
}

func (h *Handler) DeleteNotePageChannelRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	channelUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
DELETE FROM note_page_channel_ref
WHERE page_id = $1 AND channel_id = $2 AND workspace_id = $3`, page.ID, channelUUID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete note channel ref")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "note channel ref not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upsertNotePageChannelRef inserts or no-ops an existing (page, channel) row.
func (h *Handler) upsertNotePageChannelRef(
	ctx context.Context,
	pageID, channelID, workspaceID, createdBy pgtype.UUID,
	kind string,
) (pgtype.Timestamptz, error) {
	kind = normalizeNotePageChannelRefKind(kind)
	var createdAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
INSERT INTO note_page_channel_ref (page_id, channel_id, workspace_id, kind, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (page_id, channel_id) DO UPDATE
SET kind = note_page_channel_ref.kind
RETURNING created_at`, pageID, channelID, workspaceID, kind, createdBy).Scan(&createdAt)
	return createdAt, err
}

// bestEffortUpsertNotePageChannelRef logs and swallows errors so Worker dispatch
// is not rolled back when the collaboration anchor fails (N2-A2).
func (h *Handler) bestEffortUpsertNotePageChannelRef(
	ctx context.Context,
	pageID, channelID, workspaceID, createdBy pgtype.UUID,
	kind string,
) {
	if _, err := h.upsertNotePageChannelRef(ctx, pageID, channelID, workspaceID, createdBy, kind); err != nil {
		slog.Warn("note_page_channel_ref upsert failed after worker dispatch",
			"page_id", uuidToString(pageID),
			"channel_id", uuidToString(channelID),
			"error", err,
		)
	}
}
