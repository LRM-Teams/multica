package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (h *Handler) loadResearchV6InquiryProjection(ctx context.Context, workspaceID, runID string) ([]researchV6ProjectionNode, []researchV6ProjectionEdge, error) {
	nodes := []researchV6ProjectionNode{}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, question, kind, status, required, priority, impact, uncertainty, novelty,
		       coverage, goal_version, plan_version, COALESCE(parent_question_id::text,''),
		       COALESCE(answer_claim_id::text,''), terminal_explanation, created_at, updated_at
		FROM research_question WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id
	`, workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, title, subtype, status, parentID, answerClaimID, explanation string
		var required bool
		var priority, impact, uncertainty, novelty, coverage float64
		var goalVersion, planVersion int
		var createdAt, updatedAt time.Time
		if err = rows.Scan(&id, &title, &subtype, &status, &required, &priority, &impact, &uncertainty, &novelty, &coverage, &goalVersion, &planVersion, &parentID, &answerClaimID, &explanation, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		detail := map[string]any{"required": required, "priority": priority, "impact": impact, "uncertainty": uncertainty, "novelty": novelty, "coverage": coverage, "parent_question_id": parentID, "answer_claim_id": answerClaimID, "terminal_explanation": explanation}
		nodes = append(nodes, canonicalResearchV6InquiryNode(runID, "question", id, subtype, title, status, impact, goalVersion, planVersion, createdAt, updatedAt, detail))
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	rows, err = h.DB.Query(ctx, `SELECT id::text, question_id::text, statement, applicability, expected_observations, weakening_conditions, status, confidence_low, confidence_high, last_evaluated_state_version, created_at, updated_at FROM research_hypothesis WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id`, workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, questionID, statement, status string
		var applicability, expected, weakening json.RawMessage
		var low, high *float64
		var evaluated *int64
		var createdAt, updatedAt time.Time
		if err = rows.Scan(&id, &questionID, &statement, &applicability, &expected, &weakening, &status, &low, &high, &evaluated, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		detail := map[string]any{"question_id": questionID, "applicability": jsonValue(applicability), "expected_observations": jsonValue(expected), "weakening_conditions": jsonValue(weakening), "confidence_low": low, "confidence_high": high, "last_evaluated_state_version": evaluated}
		nodes = append(nodes, canonicalResearchV6InquiryNode(runID, "hypothesis", id, "hypothesis", statement, status, .7, 0, 0, createdAt, updatedAt, detail))
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	rows, err = h.DB.Query(ctx, `SELECT id::text, COALESCE(parent_branch_id::text,''), objective, entry_conditions, exit_conditions, budget_share, status, termination_reason, created_at, updated_at FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id`, workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, parentID, objective, status, reason string
		var entry, exit json.RawMessage
		var budget float64
		var createdAt, updatedAt time.Time
		if err = rows.Scan(&id, &parentID, &objective, &entry, &exit, &budget, &status, &reason, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		detail := map[string]any{"parent_branch_id": parentID, "entry_conditions": jsonValue(entry), "exit_conditions": jsonValue(exit), "budget_share": budget, "termination_reason": reason}
		nodes = append(nodes, canonicalResearchV6InquiryNode(runID, "branch", id, "branch", objective, status, budget, 0, 0, createdAt, updatedAt, detail))
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	rows, err = h.DB.Query(ctx, `SELECT id::text,title,summary,status,importance,level,COALESCE(created_by_attempt_id::text,''),created_at,updated_at FROM research_insight WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id`, workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, title, summary, status, attemptID string
		var importance float64
		var level int
		var createdAt, updatedAt time.Time
		if err = rows.Scan(&id, &title, &summary, &status, &importance, &level, &attemptID, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		detail := map[string]any{"summary": summary, "level": level, "created_by_attempt_id": attemptID}
		nodes = append(nodes, canonicalResearchV6InquiryNode(runID, "insight", id, "insight", title, status, importance, 0, 0, createdAt, updatedAt, detail))
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	edges := []researchV6ProjectionEdge{}
	rows, err = h.DB.Query(ctx, `SELECT id::text,from_kind,from_entity_id::text,to_kind,to_entity_id::text,relation,rationale,created_at FROM research_inquiry_edge WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id`, workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, fromKind, fromID, toKind, toID, relation, rationale string
		var createdAt time.Time
		if err = rows.Scan(&id, &fromKind, &fromID, &toKind, &toID, &relation, &rationale, &createdAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		_ = rationale
		_ = createdAt
		edges = append(edges, researchV6ProjectionEdge{ID: runID + ":inquiry_edge:" + id, RunID: runID, FromNodeID: runID + ":" + fromKind + ":" + fromID, ToNodeID: runID + ":" + toKind + ":" + toID, EdgeType: relation})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	return nodes, edges, nil
}

func canonicalResearchV6InquiryNode(runID, kind, id, subtype, title, status string, importance float64, goalVersion, planVersion int, createdAt, updatedAt time.Time, detail map[string]any) researchV6ProjectionNode {
	created, updated := createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano)
	var goal, plan *string
	if goalVersion > 0 {
		value := fmt.Sprint(goalVersion)
		goal = &value
	}
	if planVersion > 0 {
		value := fmt.Sprint(planVersion)
		plan = &value
	}
	freshness := "fresh"
	return researchV6ProjectionNode{ID: runID + ":" + kind + ":" + id, RunID: runID, EntityKind: kind, EntityID: id, NodeKind: kind, NodeSubtype: subtype, SchemaVersion: 6, Title: title, Summary: title, Status: status, Importance: importance, Freshness: &freshness, ContractVersion: goal, PlanVersion: plan, CreatedAt: &created, UpdatedAt: &updated, Detail: detail}
}

func jsonValue(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
