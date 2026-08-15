package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateInquiryGraph(ctx context.Context, in CreateInquiryGraphInput) (InquiryGraphCreateResult, error) {
	if err := (inquiryModule{}).ValidateCreate(in); err != nil {
		return InquiryGraphCreateResult{}, err
	}
	payload := inquiryGraphEventPayload(in)
	tx, err := s.beginResearchTx(ctx, txOpInquiryGraphCreate, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return InquiryGraphCreateResult{}, err
	}
	defer tx.Rollback(ctx)

	if event, found, err := loadMatchingInquiryEvent(ctx, tx, in, payload); err != nil {
		return InquiryGraphCreateResult{}, err
	} else if found {
		if err = s.commitResearchTx(ctx, txOpInquiryGraphCreate, tx); err != nil {
			return InquiryGraphCreateResult{}, err
		}
		return InquiryGraphCreateResult{Event: event}, nil
	}

	var stateVersion int64
	var goalVersion, planVersion int32
	var attemptAgent, attemptStatus string
	if err = tx.QueryRow(ctx, `
		SELECT session.state_version, task.goal_version, task.plan_version,
		       attempt.assigned_agent_id::text, attempt.status
		FROM research_session session
		JOIN research_task_attempt attempt
		  ON attempt.workspace_id=session.workspace_id AND attempt.session_id=session.id
		JOIN research_task task
		  ON task.workspace_id=attempt.workspace_id AND task.session_id=attempt.session_id AND task.id=attempt.task_id
		WHERE session.workspace_id=$1::uuid AND session.id=$2::uuid AND attempt.id=$3::uuid
		FOR UPDATE OF session, attempt, task
	`, in.WorkspaceID, in.SessionID, in.AttemptID).Scan(&stateVersion, &goalVersion, &planVersion, &attemptAgent, &attemptStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InquiryGraphCreateResult{}, ErrRunNotFound
		}
		return InquiryGraphCreateResult{}, err
	}
	if stateVersion != in.ExpectedStateVersion {
		return InquiryGraphCreateResult{}, fmt.Errorf("%w: inquiry graph expected state version %d, got %d", ErrInvalidTransition, in.ExpectedStateVersion, stateVersion)
	}
	if attemptAgent != in.AgentID || (attemptStatus != string(AttemptStatusRunning) && attemptStatus != string(AttemptStatusSucceeded)) {
		return InquiryGraphCreateResult{}, fmt.Errorf("%w: inquiry graph producer is not the assigned active attempt", ErrInvalidTransition)
	}
	access, err := deriveManifestOutputAccessTx(ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID)
	if err != nil {
		return InquiryGraphCreateResult{}, err
	}

	for _, item := range in.Hypotheses {
		createdAt := time.Now().UTC()
		applicability := jsonOrDefault(item.Applicability, `{}`)
		expected := jsonOrDefault(item.ExpectedObservations, `[]`)
		weakening := jsonOrDefault(item.WeakeningConditions, `[]`)
		if _, err = tx.Exec(ctx, `INSERT INTO research_hypothesis
			(id,workspace_id,session_id,question_id,statement,applicability,expected_observations,weakening_conditions,
			 confidence_low,confidence_high,created_by_task_id,created_by_attempt_id,created_at,updated_at,client_key)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,task_id,id,$11,$11,'legacy:'||$1::text
			FROM research_task_attempt WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND id=$12::uuid`,
			item.ID, in.WorkspaceID, in.SessionID, item.QuestionID, strings.TrimSpace(item.Statement), applicability, expected, weakening,
			item.ConfidenceLow, item.ConfidenceHigh, createdAt, in.AttemptID); err != nil {
			return InquiryGraphCreateResult{}, err
		}
		content := map[string]any{"question_id": item.QuestionID, "statement": strings.TrimSpace(item.Statement), "applicability": applicability,
			"expected_observations": expected, "weakening_conditions": weakening, "status": "proposed", "confidence_low": item.ConfidenceLow, "confidence_high": item.ConfidenceHigh}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindHypothesis, in, item.ID, createdAt, goalVersion, planVersion, access, content); err != nil {
			return InquiryGraphCreateResult{}, err
		}
	}
	if err = createInquiryBranchesTx(ctx, tx, in, goalVersion, planVersion, access); err != nil {
		return InquiryGraphCreateResult{}, err
	}
	for _, item := range in.Insights {
		createdAt := time.Now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO research_insight
			(id,workspace_id,session_id,title,summary,importance,level,created_by_attempt_id,created_at,updated_at,client_key)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::uuid,$9,$9,'legacy:'||$1::text)`, item.ID, in.WorkspaceID, in.SessionID,
			strings.TrimSpace(item.Title), strings.TrimSpace(item.Summary), item.Importance, item.Level, in.AttemptID, createdAt); err != nil {
			return InquiryGraphCreateResult{}, err
		}
		content := map[string]any{"title": strings.TrimSpace(item.Title), "summary": strings.TrimSpace(item.Summary), "status": "proposed", "importance": item.Importance, "level": item.Level}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindInsight, in, item.ID, createdAt, goalVersion, planVersion, access, content); err != nil {
			return InquiryGraphCreateResult{}, err
		}
	}
	for _, item := range in.Edges {
		createdAt := time.Now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_edge
			(id,workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation,rationale,created_by_attempt_id,created_at,client_key)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7::uuid,$8,$9,$10::uuid,$11,'legacy:'||$1::text)`, item.ID, in.WorkspaceID, in.SessionID,
			string(item.From.Kind), item.From.ID, string(item.To.Kind), item.To.ID, string(item.Relation), strings.TrimSpace(item.Rationale), in.AttemptID, createdAt); err != nil {
			return InquiryGraphCreateResult{}, err
		}
		content := map[string]any{"from_kind": item.From.Kind, "from_entity_id": item.From.ID, "to_kind": item.To.Kind, "to_entity_id": item.To.ID, "relation": item.Relation, "rationale": strings.TrimSpace(item.Rationale)}
		if err = registerInquiryArtifactTx(ctx, tx, ArtifactKindInquiryEdge, in, item.ID, createdAt, goalVersion, planVersion, access, content); err != nil {
			return InquiryGraphCreateResult{}, err
		}
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "inquiry_graph_created", in.IdempotencyKey, "agent", in.AgentID, payload)
	if err != nil {
		return InquiryGraphCreateResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpInquiryGraphCreate, tx); err != nil {
		return InquiryGraphCreateResult{}, err
	}
	return InquiryGraphCreateResult{Event: event}, nil
}

func inquiryGraphEventPayload(in CreateInquiryGraphInput) map[string]any {
	return map[string]any{"attempt_id": in.AttemptID, "hypotheses": in.Hypotheses, "branches": in.Branches, "insights": in.Insights, "edges": in.Edges}
}

func loadMatchingInquiryEvent(ctx context.Context, tx pgx.Tx, in CreateInquiryGraphInput, payload any) (RunEvent, bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return RunEvent{}, false, err
	}
	var event RunEvent
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,session_id::text,sequence,event_type,idempotency_key,actor_type,
		COALESCE(actor_id::text,''),payload,projection_attempts,created_at FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND idempotency_key=$3 FOR UPDATE`, in.WorkspaceID, in.SessionID, in.IdempotencyKey).Scan(
		&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence, &event.Type, &event.IdempotencyKey, &event.ActorType, &event.ActorID, &event.Payload, &event.ProjectionAttempts, &event.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, false, nil
	}
	if err != nil {
		return RunEvent{}, false, err
	}
	if event.Type != "inquiry_graph_created" || event.ActorType != "agent" || event.ActorID != in.AgentID || !semanticJSONEqual(event.Payload, encoded) {
		return RunEvent{}, false, fmt.Errorf("%w: inquiry graph idempotency key was reused", ErrResultConflict)
	}
	return event, true, nil
}

func createInquiryBranchesTx(ctx context.Context, tx pgx.Tx, in CreateInquiryGraphInput, goalVersion, planVersion int32, access ArtifactAccessLevel) error {
	pending := append([]InquiryBranchInput(nil), in.Branches...)
	sort.Slice(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
	created := make(map[string]bool, len(pending))
	for len(pending) > 0 {
		progress := false
		for index := 0; index < len(pending); {
			item := pending[index]
			if item.ParentBranchID != "" && !created[item.ParentBranchID] {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT research_inquiry_entity_exists($1::uuid,$2::uuid,'branch',$3::uuid)`, in.WorkspaceID, in.SessionID, item.ParentBranchID).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					index++
					continue
				}
			}
			createdAt := time.Now().UTC()
			entry := jsonOrDefault(item.EntryConditions, `[]`)
			exit := jsonOrDefault(item.ExitConditions, `[]`)
			if _, err := tx.Exec(ctx, `INSERT INTO research_branch
				(id,workspace_id,session_id,parent_branch_id,objective,entry_conditions,exit_conditions,budget_share,created_by_task_id,created_at,updated_at,client_key)
				SELECT $1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,task_id,$9,$9,'legacy:'||$1::text FROM research_task_attempt
				WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND id=$10::uuid`, item.ID, in.WorkspaceID, in.SessionID, item.ParentBranchID,
				strings.TrimSpace(item.Objective), entry, exit, item.BudgetShare, createdAt, in.AttemptID); err != nil {
				return err
			}
			content := map[string]any{"parent_branch_id": item.ParentBranchID, "objective": strings.TrimSpace(item.Objective), "entry_conditions": entry, "exit_conditions": exit, "budget_share": item.BudgetShare, "status": "proposed"}
			if err := registerInquiryArtifactTx(ctx, tx, ArtifactKindBranch, in, item.ID, createdAt, goalVersion, planVersion, access, content); err != nil {
				return err
			}
			created[item.ID] = true
			pending = append(pending[:index], pending[index+1:]...)
			progress = true
		}
		if !progress {
			return fmt.Errorf("%w: branch parents are missing or cyclic", ErrInvalidContract)
		}
	}
	return nil
}

func jsonOrDefault(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

func registerInquiryArtifactTx(ctx context.Context, tx pgx.Tx, kind ArtifactEntityKind, in CreateInquiryGraphInput, entityID string,
	createdAt time.Time, goalVersion, planVersion int32, access ArtifactAccessLevel, content map[string]any) error {
	contentHash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, EntityID: entityID, Kind: kind,
		SourceCreatedAt: &createdAt, ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion: &goalVersion, PlanVersion: &planVersion, SchemaName: string(kind), SchemaVersion: "research-run-v6",
		AccessLevel: access, HashOrigin: ArtifactHashOriginProduction, ContentHash: contentHash, ProducedByAttemptID: in.AttemptID,
	})
}
