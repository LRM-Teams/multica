package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	issueStageKeyImplicit        = "implicit"
	issueStageStatusOpen         = "open"
	issueStageStatusClosed       = "closed"
	issueStageBarrierEventClosed = "closed"
)

// issueStageBarrierDecision is the gate between "a child finished" and
// "wake the parent once".
type issueStageBarrierDecision struct {
	ShouldNotify bool
	StageID      pgtype.UUID
	StageKey     string
}

// evaluateIssueStageBarrier records the child against its stage (implicit by
// default) and returns whether the parent should be notified now. Only the
// first closer of a stage gets ShouldNotify=true.
func (h *Handler) evaluateIssueStageBarrier(ctx context.Context, parent, child db.Issue) (issueStageBarrierDecision, error) {
	if parent.Status == "backlog" {
		return issueStageBarrierDecision{}, nil
	}

	stageID, stageKey, err := h.ensureIssueStageForChild(ctx, parent, child)
	if err != nil {
		return issueStageBarrierDecision{}, err
	}

	allTerminal, err := h.issueStageChildrenAllTerminal(ctx, stageID)
	if err != nil {
		return issueStageBarrierDecision{}, err
	}
	if !allTerminal {
		return issueStageBarrierDecision{StageID: stageID, StageKey: stageKey}, nil
	}

	// Claim the single close event for this stage.
	tag, err := h.DB.Exec(ctx, `
		INSERT INTO issue_stage_barrier_event (
			workspace_id, parent_issue_id, stage_id, event_type, trigger_child_issue_id
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (stage_id, event_type) DO NOTHING`,
		parent.WorkspaceID, parent.ID, stageID, issueStageBarrierEventClosed, child.ID,
	)
	if err != nil {
		return issueStageBarrierDecision{}, err
	}
	if tag.RowsAffected() == 0 {
		// Another child already closed the barrier.
		return issueStageBarrierDecision{StageID: stageID, StageKey: stageKey}, nil
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE issue_stage
		SET status = $2, updated_at = now()
		WHERE id = $1 AND status = $3`, stageID, issueStageStatusClosed, issueStageStatusOpen); err != nil {
		return issueStageBarrierDecision{}, err
	}
	return issueStageBarrierDecision{
		ShouldNotify: true,
		StageID:      stageID,
		StageKey:     stageKey,
	}, nil
}

func (h *Handler) ensureIssueStageForChild(ctx context.Context, parent, child db.Issue) (pgtype.UUID, string, error) {
	var stageID pgtype.UUID
	var stageKey string
	err := h.DB.QueryRow(ctx, `
		SELECT s.id, s.stage_key
		FROM issue_stage_child c
		JOIN issue_stage s ON s.id = c.stage_id
		WHERE c.child_issue_id = $1`, child.ID).Scan(&stageID, &stageKey)
	if err == nil {
		return stageID, stageKey, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, "", err
	}

	// Implicit stage: one open bucket per parent for unstaged children.
	stageKey = issueStageKeyImplicit
	err = h.DB.QueryRow(ctx, `
		INSERT INTO issue_stage (workspace_id, parent_issue_id, stage_key, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (parent_issue_id, stage_key) DO UPDATE
		  SET updated_at = now()
		RETURNING id, stage_key`,
		parent.WorkspaceID, parent.ID, stageKey, issueStageStatusOpen,
	).Scan(&stageID, &stageKey)
	if err != nil {
		return pgtype.UUID{}, "", err
	}

	// Attach every current child of the parent that is not already staged into
	// this implicit stage. Explicit future stages will skip children that
	// already have issue_stage_child rows.
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO issue_stage_child (stage_id, child_issue_id)
		SELECT $1, i.id
		FROM issue i
		WHERE i.parent_issue_id = $2
		  AND NOT EXISTS (
		    SELECT 1 FROM issue_stage_child c WHERE c.child_issue_id = i.id
		  )
		ON CONFLICT DO NOTHING`, stageID, parent.ID); err != nil {
		return pgtype.UUID{}, "", err
	}

	// Ensure the finishing child is attached even if the parent scan raced.
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO issue_stage_child (stage_id, child_issue_id)
		VALUES ($1, $2)
		ON CONFLICT (child_issue_id) DO NOTHING`, stageID, child.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Child already belongs to another stage; reload.
			if err := h.DB.QueryRow(ctx, `
				SELECT s.id, s.stage_key
				FROM issue_stage_child c
				JOIN issue_stage s ON s.id = c.stage_id
				WHERE c.child_issue_id = $1`, child.ID).Scan(&stageID, &stageKey); err != nil {
				return pgtype.UUID{}, "", err
			}
			return stageID, stageKey, nil
		}
		return pgtype.UUID{}, "", err
	}
	return stageID, stageKey, nil
}

func (h *Handler) issueStageChildrenAllTerminal(ctx context.Context, stageID pgtype.UUID) (bool, error) {
	var total, terminal int
	err := h.DB.QueryRow(ctx, `
		SELECT
		  count(*)::int,
		  count(*) FILTER (WHERE i.status IN ('done', 'cancelled'))::int
		FROM issue_stage_child c
		JOIN issue i ON i.id = c.child_issue_id
		WHERE c.stage_id = $1`, stageID).Scan(&total, &terminal)
	if err != nil {
		return false, err
	}
	if total == 0 {
		return false, nil
	}
	return total == terminal, nil
}

// buildParentAssigneeDisplayMention returns a display-only @label (no
// mention:// URL) so system comments never feed the ordinary mention listener
// even if a future listener change stops short-circuiting on author_type=system.
func (h *Handler) buildParentAssigneeDisplayMention(ctx context.Context, parent db.Issue) string {
	if !parent.AssigneeType.Valid || !parent.AssigneeID.Valid {
		return ""
	}
	label, ok := h.resolveAssigneeMentionLabel(ctx, parent.WorkspaceID, parent.AssigneeType.String, parent.AssigneeID)
	if !ok {
		return ""
	}
	return formatParentAssigneeDisplayMention(label)
}

func formatParentAssigneeDisplayMention(label string) string {
	cleaned := sanitizeMentionLabel(label)
	if cleaned == "" {
		return ""
	}
	return fmt.Sprintf("@%s ", cleaned)
}

func logIssueStageBarrierSkip(parent, child db.Issue, reason string) {
	slog.Info("child done: stage barrier skipped parent notify",
		"reason", reason,
		"parent_id", uuidToString(parent.ID),
		"child_id", uuidToString(child.ID),
		"parent_status", parent.Status)
}
