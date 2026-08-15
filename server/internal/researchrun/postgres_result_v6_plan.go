package researchrun

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// materializeAcceptedV6PlanTx persists the Inquiry graph and typed Task scope
// inside the same transaction that accepts the Planner Attempt. Nothing is
// visible if any endpoint, passport, dependency, or target binding fails.
func materializeAcceptedV6PlanTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, plan ResearchV6PlanResult, agentID string, questionIDs map[string]string) error {
	command, err := prepareResearchV6PlanMaterialization(state.run.SessionID, state.attemptID, agentID, state.run.StateVersion, plan)
	if err != nil {
		return err
	}
	command.InquiryGraph.WorkspaceID = state.workspaceID
	command.InquiryGraph.IdempotencyKey = "v6-plan-graph:" + state.attemptID
	for index := range command.InquiryGraph.Hypotheses {
		key := plan.Hypotheses[index].QuestionKey
		command.InquiryGraph.Hypotheses[index].QuestionID = questionIDs[key]
		if command.InquiryGraph.Hypotheses[index].QuestionID == "" {
			return fmt.Errorf("%w: unresolved persisted question %q", ErrInvalidResult, key)
		}
	}
	// Question endpoints use the UUIDs returned by the actual INSERT, never a
	// parallel identity map.
	for index := range command.InquiryGraph.Edges {
		edge := plan.InquiryEdges[index]
		if edge.From.Kind == "question" {
			command.InquiryGraph.Edges[index].From.ID = questionIDs[edge.From.Key]
		}
		if edge.To.Kind == "question" {
			command.InquiryGraph.Edges[index].To.ID = questionIDs[edge.To.Key]
		}
	}
	for index := range command.Targets {
		if command.Targets[index].Kind == InquiryKindQuestion {
			command.Targets[index].EntityID = questionIDs[command.Targets[index].EntityKey]
		}
	}
	if err = (inquiryModule{}).ValidateCreate(command.InquiryGraph); err != nil {
		return err
	}

	for _, item := range command.InquiryGraph.Hypotheses {
		createdAt := time.Now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO research_hypothesis
			(id,workspace_id,session_id,question_id,statement,applicability,expected_observations,weakening_conditions,
			 confidence_low,confidence_high,created_by_task_id,created_by_attempt_id,created_at,updated_at,client_key)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,task_id,id,$11,$11,$12
			FROM research_task_attempt WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND id=$13::uuid`,
			item.ID, state.workspaceID, state.run.SessionID, item.QuestionID, strings.TrimSpace(item.Statement),
			jsonOrDefault(item.Applicability, `{}`), jsonOrDefault(item.ExpectedObservations, `[]`), jsonOrDefault(item.WeakeningConditions, `[]`),
			item.ConfidenceLow, item.ConfidenceHigh, createdAt, item.ClientKey, state.attemptID); err != nil {
			return err
		}
		content := map[string]any{"question_id": item.QuestionID, "statement": strings.TrimSpace(item.Statement), "applicability": item.Applicability,
			"expected_observations": item.ExpectedObservations, "weakening_conditions": item.WeakeningConditions, "status": "proposed", "confidence_low": item.ConfidenceLow, "confidence_high": item.ConfidenceHigh}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindHypothesis, command.InquiryGraph, item.ID, createdAt, int32(state.run.GoalVersion), int32(state.targetPlan), state.outputAccess, content); err != nil {
			return err
		}
	}
	for _, item := range command.InquiryGraph.Branches {
		createdAt := time.Now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO research_branch
			(id,workspace_id,session_id,parent_branch_id,objective,entry_conditions,exit_conditions,budget_share,created_by_task_id,created_at,updated_at,client_key)
			SELECT $1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,task_id,$9,$9,$10 FROM research_task_attempt
			WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND id=$11::uuid`, item.ID, state.workspaceID, state.run.SessionID,
			item.ParentBranchID, strings.TrimSpace(item.Objective), jsonOrDefault(item.EntryConditions, `[]`), jsonOrDefault(item.ExitConditions, `[]`), item.BudgetShare,
			createdAt, item.ClientKey, state.attemptID); err != nil {
			return err
		}
		content := map[string]any{"parent_branch_id": item.ParentBranchID, "objective": strings.TrimSpace(item.Objective), "entry_conditions": item.EntryConditions, "exit_conditions": item.ExitConditions, "budget_share": item.BudgetShare, "status": "proposed"}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindBranch, command.InquiryGraph, item.ID, createdAt, int32(state.run.GoalVersion), int32(state.targetPlan), state.outputAccess, content); err != nil {
			return err
		}
	}
	for _, item := range command.InquiryGraph.Edges {
		createdAt := time.Now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_edge
			(id,workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation,rationale,created_by_attempt_id,created_at,client_key)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7::uuid,$8,$9,$10::uuid,$11,$12)`, item.ID, state.workspaceID, state.run.SessionID,
			string(item.From.Kind), item.From.ID, string(item.To.Kind), item.To.ID, string(item.Relation), strings.TrimSpace(item.Rationale), state.attemptID, createdAt, item.ClientKey); err != nil {
			return err
		}
		content := map[string]any{"from_kind": item.From.Kind, "from_entity_id": item.From.ID, "to_kind": item.To.Kind, "to_entity_id": item.To.ID, "relation": item.Relation, "rationale": strings.TrimSpace(item.Rationale)}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindInquiryEdge, command.InquiryGraph, item.ID, createdAt, int32(state.run.GoalVersion), int32(state.targetPlan), state.outputAccess, content); err != nil {
			return err
		}
	}
	taskIDs, err := loadTaskIDs(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
	if err != nil {
		return err
	}
	byTask := map[string][]TaskInquiryTarget{}
	for _, target := range command.Targets {
		taskID := taskIDs[target.TaskClientKey]
		if taskID == "" {
			return fmt.Errorf("%w: unresolved persisted task %q", ErrInvalidResult, target.TaskClientKey)
		}
		byTask[target.TaskClientKey] = append(byTask[target.TaskClientKey], TaskInquiryTarget{TaskID: taskID, Kind: target.Kind, EntityID: target.EntityID})
	}
	for _, task := range plan.Tasks {
		if err = bindTaskInquiryTargetsTx(ctx, tx, state.workspaceID, state.run.SessionID, taskIDs[task.ClientKey], byTask[task.ClientKey]); err != nil {
			return err
		}
	}
	_, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_plan_materialized", "v6-plan:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "questions": len(plan.Questions), "hypotheses": len(plan.Hypotheses), "branches": len(plan.Branches), "edges": len(plan.InquiryEdges), "tasks": len(plan.Tasks)})
	return err
}
