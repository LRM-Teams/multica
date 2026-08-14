package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type artifactProjectionModule struct {
	store *PostgresStore
}

type artifactProjectionScope struct {
	AllowedArtifactIDs       map[string]struct{}
	IncludeEvaluationPrivate bool
}

type artifactProjectionRow struct {
	PassportID          string
	EntityKind          string
	CurrentVersion      *int
	EligibilityRevision int64
	LifecycleStatus     string
	Provenance          string
	SchemaName          string
	SchemaVersion       string
	AccessLevel         string
	GoalVersion         *int
	PlanVersion         *int
	ProducedByTaskID    string
	ProducedByAttemptID string
	ProducedByAgentID   string
	VersionCount        int
	InputCount          int
	OutputCount         int
	ContentHash         string
}

func (m artifactProjectionModule) Load(ctx context.Context, workspaceID, sessionID string, scope artifactProjectionScope) (ArtifactProjection, error) {
	if m.store == nil || m.store.pool == nil {
		return ArtifactProjection{}, fmt.Errorf("artifact projection store is unavailable")
	}
	query := `
		SELECT p.id::text, p.entity_kind, p.current_version, p.eligibility_revision,
		       p.lifecycle_status, p.provenance_completeness,
		       COALESCE(v.schema_name,''), COALESCE(v.schema_version,''), COALESCE(v.access_level,''),
		       v.goal_version, v.plan_version,
		       COALESCE(v.produced_by_task_id::text,''), COALESCE(v.produced_by_attempt_id::text,''),
		       COALESCE(v.produced_by_agent_id::text,''),
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
		LEFT JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.artifact_id,v.version)=(p.workspace_id,p.session_id,p.id,p.current_version)
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid`
	args := []any{workspaceID, sessionID}
	if !scope.IncludeEvaluationPrivate {
		privateKinds := evaluationPrivateArtifactKindStrings()
		if len(privateKinds) > 0 {
			args = append(args, privateKinds)
			query += fmt.Sprintf(` AND NOT (p.entity_kind = ANY($%d::text[]))`, len(args))
		}
	}
	if scope.AllowedArtifactIDs != nil {
		ids := make([]string, 0, len(scope.AllowedArtifactIDs))
		for id := range scope.AllowedArtifactIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		args = append(args, ids)
		query += fmt.Sprintf(` AND p.id = ANY($%d::uuid[])`, len(args))
	}
	rows, err := m.store.pool.Query(ctx, query, args...)
	if err != nil {
		return ArtifactProjection{}, err
	}
	defer rows.Close()
	projectionRows := make([]artifactProjectionRow, 0)
	for rows.Next() {
		var row artifactProjectionRow
		if err = rows.Scan(
			&row.PassportID, &row.EntityKind, &row.CurrentVersion, &row.EligibilityRevision,
			&row.LifecycleStatus, &row.Provenance, &row.SchemaName, &row.SchemaVersion,
			&row.AccessLevel, &row.GoalVersion, &row.PlanVersion, &row.ProducedByTaskID,
			&row.ProducedByAttemptID, &row.ProducedByAgentID, &row.VersionCount,
			&row.InputCount, &row.OutputCount,
		); err != nil {
			return ArtifactProjection{}, err
		}
		projectionRows = append(projectionRows, row)
	}
	if err = rows.Err(); err != nil {
		return ArtifactProjection{}, err
	}
	return buildArtifactProjection(sessionID, projectionRows)
}

func (m artifactProjectionModule) LoadManifest(ctx context.Context, workspaceID, sessionID, manifestID string) (ArtifactProjection, error) {
	if m.store == nil || m.store.pool == nil {
		return ArtifactProjection{}, fmt.Errorf("artifact projection store is unavailable")
	}
	rows, err := m.store.pool.Query(ctx, `
		SELECT v.artifact_id::text, p.entity_kind, v.version, e.eligibility_revision,
		       e.selection_lifecycle_status, e.selection_provenance_completeness,
		       v.schema_name, v.schema_version, v.access_level,
		       v.goal_version, v.plan_version,
		       COALESCE(v.produced_by_task_id::text,''), COALESCE(v.produced_by_attempt_id::text,''),
		       COALESCE(v.produced_by_agent_id::text,''),
		       e.selection_version_count, e.selection_input_reference_count,
		       e.selection_output_reference_count
		FROM research_artifact_context_entry e
		JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p
		  ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE e.workspace_id=$1::uuid AND e.session_id=$2::uuid AND e.manifest_id=$3::uuid
		ORDER BY e.ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return ArtifactProjection{}, err
	}
	defer rows.Close()
	projectionRows := make([]artifactProjectionRow, 0)
	for rows.Next() {
		var row artifactProjectionRow
		var version int
		if err = rows.Scan(
			&row.PassportID, &row.EntityKind, &version, &row.EligibilityRevision,
			&row.LifecycleStatus, &row.Provenance, &row.SchemaName, &row.SchemaVersion,
			&row.AccessLevel, &row.GoalVersion, &row.PlanVersion, &row.ProducedByTaskID,
			&row.ProducedByAttemptID, &row.ProducedByAgentID, &row.VersionCount,
			&row.InputCount, &row.OutputCount,
		); err != nil {
			return ArtifactProjection{}, fmt.Errorf("read frozen artifact projection: %w", err)
		}
		row.CurrentVersion = &version
		projectionRows = append(projectionRows, row)
	}
	if err = rows.Err(); err != nil {
		return ArtifactProjection{}, err
	}
	return buildArtifactProjection(sessionID, projectionRows)
}

func evaluationPrivateArtifactKindStrings() []string {
	policy := ArtifactPolicy{}
	private := make([]string, 0)
	for _, kind := range RegisteredArtifactEntityKinds() {
		if policy.EvaluationPrivateKind(kind) {
			private = append(private, string(kind))
		}
	}
	sort.Strings(private)
	return private
}

func buildArtifactProjection(runID string, rows []artifactProjectionRow) (ArtifactProjection, error) {
	items := make([]ArtifactProjectionItem, 0, len(rows))
	for _, row := range rows {
		kind := normalizeArtifactProjectionKind(row.EntityKind)
		items = append(items, ArtifactProjectionItem{
			ID: runID + ":" + kind + ":" + row.PassportID, RunID: runID,
			EntityKind: kind, EntityID: row.PassportID, CurrentVersion: row.CurrentVersion,
			EligibilityRevision:    row.EligibilityRevision,
			LifecycleStatus:        normalizeArtifactProjectionLifecycle(row.LifecycleStatus),
			ProvenanceCompleteness: normalizeArtifactProjectionProvenance(row.Provenance),
			SchemaName:             row.SchemaName, SchemaVersion: row.SchemaVersion,
			AccessLevel: normalizeArtifactProjectionAccess(row.AccessLevel),
			GoalVersion: row.GoalVersion, PlanVersion: row.PlanVersion,
			ProducedByTaskID: row.ProducedByTaskID, ProducedByAttemptID: row.ProducedByAttemptID,
			ProducedByAgentID: row.ProducedByAgentID, VersionCount: row.VersionCount,
			InputReferenceCount: row.InputCount, OutputReferenceCount: row.OutputCount,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	encoded, err := json.Marshal(items)
	if err != nil {
		return ArtifactProjection{}, err
	}
	digest := sha256.Sum256(encoded)
	return ArtifactProjection{ProjectionHash: "sha256:" + hex.EncodeToString(digest[:]), Items: items}, nil
}

func normalizeArtifactProjectionKind(raw string) string {
	kind, err := ParseArtifactEntityKind(strings.TrimSpace(raw))
	if err != nil {
		return "generic"
	}
	return string(kind)
}

func normalizeArtifactProjectionLifecycle(raw string) string {
	switch ArtifactLifecycleStatus(strings.TrimSpace(raw)) {
	case ArtifactLifecycleRegistered, ArtifactLifecycleAccepted, ArtifactLifecycleRejected,
		ArtifactLifecycleStale, ArtifactLifecycleSuperseded, ArtifactLifecycleWithdrawn:
		return strings.TrimSpace(raw)
	default:
		return "unknown"
	}
}

func normalizeArtifactProjectionProvenance(raw string) string {
	switch ArtifactProvenanceCompleteness(strings.TrimSpace(raw)) {
	case ArtifactProvenanceComplete, ArtifactProvenancePartial, ArtifactProvenanceUnknown:
		return strings.TrimSpace(raw)
	default:
		return "unknown"
	}
}

func normalizeArtifactProjectionAccess(raw string) string {
	switch ArtifactAccessLevel(strings.TrimSpace(raw)) {
	case ArtifactAccessVerifiedOnly, ArtifactAccessRedacted, ArtifactAccessRaw:
		return strings.TrimSpace(raw)
	default:
		return "unknown"
	}
}
