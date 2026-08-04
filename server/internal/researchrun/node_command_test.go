package researchrun

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestValidateNodeCommandInput(t *testing.T) {
	base := NodeCommandInput{
		SessionID:       "s1",
		WorkspaceID:     "w1",
		NodeID:          "n1",
		Action:          NodeActionContinue,
		ClientRequestID: "req-1",
		AnchorQuestionID: "q1",
	}
	if err := validateNodeCommandInput(base); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	bad := base
	bad.Action = "retry"
	var denied *NodeCommandDenied
	if err := validateNodeCommandInput(bad); !errors.As(err, &denied) || denied.MachineCode != NodeCmdCodeActionNotAllowed {
		t.Fatalf("want action_not_allowed, got %v", err)
	}
	if denied.Message == "" || denied.MessageKey == "" {
		t.Fatalf("denied must carry message_key + Chinese message: %+v", denied)
	}
}

func TestDenyNodeCommandShape(t *testing.T) {
	d := DenyNodeCommand(NodeCmdCodeStateVersionConflict, "画布已更新，请刷新后重试")
	if d.MachineCode != NodeCmdCodeStateVersionConflict || d.MessageKey != d.MachineCode {
		t.Fatalf("unexpected codes: %+v", d)
	}
	if d.HTTPStatus != 0 && d.HTTPStatus != http.StatusConflict {
		// default 409 via writer; constructor leaves 409
	}
	if d.Error() != "画布已更新，请刷新后重试" {
		t.Fatalf("Error() = %q", d.Error())
	}
}

func TestDecodeNodeCommandOutcomeRoundTrip(t *testing.T) {
	task := Task{ID: "t1", Objective: "续研定价", Status: TaskStatusReady}
	outcome := NodeCommandOutcome{
		Action:          NodeActionContinue,
		ClientRequestID: "req-2",
		Task:            &task,
		ParentLineage:   ParentLineage{ParentQuestionID: "q1", SourceNodeID: "n1"},
		Queued:          true,
	}
	payload, err := json.Marshal(map[string]any{"command": outcome})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeNodeCommandOutcome(RunEvent{ID: "evt-1", Sequence: 9, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Queued || got.CommandID != "evt-1" || got.StateVersion != 9 {
		t.Fatalf("unexpected replay decode: %+v", got)
	}
	if got.Task == nil || got.Task.ID != "t1" {
		t.Fatalf("task missing: %+v", got.Task)
	}
}

func TestNodeCommandClientKeyBounded(t *testing.T) {
	key := nodeCommandClientKey(string(make([]byte, 300)), "task")
	if len(key) > maxClientKeyBytes {
		t.Fatalf("key len %d > %d", len(key), maxClientKeyBytes)
	}
}
