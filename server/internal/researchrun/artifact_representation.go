package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type frozenLegacyOrderedRepresentation[T any] struct {
	Order int `json:"order"`
	Value T   `json:"value"`
}

// freezeManifestRepresentationsTx replaces the legacy hash placeholder with
// the exact bounded wire representation supplied to an Agent. Only already
// authorized entries are read here.
func freezeManifestRepresentationsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, entries []artifactVersionCandidate) error {
	authorized := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		authorized[entry.ArtifactID] = struct{}{}
	}
	for i := range entries {
		var value any
		switch entries[i].Kind {
		case ArtifactKindLegacySource:
			var order int
			var item FrozenLegacySource
			err := tx.QueryRow(ctx, `
				WITH ordered AS (
				  SELECT s.*, row_number() OVER (ORDER BY credibility_weight DESC, created_at, id)::int - 1 AS frozen_order
				  FROM research_source s WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				)
				SELECT frozen_order, id::text, session_id::text, url, title, source_class,
				       credibility_weight, stance, relevance, summary, excerpt, payload,
				       created_at, updated_at
				FROM ordered WHERE id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&order, &item.ID, &item.SessionID, &item.URL, &item.Title,
				&item.SourceClass, &item.CredibilityWeight, &item.Stance,
				&item.Relevance, &item.Summary, &item.Excerpt, &item.Payload,
				&item.CreatedAt, &item.UpdatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze legacy source representation: %w", err)
			}
			value = frozenLegacyOrderedRepresentation[FrozenLegacySource]{Order: order, Value: item}
		case ArtifactKindResearchMessage:
			var order int
			var item FrozenResearchMessage
			err := tx.QueryRow(ctx, `
				WITH ordered AS (
				  SELECT m.*, row_number() OVER (ORDER BY created_at, id)::int - 1 AS frozen_order
				  FROM research_message m WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				)
				SELECT frozen_order, id::text, session_id::text, sender_type,
				       COALESCE(sender_id::text, ''), COALESCE(target_agent_id::text, ''),
				       body, card_kind, meta, created_at
				FROM ordered WHERE id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&order, &item.ID, &item.SessionID, &item.SenderType, &item.SenderID,
				&item.TargetAgentID, &item.Body, &item.CardKind, &item.Meta, &item.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze research message representation: %w", err)
			}
			value = frozenLegacyOrderedRepresentation[FrozenResearchMessage]{Order: order, Value: item}
		case ArtifactKindProductRoundDecision:
			var order int
			var item FrozenProductRound
			err := tx.QueryRow(ctx, `
				WITH ordered AS (
				  SELECT c.*, row_number() OVER (ORDER BY round_number, id)::int - 1 AS frozen_order
				  FROM research_product_round_card c WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				)
				SELECT frozen_order, id::text, session_id::text, round_number, decision,
				       coverage_gaps, confidence_note, budget_used, budget_remaining,
				       COALESCE(goal_patch_proposal, ''), COALESCE(next_round_focus, ''),
				       COALESCE(decided_by_agent_id::text, ''), created_at
				FROM ordered WHERE id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&order, &item.ID, &item.SessionID, &item.RoundNumber, &item.Decision,
				&item.CoverageGaps, &item.ConfidenceNote, &item.BudgetUsed,
				&item.BudgetRemaining, &item.GoalPatchProposal, &item.NextRoundFocus,
				&item.DecidedByAgentID, &item.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze product round representation: %w", err)
			}
			value = frozenLegacyOrderedRepresentation[FrozenProductRound]{Order: order, Value: item}
		case ArtifactKindGraphNode:
			var order int
			var item FrozenThoughtStrategyNode
			err := tx.QueryRow(ctx, `
				WITH ordered AS (
				  SELECT n.*, row_number() OVER (ORDER BY created_at, id)::int - 1 AS frozen_order
				  FROM research_graph_node n WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				)
				SELECT frozen_order, id::text, session_id::text, payload, updated_at
				FROM ordered WHERE id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&order, &item.ID, &item.SessionID, &item.Payload, &item.UpdatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze thought strategy node representation: %w", err)
			}
			value = frozenLegacyOrderedRepresentation[FrozenThoughtStrategyNode]{Order: order, Value: item}
		case ArtifactKindReportRevision:
			var item FrozenResearchReport
			err := tx.QueryRow(ctx, `
				SELECT id::text, session_id::text, revision, content_md, structured,
				       created_at, updated_at
				FROM research_report
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
			`, workspaceID, sessionID, entries[i].ArtifactID).Scan(
				&item.ID, &item.SessionID, &item.Revision, &item.ContentMD,
				&item.Structured, &item.CreatedAt, &item.UpdatedAt,
			)
			if err != nil {
				return fmt.Errorf("freeze report representation: %w", err)
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

func loadFrozenLegacyContextPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) (FrozenLegacyContext, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.entity_kind, e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid
		  AND p.entity_kind IN ('legacy_source','research_message','product_round_decision','graph_node','report_revision')
		ORDER BY e.ordinal
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return FrozenLegacyContext{}, err
	}
	defer rows.Close()

	type orderedSource = frozenLegacyOrderedRepresentation[FrozenLegacySource]
	type orderedMessage = frozenLegacyOrderedRepresentation[FrozenResearchMessage]
	type orderedRound = frozenLegacyOrderedRepresentation[FrozenProductRound]
	type orderedThought = frozenLegacyOrderedRepresentation[FrozenThoughtStrategyNode]
	var sources []orderedSource
	var messages []orderedMessage
	var rounds []orderedRound
	var thoughts []orderedThought
	var reports []FrozenResearchReport
	for rows.Next() {
		var kind string
		var encoded []byte
		var storedHash string
		if err = rows.Scan(&kind, &encoded, &storedHash); err != nil {
			return FrozenLegacyContext{}, err
		}
		if len(encoded) == 0 || contentHashFromPayload(encoded) != storedHash {
			return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s representation missing or invalid", ErrInvalidTransition, kind)
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindLegacySource:
			var item orderedSource
			err = json.Unmarshal(encoded, &item)
			if err == nil {
				if item.Value.SessionID != sessionID {
					return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s scope mismatch", ErrInvalidTransition, kind)
				}
				sources = append(sources, item)
			}
		case ArtifactKindResearchMessage:
			var item orderedMessage
			err = json.Unmarshal(encoded, &item)
			if err == nil {
				if item.Value.SessionID != sessionID {
					return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s scope mismatch", ErrInvalidTransition, kind)
				}
				messages = append(messages, item)
			}
		case ArtifactKindProductRoundDecision:
			var item orderedRound
			err = json.Unmarshal(encoded, &item)
			if err == nil {
				if item.Value.SessionID != sessionID {
					return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s scope mismatch", ErrInvalidTransition, kind)
				}
				rounds = append(rounds, item)
			}
		case ArtifactKindGraphNode:
			var item orderedThought
			err = json.Unmarshal(encoded, &item)
			if err == nil {
				if item.Value.SessionID != sessionID {
					return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s scope mismatch", ErrInvalidTransition, kind)
				}
				thoughts = append(thoughts, item)
			}
		case ArtifactKindReportRevision:
			var item FrozenResearchReport
			err = json.Unmarshal(encoded, &item)
			if err == nil {
				if item.SessionID != sessionID {
					return FrozenLegacyContext{}, fmt.Errorf("%w: frozen %s scope mismatch", ErrInvalidTransition, kind)
				}
				reports = append(reports, item)
			}
		}
		if err != nil {
			return FrozenLegacyContext{}, fmt.Errorf("decode frozen %s representation: %w", kind, err)
		}
	}
	if err = rows.Err(); err != nil {
		return FrozenLegacyContext{}, err
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Order < sources[j].Order })
	sort.Slice(messages, func(i, j int) bool { return messages[i].Order < messages[j].Order })
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].Order < rounds[j].Order })
	sort.Slice(thoughts, func(i, j int) bool { return thoughts[i].Order < thoughts[j].Order })
	sort.Slice(reports, func(i, j int) bool { return reports[i].Revision < reports[j].Revision })
	out := FrozenLegacyContext{}
	for _, item := range sources {
		out.Sources = append(out.Sources, item.Value)
	}
	for _, item := range messages {
		out.Messages = append(out.Messages, item.Value)
	}
	for _, item := range rounds {
		out.ProductRounds = append(out.ProductRounds, item.Value)
	}
	for _, item := range thoughts {
		out.ThoughtStrategies = append(out.ThoughtStrategies, item.Value)
	}
	if len(reports) > 0 {
		report := reports[len(reports)-1]
		out.Report = &report
	}
	return out, nil
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
