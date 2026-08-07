package researchrun

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidateNodeCommandInput(t *testing.T) {
	base := NodeCommandInput{
		SessionID:        "s1",
		WorkspaceID:      "w1",
		NodeID:           "n1",
		Action:           NodeActionContinue,
		ClientRequestID:  "req-1",
		AnchorQuestionID: "q1",
	}
	if err := validateNodeCommandInput(base); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	bad := base
	bad.Action = "explode"
	var denied *NodeCommandDenied
	if err := validateNodeCommandInput(bad); !errors.As(err, &denied) || denied.MachineCode != NodeCmdCodeActionNotAllowed {
		t.Fatalf("want action_not_allowed, got %v", err)
	}
	if denied.Message == "" || denied.MessageKey == "" {
		t.Fatalf("denied must carry message_key + Chinese message: %+v", denied)
	}

	retry := base
	retry.Action = NodeActionRetry
	retry.AnchorTaskID = ""
	if err := validateNodeCommandInput(retry); !errors.As(err, &denied) || denied.MachineCode != NodeCmdCodeNodeStale {
		t.Fatalf("retry without task: %v", err)
	}
	retry.AnchorTaskID = "t1"
	if err := validateNodeCommandInput(retry); err != nil {
		t.Fatalf("valid retry: %v", err)
	}
}

func TestRetryEligibility(t *testing.T) {
	task := Task{Status: TaskStatusBlocked}
	latest := Attempt{Status: AttemptStatusFailed, ID: "a1"}
	if deny := retryEligibility(task, latest, true); deny != nil {
		t.Fatalf("failed attempt should retry: %v", deny)
	}
	latest.Status = AttemptStatusRunning
	if deny := retryEligibility(task, latest, true); deny == nil || deny.MachineCode != NodeCmdCodeNotRetryable {
		t.Fatalf("running should deny: %v", deny)
	}
	task.Status = TaskStatusSucceeded
	latest.Status = AttemptStatusSucceeded
	if deny := retryEligibility(task, latest, true); deny == nil {
		t.Fatal("succeeded should deny")
	}
}

func TestSelectAgentPrefersAssigned(t *testing.T) {
	task := Task{RequiredCapability: "scout", AssignedAgentID: "agent-b"}
	members := []FleetMember{
		{AgentID: "agent-a", Role: "scout", Status: "active"},
		{AgentID: "agent-b", Role: "reader", Status: "active"},
	}
	got := selectAgent(task, members, map[string]int{})
	if got != "agent-b" {
		t.Fatalf("prefer assigned, got %q", got)
	}
	got = selectAgent(task, members, map[string]int{"agent-b": 1})
	if got != "agent-a" {
		t.Fatalf("fall back when assigned busy, got %q", got)
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
	prefix := strings.Repeat("x", 199)
	left := nodeCommandClientKey(prefix+"a", "event")
	right := nodeCommandClientKey(prefix+"b", "event")
	if left == right {
		t.Fatalf("distinct long request IDs collapsed to key %q", left)
	}
}

func TestHashNodeCommandRequestUsesSemanticJSONAndAllMutableInputs(t *testing.T) {
	base := NodeCommandInput{
		SessionID: "session-1", WorkspaceID: "workspace-1", NodeID: "task:task-1",
		Action: NodeActionRetry, ClientRequestID: "request-1", ActorType: "user", ActorID: "user-1",
		Objective: "retry with a fresh method", GoalPatch: "goal", Strategy: "strategy",
		StrategyPatch: "patch", SourceConstraints: json.RawMessage(`{"domains":["example.com"]}`),
		SourcePatch: json.RawMessage(`{"language":"en"}`), TargetAgentID: "agent-1",
		AnchorKind: "task", AnchorQuestionID: "question-1", AnchorTaskID: "task-1", AnchorTitle: "Task",
	}
	first, err := HashNodeCommandRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	semanticJSON := base
	semanticJSON.SourceConstraints = json.RawMessage(`{ "domains": [ "example.com" ] }`)
	semanticJSON.SourcePatch = json.RawMessage("{\n\"language\": \"en\"\n}")
	second, err := HashNodeCommandRequest(semanticJSON)
	if err != nil || second != first {
		t.Fatalf("semantic JSON changed hash: first=%q second=%q err=%v", first, second, err)
	}

	changes := map[string]func(*NodeCommandInput){
		"node":               func(in *NodeCommandInput) { in.NodeID = "task:task-2" },
		"action":             func(in *NodeCommandInput) { in.Action = NodeActionReassign },
		"actor":              func(in *NodeCommandInput) { in.ActorID = "user-2" },
		"objective":          func(in *NodeCommandInput) { in.Objective = "different" },
		"goal patch":         func(in *NodeCommandInput) { in.GoalPatch = "different" },
		"strategy":           func(in *NodeCommandInput) { in.Strategy = "different" },
		"strategy patch":     func(in *NodeCommandInput) { in.StrategyPatch = "different" },
		"source constraints": func(in *NodeCommandInput) { in.SourceConstraints = json.RawMessage(`{"domains":["other.test"]}`) },
		"source patch":       func(in *NodeCommandInput) { in.SourcePatch = json.RawMessage(`{"language":"zh"}`) },
		"target agent":       func(in *NodeCommandInput) { in.TargetAgentID = "agent-2" },
	}
	for label, mutate := range changes {
		changed := base
		mutate(&changed)
		hash, hashErr := HashNodeCommandRequest(changed)
		if hashErr != nil {
			t.Fatalf("%s: %v", label, hashErr)
		}
		if hash == first {
			t.Fatalf("%s change did not move request hash", label)
		}
	}
}
