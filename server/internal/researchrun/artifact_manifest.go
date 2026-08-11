package researchrun

import (
	"context"
	"encoding/json"
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

func reservePolicyWatermarkCASTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, expected int64) (int64, error) {
	var reserved int64
	err := tx.QueryRow(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND watermark = $3
		RETURNING watermark
	`, workspaceID, sessionID, expected).Scan(&reserved)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, fmt.Errorf("%w: policy watermark CAS failed", ErrInvalidTransition)
		}
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
	rows, err := tx.Query(ctx, `
		SELECT artifact_version_id::text, ordinal
		FROM research_artifact_context_entry
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid
		ORDER BY ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var inputVersionID string
		var ordinal int
		if err := rows.Scan(&inputVersionID, &ordinal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_artifact_input_reference (
			  workspace_id, session_id, consumer_version_id, input_version_id,
			  relation, manifest_id, explicitly_used, purpose, ordinal
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
			  'manifest_input', $5::uuid, true, 'task_execution', $6
			)
			ON CONFLICT (workspace_id, session_id, consumer_version_id, input_version_id, relation) DO NOTHING
		`, workspaceID, sessionID, manifestVersionRowID, inputVersionID, manifestID, ordinal); err != nil {
			return err
		}
	}
	return rows.Err()
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
	rows, err := tx.Query(ctx, `
		SELECT artifact_version_id::text, ordinal
		FROM research_artifact_context_entry
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid
		ORDER BY ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var inputVersionID string
		var ordinal int
		if err := rows.Scan(&inputVersionID, &ordinal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_artifact_input_reference (
			  workspace_id, session_id, consumer_version_id, input_version_id,
			  relation, manifest_id, explicitly_used, purpose, ordinal
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
			  'acceptance_input', $5::uuid, true, 'result_acceptance', $6
			)
			ON CONFLICT (workspace_id, session_id, consumer_version_id, input_version_id, relation) DO NOTHING
		`, workspaceID, sessionID, resultVersionRowID, inputVersionID, manifestID, ordinal); err != nil {
			return err
		}
	}
	return rows.Err()
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
	if len(allowed) == 0 {
		return snapshot
	}
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
	return manifestWatermark, reserved, nil
}
