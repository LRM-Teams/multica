package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) executeV6CreateAgentAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "agent.create.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		Name             string          `json:"name"`
		Capability       string          `json:"capability"`
		MissionPrompt    string          `json:"mission_prompt"`
		CapacityReason   string          `json:"capacity_reason"`
		ModelConfig      json.RawMessage `json:"model_config"`
		ToolConfig       json.RawMessage `json:"tool_config"`
		PermissionConfig json.RawMessage `json:"permission_config"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.MissionPrompt) == "" {
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
	var state int64
	var activeCount int
	if err = tx.QueryRow(ctx, `SELECT state_version,(SELECT count(*)::int FROM research_team_membership m WHERE m.session_id=s.id AND m.state IN ('idle','working','offline','retiring')) FROM research_session s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(&state, &activeCount); err != nil {
		return err
	}
	if state != expectedState || activeCount >= 50 || (activeCount >= 20 && strings.TrimSpace(payload.CapacityReason) == "") {
		return ErrWorkItemChanged
	}
	outboxPayload, err := json.Marshal(map[string]any{
		"spec":       V6AgentSpec{Name: payload.Name, Capability: payload.Capability, MissionPrompt: payload.MissionPrompt, ModelConfig: payload.ModelConfig, ToolConfig: payload.ToolConfig},
		"membership": AddV6TeamMemberInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID, DirectorCycleID: cycleID, MissionPrompt: payload.MissionPrompt, CapacityReason: payload.CapacityReason, ModelConfig: payload.ModelConfig, ToolConfig: payload.ToolConfig, PermissionConfig: payload.PermissionConfig},
	})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_v6_outbox(workspace_id,session_id,kind,idempotency_key,payload) VALUES($1::uuid,$2::uuid,'create_agent',$3,$4::jsonb) ON CONFLICT (workspace_id,session_id,idempotency_key) DO NOTHING`, proposal.WorkspaceID, proposal.RunID, action.IdempotencyKey, outboxPayload); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_agent_creation_requested", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"action_id": action.ActionID, "director_cycle_id": cycleID, "name": payload.Name}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6CreateWorkAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "work.create.v1" && action.PayloadSchema != "collaboration.create.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		Kind                   string          `json:"kind"`
		AssigneeAgentID        string          `json:"assignee_agent_id"`
		Mission                string          `json:"mission"`
		ExpectedResultSchemaID string          `json:"expected_result_schema_id"`
		PayloadSchemaID        string          `json:"payload_schema_id"`
		Payload                json.RawMessage `json:"payload"`
		Priority               float64         `json:"priority"`
		MaxAttempts            int             `json:"max_attempts"`
		BranchIDs              []string        `json:"branch_ids"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.Kind) == "" || strings.TrimSpace(payload.Mission) == "" || payload.Priority < 0 || payload.Priority > 1 || payload.MaxAttempts < 1 || payload.MaxAttempts > 100 {
		return ErrInvalidContract
	}
	expectedKind := V6ContractKind(payload.ExpectedResultSchemaID)
	persistedKind := ""
	switch expectedKind {
	case V6ContractAtomicResultSubmission:
		persistedKind = "research"
	case V6ContractDiscussionTurnSubmission:
		persistedKind = "discussion"
	case V6ContractIntegrationSubmission:
		persistedKind = "integration"
	case V6ContractReportPackageSubmission:
		persistedKind = "report"
	default:
		return ErrInvalidContract
	}
	workPayload, err := v6WorkPayloadWithMission(payload.Payload, payload.Mission, payload.Kind)
	if err != nil {
		return err
	}
	if expectedKind == V6ContractAtomicResultSubmission {
		var config struct {
			TaskSpecificSchema json.RawMessage `json:"task_specific_schema"`
		}
		if json.Unmarshal(workPayload, &config) != nil || len(config.TaskSpecificSchema) == 0 || string(config.TaskSpecificSchema) == "null" || strings.TrimSpace(payload.PayloadSchemaID) == "" || payload.PayloadSchemaID == "no_op.v1" {
			return fmt.Errorf("%w: atomic Work payload_schema_id must never be no_op.v1 and payload.task_specific_schema is required", ErrInvalidContract)
		}
	}
	if !validV6ActionUUID(payload.AssigneeAgentID) {
		return ErrInvalidContract
	}
	for _, branchID := range payload.BranchIDs {
		if !validV6ActionUUID(branchID) {
			return ErrInvalidContract
		}
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
	var state, sequence int64
	var member, assigneeIsDirector, assigneeHasActiveWork bool
	if err = tx.QueryRow(ctx, `SELECT goal_version,state_version,
		COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0),
		EXISTS(SELECT 1 FROM research_team_membership m WHERE m.session_id=s.id AND m.agent_id=$3::uuid AND m.state IN ('idle','working','offline','retiring')),
		EXISTS(SELECT 1 FROM research_director_assignment d WHERE d.id=s.current_director_assignment_id AND d.status='active' AND d.director_agent_id=$3::uuid),
		EXISTS(SELECT 1 FROM research_work_item active WHERE active.workspace_id=s.workspace_id AND active.session_id=s.id
			AND active.assigned_agent_id=$3::uuid AND active.status IN ('ready','dispatching','enqueued','running','awaiting_input'))
		FROM research_session s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, proposal.WorkspaceID, proposal.RunID, payload.AssigneeAgentID).Scan(&goalVersion, &state, &sequence, &member, &assigneeIsDirector, &assigneeHasActiveWork); err != nil {
		return err
	}
	if state != expectedState || !member {
		return ErrWorkItemChanged
	}
	if expectedKind == V6ContractAtomicResultSubmission && assigneeIsDirector {
		return fmt.Errorf("%w: Research Director cannot execute atomic research; create a run-scoped Agent first", ErrInvalidContract)
	}
	if expectedKind == V6ContractAtomicResultSubmission && assigneeHasActiveWork {
		return fmt.Errorf("%w: 该智能体已有活动中的 Work；独立调研方向必须分配给不同的 run-scoped Agent，或等待当前 Work 完成", ErrInvalidContract)
	}
	if len(payload.BranchIDs) > 0 {
		branchIDs := make([]string, 0, len(payload.BranchIDs))
		seen := make(map[string]struct{}, len(payload.BranchIDs))
		for _, branchID := range payload.BranchIDs {
			if _, exists := seen[branchID]; exists {
				continue
			}
			seen[branchID] = struct{}{}
			branchIDs = append(branchIDs, branchID)
		}
		var branchCount int
		if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=ANY($3::uuid[])`,
			proposal.WorkspaceID, proposal.RunID, branchIDs).Scan(&branchCount); err != nil {
			return err
		}
		if branchCount != len(branchIDs) {
			return fmt.Errorf("%w: Work branch_ids must reference branches in the current Run", ErrInvalidContract)
		}
	}
	workID := uuid.NewString()
	result, err := tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,created_by_director_cycle_id,assigned_agent_id,priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at,reason)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,'ready','',$5,$5,$6,$7,$8,$9::uuid,$10::uuid,$11,$12,$13,$14,$15::jsonb,1,now(),$16) ON CONFLICT (session_id,goal_version,idempotency_key) WHERE goal_version IS NOT NULL AND idempotency_key<>'' DO NOTHING`, workID, proposal.WorkspaceID, proposal.RunID, persistedKind, action.IdempotencyKey, goalVersion, state, sequence, cycleID, payload.AssigneeAgentID, payload.Priority, payload.MaxAttempts, payload.PayloadSchemaID, payload.ExpectedResultSchemaID, workPayload, payload.Mission)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	taskID := ""
	if expectedKind == V6ContractAtomicResultSubmission {
		taskID, err = ensureV6BackingTaskTx(ctx, tx, workID)
		if err != nil {
			return err
		}
	}
	for _, branchID := range payload.BranchIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO research_v6_work_item_branch(workspace_id,session_id,work_item_id,branch_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid) ON CONFLICT DO NOTHING`, proposal.WorkspaceID, proposal.RunID, workID, branchID); err != nil {
			return err
		}
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_work_item_created", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"action_id": action.ActionID, "work_item_id": workID, "task_id": taskID, "branch_ids": payload.BranchIDs}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func v6WorkPayloadWithMission(raw json.RawMessage, mission, taskKind string) (json.RawMessage, error) {
	value := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &value) != nil {
		return nil, ErrInvalidContract
	}
	value["mission_prompt"] = strings.TrimSpace(mission)
	value["task_kind"] = strings.TrimSpace(taskKind)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (s *PostgresStore) executeV6CreateBranchAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, expectedState int64) error {
	if action.PayloadSchema != "branch.create.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		Objective      string          `json:"objective"`
		ParentBranchID string          `json:"parent_branch_id"`
		Scope          json.RawMessage `json:"scope"`
		BudgetShare    float64         `json:"budget_share"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.Objective) == "" || payload.BudgetShare < 0 || payload.BudgetShare > 1 {
		return ErrInvalidContract
	}
	if strings.TrimSpace(payload.ParentBranchID) != "" && !validV6ActionUUID(payload.ParentBranchID) {
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
	var state int64
	if err = tx.QueryRow(ctx, `SELECT goal_version,state_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(&goalVersion, &state); err != nil {
		return err
	}
	if state != expectedState {
		return ErrWorkItemChanged
	}
	branchID := uuid.NewString()
	createdAt := time.Now().UTC()
	if _, err = tx.Exec(ctx, `INSERT INTO research_branch(id,workspace_id,session_id,client_key,parent_branch_id,objective,entry_conditions,exit_conditions,budget_share,status,goal_version,scope,state_version,created_by_director_cycle_id,created_at,updated_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,'director:'||$1::text,NULLIF($4,'')::uuid,$5,'[]'::jsonb,'[]'::jsonb,$6,'active',$7,$8::jsonb,1,$9::uuid,$10,$10)`, branchID, proposal.WorkspaceID, proposal.RunID, payload.ParentBranchID, payload.Objective, payload.BudgetShare, goalVersion, normalizedV6JSON(payload.Scope, `{}`), cycleID, createdAt); err != nil {
		return err
	}
	if err = registerV6BranchArtifactTx(ctx, tx, proposal.WorkspaceID, proposal.RunID, branchID, createdAt, int32(goalVersion), map[string]any{
		"parent_branch_id": payload.ParentBranchID, "objective": payload.Objective, "entry_conditions": json.RawMessage(`[]`),
		"exit_conditions": json.RawMessage(`[]`), "budget_share": payload.BudgetShare, "status": "active",
	}); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, proposal.WorkspaceID, proposal.RunID, "v6_branch_created", "v6-director-action:"+action.IdempotencyKey, "director", "", map[string]any{"action_id": action.ActionID, "branch_id": branchID}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6CreateReportAction(ctx context.Context, proposal v6DirectorProposal, cycleID string, action v6DirectorAction, goalVersion int, expectedState int64) error {
	if action.PayloadSchema != "report.create.v1" {
		return ErrInvalidContract
	}
	var payload struct {
		AssigneeAgentID string             `json:"assignee_agent_id"`
		Title           string             `json:"title"`
		Inputs          []V6ReportInputRef `json:"inputs"`
	}
	if json.Unmarshal(action.Payload, &payload) != nil || !validV6ActionUUID(payload.AssigneeAgentID) || strings.TrimSpace(payload.Title) == "" {
		return ErrInvalidContract
	}
	for _, input := range payload.Inputs {
		if strings.TrimSpace(input.BranchID) != "" && !validV6ActionUUID(input.BranchID) {
			return ErrInvalidContract
		}
		if strings.TrimSpace(input.NodeArtifactVersionID) != "" && !validV6ActionUUID(input.NodeArtifactVersionID) {
			return ErrInvalidContract
		}
	}
	var sequence int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(max(sequence),0) FROM research_run_event WHERE session_id=$1::uuid`, proposal.RunID).Scan(&sequence); err != nil {
		return err
	}
	_, err := s.CreateV6ReportWork(ctx, CreateV6ReportWorkInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID, DirectorCycleID: cycleID, AssigneeAgentID: payload.AssigneeAgentID, IdempotencyKey: action.IdempotencyKey, Title: payload.Title, Reason: action.Reason, ExpectedGoalVersion: goalVersion, ExpectedStateVersion: expectedState, InputEventSequence: sequence, Inputs: payload.Inputs})
	return err
}
