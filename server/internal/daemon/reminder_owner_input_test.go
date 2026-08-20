package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type reminderOwnerInputFakeRuntime struct {
	mu     sync.Mutex
	inputs []agent.ResidentReminderInput
	err    error
}

func (r *reminderOwnerInputFakeRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *reminderOwnerInputFakeRuntime) AcceptReminderInput(_ context.Context, input agent.ResidentReminderInput) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return agent.ResidentMessageAcceptance{}, err
	}
	done := make(chan error)
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func (r *reminderOwnerInputFakeRuntime) snapshot() []agent.ResidentReminderInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.ResidentReminderInput(nil), r.inputs...)
}

func validReminderOwnerInputPayload() protocol.ReminderOwnerInputPayload {
	return protocol.ReminderOwnerInputPayload{
		WorkspaceID: "workspace-a",
		AgentID:     "agent-a",
		RuntimeID:   "runtime-a",
		ReminderID:  "reminder-a",
		Version:     4,
		Title:       "Review the deployment",
		Anchor: protocol.ReminderOwnerInputAnchor{
			Available:   true,
			ChannelID:   "channel-a",
			MessageID:   "message-a",
			Target:      "channel:channel-a",
			ReplyTarget: "#general",
			Excerpt:     "Please review after deploy.",
		},
		Occurrence: protocol.ReminderOwnerInputOccurrence{
			OccurrenceID: "occurrence-a",
			ScheduledFor: "2026-08-11T07:00:00Z",
			DueAt:        "2026-08-11T07:00:00Z",
			Cadence:      "every:1h",
			Timezone:     "Asia/Shanghai",
		},
	}
}

func newReminderOwnerInputDaemon(t *testing.T, runtime *reminderOwnerInputFakeRuntime, capable bool) *Daemon {
	t.Helper()
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	capabilities := []string(nil)
	if capable {
		capabilities = append(capabilities, protocol.DaemonCapabilityReminderTransientInput)
	}
	d.mu.Lock()
	d.runtimeIndex["runtime-a"] = Runtime{ID: "runtime-a", WorkspaceID: "workspace-a"}
	d.workspaces["workspace-a"] = newWorkspaceState("workspace-a", []string{"runtime-a"}, capabilities...)
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-a", nil)
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "launch-a", StartDispatchID: "dispatch-a"}); err != nil {
		t.Fatalf("seed Reminder owner start: %v", err)
	}
	d.canonicalRuntimes.slots["agent-a\x00runtime-a"] = &canonicalAgentRuntimeSlot{
		backend: runtime,
	}
	return d
}

func TestReminderOwnerInputIdleInjectsExactlyOnce(t *testing.T) {
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, true)
	payload := validReminderOwnerInputPayload()

	if outcome := d.handleReminderOwnerInput(context.Background(), payload); outcome != reminderOwnerInputAccepted {
		t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputAccepted)
	}
	inputs := runtime.snapshot()
	if len(inputs) != 1 {
		t.Fatalf("inputs=%d want=1", len(inputs))
	}
	if inputs[0].ReminderID != payload.ReminderID || inputs[0].Version != payload.Version || inputs[0].Anchor.ReplyTarget != payload.Anchor.ReplyTarget || inputs[0].Occurrence.OccurrenceID != payload.Occurrence.OccurrenceID {
		t.Fatalf("concrete Reminder input=%+v", inputs[0])
	}
	if runner := d.currentWorkspaceRunner(payload.WorkspaceID); runner != nil {
		if _, _, found := runner.inboxes.Resolve(payload.AgentID); found {
			t.Fatal("accepted Reminder created a MessageCoordinator")
		}
	}
}

func TestReminderOwnerInputBusyIsAcceptedDiscardWithoutReplay(t *testing.T) {
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, true)
	slot := d.canonicalRuntimes.slots["agent-a\x00runtime-a"]
	slot.running = true

	if outcome := d.handleReminderOwnerInput(context.Background(), validReminderOwnerInputPayload()); outcome != reminderOwnerInputDiscardedBusy {
		t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputDiscardedBusy)
	}
	if got := len(runtime.snapshot()); got != 0 {
		t.Fatalf("busy input reached runtime: %d", got)
	}
	slot.running = false
	if got := len(runtime.snapshot()); got != 0 {
		t.Fatalf("busy input replayed at idle boundary: %d", got)
	}
	if runner := d.currentWorkspaceRunner("workspace-a"); runner != nil {
		if _, _, found := runner.inboxes.Resolve("agent-a"); found {
			t.Fatal("busy Reminder created a MessageCoordinator")
		}
	}
}

func TestReminderOwnerInputInjectionFailureIsFinal(t *testing.T) {
	runtime := &reminderOwnerInputFakeRuntime{err: errors.New("native write failed")}
	d := newReminderOwnerInputDaemon(t, runtime, true)

	if outcome := d.handleReminderOwnerInput(context.Background(), validReminderOwnerInputPayload()); outcome != reminderOwnerInputInjectionFailed {
		t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputInjectionFailed)
	}
	if got := len(runtime.snapshot()); got != 1 {
		t.Fatalf("injection attempts=%d want=1", got)
	}
	if runner := d.currentWorkspaceRunner("workspace-a"); runner != nil {
		if _, _, found := runner.inboxes.Resolve("agent-a"); found {
			t.Fatal("failed Reminder created a MessageCoordinator")
		}
	}
}

func TestReminderOwnerInputRejectsUnauthorizedStaleAndOversizedPayloads(t *testing.T) {
	tests := map[string]func(*protocol.ReminderOwnerInputPayload){
		"cross owner":     func(p *protocol.ReminderOwnerInputPayload) { p.AgentID = "agent-b" },
		"cross workspace": func(p *protocol.ReminderOwnerInputPayload) { p.WorkspaceID = "workspace-b" },
		"oversized title": func(p *protocol.ReminderOwnerInputPayload) { p.Title = strings.Repeat("x", 501) },
		"oversized excerpt": func(p *protocol.ReminderOwnerInputPayload) {
			p.Anchor.Excerpt = strings.Repeat("x", reminderOwnerInputMaxExcerptBytes+1)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			runtime := &reminderOwnerInputFakeRuntime{}
			d := newReminderOwnerInputDaemon(t, runtime, true)
			payload := validReminderOwnerInputPayload()
			mutate(&payload)
			if outcome := d.handleReminderOwnerInput(context.Background(), payload); outcome != reminderOwnerInputRejected {
				t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputRejected)
			}
			if got := len(runtime.snapshot()); got != 0 {
				t.Fatalf("rejected input reached runtime: %d", got)
			}
		})
	}
}

func TestReminderOwnerInputMismatchLogsAtDebug(t *testing.T) {
	var buf bytes.Buffer
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, true)
	d.logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	payload := validReminderOwnerInputPayload()
	payload.AgentID = "agent-b"

	if outcome := d.handleReminderOwnerInput(context.Background(), payload); outcome != reminderOwnerInputRejected {
		t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputRejected)
	}
	var record struct {
		Level  string `json:"level"`
		Msg    string `json:"msg"`
		Reason string `json:"reason_code"`
	}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode reminder owner input log: %v\n%s", err, buf.String())
	}
	if record.Level != "DEBUG" || record.Msg != "transient Reminder owner input" || record.Reason != "agent_start_mismatch" {
		t.Fatalf("log=%+v want debug agent_start_mismatch", record)
	}
	if strings.Contains(buf.String(), "start_identity") {
		t.Fatalf("reminder owner input log leaked start_identity: %s", buf.String())
	}
}

func TestReminderOwnerInputRequiresNegotiatedCapability(t *testing.T) {
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, false)
	if outcome := d.handleReminderOwnerInput(context.Background(), validReminderOwnerInputPayload()); outcome != reminderOwnerInputRejected {
		t.Fatalf("outcome=%q want=%q", outcome, reminderOwnerInputRejected)
	}
	if got := len(runtime.snapshot()); got != 0 {
		t.Fatalf("capability-gated input reached runtime: %d", got)
	}
}
