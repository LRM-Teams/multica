package daemon

import (
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
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

func (runner *WorkspaceDaemon) managedLaunchForProcess(callback agentProcessCallback, runtimeID string) (managedAgentLifecycle, bool) {
	launch, found := runner.managedLaunch(callback.AgentID, runtimeID)
	if !found || callback.AgentInstanceID == "" || callback.ProcessInstanceID == "" ||
		launch.AgentInstanceID != callback.AgentInstanceID || launch.ProcessInstanceID != callback.ProcessInstanceID {
		return managedAgentLifecycle{}, false
	}
	return launch, true
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

func (runner *WorkspaceDaemon) observeResidentMessageRuntime(agentID, runtimeID string, message agent.Message) {
	launch, _ := runner.managedLaunch(agentID, runtimeID)
	runner.observeResidentMessageRuntimeForLaunch(agentID, runtimeID, message, launch)
}

func (runner *WorkspaceDaemon) observeResidentMessageRuntimeForProcess(callback agentProcessCallback, runtimeID string, message agent.Message) {
	launch, found := runner.managedLaunchForProcess(callback, runtimeID)
	if !found {
		return
	}
	runner.observeResidentMessageRuntimeForLaunch(callback.AgentID, runtimeID, message, launch, callback)
}

func (runner *WorkspaceDaemon) observeResidentMessageRuntimeForLaunch(agentID, runtimeID string, message agent.Message, launch managedAgentLifecycle, callbacks ...agentProcessCallback) {
	poisoned := message.Type == agent.MessageError
	if poisoned {
		if _, ok := classifyPoisonedError(message.Content); !ok {
			poisoned = false
		}
	}
	updateSession := func() {
		if poisoned {
			if runner.recordProviderSession != nil {
				runner.recordProviderSession(agentID, runtimeID, "")
			}
		} else if message.SessionID != "" {
			if runner.recordProviderSession != nil {
				runner.recordProviderSession(agentID, runtimeID, message.SessionID)
			}
			if runner.activity != nil && launch.AgentInstanceID != "" {
				session := protocol.AgentSessionPayload{AgentID: agentID, ProviderSessionID: message.SessionID}
				if changed, err := runner.activity.UpdateProviderSession(launch.AgentInstanceID, session); err == nil && changed {
					runner.sendAgentFrame(protocol.EventAgentSession, session)
				}
			}
		}
	}
	if len(callbacks) != 0 {
		if !runner.processes.withActiveManagedProcess(callbacks[0], updateSession) {
			return
		}
	} else {
		updateSession()
	}
	if launch.AgentInstanceID == "" {
		return
	}
	if message.Type == agent.MessageDiagnostic {
		runner.observeResidentRuntimeDiagnosticForLaunch(agentID, runtimeID, message, launch)
		return
	}
	if runner.activity == nil {
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
	runner.observeActivity(AgentObservation{AgentID: agentID, AgentInstanceID: launch.AgentInstanceID, Kind: kind, Data: data, At: at}, "Message Runtime")
}

func (runner *WorkspaceDaemon) observeResidentRuntimeDiagnostic(agentID, runtimeID string, message agent.Message) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found {
		return
	}
	runner.observeResidentRuntimeDiagnosticForLaunch(agentID, runtimeID, message, launch)
}

func (runner *WorkspaceDaemon) observeResidentRuntimeDiagnosticForLaunch(agentID, runtimeID string, message agent.Message, launch managedAgentLifecycle) {
	if message.Level != "warning" || strings.TrimSpace(message.Title) == "" || strings.TrimSpace(message.Content) == "" {
		return
	}
	if runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, AgentInstanceID: launch.AgentInstanceID, Kind: AgentObservationRuntimeDiagnostic,
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

func (runner *WorkspaceDaemon) observeMessageTurnCompletionForProcess(callback agentProcessCallback, runtimeID string, turnErr error) {
	_, found := runner.managedLaunchForProcess(callback, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	at := time.Now().UTC()
	stage := AgentRuntimeStageObservationData{RuntimeID: runtimeID}
	if turnErr != nil {
		runner.failManagedRuntime(callback.AgentID, runtimeID, callback.AgentInstanceID, managedRuntimeFailureRuntime, "provider_turn_failed", turnErr.Error(), at)
		return
	}
	_, _ = runner.activity.CompleteCompactionIfActive(callback.AgentID, callback.AgentInstanceID, stage, at)
	runner.observeActivity(AgentObservation{AgentID: callback.AgentID, AgentInstanceID: callback.AgentInstanceID, Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, "Message completion")
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
