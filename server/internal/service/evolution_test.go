package service

import (
	"strings"
	"testing"

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
