package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/skill"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrEvolutionSkillUnitNotFound      = errors.New("evolution skill unit not found")
	ErrEvolutionSkillVersionNotFound   = errors.New("evolution skill version not found")
	ErrEvolutionSkillVersionConflict   = errors.New("evolution skill current version changed")
	ErrEvolutionSkillNotMaterialized   = errors.New("evolution skill is not materialized")
	ErrEvolutionSkillVersionIncomplete = errors.New("evolution skill version has no SKILL.md")
	ErrEvolutionSkillVersionSnapshot   = errors.New("evolution skill version matcher snapshot is missing or invalid")
	ErrEvolutionSkillMaterializedDrift = errors.New("evolution skill materialized state conflicts with current version")
)

type EvolutionSkillUnitVersion struct {
	Version   db.SharedEvolutionUnitVersion
	IsCurrent bool
}

type EvolutionVersionFeedbackCounts struct {
	Total    int64 `json:"total"`
	Injected int64 `json:"injected"`
	Used     int64 `json:"used"`
	Success  int64 `json:"success"`
	Failure  int64 `json:"failure"`
	Ignored  int64 `json:"ignored"`
	Conflict int64 `json:"conflict"`
}

type EvolutionVersionEvalSummary struct {
	Basis                  string                         `json:"basis"`
	Verdict                string                         `json:"verdict"`
	Counts                 EvolutionVersionFeedbackCounts `json:"counts"`
	VersionAttributed      EvolutionVersionFeedbackCounts `json:"version_attributed"`
	UnitLifetime           EvolutionVersionFeedbackCounts `json:"unit_lifetime"`
	UnitUnattributedEvents int64                          `json:"unit_unattributed_events"`
	SuccessRate            *float64                       `json:"success_rate,omitempty"`
	UsageRate              *float64                       `json:"usage_rate,omitempty"`
	Explanations           []string                       `json:"explanations"`
}

const evolutionMatcherSnapshotMetadataKey = "matcher_snapshot"

type EvolutionMatcherSnapshot struct {
	CanonicalSummary string   `json:"canonical_summary"`
	Tags             []string `json:"tags"`
	Tools            []string `json:"tools"`
	TaskTypes        []string `json:"task_types"`
	ProjectTypes     []string `json:"project_types"`
	Languages        []string `json:"languages"`
	Frameworks       []string `json:"frameworks"`
}

type EvolutionSkillVersionRollbackResult struct {
	Unit    db.SharedEvolutionUnit
	Version db.SharedEvolutionUnitVersion
	Skill   db.Skill
	Changed bool
}

type EvolutionVersionService struct {
	db      db.DBTX
	queries *db.Queries
}

func NewEvolutionVersionService(executor db.DBTX) *EvolutionVersionService {
	return &EvolutionVersionService{db: executor, queries: db.New(executor)}
}

func (s *EvolutionVersionService) GetSkillUnit(ctx context.Context, workspaceID, unitID pgtype.UUID) (db.SharedEvolutionUnit, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, unit_type, title, canonical_summary, content, metadata, applies,
		       failure_cases, scope, tags, tools, task_types, project_types, languages, frameworks,
		       applicable_agent_types, applicable_projects, priority, score, success_count,
		       failure_count, ignored_count, conflict_count, last_used_at, status,
		       current_version_id, created_at, updated_at
		  FROM shared_evolution_unit
		 WHERE id = $1 AND workspace_id = $2 AND unit_type = 'skill'
	`, unitID, workspaceID)
	unit, err := scanSharedEvolutionUnit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SharedEvolutionUnit{}, ErrEvolutionSkillUnitNotFound
	}
	return unit, err
}

func (s *EvolutionVersionService) ListSkillVersions(ctx context.Context, workspaceID, unitID pgtype.UUID) ([]EvolutionSkillUnitVersion, error) {
	if _, err := s.GetSkillUnit(ctx, workspaceID, unitID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT v.id, v.workspace_id, v.unit_id, v.version, v.title, v.content, v.metadata,
		       v.applies, v.failure_cases, v.source_submission_ids, v.change_reason,
		       v.created_by, v.created_at, (u.current_version_id = v.id) AS is_current
		  FROM shared_evolution_unit_version v
		  JOIN shared_evolution_unit u
		    ON u.id = v.unit_id AND u.workspace_id = v.workspace_id AND u.unit_type = 'skill'
		 WHERE v.workspace_id = $1 AND v.unit_id = $2
		 ORDER BY v.version DESC, v.created_at DESC
	`, workspaceID, unitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]EvolutionSkillUnitVersion, 0)
	for rows.Next() {
		version, isCurrent, err := scanSharedEvolutionUnitVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, EvolutionSkillUnitVersion{Version: version, IsCurrent: isCurrent})
	}
	return versions, rows.Err()
}

func (s *EvolutionVersionService) GetSkillVersion(ctx context.Context, workspaceID, unitID, versionID pgtype.UUID) (EvolutionSkillUnitVersion, error) {
	row := s.db.QueryRow(ctx, `
		SELECT v.id, v.workspace_id, v.unit_id, v.version, v.title, v.content, v.metadata,
		       v.applies, v.failure_cases, v.source_submission_ids, v.change_reason,
		       v.created_by, v.created_at, (u.current_version_id = v.id) AS is_current
		  FROM shared_evolution_unit_version v
		  JOIN shared_evolution_unit u
		    ON u.id = v.unit_id AND u.workspace_id = v.workspace_id AND u.unit_type = 'skill'
		 WHERE v.id = $1 AND v.workspace_id = $2 AND v.unit_id = $3
	`, versionID, workspaceID, unitID)
	version, isCurrent, err := scanSharedEvolutionUnitVersion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvolutionSkillUnitVersion{}, ErrEvolutionSkillVersionNotFound
	}
	return EvolutionSkillUnitVersion{Version: version, IsCurrent: isCurrent}, err
}

func (s *EvolutionVersionService) ListSkillVersionFiles(ctx context.Context, workspaceID, unitID, versionID pgtype.UUID) ([]db.SharedEvolutionUnitFile, error) {
	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.workspace_id, f.unit_id, f.version_id, f.path, f.content,
		       f.content_hash, f.mime_type, f.size_bytes, f.created_at
		  FROM shared_evolution_unit_file f
		  JOIN shared_evolution_unit_version v
		    ON v.id = f.version_id AND v.workspace_id = f.workspace_id AND v.unit_id = f.unit_id
		 WHERE f.workspace_id = $1 AND f.unit_id = $2 AND f.version_id = $3
		 ORDER BY f.path ASC
	`, workspaceID, unitID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]db.SharedEvolutionUnitFile, 0)
	for rows.Next() {
		var file db.SharedEvolutionUnitFile
		if err := rows.Scan(&file.ID, &file.WorkspaceID, &file.UnitID, &file.VersionID, &file.Path, &file.Content, &file.ContentHash, &file.MimeType, &file.SizeBytes, &file.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *EvolutionVersionService) EvalSkillVersion(ctx context.Context, workspaceID, unitID, versionID pgtype.UUID) (EvolutionVersionEvalSummary, error) {
	var lifetime, attributed EvolutionVersionFeedbackCounts
	var explicitlyVersioned int64
	err := s.db.QueryRow(ctx, `
		SELECT count(*) AS total,
		       count(*) FILTER (WHERE event = 'injected') AS injected,
		       count(*) FILTER (WHERE event = 'used') AS used,
		       count(*) FILTER (WHERE event = 'success' OR outcome = 'success') AS success,
		       count(*) FILTER (WHERE event = 'failure' OR outcome = 'failure') AS failure,
		       count(*) FILTER (WHERE event = 'ignored') AS ignored,
		       count(*) FILTER (WHERE event = 'conflict') AS conflict,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text) AS version_total,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND event = 'injected') AS version_injected,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND event = 'used') AS version_used,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND (event = 'success' OR outcome = 'success')) AS version_success,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND (event = 'failure' OR outcome = 'failure')) AS version_failure,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND event = 'ignored') AS version_ignored,
		       count(*) FILTER (WHERE metadata->>'version_id' = $3::text AND event = 'conflict') AS version_conflict,
		       count(*) FILTER (WHERE COALESCE(metadata->>'version_id', '') <> '') AS explicitly_versioned
		  FROM evolution_unit_feedback_event
		 WHERE workspace_id = $1 AND unit_id = $2 AND unit_type = 'skill'
	`, workspaceID, unitID, versionID).Scan(
		&lifetime.Total, &lifetime.Injected, &lifetime.Used, &lifetime.Success, &lifetime.Failure, &lifetime.Ignored, &lifetime.Conflict,
		&attributed.Total, &attributed.Injected, &attributed.Used, &attributed.Success, &attributed.Failure, &attributed.Ignored, &attributed.Conflict,
		&explicitlyVersioned,
	)
	if err != nil {
		return EvolutionVersionEvalSummary{}, err
	}
	return BuildEvolutionVersionEvalSummary(lifetime, attributed, lifetime.Total-explicitlyVersioned), nil
}

func BuildEvolutionVersionEvalSummary(lifetime, attributed EvolutionVersionFeedbackCounts, unattributed int64) EvolutionVersionEvalSummary {
	basis := "version_attributed"
	counts := attributed
	explanations := []string{fmt.Sprintf("Evaluation uses %d feedback events whose metadata.version_id matches this version.", attributed.Total)}
	if attributed.Total == 0 {
		basis = "unit_lifetime_fallback"
		counts = lifetime
		explanations = []string{fmt.Sprintf("No feedback event identifies this version; evaluation falls back to %d unit-lifetime events.", lifetime.Total)}
	}
	explanations = append(explanations,
		fmt.Sprintf("%d unit feedback events do not declare metadata.version_id and cannot be assigned to a version.", unattributed),
		"Success rate is success/(success+failure); usage rate is used/injected.",
	)
	result := EvolutionVersionEvalSummary{
		Basis:                  basis,
		Verdict:                evolutionVersionVerdict(counts),
		Counts:                 counts,
		VersionAttributed:      attributed,
		UnitLifetime:           lifetime,
		UnitUnattributedEvents: unattributed,
		Explanations:           explanations,
	}
	if outcomes := counts.Success + counts.Failure; outcomes > 0 {
		rate := float64(counts.Success) / float64(outcomes)
		result.SuccessRate = &rate
	}
	if counts.Injected > 0 {
		rate := float64(counts.Used) / float64(counts.Injected)
		result.UsageRate = &rate
	}
	return result
}

func evolutionVersionVerdict(counts EvolutionVersionFeedbackCounts) string {
	outcomes := counts.Success + counts.Failure
	if outcomes == 0 {
		return "insufficient_data"
	}
	rate := float64(counts.Success) / float64(outcomes)
	switch {
	case rate >= 0.8 && counts.Conflict == 0:
		return "positive"
	case rate < 0.5 || counts.Conflict > counts.Success:
		return "negative"
	default:
		return "mixed"
	}
}

// ApplySkillVersionRollback mutates one already-open transaction. The caller
// must commit only after this method returns successfully.
func (s *EvolutionVersionService) ApplySkillVersionRollback(ctx context.Context, workspaceID, unitID, versionID, expectedCurrentVersionID, actorUserID pgtype.UUID) (EvolutionSkillVersionRollbackResult, error) {
	unit, err := s.lockSkillUnit(ctx, workspaceID, unitID)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	version, err := s.GetSkillVersion(ctx, workspaceID, unitID, versionID)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	if unit.CurrentVersionID != versionID && (!expectedCurrentVersionID.Valid || unit.CurrentVersionID != expectedCurrentVersionID) {
		return EvolutionSkillVersionRollbackResult{}, ErrEvolutionSkillVersionConflict
	}

	snapshot, err := evolutionMatcherSnapshotFromMetadata(version.Version.Metadata)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	files, err := s.ListSkillVersionFiles(ctx, workspaceID, unitID, versionID)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	mainContent, supporting, err := materializedEvolutionVersionFiles(files)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	skillRow, err := s.getMaterializedSkillForUpdate(ctx, workspaceID, unitID)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	name, description := skill.ParseSkillFrontmatter(mainContent)
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(version.Version.Title)
	}
	if name == "" {
		return EvolutionSkillVersionRollbackResult{}, ErrEvolutionSkillVersionIncomplete
	}

	materializedMatches, err := s.materializedSkillMatchesVersion(ctx, skillRow, name, description, mainContent, supporting)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	unitMatches := evolutionUnitMatchesVersion(unit, version.Version, snapshot)
	if unit.CurrentVersionID == versionID && unitMatches && materializedMatches {
		return EvolutionSkillVersionRollbackResult{Unit: unit, Version: version.Version, Skill: skillRow, Changed: false}, nil
	}

	fromVersionID := unit.CurrentVersionID
	unit, err = s.updateSkillUnitVersion(ctx, workspaceID, unitID, version.Version, snapshot)
	if err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	if err := s.replaceMaterializedSkill(ctx, skillRow.ID, workspaceID, name, description, mainContent, supporting); err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}
	if err := NewEvolutionService(s.queries).RefreshWorkspaceAgentSkillSuggestions(ctx, workspaceID); err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}

	details, _ := json.Marshal(map[string]any{
		"unit_id":                 uuidString(unitID),
		"skill_id":                uuidString(skillRow.ID),
		"from_version_id":         uuidString(fromVersionID),
		"to_version_id":           uuidString(versionID),
		"to_version":              version.Version.Version,
		"materialized_file_count": len(supporting) + 1,
		"repaired_drift":          fromVersionID == versionID,
	})
	if _, err := s.queries.CreateActivity(ctx, db.CreateActivityParams{
		WorkspaceID: workspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     actorUserID,
		Action:      "evolution_skill_version_rolled_back",
		Details:     details,
	}); err != nil {
		return EvolutionSkillVersionRollbackResult{}, err
	}

	skillRow.Name = name
	skillRow.Description = description
	skillRow.Content = mainContent
	return EvolutionSkillVersionRollbackResult{Unit: unit, Version: version.Version, Skill: skillRow, Changed: true}, nil
}

func (s *EvolutionVersionService) lockSkillUnit(ctx context.Context, workspaceID, unitID pgtype.UUID) (db.SharedEvolutionUnit, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, unit_type, title, canonical_summary, content, metadata, applies,
		       failure_cases, scope, tags, tools, task_types, project_types, languages, frameworks,
		       applicable_agent_types, applicable_projects, priority, score, success_count,
		       failure_count, ignored_count, conflict_count, last_used_at, status,
		       current_version_id, created_at, updated_at
		  FROM shared_evolution_unit
		 WHERE id = $1 AND workspace_id = $2 AND unit_type = 'skill'
		 FOR UPDATE
	`, unitID, workspaceID)
	unit, err := scanSharedEvolutionUnit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SharedEvolutionUnit{}, ErrEvolutionSkillUnitNotFound
	}
	return unit, err
}

func (s *EvolutionVersionService) getMaterializedSkillForUpdate(ctx context.Context, workspaceID, unitID pgtype.UUID) (db.Skill, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, content, config, created_by,
		       created_at, updated_at, source_evolution_unit_id
		  FROM skill
		 WHERE workspace_id = $1 AND source_evolution_unit_id = $2
		 FOR UPDATE
	`, workspaceID, unitID)
	var result db.Skill
	err := row.Scan(&result.ID, &result.WorkspaceID, &result.Name, &result.Description, &result.Content, &result.Config, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt, &result.SourceEvolutionUnitID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Skill{}, ErrEvolutionSkillNotMaterialized
	}
	return result, err
}

func (s *EvolutionVersionService) updateSkillUnitVersion(ctx context.Context, workspaceID, unitID pgtype.UUID, version db.SharedEvolutionUnitVersion, snapshot EvolutionMatcherSnapshot) (db.SharedEvolutionUnit, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE shared_evolution_unit
		   SET current_version_id = $3,
		       title = $4,
		       content = $5,
		       metadata = $6,
		       applies = $7,
		       failure_cases = $8,
		       canonical_summary = $9,
		       tags = $10,
		       tools = $11,
		       task_types = $12,
		       project_types = $13,
		       languages = $14,
		       frameworks = $15,
		       updated_at = now()
		 WHERE id = $1 AND workspace_id = $2 AND unit_type = 'skill'
		 RETURNING id, workspace_id, unit_type, title, canonical_summary, content, metadata, applies,
		           failure_cases, scope, tags, tools, task_types, project_types, languages, frameworks,
		           applicable_agent_types, applicable_projects, priority, score, success_count,
		           failure_count, ignored_count, conflict_count, last_used_at, status,
		           current_version_id, created_at, updated_at
	`, unitID, workspaceID, version.ID, version.Title, version.Content, version.Metadata, version.Applies, version.FailureCases,
		snapshot.CanonicalSummary, snapshot.Tags, snapshot.Tools, snapshot.TaskTypes, snapshot.ProjectTypes, snapshot.Languages, snapshot.Frameworks)
	return scanSharedEvolutionUnit(row)
}

func evolutionMatcherSnapshotFromMetadata(metadata []byte) (EvolutionMatcherSnapshot, error) {
	var envelope map[string]json.RawMessage
	if len(metadata) == 0 || json.Unmarshal(metadata, &envelope) != nil {
		return EvolutionMatcherSnapshot{}, ErrEvolutionSkillVersionSnapshot
	}
	raw, ok := envelope[evolutionMatcherSnapshotMetadataKey]
	if !ok {
		return EvolutionMatcherSnapshot{}, ErrEvolutionSkillVersionSnapshot
	}
	var snapshot EvolutionMatcherSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Tags == nil || snapshot.Tools == nil || snapshot.TaskTypes == nil ||
		snapshot.ProjectTypes == nil || snapshot.Languages == nil || snapshot.Frameworks == nil {
		return EvolutionMatcherSnapshot{}, ErrEvolutionSkillVersionSnapshot
	}
	return snapshot, nil
}

func evolutionUnitMatchesVersion(unit db.SharedEvolutionUnit, version db.SharedEvolutionUnitVersion, snapshot EvolutionMatcherSnapshot) bool {
	return unit.Title == version.Title && unit.Content == version.Content && jsonBytesEqual(unit.Metadata, version.Metadata) &&
		jsonBytesEqual(unit.Applies, version.Applies) && jsonBytesEqual(unit.FailureCases, version.FailureCases) &&
		unit.CanonicalSummary == snapshot.CanonicalSummary && stringSlicesEqual(unit.Tags, snapshot.Tags) &&
		stringSlicesEqual(unit.Tools, snapshot.Tools) && stringSlicesEqual(unit.TaskTypes, snapshot.TaskTypes) &&
		stringSlicesEqual(unit.ProjectTypes, snapshot.ProjectTypes) && stringSlicesEqual(unit.Languages, snapshot.Languages) &&
		stringSlicesEqual(unit.Frameworks, snapshot.Frameworks)
}

func jsonBytesEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *EvolutionVersionService) materializedSkillMatchesVersion(ctx context.Context, skillRow db.Skill, name, description, mainContent string, files []db.SharedEvolutionUnitFile) (bool, error) {
	if skillRow.Name != name || skillRow.Description != description || skillRow.Content != mainContent {
		return false, nil
	}
	rows, err := s.db.Query(ctx, `SELECT path, content FROM skill_file WHERE skill_id = $1 ORDER BY path`, skillRow.ID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	actual := map[string]string{}
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return false, err
		}
		actual[path] = content
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	expected := map[string]string{}
	for _, file := range files {
		if !skill.IsReservedContentPath(file.Path) {
			expected[file.Path] = file.Content
		}
	}
	if len(actual) != len(expected) {
		return false, nil
	}
	for path, content := range expected {
		if actual[path] != content {
			return false, nil
		}
	}
	return true, nil
}

func (s *EvolutionVersionService) replaceMaterializedSkill(ctx context.Context, skillID, workspaceID pgtype.UUID, name, description, mainContent string, files []db.SharedEvolutionUnitFile) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE skill SET name = $3, description = $4, content = $5, updated_at = now()
		 WHERE id = $1 AND workspace_id = $2
	`, skillID, workspaceID, name, description, mainContent); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM skill_file WHERE skill_id = $1`, skillID); err != nil {
		return err
	}
	for _, file := range files {
		if skill.IsReservedContentPath(file.Path) {
			continue
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO skill_file (skill_id, path, content)
			VALUES ($1, $2, $3)
			ON CONFLICT (skill_id, path) DO UPDATE SET content = EXCLUDED.content, updated_at = now()
		`, skillID, file.Path, file.Content); err != nil {
			return err
		}
	}
	return nil
}

func materializedEvolutionVersionFiles(files []db.SharedEvolutionUnitFile) (string, []db.SharedEvolutionUnitFile, error) {
	var mainContent string
	supporting := make([]db.SharedEvolutionUnitFile, 0, len(files))
	for _, file := range files {
		if strings.EqualFold(strings.TrimSpace(file.Path), "SKILL.md") {
			mainContent = file.Content
			continue
		}
		supporting = append(supporting, file)
	}
	if strings.TrimSpace(mainContent) == "" {
		return "", nil, ErrEvolutionSkillVersionIncomplete
	}
	return mainContent, supporting, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSharedEvolutionUnit(row rowScanner) (db.SharedEvolutionUnit, error) {
	var unit db.SharedEvolutionUnit
	err := row.Scan(
		&unit.ID, &unit.WorkspaceID, &unit.UnitType, &unit.Title, &unit.CanonicalSummary, &unit.Content,
		&unit.Metadata, &unit.Applies, &unit.FailureCases, &unit.Scope, &unit.Tags, &unit.Tools,
		&unit.TaskTypes, &unit.ProjectTypes, &unit.Languages, &unit.Frameworks,
		&unit.ApplicableAgentTypes, &unit.ApplicableProjects, &unit.Priority, &unit.Score,
		&unit.SuccessCount, &unit.FailureCount, &unit.IgnoredCount, &unit.ConflictCount,
		&unit.LastUsedAt, &unit.Status, &unit.CurrentVersionID, &unit.CreatedAt, &unit.UpdatedAt,
	)
	return unit, err
}

func scanSharedEvolutionUnitVersion(row rowScanner) (db.SharedEvolutionUnitVersion, bool, error) {
	var version db.SharedEvolutionUnitVersion
	var isCurrent bool
	err := row.Scan(
		&version.ID, &version.WorkspaceID, &version.UnitID, &version.Version, &version.Title,
		&version.Content, &version.Metadata, &version.Applies, &version.FailureCases,
		&version.SourceSubmissionIds, &version.ChangeReason, &version.CreatedBy, &version.CreatedAt,
		&isCurrent,
	)
	return version, isCurrent, err
}
