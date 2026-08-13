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
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var ErrCanonicalAgentRuntimeBusy = errors.New("canonical agent runtime busy")

type canonicalRuntimeMode string

const (
	canonicalRuntimeResident canonicalRuntimeMode = "resident"
	canonicalRuntimeOneShot  canonicalRuntimeMode = "one_shot"

	// canonicalIdleAcceptTimeout bounds how long a canonical Message handoff may
	// wait for the resident runtime to accept an idle input batch. A busy or
	// unresponsive runtime must never block the recovery/Flush path forever
	// (Raft alignment: queue + content-free notice + agent pull, not a blocking
	// hard inject). When the wait elapses or a native busy error is surfaced the
	// handoff returns ErrCanonicalAgentRuntimeBusy so the coordinator schedules a
	// pending notice and retries, keeping messages queued. Recoveries that hit a
	// still-booting process get a generous window so they are not thrashingly
	// disposed before the resident registers its control reader.
	canonicalIdleAcceptTimeout = 20 * time.Second
)

// canonicalAgentRuntimeIdentity contains only process-stable configuration.
// The logical slot is always agent×runtime. The fingerprint decides whether
// that slot's provider backend must restart; it never creates another slot.
//
// Product (Frank/Parker 2026-07-28): one long-lived resident session per
// agent×runtime across channel/DM/thread. Delivery and requester facts are
// per-message details — never fingerprint / force-fresh inputs.
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
	// never put delivery, requester, Issue, or other per-turn fields here.
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
	// PrepareLaunchEnvironment may add launch-scoped transport state after the
	// stable runtime fingerprint has been computed. Its cleanup is owned by the
	// concrete backend lifetime and runs on every create failure.
	PrepareLaunchEnvironment func(map[string]string) (func(), error)
	Now                      time.Time
	// Context bounds capacity-wait when the pool is full of running agents
	// and no idle resident can be evicted. Nil → context.Background().
	Context context.Context
}

// ResidentRuntimeRecoveredSubscriber is notified when a resident backend is
// successfully factory-created for an agent×runtime slot (not on reuse).
// Used to clear the server-side crashed_since after local recovery.
type ResidentRuntimeRecoveredSubscriber func(agentID, runtimeID string)

type canonicalAgentRuntimePool struct {
	mu                      sync.Mutex
	slots                   map[string]*canonicalAgentRuntimeSlot
	managedProcessGrants    map[string]agentProcessCapacityGrant
	pendingManagedProcesses map[string]pendingManagedProcess
	pendingManagedOrder     []string

	// maxAgentProcesses bounds distinct agents with a live resident backend
	// (backend != nil). 0 = unlimited. See #35 / resolveMaxAgentProcesses.
	maxAgentProcesses int
	// pendingAgents reserves capacity for in-flight creates so concurrent
	// acquires cannot overshoot the cap between reserve and backend attach.
	pendingAgents map[string]struct{}
	capacityCond  *sync.Cond

	// Metrics (#35): live distinct agents with backend; idle-for-cap evictions.
	liveAgentProcesses atomic.Int64
	evictForCapTotal   atomic.Int64

	crashMu          sync.Mutex
	crashSubscribers []ResidentRuntimeCrashSubscriber

	recoverMu          sync.Mutex
	recoverSubscribers []ResidentRuntimeRecoveredSubscriber

	// nextResume is the composer-applied provider session for the next
	// acquire when the caller does not pass CanonicalSessionID. An explicit
	// empty value means "start fresh" after session reset.
	nextResume map[string]string
}

type canonicalAgentRuntimeSlot struct {
	mu                             sync.Mutex
	fingerprint                    string
	mode                           canonicalRuntimeMode
	provider                       string
	running                        bool
	idleSince                      time.Time
	backend                        agent.Backend
	close                          func()
	piRunIdentity                  *agent.PiRunIdentity
	messageInputDone               <-chan error
	messageInputGeneration         uint64
	messageInputAttempt            uint64
	invalidationGeneration         uint64
	invalidateAfterInput           bool
	compacting                     bool
	lastPendingNoticeFingerprint   string
	lastPendingTargetFingerprint   map[string]string
	lastPendingNoticeCoordinatorID string
	lastPendingNoticeGeneration    uint64
}

func newCanonicalAgentRuntimePool() *canonicalAgentRuntimePool {
	p := &canonicalAgentRuntimePool{
		slots:                   make(map[string]*canonicalAgentRuntimeSlot),
		managedProcessGrants:    make(map[string]agentProcessCapacityGrant),
		pendingManagedProcesses: make(map[string]pendingManagedProcess),
		pendingAgents:           make(map[string]struct{}),
		nextResume:              make(map[string]string),
	}
	p.capacityCond = sync.NewCond(&p.mu)
	return p
}

// setMaxAgentProcesses configures the #35 live-resident-agent process ceiling.
// 0 disables the cap (unlimited). Safe to call once at daemon init.
func (p *canonicalAgentRuntimePool) setMaxAgentProcesses(n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if n < 0 {
		n = 0
	}
	p.maxAgentProcesses = n
	p.capacityCond.Broadcast()
	wakeups := p.promoteManagedProcessesLocked()
	p.mu.Unlock()
	invokeManagedProcessGrantWakeups(wakeups)
}

// LiveAgentProcessCount returns the last published distinct-agent live count.
func (p *canonicalAgentRuntimePool) LiveAgentProcessCount() int64 {
	if p == nil {
		return 0
	}
	return p.liveAgentProcesses.Load()
}

// EvictForCapTotal returns how many idle residents were closed to free cap.
func (p *canonicalAgentRuntimePool) EvictForCapTotal() int64 {
	if p == nil {
		return 0
	}
	return p.evictForCapTotal.Load()
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
	if request.CanonicalSessionID == "" {
		if next, ok := p.takeNextResumeSession(request.Identity.AgentID, request.Identity.RuntimeID); ok {
			resumeSessionID = next
		}
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	// #35: resident acquires may need a new live process for this agent.
	// Reserve capacity (and close idle other-runtime slots for rebind) before
	// locking the target slot. One-shot does not retain a pool backend and is
	// not counted toward the cap (Parker: v1 one-shot out of scope).
	reserved := false
	if request.Mode == canonicalRuntimeResident {
		if err := p.reserveAgentProcessCapacity(request.Context, request.Identity.AgentID, request.Identity.RuntimeID); err != nil {
			return nil, err
		}
		reserved = true
		defer func() {
			if reserved {
				p.releaseAgentProcessReservation(request.Identity.AgentID)
			}
		}()
	}

	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		slot = &canonicalAgentRuntimeSlot{}
		p.slots[key] = slot
	}
	slot.mu.Lock()
	p.mu.Unlock()

	// After unlock: never call pool methods that take p.mu while holding
	// slot.mu (reserve/count take p.mu then slot.mu — reverse order deadlocks).
	publishLiveAfterUnlock := false
	clearReservationAfterUnlock := false
	// closedLiveForRecreate: configDrift (or similar) closed a live backend under
	// this lock. If recreate fails we must wake capacity waiters after unlock.
	closedLiveForRecreate := false
	reservationAgentID := request.Identity.AgentID
	defer func() {
		slot.mu.Unlock()
		if clearReservationAfterUnlock {
			p.clearAgentProcessReservation(reservationAgentID)
		}
		if publishLiveAfterUnlock {
			p.publishLiveAgentProcessCount()
		} else if closedLiveForRecreate {
			// Failed to re-attach after closing live process — free the cap slot.
			p.signalAgentProcessCapacityFreed()
		}
	}()
	if slot.running {
		return nil, ErrCanonicalAgentRuntimeBusy
	}

	// Fingerprint drift (slow config / AGENTS digest) → dispose process and recreate.
	// Cross-surface chat change alone does NOT dispose or force-fresh Prior.
	configDrift := slot.fingerprint != "" &&
		(slot.fingerprint != fingerprint || slot.mode != request.Mode)
	if configDrift {
		if slot.backend != nil {
			closedLiveForRecreate = true
		}
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
		var processCleanup func()
		if request.PrepareLaunchEnvironment != nil {
			processCleanup, err = request.PrepareLaunchEnvironment(config.Env)
			if err != nil {
				if processCleanup != nil {
					processCleanup()
				}
				slot.running = false
				slot.fingerprint = prevFingerprint
				slot.mode = prevMode
				return nil, fmt.Errorf("prepare canonical runtime process: %w", err)
			}
		}
		created, closeFn, err := request.Factory(config)
		if err != nil {
			if processCleanup != nil {
				processCleanup()
			}
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
			if processCleanup != nil {
				processCleanup()
			}
			return nil, errors.New("canonical runtime backend factory returned nil backend")
		}
		backend = created
		closeBackend = combineRuntimeCleanup(closeFn, processCleanup)
		if request.Mode == canonicalRuntimeResident {
			slot.backend = created
			slot.close = closeBackend
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
	// Resident success: backend attached/reused counts as live. Drop pending
	// reservation + publish gauge only after slot.mu is released.
	if request.Mode == canonicalRuntimeResident {
		reserved = false
		clearReservationAfterUnlock = true
		publishLiveAfterUnlock = true
	}
	return &canonicalAgentRuntimeLease{
		slot:      slot,
		backend:   wrapped,
		mode:      request.Mode,
		turnClose: closeBackend,
		pool:      p,
	}, nil
}

func combineRuntimeCleanup(cleanups ...func()) func() {
	return func() {
		for _, cleanup := range cleanups {
			if cleanup != nil {
				cleanup()
			}
		}
	}
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
	pool      *canonicalAgentRuntimePool
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
		closedLive := false
		if !l.slot.running {
			l.slot.mu.Unlock()
			return
		}
		if l.mode == canonicalRuntimeOneShot {
			if l.turnClose != nil {
				l.turnClose()
			}
		} else if !healthy {
			if l.slot.backend != nil {
				closedLive = true
			}
			l.slot.closeBackend()
		}
		l.slot.running = false
		l.slot.idleSince = now
		l.slot.mu.Unlock()
		if closedLive && l.pool != nil {
			l.pool.signalAgentProcessCapacityFreed()
		}
	})
}

func (slot *canonicalAgentRuntimeSlot) closeBackend() {
	if slot.close != nil {
		slot.close()
	}
	slot.backend = nil
	slot.close = nil
	slot.piRunIdentity = nil
	slot.lastPendingNoticeFingerprint = ""
	slot.lastPendingTargetFingerprint = nil
	slot.lastPendingNoticeCoordinatorID = ""
	slot.lastPendingNoticeGeneration = 0
	slot.invalidateAfterInput = false
}

func (p *canonicalAgentRuntimePool) slotCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// hasResidentBackend reports whether the Agent×runtime slot already owns a
// usable resident provider process. It intentionally does not expose whether
// the process is currently handling a Message; MessageCoordinator retains
// that admission boundary.
func (p *canonicalAgentRuntimePool) setNextResumeSession(agentID, runtimeID, sessionID string) {
	if p == nil {
		return
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nextResume == nil {
		p.nextResume = make(map[string]string)
	}
	p.nextResume[key] = strings.TrimSpace(sessionID)
}

func (p *canonicalAgentRuntimePool) takeNextResumeSession(agentID, runtimeID string) (string, bool) {
	if p == nil {
		return "", false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nextResume == nil {
		return "", false
	}
	sessionID, ok := p.nextResume[key]
	if !ok {
		return "", false
	}
	delete(p.nextResume, key)
	return sessionID, true
}

func (p *canonicalAgentRuntimePool) hasLiveLease(agentID, runtimeID string) bool {
	if p == nil {
		return false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return false
	}
	defer slot.mu.Unlock()
	return slot.running
}

func (p *canonicalAgentRuntimePool) hasResidentBackend(agentID, runtimeID string) bool {
	if p == nil {
		return false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return false
	}
	defer slot.mu.Unlock()
	return slot.mode == canonicalRuntimeResident && slot.backend != nil
}

func (p *canonicalAgentRuntimePool) bindResidentPiRunIdentity(ctx context.Context, agentID, runtimeID string, identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	if p == nil {
		return agent.PiRunBinding{}, errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return agent.PiRunBinding{}, errors.New("canonical resident runtime is not registered")
	}
	defer slot.mu.Unlock()
	backend, ok := slot.backend.(agent.PiRPCBackend)
	if !ok {
		return agent.PiRunBinding{}, errors.New("canonical resident runtime is not Pi RPC")
	}
	binding, err := backend.PrepareRun(ctx, identity)
	if err != nil {
		return agent.PiRunBinding{}, err
	}
	copyIdentity := identity
	slot.piRunIdentity = &copyIdentity
	return binding, nil
}

func (p *canonicalAgentRuntimePool) handoffIdleMessages(
	ctx context.Context,
	agentID, runtimeID string,
	messages []protocol.AgentMessageProjection,
	onStarting func(),
	onAccepted func(),
	onMessage func(agent.Message),
	onComplete func(error, uint64, *agent.ResidentTurnCapture),
) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return errors.New("canonical resident runtime is not registered")
	}
	slot.mu.Lock()
	p.mu.Unlock()
	if slot.running {
		slot.mu.Unlock()
		return ErrCanonicalAgentRuntimeBusy
	}
	if slot.mode != canonicalRuntimeResident || slot.backend == nil {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime is unavailable")
	}
	input, ok := slot.backend.(agent.ResidentMessageInput)
	if !ok {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime does not support idle Message input")
	}
	var runtimeWasConfirmedDead bool
	if liveness, ok := slot.backend.(agent.ResidentRuntimeLivenessChecker); ok {
		alive, known := liveness.RuntimeAlive()
		runtimeWasConfirmedDead = known && !alive
	}
	batch := make([]agent.ResidentMessage, 0, len(messages))
	for _, message := range messages {
		partsJSON, err := json.Marshal(message.Parts)
		if err != nil {
			slot.mu.Unlock()
			return fmt.Errorf("marshal resident Message parts: %w", err)
		}
		batch = append(batch, agent.ResidentMessage{
			ID: message.ID, Target: message.Target, ReplyTarget: message.ReplyTarget, Seq: message.Seq, Content: message.Content, PartsJSON: partsJSON, RuntimeContext: message.RuntimeContext,
		})
	}
	// Starting native acceptance is itself an admission state. Publish it
	// before calling the provider so a lifecycle restart can observe and kill a
	// runtime stuck in initialize/thread-start instead of blocking on slot.mu.
	// This mirrors Raft's starting-process inbox plus stop-epoch fence: the
	// canonical Message is not acknowledged until native acceptance succeeds.
	slot.running = true
	slot.messageInputAttempt++
	attempt := slot.messageInputAttempt
	invalidationGeneration := slot.invalidationGeneration
	slot.mu.Unlock()

	observeRuntimeMessage := func(message agent.Message) {
		p.observeResidentRuntimeMessage(slot, message)
		if onMessage != nil {
			onMessage(message)
		}
	}
	if preparation, ok := slot.backend.(agent.ResidentMessagePreparation); ok {
		if err := preparation.PrepareMessageInput(ctx, observeRuntimeMessage); err != nil {
			if p.failResidentMessageInputAttempt(slot, attempt) {
				p.signalAgentProcessCapacityFreed()
			}
			return err
		}
		slot.mu.Lock()
		invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
		slot.mu.Unlock()
		if invalidated {
			if p.failResidentMessageInputAttempt(slot, attempt) {
				p.signalAgentProcessCapacityFreed()
			}
			return errors.New("canonical resident runtime was invalidated during Message input preparation")
		}
	}

	// Bound native idle acceptance so a busy or control-unresponsive resident
	// runtime never blocks recovery/Flush forever. On timeout or a native busy
	// signal we fall through to the busy path below (queue + pending notice + retry),
	// while the slot's running/admission state is still released so a later retry can
	// re-attempt. This is the Raft-aligned "no blocking hard inject" behavior.
	acceptCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		acceptCtx, cancel = context.WithTimeout(ctx, canonicalIdleAcceptTimeout)
		defer cancel()
	}
	acceptance, err := input.AcceptMessageBatch(acceptCtx, batch)
	slot.mu.Lock()
	if err != nil {
		slot.mu.Unlock()
		freed := p.failResidentMessageInputAttempt(slot, attempt)
		if isResidentAcceptBusyErr(err) {
			// Busy / unresponsive-to-idle is a queued-and-retry condition, not a
			// hard failure: hand it back as ErrCanonicalAgentRuntimeBusy so the
			// coordinator schedules a pending notice and keeps messages queued
			// (Raft alignment), instead of surfacing a distinct error that the
			// flush loop would treat as fatal.
			if freed {
				p.signalAgentProcessCapacityFreed()
			}
			return ErrCanonicalAgentRuntimeBusy
		}
		if freed {
			p.signalAgentProcessCapacityFreed()
		}
		return err
	}
	if acceptance.Done == nil {
		slot.mu.Unlock()
		freed := p.failResidentMessageInputAttempt(slot, attempt)
		if freed {
			p.signalAgentProcessCapacityFreed()
		}
		return errors.New("canonical resident runtime returned no Message input completion receipt")
	}
	invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
	// Native acceptance is the Context Boundary receipt. The provider may keep
	// processing that accepted input afterward, so retain pool-level admission
	// until its completion receipt resolves without delaying boundary persistence.
	slot.messageInputDone = acceptance.Done
	var generation uint64
	if !invalidated {
		slot.messageInputGeneration++
		generation = slot.messageInputGeneration
	}
	slot.mu.Unlock()
	if invalidated {
		activityDone := drainResidentActivity(acceptance.Messages, func(message agent.Message) {
			p.observeResidentRuntimeMessage(slot, message)
		})
		go p.finishResidentMessageInput(slot, acceptance.Done, activityDone, drainResidentCapture(acceptance.Capture), 0, nil)
		return errors.New("canonical resident runtime was invalidated during Message input acceptance")
	}
	if runtimeWasConfirmedDead && onStarting != nil {
		if liveness, ok := slot.backend.(agent.ResidentRuntimeLivenessChecker); ok {
			alive, known := liveness.RuntimeAlive()
			if known && alive {
				onStarting()
			}
		}
	}
	if onAccepted != nil {
		onAccepted()
	}
	activityDone := drainResidentActivity(acceptance.Messages, observeRuntimeMessage)
	go p.finishResidentMessageInput(slot, acceptance.Done, activityDone, drainResidentCapture(acceptance.Capture), generation, onComplete)
	return nil
}

func (p *canonicalAgentRuntimePool) failResidentMessageInputAttempt(slot *canonicalAgentRuntimeSlot, attempt uint64) bool {
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.messageInputAttempt != attempt {
		return false
	}
	slot.running = false
	slot.compacting = false
	slot.idleSince = time.Now()
	if !slot.invalidateAfterInput {
		return false
	}
	freed := slot.backend != nil
	slot.closeBackend()
	slot.fingerprint = ""
	slot.mode = ""
	return freed
}

func (p *canonicalAgentRuntimePool) observeResidentRuntimeMessage(slot *canonicalAgentRuntimeSlot, message agent.Message) {
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	switch message.Type {
	case agent.MessageCompactionStarted:
		slot.compacting = true
	case agent.MessageCompactionFinished, agent.MessageThinking, agent.MessageText, agent.MessageToolUse, agent.MessageError:
		slot.compacting = false
	}
}

// handoffIdleReminderInput crosses the same single resident-turn admission
// lock as canonical Messages but owns no MessageCoordinator state. Busy and
// native-acceptance failures are returned directly to the transient caller;
// nothing here queues, retries, or schedules an idle-boundary replay.
func (p *canonicalAgentRuntimePool) handoffIdleReminderInput(ctx context.Context, agentID, runtimeID string, inputValue agent.ResidentReminderInput) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return errors.New("canonical resident runtime is not registered")
	}
	slot.mu.Lock()
	p.mu.Unlock()
	if slot.running {
		slot.mu.Unlock()
		return ErrCanonicalAgentRuntimeBusy
	}
	if slot.mode != canonicalRuntimeResident || slot.backend == nil {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime is unavailable")
	}
	input, ok := slot.backend.(agent.ResidentReminderInputReceiver)
	if !ok {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime does not support transient Reminder input")
	}
	slot.running = true
	slot.messageInputAttempt++
	attempt := slot.messageInputAttempt
	invalidationGeneration := slot.invalidationGeneration
	slot.mu.Unlock()

	acceptCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		acceptCtx, cancel = context.WithTimeout(ctx, canonicalIdleAcceptTimeout)
		defer cancel()
	}
	acceptance, err := input.AcceptReminderInput(acceptCtx, inputValue)
	slot.mu.Lock()
	if err != nil {
		freed := false
		if slot.messageInputAttempt == attempt {
			slot.running = false
			slot.idleSince = time.Now()
			if slot.invalidateAfterInput {
				freed = slot.backend != nil
				slot.closeBackend()
				slot.fingerprint = ""
				slot.mode = ""
			}
		}
		slot.mu.Unlock()
		if freed {
			p.signalAgentProcessCapacityFreed()
		}
		if isResidentAcceptBusyErr(err) {
			return ErrCanonicalAgentRuntimeBusy
		}
		return err
	}
	if acceptance.Done == nil {
		if slot.messageInputAttempt == attempt {
			slot.running = false
			slot.idleSince = time.Now()
		}
		slot.mu.Unlock()
		return errors.New("canonical resident runtime returned no Reminder input completion receipt")
	}
	invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
	slot.messageInputDone = acceptance.Done
	slot.mu.Unlock()
	activityDone := drainResidentActivity(acceptance.Messages, nil)
	go p.finishResidentMessageInput(slot, acceptance.Done, activityDone, drainResidentCapture(acceptance.Capture), 0, nil)
	if invalidated {
		return errors.New("canonical resident runtime was invalidated during Reminder input acceptance")
	}
	return nil
}

func drainResidentActivity(messages <-chan agent.Message, onMessage func(agent.Message)) <-chan struct{} {
	done := make(chan struct{})
	if messages == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		for message := range messages {
			if onMessage != nil {
				onMessage(message)
			}
		}
	}()
	return done
}

func drainResidentCapture(captures <-chan agent.ResidentTurnCapture) <-chan *agent.ResidentTurnCapture {
	done := make(chan *agent.ResidentTurnCapture, 1)
	if captures == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		for capture := range captures {
			copy := capture
			done <- &copy
		}
	}()
	return done
}

// isResidentAcceptBusyErr reports whether an idle Message acceptance error
// indicates the resident runtime is busy or unresponsive to idle input and
// should therefore be treated as a queued-and-retry (ErrCanonicalAgentRuntimeBusy)
// condition rather than a hard handoff failure. It covers the bounded wait
// elapsing (context deadline) and a native busy/active-input signal. The
// native-busy match is by sentinel text to avoid coupling the daemon pool to
// provider-specific error types.
func isResidentAcceptBusyErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	// Native busy errors across resident providers share the sentinel "turn
	// busy" (e.g. "claude stream-json turn busy", "claude ACP turn busy",
	// "codex app-server turn busy", "cursor ACP turn busy", "grok ACP turn
	// busy"), sometimes wrapped with an overlap/active-input description.
	return strings.Contains(strings.ToLower(err.Error()), "turn busy")
}

func (p *canonicalAgentRuntimePool) handoffBusyNotice(ctx context.Context, agentID, runtimeID string, snapshot PendingNoticeSnapshot, commitIfCurrent PendingNoticeCommitIfCurrent) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	if commitIfCurrent == nil {
		return errors.New("Pending Notice commit callback is required")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return errors.New("canonical resident runtime is not registered")
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if !slot.running {
		return errors.New("canonical resident runtime is idle")
	}
	if slot.mode != canonicalRuntimeResident || slot.backend == nil {
		return errors.New("canonical resident runtime is unavailable")
	}
	if snapshot.Fingerprint == slot.lastPendingNoticeFingerprint &&
		snapshot.CoordinatorID == slot.lastPendingNoticeCoordinatorID &&
		snapshot.PendingGeneration == slot.lastPendingNoticeGeneration {
		if !commitIfCurrent(func() {}) {
			return errPendingNoticeGenerationChanged
		}
		return nil
	}
	if slot.compacting {
		return ErrCanonicalAgentRuntimeBusy
	}
	input, ok := slot.backend.(agent.ResidentPendingNoticeInput)
	if !ok {
		return errors.New("canonical resident runtime does not support Pending Notice input")
	}
	notice := snapshot.Notice
	notice.ChangedTargets = make([]agent.ResidentPendingTarget, 0, len(snapshot.Notice.ChangedTargets))
	for _, target := range snapshot.Notice.ChangedTargets {
		if snapshot.TargetFingerprints[target.Target] != slot.lastPendingTargetFingerprint[target.Target] {
			notice.ChangedTargets = append(notice.ChangedTargets, target)
		}
	}
	if len(notice.ChangedTargets) == 0 {
		notice.ChangedTargets = append(notice.ChangedTargets, snapshot.Notice.ChangedTargets...)
	}
	if err := input.AcceptPendingNotice(ctx, notice); err != nil {
		return err
	}
	if !commitIfCurrent(func() {
		slot.lastPendingNoticeFingerprint = snapshot.Fingerprint
		slot.lastPendingTargetFingerprint = make(map[string]string, len(snapshot.TargetFingerprints))
		for target, fingerprint := range snapshot.TargetFingerprints {
			slot.lastPendingTargetFingerprint[target] = fingerprint
		}
		slot.lastPendingNoticeCoordinatorID = snapshot.CoordinatorID
		slot.lastPendingNoticeGeneration = snapshot.PendingGeneration
	}) {
		return errPendingNoticeGenerationChanged
	}
	return nil
}

func (p *canonicalAgentRuntimePool) finishResidentMessageInput(slot *canonicalAgentRuntimeSlot, done <-chan error, activityDone <-chan struct{}, captureDone <-chan *agent.ResidentTurnCapture, generation uint64, onComplete func(error, uint64, *agent.ResidentTurnCapture)) {
	turnErr := <-done
	var capture *agent.ResidentTurnCapture
	// A failed provider may exit without closing its auxiliary Activity/Capture
	// streams. The provider completion receipt is authoritative for admission:
	// release the slot immediately on failure so Pending Messages can be retried.
	// Successful turns still drain both streams before settlement and completion.
	if turnErr == nil {
		<-activityDone
		if captureDone != nil {
			capture = <-captureDone
		}
	}
	completed := false
	freed := false
	var settleBackend agent.PiRPCBackend
	var settleIdentity *agent.PiRunIdentity
	slot.mu.Lock()
	if slot.messageInputDone == done {
		if slot.invalidateAfterInput {
			slot.messageInputDone = nil
			slot.running = false
			slot.compacting = false
			slot.idleSince = time.Now()
			freed = slot.backend != nil
			slot.closeBackend()
			slot.fingerprint = ""
			slot.mode = ""
			completed = true
		} else if slot.piRunIdentity != nil {
			if backend, ok := slot.backend.(agent.PiRPCBackend); ok {
				settleBackend = backend
				identity := *slot.piRunIdentity
				settleIdentity = &identity
			} else {
				slot.messageInputDone = nil
				slot.running = false
				slot.compacting = false
				slot.idleSince = time.Now()
				completed = true
			}
		} else {
			slot.messageInputDone = nil
			slot.running = false
			slot.compacting = false
			slot.idleSince = time.Now()
			completed = true
		}
	}
	slot.mu.Unlock()

	if settleBackend != nil && settleIdentity != nil {
		if err := settleBackend.SettleRunTurn(*settleIdentity); err != nil && turnErr == nil {
			turnErr = err
		}
		slot.mu.Lock()
		if slot.messageInputDone == done {
			slot.messageInputDone = nil
			slot.running = false
			slot.compacting = false
			slot.idleSince = time.Now()
			if slot.invalidateAfterInput {
				freed = slot.backend != nil
				slot.closeBackend()
				slot.fingerprint = ""
				slot.mode = ""
			}
			completed = true
		}
		slot.mu.Unlock()
	}
	if freed {
		p.signalAgentProcessCapacityFreed()
	}
	if completed && onComplete != nil {
		onComplete(turnErr, generation, capture)
	}
}

// publishIfMessageTurnStillIdle serializes a terminal Activity observation
// with admission of the next Message turn. If another delivery already began,
// its Working Activity remains authoritative and this stale terminal state is
// suppressed.
func (p *canonicalAgentRuntimePool) publishIfMessageTurnStillIdle(agentID, runtimeID string, generation uint64, publish func()) bool {
	if p == nil || publish == nil {
		return false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return false
	}
	defer slot.mu.Unlock()
	if slot.running || slot.messageInputDone != nil || slot.messageInputGeneration != generation {
		return false
	}
	publish()
	return true
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
	freed := false
	defer func() {
		slot.mu.Unlock()
		if freed {
			p.signalAgentProcessCapacityFreed()
		}
	}()
	if slot.running {
		return ErrCanonicalAgentRuntimeBusy
	}
	if slot.backend != nil {
		freed = true
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
	// Fence any native acceptance that races this restart. The in-flight owner
	// keeps admission until the killed process actually finishes, then closes
	// and detaches this backend so the next handoff creates a fresh instance.
	slot.invalidationGeneration++
	slot.invalidateAfterInput = true
	return killable.ForceKill()
}

// revokeResidentPiRunIdentity retires only the requested run binding. A stale
// rollback can therefore never kill a newer run that reused the same
// Agent×runtime slot. Busy native input is force-killed and fenced for detach
// by finishResidentMessageInput; idle prepared processes are closed now.
func (p *canonicalAgentRuntimePool) revokeResidentPiRunIdentity(agentID, runtimeID string, identity agent.PiRunIdentity) error {
	if p == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" || runtimeID == "" || strings.TrimSpace(identity.RunID) == "" || strings.TrimSpace(identity.RunAgentID) == "" {
		return errors.New("canonical Pi run revocation identity is incomplete")
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
	if slot.piRunIdentity == nil {
		return nil
	}
	if *slot.piRunIdentity != identity {
		return errors.New("canonical Pi run revocation identity mismatch")
	}
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
	slot.invalidationGeneration++
	slot.invalidateAfterInput = true
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
	if removed > 0 {
		p.publishLiveAgentProcessCountLocked()
		p.capacityCond.Broadcast()
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

// forceTerminateAll interrupts only processes owned by this pool. A backend
// without the explicit concurrent ForceKill contract is deliberately left
// alone and makes the caller fail closed rather than guessing how to kill it.
func (p *canonicalAgentRuntimePool) forceTerminateAll() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	slots := make([]*canonicalAgentRuntimeSlot, 0, len(p.slots))
	for _, slot := range p.slots {
		slots = append(slots, slot)
	}
	p.mu.Unlock()
	for _, slot := range slots {
		slot.mu.Lock()
		backend := slot.backend
		running := slot.running
		slot.mu.Unlock()
		if !running || backend == nil {
			continue
		}
		killable, ok := backend.(agent.ResidentRuntimeForceKillable)
		if !ok {
			return ErrCanonicalAgentRuntimeBusy
		}
		if err := killable.ForceKill(); err != nil {
			return err
		}
	}
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
			case "kiro":
				return newCanonicalKiroResidentBackend(config)
			case "codex":
				return newCanonicalCodexResidentBackend(config)
			case "claude":
				return newCanonicalClaudeResidentBackend(config)
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
