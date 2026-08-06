package repocache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestMaterializeAgentRepoSeparatesDurableAndTurnWorktrees(t *testing.T) {
	sourceRepo := createTestRepo(t)
	workspacesRoot := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	turnAID := uuid.NewString()
	turnBID := uuid.NewString()

	cache := New(filepath.Join(workspacesRoot, ".repos"), testLogger())
	if err := cache.Sync(workspaceID, []RepoInfo{{URL: sourceRepo}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	base := AgentRepoParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		TurnID:         turnAID,
		RepoURL:        sourceRepo,
		AgentName:      "Backend Agent",
	}
	first, err := cache.MaterializeAgentRepo(base)
	if err != nil {
		t.Fatalf("MaterializeAgentRepo first: %v", err)
	}
	layout, err := execenv.ResolveAgentWorkspaceLayout(workspacesRoot, workspaceID, agentID)
	if err != nil {
		t.Fatalf("ResolveAgentWorkspaceLayout: %v", err)
	}
	turnA, err := layout.Turn(turnAID)
	if err != nil {
		t.Fatalf("Turn A: %v", err)
	}
	if !pathWithinRoot(layout.ReposDir, first.DurablePath) {
		t.Fatalf("durable path %q is outside %q", first.DurablePath, layout.ReposDir)
	}
	if !pathWithinRoot(turnA.WorktreesDir, first.Path) {
		t.Fatalf("turn path %q is outside %q", first.Path, turnA.WorktreesDir)
	}
	if !strings.Contains(first.BranchName, turnAID) {
		t.Fatalf("branch %q does not contain full turn UUID %q", first.BranchName, turnAID)
	}

	durableFile := filepath.Join(first.DurablePath, "durable-note.txt")
	turnFile := filepath.Join(first.Path, "turn-change.txt")
	if err := os.WriteFile(durableFile, []byte("preserve durable\n"), 0o644); err != nil {
		t.Fatalf("write durable file: %v", err)
	}
	if err := os.WriteFile(turnFile, []byte("preserve turn\n"), 0o644); err != nil {
		t.Fatalf("write turn file: %v", err)
	}

	repeated, err := cache.MaterializeAgentRepo(base)
	if err != nil {
		t.Fatalf("MaterializeAgentRepo repeated: %v", err)
	}
	if repeated.DurablePath != first.DurablePath || repeated.Path != first.Path || repeated.BranchName != first.BranchName {
		t.Fatalf("same-turn materialization changed result: first=%+v repeated=%+v", first, repeated)
	}
	for path, want := range map[string]string{
		durableFile: "preserve durable\n",
		turnFile:    "preserve turn\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read preserved file %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("preserved file %s = %q, want %q", path, got, want)
		}
	}

	base.TurnID = turnBID
	secondTurn, err := cache.MaterializeAgentRepo(base)
	if err != nil {
		t.Fatalf("MaterializeAgentRepo second turn: %v", err)
	}
	if secondTurn.DurablePath != first.DurablePath {
		t.Fatalf("durable checkout changed across turns: %q != %q", secondTurn.DurablePath, first.DurablePath)
	}
	if secondTurn.Path == first.Path {
		t.Fatalf("two turns shared a worktree: %q", first.Path)
	}
	if _, err := os.Stat(turnFile); err != nil {
		t.Fatalf("new turn changed prior turn worktree: %v", err)
	}
}

func TestMaterializeAgentRepoReplayIgnoresAgentRenameAndPreservesChanges(t *testing.T) {
	sourceRepo := createTestRepo(t)
	workspacesRoot := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	turnID := uuid.NewString()

	cache := New(filepath.Join(workspacesRoot, ".repos"), testLogger())
	if err := cache.Sync(workspaceID, []RepoInfo{{URL: sourceRepo}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	params := AgentRepoParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		TurnID:         turnID,
		RepoURL:        sourceRepo,
		AgentName:      "Old Display Name",
	}
	first, err := cache.MaterializeAgentRepo(params)
	if err != nil {
		t.Fatalf("MaterializeAgentRepo first: %v", err)
	}
	wantBranch := "agent/" + agentID + "/" + turnID
	if first.BranchName != wantBranch {
		t.Fatalf("first branch = %q, want immutable identity %q", first.BranchName, wantBranch)
	}

	trackedPath := filepath.Join(first.Path, "README.md")
	if err := os.WriteFile(trackedPath, []byte("dirty tracked change\n"), 0o644); err != nil {
		t.Fatalf("write dirty tracked file: %v", err)
	}
	untrackedPath := filepath.Join(first.Path, "untracked.txt")
	if err := os.WriteFile(untrackedPath, []byte("preserve untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	params.AgentName = "New Display Name"
	replayed, err := cache.MaterializeAgentRepo(params)
	if err != nil {
		t.Fatalf("MaterializeAgentRepo after rename: %v", err)
	}
	if *replayed != *first {
		t.Fatalf("rename changed replay result: first=%+v replayed=%+v", first, replayed)
	}
	for path, want := range map[string]string{
		trackedPath:   "dirty tracked change\n",
		untrackedPath: "preserve untracked\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read preserved file %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("preserved file %s = %q, want %q", path, got, want)
		}
	}
}

func TestMaterializeAgentRepoConcurrentTurnsSerializeBareRepoMutation(t *testing.T) {
	sourceRepo := createTestRepo(t)
	workspacesRoot := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	cache := New(filepath.Join(workspacesRoot, ".repos"), testLogger())
	if err := cache.Sync(workspaceID, []RepoInfo{{URL: sourceRepo}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	const workers = 8
	results := make(chan *AgentRepoResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func(turnID string) {
			defer wg.Done()
			result, err := cache.MaterializeAgentRepo(AgentRepoParams{
				WorkspacesRoot: workspacesRoot,
				WorkspaceID:    workspaceID,
				AgentID:        agentID,
				TurnID:         turnID,
				RepoURL:        sourceRepo,
				AgentName:      "Backend Agent",
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(uuid.NewString())
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent materialization: %v", err)
	}
	paths := map[string]struct{}{}
	durablePath := ""
	for result := range results {
		if durablePath == "" {
			durablePath = result.DurablePath
		} else if result.DurablePath != durablePath {
			t.Errorf("durable checkout changed across concurrent turns: %q != %q", result.DurablePath, durablePath)
		}
		if _, exists := paths[result.Path]; exists {
			t.Errorf("duplicate turn worktree path: %q", result.Path)
		}
		paths[result.Path] = struct{}{}
	}
	if len(paths) != workers {
		t.Fatalf("materialized %d turn worktrees, want %d", len(paths), workers)
	}
}

func TestMaterializeAgentRepoRefusesUnmanagedCollisionWithoutDeletingIt(t *testing.T) {
	sourceRepo := createTestRepo(t)
	workspacesRoot := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	turnID := uuid.NewString()
	cache := New(filepath.Join(workspacesRoot, ".repos"), testLogger())
	if err := cache.Sync(workspaceID, []RepoInfo{{URL: sourceRepo}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	layout, err := execenv.ProvisionAgentWorkspace(workspacesRoot, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}
	turn, err := execenv.ProvisionAgentTurn(*layout, turnID)
	if err != nil {
		t.Fatalf("ProvisionAgentTurn: %v", err)
	}
	repoDir := strings.TrimSuffix(bareDirName(sourceRepo), ".git")
	collision := filepath.Join(turn.WorktreesDir, repoDir)
	if err := os.MkdirAll(collision, 0o755); err != nil {
		t.Fatalf("MkdirAll collision: %v", err)
	}
	sentinel := filepath.Join(collision, "user-file.txt")
	if err := os.WriteFile(sentinel, []byte("do not delete"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, err = cache.MaterializeAgentRepo(AgentRepoParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		TurnID:         turnID,
		RepoURL:        sourceRepo,
		AgentName:      "Backend Agent",
	})
	if err == nil {
		t.Fatal("MaterializeAgentRepo accepted unmanaged path collision")
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "do not delete" {
		t.Fatalf("unmanaged collision was changed: content=%q err=%v", got, readErr)
	}
}

func TestMaterializeAgentRepoRejectsSymlinkedDurableNamespace(t *testing.T) {
	sourceRepo := createTestRepo(t)
	workspacesRoot := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	cache := New(filepath.Join(workspacesRoot, ".repos"), testLogger())
	if err := cache.Sync(workspaceID, []RepoInfo{{URL: sourceRepo}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	layout, err := execenv.ProvisionAgentWorkspace(workspacesRoot, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}
	outside := t.TempDir()
	if err := os.Remove(layout.ReposDir); err != nil {
		t.Fatalf("remove empty repos dir: %v", err)
	}
	if err := os.Symlink(outside, layout.ReposDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err = cache.MaterializeAgentRepo(AgentRepoParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		TurnID:         uuid.NewString(),
		RepoURL:        sourceRepo,
		AgentName:      "Backend Agent",
	})
	if err == nil {
		t.Fatal("MaterializeAgentRepo accepted symlinked durable repo namespace")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read outside dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target was changed: %v", entries)
	}
}

func TestAgentRepoMaterializationSourceHasNoResetCleanOrRemoveAll(t *testing.T) {
	raw, err := os.ReadFile("agent_workspace.go")
	if err != nil {
		t.Fatalf("read agent_workspace.go: %v", err)
	}
	source := string(raw)
	for _, forbidden := range []string{
		`"reset"`,
		`"--hard"`,
		`"clean"`,
		`"-fd"`,
		"os.RemoveAll",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("agent repo materialization source contains forbidden destructive primitive %q", forbidden)
		}
	}
}
