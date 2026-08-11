package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

type canonicalRuntimeTestBackend struct {
	mu               sync.Mutex
	resumeSessionIDs []string
	forceKillCalls   int
	forceKillErr     error
}

// ForceKill implements ResidentRuntimeForceKillable for tests exercising
// forceInvalidateSession without a real OS process.
func (b *canonicalRuntimeTestBackend) ForceKill() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.forceKillCalls++
	return b.forceKillErr
}

func (b *canonicalRuntimeTestBackend) forceKillCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.forceKillCalls
}

func (b *canonicalRuntimeTestBackend) Execute(_ context.Context, _ string, opts agent.ExecOptions) (*agent.Session, error) {
	b.mu.Lock()
	b.resumeSessionIDs = append(b.resumeSessionIDs, opts.ResumeSessionID)
	b.mu.Unlock()
	messages := make(chan agent.Message)
	result := make(chan agent.Result, 1)
	close(messages)
	result <- agent.Result{Status: "completed", SessionID: opts.ResumeSessionID}
	close(result)
	return &agent.Session{Messages: messages, Result: result}, nil
}

func (b *canonicalRuntimeTestBackend) lastResumeSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.resumeSessionIDs) == 0 {
		return ""
	}
	return b.resumeSessionIDs[len(b.resumeSessionIDs)-1]
}

type canonicalRuntimeFactoryProbe struct {
	mu       sync.Mutex
	backends []*canonicalRuntimeTestBackend
	configs  []agent.Config
	closed   int
}

func (p *canonicalRuntimeFactoryProbe) factory(config agent.Config) (agent.Backend, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	backend := &canonicalRuntimeTestBackend{}
	p.backends = append(p.backends, backend)
	p.configs = append(p.configs, config)
	return backend, func() {
		p.mu.Lock()
		p.closed++
		p.mu.Unlock()
	}, nil
}

func (p *canonicalRuntimeFactoryProbe) counts() (created, closed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.backends), p.closed
}

func (p *canonicalRuntimeFactoryProbe) latestConfig() agent.Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.configs) == 0 {
		return agent.Config{}
	}
	return p.configs[len(p.configs)-1]
}

func canonicalRuntimeIdentityForTest(t *testing.T, model string, environment map[string]string) canonicalAgentRuntimeIdentity {
	t.Helper()
	return canonicalRuntimeIdentityForTestWithContext(t, model, "chat-session-a", environment)
}

func canonicalRuntimeIdentityForTestWithContext(t *testing.T, model, contextKey string, environment map[string]string) canonicalAgentRuntimeIdentity {
	t.Helper()
	stable, currentTurn, err := splitAgentProcessEnvironment(environment)
	if err != nil {
		t.Fatalf("splitAgentProcessEnvironment: %v", err)
	}
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:      "agent-a",
		RuntimeID:    "runtime-a",
		Provider:     "pi",
		Executable:   "/usr/local/bin/pi",
		Model:        model,
		Thinking:     "high",
		WorkDir:      "/var/lib/multica/agent-a/workspace",
		SystemPrompt: "stable system prompt",
		MCP:          `{"servers":[]}`,
		CustomArgs:   []string{"--flag"},
		Environment:  stable,
		WorkspaceID:  "workspace-a",
	})
	if err != nil {
		t.Fatalf("newCanonicalAgentRuntimeIdentity: %v", err)
	}
	if currentTurn["MULTICA_TASK_ID"] == "" {
		t.Fatal("current-turn environment did not retain MULTICA_TASK_ID")
	}
	return identity
}

func TestCanonicalAgentRuntimeIdentityExcludesCurrentTurnEnvironment(t *testing.T) {
	first := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"PATH":                            "/usr/bin",
		"ANTHROPIC_API_KEY":               "stable-provider-secret",
		"MULTICA_SERVER_URL":              "https://multica.example",
		"MULTICA_WORKSPACE_ID":            "workspace-a",
		"MULTICA_AGENT_ID":                "agent-a",
		"MULTICA_TASK_ID":                 "turn-a",
		"MULTICA_RUN_ID":                  "turn-a",
		"MULTICA_AGENT_INBOX_DELIVERY_ID": "delivery-a",
	})
	second := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"PATH":                            "/usr/bin",
		"ANTHROPIC_API_KEY":               "stable-provider-secret",
		"MULTICA_SERVER_URL":              "https://multica.example",
		"MULTICA_WORKSPACE_ID":            "workspace-a",
		"MULTICA_AGENT_ID":                "agent-a",
		"MULTICA_TASK_ID":                 "turn-b",
		"MULTICA_RUN_ID":                  "turn-b",
		"MULTICA_AGENT_INBOX_DELIVERY_ID": "delivery-b",
	})

	if first.fingerprint() != second.fingerprint() {
		t.Fatal("current-turn metadata changed the stable runtime fingerprint")
	}
	for _, key := range []string{
		"MULTICA_TASK_ID",
		"MULTICA_RUN_ID",
		"MULTICA_AGENT_INBOX_DELIVERY_ID",
	} {
		if _, ok := first.Environment[key]; ok {
			t.Fatalf("stable process environment retained current-turn key %s", key)
		}
	}
}

func TestCanonicalAgentRuntimeIdentityFailsClosedOnCredentialOrUnknownMulticaEnvironment(t *testing.T) {
	for _, key := range []string{"MULTICA_TOKEN", "MULTICA_TOKEN_FILE", "MULTICA_FUTURE_UNCLASSIFIED"} {
		_, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
			AgentID:     "agent-a",
			RuntimeID:   "runtime-a",
			Provider:    "pi",
			Executable:  "/usr/local/bin/pi",
			WorkDir:     "/var/lib/multica/agent-a/workspace",
			Environment: map[string]string{key: "secret-or-unknown"},
		})
		if err == nil {
			t.Fatalf("%s was accepted into canonical process identity", key)
		}
	}
}

func trainingCanonicalRuntimeIdentityForTest(t *testing.T, agentID, sessionKey string) canonicalAgentRuntimeIdentity {
	t.Helper()
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:    agentID,
		RuntimeID:  "shared-runtime",
		Provider:   "pi",
		Executable: "/usr/local/bin/pi",
		Model:      "openai/areal-default",
		WorkDir:    "/var/lib/multica/training",
		CustomArgs: []string{"--api-key", sessionKey},
		Environment: map[string]string{
			"MULTICA_SERVER_URL":   "https://multica.invalid",
			"MULTICA_WORKSPACE_ID": "workspace-a",
			"MULTICA_AGENT_ID":     agentID,
		},
		WorkspaceID: "workspace-a",
	})
	if err != nil {
		t.Fatalf("build training identity: %v", err)
	}
	return identity
}

func TestCanonicalAgentRuntimeIdentitySeparatesTrainingAgentsAndSessionKeys(t *testing.T) {
	first := trainingCanonicalRuntimeIdentityForTest(t, "training-agent-a", "synthetic-session-key-a")
	second := trainingCanonicalRuntimeIdentityForTest(t, "training-agent-b", "synthetic-session-key-b")
	if first.slotKey() == second.slotKey() {
		t.Fatal("distinct training agents shared one canonical runtime slot")
	}
	if first.fingerprint() == second.fingerprint() {
		t.Fatal("distinct task-scoped session arguments shared one process fingerprint")
	}
}

func TestCanonicalAgentRuntimeIdentityKeyRotationChangesRestartBoundary(t *testing.T) {
	before := trainingCanonicalRuntimeIdentityForTest(t, "training-agent", "synthetic-session-key-before")
	after := trainingCanonicalRuntimeIdentityForTest(t, "training-agent", "synthetic-session-key-after")
	if before.slotKey() != after.slotKey() {
		t.Fatal("same-agent key rotation unexpectedly changed the logical slot")
	}
	if before.fingerprint() == after.fingerprint() {
		t.Fatal("same-agent key rotation did not change the process restart boundary")
	}
}

func TestCanonicalAgentRuntimePoolCleansPreparedProcessWhenFactoryFails(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"PATH":            "/usr/bin",
		"MULTICA_TASK_ID": "turn-a",
	})
	cleanupCalls := 0
	_, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		PrepareLaunchEnvironment: func(environment map[string]string) (func(), error) {
			environment["PATH"] = "/launch-scoped/bin:" + environment["PATH"]
			return func() { cleanupCalls++ }, nil
		},
		Factory: func(config agent.Config) (agent.Backend, func(), error) {
			if !strings.HasPrefix(config.Env["PATH"], "/launch-scoped/bin:") {
				t.Fatalf("factory did not receive prepared process environment: %#v", config.Env)
			}
			return nil, nil, errors.New("factory failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("acquire error = %v, want factory failure", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("prepared process cleanup calls = %d, want 1", cleanupCalls)
	}
}

// Frank/Parker: agent×runtime long-lived across chats — no force-fresh on chat change.
func TestCanonicalAgentRuntimePoolReusesAcrossChatSurfaces(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	env := map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	}
	firstIdentity := canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-A", env)
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           firstIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := first.backend.Execute(context.Background(), "a", agent.ExecOptions{}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if got := first.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-A" {
		t.Fatalf("turn A resume = %q, want provider-session-A", got)
	}
	first.release(true)

	// Different chat surface, same agent×runtime fingerprint → reuse backend + keep Prior.
	envB := map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-b",
	}
	secondIdentity := canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", envB)
	if firstIdentity.fingerprint() != secondIdentity.fingerprint() {
		t.Fatal("chat surface alone must not change process fingerprint")
	}
	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           secondIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer second.release(true)
	if _, err := second.backend.Execute(context.Background(), "b", agent.ExecOptions{}); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if got := second.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-A" {
		t.Fatalf("turn B resume = %q, want provider-session-A (no force-fresh)", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("factory created=%d closed=%d, want 1/0 (reuse across chat)", created, closed)
	}
	if first.backend.(*canonicalSessionBackend).backend != second.backend.(*canonicalSessionBackend).backend {
		t.Fatal("cross-chat must reuse resident backend")
	}
}

func TestCanonicalSessionBackendStaleResumeFallbackClearsWrapper(t *testing.T) {
	// runTask sets opts.ResumeSessionID="" on stale resume; wrapper must also
	// drop its forced id or retry keeps the bad Prior (Barry activation BLOCK).
	inner := &canonicalRuntimeTestBackend{}
	wrapped := &canonicalSessionBackend{backend: inner, canonicalSessionID: "stale-prior"}
	if _, err := wrapped.Execute(context.Background(), "first", agent.ExecOptions{ResumeSessionID: "ignored"}); err != nil {
		t.Fatal(err)
	}
	if got := inner.lastResumeSessionID(); got != "stale-prior" {
		t.Fatalf("first resume = %q, want stale-prior", got)
	}
	// Simulate runTask fallback: clear opts is insufficient without ClearCanonicalResume.
	opts := agent.ExecOptions{ResumeSessionID: ""}
	clearCanonicalResumeIfPresent(wrapped)
	if _, err := wrapped.Execute(context.Background(), "retry", opts); err != nil {
		t.Fatal(err)
	}
	if got := inner.lastResumeSessionID(); got != "" {
		t.Fatalf("retry resume = %q, want empty after ClearCanonicalResume", got)
	}
}

func TestCanonicalAgentRuntimePoolRestartsProcessKeepsPriorOnHardFieldDrift(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	env := map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	}
	firstIdentity := canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-same", env)
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           firstIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-chat",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.release(true)

	// Slow field change (AgentInstructions) → fingerprint drift → new process, keep Prior.
	secondIdentity := firstIdentity
	secondIdentity.AgentInstructions = "updated-instructions"
	if firstIdentity.fingerprint() == secondIdentity.fingerprint() {
		t.Fatal("AgentInstructions change must alter fingerprint")
	}
	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           secondIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-chat",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer second.release(true)
	if _, err := second.backend.Execute(context.Background(), "b", agent.ExecOptions{}); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if got := second.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-chat" {
		t.Fatalf("same-chat hard-field restart resume = %q, want provider-session-chat", got)
	}
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory created=%d closed=%d, want 2/1", created, closed)
	}
}

func TestCanonicalAgentRuntimePoolReusesOneResidentSlotAcrossTurns(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	firstIdentity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})

	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           firstIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "session-agent-a",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	firstBackend := first.backend.(*canonicalSessionBackend).backend
	if _, err := first.backend.Execute(context.Background(), "first", agent.ExecOptions{ResumeSessionID: "legacy-chat-session"}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	first.release(true)

	secondIdentity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-b",
	})
	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           secondIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "session-agent-a",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer second.release(true)

	if second.backend.(*canonicalSessionBackend).backend != firstBackend {
		t.Fatal("same agent×runtime slot did not retain its resident backend")
	}
	if got := pool.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want 1", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("factory counts = created %d closed %d, want 1/0", created, closed)
	}
	if got := firstBackend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "session-agent-a" {
		t.Fatalf("resume session = %q, want canonical agent session", got)
	}
}

func TestCanonicalAgentRuntimeConfigDriftRestartsSameLogicalSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	firstIdentity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: firstIdentity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	slot := first.slot
	first.release(true)

	driftedIdentity := canonicalRuntimeIdentityForTest(t, "model-b", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-b",
	})
	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: driftedIdentity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("drift acquire: %v", err)
	}
	defer second.release(true)

	if second.slot != slot {
		t.Fatal("config drift created a second logical slot")
	}
	if got := pool.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want 1", got)
	}
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory counts = created %d closed %d, want 2/1", created, closed)
	}
}

func TestCanonicalAgentRuntimeConfigDriftCannotBypassBusySlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	firstIdentity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: firstIdentity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	driftedIdentity := firstIdentity
	driftedIdentity.Model = "model-b"
	_, err = pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: driftedIdentity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("drift while busy error = %v, want busy", err)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("busy drift touched backend: created %d closed %d", created, closed)
	}
	first.release(true)
}

func TestCanonicalAgentRuntimeOneShotAdapterAlwaysResumesCanonicalSession(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})

	for _, turnID := range []string{"turn-a", "turn-b"} {
		lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
			Identity:           identity,
			Mode:               canonicalRuntimeOneShot,
			CanonicalSessionID: "session-agent-a",
			Factory:            probe.factory,
		})
		if err != nil {
			t.Fatalf("%s acquire: %v", turnID, err)
		}
		if _, err := lease.backend.Execute(context.Background(), turnID, agent.ExecOptions{ResumeSessionID: "legacy-surface-session"}); err != nil {
			t.Fatalf("%s Execute: %v", turnID, err)
		}
		raw := lease.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend)
		if got := raw.lastResumeSessionID(); got != "session-agent-a" {
			t.Fatalf("%s resume session = %q, want canonical", turnID, got)
		}
		lease.release(true)
	}
	created, closed := probe.counts()
	if created != 2 || closed != 2 {
		t.Fatalf("factory counts = created %d closed %d, want 2/2", created, closed)
	}
	if got := pool.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want 1", got)
	}
}

func TestCanonicalAgentRuntimePoolRejectsConcurrentTurnInSameSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.release(true)

	_, err = pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("concurrent acquire error = %v, want ErrCanonicalAgentRuntimeBusy", err)
	}
}

func TestCanonicalAgentRuntimePoolRejectsUnsplitTurnEnvironment(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	identity.Environment["MULTICA_TASK_ID"] = "turn-a"
	_, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err == nil || !strings.Contains(err.Error(), "current-turn environment") {
		t.Fatalf("acquire error = %v, want current-turn rejection", err)
	}
}

func TestCanonicalAgentRuntimeUnhealthyReleaseDropsResidentBackend(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.release(false)

	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	second.release(true)
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory counts = created %d closed %d, want 2/1", created, closed)
	}
}

func TestCanonicalAgentRuntimeCancelledTurnDropsResidentBackend(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.releaseForResult("aborted", context.Canceled)

	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	second.releaseForResult("completed", nil)
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory counts = created %d closed %d, want 2/1", created, closed)
	}
}

func TestCanonicalRuntimeResultHealthIsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
		want   bool
	}{
		{status: "completed", want: true},
		{status: "completed", err: errors.New("drain failed")},
		{status: "failed"},
		{status: "aborted"},
		{status: "cancelled"},
		{status: "timeout"},
		{status: ""},
	} {
		if got := canonicalRuntimeResultHealthy(tc.status, tc.err); got != tc.want {
			t.Fatalf("canonicalRuntimeResultHealthy(%q, %v) = %v, want %v", tc.status, tc.err, got, tc.want)
		}
	}
}

func TestCanonicalRuntimeModeUsesResidentForCanonicalProviders(t *testing.T) {
	for _, tc := range []struct {
		provider string
		profile  string
		want     canonicalRuntimeMode
		wantErr  bool
	}{
		{provider: "pi", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "grok", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "cursor", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "claude", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "codex", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "kiro", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "opencode", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "pi", profile: executionProfileProtocolTurn, wantErr: true},
	} {
		t.Run(tc.provider+"/"+tc.profile, func(t *testing.T) {
			got, err := canonicalRuntimeModeFor(tc.provider, tc.profile)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected restricted sidecar to stay outside canonical session")
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalRuntimeModeFor: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCanonicalRuntimeDefaultFactoriesMatchProviderMode(t *testing.T) {
	for _, provider := range []string{"pi", "grok", "cursor", "opencode", "kiro", "codex", "claude"} {
		factory := defaultCanonicalRuntimeFactory(provider, canonicalRuntimeResident)
		backend, closeBackend, err := factory(agent.Config{ExecutablePath: "/nonexistent/" + provider})
		if err != nil {
			t.Fatalf("%s resident factory: %v", provider, err)
		}
		if backend == nil || closeBackend == nil {
			t.Fatalf("%s resident factory returned backend_nil=%v close_nil=%v", provider, backend == nil, closeBackend == nil)
		}
		closeBackend()
	}

	factory := defaultCanonicalRuntimeFactory("claude", canonicalRuntimeOneShot)
	backend, closeBackend, err := factory(agent.Config{ExecutablePath: "/nonexistent/claude"})
	if err != nil {
		t.Fatalf("one-shot factory: %v", err)
	}
	if backend == nil || closeBackend != nil {
		t.Fatalf("one-shot factory returned backend_nil=%v close_nil=%v", backend == nil, closeBackend == nil)
	}
}

func TestCanonicalAgentRuntimeDifferentAgentsDoNotShareSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	first := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	second := first
	second.AgentID = "agent-b"
	second.Environment = cloneStringMap(first.Environment)
	second.Environment["MULTICA_AGENT_ID"] = "agent-b"

	firstLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: first,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer firstLease.release(true)
	secondLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: second,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("second agent acquire: %v", err)
	}
	defer secondLease.release(true)

	if got := pool.slotCount(); got != 2 {
		t.Fatalf("slot count = %d, want 2", got)
	}
}

func TestCanonicalAgentRuntimeCloseAllRejectsBusySlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := pool.closeAll(); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("closeAll error = %v, want busy", err)
	}
	lease.release(true)
	if err := pool.closeAll(); err != nil {
		t.Fatalf("closeAll idle: %v", err)
	}
	if got := pool.slotCount(); got != 0 {
		t.Fatalf("slot count after closeAll = %d", got)
	}
}

func TestCanonicalAgentRuntimeEvictIdleClosesOnlyExpiredResidentSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.releaseAt(true, time.Unix(100, 0))
	if got := pool.evictIdle(time.Unix(100, 0)); got != 0 {
		t.Fatalf("evicted at equal boundary = %d, want 0", got)
	}
	if got := pool.evictIdle(time.Unix(101, 0)); got != 1 {
		t.Fatalf("evicted expired = %d, want 1", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 1 {
		t.Fatalf("factory counts = created %d closed %d, want 1/1", created, closed)
	}
}

func TestCanonicalAgentRuntimeSessionResetInvalidatesIdleBackendInSameSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	first, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "session-before-reset",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	slot := first.slot
	first.release(true)
	if err := pool.invalidateSession("agent-a", "runtime-a"); err != nil {
		t.Fatalf("invalidateSession: %v", err)
	}

	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer second.release(true)
	if second.slot != slot {
		t.Fatal("session reset created a second logical slot")
	}
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory counts after reset = created %d closed %d, want 2/1", created, closed)
	}
}

func TestCanonicalAgentRuntimeSessionResetRejectsBusySlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.release(true)
	if err := pool.invalidateSession("agent-a", "runtime-a"); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("busy invalidate error = %v, want busy", err)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("busy invalidate touched backend: created %d closed %d", created, closed)
	}
}

// TestCanonicalAgentRuntimeForceInvalidateSessionKillsBusySlot pins task
// #62's core requirement: unlike invalidateSession, forceInvalidateSession
// must not refuse a busy slot — it must call the backend's ForceKill()
// instead of closeBackend(), leaving the in-flight turn's own goroutine
// responsible for releasing the slot once it observes the failure (see
// the design doc's §3/§4 for why closeBackend() itself stays off-limits
// while running=true).
func TestCanonicalAgentRuntimeForceInvalidateSessionKillsBusySlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	backend := probe.backends[0]

	if err := pool.forceInvalidateSession("agent-a", "runtime-a"); err != nil {
		t.Fatalf("forceInvalidateSession on busy slot: %v", err)
	}
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("ForceKill called %d times, want 1", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("forceInvalidateSession touched closeBackend: created %d closed %d — it must not call closeBackend() on a running slot, only ForceKill()", created, closed)
	}

	// The in-flight "turn" (simulated by not having released the lease yet)
	// releasing afterward — exactly as Execute()'s own goroutine would once
	// it observes the killed process. A force-killed turn is not a healthy
	// completion, so it releases as unhealthy, same as any other execution
	// error — that's what actually triggers closeBackend() here.
	lease.release(false)
	created, closed = probe.counts()
	if closed != 1 {
		t.Fatalf("closed count after release = %d, want 1", closed)
	}
	_ = created
}

// TestCanonicalAgentRuntimeForceInvalidateSessionRejectsNonForceKillableBackend
// pins the fail-closed requirement from the design doc: a backend that
// doesn't implement ResidentRuntimeForceKillable must not be silently
// no-op'd — forceInvalidateSession falls back to the existing busy
// rejection so a missing capability is loud, not silent.
func TestCanonicalAgentRuntimeForceInvalidateSessionRejectsNonForceKillableBackend(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	notForceKillable := &canonicalRuntimeNonForceKillableTestBackend{}
	factory := func(_ agent.Config) (agent.Backend, func(), error) {
		return notForceKillable, func() {}, nil
	}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.release(true)
	if err := pool.forceInvalidateSession("agent-a", "runtime-a"); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("forceInvalidateSession error = %v, want busy (fail closed, not silent no-op)", err)
	}
}

// canonicalRuntimeNonForceKillableTestBackend deliberately does not
// implement ResidentRuntimeForceKillable.
type canonicalRuntimeNonForceKillableTestBackend struct{}

func (b *canonicalRuntimeNonForceKillableTestBackend) Execute(_ context.Context, _ string, opts agent.ExecOptions) (*agent.Session, error) {
	messages := make(chan agent.Message)
	result := make(chan agent.Result, 1)
	close(messages)
	result <- agent.Result{Status: "completed", SessionID: opts.ResumeSessionID}
	close(result)
	return &agent.Session{Messages: messages, Result: result}, nil
}

func TestCanonicalAgentRuntimeSessionResetRejectsEmptyIdentity(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	for _, identity := range [][2]string{{"", "runtime-a"}, {"agent-a", ""}} {
		if err := pool.invalidateSession(identity[0], identity[1]); err == nil {
			t.Fatalf("invalidateSession(%q, %q) accepted empty identity", identity[0], identity[1])
		}
	}
}

func TestCanonicalAgentRuntimeLeaseReleaseIsIdempotent(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeOneShot,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.release(true)
	lease.release(false)
	created, closed := probe.counts()
	if created != 1 || closed != 1 {
		t.Fatalf("factory counts = created %d closed %d, want 1/1", created, closed)
	}
}

func TestCanonicalAgentRuntimeSlotIdleTimestampAdvances(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
		Now:      time.Unix(100, 0),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.releaseAt(true, time.Unix(200, 0))
	lease.slot.mu.Lock()
	idleSince := lease.slot.idleSince
	lease.slot.mu.Unlock()
	if !idleSince.Equal(time.Unix(200, 0)) {
		t.Fatalf("idleSince = %v, want release time", idleSince)
	}
}

// canonicalRuntimeLivenessTestBackend extends canonicalRuntimeTestBackend
// with a controllable agent.ResidentRuntimeLivenessChecker answer, so tests
// can simulate a resident process that has died between turns without
// spawning a real subprocess.
type canonicalRuntimeLivenessTestBackend struct {
	canonicalRuntimeTestBackend
	mu     sync.Mutex
	alive  bool
	known  bool
	closed bool
}

func (b *canonicalRuntimeLivenessTestBackend) RuntimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.alive, b.known
}

func (b *canonicalRuntimeLivenessTestBackend) setLiveness(alive, known bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.alive, b.known = alive, known
}

func (b *canonicalRuntimeLivenessTestBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}

func newLivenessFactory(backend *canonicalRuntimeLivenessTestBackend) canonicalRuntimeBackendFactory {
	return func(agent.Config) (agent.Backend, func(), error) {
		return backend, backend.Close, nil
	}
}

// TestCheckResidentLivenessEvictsAndReportsOnlyConfirmedDeadIdleSlots pins the
// fail-open contract task #42 needs: a confirmed-dead idle resident slot is
// evicted and reported exactly once; a slot that is alive, unknown, busy
// (in-flight turn), or non-resident must never be touched or reported —
// misclassifying any of those as a crash would kill a healthy session.
func TestCheckResidentLivenessEvictsAndReportsOnlyConfirmedDeadIdleSlots(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()

	dead := &canonicalRuntimeLivenessTestBackend{}
	dead.setLiveness(false, true) // known dead
	deadIdentity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-dead", RuntimeID: "runtime-dead", Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/agent-dead",
	})
	if err != nil {
		t.Fatalf("dead identity: %v", err)
	}
	deadLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: deadIdentity, Mode: canonicalRuntimeResident, Factory: newLivenessFactory(dead),
	})
	if err != nil {
		t.Fatalf("acquire dead: %v", err)
	}
	deadLease.release(true)

	alive := &canonicalRuntimeLivenessTestBackend{}
	alive.setLiveness(true, true) // known alive
	aliveIdentity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-alive", RuntimeID: "runtime-alive", Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/agent-alive",
	})
	if err != nil {
		t.Fatalf("alive identity: %v", err)
	}
	aliveLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: aliveIdentity, Mode: canonicalRuntimeResident, Factory: newLivenessFactory(alive),
	})
	if err != nil {
		t.Fatalf("acquire alive: %v", err)
	}
	aliveLease.release(true)

	unknown := &canonicalRuntimeLivenessTestBackend{}
	unknown.setLiveness(false, false) // liveness undetermined — must fail open
	unknownIdentity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-unknown", RuntimeID: "runtime-unknown", Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/agent-unknown",
	})
	if err != nil {
		t.Fatalf("unknown identity: %v", err)
	}
	unknownLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: unknownIdentity, Mode: canonicalRuntimeResident, Factory: newLivenessFactory(unknown),
	})
	if err != nil {
		t.Fatalf("acquire unknown: %v", err)
	}
	unknownLease.release(true)

	busyDead := &canonicalRuntimeLivenessTestBackend{}
	busyDead.setLiveness(false, true) // would report dead if it weren't mid-turn
	busyIdentity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-busy", RuntimeID: "runtime-busy", Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/agent-busy",
	})
	if err != nil {
		t.Fatalf("busy identity: %v", err)
	}
	// Left running (no release) to simulate an in-flight turn.
	if _, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: busyIdentity, Mode: canonicalRuntimeResident, Factory: newLivenessFactory(busyDead),
	}); err != nil {
		t.Fatalf("acquire busy: %v", err)
	}

	oneShotProbe := &canonicalRuntimeFactoryProbe{}
	oneShotIdentity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-oneshot", RuntimeID: "runtime-oneshot", Provider: "claude",
		Executable: "/usr/local/bin/claude", WorkDir: "/var/lib/multica/agent-oneshot",
	})
	if err != nil {
		t.Fatalf("oneshot identity: %v", err)
	}
	oneShotLease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: oneShotIdentity, Mode: canonicalRuntimeOneShot, Factory: oneShotProbe.factory,
	})
	if err != nil {
		t.Fatalf("acquire one-shot: %v", err)
	}
	oneShotLease.release(true)

	var mu sync.Mutex
	var received []ResidentRuntimeCrashEvent
	pool.subscribeResidentRuntimeCrash(func(ev ResidentRuntimeCrashEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, ev)
	})

	events := pool.checkResidentLiveness(time.Unix(500, 0))
	if len(events) != 1 {
		t.Fatalf("checkResidentLiveness returned %d events, want 1: %+v", len(events), events)
	}
	if events[0].AgentID != "agent-dead" || events[0].RuntimeID != "runtime-dead" || events[0].Provider != "opencode" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
	if !events[0].DetectedAt.Equal(time.Unix(500, 0)) {
		t.Fatalf("DetectedAt = %v, want %v", events[0].DetectedAt, time.Unix(500, 0))
	}

	mu.Lock()
	gotSubscriber := len(received) == 1 && received[0].AgentID == "agent-dead"
	mu.Unlock()
	if !gotSubscriber {
		t.Fatalf("subscriber did not receive the crash event: %+v", received)
	}

	if !dead.closed {
		t.Fatal("confirmed-dead backend was not closed")
	}
	if alive.closed {
		t.Fatal("alive backend must not be closed")
	}
	if unknown.closed {
		t.Fatal("unknown-liveness backend must not be closed (fail open)")
	}
	if busyDead.closed {
		t.Fatal("busy (in-flight) backend must not be closed even though it would report dead")
	}

	// A second pass over the now-evicted dead slot must not re-report it —
	// closeBackend already nils slot.backend, so the type assertion on nil
	// backend naturally excludes it.
	if events := pool.checkResidentLiveness(time.Unix(600, 0)); len(events) != 0 {
		t.Fatalf("second pass reported %d events, want 0 (no duplicate crash reports): %+v", len(events), events)
	}
}

// TestCheckResidentLivenessSupportsMultipleSubscribers pins the "one
// detection pass, many independent consumers" contract Vera's #42③ status
// reporting depends on — it must not have to run its own liveness poll.
func TestCheckResidentLivenessSupportsMultipleSubscribers(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	dead := &canonicalRuntimeLivenessTestBackend{}
	dead.setLiveness(false, true)
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: "agent-dead", RuntimeID: "runtime-dead", Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/agent-dead",
	})
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity, Mode: canonicalRuntimeResident, Factory: newLivenessFactory(dead),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.release(true)

	var mu sync.Mutex
	var firstSeen, secondSeen int
	pool.subscribeResidentRuntimeCrash(func(ResidentRuntimeCrashEvent) {
		mu.Lock()
		firstSeen++
		mu.Unlock()
	})
	pool.subscribeResidentRuntimeCrash(func(ResidentRuntimeCrashEvent) {
		mu.Lock()
		secondSeen++
		mu.Unlock()
	})

	pool.checkResidentLiveness(time.Unix(700, 0))

	mu.Lock()
	defer mu.Unlock()
	if firstSeen != 1 || secondSeen != 1 {
		t.Fatalf("subscriber call counts = %d, %d, want 1, 1", firstSeen, secondSeen)
	}
}

func TestCanonicalAgentRuntimePoolIsActivatedForMessageCoordinator(t *testing.T) {
	// The coordinator must establish a resident provider before accepting a
	// canonical Delivery, otherwise an ACK could outrun local durability.
	raw, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("read daemon.go: %v", err)
	}
	if !strings.Contains(string(raw), "ensureResidentMessageRuntime(") {
		t.Fatal("Message delivery must ensure its resident runtime before acceptance")
	}
}

func TestIsResidentAcceptBusyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not busy", err: nil, want: false},
		{name: "deadline exceeded is busy", err: context.DeadlineExceeded, want: true},
		{name: "context canceled is busy", err: context.Canceled, want: true},
		{name: "claude resident turn busy", err: errors.New("claude stream-json turn busy: native turn is active"), want: true},
		{name: "claude acp turn busy", err: errors.New("claude ACP turn busy: concurrent claude ACP turn"), want: true},
		{name: "codex resident overlap", err: errors.New("codex app-server turn busy: canonical Message handoff overlaps an active turn"), want: true},
		{name: "cursor acp turn busy", err: errors.New("cursor ACP turn busy: concurrent cursor ACP turn"), want: true},
		{name: "grok acp turn busy", err: errors.New("grok ACP turn busy"), want: true},
		{name: "pi rpc turn busy", err: errors.New("pi rpc turn busy"), want: true},
		{name: "unrelated marshal error is not busy", err: errors.New("json: unsupported type"), want: false},
		{name: "unrelated io error is not busy", err: errors.New("read: connection reset"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isResidentAcceptBusyErr(tc.err); got != tc.want {
				t.Fatalf("isResidentAcceptBusyErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
