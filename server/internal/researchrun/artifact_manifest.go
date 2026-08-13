package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func readPolicyWatermarkTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) (int64, error) {
	var watermark int64
	err := tx.QueryRow(ctx, `
		SELECT watermark
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID).Scan(&watermark)
	return watermark, err
}

func casPassportEligibilityRevisionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, artifactID string,
	currentVersion int32,
	eligibilityRevision int64,
	lifecycle ArtifactLifecycleStatus,
) error {
	// Passport has no updated_at column (318 schema). CAS is a pure predicate
	// match: rewrite a column to itself so RowsAffected reports the lock result.
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		  AND current_version = $4
		  AND eligibility_revision = $5
		  AND lifecycle_status = $6
	`, workspaceID, sessionID, artifactID, currentVersion, eligibilityRevision, lifecycle)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: passport eligibility CAS failed", ErrInvalidTransition)
	}
	return nil
}

func casArtifactVersionRepresentationTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, versionRowID string,
	contentHash, representationHash string,
) error {
	var matched bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM research_artifact_version
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		  AND content_hash = $4
	`, workspaceID, sessionID, versionRowID, contentHash).Scan(&matched)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: artifact version representation CAS failed", ErrInvalidTransition)
	}
	if err != nil {
		return err
	}
	_ = representationHash
	return nil
}

func reservePolicyWatermarkCASTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, expected int64) (int64, error) {
	var current int64
	err := tx.QueryRow(ctx, `
		SELECT watermark
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		FOR UPDATE
	`, workspaceID, sessionID).Scan(&current)
	if err != nil {
		return 0, err
	}
	if current != expected {
		return 0, fmt.Errorf("%w: policy watermark CAS failed", ErrInvalidTransition)
	}
	var reserved int64
	err = tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&reserved)
	if err != nil {
		return 0, err
	}
	return reserved, nil
}

func loadManifestVersionRowIDTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, manifestID string,
) (string, error) {
	var versionRowID string
	err := tx.QueryRow(ctx, `
		SELECT v.id::text
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON v.workspace_id = p.workspace_id
		 AND v.session_id = p.session_id
		 AND v.artifact_id = p.id
		 AND v.version = p.current_version
		WHERE p.workspace_id = $1::uuid
		  AND p.session_id = $2::uuid
		  AND p.id = $3::uuid
	`, workspaceID, sessionID, manifestID).Scan(&versionRowID)
	return versionRowID, err
}

func persistManifestInputReferencesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, manifestID, manifestVersionRowID string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_input_reference (
		  workspace_id, session_id, consumer_version_id, input_version_id,
		  relation, manifest_id, explicitly_used, purpose, ordinal
		)
		SELECT
		  e.workspace_id, e.session_id, $4::uuid, e.artifact_version_id,
		  'manifest_input', e.manifest_id, true, 'task_execution', e.ordinal
		FROM research_artifact_context_entry e
		WHERE e.workspace_id = $1::uuid
		  AND e.session_id = $2::uuid
		  AND e.manifest_id = $3::uuid
		ON CONFLICT (workspace_id, session_id, consumer_version_id, input_version_id, relation) DO NOTHING
	`, workspaceID, sessionID, manifestID, manifestVersionRowID)
	return err
}

func persistResultArtifactInputReferencesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID, resultVersionRowID string,
) error {
	var manifestID string
	err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_input_reference (
		  workspace_id, session_id, consumer_version_id, input_version_id,
		  relation, manifest_id, explicitly_used, purpose, ordinal
		)
		SELECT
		  e.workspace_id, e.session_id, $4::uuid, e.artifact_version_id,
		  'acceptance_input', e.manifest_id, true, 'result_acceptance', e.ordinal
		FROM research_artifact_context_entry e
		WHERE e.workspace_id = $1::uuid
		  AND e.session_id = $2::uuid
		  AND e.manifest_id = $3::uuid
		ON CONFLICT (workspace_id, session_id, consumer_version_id, input_version_id, relation) DO NOTHING
	`, workspaceID, sessionID, manifestID, resultVersionRowID)
	return err
}

func manifestInputVersionSetHashTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, manifestID string,
) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT artifact_version_id::text
		FROM research_artifact_context_entry
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND manifest_id=$3::uuid
		ORDER BY artifact_version_id::text
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return contentHashFromPayload([]byte(strings.Join(ids, "\n"))), nil
}

type manifestArtifactSet struct {
	ArtifactIDs map[string]struct{}
	Hash        string
}

func loadManifestArtifactSetTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, manifestID string,
) (manifestArtifactSet, error) {
	rows, err := tx.Query(ctx, `
		SELECT v.artifact_id::text
		FROM research_artifact_context_entry e
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		WHERE e.workspace_id = $1::uuid
		  AND e.session_id = $2::uuid
		  AND e.manifest_id = $3::uuid
		ORDER BY v.artifact_id::text
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return manifestArtifactSet{}, err
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return manifestArtifactSet{}, err
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return manifestArtifactSet{}, err
	}
	var hash string
	err = tx.QueryRow(ctx, `
		SELECT manifest_hash
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, manifestID).Scan(&hash)
	if err != nil {
		return manifestArtifactSet{}, err
	}
	return manifestArtifactSet{ArtifactIDs: ids, Hash: hash}, nil
}

func compareShadowManifestError(
	liveIDs map[string]struct{},
	manifest manifestArtifactSet,
) error {
	if len(liveIDs) == len(manifest.ArtifactIDs) {
		match := true
		for id := range liveIDs {
			if _, ok := manifest.ArtifactIDs[id]; !ok {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"live_artifact_ids":     sortedKeys(liveIDs),
		"manifest_artifact_ids": sortedKeys(manifest.ArtifactIDs),
		"manifest_hash":         manifest.Hash,
	})
	return fmt.Errorf("%w: manifest shadow mismatch: %s", ErrInvalidTransition, string(payload))
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func loadManifestAuthorizedArtifactIDsPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) (map[string]struct{}, bool, error) {
	var manifestID string
	err := pool.QueryRow(ctx, `
		SELECT id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err = verifyAttemptManifestReadGrantPool(ctx, pool, workspaceID, sessionID, attemptID); err != nil {
		return nil, false, err
	}
	rows, err := pool.Query(ctx, `
		SELECT v.artifact_id::text
		FROM research_artifact_context_entry e
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		WHERE e.workspace_id = $1::uuid
		  AND e.session_id = $2::uuid
		  AND e.manifest_id = $3::uuid
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return ids, true, nil
}

func verifyAttemptManifestReadGrantPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) error {
	var authorized bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_artifact_context_manifest m
		  JOIN research_task_attempt a
		    ON (a.workspace_id, a.session_id, a.id, a.task_id) =
		       (m.workspace_id, m.session_id, m.attempt_id, m.task_id)
		  JOIN research_artifact_policy_grant normal
		    ON (normal.workspace_id, normal.session_id, normal.id, normal.revision) =
		       (m.workspace_id, m.session_id, m.normal_grant_id, m.normal_grant_revision)
		  LEFT JOIN research_artifact_policy_grant evaluation
		    ON (evaluation.workspace_id, evaluation.session_id, evaluation.id, evaluation.revision) =
		       (m.workspace_id, m.session_id, m.evaluation_grant_id, m.evaluation_grant_revision)
		  WHERE m.workspace_id = $1::uuid
		    AND m.session_id = $2::uuid
		    AND m.attempt_id = $3::uuid
		    AND normal.principal_kind = 'agent'
		    AND normal.principal_id = a.assigned_agent_id
		    AND normal.purpose = m.purpose
		    AND normal.policy_version = m.policy_version
		    AND normal.status = 'active'
		    AND normal.evaluation_private = false
		    AND normal.normal_clearance IS NOT NULL
		    AND research_artifact_access_level_allowed(normal.normal_clearance)
		    AND (
		      (m.purpose = 'task_execution' AND m.evaluation_grant_id IS NULL)
		      OR
		      (m.purpose = 'evaluation'
		       AND evaluation.id IS NOT NULL
		       AND evaluation.principal_kind = 'agent'
		       AND evaluation.principal_id = a.assigned_agent_id
		       AND evaluation.purpose = m.purpose
		       AND evaluation.policy_version = m.policy_version
		       AND evaluation.status = 'active'
		       AND evaluation.evaluation_private = true)
		    )
		)
	`, workspaceID, sessionID, attemptID).Scan(&authorized)
	if err != nil {
		return fmt.Errorf("verify attempt manifest read grant: %w", err)
	}
	if !authorized {
		return fmt.Errorf("%w: attempt manifest grant no longer authorizes task context", ErrInvalidTransition)
	}
	return nil
}

func loadAttemptManifestSummaryPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) (manifestID, manifestHash string, policyWatermark int64, ok bool, err error) {
	err = pool.QueryRow(ctx, `
		SELECT id::text, manifest_hash, policy_watermark
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID, &manifestHash, &policyWatermark)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", "", 0, false, nil
		}
		return "", "", 0, false, err
	}
	return manifestID, manifestHash, policyWatermark, true, nil
}

func filterRunSnapshotByManifest(snapshot RunSnapshot, allowed map[string]struct{}) RunSnapshot {
	filtered := snapshot
	filtered.Sources = filterSourcesByManifest(snapshot.Sources, allowed)
	filtered.Observations = filterObservationsByManifest(snapshot.Observations, allowed)
	filtered.Claims = filterClaimsByManifest(snapshot.Claims, allowed)
	return filtered
}

func filterSourcesByManifest(sources []SourceSnapshotView, allowed map[string]struct{}) []SourceSnapshotView {
	out := make([]SourceSnapshotView, 0, len(sources))
	for _, s := range sources {
		if _, ok := allowed[s.ID]; ok {
			out = append(out, s)
		}
	}
	return out
}

func filterObservationsByManifest(obs []Observation, allowed map[string]struct{}) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if _, ok := allowed[o.ID]; ok {
			out = append(out, o)
		}
	}
	return out
}

func filterClaimsByManifest(claims []Claim, allowed map[string]struct{}) []Claim {
	out := make([]Claim, 0, len(claims))
	for _, c := range claims {
		if _, ok := allowed[c.ID]; ok {
			out = append(out, c)
		}
	}
	return out
}

func collectLiveTaskContextArtifactIDs(snapshot RunSnapshot) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, s := range snapshot.Sources {
		ids[s.ID] = struct{}{}
	}
	for _, o := range snapshot.Observations {
		ids[o.ID] = struct{}{}
	}
	for _, c := range snapshot.Claims {
		ids[c.ID] = struct{}{}
	}
	return ids
}

func verifyShadowEquivalenceTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	stateVersion int64,
) error {
	module := NewArtifactContextModule()
	plan, err := module.PlanDispatchManifest(ctx, tx, workspaceID, sessionID, stateVersion)
	if err != nil {
		return err
	}
	liveIDs, err := loadLegacyManifestVisibleArtifactIDsTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return err
	}
	manifestIDs := make(map[string]struct{}, len(plan.Entries))
	for _, entry := range plan.Entries {
		manifestIDs[entry.ArtifactID] = struct{}{}
	}
	return compareShadowManifestError(liveIDs, manifestArtifactSet{
		ArtifactIDs: manifestIDs,
		Hash:        plan.ManifestHash,
	})
}

func loadLegacyManifestVisibleArtifactIDsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
) (map[string]struct{}, error) {
	candidates, err := loadArtifactVersionCandidates(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	module := NewArtifactContextModule()
	clearance := defaultTaskExecutionClearance()
	purpose := manifestPurposeForTask()
	ids := make(map[string]struct{})
	for _, candidate := range candidates {
		if module.policy.EvaluationPrivateKind(candidate.Kind) && purpose == ArtifactPurposeTaskExecution {
			continue
		}
		admitted, _ := module.policy.LegacyAdmissionAllowed(
			candidate.Kind, candidate.Lifecycle, candidate.Provenance,
		)
		if !admitted {
			continue
		}
		allowed, _ := module.policy.CanReadNormal(
			clearance, candidate.AccessLevel, purpose, false,
		)
		if !allowed {
			continue
		}
		ids[candidate.ArtifactID] = struct{}{}
	}
	return ids, nil
}

func verifyAcceptanceManifestPolicyTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) (int64, int64, error) {
	manifestID, _, manifestWatermark, ok, err := loadAttemptManifestSummary(ctx, tx, workspaceID, sessionID, attemptID)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, fmt.Errorf("%w: acceptance requires dispatch manifest", ErrInvalidTransition)
	}
	if strings.TrimSpace(manifestID) == "" {
		return 0, 0, fmt.Errorf("%w: acceptance manifest missing", ErrInvalidTransition)
	}
	currentWatermark, err := readPolicyWatermarkTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return 0, 0, err
	}
	if manifestWatermark > currentWatermark {
		return 0, 0, fmt.Errorf("%w: manifest policy watermark ahead of session state", ErrInvalidTransition)
	}
	reserved, err := reservePolicyWatermarkCASTx(ctx, tx, workspaceID, sessionID, currentWatermark)
	if err != nil {
		return 0, 0, err
	}
	if err = verifyAcceptanceManifestGrantsTx(ctx, tx, workspaceID, sessionID, attemptID); err != nil {
		return 0, 0, err
	}
	return manifestWatermark, reserved, nil
}

type acceptanceManifestGrantBinding struct {
	purpose                 string
	policyVersion           string
	normalGrantID           string
	normalGrantRevision     int64
	evaluationGrantID       string
	evaluationGrantRevision int64
	assignedAgentID         string
	fleetID                 string
}

func acceptanceManifestGrantShapeAllowed(purpose, normalGrantID, evaluationGrantID string) bool {
	switch ArtifactPurpose(strings.TrimSpace(purpose)) {
	case ArtifactPurposeTaskExecution:
		return normalGrantID != "" && evaluationGrantID == ""
	case ArtifactPurposeEvaluation:
		return normalGrantID != "" && evaluationGrantID != ""
	default:
		return false
	}
}

func verifyAcceptanceManifestGrantsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) error {
	var binding acceptanceManifestGrantBinding
	err := tx.QueryRow(ctx, `
		SELECT m.purpose, m.policy_version,
		       COALESCE(m.normal_grant_id::text, ''), COALESCE(m.normal_grant_revision, 0),
		       COALESCE(m.evaluation_grant_id::text, ''), COALESCE(m.evaluation_grant_revision, 0),
		       COALESCE(a.assigned_agent_id::text, ''), s.fleet_id::text
		FROM research_artifact_context_manifest m
		JOIN research_task_attempt a
		  ON a.workspace_id = m.workspace_id
		 AND a.session_id = m.session_id
		 AND a.id = m.attempt_id
		 AND a.task_id = m.task_id
		JOIN research_session s
		  ON s.workspace_id = m.workspace_id
		 AND s.id = m.session_id
		WHERE m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(
		&binding.purpose, &binding.policyVersion,
		&binding.normalGrantID, &binding.normalGrantRevision,
		&binding.evaluationGrantID, &binding.evaluationGrantRevision,
		&binding.assignedAgentID, &binding.fleetID,
	)
	if err != nil {
		return fmt.Errorf("load acceptance manifest grant binding: %w", err)
	}
	if binding.assignedAgentID == "" || !acceptanceManifestGrantShapeAllowed(
		binding.purpose, binding.normalGrantID, binding.evaluationGrantID,
	) {
		return fmt.Errorf("%w: acceptance manifest grant binding is invalid", ErrInvalidTransition)
	}
	if err = verifyAcceptanceGrantTx(ctx, tx, workspaceID, sessionID,
		binding.normalGrantID, binding.normalGrantRevision, binding.assignedAgentID,
		binding.purpose, binding.policyVersion, false,
	); err != nil {
		return err
	}
	if binding.evaluationGrantID != "" {
		if err = verifyAcceptanceGrantTx(ctx, tx, workspaceID, sessionID,
			binding.evaluationGrantID, binding.evaluationGrantRevision, binding.assignedAgentID,
			binding.purpose, binding.policyVersion, true,
		); err != nil {
			return err
		}
	}

	var memberID string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM research_fleet_member
		WHERE workspace_id = $1::uuid
		  AND fleet_id = $2::uuid
		  AND agent_id = $3::uuid
		  AND status = 'active'
		FOR UPDATE
	`, workspaceID, binding.fleetID, binding.assignedAgentID).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: assigned agent is not an active fleet member", ErrInvalidTransition)
	}
	if err != nil {
		return fmt.Errorf("lock acceptance fleet membership: %w", err)
	}
	return nil
}

func verifyAcceptanceGrantTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, grantID string,
	expectedRevision int64,
	expectedPrincipalID, expectedPurpose, expectedPolicyVersion string,
	expectedEvaluationPrivate bool,
) error {
	var principalKind, principalID, purpose, policyVersion, status string
	var revision int64
	var evaluationPrivate, clearanceAllowed bool
	err := tx.QueryRow(ctx, `
		SELECT principal_kind, principal_id::text, purpose, policy_version,
		       revision, status, evaluation_private,
		       normal_clearance IS NOT NULL
		         AND research_artifact_access_level_allowed(normal_clearance)
		FROM research_artifact_policy_grant
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, grantID).Scan(
		&principalKind, &principalID, &purpose, &policyVersion,
		&revision, &status, &evaluationPrivate, &clearanceAllowed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: acceptance policy grant is missing", ErrInvalidTransition)
	}
	if err != nil {
		return fmt.Errorf("lock acceptance policy grant: %w", err)
	}
	if principalKind != "agent" || principalID != expectedPrincipalID ||
		purpose != expectedPurpose || policyVersion != expectedPolicyVersion ||
		revision != expectedRevision || status != "active" ||
		evaluationPrivate != expectedEvaluationPrivate ||
		(!expectedEvaluationPrivate && !clearanceAllowed) {
		return fmt.Errorf("%w: acceptance policy grant no longer authorizes the assigned agent", ErrInvalidTransition)
	}
	return nil
}

func verifyAcceptanceManifestEntryEligibilityTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) error {
	var stale bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_artifact_context_entry e
		  JOIN research_artifact_context_manifest m
		    ON m.workspace_id = e.workspace_id
		   AND m.session_id = e.session_id
		   AND m.id = e.manifest_id
		  JOIN research_artifact_version v
		    ON v.workspace_id = e.workspace_id
		   AND v.session_id = e.session_id
		   AND v.id = e.artifact_version_id
		  JOIN research_artifact_passport p
		    ON p.workspace_id = v.workspace_id
		   AND p.session_id = v.session_id
		   AND p.id = v.artifact_id
		  WHERE m.workspace_id = $1::uuid
		    AND m.session_id = $2::uuid
		    AND m.attempt_id = $3::uuid
		    AND (
		      e.eligibility_revision <> p.eligibility_revision
		      OR p.lifecycle_status NOT IN ('registered', 'accepted')
		    )
		)
	`, workspaceID, sessionID, attemptID).Scan(&stale)
	if err != nil {
		return err
	}
	if stale {
		return fmt.Errorf("%w: acceptance manifest entry stale", ErrInvalidTransition)
	}
	rows, err := tx.Query(ctx, `
		SELECT e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON (m.workspace_id, m.session_id, m.id) = (e.workspace_id, e.session_id, e.manifest_id)
		WHERE m.workspace_id = $1::uuid AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var representation []byte
		var storedHash string
		if err = rows.Scan(&representation, &storedHash); err != nil {
			return err
		}
		if contentHashFromPayload(representation) != storedHash {
			return fmt.Errorf("%w: acceptance manifest representation hash mismatch", ErrInvalidTransition)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return nil
}

func loadManifestEntryCandidatesForAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) ([]artifactVersionCandidate, dispatchManifestHashInput, string, error) {
	var hashInput dispatchManifestHashInput
	var purposeRaw string
	var storedHash string
	err := tx.QueryRow(ctx, `
		SELECT workspace_id::text, session_id::text, attempt_id::text, task_id::text,
		       purpose, policy_version, policy_watermark, through_state_version,
		       manifest_hash
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(
		&hashInput.WorkspaceID, &hashInput.SessionID, &hashInput.AttemptID, &hashInput.TaskID,
		&purposeRaw, &hashInput.PolicyVersion, &hashInput.PolicyWatermark,
		&hashInput.ThroughStateVersion, &storedHash,
	)
	if err != nil {
		return nil, dispatchManifestHashInput{}, "", err
	}
	hashInput.Purpose = ArtifactPurpose(purposeRaw)

	rows, err := tx.Query(ctx, `
		SELECT
		  v.id::text,
		  v.artifact_id::text,
		  p.entity_kind,
		  v.version,
		  e.eligibility_revision,
		  v.content_hash,
		  e.representation,
		  e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON m.workspace_id = e.workspace_id
		 AND m.session_id = e.session_id
		 AND m.id = e.manifest_id
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		JOIN research_artifact_passport p
		  ON p.workspace_id = v.workspace_id
		 AND p.session_id = v.session_id
		 AND p.id = v.artifact_id
		WHERE m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
		ORDER BY e.ordinal
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, dispatchManifestHashInput{}, "", err
	}
	defer rows.Close()

	var entries []artifactVersionCandidate
	for rows.Next() {
		var entry artifactVersionCandidate
		var kindRaw string
		if err = rows.Scan(
			&entry.VersionRowID, &entry.ArtifactID, &kindRaw, &entry.Version, &entry.EligibilityRevision,
			&entry.ContentHash, &entry.Representation, &entry.RepresentationHash,
		); err != nil {
			return nil, dispatchManifestHashInput{}, "", err
		}
		entry.Kind, err = ParseArtifactEntityKind(kindRaw)
		if err != nil {
			return nil, dispatchManifestHashInput{}, "", err
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, dispatchManifestHashInput{}, "", err
	}
	hashInput.Entries = entries
	return entries, hashInput, storedHash, nil
}

func verifyAcceptanceManifestHashTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) error {
	_, hashInput, storedHash, err := loadManifestEntryCandidatesForAttemptTx(ctx, tx, workspaceID, sessionID, attemptID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(storedHash) == "" {
		return fmt.Errorf("%w: acceptance manifest hash missing", ErrInvalidTransition)
	}
	if hashDispatchManifest(hashInput) != storedHash {
		return fmt.Errorf("%w: acceptance manifest hash mismatch", ErrInvalidTransition)
	}
	return nil
}

type acceptanceManifestLockTarget struct {
	Kind         ArtifactEntityKind
	ArtifactID   string
	VersionRowID string
}

func loadAcceptanceManifestLockTargetsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) ([]acceptanceManifestLockTarget, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.entity_kind, v.artifact_id::text, v.id::text
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON m.workspace_id = e.workspace_id
		 AND m.session_id = e.session_id
		 AND m.id = e.manifest_id
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		JOIN research_artifact_passport p
		  ON p.workspace_id = v.workspace_id
		 AND p.session_id = v.session_id
		 AND p.id = v.artifact_id
		WHERE m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []acceptanceManifestLockTarget
	for rows.Next() {
		var target acceptanceManifestLockTarget
		if err = rows.Scan(&target.Kind, &target.ArtifactID, &target.VersionRowID); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func sortAcceptanceManifestLockTargets(targets []acceptanceManifestLockTarget) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		return targets[i].ArtifactID < targets[j].ArtifactID
	})
}

func lockArtifactPassportRowTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, artifactID string,
) error {
	var locked bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM research_artifact_passport
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, artifactID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: acceptance manifest passport missing", ErrInvalidTransition)
	}
	return err
}

func lockArtifactVersionRowTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, versionRowID string,
) error {
	var locked bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM research_artifact_version
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, versionRowID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: acceptance manifest version missing", ErrInvalidTransition)
	}
	return err
}

func lockAcceptanceManifestAuthorizationTargetsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) error {
	var locked bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND attempt_id = $3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, attemptID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: acceptance requires dispatch manifest", ErrInvalidTransition)
	}
	if err != nil {
		return err
	}
	targets, err := loadAcceptanceManifestLockTargetsTx(ctx, tx, workspaceID, sessionID, attemptID)
	if err != nil {
		return err
	}
	sortAcceptanceManifestLockTargets(targets)
	for _, target := range targets {
		if err = lockArtifactPassportRowTx(ctx, tx, workspaceID, sessionID, target.ArtifactID); err != nil {
			return err
		}
	}
	versionRowIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		versionRowIDs = append(versionRowIDs, target.VersionRowID)
	}
	sort.Strings(versionRowIDs)
	for _, versionRowID := range versionRowIDs {
		if err = lockArtifactVersionRowTx(ctx, tx, workspaceID, sessionID, versionRowID); err != nil {
			return err
		}
	}
	return nil
}
