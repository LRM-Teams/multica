package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var ErrCanonicalAgentRuntimeBusy = errors.New("canonical agent runtime busy")

const (
	// canonicalIdleAcceptTimeout bounds how long canonical Message delivery may
	// wait for the resident runtime to accept an idle input batch. A busy or
	// unresponsive runtime must never block the recovery/Flush path forever
	// (Raft alignment: queue + content-free notice + agent pull, not a blocking
	// hard inject). When the wait elapses or a native busy error is surfaced the
	// delivery returns ErrCanonicalAgentRuntimeBusy so the coordinator schedules a
	// pending notice and retries, keeping messages queued. Recoveries that hit a
	// still-booting process get a generous window so they are not thrashingly
	// disposed before the resident registers its control reader.
	canonicalIdleAcceptTimeout = 20 * time.Second

	// defaultResidentTurnCompletionTimeout is an absolute deadline from native
	// Message acceptance through provider Done, Activity/Capture drain, and Pi
	// settlement. Progress and tool calls do not extend it: those signals protect
	// the silence watchdog, but cannot leave canonical admission owned forever.
	defaultResidentTurnCompletionTimeout = 15 * time.Minute
	// residentTurnForceKillGrace bounds a misbehaving ForceKill implementation.
	// The old backend is generation-fenced before this wait and detached after it.
	residentTurnForceKillGrace = 5 * time.Second
)

// canonicalAgentRuntimeIdentity locates the agent×runtime slot and carries
// the facts needed to start a process. It is not a recycle key: a live
// resident is reused as-is. Model, MCP, AGENTS, and other create-time
// bake-ins take effect on the next explicit restart/reset or after the
// process dies — never by hashing the next acquire against the slot.
//
// Product (Frank/Parker 2026-07-28): one long-lived resident session per
// agent×runtime across channel/DM/thread. Delivery and requester facts are
// per-message details — never recycle / force-fresh inputs.
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

func requireCanonicalResidentProvider(provider, executionProfile string) error {
	if executionProfile != executionProfileFull {
		return fmt.Errorf("execution profile %q must not use the canonical agent session", executionProfile)
	}
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return errors.New("provider is required")
	}
	if !isCanonicalResidentProvider(trimmed) {
		return fmt.Errorf("provider %q has no resident adapter", trimmed)
	}
	return nil
}

type canonicalRuntimeBackendFactory func(agent.Config) (agent.Backend, func(), error)

type agentRuntimeAcquireRequest struct {
	Identity           canonicalAgentRuntimeIdentity
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
	// ForceFreshSession discards CanonicalSessionID and any queued
	// nextResume pointer so this acquire cannot continue a poisoned Pi
	// conversation. Period Brief collect/synth/retry set this on claim.
	ForceFreshSession bool
}

type agentRuntimePool struct {
	mu    sync.Mutex
	slots map[string]*agentRuntimeSlot

	// residentProcessMu guards residentProcessSubscribers and serializes
	// delivery through emitResidentProcessEvent (resident_process_event.go).
	residentProcessMu          sync.Mutex
	residentProcessSubscribers []func(residentProcessEvent)

	// nextResume is the composer-applied provider session for the next
	// acquire when the caller does not pass CanonicalSessionID. An explicit
	// empty value means "start fresh" after session reset.
	nextResume map[string]string
	// residentStallWatchdog is the silence budget for an accepted resident
	// Message turn. Zero disables the recovery watchdog (used by tests and
	// operators that opt out).
	residentStallWatchdog time.Duration
	// residentTurnCompletionTimeout is the non-renewable wall-clock budget for
	// the complete accepted-turn settlement chain. It is separate from silence:
	// provider progress may refresh the watchdog but never this deadline.
	residentTurnCompletionTimeout time.Duration
}

type agentRuntimeSlot struct {
	mu                             sync.Mutex
	provider                       string
	agentInstanceID                string
	processInstanceID              string
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
	lastAppInboxNoticeFingerprint  string
	activeDirectedRunID            string
	activeDirectedTurnID           string
	// lastRuntimeActivityAt is the slot-level silence clock. Unlike the
	// per-turn watchdog stamp it survives across turns, so a delivery that
	// never reaches native acceptance can still tell how long this resident
	// runtime has been silent. Stamped at process create and on every
	// observed provider Message; zero means "unknown", which never recovers.
	lastRuntimeActivityAt time.Time
	// outstandingToolCalls tracks tool calls the current turn started but has
	// not reported a result for. Recent activity protects a legitimately long
	// tool; calls left after turn completion or the stall window are stale.
	outstandingToolCalls map[string]struct{}
	// stalledRecovering is Raft's alreadyRecovering fence. The server
	// redelivers roughly every 20s, so without it each redelivery would fire
	// another kill while the first teardown is still in flight.
	stalledRecovering bool
	// replacementBlocked keeps an unconfirmed timed-out process from being
	// replaced in parallel. It is cleared only when the bounded teardown worker
	// eventually confirms cleanup returned (or on a normal confirmed close).
	replacementBlocked bool
	// terminated signals that this slot's resident process is confirmed gone
	// AND slot.running has been released — the single completion fact
	// awaitTerminated waits on instead of polling hasRunningTurn/
	// residentProcessAlive separately. It is created fresh whenever a new
	// backend is attached (see acquire()'s create-only branch) and closed by
	// closeBackend(), the one place both halves of that fact are always
	// settled together before slot.mu is released (see closeBackend's
	// comment for why). A nil channel means this slot has never had a
	// backend, so there is nothing to await.
	terminated chan struct{}
}

func newAgentRuntimePool() *agentRuntimePool {
	return &agentRuntimePool{
		slots:                         make(map[string]*agentRuntimeSlot),
		nextResume:                    make(map[string]string),
		residentTurnCompletionTimeout: defaultResidentTurnCompletionTimeout,
	}
}

func (p *agentRuntimePool) setResidentStallWatchdog(window time.Duration) {
	if p == nil {
		return
	}
	p.residentStallWatchdog = window
}

func (p *agentRuntimePool) setResidentTurnCompletionTimeout(timeout time.Duration) {
	if p == nil {
		return
	}
	p.residentTurnCompletionTimeout = timeout
}

func (p *agentRuntimePool) acquire(request agentRuntimeAcquireRequest) (*agentRuntimeLease, error) {
	if p == nil {
		return nil, errors.New("canonical agent runtime pool is nil")
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
	resumeSessionID := strings.TrimSpace(request.CanonicalSessionID)
	if request.ForceFreshSession {
		// Drain a queued composer resume so it cannot leak onto the next
		// non-fresh acquire after this one-shot wake.
		_, _ = p.takeNextResumeSession(request.Identity.AgentID, request.Identity.RuntimeID)
		resumeSessionID = ""
	} else if request.CanonicalSessionID == "" {
		if next, ok := p.takeNextResumeSession(request.Identity.AgentID, request.Identity.RuntimeID); ok {
			resumeSessionID = next
		}
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		slot = &agentRuntimeSlot{}
		p.slots[key] = slot
	}
	slot.mu.Lock()
	p.mu.Unlock()

	defer slot.mu.Unlock()
	if slot.running {
		return nil, ErrCanonicalAgentRuntimeBusy
	}
	if slot.replacementBlocked {
		return nil, errors.New("canonical resident runtime termination is unconfirmed")
	}

	// Live process → reuse. No hash, no implicit stop+start.
	// Model/MCP/AGENTS baked at start stay until explicit restart/reset
	// or the process dies.
	reused := slot.backend != nil
	slot.running = true

	var backend agent.Backend
	var closeBackend func()
	if reused {
		backend = slot.backend
		closeBackend = slot.close
	} else {
		// Create-only: AGENTS (and any future startup disk) only on process create.
		if request.BeforeCreate != nil {
			if err := request.BeforeCreate(); err != nil {
				slot.running = false
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
				return nil, fmt.Errorf("prepare canonical runtime process: %w", err)
			}
		}
		created, closeFn, err := request.Factory(config)
		if err != nil {
			if processCleanup != nil {
				processCleanup()
			}
			slot.running = false
			return nil, fmt.Errorf("create canonical runtime backend: %w", err)
		}
		if created == nil {
			slot.running = false
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
		slot.backend = created
		slot.close = closeBackend
		slot.provider = request.Identity.Provider
		slot.agentInstanceID = ""
		slot.processInstanceID = ""
		// Providers whose stream traffic maps to no Message (Pi's empty or
		// unknown update events) still prove liveness. Refresh the silence
		// clock on every stream event so a long generation with no tool calls
		// is not mistaken for a wedged runtime.
		if listener, ok := created.(agent.ResidentProgressListener); ok {
			listener.SetProgressListener(slot.stampRuntimeActivity)
		}
		// Re-arm the termination signal for this new process. A stale closed
		// channel from a previous backend on this same slot must never make
		// the next awaitTerminated return instantly.
		slot.terminated = make(chan struct{})
		// Stamp the silence clock at spawn. A zero value would make the very
		// first deferred delivery treat a still-booting process as stalled.
		slot.lastRuntimeActivityAt = now
		slot.outstandingToolCalls = nil
		slot.stalledRecovering = false
		// New resident process is up — clear any server-side "crashed"
		// fact from a prior idle death. First-ever create is a no-op clear.
		// Fire async: we still hold slot.mu here and subscribers may do I/O.
		agentID, runtimeID := request.Identity.AgentID, request.Identity.RuntimeID
		go p.emitResidentProcessEvent(residentProcessEvent{
			AgentID: agentID, RuntimeID: runtimeID, Kind: residentProcessRecovered, At: now,
		})
	}

	wrapped := &canonicalSessionBackend{
		backend:            backend,
		canonicalSessionID: resumeSessionID,
	}
	return &agentRuntimeLease{
		slot:      slot,
		backend:   wrapped,
		turnClose: closeBackend,
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

type agentRuntimeLease struct {
	slot      *agentRuntimeSlot
	backend   agent.Backend
	turnClose func()
	once      sync.Once
}

func (l *agentRuntimeLease) release(healthy bool) {
	l.releaseAt(healthy, time.Now())
}

func (l *agentRuntimeLease) releaseForResult(status string, executionErr error) {
	l.release(canonicalRuntimeResultHealthy(status, executionErr))
}

func canonicalRuntimeResultHealthy(status string, executionErr error) bool {
	return executionErr == nil && status == "completed"
}

func (l *agentRuntimeLease) releaseAt(healthy bool, now time.Time) {
	if l == nil || l.slot == nil {
		return
	}
	l.once.Do(func() {
		l.slot.mu.Lock()
		if !l.slot.running {
			l.slot.mu.Unlock()
			return
		}
		if !healthy {
			l.slot.closeBackend()
		}
		l.slot.running = false
		l.slot.idleSince = now
		l.slot.mu.Unlock()
	})
}

// silentFor reports how long this resident runtime has produced no provider
// activity, reading the single lastRuntimeActivityAt clock. ok is false when
// the slot has no activity stamp yet (no process, or one that has not been
// stamped), which never counts as stalled. This is the sole staleness
// accessor: both startResidentStallWatchdog (resident_stall_watch.go) and
// recoverStalledSlotForQueuedMessage (resident_stall_queued_recovery.go)
// read the same clock through this method rather than tracking their own.
func (slot *agentRuntimeSlot) silentFor(now time.Time) (time.Duration, bool) {
	if slot == nil {
		return 0, false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.silentForLocked(now)
}

// stampRuntimeActivity refreshes the silence clock from the backend's stream
// reader goroutine (ResidentProgressListener). It deliberately touches only
// lastRuntimeActivityAt: progress evidence must never fabricate a Message or
// an Activity observation.
func (slot *agentRuntimeSlot) stampRuntimeActivity() {
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.lastRuntimeActivityAt = time.Now()
	slot.mu.Unlock()
}

// silentForLocked is silentFor for callers that already hold slot.mu.
func (slot *agentRuntimeSlot) silentForLocked(now time.Time) (time.Duration, bool) {
	if slot.lastRuntimeActivityAt.IsZero() {
		return 0, false
	}
	return now.Sub(slot.lastRuntimeActivityAt), true
}

func (slot *agentRuntimeSlot) closeBackend() {
	if slot.close != nil {
		slot.close()
	}
	slot.clearBackendStateLocked()
}

// clearBackendStateLocked detaches the current backend identity and settles
// the slot termination signal. Most callers use closeBackend, which runs the
// provider cleanup first. The accepted-turn timeout path calls this directly
// only after its bounded ForceKill attempt, then runs cleanup asynchronously:
// a cleanup that waits forever must not reacquire canonical admission forever.
// slot.mu must be held.
func (slot *agentRuntimeSlot) clearBackendStateLocked() {
	slot.detachBackendStateLocked(true)
}

// detachBackendStateLocked clears backend ownership while preserving whether
// replacement is safe. confirmed=false deliberately leaves terminated open
// and blocks acquire until confirmDetachedBackendTermination observes cleanup.
// slot.mu must be held.
func (slot *agentRuntimeSlot) detachBackendStateLocked(confirmed bool) {
	slot.backend = nil
	slot.close = nil
	slot.agentInstanceID = ""
	slot.processInstanceID = ""
	slot.piRunIdentity = nil
	slot.lastPendingNoticeFingerprint = ""
	slot.lastPendingTargetFingerprint = nil
	slot.lastPendingNoticeCoordinatorID = ""
	slot.lastPendingNoticeGeneration = 0
	slot.lastAppInboxNoticeFingerprint = ""
	slot.invalidateAfterInput = false
	slot.lastRuntimeActivityAt = time.Time{}
	slot.outstandingToolCalls = nil
	slot.stalledRecovering = false
	slot.replacementBlocked = !confirmed
	// Every closeBackend() caller either already has slot.running false or
	// sets it false in the same slot.mu critical section as this call (see
	// releaseAt's unhealthy branch, failResidentMessageInputAttempt,
	// finishResidentMessageInput), so by the time slot.mu is released both
	// halves of the terminated fact — process gone, running released — are
	// always settled together; no waiter can observe them out of order. Every
	// call site is a real process teardown (an idle-path close, a
	// force-killed turn's own detach, idle eviction, or a confirmed-dead
	// liveness sweep), so this is the single correct place to close the
	// signal. Guard against a double close: closeBackend() is idempotent by
	// design (e.g. beginResidentTermination's idle path can run again
	// against an already-closed slot).
	if confirmed && slot.terminated != nil {
		select {
		case <-slot.terminated:
		default:
			close(slot.terminated)
		}
	}
}

func (slot *agentRuntimeSlot) confirmDetachedBackendTermination() {
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.backend != nil || !slot.replacementBlocked {
		return
	}
	slot.replacementBlocked = false
	if slot.terminated != nil {
		select {
		case <-slot.terminated:
		default:
			close(slot.terminated)
		}
	}
}

func (p *agentRuntimePool) bindManagedProcess(agentID, runtimeID string, callback agentProcessCallback) bool {
	if p == nil || callback.AgentInstanceID == "" || callback.ProcessInstanceID == "" {
		return false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return false
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if slot.backend == nil {
		return false
	}
	slot.agentInstanceID = callback.AgentInstanceID
	slot.processInstanceID = callback.ProcessInstanceID
	return true
}

func (p *agentRuntimePool) managedProcessCallback(agentID, runtimeID string) (agentProcessCallback, bool) {
	if p == nil {
		return agentProcessCallback{}, false
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return agentProcessCallback{}, false
	}
	slot.mu.Lock()
	p.mu.Unlock()
	defer slot.mu.Unlock()
	if slot.agentInstanceID == "" || slot.processInstanceID == "" {
		return agentProcessCallback{}, false
	}
	return agentProcessCallback{
		AgentID: agentID, AgentInstanceID: slot.agentInstanceID, ProcessInstanceID: slot.processInstanceID,
	}, true
}

// awaitTerminated blocks until this slot's resident process is confirmed
// gone and slot.running has been released (see the terminated field), or
// until ctx is done. A slot that has never had a backend has nothing to
// await and returns immediately.
func (slot *agentRuntimeSlot) awaitTerminated(ctx context.Context) error {
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	ch := slot.terminated
	slot.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *agentRuntimePool) slotCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

func (p *agentRuntimePool) setNextResumeSession(agentID, runtimeID, sessionID string) {
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

func (p *agentRuntimePool) takeNextResumeSession(agentID, runtimeID string) (string, bool) {
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

func (p *agentRuntimePool) runtimeStats(ctx context.Context, agentID, runtimeID string) (*agent.RuntimeTokenStats, error) {
	if p == nil {
		return nil, errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	p.mu.Unlock()
	if slot == nil {
		return nil, errors.New("canonical resident runtime is unavailable")
	}
	slot.mu.Lock()
	backend := slot.backend
	slot.mu.Unlock()
	statsProvider, ok := backend.(interface {
		RuntimeStats(context.Context) (*agent.RuntimeTokenStats, error)
	})
	if !ok {
		return nil, errors.New("canonical resident runtime does not expose token stats")
	}
	return statsProvider.RuntimeStats(ctx)
}

func (p *agentRuntimePool) bindDirectedTurn(agentID, runtimeID, runID, turnID string) error {
	if p == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(turnID) == "" {
		return errors.New("directed turn identity is required")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return errors.New("canonical resident runtime is not registered")
	}
	defer slot.mu.Unlock()
	if !slot.running || slot.backend == nil {
		return errors.New("canonical resident runtime is idle")
	}
	slot.activeDirectedRunID = strings.TrimSpace(runID)
	slot.activeDirectedTurnID = strings.TrimSpace(turnID)
	return nil
}

func (p *agentRuntimePool) deliverDirectedMessage(ctx context.Context, agentID, runtimeID string, message agent.ResidentDirectedMessage) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return errors.New("canonical resident runtime is not registered")
	}
	defer slot.mu.Unlock()
	if !slot.running || slot.backend == nil {
		return errors.New("canonical resident runtime is idle")
	}
	if slot.activeDirectedRunID != message.RunID || slot.activeDirectedTurnID != message.TurnID {
		return errors.New("directed Message does not match the active run and turn")
	}
	input, ok := slot.backend.(agent.ResidentDirectedMessageInput)
	if !ok {
		return errors.New("canonical resident runtime does not support directed Message steering")
	}
	return input.AcceptDirectedMessage(ctx, message)
}

func (p *agentRuntimePool) clearDirectedTurn(agentID, runtimeID, runID, turnID string) {
	if p == nil {
		return
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return
	}
	defer slot.mu.Unlock()
	if slot.activeDirectedRunID == runID && slot.activeDirectedTurnID == turnID {
		slot.activeDirectedRunID = ""
		slot.activeDirectedTurnID = ""
	}
}

func (p *agentRuntimePool) deliverActiveDirectedProjection(ctx context.Context, agentID, runtimeID string, projection protocol.AgentMessageProjection) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return errors.New("canonical resident runtime is not registered")
	}
	runID, turnID := slot.activeDirectedRunID, slot.activeDirectedTurnID
	running := slot.running && slot.backend != nil
	slot.mu.Unlock()
	if !running || runID == "" || turnID == "" {
		return errors.New("canonical resident runtime has no active directed turn")
	}
	parts, _ := json.Marshal(projection.Parts)
	boundedContext, _ := json.Marshal(map[string]any{
		"channel_id":   projection.ChannelID,
		"reply_target": projection.ReplyTarget,
		"seq":          projection.Seq,
	})
	actorName := strings.TrimSpace(projection.InitiatorName)
	if actorName == "" {
		actorName = strings.TrimSpace(projection.InitiatorID)
	}
	if actorName == "" {
		actorName = strings.TrimSpace(projection.InitiatorType)
	}
	return p.deliverDirectedMessage(ctx, agentID, runtimeID, agent.ResidentDirectedMessage{
		RunID: runID, TurnID: turnID, MessageID: projection.ID, Target: projection.Target,
		ActorType: projection.InitiatorType, ActorID: projection.InitiatorID, ActorName: actorName,
		Content: projection.Content, Parts: parts, BoundedContext: boundedContext,
	})
}

func (p *agentRuntimePool) hasRunningTurn(agentID, runtimeID string) bool {
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

// agentHasLiveRuntime reports whether any Runtime slot for agentID still owns
// an active lease or a live resident provider process.
func (p *agentRuntimePool) agentHasLiveRuntime(agentID string) bool {
	if p == nil {
		return false
	}
	agentID = strings.TrimSpace(agentID)
	p.mu.Lock()
	slots := make([]*agentRuntimeSlot, 0)
	for key, slot := range p.slots {
		candidate, _ := splitCanonicalSlotKey(key)
		if candidate == agentID {
			slots = append(slots, slot)
		}
	}
	p.mu.Unlock()
	for _, slot := range slots {
		slot.mu.Lock()
		running := slot.running
		backend := slot.backend
		slot.mu.Unlock()
		if running {
			return true
		}
		checker, ok := backend.(agent.ResidentRuntimeLivenessChecker)
		if !ok {
			continue
		}
		if alive, known := checker.RuntimeAlive(); known && alive {
			return true
		}
	}
	return false
}

func (p *agentRuntimePool) ensureResidentProcess(ctx context.Context, agentID, runtimeID string) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil || slot.backend == nil {
		if slot != nil {
			slot.mu.Unlock()
		}
		return errors.New("canonical resident runtime is not registered")
	}
	backend := slot.backend
	if checker, ok := backend.(agent.ResidentRuntimeLivenessChecker); ok {
		if alive, known := checker.RuntimeAlive(); known && alive {
			slot.mu.Unlock()
			return nil
		}
	}
	starter, ok := backend.(agent.ResidentRuntimeStarter)
	slot.mu.Unlock()
	if !ok {
		return errors.New("canonical resident runtime cannot start a provider process")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return starter.EnsureResidentProcess(ctx)
}

func (p *agentRuntimePool) residentProviderSession(agentID, runtimeID string) string {
	if p == nil {
		return ""
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return ""
	}
	backend := slot.backend
	slot.mu.Unlock()
	session, ok := backend.(agent.ResidentRuntimeSession)
	if !ok {
		return ""
	}
	return strings.TrimSpace(session.ProviderSessionID())
}

// activityContext returns only stable, user-safe association labels for a
// resident warning. Run identity is present only while a run is actively
// bound; no synthetic reference is created for idle diagnostics.
func (p *agentRuntimePool) activityContext(agentID, runtimeID string) (provider, runID string) {
	if p == nil {
		return "", ""
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return "", ""
	}
	defer slot.mu.Unlock()
	provider = strings.TrimSpace(slot.provider)
	if slot.piRunIdentity != nil {
		runID = strings.TrimSpace(slot.piRunIdentity.RunID)
	}
	return provider, runID
}

func (p *agentRuntimePool) hasResidentBackend(agentID, runtimeID string) bool {
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
	return slot.backend != nil
}

func (p *agentRuntimePool) residentProcessAlive(agentID, runtimeID string) bool {
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
	if slot == nil || slot.backend == nil {
		if slot != nil {
			slot.mu.Unlock()
		}
		return false
	}
	backend := slot.backend
	slot.mu.Unlock()
	checker, ok := backend.(agent.ResidentRuntimeLivenessChecker)
	if !ok {
		return false
	}
	alive, known := checker.RuntimeAlive()
	return known && alive
}

func (p *agentRuntimePool) bindResidentPiRunIdentity(ctx context.Context, agentID, runtimeID string, identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
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

func (p *agentRuntimePool) deliverIdleMessages(
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
	if slot.backend == nil {
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

	compactedThisInput := false
	observeRuntimeMessage := func(message agent.Message) {
		if message.Type == agent.MessageCompactionStarted {
			compactedThisInput = true
		}
		p.observeResidentRuntimeMessage(slot, message)
		if onMessage != nil {
			onMessage(message)
		}
	}
	if preparation, ok := slot.backend.(agent.ResidentMessagePreparation); ok {
		if err := preparation.PrepareMessageInput(ctx, observeRuntimeMessage); err != nil {
			p.failResidentMessageInputAttempt(slot, attempt)
			return err
		}
		slot.mu.Lock()
		invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
		slot.mu.Unlock()
		if invalidated {
			p.failResidentMessageInputAttempt(slot, attempt)
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
		p.failResidentMessageInputAttempt(slot, attempt)
		if isResidentAcceptBusyErr(err) {
			// Busy / unresponsive-to-idle is a queued-and-retry condition, not a
			// hard failure: hand it back as ErrCanonicalAgentRuntimeBusy so the
			// coordinator schedules a pending notice and keeps messages queued
			// (Raft alignment), instead of surfacing a distinct error that the
			// flush loop would treat as fatal.
			return ErrCanonicalAgentRuntimeBusy
		}
		return err
	}
	if acceptance.Done == nil {
		slot.mu.Unlock()
		p.failResidentMessageInputAttempt(slot, attempt)
		return errors.New("canonical resident runtime returned no Message input completion receipt")
	}
	invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
	// Native acceptance is the Context Boundary receipt. The provider may keep
	// processing that accepted input afterward, so retain pool-level admission
	// until its completion receipt resolves without delaying boundary persistence.
	slot.messageInputDone = acceptance.Done
	acceptedAt := time.Now()
	slot.lastRuntimeActivityAt = acceptedAt
	completionDeadline := p.residentTurnDeadline(acceptedAt)
	slot.messageInputGeneration++
	generation := slot.messageInputGeneration
	slot.mu.Unlock()
	if invalidated {
		drains := startResidentTurnDrains(acceptance.Messages, acceptance.Capture, func(message agent.Message) {
			p.observeResidentRuntimeMessageIfOwned(slot, acceptance.Done, generation, message)
		}, completionDeadline)
		go p.finishResidentMessageInput(slot, acceptance.Done, drains, 0, nil)
		return errors.New("canonical resident runtime was invalidated during Message input acceptance")
	}
	acceptedObserveRuntimeMessage := func(message agent.Message) {
		if !p.observeResidentRuntimeMessageIfOwned(slot, acceptance.Done, generation, message) {
			return
		}
		if onMessage != nil && slot.ownsResidentMessageInput(acceptance.Done, generation) {
			onMessage(message)
		}
	}
	if runtimeWasConfirmedDead && onStarting != nil {
		if liveness, ok := slot.backend.(agent.ResidentRuntimeLivenessChecker); ok {
			alive, known := liveness.RuntimeAlive()
			if known && alive {
				onStarting()
			}
		}
	}
	if !compactedThisInput {
		// Arm completion before synchronous acceptance reporting: Activity
		// transport is best effort and must not retain canonical admission. The
		// gate preserves the public ordering guarantee that Message accepted is
		// published before any buffered provider Activity.
		activityStart := make(chan struct{})
		gatedObserveRuntimeMessage := func(message agent.Message) {
			if waitForResidentActivityStart(activityStart, completionDeadline) {
				acceptedObserveRuntimeMessage(message)
			}
		}
		drains := startResidentTurnDrains(acceptance.Messages, acceptance.Capture, gatedObserveRuntimeMessage, completionDeadline)
		turnDone := make(chan struct{})
		go p.finishResidentMessageInput(slot, acceptance.Done, drains, generation, func(turnErr error, gen uint64, capture *agent.ResidentTurnCapture) {
			close(turnDone)
			if onComplete != nil {
				onComplete(turnErr, gen, capture)
			}
		})
		p.startResidentStallWatchdog(agentID, runtimeID, slot, turnDone)
		if onAccepted != nil {
			onAccepted()
		}
		close(activityStart)
		return nil
	}
	// Raft: compaction does not cover inbox. After a prepare-time compact,
	// wait for the follow-up turn to do real work before the Context Boundary
	// receipt. An empty/compaction-only turn stays uncommitted so the Message
	// can be retried.
	drains := startResidentTurnDrains(acceptance.Messages, acceptance.Capture, acceptedObserveRuntimeMessage, completionDeadline)
	finished := make(chan error, 1)
	turnDone := make(chan struct{})
	go p.finishResidentMessageInput(slot, acceptance.Done, drains, generation, func(turnErr error, gen uint64, capture *agent.ResidentTurnCapture) {
		close(turnDone)
		finished <- turnErr
		if onComplete != nil {
			onComplete(turnErr, gen, capture)
		}
	})
	p.startResidentStallWatchdog(agentID, runtimeID, slot, turnDone)
	turnErr := <-finished
	if turnErr != nil {
		return turnErr
	}
	if onAccepted != nil {
		onAccepted()
	}
	return nil
}

func waitForResidentActivityStart(start <-chan struct{}, deadline time.Time) bool {
	if start == nil {
		return true
	}
	if deadline.IsZero() {
		<-start
		return true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-start:
		return time.Now().Before(deadline)
	case <-timer.C:
		return false
	}
}

func (slot *agentRuntimeSlot) ownsResidentMessageInput(done <-chan error, generation uint64) bool {
	if slot == nil || generation == 0 {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.running && slot.messageInputDone == done && slot.messageInputGeneration == generation
}

func (p *agentRuntimePool) failResidentMessageInputAttempt(slot *agentRuntimeSlot, attempt uint64) bool {
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

	return freed
}

func (p *agentRuntimePool) observeResidentRuntimeMessage(slot *agentRuntimeSlot, message agent.Message) {
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	observeResidentRuntimeMessageLocked(slot, message)
}

func (p *agentRuntimePool) observeResidentRuntimeMessageIfOwned(slot *agentRuntimeSlot, done <-chan error, generation uint64, message agent.Message) bool {
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.running || slot.messageInputDone != done || generation != 0 && slot.messageInputGeneration != generation {
		return false
	}
	observeResidentRuntimeMessageLocked(slot, message)
	return true
}

func observeResidentRuntimeMessageLocked(slot *agentRuntimeSlot, message agent.Message) {
	slot.lastRuntimeActivityAt = time.Now()
	switch message.Type {
	case agent.MessageCompactionStarted:
		slot.compacting = true
	case agent.MessageToolUse:
		slot.compacting = false
		if callID := strings.TrimSpace(message.CallID); callID != "" {
			if slot.outstandingToolCalls == nil {
				slot.outstandingToolCalls = make(map[string]struct{}, 1)
			}
			slot.outstandingToolCalls[callID] = struct{}{}
		}
	case agent.MessageToolResult:
		if callID := strings.TrimSpace(message.CallID); callID != "" {
			delete(slot.outstandingToolCalls, callID)
		}
	case agent.MessageCompactionFinished, agent.MessageThinking, agent.MessageText, agent.MessageError:
		slot.compacting = false
	}
}

// deliverAppInboxNotice crosses the resident input boundary without carrying
// any App item body. The durable App Inbox owns retry and consumption; this
// method owns only per-process notice suppression.
func (p *agentRuntimePool) deliverAppInboxNotice(ctx context.Context, agentID, runtimeID string, notice agent.ResidentPendingNotice, fingerprint string) error {
	if p == nil {
		return errors.New("canonical agent runtime pool is nil")
	}
	if strings.TrimSpace(fingerprint) == "" || notice.PendingAppItems < 1 {
		return errors.New("App Inbox notice identity and positive item count are required")
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
	if slot.backend == nil {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime is unavailable")
	}
	if slot.lastAppInboxNoticeFingerprint == fingerprint {
		slot.mu.Unlock()
		return nil
	}
	if slot.running {
		if slot.compacting {
			slot.mu.Unlock()
			return ErrCanonicalAgentRuntimeBusy
		}
		input, ok := slot.backend.(agent.ResidentPendingNoticeInput)
		if !ok {
			slot.mu.Unlock()
			return errors.New("canonical resident runtime does not support busy Inbox Notice input")
		}
		if err := input.AcceptPendingNotice(ctx, notice); err != nil {
			slot.mu.Unlock()
			return err
		}
		slot.lastAppInboxNoticeFingerprint = fingerprint
		slot.mu.Unlock()
		return nil
	}
	input, ok := slot.backend.(agent.ResidentIdleInboxNoticeInput)
	if !ok {
		slot.mu.Unlock()
		return errors.New("canonical resident runtime does not support idle Inbox Notice input")
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
	acceptance, err := input.AcceptIdleInboxNotice(acceptCtx, notice)
	slot.mu.Lock()
	if err != nil {
		if slot.messageInputAttempt == attempt {
			slot.running = false
			slot.idleSince = time.Now()
			if slot.invalidateAfterInput {
				slot.closeBackend()

			}
		}
		slot.mu.Unlock()
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
		return errors.New("canonical resident runtime returned no Inbox Notice completion receipt")
	}
	invalidated := slot.messageInputAttempt != attempt || slot.invalidationGeneration != invalidationGeneration
	slot.messageInputDone = acceptance.Done
	if !invalidated {
		slot.lastAppInboxNoticeFingerprint = fingerprint
	}
	slot.mu.Unlock()
	drains := startResidentTurnDrains(acceptance.Messages, acceptance.Capture, nil, time.Time{})
	drains.deadlineDisabled = true
	go p.finishResidentMessageInput(slot, acceptance.Done, drains, 0, nil)
	if invalidated {
		return errors.New("canonical resident runtime was invalidated during Inbox Notice acceptance")
	}
	return nil
}

func (p *agentRuntimePool) clearAppInboxNoticeMemo(agentID, runtimeID string) {
	if p == nil {
		return
	}
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
	p.mu.Lock()
	slot := p.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	p.mu.Unlock()
	if slot == nil {
		return
	}
	slot.lastAppInboxNoticeFingerprint = ""
	slot.mu.Unlock()
}

type residentTurnDrains struct {
	activityDone     <-chan struct{}
	captureDone      <-chan *agent.ResidentTurnCapture
	cancel           context.CancelFunc
	deadline         time.Time
	deadlineDisabled bool
}

func (p *agentRuntimePool) residentTurnDeadline(acceptedAt time.Time) time.Time {
	if p == nil || p.residentTurnCompletionTimeout <= 0 {
		return time.Time{}
	}
	if acceptedAt.IsZero() {
		acceptedAt = time.Now()
	}
	return acceptedAt.Add(p.residentTurnCompletionTimeout)
}

func startResidentTurnDrains(messages <-chan agent.Message, captures <-chan agent.ResidentTurnCapture, onMessage func(agent.Message), deadline time.Time) residentTurnDrains {
	ctx, cancel := context.WithCancel(context.Background())
	return residentTurnDrains{
		activityDone: drainResidentActivity(ctx, messages, onMessage, deadline),
		captureDone:  drainResidentCapture(ctx, captures),
		cancel:       cancel,
		deadline:     deadline,
	}
}

func drainResidentActivity(ctx context.Context, messages <-chan agent.Message, onMessage func(agent.Message), deadline time.Time) <-chan struct{} {
	done := make(chan struct{})
	if messages == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		var timer *time.Timer
		var deadlineC <-chan time.Time
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining < 0 {
				remaining = 0
			}
			timer = time.NewTimer(remaining)
			deadlineC = timer.C
			defer timer.Stop()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-deadlineC:
				return
			case message, ok := <-messages:
				if !ok {
					return
				}
				if !deadline.IsZero() && !time.Now().Before(deadline) {
					return
				}
				if onMessage != nil {
					onMessage(message)
				}
			}
		}
	}()
	return done
}

func drainResidentCapture(ctx context.Context, captures <-chan agent.ResidentTurnCapture) <-chan *agent.ResidentTurnCapture {
	done := make(chan *agent.ResidentTurnCapture, 1)
	if captures == nil {
		done <- nil
		close(done)
		return done
	}
	go func() {
		defer close(done)
		var latest *agent.ResidentTurnCapture
		for {
			select {
			case <-ctx.Done():
				return
			case capture, ok := <-captures:
				if !ok {
					done <- latest
					return
				}
				copy := capture
				latest = &copy
			}
		}
	}()
	return done
}

func stopResidentTurnDrains(drains residentTurnDrains) {
	if drains.cancel != nil {
		drains.cancel()
	}
	// Activity observers are provider-independent application code and cannot
	// be preempted safely; Capture producers may also ignore closure. Never let
	// either auxiliary path retain slot admission after the hard deadline.
	// ownsResidentMessageInput fences any late Activity continuation.
}

// isResidentAcceptBusyErr reports whether an idle Message acceptance error
// indicates the resident runtime is busy or unresponsive to idle input and
// should therefore be treated as a queued-and-retry (ErrCanonicalAgentRuntimeBusy)
// condition rather than a hard delivery failure. It covers the bounded wait
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

func (p *agentRuntimePool) deliverBusyInboxNotice(ctx context.Context, agentID, runtimeID string, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
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
	if slot.backend == nil {
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
	if len(snapshot.TargetKeys) != len(snapshot.Notice.ChangedTargets) {
		return errors.New("pending Notice target metadata is inconsistent")
	}
	notice.ChangedTargets = make([]agent.ResidentPendingTarget, 0, len(snapshot.Notice.ChangedTargets))
	for i, target := range snapshot.Notice.ChangedTargets {
		internalTarget := snapshot.TargetKeys[i]
		if snapshot.TargetFingerprints[internalTarget] != slot.lastPendingTargetFingerprint[internalTarget] {
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

type residentTurnCompletionTimeout struct {
	Timeout     time.Duration
	Phase       string
	RestartSafe bool
}

func (e *residentTurnCompletionTimeout) Error() string {
	if e == nil {
		return "resident provider turn completion timed out"
	}
	phase := strings.TrimSpace(e.Phase)
	if phase == "" {
		phase = "completion"
	}
	return fmt.Sprintf("resident provider turn completion timed out after %s while waiting for %s", e.Timeout, phase)
}

func asResidentTurnCompletionTimeout(err error) (*residentTurnCompletionTimeout, bool) {
	var timeout *residentTurnCompletionTimeout
	ok := errors.As(err, &timeout)
	return timeout, ok
}

func (p *agentRuntimePool) finishResidentMessageInput(slot *agentRuntimeSlot, done <-chan error, drains residentTurnDrains, generation uint64, onComplete func(error, uint64, *agent.ResidentTurnCapture)) {
	if drains.cancel != nil {
		defer drains.cancel()
	}
	timeout := p.residentTurnCompletionTimeout
	deadlineAt := drains.deadline
	if deadlineAt.IsZero() && timeout > 0 && !drains.deadlineDisabled {
		deadlineAt = time.Now().Add(timeout)
	}
	var timer *time.Timer
	var deadline <-chan time.Time
	if !deadlineAt.IsZero() {
		remaining := time.Until(deadlineAt)
		if remaining < 0 {
			remaining = 0
		}
		timer = time.NewTimer(remaining)
		deadline = timer.C
		defer timer.Stop()
	}
	expired := func() bool {
		return !deadlineAt.IsZero() && !time.Now().Before(deadlineAt)
	}

	var turnErr error
	providerDoneSettled := false
	var timeoutErr *residentTurnCompletionTimeout
	if expired() {
		timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider completion"}
	} else {
		select {
		case turnErr = <-done:
			providerDoneSettled = true
			if expired() {
				timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider completion"}
			}
		case <-deadline:
			timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider completion"}
		}
	}
	var capture *agent.ResidentTurnCapture
	// A failed provider may exit without closing its auxiliary Activity/Capture
	// streams. Done remains authoritative in that case. A successful Done is not
	// sufficient by itself: all auxiliary drains and Pi settlement share the same
	// absolute deadline so none can retain admission indefinitely.
	if timeoutErr == nil && turnErr == nil {
		if expired() {
			timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider activity drain"}
		} else {
			select {
			case <-drains.activityDone:
				if expired() {
					timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider activity drain"}
				}
			case <-deadline:
				timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider activity drain"}
			}
		}
	}
	if timeoutErr == nil && turnErr == nil {
		if expired() {
			timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider capture drain"}
		} else {
			select {
			case capture = <-drains.captureDone:
				if expired() {
					timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider capture drain"}
				}
			case <-deadline:
				timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider capture drain"}
			}
		}
	}

	if timeoutErr != nil {
		stopResidentTurnDrains(drains)
		restartSafe, completed := p.retireTimedOutResidentMessageInput(slot, done, providerDoneSettled, nil)
		timeoutErr.RestartSafe = restartSafe
		if completed && onComplete != nil {
			onComplete(timeoutErr, generation, capture)
		}
		return
	}

	completed := false
	finishInvalidated := false
	var settleBackend agent.PiRPCBackend
	var settleIdentity *agent.PiRunIdentity
	slot.mu.Lock()
	if slot.messageInputDone == done {
		// Provider completion is authoritative for the turn. A tool without a
		// result cannot remain live after its owning turn has completed.
		slot.outstandingToolCalls = nil
		if slot.invalidateAfterInput {
			finishInvalidated = true
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

	if finishInvalidated {
		handled, cleanupTimedOut := p.finishInvalidatedResidentMessageInput(slot, done, deadlineAt)
		if handled && onComplete != nil {
			if cleanupTimedOut {
				turnErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider cleanup"}
			}
			onComplete(turnErr, generation, capture)
		}
		return
	}

	if settleBackend != nil && settleIdentity != nil {
		var settlementQuiesced <-chan struct{}
		if expired() {
			timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "Pi turn settlement"}
		} else {
			settled := make(chan error, 1)
			settlementDone := make(chan struct{})
			settlementQuiesced = settlementDone
			go func() {
				defer close(settlementDone)
				settled <- settleBackend.SettleRunTurn(*settleIdentity)
			}()
			select {
			case err := <-settled:
				if expired() {
					timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "Pi turn settlement"}
				} else if err != nil && turnErr == nil {
					turnErr = err
				}
			case <-deadline:
				timeoutErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "Pi turn settlement"}
			}
		}
		if timeoutErr != nil {
			stopResidentTurnDrains(drains)
			restartSafe, retired := p.retireTimedOutResidentMessageInput(slot, done, true, settlementQuiesced)
			timeoutErr.RestartSafe = restartSafe
			if retired && onComplete != nil {
				onComplete(timeoutErr, generation, capture)
			}
			return
		}
		finishInvalidated = false
		slot.mu.Lock()
		if slot.messageInputDone == done {
			if slot.invalidateAfterInput {
				finishInvalidated = true
			} else {
				slot.messageInputDone = nil
				slot.running = false
				slot.compacting = false
				slot.idleSince = time.Now()
				completed = true
			}
		}
		slot.mu.Unlock()
		if finishInvalidated {
			handled, cleanupTimedOut := p.finishInvalidatedResidentMessageInput(slot, done, deadlineAt)
			if handled && onComplete != nil {
				if cleanupTimedOut {
					turnErr = &residentTurnCompletionTimeout{Timeout: timeout, Phase: "provider cleanup"}
				}
				onComplete(turnErr, generation, capture)
			}
			return
		}
	}
	if completed && onComplete != nil {
		onComplete(turnErr, generation, capture)
	}
}

// finishInvalidatedResidentMessageInput closes a lifecycle-invalidated backend
// only after its turn owner has settled. Provider Close may itself wait/reap,
// so it runs outside slot.mu and is bounded by the accepted-turn deadline.
// A timed-out cleanup detaches logical ownership but retains replacementBlocked
// and the open terminated signal until the cleanup goroutine eventually returns.
func (p *agentRuntimePool) finishInvalidatedResidentMessageInput(slot *agentRuntimeSlot, done <-chan error, deadline time.Time) (handled, timedOut bool) {
	if slot == nil {
		return false, false
	}
	slot.mu.Lock()
	if slot.messageInputDone != done || !slot.invalidateAfterInput {
		slot.mu.Unlock()
		return false, false
	}
	backend := slot.backend
	cleanup := slot.close
	slot.mu.Unlock()

	cleanupDone := make(chan struct{})
	go func() {
		if cleanup != nil {
			cleanup()
		}
		close(cleanupDone)
	}()
	confirmed := false
	if deadline.IsZero() {
		<-cleanupDone
		confirmed = true
	} else {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		timer := time.NewTimer(remaining)
		select {
		case <-cleanupDone:
			confirmed = true
			timedOut = !time.Now().Before(deadline)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			timedOut = true
		}
	}

	slot.mu.Lock()
	if slot.messageInputDone != done || slot.backend != backend {
		slot.mu.Unlock()
		return false, timedOut
	}
	slot.messageInputDone = nil
	slot.running = false
	slot.compacting = false
	slot.outstandingToolCalls = nil
	slot.idleSince = time.Now()
	slot.detachBackendStateLocked(confirmed)
	slot.mu.Unlock()
	if !confirmed {
		go func() {
			<-cleanupDone
			slot.confirmDetachedBackendTermination()
		}()
	}
	return true, timedOut
}

// retireTimedOutResidentMessageInput is the timeout owner's only detach path.
// It fences native acceptance, bounds ForceKill, and releases the exact
// Done/backend generation. ForceKill's contract permits replacement after a
// successful interrupt, but provider Close/Wait is deferred until the original
// Done settles so cleanup never races the in-flight reader/reaper.
func (p *agentRuntimePool) retireTimedOutResidentMessageInput(slot *agentRuntimeSlot, done <-chan error, providerDoneSettled bool, additionalQuiescence <-chan struct{}) (restartSafe, completed bool) {
	if slot == nil {
		return false, false
	}
	slot.mu.Lock()
	if slot.messageInputDone != done {
		slot.mu.Unlock()
		return false, false
	}
	slot.invalidationGeneration++
	slot.invalidateAfterInput = true
	backend := slot.backend
	cleanup := slot.close
	slot.mu.Unlock()

	decision := make(chan bool, 1)
	eventualTermination := make(chan struct{})
	go func() {
		providerSettled := providerDoneSettled
		if !providerSettled {
			select {
			case <-done:
				providerSettled = true
			default:
			}
		}
		additionalSettled := additionalQuiescence == nil
		if !additionalSettled {
			select {
			case <-additionalQuiescence:
				additionalSettled = true
			default:
			}
		}
		if providerSettled && additionalSettled {
			if cleanup != nil {
				cleanup()
			}
			decision <- true
			close(eventualTermination)
			return
		}

		killSucceeded := false
		if killable, ok := backend.(agent.ResidentRuntimeForceKillable); ok {
			killSucceeded = killable.ForceKill() == nil
		}
		// A successful ForceKill is the backend contract's process-termination
		// boundary, so replacement may proceed. Cleanup still waits for every
		// prior owner; failed/unsupported kills remain fenced until settlement.
		decision <- killSucceeded
		if !providerSettled {
			<-done
		}
		if !additionalSettled {
			<-additionalQuiescence
		}
		if cleanup != nil {
			cleanup()
		}
		close(eventualTermination)
	}()

	decided := false
	killTimer := time.NewTimer(residentTurnForceKillGrace)
	select {
	case restartSafe = <-decision:
		decided = true
		killTimer.Stop()
	case <-killTimer.C:
	}

	slot.mu.Lock()
	if slot.messageInputDone != done || slot.backend != backend {
		slot.mu.Unlock()
		return false, false
	}
	slot.messageInputDone = nil
	slot.running = false
	slot.compacting = false
	slot.outstandingToolCalls = nil
	slot.idleSince = time.Now()
	slot.detachBackendStateLocked(decided && restartSafe)
	slot.mu.Unlock()

	if !decided {
		go func() {
			if safe := <-decision; safe {
				slot.confirmDetachedBackendTermination()
				return
			}
			<-eventualTermination
			slot.confirmDetachedBackendTermination()
		}()
	} else if !restartSafe {
		go func() {
			<-eventualTermination
			slot.confirmDetachedBackendTermination()
		}()
	}
	return decided && restartSafe, true
}

// publishIfMessageTurnStillIdle serializes a terminal Activity observation
// with admission of the next Message turn. If another delivery already began,
// its Working Activity remains authoritative and this stale terminal state is
// suppressed.
func (p *agentRuntimePool) publishIfMessageTurnStillIdle(agentID, runtimeID string, generation uint64, publish func()) bool {
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
func (p *agentRuntimePool) invalidateSession(agentID, runtimeID string) error {
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
	slot.idleSince = time.Time{}
	return nil
}

// beginResidentTermination interrupts a busy slot instead of refusing it
// (task #62 — invalidateSession is deliberately for the already-idle case
// only) and requests termination without waiting for it. It never calls
// closeBackend() on a running slot: that would race with whatever goroutine
// currently holds Execute() against the same backend. Instead it asks the
// backend itself to force-kill its process via agent.ResidentRuntimeForceKillable,
// then returns — the in-flight turn's own goroutine is expected to observe
// the failure and release the slot, exactly as it already does for an
// unexpected crash (task #42②'s self-heal path).
//
// If the slot is idle, this behaves exactly like invalidateSession (no
// force-kill needed, nothing to interrupt). If the backend does not
// implement ResidentRuntimeForceKillable, this fails closed with
// ErrCanonicalAgentRuntimeBusy rather than silently no-op'ing — a missing
// capability must be loud, not indistinguishable from "nothing to do."
//
// This is the non-blocking half of resident termination. Callers that need
// to know the process is actually gone want terminateResident instead.
func (p *agentRuntimePool) beginResidentTermination(agentID, runtimeID string) error {
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
		slot.idleSince = time.Time{}
		return nil
	}
	killable, ok := slot.backend.(agent.ResidentRuntimeForceKillable)
	if !ok {
		return ErrCanonicalAgentRuntimeBusy
	}
	// Fence any native acceptance that races this restart. The in-flight owner
	// keeps admission until the killed process actually finishes, then closes
	// and detaches this backend so the next delivery creates a fresh instance.
	slot.invalidationGeneration++
	// An accepted Message turn owns a terminal Activity callback keyed by its
	// messageInputGeneration. Lifecycle interruption is an expected boundary,
	// not a provider failure: advance that generation now so the killed turn's
	// late completion cannot remove the replacement managed launch or repaint
	// it Offline after restart.
	if slot.messageInputDone != nil {
		slot.messageInputGeneration++
	}
	slot.invalidateAfterInput = true
	return killable.ForceKill()
}

// residentTerminationWait bounds terminateResident's wait for confirmation
// that a resident process is actually gone. Internal to the pool: callers
// pass their own ctx for cancellation, but must never be able to make this
// wait unbounded (a caller's connection ctx living far longer than any
// process teardown should is exactly the bug this replaces).
const residentTerminationWait = 5 * time.Second

// residentTerminationTimeout reports the state of each condition that was
// still unmet when terminateResident's bounded wait elapsed. Both conditions
// are independent and can be true at once, which is why this is a struct and
// not a pair of sentinel errors: ProcessAlive means the OS process itself is
// still present (usually uninterruptible sleep, a process-level problem);
// TurnRunning means our own turn goroutine never released the slot (a code
// wedge). Distinguishing them is the entire diagnostic value of this type.
type residentTerminationTimeout struct {
	AgentID, RuntimeID string
	// ProcessAlive is true when the OS process is still present.
	ProcessAlive bool
	// TurnRunning is true when slot.running was never released.
	TurnRunning bool
}

func (e *residentTerminationTimeout) Error() string {
	var unmet []string
	if e.ProcessAlive {
		unmet = append(unmet, "OS process still alive")
	}
	if e.TurnRunning {
		unmet = append(unmet, "turn goroutine never released the slot")
	}
	if len(unmet) == 0 {
		unmet = []string{"unknown condition"}
	}
	return fmt.Sprintf("resident termination timed out for agent %s runtime %s: %s",
		e.AgentID, e.RuntimeID, strings.Join(unmet, ", "))
}

// awaitResidentTerminated is the confirm-only half of resident termination:
// it blocks until the resident process for agentID/runtimeID is confirmed
// gone and the slot has been released, bounded internally to
// residentTerminationWait (intersected with ctx, whichever is shorter). It
// does not itself request termination — call beginResidentTermination first.
// Splitting the two lets a caller dispatch the kill immediately and defer
// the bounded wait until some other precondition has resolved, e.g.
// stopManagedAgent must fire the kill before waiting on a concurrent managed
// start's startupDone: a start blocked inside provider spawn runs on the
// WorkspaceDaemon's own lifetime context, not the stop's ctx, so nothing
// but an explicit kill can ever unblock it — waiting first would deadlock.
// If the wait elapses without confirmation, the returned error is a
// *residentTerminationTimeout reporting which condition(s) were still unmet,
// so a caller can tell a wedged OS process apart from a wedged turn
// goroutine.
func (p *agentRuntimePool) awaitResidentTerminated(ctx context.Context, agentID, runtimeID string) error {
	if p == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	key := agentID + "\x00" + runtimeID
	p.mu.Lock()
	slot := p.slots[key]
	p.mu.Unlock()
	if slot == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, residentTerminationWait)
	defer cancel()
	if err := slot.awaitTerminated(waitCtx); err != nil {
		return &residentTerminationTimeout{
			AgentID:      agentID,
			RuntimeID:    runtimeID,
			ProcessAlive: p.residentProcessAlive(agentID, runtimeID),
			TurnRunning:  p.hasRunningTurn(agentID, runtimeID),
		}
	}
	return nil
}

// terminateResident terminates the resident process and returns once it is
// actually gone and the slot has been released. It is beginResidentTermination
// plus awaitResidentTerminated — no polling — for callers that do not need
// the kill and the confirm wait split across some other precondition.
func (p *agentRuntimePool) terminateResident(ctx context.Context, agentID, runtimeID string) error {
	if p == nil {
		return nil
	}
	if err := p.beginResidentTermination(agentID, runtimeID); err != nil {
		return err
	}
	return p.awaitResidentTerminated(ctx, agentID, runtimeID)
}

// revokeResidentPiRunIdentity retires only the requested run binding. A stale
// rollback can therefore never kill a newer run that reused the same
// Agent×runtime slot. Busy native input is force-killed and fenced for detach
// by finishResidentMessageInput; idle prepared processes are closed now.
func (p *agentRuntimePool) revokeResidentPiRunIdentity(agentID, runtimeID string, identity agent.PiRunIdentity) error {
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

func (p *agentRuntimePool) evictIdle(before time.Time) int {
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

// checkResidentLiveness polls every idle resident slot's process liveness and
// evicts + reports any found definitively dead (known=true, alive=false) —
// the case task #42 exists for. A turn's own error path already surfaces a
// dead process; this is the proactive path for when no turn happens to be
// running at the moment it died, which is exactly how the ui-designer
// agent's opencode process sat crashed for 8 hours with nothing noticing.
//
// This must fail open: an in-flight turn, a non-resident slot, a backend
// that doesn't implement agent.ResidentRuntimeLivenessChecker, or an unknown
// liveness answer are all left untouched — none of those are proof of a
// crash, and misclassifying a merely-quiet process as dead would kill a
// perfectly healthy resident session. Every confirmed-dead slot is both
// returned and delivered through emitResidentProcessEvent as a
// residentProcessExited event, so callers that want the detection pass
// itself and callers that only want the notification can each be served
// without polling liveness twice.
func (p *agentRuntimePool) checkResidentLiveness(now time.Time) []residentProcessEvent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	type slotRef struct {
		key  string
		slot *agentRuntimeSlot
	}
	refs := make([]slotRef, 0, len(p.slots))
	for key, slot := range p.slots {
		refs = append(refs, slotRef{key, slot})
	}
	p.mu.Unlock()

	var events []residentProcessEvent
	for _, ref := range refs {
		ref.slot.mu.Lock()
		if ref.slot.running || ref.slot.backend == nil {
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
		agentInstanceID := ref.slot.agentInstanceID
		processInstanceID := ref.slot.processInstanceID
		ref.slot.closeBackend()
		ref.slot.mu.Unlock()

		agentID, runtimeID := splitCanonicalSlotKey(ref.key)
		events = append(events, residentProcessEvent{
			AgentID: agentID, RuntimeID: runtimeID,
			AgentInstanceID: agentInstanceID, ProcessInstanceID: processInstanceID,
			Kind: residentProcessExited, Provider: provider, At: now,
		})
	}

	for _, ev := range events {
		p.emitResidentProcessEvent(ev)
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

func (p *agentRuntimePool) closeAll() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	slots := make([]*agentRuntimeSlot, 0, len(p.slots))
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
	p.slots = make(map[string]*agentRuntimeSlot)
	return nil
}

// forceTerminateAll interrupts only processes owned by this pool. A backend
// without the explicit concurrent ForceKill contract is deliberately left
// alone and makes the caller fail closed rather than guessing how to kill it.
func (p *agentRuntimePool) forceTerminateAll() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	slots := make([]*agentRuntimeSlot, 0, len(p.slots))
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

func defaultCanonicalRuntimeFactory(provider string) canonicalRuntimeBackendFactory {
	return func(config agent.Config) (agent.Backend, func(), error) {
		switch provider {
		case agent.ProviderPi:
			return newCanonicalPiResidentBackend(config)
		case agent.ProviderGrok:
			return newCanonicalGrokResidentBackend(config)
		case agent.ProviderCursor:
			return newCanonicalCursorResidentBackend(config)
		case agent.ProviderOpenCode:
			return newCanonicalOpenCodeResidentBackend(config)
		case agent.ProviderKiro:
			return newCanonicalKiroResidentBackend(config)
		case agent.ProviderCodex:
			return newCanonicalCodexResidentBackend(config)
		case agent.ProviderClaude:
			return newCanonicalClaudeResidentBackend(config)
		default:
			return nil, nil, fmt.Errorf("provider %q has no resident adapter", provider)
		}
	}
}

// acquireCanonicalAgentRuntime is the D4 provider adapter entry point. D6
// supplies the provider-neutral slot contract and activates the production
// caller after wake serialization and current-turn binding are live.
func (p *agentRuntimePool) acquireCanonicalAgentRuntime(
	identity canonicalAgentRuntimeIdentity,
	canonicalSessionID string,
	executionProfile string,
	config agent.Config,
) (*agentRuntimeLease, error) {
	if err := requireCanonicalResidentProvider(identity.Provider, executionProfile); err != nil {
		return nil, err
	}
	return p.acquire(agentRuntimeAcquireRequest{
		Identity:           identity,
		CanonicalSessionID: canonicalSessionID,
		BackendConfig:      config,
		Factory:            defaultCanonicalRuntimeFactory(identity.Provider),
	})
}
