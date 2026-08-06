package execenv

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestAgentWorkspaceLayoutUsesCanonicalFullIDs(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	turnID := uuid.NewString()

	layout, err := ResolveAgentWorkspaceLayout(root, workspaceID, agentID)
	if err != nil {
		t.Fatalf("ResolveAgentWorkspaceLayout: %v", err)
	}
	wantRoot := filepath.Join(root, workspaceID, ".multica", "agents", agentID)
	if layout.RootDir != wantRoot {
		t.Fatalf("RootDir = %q, want %q", layout.RootDir, wantRoot)
	}
	if layout.WorkDir != filepath.Join(wantRoot, "workspace") {
		t.Fatalf("WorkDir = %q", layout.WorkDir)
	}
	if layout.ReposDir != filepath.Join(wantRoot, "repos") {
		t.Fatalf("ReposDir = %q", layout.ReposDir)
	}

	turn, err := layout.Turn(turnID)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	wantTurnRoot := filepath.Join(wantRoot, "turns", turnID)
	if turn.RootDir != wantTurnRoot {
		t.Fatalf("turn RootDir = %q, want %q", turn.RootDir, wantTurnRoot)
	}
	if turn.WorktreesDir != filepath.Join(wantTurnRoot, "worktree") {
		t.Fatalf("WorktreesDir = %q", turn.WorktreesDir)
	}
	if turn.ArtifactsDir != filepath.Join(wantTurnRoot, "artifacts") {
		t.Fatalf("ArtifactsDir = %q", turn.ArtifactsDir)
	}

	for _, invalid := range []string{
		"",
		uuid.Nil.String(),
		"agent-1",
		agentID[:8],
		"../" + agentID,
		"{" + agentID + "}",
	} {
		if got := PredictAgentRootDir(root, workspaceID, invalid); got != "" {
			t.Errorf("PredictAgentRootDir accepted invalid agent ID %q: %q", invalid, got)
		}
		if _, err := layout.Turn(invalid); err == nil {
			t.Errorf("Turn accepted invalid turn ID %q", invalid)
		}
	}
}

func TestProvisionAgentWorkspaceIsIdempotentAndPreservesFiles(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()

	layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("first ProvisionAgentWorkspace: %v", err)
	}
	memoryDir := filepath.Join(layout.RootDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll memory: %v", err)
	}
	memoryPath := filepath.Join(memoryDir, "MEMORY.md")
	ordinaryPath := filepath.Join(layout.WorkDir, "ordinary.txt")
	if err := os.WriteFile(memoryPath, []byte("durable memory\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(ordinaryPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write ordinary file: %v", err)
	}

	reprovisioned, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("second ProvisionAgentWorkspace: %v", err)
	}
	if reprovisioned.RootDir != layout.RootDir {
		t.Fatalf("root changed across provision: %q != %q", reprovisioned.RootDir, layout.RootDir)
	}
	for path, want := range map[string]string{
		memoryPath:   "durable memory\n",
		ordinaryPath: "keep me\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read preserved file %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("preserved file %s = %q, want %q", path, got, want)
		}
	}

	raw, err := os.ReadFile(filepath.Join(layout.RootDir, agentWorkspaceMetaFile))
	if err != nil {
		t.Fatalf("read workspace metadata: %v", err)
	}
	var meta agentWorkspaceMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal workspace metadata: %v", err)
	}
	if meta.SchemaVersion != agentWorkspaceSchemaVersion ||
		meta.WorkspaceID != workspaceID ||
		meta.AgentID != agentID {
		t.Fatalf("unexpected workspace metadata: %+v", meta)
	}
}

func TestProvisionAgentWorkspaceConcurrentSameRoot(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()

	const workers = 32
	roots := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
			if err != nil {
				errs <- err
				return
			}
			roots <- layout.RootDir
		}()
	}
	wg.Wait()
	close(roots)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent provision: %v", err)
	}
	want := PredictAgentRootDir(root, workspaceID, agentID)
	for got := range roots {
		if got != want {
			t.Errorf("concurrent root = %q, want %q", got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(want, agentWorkspaceMetaFile)); err != nil {
		t.Fatalf("metadata missing after concurrent provision: %v", err)
	}
}

func TestAgentTurnCleanupRemovesOnlyExactTurn(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	turnAID := uuid.NewString()
	turnBID := uuid.NewString()

	layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}
	turnA, err := ProvisionAgentTurn(*layout, turnAID)
	if err != nil {
		t.Fatalf("ProvisionAgentTurn A: %v", err)
	}
	turnB, err := ProvisionAgentTurn(*layout, turnBID)
	if err != nil {
		t.Fatalf("ProvisionAgentTurn B: %v", err)
	}
	if err := os.WriteFile(filepath.Join(turnA.ArtifactsDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write turn A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(turnB.ArtifactsDir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write turn B: %v", err)
	}
	workspaceFile := filepath.Join(layout.WorkDir, "persistent.txt")
	if err := os.WriteFile(workspaceFile, []byte("persistent"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	if err := CleanupAgentTurn(*layout, turnAID); err != nil {
		t.Fatalf("CleanupAgentTurn: %v", err)
	}
	if _, err := os.Stat(turnA.RootDir); !os.IsNotExist(err) {
		t.Fatalf("turn A still exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(turnB.ArtifactsDir, "b.txt")); err != nil {
		t.Fatalf("turn B was affected: %v", err)
	}
	if got, err := os.ReadFile(workspaceFile); err != nil || string(got) != "persistent" {
		t.Fatalf("durable workspace was affected: content=%q err=%v", got, err)
	}
}

func TestAgentTurnProvisionRejectsSymlinkedManagedNamespace(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}
	outside := t.TempDir()
	if err := os.Remove(layout.TurnsDir); err != nil {
		t.Fatalf("remove empty turns dir: %v", err)
	}
	if err := os.Symlink(outside, layout.TurnsDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := ProvisionAgentTurn(*layout, uuid.NewString()); err == nil {
		t.Fatal("ProvisionAgentTurn accepted symlinked turns namespace")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read outside dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target was changed: %v", entries)
	}
}

func TestRemoveAgentWorkspaceRequiresExactRootAndQuiescence(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}

	base := RemoveAgentWorkspaceParams{
		WorkspacesRoot: root,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		RootDir:        layout.RootDir,
		Reason:         AgentWorkspaceRemovalFullReset,
	}
	if err := RemoveAgentWorkspace(base); err == nil {
		t.Fatal("RemoveAgentWorkspace succeeded without quiescence proof")
	}
	if _, err := os.Stat(layout.RootDir); err != nil {
		t.Fatalf("workspace changed after rejected removal: %v", err)
	}

	base.Proof = AgentWorkspaceRemovalProof{NoActiveTurn: true, NoActiveProviderLease: true}
	base.RootDir = filepath.Dir(layout.RootDir)
	if err := RemoveAgentWorkspace(base); err == nil {
		t.Fatal("RemoveAgentWorkspace accepted non-canonical root")
	}
	if _, err := os.Stat(layout.RootDir); err != nil {
		t.Fatalf("workspace changed after wrong-root rejection: %v", err)
	}

	base.RootDir = layout.RootDir
	if err := RemoveAgentWorkspace(base); err != nil {
		t.Fatalf("RemoveAgentWorkspace: %v", err)
	}
	if _, err := os.Stat(layout.RootDir); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists or unexpected stat error: %v", err)
	}
}

func TestRemoveAgentWorkspaceRejectsSymlinkedCanonicalRoot(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	layout, err := ProvisionAgentWorkspace(root, workspaceID, agentID, testLogger())
	if err != nil {
		t.Fatalf("ProvisionAgentWorkspace: %v", err)
	}
	if err := os.RemoveAll(layout.RootDir); err != nil {
		t.Fatalf("remove test agent root: %v", err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := os.Symlink(outside, layout.RootDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err = RemoveAgentWorkspace(RemoveAgentWorkspaceParams{
		WorkspacesRoot: root,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		RootDir:        layout.RootDir,
		Reason:         AgentWorkspaceRemovalFullReset,
		Proof: AgentWorkspaceRemovalProof{
			NoActiveTurn:          true,
			NoActiveProviderLease: true,
		},
	})
	if err == nil {
		t.Fatal("RemoveAgentWorkspace accepted symlinked canonical root")
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve" {
		t.Fatalf("symlink target was changed: content=%q err=%v", got, readErr)
	}
}

func TestProvisionFunctionsCannotReachDestructiveFilesystemCalls(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "agent_workspace.go", nil, 0)
	if err != nil {
		t.Fatalf("parse agent_workspace.go: %v", err)
	}
	forbidden := map[string]struct{}{
		"Remove":    {},
		"RemoveAll": {},
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		switch fn.Name.Name {
		case "ProvisionAgentWorkspace", "ProvisionAgentTurn":
		default:
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, blocked := forbidden[selector.Sel.Name]; blocked {
				t.Errorf("%s must not call destructive filesystem helper %s", fn.Name.Name, selector.Sel.Name)
			}
			return true
		})
	}
}
