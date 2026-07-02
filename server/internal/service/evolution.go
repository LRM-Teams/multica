package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/skill"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionService struct {
	Queries       *db.Queries
	Reviewer      EvolutionReviewer
	ReviewEnabled bool
}

const (
	maxEvolutionContentBytes     = 256 * 1024
	maxEvolutionPayloadBytes     = 512 * 1024
	maxEvolutionFileBytes        = 1024 * 1024
	maxEvolutionBundleBytes      = 8 * 1024 * 1024
	maxEvolutionBundleFileCount  = 128
	maxEvolutionCandidateTitle   = 200
	maxEvolutionCandidateSummary = 2000
	semanticDedupeScanLimit      = 200
	semanticDedupeThreshold      = 0.32
	semanticMatchThreshold       = 0.45
	semanticMatchStrongThreshold = 0.55
)

type EvolutionCurateResult struct {
	Promoted    int `json:"promoted"`
	Rejected    int `json:"rejected"`
	NeedsReview int `json:"needs_review"`
	Skipped     int `json:"skipped"`
	Matched     int `json:"matched"`
}

var ErrEvolutionSubmissionNotReviewable = errors.New("evolution submission is not awaiting review")

func NewEvolutionService(queries *db.Queries) *EvolutionService {
	return NewEvolutionServiceWithReviewer(queries, NoopEvolutionReviewer{}, false)
}

func NewEvolutionServiceWithReviewer(queries *db.Queries, reviewer EvolutionReviewer, enabled bool) *EvolutionService {
	if reviewer == nil {
		reviewer = NoopEvolutionReviewer{}
	}
	return &EvolutionService{Queries: queries, Reviewer: reviewer, ReviewEnabled: enabled}
}

type evolutionCurationStatus string

const (
	evolutionCurationPromoted    evolutionCurationStatus = "promoted"
	evolutionCurationRejected    evolutionCurationStatus = "rejected"
	evolutionCurationNeedsReview evolutionCurationStatus = "needs_review"
	evolutionCurationSkipped     evolutionCurationStatus = "skipped"
)

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
		unit, status, err := s.curateSubmission(ctx, submission)
		if err != nil {
			return result, err
		}
		switch status {
		case evolutionCurationRejected:
			result.Rejected++
			continue
		case evolutionCurationNeedsReview:
			result.NeedsReview++
			continue
		case evolutionCurationSkipped:
			result.Skipped++
			continue
		case evolutionCurationPromoted:
			result.Promoted++
			if submission.UnitType == "skill" {
				created, err := s.finalizeSkillPromotion(ctx, submission, unit)
				if err != nil {
					return result, err
				}
				if created {
					result.Matched++
				}
			} else if isEvolutionAutoAssignMemoryUnit(submission.UnitType) {
				result.Matched++
			}
		default:
			result.Skipped++
			continue
		}
	}
	return result, nil
}

func (s *EvolutionService) finalizeSkillPromotion(ctx context.Context, submission db.EvolutionUnitSubmission, unit db.SharedEvolutionUnit) (bool, error) {
	if submission.UnitType != "skill" {
		return false, nil
	}
	files, err := s.Queries.ListEvolutionSubmissionFiles(ctx, db.ListEvolutionSubmissionFilesParams{
		WorkspaceID:  submission.WorkspaceID,
		SubmissionID: submission.ID,
	})
	if err != nil {
		return false, err
	}
	skill, err := s.MaterializePromotedSkill(ctx, submission, unit, files)
	if err != nil {
		return false, err
	}
	if err := s.assignEvolutionSkillToSourceAgent(ctx, submission, skill); err != nil {
		return false, err
	}
	if err := s.RefreshWorkspaceAgentSkillSuggestions(ctx, unit.WorkspaceID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *EvolutionService) curateSubmission(ctx context.Context, submission db.EvolutionUnitSubmission) (db.SharedEvolutionUnit, evolutionCurationStatus, error) {
	files, err := s.Queries.ListEvolutionSubmissionFiles(ctx, db.ListEvolutionSubmissionFilesParams{WorkspaceID: submission.WorkspaceID, SubmissionID: submission.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
	}
	if reason := rejectEvolutionSubmissionReason(submission, files); reason != "" {
		_, err := s.rejectSubmissionWithReview(ctx, submission, reason, "high")
		return db.SharedEvolutionUnit{}, evolutionCurationRejected, err
	}
	if evolutionDedupeHash(submission) == "" {
		_, err := s.rejectSubmissionWithReview(ctx, submission, "missing content hash", "medium")
		return db.SharedEvolutionUnit{}, evolutionCurationRejected, err
	}

	if isEvolutionAutoAssignMemoryUnit(submission.UnitType) {
		status, err := s.curateMemorySubmission(ctx, submission)
		return db.SharedEvolutionUnit{}, status, err
	}

	if !s.ReviewEnabled {
		if submission.Confidence != "high" || (submission.Sensitivity != "none" && submission.Sensitivity != "local_path") {
			reason := needsReviewReason(submission)
			_, err := s.markSubmissionNeedsReview(ctx, submission, reason)
			return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, err
		}
		return s.promoteSubmission(ctx, submission, files, nil)
	}

	review, err := s.Reviewer.Review(ctx, evolutionReviewInput(submission, files))
	if err != nil {
		_, markErr := s.markSubmissionNeedsReviewForReviewerError(ctx, submission, err)
		return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, markErr
	}
	if reason := invalidEvolutionReviewResultReason(review); reason != "" {
		_, markErr := s.markSubmissionNeedsReviewForInvalidReview(ctx, submission, review, reason)
		return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, markErr
	}

	switch review.Decision {
	case EvolutionReviewReject:
		_, err := s.rejectSubmissionWithReviewResult(ctx, submission, review)
		return db.SharedEvolutionUnit{}, evolutionCurationRejected, err
	case EvolutionReviewNeedsReview:
		_, err := s.markSubmissionNeedsReviewWithResult(ctx, submission, review, reviewReason(review, "reviewer requested manual review"))
		return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, err
	case EvolutionReviewPromote:
		if review.RiskLevel != EvolutionReviewRiskLow {
			_, err := s.markSubmissionNeedsReviewWithResult(ctx, submission, review, reviewReason(review, "reviewer promotion requires manual review due to risk level"))
			return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, err
		}
		return s.promoteSubmission(ctx, submission, files, &review)
	default:
		_, err := s.markSubmissionNeedsReviewForInvalidReview(ctx, submission, review, "unknown review decision")
		return db.SharedEvolutionUnit{}, evolutionCurationNeedsReview, err
	}
}

type PromoteSubmissionReviewOptions struct {
	Reason                 string
	ApplyReviewSuggestions bool
}

func (s *EvolutionService) PromoteSubmissionFromReview(ctx context.Context, workspaceID, submissionID pgtype.UUID, opts PromoteSubmissionReviewOptions) (db.SharedEvolutionUnit, error) {
	submission, err := s.Queries.GetEvolutionUnitSubmissionInWorkspace(ctx, db.GetEvolutionUnitSubmissionInWorkspaceParams{ID: submissionID, WorkspaceID: workspaceID})
	if err != nil {
		return db.SharedEvolutionUnit{}, err
	}
	if submission.Status != "needs_review" {
		return db.SharedEvolutionUnit{}, ErrEvolutionSubmissionNotReviewable
	}
	files, err := s.Queries.ListEvolutionSubmissionFiles(ctx, db.ListEvolutionSubmissionFilesParams{WorkspaceID: workspaceID, SubmissionID: submissionID})
	if err != nil {
		return db.SharedEvolutionUnit{}, err
	}
	review := humanEvolutionReviewResult(EvolutionReviewPromote, opts.Reason)
	if opts.ApplyReviewSuggestions {
		var applied bool
		submission, applied = applyEvolutionReviewSuggestions(submission)
		if applied {
			review.Metadata["applied_review_suggestions"] = true
		}
	}
	unit, _, err := s.promoteSubmission(ctx, submission, files, &review)
	if err != nil {
		return db.SharedEvolutionUnit{}, err
	}
	if _, err := s.finalizeSkillPromotion(ctx, submission, unit); err != nil {
		return db.SharedEvolutionUnit{}, err
	}
	return unit, nil
}

func (s *EvolutionService) RejectSubmissionFromReview(ctx context.Context, workspaceID, submissionID pgtype.UUID, reason string) (db.EvolutionUnitSubmission, error) {
	submission, err := s.Queries.GetEvolutionUnitSubmissionInWorkspace(ctx, db.GetEvolutionUnitSubmissionInWorkspaceParams{ID: submissionID, WorkspaceID: workspaceID})
	if err != nil {
		return db.EvolutionUnitSubmission{}, err
	}
	if submission.Status != "needs_review" {
		return db.EvolutionUnitSubmission{}, ErrEvolutionSubmissionNotReviewable
	}
	review := humanEvolutionReviewResult(EvolutionReviewReject, reason)
	return s.rejectSubmissionWithReviewResult(ctx, submission, review)
}

func (s *EvolutionService) promoteSubmission(ctx context.Context, submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile, review *EvolutionReviewResult) (db.SharedEvolutionUnit, evolutionCurationStatus, error) {
	dedupeHash := evolutionDedupeHash(submission)
	existing, err := s.Queries.FindSharedEvolutionUnitByHash(ctx, db.FindSharedEvolutionUnitByHashParams{WorkspaceID: submission.WorkspaceID, UnitType: submission.UnitType, DedupeHash: dedupeHash})
	if err == nil {
		existing, err = s.Queries.SyncSharedEvolutionUnitMatchMetadata(ctx, db.SyncSharedEvolutionUnitMatchMetadataParams{
			ID:               existing.ID,
			WorkspaceID:      submission.WorkspaceID,
			Title:            submission.Title,
			CanonicalSummary: submission.Summary,
			Content:          submission.Content,
			Tags:             submission.Tags,
			Tools:            submission.Tools,
			TaskTypes:        submission.TaskTypes,
			ProjectTypes:     submission.ProjectTypes,
			Languages:        submission.Languages,
			Frameworks:       submission.Frameworks,
		})
		if err != nil {
			return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
		}
		err = s.markSubmissionPromoted(ctx, submission, existing.ID, review)
		return existing, evolutionCurationPromoted, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
	}
	if duplicate, ok, err := s.findSemanticDuplicate(ctx, submission, dedupeHash, review); err != nil {
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
	} else if ok {
		err = s.markSubmissionPromoted(ctx, submission, duplicate.ID, review)
		return duplicate, evolutionCurationPromoted, err
	}

	metadata := promotionMetadata(submission, dedupeHash, review)
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
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
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
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
	}
	unit, err = s.Queries.SetSharedEvolutionUnitCurrentVersion(ctx, db.SetSharedEvolutionUnitCurrentVersionParams{ID: unit.ID, WorkspaceID: submission.WorkspaceID, CurrentVersionID: version.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
	}
	for _, file := range files {
		_, err := s.Queries.UpsertSharedEvolutionUnitFile(ctx, db.UpsertSharedEvolutionUnitFileParams{WorkspaceID: submission.WorkspaceID, UnitID: unit.ID, VersionID: version.ID, Path: file.Path, Content: file.Content, ContentHash: file.ContentHash, MimeType: file.MimeType, SizeBytes: file.SizeBytes})
		if err != nil {
			return db.SharedEvolutionUnit{}, evolutionCurationSkipped, err
		}
	}
	err = s.markSubmissionPromoted(ctx, submission, unit.ID, review)
	return unit, evolutionCurationPromoted, err
}

func (s *EvolutionService) findSemanticDuplicate(ctx context.Context, submission db.EvolutionUnitSubmission, dedupeHash string, review *EvolutionReviewResult) (db.SharedEvolutionUnit, bool, error) {
	candidates, err := s.Queries.ListActiveSharedEvolutionUnitsByWorkspace(ctx, db.ListActiveSharedEvolutionUnitsByWorkspaceParams{WorkspaceID: submission.WorkspaceID, LimitCount: semanticDedupeScanLimit})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, err
	}
	best, score := bestSemanticDuplicate(submission, candidates)
	if score < semanticDedupeThreshold {
		return db.SharedEvolutionUnit{}, false, nil
	}
	maxVersion, err := s.Queries.MaxSharedEvolutionUnitVersion(ctx, db.MaxSharedEvolutionUnitVersionParams{WorkspaceID: submission.WorkspaceID, UnitID: best.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, err
	}
	metadata := promotionMetadata(submission, dedupeHash, review)
	version, err := s.Queries.CreateSharedEvolutionUnitVersion(ctx, db.CreateSharedEvolutionUnitVersionParams{
		WorkspaceID:  submission.WorkspaceID,
		UnitID:       best.ID,
		Version:      maxVersion + 1,
		Title:        best.Title,
		Content:      best.Content,
		Metadata:     mergeSemanticDuplicateMetadata(metadata, best, score),
		Applies:      best.Applies,
		SubmissionID: submission.ID,
		ChangeReason: "semantic duplicate candidate",
	})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, err
	}
	unit, err := s.Queries.SetSharedEvolutionUnitCurrentVersion(ctx, db.SetSharedEvolutionUnitCurrentVersionParams{ID: best.ID, WorkspaceID: submission.WorkspaceID, CurrentVersionID: version.ID})
	if err != nil {
		return db.SharedEvolutionUnit{}, false, err
	}
	return unit, true, nil
}

func (s *EvolutionService) markSubmissionPromoted(ctx context.Context, submission db.EvolutionUnitSubmission, promotedUnitID pgtype.UUID, review *EvolutionReviewResult) error {
	if review == nil {
		_, err := s.Queries.MarkEvolutionSubmissionPromoted(ctx, db.MarkEvolutionSubmissionPromotedParams{ID: submission.ID, WorkspaceID: submission.WorkspaceID, PromotedUnitID: promotedUnitID})
		return err
	}
	_, err := s.Queries.MarkEvolutionSubmissionPromotedWithReview(ctx, db.MarkEvolutionSubmissionPromotedWithReviewParams{
		ID:               submission.ID,
		WorkspaceID:      submission.WorkspaceID,
		PromotedUnitID:   promotedUnitID,
		ReviewDecision:   string(review.Decision),
		ReviewConfidence: reviewConfidence(review.Confidence),
		ReviewRiskLevel:  string(review.RiskLevel),
		ReviewReason:     reviewReason(*review, "reviewer approved promotion"),
		ReviewMetadata:   reviewerMetadata(*review),
	})
	return err
}

func (s *EvolutionService) rejectSubmissionWithReview(ctx context.Context, submission db.EvolutionUnitSubmission, reason, riskLevel string) (db.EvolutionUnitSubmission, error) {
	metadata := deterministicReviewMetadata(submission, "rejected")
	return s.Queries.RejectEvolutionSubmissionWithReview(ctx, db.RejectEvolutionSubmissionWithReviewParams{
		ID:              submission.ID,
		WorkspaceID:     submission.WorkspaceID,
		RejectReason:    reason,
		ReviewDecision:  string(EvolutionReviewReject),
		ReviewRiskLevel: riskLevel,
		ReviewReason:    reason,
		ReviewMetadata:  metadata,
	})
}

func (s *EvolutionService) rejectSubmissionWithReviewResult(ctx context.Context, submission db.EvolutionUnitSubmission, review EvolutionReviewResult) (db.EvolutionUnitSubmission, error) {
	reason := reviewReason(review, "reviewer rejected submission")
	return s.Queries.RejectEvolutionSubmissionWithReview(ctx, db.RejectEvolutionSubmissionWithReviewParams{
		ID:               submission.ID,
		WorkspaceID:      submission.WorkspaceID,
		RejectReason:     reason,
		ReviewDecision:   string(EvolutionReviewReject),
		ReviewConfidence: reviewConfidence(review.Confidence),
		ReviewRiskLevel:  string(review.RiskLevel),
		ReviewReason:     reason,
		ReviewMetadata:   reviewerMetadata(review),
	})
}

func (s *EvolutionService) markSubmissionNeedsReview(ctx context.Context, submission db.EvolutionUnitSubmission, reason string) (db.EvolutionUnitSubmission, error) {
	metadata := deterministicReviewMetadata(submission, "needs_review")
	return s.Queries.MarkEvolutionSubmissionNeedsReview(ctx, db.MarkEvolutionSubmissionNeedsReviewParams{
		ID:              submission.ID,
		WorkspaceID:     submission.WorkspaceID,
		ReviewDecision:  string(EvolutionReviewNeedsReview),
		ReviewRiskLevel: string(EvolutionReviewRiskMedium),
		ReviewReason:    reason,
		ReviewMetadata:  metadata,
	})
}

func (s *EvolutionService) markSubmissionNeedsReviewWithResult(ctx context.Context, submission db.EvolutionUnitSubmission, review EvolutionReviewResult, reason string) (db.EvolutionUnitSubmission, error) {
	return s.Queries.MarkEvolutionSubmissionNeedsReview(ctx, db.MarkEvolutionSubmissionNeedsReviewParams{
		ID:               submission.ID,
		WorkspaceID:      submission.WorkspaceID,
		ReviewDecision:   string(review.Decision),
		ReviewConfidence: reviewConfidence(review.Confidence),
		ReviewRiskLevel:  string(review.RiskLevel),
		ReviewReason:     reason,
		ReviewMetadata:   reviewerMetadata(review),
	})
}

func (s *EvolutionService) markSubmissionNeedsReviewForReviewerError(ctx context.Context, submission db.EvolutionUnitSubmission, reviewErr error) (db.EvolutionUnitSubmission, error) {
	metadata := reviewerFailureMetadata("reviewer_error", reviewErr.Error(), nil)
	return s.Queries.MarkEvolutionSubmissionNeedsReview(ctx, db.MarkEvolutionSubmissionNeedsReviewParams{
		ID:              submission.ID,
		WorkspaceID:     submission.WorkspaceID,
		ReviewDecision:  string(EvolutionReviewNeedsReview),
		ReviewRiskLevel: string(EvolutionReviewRiskMedium),
		ReviewReason:    "reviewer error",
		ReviewMetadata:  metadata,
	})
}

func (s *EvolutionService) markSubmissionNeedsReviewForInvalidReview(ctx context.Context, submission db.EvolutionUnitSubmission, review EvolutionReviewResult, reason string) (db.EvolutionUnitSubmission, error) {
	metadata := reviewerFailureMetadata("invalid_review", reason, &review)
	return s.Queries.MarkEvolutionSubmissionNeedsReview(ctx, db.MarkEvolutionSubmissionNeedsReviewParams{
		ID:               submission.ID,
		WorkspaceID:      submission.WorkspaceID,
		ReviewDecision:   string(EvolutionReviewNeedsReview),
		ReviewConfidence: reviewConfidence(review.Confidence),
		ReviewRiskLevel:  string(EvolutionReviewRiskMedium),
		ReviewReason:     reason,
		ReviewMetadata:   metadata,
	})
}

func deterministicReviewMetadata(submission db.EvolutionUnitSubmission, outcome string) []byte {
	metadata, _ := json.Marshal(map[string]any{
		"source":      "deterministic_rules",
		"outcome":     outcome,
		"sensitivity": submission.Sensitivity,
		"confidence":  submission.Confidence,
	})
	return metadata
}

func promotionMetadata(submission db.EvolutionUnitSubmission, dedupeHash string, review *EvolutionReviewResult) []byte {
	metadata := map[string]any{
		"dedupe_hash":     dedupeHash,
		"content_hash":    submission.ContentHash,
		"bundle_hash":     submission.BundleHash,
		"source_agent_id": uuidString(submission.SourceAgentID),
		"local_unit_id":   submission.LocalUnitID,
	}
	if review != nil {
		metadata["review"] = reviewMetadataMap(*review)
	}
	encoded, _ := json.Marshal(metadata)
	return encoded
}

func needsReviewReason(submission db.EvolutionUnitSubmission) string {
	if submission.Confidence != "high" {
		return "confidence requires review"
	}
	if submission.Sensitivity != "none" && submission.Sensitivity != "local_path" {
		return "sensitivity requires review"
	}
	return "manual review required"
}

func evolutionReviewInput(submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile) EvolutionReviewInput {
	reviewFiles := make([]EvolutionReviewFile, 0, len(files))
	for _, file := range files {
		reviewFiles = append(reviewFiles, EvolutionReviewFile{
			Path:      file.Path,
			Content:   file.Content,
			MimeType:  file.MimeType,
			SizeBytes: file.SizeBytes,
		})
	}
	return EvolutionReviewInput{
		WorkspaceID:    uuidString(submission.WorkspaceID),
		SubmissionID:   uuidString(submission.ID),
		UnitType:       submission.UnitType,
		Title:          submission.Title,
		Summary:        submission.Summary,
		Content:        submission.Content,
		Sensitivity:    submission.Sensitivity,
		Confidence:     submission.Confidence,
		SuggestedScope: submission.SuggestedScope,
		Tags:           submission.Tags,
		Tools:          submission.Tools,
		TaskTypes:      submission.TaskTypes,
		ProjectTypes:   submission.ProjectTypes,
		Languages:      submission.Languages,
		Frameworks:     submission.Frameworks,
		Files:          reviewFiles,
	}
}

func invalidEvolutionReviewResultReason(review EvolutionReviewResult) string {
	switch review.Decision {
	case EvolutionReviewPromote, EvolutionReviewNeedsReview, EvolutionReviewReject:
	default:
		return "unknown review decision"
	}
	switch review.RiskLevel {
	case EvolutionReviewRiskLow, EvolutionReviewRiskMedium, EvolutionReviewRiskHigh:
	default:
		return "unknown review risk level"
	}
	if math.IsNaN(review.Confidence) || math.IsInf(review.Confidence, 0) || review.Confidence < 0 || review.Confidence > 1 {
		return "invalid review confidence"
	}
	return ""
}

func reviewReason(review EvolutionReviewResult, fallback string) string {
	if strings.TrimSpace(review.Rationale) != "" {
		return strings.TrimSpace(review.Rationale)
	}
	return fallback
}

func reviewConfidence(value float64) pgtype.Float8 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: value, Valid: true}
}

func reviewerMetadata(review EvolutionReviewResult) []byte {
	encoded, _ := json.Marshal(reviewMetadataMap(review))
	return encoded
}

func reviewerFailureMetadata(kind, reason string, review *EvolutionReviewResult) []byte {
	metadata := map[string]any{
		"source": "evolution_reviewer",
		"kind":   kind,
		"reason": reason,
	}
	if review != nil {
		metadata["review"] = reviewMetadataMap(*review)
	}
	encoded, _ := json.Marshal(metadata)
	return encoded
}

func reviewMetadataMap(review EvolutionReviewResult) map[string]any {
	source := "evolution_reviewer"
	if review.Metadata != nil {
		if value, ok := review.Metadata["source"].(string); ok && strings.TrimSpace(value) != "" {
			source = value
		}
	}
	metadata := map[string]any{
		"source":               source,
		"decision":             review.Decision,
		"confidence":           review.Confidence,
		"risk_level":           review.RiskLevel,
		"title":                review.Title,
		"summary":              review.Summary,
		"suggested_tags":       review.SuggestedTags,
		"suggested_task_types": review.SuggestedTaskTypes,
		"suggested_scope":      review.SuggestedScope,
		"risks":                review.Risks,
		"rationale":            review.Rationale,
	}
	if review.Metadata != nil {
		metadata["metadata"] = review.Metadata
		if value, ok := review.Metadata["human_decision"]; ok {
			metadata["human_decision"] = value
		}
	}
	return metadata
}

func applyEvolutionReviewSuggestions(submission db.EvolutionUnitSubmission) (db.EvolutionUnitSubmission, bool) {
	var metadata map[string]any
	if len(submission.ReviewMetadata) == 0 || json.Unmarshal(submission.ReviewMetadata, &metadata) != nil {
		return submission, false
	}
	applied := false
	if title := reviewMetadataString(metadata, "title", maxEvolutionCandidateTitle); title != "" {
		submission.Title = title
		applied = true
	}
	if summary := reviewMetadataString(metadata, "summary", maxEvolutionCandidateSummary); summary != "" {
		submission.Summary = summary
		applied = true
	}
	if scope := reviewMetadataString(metadata, "suggested_scope", 64); scope != "" {
		submission.SuggestedScope = scope
		applied = true
	}
	if tags := reviewMetadataStringList(metadata, "suggested_tags", 12, 64); tags != nil {
		submission.Tags = tags
		applied = true
	}
	if taskTypes := reviewMetadataStringList(metadata, "suggested_task_types", 12, 64); taskTypes != nil {
		submission.TaskTypes = taskTypes
		applied = true
	}
	return submission, applied
}

func reviewMetadataString(metadata map[string]any, key string, maxBytes int) string {
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return truncateUTF8Bytes(strings.TrimSpace(value), maxBytes)
}

func reviewMetadataStringList(metadata map[string]any, key string, maxItems, maxBytes int) []string {
	values, ok := metadata[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, min(len(values), maxItems))
	seen := map[string]struct{}{}
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = truncateUTF8Bytes(strings.TrimSpace(text), maxBytes)
		if text == "" {
			continue
		}
		if _, exists := seen[text]; exists {
			continue
		}
		seen[text] = struct{}{}
		out = append(out, text)
		if len(out) >= maxItems {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func humanEvolutionReviewResult(decision EvolutionReviewDecision, reason string) EvolutionReviewResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		switch decision {
		case EvolutionReviewReject:
			reason = "human rejected submission"
		default:
			reason = "human approved promotion"
		}
	}
	return EvolutionReviewResult{
		Decision:   decision,
		Confidence: 1,
		RiskLevel:  EvolutionReviewRiskLow,
		Rationale:  reason,
		Metadata: map[string]any{
			"source":         "human_review",
			"human_decision": string(decision),
		},
	}
}

type evolutionDeliveryMatchTarget struct {
	AgentID pgtype.UUID
	Score   float64
	Reason  string
	Details map[string]any
}

func shouldCreateEvolutionDeliveryMatch(target evolutionDeliveryMatchTarget) bool {
	semanticScore, _ := target.Details["semantic_score"].(float64)
	if target.Score >= semanticMatchThreshold || semanticScore >= semanticMatchStrongThreshold {
		return true
	}
	matched, _ := target.Details["matched"].(map[string][]string)
	if len(matched) < 2 {
		return false
	}
	_, hasTool := matched["tools"]
	_, hasLanguage := matched["languages"]
	_, hasFramework := matched["frameworks"]
	_, hasTaskType := matched["task_types"]
	return hasTool && (hasLanguage || hasFramework || hasTaskType) || hasTaskType && (hasLanguage || hasFramework)
}

func scoreEvolutionDeliveryTarget(sourceAgentID pgtype.UUID, unit db.SharedEvolutionUnit, agent db.Agent, submission *db.EvolutionUnitSubmission) evolutionDeliveryMatchTarget {
	tags, tools, taskTypes, projectTypes, languages, frameworks := evolutionMatchDimensions(unit, submission)
	text := strings.ToLower(strings.Join([]string{agent.Name, agent.Description, agent.Instructions, string(agent.RuntimeConfig), string(agent.CustomArgs), agent.RuntimeMode, agent.Model.String}, " "))
	matched := map[string][]string{}
	score := 0.0
	if values := matchingEvolutionTerms(text, tools); len(values) > 0 {
		matched["tools"] = values
		score += 0.3
	}
	if values := matchingEvolutionTerms(text, taskTypes); len(values) > 0 {
		matched["task_types"] = values
		score += 0.25
	}
	if values := matchingEvolutionTerms(text, languages); len(values) > 0 {
		matched["languages"] = values
		score += 0.2
	}
	if values := matchingEvolutionTerms(text, frameworks); len(values) > 0 {
		matched["frameworks"] = values
		score += 0.2
	}
	if values := matchingEvolutionTerms(text, projectTypes); len(values) > 0 {
		matched["project_types"] = values
		score += 0.15
	}
	if values := matchingEvolutionTerms(text, tags); len(values) > 0 {
		matched["tags"] = values
		score += 0.1
	}
	semanticScore := semanticSimilarity(evolutionUnitMatchText(unit, submission), text)
	if semanticScore >= semanticMatchStrongThreshold {
		score += 0.2
	} else if semanticScore >= semanticMatchThreshold {
		score += 0.1
	}
	if score > 1 {
		score = 1
	}
	return evolutionDeliveryMatchTarget{
		AgentID: agent.ID,
		Score:   score,
		Reason:  evolutionMatchReason(score, semanticScore),
		Details: map[string]any{
			"strategy":         "hybrid_metadata_semantic",
			"source_agent_id":  uuidString(sourceAgentID),
			"target_agent_id":  uuidString(agent.ID),
			"matched":          matched,
			"semantic_score":   semanticScore,
			"metadata_score":   score - semanticMatchBonus(semanticScore),
			"threshold":        semanticMatchThreshold,
			"strong_threshold": semanticMatchStrongThreshold,
		},
	}
}

func matchingEvolutionTerms(text string, values []string) []string {
	if len(values) == 0 || text == "" {
		return nil
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		term := strings.ToLower(strings.TrimSpace(value))
		if len(term) < 2 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		if containsEvolutionTerm(text, term) {
			seen[term] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func containsEvolutionTerm(text, term string) bool {
	if len(term) <= 3 {
		for _, token := range strings.FieldsFunc(text, func(r rune) bool {
			return !(r == '#' || r == '+' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z')
		}) {
			if token == term {
				return true
			}
		}
		return false
	}
	return strings.Contains(text, term)
}

func evolutionMatchReason(score, semanticScore float64) string {
	if semanticScore >= semanticMatchStrongThreshold {
		return "hybrid metadata and semantic match"
	}
	if score >= semanticMatchThreshold {
		return "deterministic metadata match"
	}
	return "semantic match"
}

func semanticMatchBonus(score float64) float64 {
	if score >= semanticMatchStrongThreshold {
		return 0.2
	}
	if score >= semanticMatchThreshold {
		return 0.1
	}
	return 0
}

func evolutionStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func bestSemanticDuplicate(submission db.EvolutionUnitSubmission, candidates []db.SharedEvolutionUnit) (db.SharedEvolutionUnit, float64) {
	best := db.SharedEvolutionUnit{}
	bestScore := 0.0
	for _, candidate := range candidates {
		if candidate.UnitType != submission.UnitType || candidate.Status != "active" {
			continue
		}
		score := semanticDuplicateScore(submission, candidate)
		if score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	return best, bestScore
}

func semanticDuplicateScore(submission db.EvolutionUnitSubmission, unit db.SharedEvolutionUnit) float64 {
	textScore := semanticSimilarity(evolutionSubmissionText(submission), evolutionUnitText(unit))
	metadataScore := weightedJaccard(submissionMetadataTerms(submission), unitMetadataTerms(unit))
	if metadataScore == 0 {
		return textScore
	}
	return textScore*0.75 + metadataScore*0.25
}

func evolutionSubmissionText(submission db.EvolutionUnitSubmission) string {
	return strings.Join([]string{submission.Title, submission.Summary, submission.Content}, " ")
}

func evolutionUnitText(unit db.SharedEvolutionUnit) string {
	return strings.Join([]string{unit.Title, unit.CanonicalSummary, unit.Content}, " ")
}

func evolutionUnitMatchText(unit db.SharedEvolutionUnit, submission *db.EvolutionUnitSubmission) string {
	if submission != nil {
		return strings.Join([]string{submission.Title, submission.Summary, submission.Content}, " ")
	}
	return evolutionUnitText(unit)
}

func evolutionMatchDimensions(unit db.SharedEvolutionUnit, submission *db.EvolutionUnitSubmission) (tags, tools, taskTypes, projectTypes, languages, frameworks []string) {
	if submission != nil {
		return evolutionStrings(submission.Tags),
			evolutionStrings(submission.Tools),
			evolutionStrings(submission.TaskTypes),
			evolutionStrings(submission.ProjectTypes),
			evolutionStrings(submission.Languages),
			evolutionStrings(submission.Frameworks)
	}
	return evolutionStrings(unit.Tags),
		evolutionStrings(unit.Tools),
		evolutionStrings(unit.TaskTypes),
		evolutionStrings(unit.ProjectTypes),
		evolutionStrings(unit.Languages),
		evolutionStrings(unit.Frameworks)
}

func submissionMetadataTerms(submission db.EvolutionUnitSubmission) []string {
	return appendEvolutionTerms(nil, submission.Tags, submission.Tools, submission.TaskTypes, submission.ProjectTypes, submission.Languages, submission.Frameworks)
}

func unitMetadataTerms(unit db.SharedEvolutionUnit) []string {
	return appendEvolutionTerms(nil, unit.Tags, unit.Tools, unit.TaskTypes, unit.ProjectTypes, unit.Languages, unit.Frameworks)
}

func appendEvolutionTerms(out []string, groups ...[]string) []string {
	for _, group := range groups {
		for _, value := range group {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func semanticSimilarity(a, b string) float64 {
	left := semanticTermWeights(a)
	right := semanticTermWeights(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	dot := 0.0
	leftNorm := 0.0
	rightNorm := 0.0
	for term, weight := range left {
		leftNorm += weight * weight
		if other, ok := right[term]; ok {
			dot += weight * other
		}
	}
	for _, weight := range right {
		rightNorm += weight * weight
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}

func semanticTermWeights(text string) map[string]float64 {
	weights := map[string]float64{}
	for _, token := range semanticTokens(text) {
		weights[token]++
	}
	return weights
}

func semanticTokens(text string) []string {
	text = strings.ToLower(text)
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return !(r == '#' || r == '+' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z')
	})
	out := make([]string, 0, len(raw))
	for _, token := range raw {
		token = normalizeSemanticToken(strings.Trim(token, "-_.+#"))
		if len(token) < 2 || evolutionStopWords[token] {
			continue
		}
		out = append(out, token)
	}
	return out
}

func normalizeSemanticToken(token string) string {
	switch token {
	case "prs":
		return "pr"
	case "pullrequest", "pullrequests":
		return "pr"
	}
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix)
		}
	}
	return token
}

func weightedJaccard(leftValues, rightValues []string) float64 {
	left := termSet(leftValues)
	right := termSet(rightValues)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for value := range left {
		if right[value] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func termSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, token := range semanticTokens(value) {
			out[token] = true
		}
	}
	return out
}

func mergeSemanticDuplicateMetadata(metadata []byte, existing db.SharedEvolutionUnit, score float64) []byte {
	merged := map[string]any{}
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &merged)
	}
	merged["semantic_duplicate"] = map[string]any{
		"unit_id": uuidString(existing.ID),
		"score":   score,
		"action":  "version_metadata_merge",
	}
	encoded, _ := json.Marshal(merged)
	return encoded
}

var evolutionStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true, "that": true, "from": true,
	"into": true, "when": true, "then": true, "than": true, "safe": true, "use": true, "uses": true,
	"using": true, "reusable": true, "lesson": true, "memory": true, "skill": true, "workflow": true,
}

func rejectEvolutionSubmissionReason(submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile) string {
	if submission.Sensitivity == "secret" {
		return "sensitivity marked secret"
	}
	if strings.TrimSpace(submission.Content) == "" && len(files) == 0 {
		return "empty content"
	}
	if reason := validateEvolutionTextFields(submission); reason != "" {
		return reason
	}
	if reason := validateEvolutionFiles(submission, files); reason != "" {
		return reason
	}
	return ""
}

func validateEvolutionTextFields(submission db.EvolutionUnitSubmission) string {
	if len(submission.Title) > maxEvolutionCandidateTitle {
		return "title exceeds size limit"
	}
	if len(submission.Summary) > maxEvolutionCandidateSummary {
		return "summary exceeds size limit"
	}
	if len(submission.Content) > maxEvolutionContentBytes {
		return "content exceeds size limit"
	}
	if len(submission.Payload) > maxEvolutionPayloadBytes || len(submission.SanitizedPayload) > maxEvolutionPayloadBytes {
		return "payload exceeds size limit"
	}
	if hasSecretPattern(submission.Content) || hasSecretPattern(string(submission.Payload)) || hasSecretPattern(string(submission.SanitizedPayload)) {
		return "secret pattern detected"
	}
	return ""
}

func validateEvolutionFiles(submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile) string {
	if len(files) > maxEvolutionBundleFileCount {
		return "too many files"
	}
	hasSkillFile := false
	totalSize := 0
	seenPaths := map[string]struct{}{}
	for _, file := range files {
		cleanPath, ok := cleanEvolutionFilePath(file.Path)
		if !ok {
			return "invalid file path"
		}
		if _, exists := seenPaths[cleanPath]; exists {
			return "duplicate file path"
		}
		seenPaths[cleanPath] = struct{}{}
		if isDangerousEvolutionFilePath(cleanPath) {
			return "unsafe file path"
		}
		if !isAllowedEvolutionFileMimeType(file.MimeType) {
			return "unsupported file mime type"
		}
		if file.SizeBytes > maxEvolutionFileBytes || len(file.Content) > maxEvolutionFileBytes {
			return "file exceeds size limit"
		}
		if isBinaryEvolutionFileContent(file.Content) {
			return "binary file detected"
		}
		totalSize += len(file.Content)
		if totalSize > maxEvolutionBundleBytes {
			return "bundle exceeds size limit"
		}
		if hasSecretPattern(file.Content) {
			return "secret pattern detected in file"
		}
		if submission.UnitType == "skill" && cleanPath == "SKILL.md" {
			hasSkillFile = true
			if reason := validateEvolutionSkillMainFile(file.Content); reason != "" {
				return reason
			}
		}
	}
	if submission.UnitType == "skill" && !hasSkillFile {
		return "skill missing SKILL.md"
	}
	return ""
}

func cleanEvolutionFilePath(raw string) (string, bool) {
	replaced := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if replaced == "" || strings.HasPrefix(replaced, "/") || strings.Contains(replaced, "\x00") {
		return "", false
	}
	cleaned := path.Clean(replaced)
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", false
	}
	return cleaned, true
}

func isDangerousEvolutionFilePath(filePath string) bool {
	lowerPath := strings.ToLower(filePath)
	base := strings.ToLower(path.Base(filePath))
	if strings.HasPrefix(base, ".env") || strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_dsa") || strings.HasPrefix(base, "id_ecdsa") || strings.HasPrefix(base, "id_ed25519") {
		return true
	}
	switch path.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx":
		return true
	}
	segments := strings.Split(lowerPath, "/")
	for i, segment := range segments {
		switch segment {
		case ".ssh", ".aws":
			return true
		case ".kube":
			if i+1 < len(segments) && segments[i+1] == "config" {
				return true
			}
		case ".config":
			for _, nested := range segments[i+1:] {
				if nested == "credentials" || strings.HasPrefix(nested, "credentials.") {
					return true
				}
			}
		}
	}
	switch base {
	case ".netrc", ".npmrc", ".pypirc", "credentials", "credentials.json", "auth.json", "secrets.json", "secret.json", "known_hosts":
		return true
	default:
		return false
	}
}

func isAllowedEvolutionFileMimeType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if mimeType == "" || strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json",
		"application/ld+json",
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/toml",
		"application/javascript",
		"application/x-javascript",
		"application/typescript",
		"application/x-sh",
		"application/x-shellscript",
		"application/graphql",
		"application/sql":
		return true
	default:
		return false
	}
}

func isBinaryEvolutionFileContent(content string) bool {
	if content == "" {
		return false
	}
	if strings.Contains(content, "\x00") || !utf8.ValidString(content) {
		return true
	}
	control := 0
	total := 0
	for _, r := range content {
		total++
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			control++
		}
	}
	return control >= 8 && float64(control)/float64(total) > 0.05
}

func validateEvolutionSkillMainFile(content string) string {
	name, description := skill.ParseSkillFrontmatter(content)
	if strings.TrimSpace(name) == "" {
		return "skill missing frontmatter name"
	}
	if strings.TrimSpace(description) == "" {
		return "skill missing frontmatter description"
	}
	return ""
}

func hasSecretPattern(content string) bool {
	if content == "" {
		return false
	}
	for _, pattern := range secretRegexPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(content, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\'' || r == '"' || r == '`' || r == ',' || r == ';'
	}) {
		if looksHighEntropySecret(token) {
			return true
		}
	}
	return false
}

var secretRegexPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bASIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`(?i)\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|database_url|npm_token|password)\s*[:=]\s*['\"]?[^'\"\s]{8,}`),
	regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis)://[^\s:@]+:[^\s@]+@`),
}

func looksHighEntropySecret(token string) bool {
	trimmed := strings.Trim(token, "()[]{}<>.,;:!?/\\")
	if len(trimmed) < 32 || len(trimmed) > 256 || !utf8.ValidString(trimmed) {
		return false
	}
	if strings.HasPrefix(trimmed, "sha256:") || strings.Contains(trimmed, "://") {
		return false
	}
	classes := 0
	for _, chars := range []string{"abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "0123456789", "-_+/="} {
		if strings.ContainsAny(trimmed, chars) {
			classes++
		}
	}
	return classes >= 3 && shannonEntropy(trimmed) >= 4.5
}

func shannonEntropy(value string) float64 {
	counts := map[rune]int{}
	total := 0
	for _, r := range value {
		counts[r]++
		total++
	}
	if total == 0 {
		return 0
	}
	entropy := 0.0
	for _, count := range counts {
		p := float64(count) / float64(total)
		entropy -= p * math.Log2(p)
	}
	return entropy
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

func sourceAgentIDFromUnitMetadata(unit db.SharedEvolutionUnit) pgtype.UUID {
	if len(unit.Metadata) == 0 {
		return pgtype.UUID{}
	}
	var metadata map[string]any
	if err := json.Unmarshal(unit.Metadata, &metadata); err != nil {
		return pgtype.UUID{}
	}
	raw, _ := metadata["source_agent_id"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}
	}
	var id pgtype.UUID
	if err := id.Scan(raw); err != nil {
		return pgtype.UUID{}
	}
	return id
}

func isEvolutionAutoAssignMemoryUnit(unitType string) bool {
	switch unitType {
	case "memory", "preference", "tool_pattern", "workflow":
		return true
	default:
		return false
	}
}

func (s *EvolutionService) curateMemorySubmission(ctx context.Context, submission db.EvolutionUnitSubmission) (evolutionCurationStatus, error) {
	if err := s.assignEvolutionMemory(ctx, submission); err != nil {
		_, rejErr := s.rejectSubmissionWithReview(ctx, submission, err.Error(), "high")
		return evolutionCurationRejected, rejErr
	}
	_, err := s.Queries.MarkEvolutionSubmissionPromoted(ctx, db.MarkEvolutionSubmissionPromotedParams{
		ID:             submission.ID,
		WorkspaceID:    submission.WorkspaceID,
		PromotedUnitID: pgtype.UUID{},
	})
	if err != nil {
		return evolutionCurationSkipped, err
	}
	return evolutionCurationPromoted, nil
}

func (s *EvolutionService) assignEvolutionMemory(ctx context.Context, submission db.EvolutionUnitSubmission) error {
	if !submission.SourceAgentID.Valid {
		return errors.New("memory submission missing source agent")
	}
	name := strings.TrimSpace(submission.Title)
	if name == "" {
		name = strings.TrimSpace(submission.LocalUnitID)
	}
	if name == "" {
		return errors.New("memory submission missing name")
	}
	content := strings.TrimSpace(submission.Content)
	if content == "" {
		if summary := strings.TrimSpace(submission.Summary); summary != "" {
			content = summary
		}
	}
	if content == "" {
		return errors.New("memory submission missing content")
	}

	syncKey := fmt.Sprintf("evolution/%s/%s", submission.UnitType, strings.TrimSpace(submission.LocalUnitID))
	contentHash := strings.TrimSpace(submission.ContentHash)
	if contentHash == "" {
		contentHash = hashEvolutionContent(content)
	}
	config, _ := json.Marshal(map[string]any{
		"origin": map[string]any{
			"type":          "evolution",
			"submission_id": uuidString(submission.ID),
			"local_unit_id": submission.LocalUnitID,
			"unit_type":     submission.UnitType,
		},
	})
	createdBy, err := s.skillCreatedByFromSubmission(ctx, submission)
	if err != nil {
		return err
	}

	existing, err := s.Queries.GetAgentMemoryByAgentAndSyncKey(ctx, db.GetAgentMemoryByAgentAndSyncKeyParams{
		AgentID: submission.SourceAgentID,
		SyncKey: syncKey,
	})
	if err == nil {
		_, err = s.Queries.UpdateAgentMemory(ctx, db.UpdateAgentMemoryParams{
			ID:          existing.ID,
			Name:        pgtype.Text{String: name, Valid: true},
			Content:     pgtype.Text{String: content, Valid: true},
			Config:      config,
			ContentHash: pgtype.Text{String: contentHash, Valid: true},
		})
		return err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = s.Queries.CreateAgentMemory(ctx, db.CreateAgentMemoryParams{
		WorkspaceID: submission.WorkspaceID,
		AgentID:     submission.SourceAgentID,
		Name:        name,
		Content:     content,
		Config:      config,
		SyncKey:     syncKey,
		ContentHash: contentHash,
		CreatedBy:   createdBy,
	})
	return err
}

func hashEvolutionContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(h[:])
}
