package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type frozenOrderedRepresentation[T any] struct {
	Order int `json:"order"`
	Value T   `json:"value"`
}

// freezeArtifactRepresentationsTx replaces hash placeholders with exact bounded
// wire representations supplied to an Agent. Only already-authorized entries
// are read here. Kinds not yet projected retain their legacy placeholder and
// must not be used as frozen task response content.
func freezeArtifactRepresentationsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, entries []artifactVersionCandidate) error {
	authorized := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		authorized[entry.ArtifactID] = struct{}{}
	}
	for i := range entries {
		var value any
		switch entries[i].Kind {
		case ArtifactKindRunSession:
			item, err := loadRun(ctx, tx, sessionID, workspaceID, false)
			if err != nil {
				return fmt.Errorf("freeze run session representation: %w", err)
			}
			value = item
		case ArtifactKindContractRevision:
			var item ResearchContract
			err := tx.QueryRow(ctx, `SELECT goal_version, goal, scope, audience, freshness, language, source_policy, run_limits, reason, created_at FROM research_contract_revision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.GoalVersion, &item.Goal, &item.Scope, &item.Audience, &item.Freshness, &item.Language, &item.SourcePolicy, &item.RunLimits, &item.Reason, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze contract representation: %w", err)
			}
			value = frozenOrderedRepresentation[ResearchContract]{Value: item}
		case ArtifactKindMethodDecision:
			var outcome []byte
			var goalVersion, planVersion int
			var actorID, taskID string
			var createdAt time.Time
			err := tx.QueryRow(ctx, `SELECT outcome, goal_version, plan_version, COALESCE(actor_id::text, ''), COALESCE(inputs->>'task_id', ''), created_at FROM research_decision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND decision_kind='research_method'`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&outcome, &goalVersion, &planVersion, &actorID, &taskID, &createdAt)
			if err != nil {
				return fmt.Errorf("freeze method representation: %w", err)
			}
			var item ResearchMethod
			if err = json.Unmarshal(outcome, &item); err != nil {
				return fmt.Errorf("decode method representation: %w", err)
			}
			item.GoalVersion = goalVersion
			item.PlanVersion = planVersion
			item.CreatedByAgentID = actorID
			item.CreatedByTaskID = taskID
			item.CreatedAt = createdAt
			value = frozenOrderedRepresentation[ResearchMethod]{Value: item}
		case ArtifactKindQuestion:
			var order int
			var item Question
			err := tx.QueryRow(ctx, `
				WITH ordered AS (
				  SELECT q.*, row_number() OVER (ORDER BY required DESC, priority DESC, created_at, id)::int - 1 AS frozen_order
				  FROM research_question q WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				)
				SELECT frozen_order, id::text, session_id::text, COALESCE(parent_question_id::text, ''), COALESCE(created_by_task_id::text, ''), client_key, kind, question, required, status, priority, impact, uncertainty, novelty, coverage, goal_version, plan_version, COALESCE(answer_claim_id::text, ''), terminal_explanation
				FROM ordered WHERE id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&order, &item.ID, &item.SessionID, &item.ParentQuestionID, &item.CreatedByTaskID, &item.ClientKey, &item.Kind, &item.Question, &item.Required, &item.Status, &item.Priority, &item.Impact, &item.Uncertainty, &item.Novelty, &item.Coverage, &item.GoalVersion, &item.PlanVersion, &item.AnswerClaimID, &item.TerminalExplanation)
			if err != nil {
				return fmt.Errorf("freeze question representation: %w", err)
			}
			value = frozenOrderedRepresentation[Question]{Order: order, Value: item}
		case ArtifactKindTask:
			var order int
			if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task candidate JOIN research_task target ON target.workspace_id=candidate.workspace_id AND target.session_id=candidate.session_id WHERE target.workspace_id=$1::uuid AND target.session_id=$2::uuid AND target.id=$3::uuid AND (candidate.priority > target.priority OR (candidate.priority = target.priority AND (candidate.created_at < target.created_at OR (candidate.created_at = target.created_at AND candidate.id < target.id))))`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&order); err != nil {
				return fmt.Errorf("freeze task order: %w", err)
			}
			item, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.workspace_id=$1::uuid AND t.session_id=$2::uuid AND t.id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID))
			if err != nil {
				return fmt.Errorf("freeze task representation: %w", err)
			}
			value = frozenOrderedRepresentation[Task]{Order: order, Value: item}
		case ArtifactKindAttempt:
			var order int
			if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task_attempt candidate JOIN research_task_attempt target ON target.workspace_id=candidate.workspace_id AND target.session_id=candidate.session_id WHERE target.workspace_id=$1::uuid AND target.session_id=$2::uuid AND target.id=$3::uuid AND (candidate.created_at < target.created_at OR (candidate.created_at = target.created_at AND candidate.id < target.id))`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&order); err != nil {
				return fmt.Errorf("freeze attempt order: %w", err)
			}
			item, err := scanAttempt(tx.QueryRow(ctx, attemptSelectSQL+` WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID))
			if err != nil {
				return fmt.Errorf("freeze attempt representation: %w", err)
			}
			value = frozenOrderedRepresentation[Attempt]{Order: order, Value: item}
		case ArtifactKindSourceSnapshot:
			var item SourceSnapshotView
			err := tx.QueryRow(ctx, `SELECT id::text, COALESCE(produced_by_task_id::text, ''), canonical_url, title, publisher, source_class, evidence_traits, independence_key, retrieved_at, content_hash, left(snapshot_text, $4), metadata, verification_status, created_at FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID, sourceSnapshotExcerptChars).Scan(&item.ID, &item.ProducedByTaskID, &item.CanonicalURL, &item.Title, &item.Publisher, &item.SourceClass, &item.EvidenceTraits, &item.IndependenceKey, &item.RetrievedAt, &item.ContentHash, &item.SnapshotExcerpt, &item.Metadata, &item.VerificationStatus, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze source representation: %w", err)
			}
			value = item
		case ArtifactKindObservation:
			var item Observation
			err := tx.QueryRow(ctx, `SELECT id::text, source_snapshot_id::text, COALESCE(produced_by_task_id::text, ''), quote, datum, locator, interpretation, verification_status, created_at FROM research_observation WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.SourceSnapshotID, &item.ProducedByTaskID, &item.Quote, &item.Datum, &item.Locator, &item.Interpretation, &item.VerificationStatus, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze observation representation: %w", err)
			}
			value = item
		case ArtifactKindClaim:
			var item Claim
			err := tx.QueryRow(ctx, `SELECT id::text, COALESCE(produced_by_task_id::text, ''), client_key, evidence_standard_key, claim_text, significance, confidence, status, goal_version, plan_version, resolution, created_at, updated_at FROM research_claim WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.ProducedByTaskID, &item.ClientKey, &item.EvidenceStandardKey, &item.Text, &item.Significance, &item.Confidence, &item.Status, &item.GoalVersion, &item.PlanVersion, &item.Resolution, &item.CreatedAt, &item.UpdatedAt)
			if err != nil {
				return fmt.Errorf("freeze claim representation: %w", err)
			}
			item.Evidence = []ClaimEvidence{}
			rows, evidenceErr := tx.Query(ctx, `SELECT id::text, observation_id::text, relation, strength, directness, method_fit, verification_status, COALESCE(verified_by_task_id::text, ''), rationale FROM research_claim_evidence WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND claim_id=$3::uuid ORDER BY created_at, claim_id, observation_id, relation, id`, workspaceID, sessionID, item.ID)
			if evidenceErr != nil {
				return fmt.Errorf("freeze claim evidence: %w", evidenceErr)
			}
			for rows.Next() {
				var evidenceID string
				var evidence ClaimEvidence
				if evidenceErr = rows.Scan(&evidenceID, &evidence.ObservationID, &evidence.Relation, &evidence.Strength, &evidence.Directness, &evidence.MethodFit, &evidence.VerificationStatus, &evidence.VerifiedByTaskID, &evidence.Rationale); evidenceErr != nil {
					rows.Close()
					return fmt.Errorf("scan claim evidence: %w", evidenceErr)
				}
				if _, ok := authorized[evidenceID]; ok {
					item.Evidence = append(item.Evidence, evidence)
				}
			}
			evidenceErr = rows.Err()
			rows.Close()
			if evidenceErr != nil {
				return fmt.Errorf("read claim evidence: %w", evidenceErr)
			}
			value = item
		case ArtifactKindStageEvaluation:
			var item EvaluationPrivateContext
			err := tx.QueryRow(ctx, `SELECT id::text, stage, passed, score, findings, remediation, created_at FROM research_stage_eval WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.Stage, &item.Passed, &item.Score, &item.Findings, &item.Remediation, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze evaluation-private representation: %w", err)
			}
			value = item
		default:
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode %s representation: %w", entries[i].Kind, err)
		}
		entries[i].Representation = "full"
		entries[i].RepresentationBytes = encoded
		entries[i].RepresentationHash = contentHashFromPayload(encoded)
	}
	return nil
}

type frozenDurableContext struct {
	Contract  ResearchContract
	Method    *ResearchMethod
	Questions []Question
	Tasks     []Task
	Attempts  []Attempt
}

func loadFrozenDurableContextPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) (frozenDurableContext, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.entity_kind, e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid
		  AND p.entity_kind IN ('contract_revision','method_decision','question','task','attempt')
		ORDER BY e.ordinal
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return frozenDurableContext{}, err
	}
	defer rows.Close()

	var contracts []frozenOrderedRepresentation[ResearchContract]
	var methods []frozenOrderedRepresentation[ResearchMethod]
	var questions []frozenOrderedRepresentation[Question]
	var tasks []frozenOrderedRepresentation[Task]
	var attempts []frozenOrderedRepresentation[Attempt]
	for rows.Next() {
		var kind string
		var encoded []byte
		var storedHash string
		if err = rows.Scan(&kind, &encoded, &storedHash); err != nil {
			return frozenDurableContext{}, err
		}
		if len(encoded) == 0 || contentHashFromPayload(encoded) != storedHash {
			return frozenDurableContext{}, fmt.Errorf("%w: frozen %s representation missing or invalid", ErrInvalidTransition, kind)
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindContractRevision:
			var item frozenOrderedRepresentation[ResearchContract]
			err = json.Unmarshal(encoded, &item)
			if err != nil {
				break
			}
			contracts = append(contracts, item)
		case ArtifactKindMethodDecision:
			var item frozenOrderedRepresentation[ResearchMethod]
			err = json.Unmarshal(encoded, &item)
			if err != nil {
				break
			}
			methods = append(methods, item)
		case ArtifactKindQuestion:
			var item frozenOrderedRepresentation[Question]
			err = json.Unmarshal(encoded, &item)
			if err != nil {
				break
			}
			if item.Value.SessionID != sessionID {
				return frozenDurableContext{}, fmt.Errorf("%w: frozen question scope mismatch", ErrInvalidTransition)
			}
			questions = append(questions, item)
		case ArtifactKindTask:
			var item frozenOrderedRepresentation[Task]
			err = json.Unmarshal(encoded, &item)
			if err != nil {
				break
			}
			if item.Value.SessionID != sessionID || item.Value.WorkspaceID != workspaceID {
				return frozenDurableContext{}, fmt.Errorf("%w: frozen task scope mismatch", ErrInvalidTransition)
			}
			tasks = append(tasks, item)
		case ArtifactKindAttempt:
			var item frozenOrderedRepresentation[Attempt]
			err = json.Unmarshal(encoded, &item)
			if err != nil {
				break
			}
			if item.Value.SessionID != sessionID || item.Value.WorkspaceID != workspaceID {
				return frozenDurableContext{}, fmt.Errorf("%w: frozen attempt scope mismatch", ErrInvalidTransition)
			}
			attempts = append(attempts, item)
		}
		if err != nil {
			return frozenDurableContext{}, fmt.Errorf("decode frozen %s representation: %w", kind, err)
		}
	}
	if err = rows.Err(); err != nil {
		return frozenDurableContext{}, err
	}
	if len(contracts) == 0 || len(tasks) == 0 || len(attempts) == 0 {
		return frozenDurableContext{}, fmt.Errorf("%w: frozen durable context is incomplete", ErrInvalidTransition)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Value.GoalVersion < contracts[j].Value.GoalVersion })
	sort.Slice(methods, func(i, j int) bool {
		if methods[i].Value.GoalVersion != methods[j].Value.GoalVersion {
			return methods[i].Value.GoalVersion < methods[j].Value.GoalVersion
		}
		return methods[i].Value.PlanVersion < methods[j].Value.PlanVersion
	})
	sort.Slice(questions, func(i, j int) bool { return questions[i].Order < questions[j].Order })
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Order < tasks[j].Order })
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].Order < attempts[j].Order })
	out := frozenDurableContext{Contract: contracts[len(contracts)-1].Value}
	if len(methods) > 0 {
		method := methods[len(methods)-1].Value
		out.Method = &method
	}
	for _, item := range questions {
		out.Questions = append(out.Questions, item.Value)
	}
	for _, item := range tasks {
		out.Tasks = append(out.Tasks, item.Value)
	}
	for _, item := range attempts {
		out.Attempts = append(out.Attempts, item.Value)
	}
	return out, nil
}

func loadFrozenRunRepresentationPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) (Run, error) {
	var encoded []byte
	var storedHash string
	err := pool.QueryRow(ctx, `
		SELECT e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p
		  ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid
		  AND p.entity_kind='run_session'
	`, workspaceID, sessionID, attemptID).Scan(&encoded, &storedHash)
	if err != nil {
		return Run{}, fmt.Errorf("load frozen run session representation: %w", err)
	}
	if len(encoded) == 0 || contentHashFromPayload(encoded) != storedHash {
		return Run{}, fmt.Errorf("%w: frozen run session representation missing or invalid", ErrInvalidTransition)
	}
	var run Run
	if err = json.Unmarshal(encoded, &run); err != nil {
		return Run{}, fmt.Errorf("decode frozen run session representation: %w", err)
	}
	if run.SessionID != sessionID || run.WorkspaceID != workspaceID {
		return Run{}, fmt.Errorf("%w: frozen run session scope mismatch", ErrInvalidTransition)
	}
	return run, nil
}

func loadFrozenEvaluationPrivatePool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) ([]EvaluationPrivateContext, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.representation_bytes
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_policy_grant g ON (g.workspace_id,g.session_id,g.id)=(m.workspace_id,m.session_id,m.evaluation_grant_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid
		  AND m.purpose='evaluation' AND g.status='active' AND g.revision=m.evaluation_grant_revision
		  AND g.evaluation_private=true AND p.entity_kind='stage_evaluation'
		ORDER BY e.ordinal
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvaluationPrivateContext{}
	for rows.Next() {
		var encoded []byte
		if err = rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var item EvaluationPrivateContext
		if err = json.Unmarshal(encoded, &item); err != nil {
			return nil, fmt.Errorf("decode evaluation-private representation: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadFrozenEvaluationPrivateTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, manifestID string) ([]EvaluationPrivateContext, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.representation_bytes
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_policy_grant g ON (g.workspace_id,g.session_id,g.id)=(m.workspace_id,m.session_id,m.evaluation_grant_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.id=$3::uuid
		  AND m.purpose='evaluation' AND g.status='active' AND g.revision=m.evaluation_grant_revision
		  AND g.evaluation_private=true AND p.entity_kind='stage_evaluation'
		ORDER BY e.ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvaluationPrivateContext{}
	for rows.Next() {
		var encoded []byte
		if err = rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var item EvaluationPrivateContext
		if err = json.Unmarshal(encoded, &item); err != nil {
			return nil, fmt.Errorf("decode evaluation-private representation: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadFrozenEvidenceRepresentationsPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) ([]SourceSnapshotView, []Observation, []Claim, error) {
	rows, err := pool.Query(ctx, `SELECT p.entity_kind, e.representation_bytes FROM research_artifact_context_entry e JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id) JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id) JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id) WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid AND p.entity_kind IN ('source_snapshot','observation','claim') ORDER BY e.ordinal`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	sources := []SourceSnapshotView{}
	observations := []Observation{}
	claims := []Claim{}
	for rows.Next() {
		var kind string
		var encoded []byte
		if err = rows.Scan(&kind, &encoded); err != nil {
			return nil, nil, nil, err
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindSourceSnapshot:
			var item SourceSnapshotView
			if err = json.Unmarshal(encoded, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen source: %w", err)
			}
			sources = append(sources, item)
		case ArtifactKindObservation:
			var item Observation
			if err = json.Unmarshal(encoded, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen observation: %w", err)
			}
			observations = append(observations, item)
		case ArtifactKindClaim:
			var item Claim
			if err = json.Unmarshal(encoded, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen claim: %w", err)
			}
			claims = append(claims, item)
		}
	}
	return sources, observations, claims, rows.Err()
}
