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

func (runner *WorkspaceRunner) managedLaunch(agentID, runtimeID string) (agentProcessManagerSnapshot, bool) {
	if runner == nil || runner.processes == nil {
		return agentProcessManagerSnapshot{}, false
	}
	launch, found := runner.processes.Snapshot(agentID)
	return launch, found && (runtimeID == "" || launch.RuntimeID == runtimeID)
}

// observeRuntimeStarting is Raft 1.0.16 spawn Activity: working / starting /
// "Starting…". The process must already be in APM (this.agents.set).
func (runner *WorkspaceRunner) observeRuntimeStarting(agentID, runtimeID, phase string) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil || launch.ProcessInstanceID == "" {
		return
	}
	if launch.QueueState == protocol.AgentStartQueueRunning && phase != "Managed start" {
		// After APM admits Running, later Messages must not repaint Starting.
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeStarting,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, phase)
}

// observeResidentRuntimeReady closes the resident-only gap after provider
// initialization. Raft advances Starting from the first runtime event because
// its spawn always carries an initial turn. A resident provider can initialize
// and sit idle without a turn, so APM readiness itself is the terminal startup
// fact and must settle Activity to Online instead of waiting for a Message.
func (runner *WorkspaceRunner) observeResidentRuntimeReady(agentID, runtimeID string) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil || launch.QueueState != protocol.AgentStartQueueRunning || launch.ProcessInstanceID == "" {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeIdle,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, "Resident runtime ready")
}

// publishManagedAgentStartActivity runs only after a new provider spawn has
// written active status. Replayed starts must not call this: Raft's
// rebindRunningStart republishes status/session and leaves lastActivity alone.
func (runner *WorkspaceRunner) publishManagedAgentStartActivity(agentID, runtimeID string) {
	runner.observeRuntimeStarting(agentID, runtimeID, "Managed start")
	runner.observeResidentRuntimeReady(agentID, runtimeID)
}

// stopManagedAgent owns the complete Raft stop transition. The inactive
// lifecycle fact must reach the server before the terminal Stopped Activity;
// only after both have been published may the local Activity state be
// forgotten. No second ownership registry participates in this operation.
func (runner *WorkspaceRunner) stopManagedAgent(ctx context.Context, payload protocol.WorkspaceRunnerAgentStopPayload, pause func(), writeFrame func(string, any) error) error {
	if runner == nil || runner.processes == nil || runner.runtimes == nil || runner.inboxes == nil || runner.activity == nil || writeFrame == nil {
		return errors.New("Workspace Runner stop dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	callback := agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID}
	launch, startupDone, found, err := runner.processes.beginManagedStop(callback)
	if err != nil {
		// A stop for an older launch must never tear down its replacement.
		return nil
	}
	runtimeID := launch.RuntimeID
	if !found {
		// APM can disappear independently of a resident provider during socket
		// recovery. Residency retains the launch fence and Runtime needed to
		// finish that stop. A different resident launch is a stale command.
		if resident, ok := runner.residency.get(payload.AgentID); ok {
			if resident.launchID != "" && resident.launchID != payload.LaunchID {
				return nil
			}
			runtimeID = resident.runtimeID
		}
	}
	if pause != nil {
		pause()
	}
	if runner.residency != nil {
		runner.residency.clear(payload.AgentID)
	}
	runner.inboxes.Remove(payload.AgentID, runtimeID)
	if runtimeID != "" {
		if err := runner.runtimes.forceInvalidateSession(payload.AgentID, runtimeID); err != nil {
			return fmt.Errorf("stop managed Agent provider: %w", err)
		}
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
		if err := runner.runtimes.awaitSessionQuiescence(ctx, payload.AgentID, runtimeID); err != nil {
			return fmt.Errorf("wait for managed Agent provider stop: %w", err)
		}
	}
	// Provider startup may have recorded a terminal residency after the stop
	// first cleared it. Re-clear only after startup and provider quiescence so a
	// late failure cannot survive the stop epoch.
	if runner.residency != nil {
		runner.residency.clear(payload.AgentID)
	}
	if found {
		runner.processes.completeManagedStop(callback)
	}
	return runner.publishManagedAgentInactive(payload, runtimeID, writeFrame)
}

func (runner *WorkspaceRunner) publishManagedAgentInactive(payload protocol.WorkspaceRunnerAgentStopPayload, runtimeID string, writeFrame func(string, any) error) error {
	status := protocol.AgentStatusPayload{AgentID: payload.AgentID, LaunchID: payload.LaunchID, Status: protocol.AgentStatusInactive}
	if err := runner.activity.SetManaged(status, protocol.AgentSessionPayload{AgentID: payload.AgentID, LaunchID: payload.LaunchID}); err != nil {
		return fmt.Errorf("record managed stop: %w", err)
	}
	if err := writeFrame(protocol.EventAgentStatus, status); err != nil {
		return err
	}
	runner.activity.InterruptCompactionIfActive(payload.AgentID, payload.LaunchID)
	if runtimeID == "" {
		runner.activity.RemoveManaged(payload.AgentID, payload.LaunchID)
		return nil
	}
	if err := runner.activity.Observe(AgentObservation{
		AgentID: payload.AgentID, LaunchID: payload.LaunchID, Kind: AgentObservationOffline,
		Data: AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: "stopped"}, At: runner.activity.now().UTC(),
	}); err != nil {
		return fmt.Errorf("publish managed stop Activity: %w", err)
	}
	runner.activity.RemoveManaged(payload.AgentID, payload.LaunchID)
	return nil
}

func (runner *WorkspaceRunner) observeResidentMessageRuntime(agentID, runtimeID string, message agent.Message) {
	if message.SessionID != "" {
		if runner.recordProviderSession != nil {
			runner.recordProviderSession(agentID, runtimeID, message.SessionID)
		}
		if launch, found := runner.managedLaunch(agentID, runtimeID); found && runner.activity != nil {
			session := protocol.AgentSessionPayload{AgentID: agentID, LaunchID: launch.LaunchID, ProviderSessionID: message.SessionID}
			if changed, err := runner.activity.UpdateProviderSession(session); err == nil && changed {
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
		_, _ = runner.activity.CompleteCompactionIfActive(agentID, launch.LaunchID, stage, at)
	case agent.MessageError:
		runner.activity.InterruptCompactionIfActive(agentID, launch.LaunchID)
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
	runner.observeActivity(AgentObservation{AgentID: agentID, LaunchID: launch.LaunchID, Kind: kind, Data: data, At: at}, "Message Runtime")
}

func (runner *WorkspaceRunner) observeResidentRuntimeDiagnostic(agentID, runtimeID string, message agent.Message) {
	if message.Level != "warning" || strings.TrimSpace(message.Title) == "" || strings.TrimSpace(message.Content) == "" {
		return
	}
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeDiagnostic,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, "Runtime diagnostic")
}

func (runner *WorkspaceRunner) observeMessageTurnCompletion(agentID, runtimeID string, turnErr error) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	at := time.Now().UTC()
	stage := AgentRuntimeStageObservationData{RuntimeID: runtimeID}
	if turnErr != nil {
		runner.failManagedRuntime(agentID, runtimeID, launch.LaunchID, managedRuntimeFailureRuntime, "provider_turn_failed", turnErr.Error(), at)
		return
	}
	_, _ = runner.activity.CompleteCompactionIfActive(agentID, launch.LaunchID, stage, at)
	runner.observeActivity(AgentObservation{AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, "Message completion")
}

// failManagedRuntime owns the Raft-style runtime-error transition. Activity
// projection remains in agentActivityProducer; this method only coordinates
// the lifecycle facts that must change together.
func (runner *WorkspaceRunner) failManagedRuntime(agentID, runtimeID, launchID string, stage managedRuntimeFailureStage, reasonCode, message string, at time.Time) protocol.AgentStatusPayload {
	status := runner.prepareManagedRuntimeFailure(agentID, runtimeID, launchID, stage, reasonCode, message)
	if status.AgentID != "" {
		runner.publishManagedRuntimeFailure(status, runtimeID, stage, reasonCode, message, at)
	}
	return status
}

func (runner *WorkspaceRunner) prepareManagedRuntimeFailure(agentID, runtimeID, launchID string, stage managedRuntimeFailureStage, reasonCode, message string) protocol.AgentStatusPayload {
	if !runner.processes.failManagedProcess(agentProcessCallback{AgentID: agentID, LaunchID: launchID}) {
		// Lifecycle stop already owns this launch. Its quiescence fence is the
		// only path allowed to publish inactive for the stop launch.
		return protocol.AgentStatusPayload{}
	}
	if runner.residency != nil {
		runner.residency.rememberFailure(agentID, runtimeID, launchID, stage, reasonCode, message)
	}
	return protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusInactive}
}

func (runner *WorkspaceRunner) publishManagedRuntimeFailure(status protocol.AgentStatusPayload, runtimeID string, stage managedRuntimeFailureStage, reasonCode, message string, at time.Time) {
	runner.activity.InterruptCompactionIfActive(status.AgentID, status.LaunchID)
	_ = runner.activity.SetManaged(status, protocol.AgentSessionPayload{AgentID: status.AgentID, LaunchID: status.LaunchID})
	runner.sendAgentFrame(protocol.EventAgentStatus, status)
	kind := AgentObservationError
	if stage == managedRuntimeFailureSpawn {
		kind = AgentObservationOffline
	}
	runner.observeActivity(AgentObservation{
		AgentID: status.AgentID, LaunchID: status.LaunchID, Kind: kind,
		Data: AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: reasonCode, Message: message}, At: at,
	}, "Runtime failure")
}

// broadcastMessageReceivedActivity matches Raft 1.0.16's single write site:
// the ordinary Message batch has crossed the provider runtime input boundary.
// Pending acceptance and content-free Notices do not publish this Activity.
func (runner *WorkspaceRunner) broadcastMessageReceivedActivity(agentID, runtimeID string, messages []protocol.AgentMessageProjection) {
	if len(messages) == 0 {
		return
	}
	if launch, found := runner.managedLaunch(agentID, runtimeID); found && runner.activity != nil {
		runner.observeActivity(AgentObservation{
			AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationMessageBodyAccepted,
			Data: AgentMessageAcceptanceObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
		}, "Message accepted")
	}
}

func (runner *WorkspaceRunner) observeMessageSendHold(agentID, target string, newer int64, reason string) {
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
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationFreshnessHeld,
		Data: AgentFreshnessHoldObservationData{RuntimeID: launch.RuntimeID, Target: target, NewMessageCount: int(newer), ReasonCode: reason}, At: time.Now().UTC(),
	}, "Message send hold")
}

func (runner *WorkspaceRunner) observeMessageSendDraftSent(agentID, target string, anyway bool) {
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
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationDraftSent,
		Data: AgentDraftSentObservationData{RuntimeID: launch.RuntimeID, Target: target, Anyway: anyway}, At: time.Now().UTC(),
	}, "Draft sent")
}

func (runner *WorkspaceRunner) observeActivity(observation AgentObservation, phase string) {
	if runner == nil || runner.activity == nil {
		return
	}
	if err := runner.activity.Observe(observation); err != nil && runner.logger != nil {
		runner.logger.Debug("Workspace Runner Activity observation deferred", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", observation.AgentID, "phase", phase)
	}
}
