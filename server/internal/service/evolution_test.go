package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	submissions   []db.EvolutionUnitSubmission
	files         []db.EvolutionUnitSubmissionFile
	unit          db.SharedEvolutionUnit
	version       db.SharedEvolutionUnitVersion
	agents        []db.Agent
	memories      []db.AgentMemory
	existingFound bool
	activeUnits   []db.SharedEvolutionUnit
	maxVersion    int32
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
		maxVersion: 1,
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
	case strings.Contains(sql, "ListCandidateEvolutionSubmissions"):
		rows := make([][]any, 0, len(m.submissions))
		for _, item := range m.submissions {
			rows = append(rows, evolutionSubmissionValues(item))
		}
		if len(rows) == 0 {
			rows = append(rows, evolutionSubmissionValues(m.submission))
		}
		return &evolutionMockRows{rows: rows}, nil
	case strings.Contains(sql, "FROM evolution_unit_submission_file"):
		rows := make([][]any, 0, len(m.files))
		for _, file := range m.files {
			rows = append(rows, evolutionFileValues(file))
		}
		return &evolutionMockRows{rows: rows}, nil
	case strings.Contains(sql, "FROM shared_evolution_unit_file"):
		return &evolutionMockRows{}, nil
	case strings.Contains(sql, "FROM shared_evolution_unit"):
		rows := make([][]any, 0, len(m.activeUnits))
		for _, unit := range m.activeUnits {
			rows = append(rows, sharedEvolutionUnitValues(unit))
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
	case strings.Contains(sql, "SyncSharedEvolutionUnitMatchMetadata"):
		updated := m.unit
		updated.Title = stringArg(args, 2)
		updated.CanonicalSummary = stringArg(args, 3)
		updated.Content = stringArg(args, 4)
		updated.Tags = stringSliceArg(args, 5)
		updated.Tools = stringSliceArg(args, 6)
		updated.TaskTypes = stringSliceArg(args, 7)
		updated.ProjectTypes = stringSliceArg(args, 8)
		updated.Languages = stringSliceArg(args, 9)
		updated.Frameworks = stringSliceArg(args, 10)
		m.unit = updated
		for i, unit := range m.activeUnits {
			if unit.ID == updated.ID {
				m.activeUnits[i] = updated
				break
			}
		}
		return &evolutionMockRow{values: sharedEvolutionUnitValues(updated)}
	case strings.Contains(sql, "MaxSharedEvolutionUnitVersion"):
		return &evolutionMockRow{values: []any{m.maxVersion}}
	case strings.Contains(sql, "CreateSharedEvolutionUnitVersion"):
		m.version.WorkspaceID = uuidArg(args, 0)
		m.version.UnitID = uuidArg(args, 1)
		m.version.Version = int32Arg(args, 2)
		m.version.Title = stringArg(args, 3)
		m.version.Content = stringArg(args, 4)
		m.version.Metadata = bytesArg(args, 5)
		m.version.Applies = bytesArg(args, 6)
		m.version.SourceSubmissionIds = []pgtype.UUID{uuidArg(args, 7)}
		m.version.ChangeReason = stringArg(args, 8)
		return &evolutionMockRow{values: sharedEvolutionUnitVersionValues(m.version)}
	case strings.Contains(sql, "SetSharedEvolutionUnitCurrentVersion"):
		updated := m.unit
		updated.ID = uuidArg(args, 0)
		updated.WorkspaceID = uuidArg(args, 1)
		updated.CurrentVersionID = uuidArg(args, 2)
		for _, unit := range m.activeUnits {
			if unit.ID == updated.ID {
				updated = unit
				updated.CurrentVersionID = uuidArg(args, 2)
				break
			}
		}
		m.unit = updated
		return &evolutionMockRow{values: sharedEvolutionUnitValues(updated)}
	case strings.Contains(sql, "CreateSharedEvolutionUnit"):
		return &evolutionMockRow{values: sharedEvolutionUnitValues(m.unit)}
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
	case strings.Contains(sql, "UpsertSharedEvolutionUnitFile"):
		return &evolutionMockRow{values: []any{testUUID(99), m.submission.WorkspaceID, uuidArg(args, 1), uuidArg(args, 2), stringArg(args, 3), stringArg(args, 4), stringArg(args, 5), stringArg(args, 6), int64Arg(args, 7), pgtype.Timestamptz{Time: time.Now(), Valid: true}}}
	case strings.Contains(sql, "sync_key = $2"):
		syncKey := stringArg(args, 1)
		for _, memory := range m.memories {
			if memory.SyncKey == syncKey {
				return &evolutionMockRow{values: agentMemoryValues(memory)}
			}
		}
		return &evolutionMockRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "FROM agent_memory") && strings.Contains(sql, "name = $2"):
		return &evolutionMockRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "INSERT INTO agent_memory"):
		memory := db.AgentMemory{
			ID:          testUUID(91),
			WorkspaceID: uuidArg(args, 0),
			AgentID:     uuidArg(args, 1),
			Name:        stringArg(args, 2),
			Content:     stringArg(args, 3),
			Config:      bytesArg(args, 4),
			SyncKey:     stringArg(args, 5),
			ContentHash: stringArg(args, 6),
			CreatedBy:   uuidArg(args, 7),
		}
		m.memories = append(m.memories, memory)
		return &evolutionMockRow{values: agentMemoryValues(memory)}
	case strings.Contains(sql, "UPDATE agent_memory SET"):
		id := uuidArg(args, 0)
		for i, memory := range m.memories {
			if memory.ID == id {
				if name := textArg(args, 1); name != "" {
					memory.Name = name
				}
				if content := textArg(args, 2); content != "" {
					memory.Content = content
				}
				memory.Config = bytesArg(args, 3)
				if hash := textArg(args, 4); hash != "" {
					memory.ContentHash = hash
				}
				m.memories[i] = memory
				return &evolutionMockRow{values: agentMemoryValues(memory)}
			}
		}
		return &evolutionMockRow{err: pgx.ErrNoRows}
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
	return []any{a.ID, a.WorkspaceID, a.Name, a.AvatarUrl, a.RuntimeMode, a.RuntimeConfig, a.Status, a.MaxConcurrentTasks, a.OwnerID, a.CreatedAt, a.UpdatedAt, a.Description, a.RuntimeID, a.Instructions, a.ArchivedAt, a.ArchivedBy, a.CustomEnv, a.CustomArgs, a.McpConfig, a.Model, a.ThinkingLevel}
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

func agentMemoryValues(m db.AgentMemory) []any {
	return []any{m.ID, m.WorkspaceID, m.AgentID, m.Name, m.Content, m.Config, m.SyncKey, m.ContentHash, m.CreatedBy, m.CreatedAt, m.UpdatedAt}
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

func int32Arg(args []interface{}, index int) int32 {
	if index >= len(args) {
		return 0
	}
	value, _ := args[index].(int32)
	return value
}

func int64Arg(args []interface{}, index int) int64 {
	if index >= len(args) {
		return 0
	}
	value, _ := args[index].(int64)
	return value
}

func textArg(args []interface{}, index int) string {
	if index >= len(args) {
		return ""
	}
	switch value := args[index].(type) {
	case string:
		return value
	case pgtype.Text:
		if value.Valid {
			return value.String
		}
	}
	return ""
}

func stringSliceArg(args []interface{}, index int) []string {
	if index >= len(args) {
		return nil
	}
	value, _ := args[index].([]string)
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
	submission := validSkillSubmission()
	submission.Confidence = "low"
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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

func TestCurateSubmissionSourceHumanReviewGateSkipsReviewer(t *testing.T) {
	for _, reviewEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("review_enabled_%t", reviewEnabled), func(t *testing.T) {
			submission := validSkillSubmission()
			submission.Evidence = []byte(`{"source":"memory_curation_l3_reviewer","requires_human_review":true}`)
			reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
			mock := newEvolutionMockDB(submission)
			mock.files = []db.EvolutionUnitSubmissionFile{
				{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
			}
			service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, reviewEnabled)

			_, status, err := service.curateSubmission(context.Background(), submission)
			if err != nil {
				t.Fatalf("curateSubmission error = %v", err)
			}
			if status != evolutionCurationNeedsReview || mock.submission.Status != "needs_review" || mock.submission.ReviewReason != "source requires human review" {
				t.Fatalf("status/review = %q/%q/%q", status, mock.submission.Status, mock.submission.ReviewReason)
			}
			if reviewer.called != 0 {
				t.Fatalf("reviewer called %d times, want 0", reviewer.called)
			}
		})
	}
}

func TestCurateSubmissionHumanReviewFlagOnlyGatesSkills(t *testing.T) {
	submission := validMemorySubmission()
	submission.Evidence = []byte(`{"source":"memory_curation_l3_reviewer","requires_human_review":true}`)
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionService(db.New(mock))

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted || mock.submission.Status != "promoted" {
		t.Fatalf("status/submission = %q/%q, want promoted/promoted", status, mock.submission.Status)
	}
}

func TestCurateSubmissionMemoryLowConfidenceNeedsReview(t *testing.T) {
	submission := validMemorySubmission()
	submission.Confidence = "low"
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionService(db.New(mock))

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationNeedsReview || mock.submission.Status != "needs_review" {
		t.Fatalf("status/submission = %q/%q, want needs_review/needs_review", status, mock.submission.Status)
	}
}

func TestCurateSubmissionMemoryUsesReviewerWhenEnabled(t *testing.T) {
	submission := validMemorySubmission()
	reviewer := &fakeEvolutionReviewer{result: EvolutionReviewResult{Decision: EvolutionReviewReject, Confidence: 0.9, RiskLevel: EvolutionReviewRiskHigh, Rationale: "unsafe"}}
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionServiceWithReviewer(db.New(mock), reviewer, true)

	_, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationRejected || mock.submission.Status != "rejected" {
		t.Fatalf("status/submission = %q/%q, want rejected/rejected", status, mock.submission.Status)
	}
	if reviewer.called != 1 {
		t.Fatalf("reviewer called %d times, want 1", reviewer.called)
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
	submission := validSkillSubmission()
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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

func TestCurateSubmissionSemanticDuplicateMarksPromotedWithoutNewUnit(t *testing.T) {
	submission := validSkillSubmission()
	submission.Title = "Reusable Go review checklist"
	submission.Summary = "Check Go pull requests for tests and migrations."
	submission.Content = "When reviewing Go pull requests, verify targeted tests and database migrations before approval."
	submission.ContentHash = "sha256:new"
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
	existing := mock.unit
	existing.ID = testUUID(64)
	existing.Title = "Go pull request review checklist"
	existing.CanonicalSummary = "Check Go pull requests for tests and migrations."
	existing.Content = "When reviewing Go pull requests, verify targeted tests and database migrations before approval."
	existing.Tags = []string{"go", "review"}
	existing.TaskTypes = []string{"review"}
	existing.Languages = []string{"go"}
	existing.Status = "active"
	mock.activeUnits = []db.SharedEvolutionUnit{existing}
	mock.maxVersion = 3
	if best, score := bestSemanticDuplicate(submission, mock.activeUnits); best.ID != existing.ID || score < semanticDedupeThreshold {
		t.Fatalf("semantic duplicate precheck = %v %.2f, want %v above threshold", best.ID, score, existing.ID)
	}
	service := NewEvolutionService(db.New(mock))

	unit, status, err := service.curateSubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateSubmission error = %v", err)
	}
	if status != evolutionCurationPromoted || unit.ID != existing.ID || mock.submission.PromotedUnitID != existing.ID {
		t.Fatalf("status/unit/promoted = %q/%v/%v, want semantic duplicate %v", status, unit.ID, mock.submission.PromotedUnitID, existing.ID)
	}
	if mock.version.Version != 4 || mock.version.ChangeReason != "semantic duplicate candidate" {
		t.Fatalf("version = %d reason %q, want 4 semantic duplicate candidate", mock.version.Version, mock.version.ChangeReason)
	}
}

func TestSemanticSimilaritySeparatesRelatedAndUnrelatedText(t *testing.T) {
	related := semanticSimilarity("review go pull request migrations tests", "go pr review test migration checklist")
	unrelated := semanticSimilarity("review go pull request migrations tests", "polish landing page typography colors")
	if related <= unrelated || related < semanticMatchThreshold {
		t.Fatalf("related %.2f unrelated %.2f, want related above threshold and unrelated lower", related, unrelated)
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

func TestScoreEvolutionDeliveryTargetPrefersSubmissionMetadata(t *testing.T) {
	submission := validSkillSubmission()
	submission.Tools = []string{"github", "docker"}
	submission.Languages = []string{"go"}
	unit := db.SharedEvolutionUnit{
		Tools:     []string{"github"},
		Languages: []string{"python"},
	}
	agent := db.Agent{
		Name:        "Container helper",
		Description: "Builds docker images for Go services on GitHub.",
	}
	target := scoreEvolutionDeliveryTarget(testUUID(11), unit, agent, &submission)
	if !shouldCreateEvolutionDeliveryMatch(target) {
		t.Fatalf("target = %#v, want match from submission metadata", target)
	}
	unitOnly := scoreEvolutionDeliveryTarget(testUUID(11), unit, agent, nil)
	if shouldCreateEvolutionDeliveryMatch(unitOnly) {
		t.Fatalf("unit-only target = %#v, want no match from stale unit metadata", unitOnly)
	}
}

func TestCurateSubmissionWithReviewerPromoteLowRiskPromotes(t *testing.T) {
	submission := validSkillSubmission()
	reviewer := &fakeEvolutionReviewer{result: promoteLowRiskReview()}
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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
	submission := validSkillSubmission()
	reviewer := &fakeEvolutionReviewer{result: EvolutionReviewResult{Decision: EvolutionReviewPromote, Confidence: 0.9, RiskLevel: EvolutionReviewRiskMedium, Rationale: "medium risk"}}
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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
	submission := validSkillSubmission()
	reviewer := &fakeEvolutionReviewer{result: EvolutionReviewResult{Decision: EvolutionReviewReject, Confidence: 0.8, RiskLevel: EvolutionReviewRiskHigh, Rationale: "unsafe"}}
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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
	submission := validSkillSubmission()
	reviewer := &fakeEvolutionReviewer{err: errors.New("review failed")}
	mock := newEvolutionMockDB(submission)
	mock.files = []db.EvolutionUnitSubmissionFile{
		{Path: "SKILL.md", Content: validSkillMainFile(), MimeType: "text/markdown", SizeBytes: int64(len(validSkillMainFile()))},
	}
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
