package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) RecordSearchLineageBatch(ctx context.Context, in SearchLineageBatch) (SearchLineageBatchResult, error) {
	if err := validateSearchLineageBatch(in); err != nil {
		return SearchLineageBatchResult{}, err
	}
	requestHash, err := hashSearchLineageBatch(in)
	if err != nil {
		return SearchLineageBatchResult{}, err
	}
	tx, err := s.beginResearchTx(ctx, txOpSearchLineageRecord, pgx.TxOptions{})
	if err != nil {
		return SearchLineageBatchResult{}, err
	}
	defer tx.Rollback(ctx)

	var attemptStatus AttemptStatus
	if err = tx.QueryRow(ctx, `
		SELECT attempt.status
		FROM research_session session
		JOIN research_task task
		  ON (task.workspace_id, task.session_id) = (session.workspace_id, session.id)
		JOIN research_task_attempt attempt
		  ON (attempt.workspace_id, attempt.session_id, attempt.task_id) =
		     (task.workspace_id, task.session_id, task.id)
		WHERE session.workspace_id = $1::uuid
		  AND session.id = $2::uuid
		  AND task.id = $3::uuid
		  AND attempt.id = $4::uuid
		FOR UPDATE OF session, task, attempt
	`, in.WorkspaceID, in.SessionID, in.TaskID, in.AttemptID).Scan(&attemptStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SearchLineageBatchResult{}, ErrRunNotFound
		}
		return SearchLineageBatchResult{}, err
	}
	if _, err = tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, "research-search-lineage:"+in.WorkspaceID+":"+in.SessionID+":"+in.ClientRequestID); err != nil {
		return SearchLineageBatchResult{}, err
	}

	if replay, found, replayErr := loadSearchLineageReplayTx(ctx, tx, in.WorkspaceID, in.SessionID, in.ClientRequestID, requestHash); replayErr != nil {
		return SearchLineageBatchResult{}, replayErr
	} else if found {
		if err = s.commitResearchTx(ctx, txOpSearchLineageRecord, tx); err != nil {
			return SearchLineageBatchResult{}, err
		}
		return replay, nil
	}
	if attemptStatus != AttemptStatusRunning {
		return SearchLineageBatchResult{}, fmt.Errorf("%w: Search lineage requires a running Attempt", ErrInvalidTransition)
	}

	result := SearchLineageBatchResult{
		PlanID: uuid.NewString(), QueryExecutionID: uuid.NewString(),
		CandidateIDs: map[string]string{}, DecisionIDs: map[string]string{},
	}
	var persistedPlanID, persistedTaskID, persistedAttemptID, persistedObjective string
	err = tx.QueryRow(ctx, `
		WITH inserted AS (
		  INSERT INTO research_search_plan (
		    id, workspace_id, session_id, task_id, created_by_attempt_id,
		    client_key, objective
		  ) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7)
		  ON CONFLICT (workspace_id, session_id, task_id, client_key) DO NOTHING
		  RETURNING id, task_id, created_by_attempt_id, objective
		)
		SELECT id::text, task_id::text, created_by_attempt_id::text, objective FROM inserted
		UNION ALL
		SELECT id::text, task_id::text, created_by_attempt_id::text, objective
		FROM research_search_plan
		WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND task_id=$4::uuid AND client_key=$6
		LIMIT 1
	`, result.PlanID, in.WorkspaceID, in.SessionID, in.TaskID, in.AttemptID, in.PlanClientKey, in.PlanObjective).Scan(
		&persistedPlanID, &persistedTaskID, &persistedAttemptID, &persistedObjective,
	)
	if err != nil {
		return SearchLineageBatchResult{}, err
	}
	if persistedTaskID != in.TaskID || persistedAttemptID != in.AttemptID || persistedObjective != in.PlanObjective {
		return SearchLineageBatchResult{}, fmt.Errorf("%w: Search Plan client key was reused for different content", ErrResultConflict)
	}
	result.PlanID = persistedPlanID
	if err = registerSearchLineageArtifactTx(ctx, tx, in, result.PlanID, ArtifactKindSearchPlan, map[string]any{
		"task_id": in.TaskID, "attempt_id": in.AttemptID, "client_key": in.PlanClientKey,
		"objective": in.PlanObjective,
	}); err != nil {
		return SearchLineageBatchResult{}, err
	}
	cost := normalizedJSONObject(in.Cost)
	safety := normalizedJSONObject(in.Safety)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_query_execution (
		  id, workspace_id, session_id, search_plan_id, client_request_id, request_hash,
		  adapter, query_text, cursor_in, cursor_out, status, failure_class, failure_reason,
		  cost, safety, executed_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6,
		  $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16
		)
	`, result.QueryExecutionID, in.WorkspaceID, in.SessionID, result.PlanID, in.ClientRequestID, requestHash,
		in.Adapter, in.Query, in.CursorIn, in.CursorOut, in.Status, in.FailureClass, in.FailureReason,
		cost, safety, in.ExecutedAt); err != nil {
		return SearchLineageBatchResult{}, err
	}
	if err = registerSearchLineageArtifactTx(ctx, tx, in, result.QueryExecutionID, ArtifactKindQueryExecution, map[string]any{
		"search_plan_id": result.PlanID, "client_request_id": in.ClientRequestID, "request_hash": requestHash,
		"adapter": in.Adapter, "query": in.Query, "cursor_in": in.CursorIn, "cursor_out": in.CursorOut,
		"status": in.Status, "failure_class": in.FailureClass, "failure_reason": in.FailureReason,
		"cost": json.RawMessage(cost), "safety": json.RawMessage(safety), "executed_at": in.ExecutedAt,
	}); err != nil {
		return SearchLineageBatchResult{}, err
	}
	for _, candidate := range in.Candidates {
		candidateID := uuid.NewString()
		result.CandidateIDs[candidate.ClientKey] = candidateID
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_source_candidate (
			  id, workspace_id, session_id, query_execution_id, client_key,
			  canonical_url, canonical_identity, title, snippet, publisher,
			  independence_family, content_hash, result_position, metadata
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5,
			  $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb
			)
		`, candidateID, in.WorkspaceID, in.SessionID, result.QueryExecutionID, candidate.ClientKey,
			candidate.CanonicalURL, candidate.CanonicalIdentity, candidate.Title, candidate.Snippet,
			candidate.Publisher, candidate.IndependenceFamily, candidate.ContentHash, candidate.Position,
			normalizedJSONObject(candidate.Metadata)); err != nil {
			return SearchLineageBatchResult{}, err
		}
		if err = registerSearchLineageArtifactTx(ctx, tx, in, candidateID, ArtifactKindSourceCandidate, map[string]any{
			"query_execution_id": result.QueryExecutionID, "client_key": candidate.ClientKey,
			"canonical_url": candidate.CanonicalURL, "canonical_identity": candidate.CanonicalIdentity,
			"title": candidate.Title, "snippet": candidate.Snippet, "publisher": candidate.Publisher,
			"independence_family": candidate.IndependenceFamily, "content_hash": candidate.ContentHash,
			"position": candidate.Position, "metadata": json.RawMessage(normalizedJSONObject(candidate.Metadata)),
		}); err != nil {
			return SearchLineageBatchResult{}, err
		}
	}
	for _, candidate := range in.Candidates {
		decisionID := uuid.NewString()
		result.DecisionIDs[candidate.ClientKey] = decisionID
		canonicalCandidateID := result.CandidateIDs[candidate.CanonicalCandidateKey]
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_screening_decision (
			  id, workspace_id, session_id, query_execution_id, source_candidate_id,
			  decided_by_attempt_id, disposition, reason_code, reason,
			  effective_independence_family, canonical_candidate_id, decided_at
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
			  $6::uuid, $7, $8, $9, $10, NULLIF($11, '')::uuid, $12
			)
		`, decisionID, in.WorkspaceID, in.SessionID, result.QueryExecutionID,
			result.CandidateIDs[candidate.ClientKey], in.AttemptID, candidate.Disposition,
			candidate.ReasonCode, candidate.Reason, candidate.EffectiveIndependenceFamily,
			canonicalCandidateID, candidate.DecidedAt); err != nil {
			return SearchLineageBatchResult{}, err
		}
		if err = registerSearchLineageArtifactTx(ctx, tx, in, decisionID, ArtifactKindScreeningDecision, map[string]any{
			"query_execution_id": result.QueryExecutionID, "source_candidate_id": result.CandidateIDs[candidate.ClientKey],
			"attempt_id": in.AttemptID, "disposition": candidate.Disposition, "reason_code": candidate.ReasonCode,
			"reason": candidate.Reason, "effective_independence_family": candidate.EffectiveIndependenceFamily,
			"canonical_candidate_id": canonicalCandidateID, "decided_at": candidate.DecidedAt,
		}); err != nil {
			return SearchLineageBatchResult{}, err
		}
	}
	if err = s.commitResearchTx(ctx, txOpSearchLineageRecord, tx); err != nil {
		return SearchLineageBatchResult{}, err
	}
	return result, nil
}

func loadSearchLineageReplayTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, requestID, requestHash string) (SearchLineageBatchResult, bool, error) {
	var result SearchLineageBatchResult
	var storedHash string
	err := tx.QueryRow(ctx, `
		SELECT search_plan_id::text, id::text, request_hash
		FROM research_query_execution
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND client_request_id = $3
	`, workspaceID, sessionID, requestID).Scan(&result.PlanID, &result.QueryExecutionID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return SearchLineageBatchResult{}, false, nil
	}
	if err != nil {
		return SearchLineageBatchResult{}, false, err
	}
	if storedHash != requestHash {
		return SearchLineageBatchResult{}, false, fmt.Errorf("%w: Search execution request ID was reused for different content", ErrResultConflict)
	}
	result.CandidateIDs = map[string]string{}
	result.DecisionIDs = map[string]string{}
	rows, err := tx.Query(ctx, `
		SELECT candidate.client_key, candidate.id::text, decision.id::text
		FROM research_source_candidate candidate
		JOIN research_screening_decision decision
		  ON (decision.workspace_id, decision.session_id, decision.query_execution_id, decision.source_candidate_id) =
		     (candidate.workspace_id, candidate.session_id, candidate.query_execution_id, candidate.id)
		WHERE candidate.workspace_id = $1::uuid AND candidate.session_id = $2::uuid
		  AND candidate.query_execution_id = $3::uuid
		ORDER BY candidate.client_key
	`, workspaceID, sessionID, result.QueryExecutionID)
	if err != nil {
		return SearchLineageBatchResult{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, candidateID, decisionID string
		if err = rows.Scan(&key, &candidateID, &decisionID); err != nil {
			return SearchLineageBatchResult{}, false, err
		}
		result.CandidateIDs[key] = candidateID
		result.DecisionIDs[key] = decisionID
	}
	if err = rows.Err(); err != nil {
		return SearchLineageBatchResult{}, false, err
	}
	result.Replayed = true
	return result, true, nil
}

func normalizedJSONObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}

func (s *PostgresStore) SourceSearchLineage(ctx context.Context, workspaceID, sessionID, sourceSnapshotID string) (SourceSearchLineage, error) {
	for _, value := range []string{workspaceID, sessionID, sourceSnapshotID} {
		if _, err := uuid.Parse(value); err != nil {
			return SourceSearchLineage{}, fmt.Errorf("%w: Source lineage scope is invalid", ErrInvalidContract)
		}
	}
	var lineage SourceSearchLineage
	err := s.pool.QueryRow(ctx, `
		SELECT source.id::text, source.ingestion_kind,
		       COALESCE(decision.id::text, ''), COALESCE(candidate.id::text, ''),
		       COALESCE(execution.id::text, ''), COALESCE(plan.id::text, ''),
		       COALESCE(decision.disposition, ''), COALESCE(decision.reason_code, '')
		FROM research_source_snapshot source
		LEFT JOIN research_screening_decision decision
		  ON (decision.workspace_id, decision.session_id, decision.id) =
		     (source.workspace_id, source.session_id, source.screening_decision_id)
		LEFT JOIN research_source_candidate candidate
		  ON (candidate.workspace_id, candidate.session_id, candidate.query_execution_id, candidate.id) =
		     (decision.workspace_id, decision.session_id, decision.query_execution_id, decision.source_candidate_id)
		LEFT JOIN research_query_execution execution
		  ON (execution.workspace_id, execution.session_id, execution.id) =
		     (candidate.workspace_id, candidate.session_id, candidate.query_execution_id)
		LEFT JOIN research_search_plan plan
		  ON (plan.workspace_id, plan.session_id, plan.id) =
		     (execution.workspace_id, execution.session_id, execution.search_plan_id)
		WHERE source.workspace_id=$1::uuid AND source.session_id=$2::uuid AND source.id=$3::uuid
	`, workspaceID, sessionID, sourceSnapshotID).Scan(
		&lineage.SourceSnapshotID, &lineage.IngestionKind, &lineage.ScreeningDecisionID,
		&lineage.SourceCandidateID, &lineage.QueryExecutionID, &lineage.SearchPlanID,
		&lineage.Disposition, &lineage.ReasonCode,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceSearchLineage{}, ErrRunNotFound
	}
	if err != nil {
		return SourceSearchLineage{}, err
	}
	if lineage.IngestionKind == "screened_retrieval" && (lineage.ScreeningDecisionID == "" || lineage.SourceCandidateID == "" || lineage.QueryExecutionID == "" || lineage.SearchPlanID == "" || lineage.Disposition != "accepted") {
		return SourceSearchLineage{}, fmt.Errorf("%w: screened Source lineage is incomplete", ErrInvalidTransition)
	}
	return lineage, nil
}

func registerSearchLineageArtifactTx(ctx context.Context, tx pgx.Tx, in SearchLineageBatch, entityID string, kind ArtifactEntityKind, content map[string]any) error {
	contentHash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, EntityID: entityID, Kind: kind,
		ProvenanceCompleteness: ArtifactProvenanceComplete, AccessLevel: ArtifactAccessRaw,
		HashOrigin: ArtifactHashOriginProduction, ContentHash: contentHash, ProducedByAttemptID: in.AttemptID,
	})
}
