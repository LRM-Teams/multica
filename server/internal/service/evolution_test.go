package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRejectEvolutionSubmissionReasonRejectsSecretPatterns(t *testing.T) {
	submission := db.EvolutionUnitSubmission{
		UnitType:       "memory",
		Title:          "Leaked token",
		Summary:        "Do not promote",
		Content:        "export OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456",
		Sensitivity:    "none",
		Confidence:     "high",
		ContentHash:    "sha256:test",
		BundleHash:     "",
		SuggestedScope: "workspace",
	}

	if got := rejectEvolutionSubmissionReason(submission, nil); got != "secret pattern detected" {
		t.Fatalf("reject reason = %q, want secret pattern detected", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsUnsafeSkillFiles(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), SizeBytes: int64(len(validSkillMainFile()))},
		{Path: ".env.local", Content: "SAFE_VALUE=1", SizeBytes: 12},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "unsafe file path" {
		t.Fatalf("reject reason = %q, want unsafe file path", got)
	}
}

func TestRejectEvolutionSubmissionReasonValidatesSkillFrontmatter(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: "---\nname: helper\n---\n# Helper\n", SizeBytes: int64(len("---\nname: helper\n---\n# Helper\n"))},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "skill missing frontmatter description" {
		t.Fatalf("reject reason = %q, want skill missing frontmatter description", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsOversizedBundle(t *testing.T) {
	submission := validSkillSubmission()
	bigContent := strings.Repeat("a", maxEvolutionFileBytes+1)
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), SizeBytes: int64(len(validSkillMainFile()))},
		{Path: "references/big.md", Content: bigContent, SizeBytes: int64(len(bigContent))},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "file exceeds size limit" {
		t.Fatalf("reject reason = %q, want file exceeds size limit", got)
	}
}

func TestRejectEvolutionSubmissionReasonAllowsValidSkill(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), SizeBytes: int64(len(validSkillMainFile()))},
		{Path: "references/guide.md", Content: "Use this skill for safe reviews.", SizeBytes: 32},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "" {
		t.Fatalf("reject reason = %q, want empty", got)
	}
}

type fakeEvolutionReviewer struct {
	result EvolutionReviewResult
	err    error
	called int
}

func (f *fakeEvolutionReviewer) Review(context.Context, EvolutionReviewInput) (EvolutionReviewResult, error) {
	f.called++
	return f.result, f.err
}

type evolutionMockDB struct {
	submission db.EvolutionUnitSubmission
	files      []db.EvolutionUnitSubmissionFile
	unit       db.SharedEvolutionUnit
	version    db.SharedEvolutionUnitVersion
	delivery   db.EvolutionUnitDelivery
}

func newEvolutionMockDB(submission db.EvolutionUnitSubmission) *evolutionMockDB {
	unitID := testUUID(41)
	versionID := testUUID(42)
	unit := db.SharedEvolutionUnit{
		ID:               unitID,
		WorkspaceID:      submission.WorkspaceID,
		UnitType:         submission.UnitType,
		Title:            submission.Title,
		CanonicalSummary: submission.Summary,
		Content:          submission.Content,
		Status:           "active",
		CurrentVersionID: versionID,
	}
	return &evolutionMockDB{
		submission: submission,
		unit:       unit,
		version:    db.SharedEvolutionUnitVersion{ID: versionID, WorkspaceID: submission.WorkspaceID, UnitID: unitID, Version: 1},
		delivery:   db.EvolutionUnitDelivery{ID: testUUID(43), WorkspaceID: submission.WorkspaceID, UnitID: unitID, VersionID: versionID, TargetAgentID: submission.SourceAgentID, Status: "pending"},
	}
}

func (m *evolutionMockDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (m *evolutionMockDB) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM evolution_unit_submission_file") {
		rows := make([][]any, 0, len(m.files))
		for _, file := range m.files {
			rows = append(rows, evolutionFileValues(file))
		}
		return &evolutionMockRows{rows: rows}, nil
	}
	return &evolutionMockRows{}, nil
}

func (m *evolutionMockDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(sql, "FindSharedEvolutionUnitByHash"):
		return &evolutionMockRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "CreateSharedEvolutionUnitVersion"):
		return &evolutionMockRow{values: sharedEvolutionUnitVersionValues(m.version)}
	case strings.Contains(sql, "CreateSharedEvolutionUnit") || strings.Contains(sql, "SetSharedEvolutionUnitCurrentVersion"):
		return &evolutionMockRow{values: sharedEvolutionUnitValues(m.unit)}
	case strings.Contains(sql, "CreateEvolutionDelivery"):
		return &evolutionMockRow{values: evolutionDeliveryValues(m.delivery)}
	case strings.Contains(sql, "RejectEvolutionSubmissionWithReview"):
		updated := m.submission
		updated.Status = "rejected"
		updated.RejectReason = stringArg(args, 2)
		updated.ReviewDecision = stringArg(args, 3)
		updated.ReviewConfidence = float8Arg(args, 4)
		updated.ReviewRiskLevel = stringArg(args, 5)
		updated.ReviewReason = stringArg(args, 6)
		updated.ReviewMetadata = bytesArg(args, 7)
		m.submission = updated
		return &evolutionMockRow{values: evolutionSubmissionValues(updated)}
	case strings.Contains(sql, "MarkEvolutionSubmissionNeedsReview"):
		updated := m.submission
		updated.Status = "needs_review"
		updated.ReviewDecision = stringArg(args, 2)
		updated.ReviewConfidence = float8Arg(args, 3)
		updated.ReviewRiskLevel = stringArg(args, 4)
		updated.ReviewReason = stringArg(args, 5)
		updated.ReviewMetadata = bytesArg(args, 6)
		m.submission = updated
		return &evolutionMockRow{values: evolutionSubmissionValues(updated)}
	case strings.Contains(sql, "MarkEvolutionSubmissionPromotedWithReview"):
		updated := m.submission
		updated.Status = "promoted"
		updated.PromotedUnitID = uuidArg(args, 2)
		updated.ReviewDecision = stringArg(args, 3)
		updated.ReviewConfidence = float8Arg(args, 4)
		updated.ReviewRiskLevel = stringArg(args, 5)
		updated.ReviewReason = stringArg(args, 6)
		updated.ReviewMetadata = bytesArg(args, 7)
		m.submission = updated
		return &evolutionMockRow{values: evolutionSubmissionValues(updated)}
	case strings.Contains(sql, "MarkEvolutionSubmissionPromoted"):
		updated := m.submission
		updated.Status = "promoted"
		updated.PromotedUnitID = uuidArg(args, 2)
		m.submission = updated
		return &evolutionMockRow{values: evolutionSubmissionValues(updated)}
	default:
		return &evolutionMockRow{err: pgx.ErrNoRows}
	}
}

type evolutionMockRows struct {
	rows   [][]any
	cursor int
	err    error
}

func (r *evolutionMockRows) Close()                                       {}
func (r *evolutionMockRows) Err() error                                   { return r.err }
func (r *evolutionMockRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("") }
func (r *evolutionMockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *evolutionMockRows) Values() ([]any, error)                       { return nil, nil }
func (r *evolutionMockRows) RawValues() [][]byte                          { return nil }
func (r *evolutionMockRows) Conn() *pgx.Conn                              { return nil }

func (r *evolutionMockRows) Next() bool {
	if r.cursor >= len(r.rows) {
		return false
	}
	r.cursor++
	return true
}

func (r *evolutionMockRows) Scan(dest ...any) error {
	if r.cursor == 0 || r.cursor > len(r.rows) {
		return pgx.ErrNoRows
	}
	return assignEvolutionValues(dest, r.rows[r.cursor-1])
}

type evolutionMockRow struct {
	values []any
	err    error
}

func (r *evolutionMockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assignEvolutionValues(dest, r.values)
}

func assignEvolutionValues(dest []any, values []any) error {
	for i := range dest {
		if i >= len(values) {
			break
		}
		switch d := dest[i].(type) {
		case *pgtype.UUID:
			*d = values[i].(pgtype.UUID)
		case *string:
			*d = values[i].(string)
		case *[]byte:
			*d = values[i].([]byte)
		case *[]string:
			*d = values[i].([]string)
		case *pgtype.Float8:
			*d = values[i].(pgtype.Float8)
		case *pgtype.Timestamptz:
			*d = values[i].(pgtype.Timestamptz)
		case *int32:
			*d = values[i].(int32)
		case *float64:
			*d = values[i].(float64)
		case *[]pgtype.UUID:
			*d = values[i].([]pgtype.UUID)
		case *int64:
			*d = values[i].(int64)
		}
	}
	return nil
}

func evolutionSubmissionValues(s db.EvolutionUnitSubmission) []any {
	return []any{s.ID, s.WorkspaceID, s.SourceAgentID, s.SourceMemberID, s.UnitType, s.LocalUnitID, s.Title, s.Summary, s.Content, s.Payload, s.SanitizedPayload, s.ContentHash, s.BundleHash, s.BundleRef, s.Sensitivity, s.Confidence, s.SuggestedScope, s.Evidence, s.Applies, s.Tags, s.Tools, s.TaskTypes, s.ProjectTypes, s.Languages, s.Frameworks, s.Status, s.RejectReason, s.ReviewDecision, s.ReviewConfidence, s.ReviewRiskLevel, s.ReviewReason, s.ReviewMetadata, s.ReviewedAt, s.PromotedUnitID, s.SourceCreatedAt, s.CreatedAt, s.UpdatedAt}
}

func evolutionFileValues(f db.EvolutionUnitSubmissionFile) []any {
	return []any{f.ID, f.WorkspaceID, f.SubmissionID, f.Path, f.Content, f.ContentHash, f.MimeType, f.SizeBytes, f.CreatedAt}
}

func sharedEvolutionUnitValues(u db.SharedEvolutionUnit) []any {
	return []any{u.ID, u.WorkspaceID, u.UnitType, u.Title, u.CanonicalSummary, u.Content, u.Metadata, u.Applies, u.FailureCases, u.Scope, u.Tags, u.Tools, u.TaskTypes, u.ProjectTypes, u.Languages, u.Frameworks, u.ApplicableAgentTypes, u.ApplicableProjects, u.Priority, u.Score, u.SuccessCount, u.FailureCount, u.IgnoredCount, u.ConflictCount, u.LastUsedAt, u.Status, u.CurrentVersionID, u.CreatedAt, u.UpdatedAt}
}

func sharedEvolutionUnitVersionValues(v db.SharedEvolutionUnitVersion) []any {
	return []any{v.ID, v.WorkspaceID, v.UnitID, v.Version, v.Title, v.Content, v.Metadata, v.Applies, v.FailureCases, v.SourceSubmissionIds, v.ChangeReason, v.CreatedBy, v.CreatedAt}
}

func evolutionDeliveryValues(d db.EvolutionUnitDelivery) []any {
	return []any{d.ID, d.WorkspaceID, d.UnitID, d.VersionID, d.TargetAgentID, d.DeliveryType, d.Status, d.Reason, d.MatcherScore, d.MatcherDetails, d.DeliveredPath, d.Error, d.DecidedAt, d.DeliveredAt, d.CreatedAt, d.UpdatedAt}
}

func stringArg(args []interface{}, index int) string {
	if index >= len(args) {
		return ""
	}
	value, _ := args[index].(string)
	return value
}

func bytesArg(args []interface{}, index int) []byte {
	if index >= len(args) {
		return nil
	}
	value, _ := args[index].([]byte)
	return value
}

func uuidArg(args []interface{}, index int) pgtype.UUID {
	if index >= len(args) {
		return pgtype.UUID{}
	}
	value, _ := args[index].(pgtype.UUID)
	return value
}

func float8Arg(args []interface{}, index int) pgtype.Float8 {
	if index >= len(args) {
		return pgtype.Float8{}
	}
	value, _ := args[index].(pgtype.Float8)
	return value
}

func TestCurateSubmissionWithReviewerDeterministicRejectDoesNotCallReviewer(t *testing.T) {
	submission := validMemorySubmission()
	submission.Sensitivity = "secret"
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationRejected {
		t.Fatalf("status = %q, want rejected", status)
	}
	if reviewer.called != 0 {
		t.Fatalf("reviewer called %d times, want 0", reviewer.called)
	}
}

func TestCurateSubmissionWithReviewerPromoteLowRiskPromotes(t *testing.T) {
	submission := validMemorySubmission()
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted {
		t.Fatalf("status = %q, want promoted", status)
	}
	if reviewer.called != 1 {
		t.Fatalf("reviewer called %d times, want 1", reviewer.called)
	}
	if mock.submission.Status != "promoted" || mock.submission.ReviewDecision != "promote" || mock.submission.ReviewRiskLevel != "low" {
		t.Fatalf("submission review state = status %q decision %q risk %q", mock.submission.Status, mock.submission.ReviewDecision, mock.submission.ReviewRiskLevel)
	}
}

func TestCurateSubmissionWithReviewerPromoteMediumRiskNeedsReview(t *testing.T) {
	submission := validMemorySubmission()
	reviewer := &fakeEvolutionReviewer{result: EvolutionReviewResult{Decision: EvolutionReviewPromote, Confidence: 0.9, RiskLevel: EvolutionReviewRiskMedium, Rationale: "medium risk"}}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationNeedsReview {
		t.Fatalf("status = %q, want needs_review", status)
	}
	if mock.submission.Status != "needs_review" || mock.submission.ReviewDecision != "promote" || mock.submission.ReviewRiskLevel != "medium" {
		t.Fatalf("submission review state = status %q decision %q risk %q", mock.submission.Status, mock.submission.ReviewDecision, mock.submission.ReviewRiskLevel)
	}
}

func TestCurateSubmissionWithReviewerRejectRejects(t *testing.T) {
	submission := validMemorySubmission()
	reviewer := &fakeEvolutionReviewer{result: EvolutionReviewResult{Decision: EvolutionReviewReject, Confidence: 0.8, RiskLevel: EvolutionReviewRiskHigh, Rationale: "unsafe"}}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationRejected {
		t.Fatalf("status = %q, want rejected", status)
	}
	if mock.submission.Status != "rejected" || mock.submission.ReviewDecision != "reject" || mock.submission.RejectReason != "unsafe" {
		t.Fatalf("submission review state = status %q decision %q reason %q", mock.submission.Status, mock.submission.ReviewDecision, mock.submission.RejectReason)
	}
}

func TestCurateSubmissionWithReviewerErrorNeedsReview(t *testing.T) {
	submission := validMemorySubmission()
	reviewer := &fakeEvolutionReviewer{err: errors.New("review failed")}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationNeedsReview {
		t.Fatalf("status = %q, want needs_review", status)
	}
	if mock.submission.Status != "needs_review" || mock.submission.ReviewReason != "reviewer error" {
		t.Fatalf("submission review state = status %q reason %q", mock.submission.Status, mock.submission.ReviewReason)
	}
}

func promoteLowRiskReview() EvolutionReviewResult {
	return EvolutionReviewResult{Decision: EvolutionReviewPromote, Confidence: 0.95, RiskLevel: EvolutionReviewRiskLow, Rationale: "safe to promote"}
}

func validMemorySubmission() db.EvolutionUnitSubmission {
	return db.EvolutionUnitSubmission{
		ID:             testUUID(11),
		WorkspaceID:    testUUID(12),
		SourceAgentID:  testUUID(13),
		SourceMemberID: testUUID(14),
		UnitType:       "memory",
		LocalUnitID:    "memory-1",
		Title:          "Reusable lesson",
		Summary:        "A reusable lesson.",
		Content:        "Use this safe reusable lesson.",
		ContentHash:    "sha256:content",
		Sensitivity:    "none",
		Confidence:     "high",
		SuggestedScope: "workspace",
	}
}

func validSkillSubmission() db.EvolutionUnitSubmission {
	return db.EvolutionUnitSubmission{
		UnitType:       "skill",
		Title:          "Review Helper",
		Summary:        "Helps review code safely.",
		Content:        "",
		Sensitivity:    "none",
		Confidence:     "high",
		ContentHash:    "sha256:content",
		BundleHash:     "sha256:bundle",
		SuggestedScope: "workspace",
	}
}

func validSkillMainFile() string {
	return "---\nname: review-helper\ndescription: Helps review code safely.\n---\n# Review Helper\n"
}
