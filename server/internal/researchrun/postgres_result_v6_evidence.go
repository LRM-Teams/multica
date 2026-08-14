package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func materializeAcceptedV6EvidenceTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, result V6EvidenceResult, agentID string) error {
	plans, err := frozenV6SearchPlans(state.task.AcceptanceCriteria)
	if err != nil {
		return err
	}
	planIDs := map[string]string{}
	queryIDs := map[string]string{}
	candidateIDs := map[string]string{}

	for _, execution := range result.QueryExecutions {
		plan, ok := plans[execution.SearchPlanKey]
		if !ok {
			return fmt.Errorf("%w: query %q references a Search Plan outside its frozen Task contract", ErrInvalidResult, execution.ClientKey)
		}
		if plan.Adapter != execution.Adapter {
			return fmt.Errorf("%w: query %q changed its frozen Search Adapter", ErrInvalidResult, execution.ClientKey)
		}
		planID := planIDs[plan.ClientKey]
		if planID == "" {
			planID = uuid.NewString()
			if _, err = tx.Exec(ctx, `INSERT INTO research_search_plan
				(id,workspace_id,session_id,task_id,created_by_attempt_id,client_key,objective)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7)`, planID, state.workspaceID, state.run.SessionID,
				state.task.ID, state.attemptID, plan.ClientKey, plan.QueryStrategy); err != nil {
				return err
			}
			if err = registerAcceptedV6SearchArtifactTx(ctx, tx, state, planID, ArtifactKindSearchPlan, map[string]any{"task_id": state.task.ID, "attempt_id": state.attemptID, "client_key": plan.ClientKey, "objective": plan.QueryStrategy}); err != nil {
				return err
			}
			planIDs[plan.ClientKey] = planID
		}
		status, failureReason := "succeeded", ""
		if execution.Outcome == "failed" || execution.Outcome == "blocked" {
			status, failureReason = "failed", "V6 query outcome "+execution.Outcome+": "+execution.FailureClass
		}
		queryID := uuid.NewString()
		requestID := result.ClientRequestID + ":" + execution.ClientKey
		safetyFacts := execution.Safety
		if safetyFacts == nil {
			safetyFacts = map[string]any{}
		}
		executionContent := map[string]any{"client_key": execution.ClientKey, "search_plan_key": execution.SearchPlanKey, "adapter": execution.Adapter,
			"query": execution.Query, "cursor_in": execution.CursorIn, "cursor_out": execution.CursorOut, "started_at": execution.StartedAt,
			"finished_at": execution.FinishedAt, "outcome": execution.Outcome, "failure_class": execution.FailureClass, "cost": execution.Cost, "safety": safetyFacts}
		requestHash, hashErr := ArtifactContentHash(ArtifactKindQueryExecution, executionContent)
		if hashErr != nil {
			return hashErr
		}
		cost, _ := json.Marshal(execution.Cost)
		safety, _ := json.Marshal(safetyFacts)
		if _, err = tx.Exec(ctx, `INSERT INTO research_query_execution
			(id,workspace_id,session_id,search_plan_id,client_request_id,request_hash,adapter,query_text,cursor_in,cursor_out,status,failure_class,failure_reason,cost,safety,executed_at)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16)`, queryID,
			state.workspaceID, state.run.SessionID, planID, requestID, requestHash, execution.Adapter, execution.Query, execution.CursorIn,
			execution.CursorOut, status, execution.FailureClass, failureReason, cost, safety, execution.FinishedAt); err != nil {
			return err
		}
		if err = registerAcceptedV6SearchArtifactTx(ctx, tx, state, queryID, ArtifactKindQueryExecution, map[string]any{"search_plan_id": planID, "client_request_id": requestID, "request_hash": requestHash, "adapter": execution.Adapter, "query": execution.Query, "cursor_in": execution.CursorIn, "cursor_out": execution.CursorOut, "status": status, "failure_class": execution.FailureClass, "failure_reason": failureReason, "cost": execution.Cost, "safety": safetyFacts, "executed_at": execution.FinishedAt}); err != nil {
			return err
		}
		queryIDs[execution.ClientKey] = queryID
	}

	for position, candidate := range result.SourceCandidates {
		queryID := queryIDs[candidate.QueryExecutionKey]
		if queryID == "" {
			return fmt.Errorf("%w: unresolved query for candidate %q", ErrInvalidResult, candidate.ClientKey)
		}
		candidateID := uuid.NewString()
		metadata, _ := json.Marshal(map[string]any{"screening": candidate.Screening})
		if _, err = tx.Exec(ctx, `INSERT INTO research_source_candidate
			(id,workspace_id,session_id,query_execution_id,client_key,canonical_url,canonical_identity,title,snippet,publisher,independence_family,content_hash,result_position,metadata)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,'','',$9,$10,$11,$12::jsonb)`, candidateID, state.workspaceID,
			state.run.SessionID, queryID, candidate.ClientKey, candidate.URL, candidate.URL, candidate.Title, candidate.IndependenceFamily,
			candidate.ContentHash, position+1, metadata); err != nil {
			return err
		}
		if err = registerAcceptedV6SearchArtifactTx(ctx, tx, state, candidateID, ArtifactKindSourceCandidate, map[string]any{"query_execution_id": queryID, "client_key": candidate.ClientKey, "canonical_url": candidate.URL, "canonical_identity": candidate.URL, "title": candidate.Title, "snippet": "", "publisher": "", "independence_family": candidate.IndependenceFamily, "content_hash": candidate.ContentHash, "position": position + 1, "metadata": json.RawMessage(metadata)}); err != nil {
			return err
		}
		candidateIDs[candidate.ClientKey] = candidateID
	}
	for _, candidate := range result.SourceCandidates {
		disposition, reasonCode := screeningDisposition(candidate.Screening.Decision)
		canonicalID := ""
		if disposition == "duplicate" {
			for _, other := range result.SourceCandidates {
				if other.ClientKey != candidate.ClientKey && candidateIDs[other.ClientKey] != "" && other.Screening.Decision == "include" && (other.URL == candidate.URL || other.ContentHash == candidate.ContentHash) {
					canonicalID = candidateIDs[other.ClientKey]
					break
				}
			}
			if canonicalID == "" {
				return fmt.Errorf("%w: duplicate candidate %q has no server-verifiable canonical peer", ErrInvalidResult, candidate.ClientKey)
			}
		}
		decisionID := uuid.NewString()
		// The complete reasons remain in the immutable Result Artifact. The
		// indexed Screening ledger keeps its schema-bounded audit summary.
		reason := truncateBytes(strings.Join(candidate.Screening.Reasons, "; "), 4096)
		if _, err = tx.Exec(ctx, `INSERT INTO research_screening_decision
			(id,workspace_id,session_id,query_execution_id,source_candidate_id,decided_by_attempt_id,disposition,reason_code,reason,effective_independence_family,canonical_candidate_id,decided_at)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9,$10,NULLIF($11,'')::uuid,now())`, decisionID,
			state.workspaceID, state.run.SessionID, queryIDs[candidate.QueryExecutionKey], candidateIDs[candidate.ClientKey], state.attemptID,
			disposition, reasonCode, reason, candidate.IndependenceFamily, canonicalID); err != nil {
			return err
		}
		if err = registerAcceptedV6SearchArtifactTx(ctx, tx, state, decisionID, ArtifactKindScreeningDecision, map[string]any{"query_execution_id": queryIDs[candidate.QueryExecutionKey], "source_candidate_id": candidateIDs[candidate.ClientKey], "attempt_id": state.attemptID, "disposition": disposition, "reason_code": reasonCode, "reason": reason, "effective_independence_family": candidate.IndependenceFamily, "canonical_candidate_id": canonicalID}); err != nil {
			return err
		}
	}
	if err = applyAcceptedV6StatusUpdatesTx(ctx, tx, state, result.StatusUpdates, agentID); err != nil {
		return err
	}
	_, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_evidence_screened", "v6-evidence:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "queries": len(result.QueryExecutions), "candidates": len(result.SourceCandidates)})
	return err
}

func applyAcceptedV6StatusUpdatesTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, updates []V6StatusUpdate, agentID string) error {
	namespace, err := uuid.Parse(state.attemptID)
	if err != nil {
		return err
	}
	for index, update := range updates {
		targetID, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, update.Target)
		if resolveErr != nil {
			return resolveErr
		}
		input := UpdateInquiryStatusInput{
			WorkspaceID: state.workspaceID, SessionID: state.run.SessionID,
			TransitionID: uuid.NewSHA1(namespace, []byte(fmt.Sprintf("status/%d", index))).String(), AttemptID: state.attemptID,
			AgentID: agentID, IdempotencyKey: fmt.Sprintf("v6-inquiry-status:%s:%d", state.attemptID, index), ExpectedStateVersion: state.run.StateVersion,
			Target: InquiryEndpoint{Kind: InquiryEntityKind(update.Target.Kind), ID: targetID}, Before: update.Before, After: update.After, Reason: update.Reason,
		}
		for _, ref := range update.EvidenceRefs {
			id, refErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
			if refErr != nil {
				return refErr
			}
			input.EvidenceRefs = append(input.EvidenceRefs, InquiryStatusEvidenceRef{Kind: ref.Kind, ID: id})
		}
		if err = (inquiryStatusUpdateModule{}).Validate(input); err != nil {
			return err
		}
		current, goalVersion, planVersion, lockErr := lockInquiryStatusTarget(ctx, tx, input)
		if lockErr != nil {
			return lockErr
		}
		if current != input.Before || int(goalVersion) != state.run.GoalVersion || int(planVersion) != state.targetPlan {
			return fmt.Errorf("%w: V6 Inquiry status target changed", ErrControlTargetChanged)
		}
		for _, ref := range input.EvidenceRefs {
			if err = requireInquiryStatusEvidenceTx(ctx, tx, state.workspaceID, state.run.SessionID, ref); err != nil {
				return err
			}
		}
		payload := inquiryStatusEventPayload(input)
		event, eventErr := appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "inquiry_status_updated", input.IdempotencyKey, "agent", agentID, payload)
		if eventErr != nil {
			return eventErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_status_transition
			(id,workspace_id,session_id,target_kind,target_entity_id,before_status,after_status,reason,goal_version,plan_version,produced_by_attempt_id,event_sequence)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10,$11::uuid,$12)`, input.TransitionID, state.workspaceID,
			state.run.SessionID, string(input.Target.Kind), input.Target.ID, input.Before, input.After, strings.TrimSpace(input.Reason), goalVersion, planVersion, state.attemptID, event.Sequence); err != nil {
			return err
		}
		for ordinal, ref := range payload.EvidenceRefs {
			if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_status_evidence(workspace_id,session_id,transition_id,ordinal,evidence_kind,evidence_id)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid)`, state.workspaceID, state.run.SessionID, input.TransitionID, ordinal, ref.Kind, ref.ID); err != nil {
				return err
			}
		}
		if err = applyInquiryStatusTargetTx(ctx, tx, input, strings.TrimSpace(input.Reason), event.Sequence); err != nil {
			return err
		}
	}
	return nil
}

func resolveV6EntityKeyTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, ref V6EntityRef) (string, error) {
	if ref.Kind == "source" {
		if _, err := uuid.Parse(ref.Key); err != nil {
			return "", fmt.Errorf("%w: Source references require a canonical Source Snapshot UUID", ErrInvalidContract)
		}
		var id string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, state.workspaceID, state.run.SessionID, ref.Key).Scan(&id); err != nil {
			if err == pgx.ErrNoRows {
				return "", fmt.Errorf("%w: unresolved V6 Source Snapshot %s", ErrInvalidContract, ref.Key)
			}
			return "", err
		}
		return id, nil
	}
	tables := map[string]string{"question": "research_question", "hypothesis": "research_hypothesis", "branch": "research_branch", "claim": "research_claim", "insight": "research_insight", "dispute": "research_dispute", "task": "research_task"}
	table := tables[ref.Kind]
	if table == "" {
		return "", fmt.Errorf("%w: unsupported V6 entity kind %q", ErrInvalidContract, ref.Kind)
	}
	query := fmt.Sprintf(`SELECT id::text FROM %s WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND client_key=$3`, table)
	args := []any{state.workspaceID, state.run.SessionID, ref.Key}
	if ref.Kind == "question" || ref.Kind == "claim" || ref.Kind == "task" {
		query += ` AND goal_version=$4 AND plan_version=$5`
		args = append(args, state.run.GoalVersion, state.targetPlan)
	}
	var id string
	if err := tx.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("%w: unresolved V6 entity %s:%s", ErrInvalidContract, ref.Kind, ref.Key)
		}
		return "", err
	}
	return id, nil
}

func frozenV6SearchPlans(criteria json.RawMessage) (map[string]ResearchV6SearchPlan, error) {
	var contract struct {
		SearchPlans []ResearchV6SearchPlan `json:"search_plans"`
	}
	if err := json.Unmarshal(criteria, &contract); err != nil || len(contract.SearchPlans) == 0 {
		return nil, fmt.Errorf("%w: V6 evidence Task has no frozen Search Plan", ErrInvalidContract)
	}
	plans := map[string]ResearchV6SearchPlan{}
	for _, plan := range contract.SearchPlans {
		plans[plan.ClientKey] = plan
	}
	return plans, nil
}

func screeningDisposition(decision string) (string, string) {
	switch decision {
	case "include":
		return "accepted", "plan_include"
	case "duplicate":
		return "duplicate", "deduplicated"
	case "unsafe":
		return "excluded", "unsafe"
	default:
		return "excluded", "plan_exclude"
	}
}

func registerAcceptedV6SearchArtifactTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, entityID string, kind ArtifactEntityKind, content map[string]any) error {
	hash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	goal, plan := int32(state.run.GoalVersion), int32(state.targetPlan)
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: state.workspaceID, SessionID: state.run.SessionID,
		EntityID: entityID, Kind: kind, ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goal, PlanVersion: &plan,
		AccessLevel: state.outputAccess, HashOrigin: ArtifactHashOriginProduction, ContentHash: hash, ProducedByTaskID: state.task.ID,
		ProducedByAttemptID: state.attemptID, SchemaName: string(kind), SchemaVersion: OrchestratorVersionV6})
}
