package execenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

func TestAgentWorkspaceLayoutUsesCanonicalFullIDs(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()

	layout, err := ResolveAgentWorkspaceLayout(root, workspaceID, agentID)
	if err != nil {
		t.Fatalf("ResolveAgentWorkspaceLayout: %v", err)
	}
	wantRoot := agentworkspace.Root(root, workspaceID, agentID)
	if layout.AgentRoot != wantRoot {
		t.Fatalf("RootDir = %q, want %q", layout.AgentRoot, wantRoot)
	}
	if layout.AgentRoot != wantRoot {
		t.Fatalf("WorkDir = %q", layout.AgentRoot)
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
	entries, err := os.ReadDir(layout.AgentRoot)
	if err != nil {
		t.Fatalf("read fresh AgentRoot: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("fresh AgentRoot should be empty: %+v", entries)
	}
	memoryDir := filepath.Join(layout.AgentRoot, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll memory: %v", err)
	}
	memoryPath := filepath.Join(memoryDir, "MEMORY.md")
	ordinaryPath := filepath.Join(layout.AgentRoot, "ordinary.txt")
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
	if reprovisioned.AgentRoot != layout.AgentRoot {
		t.Fatalf("root changed across provision: %q != %q", reprovisioned.AgentRoot, layout.AgentRoot)
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
			roots <- layout.AgentRoot
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
	entries, err := os.ReadDir(want)
	if err != nil {
		t.Fatalf("read concurrently provisioned root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("concurrent provision should not pre-populate AgentRoot: %+v", entries)
	}
}

func TestRemoveAgentWorkspaceRequiresExactRoot(t *testing.T) {
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
		AgentRoot:      layout.AgentRoot,
		Reason:         AgentWorkspaceRemovalFullReset,
	}
	base.AgentRoot = filepath.Dir(layout.AgentRoot)
	if err := RemoveAgentWorkspace(base); err == nil {
		t.Fatal("RemoveAgentWorkspace accepted non-canonical root")
	}
	if _, err := os.Stat(layout.AgentRoot); err != nil {
		t.Fatalf("workspace changed after wrong-root rejection: %v", err)
	}

	base.AgentRoot = layout.AgentRoot
	if err := RemoveAgentWorkspace(base); err != nil {
		t.Fatalf("RemoveAgentWorkspace: %v", err)
	}
	if _, err := os.Stat(layout.AgentRoot); !os.IsNotExist(err) {
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
	if err := os.RemoveAll(layout.AgentRoot); err != nil {
		t.Fatalf("remove test agent root: %v", err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := os.Symlink(outside, layout.AgentRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err = RemoveAgentWorkspace(RemoveAgentWorkspaceParams{
		WorkspacesRoot: root,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		AgentRoot:      layout.AgentRoot,
		Reason:         AgentWorkspaceRemovalFullReset,
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
		case "ProvisionAgentWorkspace":
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
