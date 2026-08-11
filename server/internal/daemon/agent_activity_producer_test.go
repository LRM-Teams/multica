package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentActivityProducerObserveGoldenMappings(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	runtime := AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 3}
	tool := runtime
	tool.ToolName = "exec_command"
	tool.ToolCallID = "call-1"
	tests := []struct {
		name        string
		observation AgentObservation
		kind        string
		detail      string
		entryKind   string
		entryText   string
		processID   string
	}{
		{name: "attached", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationAttached, Data: AgentAttachmentObservationData{RuntimeID: "runtime-1", AttachmentGeneration: 1}, At: at}, kind: protocol.ActivityKindOnline, detail: "attached", entryKind: "narrative", entryText: "Agent attached"},
		{name: "launch accepted", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationLaunchAccepted, Data: AgentLaunchObservationData{RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"}, At: at}, kind: protocol.ActivityKindOnline, detail: "launch_accepted", entryKind: "narrative", entryText: "Launch accepted"},
		{name: "runtime ready", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeReady, Data: runtime, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "narrative", entryText: "Online", processID: "process-1"},
		{name: "runtime working", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeWorking, Data: runtime, At: at}, kind: protocol.ActivityKindWorking, entryKind: "narrative", entryText: "Working", processID: "process-1"},
		{name: "runtime thinking", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeThinking, Data: runtime, At: at}, kind: protocol.ActivityKindThinking, entryKind: "narrative", entryText: "Thinking", processID: "process-1"},
		{name: "runtime tool", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeTool, Data: tool, At: at}, kind: protocol.ActivityKindWorking, detail: "running_command", entryKind: "narrative", entryText: "Running tool", processID: "process-1"},
		{name: "message accepted", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1", HandoffID: "handoff-1", MessageCount: 2}, At: at}, kind: protocol.ActivityKindWorking, detail: "message_received", entryKind: "narrative", entryText: "Message received"},
		{name: "freshness held", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", NewMessageCount: 2, ReasonCode: "local_pending"}, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "2 newer messages available — review then resend"},
		{name: "error", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationError, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ReasonCode: "provider_failed"}, At: at}, kind: protocol.ActivityKindError, detail: "runtime_error", entryKind: "narrative", entryText: "Agent execution failed", processID: "process-1"},
		{name: "stopped", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationStopped, Data: AgentStopObservationData{RuntimeID: "runtime-1", ReasonCode: "requested"}, At: at}, kind: protocol.ActivityKindOffline, detail: "stopped", entryKind: "narrative", entryText: "Stopped"},
		{name: "detached", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationDetached, Data: AgentAttachmentObservationData{RuntimeID: "runtime-1", AttachmentGeneration: 1}, At: at}, kind: protocol.ActivityKindOffline, detail: "detached", entryKind: "narrative", entryText: "Agent detached"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sent []protocol.AgentActivityPayload
			producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
			installActivityProducerAgent(t, producer)
			if err := producer.Observe(test.observation); err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if len(sent) != 1 {
				t.Fatalf("sent = %d, want 1", len(sent))
			}
			payload := sent[0]
			if payload.Snapshot.ActivityKind != test.kind || payload.Snapshot.DetailKind != test.detail || payload.Snapshot.ProcessInstanceID != test.processID || !payload.Snapshot.ObservedAt.Equal(at) {
				t.Fatalf("Snapshot = %+v", payload.Snapshot)
			}
			if len(payload.Entries) != 1 || payload.Entries[0].Kind != test.entryKind {
				t.Fatalf("Entries = %+v", payload.Entries)
			}
			var body struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(payload.Entries[0].Body, &body); err != nil || body.Text != test.entryText {
				t.Fatalf("entry body = %+v err=%v", body, err)
			}
		})
	}
}

func TestAgentActivityProducerObserveUsesDeterministicFactIdentity(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	observation := AgentObservation{
		AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeThinking,
		Data: AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 1}, At: at,
	}
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0].Snapshot.ProducerFactID == "" || sent[0].Snapshot.ProducerFactID != sent[1].Snapshot.ProducerFactID || sent[0].Snapshot.ClientSequence != 1 || sent[1].Snapshot.ClientSequence != 2 {
		t.Fatalf("observed payloads = %+v", sent)
	}
}

func TestAgentActivityProducerObserveRejectsMissingOrStaleLaunch(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	observation := AgentObservation{
		AgentID: "agent-a", LaunchID: "launch-stale", Kind: AgentObservationRuntimeWorking,
		Data: AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 1}, At: at,
	}
	if err := producer.Observe(observation); err == nil {
		t.Fatal("Observe accepted a stale launch")
	}
	if len(sent) != 0 || len(producer.states) != 1 {
		t.Fatalf("stale observation mutated producer: sent=%d states=%+v", len(sent), producer.states)
	}
	unmanaged := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	attached := AgentObservation{AgentID: "agent-unmanaged", Kind: AgentObservationAttached, Data: AgentAttachmentObservationData{RuntimeID: "runtime-1", AttachmentGeneration: 1}, At: at}
	if err := unmanaged.Observe(attached); err == nil {
		t.Fatal("Observe created a synthetic launch for an Attachment")
	}
	if len(unmanaged.states) != 0 {
		t.Fatalf("Attachment observation created managed state: %+v", unmanaged.states)
	}
}

func TestAgentActivityProducerObserveKeepsSessionAndProcessIdentitiesDistinct(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, nil)
	installActivityProducerAgent(t, producer)
	observation := AgentObservation{
		AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeWorking, At: at,
		Data: AgentRuntimeObservationData{
			RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ProviderSessionID: "session-1", TurnID: "turn-1", RuntimeGeneration: 4,
		},
	}
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	state := producer.states[agentActivityProducerKey{agentID: "agent-a", launchID: "launch-a"}]
	if state.snapshot.ProcessInstanceID != "process-1" || state.session.ProviderSessionID != "session-1" || state.session.TurnID != "turn-1" || state.session.RuntimeGeneration != 4 {
		t.Fatalf("managed identities = snapshot:%+v session:%+v", state.snapshot, state.session)
	}
}

func TestAgentActivityProducerRetainsOnlyLatestSnapshotWhileDisconnected(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	producer.newID = sequentialActivityFactIDs()
	installActivityProducerAgent(t, producer)
	producer.SetConnected("agent-a", "launch-a", false)
	if err := producer.Publish(activitySnapshot("daemon-1", "working", 1, "fact-1", now), []protocol.AgentActivityEntry{{Kind: "narrative", Position: 0, Body: []byte(`{"text":"lost"}`)}}); err != nil {
		t.Fatalf("Publish(first): %v", err)
	}
	now = now.Add(time.Second)
	if err := producer.Publish(activitySnapshot("daemon-1", "thinking", 2, "fact-2", now), []protocol.AgentActivityEntry{{Kind: "narrative", Position: 0, Body: []byte(`{"text":"also-lost"}`)}}); err != nil {
		t.Fatalf("Publish(second): %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("disconnected producer sent %d payloads", len(sent))
	}
	frames := producer.ReconnectFrames()
	if len(frames) != 3 {
		t.Fatalf("reconnect frames = %d, want status/session/latest Snapshot", len(frames))
	}
	payload, ok := frames[2].Payload.(protocol.AgentActivityPayload)
	if !ok || payload.Snapshot.ActivityKind != protocol.ActivityKindThinking || len(payload.Entries) != 0 {
		t.Fatalf("replayed payload = %+v", frames[2].Payload)
	}
}

func TestAgentActivityProducerHeartbeatsAndProbeDoNotInventState(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	producer.newID = sequentialActivityFactIDs()
	installActivityProducerAgent(t, producer)
	if err := producer.Publish(activitySnapshot("daemon-1", "working", 1, "fact-1", now), nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	producer.Tick()
	if len(sent) != 1 {
		t.Fatalf("heartbeat before 60 seconds sent %d payloads", len(sent))
	}
	now = now.Add(time.Second)
	producer.Tick()
	if len(sent) != 2 || sent[1].Snapshot.ClientSequence != 2 || len(sent[1].Entries) != 0 {
		t.Fatalf("heartbeat payload = %+v", sent)
	}
	probe, err := producer.Probe(protocol.AgentActivityProbePayload{AgentID: "agent-a", LaunchID: "launch-a", ProbeID: "probe-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if probe.Snapshot.ProbeID != "probe-1" || probe.Snapshot.ActivityKind != protocol.ActivityKindWorking || probe.Snapshot.ClientSequence != 2 {
		t.Fatalf("probe = %+v", probe.Snapshot)
	}
	state, ok := producer.states[agentActivityProducerKey{agentID: "agent-a", launchID: "launch-a"}]
	if !ok || state.snapshot.ProbeID != "" || state.lastClientSequence != 2 {
		t.Fatalf("probe mutated producer state: %+v", state)
	}
}

func TestAgentActivityProducerReplacedTransportKeepsNewestRunnerConnected(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, nil)
	installActivityProducerAgent(t, producer)
	var first, second []protocol.AgentActivityPayload
	firstGeneration, _ := producer.AttachTransport(func(payload protocol.AgentActivityPayload) { first = append(first, payload) })
	_, _ = producer.AttachTransport(func(payload protocol.AgentActivityPayload) { second = append(second, payload) })
	producer.DetachTransport(firstGeneration)
	if err := producer.Publish(activitySnapshot("daemon-1", protocol.ActivityKindWorking, 1, "fact-1", now), nil); err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 || len(second) != 1 {
		t.Fatalf("replaced transport delivery first=%d second=%d", len(first), len(second))
	}
}

func TestAgentActivityProducerPublishesManagedMessageHandoff(t *testing.T) {
	now := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	if err := producer.PublishForManagedAgent("agent-a", "daemon-1", protocol.ActivityKindWorking, "message_received", []protocol.AgentActivityEntry{{Kind: "narrative", Position: 0, Body: []byte(`{"text":"Message received"}`)}}); err != nil {
		t.Fatalf("PublishForManagedAgent: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	got := sent[0]
	if got.Snapshot.AgentID != "agent-a" || got.Snapshot.LaunchID != "launch-a" || got.Snapshot.DaemonInstanceID != "daemon-1" || got.Snapshot.ActivityKind != protocol.ActivityKindWorking || got.Snapshot.DetailKind != "message_received" || got.Snapshot.ClientSequence != 1 {
		t.Fatalf("managed Message Activity = %+v", got.Snapshot)
	}
}

func TestMessageHandoffEstablishesResidentManagedLaunchBeforeActivity(t *testing.T) {
	now := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-instance-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	var frames []string
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", func(eventType string, _ any) error {
		frames = append(frames, eventType)
		return nil
	})
	runner.activity = producer
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.emitMessageReceivedActivity("agent-1", "runtime-1", []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:agent-1", Seq: 1,
	}})

	wantFrames := []string{protocol.EventAgentStatus, protocol.EventAgentSession, protocol.EventAgentMessageHandoff}
	if len(frames) != len(wantFrames) {
		t.Fatalf("Runner frames=%v, want %v", frames, wantFrames)
	}
	for i := range wantFrames {
		if frames[i] != wantFrames[i] {
			t.Fatalf("Runner frames=%v, want %v", frames, wantFrames)
		}
	}
	if len(activities) != 1 || activities[0].Snapshot.ActivityKind != protocol.ActivityKindWorking || activities[0].Snapshot.LaunchID == "" {
		t.Fatalf("Message Activity=%+v", activities)
	}
}

func TestResidentRuntimeEventsPublishRaftActivityLifecycle(t *testing.T) {
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	installTestRunnerActivity(t, d, "workspace-1", producer)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	for _, message := range []agent.Message{
		{Type: agent.MessageThinking},
		{Type: agent.MessageToolUse, Tool: "exec_command", Input: map[string]any{"command": "ls -la"}},
		{Type: agent.MessageStatus, Status: "reconnecting"},
		{Type: agent.MessageDiagnostic, Title: "Codex config warning", Level: "warning", Diagnostic: "configWarning", Content: "User namespaces are unavailable"},
		{Type: agent.MessageError, Content: "sensitive provider text"},
	} {
		d.emitResidentMessageRuntimeActivity("agent-a", "runtime-1", message)
	}
	wantKinds := []string{protocol.ActivityKindThinking, protocol.ActivityKindWorking, protocol.ActivityKindWorking, protocol.ActivityKindError}
	wantDetails := []string{"", "running_command", "running_command", "runtime_error"}
	if len(activities) != len(wantKinds) {
		t.Fatalf("Activity count = %d, want %d", len(activities), len(wantKinds))
	}
	for index := range wantKinds {
		if activities[index].Snapshot.ActivityKind != wantKinds[index] || activities[index].Snapshot.DetailKind != wantDetails[index] {
			t.Fatalf("Activity[%d] = %+v", index, activities[index].Snapshot)
		}
	}
	var toolBody protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(activities[1].Entries[0].Body, &toolBody); err != nil {
		t.Fatal(err)
	}
	if toolBody.Text != "ls -la" || toolBody.ActivityKind != protocol.ActivityKindWorking || toolBody.DetailKind != "running_command" {
		t.Fatalf("tool-use Activity body = %+v, want the actual command as narrative text", toolBody)
	}
	var diagnostic protocol.AgentActivitySystemBody
	if err := json.Unmarshal(activities[2].Entries[0].Body, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if activities[2].Entries[0].Kind != "system" || diagnostic.Title != "Codex config warning" || diagnostic.Text != "User namespaces are unavailable" {
		t.Fatalf("runtime diagnostic Activity = kind:%q body:%+v", activities[2].Entries[0].Kind, diagnostic)
	}
	var errorBody protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(activities[len(activities)-1].Entries[0].Body, &errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody.Text != "sensitive provider text" || errorBody.ActivityKind != protocol.ActivityKindError || errorBody.DetailKind != "runtime_error" {
		t.Fatalf("runtime error Activity = %+v, want provider reason", errorBody)
	}
}

func TestIdleMessageAcceptanceFailurePublishesVisibleErrorActivity(t *testing.T) {
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	installTestRunnerActivity(t, d, "workspace-1", producer)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.canonicalRuntimes.slots["agent-a\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: failingResidentMessageRuntime{},
	}

	err := d.handoffIdleMessageBatch(context.Background(), "agent-a", "runtime-1", []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:agent-a", Seq: 1, Content: "hello",
	}})
	if err == nil || !strings.Contains(err.Error(), "runtime Message handoff unavailable") {
		t.Fatalf("handoff error = %v, want provider acceptance failure", err)
	}
	if len(activities) != 1 {
		t.Fatalf("activities = %d, want one visible Error", len(activities))
	}
	got := activities[0]
	if got.Snapshot.ActivityKind != protocol.ActivityKindError || got.Snapshot.DetailKind != "runtime_error" || len(got.Entries) != 1 {
		t.Fatalf("failure Activity = %+v", got)
	}
	var body protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(got.Entries[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != "runtime Message handoff unavailable (simulated crash window)" {
		t.Fatalf("failure narrative = %q", body.Text)
	}
}

func TestTaskRunnerActivityIsSanitizedBeforePublishing(t *testing.T) {
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	installTestRunnerActivity(t, d, "workspace-1", producer)
	d.publishTaskRunnerActivity(Task{ID: "task-1", AgentID: "agent-a", WorkspaceID: "workspace-1"}, protocol.ActivityKindWorking, "running_command", "Running command")
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	got := sent[0]
	if got.Snapshot.ActivityKind != protocol.ActivityKindWorking || got.Snapshot.DetailKind != "running_command" || len(got.Entries) != 1 {
		t.Fatalf("task Activity = %+v", got)
	}
	var body protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(got.Entries[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != "Running command" || body.ActivityKind != protocol.ActivityKindWorking || body.DetailKind != "running_command" {
		t.Fatalf("task Activity body = %+v", body)
	}
}

func TestTaskFailurePublishesRunnerErrorWithoutRawFailureText(t *testing.T) {
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	installTestRunnerActivity(t, d, "workspace-1", producer)

	d.reportTaskFailure(context.Background(), Task{ID: "task-1", AgentID: "agent-a", WorkspaceID: "workspace-1"}, "sensitive provider failure", "", "", "provider_error", slog.Default())

	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	got := sent[0]
	if got.Snapshot.ActivityKind != protocol.ActivityKindError || got.Snapshot.DetailKind != "task_failed" || len(got.Entries) != 1 {
		t.Fatalf("failure Activity = %+v", got)
	}
	var body protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(got.Entries[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != "Agent execution failed" || body.ActivityKind != protocol.ActivityKindError || body.DetailKind != "task_failed" {
		t.Fatalf("failure Activity body = %+v", body)
	}
}

func installActivityProducerAgent(t *testing.T, producer *agentActivityProducer) {
	t.Helper()
	if err := producer.SetManaged(protocol.AgentStatusPayload{AgentID: "agent-a", LaunchID: "launch-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a", LaunchID: "launch-a", RuntimeGeneration: 1}); err != nil {
		t.Fatalf("SetManaged: %v", err)
	}
}

func activitySnapshot(daemonID, kind string, sequence int64, factID string, observedAt time.Time) protocol.AgentActivitySnapshot {
	return protocol.AgentActivitySnapshot{AgentID: "agent-a", LaunchID: "launch-a", DaemonInstanceID: daemonID, ClientSequence: sequence, ProducerFactID: factID, ObservedAt: observedAt, ActivityKind: kind}
}

func sequentialActivityFactIDs() func() string {
	ids := []string{"heartbeat-1", "heartbeat-2", "heartbeat-3"}
	index := 0
	return func() string {
		id := ids[index]
		index++
		return id
	}
}

// TestActivityProducerPublishHoldEntryForUnmanagedAgentPushesFragment covers the
// core fix: a soft-held send is projected onto the Activity timeline even when
// the Agent has no locally managed launch (Raft: ap undefined -> still report
// the fact). The fragment must not register managed state or perturb client-seq
// bookkeeping, and it is fail-soft when no transport is attached.
func TestActivityProducerPublishHoldEntryForUnmanagedAgentPushesFragment(t *testing.T) {
	now := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	// NOTE: no installActivityProducerAgent -> agent-a/launch-a is NOT managed.

	entry, err := activitySystemEntry("Message held", "1 newer message available")
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.PublishHoldEntry("agent-unmanaged", "daemon-1", []protocol.AgentActivityEntry{entry}); err != nil {
		t.Fatalf("PublishHoldEntry(unmanaged): %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1 unmanaged hold fragment", len(sent))
	}
	got := sent[0]
	if got.Snapshot.AgentID != "agent-unmanaged" || got.Snapshot.LaunchID == "" || got.Snapshot.DaemonInstanceID != "daemon-1" {
		t.Fatalf("unmanaged hold snapshot identity = %+v", got.Snapshot)
	}
	if got.Snapshot.ClientSequence != 1 || got.Snapshot.ProducerFactID == "" || got.Snapshot.ActivityKind != protocol.ActivityKindOnline {
		t.Fatalf("unmanaged hold fragment must be a standalone Online seq=1 fact, got %+v", got.Snapshot)
	}
	if len(got.Entries) != 1 || got.Entries[0].Kind != "system" {
		t.Fatalf("hold fragment entries = %+v", got.Entries)
	}

	// The fragment must not register managed state: the agent is still NOT
	// managed afterwards, so the managed-only entry path still rejects it.
	producerWithTransport := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	if err := producerWithTransport.PublishEntryForManagedAgent("agent-unmanaged", "daemon-1", []protocol.AgentActivityEntry{entry}); err == nil {
		t.Fatal("expected managed-only entry path to still reject an unmanaged agent (fragment must not touch states map)")
	}
}

// TestActivityProducerPublishHoldEntryForManagedAgentKeepsManagedStream ensures
// the managed main path is untouched: when the Agent has a managed launch, a hold
// entry reuses the managed identity and stays inside the managed client-sequence
// stream instead of taking the standalone fragment branch.
func TestActivityProducerPublishHoldEntryForManagedAgentKeepsManagedStream(t *testing.T) {
	now := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	producer.newID = sequentialActivityFactIDs()
	installActivityProducerAgent(t, producer)

	if err := producer.Publish(activitySnapshot("daemon-1", "working", 1, "fact-1", now), nil); err != nil {
		t.Fatal(err)
	}
	entry, err := activitySystemEntry("Message held", "2 newer messages available")
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.PublishHoldEntry("agent-a", "daemon-1", []protocol.AgentActivityEntry{entry}); err != nil {
		t.Fatalf("PublishHoldEntry(managed): %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent = %d, want base snapshot + managed hold entry", len(sent))
	}
	got := sent[1]
	if got.Snapshot.LaunchID != "launch-a" {
		t.Fatalf("managed hold must reuse launch-a, got %+v", got.Snapshot)
	}
	if got.Snapshot.ClientSequence != 2 {
		t.Fatalf("managed hold client sequence = %d, want 2 (advanced in managed stream)", got.Snapshot.ClientSequence)
	}
	if len(got.Entries) != 1 || got.Entries[0].Kind != "system" {
		t.Fatalf("managed hold entries = %+v", got.Entries)
	}
}
