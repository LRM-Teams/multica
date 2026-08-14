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

	agents        map[string]*managedAgentProcess
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
}

type agentProcessManagerSnapshot struct {
	AgentID           string
	RuntimeID         string
	LaunchID          string
	ProcessInstanceID string
	QueueState        string
	Managed           bool
}

// agentLifecycleTransition is local structured evidence. The diagnostics
// ticket writes it to JSONL; nothing here turns it into Activity, a Message,
// a Task result, billing, or any other business fact.
type agentLifecycleTransition struct {
	StateInstanceID string
	LaunchID        string
	Sequence        int64
	Phase           string
	State           string
	Event           string
	Result          string
	At              time.Time
}

type managedAgentProcess struct {
	agentID   string
	runtimeID string
	launchID  string

	queueState        string
	processInstanceID string
	readinessPolicy   string
	deliveryMode      string
	capacityGrant     agentProcessCapacityGrant
	admitted          chan struct{}
	managed           bool
	sequence          int64
	transitions       map[string]*openLifecycleTransition
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
	}
	m.agents = nil
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
				return agentProcessStartResult{Acknowledgement: m.rememberAcceptanceLocked(request, m.acceptanceLocked(existing, existing.queueState))}, nil
			}
			m.rebindLaunchLocked(existing, request.LaunchID)
			return agentProcessStartResult{Acknowledgement: m.rememberAcceptanceLocked(request, m.acceptanceLocked(existing, protocol.AgentStartQueueRebound))}, nil
		}
	}

	managed := &managedAgentProcess{
		agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: request.LaunchID, managed: true,
		readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode,
		admitted: make(chan struct{}), transitions: make(map[string]*openLifecycleTransition),
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
func (m *agentProcessManager) RestoreIdle(agentID, runtimeID, launchID string) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	if err := validateAgentProcessStartRequest(agentProcessStartRequest{
		AgentID: agentID, RuntimeID: runtimeID, LaunchID: launchID, StartDispatchID: launchID,
	}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.agents[agentID]; existing != nil && existing.managed {
		if existing.runtimeID != runtimeID {
			return errors.New("managed Agent must stop its current Runtime before starting another")
		}
		return nil
	}
	managed := &managedAgentProcess{
		agentID: agentID, runtimeID: runtimeID, launchID: launchID, managed: true,
		readinessPolicy: agentRuntimeReadinessFirstEvent,
		admitted:        make(chan struct{}), transitions: make(map[string]*openLifecycleTransition),
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
	m.stopLocked(managed)
	return nil
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
			m.rebindLaunchLocked(managed, request.LaunchID)
			return m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, protocol.AgentStartQueueRebound)), nil
		}
		return m.rememberAcceptanceLocked(request, m.acceptanceLocked(managed, managed.queueState)), nil
	}
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
	m.closeAllLocked(managed, "terminal")
	managed.processInstanceID = ""
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
	return agentProcessManagerSnapshot{AgentID: managed.agentID, RuntimeID: managed.runtimeID, LaunchID: managed.launchID, ProcessInstanceID: managed.processInstanceID, QueueState: managed.queueState, Managed: managed.managed}, true
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
	managed := &managedAgentProcess{agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: request.LaunchID, readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode, admitted: make(chan struct{}), managed: true, transitions: make(map[string]*openLifecycleTransition)}
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
func (m *agentProcessManager) rebindLaunchLocked(managed *managedAgentProcess, launchID string) {
	wasQueued := managed.queueState == protocol.AgentStartQueueQueued
	m.releaseLocked(managed)
	managed.launchID = launchID
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
	m.emitLocked(agentLifecycleTransition{StateInstanceID: open.id, LaunchID: managed.launchID, Sequence: open.sequence, Phase: phase, State: state, Event: "enter", At: m.now().UTC()})
}

func (m *agentProcessManager) closeLocked(managed *managedAgentProcess, phase, result string) {
	open := managed.transitions[phase]
	if open == nil {
		return
	}
	delete(managed.transitions, phase)
	m.emitLocked(agentLifecycleTransition{StateInstanceID: open.id, LaunchID: managed.launchID, Sequence: open.sequence, Phase: phase, State: open.state, Event: "close", Result: result, At: m.now().UTC()})
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
