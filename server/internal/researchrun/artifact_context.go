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
	VersionRowID        string
	ArtifactID          string
	Kind                ArtifactEntityKind
	Version             int32
	EligibilityRevision int64
	AccessLevel         ArtifactAccessLevel
	Lifecycle           ArtifactLifecycleStatus
	Provenance          ArtifactProvenanceCompleteness
	ContentHash         string
	Representation      string
	RepresentationBytes []byte
	RepresentationHash  string
	OmissionReason      string
}

type dispatchManifestPlan struct {
	ManifestID          string
	Entries             []artifactVersionCandidate
	Omissions           []artifactVersionCandidate
	PolicyWatermark     int64
	ThroughStateVersion int64
	ManifestHash        string
}

func (m ArtifactContextModule) PlanDispatchManifest(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	stateVersion int64,
) (dispatchManifestPlan, error) {
	candidates, err := loadArtifactVersionCandidates(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	clearance := defaultTaskExecutionClearance()
	purpose := manifestPurposeForTask()

	var entries []artifactVersionCandidate
	var omissions []artifactVersionCandidate
	for _, candidate := range candidates {
		if m.policy.EvaluationPrivateKind(candidate.Kind) && purpose == ArtifactPurposeTaskExecution {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(ArtifactDenyEvaluationCompartment)
			omissions = append(omissions, candidate)
			continue
		}
		admitted, deny := m.policy.LegacyAdmissionAllowed(
			candidate.Kind, candidate.Lifecycle, candidate.Provenance,
		)
		if !admitted {
			candidate.OmissionReason = m.policy.ManifestOmissionReason(deny)
			omissions = append(omissions, candidate)
			continue
		}
		allowed, deny := m.policy.CanReadNormal(
			clearance, candidate.AccessLevel, purpose, false,
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
	}, nil
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
			"%s:%d:%d:%s:%s",
			entry.ArtifactID, entry.Version, entry.EligibilityRevision,
			entry.Representation, entry.RepresentationHash,
		))
	}
	sort.Strings(parts)
	payload := strings.Join(parts, "\n")
	sum := sha256.Sum256([]byte(payload))
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
		  v.content_hash
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
			&accessRaw, &lifecycleRaw, &provenanceRaw, &candidate.ContentHash,
		); err != nil {
			return nil, err
		}
		kind, parseErr := ParseArtifactEntityKind(kindRaw)
		if parseErr != nil {
			continue
		}
		candidate.Kind = kind
		candidate.AccessLevel = ArtifactAccessLevel(accessRaw)
		candidate.Lifecycle = ArtifactLifecycleStatus(lifecycleRaw)
		candidate.Provenance = ArtifactProvenanceCompleteness(provenanceRaw)
		out = append(out, candidate)
	}
	return out, rows.Err()
}

type persistDispatchManifestInput struct {
	WorkspaceID       string
	SessionID         string
	AttemptID         string
	TaskID            string
	Plan              dispatchManifestPlan
	ExpectedWatermark int64
}

func persistDispatchManifestTx(ctx context.Context, tx pgx.Tx, in persistDispatchManifestInput) (dispatchManifestPlan, error) {
	reserved, err := reservePolicyWatermarkCASTx(ctx, tx, in.WorkspaceID, in.SessionID, in.ExpectedWatermark)
	if err != nil {
		return dispatchManifestPlan{}, err
	}
	plan := in.Plan
	plan.PolicyWatermark = reserved

	if err := registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            in.WorkspaceID,
		SessionID:              in.SessionID,
		EntityID:               plan.ManifestID,
		Kind:                   ArtifactKindContextManifest,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
	}); err != nil {
		return dispatchManifestPlan{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_context_manifest (
		  id, workspace_id, session_id, attempt_id, task_id,
		  purpose, policy_version, policy_watermark, through_state_version,
		  manifest_hash
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		  'task_execution', $6, $7, $8, $9
		)
	`, plan.ManifestID, in.WorkspaceID, in.SessionID, in.AttemptID, in.TaskID,
		LegacyV1V5CompatPolicy, plan.PolicyWatermark, plan.ThroughStateVersion,
		plan.ManifestHash); err != nil {
		return dispatchManifestPlan{}, fmt.Errorf("insert context manifest: %w", err)
	}

	if err := lockDispatchManifestCandidateRowsTx(ctx, tx, in.WorkspaceID, in.SessionID, plan.Entries); err != nil {
		return dispatchManifestPlan{}, err
	}

	for _, entry := range plan.Entries {
		if err := casPassportEligibilityRevisionTx(
			ctx, tx, in.WorkspaceID, in.SessionID, entry.ArtifactID,
			entry.Version, entry.EligibilityRevision, entry.Lifecycle,
		); err != nil {
			return dispatchManifestPlan{}, err
		}
		if err := casArtifactVersionRepresentationTx(
			ctx, tx, in.WorkspaceID, in.SessionID, entry.VersionRowID,
			entry.ContentHash, entry.RepresentationHash,
		); err != nil {
			return dispatchManifestPlan{}, err
		}
	}
	for ordinal, entry := range plan.Entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_artifact_context_entry (
			  workspace_id, session_id, manifest_id, ordinal,
			  artifact_version_id, eligibility_revision,
			  representation, representation_bytes, representation_hash, use_kind
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4,
			  $5::uuid, $6,
			  $7, $8, $9, 'input'
			)
		`, in.WorkspaceID, in.SessionID, plan.ManifestID, ordinal,
			entry.VersionRowID, entry.EligibilityRevision,
			entry.Representation, entry.RepresentationBytes, entry.RepresentationHash); err != nil {
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
	_, err = tx.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET principal_header_bytes = $4,
		    principal_header_hash = $5
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
	`, workspaceID, sessionID, manifestID, encoded, hash)
	return err
}

func loadManifestGateSnapshotPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) (GateResult, bool, error) {
	var raw []byte
	err := pool.QueryRow(ctx, `
		SELECT principal_header_bytes
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return GateResult{}, false, nil
	}
	if err != nil {
		return GateResult{}, false, err
	}
	if len(raw) == 0 {
		return GateResult{}, false, nil
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
	workspaceID, sessionID, attemptID, orchestratorVersion string,
	result ResultEnvelope,
	resultJSON []byte,
	contentHash string,
	policyWatermark int64,
) (string, error) {
	resultID := uuid.NewString()
	if err := registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               resultID,
		Kind:                   ArtifactKindResultArtifact,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		ProducedByAttemptID:    attemptID,
		SourceCreatedAt:        timePtr(time.Now()),
		SchemaName:             string(ArtifactKindResultArtifact),
		SchemaVersion:          "legacy-v1",
	}); err != nil {
		return "", err
	}
	if err := insertResultArtifactRowTx(
		ctx, tx, resultID, workspaceID, sessionID, attemptID, orchestratorVersion,
		result, resultJSON, contentHash, policyWatermark,
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
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO research_result_artifact (
		  id, workspace_id, session_id, attempt_id,
		  orchestrator_version, result_schema_version, result,
		  client_request_id, content_hash, acceptance_policy_watermark, accepted_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
		  $5, $6, $7::jsonb, $8, $9, $10, now()
		)
		ON CONFLICT (workspace_id, session_id, attempt_id) DO NOTHING
	`, resultID, workspaceID, sessionID, attemptID,
		orchestratorVersion, fmt.Sprintf("%d", result.SchemaVersion), resultJSON,
		result.ClientRequestID, contentHash, policyWatermark)
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
