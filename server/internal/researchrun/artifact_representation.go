package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// freezeEvidenceRepresentationsTx replaces the legacy hash placeholder with
// the exact bounded wire representation supplied to an Agent. Despite the
// historical name, this includes the core Run context as well as evidence.
// Only already authorized entries are read here.
func freezeEvidenceRepresentationsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, entries []artifactVersionCandidate) error {
	authorized := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		authorized[entry.ArtifactID] = struct{}{}
	}
	for i := range entries {
		var value any
		switch entries[i].Kind {
		case ArtifactKindRunSession:
			item, err := loadRun(ctx, tx, entries[i].ArtifactID, workspaceID, false)
			if err != nil {
				return fmt.Errorf("freeze run representation: %w", err)
			}
			value = item
		case ArtifactKindContractRevision:
			var item ResearchContract
			err := tx.QueryRow(ctx, `SELECT goal_version, goal, scope, audience, freshness, language, source_policy, run_limits, reason, created_at FROM research_contract_revision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&item.GoalVersion, &item.Goal, &item.Scope, &item.Audience, &item.Freshness,
				&item.Language, &item.SourcePolicy, &item.RunLimits, &item.Reason, &item.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze contract representation: %w", err)
			}
			value = item
		case ArtifactKindMethodDecision:
			var item ResearchMethod
			var outcome []byte
			var goalVersion, planVersion int
			var actorID, taskID string
			var createdAt time.Time
			err := tx.QueryRow(ctx, `SELECT outcome, goal_version, plan_version, COALESCE(actor_id::text, ''), COALESCE(inputs->>'task_id', ''), created_at FROM research_decision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND decision_kind='research_method'`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&outcome, &goalVersion, &planVersion, &actorID, &taskID, &createdAt,
			)
			if err != nil {
				return fmt.Errorf("freeze method representation: %w", err)
			}
			if err = json.Unmarshal(outcome, &item); err != nil {
				return fmt.Errorf("decode method representation: %w", err)
			}
			item.GoalVersion = goalVersion
			item.PlanVersion = planVersion
			item.CreatedByAgentID = actorID
			item.CreatedByTaskID = taskID
			item.CreatedAt = createdAt
			value = item
		case ArtifactKindQuestion:
			var item Question
			err := tx.QueryRow(ctx, `SELECT id::text, session_id::text, COALESCE(parent_question_id::text, ''), COALESCE(created_by_task_id::text, ''), client_key, kind, question, required, status, priority, impact, uncertainty, novelty, coverage, goal_version, plan_version, COALESCE(answer_claim_id::text, ''), terminal_explanation FROM research_question WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&item.ID, &item.SessionID, &item.ParentQuestionID, &item.CreatedByTaskID,
				&item.ClientKey, &item.Kind, &item.Question, &item.Required, &item.Status,
				&item.Priority, &item.Impact, &item.Uncertainty, &item.Novelty, &item.Coverage,
				&item.GoalVersion, &item.PlanVersion, &item.AnswerClaimID, &item.TerminalExplanation,
			)
			if err != nil {
				return fmt.Errorf("freeze question representation: %w", err)
			}
			value = item
		case ArtifactKindTask:
			item, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.workspace_id=$1::uuid AND t.session_id=$2::uuid AND t.id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID))
			if err != nil {
				return fmt.Errorf("freeze task representation: %w", err)
			}
			value = item
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

type frozenCoreContext struct {
	Run       *Run
	Contract  *ResearchContract
	Method    *ResearchMethod
	Questions map[string]Question
	Tasks     map[string]Task
}

func loadFrozenCoreContextPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) (frozenCoreContext, error) {
	rows, err := pool.Query(ctx, `SELECT p.entity_kind, e.representation_bytes FROM research_artifact_context_entry e JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id) JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id) JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id) WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid AND p.entity_kind IN ('run_session','contract_revision','method_decision','question','task') ORDER BY e.ordinal`, workspaceID, sessionID, attemptID)
	if err != nil {
		return frozenCoreContext{}, err
	}
	defer rows.Close()
	out := frozenCoreContext{Questions: map[string]Question{}, Tasks: map[string]Task{}}
	for rows.Next() {
		var kind string
		var encoded []byte
		if err = rows.Scan(&kind, &encoded); err != nil {
			return frozenCoreContext{}, err
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindRunSession:
			if out.Run != nil {
				return frozenCoreContext{}, fmt.Errorf("%w: duplicate frozen run representation", ErrInvalidTransition)
			}
			var item Run
			if err = json.Unmarshal(encoded, &item); err != nil {
				return frozenCoreContext{}, fmt.Errorf("decode frozen run: %w", err)
			}
			out.Run = &item
		case ArtifactKindContractRevision:
			if out.Contract != nil {
				return frozenCoreContext{}, fmt.Errorf("%w: duplicate frozen contract representation", ErrInvalidTransition)
			}
			var item ResearchContract
			if err = json.Unmarshal(encoded, &item); err != nil {
				return frozenCoreContext{}, fmt.Errorf("decode frozen contract: %w", err)
			}
			out.Contract = &item
		case ArtifactKindMethodDecision:
			if out.Method != nil {
				return frozenCoreContext{}, fmt.Errorf("%w: duplicate frozen method representation", ErrInvalidTransition)
			}
			var item ResearchMethod
			if err = json.Unmarshal(encoded, &item); err != nil {
				return frozenCoreContext{}, fmt.Errorf("decode frozen method: %w", err)
			}
			out.Method = &item
		case ArtifactKindQuestion:
			var item Question
			if err = json.Unmarshal(encoded, &item); err != nil {
				return frozenCoreContext{}, fmt.Errorf("decode frozen question: %w", err)
			}
			if item.ID == "" || out.Questions[item.ID].ID != "" {
				return frozenCoreContext{}, fmt.Errorf("%w: invalid or duplicate frozen question", ErrInvalidTransition)
			}
			out.Questions[item.ID] = item
		case ArtifactKindTask:
			var item Task
			if err = json.Unmarshal(encoded, &item); err != nil {
				return frozenCoreContext{}, fmt.Errorf("decode frozen task: %w", err)
			}
			if item.ID == "" || out.Tasks[item.ID].ID != "" {
				return frozenCoreContext{}, fmt.Errorf("%w: invalid or duplicate frozen task", ErrInvalidTransition)
			}
			out.Tasks[item.ID] = item
		}
	}
	if err = rows.Err(); err != nil {
		return frozenCoreContext{}, err
	}
	if out.Run == nil || out.Contract == nil {
		return frozenCoreContext{}, fmt.Errorf("%w: frozen core context is incomplete", ErrInvalidTransition)
	}
	return out, nil
}

func applyFrozenCoreContext(live RunSnapshot, frozen frozenCoreContext) (RunSnapshot, error) {
	if frozen.Run == nil || frozen.Contract == nil {
		return RunSnapshot{}, fmt.Errorf("%w: frozen core context is incomplete", ErrInvalidTransition)
	}
	live.Run = *frozen.Run
	live.Contract = *frozen.Contract
	live.Method = frozen.Method
	questions := make([]Question, 0, len(frozen.Questions))
	for _, item := range live.Questions {
		if frozenItem, ok := frozen.Questions[item.ID]; ok {
			questions = append(questions, frozenItem)
			delete(frozen.Questions, item.ID)
		}
	}
	if len(frozen.Questions) != 0 {
		return RunSnapshot{}, fmt.Errorf("%w: frozen question ordering source is missing", ErrInvalidTransition)
	}
	live.Questions = questions
	tasks := make([]Task, 0, len(frozen.Tasks))
	for _, item := range live.Tasks {
		if frozenItem, ok := frozen.Tasks[item.ID]; ok {
			tasks = append(tasks, frozenItem)
			delete(frozen.Tasks, item.ID)
		}
	}
	if len(frozen.Tasks) != 0 {
		return RunSnapshot{}, fmt.Errorf("%w: frozen task ordering source is missing", ErrInvalidTransition)
	}
	live.Tasks = tasks
	return live, nil
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
