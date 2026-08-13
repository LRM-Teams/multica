package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
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

func (runner *WorkspaceRunner) observeMessageLifecycle(agentID, runtimeID string) {
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found || runner.activity == nil {
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeStarting,
		Data: AgentRuntimeStageObservationData{RuntimeID: runtimeID}, At: time.Now().UTC(),
	}, "Message lifecycle")
}

func (runner *WorkspaceRunner) observeResidentMessageRuntime(agentID, runtimeID string, message agent.Message) {
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
		data = AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: "provider_failed"}
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
		runner.failManagedRuntime(agentID, runtimeID, launch.LaunchID, managedRuntimeFailureRuntime, "provider_turn_failed", at)
		return
	}
	_, _ = runner.activity.CompleteCompactionIfActive(agentID, launch.LaunchID, stage, at)
	runner.observeActivity(AgentObservation{AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, "Message completion")
}

// failManagedRuntime owns the Raft-style runtime-error transition. Activity
// projection remains in agentActivityProducer; this method only coordinates
// the lifecycle facts that must change together.
func (runner *WorkspaceRunner) failManagedRuntime(agentID, runtimeID, launchID string, stage managedRuntimeFailureStage, reasonCode string, at time.Time) protocol.AgentStatusPayload {
	runner.activity.InterruptCompactionIfActive(agentID, launchID)
	_ = runner.processes.Stop(agentProcessCallback{AgentID: agentID, LaunchID: launchID})
	if runner.residency != nil {
		runner.residency.rememberFailure(agentID, runtimeID, launchID, stage, reasonCode)
	}
	status := protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusInactive}
	_ = runner.activity.SetManaged(status, protocol.AgentSessionPayload{AgentID: agentID, LaunchID: launchID})
	runner.sendAgentFrame(protocol.EventAgentStatus, status)
	kind := AgentObservationError
	if stage == managedRuntimeFailureSpawn {
		kind = AgentObservationOffline
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: launchID, Kind: kind,
		Data: AgentErrorObservationData{RuntimeID: runtimeID, ReasonCode: reasonCode}, At: at,
	}, "Runtime failure")
	return status
}

func (runner *WorkspaceRunner) observeMessageAccepted(agentID, runtimeID string, messages []protocol.AgentMessageProjection) {
	if len(messages) == 0 {
		return
	}
	targetSet := make(map[string]struct{})
	identity := make([]string, 0, len(messages))
	for _, message := range messages {
		targetSet[message.Target] = struct{}{}
		identity = append(identity, message.ID+"\x00"+message.Target+"\x00"+strconv.FormatInt(message.Seq, 10))
	}
	sort.Strings(identity)
	handoffID := hex.EncodeToString(sha256Sum(strings.Join(identity, "\x01")))

	if launch, found := runner.managedLaunch(agentID, runtimeID); found && runner.activity != nil {
		runner.observeActivity(AgentObservation{
			AgentID: agentID, LaunchID: launch.LaunchID, Kind: AgentObservationMessageBodyAccepted,
			Data: AgentMessageAcceptanceObservationData{RuntimeID: runtimeID, HandoffID: handoffID, MessageCount: len(messages)}, At: time.Now().UTC(),
		}, "Message accepted")
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	runner.sendAgentFrame(protocol.EventAgentMessageHandoff, protocol.AgentMessageHandoffPayload{
		AgentID: agentID, RuntimeID: runtimeID, HandoffID: handoffID, Count: len(messages), Targets: targets,
	})
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

func sha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
