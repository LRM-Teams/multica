package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
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
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
		{Path: "references/guide.md", Content: "Use this skill for safe reviews.", MimeType: "text/markdown", SizeBytes: 32},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "" {
		t.Fatalf("reject reason = %q, want empty", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsMissingSkillFile(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "README.md", Content: "# Review Helper", MimeType: "text/markdown", SizeBytes: 15},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "skill missing SKILL.md" {
		t.Fatalf("reject reason = %q, want skill missing SKILL.md", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsBinaryFileContent(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
		{Path: "asset.txt", Content: "safe\x00unsafe", MimeType: "text/plain", SizeBytes: 11},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "binary file detected" {
		t.Fatalf("reject reason = %q, want binary file detected", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsUnsupportedMimeType(t *testing.T) {
	submission := validSkillSubmission()
	files := []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
		{Path: "diagram.png", Content: "not actually an image", MimeType: "image/png", SizeBytes: 21},
	}

	if got := rejectEvolutionSubmissionReason(submission, files); got != "unsupported file mime type" {
		t.Fatalf("reject reason = %q, want unsupported file mime type", got)
	}
}

func TestRejectEvolutionSubmissionReasonRejectsExpandedDangerousPaths(t *testing.T) {
	submission := validSkillSubmission()
	cases := []string{
		".ssh/config",
		".aws/credentials",
		".config/gcloud/credentials.db",
		"certs/client.pem",
		"certs/client.key",
		"certs/client.p12",
		".kube/config",
	}
	for _, filePath := range cases {
		t.Run(filePath, func(t *testing.T) {
			files := []db.EvolutionUnitSubmissionFile{
				{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
				{Path: filePath, Content: "safe placeholder", MimeType: "text/plain", SizeBytes: 16},
			}
			if got := rejectEvolutionSubmissionReason(submission, files); got != "unsafe file path" {
				t.Fatalf("reject reason = %q, want unsafe file path", got)
			}
		})
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
	submission    db.EvolutionUnitSubmission
	files         []db.EvolutionUnitSubmissionFile
	unit          db.SharedEvolutionUnit
	version       db.SharedEvolutionUnitVersion
	delivery      db.EvolutionUnitDelivery
	agents        []db.Agent
	deliveries    []db.CreateEvolutionDeliveryParams
	existingFound bool
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
		agents: []db.Agent{
			{ID: submission.SourceAgentID, WorkspaceID: submission.WorkspaceID, Name: "Source Agent"},
		},
	}
}

func (m *evolutionMockDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (m *evolutionMockDB) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	switch {
	case strings.Contains(sql, "FROM evolution_unit_submission_file"):
		rows := make([][]any, 0, len(m.files))
		for _, file := range m.files {
			rows = append(rows, evolutionFileValues(file))
		}
		return &evolutionMockRows{rows: rows}, nil
	case strings.Contains(sql, "FROM agent"):
		rows := make([][]any, 0, len(m.agents))
		for _, agent := range m.agents {
			rows = append(rows, evolutionAgentValues(agent))
		}
		return &evolutionMockRows{rows: rows}, nil
	default:
		return &evolutionMockRows{}, nil
	}
}

func (m *evolutionMockDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(sql, "FindSharedEvolutionUnitByHash"):
		if m.existingFound {
			return &evolutionMockRow{values: sharedEvolutionUnitValues(m.unit)}
		}
		return &evolutionMockRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "CreateSharedEvolutionUnitVersion"):
		return &evolutionMockRow{values: sharedEvolutionUnitVersionValues(m.version)}
	case strings.Contains(sql, "CreateSharedEvolutionUnit") || strings.Contains(sql, "SetSharedEvolutionUnitCurrentVersion"):
		return &evolutionMockRow{values: sharedEvolutionUnitValues(m.unit)}
	case strings.Contains(sql, "CreateEvolutionDelivery"):
		arg := db.CreateEvolutionDeliveryParams{
			WorkspaceID:    uuidArg(args, 0),
			UnitID:         uuidArg(args, 1),
			VersionID:      uuidArg(args, 2),
			TargetAgentID:  uuidArg(args, 3),
			DeliveryType:   stringArg(args, 4),
			Reason:         stringArg(args, 5),
			MatcherScore:   float64Arg(args, 6),
			MatcherDetails: bytesArg(args, 7),
		}
		m.deliveries = append(m.deliveries, arg)
		delivery := m.delivery
		delivery.TargetAgentID = arg.TargetAgentID
		delivery.MatcherScore = arg.MatcherScore
		delivery.MatcherDetails = arg.MatcherDetails
		return &evolutionMockRow{values: evolutionDeliveryValues(delivery)}
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

func evolutionAgentValues(a db.Agent) []any {
	return []any{a.ID, a.WorkspaceID, a.Name, a.AvatarUrl, a.RuntimeMode, a.RuntimeConfig, a.Visibility, a.Status, a.MaxConcurrentTasks, a.OwnerID, a.CreatedAt, a.UpdatedAt, a.Description, a.RuntimeID, a.Instructions, a.ArchivedAt, a.ArchivedBy, a.CustomEnv, a.CustomArgs, a.McpConfig, a.Model, a.ThinkingLevel}
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

func float64Arg(args []interface{}, index int) float64 {
	if index >= len(args) {
		return 0
	}
	value, _ := args[index].(float64)
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

func TestCurateSubmissionMissingContentHashRejects(t *testing.T) {
	submission := validMemorySubmission()
	submission.ContentHash = ""
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationRejected || mock.submission.RejectReason != "missing content hash" {
		t.Fatalf("status/reason = %q/%q, want rejected/missing content hash", status, mock.submission.RejectReason)
	}
	if reviewer.called != 0 {
		t.Fatalf("reviewer called %d times, want 0", reviewer.called)
	}
}

func TestCurateSubmissionReviewDisabledLowConfidenceNeedsReview(t *testing.T) {
	submission := validMemorySubmission()
	submission.Confidence = "low"
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, false)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationNeedsReview || mock.submission.Status != "needs_review" || mock.submission.ReviewReason != "confidence requires review" {
		t.Fatalf("status/review = %q/%q/%q, want needs_review/confidence requires review", status, mock.submission.Status, mock.submission.ReviewReason)
	}
	if reviewer.called != 0 {
		t.Fatalf("reviewer called %d times, want 0", reviewer.called)
	}
}

func TestCurateSubmissionReviewDisabledLocalPathPromotes(t *testing.T) {
	submission := validMemorySubmission()
	submission.Sensitivity = "local_path"
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, false)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted || mock.submission.Status != "promoted" {
		t.Fatalf("status/submission = %q/%q, want promoted/promoted", status, mock.submission.Status)
	}
	if reviewer.called != 0 {
		t.Fatalf("reviewer called %d times, want 0", reviewer.called)
	}
}

func TestCurateSubmissionDuplicateHashMarksPromotedWithoutCreatingUnit(t *testing.T) {
	submission := validMemorySubmission()
	mock := newEvolutionMockDB(submission)
	mock.existingFound = true
	service := NewEvolutionService(db.New(mock))

	unit, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted || mock.submission.Status != "promoted" {
		t.Fatalf("status/submission = %q/%q, want promoted/promoted", status, mock.submission.Status)
	}
	if unit.ID != mock.unit.ID || mock.submission.PromotedUnitID != mock.unit.ID {
		t.Fatalf("promoted unit = %v/%v, want existing %v", unit.ID, mock.submission.PromotedUnitID, mock.unit.ID)
	}
}

func TestCurateSubmissionMatchesMetadataRelevantAgents(t *testing.T) {
	submission := validMemorySubmission()
	submission.Tools = []string{"github"}
	submission.TaskTypes = []string{"review"}
	submission.Languages = []string{"go"}
	mock := newEvolutionMockDB(submission)
	mock.unit.Tools = submission.Tools
	mock.unit.TaskTypes = submission.TaskTypes
	mock.unit.Languages = submission.Languages
	matchedID := testUUID(61)
	unmatchedID := testUUID(62)
	mock.agents = append(mock.agents,
		db.Agent{ID: matchedID, WorkspaceID: submission.WorkspaceID, Name: "Go reviewer", Description: "Reviews Go pull requests with GitHub context."},
		db.Agent{ID: unmatchedID, WorkspaceID: submission.WorkspaceID, Name: "Designer", Description: "Polishes product copy."},
	)
	service := NewEvolutionService(db.New(mock))

	unit, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted {
		t.Fatalf("status = %q, want promoted", status)
	}
	if _, err := service.matchSourceAgent(context.Background(), submission, unit); err != nil {
		t.Fatalf("matchSourceAgent error = %v", err)
	}
	if len(mock.deliveries) != 2 {
		t.Fatalf("deliveries = %d, want source + matched agent: %#v", len(mock.deliveries), mock.deliveries)
	}
	if mock.deliveries[0].TargetAgentID != submission.SourceAgentID {
		t.Fatalf("first delivery target = %v, want source %v", mock.deliveries[0].TargetAgentID, submission.SourceAgentID)
	}
	if mock.deliveries[1].TargetAgentID != matchedID || mock.deliveries[1].MatcherScore < 0.3 {
		t.Fatalf("matched delivery = target %v score %.2f, want %v score >= 0.3", mock.deliveries[1].TargetAgentID, mock.deliveries[1].MatcherScore, matchedID)
	}
	var details map[string]any
	if err := json.Unmarshal(mock.deliveries[1].MatcherDetails, &details); err != nil {
		t.Fatalf("matcher details JSON: %v", err)
	}
	if details["strategy"] != "deterministic_metadata" {
		t.Fatalf("matcher details = %#v", details)
	}
}

func TestCurateSubmissionDoesNotMatchSingleBroadTool(t *testing.T) {
	submission := validMemorySubmission()
	submission.Tools = []string{"github"}
	mock := newEvolutionMockDB(submission)
	mock.unit.Tools = submission.Tools
	broadID := testUUID(63)
	mock.agents = append(mock.agents,
		db.Agent{ID: broadID, WorkspaceID: submission.WorkspaceID, Name: "GitHub helper", Description: "Handles GitHub notifications."},
	)
	service := NewEvolutionService(db.New(mock))

	unit, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted {
		t.Fatalf("status = %q, want promoted", status)
	}
	if _, err := service.matchSourceAgent(context.Background(), submission, unit); err != nil {
		t.Fatalf("matchSourceAgent error = %v", err)
	}
	if len(mock.deliveries) != 1 || mock.deliveries[0].TargetAgentID != submission.SourceAgentID {
		t.Fatalf("deliveries = %#v, want only source agent", mock.deliveries)
	}
}

func TestShouldCreateEvolutionDeliveryMatchAllowsComplementaryMetadata(t *testing.T) {
	target := evolutionDeliveryMatchTarget{
		Score: 0.3,
		Details: map[string]any{"matched": map[string][]string{
			"tools":     {"github"},
			"languages": {"go"},
		}},
	}
	if !shouldCreateEvolutionDeliveryMatch(target) {
		t.Fatal("expected complementary tool+language metadata to pass")
	}
}

func TestShouldCreateEvolutionDeliveryMatchRejectsSingleBroadDimension(t *testing.T) {
	target := evolutionDeliveryMatchTarget{
		Score:   0.3,
		Details: map[string]any{"matched": map[string][]string{"tools": {"github"}}},
	}
	if shouldCreateEvolutionDeliveryMatch(target) {
		t.Fatal("expected single broad tool match to be rejected")
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

func TestOpenAICompatibleEvolutionReviewerReturnsParsedReview(t *testing.T) {
	var request openAIChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"promote\",\"confidence\":0.92,\"risk_level\":\"low\",\"unit_type\":\"memory\",\"title\":\"Safe lesson\",\"summary\":\"Safe reusable lesson\",\"suggested_tags\":[\"go\"],\"suggested_task_types\":[\"review\"],\"suggested_scope\":\"workspace\",\"risks\":[],\"rationale\":\"safe to promote\"}"}}]}`))
	}))
	defer server.Close()

	reviewer, err := NewOpenAICompatibleEvolutionReviewer(EvolutionHTTPReviewConfig{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, Timeout: time.Second, Client: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEvolutionReviewer: %v", err)
	}
	result, err := reviewer.Review(context.Background(), evolutionReviewInput(validMemorySubmission(), nil))
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if result.Decision != EvolutionReviewPromote || result.RiskLevel != EvolutionReviewRiskLow || result.Confidence != 0.92 {
		t.Fatalf("result = (%q, %q, %v), want promote low 0.92", result.Decision, result.RiskLevel, result.Confidence)
	}
	if request.Model != "gpt-test" || len(request.Messages) != 2 {
		t.Fatalf("request model/messages = %q/%d", request.Model, len(request.Messages))
	}
	if !strings.Contains(request.Messages[0].Content, `"decision": "promote | needs_review | reject"`) {
		t.Fatalf("system prompt missing output schema: %s", request.Messages[0].Content)
	}
	if !strings.Contains(request.Messages[1].Content, "deterministic_validation") {
		t.Fatalf("user payload missing deterministic validation summary: %s", request.Messages[1].Content)
	}
}

func TestOpenAICompatibleEvolutionReviewerProviderErrorNeedsReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"upstream failed"}}`, http.StatusBadGateway)
	}))
	defer server.Close()

	reviewer, err := NewOpenAICompatibleEvolutionReviewer(EvolutionHTTPReviewConfig{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, Timeout: time.Second, Client: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleEvolutionReviewer: %v", err)
	}
	result, err := reviewer.Review(context.Background(), evolutionReviewInput(validMemorySubmission(), nil))
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if result.Decision != EvolutionReviewNeedsReview || result.RiskLevel != EvolutionReviewRiskMedium {
		t.Fatalf("result = (%q, %q), want needs_review medium", result.Decision, result.RiskLevel)
	}
	if result.Metadata["kind"] != "provider_error" {
		t.Fatalf("metadata kind = %v, want provider_error", result.Metadata["kind"])
	}
}

type fakeAgentReviewBackend struct {
	prompt string
	opts   agentpkg.ExecOptions
	result agentpkg.Result
	err    error
}

func (f *fakeAgentReviewBackend) Execute(_ context.Context, prompt string, opts agentpkg.ExecOptions) (*agentpkg.Session, error) {
	f.prompt = prompt
	f.opts = opts
	if f.err != nil {
		return nil, f.err
	}
	messages := make(chan agentpkg.Message)
	results := make(chan agentpkg.Result, 1)
	close(messages)
	results <- f.result
	close(results)
	return &agentpkg.Session{Messages: messages, Result: results}, nil
}

func TestAgentEvolutionReviewerReturnsParsedReview(t *testing.T) {
	backend := &fakeAgentReviewBackend{result: agentpkg.Result{
		Status:    "completed",
		Output:    `{"decision":"promote","confidence":0.91,"risk_level":"low","unit_type":"memory","title":"Safe lesson","summary":"Safe reusable lesson","suggested_tags":["go"],"suggested_task_types":["review"],"suggested_scope":"workspace","risks":[],"rationale":"safe to promote"}`,
		SessionID: "sess-1",
	}}
	reviewer, err := NewAgentEvolutionReviewer(EvolutionAgentReviewConfig{Provider: "pi", Model: "anthropic/claude-sonnet", Timeout: time.Second, Backend: backend})
	if err != nil {
		t.Fatalf("NewAgentEvolutionReviewer: %v", err)
	}

	result, err := reviewer.Review(context.Background(), evolutionReviewInput(validMemorySubmission(), nil))
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if result.Decision != EvolutionReviewPromote || result.RiskLevel != EvolutionReviewRiskLow || result.Confidence != 0.91 {
		t.Fatalf("result = (%q, %q, %v), want promote low 0.91", result.Decision, result.RiskLevel, result.Confidence)
	}
	if !strings.Contains(backend.prompt, "deterministic_validation") {
		t.Fatalf("agent prompt missing deterministic validation summary: %s", backend.prompt)
	}
	if !strings.Contains(backend.opts.SystemPrompt, `"decision": "promote | needs_review | reject"`) {
		t.Fatalf("agent system prompt missing output schema: %s", backend.opts.SystemPrompt)
	}
	if strings.Join(backend.opts.CustomArgs, " ") != "--no-tools" {
		t.Fatalf("agent custom args = %v, want --no-tools", backend.opts.CustomArgs)
	}
	if result.Metadata["source"] != "agent_reviewer" || result.Metadata["provider"] != "pi" || result.Metadata["session_id"] != "sess-1" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestAgentEvolutionReviewerFailureNeedsReview(t *testing.T) {
	backend := &fakeAgentReviewBackend{result: agentpkg.Result{Status: "failed", Error: "agent failed"}}
	reviewer, err := NewAgentEvolutionReviewer(EvolutionAgentReviewConfig{Provider: "pi", Timeout: time.Second, Backend: backend})
	if err != nil {
		t.Fatalf("NewAgentEvolutionReviewer: %v", err)
	}

	result, err := reviewer.Review(context.Background(), evolutionReviewInput(validMemorySubmission(), nil))
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if result.Decision != EvolutionReviewNeedsReview || result.Metadata["kind"] != "agent_error" {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseEvolutionReviewResultHardensInvalidOutput(t *testing.T) {
	metadata := map[string]any{"source": "test"}
	invalidJSON := parseEvolutionReviewResult("not json", metadata)
	if invalidJSON.Decision != EvolutionReviewNeedsReview || invalidJSON.Metadata["kind"] != "invalid_json" {
		t.Fatalf("invalid JSON result = %#v", invalidJSON)
	}

	unknownEnums := parseEvolutionReviewResult(`{"decision":"approve","confidence":2,"risk_level":"tiny","rationale":"ok"}`, metadata)
	if unknownEnums.Decision != EvolutionReviewNeedsReview {
		t.Fatalf("decision = %q, want needs_review", unknownEnums.Decision)
	}
	if unknownEnums.RiskLevel != EvolutionReviewRiskMedium {
		t.Fatalf("risk = %q, want medium", unknownEnums.RiskLevel)
	}
	if unknownEnums.Confidence != 1 {
		t.Fatalf("confidence = %v, want clamped 1", unknownEnums.Confidence)
	}
	if unknownEnums.Metadata["normalized_decision_reason"] != "unknown decision" {
		t.Fatalf("missing normalized decision metadata: %#v", unknownEnums.Metadata)
	}
}

func TestEvolutionReviewPayloadTruncatesLargeContent(t *testing.T) {
	input := evolutionReviewInput(validMemorySubmission(), []db.EvolutionUnitSubmissionFile{
		{Path: "a.md", Content: strings.Repeat("a", maxEvolutionReviewFileBytes*2), MimeType: "text/markdown", SizeBytes: int64(maxEvolutionReviewFileBytes * 2)},
		{Path: "b.md", Content: strings.Repeat("b", maxEvolutionReviewFileBytes*2), MimeType: "text/markdown", SizeBytes: int64(maxEvolutionReviewFileBytes * 2)},
	})
	input.Content = strings.Repeat("c", maxEvolutionReviewFileBytes*2)

	payload, meta := evolutionReviewPayload(input)
	if len(payload) > maxEvolutionReviewPayloadBytes {
		t.Fatalf("payload bytes = %d, want <= %d", len(payload), maxEvolutionReviewPayloadBytes)
	}
	if meta["content_truncated"] != true {
		t.Fatalf("content_truncated = %v, want true", meta["content_truncated"])
	}
	var decoded struct {
		Files []struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	for _, file := range decoded.Files {
		if len(file.Content) > maxEvolutionReviewFileBytes {
			t.Fatalf("file content bytes = %d, want <= %d", len(file.Content), maxEvolutionReviewFileBytes)
		}
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
