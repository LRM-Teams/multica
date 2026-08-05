package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type evidenceFitnessTarget struct {
	ID                  string
	ClientKey           string
	EvidenceStandardKey string
}

type evidenceFitnessLink struct {
	SourceID        string
	IndependenceKey string
	EvidenceTraits  []string
	Strength        float64
	Directness      float64
	MethodFit       float64
}

func (s *PostgresStore) evaluateEvidenceFitnessV4(ctx context.Context, sessionID string, goalVersion, planVersion int) ([]GateFinding, error) {
	method, err := s.loadResearchMethodForGate(ctx, sessionID, goalVersion, planVersion)
	if err != nil {
		return nil, err
	}
	standards, err := validateEvidenceStandards(method.EvidenceStandards)
	if err != nil {
		return nil, fmt.Errorf("stored research method has invalid evidence standards: %w", err)
	}
	targets, err := s.loadEvidenceFitnessTargets(ctx, sessionID, goalVersion, planVersion)
	if err != nil {
		return nil, err
	}
	links, err := s.loadEvidenceFitnessLinks(ctx, sessionID, targets)
	if err != nil {
		return nil, err
	}
	counterSearches, err := s.loadCounterSearchCoverage(ctx, sessionID, goalVersion, planVersion, targets)
	if err != nil {
		return nil, err
	}

	findings := make([]GateFinding, 0)
	for _, target := range targets {
		standard, ok := standards[target.EvidenceStandardKey]
		if !ok {
			findings = append(findings, GateFinding{
				Code:     "claim_evidence_standard_missing",
				Severity: "blocker",
				Message:  "A required or reported Claim does not reference an accepted evidence standard.",
				Metadata: map[string]any{"claim_key": target.ClientKey, "evidence_standard_key": target.EvidenceStandardKey},
			})
			continue
		}
		eligibleSources := map[string]struct{}{}
		independentSources := map[string]struct{}{}
		coveredTraits := map[string]struct{}{}
		verifiedSupports := links[target.ID]
		for _, link := range verifiedSupports {
			if link.Strength < standard.MinimumStrength || link.Directness < standard.MinimumDirectness || link.MethodFit < standard.MinimumMethodFit {
				continue
			}
			eligibleSources[link.SourceID] = struct{}{}
			if key := strings.TrimSpace(link.IndependenceKey); key != "" {
				independentSources[key] = struct{}{}
			}
			for _, trait := range link.EvidenceTraits {
				coveredTraits[strings.TrimSpace(trait)] = struct{}{}
			}
		}
		missingTraits := make([]string, 0)
		for _, trait := range standard.RequiredSourceTraits {
			if _, ok := coveredTraits[trait]; !ok {
				missingTraits = append(missingTraits, trait)
			}
		}
		sort.Strings(missingTraits)
		evidenceFit := len(independentSources) >= standard.MinimumIndependentSources && len(missingTraits) == 0
		if standard.CounterevidenceRequired && !counterSearches[target.ID] {
			findings = append(findings, GateFinding{
				Code:     "claim_counterevidence_search_missing",
				Severity: "blocker",
				Message:  "A Claim's accepted evidence standard requires a targeted counterevidence search, but none succeeded for this Claim.",
				Metadata: map[string]any{"claim_key": target.ClientKey, "evidence_standard_key": standard.ClientKey},
			})
		}
		if evidenceFit {
			continue
		}
		findings = append(findings, GateFinding{
			Code:     "claim_evidence_standard_unmet",
			Severity: "blocker",
			Message:  "A required or reported Claim does not satisfy its accepted evidence standard.",
			Metadata: map[string]any{
				"claim_key":                    target.ClientKey,
				"evidence_standard_key":        standard.ClientKey,
				"standard_purpose":             standard.Purpose,
				"verified_support_links":       len(verifiedSupports),
				"eligible_sources":             len(eligibleSources),
				"actual_independent_sources":   len(independentSources),
				"required_independent_sources": standard.MinimumIndependentSources,
				"missing_source_traits":        missingTraits,
				"minimum_strength":             standard.MinimumStrength,
				"minimum_directness":           standard.MinimumDirectness,
				"minimum_method_fit":           standard.MinimumMethodFit,
			},
		})
	}

	return findings, nil
}

func (s *PostgresStore) loadResearchMethodForGate(ctx context.Context, sessionID string, goalVersion, planVersion int) (ResearchMethod, error) {
	var outcome []byte
	err := s.pool.QueryRow(ctx, `
		SELECT outcome
		FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'research_method'
		  AND goal_version = $2 AND plan_version = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, goalVersion, planVersion).Scan(&outcome)
	if err != nil {
		return ResearchMethod{}, err
	}
	var method ResearchMethod
	if err = json.Unmarshal(outcome, &method); err != nil {
		return ResearchMethod{}, fmt.Errorf("decode research method for evidence gate: %w", err)
	}
	return method, nil
}

func (s *PostgresStore) loadEvidenceFitnessTargets(ctx context.Context, sessionID string, goalVersion, planVersion int) ([]evidenceFitnessTarget, error) {
	rows, err := s.pool.Query(ctx, `
		WITH latest_report AS (
		  SELECT id
		  FROM research_report
		  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		  ORDER BY revision DESC
		  LIMIT 1
		), target_claim_ids AS (
		  SELECT answer_claim_id AS claim_id
		  FROM research_question
		  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		    AND required AND answer_claim_id IS NOT NULL
		  UNION
		  SELECT report_claim.claim_id
		  FROM research_report_claim report_claim
		  JOIN latest_report report ON report.id = report_claim.report_id
		)
		SELECT claim.id::text, claim.client_key, claim.evidence_standard_key
		FROM target_claim_ids target
		JOIN research_claim claim ON claim.id = target.claim_id
		WHERE claim.goal_version = $2 AND claim.plan_version = $3
		ORDER BY claim.client_key, claim.id
	`, sessionID, goalVersion, planVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]evidenceFitnessTarget, 0)
	for rows.Next() {
		var target evidenceFitnessTarget
		if err = rows.Scan(&target.ID, &target.ClientKey, &target.EvidenceStandardKey); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *PostgresStore) loadEvidenceFitnessLinks(ctx context.Context, sessionID string, targets []evidenceFitnessTarget) (map[string][]evidenceFitnessLink, error) {
	links := make(map[string][]evidenceFitnessLink, len(targets))
	if len(targets) == 0 {
		return links, nil
	}
	claimIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		claimIDs = append(claimIDs, target.ID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT evidence.claim_id::text, source.id::text, source.independence_key,
		       source.evidence_traits, evidence.strength, evidence.directness, evidence.method_fit
		FROM research_claim_evidence evidence
		JOIN research_observation observation ON observation.id = evidence.observation_id
		JOIN research_source_snapshot source ON source.id = observation.source_snapshot_id
		WHERE evidence.session_id = $1::uuid
		  AND evidence.claim_id::text = ANY($2::text[])
		  AND evidence.relation = 'supports'
		  AND evidence.verification_status = 'verified'
		  AND observation.verification_status = 'verified'
		  AND source.verification_status = 'verified'
		ORDER BY evidence.claim_id, source.id, evidence.id
	`, sessionID, claimIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var claimID string
		var link evidenceFitnessLink
		if err = rows.Scan(
			&claimID, &link.SourceID, &link.IndependenceKey, &link.EvidenceTraits,
			&link.Strength, &link.Directness, &link.MethodFit,
		); err != nil {
			return nil, err
		}
		links[claimID] = append(links[claimID], link)
	}
	return links, rows.Err()
}

func (s *PostgresStore) loadCounterSearchCoverage(ctx context.Context, sessionID string, goalVersion, planVersion int, targets []evidenceFitnessTarget) (map[string]bool, error) {
	coverage := make(map[string]bool, len(targets))
	if len(targets) == 0 {
		return coverage, nil
	}
	claimIDs := make([]string, 0, len(targets))
	claimKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		claimIDs = append(claimIDs, target.ID)
		claimKeys = append(claimKeys, target.ClientKey)
	}
	rows, err := s.pool.Query(ctx, `
		WITH targets AS (
		  SELECT claim_id, claim_key
		  FROM unnest($4::text[], $5::text[]) AS target(claim_id, claim_key)
		)
		SELECT target.claim_id, EXISTS (
		  SELECT 1
		  FROM research_task task
		  JOIN research_task_attempt attempt ON attempt.task_id = task.id AND attempt.status = 'succeeded'
		  LEFT JOIN research_question question ON question.id = task.question_id
		  WHERE task.session_id = $1::uuid AND task.goal_version = $2 AND task.plan_version = $3
		    AND task.kind = 'counter_search' AND task.status = 'succeeded'
		    AND (
		      question.answer_claim_id = target.claim_id::uuid
		      OR EXISTS (
		        SELECT 1
		        FROM jsonb_array_elements(COALESCE(attempt.result->'claims', '[]'::jsonb)) result_claim
		        WHERE result_claim->>'client_key' = target.claim_key
		      )
		    )
		) AS covered
		FROM targets target
		ORDER BY target.claim_id
	`, sessionID, goalVersion, planVersion, claimIDs, claimKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var claimID string
		var covered bool
		if err = rows.Scan(&claimID, &covered); err != nil {
			return nil, err
		}
		coverage[claimID] = covered
	}
	return coverage, rows.Err()
}
