package memorycuration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAllKeepsDeterministicCandidatesUntilSensitivityIsExplicit(t *testing.T) {
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
	res, err := NewEngine(routeAllToMemoryReviewer{}).Run(Options{
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
	if res.EntriesPromoted != 0 || res.SharedCandidatesAdded != 0 || res.EntriesReviewed != 0 {
		t.Fatalf("unsafe candidates were promoted: %#v", res)
	}
	assertContains(t, filepath.Join(agentRoot, "memory", "REVIEW.md"), "jianghp3 likes playing basketball")
	assertContains(t, filepath.Join(agentRoot, "memory", "REVIEW.md"), "sensitivity: unknown")
	assertNotContains(t, filepath.Join(agentRoot, "memory", "USER.md"), "jianghp3 likes playing basketball")
	assertNotContains(t, filepath.Join(agentRoot, "memory", "MEMORY.md"), "Memory curation should run in four stages")
	assertNotContains(t, filepath.Join(agentRoot, "memory", "STATE.md"), "Follow up on 2026-07-10")
}

func TestRunAllInvokesStageAgentForEveryStageWithDBEvidence(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	agent := &recordingStageAgent{}
	res, err := NewEngine().Run(Options{
		WorkspacesRoot: root,
		WorkspaceID:    "ws-1",
		AgentIDs:       []string{"agent-1"},
		Stage:          StageAll,
		Since:          mustDate("2026-07-08"),
		Until:          mustDate("2026-07-08"),
		Now:            mustDateTime("2026-07-09T05:00:00Z"),
		Mode:           "auto",
		DBEvidence:     map[string][]EvidenceItem{"agent-1": {{Kind: "channel_message", ID: "msg-1", Title: "Remember", Snippet: "User likes direct updates."}}},
		StageAgent:     agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Stage{StageL1, StageL2, StageL3, StageL4}
	if strings.Join(stageNames(agent.calls), ",") != strings.Join(stageNames(want), ",") {
		t.Fatalf("stage calls = %v, want %v", agent.calls, want)
	}
	if len(agent.evidenceCounts) == 0 || agent.evidenceCounts[0] != 1 {
		t.Fatalf("agent did not receive DB evidence: %#v", agent.evidenceCounts)
	}
	if res.DailyFilesWritten != 1 || res.ReviewCandidatesAdded != 1 || res.EntriesReviewed != 1 || res.EntriesPromoted != 1 {
		t.Fatalf("agentic run stats = %#v", res)
	}
	assertContains(t, filepath.Join(agentRoot, "memory", "MEMORY.md"), "User likes direct updates.")
}

type recordingStageAgent struct {
	calls          []Stage
	evidenceCounts []int
}

func (r *recordingStageAgent) RunStage(_ context.Context, input StageAgentInput) (StageAgentOutput, error) {
	r.calls = append(r.calls, input.Stage)
	r.evidenceCounts = append(r.evidenceCounts, len(input.DBEvidence))
	switch input.Stage {
	case StageL1:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `# Daily Memory - 2026-07-08

## Activity Summary
- channel_message:msg-1 - User likes direct updates.

## Decisions And Stable Facts
- User likes direct updates.

## User / Teammate Preferences Observed
- No user preference extracted.

## Temporary State And Follow-ups
- No temporary follow-ups extracted.

## Evidence Index
- channel_message:msg-1 - User likes direct updates.

## Curation Status
- l1_recorded_at: 2026-07-09T05:00:00Z
- l2_extracted_at:
- l3_promoted_at:
- l4_curated_at:
`}, nil
	case StageL2:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `{"candidates":[{"type":"stable_fact","title":"Direct updates","body":"User likes direct updates.","proposed_destination":"MEMORY.md","sensitivity":"none","confidence":"high","evidence":["channel_message:msg-1"]}]}`}, nil
	case StageL3:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `{"reviews":[{"entry_id":"` + input.ReviewEntries[0].ID + `","route":"memory","confidence":0.95,"sensitivity":"none","rationale":"stable preference","memory":{"title":"Direct updates","body":"User likes direct updates."}}]}`}, nil
	case StageL4:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `{"archive_review_ids":[],"archive_state_contains":[],"dedupe_hints":[],"notes":"ok"}`}, nil
	default:
		return StageAgentOutput{}, nil
	}
}

func stageNames(stages []Stage) []string {
	out := make([]string, 0, len(stages))
	for _, stage := range stages {
		out = append(out, string(stage))
	}
	return out
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
	mergeAgentRunResult(&dst, AgentRunResult{Changed: true, EvidenceCollected: 2, DailyFilesWritten: 1, SharedCandidatesAdded: 1})
	mergeAgentRunResult(&dst, AgentRunResult{EvidenceCollected: 3, ReviewCandidatesAdded: 4, SharedCandidatesSynced: 2})
	if !dst.Changed {
		t.Fatal("Changed = false, want true")
	}
	if dst.EvidenceCollected != 5 {
		t.Fatalf("EvidenceCollected = %d, want 5", dst.EvidenceCollected)
	}
	if dst.SharedCandidatesAdded != 1 || dst.SharedCandidatesSynced != 2 {
		t.Fatalf("shared counters = %#v", dst)
	}
	if dst.DailyFilesWritten != 1 || dst.ReviewCandidatesAdded != 4 {
		t.Fatalf("merged counters = %#v", dst)
	}
}

func TestSharedMemoryEligibilityUsesNormalizedSafetyFields(t *testing.T) {
	entry := reviewEntry{Status: " candidate ", Confidence: " HIGH ", Sensitivity: " UNKNOWN ", Scope: " WORKSPACE ", Type: "stable_fact", Body: "Workspace agents should run go test before PRs."}
	if entryEligibleForSharedMemory(entry) {
		t.Fatal("unknown sensitivity should not be shared")
	}
	entry.Sensitivity = " none "
	if !entryEligibleForSharedMemory(entry) {
		t.Fatal("normalized safe workspace entry should be shared")
	}
}

type routeAllToMemoryReviewer struct{}

func (routeAllToMemoryReviewer) Review(_ context.Context, input L3ReviewInput) (L3ReviewOutput, error) {
	out := L3ReviewOutput{Provider: "test", Model: "deterministic"}
	for _, entry := range input.Entries {
		out.Decisions = append(out.Decisions, L3ReviewDecision{
			EntryID:    entry.ID,
			Route:      L3RouteMemory,
			Confidence: 1,
			Rationale:  "test memory route",
			Memory:     L3MemoryDraft{Title: entry.Title, Body: entry.Body},
		})
	}
	return out, nil
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
