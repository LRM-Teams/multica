package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func writeAgentRel(t *testing.T, root, rel string, dir bool) {
	t.Helper()
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
	if err := os.WriteFile(p, []byte("hello "+rel), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func agentFilesFixture(t *testing.T) (workspacesRoot, relPath string) {
	t.Helper()
	workspacesRoot = t.TempDir()
	relPath = "ws/agents/agent-1"
	root := filepath.Join(workspacesRoot, filepath.FromSlash(relPath))
	writeAgentRel(t, root, "AGENTS.md", false)
	writeAgentRel(t, root, ".gitignore", false)
	writeAgentRel(t, root, ".env", false)
	writeAgentRel(t, root, "api-token.json", false)
	writeAgentRel(t, root, "my-secret.md", false)
	writeAgentRel(t, root, "db-credentials.yaml", false)
	writeAgentRel(t, root, "memory/MEMORY.md", false)
	writeAgentRel(t, root, ".ssh/id_rsa", false)
	writeAgentRel(t, root, ".multica-runtime/state.json", false)
	writeAgentRel(t, root, ".multica-bound-mirror/note", false)
	writeAgentRel(t, root, "node_modules/pkg/index.js", false)
	writeAgentRel(t, root, "codex-home/keep.md", false)
	for i := 0; i < 80; i++ {
		writeAgentRel(t, root, filepath.ToSlash(filepath.Join("codex-home", "deep", "n", "file-"+string(rune('a'+i%26))+".txt")), false)
	}
	return workspacesRoot, relPath
}

func decodeListFiles(t *testing.T, frame []byte) protocol.ListWorkdirFilesResponsePayload {
	t.Helper()
	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var resp protocol.ListWorkdirFilesResponsePayload
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return resp
}

func decodeReadFile(t *testing.T, frame []byte) protocol.ReadWorkdirFileResponsePayload {
	t.Helper()
	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var resp protocol.ReadWorkdirFileResponsePayload
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return resp
}

func listAgentDir(t *testing.T, d *WorkspaceDaemonCore, relPath, dirPath string, includeHidden bool) protocol.ListWorkdirFilesResponsePayload {
	t.Helper()
	writes := make(chan []byte, 1)
	d.handleListFilesRequest(protocol.ListWorkdirFilesRequestPayload{
		RequestID:    "list-1",
		RelPath:      relPath,
		DirPath:      dirPath,
		OneLevel:     true,
		HideDotfiles: !includeHidden,
	}, writes)
	select {
	case frame := <-writes:
		return decodeListFiles(t, frame)
	case <-time.After(2 * time.Second):
		t.Fatal("expected list response")
	}
	return protocol.ListWorkdirFilesResponsePayload{}
}

func readAgentFile(t *testing.T, d *WorkspaceDaemonCore, relPath, filePath string) protocol.ReadWorkdirFileResponsePayload {
	t.Helper()
	writes := make(chan []byte, 1)
	d.handleReadFileRequest(protocol.ReadWorkdirFileRequestPayload{
		RequestID: "read-1",
		RelPath:   relPath,
		FilePath:  filePath,
	}, writes)
	select {
	case frame := <-writes:
		return decodeReadFile(t, frame)
	case <-time.After(2 * time.Second):
		t.Fatal("expected read response")
	}
	return protocol.ReadWorkdirFileResponsePayload{}
}

func nodePaths(nodes []protocol.WorkdirFileNode) map[string]protocol.WorkdirFileNode {
	out := make(map[string]protocol.WorkdirFileNode, len(nodes))
	for _, n := range nodes {
		out[n.Path] = n
	}
	return out
}

func TestHandleListFilesRequest_OneLevelRaftVisibility(t *testing.T) {
	workspacesRoot, relPath := agentFilesFixture(t)
	d := &WorkspaceDaemonCore{
		cfg:    Config{WorkspacesRoot: workspacesRoot},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rootHiddenOff := listAgentDir(t, d, relPath, "", false)
	if rootHiddenOff.Error != "" || rootHiddenOff.Missing {
		t.Fatalf("root list: %+v", rootHiddenOff)
	}
	if rootHiddenOff.Truncated {
		t.Fatal("one-level list must not set truncated")
	}
	if !strings.HasSuffix(filepath.ToSlash(rootHiddenOff.RootPath), "/"+relPath) {
		t.Fatalf("RootPath = %q, want suffix /%s", rootHiddenOff.RootPath, relPath)
	}
	got := nodePaths(rootHiddenOff.Nodes)
	if _, ok := got["codex-home"]; !ok || !got["codex-home"].IsDir {
		t.Fatalf("root should include collapsed codex-home dir, got %#v", got)
	}
	if _, ok := got["memory"]; !ok || !got["memory"].IsDir {
		t.Fatalf("root should include memory dir, got %#v", got)
	}
	if _, ok := got["AGENTS.md"]; !ok || got["AGENTS.md"].IsDir {
		t.Fatalf("root should include AGENTS.md, got %#v", got)
	}
	if _, ok := got["api-token.json"]; !ok {
		t.Fatalf("secret names remain listable, got %#v", got)
	}
	forbidden := []string{
		".gitignore", ".env", ".ssh", ".multica-runtime", ".multica-bound-mirror",
		"node_modules", "codex-home/keep.md", "memory/MEMORY.md",
	}
	for _, path := range forbidden {
		if _, ok := got[path]; ok {
			t.Fatalf("root list leaked %q: %#v", path, got)
		}
	}

	child := listAgentDir(t, d, relPath, "codex-home", false)
	childGot := nodePaths(child.Nodes)
	if _, ok := childGot["codex-home/keep.md"]; !ok {
		t.Fatalf("listing codex-home should be a separate call with its children, got %#v", childGot)
	}
	if _, ok := childGot["codex-home/deep"]; !ok || !childGot["codex-home/deep"].IsDir {
		t.Fatalf("codex-home should list deep as a dir, not its descendants, got %#v", childGot)
	}
	for path := range childGot {
		if path != "codex-home" && path != "codex-home/keep.md" && path != "codex-home/deep" &&
			len(path) > len("codex-home/") && path[:len("codex-home/")] == "codex-home/" {
			if rest := path[len("codex-home/"):]; containsSlash(rest) {
				t.Fatalf("child list included a descendant %q", path)
			}
		}
	}

	rootHiddenOn := listAgentDir(t, d, relPath, "", true)
	on := nodePaths(rootHiddenOn.Nodes)
	if _, ok := on[".gitignore"]; !ok {
		t.Fatalf(".gitignore should appear when hidden is on, got %#v", on)
	}
	if _, ok := on[".env"]; !ok {
		t.Fatalf(".env is listable when hidden is on (preview still denied), got %#v", on)
	}
	stillHidden := []string{".ssh", ".multica-runtime", ".multica-bound-mirror", "node_modules"}
	for _, path := range stillHidden {
		if _, ok := on[path]; ok {
			t.Fatalf("never-visible %q leaked with hidden on: %#v", path, on)
		}
	}

	if nodes := listAgentDir(t, d, relPath, ".ssh", true).Nodes; len(nodes) != 0 {
		t.Fatalf("entering .ssh should be empty, got %#v", nodes)
	}
	if nodes := listAgentDir(t, d, relPath, ".multica-runtime", true).Nodes; len(nodes) != 0 {
		t.Fatalf("entering .multica-runtime should be empty, got %#v", nodes)
	}
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

func TestHandleReadFileRequest_RefusesNeverVisibleAndSecrets(t *testing.T) {
	workspacesRoot, relPath := agentFilesFixture(t)
	d := &WorkspaceDaemonCore{
		cfg:    Config{WorkspacesRoot: workspacesRoot},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	denied := []string{".env", "api-token.json", "my-secret.md", "db-credentials.yaml", ".ssh/id_rsa"}
	for _, path := range denied {
		resp := readAgentFile(t, d, relPath, path)
		if resp.Error == "" {
			t.Fatalf("read %q should be refused, got %+v", path, resp)
		}
		if resp.Content != "" {
			t.Fatalf("read %q leaked content: %q", path, resp.Content)
		}
	}

	ok := readAgentFile(t, d, relPath, "AGENTS.md")
	if ok.Error != "" || ok.Content == "" {
		t.Fatalf("ordinary file should be readable, got %+v", ok)
	}
	hiddenOK := readAgentFile(t, d, relPath, ".gitignore")
	if hiddenOK.Error != "" || hiddenOK.Content == "" {
		t.Fatalf("hidden-but-allowed file should be readable, got %+v", hiddenOK)
	}
}
