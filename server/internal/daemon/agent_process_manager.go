package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// agentProcessManager is the daemon-local owner for managed Agent launches.
// It does not own Messages or Tasks: callers turn those service-owned facts
// into start/delivery commands at the Workspace Runner boundary.
type agentProcessManager struct {
	mu sync.Mutex

	workspaceID  string
	admission    agentProcessAdmission
	now          func() time.Time
	newID        func() string
	onTransition func(agentLifecycleTransition)

	agents   map[string]*managedAgentProcess
	stopping map[string]*managedAgentProcess

	// stopEpochs is Raft's per-Agent monotonic cancellation generation. A
	// start captures it at acceptance and rechecks it across async startup.
	stopEpochs    map[string]uint64
	queued        []string
	dispatches    map[string]agentStartDispatchReceipt
	dispatchOrder []string
}

const agentStartDispatchReceiptCacheSize = 1024

type agentStartDispatchReceipt struct {
	runtimeID       string
	acknowledgement protocol.AgentStartAckPayload
}

type agentProcessStartRequest struct {
	AgentID         string
	RuntimeID       string
	LaunchID        string
	StartDispatchID string
	ReadinessPolicy string
	DeliveryMode    string
	RuntimeEpoch    int64
}

type agentProcessStartResult struct {
	Acknowledgement protocol.AgentStartAckPayload
	Replayed        bool
}

const (
	agentRuntimeReadinessFirstEvent  = "first_event"
	agentRuntimeReadinessInitialTurn = "initial_turn"
	agentInitialDeliverySpawnPrompt  = "spawn_prompt"
	agentInitialDeliveryStdin        = "stdin"
)

type agentProcessCallback struct {
	AgentID           string
	LaunchID          string
	ProcessInstanceID string
	ProcessPID        int64
	ExitCode          int64
	Signal            string
	TerminationReason string
	ForceKilled       bool
}

type agentProcessManagerSnapshot struct {
	AgentID           string
	RuntimeID         string
	LaunchID          string
	StartDispatchID   string
	ProcessInstanceID string
	QueueState        string
	Managed           bool
}

// agentLifecycleTransition is local structured evidence. The diagnostics
// ticket writes it to JSONL; nothing here turns it into Activity, a Message,
// a Task result, billing, or any other business fact.
type agentLifecycleTransition struct {
	AgentID           string
	RuntimeID         string
	ProcessInstanceID string
	ProcessPID        int64
	ExitCode          int64
	Signal            string
	TerminationReason string
	ForceKilled       bool
	RuntimeEpoch      int64
	StartDispatchID   string
	StateInstanceID   string
	LaunchID          string
	Sequence          int64
	Phase             string
	State             string
	Event             string
	Result            string
	At                time.Time
}

type managedAgentProcess struct {
	agentID         string
	runtimeID       string
	launchID        string
	startDispatchID string
	// startStopEpoch is the stop generation captured by this exact launch.
	startStopEpoch uint64

	queueState        string
	processInstanceID string
	processPID        int64
	exitCode          int64
	signal            string
	terminationReason string
	forceKilled       bool
	runtimeEpoch      int64
	readinessPolicy   string
	deliveryMode      string
	capacityGrant     agentProcessCapacityGrant
	admitted          chan struct{}
	managed           bool
	sequence          int64
	transitions       map[string]*openLifecycleTransition
	startupDone       chan struct{}
	startupSettled    sync.Once
	startupOwners     map[string]int
	startupOwnerCount int
	startupFailed     bool
}

type openLifecycleTransition struct {
	id       string
	phase    string
	state    string
	sequence int64
}

func newAgentProcessManager(workspaceID string, admission agentProcessAdmission, now func() time.Time, onTransition func(agentLifecycleTransition)) *agentProcessManager {
	if now == nil {
		now = time.Now
	}
	return &agentProcessManager{
		workspaceID:  workspaceID,
		admission:    admission,
		now:          now,
		newID:        func() string { return uuid.NewString() },
		onTransition: onTransition,
		agents:       make(map[string]*managedAgentProcess),
		stopping:     make(map[string]*managedAgentProcess),
		stopEpochs:   make(map[string]uint64),
		dispatches:   make(map[string]agentStartDispatchReceipt),
	}
}

// Close releases only this Runner's in-memory launch state when its Binding is
// removed. It never reaches across Workspace boundaries or synthesizes a stop
// command on a replacement Runner.
func (m *agentProcessManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	for _, managed := range m.agents {
		m.signalAdmissionLocked(managed)
		m.releaseLocked(managed)
		if managed.startupDone != nil {
			managed.startupSettled.Do(func() { close(managed.startupDone) })
		}
	}
	for _, managed := range m.stopping {
		if managed != nil && managed.startupDone != nil {
			managed.startupSettled.Do(func() { close(managed.startupDone) })
		}
	}
	m.agents = nil
	m.stopping = nil
	m.stopEpochs = nil
	m.queued = nil
	m.dispatches = nil
	m.dispatchOrder = nil
	m.mu.Unlock()
}

func (m *agentProcessManager) Start(request agentProcessStartRequest) (protocol.AgentStartAckPayload, error) {
	result, err := m.startWithDisposition(request)
	return result.Acknowledgement, err
}

// startWithDisposition keeps the idempotency receipt inside the process
// manager while telling the Workspace Runner whether it owns provider startup.
// A replay must still return the original wire ACK, but it must not create a
// second asynchronous provider-start callback for the same immutable dispatch.
func (m *agentProcessManager) startWithDisposition(request agentProcessStartRequest) (agentProcessStartResult, error) {
	if err := validateAgentProcessStartRequest(request); err != nil {
		return agentProcessStartResult{}, err
	}
	request = canonicalAgentProcessStartRequest(request)
	if m == nil {
		return agentProcessStartResult{}, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stopping := m.stopping[request.AgentID]; stopping != nil {
		return agentProcessStartResult{}, errors.New("managed Agent stop has not settled")
	}
	if receipt, replayed := m.dispatches[request.StartDispatchID]; replayed {
		if receipt.acknowledgement.AgentID != request.AgentID || receipt.acknowledgement.LaunchID != request.LaunchID || receipt.runtimeID != request.RuntimeID {
			return agentProcessStartResult{}, errors.New("start dispatch identity conflicts with its accepted receipt")
		}
		return agentProcessStartResult{Acknowledgement: receipt.acknowledgement, Replayed: true}, nil
	}
	if existing := m.agents[request.AgentID]; existing != nil && existing.managed {
		if existing.runtimeID != request.RuntimeID {
			return agentProcessStartResult{}, errors.New("managed Agent must stop its current Runtime before starting another")
		} else {
			if existing.launchID == request.LaunchID {
				if existing.startDispatchID != request.StartDispatchID {
					return agentProcessStartResult{}, errors.New("managed Agent launch is already owned by another start dispatch")
				}
				return agentProcessStartResult{Acknowledgement: m.rememberAcceptanceLocked(request, m.acceptanceLocked(existing, existing.queueState)), Replayed: true}, nil
			}
			m.rebindLaunchLocked(existing, request.LaunchID, request.StartDispatchID)
			return agentProcessStartResult{Acknowledgement: m.rememberAcceptanceLocked(request, m.acceptanceLocked(existing, protocol.AgentStartQueueRebound))}, nil
		}
	}

	managed := &managedAgentProcess{
		agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: request.LaunchID, startDispatchID: request.StartDispatchID, managed: true,
		runtimeEpoch:    request.RuntimeEpoch,
		startStopEpoch:  m.stopEpochs[request.AgentID],
		readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode,
		admitted: make(chan struct{}), transitions: make(map[string]*openLifecycleTransition), startupDone: make(chan struct{}),
		startupOwners: map[string]int{request.LaunchID: 1}, startupOwnerCount: 1,
	}
	m.agents[request.AgentID] = managed
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
	} else {
		m.beginProcessLocked(managed)
	}
	return agentProcessStartResult{Acknowledgement: m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, managed.queueState))}, nil
}

// RestoreIdle re-creates a managed launch after the process is gone without a
// new server startDispatchId. This is Computer-local idle auto-restart, not a
// wire agent:start command.
func (m *agentProcessManager) RestoreIdle(agentID, runtimeID, launchID, startDispatchID string, startStopEpoch uint64) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	if err := validateAgentProcessStartRequest(agentProcessStartRequest{
		AgentID: agentID, RuntimeID: runtimeID, LaunchID: launchID, StartDispatchID: startDispatchID,
	}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopEpochs[agentID] != startStopEpoch || m.stopping[agentID] != nil {
		return errManagedAgentStartStopped
	}
	if existing := m.agents[agentID]; existing != nil && existing.managed {
		if existing.runtimeID != runtimeID {
			return errors.New("managed Agent must stop its current Runtime before starting another")
		}
		return nil
	}
	managed := &managedAgentProcess{
		agentID: agentID, runtimeID: runtimeID, launchID: launchID, startDispatchID: startDispatchID, managed: true,
		runtimeEpoch:    0,
		startStopEpoch:  startStopEpoch,
		readinessPolicy: agentRuntimeReadinessFirstEvent,
		admitted:        make(chan struct{}), transitions: make(map[string]*openLifecycleTransition), startupDone: make(chan struct{}),
		startupOwners: map[string]int{launchID: 1}, startupOwnerCount: 1,
	}
	m.agents[agentID] = managed
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
		return nil
	}
	m.beginProcessLocked(managed)
	return nil
}

func (m *agentProcessManager) SetRuntimeEpoch(agentID string, epoch int64) {
	if m == nil || strings.TrimSpace(agentID) == "" {
		return
	}
	m.mu.Lock()
	if managed := m.agents[agentID]; managed != nil {
		managed.runtimeEpoch = epoch
	}
	m.mu.Unlock()
}

// WaitForAdmission blocks until the exact managed launch is selected by the
// Computer-local process policy. A queued start is already accepted by APM and
// may buffer Messages, but provider startup and active status wait for this
// local scheduling fence. The policy is not part of the wire ACK contract.
func (m *agentProcessManager) WaitForAdmission(ctx context.Context, callback agentProcessCallback) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	managed, err := m.currentLocked(callback, false)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	admitted := managed.admitted
	m.mu.Unlock()
	select {
	case <-admitted:
		m.mu.Lock()
		_, err := m.currentLocked(callback, false)
		m.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *agentProcessManager) Stop(callback agentProcessCallback) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, false)
	if err != nil {
		return err
	}
	m.recordStopLocked(callback.AgentID)
	m.stopLocked(managed)
	return nil
}

// beginManagedStop removes the exact launch from admission before the caller
// waits for provider shutdown. The retained stopping record lets a reconnect
// resume the same stop while an asynchronous provider start is still settling.
func (m *agentProcessManager) beginManagedStop(callback agentProcessCallback) (agentProcessManagerSnapshot, <-chan struct{}, bool, error) {
	if m == nil {
		return agentProcessManagerSnapshot{}, nil, false, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil {
		if stopping.launchID != callback.LaunchID {
			return agentProcessManagerSnapshot{}, nil, false, errors.New("stale Agent process callback")
		}
		m.recordStopLocked(callback.AgentID)
		return snapshotManagedAgentProcess(stopping), stopping.startupDone, true, nil
	}
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed {
		return agentProcessManagerSnapshot{}, nil, false, nil
	}
	if managed.launchID != callback.LaunchID {
		return agentProcessManagerSnapshot{}, nil, false, errors.New("stale Agent process callback")
	}
	m.recordStopLocked(callback.AgentID)
	m.stopLocked(managed)
	m.stopping[callback.AgentID] = managed
	return snapshotManagedAgentProcess(managed), managed.startupDone, true, nil
}

func (m *agentProcessManager) recordStop(agentID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.recordStopLocked(agentID)
	m.mu.Unlock()
}

func (m *agentProcessManager) completeManagedStop(callback agentProcessCallback) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil && stopping.launchID == callback.LaunchID {
		delete(m.stopping, callback.AgentID)
	}
	m.mu.Unlock()
}

func (m *agentProcessManager) completeManagedStart(callback agentProcessCallback) {
	m.settleManagedStart(callback, false)
}

func (m *agentProcessManager) startStopEpoch(callback agentProcessCallback) (uint64, error) {
	if m == nil {
		return 0, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[callback.AgentID]
	if managed == nil {
		managed = m.stopping[callback.AgentID]
	}
	if managed == nil || managed.launchID != callback.LaunchID {
		return 0, errors.New("managed Agent start was superseded")
	}
	return managed.startStopEpoch, nil
}

func (m *agentProcessManager) stopEpochChanged(agentID string, captured uint64) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopEpochs[agentID] != captured
}

func (m *agentProcessManager) recordStopLocked(agentID string) uint64 {
	next := m.stopEpochs[agentID] + 1
	m.stopEpochs[agentID] = next
	return next
}

func (m *agentProcessManager) completeFailedManagedStart(callback agentProcessCallback) {
	m.settleManagedStart(callback, true)
}

func (m *agentProcessManager) settleManagedStart(callback agentProcessCallback, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	managed := m.agents[callback.AgentID]
	if managed == nil || managed.startupOwners[callback.LaunchID] == 0 {
		managed = m.stopping[callback.AgentID]
	}
	if managed != nil && failed && managed.launchID == callback.LaunchID {
		managed.startupFailed = true
	}
	if managed != nil && managed.startupOwners[callback.LaunchID] > 0 {
		managed.startupOwners[callback.LaunchID]--
		managed.startupOwnerCount--
		if managed.startupOwners[callback.LaunchID] == 0 {
			delete(managed.startupOwners, callback.LaunchID)
		}
		if managed.startupOwnerCount == 0 && managed.startupDone != nil {
			managed.startupSettled.Do(func() { close(managed.startupDone) })
			if managed.startupFailed && m.agents[callback.AgentID] == managed {
				m.stopLocked(managed)
			}
		}
	}
	m.mu.Unlock()
}

// failManagedProcess claims an ordinary provider failure only while the exact
// launch is still active in APM. Once beginManagedStop has moved it into the
// stopping epoch, that stop exclusively owns cleanup and inactive publication.
func (m *agentProcessManager) failManagedProcess(callback agentProcessCallback) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil {
		return false
	}
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.launchID != callback.LaunchID {
		return false
	}
	m.stopLocked(managed)
	return true
}

// ownsManagedProcess reports whether an ordinary startup result may still be
// published for this launch. A lifecycle stop moves the launch into stopping
// before waiting for startupDone, so a late failure cannot overtake Stopped.
func (m *agentProcessManager) ownsManagedProcess(callback agentProcessCallback) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping[callback.AgentID] != nil {
		return false
	}
	managed := m.agents[callback.AgentID]
	return managed != nil && managed.managed && managed.launchID == callback.LaunchID
}

// publishManagedStart linearizes the final Active publication with Stop. Raft
// gets this ordering from its single-threaded event loop; the Go Runner must
// hold APM ownership until every externally visible start fact is published.
func (m *agentProcessManager) publishManagedStart(callback agentProcessCallback, publish func() error) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.launchID != callback.LaunchID ||
		m.stopEpochs[callback.AgentID] != managed.startStopEpoch {
		return errManagedAgentStartStopped
	}
	return publish()
}

func (m *agentProcessManager) managedStartupDone(callback agentProcessCallback) (<-chan struct{}, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[callback.AgentID]
	if managed == nil || managed.launchID != callback.LaunchID {
		managed = m.stopping[callback.AgentID]
	}
	if managed == nil || managed.launchID != callback.LaunchID || managed.startupDone == nil {
		return nil, false
	}
	return managed.startupDone, true
}

func (m *agentProcessManager) Restart(callback agentProcessCallback, request agentProcessStartRequest) (protocol.AgentStartAckPayload, error) {
	if err := validateAgentProcessStartRequest(request); err != nil {
		return protocol.AgentStartAckPayload{}, err
	}
	if callback.AgentID != request.AgentID {
		return protocol.AgentStartAckPayload{}, errors.New("restart Agent does not match start request")
	}
	request = canonicalAgentProcessStartRequest(request)
	if m == nil {
		return protocol.AgentStartAckPayload{}, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, false)
	if err != nil {
		return protocol.AgentStartAckPayload{}, err
	}
	// Raft hasStarting: a start already in flight is rebound, not torn down.
	// Killing it here makes the in-flight spawn callback stale and leaves
	// queue_state=starting with no process — the stuck-Starting hole.
	if managed.queueState == protocol.AgentStartQueueStarting && managed.processInstanceID == "" {
		if request.LaunchID != managed.launchID {
			m.rebindLaunchLocked(managed, request.LaunchID, request.StartDispatchID)
			return m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, protocol.AgentStartQueueRebound)), nil
		}
		return m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, managed.queueState)), nil
	}
	m.recordStopLocked(callback.AgentID)
	m.stopLocked(managed)
	return m.startLocked(request)
}

func (m *agentProcessManager) ProcessSpawned(callback agentProcessCallback) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, false)
	if err != nil {
		return err
	}
	if m.admission == nil || !m.admission.Active(managed.capacityGrant) {
		return errors.New("managed launch capacity grant is no longer active")
	}
	if managed.queueState != protocol.AgentStartQueueStarting || callback.ProcessInstanceID == "" {
		return errors.New("process spawn is not valid for the current launch")
	}
	managed.processInstanceID = callback.ProcessInstanceID
	managed.processPID = callback.ProcessPID
	m.closeLocked(managed, "process_residency", "advanced")
	m.enterLocked(managed, "runtime_readiness", "waiting")
	return nil
}

func (m *agentProcessManager) RuntimeReady(callback agentProcessCallback) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, true)
	if err != nil {
		return err
	}
	if _, ok := managed.transitions["runtime_readiness"]; !ok {
		return errors.New("runtime readiness is not pending")
	}
	m.readyLocked(managed)
	return nil
}

// ObserveRuntimeEvidence contains the provider-adapter seam. Session
// initialization, diagnostics, recovery notices, and internal progress never
// establish readiness. Adapters choose only the evidence policy declared by
// the runtime: the first usable event or initial-turn progress.
func (m *agentProcessManager) ObserveRuntimeEvidence(callback agentProcessCallback, evidence string) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, true)
	if err != nil {
		return err
	}
	if _, ok := managed.transitions["runtime_readiness"]; !ok {
		return errors.New("runtime readiness is not pending")
	}
	accepted := (managed.readinessPolicy == agentRuntimeReadinessFirstEvent && evidence == "provider_event") ||
		(managed.readinessPolicy == agentRuntimeReadinessInitialTurn && evidence == "initial_turn_progress")
	if !accepted {
		return fmt.Errorf("runtime evidence %q does not establish %s readiness", evidence, managed.readinessPolicy)
	}
	m.readyLocked(managed)
	return nil
}

func (m *agentProcessManager) InitialActivationDelivered(callback agentProcessCallback) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, true)
	if err != nil {
		return err
	}
	if _, ok := managed.transitions["activation_delivery"]; !ok {
		return errors.New("initial activation delivery is not pending")
	}
	m.closeLocked(managed, "activation_delivery", "advanced")
	managed.queueState = protocol.AgentStartQueueRunning
	return nil
}

// Timeout closes exactly the named pending phase. It intentionally does not
// manufacture a business terminal outcome; the caller decides whether the
// launch remains managed for a later recovery attempt.
func (m *agentProcessManager) Timeout(callback agentProcessCallback, phase string) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, phase != "process_residency")
	if err != nil {
		return err
	}
	if _, ok := managed.transitions[phase]; !ok {
		return fmt.Errorf("%s timeout is not pending", phase)
	}
	m.closeLocked(managed, phase, "timeout")
	return nil
}

// ProcessExited either makes the currently managed launch recoverable (a new
// concrete process, same launch identity) or terminates it. Callbacks from an
// old launch/process are rejected before they can change this state.
func (m *agentProcessManager) ProcessExited(callback agentProcessCallback, recover bool) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, err := m.currentLocked(callback, true)
	if err != nil {
		return err
	}
	managed.exitCode = callback.ExitCode
	managed.signal = callback.Signal
	managed.terminationReason = callback.TerminationReason
	managed.forceKilled = callback.ForceKilled
	m.closeAllLocked(managed, "terminal")
	managed.processInstanceID = ""
	managed.processPID = 0
	managed.exitCode = 0
	managed.signal = ""
	managed.terminationReason = ""
	managed.forceKilled = false
	if !recover {
		m.releaseLocked(managed)
		managed.managed = false
		managed.queueState = protocol.AgentStartQueueQueued
		delete(m.agents, managed.agentID)
		m.promoteLocked()
		return nil
	}
	m.beginProcessLocked(managed)
	return nil
}

func (m *agentProcessManager) Snapshot(agentID string) (agentProcessManagerSnapshot, bool) {
	if m == nil {
		return agentProcessManagerSnapshot{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[agentID]
	if managed == nil {
		return agentProcessManagerSnapshot{}, false
	}
	return snapshotManagedAgentProcess(managed), true
}

func snapshotManagedAgentProcess(managed *managedAgentProcess) agentProcessManagerSnapshot {
	if managed == nil {
		return agentProcessManagerSnapshot{}
	}
	return agentProcessManagerSnapshot{AgentID: managed.agentID, RuntimeID: managed.runtimeID, LaunchID: managed.launchID, StartDispatchID: managed.startDispatchID, ProcessInstanceID: managed.processInstanceID, QueueState: managed.queueState, Managed: managed.managed}
}

func (m *agentProcessManager) RunningAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.agents))
	for agentID, managed := range m.agents {
		if managed != nil && managed.managed && managed.queueState != protocol.AgentStartQueueQueued {
			ids = append(ids, agentID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *agentProcessManager) startLocked(request agentProcessStartRequest) (protocol.AgentStartAckPayload, error) {
	managed := &managedAgentProcess{
		agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: request.LaunchID, startDispatchID: request.StartDispatchID,
		runtimeEpoch:    request.RuntimeEpoch,
		startStopEpoch:  m.stopEpochs[request.AgentID],
		readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode, admitted: make(chan struct{}), managed: true,
		transitions: make(map[string]*openLifecycleTransition), startupDone: make(chan struct{}),
		startupOwners: map[string]int{request.LaunchID: 1}, startupOwnerCount: 1,
	}
	m.agents[request.AgentID] = managed
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
	} else {
		m.beginProcessLocked(managed)
	}
	return m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, managed.queueState)), nil
}

func (m *agentProcessManager) rememberAcceptanceLocked(request agentProcessStartRequest, acknowledgement protocol.AgentStartAckPayload) protocol.AgentStartAckPayload {
	acknowledgement.StartDispatchID = request.StartDispatchID
	if _, exists := m.dispatches[request.StartDispatchID]; !exists {
		m.dispatchOrder = append(m.dispatchOrder, request.StartDispatchID)
	}
	m.dispatches[request.StartDispatchID] = agentStartDispatchReceipt{runtimeID: request.RuntimeID, acknowledgement: acknowledgement}
	for len(m.dispatchOrder) > agentStartDispatchReceiptCacheSize {
		oldest := m.dispatchOrder[0]
		m.dispatchOrder = m.dispatchOrder[1:]
		delete(m.dispatches, oldest)
	}
	return acknowledgement
}

func (m *agentProcessManager) acceptanceLocked(managed *managedAgentProcess, queueState string) protocol.AgentStartAckPayload {
	age := int64(0)
	if queueState == protocol.AgentStartQueueQueued {
		age = 1
	}
	return protocol.AgentStartAckPayload{AgentID: managed.agentID, LaunchID: managed.launchID, QueueState: queueState, QueueDepth: len(m.queued), QueueAgeMS: age}
}

// rebindLaunchLocked adopts the server's current launch epoch without
// restarting a healthy same-Runtime residency. The admission grant follows the
// epoch so callbacks from the previous launch are fenced immediately.
func (m *agentProcessManager) rebindLaunchLocked(managed *managedAgentProcess, launchID, startDispatchID string) {
	wasQueued := managed.queueState == protocol.AgentStartQueueQueued
	m.releaseLocked(managed)
	if managed.startupOwnerCount == 0 {
		managed.startupDone = make(chan struct{})
		managed.startupSettled = sync.Once{}
		managed.startupOwners = make(map[string]int)
	}
	managed.startupFailed = false
	managed.startupOwners[launchID]++
	managed.startupOwnerCount++
	managed.launchID = launchID
	managed.startDispatchID = startDispatchID
	managed.startStopEpoch = m.stopEpochs[managed.agentID]
	managed.admitted = make(chan struct{})
	if wasQueued {
		m.queued = removeQueuedAgent(m.queued, managed.agentID)
		m.closeAllLocked(managed, "superseded")
	}
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
		return
	}
	if wasQueued {
		m.beginProcessLocked(managed)
		return
	}
	m.signalAdmissionLocked(managed)
}

func (m *agentProcessManager) beginProcessLocked(managed *managedAgentProcess) {
	managed.queueState = protocol.AgentStartQueueStarting
	m.signalAdmissionLocked(managed)
	m.enterLocked(managed, "process_residency", "starting")
}

func (m *agentProcessManager) signalAdmissionLocked(managed *managedAgentProcess) {
	if managed == nil || managed.admitted == nil {
		return
	}
	select {
	case <-managed.admitted:
	default:
		close(managed.admitted)
	}
}

func (m *agentProcessManager) readyLocked(managed *managedAgentProcess) {
	m.closeLocked(managed, "runtime_readiness", "advanced")
	if managed.deliveryMode == "" {
		managed.queueState = protocol.AgentStartQueueRunning
		return
	}
	m.enterLocked(managed, "activation_delivery", managed.deliveryMode)
}

func (m *agentProcessManager) stopLocked(managed *managedAgentProcess) {
	m.signalAdmissionLocked(managed)
	m.releaseLocked(managed)
	m.queued = removeQueuedAgent(m.queued, managed.agentID)
	m.closeAllLocked(managed, "terminal")
	managed.managed = false
	delete(m.agents, managed.agentID)
	m.promoteLocked()
}

func (m *agentProcessManager) promoteLocked() {
	for len(m.queued) > 0 {
		agentID := m.queued[0]
		m.queued = m.queued[1:]
		managed := m.agents[agentID]
		if managed == nil || !managed.managed || managed.queueState != protocol.AgentStartQueueQueued {
			continue
		}
		if !m.acquireLocked(managed) {
			m.queued = append([]string{agentID}, m.queued...)
			return
		}
		m.closeLocked(managed, "process_residency", "advanced")
		m.beginProcessLocked(managed)
	}
}

func (m *agentProcessManager) acquireLocked(managed *managedAgentProcess) bool {
	if m.admission == nil {
		return false
	}
	grant, admitted := m.admission.Acquire(agentProcessCapacityRequest{
		WorkspaceID: m.workspaceID,
		AgentID:     managed.agentID,
		RuntimeID:   managed.runtimeID,
		LaunchID:    managed.launchID,
		Waiter: func(grant agentProcessCapacityGrant) {
			m.onCapacityGranted(grant)
		},
	})
	managed.capacityGrant = grant
	if !admitted {
		return false
	}
	return true
}

func (m *agentProcessManager) releaseLocked(managed *managedAgentProcess) {
	if m.admission != nil && managed.capacityGrant.LaunchID != "" {
		m.admission.Cancel(managed.capacityGrant)
	}
	managed.capacityGrant = agentProcessCapacityGrant{}
}

// onCapacityGranted is called by the pool after it has atomically recorded a
// global grant. The local launch and its opaque queued token must still match;
// otherwise a stop, detach, or replacement won the race and the callback is a
// harmless no-op.
func (m *agentProcessManager) onCapacityGranted(grant agentProcessCapacityGrant) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[grant.AgentID]
	if managed == nil || !managed.managed || managed.launchID != grant.LaunchID ||
		managed.queueState != protocol.AgentStartQueueQueued || managed.capacityGrant != grant ||
		m.admission == nil || !m.admission.Active(grant) {
		return
	}
	m.queued = removeQueuedAgent(m.queued, managed.agentID)
	m.closeLocked(managed, "process_residency", "advanced")
	m.beginProcessLocked(managed)
}

func (m *agentProcessManager) currentLocked(callback agentProcessCallback, requireProcess bool) (*managedAgentProcess, error) {
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.launchID != callback.LaunchID {
		return nil, errors.New("stale Agent process callback")
	}
	if requireProcess && (callback.ProcessInstanceID == "" || managed.processInstanceID != callback.ProcessInstanceID) {
		return nil, errors.New("stale Agent process instance callback")
	}
	return managed, nil
}

func (m *agentProcessManager) enterLocked(managed *managedAgentProcess, phase, state string) {
	if _, exists := managed.transitions[phase]; exists {
		return
	}
	managed.sequence++
	open := &openLifecycleTransition{id: m.newID(), phase: phase, state: state, sequence: managed.sequence}
	managed.transitions[phase] = open
	m.emitLocked(agentLifecycleTransition{AgentID: managed.agentID, RuntimeID: managed.runtimeID, ProcessInstanceID: managed.processInstanceID, ProcessPID: managed.processPID, ExitCode: managed.exitCode, Signal: managed.signal, TerminationReason: managed.terminationReason, ForceKilled: managed.forceKilled, RuntimeEpoch: managed.runtimeEpoch, StartDispatchID: managed.startDispatchID, StateInstanceID: open.id, LaunchID: managed.launchID, Sequence: open.sequence, Phase: phase, State: state, Event: "enter", At: m.now().UTC()})
}

func (m *agentProcessManager) closeLocked(managed *managedAgentProcess, phase, result string) {
	open := managed.transitions[phase]
	if open == nil {
		return
	}
	delete(managed.transitions, phase)
	m.emitLocked(agentLifecycleTransition{AgentID: managed.agentID, RuntimeID: managed.runtimeID, ProcessInstanceID: managed.processInstanceID, ProcessPID: managed.processPID, ExitCode: managed.exitCode, Signal: managed.signal, TerminationReason: managed.terminationReason, ForceKilled: managed.forceKilled, RuntimeEpoch: managed.runtimeEpoch, StartDispatchID: managed.startDispatchID, StateInstanceID: open.id, LaunchID: managed.launchID, Sequence: open.sequence, Phase: phase, State: open.state, Event: "close", Result: result, At: m.now().UTC()})
}

func (m *agentProcessManager) closeAllLocked(managed *managedAgentProcess, result string) {
	phases := make([]string, 0, len(managed.transitions))
	for phase := range managed.transitions {
		phases = append(phases, phase)
	}
	sort.Strings(phases)
	for _, phase := range phases {
		m.closeLocked(managed, phase, result)
	}
}

func (m *agentProcessManager) emitLocked(transition agentLifecycleTransition) {
	if m.onTransition != nil {
		m.onTransition(transition)
	}
}

func validateAgentProcessStartRequest(request agentProcessStartRequest) error {
	for name, value := range map[string]string{"agent_id": request.AgentID, "runtime_id": request.RuntimeID, "launch_id": request.LaunchID, "start_dispatch_id": request.StartDispatchID} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	request = canonicalAgentProcessStartRequest(request)
	if request.ReadinessPolicy != agentRuntimeReadinessFirstEvent && request.ReadinessPolicy != agentRuntimeReadinessInitialTurn {
		return fmt.Errorf("unsupported readiness policy %q", request.ReadinessPolicy)
	}
	if request.DeliveryMode != "" && request.DeliveryMode != agentInitialDeliverySpawnPrompt && request.DeliveryMode != agentInitialDeliveryStdin {
		return fmt.Errorf("unsupported initial delivery mode %q", request.DeliveryMode)
	}
	return nil
}

func canonicalAgentProcessStartRequest(request agentProcessStartRequest) agentProcessStartRequest {
	if request.ReadinessPolicy == "" {
		request.ReadinessPolicy = agentRuntimeReadinessFirstEvent
	}
	return request
}

func removeQueuedAgent(queue []string, agentID string) []string {
	for index, candidate := range queue {
		if candidate == agentID {
			return append(queue[:index], queue[index+1:]...)
		}
	}
	return queue
}
