package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentActivityProducerRetainsOnlyLatestSnapshotWhileDisconnected(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
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
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
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
	producer := newAgentActivityProducer(func() time.Time { return now }, nil)
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
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
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
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.agentActivityProducers["workspace-1"] = producer
	d.messageRuntimeIDs["agent-1"] = "runtime-1"
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	var frames []string
	d.attachWorkspaceRunnerMessageTransport("workspace-1", func(eventType string, _ any) error {
		frames = append(frames, eventType)
		return nil
	})
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
	producer := newAgentActivityProducer(time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	d.agentActivityProducers["workspace-1"] = producer
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	for _, message := range []agent.Message{
		{Type: agent.MessageThinking},
		{Type: agent.MessageToolUse, Tool: "exec_command"},
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
	if errorBody.Text != "Runtime error" || errorBody.ActivityKind != protocol.ActivityKindError || errorBody.DetailKind != "runtime_error" {
		t.Fatalf("runtime error Activity = %+v", errorBody)
	}
}

func TestTaskRunnerActivityIsSanitizedBeforePublishing(t *testing.T) {
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer(time.Now, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.agentActivityProducers["workspace-1"] = producer
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
	producer := newAgentActivityProducer(time.Now, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.agentActivityProducers["workspace-1"] = producer
	d.runnerInstanceID = "daemon-1"

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
