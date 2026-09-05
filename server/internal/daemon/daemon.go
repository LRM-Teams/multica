package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/internal/memoryflush"
	"github.com/multica-ai/multica/server/internal/secretscoped"
	skillpkg "github.com/multica-ai/multica/server/internal/skill"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

var configureMemoryFlushHookOnce sync.Once

const (
	taskMessageFlushInterval            = 200 * time.Millisecond
	taskMessageTrajectoryCoalesceWindow = 350 * time.Millisecond
	taskMessageTrajectoryMaxChars       = 2000

	// agentVersionProbeTimeout caps each synchronous `--version` probe so a
	// single networking ACP CLI (cursor, kiro, …) that hangs on its probe cannot
	// drag down the whole startup registration pass. Startup stays synchronous
	// (no async rework), but each agent is bounded to this window; a timed-out
	// agent is skipped and every other detected agent still registers.
	agentVersionProbeTimeout = 3 * time.Second
)

// taskRunner executes a single agent task and returns the result.
// Extracted as an interface so tests can inject a fake without spawning real
// agent processes, while keeping test scaffolding out of the production struct.
type taskRunner interface {
	run(ctx context.Context, task Task, provider string, slot int, log *slog.Logger) (TaskResult, error)
}

// taskRunnerFunc adapts a plain function to the taskRunner interface.
type taskRunnerFunc func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error)

func (f taskRunnerFunc) run(ctx context.Context, task Task, provider string, slot int, log *slog.Logger) (TaskResult, error) {
	return f(ctx, task, provider, slot, log)
}

type daemonProcessRole uint8

const (
	daemonProcessTestHarness daemonProcessRole = iota
	daemonProcessWorkspaceDaemon
)

var (
	// detectAgentVersion / checkAgentMinVersion are indirections over the
	// real agent helpers so tests can run the registration path without
	// shelling out to a real CLI. Mirrors the pattern used for the brew
	// helpers above.
	detectAgentVersion   = agent.DetectVersion
	checkAgentMinVersion = agent.CheckMinVersion
)

// workspaceState tracks registered runtimes for a single workspace.
type workspaceState struct {
	workspaceID        string
	runtimeIDs         []string
	serverCapabilities []string
}

// Daemon implements one WorkspaceDaemon's execution runtime. ComputerCore is
// composed in internal/computer and never owns Workspace execution details.
type Daemon struct {
	cfg    Config
	client *Client
	logger *slog.Logger
	role   daemonProcessRole

	messageDraftStore          *MessageDraftStore
	agentProxyCredentialMu     sync.RWMutex
	agentProxyCredentials      map[[32]byte]authenticatedAgentProxy
	messageSendMu              sync.Mutex
	messageSends               map[string]int
	workspaceDaemonMu          sync.RWMutex
	workspaceDaemons           map[string]*WorkspaceDaemon
	mixedRunActivityOutbox     *mixedRunActivityOutbox
	mixedRunActivityReporter   func(protocol.MixedRunActivityTransitionPayload) bool
	lifecycleDiagnostics       *lifecycleDiagnosticWriter
	instanceID                 string
	runnerDiagnostics          runnerDiagnosticSink
	runnerDiagnosticStore      *diagnosticlog.Store
	computerControl            *workspaceDaemonComputerControl
	workspaceDaemonDiagnostics *workspaceDaemonDiagnosticForwarder
	computerUpgradeEmit        func(string, any) error

	mu           sync.Mutex
	workspaces   map[string]*workspaceState
	runtimeIndex map[string]Runtime // runtimeID -> Runtime for provider lookups
	runtimeSet   *runtimeSetWatcher // multi-subscriber pub/sub for runtime-set changes

	versionsMu    sync.RWMutex      // guards agentVersions
	agentVersions map[string]string // provider -> detected CLI version (set during registration)

	// taskFriction maps taskID -> *memorysignal.FrictionTracker fed by the
	// task drain loop and drained by reportTaskResultForTask.
	taskFriction sync.Map

	// wsConnState is the task-wakeup WebSocket lifecycle label for internal
	// observability (connecting|open|backoff|closed). Not user-facing Activity.
	wsConnStateMu sync.RWMutex
	wsConnState   string

	reminderCache            *reminderCache
	agentAppInboxes          *AgentAppInboxRegistry
	reminderWSMu             sync.RWMutex
	reminderWrites           chan<- []byte
	reminderWSDone           <-chan struct{}
	reminderClose            func() error
	reminderGateMu           sync.Mutex
	reminderPendingSnapshots map[string]struct{}

	// runtimeGoneMu guards runtimeGoneInflight, reregisterNextAttempt, and
	// reregisterLastCompletedAt. The state lets the Runner control plane and poller
	// handlers converge on a single recovery path when they each detect that a
	// runtime row was deleted server-side without three of them stampeding
	// registerRuntimesForWorkspace.
	runtimeGoneMu             sync.Mutex
	runtimeGoneInflight       map[string]struct{}  // runtime_id -> currently recovering
	reregisterNextAttempt     map[string]time.Time // workspace_id -> earliest time the next re-register attempt may run
	reregisterLastCompletedAt map[string]time.Time // workspace_id -> wall-clock at which the last SUCCESSFUL re-register call returned (failures intentionally not stamped — see recordRegisterCompletion)

	rootCtx     context.Context // WorkspaceDaemon lifetime used by long-running recoveries that must survive per-runtime ctx cancellation
	activeTasks atomic.Int64    // number of tasks currently in handleTask
	// managedTaskCancels contains only task contexts created by this
	// WorkspaceDaemon. Computer-requested drains may stop those turns, but can never infer
	// ownership of, or signal, an arbitrary local process.
	managedTaskMu      sync.Mutex
	managedTaskCancels map[int64]context.CancelFunc
	taskSlotCounter    atomic.Int64 // ever-increasing task sequence number exposed as MULTICA_TASK_SLOT (informational only, tasks are not capacity-limited — see nextTaskSlot)
	ready              atomic.Bool  // true after WorkspaceDaemon preflight completes

	// claimMu guards pauseClaims and claimsInFlight. It is held only for the
	// microseconds it takes to make a decision; ClaimTask itself runs without
	// the lock so a slow per-runtime claim cannot stall a Computer-requested
	// WorkspaceDaemon drain or any other poller.
	//
	// The pair is the Binding handoff barrier against the requirement
	// that "升级过程中如果有 task 进来，会延后升级而不是中断 task":
	// runRuntimePoller refuses to call ClaimTask while pauseClaims is set, and
	// the explicit lifecycle refuses to flip pauseClaims while any poller is mid-claim
	// or any task is in handleTask. Together that closes the fetch-then-claim
	// race where a new task slipping in during the release-metadata fetch
	// would be cancelled while this child is being replaced.
	claimMu        sync.Mutex
	pauseClaims    bool // when true, runRuntimePoller skips ClaimTask
	claimsInFlight int  // pollers that have decided to claim but haven't yet handed the task off to handleTask
	// environmentSwitchPrepared records ownership of pauseClaims by the local
	// config-switch control flow. It prevents one Computer request from clearing a
	// barrier held by another.
	environmentSwitchPrepared atomic.Bool

	// agentWakeSlots is a daemon-side fail-safe for the server's authoritative
	// one-active-wake-per-agent rule. Every chat, DM, issue, autopilot, Radar,
	// and quick-create wake shares this slot; different agents remain concurrent.
	// The server gate prevents normal duplicate claims, while this local gate
	// also fences an executor that is still unwinding after server ownership
	// has ended.
	agentWakeSlotsMu sync.Mutex
	agentWakeSlots   map[string]chan struct{}

	runner             taskRunner    // executes agent tasks; set to d.runTask by New(), overridable in tests
	cancelPollInterval time.Duration // how often handleTask polls for server-side cancellation; overridable in tests
	// Chat transport seams keep the fail-closed setup gate testable without
	// changing process-global executable resolution or filesystem state.
	resolveExecutable   func() (string, error)
	prepareCLITransport func(Config, string, string, string, string, string) (string, string, error)
	// Binding drain seams keep the bounded execution-plane handoff
	// deterministic in tests. Production defaults are installed by the helper
	// methods.
	bindingDrainNow  func() time.Time
	bindingDrainWait func(context.Context, time.Duration) error

	sharedSkillScanMu    sync.Mutex
	sharedSkillScanCache map[string]string // scanRoot\x00skillKey -> fingerprint
	memoryCurationMu     sync.Mutex
	memoryCurationRuns   map[string]string // workspace\x00stage -> Beijing plan date
	activeCurationRuns   map[string]string // runtime id -> claimed run id

	// problemEvolution tracks the single in-flight external evolver batch per
	// runtime: one runtime never drives two batches at once.
	problemEvolutionMu         sync.Mutex
	activeProblemEvolutionRuns map[string]struct{} // runtime id

	// graphProfiles caches the server-delivered effective graph memory
	// profile per workspace (spec §10): deliveries on the resident/channel
	// path carry it, and the resident-message memory prep applies it.
	graphProfileMu sync.Mutex
	graphProfiles  map[string]graphMemoryEffectiveProfile // keyed by workspace id

	// turnScopeMemory tracks which user/project/channel scopes were already
	// injected into a provider session or resident process continuum.
	turnScopeMemory *turnScopeMemoryTracker

	// canonicalRuntimes owns the one durable provider process for each
	// Agent×runtime Message coordinator.
	canonicalRuntimes *agentRuntimePool
	// residentCrashBackoff tracks repeated crashes per agent×runtime (task
	// #42②) so a resident process stuck crash-looping is flagged terminal
	// instead of silently retried forever.
	residentCrashBackoff *residentCrashBackoffTracker
	agentRuntimeSessions *agentRuntimeSessionStore
	// canonicalResidentFactoryOverride is test-only; production uses
	// defaultCanonicalRuntimeFactory for resident Message adapters.
	canonicalResidentFactoryOverride canonicalRuntimeBackendFactory
}

// New creates an in-package execution test harness. Production composition
// enters through RunWorkspaceDaemonProcess; ComputerCore never constructs this type.
func New(cfg Config, logger *slog.Logger) *Daemon {
	return newDaemonForRole(cfg, logger, daemonProcessTestHarness)
}

func newDaemonForRole(cfg Config, logger *slog.Logger, role daemonProcessRole) *Daemon {
	bindingStateRoot := strings.TrimSpace(cfg.BindingStateRoot)
	if bindingStateRoot == "" {
		bindingStateRoot = cfg.WorkspacesRoot
	}
	client := NewClient(cfg.ServerBaseURL)
	// Tag every daemon HTTP request with the daemon's CLI version so the
	// server can split logs/metrics by client version (parallel to the CLI).
	client.SetVersion(cfg.CLIVersion)
	d := &Daemon{
		cfg:                        cfg,
		client:                     client,
		logger:                     logger,
		role:                       role,
		workspaces:                 make(map[string]*workspaceState),
		runtimeIndex:               make(map[string]Runtime),
		runtimeSet:                 newRuntimeSetWatcher(),
		agentVersions:              make(map[string]string),
		agentWakeSlots:             make(map[string]chan struct{}),
		runtimeGoneInflight:        make(map[string]struct{}),
		reregisterNextAttempt:      make(map[string]time.Time),
		reregisterLastCompletedAt:  make(map[string]time.Time),
		cancelPollInterval:         5 * time.Second,
		sharedSkillScanCache:       make(map[string]string),
		memoryCurationRuns:         make(map[string]string),
		activeCurationRuns:         make(map[string]string),
		activeProblemEvolutionRuns: make(map[string]struct{}),
		turnScopeMemory:            newTurnScopeMemoryTracker(),
		instanceID:                 uuid.NewString(),
	}
	d.initializeBindingExecution(bindingStateRoot)
	configureMemoryFlushHookOnce.Do(func() {
		agent.MemoryFlushBeforeCompaction = func(agentRoot string) {
			_ = memoryflush.BeforeCompaction(agentRoot)
		}
	})
	return d
}

func (d *Daemon) initializeBindingExecution(bindingStateRoot string) {
	d.workspaceDaemons = make(map[string]*WorkspaceDaemon)
	d.canonicalRuntimes = newAgentRuntimePool()
	d.canonicalRuntimes.setResidentStallWatchdog(d.cfg.RuntimeProgressStale)
	d.canonicalRuntimes.subscribeResidentProcess(d.onResidentProcessEvent)
	d.messageDraftStore = NewMessageDraftStore(d.cfg.WorkspacesRoot)
	d.mixedRunActivityOutbox = newMixedRunActivityOutbox(bindingStateRoot)
	d.residentCrashBackoff = newResidentCrashBackoffTracker(residentCrashBackoffWindow, residentCrashRetryCap)
	sessions := newAgentRuntimeSessionStore(d.cfg.WorkspacesRoot)
	d.agentRuntimeSessions = sessions
	d.runner = taskRunnerFunc(d.runTask)
	d.reminderCache = newReminderCache(nil, d.logger, nil)
	reminderStorageRoot, reminderStorageErr := builtInAppStorageAgentsRoot(d.cfg.BindingsRoot, d.cfg.MachineID, d.cfg.WorkspaceID, reminderInboxAppID)
	inboxStorageRoot, inboxStorageErr := builtInAppStorageAgentsRoot(d.cfg.BindingsRoot, d.cfg.MachineID, d.cfg.WorkspaceID, agentInboxAppID)
	if reminderStorageErr != nil && d.logger != nil {
		d.logger.Error("Reminder App storage unavailable", "error", reminderStorageErr)
	}
	if inboxStorageErr != nil && d.logger != nil {
		d.logger.Error("Agent Inbox App storage unavailable", "error", inboxStorageErr)
	}
	d.agentAppInboxes = newAgentAppInboxRegistry(inboxStorageRoot, func(agentID string, item AgentAppInboxItem) bool {
		if item.AppID == agentInboxAppID && item.NotificationClass == "message" && item.SourceRef.Kind == "message" && item.SourceRef.ID != "" && item.SourceRef.Revision != "" {
			return true
		}
		if item.AppID != reminderInboxAppID || item.NotificationClass != reminderDueClass || item.SourceRef.Kind != "reminder" || item.SourceRef.ID == "" {
			return false
		}
		version, ok := reminderRevision(item.SourceRef)
		if !ok {
			return false
		}
		return d.reminderCache.consumeFireReceipt(reminderDueIdentity{OwnerAgentID: agentID, ReminderID: item.SourceRef.ID, Version: version})
	})
	d.reminderCache.onFireDelivery = d.materializeReminderFire
	d.reminderCache.onFireReceipt = d.queueReminderFireReceipt
	d.reminderCache.setPersistence(reminderStorageRoot)
}

// setAgentVersion records the detected CLI version for an agent provider so
// later task-dispatch code (e.g. Codex sandbox policy) can read it.
func (d *Daemon) setAgentVersion(provider, version string) {
	d.versionsMu.Lock()
	defer d.versionsMu.Unlock()
	d.agentVersions[provider] = version
}

// agentVersion returns the last-detected CLI version for an agent provider,
// or an empty string if unknown.
func (d *Daemon) agentVersion(provider string) string {
	d.versionsMu.RLock()
	defer d.versionsMu.RUnlock()
	return d.agentVersions[provider]
}

func (d *Daemon) notifyRuntimeSetChanged() {
	d.runtimeSet.notify()
}

const reregisterCoalesceWindow = 30 * time.Second
const reregisterFailureBackoff = 60 * time.Second

func (d *Daemon) handleRuntimeGone(runtimeID string) {
	if runtimeID == "" {
		return
	}

	// entryAt anchors the same-wave-straggler check at the bottom of the
	// function. Captured at the very top so removeStaleRuntime mutex
	// contention can't push it past a sibling's register completion.
	entryAt := time.Now()

	// Stampede control per runtime ID.
	d.runtimeGoneMu.Lock()
	if _, inflight := d.runtimeGoneInflight[runtimeID]; inflight {
		d.runtimeGoneMu.Unlock()
		return
	}
	d.runtimeGoneInflight[runtimeID] = struct{}{}
	d.runtimeGoneMu.Unlock()
	defer func() {
		d.runtimeGoneMu.Lock()
		delete(d.runtimeGoneInflight, runtimeID)
		d.runtimeGoneMu.Unlock()
	}()

	workspaceID, removed := d.removeStaleRuntime(runtimeID)
	if !removed {
		// Already gone from local state — a parallel recovery already
		// cleaned this up, or Binding refresh pruned the whole workspace.
		return
	}

	d.logger.Info("runtime deleted server-side; pruned from local state",
		"runtime_id", runtimeID, "workspace_id", workspaceID)
	d.notifyRuntimeSetChanged()

	if !d.tryClaimRegisterSlot(workspaceID, entryAt, time.Now()) {
		d.logger.Debug("skip re-register: coalescing with recent attempt",
			"workspace_id", workspaceID)
		return
	}

	err := d.reregisterWorkspaceAfterRuntimeGone(d.recoveryContext(), workspaceID)
	d.recordRegisterCompletion(workspaceID, time.Now(), err)
	if err != nil {
		// Logged at Warn (not Error) because Binding refresh retries
		// independently every DefaultWorkspaceSyncInterval, so a transient
		// failure here is not a stuck state — just an extra wait.
		d.logger.Warn("re-register after runtime gone failed",
			"workspace_id", workspaceID, "error", err)
	}
}

// tryClaimRegisterSlot atomically decides whether the calling goroutine should
// run registerRuntimesForWorkspace. Returns true and claims the in-flight slot
// when the caller may proceed; returns false (without mutating state) when the
// call must be coalesced with a peer.
//
// Two gates are checked under runtimeGoneMu:
//
//  1. reregisterNextAttempt: a future timestamp means a peer holds the slot or
//     a previous attempt failed and we are inside the failure backoff window.
//  2. reregisterLastCompletedAt: a timestamp at or after our entryAt means a
//     peer's register SUCCEEDED after we entered handleRuntimeGone, so the
//     workspace state is already covered for our wave and we can bail.
//     Failures intentionally don't stamp this field (see
//     recordRegisterCompletion), so a same-wave straggler whose entryAt
//     predates a failed sibling can still retry once the failure backoff
//     expires — failures don't cover anything.
//
// entryAt is the wall-clock captured at the top of handleRuntimeGone. now is
// passed in (rather than read inside) so tests can drive the gate
// deterministically without sleeping.
func (d *Daemon) tryClaimRegisterSlot(workspaceID string, entryAt, now time.Time) bool {
	d.runtimeGoneMu.Lock()
	defer d.runtimeGoneMu.Unlock()
	if next, ok := d.reregisterNextAttempt[workspaceID]; ok && now.Before(next) {
		return false
	}
	if last, ok := d.reregisterLastCompletedAt[workspaceID]; ok && last.After(entryAt) {
		return false
	}
	d.reregisterNextAttempt[workspaceID] = now.Add(reregisterCoalesceWindow)
	return true
}

// recordRegisterCompletion records the outcome of a register call. On success
// it stamps lastCompletedAt (which suppresses same-wave stragglers via
// tryClaimRegisterSlot) and clears the in-flight slot so a genuinely later
// runtime deletion can claim immediately. On failure it extends
// reregisterNextAttempt by the failure backoff and intentionally does NOT
// stamp lastCompletedAt — a failed register did not cover any workspace
// state, so a same-wave straggler whose entryAt predates the failure must
// still be allowed to retry once the backoff expires. Binding refresh only
// retries when the workspace's runtimeIDs fully drain, so partial-deletion
// recovery has to come from the straggler path.
func (d *Daemon) recordRegisterCompletion(workspaceID string, completedAt time.Time, err error) {
	d.runtimeGoneMu.Lock()
	defer d.runtimeGoneMu.Unlock()
	if err != nil {
		d.reregisterNextAttempt[workspaceID] = completedAt.Add(reregisterFailureBackoff)
		return
	}
	d.reregisterLastCompletedAt[workspaceID] = completedAt
	delete(d.reregisterNextAttempt, workspaceID)
}

// recoveryContext returns the daemon root context for long-running recovery
// HTTP calls (re-register, recover-orphans) that must survive a Runner socket
// generation ending. Falls back to Background when the
// daemon was not started via Run(), e.g. unit-test fixtures.
func (d *Daemon) recoveryContext() context.Context {
	if d.rootCtx != nil {
		return d.rootCtx
	}
	return context.Background()
}

// removeStaleRuntime drops a runtime ID from its owning workspace's runtimeIDs
// list and the daemon-level runtimeIndex.
// Returns the workspace ID and true if the runtime was tracked, "" and false
// otherwise.
//
// Callers mutate workspaceState in place so runtime watchers keep a stable
// object while registration state converges.
func (d *Daemon) removeStaleRuntime(runtimeID string) (string, bool) {
	d.mu.Lock()
	var workspaceID string
	for wsID, ws := range d.workspaces {
		found := false
		filtered := ws.runtimeIDs[:0:0]
		for _, rid := range ws.runtimeIDs {
			if rid == runtimeID {
				found = true
				continue
			}
			filtered = append(filtered, rid)
		}
		if found {
			ws.runtimeIDs = filtered
			workspaceID = wsID
			break
		}
	}
	if workspaceID == "" {
		d.mu.Unlock()
		return "", false
	}
	delete(d.runtimeIndex, runtimeID)
	d.client.ClearRuntimeDaemonToken(runtimeID)
	d.mu.Unlock()

	if d.reminderCache != nil {
		d.reminderCache.suspend()
	}

	return workspaceID, true
}

// workspaceNeedsRuntimeRecovery reports whether a tracked workspace currently
// has zero runtime IDs — the state reached when handleRuntimeGone pruned every
// runtime and its inline re-register failed. Binding refresh calls this so
// the workspace can recover without waiting for an external trigger.
func (d *Daemon) workspaceNeedsRuntimeRecovery(workspaceID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	ws, ok := d.workspaces[workspaceID]
	if !ok {
		return false
	}
	return len(ws.runtimeIDs) == 0
}

// reregisterWorkspaceAfterRuntimeGone calls registerRuntimesForWorkspace and
// updates the existing workspaceState in place. The register response is
// authoritative for this workspace's runtime set — every configured provider
// is included, with UpsertAgentRuntime returning the same row ID for surviving
// providers and a fresh ID for any that were deleted server-side. Replacing
// (rather than appending) is required: a partial recovery, where only one
// runtime in a multi-provider workspace was deleted, would otherwise produce
// duplicates for every provider that wasn't deleted.
//
// The workspaceState pointer is never replaced; only fields are mutated.
func (d *Daemon) reregisterWorkspaceAfterRuntimeGone(ctx context.Context, workspaceID string) error {
	resp, err := d.registerRuntimesForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("register runtimes: %w", err)
	}

	newIDs := make([]string, 0, len(resp.Runtimes))
	newIDSet := make(map[string]struct{}, len(resp.Runtimes))
	for _, rt := range resp.Runtimes {
		newIDs = append(newIDs, rt.ID)
		newIDSet[rt.ID] = struct{}{}
	}

	d.mu.Lock()
	ws, ok := d.workspaces[workspaceID]
	if !ok {
		d.mu.Unlock()
		return fmt.Errorf("workspace %s no longer tracked", workspaceID)
	}
	// Drop runtimeIndex entries for prior runtime IDs that the server did not
	// return — typically there are none for upsert-on-existing-provider, but
	// a daemon config change (provider removed) would leak entries otherwise.
	for _, oldID := range ws.runtimeIDs {
		if _, kept := newIDSet[oldID]; !kept {
			delete(d.runtimeIndex, oldID)
			d.client.ClearRuntimeDaemonToken(oldID)
		}
	}
	for _, rt := range resp.Runtimes {
		d.runtimeIndex[rt.ID] = rt
	}
	// Response is authoritative — replace, do not append. Replacing also
	// catches the rare case where UpsertAgentRuntime returns a different ID
	// for a surviving provider (e.g. schema change); the daemon converges on
	// what the server says without leaving stale heartbeat goroutines.
	ws.runtimeIDs = newIDs
	ws.serverCapabilities = append([]string(nil), resp.ServerCapabilities...)
	d.mu.Unlock()
	if d.computerControl != nil {
		if err := d.computerControl.reportRuntimeSet(ctx, resp.Runtimes, resp.DaemonToken, resp.DaemonTokenExpiresAt); err != nil {
			return fmt.Errorf("report re-registered WorkspaceDaemon Runtime set: %w", err)
		}
	}

	for _, rid := range newIDs {
		d.logger.Info("re-registered runtime after server-side deletion",
			"workspace_id", workspaceID, "runtime_id", rid)
	}
	d.notifyRuntimeSetChanged()

	// Tell the server about any tasks the previous (now-deleted) runtime
	// was working on, mirroring the registration path's recover-orphans call.
	for _, rid := range newIDs {
		if err := d.client.RecoverOrphans(ctx, rid); err != nil {
			d.logger.Warn("recover-orphans after re-register failed",
				"runtime_id", rid, "error", err)
		}
	}
	return nil
}

// runtimeSetWatcher is a tiny pub/sub for runtime-set changes. It exists
// because more than one supervisor (taskWakeupLoop, WorkspaceDaemons, pollLoop)
// needs to react to runtime-set changes; a single buffered channel would
// race so only the first listener would learn about each change.
//
// Each subscriber gets a 1-slot channel; missed nudges coalesce into a
// single signal — the subscriber is expected to re-derive the current
// runtime set via allRuntimeIDs() rather than relying on edge counts.
type runtimeSetWatcher struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func newRuntimeSetWatcher() *runtimeSetWatcher {
	return &runtimeSetWatcher{subscribers: make(map[chan struct{}]struct{})}
}

// Subscribe returns a channel that receives a non-blocking nudge whenever
// the runtime set changes, and an unsubscribe func the caller must invoke
// when done.
func (w *runtimeSetWatcher) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.subscribers[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subscribers, ch)
		w.mu.Unlock()
	}
}

func (w *runtimeSetWatcher) notify() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// deregisterRuntimes notifies the server that all runtimes are going offline.
func (d *Daemon) deregisterRuntimes() {
	runtimeIDs := d.allRuntimeIDs()
	if len(runtimeIDs) == 0 {
		d.logger.Debug("deregister: no runtimes to deregister")
		return
	}

	d.logger.Debug("deregistering runtimes on shutdown", "count", len(runtimeIDs), "runtime_ids", runtimeIDs)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.client.Deregister(ctx, d.cfg.WorkspaceID, runtimeIDs); err != nil {
		d.logger.Warn("failed to deregister runtimes on shutdown", "error", err)
	} else {
		d.logger.Info("deregistered runtimes", "count", len(runtimeIDs))
	}
}

// allRuntimeIDs returns all runtime IDs across all watched workspaces.
func (d *Daemon) allRuntimeIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	for _, ws := range d.workspaces {
		ids = append(ids, ws.runtimeIDs...)
	}
	return ids
}

// findRuntime looks up a Runtime by its ID.
func (d *Daemon) findRuntime(id string) *Runtime {
	d.mu.Lock()
	defer d.mu.Unlock()
	if rt, ok := d.runtimeIndex[id]; ok {
		return &rt
	}
	return nil
}

func daemonRegistrationCapabilities(includeCredentialTransport bool) []string {
	capabilities := []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityAgentCLITransport,
		protocol.DaemonCapabilityMemoryCuration,
		protocol.DaemonCapabilityMemoryCrossDeviceSync,
		protocol.DaemonCapabilityRestrictedExecution,
		protocol.DaemonCapabilityReminderVersionedCache,
		protocol.DaemonCapabilityReminderFireRequest,
		protocol.DaemonCapabilityWorkspaceDaemonAgentProcess,
		protocol.DaemonCapabilityWorkspaceDaemonAgentReset,
		// WorkspaceDaemons advertise the wire capability so the server can
		// deliver the machine action. They only forward it to ComputerCore;
		// acceptance and execution do not live in this package.
		protocol.DaemonCapabilityMachineUpgrade,
		// Explore v2 is negotiated per run: the server only offers
		// generation 2 when this capability AND its phase gate are green.
		protocol.DaemonCapabilityMemoryExploreV2,
	}
	if includeCredentialTransport {
		capabilities = append(capabilities, protocol.DaemonCapabilityAgentCredentialTransport)
	}
	return capabilities
}

// registrationCapabilities adds the capabilities that depend on this daemon's
// configuration. Problem evolution is only advertised when an external evolver
// program is configured, so the server never queues a run to a machine that
// cannot execute it.
func (d *Daemon) registrationCapabilities(includeCredentialTransport bool) []string {
	capabilities := daemonRegistrationCapabilities(includeCredentialTransport)
	if d.problemEvolutionEnabled() {
		capabilities = append(capabilities, protocol.DaemonCapabilityProblemEvolution)
	}
	return capabilities
}

func (d *Daemon) applyRegisterDaemonToken(workspaceID string, resp *RegisterResponse) (bool, error) {
	if resp == nil || resp.DaemonToken == "" || resp.DaemonTokenExpiresAt == "" {
		return false, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, resp.DaemonTokenExpiresAt)
	if err != nil {
		d.logger.Warn("register response carried invalid daemon token expiry", "workspace_id", workspaceID, "error", err)
		return false, nil
	}
	if strings.TrimSpace(d.cfg.BindingsRoot) != "" {
		if err := computer.NewBindingsStore(d.cfg.BindingsRoot).RefreshCredential(
			d.cfg.Environment, workspaceID, d.cfg.DaemonID, resp.DaemonToken, expiresAt,
		); err != nil {
			return false, fmt.Errorf("persist refreshed Workspace Binding credential: %w", err)
		}
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, resp.DaemonToken, expiresAt)
	for _, rt := range resp.Runtimes {
		d.client.SetRuntimeDaemonToken(rt.ID, resp.DaemonToken, expiresAt)
	}
	d.logger.Debug("daemon token cached for workspace", "workspace_id", workspaceID, "runtimes", len(resp.Runtimes), "expires_at", expiresAt)
	return true, nil
}

func (d *Daemon) clearDaemonTokensForWorkspace(workspaceID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.client.ClearWorkspaceDaemonToken(workspaceID)
	if ws, ok := d.workspaces[workspaceID]; ok {
		for _, rid := range ws.runtimeIDs {
			d.client.ClearRuntimeDaemonToken(rid)
		}
	}
}

// probeFailureReason maps a version-probe error to a short English reason for
// the one-line startup output, and the corresponding self-check command users
// can run themselves to confirm whether the CLI detects at all.
func probeFailureReason(name string, err error) (reason, selfCheck string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe timed out (network-backed CLI unreachable)", name + " --version"
	}
	return "version probe failed", name + " --version"
}

func (d *Daemon) registerRuntimesForWorkspace(ctx context.Context, workspaceID string) (*RegisterResponse, error) {
	d.logger.Debug("registering runtimes for workspace", "workspace_id", workspaceID, "agent_count", len(d.cfg.Agents))
	var runtimes []map[string]string

	// Surface detection progress to stderr directly so a foreground/attached
	// terminal shows the per-agent pass live; when the computer is backgrounded
	// stderr is the log file, so the same lines are still captured. One line per
	// agent, English, emoji-prefixed, with an actionable self-check command on
	// failure — so users never have to open logs to see who registered.
	fmt.Fprintf(os.Stderr, "-- Detecting available code agents --\n")
	for name, entry := range d.cfg.Agents {
		// Bound each synchronous `--version` probe: a single networking ACP
		// CLI (cursor, kiro, …) that hangs on its probe must not stall every
		// other agent's registration.
		probeCtx, cancel := context.WithTimeout(ctx, agentVersionProbeTimeout)
		version, err := detectAgentVersion(probeCtx, entry.Path)
		cancel()
		if err != nil {
			reason, selfCheck := probeFailureReason(name, err)
			fmt.Fprintf(os.Stderr, "⚠️ skip %s: %s — try running '%s' to check\n", name, reason, selfCheck)
			d.logger.Warn("skip registering runtime", "name", name, "error", err)
			continue
		}
		if err := checkAgentMinVersion(name, version); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ skip %s: version too old (%s) — upgrade %s then restart the computer\n", name, version, name)
			d.logger.Warn("skip registering runtime: version too old", "name", name, "version", version, "error", err)
			continue
		}
		d.setAgentVersion(name, version)
		fmt.Fprintf(os.Stderr, "✅ %s v%s\n", name, version)
		d.logger.Debug("agent version detected", "name", name, "version", version, "path", entry.Path)
		displayName := strings.ToUpper(name[:1]) + name[1:]
		if d.cfg.DeviceName != "" {
			displayName = fmt.Sprintf("%s (%s)", displayName, d.cfg.DeviceName)
		}
		runtimes = append(runtimes, map[string]string{
			"name":    displayName,
			"type":    name,
			"version": version,
			"status":  "online",
		})
	}
	if len(runtimes) == 0 {
		// Detected CLIs that cannot be registered (too old, version probe
		// failed) must not take the Computer offline. The server register
		// endpoint also rejects an empty runtime list.
		d.logger.Warn("no agent runtimes could be registered; Computer stays connected without runtimes", "workspace_id", workspaceID)
		return &RegisterResponse{}, nil
	}

	includeCredentialTransport := d.client.WorkspaceDaemonTokenAvailable(workspaceID, time.Now())
	req := map[string]any{
		"workspace_id":      workspaceID,
		"daemon_id":         d.cfg.DaemonID,
		"legacy_daemon_ids": d.cfg.LegacyDaemonIDs,
		"device_name":       d.cfg.DeviceName,
		"machine_id":        d.cfg.MachineID,
		"os":                runtime.GOOS,
		"cli_version":       d.cfg.CLIVersion,
		"launched_by":       d.cfg.LaunchedBy,
		"capabilities":      d.registrationCapabilities(includeCredentialTransport),
		"runtimes":          runtimes,
	}
	// MULTICA_SANDBOX_INSTANCE_ID is set by mintSandboxRuntimeEnv for daemon-
	// enabled env-dispatch sandboxes. Forwarding it lets the server record
	// sandbox_instance_id on the registered runtime so env-dispatch can discover
	// it by (workspace, daemon_id, sandbox_instance_id). Empty for non-sandbox
	// daemons (regular machine runtimes), which do not need it.
	if sid := strings.TrimSpace(os.Getenv("MULTICA_SANDBOX_INSTANCE_ID")); sid != "" {
		req["sandbox_instance_id"] = sid
	}

	resp, err := d.client.RegisterForWorkspace(ctx, workspaceID, req)
	if err != nil {
		if includeCredentialTransport && isInvalidDaemonTokenError(err) {
			// An explicit Binding credential that the server revoked must fail
			// closed. Falling back to the user's broad session token here would
			// silently recreate execution authority after a Web removal.
			return nil, fmt.Errorf("Workspace Binding credential rejected; run `multica setup /<workspace-slug>` to repair: %w", err)
		}
		if err != nil {
			return nil, fmt.Errorf("register runtimes: %w", err)
		}
	}
	tokenApplied, err := d.applyRegisterDaemonToken(workspaceID, resp)
	if err != nil {
		return nil, err
	}
	if tokenApplied && !includeCredentialTransport {
		legacyResp := resp
		req["capabilities"] = d.registrationCapabilities(true)
		resp, err = d.client.RegisterForWorkspace(ctx, workspaceID, req)
		if err != nil {
			d.client.ClearWorkspaceDaemonToken(workspaceID)
			for _, rt := range legacyResp.Runtimes {
				d.client.ClearRuntimeDaemonToken(rt.ID)
			}
			d.logger.Warn("daemon-token re-register failed; continuing without credential transport capability", "workspace_id", workspaceID, "error", err)
			resp = legacyResp
		} else {
			if _, err := d.applyRegisterDaemonToken(workspaceID, resp); err != nil {
				return nil, err
			}
		}
	}
	if len(resp.Runtimes) == 0 {
		return nil, fmt.Errorf("register runtimes: empty response")
	}
	d.logger.Debug("register response", "workspace_id", workspaceID, "runtimes", len(resp.Runtimes))
	return resp, nil
}

func newWorkspaceState(workspaceID string, runtimeIDs []string, serverCapabilities ...string) *workspaceState {
	return &workspaceState{
		workspaceID:        workspaceID,
		runtimeIDs:         runtimeIDs,
		serverCapabilities: append([]string(nil), serverCapabilities...),
	}
}

func (d *Daemon) configuredWorkspaceBindings() (map[string]computer.WorkspaceBinding, error) {
	// Explicitly constructed tests and legacy internal callers may omit the
	// store seam; retain their one-workspace behavior without letting the public
	// Computer create profile-selected state.
	if strings.TrimSpace(d.cfg.BindingsRoot) == "" {
		workspaceID := strings.TrimSpace(d.cfg.WorkspaceID)
		if workspaceID == "" {
			return nil, fmt.Errorf("workspace is required")
		}
		return map[string]computer.WorkspaceBinding{
			workspaceID: {WorkspaceID: workspaceID, ComputerID: d.cfg.DaemonID, Active: true},
		}, nil
	}
	all, err := computer.NewBindingsStore(d.cfg.BindingsRoot).AllActiveForEnvironment(d.cfg.Environment)
	if err != nil {
		return nil, fmt.Errorf("load Computer Bindings: %w", err)
	}
	out := make(map[string]computer.WorkspaceBinding, len(all))
	for _, binding := range all {
		if strings.TrimSpace(binding.WorkspaceID) == "" || binding.ComputerID != d.cfg.DaemonID {
			continue
		}
		out[binding.WorkspaceID] = binding
	}
	return out, nil
}

// handleHeartbeatActions dispatches the pending-action set returned by the
// current WorkspaceDaemon control plane.
// Each action is dispatched in its own goroutine so a slow handler cannot
// block subsequent heartbeats.
func (d *Daemon) handleHeartbeatActions(ctx context.Context, runtimeID string, resp *HeartbeatResponse) {
	if resp == nil {
		return
	}
	if resp.PendingModelList != nil || resp.PendingLocalSkills != nil || resp.PendingLocalSkillImport != nil || resp.PendingMemoryCuration != nil {
		d.logger.Debug("heartbeat: pending Workspace actions",
			"runtime_id", runtimeID,
			"model_list", resp.PendingModelList != nil,
			"local_skills", resp.PendingLocalSkills != nil,
			"local_skill_import", resp.PendingLocalSkillImport != nil,
			"memory_curation", resp.PendingMemoryCuration != nil,
		)
	}
	if resp.PendingModelList != nil {
		if rt := d.findRuntime(runtimeID); rt != nil {
			go d.handleModelList(ctx, *rt, resp.PendingModelList.ID, resp.PendingModelList.Environment)
		}
	}
	if resp.PendingLocalSkills != nil {
		if rt := d.findRuntime(runtimeID); rt != nil {
			go d.handleLocalSkillList(ctx, *rt, resp.PendingLocalSkills.ID)
		}
	}
	// Prefer the batch field (new backend); fall back to singular (old backend).
	if len(resp.PendingLocalSkillImports) > 0 {
		if rt := d.findRuntime(runtimeID); rt != nil {
			for _, imp := range resp.PendingLocalSkillImports {
				go d.handleLocalSkillImport(ctx, *rt, imp)
			}
		}
	} else if resp.PendingLocalSkillImport != nil {
		if rt := d.findRuntime(runtimeID); rt != nil {
			go d.handleLocalSkillImport(ctx, *rt, *resp.PendingLocalSkillImport)
		}
	}
	if resp.PendingMemoryCuration != nil {
		if rt := d.findRuntime(runtimeID); rt == nil {
			d.logger.Warn("memory curation claim dropped: runtime not found", "runtime_id", runtimeID, "run_id", resp.PendingMemoryCuration.ID)
		} else if !d.beginMemoryCurationRun(runtimeID, resp.PendingMemoryCuration.ID) {
			// Server already flipped the row to running; if we silently drop
			// it here the evolution page spins on a zombie claim. Fail fast
			// so reclaim/timeout logic can unblock the queue.
			d.logger.Warn("memory curation claim dropped: already active", "runtime_id", runtimeID, "run_id", resp.PendingMemoryCuration.ID)
			pending := *resp.PendingMemoryCuration
			go d.reportMemoryCurationResult(ctx, *rt, pending.ID, map[string]any{
				"status":      "failed",
				"claim_token": pending.ClaimToken,
				"error":       "daemon already running another memory curation claim",
				"result":      map[string]any{},
			})
		} else {
			go d.handleMemoryCuration(ctx, *rt, *resp.PendingMemoryCuration)
		}
	}
	// Problem-evolution runs are pulled rather than pushed: the server keeps
	// the queue, and each heartbeat is the daemon's cue to try one claim.
	if rt := d.findRuntime(runtimeID); rt != nil {
		d.pollProblemEvolution(ctx, *rt)
	}
}

// handleModelList resolves the provider's supported models (via static
// catalog or by shelling out to the agent CLI) and reports the result
// back to the server. Model discovery failures are reported as empty
// lists rather than errors so the UI can still render a creatable
// dropdown.
func (d *Daemon) handleModelList(ctx context.Context, rt Runtime, requestID string, modelListEnvironment map[string]string) {
	d.logger.Info("model list requested", "runtime_id", rt.ID, "request_id", requestID, "provider", rt.Provider)

	entry, ok := d.cfg.Agents[rt.Provider]
	if !ok {
		d.reportModelListResult(ctx, rt, requestID, map[string]any{
			"status": "failed",
			"error":  fmt.Sprintf("no agent configured for provider %q", rt.Provider),
		})
		return
	}

	models, err := agent.ListModelsWithEnvironment(ctx, rt.Provider, entry.Path, modelListEnvironment)
	if err != nil {
		d.reportModelListResult(ctx, rt, requestID, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}

	// Wire format matches handler.ModelEntry. Use a struct (not
	// map[string]string) so the Default bool and the per-model
	// Thinking catalog round-trip — without it the UI loses its
	// "default" badge on the advertised pick and the thinking-level
	// picker for claude/codex (MUL-2339).
	type thinkingLevelWire struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	}
	type modelThinkingWire struct {
		SupportedLevels []thinkingLevelWire `json:"supported_levels"`
		DefaultLevel    string              `json:"default_level,omitempty"`
	}
	type modelWire struct {
		ID       string             `json:"id"`
		Label    string             `json:"label"`
		Provider string             `json:"provider,omitempty"`
		Default  bool               `json:"default,omitempty"`
		Thinking *modelThinkingWire `json:"thinking,omitempty"`
	}
	wire := make([]modelWire, 0, len(models))
	for _, m := range models {
		entry := modelWire{
			ID:       m.ID,
			Label:    m.Label,
			Provider: m.Provider,
			Default:  m.Default,
		}
		if m.Thinking != nil {
			levels := make([]thinkingLevelWire, 0, len(m.Thinking.SupportedLevels))
			for _, lvl := range m.Thinking.SupportedLevels {
				levels = append(levels, thinkingLevelWire{
					Value:       lvl.Value,
					Label:       lvl.Label,
					Description: lvl.Description,
				})
			}
			entry.Thinking = &modelThinkingWire{
				SupportedLevels: levels,
				DefaultLevel:    m.Thinking.DefaultLevel,
			}
		}
		wire = append(wire, entry)
	}
	d.reportModelListResult(ctx, rt, requestID, map[string]any{
		"status":    "completed",
		"models":    wire,
		"supported": agent.ModelSelectionSupported(rt.Provider),
	})
}

func (d *Daemon) handleLocalSkillList(ctx context.Context, rt Runtime, requestID string) {
	d.logger.Info("runtime local skills requested", "runtime_id", rt.ID, "request_id", requestID, "provider", rt.Provider)

	skills, supported, err := listRuntimeLocalSkills(rt.Provider)
	if err != nil {
		d.reportLocalSkillListResult(ctx, rt, requestID, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}

	d.reportLocalSkillListResult(ctx, rt, requestID, map[string]any{
		"status":    "completed",
		"skills":    skills,
		"supported": supported,
	})
}

func (d *Daemon) handleLocalSkillImport(ctx context.Context, rt Runtime, pending PendingLocalSkillImport) {
	d.logger.Info("runtime local skill import requested", "runtime_id", rt.ID, "request_id", pending.ID, "provider", rt.Provider, "skill_key", pending.SkillKey)

	skill, supported, err := loadRuntimeLocalSkillBundle(rt.Provider, pending.SkillKey)
	if err != nil {
		d.reportLocalSkillImportResult(ctx, rt, pending.ID, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}
	if !supported {
		d.reportLocalSkillImportResult(ctx, rt, pending.ID, map[string]any{
			"status": "failed",
			"error":  fmt.Sprintf("provider %q does not expose runtime local skills", rt.Provider),
		})
		return
	}

	d.reportLocalSkillImportResult(ctx, rt, pending.ID, map[string]any{
		"status": "completed",
		"skill":  skill,
	})
}

// runtimeReportBackoffs defines the retry schedule for delivering any
// daemon→server async result (model list, local-skill list, local-skill
// import). First attempt runs immediately, then we back off. The sum
// (≈6.5s) stays well under the server-side running timeout (60s) so a
// report that eventually lands still updates the request instead of
// racing a timeout transition.
//
// Overridable for tests to avoid real sleeps.
var runtimeReportBackoffs = []time.Duration{0, 500 * time.Millisecond, 2 * time.Second, 4 * time.Second}

// reportLocalSkillListResult delivers a list-report to the server with retry
// on transient failures. See reportRuntimeResultWithRetry for semantics.
func (d *Daemon) reportLocalSkillListResult(ctx context.Context, rt Runtime, requestID string, payload map[string]any) {
	d.reportRuntimeResultWithRetry(ctx, "local_skill_list", rt.ID, requestID, func(ctx context.Context) error {
		return d.client.ReportLocalSkillListResult(ctx, rt.ID, requestID, payload)
	})
}

// reportLocalSkillImportResult delivers an import-report to the server with
// retry on transient failures.
func (d *Daemon) reportLocalSkillImportResult(ctx context.Context, rt Runtime, requestID string, payload map[string]any) {
	d.reportRuntimeResultWithRetry(ctx, "local_skill_import", rt.ID, requestID, func(ctx context.Context) error {
		return d.client.ReportLocalSkillImportResult(ctx, rt.ID, requestID, payload)
	})
}

// reportModelListResult delivers a model-list report to the server with retry
// on transient failures. Without this the daemon used to fire once and
// swallow any 5xx, leaving the request stranded in "running" on the server
// until its 60s timeout — defeating the multi-node store fix.
func (d *Daemon) reportModelListResult(ctx context.Context, rt Runtime, requestID string, payload map[string]any) {
	d.reportRuntimeResultWithRetry(ctx, "model_list", rt.ID, requestID, func(ctx context.Context) error {
		return d.client.ReportModelListResult(ctx, rt.ID, requestID, payload)
	})
}

// reportRuntimeResultWithRetry retries `fn` on 5xx / network errors and
// stops on success, 4xx, or after exhausting runtimeReportBackoffs.
//
// Why this exists: the server persists the report through a Redis / DB
// write; on a transient store failure it correctly returns 500. Without a
// client-side retry the daemon would fire once, swallow the error, and the
// pending request stays in "running" on the server until its timeout — which
// is exactly the "daemon did not respond" failure mode the multi-node store
// fix was meant to eliminate. 4xx is treated as permanent (request-not-found,
// cross-workspace token rejected, bad body) — retrying those just wastes
// heartbeat cycles.
func (d *Daemon) reportRuntimeResultWithRetry(ctx context.Context, kind, runtimeID, requestID string, fn func(context.Context) error) {
	var lastErr error
	for attempt, wait := range runtimeReportBackoffs {
		if wait > 0 {
			select {
			case <-ctx.Done():
				d.logger.Error("runtime async report cancelled",
					"kind", kind, "runtime_id", runtimeID, "request_id", requestID,
					"attempt", attempt, "error", ctx.Err())
				return
			case <-time.After(wait):
			}
		}
		err := fn(ctx)
		if err == nil {
			if attempt > 0 {
				d.logger.Info("runtime async report succeeded after retry",
					"kind", kind, "runtime_id", runtimeID, "request_id", requestID,
					"attempt", attempt+1)
			}
			return
		}
		lastErr = err

		// 4xx is permanent (request expired, workspace mismatch, malformed
		// body). No amount of retrying will make it succeed.
		var reqErr *requestError
		if errors.As(err, &reqErr) && reqErr.StatusCode >= 400 && reqErr.StatusCode < 500 {
			d.logger.Error("runtime async report rejected — not retrying",
				"kind", kind, "runtime_id", runtimeID, "request_id", requestID,
				"status", reqErr.StatusCode, "error", err)
			return
		}

		d.logger.Warn("runtime async report failed — will retry",
			"kind", kind, "runtime_id", runtimeID, "request_id", requestID,
			"attempt", attempt+1, "error", err)
	}
	d.logger.Error("runtime async report exhausted retries",
		"kind", kind, "runtime_id", runtimeID, "request_id", requestID, "error", lastErr)
}

func (d *Daemon) tryEnterClaim() bool {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	if d.pauseClaims {
		return false
	}
	d.claimsInFlight++
	return true
}

// exitClaim releases the in-flight claim recorded by tryEnterClaim.
func (d *Daemon) exitClaim() {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	d.claimsInFlight--
}

// trySetClaimBarrier atomically pauses new ClaimTask calls if the daemon is
// fully idle (no claims in flight, no tasks running). Returns true if the
// caller now holds the barrier and must release it with releaseClaimBarrier
// on every non-restart exit path; false if the daemon is busy and the caller
// should defer until the explicit lifecycle can safely begin handoff.
func (d *Daemon) trySetClaimBarrier() bool {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	if d.claimsInFlight > 0 || d.activeTasks.Load() > 0 {
		return false
	}
	d.pauseClaims = true
	return true
}

// setClaimBarrier atomically prevents any new ClaimTask call. Unlike
// trySetClaimBarrier, it deliberately does not require the daemon to be idle:
// callers use it at a staged-update deadline, then wait for every claim that
// crossed the barrier and every active task to drain before restarting.
func (d *Daemon) setClaimBarrier() {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	d.pauseClaims = true
}

// trySetEnvironmentSwitchBarrier acquires the claim barrier only when no
// other handoff currently owns it. The environment-switch handler keeps this
// barrier held across config persistence and process shutdown.
func (d *Daemon) trySetEnvironmentSwitchBarrier() bool {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	if d.pauseClaims {
		return false
	}
	d.pauseClaims = true
	d.environmentSwitchPrepared.Store(true)
	return true
}

func (d *Daemon) releaseEnvironmentSwitchBarrier() bool {
	if !d.environmentSwitchPrepared.CompareAndSwap(true, false) {
		return false
	}
	d.releaseClaimBarrier()
	return true
}

// claimBarrierDrained reports whether the stop-claim barrier is held and all
// work admitted before it has left both the claim handoff and active-task
// phases. Reading the counters under claimMu preserves the handoff boundary
// established by tryEnterClaim/exitClaim.
func (d *Daemon) claimBarrierDrained() bool {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	return d.pauseClaims && d.claimsInFlight == 0 && d.activeTasks.Load() == 0
}

// releaseClaimBarrier clears a Computer-held WorkspaceDaemon barrier so pollers
// may resume claiming. A successful prepare leaves the barrier set until the
// Computer explicitly releases it or terminates the WorkspaceDaemon.
func (d *Daemon) releaseClaimBarrier() {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	d.pauseClaims = false
}

func (d *Daemon) pollLoop(ctx context.Context, taskWakeups <-chan taskWakeup) error {
	var taskWG sync.WaitGroup   // tracks in-flight handleTask goroutines
	var pollerWG sync.WaitGroup // tracks runRuntimePoller goroutines

	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	type pollerHandle struct {
		cancel context.CancelFunc
		wakeup chan struct{}
	}
	pollers := make(map[string]*pollerHandle)

	syncPollers := func() {
		want := make(map[string]struct{})
		for _, rid := range d.allRuntimeIDs() {
			want[rid] = struct{}{}
		}
		for rid, h := range pollers {
			if _, ok := want[rid]; !ok {
				h.cancel()
				delete(pollers, rid)
			}
		}
		for rid := range want {
			if _, ok := pollers[rid]; ok {
				continue
			}
			pctx, pcancel := context.WithCancel(ctx)
			wakeup := make(chan struct{}, 1)
			pollers[rid] = &pollerHandle{cancel: pcancel, wakeup: wakeup}
			pollerWG.Add(1)
			go func(rid string, pctx context.Context, wakeup <-chan struct{}) {
				defer pollerWG.Done()
				d.runRuntimePoller(pctx, ctx, rid, wakeup, &taskWG)
			}(rid, pctx, wakeup)
		}
	}

	syncPollers()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("poll loop stopping, waiting for in-flight tasks", "max_wait", "30s")
			for _, h := range pollers {
				h.cancel()
			}
			// Wait for all pollers to fully return before waiting on taskWG.
			// Otherwise a poller that's between ClaimTask and taskWG.Add(1)
			// could race with taskWG.Wait when the counter is zero, which
			// is an undefined sync.WaitGroup misuse.
			pollerWG.Wait()

			waitDone := make(chan struct{})
			go func() { taskWG.Wait(); close(waitDone) }()
			select {
			case <-waitDone:
			case <-time.After(30 * time.Second):
				d.logger.Warn("timed out waiting for in-flight tasks")
			}
			return ctx.Err()
		case <-runtimeSetCh:
			syncPollers()
		case wakeup := <-taskWakeups:
			if wakeup.runtimeID != "" {
				if h, ok := pollers[wakeup.runtimeID]; ok {
					d.logger.Debug("task wakeup: signaling runtime poller", "runtime_id", wakeup.runtimeID)
					select {
					case h.wakeup <- struct{}{}:
					default:
					}
				} else {
					d.logger.Debug("task wakeup: runtime poller not found", "runtime_id", wakeup.runtimeID, "pollers", len(pollers))
				}
				continue
			}

			// A wakeup without a runtime_id is a catch-up signal (for example,
			// immediately after the websocket connects). Fan it out so queued
			// work that existed before the connection is still discovered.
			d.logger.Debug("task wakeup: fanning out to pollers", "pollers", len(pollers))
			for _, h := range pollers {
				select {
				case h.wakeup <- struct{}{}:
				default:
				}
			}
		}
	}
}

// runRuntimePoller is the per-runtime claim+dispatch loop. It owns its own
// poll cadence and wakeup channel so that a slow HTTP claim for this runtime
// cannot delay any other runtime's claims.
//
// Entry into the active-agent gate happens BEFORE ClaimTask, same reasoning
// as before this was task-count-based: claiming first and then waiting for
// capacity would let claimed tasks pile up in the server-side `dispatched`
// state without a corresponding StartTask, and the server's sweeper would
// fail them as `failed/timeout` after dispatchTimeoutSeconds=300s
// (runtime_sweeper.go:25). That is the exact user-visible failure MUL-1744
// fixed, so we cannot risk recreating it under load.
//
// Gate-before-claim does mean a slow claim holds this runtime's gate entry
// during its HTTP roundtrip; the upper bound is `client.Timeout = 30s`
// (client.go:59), well below the 300s dispatch timeout, so other runtimes'
// tasks stay in server-side `queued` state (which has no timeout) rather
// than entering `dispatched` and racing the sweeper.
//
// pollerCtx is cancelled when this runtime is removed from the watched set
// (e.g. workspace de-registered). parentCtx is the daemon's root ctx and is
// passed to handleTask so an in-flight task is not killed just because the
// runtime set changed mid-flight — the task continues to run until the
// daemon itself shuts down (or the server cancels it).
func (d *Daemon) runRuntimePoller(
	pollerCtx, parentCtx context.Context,
	rid string,
	wakeup <-chan struct{},
	taskWG *sync.WaitGroup,
) {
	nextIdleSleep := d.cfg.PollInterval
	if offset := runtimePollOffset(rid, d.cfg.PollInterval); offset > 0 {
		d.logger.Debug("poll: initial offset deferred", "runtime_id", rid, "offset", offset)
		nextIdleSleep = offset
	}
	sleepAfterIdleClaim := func() error {
		wait := nextIdleSleep
		nextIdleSleep = d.cfg.PollInterval
		return sleepWithContextOrWakeup(pollerCtx, wait, wakeup)
	}

	for {
		if pollerCtx.Err() != nil {
			return
		}

		// Refuse new claims while an explicit Machine Upgrade handoff is
		// preparing to roll the process. The lifecycle re-checks the same
		// counters before handoff, so claims remain accounted for.
		if !d.tryEnterClaim() {
			if err := sleepAfterIdleClaim(); err != nil {
				return
			}
			continue
		}

		task, err := d.drainInboxTask(pollerCtx, rid)
		if err != nil {
			d.exitClaim()
			if pollerCtx.Err() == nil {
				if isRuntimeNotFoundError(err) {
					// Server says this runtime is gone — recover and exit
					// the poller; the runtime-set watcher will tear this
					// goroutine down via pollerCtx once the workspace is
					// re-registered with a new runtime ID.
					go d.handleRuntimeGone(rid)
					return
				}
				d.logger.Warn("drain inbox failed", "runtime_id", rid, "error", err)
			}
			if err := sleepAfterIdleClaim(); err != nil {
				return
			}
			continue
		}
		if task == nil {
			d.exitClaim()
			if err := sleepAfterIdleClaim(); err != nil {
				return
			}
			continue
		}

		d.logger.Info("task received", "task", shortID(task.ID), "issue", task.IssueID)
		taskWG.Add(1)
		d.activeTasks.Add(1)
		slot := d.nextTaskSlot()
		go func(t Task, slot int) {
			defer taskWG.Done()
			defer d.exitClaim()
			defer d.activeTasks.Add(-1)
			taskCtx, cancel := context.WithCancel(parentCtx)
			d.registerManagedTask(slot, cancel)
			defer func() {
				d.unregisterManagedTask(slot)
				cancel()
			}()
			d.handleTask(taskCtx, t, slot)
		}(*task, slot)
		// Loop immediately: more tasks may already be queued for this runtime.
	}
}

func (d *Daemon) drainInboxTask(ctx context.Context, runtimeID string) (*Task, error) {
	for {
		batch, err := d.client.DrainAgentInbox(ctx, runtimeID)
		if err != nil {
			return nil, err
		}
		if batch == nil || len(batch.Events) == 0 {
			return nil, nil
		}

		// Turn-fold: one conversation batch → at most one Task. Ack non-runnable
		// events; fold remaining same-conversation leases onto the primary task.
		var primary *AgentInboxEvent
		var folded []*AgentInboxLease
		for _, event := range batch.Events {
			if event == nil {
				continue
			}
			lease := agentInboxLeaseFromEvent(event, runtimeID)
			// Residual dual-write channel chat reasons must not execute.
			// Standalone bubble (chat_session) and product tasks still run.
			if event.Task == nil || protocol.IsResidualChannelChatInboxReason(event.Reason) {
				if err := d.client.AckAgentInboxEvent(ctx, lease); err != nil {
					// fail-soft: try to ack remaining leases before surfacing
					d.ackFoldedInboxLeasesBestEffort(ctx, append(folded, remainingLeasesAfter(batch.Events, event, runtimeID)...))
					return nil, err
				}
				if event.Task == nil {
					d.logger.Debug("acked non-runnable inbox event", "runtime_id", runtimeID, "event", shortID(event.ID))
				} else {
					d.logger.Info("acked residual channel chat inbox event without execution",
						"runtime_id", runtimeID,
						"event", shortID(event.ID),
						"reason", event.Reason,
					)
				}
				continue
			}
			if primary == nil {
				copy := *event
				primary = &copy
				continue
			}
			l := lease
			folded = append(folded, &l)
		}
		if primary == nil {
			// Entire batch was non-runnable; drain again for the next conversation.
			continue
		}

		task := primary.Task
		primaryLease := agentInboxLeaseFromEvent(primary, runtimeID)
		// Merge seq range across the whole conversation batch so the agent sees
		// the full exchange in one turn (turn-fold / one exchange = one turn).
		seqFrom, seqTo := primaryLease.SeqFrom, primaryLease.SeqTo
		for _, f := range folded {
			if f.SeqFrom > 0 && (seqFrom == 0 || f.SeqFrom < seqFrom) {
				seqFrom = f.SeqFrom
			}
			if f.SeqTo > seqTo {
				seqTo = f.SeqTo
			}
		}
		primaryLease.SeqFrom = seqFrom
		primaryLease.SeqTo = seqTo
		task.InboxEvent = &primaryLease
		task.FoldedInboxEvents = folded
		if len(folded) > 0 {
			d.logger.Info("turn-fold: one exchange as one turn",
				"runtime_id", runtimeID,
				"conversation_id", primaryLease.ConversationID,
				"primary_event", shortID(primaryLease.ID),
				"folded_count", len(folded),
				"seq_from", seqFrom,
				"seq_to", seqTo,
			)
		}
		d.recordStandaloneChatCheckpoint(*task, "lease_acquired", "accepted", "draining", "", "", "", 0)
		return task, nil
	}
}

func agentInboxLeaseFromEvent(event *AgentInboxEvent, runtimeID string) AgentInboxLease {
	return AgentInboxLease{
		ID:              event.ID,
		DeliveryID:      event.DeliveryID,
		ConversationID:  event.ConversationID,
		SourceMessageID: event.SourceMessageID,
		ResponseMode:    event.ResponseMode,
		LeaseToken:      event.LeaseToken,
		LeaseExpiresAt:  event.LeaseExpiresAt,
		SeqFrom:         event.SeqFrom,
		SeqTo:           event.SeqTo,
		RequiresWake:    event.RequiresWake,
		Reason:          event.Reason,
		RuntimeID:       runtimeID,
	}
}

// remainingLeasesAfter builds leases for events after `after` in the batch
// (used only on fail-soft cleanup when an early ack fails).
func remainingLeasesAfter(events []*AgentInboxEvent, after *AgentInboxEvent, runtimeID string) []*AgentInboxLease {
	var out []*AgentInboxLease
	seen := false
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if !seen {
			if ev == after || (after != nil && ev.ID == after.ID) {
				seen = true
			}
			continue
		}
		l := agentInboxLeaseFromEvent(ev, runtimeID)
		out = append(out, &l)
	}
	return out
}

func (d *Daemon) ackFoldedInboxLeasesBestEffort(ctx context.Context, leases []*AgentInboxLease) {
	for _, lease := range leases {
		if lease == nil {
			continue
		}
		if err := d.client.AckAgentInboxEvent(ctx, *lease); err != nil {
			d.logger.Warn("fail-soft ack of folded inbox lease failed", "event", shortID(lease.ID), "error", err)
		}
	}
}

func runtimePollOffset(runtimeID string, interval time.Duration) time.Duration {
	if interval <= 0 || runtimeID == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(runtimeID))
	return time.Duration(h.Sum64() % uint64(interval))
}

// nextTaskSlot returns a unique, ever-increasing sequence number for
// MULTICA_TASK_SLOT. Tasks are not capacity-limited — a daemon-wide
// concurrent-agent-count gate (activeAgentGate) existed briefly on
// 2026-07-30 (PR #1528) and was deleted the same week (see git history for
// the removal PR): it was added to fix a real, observed problem — a
// task-count semaphore hard-capping the whole daemon at 20 concurrent tasks,
// which stalled 12+ distinct runtimes under D6-1b's always-resident
// sessions — but nobody could say what resource the number 20 itself was
// meant to protect (Raft's own daemon has no equivalent limiter, only a
// billing-seat live-process cap; the real constraint, OS memory pressure,
// already fails closed on its own). A gate whose threshold nobody can
// justify is not a safety mechanism, so rather than keep an unjustified
// number (however "harmless" as a high default) or replace it with a
// differently-shaped guess, this daemon simply doesn't gate concurrency: a
// claimed task always runs immediately. If a real resource-exhaustion
// symptom shows up in production, design the limiter around that specific
// symptom (measured memory ceiling? provider-side rate limit?) rather than
// resurrecting this one — this value exists only so spawned tasks/logs can
// distinguish concurrently running tasks from each other, not to select a
// bounded resource.
func (d *Daemon) nextTaskSlot() int {
	return int(d.taskSlotCounter.Add(1))
}

// acquireAgentWakeSlot serializes executions that share mutable conversation
// state. The key is agent-scoped for chat/channel wakes and
// (agent, issue)-scoped for Issue work, so independent Issues can run as
// platform-level subagents while follow-ups on the same Issue remain ordered.
func (d *Daemon) acquireAgentWakeSlot(ctx context.Context, serializationKey string) (func(), error) {
	serializationKey = strings.TrimSpace(serializationKey)
	if serializationKey == "" {
		return nil, errors.New("agent wake execution requires a serialization key")
	}

	d.agentWakeSlotsMu.Lock()
	if d.agentWakeSlots == nil {
		d.agentWakeSlots = make(map[string]chan struct{})
	}
	slot := d.agentWakeSlots[serializationKey]
	if slot == nil {
		slot = make(chan struct{}, 1)
		slot <- struct{}{}
		d.agentWakeSlots[serializationKey] = slot
	}
	d.agentWakeSlotsMu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-slot:
	}

	var once sync.Once
	return func() {
		once.Do(func() { slot <- struct{}{} })
	}, nil
}

func taskWakeSerializationKey(task Task) string {
	agentID := strings.TrimSpace(task.AgentID)
	if agentID == "" {
		return ""
	}
	if issueID := strings.TrimSpace(task.IssueID); issueID != "" {
		return agentID + ":issue:" + issueID
	}
	// Chat, DM, channel, onboarding and other message wakes share the Agent's
	// conversational lane. They must not race the same durable conversation.
	return agentID + ":conversation"
}

// taskExecutionRoot isolates mutable provider configuration and cwd files for
// Issue lanes while preserving the AgentRoot as the durable identity/memory
// store. The same Issue reuses one root (and is serialized above); different
// Issues owned by the same Agent can therefore run concurrently without
// replacing each other's AGENTS.md, .agent_context, CODEX_HOME or sidecars.
func taskExecutionRoot(agentRoot string, task Task) string {
	issueID := strings.TrimSpace(task.IssueID)
	if agentRoot == "" || issueID == "" {
		return agentRoot
	}
	lane := issueID
	if parsed, err := uuid.Parse(issueID); err == nil {
		lane = parsed.String()
	} else {
		h := fnv.New64a()
		_, _ = h.Write([]byte(issueID))
		lane = fmt.Sprintf("%x", h.Sum64())
	}
	return filepath.Join(agentRoot, ".multica", "issue-runs", lane)
}

func isAgentTaskTerminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// shouldInterruptAgent decides whether the running agent should be cancelled
// based on the latest GetTaskStatus call. Pure function so the decision is
// trivially testable; the polling goroutine in watchTaskCancellation is just
// I/O around it.
//
// Two conditions trigger cancellation:
//
//  1. status is a terminal state — "completed", "failed", or "cancelled"
//     (isAgentTaskTerminal). The server has already finalized the task: user
//     cancel, issue reassignment, the runtime offline sweeper flipping
//     running → failed during a disconnect, or a duplicate execution that
//     already completed it. Letting the local agent run on is pure waste —
//     CompleteAgentTask only accepts status == "running", so its eventual
//     CompleteTask/FailTask callback is guaranteed to fail and just adds log
//     noise.
//  2. err is a 404 with "task not found" — the task row was deleted while
//     the agent was running. Without this we'd let the local agent keep
//     emitting tool calls against a dead task for its full timeout window.
//
// All other errors (transient network, 5xx, ...) intentionally do NOT
// trigger cancellation — the next tick will retry and we don't want a
// flaky link to kill an in-flight agent.
func shouldInterruptAgent(status string, err error) bool {
	if err != nil {
		return isTaskNotFoundError(err)
	}
	return isAgentTaskTerminal(status)
}

// watchTaskCancellation polls the server for the task's status on the given
// interval and returns a channel that is closed when the running agent
// should be interrupted. The polling goroutine stops when ctx is cancelled,
// so callers should pass the runCtx that was set up around the agent run.
func (d *Daemon) watchTaskCancellation(ctx context.Context, taskID, runtimeID string, pollInterval time.Duration, taskLog *slog.Logger) <-chan struct{} {
	cancelled := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, err := d.client.GetTaskStatus(ctx, taskID, runtimeID)
				if !shouldInterruptAgent(status, err) {
					continue
				}
				if err != nil {
					taskLog.Info("task gone server-side, interrupting agent", "error", err)
				} else {
					taskLog.Info("task reached terminal state server-side, interrupting agent", "status", status)
				}
				close(cancelled)
				return
			}
		}
	}()
	return cancelled
}

func (d *Daemon) handleTask(ctx context.Context, task Task, slot int) {
	reportCtx := ctx
	d.mu.Lock()
	rt := d.runtimeIndex[task.RuntimeID]
	d.mu.Unlock()
	provider := rt.Provider
	profile, profileErr := taskExecutionProfile(task)
	if profileErr == nil {
		profileErr = validateExecutionProfileProvider(profile, provider)
	}
	if profileErr != nil {
		taskLog := d.logger.With("task", shortID(task.ID), "execution_profile", profile)
		d.reportTaskFailure(ctx, task, profileErr.Error(), "", "", "execution_profile_invalid", taskLog)
		return
	}
	task = restrictTaskForExecutionProfile(task, profile)

	// Task-scoped logger with short ID for readable concurrent logs.
	taskLog := d.logger.With("task", shortID(task.ID), "execution_profile", profile)
	agentName := "agent"
	if task.Agent != nil {
		agentName = task.Agent.Name
	}
	taskLog.Info("picked task execution", "issue", task.IssueID, "agent", agentName, "provider", provider)
	taskLog.Debug("task context",
		"workspace_id", task.WorkspaceID,
		"runtime_id", task.RuntimeID,
		"agent_id", task.AgentID,
		"project_id", task.ProjectID,
		"autopilot_run_id", task.AutopilotRunID,
		"trigger_comment_id", task.TriggerCommentID,
		"resume_session", task.PriorSessionID != "",
		"reuse_workdir", task.PriorWorkDir != "",
	)

	// Server admission is the source-of-truth serialization gate, but a stale
	// local executor may still be unwinding after server ownership has ended.
	// Every wake therefore acquires its daemon-side conversation/Issue lane
	// before touching current-turn state or provider sessions. Inbox
	// wakes additionally renew their server lease while waiting and cancel
	// immediately on permanent lease loss.
	var inboxLeaseLost <-chan struct{}
	executionCtx := ctx
	var executionCancel context.CancelFunc
	if task.isInboxTask() {
		executionCtx, executionCancel = context.WithCancel(ctx)
		defer executionCancel()
		// Renew primary + all folded same-conversation leases for this turn.
		inboxLeaseLost = d.watchInboxLeases(executionCtx, task.inboxLeases(), d.cancelPollInterval, taskLog)
		select {
		case <-inboxLeaseLost:
			d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", "draining", "lease_lost_before_execution", "", provider, 0)
			taskLog.Info("agent inbox lease lost before execution; discarding delivery")
			return
		default:
		}
		go func() {
			select {
			case <-inboxLeaseLost:
				executionCancel()
			case <-executionCtx.Done():
			}
		}()
	}
	if strings.TrimSpace(task.AgentID) != "" {
		releaseWakeSlot, err := d.acquireAgentWakeSlot(executionCtx, taskWakeSerializationKey(task))
		if err != nil {
			if task.isInboxTask() {
				select {
				case <-inboxLeaseLost:
					d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", "draining", "lease_lost_before_execution", "", provider, 0)
					taskLog.Info("agent inbox lease lost before execution; discarding delivery")
				default:
					d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", "draining", "execution_context_cancelled", "", provider, 0)
					taskLog.Info("agent wake cancelled while waiting for agent slot", "error", err)
				}
			} else {
				taskLog.Info("agent wake cancelled while waiting for agent slot", "error", err)
			}
			return
		}
		defer releaseWakeSlot()
	} else {
		// Persisted wakes cannot hit this path because agent_inbox_event.agent_id
		// and agent_inbox_event.agent_id are NOT NULL. Keep synthetic unit tasks
		// usable without inventing a task-id fallback that would pretend to
		// provide per-agent serialization.
		taskLog.Debug("synthetic task has no agent_id; wake slot not applicable")
	}
	ctx = executionCtx

	// Create a cancellable context so we can interrupt the running agent
	// when the server signals the task should stop — either the task reached
	// a terminal state (completed/failed/cancelled) or the task row is
	// deleted (404).
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Poll interval is d.cancelPollInterval (5s in production, reduced in tests
	// via direct field override). Guard against zero so a misconfigured daemon
	// doesn't panic time.NewTicker.
	pollInterval := d.cancelPollInterval
	if pollInterval == 0 {
		pollInterval = 5 * time.Second
	}
	// task #60: this used to be `if !task.isInboxTask()` — every task the
	// daemon dispatches has been inbox-backed since #1164 (2026-07-24) cut
	// all wakes over to the canonical inbox, so that guard silently turned
	// this entire cancellation watch into dead code for all production
	// traffic. A human clicking "cancel" on a stuck task reached CancelTask
	// (which does exactly one thing: UPDATE ... SET status='suppressed'),
	// but nothing ever told this goroutine to look — the daemon ran the
	// stuck one-shot backend to completion regardless. GetTaskStatus
	// (handler/daemon.go) already maps
	// status="suppressed" to "cancelled" generically, with no inbox/legacy
	// distinction, so this is safe for inbox tasks without any server-side
	// change.
	cancelledByPoll := d.watchTaskCancellation(runCtx, task.ID, task.RuntimeID, pollInterval, taskLog)
	go func() {
		select {
		case <-cancelledByPoll:
			runCancel()
		case <-runCtx.Done():
		}
	}()

	// A delivery only grants transport ownership and may renew many times. Mint
	// and persist the provider-run identity at the actual run boundary so a
	// report retry is idempotent while a true reclaim/restart receives a new
	// immutable execution record.
	executionID := ""
	if task.isInboxTask() {
		executionID = uuid.NewString()
		if err := d.client.StartAgentInboxExecution(reportCtx, *task.InboxEvent, executionID); err != nil {
			// The UUID is only a local candidate until the execution ledger
			// accepts it. Do not publish an execution identity that never became
			// durable server evidence.
			d.recordStandaloneChatCheckpoint(task, "execution_start_rejected", "rejected", "draining", "execution_ledger_error", "", provider, 0)
			taskLog.Error("start inbox execution ledger record failed", "error", err)
			d.reportTaskFailure(reportCtx, task, fmt.Sprintf("start inbox execution ledger record: %v", err), "", "", "execution_ledger_error", taskLog)
			return
		}
		task.InboxEvent.ExecutionID = executionID
		d.recordStandaloneChatCheckpoint(task, "execution_started", "accepted", "running", "", executionID, provider, 0)
	}

	result, err := d.runner.run(runCtx, task, provider, slot, taskLog)
	result.ExecutionID = executionID
	result = restrictResultForExecutionProfile(result, profile)
	providerOutcome := "succeeded"
	providerStatus := result.Status
	providerReason := ""
	if err != nil {
		providerOutcome = "failed"
		providerStatus = "failed"
		providerReason = taskfailure.Classify(err.Error()).String()
	}
	d.recordStandaloneChatCheckpoint(task, "provider_finished", providerOutcome, providerStatus, providerReason, executionID, provider, 0)

	// Lease-loss cancellation owns only the provider execution context. Keep
	// terminal reporting on the daemon's parent context so a renew request that
	// races with a successful complete/fail transition cannot cancel the HTTP
	// callback that is committing that transition.
	ctx = reportCtx

	// Report usage before any early return — the agent accumulates tokens
	// whether the task completes, errors, or is cancelled mid-run by the poll
	// goroutine. Both claude.go and codex.go populate result.Usage even when
	// runCtx is cancelled, so dropping this on the cancelled path silently
	// under-reports billing.
	if len(result.Usage) > 0 {
		if task.isInboxTask() {
			if usageErr := d.client.ReportAgentInboxUsage(ctx, *task.InboxEvent, executionID, result.Usage); usageErr != nil {
				taskLog.Warn("report inbox usage failed", "execution_id", executionID, "error", usageErr)
			}
		}
	}

	// Do not publish stale output or terminal state after delivery ownership is
	// lost. Usage is deliberately reported first: it belongs to the immutable
	// provider execution already persisted above, even if another delivery has
	// since reclaimed the source event.
	if inboxLeaseLost != nil {
		select {
		case <-inboxLeaseLost:
			d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", providerStatus, "lease_lost_during_execution", executionID, provider, 0)
			taskLog.Info("agent inbox lease lost during execution; usage recorded and result discarded")
			return
		default:
		}
	}

	// Check if we were cancelled by the polling goroutine.
	if cancelledByPoll != nil {
		select {
		case <-cancelledByPoll:
			d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", providerStatus, "task_cancelled_during_execution", executionID, provider, 0)
			taskLog.Info("task cancelled during execution, discarding result")
			return
		default:
		}
	}

	if err != nil {
		taskLog.Error("task failed", "error", err)
		// runTask returned without a TaskResult, so we don't have a SessionID
		// to forward — best we can do is record the failure.
		// MUL-2946: route the bare error string through the canonical
		// classifier so the failure_reason column reflects the actual
		// shape of the failure (provider 5xx, network, process crash,
		// …) rather than the coarse legacy "agent_error" bucket.
		d.reportTaskFailure(ctx, task, err.Error(), "", "", taskfailure.Classify(err.Error()).String(), taskLog)
		return
	}

	if !task.isInboxTask() {
		_ = d.client.ReportProgress(ctx, task.ID, "Finishing task", 2, 2, task.RuntimeID)
	}

	// Final pre-completion check: if the server already moved the task to a
	// terminal state (completed/failed/cancelled) or deleted the row
	// outright, skip reporting — the complete/fail callbacks would fail
	// anyway. Reuse shouldInterruptAgent so this guard honors the same
	// signals as the in-flight watcher. task #60: same stale
	// `!isInboxTask()` guard as the in-flight watcher above — see that
	// comment. This one closes a narrower race (cancelled in the gap
	// between the last poll tick and the backend returning) rather than
	// the main cancellation path, but was equally dead for every real task.
	if status, err := d.client.GetTaskStatus(ctx, task.ID, task.RuntimeID); shouldInterruptAgent(status, err) {
		d.recordStandaloneChatCheckpoint(task, "result_discarded", "discarded", providerStatus, taskResultDiscardReason(status, err), executionID, provider, 0)
		taskLog.Info("task cancelled during execution, discarding result", "status", status, "error", err)
		return
	}

	d.reportTaskResultForTask(ctx, task, result, taskLog)

}

func taskResultDiscardReason(status string, err error) string {
	status = strings.TrimSpace(status)
	switch status {
	case "cancelled", "suppressed":
		return "task_cancelled_before_terminal"
	case "completed", "failed":
		return "task_already_terminal"
	}
	if isTaskNotFoundError(err) {
		return "task_deleted_before_terminal"
	}
	return "task_interrupted_before_terminal"
}

// reportTaskResult writes the final task disposition back to the server.
//
// Fail closed: only an explicit "completed" status is reported as success.
// Anything else — "blocked", "cancelled", or any future status we forget to
// enumerate — must go through FailTask, so a run that never produced a real
// result can never be displayed as "Completed" in the UI (e.g. provider 429 /
// out-of-credit / runtime crash). Forward SessionID/WorkDir on every path:
// the agent may have built a real session before getting stuck, and we want
// the next chat turn to resume there rather than start over and "forget"
// the conversation.
func (d *Daemon) reportTaskResultForTask(ctx context.Context, task Task, result TaskResult, taskLog *slog.Logger) {
	// Always drain the task's friction tracker here, whatever the status,
	// so finished tasks never accumulate in the map.
	friction := d.takeTaskFrictionVector(task.ID)
	switch result.Status {
	case "completed":
		taskLog.Info("task completed", "status", result.Status)
		if !task.isInboxTask() {
			taskLog.Error("task is missing its canonical inbox lease")
			return
		}
		receipt, err := d.client.CompleteAgentInboxEvent(ctx, *task.InboxEvent, result)
		if err == nil {
			d.recordStandaloneChatCheckpoint(task, "terminal_accepted", "accepted", "completed", "", result.ExecutionID, "", receipt.AckedSeq)
			// Primary completed with agent output; ack folded leases so none
			// remain leased for reclaim (Alice boundary #1).
			d.ackFoldedInboxLeases(ctx, task, taskLog)
			if result.Status == "completed" {
				d.reportAgentMemoryWrites(ctx, task, friction)
			}
			return
		}
		// CompleteTask retries transient errors internally. A transient
		// error reaching us here means the schedule was exhausted while
		// the upstream was still 5xx / unreachable. Converting that into
		// a fail would lose the agent's actual result and surface a
		// misleading red badge in the UI — leave the task in running
		// instead so a future fix (server-side stuck-task reaper, or a
		// daemon-side persistent pending queue) can recover it. Only
		// permanent server-side rejections (4xx other than 408/429)
		// warrant the legacy fallback, because at that point the server
		// has already refused this task and the only useful UI signal
		// left is a concrete failure.
		if isTransientError(err) {
			d.recordStandaloneChatCheckpoint(task, "terminal_rejected", "rejected", "running", "terminal_transient_error", result.ExecutionID, "", 0)
			taskLog.Error("complete task failed after retries; leaving task in running rather than falling back to fail", "error", err)
			return
		}
		d.recordStandaloneChatCheckpoint(task, "terminal_rejected", "rejected", "running", "terminal_permanent_error", result.ExecutionID, "", 0)
		taskLog.Error("complete task rejected by server, falling back to fail", "error", err)
		// MUL-2946: this fallback fires when a server-side complete
		// callback was permanently rejected (4xx other than 408/429)
		// — the agent itself succeeded, so the err here describes the
		// server response rather than an agent failure. The classifier
		// is unlikely to match anything in the server's error text and
		// will land at ReasonAgentUnknown ("agent_error.unknown"),
		// which is the canonical replacement for the legacy
		// "agent_error" coarse bucket.
		fallbackErrMsg := fmt.Sprintf("complete task failed: %s", err.Error())
		d.reportTaskFailure(ctx, task, fallbackErrMsg, result.SessionID, result.WorkDir, taskfailure.Classify(fallbackErrMsg).String(), taskLog)
	default:
		failureReason := result.FailureReason
		if failureReason == "" {
			if result.Status == "cancelled" {
				// "cancelled" is a deliberate non-failure terminal
				// state masquerading as a failure_reason — preserved
				// outside the canonical taxonomy so the UI can render
				// it differently from a real failure.
				failureReason = "cancelled"
			} else {
				// MUL-2946: classify the agent's comment text so the
				// failure_reason lands in the refined taxonomy
				// (provider_auth_or_access, context_overflow,
				// process_failure, …) instead of the legacy coarse
				// "agent_error" bucket. Empty comment lands in
				// ReasonAgentUnknown.
				failureReason = taskfailure.Classify(result.Comment).String()
			}
		}
		taskLog.Info("task did not complete, reporting failure", "status", result.Status, "failure_reason", failureReason)
		d.reportTaskFailure(ctx, task, result.Comment, result.SessionID, result.WorkDir, failureReason, taskLog)
	}
}

// watchInboxLease renews a single inbox delivery. Prefer watchInboxLeases when
// the turn may carry folded same-conversation leases.
func (d *Daemon) watchInboxLease(ctx context.Context, lease AgentInboxLease, interval time.Duration, taskLog *slog.Logger) <-chan struct{} {
	return d.watchInboxLeases(ctx, []AgentInboxLease{lease}, interval, taskLog)
}

// watchInboxLeases renews every lease in the turn-fold batch and closes the
// returned channel on the first permanent rejection. Holding all leases for the
// turn's duration is intentional (not a park): they belong to one exchange.
func (d *Daemon) watchInboxLeases(ctx context.Context, leases []AgentInboxLease, interval time.Duration, taskLog *slog.Logger) <-chan struct{} {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	lost := make(chan struct{})
	if len(leases) == 0 {
		close(lost)
		return lost
	}
	renew := func() bool {
		for _, lease := range leases {
			if err := d.client.RenewAgentInboxEvent(ctx, lease); err != nil {
				if isTransientError(err) {
					taskLog.Debug("agent inbox lease renew transient failure", "event", shortID(lease.ID), "error", err)
					// keep trying other leases; transient does not prove loss
					continue
				}
				taskLog.Warn("agent inbox lease renew rejected; cancelling stale executor", "event", shortID(lease.ID), "error", err)
				close(lost)
				return false
			}
			taskLog.Debug("agent inbox lease renewed", "event", shortID(lease.ID))
		}
		return true
	}
	if !renew() {
		return lost
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !renew() {
					return
				}
			}
		}
	}()
	return lost
}

func (d *Daemon) reportTaskFailure(ctx context.Context, task Task, errMsg, sessionID, workDir, failureReason string, taskLog *slog.Logger) {
	if !task.isInboxTask() {
		taskLog.Error("failed task is missing its canonical inbox lease")
		return
	}
	reasonCode := failureReason
	if strings.Contains(errMsg, agent.ProviderAuthRequiredMarker) {
		reasonCode = agent.ProviderAuthRequiredMarker
	}
	// Task #62: a turn interrupted by ForceKill() must never fall into
	// taskfailure.Classify's generic substring taxonomy (that classifier's
	// 21 categories are governed by an external SQL source of truth for
	// genuine agent-side failures, and this isn't one) or read as an
	// unexplained crash — the user asked for this restart.
	if strings.Contains(errMsg, agent.AgentForceKilledMarker) {
		reasonCode = "restarted_by_user"
	}
	if err := d.client.FailAgentInboxEvent(ctx, *task.InboxEvent, errMsg, sessionID, workDir, failureReason, reasonCode); err != nil {
		d.recordStandaloneChatCheckpoint(task, "terminal_rejected", "rejected", "running", "terminal_fail_error", "", "", 0)
		taskLog.Error("report failed inbox event failed", "error", err)
	} else {
		d.recordStandaloneChatCheckpoint(task, "terminal_accepted", "accepted", "failed", reasonCode, "", "", 0)
	}
	// Folded leases must not stay leased after primary failure (no orphan reclaim).
	d.failFoldedInboxLeases(ctx, task, errMsg, sessionID, workDir, failureReason, reasonCode, taskLog)
}

func (d *Daemon) ackFoldedInboxLeases(ctx context.Context, task Task, taskLog *slog.Logger) {
	for _, lease := range task.FoldedInboxEvents {
		if lease == nil {
			continue
		}
		if err := d.client.AckAgentInboxEvent(ctx, *lease); err != nil {
			taskLog.Warn("ack folded inbox lease failed", "event", shortID(lease.ID), "error", err)
		}
	}
}

func (d *Daemon) failFoldedInboxLeases(ctx context.Context, task Task, errMsg, sessionID, workDir, failureReason, reasonCode string, taskLog *slog.Logger) {
	for _, lease := range task.FoldedInboxEvents {
		if lease == nil {
			continue
		}
		if err := d.client.FailAgentInboxEvent(ctx, *lease, errMsg, sessionID, workDir, failureReason, reasonCode); err != nil {
			// Fallback: ack so the lease is not reclaimed into a second turn.
			if ackErr := d.client.AckAgentInboxEvent(ctx, *lease); ackErr != nil {
				taskLog.Error("fail/ack folded inbox lease failed", "event", shortID(lease.ID), "fail_error", err, "ack_error", ackErr)
			} else {
				taskLog.Warn("folded inbox lease acked after fail rejected", "event", shortID(lease.ID), "error", err)
			}
		}
	}
}

func (t Task) isInboxTask() bool {
	return t.InboxEvent != nil && t.InboxEvent.ID != ""
}

// inboxLeases returns primary + folded leases for renew/complete bookkeeping.
func (t Task) inboxLeases() []AgentInboxLease {
	if t.InboxEvent == nil {
		return nil
	}
	out := make([]AgentInboxLease, 0, 1+len(t.FoldedInboxEvents))
	out = append(out, *t.InboxEvent)
	for _, f := range t.FoldedInboxEvents {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// providerNeedsInlineSystemPrompt is sourced from agent.Capabilities
// (task #47) — do not re-list providers here.
func providerNeedsInlineSystemPrompt(provider string) bool {
	return agent.Capabilities(provider).NeedsInlineSystemPrompt
}

// appendAgentScopeSystemPrompt injects agent-global memory into SystemPrompt
// only for fresh sessions. Resume leaves the prior session's system prompt
// alone so Claude/Pi do not --append-system-prompt the same block again.
func appendAgentScopeSystemPrompt(opts *agent.ExecOptions, memories []execenv.MemoryContextForEnv) {
	if opts == nil || !execenv.ShouldInjectAgentScopeSystemPrompt(opts.ResumeSessionID) {
		return
	}
	mem := execenv.RenderAgentScopeMemory(memories)
	if mem == "" {
		return
	}
	if opts.SystemPrompt != "" {
		opts.SystemPrompt += "\n\n" + mem
	} else {
		opts.SystemPrompt = mem
	}
}

// gateResumeToReusedWorkdir clears the task's prior session unless the task
// runs in the exact workdir the session was recorded against, and reports
// whether that workdir was reused. CLI backends key their session stores to
// the cwd (Claude Code looks sessions up under ~/.claude/projects/<encoded-cwd>/),
// so a session id from a different workdir can never resolve: the CLI exits
// within a second and the run fails before doing any work — permanently,
// because the failed run records no session and the next claim serves the
// same stale pointer again. This fires whenever the prior workdir no longer
// exists (GC'd after the issue went done, daemon reinstall, manual cleanup)
// and execenv.Reuse fell back to a fresh workspace provisioning (GitHub #3854).
func gateResumeToReusedWorkdir(task *Task, taskCtx *execenv.TaskContextForEnv, envWorkDir string, taskLog *slog.Logger) bool {
	reused := task.PriorWorkDir != "" && envWorkDir == task.PriorWorkDir
	if !reused && task.PriorSessionID != "" {
		taskLog.Info("dropping prior session: workdir not reused, per-cwd session cannot resolve",
			"session_id", task.PriorSessionID,
			"prior_workdir", task.PriorWorkDir,
			"workdir", envWorkDir,
		)
		task.PriorSessionID = ""
		taskCtx.PriorSessionResumed = false
	}
	return reused
}

// applyForceFreshSession drops any claimed prior provider session when the
// server marked the wake as a one-shot. Period Brief collectors and the
// synthesizer carry a self-contained prompt and must not resume a poisoned
// Pi conversation (OpenAI Responses `input[n].status` 400). runTask must
// also pass Task.ForceFreshSession into the resident acquire — clearing
// PriorSessionID alone leaves the live Pi process in place.
func applyForceFreshSession(task *Task, taskLog *slog.Logger) {
	if task == nil || !task.ForceFreshSession {
		return
	}
	if task.PriorSessionID != "" && taskLog != nil {
		taskLog.Info("force_fresh_session: dropping prior session", "session_id", task.PriorSessionID)
	}
	task.PriorSessionID = ""
}

func (d *Daemon) runTask(ctx context.Context, task Task, provider string, slot int, taskLog *slog.Logger) (TaskResult, error) {
	// Refuse to spawn an agent without a workspace. An empty workspace_id
	// here would make MULTICA_WORKSPACE_ID empty in the agent env, and the
	// CLI would otherwise silently fall back to the user-global config — a
	// path that can leak operations into an unrelated workspace when
	// multiple workspaces share a host.
	if task.WorkspaceID == "" {
		return TaskResult{}, fmt.Errorf("refusing to spawn agent: task has no workspace_id (task_id=%s)", task.ID)
	}
	profile, err := taskExecutionProfile(task)
	if err != nil {
		return TaskResult{}, err
	}
	if err := validateExecutionProfileProvider(profile, provider); err != nil {
		return TaskResult{}, err
	}
	restrictedExecution := isRestrictedExecutionProfile(profile)
	restrictedMaxOutputTokens := restrictedOutputTokenLimitForTask(task, profile)
	task = restrictTaskForExecutionProfile(task, profile)
	applyForceFreshSession(&task, taskLog)
	taskLog = taskLog.With("execution_profile", profile)

	entry, ok := d.cfg.Agents[provider]
	if !ok {
		return TaskResult{}, fmt.Errorf("no agent configured for provider %q", provider)
	}

	agentName := "agent"
	agentID := resolvedTaskAgentID(task)
	var skills []SkillData
	var instructions string
	var managedRole string
	if task.Agent != nil {
		agentName = task.Agent.Name
		managedRole = task.Agent.ManagedRole
		skills = append([]SkillData(nil), task.Agent.Skills...)
		instructions = task.Agent.Instructions
	}

	agentRootPath := ""
	if !restrictedExecution && task.WorkspaceID != "" && agentID != "" {
		agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, task.WorkspaceID, agentID)
		if err := ensureMulticaAgentRoot(agentRoot); err != nil {
			taskLog.Warn("multica agent root creation failed", "error", err)
		} else {
			// task #94/#204: the agent's own tool calls write directly to
			// this directory during the turn (bash/edit tools operating on
			// MULTICA_AGENT_ROOT below), completely
			// outside any daemon-mediated write path — there is no
			// byte-level write to intercept. The only point where the
			// daemon can enforce a cap at all is here, before the turn
			// starts: refuse the turn outright once the workspace is
			// already at or over its cap, rather than letting it grow
			// further unnoticed.
			// quota <= 0 means unlimited (default after LRM-1047). Positive
			// MULTICA_AGENT_WORKSPACE_QUOTA_BYTES re-enables the turn-start gate.
			// Uses the same at-or-over helper as write/seed (#111) so the three
			// gates cannot drift on the capacity predicate.
			if used, over := agentWorkspaceAtOrOverCap(agentRoot, d.cfg.AgentWorkspaceQuotaBytes); over {
				quota := d.cfg.AgentWorkspaceQuotaBytes
				// Env var name deliberately kept out of the user-visible
				// Comment below (it contains "QUOTA", the exact substring
				// this fix otherwise avoids) — operator-facing detail
				// belongs in the log, not in front of the user.
				taskLog.Warn("agent workspace at or over its size cap, refusing to start turn",
					"agent_id", agentID, "used_bytes", used, "quota_bytes", quota,
					"raise_cap_env", "MULTICA_AGENT_WORKSPACE_QUOTA_BYTES")
				// This will not resolve on its own: the gate blocks every
				// turn for this agent, including one that might otherwise
				// clean up its own workspace, so say who else can act.
				// UpdateAgentFileContent (owner/workspace-admin only) is
				// the one existing path that can actually remove files
				// here today.
				return TaskResult{
					Status: "failed",
					Comment: fmt.Sprintf(
						"agent workspace over capacity: cannot start turn (uses %d bytes, cap %d bytes) — this will not resolve on its own; an owner or workspace admin must remove files under the agent's workspace to free space",
						used, quota,
					),
					FailureReason: "agent_workspace_over_capacity",
				}, nil
			}
			d.hydrateAgentMemoryCenter(ctx, task.WorkspaceID, agentID, task.RuntimeID, agentRoot)
		}
		agentRootPath = agentRoot
	}
	serverMemories := convertMemoriesForEnv(task.Agent)
	memoryTask := task
	memoryTask.AgentID = agentID
	allTurnMemories := serverMemories
	var agentScopeMemories []execenv.MemoryContextForEnv
	if !restrictedExecution {
		agentScopeMemories, _ = prepareAgentScopeMemory(agentRootPath, memoryTask, serverMemories)
		if effectiveMemoryType(d.cfg.MemoryType, memoryTask.MemoryType) == MemoryTypeGraph {
			// Graph mode (spec §8): legacy user/agent retained (no daily);
			// graph owns project/channel/daily. Split agent out for
			// session-start injection; turn context keeps user + graph blob.
			graphCurrent, graphResearch := d.graphExecutionMemories(ctx, memoryTask, taskLog)
			combined := mergeGraphModeExecutionMemory(
				agentRootPath, memoryTask, serverMemories, graphCurrent, graphResearch,
			)
			allTurnMemories = withoutAgentScopeMemories(combined)
			agentScopeMemories = withoutGraphModeLegacyDaily(agentScopeMemories)
		} else {
			allTurnMemories, _ = prepareTurnScopeMemory(agentRootPath, memoryTask, serverMemories)
		}
	}
	// Same provider session: skip user/project/channel scopes already injected.
	// Fresh session (no PriorSessionID) loads them again.
	freshTurnScopeSession := strings.TrimSpace(task.PriorSessionID) == ""
	turnScopeSessionKey := issueTurnScopeSessionKey(agentID, task.RuntimeID, task.PriorSessionID)
	turnMemories := allTurnMemories
	if d.turnScopeMemory != nil {
		turnMemories = d.turnScopeMemory.selectForInject(turnScopeSessionKey, allTurnMemories, freshTurnScopeSession)
	}

	// Prepare the agent's durable execution environment.
	taskCtx := execenv.TaskContextForEnv{
		IssueID:                          task.IssueID,
		TriggerCommentID:                 task.TriggerCommentID,
		TriggerThreadID:                  task.TriggerThreadID,
		NewCommentCount:                  task.NewCommentCount,
		NewCommentsSince:                 task.NewCommentsSince,
		AssignmentSnapshot:               task.AssignmentSnapshot,
		PriorSessionResumed:              task.PriorSessionID != "",
		AgentID:                          agentID,
		AgentName:                        agentName,
		ManagedRole:                      managedRole,
		AgentInstructions:                instructions,
		AgentRoot:                        agentRootPath,
		AgentSkills:                      convertSkillsForEnv(skills),
		AgentMemories:                    turnMemories,
		AgentScopeMemories:               agentScopeMemories,
		ProjectID:                        task.ProjectID,
		ChannelID:                        task.ChannelID,
		ProjectTitle:                     task.ProjectTitle,
		AutopilotRunID:                   task.AutopilotRunID,
		AutopilotID:                      task.AutopilotID,
		AutopilotTitle:                   task.AutopilotTitle,
		AutopilotDescription:             task.AutopilotDescription,
		AutopilotSource:                  task.AutopilotSource,
		AutopilotTriggerPayload:          strings.TrimSpace(string(task.AutopilotTriggerPayload)),
		QuickCreatePrompt:                task.QuickCreatePrompt,
		QuickCreateSource:                task.QuickCreateSource,
		AgentRadarPrompt:                 task.AgentRadarPrompt,
		RequestingUserName:               task.RequestingUserName,
		RequestingUserProfileDescription: task.RequestingUserProfileDescription,
		InitiatorType:                    task.InitiatorType,
		InitiatorID:                      task.InitiatorID,
		InitiatorName:                    task.InitiatorName,
		InitiatorEmail:                   task.InitiatorEmail,
		WorkspaceContext:                 task.WorkspaceContext,
	}

	if strings.TrimSpace(agentID) == "" {
		return TaskResult{}, errors.New("refusing to spawn agent: task has no agent_id")
	}
	workspace, err := execenv.ProvisionAgentWorkspace(
		d.cfg.WorkspacesRoot,
		task.WorkspaceID,
		agentID,
		d.logger,
	)
	if err != nil {
		return TaskResult{}, fmt.Errorf("provision agent workspace: %w", err)
	}

	// Identity and memory remain in the durable AgentRoot. Mutable provider
	// bootstrap files live in an Issue-scoped execution root so different
	// Issues assigned to one Agent can safely execute in parallel.
	executionRoot := taskExecutionRoot(workspace.AgentRoot, task)
	if err := os.MkdirAll(executionRoot, 0o755); err != nil {
		return TaskResult{}, fmt.Errorf("create task execution root: %w", err)
	}
	codexVersion := d.agentVersion(agent.ProviderCodex)
	var agentMcpConfig json.RawMessage
	if task.Agent != nil {
		agentMcpConfig = task.Agent.McpConfig
	}
	env := execenv.Reuse(execenv.ReuseParams{
		AgentRoot:    executionRoot,
		Provider:     provider,
		CodexVersion: codexVersion,
		McpConfig:    agentMcpConfig,
		Task:         taskCtx,
	}, d.logger)
	if env == nil {
		return TaskResult{}, errors.New("prepare durable agent environment")
	}

	if !task.isInboxTask() {
		return TaskResult{}, errors.New("task is missing its canonical inbox lease")
	}

	reused := gateResumeToReusedWorkdir(&task, &taskCtx, env.AgentRoot, taskLog)

	// Prepare the per-run Multica CLI wrapper before injecting the runtime
	// brief. Product-task transport remains task-scoped; Message delivery has
	// its own resident Credential Proxy and never enters this runner.
	// Task execution keeps its short-lived inbox credential. A resident
	// launch credential must never be copied into the task token file.
	agentToken := task.AuthToken
	if agentToken == "" && !restrictedExecution {
		return TaskResult{
			Status:        "failed",
			Comment:       "credential_unavailable: task inbox credential is missing",
			FailureReason: "credential_unavailable",
		}, nil
	}
	cliWrapperDir := ""
	cliTokenFile := ""
	cliBinDir := ""
	selfBin := ""
	transportAttemptPath := ""
	var stableTransport *turntransport.Transport
	var stableTransportBinding turntransport.Binding
	resolveExecutable := d.resolveExecutable
	if resolveExecutable == nil {
		resolveExecutable = os.Executable
	}
	prepareCLITransport := d.prepareCLITransport
	if prepareCLITransport == nil {
		prepareCLITransport = prepareTaskCLITransport
	}
	if restrictedExecution {
		taskLog.Info("agent cli transport omitted for restricted execution")
	} else {
		selfBin, err = resolveExecutable()
		if err != nil {
			taskLog.Warn("agent cli transport: unable to resolve multica executable", "error", err)
		} else {
			cliBinDir = filepath.Dir(selfBin)
			if agentToken == "" {
				taskLog.Warn("agent cli transport: no run bearer token available; CLI API calls will require external auth")
			} else {
				wrapperDir, tokenFile, err := prepareCLITransport(d.cfg, task.WorkspaceID, agentID, task.ID, selfBin, agentToken)
				if err != nil {
					return TaskResult{}, fmt.Errorf("prepare agent CLI transport: %w", err)
				}
				cliWrapperDir = wrapperDir
				cliTokenFile = tokenFile
				transportAttemptPath = turntransport.AttemptPath(wrapperDir)
				if err := os.Remove(transportAttemptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return TaskResult{}, fmt.Errorf("clear stale transport attempt marker: %w", err)
				}
				taskLog.Info("agent cli transport prepared", "wrapper_dir", wrapperDir, "token_file", tokenFile)
			}
		}
	}

	// Inject runtime-specific config (meta skill) so the agent discovers .agent_context/.
	runtimeBrief := restrictedExecutionSystemPrompt(profile)
	if !restrictedExecution {
		runtimeBrief, err = execenv.InjectRuntimeKernel(env.AgentRoot, provider, taskCtx)
		if err != nil {
			d.logger.Warn("execenv: inject runtime config failed (non-fatal)", "error", err)
		}
	}

	prompt := execenv.RenderTurnContext(taskCtx) + BuildPrompt(task, provider, agentRootPath)
	injectedTurnMemories := turnMemories

	// Pass the product-task execution context so the spawned agent CLI can
	// call its task APIs. Message commands use the separate local Credential
	// Proxy in the resident process and never inherit this environment.
	// MULTICA_TASK_SLOT is allocated from the daemon-wide concurrency pool, not
	// per-agent. When one daemon hosts multiple agents, slots index shared
	// daemon-level resources such as GPUs.
	// The API credential itself is written below into a per-agent/per-run token
	// file and exposed through a CLI wrapper, not as a raw environment value.
	// Use only the per-run bearer token the server minted at claim/drain time.
	// It is bound to the product task's inbox event and delivery. Auth middleware rejects it on owner-only
	// endpoints (e.g. `/api/agents/{id}/env`), so the agent cannot use it to
	// read another agent's secrets. Do not fall back to the daemon's own
	// long-lived credential for agent transport.
	agentEnv := map[string]string{
		"MULTICA_SERVER_URL":   d.cfg.ServerBaseURL,
		"MULTICA_DAEMON_PORT":  fmt.Sprintf("%d", d.cfg.HealthPort),
		"MULTICA_WORKSPACE_ID": task.WorkspaceID,
		"MULTICA_AGENT_NAME":   agentName,
		"MULTICA_AGENT_ID":     agentID,
		"MULTICA_RUN_ID":       task.ID,
		"MULTICA_TASK_SLOT":    strconv.Itoa(slot),
	}
	agentEnv["MULTICA_TASK_ID"] = task.ID
	if task.InboxEvent != nil {
		agentEnv["MULTICA_AGENT_INBOX_EVENT_ID"] = task.InboxEvent.ID
		agentEnv["MULTICA_AGENT_INBOX_DELIVERY_ID"] = task.InboxEvent.DeliveryID
		agentEnv["MULTICA_AGENT_INBOX_LEASE_TOKEN"] = task.InboxEvent.LeaseToken
	}
	if transportAttemptPath != "" {
		agentEnv[turntransport.AttemptPathEnv] = transportAttemptPath
	}
	if task.InitiatorType == "member" {
		agentEnv["MULTICA_MEMBER_ID"] = task.InitiatorID
	}
	if task.ProjectID != "" {
		agentEnv["MULTICA_PROJECT_ID"] = task.ProjectID
	}
	if task.ChannelID != "" {
		agentEnv["MULTICA_CHANNEL_ID"] = task.ChannelID
	}
	if !restrictedExecution {
		addMulticaAgentEnv(agentEnv, d.cfg, task.WorkspaceID, agentID)
		if provider == agent.ProviderPi {
			addPiMemoryFastModeEnv(agentEnv)
		}
	}
	if task.AutopilotRunID != "" {
		agentEnv["MULTICA_AUTOPILOT_RUN_ID"] = task.AutopilotRunID
	}
	if task.AutopilotID != "" {
		agentEnv["MULTICA_AUTOPILOT_ID"] = task.AutopilotID
	}
	// Quick-create marker — when set, the multica CLI's `issue create`
	// command stamps the new issue with origin_type=quick_create +
	// origin_id=<task_id> so the completion handler can find it
	// deterministically (see GetIssueByOrigin).
	if task.QuickCreatePrompt != "" {
		agentEnv["MULTICA_QUICK_CREATE_TASK_ID"] = task.ID
		if len(task.QuickCreateAttachmentIDs) > 0 {
			if raw, err := json.Marshal(task.QuickCreateAttachmentIDs); err == nil {
				agentEnv["MULTICA_QUICK_CREATE_ATTACHMENT_IDS"] = string(raw)
			} else {
				taskLog.Warn("quick-create attachment ids: marshal failed; skipping env injection", "error", err)
			}
		}
		// The source anchor is a server-owned provenance fact, not a prompt
		// convention for the agent to remember. Make the CLI persist it with
		// the issue creation transaction so channel backflow has a durable
		// target before the issue-created event is published.
		if source := task.QuickCreateSource; source != nil {
			if source.ChannelID != "" && source.ThreadRootMessageID != "" {
				agentEnv["MULTICA_QUICK_CREATE_SOURCE_CHANNEL_ID"] = source.ChannelID
				agentEnv["MULTICA_QUICK_CREATE_SOURCE_MESSAGE_ID"] = source.ThreadRootMessageID
			}
		}
	}
	// Ensure the multica CLI is on PATH inside the agent's environment.
	// Some runtimes (e.g. Codex) run in an isolated sandbox that may not
	// inherit the daemon's PATH. Prepend a per-run wrapper directory so
	// `multica` resolves to the task-scoped transport wrapper first; keep the
	// real binary directory after it as a fallback for explicit wrapper exec.
	if cliBinDir != "" {
		pathDirectories := make([]string, 0, 3)
		if cliWrapperDir != "" {
			if cliTokenFile != "" {
				agentEnv["MULTICA_TOKEN_FILE"] = cliTokenFile
			}
			pathDirectories = append(pathDirectories, cliWrapperDir)
		}
		pathDirectories = append(pathDirectories, cliBinDir, os.Getenv("PATH"))
		agentEnv["PATH"] = strings.Join(pathDirectories, string(os.PathListSeparator))
	}
	// Inject user-configured custom_env (agent-scoped) plus claim-time
	// scoped_secrets after channel/project filtering (LRM-953). Channel A
	// secrets must not enter channel B; project secrets require a bound
	// project. Critical Multica/internal keys stay blocklisted.
	injectScopedSecrets(agentEnv, task, d.logger)
	// AReaL RL proxy override (§4.4): when the claimed task carries an
	// areal_proxy config, force the runtime onto the RL proxy provider. The
	// base_url env must be injected before the backend is created (it is part
	// of the agent process env); the model + api-key are applied at ExecOptions
	// build below. See arealProxyExecOverride for pi's arg/env contract.
	arealModel, arealArgs, arealEnvKey, arealEnvVal, arealOverride := arealProxyExecOverride(task.ArealProxy)
	if arealOverride && arealEnvKey != "" {
		agentEnv[arealEnvKey] = arealEnvVal
	}
	if !restrictedExecution && profile == executionProfileFull && isCanonicalResidentProvider(provider) && agentToken != "" {
		var transportErr error
		stableTransport, transportErr = prepareStableAgentCLITransport(d.cfg, task.WorkspaceID, agentID, selfBin)
		if transportErr != nil {
			return TaskResult{}, fmt.Errorf("prepare stable agent CLI transport: %w", transportErr)
		}
		currentTurn := make(map[string]string)
		for key, value := range agentEnv {
			if turntransport.IsTurnEnvironmentKey(key) {
				currentTurn[key] = value
			}
		}
		var bindErr error
		stableTransportBinding, bindErr = stableTransport.Bind(task.ID, agentToken, currentTurn)
		if bindErr != nil {
			return TaskResult{}, fmt.Errorf("bind stable agent CLI transport: %w", bindErr)
		}
		defer func() {
			if _, unbindErr := turntransport.Unbind(stableTransportBinding); unbindErr != nil {
				taskLog.Warn("stable agent CLI transport cleanup failed", "error", unbindErr)
			}
		}()
		delete(agentEnv, "MULTICA_TOKEN_FILE")
		stablePath := filepath.Dir(stableTransport.WrapperPath())
		if cliBinDir != "" {
			stablePath += string(os.PathListSeparator) + cliBinDir
		}
		stablePath += string(os.PathListSeparator) + os.Getenv("PATH")
		agentEnv["PATH"] = stablePath
		transportAttemptPath = turntransport.AttemptPath(stableTransport.Root())
	}
	backendCfg := agent.Config{
		ExecutablePath: entry.Path,
		Env:            agentEnv,
		Logger:         d.logger,
	}

	taskLog.Info("starting agent",
		"provider", provider,
		"workdir", env.AgentRoot,
		"model", entry.Model,
		"reused", reused,
	)
	if task.PriorSessionID != "" {
		taskLog.Info("resuming session", "session_id", task.PriorSessionID)
	}

	taskStart := time.Now()

	var customArgs []string
	extraArgs := defaultArgsForProvider(d.cfg, provider)
	var mcpConfig json.RawMessage
	if task.Agent != nil {
		customArgs = task.Agent.CustomArgs
		mcpConfig = task.Agent.McpConfig
	}
	// Two-tier model resolution: an explicit agent.model wins,
	// then the daemon-wide MULTICA_<PROVIDER>_MODEL env var. If
	// both are empty we deliberately pass "" through — each
	// backend omits `--model` from the CLI invocation, so the
	// provider picks its own default (Claude Code's shipped
	// default, codex app-server's account-scoped default, etc.).
	// Baking a Go-side "recommended default" here is how the
	// cursor regression happened — static guesses drift from
	// whatever the upstream CLI actually accepts.
	model := ""
	if task.Agent != nil && task.Agent.Model != "" {
		model = task.Agent.Model
	}
	if model == "" {
		model = entry.Model
	}
	thinkingLevel := ""
	if task.Agent != nil {
		thinkingLevel = task.Agent.ThinkingLevel
	}
	// Per-model guard: the server validates the literal token against the
	// provider's enum, but per-model gaps (Claude's `xhigh` on a non-Opus
	// model, Codex's per-model `supported_reasoning_levels`) only resolve
	// here, against the daemon's local CLI catalog. Invalid combinations
	// log a warning and drop the level rather than failing the task, so a
	// stale persisted value never blocks execution. Empty model is passed
	// through unchanged — ValidateThinkingLevel resolves it to the
	// provider's default model internally so default-model tasks aren't
	// misjudged. Discovery errors fail open: if we can't list models, we
	// keep the persisted level and let the CLI surface any objection.
	if thinkingLevel != "" {
		ok, err := agent.ValidateThinkingLevel(ctx, provider, entry.Path, model, thinkingLevel)
		if err != nil {
			taskLog.Warn("thinking_level: catalog lookup failed; passing through",
				"provider", provider,
				"model", model,
				"thinking_level", thinkingLevel,
				"error", err,
			)
		} else if !ok {
			taskLog.Warn("thinking_level: not valid for this (provider, model); skipping injection",
				"provider", provider,
				"model", model,
				"thinking_level", thinkingLevel,
			)
			thinkingLevel = ""
		}
	}
	if arealOverride {
		model = arealModel
		customArgs = append(customArgs, arealArgs...)
		taskLog.Info("areal proxy: routing runtime through RL bridge",
			"runtime_provider", provider,
			"model", model,
			"base_url", arealEnvVal,
		)
	}
	execOpts := agent.ExecOptions{
		Cwd:                       env.AgentRoot,
		Model:                     model,
		ThreadName:                deriveTaskThreadName(task),
		Timeout:                   d.cfg.AgentTimeout,
		SemanticInactivityTimeout: d.cfg.CodexSemanticInactivityTimeout,
		ResumeSessionID:           task.PriorSessionID,
		ExtraArgs:                 extraArgs,
		CustomArgs:                customArgs,
		McpConfig:                 mcpConfig,
		ThinkingLevel:             thinkingLevel,
		DisableTools:              restrictedExecution,
		EphemeralSession:          restrictedExecution,
		MaxOutputTokens:           restrictedMaxOutputTokens,
	}
	// Kiro's CLI cwd handling is opaque enough that we can't trust the
	// file-based runtime config path. Pass the compact runtime kernel inline
	// so it still sees workflow / identity / skills. Turn-specific workflow
	// remains in the user prompt.
	if restrictedExecution || providerNeedsInlineSystemPrompt(provider) {
		execOpts.SystemPrompt = runtimeBrief
	}
	// Agent-scope memory: inject once on fresh session only. Resume must not
	// re-append (Claude/Pi --append-system-prompt). On-disk AGENTS brief still
	// carries agent memory via InjectRuntimeKernel for providers that ignore
	// SystemPrompt (e.g. Cursor).
	appendAgentScopeSystemPrompt(&execOpts, agentScopeMemories)

	var runtimeLease *agentRuntimeLease
	var backend agent.Backend
	var createErr error
	if !restrictedExecution && profile == executionProfileFull && isCanonicalResidentProvider(provider) {
		stableEnvironment, envErr := stableCanonicalRuntimeEnvironment(agentEnv)
		if envErr != nil {
			return TaskResult{}, fmt.Errorf("build canonical runtime identity: %w", envErr)
		}
		identity, identityErr := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
			AgentID:             agentID,
			RuntimeID:           task.RuntimeID,
			Provider:            provider,
			Executable:          entry.Path,
			Model:               model,
			Thinking:            thinkingLevel,
			WorkDir:             agentRootPath,
			SystemPrompt:        execOpts.SystemPrompt,
			MCP:                 string(mcpConfig),
			CustomArgs:          customArgs,
			Environment:         stableEnvironment,
			WorkspaceID:         task.WorkspaceID,
			AgentInstructions:   instructions,
			WorkspaceContext:    task.WorkspaceContext,
			StartupStaticDigest: execenv.StartupStaticDigest(provider, taskCtx),
		})
		if identityErr != nil {
			return TaskResult{}, fmt.Errorf("build canonical runtime identity: %w", identityErr)
		}
		residentConfig := backendCfg
		residentConfig.ResidentOptions = execOpts
		residentConfig.ResidentOptions.Cwd = agentRootPath
		residentAgentInstanceID := "resident-" + uuid.NewString()
		acquireRequest := agentRuntimeAcquireRequest{
			Identity:          identity,
			BackendConfig:     residentConfig,
			Factory:           defaultCanonicalRuntimeFactory(provider),
			ForceFreshSession: task.ForceFreshSession,
			PrepareLaunchEnvironment: func(environment map[string]string) (string, func(), error) {
				return d.prepareCanonicalAgentProxyLaunch(
					ctx, environment, task.WorkspaceID, task.RuntimeID, agentID,
					residentAgentInstanceID, selfBin, false,
				)
			},
		}
		// A resident slot is deliberately single-flight. Tasks for the same
		// Agent/runtime wait here and are picked up by the same provider process
		// after the previous turn releases the slot, instead of spawning another
		// provider or failing the task as "busy".
		for {
			runtimeLease, createErr = d.canonicalRuntimes.acquire(acquireRequest)
			if !errors.Is(createErr, ErrCanonicalAgentRuntimeBusy) {
				break
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				createErr = ctx.Err()
			case <-timer.C:
			}
			if createErr != nil && !errors.Is(createErr, ErrCanonicalAgentRuntimeBusy) {
				break
			}
		}
		if createErr != nil {
			return TaskResult{}, fmt.Errorf("acquire canonical agent runtime: %w", createErr)
		}
		backend = runtimeLease.backend
	} else {
		backend, createErr = agent.New(provider, backendCfg)
		if createErr != nil {
			return TaskResult{}, fmt.Errorf("create agent backend: %w", createErr)
		}
	}

	taskLog.Debug("invoking backend",
		"provider", provider,
		"model", model,
		"prompt_bytes", len(prompt),
		"runtime_brief_bytes", len(runtimeBrief),
		"custom_args", len(customArgs),
		"extra_args", len(extraArgs),
		"mcp_config", len(mcpConfig) > 0,
		"inline_system_prompt", execOpts.SystemPrompt != "",
		"resume_session", execOpts.ResumeSessionID != "",
		"timeout", execOpts.Timeout,
	)

	result, tools, err := d.executeAndDrainForTask(ctx, backend, prompt, execOpts, taskLog, task)
	if err != nil {
		return TaskResult{}, err
	}

	// Fallback: if session resume failed before establishing a session, retry
	// with a fresh session. We check SessionID == "" to distinguish a resume
	// failure (no session established) from a failure during actual execution.
	// Skip sticky provider-quota locks (task #92) — a second attempt cannot
	// clear an external usage cap.
	if result.Status == "failed" && task.PriorSessionID != "" && result.SessionID == "" &&
		!taskfailure.IsStickyProviderQuotaLock(result.Error, "") {
		firstUsage := result.Usage
		taskLog.Warn("session resume failed, retrying with fresh session", "error", result.Error)
		execOpts.ResumeSessionID = ""
		clearCanonicalResumeIfPresent(backend)
		// Fresh session: reinject full turn-scope + agent-scope memory.
		injectedTurnMemories = allTurnMemories
		taskCtx.AgentMemories = allTurnMemories
		prompt = execenv.RenderTurnContext(taskCtx) + BuildPrompt(task, provider, agentRootPath)
		appendAgentScopeSystemPrompt(&execOpts, agentScopeMemories)
		retryResult, retryTools, retryErr := d.executeAndDrainForTask(ctx, backend, prompt, execOpts, taskLog, task)
		if retryErr != nil {
			taskLog.Error("fresh session also failed to start", "error", retryErr)
		} else {
			result = retryResult
			result.Usage = mergeUsage(firstUsage, result.Usage)
			tools = retryTools
		}
	}
	if d.turnScopeMemory != nil {
		markSessionID := strings.TrimSpace(task.PriorSessionID)
		if markSessionID == "" {
			markSessionID = strings.TrimSpace(result.SessionID)
		}
		if markSessionID != "" {
			d.turnScopeMemory.markInjected(issueTurnScopeSessionKey(agentID, task.RuntimeID, markSessionID), injectedTurnMemories)
		}
	}
	if runtimeLease != nil {
		runtimeLease.releaseForResult(result.Status, nil)
	}
	elapsed := time.Since(taskStart).Round(time.Second)
	taskLog.Info("agent finished",
		"status", result.Status,
		"duration", elapsed.String(),
		"tools", tools,
	)
	taskLog.Debug("agent result detail",
		"status", result.Status,
		"output_bytes", len(result.Output),
		"session_id", result.SessionID,
		"models_with_usage", len(result.Usage),
		"agent_error", result.Error,
	)

	// Convert agent usage map to task usage entries.
	var usageEntries []TaskUsageEntry
	for model, u := range result.Usage {
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
			continue
		}
		usageEntries = append(usageEntries, TaskUsageEntry{
			Provider:         provider,
			Model:            model,
			InputTokens:      u.InputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		})
	}

	runtimeStats := runtimeStatsFromAgent(result.RuntimeStats)
	// Cursor / Codex / Claude etc. often only fill Result.Usage (not Pi-style
	// RuntimeStats). Map usage into session runtime_stats so chat bubbles can
	// show a token bar without a provider-specific UI path.
	if runtimeStats == nil {
		runtimeStats = runtimeStatsFromUsage(provider, result.Usage)
	}
	transportAttempted, attemptErr := transportAttemptWasRecorded(transportAttemptPath)
	if attemptErr != nil {
		taskLog.Warn("failed to inspect transport attempt marker", "path", transportAttemptPath, "error", attemptErr)
	}

	output := result.Output
	var internalOutput json.RawMessage
	if restrictedExecution && result.Status == "completed" {
		if outputTokens := totalTaskOutputTokens(usageEntries); outputTokens > int64(restrictedMaxOutputTokens) {
			return TaskResult{
				Status:                 "blocked",
				Comment:                fmt.Sprintf("restricted execution output used %d tokens; limit is %d", outputTokens, restrictedMaxOutputTokens),
				FailureReason:          "restricted_output_token_limit",
				Usage:                  usageEntries,
				RuntimeStats:           runtimeStats,
				OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonRestrictedExecutionProfile,
			}, nil
		}
		parsedOutput, parseErr := parseRestrictedExecutionOutput(profile, output)
		if parseErr != nil {
			return TaskResult{
				Status:                 "blocked",
				Comment:                parseErr.Error(),
				FailureReason:          "restricted_output_invalid",
				Usage:                  usageEntries,
				RuntimeStats:           runtimeStats,
				OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonRestrictedExecutionProfile,
			}, nil
		}
		internalOutput, parseErr = bindRestrictedOutputMetadata(profile, parsedOutput, model, usageEntries, task)
		if parseErr != nil {
			return TaskResult{
				Status:                 "blocked",
				Comment:                parseErr.Error(),
				FailureReason:          "restricted_output_invalid",
				Usage:                  usageEntries,
				RuntimeStats:           runtimeStats,
				OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonRestrictedExecutionProfile,
			}, nil
		}
	}
	var parts []protocol.MessagePart
	var reaction *protocol.ChatReactionPayload
	outputAction := ""
	outputTarget := ""
	outputType := ""

	switch result.Status {
	case "completed":
		if reason, ok := classifyFailedOutput(output); ok {
			taskLog.Warn("agent emitted a provider failure as final output", "failure_reason", reason)
			return TaskResult{
				Status:        "blocked",
				Comment:       output,
				SessionID:     result.SessionID,
				WorkDir:       env.AgentRoot,
				Usage:         usageEntries,
				RuntimeStats:  runtimeStats,
				FailureReason: reason,
			}, nil
		}
		if output == "" && len(parts) == 0 && reaction == nil {
			// The agent completed successfully but produced no text output.
			// This is valid — the agent may have done all its work via tool
			// calls (e.g. posting comments via CLI, pushing code). Treat as
			// a normal completion so the task is not incorrectly marked as
			// blocked.
			return TaskResult{
				Status:             "completed",
				Comment:            "",
				Action:             protocol.ChatOutputActionNoReply,
				Type:               protocol.ChatOutputKindNoReply,
				SessionID:          result.SessionID,
				WorkDir:            env.AgentRoot,
				Usage:              usageEntries,
				RuntimeStats:       runtimeStats,
				TransportAttempted: transportAttempted,
			}, nil
		}
		if len(parts) == 0 && reaction == nil && isSilentNoReplyOutput(output) {
			taskLog.Info("agent produced silent no-reply status output; completing as structured no_reply")
			return TaskResult{
				Status:             "completed",
				Comment:            "",
				Action:             protocol.ChatOutputActionNoReply,
				Type:               protocol.ChatOutputKindNoReply,
				SessionID:          result.SessionID,
				WorkDir:            env.AgentRoot,
				Usage:              usageEntries,
				RuntimeStats:       runtimeStats,
				TransportAttempted: transportAttempted,
			}, nil
		}
		// Detect "poisoned" terminal output: the agent didn't reach a real
		// conclusion but emitted a known fallback marker (iteration limit,
		// fallback meta message). Route through the blocked path with a
		// specific failure_reason so the server can exclude this session
		// from the (agent_id, issue_id) resume lookup — otherwise a manual
		// rerun would inherit the same poisoned session and reproduce the
		// same bad output.
		if reason, ok := classifyPoisonedOutput(output); ok {
			taskLog.Warn("agent finished with poisoned fallback output, classifying as blocked",
				"failure_reason", reason,
			)
			return TaskResult{
				Status:        "blocked",
				Comment:       output,
				SessionID:     result.SessionID,
				WorkDir:       env.AgentRoot,
				Usage:         usageEntries,
				RuntimeStats:  runtimeStats,
				FailureReason: reason,
			}, nil
		}
		return TaskResult{
			Status:             "completed",
			Comment:            output,
			Action:             outputAction,
			Target:             outputTarget,
			Type:               outputType,
			Parts:              parts,
			Reaction:           reaction,
			SessionID:          result.SessionID,
			WorkDir:            env.AgentRoot,
			Usage:              usageEntries,
			RuntimeStats:       runtimeStats,
			InternalOutput:     internalOutput,
			TransportAttempted: transportAttempted,
		}, nil
	case "timeout":
		// Surface session_id/work_dir so the chat resume pointer is kept
		// in sync even when the agent times out after building a session.
		// We mark as "blocked" (not a hard error return) so handleTask
		// goes through the FailTask path that forwards session info.
		comment := result.Error
		if comment == "" {
			comment = fmt.Sprintf("%s timed out after %s", provider, d.cfg.AgentTimeout)
		}
		failureReason := "timeout"
		if reason, ok := classifyResumeUnsafeTimeout(provider, comment); ok {
			taskLog.Warn("agent timed out with resume-unsafe session, classifying as blocked",
				"failure_reason", reason,
			)
			failureReason = reason
		}
		return TaskResult{
			Status:        "blocked",
			Comment:       comment,
			SessionID:     result.SessionID,
			WorkDir:       env.AgentRoot,
			FailureReason: failureReason,
			Usage:         usageEntries,
			RuntimeStats:  runtimeStats,
		}, nil
	case "cancelled":
		// Server cancelled the task (e.g. issue reassignment, user cancel).
		// handleTask's cancelledByPoll branch already discards this result,
		// so this case is mainly defensive — and preserves the "cancelled"
		// status string for the "agent finished" log line so operators can
		// distinguish "task cancelled by server" from a real timeout.
		return TaskResult{
			Status:       "cancelled",
			Comment:      "task cancelled by server",
			SessionID:    result.SessionID,
			WorkDir:      env.AgentRoot,
			Usage:        usageEntries,
			RuntimeStats: runtimeStats,
		}, nil
	default:
		errMsg := result.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("%s execution %s", provider, result.Status)
		}
		// Forward SessionID/WorkDir on the blocked path: backends commonly
		// emit a real session_id before failing (rate-limit, tool error,
		// model reject, …). Without this the chat_session resume pointer
		// would either be left stale or overwritten with NULL on the
		// server, causing the next chat turn to lose context.
		//
		// Classify resume-unsafe no-progress failures and upstream API
		// 400 invalid_request_error failures with a dedicated
		// failure_reason so GetLastTaskSession excludes the task from the
		// (agent_id, issue_id) resume lookup. Without this classifier a
		// corrupt image, oversized payload, or provider session that never
		// reaches first-turn progress can permanently block the issue:
		// every follow-up task resumes the same bad state and hits the
		// same failure.
		failureReason := classifyAgentRunFailureReason(provider, errMsg, taskLog)
		return TaskResult{
			Status:        "blocked",
			Comment:       errMsg,
			SessionID:     result.SessionID,
			WorkDir:       env.AgentRoot,
			Usage:         usageEntries,
			RuntimeStats:  runtimeStats,
			FailureReason: failureReason,
		}, nil
	}
}

func transportAttemptWasRecorded(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("transport attempt marker is not a regular file")
	}
	return true, nil
}

func runtimeStatsFromAgent(stats *agent.RuntimeTokenStats) *protocol.RuntimeTokenStats {
	if stats == nil {
		return nil
	}
	return &protocol.RuntimeTokenStats{
		Provider:              stats.Provider,
		Model:                 stats.Model,
		InputTokens:           stats.InputTokens,
		OutputTokens:          stats.OutputTokens,
		CacheReadTokens:       stats.CacheReadTokens,
		CacheWriteTokens:      stats.CacheWriteTokens,
		TotalTokens:           stats.TotalTokens,
		CostUSD:               stats.CostUSD,
		ContextTokens:         stats.ContextTokens,
		ContextWindow:         stats.ContextWindow,
		ContextPercent:        stats.ContextPercent,
		AutoCompactionEnabled: stats.AutoCompactionEnabled,
	}
}

// runtimeStatsFromUsage builds session telemetry from per-model TokenUsage when
// the backend did not supply native RuntimeStats (common for Cursor stream-json).
func runtimeStatsFromUsage(provider string, usage map[string]agent.TokenUsage) *protocol.RuntimeTokenStats {
	if len(usage) == 0 {
		return nil
	}
	var (
		model                          string
		in, out, cacheRead, cacheWrite int64
		bestTotal                      int64
	)
	for m, u := range usage {
		total := u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
		if total == 0 {
			continue
		}
		in += u.InputTokens
		out += u.OutputTokens
		cacheRead += u.CacheReadTokens
		cacheWrite += u.CacheWriteTokens
		if total > bestTotal {
			bestTotal = total
			model = m
		}
	}
	total := in + out + cacheRead + cacheWrite
	if total == 0 {
		return nil
	}
	return &protocol.RuntimeTokenStats{
		Provider:         provider,
		Model:            model,
		InputTokens:      in,
		OutputTokens:     out,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		TotalTokens:      total,
	}
}

func classifyAgentRunFailureReason(provider, errMsg string, taskLog *slog.Logger) string {
	if failureReason, ok := classifyResumeUnsafeToolFailure(provider, errMsg); ok {
		taskLog.Warn("agent failed with resume-unsafe tool permission error, classifying as blocked",
			"failure_reason", failureReason,
		)
		return failureReason
	}
	if failureReason, ok := classifyResumeUnsafeTimeout(provider, errMsg); ok {
		taskLog.Warn("agent failed with resume-unsafe no-progress error, classifying as blocked",
			"failure_reason", failureReason,
		)
		return failureReason
	}
	if failureReason, ok := classifyPoisonedError(errMsg); ok {
		taskLog.Warn("agent failed with poisoned API error, classifying as blocked",
			"failure_reason", failureReason,
		)
		return failureReason
	}
	// MUL-2946: classifyPoisonedError only matches the session-poisoning
	// Anthropic 400 shape. Everything else falls through to
	// taskfailure.Classify, which maps the raw error string to one of the
	// agent_error.* sub-reasons (provider auth, capacity, context overflow,
	// runner crash, …) or to ReasonAgentUnknown. This keeps failure_reason in
	// the canonical refined taxonomy at write time instead of waiting on the
	// MUL-1949 offline backfill to re-classify after the fact.
	return taskfailure.Classify(errMsg).String()
}

// executeAndDrainForTask runs one inbox/issue task. It reports task messages
// and logs only. User-facing Runner Activity stays on the resident Message
// seam; this path must not Observe or overwrite that timeline.
func (d *Daemon) executeAndDrainForTask(ctx context.Context, backend agent.Backend, prompt string, opts agent.ExecOptions, taskLog *slog.Logger, task Task) (agent.Result, int32, error) {
	taskID := task.ID
	// Wrap the caller's ctx so the agent subprocess and drain loop share
	// cancellation. Task execution no longer owns a stalled watchdog;
	// resident Message delivery owns that recovery policy.
	agentCtx, agentCancel := context.WithCancel(ctx)
	defer agentCancel()

	session, err := backend.Execute(agentCtx, prompt, opts)
	if err != nil {
		taskLog.Debug("backend execute returned error", "error", err)
		return agent.Result{}, 0, err
	}
	taskLog.Debug("backend started, draining messages")

	// Bound the drain loop only when there is a wall-clock cap. With a positive
	// opts.Timeout, give the drain a slightly longer deadline than the backend
	// so it can still collect the backend's own timeout Result if the scanner
	// is stuck on a hung stdout pipe (the extra 30 s covers cleanup after the
	// backend's own deadline fires). With no cap (opts.Timeout <= 0) the
	// inactivity watchdog is the only liveness net, so the drain must NOT
	// impose its own deadline either — otherwise an actively streaming long run
	// would be cut off here regardless of progress (MUL-3064).
	var drainCtx context.Context
	var drainCancel context.CancelFunc
	if opts.Timeout > 0 {
		drainCtx, drainCancel = context.WithTimeout(agentCtx, opts.Timeout+30*time.Second)
	} else {
		drainCtx, drainCancel = context.WithCancel(agentCtx)
	}
	defer drainCancel()

	var toolCount atomic.Int32

	go func() {
		var seq atomic.Int32
		var mu sync.Mutex
		var trajectory taskMessageTrajectoryBuffer
		var batch []TaskMessageData
		callIDToTool := map[string]string{}
		emitTrajectory := func(kind, content, lineage string) {
			s := seq.Add(1)
			batch = append(batch, TaskMessageData{
				Seq:     int(s),
				Type:    kind,
				Content: content,
				Lineage: lineage,
			})
		}
		flush := func(force bool) {
			mu.Lock()
			trajectory.flush(time.Now(), force, emitTrajectory)
			toSend := batch
			batch = nil
			mu.Unlock()

			if len(toSend) > 0 {
				sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var err error
				if task.isInboxTask() {
					err = d.client.ReportAgentInboxMessages(sendCtx, *task.InboxEvent, toSend)
				} else {
					err = errors.New("task is missing its canonical inbox lease")
				}
				if err != nil {
					taskLog.Debug("failed to report task messages", "inbox_task", task.isInboxTask(), "error", err)
				} else {
					taskLog.Debug("reported task messages", "inbox_task", task.isInboxTask(), "count", len(toSend), "last_seq", toSend[len(toSend)-1].Seq)
				}
				cancel()
			}
		}

		ticker := time.NewTicker(taskMessageFlushInterval)
		defer ticker.Stop()

		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ticker.C:
					flush(false)
				case <-done:
					return
				}
			}
		}()

		var sessionPinned atomic.Bool
		for {
			select {
			case msg, ok := <-session.Messages:
				if !ok {
					goto drainDone
				}
				switch msg.Type {
				case agent.MessageStatus:
					// Pin the provider-CLI resume token the moment the backend
					// reports a session_id — earliest point it's known, well
					// before the task completes. Without this, a daemon crash
					// mid-task loses session_id/work_dir entirely (previously
					// only ever reported at completion), so the server-side
					// retry starts the agent completely fresh with no memory
					// of any work already done. Only fire once per task
					// (CompareAndSwap false->true) since backends may repeat
					// MessageStatus on every turn. Best-effort and bounded: a
					// failure here must never block task execution, it only
					// costs the resume pointer for this cycle.
					// Not Multica agent_session / agent_session_id (inbox
					// wake/drain UUID) — different "session", task #109.
					if msg.SessionID != "" && sessionPinned.CompareAndSwap(false, true) {
						// Pin is task-scoped (session + cwd). Do not write the
						// DaemonCore resume cache: that cache is the resident
						// RuntimeSession, same as Raft's start.config.sessionId.
						// An issue-run cwd would poison the next agent-home restart.
						pinCtx, pinCancel := context.WithTimeout(context.Background(), 5*time.Second)
						if err := d.client.PinTaskSession(pinCtx, taskID, msg.SessionID, opts.Cwd, task.RuntimeID); err != nil {
							taskLog.Warn("pin task session failed (task still runs; resume pointer lost for this cycle)", "error", err)
						}
						pinCancel()
					}
				case agent.MessageToolUse:
					n := toolCount.Add(1)
					taskLog.Info(fmt.Sprintf("tool #%d: %s", n, msg.Tool))
					d.frictionTrackerForTask(taskID).ObserveToolUse(msg.Tool, frictionToolInputHash(msg.Input))
					if msg.CallID != "" {
						mu.Lock()
						callIDToTool[msg.CallID] = msg.Tool
						mu.Unlock()
					}
					mu.Lock()
					trajectory.flush(time.Now(), true, emitTrajectory)
					s := seq.Add(1)
					batch = append(batch, TaskMessageData{
						Seq:    int(s),
						Type:   "tool_use",
						Tool:   msg.Tool,
						CallID: msg.CallID,
						Input:  msg.Input,
					})
					mu.Unlock()
				case agent.MessageToolResult:
					output := msg.Output
					if len(output) > 8192 {
						output = output[:8192]
					}
					toolName := msg.Tool
					if toolName == "" && msg.CallID != "" {
						mu.Lock()
						toolName = callIDToTool[msg.CallID]
						mu.Unlock()
					}
					mu.Lock()
					trajectory.flush(time.Now(), true, emitTrajectory)
					s := seq.Add(1)
					// #103 temporary: when MULTICA_DEBUG_TOOL_RESULT_INPUT=1, log
					// whether completed tool Input is empty (backfill depends on it).
					// No Input contents — keys/emptiness only. Remove after dig.
					if strings.TrimSpace(os.Getenv("MULTICA_DEBUG_TOOL_RESULT_INPUT")) == "1" {
						inputEmpty := len(msg.Input) == 0
						hasPath := toolResultInputHasPath(msg.Input)
						taskLog.Info("tool_result observed",
							"seq", s,
							"tool", toolName,
							"call_id", msg.CallID,
							"input_empty", inputEmpty,
							"input_has_path", hasPath,
							"input_key_count", len(msg.Input),
						)
					} else {
						taskLog.Info("tool_result observed", "seq", s, "tool", toolName, "call_id", msg.CallID)
					}
					batch = append(batch, TaskMessageData{
						Seq:    int(s),
						Type:   "tool_result",
						Tool:   toolName,
						CallID: msg.CallID,
						Input:  msg.Input, // LRM-689: backfill carrier when started had empty args
						Output: output,
					})
					mu.Unlock()
				case agent.MessageThinking:
					if msg.Content != "" {
						d.frictionTrackerForTask(taskID).ObserveProgress()
						mu.Lock()
						trajectory.append("thinking", msg.Content, msg.Lineage, time.Now(), emitTrajectory)
						mu.Unlock()
					}
				case agent.MessageCompactionStarted, agent.MessageCompactionFinished:
					mu.Lock()
					trajectory.flush(time.Now(), true, emitTrajectory)
					s := seq.Add(1)
					messageType := "compaction_started"
					if msg.Type == agent.MessageCompactionFinished {
						messageType = "compaction_finished"
					}
					batch = append(batch, TaskMessageData{Seq: int(s), Type: messageType, Content: msg.Content})
					mu.Unlock()
				case agent.MessageText:
					if msg.Content != "" {
						taskLog.Debug("agent", "text", truncateLog(msg.Content, 200))
						d.frictionTrackerForTask(taskID).ObserveProgress()
						mu.Lock()
						trajectory.append("text", msg.Content, msg.Lineage, time.Now(), emitTrajectory)
						mu.Unlock()
					}
				case agent.MessageError:
					taskLog.Error("agent error", "content", msg.Content)
					d.frictionTrackerForTask(taskID).ObserveError()
					mu.Lock()
					trajectory.flush(time.Now(), true, emitTrajectory)
					s := seq.Add(1)
					batch = append(batch, TaskMessageData{
						Seq:     int(s),
						Type:    "error",
						Content: msg.Content,
					})
					mu.Unlock()
				case agent.MessageLog:
					if msg.Content != "" {
						taskLog.Debug("agent log", "level", msg.Level, "content", truncateLog(msg.Content, 200))
						mu.Lock()
						trajectory.flush(time.Now(), true, emitTrajectory)
						s := seq.Add(1)
						batch = append(batch, TaskMessageData{
							Seq:     int(s),
							Type:    "log",
							Content: msg.Content,
						})
						mu.Unlock()
					}
				}
			case <-drainCtx.Done():
				goto drainDone
			}
		}
	drainDone:
		close(done)
		flush(true)
	}()

	select {
	case result := <-session.Result:
		return result, toolCount.Load(), nil
	case <-drainCtx.Done():
		// Distinguish external cancellation (e.g. server-initiated cancel
		// because the issue was reassigned, or the user invoked CancelTask)
		// from genuine drain-deadline timeouts. context.Canceled means the
		// upstream runCtx fired runCancel(); context.DeadlineExceeded is the
		// drain deadline expiring on its own.
		if errors.Is(drainCtx.Err(), context.Canceled) {
			return agent.Result{
				Status: "cancelled",
				Error:  "task cancelled by upstream context (server cancel or daemon shutdown)",
			}, toolCount.Load(), nil
		}
		return agent.Result{
			Status: "timeout",
			Error:  "agent did not produce result within drain timeout",
		}, toolCount.Load(), nil
	}
}

func mergeUsage(a, b map[string]agent.TokenUsage) map[string]agent.TokenUsage {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	merged := make(map[string]agent.TokenUsage, len(a)+len(b))
	for model, u := range a {
		merged[model] = u
	}
	for model, u := range b {
		existing := merged[model]
		existing.InputTokens += u.InputTokens
		existing.OutputTokens += u.OutputTokens
		existing.CacheReadTokens += u.CacheReadTokens
		existing.CacheWriteTokens += u.CacheWriteTokens
		merged[model] = existing
	}
	return merged
}

// shortID returns the first 8 characters of an ID for readable logs.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// truncateLog truncates a string to maxLen, appending "…" if truncated.
// Also collapses newlines to spaces for single-line log output.
func truncateLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func convertSkillsForEnv(skills []SkillData) []execenv.SkillContextForEnv {
	if len(skills) == 0 {
		return nil
	}
	result := make([]execenv.SkillContextForEnv, len(skills))
	for i, s := range skills {
		desc := strings.TrimSpace(s.Description)
		if desc == "" && strings.TrimSpace(s.Content) != "" {
			// Progressive skill index needs a description to decide when to
			// open SKILL.md. Older/manual rows sometimes store description
			// only inside frontmatter — recover it so the brief is useful.
			if _, fmDesc := skillpkg.ParseSkillFrontmatter(s.Content); strings.TrimSpace(fmDesc) != "" {
				desc = strings.TrimSpace(fmDesc)
			}
		}
		result[i] = execenv.SkillContextForEnv{
			Name:        s.Name,
			Description: desc,
			Content:     s.Content,
		}
		for _, f := range s.Files {
			result[i].Files = append(result[i].Files, execenv.SkillFileContextForEnv{
				Path:    f.Path,
				Content: f.Content,
			})
		}
	}
	return result
}

func convertMemoriesForEnv(agent *AgentData) []execenv.MemoryContextForEnv {
	if agent == nil || len(agent.Memories) == 0 {
		return nil
	}
	result := make([]execenv.MemoryContextForEnv, 0, len(agent.Memories))
	for _, memory := range agent.Memories {
		result = append(result, execenv.MemoryContextForEnv{
			Name: memory.Name, Content: memory.Content, Scope: memory.Scope,
			SubjectType: memory.SubjectType, SubjectID: memory.SubjectID,
		})
	}
	return result
}

func resolvedTaskAgentID(task Task) string {
	if task.Agent != nil && task.Agent.ID != "" {
		return task.Agent.ID
	}
	return task.AgentID
}

func addMulticaAgentEnv(env map[string]string, cfg Config, workspaceID, agentID string) {
	if workspaceID == "" || agentID == "" {
		return
	}
	agentRoot := agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID)
	env["MULTICA_AGENT_ROOT"] = agentRoot
}

func addPiMemoryFastModeEnv(env map[string]string) {
	// Multica-managed Pi runs should keep explicit memory tools available, but
	// skip the expensive automatic shutdown pipeline (session summary, learning,
	// qmd refresh, remote sync). These are defaults so custom_env can opt back in
	// for both one-shot and resident runtimes.
	defaults := map[string]string{
		"PI_MEMORY_FINALIZE":                     "off",
		"PI_MEMORY_BACKGROUND_SHUTDOWN":          "off",
		"PI_MEMORY_LEARNING":                     "off",
		"PI_MEMORY_SKILL_DRAFTS":                 "off",
		"PI_MEMORY_QMD_UPDATE":                   "off",
		"PI_MEMORY_AUTO_SYNC":                    "0",
		"PI_MEMORY_AUTO_SYNC_PULL":               "0",
		"PI_MEMORY_AUTO_SYNC_PULL_ON_START":      "0",
		"PI_MEMORY_AUTO_SYNC_UPLOAD":             "0",
		"PI_MEMORY_AUTO_SYNC_UPLOAD_ON_SHUTDOWN": "0",
		"PI_MEMORY_NO_SEARCH":                    "1",
		"PI_MEMORY_REVIEW_STARTUP_HINT":          "0",
	}
	for key, value := range defaults {
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
}

// AReaL RL proxy runtime override (§4.4). A trained task carries an
// `areal_proxy` object in its context (written server-side by the session-open
// hook); the server surfaces it on the claim response and the daemon consumes
// it here to route the runtime through the RL bridge.
//
// pi's real arg/env contract (confirmed against River2.0/packages/coding-agent,
// the pi CLI source):
//   - provider/model: pkg/agent/pi.go buildPiArgs splits ExecOptions.Model on
//     "/" into `--provider <p> --model <m>` (pi src/cli/args.ts:87,89 parse
//     both). So Model="areal/areal-default" yields
//     `--provider areal --model areal-default`.
//   - api-key: pi's `--api-key <key>` flag (src/cli/args.ts:91 ->
//     src/main.ts:697 authStorage.setRuntimeApiKey(model.provider, key)).
//     buildPiArgs does NOT emit it and it is NOT in piBlockedArgs, so we inject
//     it via CustomArgs. This matches spec §1's literal
//     `pi -p --provider areal --model areal-default --api-key <proxy_key>`.
//   - base_url: pi has NO --base-url flag and NO generic per-provider base-url
//     env var (packages/ai/src/env-api-keys.ts maps only built-in providers;
//     "areal" is not one). A custom provider's base_url is supplied via pi's
//     models.json / registerProvider, both of which support "$ENV" references.
//     We therefore export base_url as AREAL_PROXY_BASE_URL, which the
//     deployment's `areal` provider entry in models.json references; registering
//     that entry on the runtime host is Task 8's job. Injected into agentEnv
//     alongside the other provider creds (mirrors CustomEnv's ANTHROPIC_BASE_URL).
const (
	arealProxyProvider      = "openai"
	arealProxyModel         = "areal-default"
	arealProxyBaseURLEnvVar = "AREAL_PROXY_BASE_URL"
)

// arealProxyExecOverride computes the runtime overrides for a task routed
// through the AReaL RL proxy. It returns the "provider/model" string
// buildPiArgs maps to `--provider`/`--model`, the api-key custom args pi reads
// via its `--api-key` flag, and the base_url env var (key + value) the
// deployment's models.json references. ok is false when the task carries no
// proxy config, leaving the caller's runtime configuration untouched.
func arealProxyExecOverride(p *ArealProxy) (model string, extraArgs []string, envKey, envVal string, ok bool) {
	if p == nil {
		return "", nil, "", "", false
	}
	provider := p.Provider
	if provider == "" {
		provider = arealProxyProvider
	}
	m := p.Model
	if m == "" {
		m = arealProxyModel
	}
	model = provider + "/" + m
	if p.APIKey != "" {
		extraArgs = []string{"--api-key", p.APIKey}
	}
	if p.BaseURL != "" {
		envKey, envVal = arealProxyBaseURLEnvVar, p.BaseURL
	}
	return model, extraArgs, envKey, envVal, true
}

// isBlockedEnvKey returns true if the key must not be overridden by user-
// configured custom_env. This prevents accidental or malicious override of
// daemon-internal variables and critical system paths.
func injectScopedSecrets(agentEnv map[string]string, task Task, logger *slog.Logger) {
	// Runtime-level env is the machine-default base layer; agent and scoped
	// secrets override it on key collision (they are more specific).
	injectRuntimeCustomEnv(agentEnv, task.RuntimeEnv, logger)

	secrets := make([]secretscoped.Secret, 0, 8)
	if task.Agent != nil {
		secrets = append(secrets, secretscoped.FromAgentEnv(task.Agent.CustomEnv)...)
	}
	for _, secret := range task.ScopedSecrets {
		secrets = append(secrets, secretscoped.Secret{
			Key:       secret.Key,
			Value:     secret.Value,
			Scope:     secret.Scope,
			ChannelID: secret.ChannelID,
			ProjectID: secret.ProjectID,
		})
	}
	filtered := secretscoped.Filter(secrets, secretscoped.TaskScope{
		ChannelID: task.ChannelID,
		ProjectID: task.ProjectID,
	})
	for key, value := range filtered {
		if isBlockedEnvKey(key) {
			if logger != nil {
				logger.Warn("scoped_secret/custom_env: blocked key skipped", "key", key)
			}
			continue
		}
		agentEnv[key] = value
	}
}

// injectAgentCustomEnv applies the agent-owned portion of environment
// configuration without manufacturing a Task scope. Resident Message runtimes
// must never inherit channel- or project-scoped secrets.
func injectAgentCustomEnv(agentEnv map[string]string, agentData *AgentData, logger *slog.Logger) {
	if agentData == nil {
		return
	}
	filtered := secretscoped.Filter(secretscoped.FromAgentEnv(agentData.CustomEnv), secretscoped.TaskScope{})
	for key, value := range filtered {
		if isBlockedEnvKey(key) {
			if logger != nil {
				logger.Warn("custom_env: blocked key skipped", "key", key)
			}
			continue
		}
		agentEnv[key] = value
	}
}

// injectRuntimeCustomEnv applies the machine-default environment layer for a
// runtime. It is deliberately agent-scoped (no channel/project filtering) so
// every agent on the runtime inherits it; agent custom_env is injected after
// and overrides on key collision.
func injectRuntimeCustomEnv(agentEnv map[string]string, runtimeEnv map[string]string, logger *slog.Logger) {
	if len(runtimeEnv) == 0 {
		return
	}
	filtered := secretscoped.Filter(secretscoped.FromAgentEnv(runtimeEnv), secretscoped.TaskScope{})
	for key, value := range filtered {
		if isBlockedEnvKey(key) {
			if logger != nil {
				logger.Warn("runtime_env: blocked key skipped", "key", key)
			}
			continue
		}
		agentEnv[key] = value
	}
}

func isBlockedEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "MULTICA_") {
		return true
	}
	switch upper {
	case "HOME", "PATH", "USER", "SHELL", "TERM", "CODEX_HOME", "OPENCLAW_CONFIG_PATH", "OPENCLAW_INCLUDE_ROOTS",
		"MULTICA_AGENT_ROOT":
		return true
	}
	return false
}

func defaultArgsForProvider(cfg Config, provider string) []string {
	var args []string
	switch provider {
	case agent.ProviderClaude:
		args = cfg.ClaudeArgs
	case agent.ProviderCodex:
		args = cfg.CodexArgs
	default:
		return nil
	}
	return append([]string(nil), args...)
}
