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
// into start/delivery commands at the WorkspaceDaemon boundary.
type agentProcessManager struct {
	mu sync.Mutex

	now          func() time.Time
	newID        func() string
	onTransition func(agentLifecycleTransition)

	agents   map[string]*managedAgentProcess
	stopping map[string]*managedAgentProcess
}

type agentProcessStartRequest struct {
	AgentID         string
	RuntimeID       string
	ReadinessPolicy string
	DeliveryMode    string
}

type agentProcessStartAcceptance struct {
	AgentID         string
	AgentInstanceID string
	QueueState      string
}

type agentProcessStartResult struct {
	Acceptance agentProcessStartAcceptance
	Replayed   bool
}

const (
	agentRuntimeReadinessFirstEvent  = "first_event"
	agentRuntimeReadinessInitialTurn = "initial_turn"
	agentInitialDeliverySpawnPrompt  = "spawn_prompt"
	agentInitialDeliveryStdin        = "stdin"
)

type agentProcessCallback struct {
	AgentID           string
	AgentInstanceID   string
	ProcessInstanceID string
}

type agentProcessManagerSnapshot struct {
	AgentID           string
	RuntimeID         string
	AgentInstanceID   string
	ProcessInstanceID string
	QueueState        string
	Managed           bool
}

// agentLifecycleTransition is local structured evidence. The diagnostics
// ticket writes it to JSONL; nothing here turns it into Activity, a Message,
// a Task result, billing, or any other business fact.
type agentLifecycleTransition struct {
	StateInstanceID string
	AgentInstanceID string
	Sequence        int64
	Phase           string
	State           string
	Event           string
	Result          string
	At              time.Time
}

type managedAgentProcess struct {
	agentID         string
	runtimeID       string
	agentInstanceID string

	queueState        string
	processInstanceID string
	readinessPolicy   string
	deliveryMode      string
	managed           bool
	sequence          int64
	transitions       map[string]*openLifecycleTransition
	startupDone       chan struct{}
	startupSettled    sync.Once
}

type openLifecycleTransition struct {
	id       string
	phase    string
	state    string
	sequence int64
}

func newAgentProcessManager(now func() time.Time, onTransition func(agentLifecycleTransition)) *agentProcessManager {
	if now == nil {
		now = time.Now
	}
	return &agentProcessManager{
		now:          now,
		newID:        func() string { return uuid.NewString() },
		onTransition: onTransition,
		agents:       make(map[string]*managedAgentProcess),
		stopping:     make(map[string]*managedAgentProcess),
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
	m.mu.Unlock()
}

func (m *agentProcessManager) Start(request agentProcessStartRequest) (agentProcessStartAcceptance, error) {
	result, err := m.startWithDisposition(request)
	return result.Acceptance, err
}

// startWithDisposition tells the WorkspaceDaemon whether it owns provider
// startup. A same-Runtime replay returns the current local acceptance without
// creating another provider-start callback.
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
	if existing := m.agents[request.AgentID]; existing != nil && existing.managed {
		if existing.runtimeID != request.RuntimeID {
			return agentProcessStartResult{}, errors.New("managed Agent must stop its current Runtime before starting another")
		}
		return agentProcessStartResult{Acceptance: m.acceptanceLocked(existing, existing.queueState), Replayed: true}, nil
	}

	agentInstanceID := m.newID()
	managed := &managedAgentProcess{
		agentID: request.AgentID, runtimeID: request.RuntimeID, agentInstanceID: agentInstanceID, managed: true,
		readinessPolicy: request.ReadinessPolicy, deliveryMode: request.DeliveryMode,
		transitions: make(map[string]*openLifecycleTransition), startupDone: make(chan struct{}),
	}
	m.agents[request.AgentID] = managed
	m.beginProcessLocked(managed)
	return agentProcessStartResult{Acceptance: m.acceptanceLocked(managed, managed.queueState)}, nil
}

// RestoreIdle re-creates a managed Agent instance after the provider process
// is gone. This is Computer-local idle auto-restart, not a wire agent:start.
func (m *agentProcessManager) RestoreIdle(agentID, runtimeID, previousAgentInstanceID string, residency *agentResidencyStore) error {
	if m == nil {
		return errors.New("agent process manager is not configured")
	}
	if err := validateAgentProcessStartRequest(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping[agentID] != nil || residency == nil {
		return errManagedAgentStartStopped
	}
	if existing := m.agents[agentID]; existing != nil && existing.managed {
		if existing.runtimeID != runtimeID {
			return errors.New("managed Agent must stop its current Runtime before starting another")
		}
		return nil
	}
	agentInstanceID := m.newID()
	if !residency.replaceIdleInstance(agentID, runtimeID, previousAgentInstanceID, agentInstanceID) {
		return errManagedAgentStartStopped
	}
	managed := &managedAgentProcess{
		agentID: agentID, runtimeID: runtimeID, agentInstanceID: agentInstanceID, managed: true,
		readinessPolicy: agentRuntimeReadinessFirstEvent,
		transitions:     make(map[string]*openLifecycleTransition), startupDone: make(chan struct{}),
	}
	m.agents[agentID] = managed
	m.beginProcessLocked(managed)
	return nil
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

// beginManagedStop removes the exact Agent instance before the caller waits
// for provider shutdown. The retained stopping record lets a reconnect
// resume the same stop while an asynchronous provider start is still settling.
func (m *agentProcessManager) beginManagedStop(callback agentProcessCallback) (agentProcessManagerSnapshot, <-chan struct{}, bool, error) {
	if m == nil {
		return agentProcessManagerSnapshot{}, nil, false, errors.New("agent process manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil {
		if stopping.agentInstanceID != callback.AgentInstanceID {
			return agentProcessManagerSnapshot{}, nil, false, errors.New("stale Agent process callback")
		}
		return snapshotManagedAgentProcess(stopping), stopping.startupDone, true, nil
	}
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed {
		return agentProcessManagerSnapshot{}, nil, false, nil
	}
	if managed.agentInstanceID != callback.AgentInstanceID {
		return agentProcessManagerSnapshot{}, nil, false, errors.New("stale Agent process callback")
	}
	m.stopLocked(managed)
	m.stopping[callback.AgentID] = managed
	return snapshotManagedAgentProcess(managed), managed.startupDone, true, nil
}

func (m *agentProcessManager) completeManagedStop(callback agentProcessCallback) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil && stopping.agentInstanceID == callback.AgentInstanceID {
		delete(m.stopping, callback.AgentID)
	}
	m.mu.Unlock()
}

func (m *agentProcessManager) completeManagedStart(callback agentProcessCallback) {
	m.settleManagedStart(callback, false)
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
	if managed == nil || managed.agentInstanceID != callback.AgentInstanceID {
		managed = m.stopping[callback.AgentID]
	}
	if managed != nil && managed.agentInstanceID == callback.AgentInstanceID {
		managed.startupSettled.Do(func() { close(managed.startupDone) })
		if failed && m.agents[callback.AgentID] == managed {
			m.stopLocked(managed)
		}
	}
	m.mu.Unlock()
}

// failManagedProcess claims an ordinary provider failure only while the exact
// Agent instance is still active in APM. Once beginManagedStop has moved it into the
// stopping ownership, that stop exclusively owns cleanup and inactive publication.
func (m *agentProcessManager) failManagedProcess(callback agentProcessCallback, beforeRelease func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stopping := m.stopping[callback.AgentID]; stopping != nil {
		return false
	}
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.agentInstanceID != callback.AgentInstanceID {
		return false
	}
	if beforeRelease != nil {
		beforeRelease()
	}
	m.stopLocked(managed)
	return true
}

// withActiveAgentInstance runs update while APM still owns the exact active
// Agent instance. Stop takes the same lock before moving it to stopping.
func (m *agentProcessManager) withActiveAgentInstance(callback agentProcessCallback, update func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.agentInstanceID != callback.AgentInstanceID {
		return false
	}
	if update != nil {
		update()
	}
	return true
}

// ownsManagedProcess reports whether an ordinary startup result may still be
// published for this Agent instance. A lifecycle stop moves it into stopping
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
	return managed != nil && managed.managed && managed.agentInstanceID == callback.AgentInstanceID
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
	if managed == nil || !managed.managed || managed.agentInstanceID != callback.AgentInstanceID {
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
	if managed == nil || managed.agentInstanceID != callback.AgentInstanceID {
		managed = m.stopping[callback.AgentID]
	}
	if managed == nil || managed.agentInstanceID != callback.AgentInstanceID || managed.startupDone == nil {
		return nil, false
	}
	return managed.startupDone, true
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

// ProcessExited either makes the current Agent instance recoverable with a new
// concrete process or terminates it. Old instance/process callbacks are fenced.
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
		managed.managed = false
		delete(m.agents, managed.agentID)
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

func (m *agentProcessManager) stoppingSnapshot(agentID string) (agentProcessManagerSnapshot, bool) {
	if m == nil {
		return agentProcessManagerSnapshot{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.stopping[agentID]
	if managed == nil {
		return agentProcessManagerSnapshot{}, false
	}
	return snapshotManagedAgentProcess(managed), true
}

func snapshotManagedAgentProcess(managed *managedAgentProcess) agentProcessManagerSnapshot {
	if managed == nil {
		return agentProcessManagerSnapshot{}
	}
	return agentProcessManagerSnapshot{AgentID: managed.agentID, RuntimeID: managed.runtimeID, AgentInstanceID: managed.agentInstanceID, ProcessInstanceID: managed.processInstanceID, QueueState: managed.queueState, Managed: managed.managed}
}

func (m *agentProcessManager) RunningAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.agents))
	for agentID, managed := range m.agents {
		if managed != nil && managed.managed {
			ids = append(ids, agentID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *agentProcessManager) acceptanceLocked(managed *managedAgentProcess, queueState string) agentProcessStartAcceptance {
	return agentProcessStartAcceptance{AgentID: managed.agentID, AgentInstanceID: managed.agentInstanceID, QueueState: queueState}
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
	m.closeAllLocked(managed, "terminal")
	managed.managed = false
	delete(m.agents, managed.agentID)
}

func (m *agentProcessManager) currentLocked(callback agentProcessCallback, requireProcess bool) (*managedAgentProcess, error) {
	managed := m.agents[callback.AgentID]
	if managed == nil || !managed.managed || managed.agentInstanceID != callback.AgentInstanceID {
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
	m.emitLocked(agentLifecycleTransition{StateInstanceID: open.id, AgentInstanceID: managed.agentInstanceID, Sequence: open.sequence, Phase: phase, State: state, Event: "enter", At: m.now().UTC()})
}

func (m *agentProcessManager) closeLocked(managed *managedAgentProcess, phase, result string) {
	open := managed.transitions[phase]
	if open == nil {
		return
	}
	delete(managed.transitions, phase)
	m.emitLocked(agentLifecycleTransition{StateInstanceID: open.id, AgentInstanceID: managed.agentInstanceID, Sequence: open.sequence, Phase: phase, State: open.state, Event: "close", Result: result, At: m.now().UTC()})
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
	for name, value := range map[string]string{"agent_id": request.AgentID, "runtime_id": request.RuntimeID} {
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
