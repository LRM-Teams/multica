package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/memorysignal"
)

func TestFilepathToSlash(t *testing.T) {
	if got := filepathToSlash(`memory\MEMORY.md`); got != "memory/MEMORY.md" {
		t.Fatalf("got %q", got)
	}
}

func TestAllowedAgentMemoryScopeTypes(t *testing.T) {
	for _, scope := range []string{"agent_global", "agent_state", "user", "channel", "project"} {
		if _, ok := allowedAgentMemoryScopeTypes[scope]; !ok {
			t.Fatalf("missing %s", scope)
		}
	}
}

func TestSelfReviewCandidateShareable(t *testing.T) {
	if selfReviewCandidateShareable("user", "none") {
		t.Fatal("user scope must not be shareable")
	}
	if selfReviewCandidateShareable("agent", "sensitive") {
		t.Fatal("sensitive must not be shareable")
	}
	if selfReviewCandidateShareable("agent", "") {
		t.Fatal("missing sensitivity must fail closed")
	}
	if !selfReviewCandidateShareable("agent", "none") {
		t.Fatal("agent/none should be shareable")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abcdefghij", 4); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
}

func TestMissedWriteDetectionHelpers(t *testing.T) {
	writes := []memorysignal.WriteEntry{{RelPath: "memory/daily/x.md", ScopeType: "agent_daily", FileKey: "DAILY"}}
	miss, ok := memorysignal.DetectMissedWrite("记住这个偏好", nil, writes, "m1")
	if !ok {
		t.Fatal("expected miss")
	}
	if miss.CandidateType != "user_preference" {
		t.Fatalf("type=%q", miss.CandidateType)
	}
}
