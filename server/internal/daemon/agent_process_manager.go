package daemon

import (
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

	agents     map[string]*managedAgentProcess
	dispatches map[string]protocol.AgentStartAckPayload
	queued     []string
}

type agentProcessStartRequest struct {
	AgentID         string
	RuntimeID       string
	StartDispatchID string
	ReadinessPolicy string
	DeliveryMode    string
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
		dispatches:   make(map[string]protocol.AgentStartAckPayload),
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
	m.agents = nil
	m.dispatches = nil
	m.queued = nil
	for _, managed := range m.agents {
		m.releaseLocked(managed)
	}
	m.mu.Unlock()
}

func (m *agentProcessManager) Start(request agentProcessStartRequest) (protocol.AgentStartAckPayload, error) {
	if err := validateAgentProcessStartRequest(request); err != nil {
		return protocol.AgentStartAckPayload{}, err
	}
	request = canonicalAgentProcessStartRequest(request)
	if m == nil {
		return protocol.AgentStartAckPayload{}, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if accepted, ok := m.dispatches[request.StartDispatchID]; ok {
		return accepted, nil
	}
	if existing := m.agents[request.AgentID]; existing != nil && existing.managed {
		if existing.runtimeID != request.RuntimeID {
			m.stopLocked(existing)
		} else {
			accepted := m.acceptanceLocked(existing, request.StartDispatchID, protocol.AgentStartQueueRebound)
			m.dispatches[request.StartDispatchID] = accepted
			return accepted, nil
		}
	}

	managed := &managedAgentProcess{
		agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: m.newID(), managed: true,
		readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode,
		transitions: make(map[string]*openLifecycleTransition),
	}
	m.agents[request.AgentID] = managed
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
	} else {
		m.beginProcessLocked(managed)
	}
	accepted := m.acceptanceLocked(managed, request.StartDispatchID, managed.queueState)
	m.dispatches[request.StartDispatchID] = accepted
	return accepted, nil
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

func (m *agentProcessManager) startLocked(request agentProcessStartRequest) (protocol.AgentStartAckPayload, error) {
	if accepted, ok := m.dispatches[request.StartDispatchID]; ok {
		return accepted, nil
	}
	managed := &managedAgentProcess{agentID: request.AgentID, runtimeID: request.RuntimeID, launchID: m.newID(), readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode, managed: true, transitions: make(map[string]*openLifecycleTransition)}
	m.agents[request.AgentID] = managed
	if !m.acquireLocked(managed) {
		managed.queueState = protocol.AgentStartQueueQueued
		m.queued = append(m.queued, managed.agentID)
		m.enterLocked(managed, "process_residency", "queued")
	} else {
		m.beginProcessLocked(managed)
	}
	accepted := m.acceptanceLocked(managed, request.StartDispatchID, managed.queueState)
	m.dispatches[request.StartDispatchID] = accepted
	return accepted, nil
}

func (m *agentProcessManager) acceptanceLocked(managed *managedAgentProcess, dispatchID, queueState string) protocol.AgentStartAckPayload {
	age := int64(0)
	if queueState == protocol.AgentStartQueueQueued {
		age = 1
	}
	return protocol.AgentStartAckPayload{AgentID: managed.agentID, LaunchID: managed.launchID, StartDispatchID: dispatchID, QueueState: queueState, QueueDepth: len(m.queued), QueueAgeMS: age}
}

func (m *agentProcessManager) beginProcessLocked(managed *managedAgentProcess) {
	managed.queueState = protocol.AgentStartQueueStarting
	m.enterLocked(managed, "process_residency", "starting")
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
	grant, admitted := m.admission.Acquire(agentProcessCapacityRequest{WorkspaceID: m.workspaceID, AgentID: managed.agentID, RuntimeID: managed.runtimeID, LaunchID: managed.launchID})
	if !admitted {
		return false
	}
	managed.capacityGrant = grant
	return true
}

func (m *agentProcessManager) releaseLocked(managed *managedAgentProcess) {
	if m.admission != nil && managed.capacityGrant.LaunchID != "" {
		m.admission.Release(managed.capacityGrant)
	}
	managed.capacityGrant = agentProcessCapacityGrant{}
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
	for name, value := range map[string]string{"agent_id": request.AgentID, "runtime_id": request.RuntimeID, "start_dispatch_id": request.StartDispatchID} {
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
