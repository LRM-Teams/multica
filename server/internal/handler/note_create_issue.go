package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type noteCreateIssueRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

type NoteCreateIssueResponse struct {
	Issue IssueResponse            `json:"issue"`
	Ref   NotePageIssueRefResponse `json:"ref"`
}

func normalizeNoteIssueTitle(title, fallback string) string {
	title = strings.TrimSpace(strings.Join(strings.Fields(title), " "))
	if title == "" {
		title = strings.TrimSpace(strings.Join(strings.Fields(fallback), " "))
	}
	if title == "" {
		title = "Untitled"
	}
	if utf8.RuneCountInString(title) > 200 {
		runes := []rune(title)
		title = string(runes[:200])
	}
	return title
}

// CreateNotePageIssue creates an Issue from a note (S1-A1), then writes the
// note→issue association. Does not assign an agent (Worker is Slice 2).
func (h *Handler) CreateNotePageIssue(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDStr, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req noteCreateIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := normalizeNoteIssueTitle(req.Title, page.Title)
	description := pgtype.Text{}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		if desc != "" {
			description = pgtype.Text{String: desc, Valid: true}
		}
	}

	creatorType, actualCreatorID := h.resolveActor(r, userIDStr, uuidToString(page.WorkspaceID))
	prefix := h.getIssuePrefix(r.Context(), page.WorkspaceID)

	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:    page.WorkspaceID,
		Title:          title,
		Description:    description,
		Status:         "todo",
		Priority:       "none",
		CreatorType:    creatorType,
		CreatorID:      parseUUID(actualCreatorID),
		AllowDuplicate: true,
	}, service.IssueCreateOpts{
		ActorID: actualCreatorID,
		Platform: func() string {
			p, _, _ := middleware.ClientMetadataFromContext(r.Context())
			return p
		}(),
		BroadcastPayload: func(issue db.Issue, _ []db.Attachment) map[string]any {
			payload := issueToResponse(issue, prefix)
			return map[string]any{"issue": payload}
		},
	})
	if err != nil {
		slog.Warn("create issue from note failed", append(logger.RequestAttrs(r), "error", err, "note_id", uuidToString(page.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create issue from note")
		return
	}

	issue := res.Issue
	var createdAt pgtype.Timestamptz
	err = h.DB.QueryRow(r.Context(), `
INSERT INTO note_page_issue_ref (page_id, issue_id, workspace_id, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (page_id, issue_id) DO UPDATE
SET created_by = note_page_issue_ref.created_by
RETURNING created_at`, page.ID, issue.ID, page.WorkspaceID, userID).Scan(&createdAt)
	if err != nil {
		slog.Warn("link note to new issue failed", append(logger.RequestAttrs(r), "error", err, "note_id", uuidToString(page.ID), "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "issue created but failed to link note")
		return
	}

	h.syncWendyWorkGraphAfterIssueCreate(r.Context(), issue)
	if creatorType == "member" {
		h.awardHonorXP(r.Context(), parseUUID(actualCreatorID), "issue.create", uuidToString(issue.ID))
	}

	writeJSON(w, http.StatusCreated, NoteCreateIssueResponse{
		Issue: issueToResponse(issue, prefix),
		Ref: accessibleIssueRef(
			uuidToString(page.ID),
			uuidToString(issue.ID),
			uuidToString(page.WorkspaceID),
			timestampToString(createdAt),
			prefix,
			issue.Title,
			issue.Number,
		),
	})
}
