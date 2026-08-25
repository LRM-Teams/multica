package daemon

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestActivityProducerSendsDisplayReadyPresentation(t *testing.T) {
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("instance-1", time.Now, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	if err := producer.SetManaged("agent-instance-1",
		protocol.AgentStatusPayload{AgentID: "agent-1", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{
		AgentID: "agent-1", AgentInstanceID: "agent-instance-1",
		Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "bash", ToolInput: map[string]any{"command": "pnpm test"}},
		At:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Snapshot.ActivityKind != protocol.ActivityKindWorking || sent[0].Summary.Label != "Running command..." {
		t.Fatalf("Activity presentation = %+v", sent)
	}
	if len(sent[0].Timeline) != 1 || sent[0].Timeline[0].Title != "Running command" {
		t.Fatalf("Activity timeline = %+v", sent[0].Timeline)
	}
}

func installActivityProducerAgent(t *testing.T, producer *agentActivityProducer) {
	t.Helper()
	if err := producer.SetManaged("instance-a",
		protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-a", RuntimeGeneration: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestStaleResidentMessageCallbackCannotChangeReplacementSession(t *testing.T) {
	sessions := map[string]string{"agent-a/runtime-1": "replacement-session"}
	runner := &WorkspaceDaemon{
		processes: newAgentProcessManager(time.Now, nil),
		recordProviderSession: func(agentID, runtimeID, sessionID string) {
			key := agentID + "/" + runtimeID
			if sessionID == "" {
				delete(sessions, key)
			} else {
				sessions[key] = sessionID
			}
		},
	}
	first := startTestManagedAgent(t, runner, "agent-a", "runtime-1", "first")
	markTestLaunchRunning(t, runner, "agent-a")
	old, _ := runner.processes.Snapshot("agent-a")
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID}); err != nil {
		t.Fatal(err)
	}
	startTestManagedAgent(t, runner, "agent-a", "runtime-1", "replacement")
	markTestLaunchRunning(t, runner, "agent-a")

	runner.observeResidentMessageRuntimeForProcess(agentProcessCallback{
		AgentID: "agent-a", AgentInstanceID: old.AgentInstanceID, ProcessInstanceID: old.ProcessInstanceID,
	}, "runtime-1", agent.Message{Type: agent.MessageError, SessionID: "old-session", Content: "Unknown parameter: 'input[86].status'"})
	if got := sessions["agent-a/runtime-1"]; got != "replacement-session" {
		t.Fatalf("stale callback changed replacement provider session to %q", got)
	}
}
