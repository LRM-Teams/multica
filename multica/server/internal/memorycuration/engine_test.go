package memorycuration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyL2KeepsDeterministicCandidatesUntilSensitivityIsExplicit(t *testing.T) {
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
		Stage:          StageL2,
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

func TestRunAllInvokesSelfReviewThenTeamCurationWithDBEvidence(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "REVIEW.md"), []byte("# Memory Review\n\n- pending team fact\n"), 0o644); err != nil {
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
	want := []Stage{StageAgentSelfReview, StageTeamCuration}
	if strings.Join(stageNames(agent.calls), ",") != strings.Join(stageNames(want), ",") {
		t.Fatalf("stage calls = %v, want %v", agent.calls, want)
	}
	if len(agent.evidenceCounts) < 2 {
		t.Fatalf("expected self-review + team curation evidence counts: %#v", agent.evidenceCounts)
	}
	if agent.evidenceCounts[0] != 1 {
		t.Fatalf("self-review did not receive DB evidence: %#v", agent.evidenceCounts)
	}
	if agent.evidenceCounts[1] != 0 {
		t.Fatalf("team curation must not receive raw chat DB evidence: %#v", agent.evidenceCounts)
	}
	if len(agent.localFileCounts) < 2 || agent.localFileCounts[1] == 0 {
		t.Fatalf("team curation should receive self-review local files: %#v", agent.localFileCounts)
	}
	if res.DailyFilesWritten != 1 || res.ReviewCandidatesAdded != 1 || res.SharedCandidatesAdded != 1 {
		t.Fatalf("agentic run stats = %#v", res)
	}
	for _, ev := range res.Events {
		if ev.Status == "running" {
			t.Fatalf("completed run left event running: %#v", res.Events)
		}
	}
	if !hasRunEvent(res.Events, "read_local_files", "agent-1", "done") || !hasRunEvent(res.Events, "invoked_curator", "agent-1", "done") || !hasRunEvent(res.Events, "invoked_curator", "team", "done") {
		t.Fatalf("missing fine-grained completion events: %#v", res.Events)
	}
}

func TestTeamCurationUsesPendingCandidatesNotRawChat(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "MEMORY.md"), []byte("# Agent Memory\n\nStable team-useful fact.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentRoot, "skills", "drafts", "shareable-runbook"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "skills", "drafts", "shareable-runbook", "SKILL.md"), []byte("# Shareable Runbook\n\nSteps...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &recordingStageAgent{}
	_, err := NewEngine().Run(Options{
		WorkspacesRoot: root,
		WorkspaceID:    "ws-1",
		AgentIDs:       []string{"agent-1"},
		Stage:          StageTeamCuration,
		Since:          mustDate("2026-07-08"),
		Until:          mustDate("2026-07-08"),
		Now:            mustDateTime("2026-07-09T05:00:00Z"),
		Mode:           "auto",
		DBEvidence: map[string][]EvidenceItem{
			"agent-1": {
				{Kind: "curation_candidate", ID: "cand-1", Title: "Pending", Snippet: "promote me"},
				{Kind: "channel_message", ID: "msg-should-not-matter", Title: "Raw chat", Snippet: "should still be passed if server sent it, but product path should not collect these"},
			},
		},
		StageAgent: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.calls) != 1 || agent.calls[0] != StageTeamCuration {
		t.Fatalf("calls = %v", agent.calls)
	}
	if agent.localFileCounts[0] < 2 {
		t.Fatalf("expected MEMORY + skill draft in local files, got %#v", agent.localFileCounts)
	}
	if agent.evidenceCounts[0] != 1 {
		t.Fatalf("team curation should keep only curation_candidate rows, got %#v", agent.evidenceCounts)
	}
	foundSkill := false
	for name := range agent.lastLocalFiles {
		if strings.Contains(name, "skills/drafts/shareable-runbook/SKILL.md") {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("skill draft missing from team local files: %#v", agent.lastLocalFiles)
	}
}

func hasRunEvent(events []RunEvent, key, agentID, status string) bool {
	for _, ev := range events {
		if ev.Key == key && ev.AgentID == agentID && ev.Status == status {
			return true
		}
	}
	return false
}

type recordingStageAgent struct {
	calls           []Stage
	evidenceCounts  []int
	localFileCounts []int
	lastLocalFiles  map[string]string
}

func (r *recordingStageAgent) RunStage(_ context.Context, input StageAgentInput) (StageAgentOutput, error) {
	r.calls = append(r.calls, input.Stage)
	r.evidenceCounts = append(r.evidenceCounts, len(input.DBEvidence))
	r.localFileCounts = append(r.localFileCounts, len(input.LocalFiles))
	r.lastLocalFiles = input.LocalFiles
	switch input.Stage {
	case StageAgentSelfReview:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `{"summary":"wrote daily","candidates":[{"type":"memory","scope":"agent","title":"Direct updates","content":"User likes direct updates.","confidence":0.95,"evidence_refs":["channel_message:msg-1"]}]}`}, nil
	case StageTeamCuration:
		return StageAgentOutput{Provider: "test", Model: "stage", Content: `{"team_knowledge":[{"kind":"memory","title":"Direct updates","content":"User likes direct updates.","source_candidate_ids":[]}]}`}, nil
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

func TestStageFilesWithScopedIncludesCanonicalScopesOnlyWithinBudget(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"users/member-1/USER.md":             "Frank prefers an immediate acknowledgment.",
		"users/member-1/RELATIONSHIP.md":     "Frank is the stable member identity.",
		"projects/project-1/STATE.md":        strings.Repeat("project state ", 400),
		"projects/project-1/DECISIONS.md":    "Use project-scoped memory.",
		"projects/project-1/MEMORY.md":       "Project conventions.",
		"channels/channel-1/CONTEXT.md":      "Product discussion channel.",
		"memory/daily/2026-07-16.md":         "historical daily must stay lazy",
		"notes/channels.md":                  "channel index must stay lazy",
		"projects/project-1/UNRECOGNIZED.md": "must not be loaded",
		"channels/channel-1/TRANSCRIPT.md":   "must not be loaded",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := stageFilesWithScoped(root)
	for _, name := range []string{"users/member-1/USER.md", "projects/project-1/STATE.md", "projects/project-1/DECISIONS.md", "channels/channel-1/CONTEXT.md"} {
		if got[name] == "" {
			t.Fatalf("canonical scoped file %q was not staged: %#v", name, got)
		}
	}
	for _, name := range []string{"memory/daily/2026-07-16.md", "notes/channels.md", "projects/project-1/UNRECOGNIZED.md", "channels/channel-1/TRANSCRIPT.md"} {
		if _, exists := got[name]; exists {
			t.Fatalf("lazy/unrecognized file %q was staged", name)
		}
	}
	total := 0
	for _, content := range got {
		total += len(content)
	}
	if total > maxScopedCurationBytes {
		t.Fatalf("scoped curation bytes = %d, want <= %d", total, maxScopedCurationBytes)
	}
}

func TestL4ExpiresAndDedupesProjectMemory(t *testing.T) {
	root := t.TempDir()
	agentRoot := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1")
	if err := ensureMemoryRoot(agentRoot); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(agentRoot, "projects", "project-1")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	state := "# Project State\n\n§\n[type:temporary]\n[expires_at:2026-07-07]\n- Old project blocker.\n§\n[type:temporary]\n[expires_at:2026-07-20]\n- Future project blocker.\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "STATE.md"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "DECISIONS.md"), []byte("# Decisions\n- Keep scope exact.\n- Keep scope exact.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewEngine().Run(Options{WorkspacesRoot: root, WorkspaceID: "ws-1", AgentIDs: []string{"agent-1"}, Stage: StageL4, Since: mustDate("2026-07-08"), Until: mustDate("2026-07-08"), Now: mustDateTime("2026-07-09T04:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if res.EntriesArchived != 1 || res.DuplicatesMerged != 1 {
		t.Fatalf("project cleanup stats = %#v", res)
	}
	assertNotContains(t, filepath.Join(projectRoot, "STATE.md"), "Old project blocker")
	assertContains(t, filepath.Join(projectRoot, "STATE.md"), "Future project blocker")
	if got := strings.Count(readTestFile(t, filepath.Join(projectRoot, "DECISIONS.md")), "Keep scope exact."); got != 1 {
		t.Fatalf("project decision copies = %d, want 1", got)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
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
