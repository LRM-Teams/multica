package daemon

import (
	"errors"
	"testing"
	"time"
)

func TestPersistentRuntimeIdentityKeyIncludesExecutionBoundaries(t *testing.T) {
	base := persistentRuntimeIdentity{
		AgentID: "a", RuntimeID: "r", ChatSessionID: "chat-a", Executable: "grok", Model: "grok-build",
		Thinking: "high", WorkDir: "/repo", SystemPrompt: "system", MCP: "{}",
		CustomArgs: []string{"--safe"}, Environment: map[string]string{"X": "1", "Y": "2"},
	}
	if got, want := base.key(), base.key(); got != want {
		t.Fatalf("same identity key = %q, want %q", got, want)
	}
	for name, mutate := range map[string]func(*persistentRuntimeIdentity){
		"chat session": func(i *persistentRuntimeIdentity) { i.ChatSessionID = "chat-b" },
		"model":        func(i *persistentRuntimeIdentity) { i.Model = "other" },
		"thinking":     func(i *persistentRuntimeIdentity) { i.Thinking = "low" },
		"workdir":      func(i *persistentRuntimeIdentity) { i.WorkDir = "/other" },
		"mcp":          func(i *persistentRuntimeIdentity) { i.MCP = "{\\\"mcpServers\\\":{}}" },
		"environment":  func(i *persistentRuntimeIdentity) { i.Environment["X"] = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Environment = map[string]string{"X": "1", "Y": "2"}
			mutate(&changed)
			if changed.key() == base.key() {
				t.Fatalf("%s change reused the session key", name)
			}
		})
	}
}

func TestPersistentRuntimePoolNeverQueuesAndRecyclesFailures(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	p := newPersistentRuntimePool()
	id := persistentRuntimeIdentity{AgentID: "a", RuntimeID: "r"}
	first, err := p.acquire(id, now)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := p.acquire(id, now); !errors.Is(err, ErrPersistentRuntimeSessionBusy) {
		t.Fatalf("busy acquire error = %v, want ErrPersistentRuntimeSessionBusy", err)
	}
	first.release(true, now.Add(time.Second))
	second, err := p.acquire(id, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("reacquire idle session: %v", err)
	}
	if second.session != first.session {
		t.Fatal("healthy idle session was not reused")
	}
	second.release(false, now.Add(3*time.Second))
	fresh, err := p.acquire(id, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("acquire after failure: %v", err)
	}
	if fresh.session == first.session {
		t.Fatal("failed session was reused")
	}
}

func TestPersistentRuntimePoolEvictsOnlyIdleSessions(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	p := newPersistentRuntimePool()
	idle, _ := p.acquire(persistentRuntimeIdentity{AgentID: "idle"}, now)
	idle.release(true, now)
	running, _ := p.acquire(persistentRuntimeIdentity{AgentID: "running"}, now)
	if got := p.evictIdle(now.Add(time.Second)); got != 1 {
		t.Fatalf("evicted %d sessions, want 1", got)
	}
	if _, err := p.acquire(persistentRuntimeIdentity{AgentID: "running"}, now); !errors.Is(err, ErrPersistentRuntimeSessionBusy) {
		t.Fatalf("running session lost during eviction: %v", err)
	}
	running.release(false, now)
}

func TestPersistentRuntimePoolDoesNotShareAcrossChatSessions(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	p := newPersistentRuntimePool()
	base := persistentRuntimeIdentity{AgentID: "agent", RuntimeID: "runtime", ChatSessionID: "chat-a"}
	first, err := p.acquire(base, now)
	if err != nil {
		t.Fatalf("acquire first chat: %v", err)
	}
	first.release(true, now)

	otherChat := base
	otherChat.ChatSessionID = "chat-b"
	second, err := p.acquire(otherChat, now)
	if err != nil {
		t.Fatalf("acquire second chat: %v", err)
	}
	if second.session == first.session {
		t.Fatal("different chat sessions reused one persistent runtime")
	}
	second.release(false, now)
}

func TestPersistentRuntimePoolEvictsMatchingChatAfterUnsafeCompletion(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	p := newPersistentRuntimePool()
	target := persistentRuntimeIdentity{AgentID: "agent", RuntimeID: "runtime", ChatSessionID: "chat-a"}
	kept := persistentRuntimeIdentity{AgentID: "agent", RuntimeID: "runtime", ChatSessionID: "chat-b"}

	targetLease, _ := p.acquire(target, now)
	targetSession := targetLease.session
	targetLease.release(true, now)
	keptLease, _ := p.acquire(kept, now)
	keptSession := keptLease.session
	keptLease.release(true, now)

	if got := p.evictChat("agent", "runtime", "chat-a"); got != 1 {
		t.Fatalf("evicted matching chat sessions = %d, want 1", got)
	}
	reacquiredTarget, err := p.acquire(target, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reacquire evicted target: %v", err)
	}
	if reacquiredTarget.session == targetSession {
		t.Fatal("resume-unsafe target session was retained")
	}
	reacquiredTarget.release(false, now)

	reacquiredKept, err := p.acquire(kept, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reacquire unrelated chat: %v", err)
	}
	if reacquiredKept.session != keptSession {
		t.Fatal("unrelated chat session was evicted")
	}
	reacquiredKept.release(false, now)
}

func TestPiPersistentRuntimePoolEvictsMatchingChatAfterUnsafeCompletion(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	p := newPiPersistentPool()
	target := piPersistentIdentity{AgentID: "agent", RuntimeID: "runtime", ChatSessionID: "chat-a"}
	kept := piPersistentIdentity{AgentID: "agent", RuntimeID: "runtime", ChatSessionID: "chat-b"}

	targetLease, _ := p.acquire(target, now)
	targetSession := targetLease.session
	targetLease.release(true, now)
	keptLease, _ := p.acquire(kept, now)
	keptSession := keptLease.session
	keptLease.release(true, now)

	if got := p.evictChat("agent", "runtime", "chat-a"); got != 1 {
		t.Fatalf("evicted matching chat sessions = %d, want 1", got)
	}
	reacquiredTarget, err := p.acquire(target, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reacquire evicted target: %v", err)
	}
	if reacquiredTarget.session == targetSession {
		t.Fatal("resume-unsafe target session was retained")
	}
	reacquiredTarget.release(false, now)

	reacquiredKept, err := p.acquire(kept, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reacquire unrelated chat: %v", err)
	}
	if reacquiredKept.session != keptSession {
		t.Fatal("unrelated chat session was evicted")
	}
	reacquiredKept.release(false, now)
}

func TestUsesPersistentGrokChatRuntime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		task     Task
		want     bool
	}{
		{name: "grok chat", provider: "grok", task: Task{ChatMessage: "hello"}, want: true},
		{name: "grok issue", provider: "grok", task: Task{IssueID: "issue-1", ChatMessage: "hello"}, want: false},
		{name: "other provider chat", provider: "pi", task: Task{ChatMessage: "hello"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesPersistentGrokChatRuntime(tc.provider, tc.task); got != tc.want {
				t.Fatalf("usesPersistentGrokChatRuntime(%q, %+v) = %v, want %v", tc.provider, tc.task, got, tc.want)
			}
		})
	}
}
