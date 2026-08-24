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
	Window   string   `json:"window"` // day | week | month
	Date     string   `json:"date"`   // YYYY-MM-DD in timezone
	Timezone string   `json:"timezone"`
	Sources  []string `json:"sources"`
}

type noteRetrospectiveWindowResponse struct {
	Kind     string `json:"kind"`
	Timezone string `json:"timezone"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Label    string `json:"label"`
}

type createNoteRetrospectiveResponse struct {
	Page           NotePageResponse                `json:"page"`
	Window         noteRetrospectiveWindowResponse `json:"window"`
	SourcesUsed    []string                        `json:"sources_used"`
	SourcesEmpty   []string                        `json:"sources_empty"`
	SourcesSkipped []string                        `json:"sources_skipped"`
	FactCount      int                             `json:"fact_count"`
	Composition    string                          `json:"composition"`
	LayersUsed     []string                        `json:"layers_used"`
	ChildPagesUsed []string                        `json:"child_pages_used"`
}

// CreateNoteRetrospective aggregates Facts in a viewing-timezone window and
// writes a private note under 回顾/ (S4-S1). Missing sources degrade honestly.
// S4-S2 buckets issue/note facts into 亲手 / 委派 Agent / 仅相关.
// S4-S3 week/month compose from day/week summaries (not full raw dumps).
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
	bundle, err := h.loadNoteRetrospectiveFactsBundle(r.Context(), workspaceID, userID, window.Start, window.End, req.Sources)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load retrospective facts")
		return
	}
	facts := bundle.Facts
	used := bundle.SourcesUsed
	empty := bundle.SourcesEmpty
	skipped := bundle.SourcesSkipped

	folderID, err := h.ensureNoteRetrospectiveFolder(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure retrospective folder")
		return
	}

	composition := noteRetrospectiveCompositionDayRaw
	layersUsed := make([]string, 0)
	childPagesUsed := make([]string, 0)
	var title, content string
	if window.Kind == noteRetrospectiveWindowDay {
		title, content = buildNoteRetrospectiveMarkdown(window, facts, used, empty, skipped)
	} else {
		children, err := h.loadNoteRetrospectiveChildNotes(r.Context(), workspaceID, userID, folderID, expectedNoteRetrospectiveChildTitles(window))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load child retrospective notes")
			return
		}
		summaries, layers := composeNoteRetrospectivePeriodSummaries(window, facts, children)
		layersUsed = layers
		for _, summary := range summaries {
			if summary.Source == "existing_note" && summary.PageID != "" {
				childPagesUsed = append(childPagesUsed, summary.PageID)
			}
		}
		title, content = buildNoteRetrospectiveLayeredMarkdown(window, summaries, layers, used, empty, skipped)
		composition = noteRetrospectiveCompositionLayered
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
	// Sync run mentions for reverse discovery (same pattern as issue refs).
	if len(facts.Runs) > 0 {
		seen := map[string]struct{}{}
		for _, fact := range facts.Runs {
			if fact.RunID == "" || fact.AgentID == "" {
				continue
			}
			if _, ok := seen[fact.RunID]; ok {
				continue
			}
			seen[fact.RunID] = struct{}{}
			_, _ = h.DB.Exec(r.Context(), `
INSERT INTO note_page_run_ref (workspace_id, page_id, run_id, agent_id, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (page_id, run_id) DO NOTHING`,
				workspaceID, page.ID, parseUUID(fact.RunID), parseUUID(fact.AgentID), userID)
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
		FactCount:      len(facts.Issues) + len(facts.Notes) + len(facts.Runs),
		Composition:    composition,
		LayersUsed:     layersUsed,
		ChildPagesUsed: childPagesUsed,
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

func (h *Handler) loadNoteRetrospectiveChildNotes(
	ctx context.Context,
	workspaceID, userID, folderID pgtype.UUID,
	titles []string,
) (map[string]noteRetrospectiveChildNote, error) {
	out := map[string]noteRetrospectiveChildNote{}
	if len(titles) == 0 {
		return out, nil
	}
	rows, err := h.DB.Query(ctx, `
SELECT id, title, content
FROM note_page
WHERE workspace_id = $1
  AND owner_user_id = $2
  AND parent_id = $3
  AND deleted_at IS NULL
  AND title = ANY($4::text[])`, workspaceID, userID, folderID, titles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id      pgtype.UUID
			title   string
			content string
		)
		if err := rows.Scan(&id, &title, &content); err != nil {
			return nil, err
		}
		out[title] = noteRetrospectiveChildNote{
			PageID:  uuidToString(id),
			Title:   title,
			Content: content,
		}
	}
	return out, rows.Err()
}

func (h *Handler) loadNoteRetrospectiveIssueFacts(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
) ([]noteRetrospectiveIssueFact, error) {
	viewerID := uuidToString(userID)
	// S4-S2: include viewer actions plus activity on issues related to the
	// viewer (creator / assignee / previously assigned an agent), then bucket
	// into hands_on / delegated / related.
	rows, err := h.DB.Query(ctx, `
SELECT a.action, a.details, a.created_at, a.actor_type, a.actor_id,
       i.id, i.number, i.title, COALESCE(w.issue_prefix, ''),
       COALESCE(ag_actor.name, ''), COALESCE(ag_to.name, '')
FROM activity_log a
JOIN issue i ON i.id = a.issue_id AND i.workspace_id = a.workspace_id
JOIN workspace w ON w.id = a.workspace_id
LEFT JOIN agent ag_actor ON a.actor_type = 'agent' AND ag_actor.id = a.actor_id
LEFT JOIN agent ag_to ON a.action = 'assignee_changed'
  AND a.details->>'to_type' = 'agent'
  AND ag_to.id::text = a.details->>'to_id'
WHERE a.workspace_id = $1
  AND a.created_at >= $3
  AND a.created_at < $4
  AND a.action IN ('status_changed', 'created', 'assignee_changed', 'priority_changed')
  AND (
    (a.actor_type = 'member' AND a.actor_id = $2)
    OR (i.creator_type = 'member' AND i.creator_id = $2)
    OR (i.assignee_type = 'member' AND i.assignee_id = $2)
    OR EXISTS (
      SELECT 1 FROM activity_log a2
      WHERE a2.issue_id = i.id
        AND a2.workspace_id = a.workspace_id
        AND a2.actor_type = 'member'
        AND a2.actor_id = $2
        AND a2.action = 'assignee_changed'
        AND a2.details->>'to_type' = 'agent'
    )
  )
ORDER BY a.created_at ASC, a.id ASC
LIMIT 300`, workspaceID, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]noteRetrospectiveIssueFact, 0)
	for rows.Next() {
		var (
			action      string
			details     []byte
			createdAt   time.Time
			actorType   string
			actorID     pgtype.UUID
			issueID     pgtype.UUID
			number      int32
			title       string
			prefix      string
			actorAgent  string
			toAgentName string
		)
		if err := rows.Scan(
			&action, &details, &createdAt, &actorType, &actorID,
			&issueID, &number, &title, &prefix, &actorAgent, &toAgentName,
		); err != nil {
			return nil, err
		}
		identifier := fmt.Sprintf("%s-%d", prefix, number)
		if strings.TrimSpace(prefix) == "" {
			identifier = uuidToString(issueID)
		}
		actorIDStr := uuidToString(actorID)
		attr := classifyNoteRetrospectiveIssueAttribution(viewerID, actorType, actorIDStr, action, details)
		agentID, agentName := "", ""
		if actorType == "agent" {
			agentID = actorIDStr
			agentName = actorAgent
		} else if action == "assignee_changed" && jsonStringField(details, "to_type") == "agent" {
			agentID = jsonStringField(details, "to_id")
			agentName = toAgentName
		}
		out = append(out, noteRetrospectiveIssueFact{
			IssueID:      uuidToString(issueID),
			Identifier:   identifier,
			Title:        title,
			Action:       action,
			Detail:       formatIssueActivityDetail(action, details),
			At:           createdAt.UTC(),
			ActorType:    actorType,
			ActorID:      actorIDStr,
			AgentID:      agentID,
			AgentName:    agentName,
			Attribution:  attr,
			PullRequests: []noteRetrospectivePullRequestFact{},
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
			PageID:      uuidToString(id),
			Title:       title,
			At:          updatedAt.UTC(),
			Attribution: noteRetrospectiveAttrHandsOn,
		})
	}
	return out, rows.Err()
}

// loadNoteRetrospectiveRunFacts loads short Agent run summaries for retrospectives.
// Scope: runs I initiated, or runs on Agents I own (archived agents excluded).
// Text is trigger_summary only — never result/thinking payloads.
func (h *Handler) loadNoteRetrospectiveRunFacts(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
) ([]noteRetrospectiveRunFact, error) {
	rows, err := h.DB.Query(ctx, `
SELECT e.id, e.agent_id, COALESCE(a.name, ''), COALESCE(e.reason, ''), COALESCE(e.trigger_summary, ''),
       COALESCE(e.terminal_outcome, ''), e.status,
       e.issue_id, i.number, COALESCE(i.title, ''), COALESCE(w.issue_prefix, ''),
       COALESCE(e.completed_at, e.terminal_at, e.created_at) AS happened_at
FROM agent_inbox_event e
JOIN agent a ON a.id = e.agent_id AND a.workspace_id = e.workspace_id
JOIN workspace w ON w.id = e.workspace_id
LEFT JOIN issue i ON i.id = e.issue_id AND i.workspace_id = e.workspace_id
WHERE e.workspace_id = $1
  AND a.archived_at IS NULL
  AND (e.initiator_user_id = $2 OR a.owner_id = $2)
  AND COALESCE(e.completed_at, e.terminal_at, e.created_at) >= $3
  AND COALESCE(e.completed_at, e.terminal_at, e.created_at) < $4
  AND (
    e.terminal_outcome IS NOT NULL
    OR e.completed_at IS NOT NULL
    OR e.status IN ('completed', 'acked', 'failed', 'cancelled', 'suppressed')
  )
ORDER BY COALESCE(e.completed_at, e.terminal_at, e.created_at) ASC, e.id ASC
LIMIT 200`, workspaceID, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNoteRetrospectiveRunFacts(rows)
}

func scanNoteRetrospectiveRunFacts(rows pgx.Rows) ([]noteRetrospectiveRunFact, error) {
	out := make([]noteRetrospectiveRunFact, 0)
	for rows.Next() {
		var (
			runID, agentID                                     pgtype.UUID
			agentName, reason, triggerSummary, outcome, status string
			issueID                                            pgtype.UUID
			issueNumber                                        pgtype.Int4
			issueTitle, prefix                                 string
			happenedAt                                         time.Time
		)
		if err := rows.Scan(
			&runID, &agentID, &agentName, &reason, &triggerSummary, &outcome, &status,
			&issueID, &issueNumber, &issueTitle, &prefix, &happenedAt,
		); err != nil {
			return nil, err
		}
		fact := noteRetrospectiveRunFact{
			RunID:       uuidToString(runID),
			AgentID:     uuidToString(agentID),
			AgentName:   agentName,
			Reason:      reason,
			Summary:     formatNoteRetrospectiveRunSummary(triggerSummary, outcome, status),
			Outcome:     outcome,
			At:          happenedAt.UTC(),
			Attribution: noteRetrospectiveAttrDelegated,
		}
		if issueID.Valid {
			fact.IssueID = uuidToString(issueID)
			fact.IssueTitle = issueTitle
			if issueNumber.Valid {
				if strings.TrimSpace(prefix) == "" {
					fact.IssueIdentifier = uuidToString(issueID)
				} else {
					fact.IssueIdentifier = fmt.Sprintf("%s-%d", prefix, issueNumber.Int32)
				}
			}
		}
		out = append(out, fact)
	}
	return out, rows.Err()
}
