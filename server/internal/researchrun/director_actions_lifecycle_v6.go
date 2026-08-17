package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type v6TargetActionPayload struct {
	TargetID        string          `json:"target_id"`
	AssigneeAgentID string          `json:"assignee_agent_id"`
	Mission         string          `json:"mission"`
	Scope           json.RawMessage `json:"scope"`
	Reason          string          `json:"reason"`
}

func (s *PostgresStore) executeV6WorkLifecycleAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.TargetID) == "" {
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
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM research_work_item WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, proposal.WorkspaceID, proposal.RunID, payload.TargetID).Scan(&status); err != nil {
		return err
	}
	var command string
	var args []any
	switch action.Kind {
	case "cancel_work_item":
		if status == "succeeded" || status == "cancelled" {
			return ErrInvalidTransition
		}
		command = `UPDATE research_work_item SET status='cancelled',terminal_reason_code='stopped_by_director',terminal_reason_detail=$4,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`
		args = []any{proposal.WorkspaceID, proposal.RunID, payload.TargetID, action.Reason}
	case "retry_work_item":
		if status != "failed" && status != "cancelled" && status != "stale" {
			return ErrInvalidTransition
		}
		command = `UPDATE research_work_item SET status='ready',terminal_reason_code='',terminal_reason_detail='',lease_token=NULL,lease_expires_at=NULL,ready_at=now(),updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`
		args = []any{proposal.WorkspaceID, proposal.RunID, payload.TargetID}
	case "reassign_work_item":
		if strings.TrimSpace(payload.AssigneeAgentID) == "" || status == "succeeded" || status == "cancelled" {
			return ErrInvalidTransition
		}
		command = `UPDATE research_work_item w SET assigned_agent_id=$4::uuid,status='ready',lease_token=NULL,lease_expires_at=NULL,ready_at=now(),updated_at=now() WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.id=$3::uuid AND EXISTS(SELECT 1 FROM research_team_membership m WHERE m.session_id=w.session_id AND m.agent_id=$4::uuid AND m.state IN ('idle','working','offline','retiring'))`
		args = []any{proposal.WorkspaceID, proposal.RunID, payload.TargetID, payload.AssigneeAgentID}
	}
	result, err := tx.Exec(ctx, command, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_work_item_"+action.Kind, "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"work_item_id": payload.TargetID, "assignee_agent_id": payload.AssigneeAgentID, "reason": action.Reason}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6AgentLifecycleAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "agent.action.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		AgentID          string          `json:"agent_id"`
		MissionPrompt    string          `json:"mission_prompt"`
		ModelConfig      json.RawMessage `json:"model_config"`
		ToolConfig       json.RawMessage `json:"tool_config"`
		PermissionConfig json.RawMessage `json:"permission_config"`
		Reason           string          `json:"reason"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || payload.AgentID == "" {
		return ErrInvalidContract
	}
	var membershipID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid AND state IN ('idle','working','offline','retiring') ORDER BY membership_generation DESC LIMIT 1`, proposal.WorkspaceID, proposal.RunID, payload.AgentID).Scan(&membershipID); err != nil {
		return err
	}
	if action.Kind == "archive_agent" {
		_, err := s.ArchiveV6TeamMember(ctx, ArchiveV6TeamMemberInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID, MembershipID: membershipID, Reason: firstNonEmptyV6(payload.Reason, action.Reason)})
		return err
	}
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, proposal.RunID, proposal.WorkspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE research_team_membership SET mission_prompt=COALESCE(NULLIF($4,''),mission_prompt),model_config=CASE WHEN $5::jsonb='{}'::jsonb THEN model_config ELSE $5::jsonb END,tool_config=CASE WHEN $6::jsonb='{}'::jsonb THEN tool_config ELSE $6::jsonb END,permission_config=CASE WHEN $7::jsonb='{}'::jsonb THEN permission_config ELSE $7::jsonb END,mission_revision=mission_revision+1 WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, proposal.WorkspaceID, proposal.RunID, membershipID, payload.MissionPrompt, normalizedV6JSON(payload.ModelConfig, `{}`), normalizedV6JSON(payload.ToolConfig, `{}`), normalizedV6JSON(payload.PermissionConfig, `{}`))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_team_member_updated", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"membership_id": membershipID, "agent_id": payload.AgentID}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6BranchLifecycleAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || payload.TargetID == "" {
		return ErrInvalidContract
	}
	status := map[string]string{"update_branch": "active", "pause_branch": "paused", "terminate_branch": "terminated", "split_branch": "active", "merge_branch": "completed"}[action.Kind]
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, proposal.RunID, proposal.WorkspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE research_branch SET status=$4,objective=COALESCE(NULLIF($5,''),objective),scope=CASE WHEN $6::jsonb='{}'::jsonb THEN scope ELSE $6::jsonb END,state_version=state_version+1,reason_code=CASE WHEN $4='terminated' THEN 'stopped_by_director' ELSE reason_code END,reason_detail=CASE WHEN $4='terminated' THEN $7 ELSE reason_detail END,updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status NOT IN ('terminated','obsolete')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID, status, payload.Mission, normalizedV6JSON(payload.Scope, `{}`), action.Reason)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidTransition
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_branch_"+action.Kind, "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"branch_id": payload.TargetID, "reason": action.Reason}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6RunLifecycleAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "run.action.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil {
		return ErrInvalidContract
	}
	target := map[string]string{"pause_run": "paused", "resume_run": "running", "complete_run": "completed", "fail_run": "failed"}[action.Kind]
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, proposal.RunID, proposal.WorkspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE research_session SET status=$3,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$4 AND status NOT IN ('completed','archived','failed','cancelled')`, proposal.WorkspaceID, proposal.RunID, target, expectedState)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_"+action.Kind, "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"reason": firstNonEmptyV6(payload.Reason, action.Reason)}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func firstNonEmptyV6(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Director decision"
}

func withV6ActionKind(raw json.RawMessage, kind string) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	if strings.TrimSpace(fmt.Sprint(value["kind"])) == "" {
		value["kind"] = kind
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}

func (s *PostgresStore) recordV6DirectorNoOp(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64, reason string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, proposal.RunID, proposal.WorkspaceID); err != nil {
		return err
	}
	var state int64
	if err = tx.QueryRow(ctx, `SELECT state_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(&state); err != nil {
		return err
	}
	if state != expectedState {
		return ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_director_no_op", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"director_cycle_id": cycleID, "reason": firstNonEmptyV6(reason, action.Reason)}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}
