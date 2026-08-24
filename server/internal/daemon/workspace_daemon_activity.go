package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type managedRuntimeFailureStage string

const (
	managedRuntimeFailureSpawn   managedRuntimeFailureStage = "spawn"
	managedRuntimeFailureRuntime managedRuntimeFailureStage = "runtime"
)

type managedAgentLifecycle struct {
	agentProcessManagerSnapshot
}

func (runner *WorkspaceDaemon) managedLaunch(agentID, runtimeID string) (managedAgentLifecycle, bool) {
	if runner == nil || runner.processes == nil {
		return managedAgentLifecycle{}, false
	}
	process, found := runner.processes.Snapshot(agentID)
	if !found || runtimeID != "" && process.RuntimeID != runtimeID || runner.residency == nil {
		return managedAgentLifecycle{}, false
	}
	resident, found := runner.residency.get(agentID)
	if !found || resident.runtimeID != process.RuntimeID || resident.agentInstanceID == "" {
		return managedAgentLifecycle{}, false
	}
	return managedAgentLifecycle{agentProcessManagerSnapshot: process}, true
}

// retryManagedLaunchAfterExit is the resident process event bus's "exited,
// under the crash retry cap" route (resident_crash_watch.go's
// onResidentRuntimeExited is the subscriber; this is what it calls into for
// the recoverable case). The launch is kept — queueState returns to
// Starting and the next delivery lazily recreates the provider process, same
// as ProcessExited(recover=true) always did. It exists so callers outside
// the WorkspaceDaemon module never reach runner.processes directly — see
// TestWorkspaceDaemonInternalsDoNotEscapeRunnerModule.
func (runner *WorkspaceDaemon) retryManagedLaunchAfterExit(agentID string, launch agentProcessManagerSnapshot) error {
	if runner == nil || runner.processes == nil {
		return nil
	}
	callback := agentProcessCallback{AgentID: agentID, AgentInstanceID: launch.AgentInstanceID, ProcessInstanceID: launch.ProcessInstanceID}
	return runner.processes.ProcessExited(callback, true)
}

// retireManagedLaunchAfterExit is the resident process event bus's "exited,
// over the crash retry cap" route. Unlike retryManagedLaunchAfterExit it
// never calls processes.ProcessExited — failManagedRuntime's
// prepareManagedRuntimeFailure -> failManagedProcess -> stopLocked already
// performs the launch teardown (release capacity grant, drop the launch,
// promote the next queued agent), and it additionally publishes the
// AgentStatusInactive + error Activity that a mid-turn provider failure
// already gets, which a bare ProcessExited(recover=false) never did. Calling
// both would tear the same launch down twice.
func (runner *WorkspaceDaemon) retireManagedLaunchAfterExit(agentID, runtimeID string, launch agentProcessManagerSnapshot, reasonCode string) error {
	if runner == nil || runner.processes == nil || runner.activity == nil {
		return errors.New("WorkspaceDaemon is unavailable")
	}
	runner.failManagedRuntime(agentID, runtimeID, launch.AgentInstanceID, managedRuntimeFailureRuntime, reasonCode,
		"resident provider process exceeded the crash retry cap", time.Now().UTC())
	return nil
}

// broadcastActivity is Raft 1.0.16's spawn Activity boundary. Starting is
// broadcast only after the provider process exists and active status has been
// published; replaying a start never calls this method.
func (runner *WorkspaceDaemon) broadcastActivity(agentID, runtimeID, detailKind string) {
	if detailKind != "starting" {
		return
	}
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil || launch.ProcessInstanceID == "" {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, AgentInstanceID: launch.AgentInstanceID, Kind: AgentObservationRuntimeStarting,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, detailKind)
}

// observeResidentRuntimeReady closes the resident-only gap after provider
// initialization. Raft advances Starting from the first runtime event because
// its spawn always carries an initial turn. A resident provider can initialize
// and sit idle without a turn, so APM readiness itself is the terminal startup
// fact and must settle Activity to Online instead of waiting for a Message.
func (runner *WorkspaceDaemon) observeResidentRuntimeReady(agentID, runtimeID string) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil || launch.QueueState != protocol.AgentStartQueueRunning || launch.ProcessInstanceID == "" {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, AgentInstanceID: launch.AgentInstanceID, Kind: AgentObservationRuntimeIdle,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, "Resident runtime ready")
}

// resolveManagedLaunch is the single runtimeIndex -> workspace ->
// currentWorkspaceDaemon -> managedLaunch lookup shared by every resident
// process event route that needs to reach the APM-owned launch for an
// (agentID, runtimeID) pair (observeResidentRuntimeStalled, the "exited"
// route in resident_crash_watch.go). A missing runtime, runner, or launch is
// normal — e.g. the workspace detached, or the launch never reached APM —
// and callers treat a false as "nothing to route to", not an error.
func (d *Daemon) resolveManagedLaunch(agentID, runtimeID string) (agentProcessManagerSnapshot, *WorkspaceDaemon, bool) {
	if d == nil {
		return agentProcessManagerSnapshot{}, nil, false
	}
	d.mu.Lock()
	runtime, ok := d.runtimeIndex[runtimeID]
	d.mu.Unlock()
	if !ok {
		return agentProcessManagerSnapshot{}, nil, false
	}
	runner := d.currentWorkspaceDaemon(runtime.WorkspaceID)
	if runner == nil {
		return agentProcessManagerSnapshot{}, nil, false
	}
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found {
		return agentProcessManagerSnapshot{}, nil, false
	}
	return launch.agentProcessManagerSnapshot, runner, true
}

func (d *Daemon) observeResidentRuntimeStalled(agentID, runtimeID string, staleFor time.Duration) {
	if d == nil {
		return
	}
	_, runner, found := d.resolveManagedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	resident, residentFound := runner.residency.get(agentID)
	if !residentFound || resident.agentInstanceID == "" {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, AgentInstanceID: resident.agentInstanceID, Kind: AgentObservationRuntimeStalled,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID, StaleFor: staleFor}, At: time.Now().UTC(),
	}, "Resident runtime stalled")
}

// stopManagedAgent owns the complete Raft stop transition. The inactive
// lifecycle fact must reach the server before the terminal Stopped Activity;
// only after both have been published may the local Activity state be
// forgotten. No second ownership registry participates in this operation.
func (runner *WorkspaceDaemon) stopManagedAgent(ctx context.Context, payload protocol.AgentStopPayload, pause func(), writeFrame func(string, any) error) error {
	if runner == nil || runner.processes == nil || runner.runtimes == nil || runner.inboxes == nil || runner.activity == nil || writeFrame == nil {
		return errors.New("WorkspaceDaemon stop dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	current, currentFound := runner.processes.Snapshot(payload.AgentID)
	callback := agentProcessCallback{AgentID: payload.AgentID}
	if currentFound {
		callback.AgentInstanceID = current.AgentInstanceID
	}
	launch, startupDone, found, err := runner.processes.beginManagedStop(callback)
	if err != nil {
		// A stop for an older launch must never tear down its replacement.
		return nil
	}
	runtimeID := launch.RuntimeID
	if !found {
		// APM can disappear independently of a resident provider during socket
		// recovery. Residency retains the local Agent instance and Runtime
		// needed to finish the current desired Stop.
		resident, ok := runner.residency.get(payload.AgentID)
		if !ok {
			return nil
		}
		callback.AgentInstanceID = resident.agentInstanceID
		runtimeID = resident.runtimeID
		runner.processes.recordStop(payload.AgentID)
	}
	if pause != nil {
		pause()
	}
	if runner.residency != nil {
		runner.residency.clear(payload.AgentID)
	}
	runner.inboxes.Remove(payload.AgentID, runtimeID)
	if runtimeID != "" {
		// Dispatch the kill immediately, before waiting on startupDone: a
		// managed start blocked inside provider spawn runs on the Workspace
		// Runner's own lifetime context (runner.life), not this stop's ctx,
		// so nothing else can ever interrupt it. Waiting first would
		// deadlock the stop against its own precondition. The dispatch is
		// fire-and-forget here (fire-and-forget everywhere else this pool
		// method is called) — its own failure (e.g. a non-ForceKillable
		// backend) still shows up in the confirm wait below.
		_ = runner.runtimes.beginResidentTermination(payload.AgentID, runtimeID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if found {
		select {
		case <-startupDone:
		case <-ctx.Done():
			return fmt.Errorf("wait for managed Agent startup to settle: %w", ctx.Err())
		}
	}
	if runtimeID != "" {
		if err := runner.runtimes.awaitResidentTerminated(ctx, payload.AgentID, runtimeID); err != nil {
			// A stop that cannot confirm the kill must still leave the system
			// in a consistent, reported state. Falling through here (instead
			// of returning) is the fix for the half-stop: the Inbox and
			// residency are already cleared above, so an early return at this
			// point would strand the launch in `stopping` with no terminal
			// status ever published, and workspace_daemon.go's caller would
			// additionally call failConnection and tear down every other
			// agent on this connection over one unconfirmed kill.
			var timeout *residentTerminationTimeout
			if runner.logger != nil {
				if errors.As(err, &timeout) {
					runner.logger.Warn("resident termination unconfirmed, completing Stop anyway",
						"agent_id", payload.AgentID, "runtime_id", runtimeID,
						"process_alive", timeout.ProcessAlive, "turn_running", timeout.TurnRunning)
				} else {
					runner.logger.Warn("resident termination unconfirmed, completing Stop anyway",
						"agent_id", payload.AgentID, "runtime_id", runtimeID, "error", err)
				}
			}
		}
	}
	if found {
		runner.processes.completeManagedStop(callback)
	}
	return runner.publishManagedAgentInactive(payload, callback.AgentInstanceID, runtimeID, writeFrame)
}

func (runner *WorkspaceDaemon) publishManagedAgentInactive(payload protocol.AgentStopPayload, agentInstanceID, runtimeID string, writeFrame func(string, any) error) error {
	status := protocol.AgentStatusPayload{AgentID: payload.AgentID, Status: protocol.AgentStatusInactive}
	if err := runner.activity.SetManaged(agentInstanceID, status, protocol.AgentSessionPayload{AgentID: payload.AgentID}); err != nil {
		return fmt.Errorf("record managed stop: %w", err)
	}
	if err := writeFrame(protocol.EventAgentStatus, status); err != nil {
		return err
	}
	runner.activity.InterruptCompactionIfActive(payload.AgentID, agentInstanceID)
	if runtimeID == "" {
		runner.activity.RemoveManaged(payload.AgentID, agentInstanceID)
		return nil
	}
	if err := runner.activity.Observe(AgentObservation{
		AgentID: payload.AgentID, AgentInstanceID: agentInstanceID, Kind: AgentObservationOffline,
		Data: AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: "stopped"}, At: runner.activity.now().UTC(),
	}); err != nil {
		return fmt.Errorf("publish managed stop Activity: %w", err)
	}
	runner.activity.RemoveManaged(payload.AgentID, agentInstanceID)
	return nil
}

func (runner *WorkspaceDaemon) observeResidentMessageRuntime(agentID, runtimeID string, message agent.Message) {
	poisoned := message.Type == agent.MessageError
	if poisoned {
		if _, ok := classifyPoisonedError(message.Content); !ok {
			poisoned = false
		}
	}
	if poisoned {
		if runner.recordProviderSession != nil {
			runner.recordProviderSession(agentID, runtimeID, "")
		}
	} else if message.SessionID != "" {
		if runner.recordProviderSession != nil {
			runner.recordProviderSession(agentID, runtimeID, message.SessionID)
		}
		if launch, found := runner.managedLaunch(agentID, runtimeID); found && runner.activity != nil {
			session := protocol.AgentSessionPayload{AgentID: agentID, ProviderSessionID: message.SessionID}
			if changed, err := runner.activity.UpdateProviderSession(launch.AgentInstanceID, session); err == nil && changed {
				runner.sendAgentFrame(protocol.EventAgentSession, session)
			}
		}
	}
	if message.Type == agent.MessageDiagnostic {
		runner.observeResidentRuntimeDiagnostic(agentID, runtimeID, message)
		return
	}
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}

	at := time.Now().UTC()
	stage := AgentRuntimeStageObservationData{RuntimeID: runtimeID}
	switch message.Type {
	case agent.MessageThinking, agent.MessageText, agent.MessageToolUse:
		_, _ = runner.activity.CompleteCompactionIfActive(agentID, launch.AgentInstanceID, stage, at)
	case agent.MessageError:
		runner.activity.InterruptCompactionIfActive(agentID, launch.AgentInstanceID)
	}

	var kind AgentObservationKind
	switch message.Type {
	case agent.MessageThinking:
		kind = AgentObservationRuntimeThinking
	case agent.MessageText:
		// Raft 1.0.16: runtime text is Working / model_response_started.
		// Do not parse reply content into the timeline.
		kind = AgentObservationRuntimeWorking
	case agent.MessageCompactionStarted:
		kind = AgentObservationRuntimeCompacting
	case agent.MessageCompactionFinished:
		kind = AgentObservationRuntimeCompacted
	case agent.MessageError:
		kind = AgentObservationError
	case agent.MessageToolUse:
		kind = AgentObservationRuntimeTool
	}
	if kind == "" {
		return
	}
	var data AgentObservationData = stage
	if kind == AgentObservationError {
		data = AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: "provider_failed", Message: message.Content}
	} else if kind == AgentObservationRuntimeTool {
		data = AgentRuntimeStageObservationData{RuntimeID: runtimeID, ToolName: message.Tool, ToolCallID: message.CallID, ToolInput: message.Input}
	}
	runner.observeActivity(AgentObservation{AgentID: agentID, Kind: kind, Data: data, At: at}, "Message Runtime")
}

func (runner *WorkspaceDaemon) observeResidentRuntimeDiagnostic(agentID, runtimeID string, message agent.Message) {
	if message.Level != "warning" || strings.TrimSpace(message.Title) == "" || strings.TrimSpace(message.Content) == "" {
		return
	}
	_, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, Kind: AgentObservationRuntimeDiagnostic,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, "Runtime diagnostic")
}

func (runner *WorkspaceDaemon) observeMessageTurnCompletion(agentID, runtimeID string, turnErr error) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	at := time.Now().UTC()
	stage := AgentRuntimeStageObservationData{RuntimeID: runtimeID}
	if turnErr != nil {
		runner.failManagedRuntime(agentID, runtimeID, launch.AgentInstanceID, managedRuntimeFailureRuntime, "provider_turn_failed", turnErr.Error(), at)
		return
	}
	_, _ = runner.activity.CompleteCompactionIfActive(agentID, launch.AgentInstanceID, stage, at)
	runner.observeActivity(AgentObservation{AgentID: agentID, Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, "Message completion")
}

// failManagedRuntime owns the Raft-style runtime-error transition. Activity
// projection remains in agentActivityProducer; this method only coordinates
// the lifecycle facts that must change together.
func (runner *WorkspaceDaemon) failManagedRuntime(agentID, runtimeID, agentInstanceID string, stage managedRuntimeFailureStage, reasonCode, message string, at time.Time) protocol.AgentStatusPayload {
	status := runner.prepareManagedRuntimeFailure(agentID, runtimeID, agentInstanceID, stage, reasonCode, message)
	if status.AgentID != "" {
		runner.publishManagedRuntimeFailure(status, agentInstanceID, runtimeID, stage, reasonCode, message, at)
	}
	return status
}

func (runner *WorkspaceDaemon) prepareManagedRuntimeFailure(agentID, runtimeID, agentInstanceID string, stage managedRuntimeFailureStage, reasonCode, message string) protocol.AgentStatusPayload {
	callback := agentProcessCallback{AgentID: agentID, AgentInstanceID: agentInstanceID}
	startStopEpoch, err := runner.processes.startStopEpoch(callback)
	if err != nil {
		return protocol.AgentStatusPayload{}
	}
	if !runner.processes.failManagedProcess(callback) {
		// Lifecycle stop already owns this launch. Its quiescence fence is the
		// only path allowed to publish inactive for the stop launch.
		return protocol.AgentStatusPayload{}
	}
	currentAgentInstanceID := ""
	if runner.residency != nil {
		if resident, found := runner.residency.get(agentID); found {
			currentAgentInstanceID = resident.agentInstanceID
		}
	}
	if currentAgentInstanceID == "" {
		return protocol.AgentStatusPayload{}
	}
	if runner.residency != nil {
		runner.residency.rememberFailure(agentID, runtimeID, currentAgentInstanceID, startStopEpoch, stage, reasonCode, message)
	}
	return protocol.AgentStatusPayload{AgentID: agentID, Status: protocol.AgentStatusInactive}
}

func (runner *WorkspaceDaemon) publishManagedRuntimeFailure(status protocol.AgentStatusPayload, agentInstanceID, runtimeID string, stage managedRuntimeFailureStage, reasonCode, message string, at time.Time) {
	runner.activity.InterruptCompactionIfActive(status.AgentID, agentInstanceID)
	_ = runner.activity.SetManaged(agentInstanceID, status, protocol.AgentSessionPayload{AgentID: status.AgentID})
	runner.sendAgentFrame(protocol.EventAgentStatus, status)
	kind := AgentObservationError
	if stage == managedRuntimeFailureSpawn {
		kind = AgentObservationOffline
	}
	runner.observeActivity(AgentObservation{
		AgentID: status.AgentID, AgentInstanceID: agentInstanceID, Kind: kind,
		Data: AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: reasonCode, Message: message}, At: at,
	}, "Runtime failure")
}

// broadcastMessageReceivedActivity matches Raft 1.0.16's single write site:
// the ordinary Message batch has crossed the provider runtime input boundary.
// Pending acceptance and content-free Notices do not publish this Activity.
func (runner *WorkspaceDaemon) broadcastMessageReceivedActivity(agentID, runtimeID string, messages []protocol.AgentMessageProjection) {
	if len(messages) == 0 {
		return
	}
	if _, found := runner.managedLaunch(agentID, runtimeID); found && runner.activity != nil {
		runner.observeActivity(AgentObservation{
			AgentID: agentID, Kind: AgentObservationMessageBodyAccepted,
			Data: AgentMessageAcceptanceObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
		}, "Message accepted")
	}
}

func (runner *WorkspaceDaemon) observeMessageSendHold(agentID, target string, newer int64, reason string) {
	if runner == nil {
		return
	}
	if runner.logger != nil {
		runner.logger.Info("Credential Proxy Message send held", "agent_id", agentID, "workspace_id", runner.config.WorkspaceID, "target", target, "new_message_count", newer, "reason", reason)
	}
	launch, found := runner.managedLaunch(agentID, "")
	if !found || runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, Kind: AgentObservationFreshnessHeld,
		Data: AgentFreshnessHoldObservationData{RuntimeID: launch.RuntimeID, Target: target, NewMessageCount: int(newer), ReasonCode: reason}, At: time.Now().UTC(),
	}, "Message send hold")
}

func (runner *WorkspaceDaemon) observeMessageSendDraftSent(agentID, target string, anyway bool) {
	if runner == nil {
		return
	}
	if runner.logger != nil {
		runner.logger.Info("Credential Proxy saved Draft sent", "agent_id", agentID, "workspace_id", runner.config.WorkspaceID, "target", target, "anyway", anyway)
	}
	launch, found := runner.managedLaunch(agentID, "")
	if !found || runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, Kind: AgentObservationDraftSent,
		Data: AgentDraftSentObservationData{RuntimeID: launch.RuntimeID, Target: target, Anyway: anyway}, At: time.Now().UTC(),
	}, "Draft sent")
}

func (runner *WorkspaceDaemon) observeActivity(observation AgentObservation, phase string) {
	if runner == nil || runner.activity == nil {
		return
	}
	if observation.AgentInstanceID == "" {
		if launch, found := runner.managedLaunch(observation.AgentID, ""); found {
			observation.AgentInstanceID = launch.AgentInstanceID
		}
	}
	if err := runner.activity.Observe(observation); err != nil && runner.logger != nil {
		runner.logger.Debug("WorkspaceDaemon Activity observation deferred", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", observation.AgentID, "phase", phase)
	}
}
