package memorycuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAllPromotesAndCleansReview(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	daily := `# Daily Memory - 2026-07-08

## Activity Summary
- Helped with memory curation planning.

## Decisions And Stable Facts
- Memory curation should run in four stages at 01:00, 02:00, 03:00, and 04:00.

## User / Teammate Preferences Observed
- jianghp3 likes playing basketball.

## Temporary State And Follow-ups
- Follow up on 2026-07-10 with the memory curation implementation.

## Evidence Index
- channel_message:abc - Human asked to remember this.

## Curation Status
- l1_recorded_at: 2026-07-09T01:00:00Z
- l2_extracted_at:
- l3_promoted_at:
- l4_curated_at:
`
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "daily", "2026-07-08.md"), []byte(daily), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewEngine().Run(Options{
		WorkspacesRoot: root,
		WorkspaceID:    "ws-1",
		AgentIDs:       []string{"agent-1"},
		Stage:          StageAll,
		Since:          mustDate("2026-07-08"),
		Until:          mustDate("2026-07-08"),
		Now:            mustDateTime("2026-07-09T05:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReviewCandidatesAdded != 3 {
		t.Fatalf("ReviewCandidatesAdded = %d, want 3", res.ReviewCandidatesAdded)
	}
	if res.EntriesPromoted != 3 {
		t.Fatalf("EntriesPromoted = %d, want 3", res.EntriesPromoted)
	}
	if res.SharedCandidatesAdded != 1 {
		t.Fatalf("SharedCandidatesAdded = %d, want 1", res.SharedCandidatesAdded)
	}
	assertContains(t, filepath.Join(agentRoot, "sync_queue", "memory-candidates.jsonl"), "shared_mem_20260708")
	assertContains(t, filepath.Join(agentRoot, "sync_queue", "memory-candidates.jsonl"), "\"suggested_scope\":\"workspace\"")
	assertContains(t, filepath.Join(agentRoot, "memory", "USER.md"), "jianghp3 likes playing basketball")
	assertContains(t, filepath.Join(agentRoot, "memory", "MEMORY.md"), "Memory curation should run in four stages")
	assertContains(t, filepath.Join(agentRoot, "memory", "STATE.md"), "Follow up on 2026-07-10")
	assertNotContains(t, filepath.Join(agentRoot, "memory", "REVIEW.md"), "jianghp3 likes playing basketball")
}

func TestL4ExpiresStateAndClosedReviewEntries(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	state := stateHeader + `
§
[type:temporary]
[expires_at:2026-07-07]
- Old temporary work.
§
[type:temporary]
[expires_at:2026-07-20]
- Future temporary work.
`
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "STATE.md"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	review := renderReview([]reviewEntry{{ID: "mem_1", Type: "preference", Status: "promoted", Confidence: "high", Title: "Closed", Body: "Closed item"}, {ID: "mem_2", Type: "preference", Status: "candidate", Confidence: "high", Title: "Open", Body: "Open item"}})
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "REVIEW.md"), []byte(review), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewEngine().Run(Options{WorkspacesRoot: root, WorkspaceID: "ws-1", AgentIDs: []string{"agent-1"}, Stage: StageL4, Since: mustDate("2026-07-08"), Until: mustDate("2026-07-08"), Now: mustDateTime("2026-07-09T04:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if res.EntriesArchived != 2 {
		t.Fatalf("EntriesArchived = %d, want 2", res.EntriesArchived)
	}
	assertNotContains(t, filepath.Join(agentRoot, "memory", "STATE.md"), "Old temporary work")
	assertContains(t, filepath.Join(agentRoot, "memory", "STATE.md"), "Future temporary work")
	assertNotContains(t, filepath.Join(agentRoot, "memory", "REVIEW.md"), "Closed item")
	assertContains(t, filepath.Join(agentRoot, "memory", "REVIEW.md"), "Open item")
}

func TestMergeAgentRunResultIncludesEvidence(t *testing.T) {
	dst := AgentRunResult{WorkspaceID: "ws-1", AgentID: "agent-1"}
	mergeAgentRunResult(&dst, AgentRunResult{Changed: true, EvidenceCollected: 2, DailyFilesWritten: 1})
	mergeAgentRunResult(&dst, AgentRunResult{EvidenceCollected: 3, ReviewCandidatesAdded: 4})
	if !dst.Changed {
		t.Fatal("Changed = false, want true")
	}
	if dst.EvidenceCollected != 5 {
		t.Fatalf("EvidenceCollected = %d, want 5", dst.EvidenceCollected)
	}
	if dst.DailyFilesWritten != 1 || dst.ReviewCandidatesAdded != 4 {
		t.Fatalf("merged counters = %#v", dst)
	}
}

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func mustDateTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func assertContains(t *testing.T, path, needle string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), needle) {
		t.Fatalf("%s does not contain %q:\n%s", path, needle, string(b))
	}
}

func assertNotContains(t *testing.T, path, needle string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), needle) {
		t.Fatalf("%s unexpectedly contains %q:\n%s", path, needle, string(b))
	}
}
