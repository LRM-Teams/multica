package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/util"
)

// maybeProposeNoteWritebacksOnIssueDone creates pending note writebacks for
// every note linked to the issue (S1-W3). Best-effort: failures are logged and
// never fail the issue update.
func (h *Handler) maybeProposeNoteWritebacksOnIssueDone(
	ctx context.Context,
	prev db.Issue,
	issue db.Issue,
	actorType, actorID, prefix string,
) {
	if prev.Status == "done" || issue.Status != "done" {
		return
	}
	pageIDs, err := h.listNotePageIDsForIssue(ctx, issue.ID)
	if err != nil {
		slog.Warn("note writeback on done: list linked notes failed",
			"issue_id", uuidToString(issue.ID), "error", err)
		return
	}
	if len(pageIDs) == 0 {
		return
	}

	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	evidence, err := h.buildIssueDoneWritebackEvidence(ctx, issue, identifier)
	if err != nil {
		slog.Warn("note writeback on done: build evidence failed",
			"issue_id", uuidToString(issue.ID), "error", err)
		return
	}
	var runID, agentID string
	for _, item := range evidence {
		switch item.Type {
		case "run":
			if runID == "" {
				runID = item.ID
			}
		case "agent":
			if agentID == "" {
				agentID = item.ID
			}
		}
	}
	content := buildIssueDoneWritebackContent(issue, identifier, runID, agentID)
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		slog.Warn("note writeback on done: encode evidence failed",
			"issue_id", uuidToString(issue.ID), "error", err)
		return
	}

	creatorType := actorType
	if creatorType != "agent" && creatorType != "member" {
		creatorType = "member"
	}
	creatorID, err := util.ParseUUID(actorID)
	if err != nil {
		creatorID = issue.CreatorID
		creatorType = issue.CreatorType
		if creatorType != "agent" && creatorType != "member" {
			creatorType = "member"
		}
	}

	for _, pageID := range pageIDs {
		if err := h.insertIssueDoneWritebackIfNeeded(ctx, issue, pageID, content, evidenceJSON, creatorType, creatorID); err != nil {
			slog.Warn("note writeback on done: insert failed",
				"issue_id", uuidToString(issue.ID),
				"page_id", uuidToString(pageID),
				"error", err)
		}
	}
}

func (h *Handler) listNotePageIDsForIssue(ctx context.Context, issueID pgtype.UUID) ([]pgtype.UUID, error) {
	rows, err := h.DB.Query(ctx, `
SELECT page_id
FROM note_page_issue_ref
WHERE issue_id = $1`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]pgtype.UUID, 0)
	for rows.Next() {
		var pageID pgtype.UUID
		if err := rows.Scan(&pageID); err != nil {
			return nil, err
		}
		out = append(out, pageID)
	}
	return out, rows.Err()
}

func buildIssueDoneWritebackContent(issue db.Issue, identifier string, runID, agentID string) string {
	title := strings.TrimSpace(issue.Title)
	if title == "" {
		title = "Untitled"
	}
	mention := fmt.Sprintf("[%s](mention://issue/%s)", identifier, uuidToString(issue.ID))
	var b strings.Builder
	b.WriteString("### Done: ")
	b.WriteString(mention)
	b.WriteString(" — ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("- Status moved to **done**.\n")
	if runID != "" {
		b.WriteString("- Run: ")
		b.WriteString(fmt.Sprintf("[run](mention://run/%s)", runID))
		b.WriteString("\n")
	}
	if agentID != "" {
		b.WriteString("- Agent: ")
		b.WriteString(fmt.Sprintf("[@agent](mention://agent/%s)", agentID))
		b.WriteString("\n")
	}
	if issue.Description.Valid {
		summary := strings.TrimSpace(issue.Description.String)
		if summary != "" {
			summary = collapseWhitespace(summary)
			if utf8.RuneCountInString(summary) > 400 {
				runes := []rune(summary)
				summary = string(runes[:400]) + "…"
			}
			b.WriteString("- Context: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (h *Handler) buildIssueDoneWritebackEvidence(ctx context.Context, issue db.Issue, identifier string) ([]noteWritebackEvidence, error) {
	label := identifier
	evidence := []noteWritebackEvidence{{
		Type:  "issue",
		ID:    uuidToString(issue.ID),
		Label: &label,
	}}

	var taskID, agentID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id, agent_id
FROM agent_inbox_event
WHERE issue_id = $1 AND status = 'completed'
ORDER BY completed_at DESC NULLS LAST, updated_at DESC
LIMIT 1`, issue.ID).Scan(&taskID, &agentID)
	if err == nil && taskID.Valid {
		runLabel := "run"
		evidence = append(evidence, noteWritebackEvidence{
			Type:  "run",
			ID:    uuidToString(taskID),
			Label: &runLabel,
		})
		if agentID.Valid {
			agentLabel := "agent"
			evidence = append(evidence, noteWritebackEvidence{
				Type:  "agent",
				ID:    uuidToString(agentID),
				Label: &agentLabel,
			})
		}
	} else if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	return evidence, nil
}

func (h *Handler) insertIssueDoneWritebackIfNeeded(
	ctx context.Context,
	issue db.Issue,
	pageID pgtype.UUID,
	content string,
	evidenceJSON []byte,
	creatorType string,
	creatorID pgtype.UUID,
) error {
	// Skip if a pending proposal for this page already cites the same issue.
	var exists bool
	issueEvidence := fmt.Sprintf(`[{"type":"issue","id":%q}]`, uuidToString(issue.ID))
	if err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM note_page_writeback
  WHERE page_id = $1
    AND status = 'pending'
    AND evidence @> $2::jsonb
)`, pageID, issueEvidence).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err := h.DB.Exec(ctx, `
INSERT INTO note_page_writeback (
  workspace_id, page_id, action, content, evidence,
  created_by_type, created_by_id
) VALUES ($1, $2, 'append', $3, $4::jsonb, $5, $6)`,
		issue.WorkspaceID, pageID, content, evidenceJSON, creatorType, creatorID)
	return err
}
