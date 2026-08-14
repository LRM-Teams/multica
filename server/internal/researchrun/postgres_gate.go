package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) EvaluateGate(ctx context.Context, sessionID string) (GateResult, error) {
	var run Run
	var configJSON, statsJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT depth_tier, goal_version, plan_version, orchestrator_version, run_config, run_stats
		FROM research_session WHERE id = $1::uuid
	`, sessionID).Scan(&run.DepthTier, &run.GoalVersion, &run.PlanVersion, &run.OrchestratorVersion, &configJSON, &statsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return GateResult{}, ErrRunNotFound
	}
	if err != nil {
		return GateResult{}, err
	}
	if err = ensureSupportedOrchestratorVersion(run.OrchestratorVersion); err != nil {
		return GateResult{}, err
	}
	run.Config = DefaultRunConfig(run.DepthTier)
	if len(configJSON) > 0 {
		if err = json.Unmarshal(configJSON, &run.Config); err != nil {
			return GateResult{}, fmt.Errorf("decode run config: %w", err)
		}
	}
	if len(statsJSON) > 0 {
		if err = json.Unmarshal(statsJSON, &run.Stats); err != nil {
			return GateResult{}, fmt.Errorf("decode run stats: %w", err)
		}
	}
	minimumEvaluationScore := minimumEvaluationScoreForDepth(run.DepthTier)
	minimumMajorClaimSources := 2
	switch run.DepthTier {
	case "shallow":
		minimumMajorClaimSources = 1
	case "deep":
		minimumMajorClaimSources = 3
	}

	type gateCounts struct {
		planSucceeded       int
		unfinishedTasks     int
		blockedTasks        int
		requiredQuestions   int
		unansweredRequired  int
		verifiedSources     int
		independentSources  int
		reportCount         int
		reportFresh         int
		reportClaimCount    int
		reportAuthorCount   int
		unreportedRequired  int
		wrongVersionClaims  int
		unsupportedClaims   int
		weakMajorClaims     int
		unresolvedConflicts int
		unlinkedMajorClaims int
		qualityEvaluations  int
		qualityPassed       int
		qualityIndependent  int
		citationEvaluations int
		citationPassed      int
		citationIndependent int
		budgetExhausted     int
	}
	var counts gateCounts
	var qualityDecisionID, qualityReportID, qualityReviewerID string
	var citationDecisionID, citationReportID, citationReviewerID string
	var qualityOutcome, citationOutcome []byte
	err = s.pool.QueryRow(ctx, `
		WITH current_claims AS (
		  SELECT c.id, c.significance, c.resolution
		  FROM research_claim c
		  WHERE c.session_id = $1::uuid AND c.goal_version = $2 AND c.plan_version = $3
		), latest_report AS (
		  SELECT id, author_agent_id, created_at FROM research_report
		  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		  ORDER BY revision DESC LIMIT 1
		), report_claims AS (
		  SELECT rc.claim_id
		  FROM research_report_claim rc
		  JOIN latest_report r ON r.id = rc.report_id
		  JOIN current_claims c ON c.id = rc.claim_id
		), supported AS (
		  SELECT DISTINCT e.claim_id, ss.id AS source_id, ss.independence_key
		  FROM research_claim_evidence e
		  JOIN current_claims current ON current.id = e.claim_id
		  JOIN research_observation o ON o.id = e.observation_id
		  JOIN research_source_snapshot ss ON ss.id = o.source_snapshot_id
		  WHERE e.session_id = $1::uuid AND e.relation = 'supports'
		    AND e.verification_status = 'verified'
		    AND o.verification_status = 'verified'
		    AND ss.verification_status = 'verified'
		), contradicted AS (
		  SELECT DISTINCT e.claim_id
		  FROM research_claim_evidence e
		  WHERE e.session_id = $1::uuid AND e.relation = 'contradicts'
		    AND e.verification_status = 'verified'
		), latest_quality AS (
		  SELECT id, actor_id, inputs, outcome FROM research_decision
		  WHERE session_id = $1::uuid AND decision_kind = 'quality_gate'
		    AND goal_version = $2 AND plan_version = $3
		    AND inputs->>'report_id' = (SELECT id::text FROM latest_report)
		  ORDER BY created_at DESC LIMIT 1
		), latest_citation AS (
		  SELECT id, actor_id, inputs, outcome FROM research_decision
		  WHERE session_id = $1::uuid AND decision_kind = 'citation_audit'
		    AND goal_version = $2 AND plan_version = $3
		    AND inputs->>'report_id' = (SELECT id::text FROM latest_report)
		  ORDER BY created_at DESC LIMIT 1
		)
		SELECT
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND kind IN ('plan', 'replan') AND status = 'succeeded'),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND status IN ('pending', 'ready', 'dispatching', 'running')),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND status IN ('failed', 'blocked')),
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND required),
		  (SELECT count(*)::int FROM research_question q
		   WHERE q.session_id = $1::uuid AND q.goal_version = $2 AND q.plan_version = $3 AND q.required
		     AND (q.status <> 'answered' OR q.coverage < 0.8 OR q.answer_claim_id IS NULL
		          OR NOT EXISTS (SELECT 1 FROM supported s WHERE s.claim_id = q.answer_claim_id))),
		  (SELECT count(DISTINCT source_id)::int FROM supported),
		  (SELECT count(DISTINCT independence_key)::int FROM supported),
		  (SELECT count(*)::int FROM latest_report),
		  (SELECT count(*)::int FROM latest_report report
		   WHERE NOT EXISTS (
		     SELECT 1 FROM research_decision gain
		     WHERE gain.session_id = $1::uuid AND gain.decision_kind = 'information_gain'
		       AND gain.goal_version = $2 AND gain.plan_version = $3
		       AND COALESCE((gain.outcome->>'canonical_changed')::boolean, false)
		       AND gain.created_at > report.created_at
		   )),
		  (SELECT count(*)::int FROM report_claims),
		  (SELECT count(*)::int FROM latest_report WHERE author_agent_id IS NOT NULL),
		  (SELECT count(*)::int FROM research_question question
		   WHERE question.session_id = $1::uuid AND question.goal_version = $2 AND question.plan_version = $3
		     AND question.required AND question.answer_claim_id IS NOT NULL
		     AND NOT EXISTS (SELECT 1 FROM report_claims report_claim WHERE report_claim.claim_id = question.answer_claim_id)),
		  (SELECT count(*)::int FROM research_report_claim rc JOIN latest_report r ON r.id = rc.report_id WHERE NOT EXISTS (SELECT 1 FROM current_claims c WHERE c.id = rc.claim_id)),
		  (SELECT count(*)::int FROM report_claims rc WHERE NOT EXISTS (SELECT 1 FROM supported s WHERE s.claim_id = rc.claim_id)),
		  (SELECT count(*)::int
		   FROM report_claims rc JOIN current_claims c ON c.id = rc.claim_id
		   WHERE c.significance IN ('high', 'critical')
		     AND (SELECT count(DISTINCT s.independence_key) FROM supported s WHERE s.claim_id = c.id) < $5::int),
		  (SELECT count(*)::int FROM report_claims rc JOIN current_claims c ON c.id = rc.claim_id JOIN contradicted x ON x.claim_id = c.id WHERE btrim(c.resolution) = ''),
		  (SELECT count(*)::int FROM current_claims c WHERE c.significance IN ('high', 'critical') AND EXISTS (SELECT 1 FROM supported s WHERE s.claim_id = c.id) AND NOT EXISTS (SELECT 1 FROM report_claims rc WHERE rc.claim_id = c.id)),
		  (SELECT count(*)::int FROM latest_quality),
		  (SELECT count(*)::int FROM latest_quality
		   WHERE COALESCE((outcome->>'passed')::boolean, false)
		     AND LEAST(
		       COALESCE((outcome->>'factual_grounding')::double precision, 0),
		       COALESCE((outcome->>'coverage')::double precision, 0),
		       COALESCE((outcome->>'analytical_depth')::double precision, 0),
		       COALESCE((outcome->>'source_quality')::double precision, 0),
		       COALESCE((outcome->>'contradiction_handling')::double precision, 0),
		       COALESCE((outcome->>'instruction_adherence')::double precision, 0),
		       COALESCE((outcome->>'readability')::double precision, 0)
		     ) >= $4::double precision),
		  (SELECT count(*)::int FROM latest_quality quality CROSS JOIN latest_report report
		   WHERE report.author_agent_id IS NOT NULL AND quality.actor_id IS DISTINCT FROM report.author_agent_id
		     AND COALESCE((quality.outcome->>'passed')::boolean, false)
		     AND LEAST(
		       COALESCE((quality.outcome->>'factual_grounding')::double precision, 0),
		       COALESCE((quality.outcome->>'coverage')::double precision, 0),
		       COALESCE((quality.outcome->>'analytical_depth')::double precision, 0),
		       COALESCE((quality.outcome->>'source_quality')::double precision, 0),
		       COALESCE((quality.outcome->>'contradiction_handling')::double precision, 0),
		       COALESCE((quality.outcome->>'instruction_adherence')::double precision, 0),
		       COALESCE((quality.outcome->>'readability')::double precision, 0)
		     ) >= $4::double precision),
		  (SELECT count(*)::int FROM latest_citation),
		  (SELECT count(*)::int FROM latest_citation
		   WHERE COALESCE((outcome->>'passed')::boolean, false)
		     AND LEAST(
		       COALESCE((outcome->>'factual_grounding')::double precision, 0),
		       COALESCE((outcome->>'coverage')::double precision, 0),
		       COALESCE((outcome->>'analytical_depth')::double precision, 0),
		       COALESCE((outcome->>'source_quality')::double precision, 0),
		       COALESCE((outcome->>'contradiction_handling')::double precision, 0),
		       COALESCE((outcome->>'instruction_adherence')::double precision, 0),
		       COALESCE((outcome->>'readability')::double precision, 0)
		     ) >= $4::double precision),
		  (SELECT count(*)::int FROM latest_citation citation CROSS JOIN latest_report report
		   WHERE report.author_agent_id IS NOT NULL AND citation.actor_id IS DISTINCT FROM report.author_agent_id
		     AND COALESCE((citation.outcome->>'passed')::boolean, false)
		     AND LEAST(
		       COALESCE((citation.outcome->>'factual_grounding')::double precision, 0),
		       COALESCE((citation.outcome->>'coverage')::double precision, 0),
		       COALESCE((citation.outcome->>'analytical_depth')::double precision, 0),
		       COALESCE((citation.outcome->>'source_quality')::double precision, 0),
		       COALESCE((citation.outcome->>'contradiction_handling')::double precision, 0),
		       COALESCE((citation.outcome->>'instruction_adherence')::double precision, 0),
		       COALESCE((citation.outcome->>'readability')::double precision, 0)
		     ) >= $4::double precision),
		  (SELECT count(*)::int FROM research_decision WHERE session_id = $1::uuid
		     AND decision_kind = 'budget_exhausted' AND goal_version = $2 AND plan_version = $3),
		  COALESCE((SELECT id::text FROM latest_quality), ''),
		  COALESCE((SELECT inputs->>'report_id' FROM latest_quality), ''),
		  COALESCE((SELECT actor_id::text FROM latest_quality), ''),
		  COALESCE((SELECT outcome FROM latest_quality), '{}'::jsonb),
		  COALESCE((SELECT id::text FROM latest_citation), ''),
		  COALESCE((SELECT inputs->>'report_id' FROM latest_citation), ''),
		  COALESCE((SELECT actor_id::text FROM latest_citation), ''),
		  COALESCE((SELECT outcome FROM latest_citation), '{}'::jsonb)
	`, sessionID, run.GoalVersion, run.PlanVersion, minimumEvaluationScore, minimumMajorClaimSources).Scan(
		&counts.planSucceeded, &counts.unfinishedTasks, &counts.blockedTasks,
		&counts.requiredQuestions, &counts.unansweredRequired,
		&counts.verifiedSources, &counts.independentSources,
		&counts.reportCount, &counts.reportFresh, &counts.reportClaimCount, &counts.reportAuthorCount, &counts.unreportedRequired,
		&counts.wrongVersionClaims, &counts.unsupportedClaims, &counts.weakMajorClaims,
		&counts.unresolvedConflicts, &counts.unlinkedMajorClaims,
		&counts.qualityEvaluations, &counts.qualityPassed, &counts.qualityIndependent,
		&counts.citationEvaluations, &counts.citationPassed, &counts.citationIndependent,
		&counts.budgetExhausted,
		&qualityDecisionID, &qualityReportID, &qualityReviewerID, &qualityOutcome,
		&citationDecisionID, &citationReportID, &citationReviewerID, &citationOutcome,
	)
	if err != nil {
		return GateResult{}, err
	}
	methodCount := 0
	if usesResearchMethodContract(run.OrchestratorVersion) {
		if err = s.pool.QueryRow(ctx, `
			SELECT count(*)::int
			FROM research_decision
			WHERE session_id = $1::uuid AND decision_kind = 'research_method'
			  AND goal_version = $2 AND plan_version = $3
		`, sessionID, run.GoalVersion, run.PlanVersion).Scan(&methodCount); err != nil {
			return GateResult{}, err
		}
	}
	var unansweredQuestion map[string]any
	if counts.unansweredRequired > 0 {
		var found bool
		unansweredQuestion, found, err = s.loadTopUnansweredRequiredQuestion(ctx, sessionID, run.GoalVersion, run.PlanVersion)
		if err != nil {
			return GateResult{}, err
		}
		if !found {
			// Result acceptance can finish a question between the aggregate gate
			// read and this targeted read. Do not create stale unbound work.
			counts.unansweredRequired = 0
		}
	}
	var reportStructureErr error
	if usesStructuredResultContract(run.OrchestratorVersion) && counts.reportCount > 0 {
		var report ReportProposal
		report, reportStructureErr = s.loadLatestReportForGate(ctx, sessionID, run.GoalVersion, run.PlanVersion)
		if reportStructureErr == nil {
			_, reportStructureErr = validateStructuredReportV2(report, reportPolicyForDepth(run.DepthTier))
		}
	}

	minimumIndependentSources := 3
	switch run.DepthTier {
	case "shallow":
		minimumIndependentSources = 2
	case "deep":
		minimumIndependentSources = 5
	}
	findings := make([]GateFinding, 0, 12)
	add := func(code, message string, metadata map[string]any) {
		findings = append(findings, GateFinding{Code: code, Severity: "blocker", Message: message, Metadata: metadata})
	}
	if counts.planSucceeded == 0 {
		add("plan_incomplete", "The current plan has not been accepted.", nil)
	}
	if usesResearchMethodContract(run.OrchestratorVersion) && methodCount == 0 {
		add("research_method_missing", "The current plan has no accepted research method.", nil)
	}
	if counts.unfinishedTasks > 0 {
		add("tasks_incomplete", "Current-plan research tasks are still active.", map[string]any{"count": counts.unfinishedTasks})
	}
	if counts.blockedTasks > 0 {
		add("tasks_blocked", "Current-plan tasks are blocked or failed.", map[string]any{"count": counts.blockedTasks})
	}
	if counts.requiredQuestions == 0 {
		add("required_questions_missing", "The plan contains no required research questions.", nil)
	} else if counts.unansweredRequired > 0 {
		unansweredQuestion["count"] = counts.unansweredRequired
		add("required_questions_unanswered", "Required questions lack an accepted answer and sufficient coverage.", unansweredQuestion)
	}
	if !usesEvidenceFitnessContract(run.OrchestratorVersion) && counts.independentSources < minimumIndependentSources {
		add("independent_sources_insufficient", "Verified evidence does not span enough independent sources.", map[string]any{"actual": counts.independentSources, "required": minimumIndependentSources})
	}
	if counts.reportCount == 0 {
		add("report_missing", "No report revision exists for delivery.", nil)
	} else {
		if counts.reportFresh == 0 {
			add("report_stale_after_evidence", "The latest report predates accepted evidence work in the current plan.", nil)
		}
		if reportStructureErr != nil {
			add("report_structure_incomplete", "The latest report does not satisfy the durable structure and prose-coverage contract.", map[string]any{"reason": reportStructureErr.Error()})
		}
		if usesStructuredResultContract(run.OrchestratorVersion) && counts.reportAuthorCount == 0 {
			add("report_author_missing", "The latest report has no durable author attribution.", nil)
		}
		if usesStructuredResultContract(run.OrchestratorVersion) && counts.unreportedRequired > 0 {
			add("required_answers_unreported", "Required question answer claims are absent from the latest report.", map[string]any{"count": counts.unreportedRequired})
		}
		if counts.reportClaimCount == 0 {
			add("report_claims_missing", "The latest report has no normalized claim links.", nil)
		}
		if counts.wrongVersionClaims > 0 {
			add("report_claims_stale", "The latest report cites claims from an earlier goal or plan version.", map[string]any{"count": counts.wrongVersionClaims})
		}
		if !usesEvidenceFitnessContract(run.OrchestratorVersion) && counts.unsupportedClaims > 0 {
			add("report_claims_unsupported", "Report claims lack verified supporting evidence.", map[string]any{"count": counts.unsupportedClaims})
		}
		if !usesEvidenceFitnessContract(run.OrchestratorVersion) && counts.weakMajorClaims > 0 {
			add("major_claim_sources_insufficient", "High-significance report claims lack enough independent verified source families.", map[string]any{"count": counts.weakMajorClaims, "required_per_claim": minimumMajorClaimSources})
		}
		if counts.unresolvedConflicts > 0 {
			add("report_conflicts_unresolved", "Report claims have verified contradictions without a recorded resolution.", map[string]any{"count": counts.unresolvedConflicts})
		}
		if counts.unlinkedMajorClaims > 0 {
			add("major_claims_unlinked", "Supported high-significance claims are absent from the latest report.", map[string]any{"count": counts.unlinkedMajorClaims})
		}
	}
	if usesEvidenceFitnessContract(run.OrchestratorVersion) && methodCount > 0 {
		fitnessFindings, fitnessErr := s.evaluateEvidenceFitnessV4(ctx, sessionID, run.GoalVersion, run.PlanVersion)
		if fitnessErr != nil {
			return GateResult{}, fitnessErr
		}
		findings = append(findings, fitnessFindings...)
	}
	if counts.qualityEvaluations == 0 {
		add("quality_evaluation_missing", "The latest report has not passed an independent quality evaluation.", nil)
	} else if counts.qualityPassed == 0 {
		add("quality_evaluation_failed", "The latest independent quality evaluation failed or fell below the depth-tier score floor.",
			evaluationFeedbackMetadata(qualityDecisionID, qualityReportID, qualityReviewerID, qualityOutcome, minimumEvaluationScore))
	} else if usesStructuredResultContract(run.OrchestratorVersion) && counts.qualityIndependent == 0 {
		add("quality_evaluation_not_independent", "The latest quality evaluation was submitted by the report author.", nil)
	}
	if counts.citationEvaluations == 0 {
		add("citation_audit_missing", "The latest report has not passed a citation audit.", nil)
	} else if counts.citationPassed == 0 {
		add("citation_audit_failed", "The latest citation audit failed or fell below the depth-tier score floor.",
			evaluationFeedbackMetadata(citationDecisionID, citationReportID, citationReviewerID, citationOutcome, minimumEvaluationScore))
	} else if usesStructuredResultContract(run.OrchestratorVersion) && counts.citationIndependent == 0 {
		add("citation_audit_not_independent", "The latest citation audit was submitted by the report author.", nil)
	}
	if counts.budgetExhausted == 0 && run.Stats.LowGainStreak < run.Config.MarginalGainRounds {
		add("marginal_gain_not_saturated", "Recent evidence batches have not yet demonstrated diminishing information gain.", map[string]any{
			"actual_low_gain_streak":   run.Stats.LowGainStreak,
			"required_low_gain_streak": run.Config.MarginalGainRounds,
			"threshold":                run.Config.MarginalGainThreshold,
			"last_measured_gain":       run.Stats.LastMeasuredGain,
		})
	}
	return GateResult{Passed: len(findings) == 0, Findings: findings}, nil
}

func evaluationFeedbackMetadata(decisionID, reportID, reviewerID string, raw []byte, minimumScore float64) map[string]any {
	metadata := map[string]any{
		"evaluation_decision_id": decisionID,
		"report_id":              reportID,
		"reviewer_agent_id":      reviewerID,
		"minimum_score":          minimumScore,
	}
	var evaluation EvaluationProposal
	if err := json.Unmarshal(raw, &evaluation); err != nil {
		metadata["feedback_unavailable"] = "stored evaluation outcome could not be decoded"
		return metadata
	}
	scores := map[string]float64{
		"factual_grounding":      evaluation.FactualGrounding,
		"coverage":               evaluation.Coverage,
		"analytical_depth":       evaluation.AnalyticalDepth,
		"source_quality":         evaluation.SourceQuality,
		"contradiction_handling": evaluation.ContradictionHandling,
		"instruction_adherence":  evaluation.InstructionAdherence,
		"readability":            evaluation.Readability,
	}
	failedDimensions := make([]map[string]any, 0, len(evaluationDimensionNames))
	for _, dimension := range evaluationDimensionNames {
		score := scores[dimension]
		if score >= minimumScore {
			continue
		}
		failedDimensions = append(failedDimensions, map[string]any{
			"dimension": dimension,
			"score":     score,
			"rationale": truncateBytes(evaluation.DimensionFindings[dimension], 1024),
		})
	}
	metadata["passed"] = evaluation.Passed
	metadata["failed_dimensions"] = failedDimensions
	metadata["findings"] = boundedEvaluationStrings(evaluation.Findings, 8, 1024)
	metadata["reviewed_claim_keys"] = boundedEvaluationStrings(evaluation.ReviewedClaimKeys, 64, 160)
	metadata["reviewed_section_ids"] = boundedEvaluationStrings(evaluation.ReviewedSectionIDs, 64, 160)
	metadata["defects"] = boundedEvaluationDefects(evaluation.Defects)
	return metadata
}

func minimumEvaluationScoreForDepth(depthTier string) float64 {
	switch depthTier {
	case "shallow":
		return 0.65
	case "deep":
		return 0.8
	default:
		return 0.75
	}
}

func boundedEvaluationDefects(values []EvaluationDefect) []EvaluationDefect {
	if len(values) > maxEvaluationDefects {
		values = values[:maxEvaluationDefects]
	}
	bounded := make([]EvaluationDefect, 0, len(values))
	for _, value := range values {
		value.ClientKey = truncateBytes(value.ClientKey, 160)
		value.Problem = truncateBytes(value.Problem, maxEvaluationDefectTextBytes)
		value.RequiredChange = truncateBytes(value.RequiredChange, maxEvaluationDefectTextBytes)
		value.ClaimKeys = boundedEvaluationStrings(value.ClaimKeys, maxEvaluationDefectTargets, 160)
		value.SectionIDs = boundedEvaluationStrings(value.SectionIDs, maxEvaluationDefectTargets, 160)
		bounded = append(bounded, value)
	}
	return bounded
}

func boundedEvaluationStrings(values []string, maxItems, maxBytes int) []string {
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	bounded := make([]string, 0, len(values))
	for _, value := range values {
		bounded = append(bounded, truncateBytes(value, maxBytes))
	}
	return bounded
}

func (s *PostgresStore) loadTopUnansweredRequiredQuestion(ctx context.Context, sessionID string, goalVersion, planVersion int) (map[string]any, bool, error) {
	var id, clientKey, question, status, answerClaimID, answerClaimKey string
	var priority, impact, uncertainty, novelty, coverage, frontierScore float64
	var hasVerifiedSupport bool
	err := s.pool.QueryRow(ctx, `
		WITH supported AS (
		  SELECT DISTINCT evidence.claim_id
		  FROM research_claim_evidence evidence
		  JOIN research_observation observation ON observation.id = evidence.observation_id
		  JOIN research_source_snapshot source ON source.id = observation.source_snapshot_id
		  WHERE evidence.session_id = $1::uuid AND evidence.relation = 'supports'
		    AND evidence.verification_status = 'verified'
		    AND observation.verification_status = 'verified'
		    AND source.verification_status = 'verified'
		)
		SELECT q.id::text, q.client_key, q.question, q.status,
		       q.priority, q.impact, q.uncertainty, q.novelty, q.coverage,
		       COALESCE(q.answer_claim_id::text, ''), COALESCE(claim.client_key, ''),
		       COALESCE(q.answer_claim_id IN (SELECT claim_id FROM supported), false),
		       LEAST(1, 0.30*q.priority + 0.30*q.impact + 0.20*q.uncertainty +
		                0.10*q.novelty + 0.10*(1-q.coverage) +
		                CASE q.kind WHEN 'contradiction' THEN 0.10 WHEN 'gap' THEN 0.05 ELSE 0 END) AS frontier_score
		FROM research_question q
		LEFT JOIN research_claim claim ON claim.id = q.answer_claim_id
		WHERE q.session_id = $1::uuid AND q.goal_version = $2 AND q.plan_version = $3 AND q.required
		  AND (q.status <> 'answered' OR q.coverage < 0.8 OR q.answer_claim_id IS NULL
		       OR NOT EXISTS (SELECT 1 FROM supported WHERE supported.claim_id = q.answer_claim_id))
		ORDER BY frontier_score DESC, q.priority DESC, q.created_at, q.id
		LIMIT 1
	`, sessionID, goalVersion, planVersion).Scan(
		&id, &clientKey, &question, &status, &priority, &impact, &uncertainty, &novelty, &coverage,
		&answerClaimID, &answerClaimKey, &hasVerifiedSupport, &frontierScore,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	reasons := make([]string, 0, 3)
	if answerClaimID == "" {
		reasons = append(reasons, "answer_claim_missing")
	}
	if coverage < 0.8 {
		reasons = append(reasons, "coverage_below_threshold")
	}
	if answerClaimID != "" && !hasVerifiedSupport {
		reasons = append(reasons, "verified_support_missing")
	}
	return map[string]any{
		"question_id": id, "question_key": clientKey, "question": question, "status": status,
		"priority": priority, "impact": impact, "uncertainty": uncertainty, "novelty": novelty, "coverage": coverage,
		"frontier_score": frontierScore, "incomplete_reasons": reasons,
		"answer_claim_id": answerClaimID, "answer_claim_key": answerClaimKey, "has_verified_support": hasVerifiedSupport,
	}, true, nil
}

func (s *PostgresStore) loadLatestReportForGate(ctx context.Context, sessionID string, goalVersion, planVersion int) (ReportProposal, error) {
	var report ReportProposal
	var reportID string
	if err := s.pool.QueryRow(ctx, `
		SELECT id::text, content_md, structured
		FROM research_report
		WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		ORDER BY revision DESC LIMIT 1
	`, sessionID, goalVersion, planVersion).Scan(&reportID, &report.ContentMD, &report.Structured); err != nil {
		return ReportProposal{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT claim.client_key, link.section_id, link.anchor_quote
		FROM research_report_claim link
		JOIN research_claim claim ON claim.id = link.claim_id
		WHERE link.report_id = $1::uuid
		ORDER BY claim.client_key, link.section_id
	`, reportID)
	if err != nil {
		return ReportProposal{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var link ReportClaimProposal
		if err = rows.Scan(&link.ClaimKey, &link.SectionID, &link.AnchorQuote); err != nil {
			return ReportProposal{}, err
		}
		report.Claims = append(report.Claims, link)
	}
	return report, rows.Err()
}

func (s *PostgresStore) RecordBudgetExhausted(ctx context.Context, sessionID, budgetKind, details string) (RunEvent, error) {
	tx, err := s.beginResearchTx(ctx, txOpBudgetExhausted, pgx.TxOptions{})
	if err != nil {
		return RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return RunEvent{}, err
	}
	var workspaceID string
	var goalVersion, planVersion int
	if err = tx.QueryRow(ctx, `
		SELECT workspace_id::text, goal_version, plan_version
		FROM research_session WHERE id = $1::uuid FOR UPDATE
	`, sessionID).Scan(&workspaceID, &goalVersion, &planVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunEvent{}, ErrRunNotFound
		}
		return RunEvent{}, err
	}
	key := fmt.Sprintf("budget-exhausted:%s:%d:%d", budgetKind, goalVersion, planVersion)
	var existing RunEvent
	err = tx.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, sequence,
		       event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		       payload, projection_attempts, created_at
		FROM research_run_event
		WHERE session_id = $1::uuid AND idempotency_key = $2
	`, sessionID, key).Scan(
		&existing.ID, &existing.WorkspaceID, &existing.SessionID, &existing.Sequence,
		&existing.Type, &existing.IdempotencyKey, &existing.ActorType, &existing.ActorID,
		&existing.Payload, &existing.ProjectionAttempts, &existing.CreatedAt,
	)
	if err == nil {
		return existing, s.commitResearchTx(ctx, txOpBudgetExhausted, tx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, err
	}
	inputs, _ := json.Marshal(map[string]any{"budget_kind": budgetKind, "details": details})
	var decisionID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_decision (
			workspace_id, session_id, decision_kind, actor_type,
			goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'budget_exhausted', 'system', $3, $4, $5, '{}', $6)
		RETURNING id::text
	`, workspaceID, sessionID, goalVersion, planVersion, inputs, truncateBytes(details, 4096)).Scan(&decisionID); err != nil {
		return RunEvent{}, err
	}
	if err = registerProductionDecisionPassportTx(ctx, tx, workspaceID, sessionID, decisionID, "", ArtifactAccessRaw); err != nil {
		return RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_session
		SET run_stats = run_stats || jsonb_build_object(
		      'budget_exhaustion_count', COALESCE((run_stats->>'budget_exhaustion_count')::int, 0) + 1
		    ),
		    stop_reason = 'budget_exhausted', updated_at = now()
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		return RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "budget_exhausted", key, "system", "", map[string]any{
		"budget_kind": budgetKind,
		"details":     truncateBytes(details, 4096),
	})
	if err != nil {
		return RunEvent{}, err
	}
	if err = s.commitResearchTx(ctx, txOpBudgetExhausted, tx); err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

func (s *PostgresStore) ListUnprojectedEvents(ctx context.Context, sessionID string, limit int) ([]RunEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, sequence,
		       event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		       payload, projection_attempts, created_at
		FROM research_run_event
		WHERE session_id = $1::uuid AND projected_at IS NULL AND next_projection_at <= now()
		  AND NOT EXISTS (
		    SELECT 1 FROM research_run_event earlier
		    WHERE earlier.session_id = research_run_event.session_id
		      AND earlier.sequence < research_run_event.sequence
		      AND earlier.projected_at IS NULL
		  )
		ORDER BY sequence LIMIT $2
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []RunEvent{}
	for rows.Next() {
		var event RunEvent
		if err = rows.Scan(&event.ID, &event.WorkspaceID, &event.SessionID,
			&event.Sequence, &event.Type, &event.IdempotencyKey, &event.ActorType,
			&event.ActorID, &event.Payload, &event.ProjectionAttempts, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) MarkEventProjected(ctx context.Context, eventID string) error {
	tx, err := s.beginResearchTx(ctx, txOpProjectionAcknowledge, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err = tx.QueryRow(ctx, `SELECT session_id::text FROM research_run_event WHERE id = $1::uuid`, eventID).Scan(&sessionID); err != nil {
		return err
	}
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE research_run_event
		SET projected_at = now(), projection_attempts = projection_attempts + 1,
		    projection_error = ''
		WHERE id = $1::uuid
	`, eventID)
	if err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpProjectionAcknowledge, tx)
}

func (s *PostgresStore) MarkEventProjectionFailed(ctx context.Context, eventID, message string, next time.Time) error {
	tx, err := s.beginResearchTx(ctx, txOpProjectionRetry, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err = tx.QueryRow(ctx, `SELECT session_id::text FROM research_run_event WHERE id = $1::uuid`, eventID).Scan(&sessionID); err != nil {
		return err
	}
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE research_run_event
		SET projection_attempts = projection_attempts + 1,
		    projection_error = $2, next_projection_at = $3
		WHERE id = $1::uuid AND projected_at IS NULL
	`, eventID, truncateBytes(message, 4096), next)
	if err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpProjectionRetry, tx)
}

type activeAttempt struct {
	id, inboxID, dispatchKey string
}

func (s *PostgresStore) ReconcileAttempts(ctx context.Context, sessionID string, states map[string]InboxTaskState) ([]RunEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text, COALESCE(a.inbox_task_id::text, ''), a.dispatch_key
		FROM research_task_attempt a
		LEFT JOIN research_dispatch_outbox outbox ON outbox.attempt_id = a.id
		WHERE a.session_id = $1::uuid AND a.status IN ('dispatching', 'running')
		  AND (outbox.id IS NULL OR outbox.status NOT IN ('pending', 'delivering'))
		ORDER BY a.dispatched_at
	`, sessionID)
	if err != nil {
		return nil, err
	}
	active := []activeAttempt{}
	for rows.Next() {
		var item activeAttempt
		if err = rows.Scan(&item.id, &item.inboxID, &item.dispatchKey); err != nil {
			rows.Close()
			return nil, err
		}
		active = append(active, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	events := []RunEvent{}
	for _, attempt := range active {
		key := attempt.inboxID
		if key == "" {
			key = attempt.dispatchKey
		}
		state, found := states[key]
		if !found {
			state, found = states[attempt.dispatchKey]
		}
		reconciled, reconcileErr := s.reconcileAttemptRuntime(ctx, attempt, state, found)
		if reconcileErr != nil {
			if errors.Is(reconcileErr, ErrInvalidTransition) {
				continue
			}
			return events, reconcileErr
		}
		events = append(events, reconciled...)
	}
	return events, nil
}

func (s *PostgresStore) reconcileAttemptRuntime(ctx context.Context, observed activeAttempt, state InboxTaskState, found bool) ([]RunEvent, error) {
	tx, err := s.beginResearchTx(ctx, txOpAttemptRuntimeReconcile, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var sessionID, workspaceID, taskID, inboxID string
	var status AttemptStatus
	var dispatchedAt, databaseNow time.Time
	var runtimeStartedAt *time.Time
	var timeoutSeconds, staleAfterSeconds int
	err = tx.QueryRow(ctx, `
		SELECT session_id::text, task_id::text
		FROM research_task_attempt
		WHERE id = $1::uuid
	`, observed.id).Scan(&sessionID, &taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM research_task WHERE id = $1::uuid FOR UPDATE`, taskID); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `
		SELECT attempt.workspace_id::text, COALESCE(attempt.inbox_task_id::text, ''),
		       attempt.status, attempt.dispatched_at, attempt.runtime_started_at,
		       task.timeout_seconds,
		       COALESCE((session.run_config->>'stale_after_seconds')::int, 900), now()
		FROM research_task_attempt attempt
		JOIN research_task task ON task.id = attempt.task_id
		JOIN research_session session ON session.id = attempt.session_id
		WHERE attempt.id = $1::uuid
		FOR UPDATE OF attempt
	`, observed.id).Scan(&workspaceID, &inboxID, &status, &dispatchedAt,
		&runtimeStartedAt, &timeoutSeconds, &staleAfterSeconds, &databaseNow)
	if err != nil {
		return nil, err
	}
	if status != AttemptStatusDispatching && status != AttemptStatusRunning {
		return nil, ErrInvalidTransition
	}
	events := []RunEvent{}
	if found {
		if state.ID == "" {
			return nil, fmt.Errorf("%w: runtime state is missing inbox identity", ErrInvalidTransition)
		}
		if inboxID != "" && inboxID != state.ID {
			return nil, fmt.Errorf("%w: runtime identity changed for attempt", ErrResultConflict)
		}
		if state.StartedAt != nil && state.StartedAt.Before(dispatchedAt) {
			return nil, fmt.Errorf("%w: runtime start predates dispatch", ErrInvalidTransition)
		}
		observedAt := state.ObservedAt
		if observedAt.IsZero() {
			observedAt = databaseNow
		}
		if runtimeStartedAt == nil && state.StartedAt != nil {
			runtimeStartedAt = state.StartedAt
		}
		if _, err = tx.Exec(ctx, `
			UPDATE research_task_attempt
			SET inbox_task_id = COALESCE(inbox_task_id, $2::uuid),
			    runtime_started_at = COALESCE(runtime_started_at, $3),
			    runtime_lease_expires_at = CASE
			      WHEN runtime_last_observed_at IS NULL OR runtime_last_observed_at <= $4
			        THEN COALESCE($5, runtime_lease_expires_at)
			      ELSE runtime_lease_expires_at
			    END,
			    runtime_last_observed_at = GREATEST(COALESCE(runtime_last_observed_at, $4), $4),
			    updated_at = now()
			WHERE id = $1::uuid
		`, observed.id, state.ID, state.StartedAt, observedAt, state.LeaseExpiresAt); err != nil {
			return nil, err
		}
		if inboxID == "" {
			inboxID = state.ID
			event, eventErr := appendEvent(ctx, tx, workspaceID, sessionID, "task_dispatched", "task-dispatched:"+observed.id, "system", "", map[string]any{
				"task_id": taskID, "attempt_id": observed.id, "inbox_task_id": state.ID,
			})
			if eventErr != nil {
				return nil, eventErr
			}
			events = append(events, event)
		}
		if status == AttemptStatusDispatching && state.StartedAt != nil {
			if _, err = tx.Exec(ctx, `
				UPDATE research_task_attempt
				SET status = 'running', started_at = COALESCE(started_at, $2), updated_at = now()
				WHERE id = $1::uuid AND status = 'dispatching'
			`, observed.id, state.StartedAt); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `
				UPDATE research_task
				SET status = 'running', started_at = COALESCE(started_at, $2), updated_at = now()
				WHERE id = $1::uuid AND status = 'dispatching'
			`, taskID, state.StartedAt); err != nil {
				return nil, err
			}
			status = AttemptStatusRunning
			event, eventErr := appendEvent(ctx, tx, workspaceID, sessionID, "task_started", "task-started:"+observed.id, "system", "", map[string]any{
				"task_id": taskID, "attempt_id": observed.id, "inbox_task_id": inboxID,
			})
			if eventErr != nil {
				return nil, eventErr
			}
			events = append(events, event)
		}
	}

	failure := AttemptFailure{AttemptID: observed.id, Retryable: true}
	switch {
	case found && !state.HasActiveLease && (state.Status == "failed" || state.Status == "cancelled"):
		disposition := ClassifyInboxFailure(state.FailureReason, state.Retryable)
		failure.FailureClass = string(disposition.Class)
		failure.SourceReason = state.FailureReason
		failure.Diagnostics = state.FailureReason
		failure.Retryable = disposition.Retryable
	case found && !state.HasActiveLease && state.Status == "completed":
		failure.FailureClass = string(FailureResultInvalid)
		failure.Diagnostics = "The agent task completed without submitting the structured research result."
	case runtimeStartedAt != nil && timeoutSeconds > 0 && !databaseNow.Before(runtimeStartedAt.Add(time.Duration(timeoutSeconds)*time.Second)):
		diagnostics := "The agent task exceeded its configured execution timeout without a valid result."
		if _, err = tx.Exec(ctx, `
			UPDATE research_task_attempt
			SET status = 'cancelling', pending_failure_class = $3,
			    pending_failure_diagnostics = $2, pending_failure_retryable = true,
			    updated_at = now()
			WHERE id = $1::uuid AND status IN ('dispatching', 'running')
		`, observed.id, diagnostics, string(FailureTimeout)); err != nil {
			return nil, err
		}
		event, eventErr := appendEvent(ctx, tx, workspaceID, sessionID, "task_attempt_cancelling", "attempt-cancelling:"+observed.id, "system", "", map[string]any{
			"task_id": taskID, "attempt_id": observed.id, "failure_class": FailureTimeout, "diagnostics": diagnostics,
		})
		if eventErr != nil {
			return nil, eventErr
		}
		events = append(events, event)
		if err = s.commitResearchTx(ctx, txOpAttemptRuntimeReconcile, tx); err != nil {
			return nil, err
		}
		return events, nil
	case !found && !databaseNow.Before(dispatchedAt.Add(time.Duration(staleAfterSeconds)*time.Second)):
		failure.FailureClass = string(FailureRuntimeLost)
		failure.Diagnostics = "No durable inbox task could be found for the research attempt."
	default:
		if err = s.commitResearchTx(ctx, txOpAttemptRuntimeReconcile, tx); err != nil {
			return nil, err
		}
		return events, nil
	}
	event, failErr := failAttemptTx(ctx, tx, failure)
	if failErr != nil {
		return nil, failErr
	}
	events = append(events, event)
	if err = s.commitResearchTx(ctx, txOpAttemptRuntimeReconcile, tx); err != nil {
		return nil, err
	}
	return events, nil
}
