package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

func TestLocalMemoryCurationRuntimesSelectConfiguredOnlineProvider(t *testing.T) {
	d := &Daemon{
		cfg: Config{Agents: map[string]AgentEntry{
			"codex": {Path: "/usr/bin/codex"},
			"pi":    {Path: "/usr/bin/pi"},
		}},
		workspaces: map[string]*workspaceState{
			"ws-1": {runtimeIDs: []string{"rt-codex", "rt-pi"}},
			"ws-2": {runtimeIDs: []string{"rt-offline"}},
		},
		runtimeIndex: map[string]Runtime{
			"rt-codex":   {ID: "rt-codex", WorkspaceID: "ws-1", Provider: "codex", Status: "online"},
			"rt-pi":      {ID: "rt-pi", WorkspaceID: "ws-1", Provider: "pi", Status: "online"},
			"rt-offline": {ID: "rt-offline", WorkspaceID: "ws-2", Provider: "pi", Status: "offline"},
		},
	}

	runtimes := d.localMemoryCurationRuntimes()
	if len(runtimes) != 1 || runtimes[0].ID != "rt-codex" {
		t.Fatalf("runtimes = %#v, want stable configured online runtime", runtimes)
	}
}

func TestEvolutionCandidateJSONLQuarantinesMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory-candidates.jsonl")
	content := "{\"local_unit_id\":\"one\"}\nnot-json\n{\"local_unit_id\":\"two\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, issues, err := readEvolutionCandidateJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || len(issues) != 1 || issues[0].Line != 2 {
		t.Fatalf("items=%d issues=%#v", len(items), issues)
	}
	if err := quarantineEvolutionCandidateIssues(path, issues); err != nil {
		t.Fatal(err)
	}
	if err := quarantineEvolutionCandidateIssues(path, issues); err != nil {
		t.Fatal(err)
	}
	quarantined, err := os.ReadFile(path + ".invalid.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(quarantined), "\n") != 1 || !strings.Contains(string(quarantined), "not-json") {
		t.Fatalf("unexpected quarantine: %s", quarantined)
	}
}

func TestSecureSkillDraftBundleDirRejectsEscapes(t *testing.T) {
	agentRoot := filepath.Join(t.TempDir(), "agent")
	validDir := filepath.Join(agentRoot, "skills", "drafts", "candidate-1")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentSyncQueueDir(agentRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	// secureSkillDraftBundleDir returns a symlink-resolved path (it must, to
	// defend against a symlink escape below); on macOS the default TMPDIR
	// spells /private/var through its /var symlink, so t.TempDir() and the
	// function's return value canonicalize differently unless validDir is
	// resolved the same way before comparing.
	wantDir, err := filepath.EvalSymlinks(validDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := secureSkillDraftBundleDir(agentRoot, "../skills/drafts/candidate-1")
	if err != nil || got != wantDir {
		t.Fatalf("valid bundle got=%q want=%q err=%v", got, wantDir, err)
	}
	if _, err := secureSkillDraftBundleDir(agentRoot, "../../../outside"); err == nil {
		t.Fatal("expected traversal escape to be rejected")
	}

	outside := t.TempDir()
	link := filepath.Join(agentRoot, "skills", "drafts", "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := secureSkillDraftBundleDir(agentRoot, "../skills/drafts/linked"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestEvolutionAcknowledgementsRoundTrip(t *testing.T) {
	cfg := Config{WorkspacesRoot: t.TempDir()}
	want := map[string]string{"agent-1/candidate-1": "sha256:abc"}
	if err := saveEvolutionAcknowledgements(cfg, "workspace-1", want); err != nil {
		t.Fatal(err)
	}
	got, err := loadEvolutionAcknowledgements(cfg, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if got["agent-1/candidate-1"] != "sha256:abc" {
		t.Fatalf("acknowledgements = %#v", got)
	}
}

func TestEvolutionCandidateToBundlePreservesScopedMemoryIdentity(t *testing.T) {
	item := map[string]json.RawMessage{
		"unit_type":       json.RawMessage(`"preference"`),
		"local_unit_id":   json.RawMessage(`"preference-1"`),
		"title":           json.RawMessage(`"Immediate feedback"`),
		"content":         json.RawMessage(`"Acknowledge before starting work."`),
		"suggested_scope": json.RawMessage(`"user"`),
		"subject_type":    json.RawMessage(`"member"`),
		"subject_id":      json.RawMessage(`"member-1"`),
		"source_user_id":  json.RawMessage(`"member-1"`),
		"applies":         json.RawMessage(`{"project_ids":["project-1"],"channel_ids":["channel-1"],"task_types":["chat"],"expires_at":"2099-01-01T00:00:00Z"}`),
	}
	bundle := evolutionCandidateToBundle("workspace-1", "agent-1", item)
	if bundle.WorkspaceID != "workspace-1" || bundle.AgentID != "agent-1" || bundle.SuggestedScope != "user" {
		t.Fatalf("bundle identity/scope = %+v", bundle)
	}
	var payload map[string]any
	if err := json.Unmarshal(bundle.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["subject_type"] != "member" || payload["subject_id"] != "member-1" || payload["source_user_id"] != "member-1" {
		t.Fatalf("stable member identity was not preserved: %#v", payload)
	}
	var applies struct {
		ProjectIDs []string `json:"project_ids"`
		ChannelIDs []string `json:"channel_ids"`
		TaskTypes  []string `json:"task_types"`
		ExpiresAt  string   `json:"expires_at"`
	}
	if err := json.Unmarshal(bundle.Applies, &applies); err != nil {
		t.Fatal(err)
	}
	if len(applies.ProjectIDs) != 1 || applies.ProjectIDs[0] != "project-1" || len(applies.ChannelIDs) != 1 || applies.ChannelIDs[0] != "channel-1" || len(applies.TaskTypes) != 1 || applies.TaskTypes[0] != "chat" || applies.ExpiresAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("applicability was not preserved: %+v", applies)
	}
}

func TestLocalMemoryCurationPlanDateUsesBeijingYesterday(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	localNow := time.Date(2026, 7, 10, 1, 0, 0, 0, loc)
	planDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if got := planDate.Format("2006-01-02"); got != "2026-07-09" {
		t.Fatalf("planDate = %s, want 2026-07-09", got)
	}
}

func TestClaimLocalMemoryCurationRunOncePerBeijingDate(t *testing.T) {
	d := &Daemon{memoryCurationRuns: map[string]string{}}
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	if !d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now) {
		t.Fatal("first run was not claimed")
	}
	if d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now.Add(30*time.Minute)) {
		t.Fatal("same date was claimed twice")
	}
	d.releaseLocalMemoryCurationRun("ws-1", memorycuration.StageL3)
	if !d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now.Add(30*time.Minute)) {
		t.Fatal("released failed run was not retryable")
	}
}

func TestSharedSkillScanRootUsesProviderDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, ok := sharedSkillScanRoot(Config{}, "pi")
	if !ok {
		t.Fatal("expected pi shared root")
	}
	want := filepath.Join(home, ".pi", "share", "skills")
	if root != want {
		t.Fatalf("got %q want %q", root, want)
	}

	workspaceRoot := filepath.Join(home, "multica_workspaces")
	agentRoot := piAgentRoot(Config{WorkspacesRoot: workspaceRoot}, "workspace-1", "agent-1")
	agentWant := filepath.Join(workspaceRoot, "workspace-1", ".multica", "agents", "agent-1")
	if agentRoot != agentWant {
		t.Fatalf("got %q want %q", agentRoot, agentWant)
	}

	legacyRoot := legacyPiAgentRoot(Config{WorkspacesRoot: workspaceRoot}, "workspace-1", "agent-1")
	legacyWant := filepath.Join(workspaceRoot, "workspace-1", ".pi", "agents", "agent-1")
	if legacyRoot != legacyWant {
		t.Fatalf("got legacy %q want %q", legacyRoot, legacyWant)
	}

	skillQueue := piAgentSkillCandidatesPath(agentRoot)
	skillQueueWant := filepath.Join(agentWant, "sync_queue", "skill-candidates.jsonl")
	if skillQueue != skillQueueWant {
		t.Fatalf("got %q want %q", skillQueue, skillQueueWant)
	}

	if _, ok := sharedSkillScanRoot(Config{}, "codex"); ok {
		t.Fatal("expected codex to have no default shared root")
	}
}

func TestSharedSkillScanRootGlobalOverride(t *testing.T) {
	root, ok := sharedSkillScanRoot(Config{SharedSkillsDir: "/custom/shared"}, "pi")
	if !ok || root != "/custom/shared" {
		t.Fatalf("expected global override, got %q ok=%v", root, ok)
	}
}

func TestEnsureMulticaAgentRootIsLazy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace-1", ".multica", "agents", "agent-1")
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("new agent root contains placeholder entries: %+v", entries)
	}
}

func TestMulticaAgentRootStableAcrossHarnessSwitch(t *testing.T) {
	cfg := Config{WorkspacesRoot: filepath.Join(t.TempDir(), "multica_workspaces")}
	workspaceID := "workspace-1"
	agentID := "agent-shared-memory"

	codexRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	claudeRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	piRoot := piAgentRoot(cfg, workspaceID, agentID)
	if codexRoot != claudeRoot || codexRoot != piRoot {
		t.Fatalf("memory roots diverged across harnesses: codex=%q claude=%q pi=%q", codexRoot, claudeRoot, piRoot)
	}
	if strings.Contains(codexRoot, "runtime") || strings.Contains(codexRoot, "codex") || strings.Contains(codexRoot, "claude") {
		t.Fatalf("agent memory root must not embed provider/runtime: %q", codexRoot)
	}

	envCodex := map[string]string{}
	envPi := map[string]string{}
	addMulticaAgentEnv(envCodex, cfg, workspaceID, agentID, "")
	addMulticaAgentEnv(envPi, cfg, workspaceID, agentID, "")
	addPiAgentEnv(envPi, cfg, workspaceID, agentID)
	if envCodex["MULTICA_AGENT_ROOT"] != envPi["MULTICA_AGENT_ROOT"] || envCodex["MULTICA_AGENT_MEMORY_DIR"] != envPi["MULTICA_AGENT_MEMORY_DIR"] {
		t.Fatalf("MULTICA memory env diverged: %#v vs %#v", envCodex, envPi)
	}
	if envPi["PI_AGENT_ROOT"] != envPi["MULTICA_AGENT_ROOT"] || envPi["PI_MEMORY_DIR"] != envPi["MULTICA_AGENT_MEMORY_DIR"] {
		t.Fatalf("Pi aliases must point at Multica agent root: %#v", envPi)
	}
}

func TestLocalSkillScanFingerprintChangesWhenFileChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := localSkillScanFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("version-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := localSkillScanFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected fingerprint to change after file edit")
	}
}
