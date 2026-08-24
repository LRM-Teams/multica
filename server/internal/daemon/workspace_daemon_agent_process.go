package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errManagedAgentStartStopped = errors.New("managed start suppressed by stop request")

func (runner *WorkspaceDaemon) acceptManagedAgentStart(start protocol.AgentStartPayload, failConnection func(error)) (protocol.AgentStartAckPayload, agentProcessCallback, bool, func(), <-chan struct{}, <-chan bool, error) {
	ack, callback, replayed, err := runner.registerManagedAgentStartOnce(start)
	if err != nil {
		return ack, agentProcessCallback{}, false, nil, nil, nil, err
	}
	if replayed {
		publicationSettled := make(chan bool, 1)
		startupDone, found := runner.processes.managedStartupDone(callback)
		if !found {
			publicationSettled <- false
			close(publicationSettled)
		} else {
			go func() {
				<-startupDone
				current, ok := runner.processes.Snapshot(start.AgentID)
				publicationSettled <- ok && current.AgentInstanceID == callback.AgentInstanceID && current.QueueState == protocol.AgentStartQueueRunning
				close(publicationSettled)
			}()
		}
		return ack, callback, true, nil, nil, publicationSettled, nil
	}
	publicationReady := make(chan struct{})
	startupSettled := make(chan struct{})
	publicationSettled := make(chan bool, 1)
	go runner.startAgentNow(runner.life, start, callback, ack, publicationReady, startupSettled, publicationSettled, failConnection)
	var once sync.Once
	return ack, callback, false, func() { once.Do(func() { close(publicationReady) }) }, startupSettled, publicationSettled, nil
}

// startAgentNow is created as soon as APM accepts a new dispatch, before its
// wire acknowledgement is attempted. This is Raft 1.0.16's startAgentNow
// boundary: a broken socket may lose the ACK, but it cannot leave a launch with
// no goroutine capable of settling startupDone.
func (runner *WorkspaceDaemon) startAgentNow(startCtx context.Context, start protocol.AgentStartPayload, callback agentProcessCallback, ack protocol.AgentStartAckPayload, publicationReady <-chan struct{}, startupSettled chan<- struct{}, publicationSettled chan<- bool, failConnection func(error)) {
	failed := false
	published := false
	defer func() {
		if failed {
			runner.processes.completeFailedManagedStart(callback)
		} else {
			runner.processes.completeManagedStart(callback)
		}
		if publicationSettled != nil {
			publicationSettled <- published
			close(publicationSettled)
		}
	}()
	if startCtx == nil {
		startCtx = runner.life
		if startCtx == nil {
			startCtx = context.Background()
		}
	}
	outcome, err := runner.completeManagedAgentStart(startCtx, start, callback, ack)
	if startupSettled != nil {
		close(startupSettled)
	}
	if publicationReady != nil {
		<-publicationReady
	}
	if err != nil {
		failed = true
		if runner.logger != nil && !errors.Is(err, errManagedAgentStartStopped) {
			runner.logger.Warn("WorkspaceDaemon provider start failed", runner.managedStartLogAttrs(start, ack.QueueState, "provider_start_failed", "failed", err)...)
		}
		runner.publishManagedAgentStartFailure(start, callback, outcome)
		return
	}
	err = runner.processes.publishManagedStart(callback, func() error {
		if err := runner.establishManagedAgentStart(start, callback.AgentInstanceID, outcome); err != nil {
			return err
		}
		if err := runner.sendOnCurrentConnection(protocol.EventAgentStatus, outcome.status); err != nil {
			return err
		}
		if outcome.session.ProviderSessionID != "" {
			if err := runner.sendOnCurrentConnection(protocol.EventAgentSession, outcome.session); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errManagedAgentStartStopped) {
		failed = true
		return
	}
	if err != nil {
		if failConnection != nil {
			failConnection(err)
		}
		return
	}
	runner.broadcastActivity(start.AgentID, start.RuntimeID, "starting")
	runner.flushManagedAgentStartMessages(startCtx, start, ack)
	if runner.notifyAppInbox != nil {
		_ = runner.notifyAppInbox(startCtx, start.AgentID, start.RuntimeID)
	}
	// A resident provider has no initial turn to produce an idle event. Mark
	// it ready only after buffered input has crossed the runtime boundary so
	// synthetic Online cannot overwrite the first real Message activity.
	runner.observeResidentRuntimeReady(start.AgentID, start.RuntimeID)
	published = true
}

func (runner *WorkspaceDaemon) replayManagedAgentStartPublication(start protocol.AgentStartPayload, callback agentProcessCallback, failConnection func(error)) bool {
	current, ok := runner.processes.Snapshot(start.AgentID)
	if !ok || current.AgentInstanceID != callback.AgentInstanceID || current.QueueState != protocol.AgentStartQueueRunning {
		return false
	}
	status := protocol.AgentStatusPayload{AgentID: start.AgentID, Status: protocol.AgentStatusActive}
	if err := runner.sendOnCurrentConnection(protocol.EventAgentStatus, status); err != nil {
		if failConnection != nil {
			failConnection(err)
		}
		return false
	}
	if runner.currentProviderSession != nil {
		if providerSessionID, err := runner.currentProviderSession(start.AgentID, start.RuntimeID); err == nil && providerSessionID != "" {
			session := protocol.AgentSessionPayload{AgentID: start.AgentID, ProviderSessionID: providerSessionID}
			if err := runner.sendOnCurrentConnection(protocol.EventAgentSession, session); err != nil {
				if failConnection != nil {
					failConnection(err)
				}
				return false
			}
		}
	}
	// Raft 1.0.16 reconnects replay status/session/current Snapshot only.
	// Starting… is a spawn fact; repeating it on socket replay paints a
	// new timeline pair after every Binding child reconnect.
	return true
}

type managedAgentStartOutcome struct {
	status         protocol.AgentStatusPayload
	session        protocol.AgentSessionPayload
	startStopEpoch uint64
	failureStage   managedRuntimeFailureStage
	failureReason  string
	failureAt      time.Time
}

// registerManagedAgentStart runs on the socket reader before a later stop or
// replacement command can overtake this launch. Provider startup stays async.
func (runner *WorkspaceDaemon) registerManagedAgentStart(payload protocol.AgentStartPayload) (protocol.AgentStartAckPayload, error) {
	ack, _, _, err := runner.registerManagedAgentStartOnce(payload)
	return ack, err
}

// restartAgentForRuntimeChange turns a Start intent for a different Runtime
// into the existing local Stop transition. The caller performs the ordinary
// Start after this returns, so there is one Stop -> inactive -> Start path.
func (runner *WorkspaceDaemon) restartAgentForRuntimeChange(ctx context.Context, start protocol.AgentStartPayload, pause func(), writeFrame func(string, any) error) error {
	if runner == nil || runner.processes == nil {
		return errors.New("WorkspaceDaemon process manager is unavailable")
	}
	current, ok := runner.processes.Snapshot(start.AgentID)
	if !ok || current.RuntimeID == start.RuntimeID {
		return nil
	}
	if err := runner.stopManagedAgent(ctx, protocol.AgentStopPayload{
		AgentID: start.AgentID}, pause, writeFrame); err != nil {
		return err
	}
	if runner.runtimes.hasResidentBackend(start.AgentID, current.RuntimeID) || runner.runtimes.hasRunningTurn(start.AgentID, current.RuntimeID) {
		return errors.New("old managed Agent provider termination is unconfirmed")
	}
	return nil
}

func (runner *WorkspaceDaemon) registerManagedAgentStartOnce(payload protocol.AgentStartPayload) (protocol.AgentStartAckPayload, agentProcessCallback, bool, error) {
	if runner == nil || runner.processes == nil || runner.activity == nil {
		return protocol.AgentStartAckPayload{}, agentProcessCallback{}, false, errors.New("WorkspaceDaemon launch dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.AgentStartAckPayload{}, agentProcessCallback{}, false, fmt.Errorf("validate managed start: %w", err)
	}
	if !runner.hasRuntime(payload.RuntimeID) {
		return protocol.AgentStartAckPayload{}, agentProcessCallback{}, false, errors.New("managed start Runtime is outside WorkspaceDaemon scope")
	}
	result, err := runner.processes.startWithDisposition(agentProcessStartRequest{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
	if err != nil {
		return protocol.AgentStartAckPayload{}, agentProcessCallback{}, false, fmt.Errorf("start managed Agent: %w", err)
	}
	acceptance := result.Acceptance
	callback := agentProcessCallback{AgentID: payload.AgentID, AgentInstanceID: acceptance.AgentInstanceID}
	ack := protocol.AgentStartAckPayload{
		AgentID: payload.AgentID, QueueState: acceptance.QueueState,
	}
	if result.Replayed {
		return ack, callback, true, nil
	}
	// Raft lifecycle authority is the server-provided start command. Register
	// it before changing the Inbox so a cross-Runtime start that omitted its
	// required stop fails without corrupting the current launch's routing.
	if _, err := runner.inboxes.AcceptStart(payload.AgentID, payload.RuntimeID); err != nil {
		_ = runner.processes.Stop(callback)
		return protocol.AgentStartAckPayload{}, agentProcessCallback{}, false, fmt.Errorf("prepare managed Agent Inbox: %w", err)
	}
	if runner.residency != nil {
		// Best-effort, like prepareManagedAgentStartFailure below: the launch
		// was just registered in APM above, so this lookup only fails if a
		// concurrent Stop already superseded it, in which case epoch 0 is
		// safely rejected by clear's tombstone (or simply overwritten by
		// whatever legitimate write follows).
		startStopEpoch, _ := runner.processes.startStopEpoch(callback)
		runner.residency.rememberLaunch(payload.AgentID, payload.RuntimeID, callback.AgentInstanceID, startStopEpoch)
	}
	return ack, callback, false, nil
}

func (runner *WorkspaceDaemon) completeManagedAgentStart(ctx context.Context, payload protocol.AgentStartPayload, callback agentProcessCallback, ack protocol.AgentStartAckPayload) (managedAgentStartOutcome, error) {
	startStopEpoch, err := runner.processes.startStopEpoch(callback)
	if err != nil {
		return managedAgentStartOutcome{}, err
	}
	stopRequested := func() bool {
		return runner.processes.stopEpochChanged(payload.AgentID, startStopEpoch)
	}
	if stopRequested() {
		return managedAgentStartOutcome{}, errManagedAgentStartStopped
	}
	if current, ok := runner.processes.Snapshot(payload.AgentID); !ok || current.AgentInstanceID != callback.AgentInstanceID {
		return managedAgentStartOutcome{}, errors.New("managed start was superseded before provider startup")
	} else {
		ack.QueueState = current.QueueState
	}
	if runner.configureProviderSession == nil {
		return runner.prepareManagedAgentStartFailure(payload, callback, managedRuntimeFailureSpawn, "provider_session_unavailable"), errors.New("managed start provider session owner is unavailable")
	}
	if err := runner.configureProviderSession(payload.AgentID, payload.RuntimeID, payload.Config.SessionID); err != nil {
		return runner.prepareManagedAgentStartFailure(payload, callback, managedRuntimeFailureSpawn, "provider_session_failed"), fmt.Errorf("configure managed Agent provider session: %w", err)
	}
	if stopRequested() {
		return managedAgentStartOutcome{}, errManagedAgentStartStopped
	}
	if err := runner.ensureResidentRuntime(ctx, payload.AgentID, payload.RuntimeID, nil); err != nil {
		return runner.prepareManagedAgentStartFailure(payload, callback, managedRuntimeFailureSpawn, "provider_spawn_failed"), fmt.Errorf("start managed Agent provider: %w", err)
	}
	if stopRequested() {
		return managedAgentStartOutcome{}, runner.cleanupStoppedManagedAgentStart(payload)
	}
	if err := runner.admitManagedProviderProcess(payload, callback); err != nil {
		_ = runner.runtimes.beginResidentTermination(payload.AgentID, payload.RuntimeID)
		return runner.prepareManagedAgentStartFailure(payload, callback, managedRuntimeFailureSpawn, "provider_spawn_failed"), fmt.Errorf("admit managed Agent process: %w", err)
	}
	if stopRequested() {
		return managedAgentStartOutcome{}, runner.cleanupStoppedManagedAgentStart(payload)
	}
	if current, ok := runner.processes.Snapshot(payload.AgentID); !ok || current.AgentInstanceID != callback.AgentInstanceID {
		if !ok || current.RuntimeID != payload.RuntimeID {
			_ = runner.runtimes.beginResidentTermination(payload.AgentID, payload.RuntimeID)
		}
		return managedAgentStartOutcome{}, errors.New("managed start was superseded during provider startup")
	}
	status := protocol.AgentStatusPayload{AgentID: ack.AgentID, Status: protocol.AgentStatusActive}
	session := protocol.AgentSessionPayload{AgentID: ack.AgentID}
	if runner.currentProviderSession != nil {
		providerSessionID, err := runner.currentProviderSession(payload.AgentID, payload.RuntimeID)
		if err == nil {
			session.ProviderSessionID = providerSessionID
		} else {
			// Raft config.sessionId remains authoritative when a test adapter has
			// no durable local session store.
			session.ProviderSessionID = payload.Config.SessionID
		}
	}
	if stopRequested() {
		return managedAgentStartOutcome{}, runner.cleanupStoppedManagedAgentStart(payload)
	}
	return managedAgentStartOutcome{status: status, session: session, startStopEpoch: startStopEpoch}, nil
}

func (runner *WorkspaceDaemon) cleanupStoppedManagedAgentStart(payload protocol.AgentStartPayload) error {
	if err := runner.runtimes.beginResidentTermination(payload.AgentID, payload.RuntimeID); err != nil {
		return fmt.Errorf("clean up managed Agent start after Stop: %w", err)
	}
	return errManagedAgentStartStopped
}

func (runner *WorkspaceDaemon) establishManagedAgentStart(payload protocol.AgentStartPayload, agentInstanceID string, outcome managedAgentStartOutcome) error {
	if err := runner.activity.SetManaged(agentInstanceID, outcome.status, outcome.session); err != nil {
		return fmt.Errorf("record managed start: %w", err)
	}
	if runner.residency != nil {
		runner.residency.rememberIdle(payload.AgentID, payload.RuntimeID, agentInstanceID, outcome.startStopEpoch)
	}
	return nil
}

func (runner *WorkspaceDaemon) flushManagedAgentStartMessages(ctx context.Context, payload protocol.AgentStartPayload, ack protocol.AgentStartAckPayload) {
	if coordinator, runtimeID, ok := runner.messageCoordinator(payload.AgentID); ok && runtimeID == payload.RuntimeID {
		if _, err := coordinator.flushWithResult(ctx, true); err != nil {
			if runner.logger != nil {
				if errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
					runner.logger.Debug("WorkspaceDaemon buffered Message flush deferred", runner.managedStartLogAttrs(payload, ack.QueueState, "runtime_busy", "deferred", err)...)
				} else {
					runner.logger.Warn("WorkspaceDaemon buffered Message flush failed", runner.managedStartLogAttrs(payload, ack.QueueState, "message_flush_failed", "failed", err)...)
				}
			}
		}
	}
}

func (runner *WorkspaceDaemon) prepareManagedAgentStartFailure(payload protocol.AgentStartPayload, callback agentProcessCallback, stage managedRuntimeFailureStage, reason string) managedAgentStartOutcome {
	at := runner.activity.now().UTC()
	startStopEpoch, _ := runner.processes.startStopEpoch(callback)
	if runner.residency != nil {
		runner.residency.rememberFailure(payload.AgentID, payload.RuntimeID, callback.AgentInstanceID, startStopEpoch, stage, reason, "")
	}
	return managedAgentStartOutcome{
		status:       protocol.AgentStatusPayload{AgentID: payload.AgentID, Status: protocol.AgentStatusInactive},
		failureStage: stage, failureReason: reason, failureAt: at,
	}
}

func (runner *WorkspaceDaemon) publishManagedAgentStartFailure(payload protocol.AgentStartPayload, callback agentProcessCallback, outcome managedAgentStartOutcome) {
	if outcome.status.AgentID == "" || !runner.processes.ownsManagedProcess(callback) {
		return
	}
	runner.publishManagedRuntimeFailure(outcome.status, callback.AgentInstanceID, payload.RuntimeID, outcome.failureStage, outcome.failureReason, "", outcome.failureAt)
}

func (runner *WorkspaceDaemon) managedStartLogAttrs(payload protocol.AgentStartPayload, queueState, reason, outcome string, err error) []any {
	args := []any{
		"computer_id", runner.config.DaemonID,
		"workspace_id", runner.config.WorkspaceID,
		"agent_id", payload.AgentID,
		"runtime_id", payload.RuntimeID,
		"queue_state", queueState,
		"reason", reason,
		"outcome", outcome,
	}
	if err != nil {
		args = append(args, "error", err)
	}
	return args
}

// admitManagedProviderProcess is Raft's this.agents.set after spawn: the
// launch is Running only once the provider process exists. Starting Activity
// is a separate frame and does not mean the process is missing.
func (runner *WorkspaceDaemon) admitManagedProviderProcess(payload protocol.AgentStartPayload, callback agentProcessCallback) error {
	if runner == nil || runner.processes == nil {
		return errors.New("WorkspaceDaemon process manager is unavailable")
	}
	current, ok := runner.processes.Snapshot(payload.AgentID)
	if !ok || current.AgentInstanceID != callback.AgentInstanceID {
		return errors.New("managed start was superseded before process admission")
	}
	if current.QueueState == protocol.AgentStartQueueRunning {
		return nil
	}
	if current.QueueState != protocol.AgentStartQueueStarting {
		return fmt.Errorf("managed start is not admitted for process spawn: %s", current.QueueState)
	}
	callback.ProcessInstanceID = "resident-" + callback.AgentInstanceID
	if err := runner.processes.ProcessSpawned(callback); err != nil {
		return err
	}
	return runner.processes.RuntimeReady(callback)
}
