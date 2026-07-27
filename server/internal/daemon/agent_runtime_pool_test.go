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
	closed   int
}

func (p *canonicalRuntimeFactoryProbe) factory(_ agent.Config) (agent.Backend, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	backend := &canonicalRuntimeTestBackend{}
	p.backends = append(p.backends, backend)
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
		ContextKey:   contextKey,
		WorkspaceID:  "workspace-a",
		Directed:     true,
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
			ContextKey:  "chat-session-a",
			Environment: map[string]string{key: "secret-or-unknown"},
		})
		if err == nil {
			t.Fatalf("%s was accepted into canonical process identity", key)
		}
	}
}

func TestCanonicalAgentRuntimePoolRotatesFreshSessionOnContextKeyChange(t *testing.T) {
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

	// Cross-chat claim still carries a prior session id (wrong or stale) — must force fresh.
	envB := map[string]string{
		"PATH":                 "/usr/bin",
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-b",
	}
	secondIdentity := canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", envB)
	second, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           secondIdentity,
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A", // poisoned claim
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer second.release(true)
	if _, err := second.backend.Execute(context.Background(), "b", agent.ExecOptions{}); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if got := second.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "" {
		t.Fatalf("turn B resume = %q, want empty fresh session after context key rotate", got)
	}
	created, closed := probe.counts()
	if created != 2 || closed != 1 {
		t.Fatalf("factory created=%d closed=%d, want 2/1 (dispose on context key change)", created, closed)
	}
	innerA := first.backend.(*canonicalSessionBackend).backend
	innerB := second.backend.(*canonicalSessionBackend).backend
	if innerA == innerB {
		t.Fatal("context key change reused resident backend")
	}
}

func TestCanonicalAgentRuntimePoolKeepsForceFreshAfterUnhealthyCrossChat(t *testing.T) {
	// A healthy → B acquire force-fresh → B unhealthy → retry B with poisoned
	// Prior must still force-fresh (lastContextKey stays A until B healthy).
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	env := func(turn string) map[string]string {
		return map[string]string{
			"PATH":                 "/usr/bin",
			"MULTICA_SERVER_URL":   "https://multica.example",
			"MULTICA_WORKSPACE_ID": "workspace-a",
			"MULTICA_AGENT_ID":     "agent-a",
			"MULTICA_TASK_ID":      turn,
		}
	}
	a, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-A", env("turn-a")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	a.release(true)

	b1, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", env("turn-b1")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("B1 acquire: %v", err)
	}
	if _, err := b1.backend.Execute(context.Background(), "b1", agent.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := b1.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "" {
		t.Fatalf("B1 resume = %q, want empty", got)
	}
	b1.release(false) // unhealthy — must NOT commit lastContextKey=B

	b2, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", env("turn-b2")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A", // still poisoned claim
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("B2 acquire: %v", err)
	}
	defer b2.release(true)
	if _, err := b2.backend.Execute(context.Background(), "b2", agent.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := b2.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "" {
		t.Fatalf("B2 retry resume = %q, want empty (fresh fence must survive unhealthy B1)", got)
	}
}

func TestCanonicalAgentRuntimePoolKeepsForceFreshAfterFactoryFailCrossChat(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	env := func(turn string) map[string]string {
		return map[string]string{
			"PATH":                 "/usr/bin",
			"MULTICA_SERVER_URL":   "https://multica.example",
			"MULTICA_WORKSPACE_ID": "workspace-a",
			"MULTICA_AGENT_ID":     "agent-a",
			"MULTICA_TASK_ID":      turn,
		}
	}
	a, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-A", env("turn-a")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	a.release(true)

	failN := 0
	failThenProbe := func(cfg agent.Config) (agent.Backend, func(), error) {
		failN++
		if failN == 1 {
			return nil, nil, errors.New("simulated factory failure")
		}
		return probe.factory(cfg)
	}
	_, err = pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", env("turn-b-fail")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            failThenProbe,
	})
	if err == nil {
		t.Fatal("expected factory failure on first B acquire")
	}

	b2, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           canonicalRuntimeIdentityForTestWithContext(t, "model-a", "chat-B", env("turn-b-retry")),
		Mode:               canonicalRuntimeResident,
		CanonicalSessionID: "provider-session-A",
		Factory:            probe.factory,
	})
	if err != nil {
		t.Fatalf("B retry acquire: %v", err)
	}
	defer b2.release(true)
	if _, err := b2.backend.Execute(context.Background(), "b", agent.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := b2.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "" {
		t.Fatalf("B after factory-fail resume = %q, want empty", got)
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

	// Same ContextKey, Directed flips → fingerprint drift → new process, keep Prior.
	secondIdentity := firstIdentity
	secondIdentity.Directed = false
	if firstIdentity.fingerprint() == secondIdentity.fingerprint() {
		t.Fatal("Directed change must alter fingerprint")
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

func TestCanonicalRuntimeModeUsesResidentOnlyForFullPiAndGrok(t *testing.T) {
	for _, tc := range []struct {
		provider string
		profile  string
		want     canonicalRuntimeMode
		wantErr  bool
	}{
		{provider: "pi", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "grok", profile: executionProfileFull, want: canonicalRuntimeResident},
		{provider: "cursor", profile: executionProfileFull, want: canonicalRuntimeOneShot},
		{provider: "claude", profile: executionProfileFull, want: canonicalRuntimeOneShot},
		{provider: "codex", profile: executionProfileFull, want: canonicalRuntimeOneShot},
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

func TestCanonicalCursorUsesOneShotAdapterAndResumesPerAgentSession(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "cursor-model", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	identity.Provider = "cursor"
	identity.Executable = "/usr/local/bin/cursor-agent"

	mode, err := canonicalRuntimeModeFor(identity.Provider, executionProfileFull)
	if err != nil {
		t.Fatalf("canonicalRuntimeModeFor cursor: %v", err)
	}
	if mode != canonicalRuntimeOneShot {
		t.Fatalf("cursor mode = %q, want %q", mode, canonicalRuntimeOneShot)
	}

	for turn, surfaceSessionID := range []string{"group-session", "dm-session"} {
		lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
			Identity:           identity,
			Mode:               mode,
			CanonicalSessionID: "cursor-agent-session",
			Factory:            probe.factory,
		})
		if err != nil {
			t.Fatalf("turn %d acquire: %v", turn+1, err)
		}
		rawBackend := lease.backend.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend)
		if _, err := lease.backend.Execute(context.Background(), "turn", agent.ExecOptions{
			ResumeSessionID: surfaceSessionID,
		}); err != nil {
			t.Fatalf("turn %d Execute: %v", turn+1, err)
		}
		if got := rawBackend.lastResumeSessionID(); got != "cursor-agent-session" {
			t.Fatalf("turn %d resume session = %q, want canonical Cursor agent session", turn+1, got)
		}
		lease.releaseForResult("completed", nil)
	}

	if got := pool.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want one agent×runtime slot", got)
	}
	created, closed := probe.counts()
	if created != 2 || closed != 2 {
		t.Fatalf("factory counts = created %d closed %d, want 2/2", created, closed)
	}
}

func TestCanonicalRuntimeDefaultFactoriesMatchProviderMode(t *testing.T) {
	for _, provider := range []string{"pi", "grok"} {
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

func TestCanonicalAgentRuntimePoolIsActivatedForD6(t *testing.T) {
	// Behavioral proof of reuse is TestTryCanonicalChatBackendReusesResidentSlotAcrossTaskWorkdirs.
	// This only pins the runTask call site so the entry is not left unhooked.
	raw, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("read daemon.go: %v", err)
	}
	if !strings.Contains(string(raw), "tryCanonicalChatBackend(") {
		t.Fatal("D6-1b must invoke tryCanonicalChatBackend from runTask")
	}
}
