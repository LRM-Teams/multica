package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionService struct {
	Queries *db.Queries
}

type EvolutionCurateResult struct {
	Promoted int `json:"promoted"`
	Rejected int `json:"rejected"`
	Skipped  int `json:"skipped"`
	Matched  int `json:"matched"`
}

func NewEvolutionService(queries *db.Queries) *EvolutionService {
	return &EvolutionService{Queries: queries}
}

func (s *EvolutionService) CurateAndMatchWorkspace(ctx context.Context, workspaceID pgtype.UUID, limit int32) (EvolutionCurateResult, error) {
	if limit <= 0 {
		limit = 50
	}
	submissions, err := s.Queries.ListCandidateEvolutionSubmissions(ctx, db.ListCandidateEvolutionSubmissionsParams{WorkspaceID: workspaceID, LimitCount: limit})
	if err != nil {
		return EvolutionCurateResult{}, err
	}
	result := EvolutionCurateResult{}
	for _, submission := range submissions {
		unit, promoted, rejected, err := s.curateSubmission(ctx, submission)
		if err != nil {
			return result, err
		}
		if rejected {
			result.Rejected++
			continue
		}
		if !promoted {
			result.Skipped++
			continue
		}
		result.Promoted++
		created, err := s.matchSourceAgent(ctx, submission, unit)
		if err != nil {
			return result, err
		}
		if created {
			result.Matched++
		}
	}
	return result, nil
}

func (s *EvolutionService) curateSubmission(ctx context.Context, submission db.EvolutionUnitSubmission) (db.SharedEvolutionUnit, bool, bool, error) {
	files, err := s.Queries.ListEvolutionSubmissionFiles(ctx, db.ListEvolutionSubmissionFilesParams{WorkspaceID: submission.WorkspaceID, SubmissionID: submission.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, false, err
	}
	if reason := rejectEvolutionSubmissionReason(submission, files); reason != "" {
		_, err := s.Queries.RejectEvolutionSubmission(ctx, db.RejectEvolutionSubmissionParams{ID: submission.ID, WorkspaceID: submission.WorkspaceID, RejectReason: reason})
		return db.SharedEvolutionUnit{}, false, true, err
	}
	if submission.Confidence != "high" || (submission.Sensitivity != "none" && submission.Sensitivity != "local_path") {
		return db.SharedEvolutionUnit{}, false, false, nil
	}

	dedupeHash := evolutionDedupeHash(submission)
	if dedupeHash == "" {
		_, err := s.Queries.RejectEvolutionSubmission(ctx, db.RejectEvolutionSubmissionParams{ID: submission.ID, WorkspaceID: submission.WorkspaceID, RejectReason: "missing content hash"})
		return db.SharedEvolutionUnit{}, false, true, err
	}
	existing, err := s.Queries.FindSharedEvolutionUnitByHash(ctx, db.FindSharedEvolutionUnitByHashParams{WorkspaceID: submission.WorkspaceID, UnitType: submission.UnitType, DedupeHash: dedupeHash})
	if err == nil {
		_, err = s.Queries.MarkEvolutionSubmissionPromoted(ctx, db.MarkEvolutionSubmissionPromotedParams{ID: submission.ID, WorkspaceID: submission.WorkspaceID, PromotedUnitID: existing.ID})
		return existing, true, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.SharedEvolutionUnit{}, false, false, err
	}

	metadata, _ := json.Marshal(map[string]any{
		"dedupe_hash":     dedupeHash,
		"content_hash":    submission.ContentHash,
		"bundle_hash":     submission.BundleHash,
		"source_agent_id": uuidString(submission.SourceAgentID),
		"local_unit_id":   submission.LocalUnitID,
	})
	unit, err := s.Queries.CreateSharedEvolutionUnit(ctx, db.CreateSharedEvolutionUnitParams{
		WorkspaceID:      submission.WorkspaceID,
		UnitType:         submission.UnitType,
		Title:            submission.Title,
		CanonicalSummary: submission.Summary,
		Content:          submission.Content,
		Metadata:         metadata,
		Applies:          submission.Applies,
		Scope:            defaultEvolutionScope(submission.SuggestedScope),
		Tags:             submission.Tags,
		Tools:            submission.Tools,
		TaskTypes:        submission.TaskTypes,
		ProjectTypes:     submission.ProjectTypes,
		Languages:        submission.Languages,
		Frameworks:       submission.Frameworks,
		Priority:         0,
		Score:            initialEvolutionScore(submission),
	})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, false, err
	}
	version, err := s.Queries.CreateSharedEvolutionUnitVersion(ctx, db.CreateSharedEvolutionUnitVersionParams{
		WorkspaceID:  submission.WorkspaceID,
		UnitID:       unit.ID,
		Version:      1,
		Title:        submission.Title,
		Content:      submission.Content,
		Metadata:     metadata,
		Applies:      submission.Applies,
		SubmissionID: submission.ID,
		ChangeReason: "initial promotion",
	})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, false, err
	}
	unit, err = s.Queries.SetSharedEvolutionUnitCurrentVersion(ctx, db.SetSharedEvolutionUnitCurrentVersionParams{ID: unit.ID, WorkspaceID: submission.WorkspaceID, CurrentVersionID: version.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, false, err
	}
	for _, file := range files {
		_, err := s.Queries.UpsertSharedEvolutionUnitFile(ctx, db.UpsertSharedEvolutionUnitFileParams{WorkspaceID: submission.WorkspaceID, UnitID: unit.ID, VersionID: version.ID, Path: file.Path, Content: file.Content, ContentHash: file.ContentHash, MimeType: file.MimeType, SizeBytes: file.SizeBytes})
		if err != nil {
			return db.SharedEvolutionUnit{}, false, false, err
		}
	}
	_, err = s.Queries.MarkEvolutionSubmissionPromoted(ctx, db.MarkEvolutionSubmissionPromotedParams{ID: submission.ID, WorkspaceID: submission.WorkspaceID, PromotedUnitID: unit.ID})
	return unit, true, false, err
}

func (s *EvolutionService) matchSourceAgent(ctx context.Context, submission db.EvolutionUnitSubmission, unit db.SharedEvolutionUnit) (bool, error) {
	if !unit.CurrentVersionID.Valid {
		return false, nil
	}
	deliveryType := "inbox"
	if unit.UnitType == "skill" {
		deliveryType = "generated"
	}
	details, _ := json.Marshal(map[string]any{"strategy": "source_agent_mvp", "source_agent_id": uuidString(submission.SourceAgentID)})
	delivery, err := s.Queries.CreateEvolutionDelivery(ctx, db.CreateEvolutionDeliveryParams{
		WorkspaceID:    unit.WorkspaceID,
		UnitID:         unit.ID,
		VersionID:      unit.CurrentVersionID,
		TargetAgentID:  submission.SourceAgentID,
		DeliveryType:   deliveryType,
		Reason:         "source agent MVP match",
		MatcherScore:   1,
		MatcherDetails: details,
	})
	if err != nil {
		return false, err
	}
	return delivery.Status == "pending", nil
}

func rejectEvolutionSubmissionReason(submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile) string {
	if submission.Sensitivity == "secret" {
		return "sensitivity marked secret"
	}
	if strings.TrimSpace(submission.Content) == "" && len(files) == 0 {
		return "empty content"
	}
	if hasSecretPattern(submission.Content) || hasSecretPattern(string(submission.Payload)) {
		return "secret pattern detected"
	}
	if submission.UnitType == "skill" {
		hasSkillFile := false
		for _, file := range files {
			if file.Path == "SKILL.md" {
				hasSkillFile = true
			}
			if hasSecretPattern(file.Content) {
				return "secret pattern detected in file"
			}
		}
		if !hasSkillFile {
			return "skill missing SKILL.md"
		}
	}
	return ""
}

func hasSecretPattern(content string) bool {
	lower := strings.ToLower(content)
	patterns := []string{"begin private key", "xoxb-", "ghp_", "database_url=", "npm_token", "sk-"}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return strings.Contains(content, "AKIA")
}

func evolutionDedupeHash(submission db.EvolutionUnitSubmission) string {
	if submission.UnitType == "skill" || submission.UnitType == "workflow" {
		return strings.TrimSpace(submission.BundleHash)
	}
	return strings.TrimSpace(submission.ContentHash)
}

func defaultEvolutionScope(scope string) string {
	if strings.TrimSpace(scope) == "" {
		return "workspace"
	}
	return strings.TrimSpace(scope)
}

func initialEvolutionScore(submission db.EvolutionUnitSubmission) float64 {
	score := 0.5
	if submission.Confidence == "high" {
		score += 0.3
	}
	if submission.Summary != "" {
		score += 0.1
	}
	if len(submission.Tags)+len(submission.Tools)+len(submission.TaskTypes) > 0 {
		score += 0.1
	}
	return score
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return id.String()
}
