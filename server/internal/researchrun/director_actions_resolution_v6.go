package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A Discussion that escalates must end in an explicit Director decision. This
// action is deliberately limited to terminal decisions; evidence collection is
// still represented by an ordinary research Work Item.
type v6ResolveDiscussionActionPayload struct {
	DiscussionID string `json:"discussion_id"`
	Decision     string `json:"decision"`
	Rationale    string `json:"rationale"`
}

func (s *PostgresStore) executeV6ResolveDiscussionAction(
	ctx context.Context,
	proposal v6DirectorProposal,
	cycleID string,
	action v6DirectorAction,
	expectedStateVersion int64,
) error {
	if action.PayloadSchema != "discussion.resolution.v1" {
		return fmt.Errorf("%w: adjudicate_discussion requires discussion.resolution.v1", ErrInvalidContract)
	}
	var payload v6ResolveDiscussionActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.DiscussionID) || strings.TrimSpace(payload.Rationale) == "" {
		return ErrInvalidContract
	}
	if payload.Decision != "keep_separate" && payload.Decision != "terminate" && payload.Decision != "accept_residual_uncertainty" {
		return ErrInvalidContract
	}

	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, proposal.RunID, proposal.WorkspaceID); err != nil {
		return err
	}
	var status, inputSetHash, branchScopeHash string
	var goalVersion int
	if err = tx.QueryRow(ctx, `SELECT status,input_set_hash,branch_scope_hash,goal_version
		FROM research_discussion
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		FOR UPDATE`, proposal.WorkspaceID, proposal.RunID, payload.DiscussionID).Scan(&status, &inputSetHash, &branchScopeHash, &goalVersion); err != nil {
		return err
	}
	if status != "escalated" {
		return ErrInvalidTransition
	}
	var liveStateVersion int64
	if err = tx.QueryRow(ctx, `SELECT state_version FROM research_session
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND status='running'`, proposal.WorkspaceID, proposal.RunID).Scan(&liveStateVersion); err != nil {
		return err
	}
	if liveStateVersion != expectedStateVersion {
		return ErrWorkItemChanged
	}

	decision, reasonCode := "rejected", "no_semantic_gain"
	decidedBy := "director_resolution"
	switch payload.Decision {
	case "terminate":
		reasonCode = "blocked_by_scope"
	case "accept_residual_uncertainty":
		decision, reasonCode = "deferred", "insufficient_evidence"
		decidedBy = "director_resolution_residual_uncertainty"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_match_decision(
		id,workspace_id,session_id,candidate_set_hash,input_artifact_version_ids,
		goal_version,branch_scope_hash,decision,reason_code,reason_detail,decided_by,director_cycle_id)
		SELECT $1::uuid,d.workspace_id,d.session_id,d.input_set_hash,
		array_agg(i.node_artifact_version_id ORDER BY i.ordinal),d.goal_version,
		d.branch_scope_hash,$4,$5,$6,$7,$8::uuid
		FROM research_discussion d
		JOIN research_discussion_input i ON (i.workspace_id,i.session_id,i.discussion_id)=(d.workspace_id,d.session_id,d.id)
		WHERE d.workspace_id=$2::uuid AND d.session_id=$3::uuid AND d.id=$9::uuid
		GROUP BY d.workspace_id,d.session_id,d.input_set_hash,d.goal_version,d.branch_scope_hash
		ON CONFLICT DO NOTHING`, uuid.NewString(), proposal.WorkspaceID, proposal.RunID, decision, reasonCode,
		payload.Rationale, decidedBy, cycleID, payload.DiscussionID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_discussion SET status='completed',stale_reason=$4,updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status='escalated'`,
		proposal.WorkspaceID, proposal.RunID, payload.DiscussionID, payload.Rationale); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_discussion_resolved",
		"v6-discussion-resolution:"+payload.DiscussionID+":"+action.IdempotencyKey, "director", "",
		map[string]any{"discussion_id": payload.DiscussionID, "decision": payload.Decision, "reason": payload.Rationale, "input_set_hash": inputSetHash, "branch_scope_hash": branchScopeHash, "goal_version": goalVersion}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}
