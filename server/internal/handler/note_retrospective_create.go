package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type createNoteRetrospectiveRequest struct {
	Window    string   `json:"window"` // day | week
	Date      string   `json:"date"`   // YYYY-MM-DD in timezone
	Timezone  string   `json:"timezone"`
	Sources   []string `json:"sources"`
}

type noteRetrospectiveWindowResponse struct {
	Kind     string `json:"kind"`
	Timezone string `json:"timezone"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Label    string `json:"label"`
}

type createNoteRetrospectiveResponse struct {
	Page            NotePageResponse                 `json:"page"`
	Window          noteRetrospectiveWindowResponse  `json:"window"`
	SourcesUsed     []string                         `json:"sources_used"`
	SourcesEmpty    []string                         `json:"sources_empty"`
	SourcesSkipped  []string                         `json:"sources_skipped"`
	FactCount       int                              `json:"fact_count"`
}

// CreateNoteRetrospective aggregates Facts in a viewing-timezone window and
// writes a private note under 回顾/ (S4-S1). Missing sources degrade honestly.
func (h *Handler) CreateNoteRetrospective(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	var req createNoteRetrospectiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = h.resolveViewingTZ(r)
	}
	window, err := resolveNoteRetrospectiveWindow(noteRetrospectiveWindowKind(strings.TrimSpace(req.Window)), req.Date, tz, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled, skipped := normalizeNoteRetrospectiveSources(req.Sources)

	var facts noteRetrospectiveFacts
	var used, empty []string
	for _, source := range enabled {
		switch source {
		case noteRetrospectiveSourceIssue:
			items, err := h.loadNoteRetrospectiveIssueFacts(r.Context(), workspaceID, userID, window.Start, window.End)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load issue activity")
				return
			}
			facts.Issues = items
			if len(items) == 0 {
				empty = append(empty, source)
			} else {
				used = append(used, source)
			}
		case noteRetrospectiveSourceNotes:
			items, err := h.loadNoteRetrospectiveNoteFacts(r.Context(), workspaceID, userID, window.Start, window.End)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load touched notes")
				return
			}
			facts.Notes = items
			if len(items) == 0 {
				empty = append(empty, source)
			} else {
				used = append(used, source)
			}
		case noteRetrospectiveSourceRuns:
			// Not wired yet — honest empty / deferred (S4-S1 MVP).
			empty = append(empty, source)
		}
	}

	title, content := buildNoteRetrospectiveMarkdown(window, facts, used, empty, skipped)
	folderID, err := h.ensureNoteRetrospectiveFolder(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure retrospective folder")
		return
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, folderID, userID, normalizeNoteTitle(title), content))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create retrospective note")
		return
	}

	// Sync issue mentions into note_page_issue_ref so reverse discovery works.
	if len(facts.Issues) > 0 {
		seen := map[string]struct{}{}
		for _, fact := range facts.Issues {
			if fact.IssueID == "" {
				continue
			}
			if _, ok := seen[fact.IssueID]; ok {
				continue
			}
			seen[fact.IssueID] = struct{}{}
			issueUUID := parseUUID(fact.IssueID)
			_, _ = h.DB.Exec(r.Context(), `
INSERT INTO note_page_issue_ref (workspace_id, page_id, issue_id, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (page_id, issue_id) DO NOTHING`, workspaceID, page.ID, issueUUID, userID)
		}
	}

	writeJSON(w, http.StatusCreated, createNoteRetrospectiveResponse{
		Page: notePageToResponse(page, userID, []string{}, nil),
		Window: noteRetrospectiveWindowResponse{
			Kind:     string(window.Kind),
			Timezone: window.Timezone,
			Start:    window.Start.UTC().Format(time.RFC3339),
			End:      window.End.UTC().Format(time.RFC3339),
			Label:    window.Label,
		},
		SourcesUsed:    used,
		SourcesEmpty:   empty,
		SourcesSkipped: skipped,
		FactCount:      len(facts.Issues) + len(facts.Notes),
	})
}

func (h *Handler) ensureNoteRetrospectiveFolder(ctx context.Context, workspaceID, userID pgtype.UUID) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id FROM note_page
WHERE workspace_id = $1
  AND owner_user_id = $2
  AND parent_id IS NULL
  AND deleted_at IS NULL
  AND title = $3
ORDER BY created_at ASC
LIMIT 1`, workspaceID, userID, noteRetrospectiveFolderTitle).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, err
	}
	page, err := scanNotePage(h.DB.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, NULL, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, userID, noteRetrospectiveFolderTitle))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return page.ID, nil
}

func (h *Handler) loadNoteRetrospectiveIssueFacts(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
) ([]noteRetrospectiveIssueFact, error) {
	rows, err := h.DB.Query(ctx, `
SELECT a.action, a.details, a.created_at, i.id, i.number, i.title, COALESCE(w.issue_prefix, '')
FROM activity_log a
JOIN issue i ON i.id = a.issue_id AND i.workspace_id = a.workspace_id
JOIN workspace w ON w.id = a.workspace_id
WHERE a.workspace_id = $1
  AND a.actor_type = 'member'
  AND a.actor_id = $2
  AND a.created_at >= $3
  AND a.created_at < $4
  AND a.action IN ('status_changed', 'created', 'assignee_changed', 'priority_changed')
ORDER BY a.created_at ASC, a.id ASC
LIMIT 200`, workspaceID, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]noteRetrospectiveIssueFact, 0)
	for rows.Next() {
		var (
			action     string
			details    []byte
			createdAt  time.Time
			issueID    pgtype.UUID
			number     int32
			title      string
			prefix     string
		)
		if err := rows.Scan(&action, &details, &createdAt, &issueID, &number, &title, &prefix); err != nil {
			return nil, err
		}
		identifier := fmt.Sprintf("%s-%d", prefix, number)
		if strings.TrimSpace(prefix) == "" {
			identifier = uuidToString(issueID)
		}
		out = append(out, noteRetrospectiveIssueFact{
			IssueID:    uuidToString(issueID),
			Identifier: identifier,
			Title:      title,
			Action:     action,
			Detail:     formatIssueActivityDetail(action, details),
			At:         createdAt.UTC(),
		})
	}
	return out, rows.Err()
}

func (h *Handler) loadNoteRetrospectiveNoteFacts(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
) ([]noteRetrospectiveNoteFact, error) {
	rows, err := h.DB.Query(ctx, `
SELECT id, title, updated_at
FROM note_page
WHERE workspace_id = $1
  AND owner_user_id = $2
  AND deleted_at IS NULL
  AND updated_at >= $3
  AND updated_at < $4
  AND title <> $5
ORDER BY updated_at DESC, id ASC
LIMIT 100`, workspaceID, userID, start, end, noteRetrospectiveFolderTitle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]noteRetrospectiveNoteFact, 0)
	for rows.Next() {
		var (
			id        pgtype.UUID
			title     string
			updatedAt time.Time
		)
		if err := rows.Scan(&id, &title, &updatedAt); err != nil {
			return nil, err
		}
		// Skip pages whose title looks like a generated retrospective leaf.
		if strings.HasPrefix(title, "回顾 ") {
			continue
		}
		out = append(out, noteRetrospectiveNoteFact{
			PageID: uuidToString(id),
			Title:  title,
			At:     updatedAt.UTC(),
		})
	}
	return out, rows.Err()
}
