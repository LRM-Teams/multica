package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type v6TargetActionPayload struct {
	TargetID        string          `json:"target_id"`
	AssigneeAgentID string          `json:"assignee_agent_id"`
	Mission         string          `json:"mission"`
	Scope           json.RawMessage `json:"scope"`
	Reason          string          `json:"reason"`
}

func validV6ActionUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func (s *PostgresStore) executeV6WorkLifecycleAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.TargetID) {
		return ErrInvalidContract
	}
	if strings.TrimSpace(payload.AssigneeAgentID) != "" && !validV6ActionUUID(payload.AssigneeAgentID) {
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
		// A Director retry is an explicit grant of a fresh attempt budget.
		// Without the reset, a budget-exhausted item returns to 'ready' but is
		// skipped by dispatch preparation forever (attempt_count>=max_attempts),
		// leaving a silent zombie the Director believes it has retried.
		command = `UPDATE research_work_item SET status='ready',attempt_count=0,terminal_reason_code='',terminal_reason_detail='',lease_token=NULL,lease_expires_at=NULL,ready_at=now(),updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`
		args = []any{proposal.WorkspaceID, proposal.RunID, payload.TargetID}
	case "reassign_work_item":
		if strings.TrimSpace(payload.AssigneeAgentID) == "" || status == "succeeded" || status == "cancelled" {
			return ErrInvalidTransition
		}
		command = `UPDATE research_work_item w SET assigned_agent_id=$4::uuid,status='ready',attempt_count=0,lease_token=NULL,lease_expires_at=NULL,ready_at=now(),updated_at=now() WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.id=$3::uuid AND EXISTS(SELECT 1 FROM research_team_membership m WHERE m.session_id=w.session_id AND m.agent_id=$4::uuid AND m.state IN ('idle','working','offline','retiring'))`
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
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.AgentID) {
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
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.TargetID) {
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
	command, err := tx.Exec(ctx, `UPDATE research_branch SET status=$4,objective=COALESCE(NULLIF($5,''),objective),scope=CASE WHEN $6::jsonb='{}'::jsonb THEN scope ELSE $6::jsonb END,state_version=state_version+1,reason_code=CASE WHEN $4='terminated' THEN 'stopped_by_director' WHEN $4='completed' THEN 'merged_by_director' ELSE reason_code END,reason_detail=CASE WHEN $4 IN ('terminated','completed') THEN $7 ELSE reason_detail END,updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status NOT IN ('terminated','obsolete','completed')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID, status, payload.Mission, normalizedV6JSON(payload.Scope, `{}`), action.Reason)
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

func (s *PostgresStore) executeV6SplitBranchAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.TargetID) || strings.TrimSpace(payload.Mission) == "" {
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
	var goalVersion int
	if err = tx.QueryRow(ctx, `SELECT goal_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$3`, proposal.WorkspaceID, proposal.RunID, expectedState).Scan(&goalVersion); err != nil {
		return err
	}
	branchID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO research_branch(id,workspace_id,session_id,parent_branch_id,objective,entry_conditions,exit_conditions,budget_share,status,goal_version,scope,state_version,created_by_director_cycle_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'[]'::jsonb,'[]'::jsonb,0.0,'active',$6,$7::jsonb,1,$8::uuid)`, branchID, proposal.WorkspaceID, proposal.RunID, payload.TargetID, payload.Mission, goalVersion, normalizedV6JSON(payload.Scope, `{}`), cycleID); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_branch_split", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"parent_branch_id": payload.TargetID, "branch_id": branchID, "objective": payload.Mission}); err != nil {
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
	target, allowed := v6DirectorRunLifecycleTarget(action.Kind)
	if !allowed {
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

func v6DirectorRunLifecycleTarget(kind string) (string, bool) {
	target, allowed := map[string]string{"resume_run": "running", "complete_run": "completed", "fail_run": "failed"}[kind]
	return target, allowed
}

func firstNonEmptyV6(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "调研主理人决定"
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
	var runStatus string
	var hasWorker, agentCreationPending, hasIdleWorker bool
	var hasWorkerWork, hasActiveWorkerWork, hasFailedWorkerWork, hasRejectedAssignment bool
	if err = tx.QueryRow(ctx, `
		SELECT s.state_version,
		       s.status,
		       EXISTS(
		         SELECT 1 FROM research_team_membership m
		         JOIN research_director_assignment d ON d.id=s.current_director_assignment_id AND d.status='active'
		         WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id
		           AND m.agent_id<>d.director_agent_id
		           AND m.state IN ('idle','working','offline','retiring')
		       ),
		       EXISTS(
		         SELECT 1 FROM research_v6_outbox o
		         WHERE o.workspace_id=s.workspace_id AND o.session_id=s.id
		           AND o.kind='create_agent' AND o.status IN ('pending','delivering')
		       ),
		       EXISTS(
		         SELECT 1 FROM research_team_membership m
		         JOIN research_director_assignment d ON d.id=s.current_director_assignment_id AND d.status='active'
		         WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id
		           AND m.agent_id<>d.director_agent_id AND m.state='idle'
		       ),
		       EXISTS(
		         SELECT 1 FROM research_work_item w
		         JOIN research_director_assignment d ON d.id=s.current_director_assignment_id AND d.status='active'
		         WHERE w.workspace_id=s.workspace_id AND w.session_id=s.id
		           AND w.kind<>'director' AND w.assigned_agent_id<>d.director_agent_id
		       ),
		       EXISTS(
		         SELECT 1 FROM research_work_item w
		         JOIN research_director_assignment d ON d.id=s.current_director_assignment_id AND d.status='active'
		         WHERE w.workspace_id=s.workspace_id AND w.session_id=s.id
		           AND w.kind<>'director' AND w.assigned_agent_id<>d.director_agent_id
		           AND w.status IN ('ready','dispatching','enqueued','running','awaiting_input')
		       ),
		       EXISTS(
		         SELECT 1 FROM research_work_item w
		         JOIN research_director_assignment d ON d.id=s.current_director_assignment_id AND d.status='active'
		         WHERE w.workspace_id=s.workspace_id AND w.session_id=s.id
		           AND w.kind='research' AND w.assigned_agent_id<>d.director_agent_id
		           AND w.status='failed'
		       ),
		       EXISTS(
		         SELECT 1 FROM research_work_item w
		         WHERE w.workspace_id=s.workspace_id AND w.session_id=s.id
		           AND w.kind='director' AND w.status='failed'
		           AND w.terminal_reason_code='contract_rejected'
		       )
		FROM research_session s
		WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(
		&state, &runStatus, &hasWorker, &agentCreationPending, &hasIdleWorker,
		&hasWorkerWork, &hasActiveWorkerWork, &hasFailedWorkerWork, &hasRejectedAssignment,
	); err != nil {
		return err
	}
	if state != expectedState {
		return ErrWorkItemChanged
	}
	if runStatus == "running" && !hasWorker && !agentCreationPending {
		return fmt.Errorf("%w: running Research Run has no non-Director member; create a run-scoped Agent before waiting", ErrInvalidContract)
	}
	if runStatus == "running" && hasIdleWorker && !hasWorkerWork && hasRejectedAssignment {
		return fmt.Errorf("%w: the previous Work assignment was rejected; correct its schema and assign Work to an idle run-scoped Agent instead of returning no_op", ErrInvalidContract)
	}
	if runStatus == "running" && hasIdleWorker && !hasActiveWorkerWork && hasFailedWorkerWork {
		return fmt.Errorf("%w: run-scoped Agent Work failed and no Agent Work is active; retry or reassign failed Work instead of returning no_op", ErrInvalidContract)
	}
	if runStatus == "running" && !hasActiveWorkerWork && !agentCreationPending {
		return fmt.Errorf("%w: running Research Run has no active Agent Work; dispatch research, maintain the report, or complete the Run instead of returning no_op", ErrInvalidContract)
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_director_no_op", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"director_cycle_id": cycleID, "reason": firstNonEmptyV6(reason, action.Reason)}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6ReportReviewAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "report.review.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		ReportID         string `json:"report_id"`
		ExpectedRevision int    `json:"expected_revision"`
		Reason           string `json:"reason"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.ReportID) || payload.ExpectedRevision < 1 {
		return ErrInvalidContract
	}
	decision := map[string]string{"publish_report": "published", "reject_report": "technical_failure", "revise_report": "needs_revision"}[action.Kind]
	_, err := s.ReviewV6Report(ctx, ReviewV6ReportInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID, ReportID: payload.ReportID, DirectorAssignmentID: proposal.DirectorAssignmentID, DirectorCycleID: cycleID, Decision: decision, Reason: firstNonEmptyV6(payload.Reason, action.Reason), ExpectedRevision: payload.ExpectedRevision, ExpectedStateVersion: expectedState})
	return err
}

func (s *PostgresStore) executeV6NodeDecisionAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.TargetID) {
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
	var changed bool
	if action.Kind == "challenge_node" {
		result, resultErr := tx.Exec(ctx, `UPDATE research_result_node SET conclusion_state='challenged',reason_code='other',reason_detail=$4 WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_version_id=$3::uuid AND conclusion_state NOT IN ('invalid','refuted')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID, firstNonEmptyV6(payload.Reason, action.Reason))
		if resultErr != nil {
			return resultErr
		}
		changed = result.RowsAffected() == 1
		if !changed {
			insight, insightErr := tx.Exec(ctx, `UPDATE research_insight_version SET status='challenged' WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status NOT IN ('invalid','refuted','superseded','terminal')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID)
			if insightErr != nil {
				return insightErr
			}
			changed = insight.RowsAffected() == 1
		}
	} else {
		result, resultErr := tx.Exec(ctx, `UPDATE research_result_node SET conclusion_state='invalid',reason_code='stopped_by_director',reason_detail=$4 WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_version_id=$3::uuid AND conclusion_state NOT IN ('invalid','refuted')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID, firstNonEmptyV6(payload.Reason, action.Reason))
		if resultErr != nil {
			return resultErr
		}
		changed = result.RowsAffected() == 1
		if !changed {
			insight, insightErr := tx.Exec(ctx, `UPDATE research_insight_version SET status='terminal' WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status NOT IN ('invalid','refuted','superseded','terminal')`, proposal.WorkspaceID, proposal.RunID, payload.TargetID)
			if insightErr != nil {
				return insightErr
			}
			changed = insight.RowsAffected() == 1
		}
	}
	if !changed {
		return ErrInvalidTransition
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_node_"+action.Kind, "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"target_id": payload.TargetID, "reason": firstNonEmptyV6(payload.Reason, action.Reason)}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6AssignStewardAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "target.action.v1" {
		return ErrInvalidContract
	}
	var payload v6TargetActionPayload
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.TargetID) || !validV6ActionUUID(payload.AssigneeAgentID) {
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
	var membershipID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid AND state IN ('idle','working','offline','retiring') ORDER BY membership_generation DESC LIMIT 1`, proposal.WorkspaceID, proposal.RunID, payload.AssigneeAgentID).Scan(&membershipID); err != nil {
		return err
	}
	var generation int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(generation),0)+1 FROM research_node_steward_assignment WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND node_artifact_version_id=$3::uuid`, proposal.WorkspaceID, proposal.RunID, payload.TargetID).Scan(&generation); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_node_steward_assignment SET status='released',released_at=now(),reason='reassigned by Director' WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND node_artifact_version_id=$3::uuid AND status='active'`, proposal.WorkspaceID, proposal.RunID, payload.TargetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_node_steward_assignment(workspace_id,session_id,node_artifact_version_id,agent_id,membership_id,generation,status,reason,assigned_by_decision_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'active',$7,NULL)`, proposal.WorkspaceID, proposal.RunID, payload.TargetID, payload.AssigneeAgentID, membershipID, generation, firstNonEmptyV6(action.Reason, "assigned by Director")); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_node_steward_assigned", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"node_artifact_version_id": payload.TargetID, "agent_id": payload.AssigneeAgentID}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6ReviseGoalAction(ctx context.Context, proposal v6DirectorProposal, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "goal.revise.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		Goal         string          `json:"goal"`
		Scope        json.RawMessage `json:"scope"`
		Audience     string          `json:"audience"`
		Freshness    string          `json:"freshness"`
		Language     string          `json:"language"`
		SourcePolicy json.RawMessage `json:"source_policy"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.Goal) == "" {
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
	var goalVersion int
	if err = tx.QueryRow(ctx, `SELECT goal_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$3`, proposal.WorkspaceID, proposal.RunID, expectedState).Scan(&goalVersion); err != nil {
		return err
	}
	goalVersion++
	if _, err = tx.Exec(ctx, `UPDATE research_session SET goal=$3,goal_version=$4,state_version=state_version+1,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid`, proposal.WorkspaceID, proposal.RunID, strings.TrimSpace(payload.Goal), goalVersion); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_contract_revision(workspace_id,session_id,goal_version,goal,language,source_policy,run_limits,authored_by,reason) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::jsonb,'{}'::jsonb,NULL,'director_revision')`, proposal.WorkspaceID, proposal.RunID, goalVersion, strings.TrimSpace(payload.Goal), payload.Language, normalizedV6JSON(payload.SourcePolicy, `{}`)); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_goal_revised", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"goal_version": goalVersion, "goal": payload.Goal}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}
