package researchrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

const shadowDispositionEntry = "entry"

type shadowDomainProjectionRecord struct {
	Kind        ArtifactEntityKind `json:"kind"`
	ArtifactID  string             `json:"artifact_id"`
	Disposition string             `json:"disposition"`
}

// loadLegacyShadowDomainProjectionTx selects canonical domain rows independently
// from the passport/version candidate query. Passport state is joined only after
// domain membership is established so a missing or wrongly typed passport cannot
// make the legacy side silently disappear.
func loadLegacyShadowDomainProjectionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	purpose ArtifactPurpose,
	excludedContextManifestID string,
) ([]shadowDomainProjectionRecord, error) {
	rows, err := tx.Query(ctx, `
		WITH domain_ref(kind, artifact_id) AS (
		  SELECT 'run_session', run.id FROM research_session run WHERE run.workspace_id=$1::uuid AND run.id=$2::uuid
		  UNION ALL SELECT 'contract_revision', entity.id FROM research_contract_revision entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT CASE WHEN entity.decision_kind='research_method' THEN 'method_decision' ELSE 'evaluation_decision' END, entity.id FROM research_decision entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'question', entity.id FROM research_question entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'task', entity.id FROM research_task entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'attempt', entity.id FROM research_task_attempt entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'result_artifact', entity.id FROM research_result_artifact entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'legacy_source', entity.id FROM research_source entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'source_snapshot', entity.id FROM research_source_snapshot entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'observation', entity.id FROM research_observation entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'claim', entity.id FROM research_claim entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'evidence_link', entity.id FROM research_claim_evidence entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'report_revision', entity.id FROM research_report entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'stage_evaluation', entity.id FROM research_stage_eval entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'research_message', entity.id FROM research_message entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'product_round_decision', entity.id FROM research_product_round_card entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'context_manifest', entity.id FROM research_artifact_context_manifest entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid AND entity.id::text <> COALESCE(NULLIF($3, ''), '00000000-0000-0000-0000-000000000000')
		  UNION ALL SELECT 'run_event', entity.id FROM research_run_event entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'graph_node', entity.id FROM research_graph_node entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'graph_edge', entity.id FROM research_graph_edge entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'hypothesis', entity.id FROM research_hypothesis entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'branch', entity.id FROM research_branch entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'insight', entity.id FROM research_insight entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		  UNION ALL SELECT 'inquiry_edge', entity.id FROM research_inquiry_edge entity WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid
		)
		SELECT domain_ref.kind, domain_ref.artifact_id::text,
		       COALESCE(passport.entity_kind, ''), COALESCE(passport.lifecycle_status, ''),
		       COALESCE(passport.provenance_completeness, ''),
		       COALESCE(version.access_level, '')
		FROM domain_ref
		LEFT JOIN research_artifact_passport passport
		  ON passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid
		 AND passport.id=domain_ref.artifact_id
		LEFT JOIN research_artifact_version version
		  ON version.workspace_id=passport.workspace_id AND version.session_id=passport.session_id
		 AND version.artifact_id=passport.id AND version.version=passport.current_version
		ORDER BY domain_ref.kind, domain_ref.artifact_id
	`, workspaceID, sessionID, excludedContextManifestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	policy := ArtifactPolicy{}
	clearance := defaultTaskExecutionClearance()
	var projection []shadowDomainProjectionRecord
	for rows.Next() {
		var domainKindRaw, artifactID string
		var passportKindRaw, lifecycleRaw, provenanceRaw, accessRaw sql.NullString
		if err = rows.Scan(&domainKindRaw, &artifactID, &passportKindRaw, &lifecycleRaw, &provenanceRaw, &accessRaw); err != nil {
			return nil, err
		}
		domainKind, parseErr := ParseArtifactEntityKind(domainKindRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		if !isDispatchManifestCandidateKind(domainKind) {
			continue
		}
		if !passportKindRaw.Valid || passportKindRaw.String == "" {
			return nil, fmt.Errorf("%w: shadow domain artifact %s/%s is missing passport", ErrInvalidTransition, domainKind, artifactID)
		}
		if passportKindRaw.String != domainKindRaw {
			return nil, fmt.Errorf("%w: shadow domain artifact %s/%s has passport kind %s", ErrInvalidTransition, domainKind, artifactID, passportKindRaw.String)
		}
		if !lifecycleRaw.Valid || !provenanceRaw.Valid || !accessRaw.Valid || accessRaw.String == "" {
			return nil, fmt.Errorf("%w: shadow domain artifact %s/%s has incomplete passport version state", ErrInvalidTransition, domainKind, artifactID)
		}

		disposition := shadowDispositionEntry
		private := policy.EvaluationPrivateKind(domainKind)
		if private && purpose == ArtifactPurposeTaskExecution {
			disposition = policy.ManifestOmissionReason(ArtifactDenyEvaluationCompartment)
		} else if admitted, deny := policy.LegacyAdmissionAllowed(
			domainKind,
			ArtifactLifecycleStatus(lifecycleRaw.String),
			ArtifactProvenanceCompleteness(provenanceRaw.String),
		); !admitted {
			disposition = policy.ManifestOmissionReason(deny)
		} else if allowed, deny := policy.CanReadNormal(
			clearance, ArtifactAccessLevel(accessRaw.String), purpose, private,
		); !allowed {
			disposition = policy.ManifestOmissionReason(deny)
		}
		projection = append(projection, shadowDomainProjectionRecord{
			Kind: domainKind, ArtifactID: artifactID, Disposition: disposition,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return projection, nil
}

func projectManifestForShadow(plan dispatchManifestPlan) []shadowDomainProjectionRecord {
	projection := make([]shadowDomainProjectionRecord, 0, len(plan.Entries)+len(plan.Omissions))
	for _, entry := range plan.Entries {
		projection = append(projection, shadowDomainProjectionRecord{
			Kind: entry.Kind, ArtifactID: entry.ArtifactID, Disposition: shadowDispositionEntry,
		})
	}
	for _, omission := range plan.Omissions {
		projection = append(projection, shadowDomainProjectionRecord{
			Kind: omission.Kind, ArtifactID: omission.ArtifactID, Disposition: omission.OmissionReason,
		})
	}
	sort.Slice(projection, func(i, j int) bool {
		if projection[i].Kind != projection[j].Kind {
			return projection[i].Kind < projection[j].Kind
		}
		return projection[i].ArtifactID < projection[j].ArtifactID
	})
	return projection
}

func compareShadowDomainProjection(
	domain, manifest []shadowDomainProjectionRecord,
	manifestHash string,
) error {
	match := len(domain) == len(manifest)
	if match {
		for i := range domain {
			if domain[i] != manifest[i] {
				match = false
				break
			}
		}
	}
	if match {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"domain_projection":   domain,
		"manifest_projection": manifest,
		"manifest_hash":       manifestHash,
	})
	return fmt.Errorf("%w: independent domain shadow mismatch: %s", ErrInvalidTransition, payload)
}
