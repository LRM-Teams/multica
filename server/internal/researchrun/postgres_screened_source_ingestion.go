package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type FetchScreenedSourceInput struct {
	WorkspaceID        string
	SessionID          string
	CandidateID        string
	MaximumContentSize int64
}

type FetchScreenedSourceResult struct {
	SourceSnapshotID string
	Replayed         bool
	Event            RunEvent
}

type screenedCandidateFetchState struct {
	PlanID, QueryID, CandidateID, DecisionID string
	TaskID, AttemptID, AgentID               string
	Adapter, URL, Identity, Title            string
	IndependenceFamily, CandidateHash        string
	DecisionHash                             string
}

func (s *PostgresStore) ListPendingScreenedSourceIngestions(ctx context.Context, limit int) ([]FetchScreenedSourceInput, error) {
	if s == nil || s.pool == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT candidate.workspace_id::text, candidate.session_id::text, candidate.id::text
		FROM research_screening_decision decision
		JOIN research_source_candidate candidate
		  ON (candidate.workspace_id, candidate.session_id, candidate.id) =
		     (decision.workspace_id, decision.session_id, decision.source_candidate_id)
		LEFT JOIN research_source_snapshot snapshot
		  ON (snapshot.workspace_id, snapshot.session_id, snapshot.screening_decision_id) =
		     (decision.workspace_id, decision.session_id, decision.id)
		WHERE decision.disposition='accepted' AND snapshot.id IS NULL
		ORDER BY decision.decided_at, decision.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := make([]FetchScreenedSourceInput, 0, limit)
	for rows.Next() {
		var item FetchScreenedSourceInput
		if err = rows.Scan(&item.WorkspaceID, &item.SessionID, &item.CandidateID); err != nil {
			return nil, err
		}
		item.MaximumContentSize = defaultScreenedSourceFetchBytes
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

// FetchAndIngestScreenedSource performs provider I/O before opening the write
// transaction, then re-locks and revalidates the append-only lineage before it
// creates a canonical Source Snapshot. An accepted URL is never evidence until
// this method succeeds.
func (s *PostgresStore) FetchAndIngestScreenedSource(ctx context.Context, in FetchScreenedSourceInput, adapter RetrievalAdapter) (FetchScreenedSourceResult, error) {
	if adapter == nil {
		return FetchScreenedSourceResult{}, fmt.Errorf("%w: Retrieval Adapter is required", ErrInvalidContract)
	}
	state, err := s.loadScreenedCandidateFetchState(ctx, in)
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	request := RetrievalFetchRequest{Adapter: state.Adapter, CanonicalURL: state.URL, CanonicalIdentity: state.Identity, MaximumContentSize: in.MaximumContentSize}
	if err = ValidateRetrievalFetchRequest(request); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	document, err := adapter.Fetch(ctx, request)
	if err != nil {
		var failure RetrievalFailure
		if !errors.As(err, &failure) || ValidateRetrievalFailure(failure) != nil {
			return FetchScreenedSourceResult{}, fmt.Errorf("%w: Retrieval Adapter returned an unclassified failure: %v", ErrInvalidContract, err)
		}
		return FetchScreenedSourceResult{}, failure
	}
	if err = ValidateRetrievalDocument(request, document); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	if state.CandidateHash != "" && state.CandidateHash != document.ContentHash {
		return FetchScreenedSourceResult{}, fmt.Errorf("%w: fetched document changed from the screened candidate hash", ErrResultConflict)
	}
	return s.persistFetchedScreenedSource(ctx, in, state, document)
}

func (s *PostgresStore) loadScreenedCandidateFetchState(ctx context.Context, in FetchScreenedSourceInput) (screenedCandidateFetchState, error) {
	for _, value := range []string{in.WorkspaceID, in.SessionID, in.CandidateID} {
		if _, err := uuid.Parse(value); err != nil {
			return screenedCandidateFetchState{}, fmt.Errorf("%w: screened Source scope is invalid", ErrInvalidContract)
		}
	}
	var state screenedCandidateFetchState
	err := s.pool.QueryRow(ctx, `SELECT plan.id::text,execution.id::text,candidate.id::text,decision.id::text,
		plan.task_id::text,plan.created_by_attempt_id::text,attempt.assigned_agent_id::text,
		execution.adapter,candidate.canonical_url,candidate.canonical_identity,candidate.title,
		decision.effective_independence_family,candidate.content_hash,decision_version.content_hash
		FROM research_source_candidate candidate
		JOIN research_query_execution execution ON (execution.workspace_id,execution.session_id,execution.id)=(candidate.workspace_id,candidate.session_id,candidate.query_execution_id)
		JOIN research_search_plan plan ON (plan.workspace_id,plan.session_id,plan.id)=(execution.workspace_id,execution.session_id,execution.search_plan_id)
		JOIN research_screening_decision decision ON (decision.workspace_id,decision.session_id,decision.query_execution_id,decision.source_candidate_id)=(candidate.workspace_id,candidate.session_id,candidate.query_execution_id,candidate.id)
		JOIN research_task_attempt attempt ON (attempt.workspace_id,attempt.session_id,attempt.id,attempt.task_id)=(plan.workspace_id,plan.session_id,plan.created_by_attempt_id,plan.task_id)
		JOIN research_artifact_passport passport ON (passport.workspace_id,passport.session_id,passport.id)=(decision.workspace_id,decision.session_id,decision.id)
		JOIN research_artifact_version decision_version ON
		  (decision_version.workspace_id,decision_version.session_id,decision_version.artifact_id,decision_version.version)=
		  (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE candidate.workspace_id=$1::uuid AND candidate.session_id=$2::uuid AND candidate.id=$3::uuid AND decision.disposition='accepted'`,
		in.WorkspaceID, in.SessionID, in.CandidateID).Scan(&state.PlanID, &state.QueryID, &state.CandidateID, &state.DecisionID,
		&state.TaskID, &state.AttemptID, &state.AgentID, &state.Adapter, &state.URL, &state.Identity, &state.Title,
		&state.IndependenceFamily, &state.CandidateHash, &state.DecisionHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return screenedCandidateFetchState{}, fmt.Errorf("%w: Source Candidate has no accepted Screening Decision", ErrInvalidContract)
	}
	return state, err
}

func (s *PostgresStore) persistFetchedScreenedSource(ctx context.Context, in FetchScreenedSourceInput, expected screenedCandidateFetchState, document RetrievalDocument) (FetchScreenedSourceResult, error) {
	tx, err := s.beginResearchTx(ctx, txOpSearchLineageRecord, pgx.TxOptions{})
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	defer tx.Rollback(ctx)
	var locked screenedCandidateFetchState
	err = tx.QueryRow(ctx, `SELECT plan.id::text,execution.id::text,candidate.id::text,decision.id::text,
		plan.task_id::text,plan.created_by_attempt_id::text,attempt.assigned_agent_id::text,
		execution.adapter,candidate.canonical_url,candidate.canonical_identity,candidate.title,
		decision.effective_independence_family,candidate.content_hash,decision_version.content_hash
		FROM research_source_candidate candidate
		JOIN research_query_execution execution ON (execution.workspace_id,execution.session_id,execution.id)=(candidate.workspace_id,candidate.session_id,candidate.query_execution_id)
		JOIN research_search_plan plan ON (plan.workspace_id,plan.session_id,plan.id)=(execution.workspace_id,execution.session_id,execution.search_plan_id)
		JOIN research_screening_decision decision ON (decision.workspace_id,decision.session_id,decision.query_execution_id,decision.source_candidate_id)=(candidate.workspace_id,candidate.session_id,candidate.query_execution_id,candidate.id)
		JOIN research_task_attempt attempt ON (attempt.workspace_id,attempt.session_id,attempt.id,attempt.task_id)=(plan.workspace_id,plan.session_id,plan.created_by_attempt_id,plan.task_id)
		JOIN research_artifact_passport passport ON (passport.workspace_id,passport.session_id,passport.id)=(decision.workspace_id,decision.session_id,decision.id)
		JOIN research_artifact_version decision_version ON
		  (decision_version.workspace_id,decision_version.session_id,decision_version.artifact_id,decision_version.version)=
		  (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE candidate.workspace_id=$1::uuid AND candidate.session_id=$2::uuid AND candidate.id=$3::uuid AND decision.disposition='accepted'
		FOR UPDATE OF candidate,execution,plan,decision,attempt,passport,decision_version`, in.WorkspaceID, in.SessionID, in.CandidateID).Scan(
		&locked.PlanID, &locked.QueryID, &locked.CandidateID, &locked.DecisionID, &locked.TaskID, &locked.AttemptID, &locked.AgentID,
		&locked.Adapter, &locked.URL, &locked.Identity, &locked.Title, &locked.IndependenceFamily, &locked.CandidateHash, &locked.DecisionHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return FetchScreenedSourceResult{}, fmt.Errorf("%w: accepted Screening lineage changed before ingestion", ErrResultConflict)
	}
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	if locked != expected {
		return FetchScreenedSourceResult{}, fmt.Errorf("%w: accepted Screening lineage changed before ingestion", ErrResultConflict)
	}

	contentHash := document.ContentHash[len("sha256:"):]
	var existingID, existingKind, existingDecision string
	err = tx.QueryRow(ctx, `SELECT id::text,ingestion_kind,COALESCE(screening_decision_id::text,'') FROM research_source_snapshot
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND canonical_url=$3 AND content_hash=$4`, in.WorkspaceID, in.SessionID, document.CanonicalURL, contentHash).Scan(&existingID, &existingKind, &existingDecision)
	if err == nil {
		if existingKind != string(SourceIngestionScreenedRetrieval) || existingDecision != locked.DecisionID {
			return FetchScreenedSourceResult{}, fmt.Errorf("%w: fetched content already exists with different ingestion lineage", ErrResultConflict)
		}
		if err = s.commitResearchTx(ctx, txOpSearchLineageRecord, tx); err != nil {
			return FetchScreenedSourceResult{}, err
		}
		return FetchScreenedSourceResult{SourceSnapshotID: existingID, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FetchScreenedSourceResult{}, err
	}
	sourceID := uuid.NewString()
	capturedAt := time.Now().UTC()
	intent, err := ValidateSourceIngestionIntent(SourceIngestionIntent{PolicyVersion: SourceIngestionPolicyVersionV1, Kind: SourceIngestionScreenedRetrieval,
		WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, SourceSnapshotID: sourceID, ContentHash: document.ContentHash, CapturedAt: capturedAt,
		Locator: "source-candidate:" + locked.CandidateID, Reason: "The accepted screened candidate was fetched and validated as immutable evidence.",
		CanonicalURL: document.CanonicalURL, TaskID: locked.TaskID, AttemptID: locked.AttemptID, AgentID: locked.AgentID,
		SearchPlanID: locked.PlanID, QueryExecutionID: locked.QueryID, SourceCandidateID: locked.CandidateID, ScreeningDecisionID: locked.DecisionID,
		ScreeningDecisionFingerprint: locked.DecisionHash, ScreeningDisposition: "accepted"})
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	metadata, err := json.Marshal(map[string]any{"mime": document.MIME, "retrieval_cost": document.Cost, "retrieval_safety": document.Safety,
		"ingestion_policy": SourceIngestionPolicyVersionV1, "ingestion_fingerprint": intent.Fingerprint})
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_source_snapshot
		(id,workspace_id,session_id,produced_by_task_id,canonical_url,title,publisher,source_class,evidence_traits,independence_key,retrieved_at,
		 snapshot_text,content_hash,metadata,verification_status,ingestion_kind,screening_decision_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'','other','{}'::text[],$7,$8,$9,$10,$11::jsonb,'pending','screened_retrieval',$12::uuid)`,
		sourceID, in.WorkspaceID, in.SessionID, locked.TaskID, document.CanonicalURL, truncateBytes(locked.Title, 4096), truncateBytes(locked.IndependenceFamily, 160),
		capturedAt, string(document.Content), contentHash, metadata, locked.DecisionID); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	versionHash, err := ArtifactContentHash(ArtifactKindSourceSnapshot, map[string]any{"produced_by_task_id": locked.TaskID, "canonical_url": document.CanonicalURL,
		"title": truncateBytes(locked.Title, 4096), "publisher": "", "source_class": "other", "evidence_traits": []string{},
		"independence_key": truncateBytes(locked.IndependenceFamily, 160), "retrieved_at": capturedAt, "snapshot_text": string(document.Content),
		"content_hash": contentHash, "metadata": json.RawMessage(metadata), "verification_status": "pending"})
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	access, err := deriveManifestOutputAccessTx(ctx, tx, in.WorkspaceID, in.SessionID, locked.AttemptID)
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, EntityID: sourceID,
		Kind: ArtifactKindSourceSnapshot, SourceCreatedAt: &capturedAt, ProvenanceCompleteness: ArtifactProvenanceComplete, AccessLevel: access,
		HashOrigin: ArtifactHashOriginProduction, ContentHash: versionHash, ProducedByTaskID: locked.TaskID, ProducedByAttemptID: locked.AttemptID,
		SchemaName: string(ArtifactKindSourceSnapshot), SchemaVersion: OrchestratorVersionV6}); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	for ordinal, reference := range []struct {
		id       string
		kind     ArtifactEntityKind
		relation string
	}{
		{locked.DecisionID, ArtifactKindScreeningDecision, "screened_by"}, {locked.CandidateID, ArtifactKindSourceCandidate, "fetched_from"},
		{locked.QueryID, ArtifactKindQueryExecution, "retrieved_by"}, {locked.PlanID, ArtifactKindSearchPlan, "planned_by"}, {locked.TaskID, ArtifactKindTask, "source_producer"},
	} {
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, in.WorkspaceID, in.SessionID, sourceID, ArtifactKindSourceSnapshot,
			reference.id, reference.kind, reference.relation, "screened_source_ingestion", ordinal); err != nil {
			return FetchScreenedSourceResult{}, err
		}
	}
	if err = recordVerificationPolicyMutationTx(ctx, tx, in.WorkspaceID, in.SessionID, sourceID); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "screened_source_ingested", "screened-source:"+locked.DecisionID, "system", "", map[string]any{
		"source_snapshot_id": sourceID, "screening_decision_id": locked.DecisionID, "candidate_id": locked.CandidateID,
		"query_execution_id": locked.QueryID, "search_plan_id": locked.PlanID, "content_hash": document.ContentHash, "ingestion_fingerprint": intent.Fingerprint,
	})
	if err != nil {
		return FetchScreenedSourceResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpSearchLineageRecord, tx); err != nil {
		return FetchScreenedSourceResult{}, err
	}
	return FetchScreenedSourceResult{SourceSnapshotID: sourceID, Event: event}, nil
}
