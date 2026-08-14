package researchrun

import (
	"context"
	"encoding/json"
	"math"

	"github.com/jackc/pgx/v5"
)

// researchGraphState contains only canonical facts that can change the value
// of an evidence batch. It intentionally excludes Agent-reported prose and
// self-assessed confidence.
type researchGraphState struct {
	Questions                         int     `json:"questions"`
	RequiredQuestions                 int     `json:"required_questions"`
	VerifiedRequiredCoverage          float64 `json:"verified_required_coverage"`
	VerifiedAnsweredRequiredQuestions int     `json:"verified_answered_required_questions"`
	SourceSnapshots                   int     `json:"source_snapshots"`
	VerifiedIndependentSources        int     `json:"verified_independent_sources"`
	Observations                      int     `json:"observations"`
	Claims                            int     `json:"claims"`
	ResolvedClaims                    int     `json:"resolved_claims"`
	ClaimAdjudicationHash             string  `json:"claim_adjudication_hash"`
	EvidenceLinks                     int     `json:"evidence_links"`
	VerifiedEvidenceLinks             int     `json:"verified_evidence_links"`
	VerifiedContradictions            int     `json:"verified_contradictions"`
}

type informationGainBreakdown struct {
	VerifiedCoverage   float64 `json:"verified_coverage"`
	AnsweredQuestions  float64 `json:"answered_questions"`
	IndependentSources float64 `json:"independent_sources"`
	VerifiedEvidence   float64 `json:"verified_evidence"`
	Counterevidence    float64 `json:"counterevidence"`
	ClaimResolution    float64 `json:"claim_resolution"`
	ClaimAdjudication  float64 `json:"claim_adjudication"`
	SourceNovelty      float64 `json:"source_novelty"`
	ObservationNovelty float64 `json:"observation_novelty"`
	ClaimNovelty       float64 `json:"claim_novelty"`
	QuestionNovelty    float64 `json:"question_novelty"`
	Score              float64 `json:"score"`
}

func (s *PostgresStore) loadResearchGraphState(ctx context.Context, tx pgx.Tx, sessionID string, goalVersion, planVersion int) (researchGraphState, error) {
	var state researchGraphState
	err := tx.QueryRow(ctx, `
		WITH current_questions AS (
		  SELECT * FROM research_question
		  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		), current_claims AS (
		  SELECT * FROM research_claim
		  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		), current_evidence AS (
		  SELECT evidence.*
		  FROM research_claim_evidence evidence JOIN current_claims claim ON claim.id = evidence.claim_id
		), current_observations AS (
		  SELECT DISTINCT observation.*
		  FROM research_observation observation JOIN current_evidence evidence ON evidence.observation_id = observation.id
		), current_sources AS (
		  SELECT DISTINCT source.*
		  FROM research_source_snapshot source JOIN current_observations observation ON observation.source_snapshot_id = source.id
		), verified_links AS (
		  SELECT evidence.claim_id, evidence.relation, source.independence_key
		  FROM current_evidence evidence
		  JOIN research_observation observation ON observation.id = evidence.observation_id
		  JOIN research_source_snapshot source ON source.id = observation.source_snapshot_id
		  WHERE evidence.verification_status = 'verified'
		    AND observation.verification_status = 'verified'
		    AND source.verification_status = 'verified'
		), verified_support AS (
		  SELECT DISTINCT claim_id FROM verified_links WHERE relation = 'supports'
		)
		SELECT
		  (SELECT count(*)::int FROM current_questions),
		  (SELECT count(*)::int FROM current_questions WHERE required),
		  (SELECT COALESCE(
		     (count(*) FILTER (WHERE question.answer_claim_id IN (SELECT claim_id FROM verified_support)))::double precision /
		     NULLIF(count(*), 0), 0
		   ) FROM current_questions question WHERE question.required),
		  (SELECT count(*)::int FROM current_questions question
		   JOIN current_claims claim ON claim.id = question.answer_claim_id
		   WHERE question.required AND question.answer_claim_id IN (SELECT claim_id FROM verified_support)
		     AND claim.status IN ('supported', 'refuted', 'superseded')),
		  (SELECT count(*)::int FROM current_sources),
		  (SELECT count(DISTINCT independence_key)::int FROM verified_links WHERE btrim(independence_key) <> ''),
		  (SELECT count(*)::int FROM current_observations),
		  (SELECT count(*)::int FROM current_claims),
		  (SELECT count(*)::int FROM current_claims
		   WHERE btrim(resolution) <> '' AND id IN (SELECT claim_id FROM verified_links)),
		  (SELECT md5(COALESCE(string_agg(
		     id::text || ':' || status || ':' || confidence::text || ':' || resolution,
		     '|' ORDER BY id
		   ), '')) FROM current_claims),
		  (SELECT count(*)::int FROM current_evidence),
		  (SELECT count(*)::int FROM verified_links),
		  (SELECT count(*)::int FROM verified_links WHERE relation = 'contradicts')
	`, sessionID, goalVersion, planVersion).Scan(
		&state.Questions, &state.RequiredQuestions, &state.VerifiedRequiredCoverage,
		&state.VerifiedAnsweredRequiredQuestions, &state.SourceSnapshots, &state.VerifiedIndependentSources,
		&state.Observations, &state.Claims, &state.ResolvedClaims, &state.ClaimAdjudicationHash,
		&state.EvidenceLinks, &state.VerifiedEvidenceLinks, &state.VerifiedContradictions,
	)
	return state, err
}

func measuredInformationGain(before, after researchGraphState, taskKind TaskKind) informationGainBreakdown {
	gain := informationGainBreakdown{
		VerifiedCoverage:   0.35 * positiveFloat(after.VerifiedRequiredCoverage-before.VerifiedRequiredCoverage),
		AnsweredQuestions:  0.20 * normalizedIncrease(before.VerifiedAnsweredRequiredQuestions, after.VerifiedAnsweredRequiredQuestions, after.RequiredQuestions),
		IndependentSources: 0.10 * normalizedIncrease(before.VerifiedIndependentSources, after.VerifiedIndependentSources, after.VerifiedIndependentSources),
		VerifiedEvidence:   0.10 * normalizedIncrease(before.VerifiedEvidenceLinks, after.VerifiedEvidenceLinks, after.VerifiedEvidenceLinks),
		Counterevidence:    0.10 * normalizedIncrease(before.VerifiedContradictions, after.VerifiedContradictions, after.VerifiedContradictions),
		ClaimResolution:    0.10 * normalizedIncrease(before.ResolvedClaims, after.ResolvedClaims, after.Claims),
		SourceNovelty:      0.02 * normalizedIncrease(before.SourceSnapshots, after.SourceSnapshots, after.SourceSnapshots),
		ObservationNovelty: 0.02 * normalizedIncrease(before.Observations, after.Observations, after.Observations),
		ClaimNovelty:       0.01 * normalizedIncrease(before.Claims, after.Claims, after.Claims),
		QuestionNovelty:    0.01 * normalizedIncrease(before.Questions, after.Questions, after.Questions),
	}
	if (taskKind == TaskKindVerify || taskKind == TaskKindCounterSearch) && before.Claims == after.Claims && before.ClaimAdjudicationHash != after.ClaimAdjudicationHash {
		gain.ClaimAdjudication = 0.10
	}
	gain.Score = clampUnit(
		gain.VerifiedCoverage + gain.AnsweredQuestions + gain.IndependentSources + gain.VerifiedEvidence +
			gain.Counterevidence + gain.ClaimResolution + gain.ClaimAdjudication + gain.SourceNovelty + gain.ObservationNovelty +
			gain.ClaimNovelty + gain.QuestionNovelty,
	)
	return gain
}

func normalizedIncrease(before, after, denominator int) float64 {
	delta := after - before
	if delta <= 0 || denominator <= 0 {
		return 0
	}
	return clampUnit(float64(delta) / float64(denominator))
}

func positiveFloat(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return clampUnit(value)
}

func clampUnit(value float64) float64 {
	if value <= 0 {
		return 0
	}
	if value >= 1 {
		return 1
	}
	return value
}

func recordInformationGain(ctx context.Context, tx pgx.Tx, state acceptedResultState, in AcceptResultInput, before, after researchGraphState, gain informationGainBreakdown) error {
	inputs, err := json.Marshal(map[string]any{
		"attempt_id": in.AttemptID, "task_id": state.task.ID, "task_kind": state.task.Kind,
		"before": before, "after": after,
	})
	if err != nil {
		return err
	}
	outcome, err := json.Marshal(map[string]any{
		"gain": gain, "threshold": state.run.Config.MarginalGainThreshold,
		"low_gain": gain.Score < state.run.Config.MarginalGainThreshold, "canonical_changed": before != after,
	})
	if err != nil {
		return err
	}
	var decisionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_decision (
		  workspace_id, session_id, decision_kind, actor_type, goal_version, plan_version,
		  inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'information_gain', 'system', $3, $4, $5::jsonb, $6::jsonb, $7)
		RETURNING id::text
	`, state.workspaceID, state.run.SessionID, state.run.GoalVersion, state.targetPlan, inputs, outcome,
		"Measured from canonical evidence-graph changes before and after the accepted evidence result.").Scan(&decisionID)
	if err != nil {
		return err
	}
	return registerProductionDecisionPassportTx(
		ctx, tx, state.workspaceID, state.run.SessionID, decisionID,
		state.attemptID, state.outputAccess,
	)
}
