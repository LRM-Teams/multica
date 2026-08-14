package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentActivityProducerObserveGoldenMappings(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	runtime := AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 3}
	stage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}
	tool := AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "exec_command", ToolCallID: "call-1", ToolInput: map[string]any{"command": "ls -la"}}
	tests := []struct {
		name        string
		observation AgentObservation
		kind        string
		detail      string
		entryKind   string
		entryText   string
		processID   string
	}{
		{name: "runtime ready", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeReady, Data: runtime, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "narrative", entryText: "Online", processID: "process-1"},
		{name: "runtime working", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeWorking, Data: runtime, At: at}, kind: protocol.ActivityKindWorking, detail: "model_response_started", entryKind: "narrative", entryText: "Working", processID: "process-1"},
		{name: "runtime thinking", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeThinking, Data: stage, At: at}, kind: protocol.ActivityKindThinking, detail: "thinking_started"},
		{name: "runtime tool", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeTool, Data: tool, At: at}, kind: protocol.ActivityKindWorking, detail: "running_command", entryKind: "narrative", entryText: "ls -la"},
		{name: "runtime compacting", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeCompacting, Data: stage, At: at}, kind: protocol.ActivityKindWorking, detail: "compacting_context", entryKind: "narrative", entryText: "Compacting context"},
		{name: "runtime compacted", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeCompacted, Data: stage, At: at}, kind: protocol.ActivityKindWorking, detail: "compaction_finished", entryKind: "narrative", entryText: "Context compaction finished"},
		{name: "runtime idle", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "narrative", entryText: "Idle"},
		{name: "runtime diagnostic", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeDiagnostic, Data: stage, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "Provider reported a warning"},
		{name: "message accepted", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1", HandoffID: "handoff-1", MessageCount: 2}, At: at}, kind: protocol.ActivityKindWorking, detail: "message_received", entryKind: "narrative", entryText: "Message received"},
		{name: "freshness held", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", NewMessageCount: 2, ReasonCode: "local_pending"}, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "2 newer messages available — review then resend"},
		{name: "draft sent", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationDraftSent, Data: AgentDraftSentObservationData{RuntimeID: "runtime-1", Target: "#one"}, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "target: #one\nfreshness updates: 0 newer messages\ndecision: saved draft freshness check passed when sent"},
		{name: "error", observation: AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationError, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ReasonCode: "provider_failed"}, At: at}, kind: protocol.ActivityKindError, detail: "runtime_error", entryKind: "narrative", entryText: "Agent execution failed", processID: "process-1"},
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
			if test.entryKind == "" {
				if len(payload.Entries) != 0 {
					t.Fatalf("Entries = %+v, want none", payload.Entries)
				}
				return
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

func TestAgentActivityProducerDropsUnknownToolNonFact(t *testing.T) {
	at := time.Date(2026, time.August, 14, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	installActivityProducerAgent(t, producer)
	err := producer.Observe(AgentObservation{
		AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "cursor-agent", ToolCallID: "call-1"},
		At:   at,
	})
	if err == nil || !strings.Contains(err.Error(), "non-fact activity detail kind") {
		t.Fatalf("unknown tool Observe error = %v, want non-fact drop", err)
	}
	if len(sent) != 0 {
		t.Fatalf("unknown tool sent %d Activity facts, want none", len(sent))
	}
}

func TestAgentActivityProducerObserveUsesDeterministicFactIdentity(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	observation := AgentObservation{
		AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeThinking,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: at,
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
	missingLaunch := AgentObservation{AgentID: "agent-unmanaged", Kind: AgentObservationRuntimeThinking, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: at}
	if err := unmanaged.Observe(missingLaunch); err == nil {
		t.Fatal("Observe accepted a Message/runtime fact without a launch")
	}
	if len(unmanaged.states) != 0 {
		t.Fatalf("launch-free observation created managed state: %+v", unmanaged.states)
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
	installActivityProducerAgent(t, producer)
	producer.SetConnected("agent-a", "launch-a", false)
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatalf("Observe(first): %v", err)
	}
	now = now.Add(time.Second)
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeThinking, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatalf("Observe(second): %v", err)
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
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	producer.Tick()
	if len(sent) != 1 {
		t.Fatalf("heartbeat before 60 seconds sent %d payloads", len(sent))
	}
	now = now.Add(time.Second)
	producer.Tick()
	if len(sent) != 2 || sent[1].Snapshot.ClientSequence != 2 || len(sent[1].Entries) != 0 || sent[1].Detail != "Starting…" || !sent[1].IsHeartbeat {
		t.Fatalf("heartbeat payload = %+v", sent)
	}
	probe, err := producer.Probe(protocol.AgentActivityProbePayload{AgentID: "agent-a", LaunchID: "launch-a", ProbeID: "probe-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if probe.Snapshot.ProbeID != "probe-1" || probe.Snapshot.ActivityKind != protocol.ActivityKindWorking || probe.Snapshot.ClientSequence != 2 || probe.Detail != "Starting…" || probe.IsHeartbeat {
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
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", LaunchID: "launch-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 || len(second) != 1 {
		t.Fatalf("replaced transport delivery first=%d second=%d", len(first), len(second))
	}
}

func TestMessageHandoffWithoutManagedLaunchDoesNotInventActivityIdentity(t *testing.T) {
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
	runner.observeMessageAccepted("agent-1", "runtime-1", []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:agent-1", Seq: 1,
	}})

	wantFrames := []string{protocol.EventAgentMessageHandoff}
	if len(frames) != len(wantFrames) {
		t.Fatalf("Runner frames=%v, want %v", frames, wantFrames)
	}
	for i := range wantFrames {
		if frames[i] != wantFrames[i] {
			t.Fatalf("Runner frames=%v, want %v", frames, wantFrames)
		}
	}
	if len(activities) != 0 {
		t.Fatalf("Message handoff invented Activity without a managed launch: %+v", activities)
	}
}

func TestResidentRuntimeEventsPublishRaftActivityLifecycle(t *testing.T) {
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	runner := installTestRunnerActivity(t, d, "workspace-1", producer)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	ack, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.SetManaged(protocol.AgentStatusPayload{AgentID: "agent-a", LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a", LaunchID: ack.LaunchID}); err != nil {
		t.Fatal(err)
	}

	for _, message := range []agent.Message{
		{Type: agent.MessageThinking},
		{Type: agent.MessageToolUse, Tool: "exec_command", Input: map[string]any{"command": "ls -la"}},
		{Type: agent.MessageStatus, Status: "reconnecting"},
		{Type: agent.MessageDiagnostic, Title: "Codex config warning", Level: "warning", Diagnostic: "configWarning", Content: "User namespaces are unavailable"},
		{Type: agent.MessageError, Content: "sensitive provider text"},
	} {
		runner.observeResidentMessageRuntime("agent-a", "runtime-1", message)
	}
	wantKinds := []string{protocol.ActivityKindThinking, protocol.ActivityKindWorking, protocol.ActivityKindWorking, protocol.ActivityKindError}
	wantDetails := []string{"thinking_started", "running_command", "running_command", "runtime_error"}
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
	if toolBody.Text != "ls -la" || toolBody.DetailKind != "running_command" {
		t.Fatalf("tool-use Activity body = %+v", toolBody)
	}
	var diagnostic protocol.AgentActivitySystemBody
	if err := json.Unmarshal(activities[2].Entries[0].Body, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if activities[2].Entries[0].Kind != "system" || diagnostic.Title != "Runtime warning" || diagnostic.Text != "Provider reported a warning" {
		t.Fatalf("runtime diagnostic Activity = kind:%q body:%+v", activities[2].Entries[0].Kind, diagnostic)
	}
	var errorBody protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(activities[len(activities)-1].Entries[0].Body, &errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody.Text != "Agent execution failed" || errorBody.DetailKind != "runtime_error" {
		t.Fatalf("runtime error Activity = %+v, want producer-owned safe narrative", errorBody)
	}
}

func TestResidentRuntimeEventPersistsProviderSessionWithoutManagedActivity(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", agent.Message{Type: agent.MessageText, SessionID: "provider-session-1"})

	got, err := d.agentRuntimeSessions.Get("agent-1", "runtime-1")
	if err != nil {
		t.Fatalf("read recorded provider session: %v", err)
	}
	if got != "provider-session-1" {
		t.Fatalf("recorded provider session = %q, want provider-session-1", got)
	}
}

func TestResidentRuntimeEventProjectsChangedProviderSession(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	sessions := make(chan protocol.AgentSessionPayload, 2)
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", func(eventType string, payload any) error {
		if session, ok := payload.(protocol.AgentSessionPayload); ok && eventType == protocol.EventAgentSession {
			sessions <- session
		}
		return nil
	})
	ack, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.activity.SetManaged(
		protocol.AgentStatusPayload{AgentID: "agent-1", LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-1", LaunchID: ack.LaunchID},
	); err != nil {
		t.Fatal(err)
	}

	message := agent.Message{Type: agent.MessageText, SessionID: "provider-session-1"}
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", message)
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", message)
	select {
	case got := <-sessions:
		if got.ProviderSessionID != message.SessionID || got.LaunchID != ack.LaunchID {
			t.Fatalf("projected provider session = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("changed provider session was not published")
	}
	select {
	case duplicate := <-sessions:
		t.Fatalf("unchanged provider session was republished: %+v", duplicate)
	default:
	}
	frames := runner.activity.ReconnectFrames()
	found := false
	for _, frame := range frames {
		if session, ok := frame.Payload.(protocol.AgentSessionPayload); ok && frame.EventType == protocol.EventAgentSession && session.AgentID == "agent-1" {
			found = session.ProviderSessionID == message.SessionID
		}
	}
	if !found {
		t.Fatalf("reconnect frames did not retain provider session: %+v", frames)
	}
}

func TestResidentCompactionPublishesOneStaleEntryAndFinishesBeforeResumedOutput(t *testing.T) {
	now := time.Date(2026, time.August, 11, 6, 0, 0, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	var staleDelay time.Duration
	var fireStale func()
	producer.schedule = func(delay time.Duration, callback func()) func() {
		staleDelay = delay
		fireStale = callback
		return func() { fireStale = nil }
	}
	installActivityProducerAgent(t, producer)
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	runner := installTestRunnerActivity(t, d, "workspace-1", producer)
	runner.processes.newID = func() string { return "launch-a" }
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "launch-a", StartDispatchID: "launch-a" + "-dispatch"}); err != nil {
		t.Fatal(err)
	}
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionStarted})
	if len(activities) != 1 || activities[0].Snapshot.ActivityKind != protocol.ActivityKindWorking || activities[0].Snapshot.DetailKind != "compacting_context" || len(activities[0].Entries) != 1 {
		t.Fatalf("compaction start Activity = %+v", activities)
	}
	if staleDelay != 5*time.Minute || fireStale == nil {
		t.Fatalf("compaction watchdog = %v callback_present=%v, want one five-minute timer", staleDelay, fireStale != nil)
	}

	now = now.Add(5 * time.Minute)
	fireStale()
	if len(activities) != 2 || activities[1].Snapshot.ActivityKind != protocol.ActivityKindWorking || activities[1].Snapshot.DetailKind != "compaction_stale" || len(activities[1].Entries) != 1 {
		t.Fatalf("stale compaction Activity = %+v, want one Timeline entry after five minutes", activities)
	}

	now = now.Add(time.Minute)
	producer.Tick()
	if len(activities) != 3 || activities[2].Snapshot.DetailKind != "compaction_stale" || len(activities[2].Entries) != 0 {
		t.Fatalf("post-stale heartbeat = %+v, want Snapshot-only heartbeat", activities)
	}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageThinking})
	if len(activities) != 5 {
		t.Fatalf("Activity count after resumed output = %d, want inferred finish plus thinking", len(activities))
	}
	finish := activities[3]
	if finish.Snapshot.ActivityKind != protocol.ActivityKindWorking || finish.Snapshot.DetailKind != "compaction_finished" || len(finish.Entries) != 1 {
		t.Fatalf("inferred compaction finish = %+v", finish)
	}
	if thinking := activities[4]; thinking.Snapshot.ActivityKind != protocol.ActivityKindThinking || len(thinking.Entries) != 0 {
		t.Fatalf("resumed thinking Activity = %+v", thinking)
	}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionFinished})
	if len(activities) != 6 || activities[5].Snapshot.ActivityKind != protocol.ActivityKindWorking || activities[5].Snapshot.DetailKind != "compaction_finished" {
		t.Fatalf("late explicit provider finish Activity = %+v", activities)
	}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionStarted})
	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionFinished})
	if len(activities) != 8 {
		t.Fatalf("explicit compaction lifecycle Activity count = %d, want 8", len(activities))
	}
	explicitFinish := activities[7]
	if explicitFinish.Snapshot.ActivityKind != protocol.ActivityKindWorking || explicitFinish.Snapshot.DetailKind != "compaction_finished" || len(explicitFinish.Entries) != 1 {
		t.Fatalf("explicit compaction finish = %+v", explicitFinish)
	}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionStarted})
	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageError, Content: "compaction failed"})
	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageThinking})
	if len(activities) != 11 || activities[9].Snapshot.ActivityKind != protocol.ActivityKindError || activities[10].Snapshot.ActivityKind != protocol.ActivityKindThinking {
		t.Fatalf("interrupted compaction Activity = %+v", activities[8:])
	}

	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{Type: agent.MessageCompactionStarted})
	runner.observeMessageTurnCompletion("agent-a", "runtime-1", nil)
	if len(activities) != 14 || activities[12].Snapshot.DetailKind != "compaction_finished" || activities[13].Snapshot.ActivityKind != protocol.ActivityKindOnline || activities[13].Snapshot.DetailKind != "idle" {
		t.Fatalf("turn-end compaction completion Activity = %+v", activities[11:])
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
	runner := installTestRunnerActivity(t, d, "workspace-1", producer)
	runner.processes.newID = func() string { return "launch-a" }
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "launch-a", StartDispatchID: "launch-a" + "-dispatch"}); err != nil {
		t.Fatal(err)
	}
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.canonicalRuntimes.slots["agent-a\x00runtime-1"] = &canonicalAgentRuntimeSlot{
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
	if body.Text != "Agent execution failed" {
		t.Fatalf("failure narrative = %q", body.Text)
	}
	if _, found := runner.processes.Snapshot("agent-a"); found {
		t.Fatal("provider startup-request failure retained APM launch")
	}
	producer.mu.Lock()
	state := producer.states[agentActivityProducerKey{agentID: "agent-a", launchID: "launch-a"}]
	producer.mu.Unlock()
	if state == nil || state.status.Status != protocol.AgentStatusInactive {
		t.Fatalf("provider startup-request failure status = %+v, want inactive", state)
	}
}

func installActivityProducerAgent(t *testing.T, producer *agentActivityProducer) {
	t.Helper()
	if err := producer.SetManaged(protocol.AgentStatusPayload{AgentID: "agent-a", LaunchID: "launch-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a", LaunchID: "launch-a", RuntimeGeneration: 1}); err != nil {
		t.Fatalf("SetManaged: %v", err)
	}
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
