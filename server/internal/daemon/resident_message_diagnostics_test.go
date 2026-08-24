package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type residentDiagnosticCapture struct {
	event diagnosticlog.Event
}

func (capture *residentDiagnosticCapture) record(_ string, event diagnosticlog.Event) error {
	capture.event = event
	return nil
}

func TestResidentMessageDiagnosticTurnCarriesExecutionAndRuntimeEpoch(t *testing.T) {
	message := protocol.AgentMessageProjection{ID: "message-1", ChannelID: "channel-1", Seq: 7}
	event := canonicalMessageDiagnosticEventForTurn("workspace-1", "runtime-1", protocol.AgentDeliverPayload{
		AgentID:    "agent-1",
		Message:    message,
		DeliveryID: "delivery-1",
	}, "execution_started", "accepted", "", "resident-execution-1", 3)
	if event.Identity.ExecutionID != "resident-execution-1" {
		t.Fatalf("execution id = %q", event.Identity.ExecutionID)
	}
	if event.Fields.RuntimeEpoch != 3 {
		t.Fatalf("runtime epoch = %d, want 3", event.Fields.RuntimeEpoch)
	}
	if event.Fields.Phase != "execution_started" || event.Fields.Outcome != "accepted" {
		t.Fatalf("phase/outcome = %q/%q", event.Fields.Phase, event.Fields.Outcome)
	}
}

func TestResidentMessageDiagnosticCarriesLaunchJoinFields(t *testing.T) {
	capture := &residentDiagnosticCapture{}
	d := &WorkspaceDaemonCore{runnerDiagnostics: capture}
	d.recordResidentMessageBatch("workspace-1", "runtime-1", "agent-1", []protocol.AgentMessageProjection{{ID: "message-1"}}, "execution_started", "accepted", "", "execution-1", 4, "launch-1", "dispatch-1")
	if capture.event.Identity.LaunchID != "launch-1" || capture.event.Identity.StartDispatchID != "dispatch-1" {
		t.Fatalf("launch join fields = %+v", capture.event.Identity)
	}
}

func TestInboxLeaseEpochRemainsBoundAfterReregister(t *testing.T) {
	d := &WorkspaceDaemonCore{
		runtimeIndex: map[string]Runtime{"runtime-1": {ID: "runtime-1", WorkspaceID: "workspace-1"}},
		workspaces:   map[string]*workspaceState{"workspace-1": {workspaceID: "workspace-1", runtimeEpoch: 2}},
	}
	lease := &AgentInboxLease{RuntimeID: "runtime-1"}
	d.bindInboxLeaseEpoch(lease)
	d.workspaces["workspace-1"].runtimeEpoch = 3
	if lease.RuntimeEpoch != 2 {
		t.Fatalf("lease epoch = %d, want bound epoch 2", lease.RuntimeEpoch)
	}
}
