package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

var ErrCanonicalAgentRuntimeBusy = errors.New("canonical agent runtime busy")

type canonicalRuntimeMode string

const (
	canonicalRuntimeResident canonicalRuntimeMode = "resident"
	canonicalRuntimeOneShot  canonicalRuntimeMode = "one_shot"
)

// canonicalAgentRuntimeIdentity contains only process-stable configuration.
// The logical slot is always agent×runtime. The fingerprint decides whether
// that slot's provider backend must restart; it never creates another slot.
//
// Product (Frank/Parker 2026-07-28): one long-lived resident session per
// agent×runtime across channel/DM/thread. ChatSessionID / Directed / Initiator
// are per-turn prompt facts — never fingerprint / force-fresh inputs.
type canonicalAgentRuntimeIdentity struct {
	AgentID      string
	RuntimeID    string
	Provider     string
	Executable   string
	Model        string
	Thinking     string
	WorkDir      string
	SystemPrompt string
	MCP          string
	CustomArgs   []string
	Environment  map[string]string

	// Slow-changing process boundary (fingerprint). Prefer restart when unsure;
	// never put ChatSessionID, Directed, Initiator, Issue, or other per-turn fields.
	WorkspaceID         string
	AgentInstructions   string
	WorkspaceContext    string
	StartupStaticDigest string // hash of create-time AGENTS managed brief bytes
}

type canonicalAgentRuntimeIdentityParams canonicalAgentRuntimeIdentity

// newCanonicalAgentRuntimeIdentity accepts the process-stable environment
// produced by D4a's turn coordinator. It defensively revalidates that boundary
// but never extracts or owns current-turn values.
func newCanonicalAgentRuntimeIdentity(params canonicalAgentRuntimeIdentityParams) (canonicalAgentRuntimeIdentity, error) {
	if strings.TrimSpace(params.AgentID) == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime agent_id is required")
	}
	if strings.TrimSpace(params.RuntimeID) == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime runtime_id is required")
	}
	if strings.TrimSpace(params.Provider) == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime provider is required")
	}
	if strings.TrimSpace(params.Executable) == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime executable is required")
	}
	if strings.TrimSpace(params.WorkDir) == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime work_dir is required")
	}

	stable, currentTurn, err := splitAgentProcessEnvironment(params.Environment)
	if err != nil {
		return canonicalAgentRuntimeIdentity{}, fmt.Errorf("canonical runtime environment: %w", err)
	}
	if len(currentTurn) != 0 {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime identity contains current-turn environment")
	}
	return canonicalAgentRuntimeIdentity{
		AgentID:             strings.TrimSpace(params.AgentID),
		RuntimeID:           strings.TrimSpace(params.RuntimeID),
		Provider:            strings.TrimSpace(params.Provider),
		Executable:          strings.TrimSpace(params.Executable),
		Model:               strings.TrimSpace(params.Model),
		Thinking:            strings.TrimSpace(params.Thinking),
		WorkDir:             strings.TrimSpace(params.WorkDir),
		SystemPrompt:        params.SystemPrompt,
		MCP:                 params.MCP,
		CustomArgs:          append([]string(nil), params.CustomArgs...),
		Environment:         cloneStringMap(stable),
		WorkspaceID:         strings.TrimSpace(params.WorkspaceID),
		AgentInstructions:   params.AgentInstructions,
		WorkspaceContext:    params.WorkspaceContext,
		StartupStaticDigest: strings.TrimSpace(params.StartupStaticDigest),
	}, nil
}

func (i canonicalAgentRuntimeIdentity) slotKey() string {
	return i.AgentID + "\x00" + i.RuntimeID
}

func (i canonicalAgentRuntimeIdentity) fingerprint() string {
	type canonical struct {
		Provider            string      `json:"provider"`
		Executable          string      `json:"executable"`
		Model               string      `json:"model"`
		Thinking            string      `json:"thinking"`
		WorkDir             string      `json:"work_dir"`
		SystemPrompt        string      `json:"system_prompt"`
		MCP                 string      `json:"mcp"`
		CustomArgs          []string    `json:"custom_args"`
		Environment         [][2]string `json:"environment"`
		WorkspaceID         string      `json:"workspace_id"`
		AgentInstructions   string      `json:"agent_instructions"`
		WorkspaceContext    string      `json:"workspace_context"`
		StartupStaticDigest string      `json:"startup_static_digest"`
	}
	keys := make([]string, 0, len(i.Environment))
	for key := range i.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([][2]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, [2]string{key, i.Environment[key]})
	}
	payload, _ := json.Marshal(canonical{
		Provider:            i.Provider,
		Executable:          i.Executable,
		Model:               i.Model,
		Thinking:            i.Thinking,
		WorkDir:             i.WorkDir,
		SystemPrompt:        i.SystemPrompt,
		MCP:                 i.MCP,
		CustomArgs:          append([]string(nil), i.CustomArgs...),
		Environment:         environment,
		WorkspaceID:         i.WorkspaceID,
		AgentInstructions:   i.AgentInstructions,
		WorkspaceContext:    i.WorkspaceContext,
		StartupStaticDigest: i.StartupStaticDigest,
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// isCanonicalResidentProvider reports whether the provider uses the shared
// agent×runtime canonical resident pool. Sourced from
// agent.Capabilities (task #47) — do not re-list providers here.
// See TestCanonicalResidentProviderListsStayInSync.
func isCanonicalResidentProvider(provider string) bool {
	return agent.Capabilities(provider).CanonicalResident
}

func canonicalRuntimeModeFor(provider, executionProfile string) (canonicalRuntimeMode, error) {
	if executionProfile != executionProfileFull {
		return "", fmt.Errorf("execution profile %q must not use the canonical agent session", executionProfile)
	}
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return "", errors.New("provider is required")
	}
	if isCanonicalResidentProvider(trimmed) {
		return canonicalRuntimeResident, nil
	}
	return canonicalRuntimeOneShot, nil
}

type canonicalRuntimeBackendFactory func(agent.Config) (agent.Backend, func(), error)

type canonicalAgentRuntimeAcquireRequest struct {
	Identity           canonicalAgentRuntimeIdentity
	Mode               canonicalRuntimeMode
	CanonicalSessionID string
	BackendConfig      agent.Config
	Factory            canonicalRuntimeBackendFactory
	// BeforeCreate runs only when a new backend will be factory-created
	// (not on resident reuse). Create-only AGENTS write — zero filesystem I/O
	// on the reuse path. Must not leave a half-created slot if it fails.
	BeforeCreate func() error
	Now          time.Time
}

// ResidentRuntimeRecoveredSubscriber is notified when a resident backend is
// successfully factory-created for an agent×runtime slot (not on reuse).
// Used to clear the server-side crashed_since after local recovery.
type ResidentRuntimeRecoveredSubscriber func(agentID, runtimeID string)

type canonicalAgentRuntimePool struct {
	mu    sync.Mutex
	slots map[string]*canonicalAgentRuntimeSlot

	crashMu          sync.Mutex
	crashSubscribers []ResidentRuntimeCrashSubscriber

	recoverMu          sync.Mutex
	recoverSubscribers []ResidentRuntimeRecoveredSubscriber
}

type canonicalAgentRuntimeSlot struct {
	mu          sync.Mutex
	fingerprint string
	mode        canonicalRuntimeMode
	provider    string
	running     bool
	idleSince   time.Time
	backend     agent.Backend
	close       func()
}

func newCanonicalAgentRuntimePool() *canonicalAgentRuntimePool {
	return &canonicalAgentRuntimePool{slots: make(map[string]*canonicalAgentRuntimeSlot)}
}

func (p *canonicalAgentRuntimePool) acquire(request canonicalAgentRuntimeAcquireRequest) (*canonicalAgentRuntimeLease, error) {
	if p == nil {
		return nil, errors.New("canonical agent runtime pool is nil")
	}
	if request.Mode != canonicalRuntimeResident && request.Mode != canonicalRuntimeOneShot {
		return nil, fmt.Errorf("unsupported canonical runtime mode %q", request.Mode)
	}
	if request.Factory == nil {
		return nil, errors.New("canonical runtime backend factory is required")
	}
	stableEnvironment, currentTurn, err := splitAgentProcessEnvironment(request.Identity.Environment)
	if err != nil {
		return nil, fmt.Errorf("canonical runtime identity environment: %w", err)
	}
	if len(currentTurn) != 0 {
		return nil, errors.New("canonical runtime identity contains current-turn environment")
	}
	request.Identity.AgentID = strings.TrimSpace(request.Identity.AgentID)
	request.Identity.RuntimeID = strings.TrimSpace(request.Identity.RuntimeID)
	request.Identity.Provider = strings.TrimSpace(request.Identity.Provider)
	request.Identity.Executable = strings.TrimSpace(request.Identity.Executable)
	request.Identity.Model = strings.TrimSpace(request.Identity.Model)
	request.Identity.Thinking = strings.TrimSpace(request.Identity.Thinking)
	request.Identity.WorkDir = strings.TrimSpace(request.Identity.WorkDir)
	request.Identity.Environment = stableEnvironment
	key := request.Identity.slotKey()
	if request.Identity.AgentID == "" || request.Identity.RuntimeID == "" {
		return nil, errors.New("canonical runtime agent_id and runtime_id are required")
	}
	if request.Identity.Provider == "" ||
		request.Identity.Executable == "" ||
		request.Identity.WorkDir == "" {
		return nil, errors.New("canonical runtime provider, executable, and work_dir are required")
	}
	fingerprint := request.Identity.fingerprint()
	resumeSessionID := strings.TrimSpace(request.CanonicalSessionID)
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		slot = &canonicalAgentRuntimeSlot{}
		p.slots[key] = slot
	}
	slot.mu.Lock()
	p.mu.Unlock()

	defer slot.mu.Unlock()
	if slot.running {
		return nil, ErrCanonicalAgentRuntimeBusy
	}

	// Fingerprint drift (slow config / AGENTS digest) → dispose process and recreate.
	// Cross-surface chat change alone does NOT dispose or force-fresh Prior.
	configDrift := slot.fingerprint != "" &&
		(slot.fingerprint != fingerprint || slot.mode != request.Mode)
	if configDrift {
		slot.closeBackend()
	}
	prevFingerprint := slot.fingerprint
	prevMode := slot.mode
	slot.fingerprint = fingerprint
	slot.mode = request.Mode
	slot.provider = request.Identity.Provider
	slot.running = true

	var backend agent.Backend
	var closeBackend func()
	reused := request.Mode == canonicalRuntimeResident && !configDrift && slot.backend != nil
	if reused {
		backend = slot.backend
		closeBackend = slot.close
	} else {
		// Create-only: AGENTS (and any future startup disk) only on process create.
		if request.BeforeCreate != nil {
			if err := request.BeforeCreate(); err != nil {
				slot.running = false
				slot.fingerprint = prevFingerprint
				slot.mode = prevMode
				return nil, fmt.Errorf("canonical runtime before-create: %w", err)
			}
		}
		config := request.BackendConfig
		config.ExecutablePath = request.Identity.Executable
		config.Env = cloneStringMap(request.Identity.Environment)
		created, closeFn, err := request.Factory(config)
		if err != nil {
			slot.running = false
			slot.fingerprint = prevFingerprint
			slot.mode = prevMode
			return nil, fmt.Errorf("create canonical runtime backend: %w", err)
		}
		if created == nil {
			slot.running = false
			slot.fingerprint = prevFingerprint
			slot.mode = prevMode
			if closeFn != nil {
				closeFn()
			}
			return nil, errors.New("canonical runtime backend factory returned nil backend")
		}
		backend = created
		closeBackend = closeFn
		if request.Mode == canonicalRuntimeResident {
			slot.backend = created
			slot.close = closeFn
			// New resident process is up — clear any server-side "crashed"
			// fact from a prior idle death. First-ever create is a no-op clear.
			// Fire async: we still hold slot.mu here and subscribers may do I/O.
			agentID, runtimeID := request.Identity.AgentID, request.Identity.RuntimeID
			go p.notifyResidentRecovered(agentID, runtimeID)
		}
	}

	wrapped := &canonicalSessionBackend{
		backend:            backend,
		canonicalSessionID: resumeSessionID,
	}
	return &canonicalAgentRuntimeLease{
		slot:      slot,
		backend:   wrapped,
		mode:      request.Mode,
		turnClose: closeBackend,
	}, nil
}

// canonicalSessionBackend is the only Execute wrapper on the canonical path.
// Field audit (Parker): the sole unconditional override is ResumeSessionID ←
// canonicalSessionID. Prompt, Cwd, Model, SystemPrompt, MCP, ExtraArgs, and
// all other ExecOptions pass through unchanged. Upstream runTask stale-session
// fallback clears ResumeSessionID on opts AND must call ClearCanonicalResume
// or the wrapper would re-apply the stale Prior on retry.
type canonicalSessionBackend struct {
	backend            agent.Backend
	canonicalSessionID string
}

// ClearCanonicalResume drops the wrapper-owned resume id so a same-turn fresh
// retry (runTask after failed+empty SessionID) is not re-forced onto Prior.
func (b *canonicalSessionBackend) ClearCanonicalResume() {
	if b == nil {
		return
	}
	b.canonicalSessionID = ""
}

func (b *canonicalSessionBackend) Execute(ctx context.Context, prompt string, options agent.ExecOptions) (*agent.Session, error) {
	// Only ResumeSessionID is forced from lease state; see type comment audit.
	options.ResumeSessionID = b.canonicalSessionID
	return b.backend.Execute(ctx, prompt, options)
}

// clearCanonicalResumeIfPresent is the runTask hook for stale-session fallback.
func clearCanonicalResumeIfPresent(backend agent.Backend) {
	if clearer, ok := backend.(interface{ ClearCanonicalResume() }); ok {
		clearer.ClearCanonicalResume()
	}
}

type canonicalAgentRuntimeLease struct {
	slot      *canonicalAgentRuntimeSlot
	backend   agent.Backend
	mode      canonicalRuntimeMode
	turnClose func()
	once      sync.Once
}

func (l *canonicalAgentRuntimeLease) release(healthy bool) {
	l.releaseAt(healthy, time.Now())
}

func (l *canonicalAgentRuntimeLease) releaseForResult(status string, executionErr error) {
	l.release(canonicalRuntimeResultHealthy(status, executionErr))
}

func canonicalRuntimeResultHealthy(status string, executionErr error) bool {
	return executionErr == nil && status == "completed"
}

func (l *canonicalAgentRuntimeLease) releaseAt(healthy bool, now time.Time) {
	if l == nil || l.slot == nil {
		return
	}
	l.once.Do(func() {
		l.slot.mu.Lock()
		defer l.slot.mu.Unlock()
		if !l.slot.running {
			return
		}
		if l.mode == canonicalRuntimeOneShot {
			if l.turnClose != nil {
				l.turnClose()
			}
		} else if !healthy {
			l.slot.closeBackend()
		}
		l.slot.running = false
		l.slot.idleSince = now
	})
}

func (slot *canonicalAgentRuntimeSlot) closeBackend() {
	if slot.close != nil {
		slot.close()
	}
	slot.backend = nil
	slot.close = nil
}

func (p *canonicalAgentRuntimePool) slotCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// invalidateSession closes the idle provider process after the canonical
// provider session is explicitly reset. It keeps the same logical slot so the
// next turn recreates the adapter against the reset D1 state. An active turn
// must be drained by the lifecycle owner first.
func (p *canonicalAgentRuntimePool) invalidateSession(agentID, runtimeID string) error {
	if p == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" || runtimeID == "" {
		return errors.New("canonical runtime agent_id and runtime_id are required")
	}
	key := agentID + "\x00" + runtimeID
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return nil
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if slot.running {
		return ErrCanonicalAgentRuntimeBusy
	}
	slot.closeBackend()
	slot.fingerprint = ""
	slot.mode = ""
	slot.idleSince = time.Time{}
	return nil
}

// forceInvalidateSession interrupts a busy slot instead of refusing it
// (task #62 — invalidateSession is deliberately for the already-idle case
// only). It never calls closeBackend() on a running slot: that would race
// with whatever goroutine currently holds Execute() against the same
// backend. Instead it asks the backend itself to force-kill its process via
// agent.ResidentRuntimeForceKillable, then returns — the in-flight turn's own
// goroutine is expected to observe the failure and release the slot, exactly
// as it already does for an unexpected crash (task #42②'s self-heal path).
//
// If the slot is idle, this behaves exactly like invalidateSession (no
// force-kill needed, nothing to interrupt). If the backend does not
// implement ResidentRuntimeForceKillable, this fails closed with
// ErrCanonicalAgentRuntimeBusy rather than silently no-op'ing — a missing
// capability must be loud, not indistinguishable from "nothing to do."
func (p *canonicalAgentRuntimePool) forceInvalidateSession(agentID, runtimeID string) error {
	if p == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" || runtimeID == "" {
		return errors.New("canonical runtime agent_id and runtime_id are required")
	}
	key := agentID + "\x00" + runtimeID
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return nil
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if !slot.running {
		slot.closeBackend()
		slot.fingerprint = ""
		slot.mode = ""
		slot.idleSince = time.Time{}
		return nil
	}
	killable, ok := slot.backend.(agent.ResidentRuntimeForceKillable)
	if !ok {
		return ErrCanonicalAgentRuntimeBusy
	}
	return killable.ForceKill()
}

func (p *canonicalAgentRuntimePool) evictIdle(before time.Time) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	removed := 0
	for key, slot := range p.slots {
		slot.mu.Lock()
		if !slot.running && slot.idleSince.Before(before) {
			slot.closeBackend()
			delete(p.slots, key)
			removed++
		}
		slot.mu.Unlock()
	}
	return removed
}

// ResidentRuntimeCrashEvent describes a resident provider process found dead
// while idle between turns — the case task #42 exists for. A turn's own
// error path already surfaces a dead process; this is the proactive path for
// when no turn happens to be running at the moment it died, which is exactly
// how the ui-designer agent's opencode process sat crashed for 8 hours with
// nothing noticing.
type ResidentRuntimeCrashEvent struct {
	AgentID    string
	RuntimeID  string
	Provider   string
	DetectedAt time.Time
}

// ResidentRuntimeCrashSubscriber receives every crash checkResidentLiveness
// finds. Multiple independent consumers (crash-recovery restart, external
// status reporting) subscribe to the same detection pass instead of each
// polling process liveness themselves.
type ResidentRuntimeCrashSubscriber func(ResidentRuntimeCrashEvent)

// subscribeResidentRuntimeCrash registers fn to run for every future crash
// event. It does not replay past events.
func (p *canonicalAgentRuntimePool) subscribeResidentRuntimeCrash(fn ResidentRuntimeCrashSubscriber) {
	if p == nil || fn == nil {
		return
	}
	p.crashMu.Lock()
	defer p.crashMu.Unlock()
	p.crashSubscribers = append(p.crashSubscribers, fn)
}

// subscribeResidentRuntimeRecovered registers fn for successful resident
// backend creates (not reuse). Idempotent ClearAgentCrashed on the server
// is safe even when there was no prior crash.
func (p *canonicalAgentRuntimePool) subscribeResidentRuntimeRecovered(fn ResidentRuntimeRecoveredSubscriber) {
	if p == nil || fn == nil {
		return
	}
	p.recoverMu.Lock()
	defer p.recoverMu.Unlock()
	p.recoverSubscribers = append(p.recoverSubscribers, fn)
}

func (p *canonicalAgentRuntimePool) notifyResidentRecovered(agentID, runtimeID string) {
	if p == nil {
		return
	}
	p.recoverMu.Lock()
	subs := append([]ResidentRuntimeRecoveredSubscriber(nil), p.recoverSubscribers...)
	p.recoverMu.Unlock()
	for _, sub := range subs {
		sub(agentID, runtimeID)
	}
}

// checkResidentLiveness polls every idle resident slot's process liveness and
// evicts + reports any found definitively dead (known=true, alive=false).
// This must fail open: an in-flight turn, a non-resident slot, a backend that
// doesn't implement agent.ResidentRuntimeLivenessChecker, or an unknown
// liveness answer are all left untouched — none of those are proof of a
// crash, and misclassifying a merely-quiet process as dead would kill a
// perfectly healthy resident session.
func (p *canonicalAgentRuntimePool) checkResidentLiveness(now time.Time) []ResidentRuntimeCrashEvent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	type slotRef struct {
		key  string
		slot *canonicalAgentRuntimeSlot
	}
	refs := make([]slotRef, 0, len(p.slots))
	for key, slot := range p.slots {
		refs = append(refs, slotRef{key, slot})
	}
	p.mu.Unlock()

	var events []ResidentRuntimeCrashEvent
	for _, ref := range refs {
		ref.slot.mu.Lock()
		if ref.slot.running || ref.slot.mode != canonicalRuntimeResident || ref.slot.backend == nil {
			ref.slot.mu.Unlock()
			continue
		}
		checker, ok := ref.slot.backend.(agent.ResidentRuntimeLivenessChecker)
		if !ok {
			ref.slot.mu.Unlock()
			continue
		}
		alive, known := checker.RuntimeAlive()
		if !known || alive {
			ref.slot.mu.Unlock()
			continue
		}
		provider := ref.slot.provider
		ref.slot.closeBackend()
		ref.slot.fingerprint = ""
		ref.slot.mode = ""
		ref.slot.mu.Unlock()

		agentID, runtimeID := splitCanonicalSlotKey(ref.key)
		events = append(events, ResidentRuntimeCrashEvent{
			AgentID:    agentID,
			RuntimeID:  runtimeID,
			Provider:   provider,
			DetectedAt: now,
		})
	}

	if len(events) == 0 {
		return nil
	}
	p.crashMu.Lock()
	subs := append([]ResidentRuntimeCrashSubscriber(nil), p.crashSubscribers...)
	p.crashMu.Unlock()
	for _, ev := range events {
		for _, sub := range subs {
			sub(ev)
		}
	}
	return events
}

func splitCanonicalSlotKey(key string) (agentID, runtimeID string) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

func (p *canonicalAgentRuntimePool) closeAll() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	slots := make([]*canonicalAgentRuntimeSlot, 0, len(p.slots))
	for _, slot := range p.slots {
		slot.mu.Lock()
		slots = append(slots, slot)
	}
	defer func() {
		for i := len(slots) - 1; i >= 0; i-- {
			slots[i].mu.Unlock()
		}
	}()

	for _, slot := range slots {
		if slot.running {
			return ErrCanonicalAgentRuntimeBusy
		}
	}
	for _, slot := range slots {
		slot.closeBackend()
	}
	p.slots = make(map[string]*canonicalAgentRuntimeSlot)
	return nil
}

func defaultCanonicalRuntimeFactory(provider string, mode canonicalRuntimeMode) canonicalRuntimeBackendFactory {
	return func(config agent.Config) (agent.Backend, func(), error) {
		if mode == canonicalRuntimeResident {
			switch provider {
			case "pi":
				return newCanonicalPiResidentBackend(config)
			case "grok":
				return newCanonicalGrokResidentBackend(config)
			case "cursor":
				return newCanonicalCursorResidentBackend(config)
			case "opencode":
				return newCanonicalOpenCodeResidentBackend(config)
			default:
				return nil, nil, fmt.Errorf("provider %q has no resident adapter", provider)
			}
		}
		backend, err := agent.New(provider, config)
		if err != nil {
			return nil, nil, err
		}
		return backend, nil, nil
	}
}

// acquireCanonicalAgentRuntime is the D4 provider adapter entry point. D6
// supplies the provider-neutral slot contract and activates the production
// caller after wake serialization and current-turn binding are live.
func (p *canonicalAgentRuntimePool) acquireCanonicalAgentRuntime(
	identity canonicalAgentRuntimeIdentity,
	canonicalSessionID string,
	executionProfile string,
	config agent.Config,
) (*canonicalAgentRuntimeLease, error) {
	mode, err := canonicalRuntimeModeFor(identity.Provider, executionProfile)
	if err != nil {
		return nil, err
	}
	return p.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		Mode:               mode,
		CanonicalSessionID: canonicalSessionID,
		BackendConfig:      config,
		Factory:            defaultCanonicalRuntimeFactory(identity.Provider, mode),
	})
}
