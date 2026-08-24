package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHandleListFilesRequestUsesRaftAgentWorkspaceFileTree(t *testing.T) {
	workspacesRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspacesRoot, "agent-root"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := New(Config{WorkspacesRoot: workspacesRoot}, testDiscardLogger())
	writes := make(chan []byte, 1)

	d.handleListFilesRequest(protocol.ListWorkdirFilesRequestPayload{
		RequestID: "request-1",
		RelPath:   "agent-root",
	}, writes)

	var message protocol.Message
	if err := json.Unmarshal(<-writes, &message); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if message.Type != protocol.EventAgentWorkspaceFileTree {
		t.Fatalf("response type = %q, want %q", message.Type, protocol.EventAgentWorkspaceFileTree)
	}
}

func TestWalkWorkdirFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, dir bool) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if dir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir parent %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mk("src/main.ts", false)
	mk("src/util/helper.ts", false)
	mk("README.md", false)
	mk("node_modules/dep/index.js", false) // ignored subtree
	mk(".git/config", false)               // ignored subtree

	nodes, truncated, err := walkWorkdirFiles(root, 0, 0)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation")
	}

	got := map[string]bool{} // path -> isDir
	for _, n := range nodes {
		got[n.Path] = n.IsDir
	}

	wantFiles := []string{"README.md", "src/main.ts", "src/util/helper.ts"}
	for _, f := range wantFiles {
		if isDir, ok := got[f]; !ok || isDir {
			t.Errorf("expected file %q in listing, got %v (present=%v)", f, isDir, ok)
		}
	}
	wantDirs := []string{"src", "src/util"}
	for _, d := range wantDirs {
		if isDir, ok := got[d]; !ok || !isDir {
			t.Errorf("expected dir %q in listing, got isDir=%v (present=%v)", d, isDir, ok)
		}
	}
	for ignored := range map[string]struct{}{"node_modules": {}, ".git": {}} {
		if _, ok := got[ignored]; ok {
			t.Errorf("ignored dir %q should not appear", ignored)
		}
	}
	for _, n := range nodes {
		if n.Path == "node_modules/dep/index.js" || n.Path == ".git/config" {
			t.Errorf("ignored subtree leaked: %q", n.Path)
		}
	}
}

func TestWalkWorkdirFiles_EntryCapTruncates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	nodes, truncated, err := walkWorkdirFiles(root, 5, 0)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated when entries exceed cap")
	}
	if len(nodes) > 5 {
		t.Fatalf("expected at most 5 nodes, got %d", len(nodes))
	}
}

func TestWalkWorkdirFiles_HidesDotfilesWhenRequested(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"memory/MEMORY.md",
		".env",
		".hidden/note.md",
		"notes/.private.md",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	hidden, _, err := walkWorkdirFilesWithOptions(root, 0, 0, workdirWalkOptions{HideDotfiles: true})
	if err != nil {
		t.Fatalf("walk hidden: %v", err)
	}
	for _, n := range hidden {
		if n.Path == ".env" || n.Path == ".hidden" || n.Path == ".hidden/note.md" || n.Path == "notes/.private.md" {
			t.Fatalf("hidden path leaked with HideDotfiles: %q", n.Path)
		}
	}

	all, _, err := walkWorkdirFilesWithOptions(root, 0, 0, workdirWalkOptions{HideDotfiles: false})
	if err != nil {
		t.Fatalf("walk all: %v", err)
	}
	got := map[string]bool{}
	for _, n := range all {
		got[n.Path] = true
	}
	for _, path := range []string{".env", ".hidden", ".hidden/note.md", "notes/.private.md"} {
		if !got[path] {
			t.Fatalf("expected hidden path %q when HideDotfiles=false; got %#v", path, got)
		}
	}
}

func TestSeedAgentContextFilesCreatesRootAndAppendsWhitelistedMarkdown(t *testing.T) {
	root := t.TempDir()
	written, err := seedAgentContextFiles(root, map[string]string{
		"notes/agents.md":      "Reviewer: checks output.",
		"notes/not-allowed.md": "skip me",
		"../notes/channels.md": "skip traversal",
	}, map[string]string{
		"memory/MEMORY.md": "Long-lived preference.",
		"memory/USER.md":   "skip user profile",
	}, 256*1024)
	if err != nil {
		t.Fatalf("seed context: %v", err)
	}
	gotWritten := map[string]bool{}
	for _, path := range written {
		gotWritten[path] = true
	}
	for _, path := range []string{"notes/agents.md", "memory/MEMORY.md"} {
		if !gotWritten[path] {
			t.Fatalf("expected written path %q in %#v", path, written)
		}
	}
	agents, err := os.ReadFile(filepath.Join(root, "notes", "agents.md"))
	if err != nil {
		t.Fatal(err)
	}
	if content := string(agents); !strings.Contains(content, "# Agents") || !strings.Contains(content, "Reviewer: checks output.") {
		t.Fatalf("notes/agents.md missing header or seed: %q", content)
	}
	memory, err := os.ReadFile(filepath.Join(root, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(memory), "Long-lived preference.") {
		t.Fatalf("MEMORY.md missing seed: %q", string(memory))
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "not-allowed.md")); !os.IsNotExist(err) {
		t.Fatalf("disallowed notes file should not be created, err=%v", err)
	}
}

func TestWriteWorkdirTextFileRejectsHashMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "memory", "STATE.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := writeWorkdirTextFile(root, "memory/STATE.md", "new", "not-the-current-hash", 0, false)
	if !resp.Conflict {
		t.Fatalf("expected hash conflict, got %+v", resp)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("hash conflict must not modify file, got %q", got)
	}
}

func TestWriteWorkdirTextFileCreateMakesMissingFile(t *testing.T) {
	root := t.TempDir()
	resp := writeWorkdirTextFile(root, "output/answer.json", `{"ok":true}`, "", 0, true)
	if resp.Error != "" || resp.Missing || resp.Conflict {
		t.Fatalf("create write failed: %+v", resp)
	}
	got, err := os.ReadFile(filepath.Join(root, "output", "answer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("created content = %q", got)
	}
}

func TestWriteWorkdirTextFileEditOnlyLeavesMissing(t *testing.T) {
	root := t.TempDir()
	resp := writeWorkdirTextFile(root, "output/answer.json", "{}", "", 0, false)
	if !resp.Missing {
		t.Fatalf("edit-only write must report Missing, got %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(root, "output", "answer.json")); !os.IsNotExist(err) {
		t.Fatalf("edit-only write must not create the file, err=%v", err)
	}
}
