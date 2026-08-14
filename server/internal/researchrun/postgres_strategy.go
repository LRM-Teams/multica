package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PersistStrategyPromotionInput struct {
	WorkspaceID           string
	RequestKey            string
	Promotion             StrategyPromotionInput
	CandidateConfig       json.RawMessage
	EvaluationCompletedAt time.Time
}

type DurableStrategyPromotionDecision struct {
	ID                 string
	WorkspaceID        string
	RequestKey         string
	RequestHash        string
	CurrentVersionID   string
	CandidateVersionID string
	EvaluationID       string
	Action             string
	Reason             string
	ApprovedBy         *string
	DecidedAt          time.Time
	PointerGeneration  int64
	EffectiveVersion   string
}

type StrategyVersionBinding struct {
	ID                string
	WorkspaceID       string
	Version           string
	PreviousVersionID *string
	Config            json.RawMessage
	ConfigHash        string
	Generation        int64
	AssignedAt        time.Time
}

func (s *PostgresStore) CurrentStrategy(ctx context.Context, workspaceID string) (StrategyVersionBinding, error) {
	var binding StrategyVersionBinding
	err := s.pool.QueryRow(ctx, `
		SELECT v.id::text,v.workspace_id::text,v.version_key,v.previous_version_id::text,
		       v.config,v.config_hash,p.generation,p.updated_at
		FROM research_strategy_pointer p
		JOIN research_strategy_version v ON v.workspace_id=p.workspace_id AND v.id=p.current_version_id
		WHERE p.workspace_id=$1::uuid
	`, strings.TrimSpace(workspaceID)).Scan(&binding.ID, &binding.WorkspaceID, &binding.Version,
		&binding.PreviousVersionID, &binding.Config, &binding.ConfigHash, &binding.Generation, &binding.AssignedAt)
	return binding, err
}

func (s *PostgresStore) RunStrategy(ctx context.Context, workspaceID, sessionID string) (StrategyVersionBinding, error) {
	var binding StrategyVersionBinding
	err := s.pool.QueryRow(ctx, `
		SELECT v.id::text,v.workspace_id::text,v.version_key,v.previous_version_id::text,
		       v.config,v.config_hash,a.pointer_generation,a.assigned_at
		FROM research_run_strategy_assignment a
		JOIN research_strategy_version v ON v.workspace_id=a.workspace_id AND v.id=a.strategy_version_id
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid
	`, strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)).Scan(&binding.ID, &binding.WorkspaceID,
		&binding.Version, &binding.PreviousVersionID, &binding.Config, &binding.ConfigHash,
		&binding.Generation, &binding.AssignedAt)
	return binding, err
}

// PersistStrategyPromotion records the immutable candidate, evaluation, and
// decision before atomically moving the workspace pointer. Replaying the same
// request key and bytes returns the original decision; different bytes fail.
func (s *PostgresStore) PersistStrategyPromotion(ctx context.Context, input PersistStrategyPromotionInput) (DurableStrategyPromotionDecision, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.RequestKey = strings.TrimSpace(input.RequestKey)
	if input.WorkspaceID == "" || input.RequestKey == "" || len(input.RequestKey) > 256 || input.EvaluationCompletedAt.IsZero() || len(input.CandidateConfig) > 1<<20 {
		return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: durable Strategy promotion identity is incomplete", ErrInvalidContract)
	}
	decision, err := EvaluateStrategyPromotion(input.Promotion)
	if err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	if input.Promotion.Current.StrategyVersion == input.Promotion.Candidate.StrategyVersion {
		return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: candidate Strategy must differ from current", ErrInvalidContract)
	}
	config, configHash, err := canonicalStrategyJSON(input.CandidateConfig)
	if err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	_, evaluationHash, err := canonicalStrategyJSONValue(input.Promotion.Candidate)
	if err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	requestHash, err := strategyPromotionRequestHash(input, configHash, evaluationHash, decision)
	if err != nil {
		return DurableStrategyPromotionDecision{}, err
	}

	tx, err := s.beginResearchTx(ctx, txOpStrategyPromotion, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	defer tx.Rollback(ctx)

	var currentID, currentKey string
	var generation int64
	if err = tx.QueryRow(ctx, `
		SELECT p.current_version_id::text,v.version_key,p.generation
		FROM research_strategy_pointer p
		JOIN research_strategy_version v ON v.workspace_id=p.workspace_id AND v.id=p.current_version_id
		WHERE p.workspace_id=$1::uuid FOR UPDATE OF p
	`, input.WorkspaceID).Scan(&currentID, &currentKey, &generation); err != nil {
		return DurableStrategyPromotionDecision{}, err
	}

	if existing, found, loadErr := loadDurableStrategyDecision(ctx, tx, input.WorkspaceID, input.RequestKey); loadErr != nil {
		return DurableStrategyPromotionDecision{}, loadErr
	} else if found {
		if existing.RequestHash != requestHash {
			return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: Strategy promotion request key reused with different input", ErrInvalidContract)
		}
		if err = s.commitResearchTx(ctx, txOpStrategyPromotion, tx); err != nil {
			return DurableStrategyPromotionDecision{}, err
		}
		return existing, nil
	}
	if currentKey != input.Promotion.Current.StrategyVersion {
		return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: current Strategy pointer changed", ErrInvalidTransition)
	}

	var candidateID, storedConfigHash, storedPreviousID string
	if err = tx.QueryRow(ctx, `
		WITH inserted AS (
		  INSERT INTO research_strategy_version (workspace_id,version_key,previous_version_id,config,config_hash,created_by)
		  VALUES ($1::uuid,$2,$3::uuid,$4::jsonb,$5,NULLIF($6,'')::uuid)
		  ON CONFLICT (workspace_id,version_key) DO NOTHING
		  RETURNING id,config_hash,previous_version_id
		)
		SELECT id::text,config_hash,COALESCE(previous_version_id::text,'') FROM inserted
		UNION ALL
		SELECT id::text,config_hash,COALESCE(previous_version_id::text,'')
		FROM research_strategy_version
		WHERE workspace_id=$1::uuid AND version_key=$2
		LIMIT 1
	`, input.WorkspaceID, input.Promotion.Candidate.StrategyVersion, currentID, config, configHash, input.Promotion.ApproverUserID).Scan(&candidateID, &storedConfigHash, &storedPreviousID); err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	if storedConfigHash != configHash || storedPreviousID != currentID {
		return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: Strategy version key already names different content", ErrInvalidContract)
	}

	var evaluationID, storedEvaluationHash, storedEvaluationVersionID string
	if err = tx.QueryRow(ctx, `
		WITH inserted AS (
		  INSERT INTO research_strategy_evaluation (
		    workspace_id,strategy_version_id,evaluation_run_id,corpus_version,seed_count,
		    historical_replay_count,deterministic_invariants_passed,mode_scores,cost,latency,
		    report_hash,completed_at
		  ) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12)
		  ON CONFLICT (workspace_id,evaluation_run_id) DO NOTHING
		  RETURNING id,report_hash,strategy_version_id
		)
		SELECT id::text,report_hash,strategy_version_id::text FROM inserted
		UNION ALL
		SELECT id::text,report_hash,strategy_version_id::text
		FROM research_strategy_evaluation
		WHERE workspace_id=$1::uuid AND evaluation_run_id=$3
		LIMIT 1
	`, input.WorkspaceID, candidateID, input.Promotion.EvaluationRunID,
		input.Promotion.Candidate.CorpusVersion, input.Promotion.Candidate.SeedCount,
		input.Promotion.Candidate.HistoricalReplayCount, input.Promotion.Candidate.DeterministicInvariantsPassed,
		evaluationJSONModeScores(input.Promotion.Candidate.ModeScores), input.Promotion.Candidate.Cost,
		input.Promotion.Candidate.Latency, evaluationHash, input.EvaluationCompletedAt,
	).Scan(&evaluationID, &storedEvaluationHash, &storedEvaluationVersionID); err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	if storedEvaluationHash != evaluationHash || storedEvaluationVersionID != candidateID {
		return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: evaluation run already names different evidence", ErrInvalidContract)
	}

	action := "reject"
	approvedBy := ""
	effectiveVersionID := currentID
	decisionGeneration := generation
	if decision.Promoted {
		action = "promote"
		approvedBy = input.Promotion.ApproverUserID
		effectiveVersionID = candidateID
		decisionGeneration++
	}
	var result DurableStrategyPromotionDecision
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_strategy_promotion_decision (
		  workspace_id,request_key,request_hash,current_version_id,candidate_version_id,
		  evaluation_id,action,reason,approved_by,effective_version_id,pointer_generation
		) VALUES ($1::uuid,$2,$3,$4::uuid,$5::uuid,$6::uuid,$7,$8,NULLIF($9,'')::uuid,$10::uuid,$11)
		RETURNING id::text,decided_at
	`, input.WorkspaceID, input.RequestKey, requestHash, currentID, candidateID, evaluationID,
		action, decision.Reason, approvedBy, effectiveVersionID, decisionGeneration).Scan(&result.ID, &result.DecidedAt); err != nil {
		return DurableStrategyPromotionDecision{}, err
	}

	result.WorkspaceID, result.RequestKey, result.RequestHash = input.WorkspaceID, input.RequestKey, requestHash
	result.CurrentVersionID, result.CandidateVersionID, result.EvaluationID = currentID, candidateID, evaluationID
	result.Action, result.Reason, result.PointerGeneration = action, decision.Reason, decisionGeneration
	result.EffectiveVersion = currentKey
	if approvedBy != "" {
		result.ApprovedBy = &approvedBy
	}
	if decision.Promoted {
		var tag pgconn.CommandTag
		if tag, err = tx.Exec(ctx, `
			UPDATE research_strategy_pointer
			SET previous_version_id=current_version_id,current_version_id=$2::uuid,
			    generation=generation+1,updated_by_decision_id=$3::uuid,updated_at=now()
			WHERE workspace_id=$1::uuid AND current_version_id=$4::uuid
		`, input.WorkspaceID, candidateID, result.ID, currentID); err != nil {
			return DurableStrategyPromotionDecision{}, err
		}
		if tag.RowsAffected() != 1 {
			return DurableStrategyPromotionDecision{}, fmt.Errorf("%w: current Strategy pointer changed", ErrInvalidTransition)
		}
		result.EffectiveVersion = input.Promotion.Candidate.StrategyVersion
	}
	if err = s.commitResearchTx(ctx, txOpStrategyPromotion, tx); err != nil {
		return DurableStrategyPromotionDecision{}, err
	}
	return result, nil
}

func loadDurableStrategyDecision(ctx context.Context, tx pgx.Tx, workspaceID, requestKey string) (DurableStrategyPromotionDecision, bool, error) {
	var result DurableStrategyPromotionDecision
	var approvedBy *string
	err := tx.QueryRow(ctx, `
		SELECT d.id::text,d.workspace_id::text,d.request_key,d.request_hash,
		       d.current_version_id::text,d.candidate_version_id::text,d.evaluation_id::text,
		       d.action,d.reason,d.approved_by::text,d.decided_at,d.pointer_generation,v.version_key
		FROM research_strategy_promotion_decision d
		JOIN research_strategy_version v ON v.workspace_id=d.workspace_id AND v.id=d.effective_version_id
		WHERE d.workspace_id=$1::uuid AND d.request_key=$2
	`, workspaceID, requestKey).Scan(&result.ID, &result.WorkspaceID, &result.RequestKey, &result.RequestHash,
		&result.CurrentVersionID, &result.CandidateVersionID, &result.EvaluationID, &result.Action,
		&result.Reason, &approvedBy, &result.DecidedAt, &result.PointerGeneration, &result.EffectiveVersion)
	if err == pgx.ErrNoRows {
		return DurableStrategyPromotionDecision{}, false, nil
	}
	result.ApprovedBy = approvedBy
	return result, err == nil, err
}

func canonicalStrategyJSON(raw json.RawMessage) ([]byte, string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, "", fmt.Errorf("%w: Strategy config must be a JSON object", ErrInvalidContract)
	}
	return canonicalStrategyJSONValue(value)
}

func canonicalStrategyJSONValue(value any) ([]byte, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("%w: Strategy evidence cannot be encoded", ErrInvalidContract)
	}
	sum := sha256.Sum256(payload)
	return payload, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func evaluationJSONModeScores(scores map[string]float64) []byte {
	payload, _ := json.Marshal(scores)
	return payload
}

func strategyPromotionRequestHash(input PersistStrategyPromotionInput, configHash, evaluationHash string, decision StrategyPromotionDecision) (string, error) {
	material := struct {
		WorkspaceID  string
		RequestKey   string
		Promotion    StrategyPromotionInput
		ConfigHash   string
		Evaluation   string
		EvaluationAt time.Time
		Promoted     bool
		Reason       string
	}{input.WorkspaceID, input.RequestKey, input.Promotion, configHash, evaluationHash,
		input.EvaluationCompletedAt.UTC(), decision.Promoted, decision.Reason}
	_, hash, err := canonicalStrategyJSONValue(material)
	return hash, err
}
