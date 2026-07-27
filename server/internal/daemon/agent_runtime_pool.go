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
// ContextKey is Multica ChatSessionID (not task.ID). When it changes the
// resident process must dispose and start a fresh provider session — Pi/Grok
// load AGENTS/context at process start only, so cross-chat file rewrite alone
// leaves the old task's in-memory context (Parker/Barry real-Pi proof).
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

	// Hard-instruction / context boundary fields (fingerprint). Prefer rotate
	// when unsure; never put task.ID, Initiator, or other per-turn prompt fields.
	ContextKey          string // ChatSessionID
	WorkspaceID         string
	Directed            bool
	ManagedRole         string
	AgentInstructions   string
	WorkspaceContext    string
	StartupStaticDigest string // canonical hash of slow-changing startup bundle bytes
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
	identity := canonicalAgentRuntimeIdentity{
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
		ContextKey:          strings.TrimSpace(params.ContextKey),
		WorkspaceID:         strings.TrimSpace(params.WorkspaceID),
		Directed:            params.Directed,
		ManagedRole:         strings.TrimSpace(params.ManagedRole),
		AgentInstructions:   params.AgentInstructions,
		WorkspaceContext:    params.WorkspaceContext,
		StartupStaticDigest: strings.TrimSpace(params.StartupStaticDigest),
	}
	if identity.ContextKey == "" {
		return canonicalAgentRuntimeIdentity{}, errors.New("canonical runtime context_key (chat_session_id) is required")
	}
	return identity, nil
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
		ContextKey          string      `json:"context_key"`
		WorkspaceID         string      `json:"workspace_id"`
		Directed            bool        `json:"directed"`
		ManagedRole         string      `json:"managed_role"`
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
		ContextKey:          i.ContextKey,
		WorkspaceID:         i.WorkspaceID,
		Directed:            i.Directed,
		ManagedRole:         i.ManagedRole,
		AgentInstructions:   i.AgentInstructions,
		WorkspaceContext:    i.WorkspaceContext,
		StartupStaticDigest: i.StartupStaticDigest,
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// willReuseResident reports whether acquire would reuse an existing resident
// backend without factory create (same fingerprint + mode + idle backend).
// Used so option-A materialize runs only before process create, not on reuse.
func (p *canonicalAgentRuntimePool) willReuseResident(identity canonicalAgentRuntimeIdentity, mode canonicalRuntimeMode) bool {
	if p == nil || mode != canonicalRuntimeResident {
		return false
	}
	key := identity.slotKey()
	fingerprint := identity.fingerprint()
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return false
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if slot.running || slot.backend == nil {
		return false
	}
	if slot.fingerprint == "" || slot.fingerprint != fingerprint || slot.mode != mode {
		return false
	}
	// Context key rotate always creates a new process path.
	if slot.lastContextKey != "" && slot.lastContextKey != identity.ContextKey {
		return false
	}
	return true
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

func canonicalRuntimeModeFor(provider, executionProfile string) (canonicalRuntimeMode, error) {
	if executionProfile != executionProfileFull {
		return "", fmt.Errorf("execution profile %q must not use the canonical agent session", executionProfile)
	}
	switch strings.TrimSpace(provider) {
	case "pi", "grok", "cursor":
		return canonicalRuntimeResident, nil
	case "":
		return "", errors.New("provider is required")
	default:
		return canonicalRuntimeOneShot, nil
	}
}

type canonicalRuntimeBackendFactory func(agent.Config) (agent.Backend, func(), error)

type canonicalAgentRuntimeAcquireRequest struct {
	Identity           canonicalAgentRuntimeIdentity
	Mode               canonicalRuntimeMode
	CanonicalSessionID string
	BackendConfig      agent.Config
	Factory            canonicalRuntimeBackendFactory
	// BeforeCreate runs only when a new backend will be factory-created
	// (not on resident reuse). Used for option-A startup materialize — zero
	// filesystem I/O on the reuse path. Must not leave a half-created slot
	// if it fails (acquire rolls running back before return).
	BeforeCreate func() error
	Now          time.Time
}

type canonicalAgentRuntimePool struct {
	mu    sync.Mutex
	slots map[string]*canonicalAgentRuntimeSlot
}

type canonicalAgentRuntimeSlot struct {
	mu             sync.Mutex
	fingerprint    string
	mode           canonicalRuntimeMode
	lastContextKey string // last ChatSessionID bound to this resident process
	running        bool
	idleSince      time.Time
	backend        agent.Backend
	close          func()
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
	contextKey := strings.TrimSpace(request.Identity.ContextKey)
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

	// lastContextKey is the last *healthy* context only (committed on release(true)).
	// Compare against it so a failed B turn does not drop the cross-chat fresh fence
	// on retry (Barry: acquire must not optimistically commit B before healthy).
	contextRotated := slot.lastContextKey != "" && slot.lastContextKey != contextKey
	resumeSessionID := strings.TrimSpace(request.CanonicalSessionID)
	if contextRotated {
		// Never resume PriorSessionID from chat A when starting chat B.
		resumeSessionID = ""
	}

	configDrift := slot.fingerprint != "" &&
		(slot.fingerprint != fingerprint || slot.mode != request.Mode)
	if configDrift || contextRotated {
		slot.closeBackend()
	}
	// Process config may update optimistically for this attempt; context key
	// identity for force-fresh stays on last healthy until release(true).
	prevFingerprint := slot.fingerprint
	prevMode := slot.mode
	slot.fingerprint = fingerprint
	slot.mode = request.Mode
	slot.running = true

	var backend agent.Backend
	var closeBackend func()
	reused := request.Mode == canonicalRuntimeResident && !configDrift && !contextRotated && slot.backend != nil
	if reused {
		backend = slot.backend
		closeBackend = slot.close
	} else {
		// Option A: startup materialize only on the create path (Barry structure gate).
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
			// Roll back optimistic process identity so a partial B does not
			// look "already on B" for later drift decisions; lastContextKey
			// was never advanced.
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
		}
	}

	wrapped := &canonicalSessionBackend{
		backend:            backend,
		canonicalSessionID: resumeSessionID,
	}
	return &canonicalAgentRuntimeLease{
		slot:              slot,
		backend:           wrapped,
		mode:              request.Mode,
		pendingContextKey: contextKey,
		turnClose:         closeBackend,
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
	slot              *canonicalAgentRuntimeSlot
	backend           agent.Backend
	mode              canonicalRuntimeMode
	pendingContextKey string // committed to slot.lastContextKey only on healthy release
	turnClose         func()
	once              sync.Once
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
		// Commit context key only after a healthy turn — failed B leaves last
		// healthy A so retry B still sees contextRotated and force-fresh.
		if healthy {
			if key := strings.TrimSpace(l.pendingContextKey); key != "" {
				l.slot.lastContextKey = key
			}
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
	slot.lastContextKey = ""
	slot.idleSince = time.Time{}
	return nil
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
