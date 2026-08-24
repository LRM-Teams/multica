package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPrepareResidentMessageBatchScopesIdentityAndUserMemoryPerMessage(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	agentRoot := agentworkspace.Root(root, "workspace-1", "agent-1")
	if err := os.MkdirAll(filepath.Join(agentRoot, "users", "member-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "users", "member-1", "USER.md"), []byte("# User Preferences\n\n- Call me JHP.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dm := protocol.AgentMessageProjection{
		ID: "message-dm", Target: "dm:member-1", Seq: 1, Content: "hello",
		ChannelID: "channel-dm", ChannelKind: "dm", InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
	}
	group := protocol.AgentMessageProjection{
		ID: "message-group", Target: "channel:group-1", Seq: 2, Content: "hello everyone",
		ChannelID: "channel-group", ChannelKind: "group", InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
		Memories: []protocol.AgentMessageMemoryProjection{
			{Name: "server user memory", Content: "server private preference", Scope: "user", SubjectType: "member", SubjectID: "member-1"},
			{Name: "server agent memory", Content: "server global convention", Scope: "agent", SubjectType: "agent", SubjectID: "agent-1"},
		},
	}
	prepared, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{dm, group})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 2 {
		t.Fatalf("prepared messages = %d, want 2", len(prepared))
	}
	if !strings.Contains(prepared[0].RuntimeContext, "Stable member ID for preference attribution: `member-1`") || !strings.Contains(prepared[0].RuntimeContext, "Call me JHP") {
		t.Fatalf("DM runtime context lost initiator or personal memory:\n%s", prepared[0].RuntimeContext)
	}
	if !strings.Contains(prepared[1].RuntimeContext, "Stable member ID for preference attribution: `member-1`") {
		t.Fatalf("group runtime context lost attested initiator:\n%s", prepared[1].RuntimeContext)
	}
	if strings.Contains(prepared[1].RuntimeContext, "Call me JHP") || strings.Contains(prepared[1].RuntimeContext, "server private preference") {
		t.Fatalf("group runtime context leaked personal memory:\n%s", prepared[1].RuntimeContext)
	}
	// Agent-scope memory is loaded once into the session-stable system prompt /
	// AGENTS brief at resident create, not the per-message context.
	if strings.Contains(prepared[1].RuntimeContext, "server global convention") {
		t.Fatalf("group runtime context must not repeat agent-scope memory:\n%s", prepared[1].RuntimeContext)
	}
}

func TestPrepareResidentMessageBatchSkipsRepeatUserProjectChannelMemory(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	agentRoot := agentworkspace.Root(root, "workspace-1", "agent-1")
	userPath := filepath.Join(agentRoot, "users", "member-1", "USER.md")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("# User Preferences\n\n- Call me JHP.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := protocol.AgentMessageProjection{
		ID: "message-1", Target: "dm:member-1", Seq: 1, Content: "hi",
		ChannelID: "channel-dm", ChannelKind: "dm", ProjectID: "project-a",
		InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
	}
	projectPath := filepath.Join(agentRoot, "projects", "project-a", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("Project uses Go.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{msg})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first[0].RuntimeContext, "Call me JHP") || !strings.Contains(first[0].RuntimeContext, "Project uses Go") {
		t.Fatalf("first wake missing turn-scope memory:\n%s", first[0].RuntimeContext)
	}

	msg.ID = "message-2"
	msg.Seq = 2
	msg.Content = "hi again"
	second, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{msg})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second[0].RuntimeContext, "Call me JHP") || strings.Contains(second[0].RuntimeContext, "Project uses Go") {
		t.Fatalf("second wake re-injected turn-scope memory:\n%s", second[0].RuntimeContext)
	}

	msg.ID = "message-3"
	msg.InitiatorID = "member-2"
	msg.Content = "hello from other user"
	otherUser := filepath.Join(agentRoot, "users", "member-2", "USER.md")
	if err := os.MkdirAll(filepath.Dir(otherUser), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherUser, []byte("Call me Sam.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{msg})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(third[0].RuntimeContext, "Call me Sam") {
		t.Fatalf("new user should inject once:\n%s", third[0].RuntimeContext)
	}
	if strings.Contains(third[0].RuntimeContext, "Call me JHP") {
		t.Fatalf("other user's memory leaked:\n%s", third[0].RuntimeContext)
	}
}

func TestAppendAgentScopeSystemPromptOnlyOnFreshSession(t *testing.T) {
	memories := []execenv.MemoryContextForEnv{{
		Name: "Agent global memory", Content: "Prefer terse replies.", Scope: "agent",
	}}
	fresh := agent.ExecOptions{}
	appendAgentScopeSystemPrompt(&fresh, memories)
	if !strings.Contains(fresh.SystemPrompt, "Prefer terse replies") {
		t.Fatalf("fresh session missing agent memory:\n%s", fresh.SystemPrompt)
	}

	resume := agent.ExecOptions{ResumeSessionID: "sess-1", SystemPrompt: "base"}
	appendAgentScopeSystemPrompt(&resume, memories)
	if resume.SystemPrompt != "base" || strings.Contains(resume.SystemPrompt, "Prefer terse") {
		t.Fatalf("resume must not append agent memory: %q", resume.SystemPrompt)
	}

	resume.ResumeSessionID = ""
	appendAgentScopeSystemPrompt(&resume, memories)
	if !strings.Contains(resume.SystemPrompt, "Prefer terse replies") {
		t.Fatalf("fresh-retry after resume clear missing agent memory:\n%s", resume.SystemPrompt)
	}
}

func TestResidentMessageSuccessReportsAndSyncsMemoryWrites(t *testing.T) {
	reported := make(chan struct{}, 1)
	var memorySynced atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
			t.Errorf("Authorization for %s = %q", r.URL.Path, got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/daemon/agent-memory-center/hydrate":
			if memorySynced.Load() {
				_, _ = w.Write([]byte(`{"active":[],"conflicts":[],"deleted":[],"cursor":1}`))
			} else {
				_, _ = w.Write([]byte(`{"active":[],"conflicts":[],"deleted":[],"cursor":0}`))
			}
		case "/api/daemon/agent-memory-center/sync":
			memorySynced.Store(true)
			_, _ = w.Write([]byte(`{"accepted":1}`))
		case "/api/daemon/agent-memory-writes":
			var report AgentMemoryWriteReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Errorf("decode memory write report: %v", err)
			}
			if report.AgentID != "agent-1" || report.RuntimeID != "runtime-1" || len(report.Writes) != 1 || report.Writes[0].RelPath != "memory/MEMORY.md" {
				t.Errorf("memory write report = %+v", report)
			}
			select {
			case reported <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, ServerBaseURL: srv.URL}, nil)
	d.client.SetRuntimeDaemonToken("runtime-1", "runtime-token", time.Now().Add(time.Hour))
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 1)}
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: backend}

	if err := d.deliverIdleMessageBatch(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:member-1", Seq: 1, Content: "remember this",
		ChannelID: "channel-1", ChannelKind: "dm", InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
	}}); err != nil {
		t.Fatal(err)
	}
	done := <-backend.accepted
	agentRoot := agentworkspace.Root(root, "workspace-1", "agent-1")
	if err := os.MkdirAll(filepath.Join(agentRoot, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", "MEMORY.md"), []byte("# Memory\n\n- durable fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done <- nil

	select {
	case <-reported:
	case <-time.After(3 * time.Second):
		t.Fatal("resident Message completion did not report memory writes")
	}
	snapshotPath := filepath.Join(agentRoot, ".multica", "memory-write-hashes.json")
	syncStatePath := filepath.Join(agentRoot, ".multica", "memory-sync-state.json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, snapshotErr := os.ReadFile(snapshotPath)
		syncState, syncStateErr := os.ReadFile(syncStatePath)
		if snapshotErr == nil && syncStateErr == nil &&
			strings.Contains(string(snapshot), "memory/MEMORY.md") &&
			strings.Contains(string(syncState), `"cursor":1`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resident memory state was not persisted: snapshot=%v sync=%v", snapshotErr, syncStateErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResidentMessageMemoryTaskKeepsInitiatorOnlyForSingleSubject(t *testing.T) {
	task := residentMessageMemoryTask("workspace-1", "agent-1", "runtime-1", []protocol.AgentMessageProjection{
		{ID: "m1", Target: "channel:c1", Content: "one", ChannelID: "c1", ChannelKind: "group", InitiatorType: "member", InitiatorID: "u1", InitiatorName: "One"},
		{ID: "m2", Target: "channel:c1", Content: "two", ChannelID: "c1", ChannelKind: "group", InitiatorType: "member", InitiatorID: "u2", InitiatorName: "Two"},
	})
	if task.InitiatorID != "" || task.InitiatorName != "" {
		t.Fatalf("mixed-subject batch attributed to one initiator: %+v", task)
	}
	if task.ChatSessionID != "channel:c1" || task.ChatMessage != "one\ntwo" {
		t.Fatalf("resident memory task = %+v", task)
	}
}

// P0 §4.2: identical recall queries within one resident message batch
// coalesce into a single server recall; whitespace/case variants share the
// normalized key, distinct queries do not.
func TestPrepareResidentMessageBatchCoalescesIdenticalGraphRecalls(t *testing.T) {
	var recalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/graph-memory/recalls" {
			http.NotFound(w, r)
			return
		}
		recalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":true,"injection":"## Graph Memory Recall\ndispatch retries use exponential backoff","status":"explore_terminal"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, MemoryType: MemoryTypeGraph}, nil)
	d.client = NewClient(server.URL)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	msg := func(id, content string) protocol.AgentMessageProjection {
		return protocol.AgentMessageProjection{
			ID: id, Target: "channel:group-1", Seq: 1, Content: content,
			ChannelID: "channel-group", ChannelKind: "group",
			InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
		}
	}
	messages := []protocol.AgentMessageProjection{
		msg("m-1", "summarize current progress"),
		msg("m-2", "summarize current progress"),
		msg("m-3", "summarize   current  progress"), // whitespace runs collapse to the same key
		msg("m-4", "list current risks"),
	}

	prepared, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 4 {
		t.Fatalf("prepared messages = %d, want 4", len(prepared))
	}
	if got := recalls.Load(); got != 2 {
		t.Fatalf("recall calls = %d, want 2 (identical queries coalesced)", got)
	}
}
