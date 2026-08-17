package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArtifactContextModule owns D-enabled candidate selection and manifest construction.
type ArtifactContextModule struct {
	policy ArtifactPolicy
}

func NewArtifactContextModule() ArtifactContextModule {
	return ArtifactContextModule{policy: ArtifactPolicy{}}
}

type artifactVersionCandidate struct {
	VersionRowID           string
	ArtifactID             string
	Kind                   ArtifactEntityKind
	Version                int32
	EligibilityRevision    int64
	AccessLevel            ArtifactAccessLevel
	Lifecycle              ArtifactLifecycleStatus
	Provenance             ArtifactProvenanceCompleteness
	ContentHash            string
	Representation         string
	RepresentationBytes    []byte
	RepresentationHash     string
	VersionCount           int
	InputReferenceCount    int
	OutputReferenceCount   int
	RelationshipHash       string
	OmissionReason         string
	DomainStatus           string
	HasMigrationDiagnostic bool
}

type dispatchManifestPlan struct {
	ManifestID              string
	Entries                 []artifactVersionCandidate
	Omissions               []artifactVersionCandidate
	PolicyWatermark         int64
	ThroughStateVersion     int64
	ManifestHash            string
	OmissionHash            string
	NormalGrantID           string
	NormalGrantRevision     int64
	EvaluationGrantID       string
	EvaluationGrantRevision int64
	Purpose                 ArtifactPurpose
	PolicyVersion           string
	IsolationAllowlist      map[string]struct{}
}

func isDispatchManifestCandidateKind(kind ArtifactEntityKind) bool {
	switch kind {
	case ArtifactKindAttempt, ArtifactKindContextManifest, ArtifactKindResultArtifact:
		return false
	default:
		return true
	}
}

func (m ArtifactContextModule) PlanDispatchManifest(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	stateVersion int64,
) (dispatchManifestPlan, error) {
	return m.PlanDispatchManifestForPurpose(ctx, tx, workspaceID, sessionID, stateVersion, ArtifactPurposeTaskExecution)
}

func (m ArtifactContextModule) PlanDispatchManifestForPurpose(
	ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, stateVersion int64, purpose ArtifactPurpose,
) (dispatchManifestPlan, error) {
	return m.planDispatchManifestWithClearance(
		ctx, tx, workspaceID, sessionID, stateVersion, defaultTaskExecutionClearance(), purpose,
	)
}

func (m ArtifactContextModule) PlanDispatchManifestForTask(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, stateVersion int64, purpose ArtifactPurpose, task Task) (dispatchManifestPlan, error) {
	plan, err := m.PlanDispatchManifestForPurpose(ctx, tx, workspaceID, sessionID, stateVersion, purpose)
	if err != nil {
		return plan, err
	}
	return isolateDisputeReviewManifest(plan, task), nil
}

func isolateDisputeReviewManifest(plan dispatchManifestPlan, task Task) dispatchManifestPlan {
	var criteria struct {
		TaskContext struct {
			Mode               string   `json:"mode"`
			VisibleArtifactIDs []string `json:"visible_artifact_ids"`
		} `json:"task_context"`
	}
	if json.Unmarshal(task.AcceptanceCriteria, &criteria) != nil || criteria.TaskContext.Mode != "dispute_review" {
		return plan
	}
	allowed := map[string]struct{}{task.ID: {}}
	for _, id := range criteria.TaskContext.VisibleArtifactIDs {
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	kept := make([]artifactVersionCandidate, 0, len(plan.Entries))
	for _, candidate := range plan.Entries {
		_, explicit := allowed[candidate.ArtifactID]
		foundation := candidate.Kind == ArtifactKindRunSession || candidate.Kind == ArtifactKindContractRevision || candidate.Kind == ArtifactKindMethodDecision
		if explicit || foundation {
			kept = append(kept, candidate)
			continue
		}
		candidate.OmissionReason = "irrelevant"
		plan.Omissions = append(plan.Omissions, candidate)
	}
	plan.Entries, plan.IsolationAllowlist = kept, allowed
	sortManifestEntryCandidates(plan.Entries)
	plan.ManifestHash = hashManifestEntries(plan.Entries)
	plan.OmissionHash = hashManifestOmissions(plan.Omissions)
	return plan
}

func (m ArtifactContextModule) planDispatchManifestWithClearance(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	stateVersion int64,
	clearance ArtifactClearance,
	purpose ArtifactPurpose,
) (dispatchManifestPlan, error) {
	candidates, err := loadArtifactVersionCandidates(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	policyVersion, err := artifactContextPolicyVersionTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	var entries []artifactVersionCandidate
	var omissions []artifactVersionCandidate
	for _, candidate := range candidates {
		if candidate.HasMigrationDiagnostic {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(ArtifactDenyLegacyIneligible)
			omissions = append(omissions, candidate)
			continue
		}
		private := m.policy.EvaluationPrivateKind(candidate.Kind)
		if private && purpose == ArtifactPurposeTaskExecution {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(ArtifactDenyEvaluationCompartment)
			omissions = append(omissions, candidate)
			continue
		}
		admitted, deny := m.policy.AdmissionAllowedFacts(policyVersion, candidate.legacyAdmissionFacts())
		if !admitted {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(deny)
			omissions = append(omissions, candidate)
			continue
		}
		allowed, deny := m.policy.CanReadNormal(
			clearance, candidate.AccessLevel, purpose, private,
		)
		if !allowed {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(deny)
			omissions = append(omissions, candidate)
			continue
		}
		candidate.Representation = "full"
		candidate.RepresentationBytes = []byte(candidate.ContentHash)
		candidate.RepresentationHash = contentHashFromPayload(candidate.RepresentationBytes)
		entries = append(entries, candidate)
	}
	if err = auditManifestCandidateDispositions(candidates, entries, omissions, clearance, purpose, m.policy, policyVersion); err != nil {
		return dispatchManifestPlan{}, err
	}
	sortManifestEntryCandidates(entries)

	var watermark int64
	if err = tx.QueryRow(ctx, `
		SELECT watermark
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		return dispatchManifestPlan{}, fmt.Errorf("read policy watermark: %w", err)
	}

	manifestID := uuid.NewString()
	manifestHash := hashManifestEntries(entries)
	return dispatchManifestPlan{
		ManifestID:          manifestID,
		Entries:             entries,
		Omissions:           omissions,
		PolicyWatermark:     watermark,
		ThroughStateVersion: stateVersion,
		ManifestHash:        manifestHash,
		OmissionHash:        hashManifestOmissions(omissions),
		Purpose:             purpose,
		PolicyVersion:       policyVersion,
	}, nil
}

func artifactContextPolicyVersionTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) (string, error) {
	var version string
	if err := tx.QueryRow(ctx, `SELECT orchestrator_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid`, workspaceID, sessionID).Scan(&version); err != nil {
		return "", err
	}
	if version == OrchestratorVersionV6 {
		return ResearchV6ContextPolicy, nil
	}
	return LegacyV1V5CompatPolicy, nil
}

func (candidate artifactVersionCandidate) legacyAdmissionFacts() legacyAdmissionFacts {
	return legacyAdmissionFacts{
		Kind: candidate.Kind, Lifecycle: candidate.Lifecycle,
		Provenance: candidate.Provenance, DomainStatus: candidate.DomainStatus,
	}
}

func sortManifestEntryCandidates(entries []artifactVersionCandidate) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].ArtifactID < entries[j].ArtifactID
	})
}

func hashManifestEntries(entries []artifactVersionCandidate) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf(
			"%s:%d:%d:%s:%s:%s:%s:%d:%d:%d:%s",
			entry.ArtifactID, entry.Version, entry.EligibilityRevision,
			entry.Representation, entry.RepresentationHash, entry.Lifecycle, entry.Provenance,
			entry.VersionCount, entry.InputReferenceCount, entry.OutputReferenceCount, entry.RelationshipHash,
		))
	}
	sort.Strings(parts)
	payload := strings.Join(parts, "\n")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashManifestOmissions(omissions []artifactVersionCandidate) string {
	parts := make([]string, 0, len(omissions))
	for ordinal, omission := range omissions {
		parts = append(parts, fmt.Sprintf(
			"omission=%d:%s:%s",
			ordinal, omission.VersionRowID, omission.OmissionReason,
		))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

type dispatchManifestHashInput struct {
	WorkspaceID         string
	SessionID           string
	AttemptID           string
	TaskID              string
	Purpose             ArtifactPurpose
	PolicyVersion       string
	PolicyWatermark     int64
	ThroughStateVersion int64
	Entries             []artifactVersionCandidate
}

// hashDispatchManifest binds the selected representations to the complete
// authorization and dispatch scope. An entry-only digest is insufficient: the
// same bytes must not be replayable under another tenant, task, attempt, policy,
// or Run state watermark.
func hashDispatchManifest(in dispatchManifestHashInput) string {
	parts := []string{
		"workspace=" + in.WorkspaceID,
		"session=" + in.SessionID,
		"attempt=" + in.AttemptID,
		"task=" + in.TaskID,
		"purpose=" + string(in.Purpose),
		"policy_version=" + in.PolicyVersion,
		fmt.Sprintf("policy_watermark=%d", in.PolicyWatermark),
		fmt.Sprintf("through_state_version=%d", in.ThroughStateVersion),
	}
	entries := append([]artifactVersionCandidate(nil), in.Entries...)
	sortManifestEntryCandidates(entries)
	for ordinal, entry := range entries {
		entryPart := fmt.Sprintf(
			"entry=%d:%s:%s:%d:%d:%s:%s:%s:%s:%s:%d:%d:%d:input",
			ordinal, entry.VersionRowID, entry.ArtifactID, entry.Version,
			entry.EligibilityRevision, entry.AccessLevel, entry.Representation, entry.RepresentationHash,
			entry.Lifecycle, entry.Provenance, entry.VersionCount,
			entry.InputReferenceCount, entry.OutputReferenceCount,
		)
		if entry.RelationshipHash != "" {
			entryPart += ":relationships=" + entry.RelationshipHash
		}
		parts = append(parts, entryPart)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func loadArtifactVersionCandidates(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
) ([]artifactVersionCandidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT
		  v.id::text,
		  p.id::text,
		  p.entity_kind,
		  v.version,
		  p.eligibility_revision,
		  v.access_level,
		  p.lifecycle_status,
		  p.provenance_completeness,
		  v.content_hash,
		  COALESCE(CASE p.entity_kind
		    WHEN 'task' THEN (SELECT t.status FROM research_task t
		      WHERE (t.workspace_id,t.session_id,t.id)=(p.workspace_id,p.session_id,p.id))
		    WHEN 'claim' THEN (SELECT c.status FROM research_claim c
		      WHERE (c.workspace_id,c.session_id,c.id)=(p.workspace_id,p.session_id,p.id))
		    WHEN 'source_snapshot' THEN (SELECT s.verification_status FROM research_source_snapshot s
		      WHERE (s.workspace_id,s.session_id,s.id)=(p.workspace_id,p.session_id,p.id))
		    WHEN 'observation' THEN (SELECT o.verification_status FROM research_observation o
		      WHERE (o.workspace_id,o.session_id,o.id)=(p.workspace_id,p.session_id,p.id))
		    WHEN 'evidence_link' THEN (SELECT e.verification_status FROM research_claim_evidence e
		      WHERE (e.workspace_id,e.session_id,e.id)=(p.workspace_id,p.session_id,p.id))
		    ELSE ''
		  END, '') AS domain_status,
		  EXISTS (
		    SELECT 1 FROM research_artifact_migration_diagnostic diagnostic
		    WHERE diagnostic.workspace_id=p.workspace_id
		      AND diagnostic.session_id=p.session_id
		      AND diagnostic.owner_kind=p.entity_kind
		      AND diagnostic.owner_id=p.id
		  ) AS has_migration_diagnostic,
		  (SELECT count(*)::int FROM research_artifact_version all_v
		    WHERE (all_v.workspace_id,all_v.session_id,all_v.artifact_id)=(p.workspace_id,p.session_id,p.id)) AS version_count,
		  (SELECT count(*)::int FROM research_artifact_input_reference input_ref
		    JOIN research_artifact_version input_v
		      ON (input_v.workspace_id,input_v.session_id,input_v.id)=(input_ref.workspace_id,input_ref.session_id,input_ref.input_version_id)
		    WHERE (input_v.workspace_id,input_v.session_id,input_v.artifact_id)=(p.workspace_id,p.session_id,p.id)) AS input_reference_count,
		  (SELECT count(*)::int FROM research_artifact_input_reference output_ref
		    JOIN research_artifact_version output_v
		      ON (output_v.workspace_id,output_v.session_id,output_v.id)=(output_ref.workspace_id,output_ref.session_id,output_ref.consumer_version_id)
		    WHERE (output_v.workspace_id,output_v.session_id,output_v.artifact_id)=(p.workspace_id,p.session_id,p.id)) AS output_reference_count
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON v.workspace_id = p.workspace_id
		 AND v.session_id = p.session_id
		 AND v.artifact_id = p.id
		 AND v.version = p.current_version
		WHERE p.workspace_id = $1::uuid
		  AND p.session_id = $2::uuid
		  AND p.entity_kind NOT IN ('context_manifest', 'result_artifact')
		ORDER BY p.entity_kind, p.id::text
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]artifactVersionCandidate, 0)
	for rows.Next() {
		var candidate artifactVersionCandidate
		var kindRaw, lifecycleRaw, provenanceRaw, accessRaw string
		if err = rows.Scan(
			&candidate.VersionRowID, &candidate.ArtifactID, &kindRaw,
			&candidate.Version, &candidate.EligibilityRevision,
			&accessRaw, &lifecycleRaw, &provenanceRaw, &candidate.ContentHash, &candidate.DomainStatus,
			&candidate.HasMigrationDiagnostic,
			&candidate.VersionCount, &candidate.InputReferenceCount, &candidate.OutputReferenceCount,
		); err != nil {
			return nil, err
		}
		kind, parseErr := ParseArtifactEntityKind(kindRaw)
		if parseErr != nil {
			return nil, parseErr
		}
		if !isDispatchManifestCandidateKind(kind) {
			continue
		}
		candidate.Kind = kind
		candidate.AccessLevel = ArtifactAccessLevel(accessRaw)
		candidate.Lifecycle = ArtifactLifecycleStatus(lifecycleRaw)
		candidate.Provenance = ArtifactProvenanceCompleteness(provenanceRaw)
		out = append(out, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	relationshipHashes, err := loadArtifactRelationshipHashesTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].RelationshipHash = relationshipHashes[out[i].VersionRowID]
		if out[i].RelationshipHash == "" {
			out[i].RelationshipHash = contentHashFromPayload(nil)
		}
	}
	return out, nil
}

// loadDispatchAttemptCandidateTx loads only the attempt that owns the manifest.
// Attempts are excluded from the ordinary context candidate universe so prior
// execution envelopes cannot leak into a new dispatch, but the current envelope
// must still be frozen for deterministic replay.
func loadDispatchAttemptCandidateTx(
	ctx context.Context, tx pgx.Tx, workspaceID, sessionID, attemptID string,
) (artifactVersionCandidate, error) {
	var candidate artifactVersionCandidate
	var kindRaw, lifecycleRaw, provenanceRaw, accessRaw string
	err := tx.QueryRow(ctx, `
		SELECT v.id::text, p.id::text, p.entity_kind, v.version,
		       p.eligibility_revision, v.access_level, p.lifecycle_status,
		       p.provenance_completeness, v.content_hash, '' AS domain_status,
		       EXISTS (
		         SELECT 1 FROM research_artifact_migration_diagnostic diagnostic
		         WHERE diagnostic.workspace_id=p.workspace_id
		           AND diagnostic.session_id=p.session_id
		           AND diagnostic.owner_kind=p.entity_kind
		           AND diagnostic.owner_id=p.id
		       ),
		       (SELECT count(*)::int FROM research_artifact_version all_v
		         WHERE (all_v.workspace_id,all_v.session_id,all_v.artifact_id)=(p.workspace_id,p.session_id,p.id)),
		       (SELECT count(*)::int FROM research_artifact_input_reference input_ref
		         JOIN research_artifact_version input_v
		           ON (input_v.workspace_id,input_v.session_id,input_v.id)=(input_ref.workspace_id,input_ref.session_id,input_ref.input_version_id)
		         WHERE (input_v.workspace_id,input_v.session_id,input_v.artifact_id)=(p.workspace_id,p.session_id,p.id)),
		       (SELECT count(*)::int FROM research_artifact_input_reference output_ref
		         JOIN research_artifact_version output_v
		           ON (output_v.workspace_id,output_v.session_id,output_v.id)=(output_ref.workspace_id,output_ref.session_id,output_ref.consumer_version_id)
		         WHERE (output_v.workspace_id,output_v.session_id,output_v.artifact_id)=(p.workspace_id,p.session_id,p.id))
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON v.workspace_id=p.workspace_id AND v.session_id=p.session_id
		 AND v.artifact_id=p.id AND v.version=p.current_version
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid
		  AND p.id=$3::uuid AND p.entity_kind='attempt'
	`, workspaceID, sessionID, attemptID).Scan(
		&candidate.VersionRowID, &candidate.ArtifactID, &kindRaw,
		&candidate.Version, &candidate.EligibilityRevision,
		&accessRaw, &lifecycleRaw, &provenanceRaw, &candidate.ContentHash,
		&candidate.DomainStatus, &candidate.HasMigrationDiagnostic,
		&candidate.VersionCount, &candidate.InputReferenceCount, &candidate.OutputReferenceCount,
	)
	if err != nil {
		return artifactVersionCandidate{}, err
	}
	kind, err := ParseArtifactEntityKind(kindRaw)
	if err != nil {
		return artifactVersionCandidate{}, err
	}
	candidate.Kind = kind
	candidate.AccessLevel = ArtifactAccessLevel(accessRaw)
	candidate.Lifecycle = ArtifactLifecycleStatus(lifecycleRaw)
	candidate.Provenance = ArtifactProvenanceCompleteness(provenanceRaw)
	if candidate.HasMigrationDiagnostic || candidate.Lifecycle != ArtifactLifecycleRegistered ||
		candidate.Provenance != ArtifactProvenanceComplete {
		return artifactVersionCandidate{}, fmt.Errorf("%w: dispatch attempt is not eligible for frozen context", ErrInvalidTransition)
	}
	relationshipHashes, err := loadArtifactRelationshipHashesTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return artifactVersionCandidate{}, err
	}
	candidate.RelationshipHash = relationshipHashes[candidate.VersionRowID]
	if candidate.RelationshipHash == "" {
		candidate.RelationshipHash = contentHashFromPayload(nil)
	}
	return candidate, nil
}

func auditManifestCandidateDispositions(
	candidates, entries, omissions []artifactVersionCandidate,
	clearance ArtifactClearance,
	purpose ArtifactPurpose,
	policy ArtifactPolicy,
	policyVersions ...string,
) error {
	policyVersion := LegacyV1V5CompatPolicy
	if len(policyVersions) > 0 && policyVersions[0] != "" {
		policyVersion = policyVersions[0]
	}
	want := make(map[string]artifactVersionCandidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.VersionRowID == "" {
			return fmt.Errorf("%w: Manifest candidate has empty version identity", ErrInvalidTransition)
		}
		if _, duplicate := want[candidate.VersionRowID]; duplicate {
			return fmt.Errorf("%w: duplicate Manifest candidate version %s", ErrInvalidTransition, candidate.VersionRowID)
		}
		want[candidate.VersionRowID] = candidate
	}

	seen := make(map[string]string, len(candidates))
	check := func(candidate artifactVersionCandidate, disposition string) error {
		original, ok := want[candidate.VersionRowID]
		if !ok || original.ArtifactID != candidate.ArtifactID {
			return fmt.Errorf("%w: Manifest %s references a non-candidate version %s", ErrInvalidTransition, disposition, candidate.VersionRowID)
		}
		if prior, duplicate := seen[candidate.VersionRowID]; duplicate {
			return fmt.Errorf("%w: Manifest candidate version %s has both %s and %s dispositions", ErrInvalidTransition, candidate.VersionRowID, prior, disposition)
		}
		seen[candidate.VersionRowID] = disposition
		return nil
	}
	for _, entry := range entries {
		if entry.OmissionReason != "" {
			return fmt.Errorf("%w: admitted Manifest entry has omission reason", ErrInvalidTransition)
		}
		if err := check(entry, "entry"); err != nil {
			return err
		}
	}
	for _, omission := range omissions {
		if omission.OmissionReason == "" {
			return fmt.Errorf("%w: omitted Manifest candidate has no reason", ErrInvalidTransition)
		}
		if err := check(omission, "omission"); err != nil {
			return err
		}
	}
	if len(seen) != len(want) {
		missing := make([]string, 0, len(want)-len(seen))
		for versionID := range want {
			if _, ok := seen[versionID]; !ok {
				missing = append(missing, versionID)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("%w: Manifest candidates lack disposition: %s", ErrInvalidTransition, strings.Join(missing, ","))
	}

	for versionID, candidate := range want {
		expected := "entry"
		expectedReason := ""
		if candidate.HasMigrationDiagnostic {
			expected = "omission"
			expectedReason = policy.ManifestOmissionReason(ArtifactDenyLegacyIneligible)
		} else if policy.EvaluationPrivateKind(candidate.Kind) && purpose == ArtifactPurposeTaskExecution {
			expected = "omission"
			expectedReason = policy.ManifestOmissionReason(ArtifactDenyEvaluationCompartment)
		} else if admitted, deny := policy.AdmissionAllowedFacts(policyVersion, candidate.legacyAdmissionFacts()); !admitted {
			expected = "omission"
			expectedReason = policy.ManifestOmissionReason(deny)
		} else if allowed, deny := policy.CanReadNormal(
			clearance, candidate.AccessLevel, purpose, policy.EvaluationPrivateKind(candidate.Kind),
		); !allowed {
			expected = "omission"
			expectedReason = policy.ManifestOmissionReason(deny)
		}
		if seen[versionID] != expected {
			return fmt.Errorf("%w: Manifest candidate version %s disposition=%s want=%s", ErrInvalidTransition, versionID, seen[versionID], expected)
		}
		if expected == "omission" {
			for _, omission := range omissions {
				if omission.VersionRowID == versionID && omission.OmissionReason != expectedReason {
					return fmt.Errorf("%w: Manifest candidate version %s omission reason=%s want=%s", ErrInvalidTransition, versionID, omission.OmissionReason, expectedReason)
				}
			}
		}
	}
	return nil
}

type persistDispatchManifestInput struct {
	WorkspaceID       string
	SessionID         string
	AttemptID         string
	TaskID            string
	Plan              dispatchManifestPlan
	ExpectedWatermark int64
	Purpose           ArtifactPurpose
	Task              Task
	BeforeCASHook     func(context.Context, *dispatchManifestPlan) error
	PlannedHook       func(context.Context, dispatchManifestPlan) error
}

func persistDispatchManifestTx(ctx context.Context, tx pgx.Tx, in persistDispatchManifestInput) (dispatchManifestPlan, error) {
	reserved, err := reservePolicyWatermarkCASTx(ctx, tx, in.WorkspaceID, in.SessionID, in.ExpectedWatermark)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	plan := in.Plan
	plan.PolicyWatermark = reserved
	plan.NormalGrantID = uuid.NewString()
	plan.NormalGrantRevision = 1
	plan.Purpose = in.Purpose
	if plan.Purpose == "" {
		plan.Purpose = ArtifactPurposeTaskExecution
	}
	if plan.Purpose == ArtifactPurposeEvaluation {
		plan.EvaluationGrantID = uuid.NewString()
		plan.EvaluationGrantRevision = 1
	}
	if err := persistManifestGrantsTx(ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID, plan, reserved); err != nil {
		return dispatchManifestPlan{}, err
	}
	clearance, err := loadManifestNormalGrantClearanceTx(
		ctx, tx, in.WorkspaceID, in.SessionID, plan.NormalGrantID, plan.NormalGrantRevision, plan.Purpose,
	)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	authorized, err := NewArtifactContextModule().planDispatchManifestWithClearance(
		ctx, tx, in.WorkspaceID, in.SessionID, plan.ThroughStateVersion, clearance, plan.Purpose,
	)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	authorized = isolateDisputeReviewManifest(authorized, in.Task)
	authorized.NormalGrantID = plan.NormalGrantID
	authorized.NormalGrantRevision = plan.NormalGrantRevision
	authorized.EvaluationGrantID = plan.EvaluationGrantID
	authorized.EvaluationGrantRevision = plan.EvaluationGrantRevision
	authorized.Purpose = plan.Purpose
	authorized.PolicyWatermark = plan.PolicyWatermark
	plan = authorized
	dispatchAttempt, err := loadDispatchAttemptCandidateTx(
		ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID,
	)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	plan.Entries = append(plan.Entries, dispatchAttempt)
	sort.Slice(plan.Entries, func(i, j int) bool {
		if plan.Entries[i].Kind != plan.Entries[j].Kind {
			return plan.Entries[i].Kind < plan.Entries[j].Kind
		}
		return plan.Entries[i].ArtifactID < plan.Entries[j].ArtifactID
	})
	if err = freezeArtifactRepresentationsTx(ctx, tx, in.WorkspaceID, in.SessionID, plan.Entries); err != nil {
		return dispatchManifestPlan{}, err
	}
	if in.PlannedHook != nil {
		if err = in.PlannedHook(ctx, plan); err != nil {
			return dispatchManifestPlan{}, err
		}
	}
	plan.ManifestHash = hashDispatchManifest(dispatchManifestHashInput{
		WorkspaceID:         in.WorkspaceID,
		SessionID:           in.SessionID,
		AttemptID:           in.AttemptID,
		TaskID:              in.TaskID,
		Purpose:             plan.Purpose,
		PolicyVersion:       plan.PolicyVersion,
		PolicyWatermark:     plan.PolicyWatermark,
		ThroughStateVersion: plan.ThroughStateVersion,
		Entries:             plan.Entries,
	})

	if err := registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            in.WorkspaceID,
		SessionID:              in.SessionID,
		EntityID:               plan.ManifestID,
		Kind:                   ArtifactKindContextManifest,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            plan.ManifestHash,
	}); err != nil {
		return dispatchManifestPlan{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_context_manifest (
		  id, workspace_id, session_id, attempt_id, task_id,
		  purpose, policy_version, policy_watermark, through_state_version,
		  normal_grant_id, normal_grant_revision, evaluation_grant_id, evaluation_grant_revision,
		  manifest_hash, omission_hash
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		  $6, $7, $8, $9, $10::uuid, $11, NULLIF($12, '')::uuid, NULLIF($13, 0), $14, $15
		)
	`, plan.ManifestID, in.WorkspaceID, in.SessionID, in.AttemptID, in.TaskID,
		plan.Purpose, plan.PolicyVersion, plan.PolicyWatermark, plan.ThroughStateVersion,
		plan.NormalGrantID, plan.NormalGrantRevision, plan.EvaluationGrantID, plan.EvaluationGrantRevision,
		plan.ManifestHash, plan.OmissionHash); err != nil {
		return dispatchManifestPlan{}, fmt.Errorf("insert context manifest: %w", err)
	}
	if in.BeforeCASHook != nil {
		if err := in.BeforeCASHook(ctx, &plan); err != nil {
			return dispatchManifestPlan{}, err
		}
	}

	if err := lockDispatchManifestCandidateRowsTx(ctx, tx, in.WorkspaceID, in.SessionID, plan.Entries); err != nil {
		return dispatchManifestPlan{}, err
	}
	if err := casArtifactRelationshipHashesTx(
		ctx, tx, in.WorkspaceID, in.SessionID, plan.Entries,
	); err != nil {
		return dispatchManifestPlan{}, err
	}

	for _, entry := range plan.Entries {
		if err := casPassportSelectionTx(
			ctx, tx, in.WorkspaceID, in.SessionID, entry.ArtifactID,
			entry.Kind, entry.Version, entry.EligibilityRevision, entry.Lifecycle, entry.Provenance,
		); err != nil {
			return dispatchManifestPlan{}, err
		}
		if err := casArtifactVersionSelectionTx(
			ctx, tx, in.WorkspaceID, in.SessionID, entry.VersionRowID,
			entry.ContentHash, entry.AccessLevel, entry.RepresentationBytes, entry.RepresentationHash,
		); err != nil {
			return dispatchManifestPlan{}, err
		}
	}
	for ordinal, entry := range plan.Entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_artifact_context_entry (
			  workspace_id, session_id, manifest_id, ordinal,
			  artifact_version_id, eligibility_revision,
			  representation, representation_bytes, representation_hash, use_kind,
			  selection_lifecycle_status, selection_provenance_completeness,
			  selection_version_count, selection_input_reference_count, selection_output_reference_count,
			  selection_relationship_hash
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4,
			  $5::uuid, $6,
			  $7, $8, $9, 'input', $10, $11, $12, $13, $14, $15
			)
		`, in.WorkspaceID, in.SessionID, plan.ManifestID, ordinal,
			entry.VersionRowID, entry.EligibilityRevision,
			entry.Representation, entry.RepresentationBytes, entry.RepresentationHash,
			entry.Lifecycle, entry.Provenance, entry.VersionCount,
			entry.InputReferenceCount, entry.OutputReferenceCount, entry.RelationshipHash); err != nil {
			return dispatchManifestPlan{}, fmt.Errorf("insert manifest entry ordinal=%d: %w", ordinal, err)
		}
	}
	for ordinal, omission := range plan.Omissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_artifact_context_omission (
			  workspace_id, session_id, manifest_id, candidate_version_id, ordinal, reason
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6)
		`, in.WorkspaceID, in.SessionID, plan.ManifestID, omission.VersionRowID, ordinal,
			omission.OmissionReason); err != nil {
			return dispatchManifestPlan{}, fmt.Errorf("insert manifest omission ordinal=%d: %w", ordinal, err)
		}
	}
	manifestVersionRowID, err := loadManifestVersionRowIDTx(ctx, tx, in.WorkspaceID, in.SessionID, plan.ManifestID)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	if err = persistManifestInputReferencesTx(ctx, tx, in.WorkspaceID, in.SessionID, plan.ManifestID, manifestVersionRowID); err != nil {
		return dispatchManifestPlan{}, err
	}
	return plan, nil
}

func loadManifestNormalGrantClearanceTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, grantID string,
	grantRevision int64,
	purpose ArtifactPurpose,
) (ArtifactClearance, error) {
	var clearance string
	err := tx.QueryRow(ctx, `
		SELECT normal_clearance
		FROM research_artifact_policy_grant
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		  AND revision = $4
		  AND status = 'active'
		  AND purpose = $5
		  AND evaluation_private = false
	`, workspaceID, sessionID, grantID, grantRevision, purpose).Scan(&clearance)
	if err != nil {
		return "", fmt.Errorf("load task execution grant: %w", err)
	}
	parsed := ArtifactClearance(clearance)
	if parsed != ArtifactClearanceVerifiedOnly && parsed != ArtifactClearanceRedacted && parsed != ArtifactClearanceRaw {
		return "", fmt.Errorf("%w: unknown task execution clearance", ErrInvalidContract)
	}
	return parsed, nil
}

func persistManifestGrantsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
	plan dispatchManifestPlan,
	watermark int64,
) error {
	if plan.NormalGrantID == "" || plan.NormalGrantRevision != 1 {
		return fmt.Errorf("%w: invalid task execution grant", ErrInvalidTransition)
	}
	var principalID string
	if err := tx.QueryRow(ctx, `
		SELECT assigned_agent_id::text
		FROM research_task_attempt
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&principalID); err != nil {
		return fmt.Errorf("resolve task execution grant principal: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_grant (
		  id, workspace_id, session_id, principal_kind, principal_id, purpose,
		  normal_clearance, evaluation_private, policy_version, revision, status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'agent', $4::uuid, $5,
		  $6, false, $7, $8, 'active'
		)
	`, plan.NormalGrantID, workspaceID, sessionID, principalID,
		plan.Purpose, defaultTaskExecutionClearance(), plan.PolicyVersion, plan.NormalGrantRevision); err != nil {
		return fmt.Errorf("insert task execution grant: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, $5, 'active')
	`, workspaceID, sessionID, watermark, plan.NormalGrantID, plan.NormalGrantRevision); err != nil {
		return fmt.Errorf("record task execution grant: %w", err)
	}
	if plan.EvaluationGrantID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO research_artifact_policy_grant (id,workspace_id,session_id,principal_kind,principal_id,purpose,normal_clearance,evaluation_private,policy_version,revision,status) VALUES ($1::uuid,$2::uuid,$3::uuid,'agent',$4::uuid,$5,NULL,true,$6,$7,'active')`, plan.EvaluationGrantID, workspaceID, sessionID, principalID, plan.Purpose, plan.PolicyVersion, plan.EvaluationGrantRevision); err != nil {
			return fmt.Errorf("insert evaluation grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO research_artifact_policy_mutation (workspace_id,session_id,watermark,mutation_kind,policy_grant_id,old_grant_revision,new_grant_revision,new_grant_status) VALUES ($1::uuid,$2::uuid,$3,'grant_create',$4::uuid,0,$5,'active')`, workspaceID, sessionID, watermark, plan.EvaluationGrantID, plan.EvaluationGrantRevision); err != nil {
			return fmt.Errorf("record evaluation grant: %w", err)
		}
	}
	return nil
}

func lockDispatchManifestCandidateRowsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	entries []artifactVersionCandidate,
) error {
	if len(entries) == 0 {
		return nil
	}
	sorted := append([]artifactVersionCandidate(nil), entries...)
	sortManifestEntryCandidates(sorted)
	for _, entry := range sorted {
		if err := lockArtifactPassportRowTx(ctx, tx, workspaceID, sessionID, entry.ArtifactID); err != nil {
			return err
		}
	}
	versionRowIDs := make([]string, 0, len(sorted))
	for _, entry := range sorted {
		versionRowIDs = append(versionRowIDs, entry.VersionRowID)
	}
	sort.Strings(versionRowIDs)
	for _, versionRowID := range versionRowIDs {
		if err := lockArtifactVersionRowTx(ctx, tx, workspaceID, sessionID, versionRowID); err != nil {
			return err
		}
	}
	return nil
}

func persistManifestGateSnapshotTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, manifestID string,
	gate GateResult,
) error {
	encoded, err := json.Marshal(gate)
	if err != nil {
		return fmt.Errorf("encode manifest gate snapshot: %w", err)
	}
	hash := contentHashFromPayload(encoded)
	members, err := listFleetMembersTx(ctx, tx, sessionID, workspaceID)
	if err != nil {
		return err
	}
	principalBytes, err := encodeManifestPrincipalHeader(members)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET principal_header_bytes = $4,
		    principal_header_hash = $5,
		    gate_snapshot_bytes = $6,
		    gate_snapshot_hash = $7
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
	`, workspaceID, sessionID, manifestID, principalBytes, contentHashFromPayload(principalBytes), encoded, hash)
	return err
}

func loadManifestGateSnapshotPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) (GateResult, bool, error) {
	var raw []byte
	var storedHash string
	err := pool.QueryRow(ctx, `
		SELECT gate_snapshot_bytes, gate_snapshot_hash
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&raw, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return GateResult{}, false, nil
	}
	if err != nil {
		return GateResult{}, false, err
	}
	if len(raw) == 0 {
		if storedHash != "" {
			return GateResult{}, false, fmt.Errorf("%w: empty frozen gate snapshot has a hash", ErrInvalidTransition)
		}
		return GateResult{}, false, nil
	}
	if storedHash == "" || storedHash != contentHashFromPayload(raw) {
		return GateResult{}, false, fmt.Errorf("%w: frozen gate snapshot hash mismatch", ErrInvalidTransition)
	}
	var gate GateResult
	if err = json.Unmarshal(raw, &gate); err != nil {
		return GateResult{}, false, fmt.Errorf("decode manifest gate snapshot: %w", err)
	}
	return gate, true, nil
}

func loadAttemptManifestSummary(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) (manifestID, manifestHash string, policyWatermark int64, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT id::text, manifest_hash, policy_watermark
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID, &manifestHash, &policyWatermark)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	return manifestID, manifestHash, policyWatermark, true, nil
}

func persistAcceptedResultArtifactTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, taskID, attemptID, orchestratorVersion string,
	result ResultEnvelope,
	resultJSON []byte,
	contentHash string,
	policyWatermark int64,
	accessLevel ArtifactAccessLevel,
) (string, error) {
	resultID := uuid.NewString()
	passportSchemaVersion := "legacy-v1"
	if orchestratorVersion == OrchestratorVersionV6 {
		passportSchemaVersion = OrchestratorVersionV6
	}
	if err := registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               resultID,
		Kind:                   ArtifactKindResultArtifact,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            accessLevel,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		ProducedByTaskID:       taskID,
		ProducedByAttemptID:    attemptID,
		SourceCreatedAt:        timePtr(time.Now()),
		SchemaName:             string(ArtifactKindResultArtifact),
		SchemaVersion:          passportSchemaVersion,
	}); err != nil {
		return "", err
	}
	var manifestID, manifestHash string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, manifest_hash
		FROM research_artifact_context_manifest
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID, &manifestHash); err != nil {
		return "", err
	}
	inputVersionSetHash, err := manifestInputVersionSetHashTx(ctx, tx, workspaceID, sessionID, manifestID)
	if err != nil {
		return "", err
	}
	if err := insertResultArtifactRowTx(
		ctx, tx, resultID, workspaceID, sessionID, attemptID, orchestratorVersion,
		result, resultJSON, contentHash, policyWatermark,
		manifestID, manifestHash, inputVersionSetHash,
	); err != nil {
		return "", err
	}
	return resultID, nil
}

func normalizeArtifactContentHash(contentHash string) string {
	if strings.HasPrefix(contentHash, "sha256:") {
		return contentHash
	}
	return "sha256:" + contentHash
}

func insertResultArtifactRowTx(
	ctx context.Context,
	tx pgx.Tx,
	resultID, workspaceID, sessionID, attemptID, orchestratorVersion string,
	result ResultEnvelope,
	resultJSON []byte,
	contentHash string,
	policyWatermark int64,
	manifestID, manifestHash, inputVersionSetHash string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO research_result_artifact (
		  id, workspace_id, session_id, attempt_id,
		  orchestrator_version, result_schema_version, result,
		  client_request_id, content_hash, acceptance_policy_watermark, accepted_at,
		  manifest_id, manifest_hash, input_version_set_hash,
		  acceptance_manifest_id, acceptance_manifest_hash,
		  resolved_input_versions, acceptance_lineage
		)
		SELECT
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
		  $5, $6, $7::jsonb, $8, $9, $10, now(),
		  $11::uuid, $12, $13,
		  $11::uuid, $12,
		  COALESCE((
		    SELECT jsonb_agg(version_id ORDER BY version_id)
		    FROM (
		      SELECT DISTINCT entry.artifact_version_id::text AS version_id
		      FROM research_artifact_context_entry entry
		      WHERE entry.workspace_id = $2::uuid
		        AND entry.session_id = $3::uuid
		        AND entry.manifest_id = $11::uuid
		    ) versions
		  ), '[]'::jsonb),
		  COALESCE((
		    SELECT jsonb_agg(jsonb_build_object(
		      'input_version_id', entry.artifact_version_id::text,
		      'relation', 'acceptance_input',
		      'manifest_id', entry.manifest_id::text,
		      'explicitly_used', true,
		      'purpose', 'result_acceptance',
		      'ordinal', entry.ordinal
		    ) ORDER BY entry.ordinal, entry.artifact_version_id::text)
		    FROM research_artifact_context_entry entry
		    WHERE entry.workspace_id = $2::uuid
		      AND entry.session_id = $3::uuid
		      AND entry.manifest_id = $11::uuid
		  ), '[]'::jsonb)
		ON CONFLICT (workspace_id, session_id, attempt_id) DO NOTHING
	`, resultID, workspaceID, sessionID, attemptID,
		orchestratorVersion, fmt.Sprintf("%d", result.SchemaVersion), resultJSON,
		result.ClientRequestID, contentHash, policyWatermark,
		manifestID, manifestHash, inputVersionSetHash)
	return err
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func (s *PostgresStore) AttemptHasDispatchManifest(ctx context.Context, sessionID, workspaceID, attemptID string) (bool, error) {
	_, _, _, ok, err := loadAttemptManifestSummaryPool(ctx, s.pool, workspaceID, sessionID, attemptID)
	return ok, err
}

func (s *PostgresStore) SessionArtifactPassportEnabled(ctx context.Context, sessionID, workspaceID string) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT artifact_passport_enabled
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, sessionID, workspaceID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRunNotFound
	}
	return enabled, err
}

func sessionArtifactPassportEnabled(
	ctx context.Context,
	tx pgx.Tx,
	sessionID, workspaceID string,
) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT artifact_passport_enabled
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, sessionID, workspaceID).Scan(&enabled)
	return enabled, err
}
