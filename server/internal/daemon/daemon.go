package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/secretscoped"
	skillpkg "github.com/multica-ai/multica/server/internal/skill"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// errAgentReassignedElsewhere marks ensureTaskAgentCredential's terminal
// classification of a 403 "agent is not bound to this runtime" response: the
// agent this task names has been reassigned to a different runtime, and no
// amount of retrying from this daemon will change that. Callers use this to
// report a clear, distinct failure reason instead of the generic
// "credential_unavailable" every other credential error produces.
var errAgentReassignedElsewhere = errors.New("agent is no longer bound to this daemon's runtime (reassigned elsewhere)")

// errRuntimeTransitionInProgress marks ensureTaskAgentCredential's
// classification of the task #38 "runtime_transition_in_progress" 403 —
// this daemon's runtime was reassigned within the server's grace window and
// the new runtime hasn't confirmed the handoff yet. Unlike
// errAgentReassignedElsewhere this is expected to self-resolve shortly
// (either the new runtime confirms, or the grace window expires and a later
// attempt gets the terminal classification instead): callers must retry
// quietly, without reporting a failed task result or logging above debug —
// surfacing this to the user would recreate exactly the "why do I see scary
// errors during a normal machine move" complaint task #38 exists to fix.
var errRuntimeTransitionInProgress = errors.New("agent runtime transition in progress")

const (
	taskMessageFlushInterval            = 200 * time.Millisecond
	taskMessageTrajectoryCoalesceWindow = 350 * time.Millisecond
	taskMessageTrajectoryMaxChars       = 2000
	// recoveryFlushTimeout bounds a background recovery Flush so a busy /
	// idle-input-unsupported resident runtime can never stall the workspace
	// runner read loop while waiting on the provider (see
	// handleMessageRecoveryPageWithSend). The coordinator's pending-notice
	// retry completes the batch if the runtime is transiently busy.
	recoveryFlushTimeout = 30 * time.Second
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

var (
	isBrewInstall        = cli.IsBrewInstall
	getBrewPrefix        = cli.GetBrewPrefix
	matchKnownBrewPrefix = cli.MatchKnownBrewPrefix

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

// Daemon is the local agent runtime that polls for and executes tasks.
type Daemon struct {
	cfg    Config
	client *Client
	logger *slog.Logger

	messageCoordinatorMu    sync.RWMutex
	messageCoordinators     map[string]*MessageCoordinator
	messageRuntimeIDs       map[string]string
	messageSendMu           sync.Mutex
	messageSends            map[string]int
	agentProcessManagers    map[string]*agentProcessManager
	agentActivityProducers  map[string]*agentActivityProducer
	runnerMessageTransports map[string]workspaceRunnerMessageTransport
	runnerMessageGeneration map[string]uint64
	lifecycleDiagnostics    *lifecycleDiagnosticWriter
	machineUpgradeLog       *machineUpgradeEventLog
	runnerInstanceID        string

	mu           sync.Mutex
	workspaces   map[string]*workspaceState
	runtimeIndex map[string]Runtime // runtimeID -> Runtime for provider lookups
	reloading    sync.Mutex         // prevents concurrent workspace syncs
	runtimeSet   *runtimeSetWatcher // multi-subscriber pub/sub for runtime-set changes

	versionsMu    sync.RWMutex      // guards agentVersions
	agentVersions map[string]string // provider -> detected CLI version (set during registration)

	wsHBMu      sync.RWMutex         // guards wsHBLastAck
	wsHBLastAck map[string]time.Time // runtime_id -> last successful WS heartbeat ack timestamp

	// wsConnState is the task-wakeup WebSocket lifecycle label for internal
	// observability (connecting|open|backoff|closed). Not user-facing Activity.
	wsConnStateMu sync.RWMutex
	wsConnState   string

	reminderCache                    *reminderCache
	reminderAgents                   *reminderAgentManager
	reminderWSMu                     sync.RWMutex
	reminderWrites                   chan<- []byte
	reminderWSDone                   <-chan struct{}
	reminderClose                    func() error
	reminderGateMu                   sync.Mutex
	reminderLifecycleReplayInFlight  bool
	reminderReplayComplete           bool
	reminderProjectionReplayInFlight bool
	reminderProjectionReplayPending  bool
	reminderPendingSnapshots         map[string]struct{}

	// runtimeGoneMu guards runtimeGoneInflight, reregisterNextAttempt, and
	// reregisterLastCompletedAt. The state lets heartbeat / poller / WS-ack
	// handlers converge on a single recovery path when they each detect that a
	// runtime row was deleted server-side without three of them stampeding
	// registerRuntimesForWorkspace.
	runtimeGoneMu             sync.Mutex
	runtimeGoneInflight       map[string]struct{}  // runtime_id -> currently recovering
	reregisterNextAttempt     map[string]time.Time // workspace_id -> earliest time the next re-register attempt may run
	reregisterLastCompletedAt map[string]time.Time // workspace_id -> wall-clock at which the last SUCCESSFUL re-register call returned (failures intentionally not stamped — see recordRegisterCompletion)

	cancelFunc    context.CancelFunc // set by Run(); called by triggerRestart
	rootCtx       context.Context    // set by Run(); used by long-running recoveries that must survive per-runtime ctx cancellation
	restartBinary string             // non-empty after a successful update; path to the new binary
	updating      atomic.Bool        // prevents concurrent update attempts
	activeTasks   atomic.Int64       // number of tasks currently in handleTask; exposed via /health
	// activeInboxTurns maps agent_id → primary inbox lease for the in-flight
	// turn. Credential-proxy message send uses this to stamp batch
	// client_message_id when the CLI omitted MULTICA_TURN_* (Alice ①:
	// inbox-turn sends only; non-turn/draft/proactive keep the legacy path).
	activeInboxTurns sync.Map // string agentID -> AgentInboxLease
	// machineUpgradeTaskCancels contains only task contexts created by this
	// daemon. A Machine Upgrade may ask those managed turns to stop; it must
	// never infer ownership of, or signal, an arbitrary local process.
	machineUpgradeTaskMu      sync.Mutex
	machineUpgradeTaskCancels map[int64]context.CancelFunc
	taskSlotCounter           atomic.Int64                  // ever-increasing task sequence number exposed as MULTICA_TASK_SLOT (informational only, tasks are not capacity-limited — see nextTaskSlot)
	ready                     atomic.Bool                   // false until preflight completes; gates /health status (starting -> running)
	updateObservation         *updateObservationCoordinator // daemon-resolved auto/server update truth shared by every transport
	// machineUpgradeGeneration is a fresh process identity. The server records
	// it when this daemon accepts a machine operation and accepts convergence
	// attestations only from that same process generation.
	machineUpgradeGeneration string
	machineUpgradeTarget     string // exact target staged by the active Machine Upgrade
	machineUpgradeID         string
	machineUpgradeRuntimeID  string

	// serverReleaseManifestBaseURL caches the most recent non-empty
	// ReleaseManifestBaseURL seen on a heartbeat ack (task #815 step 2: the
	// server-dispatched top layer over the daemon-side env var from #1526).
	// atomic.Value (not a mutex+field) because reads by explicit Machine
	// Upgrade execution and writes by the heartbeat loop never need a compound read-then-write; a plain
	// swap/load is sufficient. Holds only string, or is unset (zero Value)
	// before the first heartbeat ack arrives. A later ack that omits the
	// field intentionally does NOT clear a previously cached value — a
	// transient server-side hiccup should not blank out a previously-good
	// override (see cli.releaseManifestBaseURLWithOverride).
	serverReleaseManifestBaseURL atomic.Value

	// claimMu guards pauseClaims and claimsInFlight. It is held only for the
	// microseconds it takes to make a decision; ClaimTask itself runs without
	// the lock so a slow per-runtime claim cannot stall a Machine Upgrade or
	// any other poller.
	//
	// The pair is the Machine Upgrade handoff barrier against the requirement
	// that "升级过程中如果有 task 进来，会延后升级而不是中断 task":
	// runRuntimePoller refuses to call ClaimTask while pauseClaims is set, and
	// the explicit lifecycle refuses to flip pauseClaims while any poller is mid-claim
	// or any task is in handleTask. Together that closes the fetch-then-claim
	// race where a new task slipping in during the release-metadata fetch
	// would be cancelled by triggerRestart's root-ctx cancel.
	claimMu        sync.Mutex
	pauseClaims    bool // when true, runRuntimePoller skips ClaimTask
	claimsInFlight int  // pollers that have decided to claim but haven't yet handed the task off to handleTask

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
	// runUpdateFn executes the release-download upgrade. Set to d.runUpdate by
	// New() and overridable in tests for explicit Machine Upgrade staging.
	runUpdateFn func(targetVersion string) (string, error)
	// verifyUpdatedBinaryFn checks the stable binary path that triggerRestart
	// would re-exec and confirms it already reports targetVersion. Set to
	// d.verifyUpdatedBinary by New() and overridable in tests.
	verifyUpdatedBinaryFn func(targetVersion, updateOutput string) (string, error)
	// activateStagedFn CAS-commits the explicit staged target and returns its
	// re-exec path. Nil → commitStagedActivation. Tests may no-op with ("", nil).
	activateStagedFn func(ctx context.Context, updateID, targetVersion string) (string, error)
	// Machine Upgrade handoff seams keep the bounded drain deterministic in
	// tests. Production defaults are installed by the helper methods.
	machineUpgradeNow  func() time.Time
	machineUpgradeWait func(context.Context, time.Duration) error

	sharedSkillScanMu    sync.Mutex
	sharedSkillScanCache map[string]string // scanRoot\x00skillKey -> fingerprint
	memoryCurationMu     sync.Mutex
	memoryCurationRuns   map[string]string // workspace\x00stage -> Beijing plan date
	activeCurationRuns   map[string]string // runtime id -> claimed run id

	// canonicalRuntimes owns the one durable provider process for each
	// Agent×runtime Message coordinator.
	canonicalRuntimes *canonicalAgentRuntimePool
	// residentCrashBackoff tracks repeated crashes per agent×runtime (task
	// #42②) so a resident process stuck crash-looping is flagged terminal
	// instead of silently retried forever.
	residentCrashBackoff *residentCrashBackoffTracker
	// agentLifecycleExecutor carries out dispatched /api/agents/{id}/lifecycle
	// operations (task #52). The daemon client clears server-owned provider
	// resume pointers before the runtime is recreated.
	agentLifecycleExecutor *agentLifecycleExecutor
	// canonicalResidentFactoryOverride is test-only; production uses
	// defaultCanonicalRuntimeFactory for resident Message adapters.
	canonicalResidentFactoryOverride canonicalRuntimeBackendFactory
}

// registerIdleMessageCoordinator installs the one long-running coordinator for
// an Agent on this Machine Service. The coordinator itself serializes delivery,
// runtime handoff, and Context Boundary persistence.
func (d *Daemon) registerIdleMessageCoordinator(agentID string, coordinator *MessageCoordinator) error {
	if d == nil || agentID == "" || coordinator == nil {
		return errors.New("agent id and Message coordinator are required")
	}
	d.messageCoordinatorMu.Lock()
	defer d.messageCoordinatorMu.Unlock()
	if d.messageCoordinators == nil {
		d.messageCoordinators = make(map[string]*MessageCoordinator)
	}
	if d.messageRuntimeIDs == nil {
		d.messageRuntimeIDs = make(map[string]string)
	}
	if _, exists := d.messageCoordinators[agentID]; exists {
		return fmt.Errorf("Message coordinator already registered for agent %q", agentID)
	}
	d.messageCoordinators[agentID] = coordinator
	return nil
}

func (d *Daemon) removeIdleMessageCoordinator(agentID, runtimeID string) {
	d.messageCoordinatorMu.Lock()
	if current := d.messageRuntimeIDs[agentID]; current != "" && current != runtimeID {
		d.messageCoordinatorMu.Unlock()
		return
	}
	coordinator := d.messageCoordinators[agentID]
	delete(d.messageCoordinators, agentID)
	delete(d.messageRuntimeIDs, agentID)
	d.messageCoordinatorMu.Unlock()
	if coordinator != nil {
		coordinator.Close()
	}
}

func (d *Daemon) acceptIdleAgentDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (protocol.AgentDeliverAckPayload, error) {
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[delivery.AgentID]
	runtimeID := d.messageRuntimeIDs[delivery.AgentID]
	d.messageCoordinatorMu.RUnlock()
	if coordinator == nil {
		if err := d.ensureIdleMessageCoordinatorForDelivery(delivery.AgentID); err != nil {
			return protocol.AgentDeliverAckPayload{}, err
		}
		d.messageCoordinatorMu.RLock()
		coordinator = d.messageCoordinators[delivery.AgentID]
		runtimeID = d.messageRuntimeIDs[delivery.AgentID]
		d.messageCoordinatorMu.RUnlock()
		if coordinator == nil {
			return protocol.AgentDeliverAckPayload{}, fmt.Errorf("no idle Message coordinator for agent %q", delivery.AgentID)
		}
	}
	if err := d.ensureResidentMessageRuntime(ctx, delivery.AgentID, runtimeID); err != nil {
		return protocol.AgentDeliverAckPayload{}, err
	}
	if _, err := coordinator.Accept(ctx, delivery); err != nil {
		return protocol.AgentDeliverAckPayload{}, err
	}
	return coordinator.Acknowledgement(delivery), nil
}

func (d *Daemon) flushIdleAgentDelivery(ctx context.Context, agentID string) error {
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[agentID]
	d.messageCoordinatorMu.RUnlock()
	if coordinator == nil {
		return fmt.Errorf("no idle Message coordinator for agent %q", agentID)
	}
	return coordinator.Flush(ctx)
}

func (d *Daemon) beginMessageRecoveryWithSend(send func(protocol.AgentRecoveryRequest) error) {
	if send == nil {
		return
	}
	d.messageCoordinatorMu.RLock()
	coordinators := make(map[string]*MessageCoordinator, len(d.messageCoordinators))
	for agentID, coordinator := range d.messageCoordinators {
		coordinators[agentID] = coordinator
	}
	d.messageCoordinatorMu.RUnlock()
	for agentID, coordinator := range coordinators {
		if err := send(coordinator.BeginRecovery(agentID, 100)); err != nil && d.logger != nil {
			d.logger.Warn("agent Message recovery request failed", "error", err, "agent_id", agentID)
		}
	}
}

func (d *Daemon) beginAgentMessageRecovery(agentID string) {
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[agentID]
	d.messageCoordinatorMu.RUnlock()
	if coordinator == nil {
		return
	}
	request := coordinator.BeginRecovery(agentID, 100)
	if !d.sendAgentMessageRunnerFrame(agentID, protocol.EventAgentRecoveryRequest, request) && d.logger != nil {
		d.logger.Warn("agent Message recovery request failed", "error", "workspace Runner Message transport unavailable", "agent_id", agentID)
	}
}

func (d *Daemon) handleMessageRecoveryPageWithSend(ctx context.Context, page protocol.AgentRecoveryPage, send func(protocol.AgentRecoveryRequest) error) error {
	if send == nil {
		return errors.New("Message recovery sender is unavailable")
	}
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[page.AgentID]
	runtimeID := d.messageRuntimeIDs[page.AgentID]
	d.messageCoordinatorMu.RUnlock()
	if coordinator == nil {
		return fmt.Errorf("no Message coordinator for recovery agent %q", page.AgentID)
	}
	// Recovery bodies cross the same resident runtime boundary as live
	// Deliveries. On daemon restart recovery can arrive before any live
	// Delivery has created that runtime, so prepare it before merging a page
	// that will need handoff.
	if len(page.Messages) > 0 {
		if err := d.ensureResidentMessageRuntime(ctx, page.AgentID, runtimeID); err != nil {
			return fmt.Errorf("prepare resident Message runtime for recovery: %w", err)
		}
	}
	if err := coordinator.MergeRecoveryPage(page); err != nil {
		return err
	}
	if page.HasMore {
		return send(coordinator.RecoveryRequest(page.AgentID, 100))
	}
	// Terminal page: hand recovered bodies to the resident runtime WITHOUT
	// blocking the workspace runner read loop. A busy / idle-input-unsupported
	// resident runtime (e.g. Pi mid-turn with ErrCanonicalAgentRuntimeBusy /
	// resident idle-input overlap) must not stall recovery of this agent or of
	// any other agent whose RecoveryPage arrives on the same connection. Flush
	// is executed on a bounded-timeout background task; the coordinator's
	// pending-notice retry path completes it if the runtime is transiently busy.
	flushCtx, cancel := context.WithTimeout(context.Background(), recoveryFlushTimeout)
	go func() {
		defer cancel()
		if err := coordinator.Flush(flushCtx); err != nil && d.logger != nil {
			d.logger.Warn("workspace Runner Message recovery flush deferred",
				"error", err, "agent_id", page.AgentID, "recovery_id", page.RecoveryID)
		}
	}()
	return nil
}

// New creates a new Daemon instance.
func New(cfg Config, logger *slog.Logger) *Daemon {
	client := NewClient(cfg.ServerBaseURL)
	// Tag every daemon HTTP request with the daemon's CLI version so the
	// server can split logs/metrics by client version (parallel to the CLI).
	client.SetVersion(cfg.CLIVersion)
	d := &Daemon{
		cfg:                       cfg,
		client:                    client,
		logger:                    logger,
		workspaces:                make(map[string]*workspaceState),
		runtimeIndex:              make(map[string]Runtime),
		runtimeSet:                newRuntimeSetWatcher(),
		agentVersions:             make(map[string]string),
		wsHBLastAck:               make(map[string]time.Time),
		agentWakeSlots:            make(map[string]chan struct{}),
		runtimeGoneInflight:       make(map[string]struct{}),
		reregisterNextAttempt:     make(map[string]time.Time),
		reregisterLastCompletedAt: make(map[string]time.Time),
		cancelPollInterval:        5 * time.Second,
		sharedSkillScanCache:      make(map[string]string),
		memoryCurationRuns:        make(map[string]string),
		activeCurationRuns:        make(map[string]string),
		canonicalRuntimes:         newCanonicalAgentRuntimePool(),
		messageCoordinators:       make(map[string]*MessageCoordinator),
		messageRuntimeIDs:         make(map[string]string),
		agentProcessManagers:      make(map[string]*agentProcessManager),
		agentActivityProducers:    make(map[string]*agentActivityProducer),
		runnerMessageTransports:   make(map[string]workspaceRunnerMessageTransport),
		runnerMessageGeneration:   make(map[string]uint64),
		residentCrashBackoff:      newResidentCrashBackoffTracker(residentCrashBackoffWindow, residentCrashRetryCap),
		machineUpgradeGeneration:  uuid.NewString(),
		runnerInstanceID:          uuid.NewString(),
	}
	d.canonicalRuntimes.setMaxAgentProcesses(cfg.MaxAgentProcesses)
	d.canonicalRuntimes.subscribeResidentRuntimeCrash(func(ev ResidentRuntimeCrashEvent) {
		d.onResidentRuntimeCrash(ev)
	})
	d.canonicalRuntimes.subscribeResidentRuntimeRecovered(func(agentID, runtimeID string) {
		d.clearAgentProviderCrashedOnServer(runtimeID, agentID)
	})
	d.updateObservation = newUpdateObservationCoordinator(cfg, logger)
	if cfg.WorkspacesRoot != "" {
		d.lifecycleDiagnostics = newLifecycleDiagnosticWriter(filepath.Join(cfg.WorkspacesRoot, ".multica", "lifecycle-diagnostics"), time.Now)
	}
	d.machineUpgradeLog = newMachineUpgradeEventLog(time.Now)
	d.agentLifecycleExecutor = &agentLifecycleExecutor{
		workspacesRoot: cfg.WorkspacesRoot,
		runtimes:       d.canonicalRuntimes,
		sessionReset:   d.client,
		logger:         logger,
	}
	d.runner = taskRunnerFunc(d.runTask)
	d.reminderCache = newReminderCache(nil, logger, d.onReminderTimer)
	d.reminderCache.setPersistence(cfg.WorkspacesRoot)
	d.reminderAgents = newReminderAgentManager(cfg.WorkspacesRoot, logger)
	d.runUpdateFn = d.runUpdate
	d.verifyUpdatedBinaryFn = d.verifyUpdatedBinary
	return d
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

func (d *Daemon) removeReminderAgent(agentID, runtimeID string, generation int64) error {
	removed := false
	if d.reminderAgents != nil {
		var err error
		removed, _, err = d.reminderAgents.applyStop(agentID, runtimeID, generation)
		if err != nil {
			return err
		}
	}
	if removed && d.reminderCache != nil {
		if err := d.reminderCache.removeOwner(agentID); err != nil {
			return err
		}
	}
	if removed {
		d.removeIdleMessageCoordinator(agentID, runtimeID)
		d.reminderGateMu.Lock()
		delete(d.reminderPendingSnapshots, agentID)
		d.reminderGateMu.Unlock()
	}
	return nil
}

// reregisterCoalesceWindow caps how often the daemon re-registers a workspace
// after detecting a runtime_not_found response. Many stale runtime IDs may be
// reported within seconds of each other (one delete clears all of a daemon's
// runtimes), and a single re-register call replaces every runtime in the
// workspace, so concurrent recoveries must collapse to one API call.
const reregisterCoalesceWindow = 30 * time.Second

// reregisterFailureBackoff is the additional wait inserted before the next
// re-register attempt when the previous one failed. This prevents heartbeat
// ticks (~15s) from converting a server-side log flood into a re-register
// flood when re-registration itself is failing (workspace removed, server
// unreachable, ...).
const reregisterFailureBackoff = 60 * time.Second

// handleRuntimeGone is the single recovery entry point shared by the HTTP
// heartbeat path, the runtime poller, and the WebSocket runtime_gone ack
// handler. All three may notice the same stale runtime within a few ms of
// each other, so this function:
//
//   - keys an in-flight set on runtimeID to drop concurrent calls for the same
//     ID after the first one is already cleaning up;
//   - keys a per-workspace next-attempt timestamp on workspaceID so that
//     concurrent recoveries triggered by the SAME initial event coalesce to a
//     single registerRuntimesForWorkspace call. The slot is cleared on success
//     so a later distinct runtime deletion in the same workspace can trigger
//     its own recovery without waiting for the coalesce window to expire; and
//   - keys a per-workspace last-completed timestamp so that a straggler whose
//     removeStaleRuntime took long enough that a sibling fully ran AND cleared
//     the slot can still recognize itself as same-wave and bail. Without this,
//     the success-case slot clear opens a race where the late caller re-claims
//     an empty slot and double-registers.
//
// On failure of the underlying re-register, the next-attempt timestamp is
// extended by reregisterFailureBackoff so we don't replace a server-side log
// flood with a daemon-side register flood. workspaceSyncLoop will retry
// independently every DefaultWorkspaceSyncInterval as a safety net.
//
// The recovery HTTP call uses the daemon root context, not the caller's. The
// heartbeat path's per-runtime ctx is cancelled by notifyRuntimeSetChanged the
// moment we prune the dead UUID, and if we forwarded that ctx the in-flight
// register would self-cancel mid-flight.
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
		// cleaned this up, or workspaceSyncLoop pruned the whole workspace.
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
		// Logged at Warn (not Error) because workspaceSyncLoop retries
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
// still be allowed to retry once the backoff expires. workspaceSyncLoop only
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
// HTTP calls (re-register, recover-orphans) that must survive the heartbeat
// loop tearing down a per-runtime context. Falls back to Background when the
// daemon was not started via Run(), e.g. unit-test fixtures.
func (d *Daemon) recoveryContext() context.Context {
	if d.rootCtx != nil {
		return d.rootCtx
	}
	return context.Background()
}

// removeStaleRuntime drops a runtime ID from its owning workspace's runtimeIDs
// list, the daemon-level runtimeIndex, and the WS heartbeat freshness map.
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

	d.wsHBMu.Lock()
	delete(d.wsHBLastAck, runtimeID)
	d.wsHBMu.Unlock()
	if d.reminderCache != nil {
		d.reminderCache.suspend()
	}

	return workspaceID, true
}

// workspaceNeedsRuntimeRecovery reports whether a tracked workspace currently
// has zero runtime IDs — the state reached when handleRuntimeGone pruned every
// runtime and its inline re-register failed. workspaceSyncLoop calls this on
// each tick so the workspace can recover without waiting for an external
// trigger.
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
// because more than one supervisor (taskWakeupLoop, heartbeatLoop, pollLoop)
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

// wsHeartbeatFreshness defines how long a WS heartbeat ack is considered
// "fresh enough" to suppress the HTTP heartbeat for that runtime. The window
// is 2× HeartbeatInterval so a single dropped WS ack still keeps HTTP
// suppressed, but two missed acks (~30s of WS silence) re-enable HTTP — well
// inside the server-side 45s offline threshold.
func (d *Daemon) wsHeartbeatFreshness() time.Duration {
	if d.cfg.HeartbeatInterval <= 0 {
		return 30 * time.Second
	}
	return 2 * d.cfg.HeartbeatInterval
}

// recordWSHeartbeatAck stamps the runtime as having received a fresh WS
// heartbeat ack from the server. Called by the WS read pump.
func (d *Daemon) recordWSHeartbeatAck(runtimeID string) {
	if runtimeID == "" {
		return
	}
	d.wsHBMu.Lock()
	d.wsHBLastAck[runtimeID] = time.Now()
	d.wsHBMu.Unlock()
}

// wsHeartbeatRecentlyAcked reports whether the runtime received a WS
// heartbeat ack inside the freshness window. The HTTP heartbeat loop uses
// this to skip duplicate work when WS is already keeping the runtime alive.
func (d *Daemon) wsHeartbeatRecentlyAcked(runtimeID string) bool {
	d.wsHBMu.RLock()
	last, ok := d.wsHBLastAck[runtimeID]
	d.wsHBMu.RUnlock()
	if !ok {
		return false
	}
	return time.Since(last) < d.wsHeartbeatFreshness()
}

// clearWSHeartbeatAcks drops all WS heartbeat freshness records. Called on
// WS disconnect so HTTP heartbeats resume on the next tick.
func (d *Daemon) clearWSHeartbeatAcks() {
	d.wsHBMu.Lock()
	for k := range d.wsHBLastAck {
		delete(d.wsHBLastAck, k)
	}
	d.wsHBMu.Unlock()
}

// Run starts the daemon: resolves auth, registers runtimes, then polls for tasks.
func (d *Daemon) Run(ctx context.Context) error {
	// Wrap context so handleUpdate can cancel the daemon for restart.
	ctx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel
	d.rootCtx = ctx
	defer func() { _ = d.canonicalRuntimes.closeAll() }()

	// Bind health port early to detect another running daemon.
	healthLn, err := d.listenHealth()
	if err != nil {
		return err
	}

	agentNames := make([]string, 0, len(d.cfg.Agents))
	for name := range d.cfg.Agents {
		agentNames = append(agentNames, name)
	}
	logFields := []any{"version", d.cfg.CLIVersion, "agents", agentNames, "server", d.cfg.ServerBaseURL}
	if d.cfg.Profile != "" {
		logFields = append(logFields, "profile", d.cfg.Profile)
	}
	d.logger.Info("starting daemon", logFields...)
	d.logger.Debug("daemon config resolved",
		"daemon_id", d.cfg.DaemonID,
		"device_name", d.cfg.DeviceName,
		"workspaces_root", d.cfg.WorkspacesRoot,
		"health_port", d.cfg.HealthPort,
		"poll_interval", d.cfg.PollInterval,
		"heartbeat_interval", d.cfg.HeartbeatInterval,
		"agent_timeout", d.cfg.AgentTimeout,
		"max_agent_processes", d.cfg.MaxAgentProcesses,
		"idle_watchdog", d.cfg.AgentIdleWatchdog,
		"machine_upgrade_mode", "explicit_only",
		"launched_by", d.cfg.LaunchedBy,
	)
	if err := d.reminderCache.stateError(); err != nil {
		return fmt.Errorf("reminder cache is not recoverable: %w", err)
	}

	// Load auth token from CLI config.
	if err := d.resolveAuth(); err != nil {
		return err
	}

	// Bind and serve the health port before the (potentially slow) preflight,
	// so `daemon start` and the desktop see a live "starting" daemon instead
	// of connection-refused while preflightAuth runs. preflightAuth's initial
	// workspace sync detects every configured agent's version by exec'ing it,
	// which on a cold cache with many agents takes ~20s. Liveness (port up) and
	// readiness (status:"running") are reported separately: /health stays
	// "starting" until d.ready is set after preflight, so a slow or *failing*
	// preflight is never misreported as a started daemon. resolveAuth has
	// already run, so a missing token still fails fast before we begin serving.
	go d.serveHealth(ctx, healthLn, time.Now())

	// Renew the PAT before the first API call, then do the initial
	// workspace sync. Both steps live in preflightAuth so the ordering
	// invariant (renew first) is enforced at one site instead of
	// scattered into Run, and tests can exercise the failure paths
	// without the full Run setup.
	if err := d.preflightAuth(ctx); err != nil {
		return err
	}
	// A target successor records local readiness only after it has completed
	// normal authenticated registration/preflight. This is durable recovery
	// evidence, not remote completion: the server still requires the exact
	// accepted sibling set to attest before it marks the operation completed.
	if err := d.markMachineUpgradeCandidateReady(); err != nil {
		d.logger.Warn("could not persist machine upgrade candidate readiness", "error", err)
	}

	// Deregister runtimes on shutdown (uses a fresh context since ctx will be cancelled).
	defer d.deregisterRuntimes()

	// Start workspace sync loop to discover newly created workspaces.
	go d.workspaceSyncLoop(ctx)

	taskWakeups := make(chan taskWakeup, 256)
	go d.taskWakeupLoop(ctx, taskWakeups)
	go d.workspaceRunnerLoop(ctx)
	go d.diagnosticsCleanupLoop(ctx)
	go d.heartbeatLoop(ctx)
	go d.residentCrashWatchLoop(ctx)
	go d.tokenRenewalLoop(ctx)
	go d.sharedSkillsSyncLoop(ctx)
	go d.autoUpdateLoop(ctx)

	// Preflight succeeded and the background loops are up: the daemon has
	// registered its runtimes and can now claim and run tasks. Flip /health
	// from "starting" to "running" — this is the signal `daemon start`'s
	// readiness wait blocks on, so success is reported only after startup
	// actually completed, not merely because the health port came up.
	d.ready.Store(true)
	d.logger.Debug("background loops launched (workspace-sync, task-wakeup, heartbeat, gc, token-renewal, shared-skills, auto-update-detection); machine upgrades are explicit-only; health now reporting ready")
	err = d.pollLoop(ctx, taskWakeups)
	d.logger.Debug("daemon main loop returning", "error", err)
	return err
}

// RestartBinary returns the path to the new binary if the daemon needs to restart
// after a successful update, or empty string if no restart is needed.
func (d *Daemon) RestartBinary() string {
	return d.restartBinary
}

// MachineUpgradeTarget is the exact candidate version the foreground launcher
// must observe before treating a detached successor as locally taken over.
func (d *Daemon) MachineUpgradeTarget() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.machineUpgradeTarget
}

// ReportMachineUpgradeTakeoverFailure leaves a durable typed failure instead
// of allowing a standalone candidate startup error to look like convergence.
// It is called by the foreground launcher after the daemon has stopped, while
// its authenticated runtime credentials are still available.
func (d *Daemon) ReportMachineUpgradeTakeoverFailure(cause error) {
	if cause == nil || d.client == nil {
		return
	}
	d.mu.Lock()
	upgradeID, runtimeID := d.machineUpgradeID, d.machineUpgradeRuntimeID
	d.mu.Unlock()
	if upgradeID == "" || runtimeID == "" {
		return
	}
	d.failMachineUpgrade(context.Background(), runtimeID, upgradeID, "candidate_takeover_failed", cause)
}

func (d *Daemon) BeginMachineUpgradeRollback(cause error) error {
	if cause == nil || d.client == nil {
		return nil
	}
	if err := d.MarkMachineUpgradeRollbackPending(); err != nil {
		return err
	}
	d.mu.Lock()
	upgradeID, runtimeID := d.machineUpgradeID, d.machineUpgradeRuntimeID
	d.mu.Unlock()
	generation := d.machineUpgradeRollbackGenerationID()
	if upgradeID == "" || runtimeID == "" || generation == "" {
		return fmt.Errorf("machine upgrade rollback identity is incomplete")
	}
	return d.client.ReportMachineUpgradeRollback(context.Background(), runtimeID, upgradeID, generation, "candidate_takeover_failed", cause.Error())
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

	if err := d.client.Deregister(ctx, runtimeIDs); err != nil {
		d.logger.Warn("failed to deregister runtimes on shutdown", "error", err)
	} else {
		d.logger.Info("deregistered runtimes", "count", len(runtimeIDs))
	}
}

// resolveAuth loads the auth token from the CLI config for the active profile.
func (d *Daemon) resolveAuth() error {
	cfg, err := cli.LoadCLIConfigForProfile(d.cfg.Profile)
	if err != nil {
		return fmt.Errorf("load CLI config: %w", err)
	}
	if cfg.Token == "" {
		loginHint := "'multica login'"
		if d.cfg.Profile != "" {
			loginHint = fmt.Sprintf("'multica login --profile %s'", d.cfg.Profile)
		}
		d.logger.Warn("not authenticated — run " + loginHint + " to authenticate, then restart the daemon")
		return fmt.Errorf("not authenticated: run %s first", loginHint)
	}
	d.client.SetToken(cfg.Token)
	d.logger.Info("authenticated")
	d.logger.Debug("auth token loaded", "profile", d.cfg.Profile, "token_len", len(cfg.Token))
	return nil
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
		protocol.DaemonCapabilityAgentLifecycleActions,
		protocol.DaemonCapabilityMachineUpgrade,
		protocol.DaemonCapabilityAgentSessionReset,
	}
	if includeCredentialTransport {
		capabilities = append(capabilities, protocol.DaemonCapabilityAgentCredentialTransport)
	}
	return capabilities
}

func (d *Daemon) applyRegisterDaemonToken(workspaceID string, resp *RegisterResponse) bool {
	if resp == nil || resp.DaemonToken == "" || resp.DaemonTokenExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, resp.DaemonTokenExpiresAt)
	if err != nil {
		d.logger.Warn("register response carried invalid daemon token expiry", "workspace_id", workspaceID, "error", err)
		return false
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, resp.DaemonToken, expiresAt)
	for _, rt := range resp.Runtimes {
		d.client.SetRuntimeDaemonToken(rt.ID, resp.DaemonToken, expiresAt)
	}
	d.logger.Debug("daemon token cached for workspace", "workspace_id", workspaceID, "runtimes", len(resp.Runtimes), "expires_at", expiresAt)
	return true
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

func (d *Daemon) registerRuntimesForWorkspace(ctx context.Context, workspaceID string) (*RegisterResponse, error) {
	d.logger.Debug("registering runtimes for workspace", "workspace_id", workspaceID, "agent_count", len(d.cfg.Agents))
	// Best-effort: tell the server we're up and about to probe agent CLI
	// versions, before that probe loop (which follows immediately below and
	// can take ~20s on a cold cache) delays the real register call. A failure
	// here must never block startup — it only costs a missed "starting"
	// display for this cycle, not a functional regression.
	if err := d.client.MarkStarting(ctx, workspaceID, d.cfg.DaemonID); err != nil {
		d.logger.Debug("mark-starting call failed; continuing without it", "workspace_id", workspaceID, "error", err)
	}
	var runtimes []map[string]string
	for name, entry := range d.cfg.Agents {
		version, err := detectAgentVersion(ctx, entry.Path)
		if err != nil {
			d.logger.Warn("skip registering runtime", "name", name, "error", err)
			continue
		}
		if err := checkAgentMinVersion(name, version); err != nil {
			d.logger.Warn("skip registering runtime: version too old", "name", name, "version", version, "error", err)
			continue
		}
		d.setAgentVersion(name, version)
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
		return nil, fmt.Errorf("no agent runtimes could be registered")
	}

	includeCredentialTransport := d.client.WorkspaceDaemonTokenAvailable(workspaceID, time.Now())
	req := map[string]any{
		"workspace_id":                        workspaceID,
		"daemon_id":                           d.cfg.DaemonID,
		"legacy_daemon_ids":                   d.cfg.LegacyDaemonIDs,
		"device_name":                         d.cfg.DeviceName,
		"cli_version":                         d.cfg.CLIVersion,
		"launched_by":                         d.cfg.LaunchedBy,
		"capabilities":                        daemonRegistrationCapabilities(includeCredentialTransport),
		"runtimes":                            runtimes,
		"pinned_version":                      d.cfg.PinnedVersion,
		"machine_upgrade_generation":          d.machineUpgradeGenerationID(),
		"machine_upgrade_rollback_generation": d.machineUpgradeRollbackGenerationID(),
	}
	if d.updateObservation != nil {
		if snapshot := d.updateObservation.PublishedSnapshot(); snapshot != nil {
			req["auto_update"] = snapshot
		}
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
			d.clearDaemonTokensForWorkspace(workspaceID)
			includeCredentialTransport = false
			req["capabilities"] = daemonRegistrationCapabilities(false)
			d.logger.Warn("workspace daemon token rejected during register; retrying with bootstrap token", "workspace_id", workspaceID, "error", err)
			resp, err = d.client.RegisterForWorkspace(ctx, workspaceID, req)
		}
		if err != nil {
			return nil, fmt.Errorf("register runtimes: %w", err)
		}
	}
	if d.applyRegisterDaemonToken(workspaceID, resp) && !includeCredentialTransport {
		legacyResp := resp
		req["capabilities"] = daemonRegistrationCapabilities(true)
		resp, err = d.client.RegisterForWorkspace(ctx, workspaceID, req)
		if err != nil {
			d.client.ClearWorkspaceDaemonToken(workspaceID)
			for _, rt := range legacyResp.Runtimes {
				d.client.ClearRuntimeDaemonToken(rt.ID)
			}
			d.logger.Warn("daemon-token re-register failed; continuing without credential transport capability", "workspace_id", workspaceID, "error", err)
			resp = legacyResp
		} else {
			d.applyRegisterDaemonToken(workspaceID, resp)
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

// DefaultTokenRenewalInterval is how often the daemon asks the server to
// extend its PAT. The server-side threshold is 7 days of remaining lifetime;
// polling every ~3 days gives at least two chances to renew before the
// window closes, so a single failed call (network blip, server restart) does
// not push the token out of the renewal window.
const DefaultTokenRenewalInterval = 3 * 24 * time.Hour

// preflightAuth runs the two auth-sensitive startup steps in their
// required order: a synchronous PAT renewal first, then the initial
// workspace sync. The order matters — running tryRenewToken before any
// other API call is what surfaces a user-actionable "run multica login"
// WARN when the PAT is already revoked or expired. If we let the
// workspace sync go first, its 401 would short-circuit Run before the
// renewal loop's first tick ever fires, and the operator would see only
// a generic auth failure in the workspace-sync log with no hint that
// re-login is the fix.
//
// The renewal is best-effort: tryRenewToken logs and returns, never
// propagating errors. preflightAuth's exit status is driven entirely by
// the workspace sync — so a transient renewal failure (network blip,
// 500) does not by itself block startup. A successful sync with zero
// workspaces is fine: a newly-signed-up user may start the daemon
// before creating their first workspace, and workspaceSyncLoop will
// register runtimes once one appears.
func (d *Daemon) preflightAuth(ctx context.Context) error {
	d.tryRenewToken(ctx)
	return d.syncWorkspacesFromAPI(ctx)
}

// tokenRenewalLoop keeps the daemon's PAT alive by periodically asking the
// server to extend its expires_at in-place. The startup renewal happens
// synchronously in preflightAuth so a daemon coming back online after a
// week of downtime gets a fresh expiry before its next heartbeat could
// 401; this loop owns the long-running ~3-day cadence after that.
//
// The server is authoritative on the renewal threshold (it sees expires_at;
// we don't), so this loop is intentionally dumb: call, log, sleep, repeat.
// On 401 we surface a clear "re-login required" warning because the daemon
// has no way to recover automatically — but we keep the loop running so the
// user sees the same warning on every cycle until they fix it, rather than
// silently exiting and forcing them to read scrollback to find the cause.
func (d *Daemon) tokenRenewalLoop(ctx context.Context) {
	ticker := time.NewTicker(DefaultTokenRenewalInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tryRenewToken(ctx)
		}
	}
}

// tryRenewToken performs one renewal round-trip with a short, isolated
// timeout. Errors are logged but never propagated — there is no caller to
// handle them. Failures are debug-level except for 401, which gets a
// user-actionable warning.
func (d *Daemon) tryRenewToken(ctx context.Context) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := d.client.RenewToken(reqCtx)
	if err != nil {
		if isUnauthorizedError(err) {
			loginHint := "'multica login'"
			if d.cfg.Profile != "" {
				loginHint = fmt.Sprintf("'multica login --profile %s'", d.cfg.Profile)
			}
			d.logger.Warn("auth token rejected by server — run "+loginHint+" to re-authenticate, then restart the daemon", "error", err)
			return
		}
		d.logger.Debug("token renewal failed; will retry on next cycle", "error", err)
		return
	}
	if resp.Renewed {
		d.logger.Info("auth token renewed", "expires_at", resp.ExpiresAt)
	} else {
		d.logger.Debug("auth token not yet eligible for renewal", "expires_at", resp.ExpiresAt)
	}
}

// workspaceSyncLoop periodically refreshes the selected workspace from the API.
func (d *Daemon) workspaceSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(DefaultWorkspaceSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.syncWorkspacesFromAPI(ctx); err != nil {
				d.logger.Debug("workspace sync failed", "error", err)
			}
		}
	}
}

// syncWorkspacesFromAPI fetches the user's workspace membership, then registers
// and refreshes only the workspace selected by `multica setup /<slug>`.
func (d *Daemon) syncWorkspacesFromAPI(ctx context.Context) error {
	d.reloading.Lock()
	defer d.reloading.Unlock()

	apiCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	workspaces, err := d.client.ListWorkspaces(apiCtx)
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	if len(workspaces) == 0 {
		return nil
	}
	selectedID := strings.TrimSpace(d.cfg.WorkspaceID)
	if selectedID == "" {
		return fmt.Errorf("workspace is required; run `multica setup /<workspace-slug>`")
	}
	d.logger.Debug("workspace sync: fetched workspaces", "count", len(workspaces), "selected_workspace_id", selectedID)

	apiIDs := make(map[string]string, len(workspaces)) // id -> name
	for _, ws := range workspaces {
		if ws.ID == selectedID {
			apiIDs[ws.ID] = ws.Name
			break
		}
	}
	if len(apiIDs) == 0 {
		return fmt.Errorf("selected workspace %q not found among your workspaces; run `multica setup /<workspace-slug>`", selectedID)
	}

	d.mu.Lock()
	currentIDs := make(map[string]bool, len(d.workspaces))
	for id := range d.workspaces {
		currentIDs[id] = true
	}
	d.mu.Unlock()

	var registered int
	var removed int
	for id, name := range apiIDs {
		if currentIDs[id] {
			if d.client.WorkspaceDaemonTokenNeedsRefresh(id, time.Now()) {
				d.logger.Info("workspace daemon token needs refresh; re-registering", "workspace_id", id, "name", name)
				if err := d.reregisterWorkspaceAfterRuntimeGone(ctx, id); err != nil {
					d.logger.Warn("daemon token refresh register failed", "workspace_id", id, "error", err)
					continue
				}
				registered++
				continue
			}
			// Only intervene further if the workspace lost all of its
			// runtimes (most commonly because handleRuntimeGone pruned them
			// and its inline re-register failed).
			if !d.workspaceNeedsRuntimeRecovery(id) {
				continue
			}
			d.logger.Info("workspace has no runtimes; retrying registration", "workspace_id", id, "name", name)
			if err := d.reregisterWorkspaceAfterRuntimeGone(ctx, id); err != nil {
				d.logger.Warn("retry register failed", "workspace_id", id, "error", err)
				continue
			}
			registered++
			continue
		}
		resp, err := d.registerRuntimesForWorkspace(ctx, id)
		if err != nil {
			d.logger.Error("failed to register runtimes", "workspace_id", id, "name", name, "error", err)
			continue
		}
		runtimeIDs := make([]string, len(resp.Runtimes))
		for i, rt := range resp.Runtimes {
			runtimeIDs[i] = rt.ID
			d.logger.Info("registered runtime", "workspace_id", id, "runtime_id", rt.ID, "provider", rt.Provider)
		}
		d.mu.Lock()
		d.workspaces[id] = newWorkspaceState(id, runtimeIDs, resp.ServerCapabilities...)
		for _, rt := range resp.Runtimes {
			d.runtimeIndex[rt.ID] = rt
		}
		d.mu.Unlock()

		// Tell the server about any tasks the previous daemon process was
		// running on these runtimes. Without this, an issue can stay stuck
		// at in_progress until the slow heartbeat sweeper or the in-flight
		// task timeout (2.5h) kicks in.
		for _, rid := range runtimeIDs {
			if err := d.client.RecoverOrphans(ctx, rid); err != nil {
				d.logger.Warn("recover-orphans failed", "runtime_id", rid, "error", err)
			}
		}

		d.logger.Info("watching workspace", "workspace_id", id, "name", name, "runtimes", len(resp.Runtimes))
		registered++
	}

	// Remove workspaces the user no longer belongs to.
	for id := range currentIDs {
		if _, ok := apiIDs[id]; !ok {
			d.mu.Lock()
			if ws, exists := d.workspaces[id]; exists {
				for _, rid := range ws.runtimeIDs {
					delete(d.runtimeIndex, rid)
					d.client.ClearRuntimeDaemonToken(rid)
				}
			}
			delete(d.workspaces, id)
			d.client.ClearWorkspaceDaemonToken(id)
			d.mu.Unlock()
			d.logger.Info("stopped watching workspace", "workspace_id", id)
			removed++
		}
	}
	if registered > 0 || removed > 0 {
		if removed > 0 && d.reminderCache != nil {
			d.reminderCache.suspend()
		}
		d.notifyRuntimeSetChanged()
	}

	if len(d.allRuntimeIDs()) == 0 && registered == 0 {
		return fmt.Errorf("failed to register runtimes for selected workspace %q", selectedID)
	}
	if registered > 0 || removed > 0 {
		d.logger.Debug("workspace sync done", "registered", registered, "removed", removed, "tracked", len(apiIDs))
	}
	return nil
}

// heartbeatLoop supervises per-runtime HTTP heartbeat goroutines. Each runtime
// gets an independent ticker so a slow heartbeat for one runtime cannot block
// heartbeats for any other runtime — this matters when a single daemon serves
// multiple workspaces, because the previous shared loop would serialize an
// up-to-30s HTTP timeout across every runtime in the set.
func (d *Daemon) heartbeatLoop(ctx context.Context) {
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	cancels := make(map[string]context.CancelFunc)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	sync := func() {
		want := make(map[string]struct{})
		for _, rid := range d.allRuntimeIDs() {
			want[rid] = struct{}{}
		}
		for rid, cancel := range cancels {
			if _, ok := want[rid]; !ok {
				cancel()
				delete(cancels, rid)
			}
		}
		for rid := range want {
			if _, ok := cancels[rid]; ok {
				continue
			}
			rctx, rcancel := context.WithCancel(ctx)
			cancels[rid] = rcancel
			go d.runRuntimeHeartbeat(rctx, rid)
		}
	}

	sync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-runtimeSetCh:
			sync()
		}
	}
}

// runRuntimeHeartbeat owns the HTTP heartbeat schedule for a single runtime.
// The first tick fires after a small jittered delay (up to one full interval)
// to avoid a thundering herd when the daemon registers many runtimes at once.
func (d *Daemon) runRuntimeHeartbeat(ctx context.Context, rid string) {
	interval := d.cfg.HeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	// Jittered initial delay; cap at the interval so the first beat still
	// happens within one period.
	if jitter := time.Duration(rand.Int63n(int64(interval))); jitter > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter):
		}
	}

	var updateChanged <-chan struct{}
	unsubscribe := func() {}
	if d.updateObservation != nil {
		updateChanged, unsubscribe = d.updateObservation.Subscribe()
	}
	defer unsubscribe()

	d.runHeartbeatTick(ctx, rid)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-updateChanged:
			d.runHeartbeatTick(ctx, rid)
		case <-ticker.C:
			d.runHeartbeatTick(ctx, rid)
		}
	}
}

func (d *Daemon) runHeartbeatTick(ctx context.Context, rid string) {
	// Skip HTTP heartbeat for runtimes that successfully acked a recent
	// WebSocket heartbeat. The WS path keeps last_seen_at fresh and delivers
	// actions, so the HTTP write would be a duplicate DB update. If the WS
	// heartbeat goes silent the freshness window expires and HTTP resumes
	// automatically on the next tick — that is the fallback the WS path
	// relies on.
	if d.wsHeartbeatRecentlyAcked(rid) {
		d.logger.Debug("heartbeat: skipping HTTP tick, WS recently acked", "runtime_id", rid)
		return
	}
	d.logger.Debug("heartbeat: HTTP tick", "runtime_id", rid)
	var observation *protocol.DaemonUpdateObservation
	if d.updateObservation != nil {
		observation = d.updateObservation.PublishedSnapshot()
	}
	resp, err := d.client.SendHeartbeat(ctx, rid, d.activeMemoryCurationRun(rid), observation)
	if err != nil {
		if ctx.Err() == nil {
			if isRuntimeNotFoundError(err) {
				// Server says this runtime is gone — recover instead of
				// looping on the dead UUID. handleRuntimeGone coalesces
				// concurrent callers and runs the recovery HTTP call under
				// the daemon root context so notifyRuntimeSetChanged
				// tearing down this heartbeat goroutine cannot abort it.
				go d.handleRuntimeGone(rid)
				return
			}
			d.logger.Warn("heartbeat failed", "runtime_id", rid, "error", err)
		}
		return
	}
	if resp != nil && resp.RuntimeGone {
		// The WS path returns a successful ack with RuntimeGone=true for the
		// same scenario; treat it the same way here in case HTTP starts
		// surfacing this signal too.
		go d.handleRuntimeGone(rid)
		return
	}
	d.handleHeartbeatActions(ctx, rid, resp)
}

// handleHeartbeatActions dispatches the pending-action set returned by either
// transport (HTTP POST /api/daemon/heartbeat or WS daemon:heartbeat_ack).
// Each action is dispatched in its own goroutine so a slow handler cannot
// block subsequent heartbeats.
func (d *Daemon) handleHeartbeatActions(ctx context.Context, runtimeID string, resp *HeartbeatResponse) {
	if resp == nil {
		return
	}
	if resp.ReleaseManifestBaseURL != "" {
		d.serverReleaseManifestBaseURL.Store(resp.ReleaseManifestBaseURL)
	}
	if resp.PendingUpdate != nil || resp.PendingMachineUpgrade != nil || resp.PendingModelList != nil || resp.PendingLocalSkills != nil || resp.PendingLocalSkillImport != nil || resp.PendingMemoryCuration != nil || resp.PendingRestart != nil || len(resp.PendingAgentLifecycleOperations) > 0 || len(resp.PendingAgentStartIntents) > 0 {
		d.logger.Debug("heartbeat: pending actions",
			"runtime_id", runtimeID,
			"update", resp.PendingUpdate != nil,
			"machine_upgrade", resp.PendingMachineUpgrade != nil,
			"model_list", resp.PendingModelList != nil,
			"local_skills", resp.PendingLocalSkills != nil,
			"local_skill_import", resp.PendingLocalSkillImport != nil,
			"memory_curation", resp.PendingMemoryCuration != nil,
			"restart", resp.PendingRestart != nil,
			"agent_lifecycle_operations", len(resp.PendingAgentLifecycleOperations),
			"agent_start_intents", len(resp.PendingAgentStartIntents),
		)
	}
	if resp.PendingUpdate != nil {
		go d.handleUpdate(ctx, runtimeID, resp.PendingUpdate)
	}
	if resp.PendingMachineUpgrade != nil {
		go d.handleMachineUpgrade(ctx, runtimeID, resp.PendingMachineUpgrade)
	}
	if resp.PendingRestart != nil {
		d.logger.Info("remote restart requested", "runtime_id", runtimeID, "restart_id", resp.PendingRestart.ID)
		d.triggerRestart()
	}
	for _, pending := range resp.PendingAgentLifecycleOperations {
		go d.handleAgentLifecycleOperation(ctx, pending)
	}
	for _, pending := range resp.PendingAgentStartIntents {
		go d.handleAgentStartIntent(ctx, pending)
	}
	if resp.PendingModelList != nil {
		if rt := d.findRuntime(runtimeID); rt != nil {
			go d.handleModelList(ctx, *rt, resp.PendingModelList.ID)
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
}

// releaseManifestBaseURLOverride returns the most recent non-empty
// server-dispatched release-manifest base URL seen on a heartbeat ack, or ""
// if none has arrived yet. Passed to cli.releaseManifestBaseURLWithOverride
// by the auto-update loop so a server-side domain change takes effect within
// one heartbeat interval, no env var or redeploy required.
func (d *Daemon) releaseManifestBaseURLOverride() string {
	v, _ := d.serverReleaseManifestBaseURL.Load().(string)
	return v
}

// handleModelList resolves the provider's supported models (via static
// catalog or by shelling out to the agent CLI) and reports the result
// back to the server. Model discovery failures are reported as empty
// lists rather than errors so the UI can still render a creatable
// dropdown.
func (d *Daemon) handleModelList(ctx context.Context, rt Runtime, requestID string) {
	d.logger.Info("model list requested", "runtime_id", rt.ID, "request_id", requestID, "provider", rt.Provider)

	entry, ok := d.cfg.Agents[rt.Provider]
	if !ok {
		d.reportModelListResult(ctx, rt, requestID, map[string]any{
			"status": "failed",
			"error":  fmt.Sprintf("no agent configured for provider %q", rt.Provider),
		})
		return
	}

	models, err := agent.ListModels(ctx, rt.Provider, entry.Path)
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

// handleUpdate performs the CLI update when triggered by the server via heartbeat.
func (d *Daemon) handleUpdate(ctx context.Context, runtimeID string, update *PendingUpdate) {
	// Desktop-managed daemons share their CLI binary with the Electron app,
	// which is responsible for shipping and replacing it. Letting the daemon
	// self-update would just get overwritten on the next Desktop launch and
	// could brick the embedded binary mid-update. Refuse cleanly.
	if d.cfg.LaunchedBy == "desktop" {
		d.logger.Info("refusing CLI self-update: daemon is managed by Desktop", "runtime_id", runtimeID, "update_id", update.ID)
		if d.beginUpdateObservation("server", "disabled", update.TargetVersion) {
			d.finishUpdateObservation("disabled", "update_failed", update.TargetVersion, "desktop_managed", "The CLI is managed by Multica Desktop.")
		}
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "CLI is managed by Multica Desktop — update the Desktop app to upgrade the CLI",
		})
		return
	}

	// Prevent concurrent update attempts.
	if !d.updating.CompareAndSwap(false, true) {
		d.logger.Warn("update already in progress, refusing server request", "runtime_id", runtimeID, "update_id", update.ID)
		// PopPending has already transitioned this request to running on the
		// server. Terminate that canonical request without touching the
		// current attempt owner's observation; otherwise an auto-update
		// metadata fetch can strand the request until its running timeout.
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "update_already_in_progress",
		})
		return
	}
	restartTriggered := false
	defer func() {
		if !restartTriggered {
			d.updating.Store(false)
		}
	}()

	d.logger.Info("CLI update requested", "runtime_id", runtimeID, "update_id", update.ID, "target_version", update.TargetVersion)
	if !d.beginUpdateObservation("server", "updating", update.TargetVersion) {
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "failed_to_persist_update_observation",
		})
		return
	}

	// Report running status.
	d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
		"status": "running",
	})

	output, err := d.runUpdateFn(update.TargetVersion)
	if err != nil {
		d.logger.Error("CLI update failed", "error", err, "output", output)
		d.finishUpdateObservation(d.idleUpdateObservationPhase(), "update_failed", update.TargetVersion, "download_update_failed", "The release download update failed.")
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}

	verifiedVersion, err := d.verifyUpdatedBinaryVersion(update.TargetVersion, output)
	if err != nil {
		d.logger.Error("CLI update verification failed", "error", err, "output", output)
		d.finishUpdateObservation(d.idleUpdateObservationPhase(), "verification_failed", update.TargetVersion, "updated_binary_verification_failed", "The updated CLI version could not be verified.")
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}

	d.logger.Info("CLI update staged successfully", "output", output, "verified_version", verifiedVersion)
	// The binary is verified, but restart is not yet pending: the server may
	// reject ready_to_apply or an older server may find the daemon busy. Keep
	// the terminal attempt outcome durable without claiming a restart until
	// the request acknowledgement and/or claim barrier actually permit one.
	if !d.finishUpdateObservation(d.idleUpdateObservationPhase(), "update_succeeded", update.TargetVersion, "", "") {
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "failed_to_persist_update_succeeded_observation",
		})
		return
	}
	stagedOutput := fmt.Sprintf("Staged %s (verified stable binary %s)", update.TargetVersion, verifiedVersion)
	if update.SupportsReadyToApply {
		if err := d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "ready_to_apply",
			"output": stagedOutput,
		}); err != nil {
			d.logger.Error("CLI update staged but ready-to-apply state was not durably acknowledged; refusing restart", "runtime_id", runtimeID, "update_id", update.ID, "error", err)
			return
		}
		if !d.finishUpdateObservation("restart_pending", "update_succeeded", update.TargetVersion, "", "") {
			d.logger.Error("CLI update is ready to apply but restart-pending observation was not durable; refusing restart", "runtime_id", runtimeID, "update_id", update.ID)
			return
		}
		restartCtx := d.rootCtx
		if restartCtx == nil {
			restartCtx = context.Background()
		}
		if d.abortStagedRestartIfCanceled(restartCtx, runtimeID, update.ID, false) {
			return
		}
		// #105 (Frank c): server/page InitiateUpdate (SupportsReadyToApply) force-applies
		// immediately — no waitForSafeRestart idle window. Busy runtimes still activate
		// + restart; setClaimBarrier blocks new claims while the process is going down.
		// #110: never restart without activateStagedAndRestart (no bare triggerRestart).
		d.setClaimBarrier()
		if d.abortStagedRestartIfCanceled(restartCtx, runtimeID, update.ID, true) {
			return
		}
		d.logger.Info("CLI update ready; force activating staged release and restarting", "runtime_id", runtimeID, "update_id", update.ID, "active_tasks", d.activeTasks.Load(), "claims_in_flight", d.claimsInFlight)
		restartTriggered = d.activateStagedAndRestart(restartCtx, runtimeID, update.ID, update.TargetVersion, stagedOutput)
		return
	}

	if !d.trySetClaimBarrier() {
		d.logger.Warn("CLI update staged but server does not support deferred apply; refusing to restart a busy daemon", "runtime_id", runtimeID, "update_id", update.ID)
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "update_ready_to_apply_but_server_does_not_support_deferred_restart",
		})
		return
	}
	if !d.finishUpdateObservation("restart_pending", "update_succeeded", update.TargetVersion, "", "") {
		d.releaseClaimBarrier()
		d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
			"status": "failed",
			"error":  "failed_to_persist_restart_pending_observation",
		})
		return
	}
	// Older servers do not complete a still-running update when the daemon
	// re-registers on the target version. Preserve the old idle-only wire
	// behavior for that mixed-version window.
	d.reportUpdateResult(ctx, runtimeID, update.ID, map[string]any{
		"status": "completed",
		"output": stagedOutput,
	})
	// #110: old-server idle path must activate staged Active before restart too.
	restartTriggered = d.activateStagedAndRestart(ctx, runtimeID, update.ID, update.TargetVersion, stagedOutput)
}

// runUpdate stages targetVersion into the immutable VersionStore. It does not
// self-replace the process executable (#815 B-cutover). Activation/CAS and
// restart from the staged path are owned by handleUpdate / activate path.
func (d *Daemon) runUpdate(targetVersion string) (string, error) {
	return d.runStageUpdate(targetVersion)
}

// activateStagedAndRestart is the sole post-stage restart entry for handleUpdate.
// It CAS-activates the staged release, sets d.restartBinary, then triggerRestart.
// SupportsReadyToApply (#105 force-apply) and the legacy idle-only server path
// must both call this — never triggerRestart alone after a staged update (#110).
// Returns true if restart was scheduled; false if activation failed (path A
// abandon, no restart).
func (d *Daemon) activateStagedAndRestart(ctx context.Context, runtimeID, updateID, targetVersion, output string) bool {
	// Thin activate: CAS staged tag to Active, then re-exec staged path.
	// Full candidate health/register is a follow-up; path A already safe.
	activate := d.activateStagedFn
	if activate == nil {
		activate = d.commitStagedActivation
	}
	if path, err := activate(ctx, updateID, targetVersion); err != nil {
		d.logger.Error("CLI update activate CAS failed; path A abandon", "error", err, "runtime_id", runtimeID, "update_id", updateID)
		d.abandonStagedUpdatePathA(ctx, runtimeID, updateID, output)
		return false
	} else if path != "" {
		d.restartBinary = path
	}
	d.logger.Info("CLI update ready; daemon drained, restarting from staged Active", "runtime_id", runtimeID, "update_id", updateID, "output", output, "binary", d.restartBinary)
	d.triggerRestart()
	return true
}

func (d *Daemon) waitForSafeRestart(ctx context.Context, runtimeID, updateID, targetVersion, output string) bool {
	interval := d.cfg.PollInterval
	if interval <= 0 || interval > 15*time.Second {
		interval = 5 * time.Second
	}
	return d.waitForSafeRestartWithWindow(
		ctx,
		runtimeID,
		updateID,
		targetVersion,
		output,
		stagedUpdateOpportunisticIdleWindow,
		interval,
	)
}

const stagedUpdateOpportunisticIdleWindow = 10 * time.Minute

// stagedUpdateHardDrainExtra is T_hard − T_idle (design D6). After the
// opportunistic idle window the barrier is forced; if still not drained by
// T_hard the attempt is abandoned with typed drain_timeout (path A).
const stagedUpdateHardDrainExtra = 5 * time.Minute

// stagedUpdateHardDrainTotal is T_hard from T0 (stage ready / ready_to_apply).
const stagedUpdateHardDrainTotal = stagedUpdateOpportunisticIdleWindow + stagedUpdateHardDrainExtra

func (d *Daemon) abortStagedRestartIfCanceled(
	ctx context.Context,
	runtimeID, updateID string,
	barrierHeld bool,
) bool {
	if ctx.Err() == nil {
		return false
	}
	if barrierHeld {
		d.releaseClaimBarrier()
	}
	d.finishUpdateObservationWithoutRestart()
	d.logger.Warn("CLI update ready but restart wait ended before the daemon drained", "runtime_id", runtimeID, "update_id", updateID, "error", ctx.Err())
	return true
}

// abandonStagedUpdatePathA releases the barrier, reports failed+drain_timeout,
// and leaves the committed Active binary untouched (CUT-T1/T2). Used at T_hard.
func (d *Daemon) abandonStagedUpdatePathA(ctx context.Context, runtimeID, updateID, output string) {
	d.releaseClaimBarrier()
	d.finishUpdateObservation(d.idleUpdateObservationPhase(), "update_failed", "", "drain_timeout", "Activation abandoned: drain did not complete within T_hard.")
	d.logger.Warn("CLI update path A abandon at T_hard (drain_timeout); committed Active unchanged",
		"runtime_id", runtimeID, "update_id", updateID, "output", compactUpdateOutput(output))
	if d.client == nil {
		return
	}
	_ = d.reportUpdateResult(ctx, runtimeID, updateID, map[string]any{
		"status": "failed",
		"error":  handlerDrainTimeoutError(),
	})
}

// handlerDrainTimeoutError returns the stable machine string for path A.
// Defined as a function so daemon does not import handler package.
func handlerDrainTimeoutError() string {
	return "drain_timeout"
}

func (d *Daemon) finishUpdateObservationWithoutRestart() {
	if d.updateObservation == nil {
		return
	}
	current := d.updateObservation.Snapshot()
	d.finishUpdateObservation(d.idleUpdateObservationPhase(), "update_succeeded", current.TargetVersion, "", "")
}

func (d *Daemon) waitForSafeRestartWithWindow(
	ctx context.Context,
	runtimeID, updateID, targetVersion, output string,
	opportunisticWindow, interval time.Duration,
) bool {
	return d.waitForSafeRestartWithWindows(
		ctx,
		runtimeID,
		updateID,
		targetVersion,
		output,
		opportunisticWindow,
		stagedUpdateHardDrainExtra,
		interval,
	)
}

// waitForSafeRestartWithWindows is the testable form with explicit T_idle and
// T_hard−T_idle windows. At T_hard without drain → path A abandon (no restart).
func (d *Daemon) waitForSafeRestartWithWindows(
	ctx context.Context,
	runtimeID, updateID, targetVersion, output string,
	opportunisticWindow, hardExtra, interval time.Duration,
) bool {
	if d.abortStagedRestartIfCanceled(ctx, runtimeID, updateID, false) {
		return false
	}
	// opportunisticWindow may be 0 (tests: fire idle deadline immediately).
	// Only negative means "use production default".
	if opportunisticWindow < 0 {
		opportunisticWindow = stagedUpdateOpportunisticIdleWindow
	}
	if hardExtra <= 0 {
		hardExtra = stagedUpdateHardDrainExtra
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	idleDeadline := time.NewTimer(opportunisticWindow)
	defer idleDeadline.Stop()
	hardDeadline := time.NewTimer(opportunisticWindow + hardExtra)
	defer hardDeadline.Stop()
	draining := false

	tryRestart := func(barrierHeld bool) bool {
		if d.abortStagedRestartIfCanceled(ctx, runtimeID, updateID, barrierHeld) {
			return false
		}
		return d.activateStagedAndRestart(ctx, runtimeID, updateID, targetVersion, output)
	}

	for {
		select {
		case <-ctx.Done():
			d.abortStagedRestartIfCanceled(ctx, runtimeID, updateID, draining)
			return false
		case <-hardDeadline.C:
			// D6 path A: T_hard reached without successful restart.
			if d.abortStagedRestartIfCanceled(ctx, runtimeID, updateID, draining) {
				return false
			}
			d.logger.Warn("CLI update T_hard reached without drain complete", "runtime_id", runtimeID, "update_id", updateID)
			d.abandonStagedUpdatePathA(ctx, runtimeID, updateID, output)
			return false
		case <-idleDeadline.C:
			d.setClaimBarrier()
			draining = true
			if d.abortStagedRestartIfCanceled(ctx, runtimeID, updateID, true) {
				return false
			}
			d.logger.Info("CLI update stop-claim deadline reached; draining already-claimed tasks before restart", "runtime_id", runtimeID, "update_id", updateID)
			if !d.claimBarrierDrained() {
				continue
			}
			return tryRestart(true)
		case <-ticker.C:
			if draining {
				if !d.claimBarrierDrained() {
					continue
				}
				return tryRestart(true)
			}
			if !d.trySetClaimBarrier() {
				continue
			}
			return tryRestart(true)
		}
	}
}

const (
	updatedBinaryVersionCheckTimeout = 10 * time.Second
	updateFailureOutputLimit         = 1200
)

func (d *Daemon) verifyUpdatedBinaryVersion(targetVersion, updateOutput string) (string, error) {
	if d.verifyUpdatedBinaryFn != nil {
		return d.verifyUpdatedBinaryFn(targetVersion, updateOutput)
	}
	return d.verifyUpdatedBinary(targetVersion, updateOutput)
}

func (d *Daemon) verifyUpdatedBinary(targetVersion, updateOutput string) (string, error) {
	// #815: after StageRelease, truth is the immutable staged binary — not the
	// still-running process executable (which must remain committed Active).
	return d.verifyStagedBinary(targetVersion, updateOutput)
}

func compactUpdateOutput(output string) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(output)), " ")
	if len(compact) <= updateFailureOutputLimit {
		return compact
	}
	return compact[:updateFailureOutputLimit] + "...(truncated)"
}

// updateReportBackoffs defines the retry schedule for delivering CLI update
// status back to the server. This mirrors localSkillReportBackoffs because
// both features have the same user-visible failure mode: the daemon completed
// work locally, but a transient report failure leaves the UI waiting until the
// server-side request times out.
//
// Overridable for tests to avoid real sleeps.
var updateReportBackoffs = []time.Duration{0, 500 * time.Millisecond, 2 * time.Second, 4 * time.Second}

func (d *Daemon) reportUpdateResult(ctx context.Context, runtimeID, updateID string, payload map[string]any) error {
	return d.reportUpdateResultWithRetry(ctx, runtimeID, updateID, func(ctx context.Context) error {
		return d.client.ReportUpdateResult(ctx, runtimeID, updateID, payload)
	})
}

func (d *Daemon) reportUpdateResultWithRetry(ctx context.Context, runtimeID, updateID string, fn func(context.Context) error) error {
	var lastErr error
	for attempt, wait := range updateReportBackoffs {
		if wait > 0 {
			select {
			case <-ctx.Done():
				d.logger.Error("CLI update report cancelled",
					"runtime_id", runtimeID, "update_id", updateID,
					"attempt", attempt, "error", ctx.Err())
				return ctx.Err()
			case <-time.After(wait):
			}
		}

		err := fn(ctx)
		if err == nil {
			if attempt > 0 {
				d.logger.Info("CLI update report succeeded after retry",
					"runtime_id", runtimeID, "update_id", updateID,
					"attempt", attempt+1)
			}
			return nil
		}
		lastErr = err

		var reqErr *requestError
		if errors.As(err, &reqErr) && reqErr.StatusCode >= 400 && reqErr.StatusCode < 500 {
			d.logger.Error("CLI update report rejected — not retrying",
				"runtime_id", runtimeID, "update_id", updateID,
				"status", reqErr.StatusCode, "error", err)
			return err
		}

		d.logger.Warn("CLI update report failed — will retry",
			"runtime_id", runtimeID, "update_id", updateID,
			"attempt", attempt+1, "error", err)
	}
	d.logger.Error("CLI update report exhausted retries",
		"runtime_id", runtimeID, "update_id", updateID, "error", lastErr)
	return lastErr
}

// tryEnterClaim records the intent to call ClaimTask. Returns true if the
// caller may proceed, false if the Machine Upgrade handoff barrier is in effect. Every
// successful call MUST be paired with an exitClaim() on every exit path —
// either right after a failed/empty claim, or via the handleTask goroutine's
// defer once the task is handed off.
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

// claimBarrierDrained reports whether the stop-claim barrier is held and all
// work admitted before it has left both the claim handoff and active-task
// phases. Reading the counters under claimMu preserves the handoff boundary
// established by tryEnterClaim/exitClaim.
func (d *Daemon) claimBarrierDrained() bool {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	return d.pauseClaims && d.claimsInFlight == 0 && d.activeTasks.Load() == 0
}

// releaseClaimBarrier clears the Machine Upgrade claim barrier so pollers may
// resume claiming. Called on failure paths only — a successful upgrade leaves
// the barrier set because triggerRestart is about to take the process down
// and clearing it would open a window for new claims during shutdown.
func (d *Daemon) releaseClaimBarrier() {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	d.pauseClaims = false
}

// triggerRestart initiates a graceful daemon restart after a successful CLI update.
// If restartBinary was already set (e.g. staged VersionStore Active path), that
// path is preferred. Otherwise falls back to brew symlink / current executable.
// The caller (cmd_daemon.go) checks RestartBinary() and launches the new process.
func (d *Daemon) triggerRestart() {
	newBin := strings.TrimSpace(d.restartBinary)
	if newBin == "" {
		var err error
		newBin, err = d.restartBinaryPath()
		if err != nil {
			d.logger.Error("could not resolve executable path for restart", "error", err)
			return
		}
	}

	d.logger.Info("scheduling daemon restart", "new_binary", newBin)
	d.restartBinary = newBin

	// Cancel the main context to trigger graceful shutdown.
	if d.cancelFunc != nil {
		d.cancelFunc()
	}
}

func (d *Daemon) restartBinaryPath() (string, error) {
	newBin, err := os.Executable()
	if err != nil {
		return "", err
	}
	// A staged Active version takes priority over the currently running
	// binary's own path (task #41): d.restartBinary is normally already set
	// from the staged path by the time triggerRestart runs, so this is only
	// reached when triggerRestart fires without a prior update — still worth
	// consulting the VersionStore for the same reason the manual `daemon
	// restart` path does (resolveDaemonLaunchBinary). Brew installs manage
	// their own binary outside the VersionStore and skip this lookup.
	if !isBrewInstall() {
		if store, storeErr := cli.OpenVersionStore(""); storeErr == nil {
			if activePath, ok, activeErr := store.ActiveBinaryPath(); activeErr == nil && ok {
				return activePath, nil
			}
		}
	}
	// On Linux, os.Executable() reads /proc/self/exe, which the kernel resolves
	// to the Cellar path. brew cleanup deletes that path after upgrade, so we
	// must use the stable <brew-prefix>/bin/multica symlink instead.
	if isBrewInstall() {
		if brewPrefix := getBrewPrefix(); brewPrefix != "" {
			newBin = filepath.Join(brewPrefix, "bin", "multica")
		} else if prefix := matchKnownBrewPrefix(newBin); prefix != "" {
			newBin = filepath.Join(prefix, "bin", "multica")
		} else {
			d.logger.Warn("brew install detected but prefix could not be resolved; restart may fail",
				"executable", newBin)
		}
	} else {
		if resolved, err := filepath.EvalSymlinks(newBin); err == nil {
			newBin = resolved
		}
	}
	return newBin, nil
}

// pollLoop supervises one runtimePoller goroutine per registered runtime,
// fans wake-up signals out to all of them, and waits for in-flight tasks to
// drain on shutdown. Per-runtime workers replace the previous round-robin
// loop so that a slow ClaimTask call (HTTP 30s timeout) for one runtime no
// longer delays claims on every other runtime — that was the cross-workspace
// stall mode reported in MUL-1744.
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
			d.registerMachineUpgradeTask(slot, cancel)
			defer func() {
				d.unregisterMachineUpgradeTask(slot)
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
			if event.Task == nil {
				if err := d.client.AckAgentInboxEvent(ctx, lease); err != nil {
					// fail-soft: try to ack remaining leases before surfacing
					d.ackFoldedInboxLeasesBestEffort(ctx, append(folded, remainingLeasesAfter(batch.Events, event, runtimeID)...))
					return nil, err
				}
				d.logger.Debug("acked non-runnable inbox event", "runtime_id", runtimeID, "event", shortID(event.ID))
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
		// the full exchange and batchClientMessageID is stable for this turn.
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
		return task, nil
	}
}

func agentInboxLeaseFromEvent(event *AgentInboxEvent, runtimeID string) AgentInboxLease {
	return AgentInboxLease{
		ID:             event.ID,
		DeliveryID:     event.DeliveryID,
		ConversationID: event.ConversationID,
		LeaseToken:     event.LeaseToken,
		LeaseExpiresAt: event.LeaseExpiresAt,
		SeqFrom:        event.SeqFrom,
		SeqTo:          event.SeqTo,
		RequiresWake:   event.RequiresWake,
		Reason:         event.Reason,
		RuntimeID:      runtimeID,
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
func (d *Daemon) watchTaskCancellation(ctx context.Context, taskID string, pollInterval time.Duration, taskLog *slog.Logger) <-chan struct{} {
	cancelled := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, err := d.client.GetTaskStatus(ctx, taskID)
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

	// Publish this turn's inbox lease for credential-proxy batch cmid stamping
	// for the whole execution (including wait-for-slot). Cleared on exit.
	if task.isInboxTask() && strings.TrimSpace(task.AgentID) != "" {
		d.registerActiveInboxTurn(task.AgentID, *task.InboxEvent)
		defer d.clearActiveInboxTurn(task.AgentID)
	}

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
					taskLog.Info("agent inbox lease lost before execution; discarding delivery")
				default:
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

	if d.reminderAgents != nil {
		if d.reminderAgents.markRunning(task.AgentID, task.RuntimeID, task.WorkspaceID) {
			d.requestReminderSnapshot(task.AgentID)
		}
		defer d.reminderAgents.markIdle(task.AgentID)
	}

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
	// stuck one-shot backend to completion (or its own idle-watchdog
	// timeout) regardless. GetTaskStatus (handler/daemon.go) already maps
	// status="suppressed" to "cancelled" generically, with no inbox/legacy
	// distinction, so this is safe for inbox tasks without any server-side
	// change.
	cancelledByPoll := d.watchTaskCancellation(runCtx, task.ID, pollInterval, taskLog)
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
			taskLog.Error("start inbox execution ledger record failed", "error", err)
			d.reportTaskFailure(reportCtx, task, fmt.Sprintf("start inbox execution ledger record: %v", err), "", "", "execution_ledger_error", taskLog)
			return
		}
	}

	result, err := d.runner.run(runCtx, task, provider, slot, taskLog)
	result.ExecutionID = executionID
	result = restrictResultForExecutionProfile(result, profile)

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
			taskLog.Info("agent inbox lease lost during execution; usage recorded and result discarded")
			return
		default:
		}
	}

	// Check if we were cancelled by the polling goroutine.
	if cancelledByPoll != nil {
		select {
		case <-cancelledByPoll:
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
		_ = d.client.ReportProgress(ctx, task.ID, "Finishing task", 2, 2)
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
	if status, err := d.client.GetTaskStatus(ctx, task.ID); shouldInterruptAgent(status, err) {
		taskLog.Info("task cancelled during execution, discarding result", "status", status, "error", err)
		return
	}

	d.reportTaskResultForTask(ctx, task, result, taskLog)

}

func (d *Daemon) ensureTaskAgentCredential(ctx context.Context, task Task, taskLog *slog.Logger) (string, error) {
	return d.ensureAgentCredential(ctx, task.WorkspaceID, task.RuntimeID, task.AgentID, taskLog)
}

func (d *Daemon) ensureAgentCredential(ctx context.Context, workspaceID, runtimeID, agentID string, taskLog *slog.Logger) (string, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(agentID) == "" {
		return "", fmt.Errorf("workspace_id, runtime_id, and agent_id are required")
	}
	cached, cacheOK := readCachedAgentCredential(d.cfg, workspaceID, runtimeID, agentID, time.Now())
	cachedCredentialID := ""
	if cacheOK {
		cachedCredentialID = cached.CredentialID
	}
	resp, err := d.client.EnsureAgentCredential(ctx, runtimeID, agentID, cachedCredentialID)
	if err != nil {
		if isRuntimeTransitionInProgressError(err) {
			// task #38: the reassignment may still complete within the
			// server's grace window — do NOT drop the cached credential
			// (it could still be valid if this ends up being a false
			// alarm) and do not warn-log; this is expected traffic during
			// a normal machine move, not a fault.
			if taskLog != nil {
				taskLog.Debug("agent runtime transition in progress; will retry",
					"agent_id", shortID(agentID), "runtime_id", shortID(runtimeID))
			}
			return "", fmt.Errorf("%w: %s", errRuntimeTransitionInProgress, err.Error())
		}
		if isAgentNotBoundToRuntimeError(err) {
			// This agent was reassigned to a different runtime (agent.runtime_id
			// is a normal, user-editable field — not data corruption). Retrying
			// with the same local state can never succeed, so drop the now-stale
			// cached credential rather than let a future attempt reuse it if this
			// agent is ever reassigned back to this runtime.
			if removeErr := removeCachedAgentCredential(d.cfg, workspaceID, agentID); removeErr != nil && taskLog != nil {
				taskLog.Warn("failed to remove stale agent credential cache", "error", removeErr, "agent_id", shortID(agentID))
			}
			if taskLog != nil {
				taskLog.Warn("agent no longer bound to this runtime; this task's agent has moved elsewhere",
					"agent_id", shortID(agentID), "runtime_id", shortID(runtimeID))
			}
			return "", fmt.Errorf("%w: %s", errAgentReassignedElsewhere, err.Error())
		}
		return "", fmt.Errorf("ensure daemon agent credential: %w", err)
	}
	if resp.Reused {
		if !cacheOK || resp.ID != cached.CredentialID {
			return "", fmt.Errorf("ensure response reused an unexpected credential")
		}
		if taskLog != nil {
			taskLog.Info("agent credential cache validated", "credential_id", shortID(cached.CredentialID), "token_prefix", cached.Prefix)
		}
		return cached.Token, nil
	}
	cached, err = writeCachedAgentCredential(d.cfg, workspaceID, runtimeID, agentID, *resp, time.Now())
	if err != nil {
		return "", err
	}
	if taskLog != nil {
		taskLog.Info("agent credential ensured",
			"credential_id", shortID(cached.CredentialID),
			"token_prefix", cached.Prefix,
			"rotation_reason", resp.RotationReason,
		)
	}
	return cached.Token, nil
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
	switch result.Status {
	case "completed":
		taskLog.Info("task completed", "status", result.Status)
		if !task.isInboxTask() {
			taskLog.Error("task is missing its canonical inbox lease")
			return
		}
		_, err := d.client.CompleteAgentInboxEvent(ctx, *task.InboxEvent, result)
		if err == nil {
			// Primary completed with agent output; ack folded leases so none
			// remain leased for reclaim (Alice boundary #1).
			d.ackFoldedInboxLeases(ctx, task, taskLog)
			if result.Status == "completed" {
				d.reportAgentMemoryWrites(ctx, task)
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
			taskLog.Error("complete task failed after retries; leaving task in running rather than falling back to fail", "error", err)
			return
		}
		if task.InboxEvent != nil &&
			task.InboxEvent.Reason == protocol.ChannelOnboardingReason &&
			result.ChannelOnboardingDecision == "" {
			taskLog.Error("channel onboarding completion rejected without send or typed skip; leaving delivery for lease retry", "error", err)
			return
		}
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
	// The Runner, rather than the legacy Activity API, is the only execution
	// observation channel. Keep this narrative deliberately generic: detailed
	// failures remain in the authorized task result, never in Activity.
	d.publishTaskRunnerActivity(task, protocol.ActivityKindError, "task_failed", "Agent execution failed")
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
		taskLog.Error("report failed inbox event failed", "error", err)
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

func (d *Daemon) registerActiveInboxTurn(agentID string, lease AgentInboxLease) {
	if d == nil || strings.TrimSpace(agentID) == "" || lease.ID == "" {
		return
	}
	d.activeInboxTurns.Store(agentID, lease)
}

func (d *Daemon) clearActiveInboxTurn(agentID string) {
	if d == nil || strings.TrimSpace(agentID) == "" {
		return
	}
	d.activeInboxTurns.Delete(agentID)
}

// lookupActiveInboxTurn returns the in-flight inbox lease for agentID, if any.
func (d *Daemon) lookupActiveInboxTurn(agentID string) (AgentInboxLease, bool) {
	if d == nil || strings.TrimSpace(agentID) == "" {
		return AgentInboxLease{}, false
	}
	v, ok := d.activeInboxTurns.Load(agentID)
	if !ok {
		return AgentInboxLease{}, false
	}
	lease, ok := v.(AgentInboxLease)
	if !ok || lease.ID == "" {
		return AgentInboxLease{}, false
	}
	return lease, true
}

// fillTurnIdentityFromActiveInboxTurn fills missing ConversationID/Seq* on a
// credential-proxy send from the agent's in-flight inbox lease. Only applies
// when an inbox turn is active (Alice: do not force-hold non-turn / draft /
// proactive sends that never had turn context).
func (d *Daemon) fillTurnIdentityFromActiveInboxTurn(request *credentialProxyMessageSendRequest) bool {
	if d == nil || request == nil {
		return false
	}
	if request.ConversationID != "" && request.SeqFrom > 0 && request.SeqTo >= request.SeqFrom {
		return false // already complete
	}
	lease, ok := d.lookupActiveInboxTurn(request.AgentID)
	if !ok || lease.ConversationID == "" || lease.SeqFrom <= 0 || lease.SeqTo < lease.SeqFrom {
		return false
	}
	if request.ConversationID == "" {
		request.ConversationID = lease.ConversationID
	}
	if request.SeqFrom <= 0 {
		request.SeqFrom = lease.SeqFrom
	}
	if request.SeqTo < request.SeqFrom {
		request.SeqTo = lease.SeqTo
	}
	return request.ConversationID != "" && request.SeqFrom > 0 && request.SeqTo >= request.SeqFrom
}

// providerNeedsInlineSystemPrompt is sourced from agent.Capabilities
// (task #47) — do not re-list providers here.
func providerNeedsInlineSystemPrompt(provider string) bool {
	return agent.Capabilities(provider).NeedsInlineSystemPrompt
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

func isChannelOnboardingSkipReceipt(task Task, output string) bool {
	return task.InboxEvent != nil &&
		task.InboxEvent.Reason == protocol.ChannelOnboardingReason &&
		strings.TrimSpace(output) == protocol.ChannelOnboardingSkipReceipt
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
	executionMemories := serverMemories
	if !restrictedExecution {
		executionMemories, _ = prepareExecutionMemory(agentRootPath, memoryTask, serverMemories)
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
		FreshSessionNoticeReason:         task.FreshSessionNoticeReason,
		AgentID:                          agentID,
		AgentName:                        agentName,
		ManagedRole:                      managedRole,
		AgentInstructions:                instructions,
		AgentRoot:                        agentRootPath,
		AgentSkills:                      convertSkillsForEnv(skills),
		AgentMemories:                    executionMemories,
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
	codexVersion := d.agentVersion("codex")
	openclawBin := ""
	if provider == "openclaw" {
		openclawBin = entry.Path
	}
	var agentMcpConfig json.RawMessage
	if task.Agent != nil {
		agentMcpConfig = task.Agent.McpConfig
	}
	env := execenv.Reuse(execenv.ReuseParams{
		AgentRoot:    executionRoot,
		Provider:     provider,
		CodexVersion: codexVersion,
		OpenclawBin:  openclawBin,
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
	agentToken := task.AuthToken
	durableAgentToken := false
	if task.isInboxTask() && agentToken == "" && !restrictedExecution {
		token, err := d.ensureTaskAgentCredential(ctx, task, taskLog)
		if err != nil {
			if errors.Is(err, errRuntimeTransitionInProgress) {
				// task #38: do not report a terminal TaskResult at all — an
				// in-progress transition is not this task's failure to own,
				// it is exactly the kind of internal noise the grace window
				// exists to keep off the user's activity feed. Returning a
				// plain error (not a TaskResult{Status:"failed",...}) routes
				// through the same un-reported retry path setup failures
				// above already use, so the caller's normal retry loop picks
				// this task back up rather than the server ever seeing an
				// outcome for it.
				return TaskResult{}, err
			}
			if errors.Is(err, errAgentReassignedElsewhere) {
				return TaskResult{
					Status:        "failed",
					Comment:       "agent_reassigned_elsewhere: this agent is now running on a different computer; no action needed on this machine",
					FailureReason: "agent_reassigned_elsewhere",
				}, nil
			}
			return TaskResult{
				Status:        "failed",
				Comment:       fmt.Sprintf("credential_unavailable: %s", err.Error()),
				FailureReason: "credential_unavailable",
			}, nil
		}
		agentToken = token
		durableAgentToken = true
	}
	cliWrapperDir := ""
	cliTokenFile := ""
	cliBinDir := ""
	transportAttemptPath := ""
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
		selfBin, err := resolveExecutable()
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
				if durableAgentToken {
					defer func() {
						if err := os.RemoveAll(wrapperDir); err != nil {
							taskLog.Warn("agent cli transport cleanup failed", "wrapper_dir", wrapperDir, "error", err)
						}
					}()
				}
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
		// Turn-at-most-once batch identity: the conversation + seq range carried by
		// this turn's inbox event. Exposed so `multica message send` can stamp the
		// send with a stable client_message_id for the whole batch (dedup), while a
		// different batch/turn gets a different id.
		if task.InboxEvent.ConversationID != "" {
			agentEnv["MULTICA_TURN_CONVERSATION_ID"] = task.InboxEvent.ConversationID
		}
		if task.InboxEvent.SeqFrom > 0 {
			agentEnv["MULTICA_TURN_SEQ_FROM"] = strconv.FormatInt(task.InboxEvent.SeqFrom, 10)
		}
		if task.InboxEvent.SeqTo > 0 {
			agentEnv["MULTICA_TURN_SEQ_TO"] = strconv.FormatInt(task.InboxEvent.SeqTo, 10)
		}
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
		if provider == "pi" {
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
		pathPrefix := cliBinDir
		if cliWrapperDir != "" {
			agentEnv["MULTICA_TOKEN_FILE"] = cliTokenFile
			pathPrefix = cliWrapperDir + string(os.PathListSeparator) + cliBinDir
		}
		agentEnv["PATH"] = pathPrefix + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	// Point Codex to the Agent-scoped CODEX_HOME so it discovers skills natively
	// without polluting the system ~/.codex/skills/.
	if env.CodexHome != "" {
		agentEnv["CODEX_HOME"] = env.CodexHome
	}
	// Point OpenClaw at the Agent-scoped synthesized config. The config pins
	// agents.defaults.workspace (and any agents.list[].workspace) to the
	// Agent workspace, so the CLI's native skill scanner picks up Agent skills
	// skills written under {workDir}/skills/. Falls back silently when the
	// preparer didn't run (non-openclaw provider, or write failure).
	if env.OpenclawConfigPath != "" {
		agentEnv["OPENCLAW_CONFIG_PATH"] = env.OpenclawConfigPath
	}
	// Grant the wrapper config permission to $include the user's active
	// config across directories. OpenClaw's $include defaults to confining
	// resolution to the wrapper's own directory; without this, the
	// wrapper-out-of-envRoot $include into ~/.openclaw/openclaw.json is
	// rejected and the run boots with no user-registered agents.
	if rootsValue, ok := composeOpenclawIncludeRoots(env.OpenclawIncludeRoot, os.Getenv("OPENCLAW_INCLUDE_ROOTS")); ok {
		agentEnv["OPENCLAW_INCLUDE_ROOTS"] = rootsValue
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
	// Some providers do not reliably load the Agent-scoped runtime config files we
	// write into the Agent workspace:
	//   - kiro and kimi are wrapped through their own CLIs whose cwd handling
	//     is opaque enough that we can't trust the file-based path either.
	// OpenClaw is pinned to the Agent workspace by prepareOpenclawConfig and
	// loads AGENTS.md there, so inlining the same kernel would duplicate it.
	// Pass the compact runtime kernel inline only for providers whose file load
	// path is still opaque. Turn-specific workflow remains in the user prompt.
	//
	// Hermes is intentionally excluded: ACP sessions start in the task cwd and
	// Hermes loads AGENTS.md / .agent_context itself. Prepending the full runtime
	// brief into the ACP user prompt duplicates that context, bloats every turn,
	// and has triggered upstream safety filters on harmless tasks.
	if restrictedExecution || providerNeedsInlineSystemPrompt(provider) {
		execOpts.SystemPrompt = runtimeBrief
	}

	backend, createErr := agent.New(provider, backendCfg)
	if createErr != nil {
		return TaskResult{}, fmt.Errorf("create agent backend: %w", createErr)
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
		retryResult, retryTools, retryErr := d.executeAndDrainForTask(ctx, backend, prompt, execOpts, taskLog, task)
		if retryErr != nil {
			taskLog.Error("fresh session also failed to start", "error", retryErr)
		} else {
			result = retryResult
			result.Usage = mergeUsage(firstUsage, result.Usage)
			tools = retryTools
		}
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
		if isChannelOnboardingSkipReceipt(task, output) {
			taskLog.Info("agent produced typed channel onboarding skip receipt")
			return TaskResult{
				Status:                    "completed",
				Comment:                   "",
				Action:                    protocol.ChatOutputActionNoReply,
				Type:                      protocol.ChatOutputKindNoReply,
				ChannelOnboardingDecision: protocol.ChannelOnboardingDecisionSkipped,
				SessionID:                 result.SessionID,
				WorkDir:                   env.AgentRoot,
				Usage:                     usageEntries,
				RuntimeStats:              runtimeStats,
				TransportAttempted:        transportAttempted,
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
	case "idle_watchdog":
		// The idle watchdog force-stopped the run because the backend
		// went silent (e.g. claude blocked on a tool call against a
		// frozen child process). Route through the blocked path with a
		// dedicated failure_reason so the run leaves "running" state and
		// operators can tell idle-stop apart from a real timeout.
		comment := result.Error
		if comment == "" {
			comment = idleWatchdogReason(d.cfg.AgentIdleWatchdog)
		}
		return TaskResult{
			Status:        "blocked",
			Comment:       comment,
			SessionID:     result.SessionID,
			WorkDir:       env.AgentRoot,
			FailureReason: "idle_watchdog",
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

func transportUnavailableResult(stage string, err error) TaskResult {
	comment := "transport_unavailable: " + stage
	if err != nil {
		comment += ": " + err.Error()
	}
	return TaskResult{
		Status:        "failed",
		Comment:       comment,
		FailureReason: "transport_unavailable",
	}
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

func (d *Daemon) publishTaskRunnerActivity(task Task, activityKind, detailKind, narrative string) {
	if d == nil || task.AgentID == "" || task.WorkspaceID == "" {
		return
	}
	d.mu.Lock()
	producer := d.agentActivityProducers[task.WorkspaceID]
	d.mu.Unlock()
	if producer == nil {
		return
	}
	var entries []protocol.AgentActivityEntry
	if narrative != "" {
		entry, err := activityNarrativeEntry(activityKind, detailKind, narrative)
		if err == nil {
			entries = []protocol.AgentActivityEntry{entry}
		}
	}
	if err := producer.PublishForManagedAgent(task.AgentID, d.runnerInstanceID, activityKind, detailKind, entries); err != nil && d.logger != nil {
		d.logger.Debug("workspace Runner task Activity publish deferred", "error", err, "agent_id", task.AgentID, "task_id", task.ID)
	}
}

func (d *Daemon) executeAndDrainForTask(ctx context.Context, backend agent.Backend, prompt string, opts agent.ExecOptions, taskLog *slog.Logger, task Task) (agent.Result, int32, error) {
	taskID := task.ID
	// Wrap the caller's ctx so the idle watchdog (below) can interrupt both
	// the agent subprocess (via the ctx passed to backend.Execute) AND the
	// drain loop with a single cancel. Without this layer the backend would
	// stay tied to the parent ctx and our cancellation could only abort
	// drain, leaving the subprocess running.
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
	// lastActivityAt records (as unix nanos) when the drain loop most
	// recently received a message from the backend. The idle watchdog
	// reads this to decide whether the agent has gone silent for too long.
	// Initialise to the start so a backend that never emits a single
	// message also trips the watchdog.
	var lastActivityAt atomic.Int64
	lastActivityAt.Store(time.Now().UnixNano())
	// activitySeq is a monotonic generation for backend messages. The idle
	// watchdog snapshots it before each runtime probe so progress drained while
	// the probe is blocked cannot be hidden by probe-return wall-clock timing.
	var activitySeq atomic.Uint64
	// inFlightTools counts tool_use messages that haven't yet been paired
	// with a matching tool_result. A non-zero count means the agent is
	// legitimately waiting on a tool (e.g. `npm install`, `docker build`)
	// that may run far longer than the idle window without emitting any
	// message — so while a tool is in flight the watchdog applies the larger
	// AgentToolWatchdog budget instead of treating that silence as a hang.
	var inFlightTools atomic.Int32
	var idleWatchdogFired atomic.Bool
	// idleWatchdogThreshold records (as nanos) which silence budget actually
	// tripped the watchdog — the idle window or the larger in-flight-tool
	// window — so the failure message reports the real duration.
	var idleWatchdogThreshold atomic.Int64
	idleWatchdogThreshold.Store(int64(d.cfg.AgentIdleWatchdog))
	idleWindow := d.cfg.AgentIdleWatchdog
	if idleWindow > 0 {
		go d.runIdleWatchdog(agentCtx, idleWindow, d.cfg.AgentToolWatchdog, &lastActivityAt, &activitySeq, &inFlightTools, &idleWatchdogFired, &idleWatchdogThreshold, agentCancel, session.Messages, session.RuntimeAlive, taskLog, taskID)
	}

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
				// Stamp activity as soon as a message lands. The idle
				// watchdog reads this to decide whether the backend has
				// gone silent — stamping before processing makes sure a
				// slow downstream call (mu.Lock contention, batch resize)
				// can't be misattributed to backend silence.
				lastActivityAt.Store(time.Now().UnixNano())
				activitySeq.Add(1)
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
						pinCtx, pinCancel := context.WithTimeout(context.Background(), 5*time.Second)
						if err := d.client.PinTaskSession(pinCtx, taskID, msg.SessionID, opts.Cwd); err != nil {
							taskLog.Warn("pin task session failed (task still runs; resume pointer lost for this cycle)", "error", err)
						}
						pinCancel()
					}
				case agent.MessageToolUse:
					toolDetailKind, toolNarrative := toolActivityFact(msg.Tool, msg.Input)
					d.publishTaskRunnerActivity(task, protocol.ActivityKindWorking, toolDetailKind, toolNarrative)
					n := toolCount.Add(1)
					inFlightTools.Add(1)
					taskLog.Info(fmt.Sprintf("tool #%d: %s", n, msg.Tool))
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
					// Decrement only when the count would stay >= 0. A stray
					// tool_result with no matching tool_use (backend bug or
					// reconnect mid-stream) shouldn't push the counter
					// negative — that would re-arm the watchdog one tool_use
					// too early on the next call.
					for {
						cur := inFlightTools.Load()
						if cur <= 0 {
							break
						}
						if inFlightTools.CompareAndSwap(cur, cur-1) {
							break
						}
					}
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
					// Thinking is a B-chain state (snapshot activity_kind), not an
					// A-chain timeline event. An empty narrative keeps publishLocked
					// from writing an entry, so bursts of thinking never flood the
					// activity timeline (raft-aligned; see workspace_runner_activity).
					d.publishTaskRunnerActivity(task, protocol.ActivityKindThinking, "", "")
					if msg.Content != "" {
						mu.Lock()
						trajectory.append("thinking", msg.Content, msg.Lineage, time.Now(), emitTrajectory)
						mu.Unlock()
					}
				case agent.MessageCompactionStarted, agent.MessageCompactionFinished:
					if msg.Type == agent.MessageCompactionStarted {
						d.publishTaskRunnerActivity(task, protocol.ActivityKindWorking, "compacting_context", "Compacting context")
					} else {
						d.publishTaskRunnerActivity(task, protocol.ActivityKindOnline, "idle", "Context compaction finished")
					}
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
						mu.Lock()
						trajectory.append("text", msg.Content, msg.Lineage, time.Now(), emitTrajectory)
						mu.Unlock()
					}
				case agent.MessageError:
					d.publishTaskRunnerActivity(task, protocol.ActivityKindError, "", "Runtime error")
					taskLog.Error("agent error", "content", msg.Content)
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
		if idleWatchdogFired.Load() {
			// The backend's wait goroutine (e.g. claude.go) translates the
			// SIGKILL we delivered via agentCancel into Status="aborted".
			// Re-tag it as "idle_watchdog" so runTask routes the
			// disposition through a dedicated failure_reason, not the
			// generic "agent_error" bucket the aborted path falls into.
			result.Status = "idle_watchdog"
			if result.Error == "" {
				result.Error = idleWatchdogReason(time.Duration(idleWatchdogThreshold.Load()))
			}
		}
		return result, toolCount.Load(), nil
	case <-drainCtx.Done():
		// Idle watchdog cancels via agentCancel(), which propagates here as
		// context.Canceled. Check this BEFORE the generic cancelled/timeout
		// classifiers so a watchdog-induced stop isn't misreported as
		// "task cancelled by server".
		if idleWatchdogFired.Load() {
			return agent.Result{
				Status: "idle_watchdog",
				Error:  idleWatchdogReason(time.Duration(idleWatchdogThreshold.Load())),
			}, toolCount.Load(), nil
		}
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

// idleWatchdogReason formats the human-facing explanation surfaced on
// idle_watchdog dispositions. Centralised so the result-arrival branch and the
// drain-timeout branch in executeAndDrain emit identical wording.
func idleWatchdogReason(window time.Duration) string {
	return fmt.Sprintf("runtime process was no longer alive after %s without progress; stopped by idle watchdog", window)
}

const idleWatchdogMaxTerminalSettleGrace = 5 * time.Second

// idleWatchdogTerminalSettleGrace leaves a bounded window for a provider to
// synthesize and publish its terminal Result after the child process exits.
// Short test/configured watchdog windows use twice their silence threshold;
// production windows cap the additional wait at five seconds.
func idleWatchdogTerminalSettleGrace(threshold time.Duration) time.Duration {
	if threshold <= 0 || threshold >= idleWatchdogMaxTerminalSettleGrace/2 {
		return idleWatchdogMaxTerminalSettleGrace
	}
	return 2 * threshold
}

// runIdleWatchdog ticks until either agentCtx is cancelled or the backend has
// been silent past the applicable budget. Silence alone is not termination
// evidence: once the budget is reached, the watchdog first probes the runtime
// child. A live child suppresses recovery, matching Raft's runtime-progress
// behavior. Only a confirmed-dead child with no buffered output can fire the
// watchdog. On firing, it records the tripped threshold, sets fired, and calls
// cancel, which propagates to backend.Execute and drainCtx. The silence budget
// depends on whether a tool call is in flight:
//
//  1. No tool in flight — probe after `window` without progress.
//  2. A tool in flight (tool_use with no matching tool_result yet) — a real
//     tool (e.g. `npm install`, `docker build`) legitimately runs silently for
//     many minutes, so the larger `toolWindow` applies instead. toolWindow <= 0
//     disables the in-flight probe.
//
// In both cases the watchdog also requires the session.Messages buffer to be
// empty — a buffered-but-undrained message means the drain loop is behind, not
// the backend.
//
// The silence tick interval is window/2 (floored at 30 s in production, but
// the floor only kicks in for windows >= 1 min so tests can pass tiny windows
// like 50 ms). Once death is first confirmed, a separate short timer schedules
// the second probe so recovery never waits for the next long silence tick.
func (d *Daemon) runIdleWatchdog(agentCtx context.Context, window, toolWindow time.Duration, lastActivityAt *atomic.Int64, activitySeq *atomic.Uint64, inFlightTools *atomic.Int32, fired *atomic.Bool, firedThreshold *atomic.Int64, cancel context.CancelFunc, messages <-chan agent.Message, runtimeAlive agent.RuntimeLivenessProbe, taskLog *slog.Logger, taskID string) {
	interval := window / 2
	if window >= time.Minute && interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if interval <= 0 {
		interval = window
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var suppressionLogged bool
	var deadConfirmedAt time.Time
	var deadConfirmedThreshold time.Duration
	var deadConfirmedActivitySeq uint64
	var settleTimer *time.Timer
	var settleTimerC <-chan time.Time
	defer func() {
		if settleTimer != nil {
			settleTimer.Stop()
		}
	}()
	resetDeadObservation := func() {
		deadConfirmedAt = time.Time{}
		deadConfirmedThreshold = 0
		deadConfirmedActivitySeq = 0
		if settleTimer != nil {
			if !settleTimer.Stop() {
				select {
				case <-settleTimer.C:
				default:
				}
			}
		}
		settleTimerC = nil
	}
	for {
		select {
		case <-agentCtx.Done():
			return
		case <-ticker.C:
		case <-settleTimerC:
			settleTimerC = nil
		}

		// Pick the silence budget. A tool in flight is expected to be
		// silent (a long build/install/test emits nothing between
		// tool_use and tool_result), so it gets the larger toolWindow;
		// toolWindow <= 0 disables the in-flight bound entirely.
		threshold := window
		toolInFlight := inFlightTools.Load() > 0
		if toolInFlight {
			if toolWindow <= 0 {
				resetDeadObservation()
				continue
			}
			threshold = toolWindow
		}
		// Snapshot before evaluating silence so the generation covers the whole
		// decision window, not only the potentially-blocking OS probe. Progress
		// that lands after this point must invalidate the observation.
		activityBeforeProbe := activitySeq.Load()
		last := time.Unix(0, lastActivityAt.Load())
		idleFor := time.Since(last)
		if idleFor < threshold {
			suppressionLogged = false
			resetDeadObservation()
			continue
		}
		// A buffered-but-undrained message means the drain loop is
		// behind, not the backend. Wait one more tick rather than
		// killing a backend that is still producing output.
		if len(messages) > 0 {
			suppressionLogged = false
			resetDeadObservation()
			continue
		}
		if activitySeq.Load() != activityBeforeProbe {
			suppressionLogged = false
			resetDeadObservation()
			continue
		}
		// Raft suppresses stale-progress recovery while the provider child is
		// alive. An unavailable probe is also not proof that the child died,
		// so fail open and keep the turn running instead of guessing.
		alive, known := false, false
		if runtimeAlive != nil {
			alive, known = runtimeAlive()
		}
		// The probe may block long enough for the drain loop to consume real
		// provider progress. Compare a monotonic generation rather than its
		// timestamp with probe-return time; otherwise progress during the probe
		// can look older than a newly-created dead observation and be lost.
		if len(messages) > 0 || activitySeq.Load() != activityBeforeProbe {
			suppressionLogged = false
			resetDeadObservation()
			continue
		}
		if !known || alive {
			resetDeadObservation()
			if !suppressionLogged {
				taskLog.Info("watchdog suppressed: runtime death is not confirmed despite silent progress",
					"task", shortID(taskID),
					"idle_for", idleFor.Round(time.Second).String(),
					"threshold", threshold.String(),
					"tool_in_flight", toolInFlight,
					"runtime_probe_available", runtimeAlive != nil,
					"runtime_probe_known", known,
					"runtime_alive", alive,
				)
				suppressionLogged = true
			}
			continue
		}
		settleGrace := idleWatchdogTerminalSettleGrace(threshold)
		if deadConfirmedAt.IsZero() || deadConfirmedThreshold != threshold {
			suppressionLogged = false
			deadConfirmedAt = time.Now()
			deadConfirmedThreshold = threshold
			deadConfirmedActivitySeq = activityBeforeProbe
			if settleTimer == nil {
				settleTimer = time.NewTimer(settleGrace)
			} else {
				settleTimer.Reset(settleGrace)
			}
			settleTimerC = settleTimer.C
			taskLog.Info("watchdog waiting for provider terminal result after runtime exit",
				"task", shortID(taskID),
				"idle_for", idleFor.Round(time.Second).String(),
				"threshold", threshold.String(),
				"terminal_settle_grace", settleGrace.String(),
				"tool_in_flight", toolInFlight,
			)
			continue
		}
		if time.Since(deadConfirmedAt) < settleGrace {
			continue
		}
		// Re-check progress after the second OS probe. A message can be
		// buffered or drained concurrently while that probe runs; either is
		// terminal progress and invalidates the prior dead observation.
		if len(messages) > 0 || activitySeq.Load() != deadConfirmedActivitySeq {
			resetDeadObservation()
			continue
		}
		taskLog.Warn("watchdog firing: runtime no longer alive after silent progress",
			"task", shortID(taskID),
			"idle_for", idleFor.Round(time.Second).String(),
			"threshold", threshold.String(),
			"tool_in_flight", toolInFlight,
		)
		firedThreshold.Store(int64(threshold))
		fired.Store(true)
		cancel()
		return
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

func mergeSkillsForEnv(primary, secondary []SkillData) []SkillData {
	if len(primary) == 0 && len(secondary) == 0 {
		return nil
	}
	merged := make([]SkillData, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	for _, skill := range primary {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, skill)
	}
	for _, skill := range secondary {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, skill)
	}
	return merged
}

// composeOpenclawIncludeRoots returns the value the daemon should set for
// OPENCLAW_INCLUDE_ROOTS on the child openclaw process so its `$include`
// loader will follow the wrapper's reference out of envRoot into the
// user's active config directory.
//
// addRoot is the directory we must grant (typically dirname of the user's
// active openclaw.json). userValue is whatever the daemon's own
// environment already has under OPENCLAW_INCLUDE_ROOTS — the user's own
// cross-directory layout. We prepend addRoot, dedupe by string equality,
// drop empty path segments, and return ok=false when there's nothing to
// grant (addRoot is empty — fresh install case), so callers can leave the
// env var alone in that case.
//
// Path separator is the OS-native list separator (`:` on Unix, `;` on
// Windows) to match how OpenClaw splits the env var.
func composeOpenclawIncludeRoots(addRoot, userValue string) (string, bool) {
	if addRoot == "" {
		return "", false
	}
	parts := []string{addRoot}
	seen := map[string]struct{}{addRoot: {}}
	for _, p := range strings.Split(userValue, string(os.PathListSeparator)) {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	return strings.Join(parts, string(os.PathListSeparator)), true
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
	// qmd refresh, remote sync). Users can still override these via custom_env.
	env["PI_MEMORY_BACKGROUND_SHUTDOWN"] = "off"
	env["PI_MEMORY_LEARNING"] = "off"
	env["PI_MEMORY_SKILL_DRAFTS"] = "off"
	env["PI_MEMORY_QMD_UPDATE"] = "off"
	env["PI_MEMORY_AUTO_SYNC"] = "0"
	env["PI_MEMORY_AUTO_SYNC_PULL"] = "0"
	env["PI_MEMORY_AUTO_SYNC_PULL_ON_START"] = "0"
	env["PI_MEMORY_AUTO_SYNC_UPLOAD"] = "0"
	env["PI_MEMORY_AUTO_SYNC_UPLOAD_ON_SHUTDOWN"] = "0"
	env["PI_MEMORY_NO_SEARCH"] = "1"
	env["PI_MEMORY_REVIEW_STARTUP_HINT"] = "0"
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
	case "claude":
		args = cfg.ClaudeArgs
	case "codex":
		args = cfg.CodexArgs
	case "codebuddy":
		args = cfg.CodebuddyArgs
	default:
		return nil
	}
	return append([]string(nil), args...)
}
