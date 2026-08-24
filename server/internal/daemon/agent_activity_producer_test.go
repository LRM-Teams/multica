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
	stalledStage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1", StaleFor: 7 * time.Minute}
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
		{name: "runtime ready", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeReady, Data: runtime, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "status", entryText: "Online", processID: "process-1"},
		{name: "runtime working", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeWorking, Data: stage, At: at}, kind: protocol.ActivityKindWorking, detail: "model_response_started"},
		{name: "runtime thinking", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeThinking, Data: stage, At: at}, kind: protocol.ActivityKindThinking, detail: "thinking_started", entryKind: "status", entryText: "Thinking"},
		{name: "runtime tool", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeTool, Data: tool, At: at}, kind: protocol.ActivityKindWorking, detail: "running_command", entryKind: "tool_start", entryText: "ls -la"},
		{name: "message check", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeTool, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "check_messages", ToolCallID: "call-check"}, At: at}, kind: protocol.ActivityKindWorking, detail: "checking_messages", entryKind: "tool_start", entryText: ""},
		{name: "message check through CLI", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeTool, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "bash", ToolCallID: "call-check-cli", ToolInput: map[string]any{"command": "multica message check"}}, At: at}, kind: protocol.ActivityKindWorking, detail: "checking_messages", entryKind: "tool_start", entryText: ""},
		{name: "runtime compacting", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeCompacting, Data: stage, At: at}, kind: protocol.ActivityKindWorking, detail: "compacting_context", entryKind: "status", entryText: "Compacting context"},
		{name: "runtime compacted", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeCompacted, Data: stage, At: at}, kind: protocol.ActivityKindWorking, detail: "compaction_finished", entryKind: "status", entryText: "Context compaction finished"},
		{name: "runtime stalled", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeStalled, Data: stalledStage, At: at}, kind: protocol.ActivityKindError, detail: "runtime_stalled", entryKind: "status", entryText: "Runtime stalled: no runtime events for 7m"},
		{name: "runtime idle", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeIdle, Data: stage, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "status", entryText: "Idle"},
		{name: "runtime diagnostic", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationRuntimeDiagnostic, Data: stage, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "Provider reported a warning"},
		{name: "message accepted", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1"}, At: at}, kind: protocol.ActivityKindWorking, detail: "message_received", entryKind: "status", entryText: "Message received"},
		{name: "freshness held", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", NewMessageCount: 2, ReasonCode: "local_pending"}, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "2 newer messages available — review then resend"},
		{name: "draft sent", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationDraftSent, Data: AgentDraftSentObservationData{RuntimeID: "runtime-1", Target: "#one"}, At: at}, kind: protocol.ActivityKindOnline, detail: "idle", entryKind: "system", entryText: "target: #one\nfreshness updates: 0 newer messages\ndecision: saved draft freshness check passed when sent"},
		{name: "error", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationError, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ReasonCode: "provider_failed", Message: "runtime failed: upstream unavailable"}, At: at}, kind: protocol.ActivityKindError, detail: "runtime_error", entryKind: "status", entryText: "runtime failed: upstream unavailable", processID: "process-1"},
		{name: "stopped by user", observation: AgentObservation{AgentID: "agent-a", Kind: AgentObservationOffline, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ReasonCode: "stopped"}, At: at}, kind: protocol.ActivityKindOffline, detail: "stopped", entryKind: "status", entryText: "Agent stopped by user"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sent []protocol.AgentActivityPayload
			producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
			installActivityProducerAgent(t, producer)
			test.observation.AgentInstanceID = "instance-a"
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
			if test.entryKind == "tool_start" {
				var body protocol.AgentActivityToolStartBody
				if err := json.Unmarshal(payload.Entries[0].Body, &body); err != nil || body.ToolInput != test.entryText {
					t.Fatalf("tool entry body = %+v err=%v", body, err)
				}
			} else if test.entryKind == "system" {
				var body protocol.AgentActivitySystemBody
				if err := json.Unmarshal(payload.Entries[0].Body, &body); err != nil || body.Text != test.entryText {
					t.Fatalf("system entry body = %+v err=%v", body, err)
				}
			} else {
				var body protocol.AgentActivityStatusBody
				if err := json.Unmarshal(payload.Entries[0].Body, &body); err != nil || body.Detail != test.entryText {
					t.Fatalf("status entry body = %+v err=%v", body, err)
				}
			}
		})
	}
}

func TestAgentActivityProducerSuppressesRepeatedIdleOnlineState(t *testing.T) {
	at := time.Date(2026, time.August, 18, 5, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	installActivityProducerAgent(t, producer)
	observation := AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeIdle,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: at,
	}
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	observation.At = at.Add(time.Minute)
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("repeated idle state emitted %d Activity facts, want 1", len(sent))
	}
}

func TestAgentActivityProducerShowsUnknownTool(t *testing.T) {
	at := time.Date(2026, time.August, 14, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	installActivityProducerAgent(t, producer)
	err := producer.Observe(AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "cursor-agent", ToolCallID: "call-1"},
		At:   at,
	})
	if err != nil {
		t.Fatalf("unknown tool Observe error = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("unknown tool sent %d Activity facts, want one", len(sent))
	}
}

func TestActivityProducerFactIDMatchesDaemonInstanceSeq(t *testing.T) {
	got := activityProducerFactID("agent-a", "daemon-1", 3)
	if got != "daemon_activity:agent-a:daemon-1:3" {
		t.Fatalf("fact id = %q", got)
	}
	if got := activityProducerFactID("agent-a", "daemon-2", 1); got != "daemon_activity:agent-a:daemon-2:1" {
		t.Fatalf("fact id = %q", got)
	}
}

func TestAgentActivityProducerObserveUsesRaftSeqFactIdentity(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	observation := AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeThinking,
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
	wantFactID := "daemon_activity:agent-a:daemon-1:1"
	if len(sent) != 1 || sent[0].Snapshot.ClientSequence != 1 || sent[0].Snapshot.ProducerFactID != wantFactID {
		t.Fatalf("observed payloads = %+v, want one Raft seq-derived fact %q", sent, wantFactID)
	}
}

func TestAgentActivityProducerCoalescesTextStagesAndPreservesToolEvents(t *testing.T) {
	at := time.Date(2026, time.August, 16, 9, 41, 19, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	installActivityProducerAgent(t, producer)
	stage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}

	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeThinking, Data: stage, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeThinking, Data: stage, At: at.Add(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeWorking, Data: stage, At: at.Add(2 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeWorking, Data: stage, At: at.Add(3 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "exec_command", ToolCallID: "call-1", ToolInput: map[string]any{"command": "pwd"}},
		At:   at.Add(4 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "exec_command", ToolCallID: "call-2", ToolInput: map[string]any{"command": "git status --short"}},
		At:   at.Add(5 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}

	if len(sent) != 4 {
		t.Fatalf("coalesced Activity count = %d, want thinking + working + both tools", len(sent))
	}
	want := []string{"thinking_started", "model_response_started", "running_command", "running_command"}
	for i, detail := range want {
		if sent[i].Snapshot.DetailKind != detail {
			t.Fatalf("Activity[%d] detail = %q, want %q", i, sent[i].Snapshot.DetailKind, detail)
		}
	}
	if len(sent[0].Entries) != 1 || sent[0].Entries[0].Kind != "status" {
		t.Fatalf("thinking Activity entries = %+v, want one status", sent[0].Entries)
	}
	if len(sent[1].Entries) != 0 {
		t.Fatalf("working Activity entries = %+v, want current-state update only", sent[1].Entries)
	}
}

func TestAgentActivityProducerDoesNotPutFinalReplyAfterCommandsOnTimeline(t *testing.T) {
	at := time.Date(2026, time.August, 17, 6, 11, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	installActivityProducerAgent(t, producer)
	stage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}
	observations := []AgentObservation{
		{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "exec_command", ToolCallID: "call-1", ToolInput: map[string]any{"command": "pwd"}}, At: at},
		{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "exec_command", ToolCallID: "call-2", ToolInput: map[string]any{"command": "git status --short"}}, At: at.Add(time.Millisecond)},
		{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeWorking, Data: stage, At: at.Add(2 * time.Millisecond)},
		{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeIdle, Data: stage, At: at.Add(3 * time.Millisecond)},
	}
	for _, observation := range observations {
		if err := producer.Observe(observation); err != nil {
			t.Fatal(err)
		}
	}

	if len(sent) != 4 {
		t.Fatalf("Activity count = %d, want two commands + state-only reply + Idle", len(sent))
	}
	wantEntryCounts := []int{1, 1, 0, 1}
	for index, want := range wantEntryCounts {
		if len(sent[index].Entries) != want {
			t.Fatalf("Activity[%d] entries = %+v, want %d", index, sent[index].Entries, want)
		}
	}
	if sent[2].Snapshot.DetailKind != "model_response_started" || sent[3].Snapshot.DetailKind != "idle" {
		t.Fatalf("terminal Activity snapshots = %q then %q, want model_response_started then idle", sent[2].Snapshot.DetailKind, sent[3].Snapshot.DetailKind)
	}
}

func TestAgentActivityProducerObserveRejectsMissingOrStaleInstance(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	observation := AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "stale-instance", Kind: AgentObservationRuntimeWorking,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: at,
	}
	if err := producer.Observe(observation); err == nil {
		t.Fatal("Observe accepted a stale local instance")
	}
	if len(sent) != 0 || len(producer.states) != 1 {
		t.Fatalf("stale observation mutated producer: sent=%d states=%+v", len(sent), producer.states)
	}
	unmanaged := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	missingInstance := AgentObservation{AgentID: "agent-unmanaged", Kind: AgentObservationRuntimeThinking, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: at}
	if err := unmanaged.Observe(missingInstance); err == nil {
		t.Fatal("Observe accepted a runtime fact without a local instance")
	}
	if len(unmanaged.states) != 0 {
		t.Fatalf("launch-free observation created managed state: %+v", unmanaged.states)
	}
}

func TestAgentActivityProducerReplacementKeepsAgentSequenceMonotonic(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, func(payload protocol.AgentActivityPayload) {
		sent = append(sent, payload)
	})
	status := protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}
	session := protocol.AgentSessionPayload{AgentID: "agent-a"}
	if err := producer.SetManaged("instance-1", status, session); err != nil {
		t.Fatal(err)
	}
	stage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-1", Kind: AgentObservationRuntimeThinking, Data: stage, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := producer.SetManaged("instance-2", status, session); err != nil {
		t.Fatal(err)
	}
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-2", Kind: AgentObservationRuntimeThinking, Data: stage, At: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0].Snapshot.ClientSequence != 1 || sent[1].Snapshot.ClientSequence != 2 || sent[0].Snapshot.ProducerFactID == sent[1].Snapshot.ProducerFactID {
		t.Fatalf("replacement Activity identities = %+v", sent)
	}
}

func TestAgentActivityProducerObserveKeepsSessionAndProcessIdentitiesDistinct(t *testing.T) {
	at := time.Date(2026, time.August, 11, 1, 0, 0, 0, time.UTC)
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return at }, nil)
	installActivityProducerAgent(t, producer)
	observation := AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeReady, At: at,
		Data: AgentRuntimeObservationData{
			RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ProviderSessionID: "session-1", TurnID: "turn-1", RuntimeGeneration: 4,
		},
	}
	if err := producer.Observe(observation); err != nil {
		t.Fatal(err)
	}
	state := producer.states[agentActivityProducerKey{agentID: "agent-a", agentInstanceID: "instance-a"}]
	if state.snapshot.ProcessInstanceID != "process-1" || state.session.ProviderSessionID != "session-1" || state.session.TurnID != "turn-1" || state.session.RuntimeGeneration != 4 {
		t.Fatalf("managed identities = snapshot:%+v session:%+v", state.snapshot, state.session)
	}
}

func TestReplayManagedAgentStartDoesNotRepaintLiveActivity(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	start := protocol.AgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1"}
	accepted := startTestManagedAgent(t, runner, start.AgentID, start.RuntimeID, start.AgentID)
	markTestLaunchRunning(t, runner, start.AgentID)
	if err := runner.activity.SetManaged(
		accepted.AgentInstanceID,
		protocol.AgentStatusPayload{AgentID: start.AgentID, Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: start.AgentID},
	); err != nil {
		t.Fatal(err)
	}
	if err := runner.activity.Observe(AgentObservation{
		AgentID: start.AgentID, AgentInstanceID: accepted.AgentInstanceID, Kind: AgentObservationRuntimeThinking,
		Data: AgentRuntimeStageObservationData{RuntimeID: start.RuntimeID}, At: now,
	}); err != nil {
		t.Fatal(err)
	}
	if !runner.replayManagedAgentStartPublication(start, agentProcessCallback{AgentID: start.AgentID, AgentInstanceID: accepted.AgentInstanceID}, nil) {
		t.Fatal("replayed a running launch as unpublished")
	}
	if len(activities) != 1 || activities[0].Snapshot.DetailKind != "thinking_started" {
		t.Fatalf("replayed start Activity = %+v, want the live thinking Snapshot left alone", activities)
	}
}

func TestResidentTextUpdatesWorkingWithoutAddingTimelineEntry(t *testing.T) {
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	accepted := startTestManagedAgent(t, runner, "agent-1", "runtime-1", "launch-1")
	markTestLaunchRunning(t, runner, "agent-1")
	if err := runner.activity.SetManaged(
		accepted.AgentInstanceID,
		protocol.AgentStatusPayload{AgentID: "agent-1", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-1"},
	); err != nil {
		t.Fatal(err)
	}
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", agent.Message{Type: agent.MessageText, Content: "working on it"})
	if len(activities) != 1 || activities[0].Snapshot.ActivityKind != protocol.ActivityKindWorking || activities[0].Snapshot.DetailKind != "model_response_started" {
		t.Fatalf("text Activity = %+v, want Raft model_response_started", activities)
	}
	if len(activities[0].Entries) != 0 {
		t.Fatalf("text Activity entries = %+v, want final reply kept out of Timeline", activities[0].Entries)
	}
}

func TestAgentActivityProducerReplaysLatestCompleteActivityWhileDisconnected(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	producer.SetConnected("agent-a", "instance-a", false)
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatalf("Observe(first): %v", err)
	}
	now = now.Add(time.Second)
	if err := producer.Observe(AgentObservation{
		AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeTool,
		Data: AgentRuntimeStageObservationData{
			RuntimeID: "runtime-1", ToolName: "Edit", ToolCallID: "call-1",
			ToolInput: map[string]any{"file_path": "/repo/out.go"},
		},
		At: now,
	}); err != nil {
		t.Fatalf("Observe(second): %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("disconnected producer sent %d payloads", len(sent))
	}
	frames := producer.ReconnectFrames()
	if len(frames) != 3 {
		t.Fatalf("reconnect frames = %d, want status/session/latest Activity", len(frames))
	}
	payload, ok := frames[2].Payload.(protocol.AgentActivityPayload)
	if !ok || payload.Snapshot.DetailKind != "editing_file" || len(payload.Entries) != 1 {
		t.Fatalf("replayed payload = %+v", frames[2].Payload)
	}
	var body protocol.AgentActivityToolStartBody
	if err := json.Unmarshal(payload.Entries[0].Body, &body); err != nil || body.ToolName != "edit_file" || body.ToolInput != "/repo/out.go" {
		t.Fatalf("replayed editing entry = %+v err=%v", body, err)
	}
}

func TestAgentActivityProducerHeartbeatsAndProbeDoNotInventState(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	producer.Tick()
	if len(sent) != 1 {
		t.Fatalf("heartbeat before 60 seconds sent %d payloads", len(sent))
	}
	now = now.Add(time.Second)
	producer.Tick()
	if len(sent) != 2 || sent[1].Snapshot.ClientSequence != 2 || sent[1].Snapshot.ProducerFactID != "daemon_activity:agent-a:daemon-1:2" || len(sent[1].Entries) != 0 || sent[1].Detail != "Starting…" || !sent[1].IsHeartbeat {
		t.Fatalf("heartbeat payload = %+v", sent)
	}
	probe, err := producer.Probe(protocol.AgentActivityProbePayload{AgentID: "agent-a", ProbeID: "probe-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if probe.Snapshot.ProbeID != "probe-1" || probe.Snapshot.ActivityKind != protocol.ActivityKindWorking || probe.Snapshot.ClientSequence != 2 || probe.Detail != "Starting…" || probe.IsHeartbeat {
		t.Fatalf("probe = %+v", probe.Snapshot)
	}
	state, ok := producer.states[agentActivityProducerKey{agentID: "agent-a", agentInstanceID: "instance-a"}]
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
	if err := producer.Observe(AgentObservation{AgentID: "agent-a", AgentInstanceID: "instance-a", Kind: AgentObservationRuntimeStarting, Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: now}); err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 || len(second) != 1 {
		t.Fatalf("replaced transport delivery first=%d second=%d", len(first), len(second))
	}
}

func TestReplayManagedStartDoesNotRepaintStarting(t *testing.T) {
	now := time.Date(2026, time.August, 16, 11, 54, 25, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{WorkspacesRoot: t.TempDir(), DaemonID: "computer-1"}, nil)
	d.runnerInstanceID = "daemon-1"
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	accepted := startTestManagedAgent(t, runner, "agent-a", "runtime-1", "launch-a")
	callback := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: accepted.AgentInstanceID, ProcessInstanceID: "resident-" + accepted.AgentInstanceID}
	if err := runner.processes.ProcessSpawned(callback); err != nil {
		t.Fatal(err)
	}
	if err := runner.processes.RuntimeReady(callback); err != nil {
		t.Fatal(err)
	}
	if err := producer.SetManaged(accepted.AgentInstanceID, protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a"}); err != nil {
		t.Fatal(err)
	}
	runner.broadcastActivity("agent-a", "runtime-1", "starting")
	runner.observeResidentRuntimeReady("agent-a", "runtime-1")
	before := len(activities)
	if !runner.replayManagedAgentStartPublication(protocol.AgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-1"}, callback, nil) {
		t.Fatal("replay did not succeed")
	}
	if len(activities) != before {
		t.Fatalf("replay painted %d extra Activity facts, want 0", len(activities)-before)
	}
}

func TestMessageAcceptanceWithoutManagedLaunchDoesNotInventActivityIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-instance-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", func(string, any) error { return nil })
	runner.activity = producer
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	messages := []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:agent-1", Seq: 1,
	}}
	runner.broadcastMessageReceivedActivity("agent-1", "runtime-1", messages)
	if len(activities) != 0 {
		t.Fatalf("Message acceptance invented Activity without a managed launch: %+v", activities)
	}
}

func TestPendingAndProviderAcceptancePublishOneMessageReceivedActivity(t *testing.T) {
	now := time.Date(2026, time.August, 17, 6, 0, 0, 0, time.UTC)
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-instance-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", func(string, any) error { return nil })
	runner.activity = producer
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	registerTestWorkspaceDaemonInbox(t, runner, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	launch, found := runner.managedLaunch("agent-1", "runtime-1")
	if !found {
		t.Fatal("managed Agent is missing")
	}
	if err := producer.SetManaged(
		launch.AgentInstanceID,
		protocol.AgentStatusPayload{AgentID: "agent-1", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-1", RuntimeGeneration: 1},
	); err != nil {
		t.Fatalf("SetManaged: %v", err)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "dm:agent-1", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "dm:agent-1", Seq: 1},
	}

	// Pending acceptance is not a runtime receipt and must not emit Activity.
	if _, err := runner.acceptMessageDelivery(context.Background(), delivery); err != nil {
		t.Fatalf("acceptMessageDelivery: %v", err)
	}
	// Raft records Message received once after the native provider accepts the body.
	messages := []protocol.AgentMessageProjection{delivery.Message}
	runner.broadcastMessageReceivedActivity("agent-1", "runtime-1", messages)

	if len(activities) != 1 || activities[0].Snapshot.DetailKind != "message_received" {
		t.Fatalf("Message received Activity = %+v, want one entry", activities)
	}
}

func TestResidentRuntimeEventsPublishRaftActivityLifecycle(t *testing.T) {
	var activities []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-1", time.Now, func(payload protocol.AgentActivityPayload) {
		activities = append(activities, payload)
	})
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-1"
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	accepted := startTestManagedAgent(t, runner, "agent-a", "runtime-1", "dispatch-a")
	if err := producer.SetManaged(accepted.AgentInstanceID, protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a"}); err != nil {
		t.Fatal(err)
	}

	for _, message := range []agent.Message{
		{Type: agent.MessageThinking},
		{Type: agent.MessageText, Content: "pong-reset"},
		{Type: agent.MessageToolUse, Tool: "exec_command", Input: map[string]any{"command": "ls -la"}},
		{Type: agent.MessageStatus, Status: "reconnecting"},
		{Type: agent.MessageDiagnostic, Title: "Codex config warning", Level: "warning", Diagnostic: "configWarning", Content: "User namespaces are unavailable"},
		{Type: agent.MessageError, Content: "sensitive provider text"},
	} {
		runner.observeResidentMessageRuntime("agent-a", "runtime-1", message)
	}
	wantKinds := []string{protocol.ActivityKindThinking, protocol.ActivityKindWorking, protocol.ActivityKindWorking, protocol.ActivityKindWorking, protocol.ActivityKindError}
	wantDetails := []string{"thinking_started", "model_response_started", "running_command", "running_command", "runtime_error"}
	if len(activities) != len(wantKinds) {
		t.Fatalf("Activity count = %d, want %d", len(activities), len(wantKinds))
	}
	for index := range wantKinds {
		if activities[index].Snapshot.ActivityKind != wantKinds[index] || activities[index].Snapshot.DetailKind != wantDetails[index] {
			t.Fatalf("Activity[%d] = %+v", index, activities[index].Snapshot)
		}
	}
	if len(activities[1].Entries) != 0 {
		t.Fatalf("text Activity entries = %+v, want current-state update without a Timeline row", activities[1].Entries)
	}
	var toolBody protocol.AgentActivityToolStartBody
	if err := json.Unmarshal(activities[2].Entries[0].Body, &toolBody); err != nil {
		t.Fatal(err)
	}
	if toolBody.ToolName != "bash" || toolBody.ToolInput != "ls -la" {
		t.Fatalf("tool-use Activity body = %+v", toolBody)
	}
	var diagnostic protocol.AgentActivitySystemBody
	if err := json.Unmarshal(activities[3].Entries[0].Body, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if activities[3].Entries[0].Kind != "system" || diagnostic.Title != "Runtime warning" || diagnostic.Text != "Provider reported a warning" {
		t.Fatalf("runtime diagnostic Activity = kind:%q body:%+v", activities[3].Entries[0].Kind, diagnostic)
	}
	var errorBody protocol.AgentActivityStatusBody
	if err := json.Unmarshal(activities[len(activities)-1].Entries[0].Body, &errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody.Detail != "sensitive provider text" || errorBody.DetailKind != "runtime_error" {
		t.Fatalf("runtime error Activity = %+v, want provider error text", errorBody)
	}
}

func TestResidentRuntimeEventPersistsProviderSessionWithoutManagedActivity(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", nil)
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
	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", func(eventType string, payload any) error {
		if session, ok := payload.(protocol.AgentSessionPayload); ok && eventType == protocol.EventAgentSession {
			sessions <- session
		}
		return nil
	})
	accepted := startTestManagedAgent(t, runner, "agent-1", "runtime-1", "launch-1")
	if err := runner.activity.SetManaged(
		accepted.AgentInstanceID,
		protocol.AgentStatusPayload{AgentID: "agent-1", Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: "agent-1"},
	); err != nil {
		t.Fatal(err)
	}

	message := agent.Message{Type: agent.MessageText, SessionID: "provider-session-1"}
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", message)
	runner.observeResidentMessageRuntime("agent-1", "runtime-1", message)
	select {
	case got := <-sessions:
		if got.ProviderSessionID != message.SessionID || got.AgentID != "agent-1" {
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
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	accepted := startTestManagedAgent(t, runner, "agent-a", "runtime-1", "launch-a")
	if err := producer.SetManaged(accepted.AgentInstanceID, protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a"}); err != nil {
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
	if thinking := activities[4]; thinking.Snapshot.ActivityKind != protocol.ActivityKindThinking || len(thinking.Entries) != 1 {
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
	runner := installTestAgentActivityProducer(t, d, "workspace-1", producer)
	accepted := startTestManagedAgent(t, runner, "agent-a", "runtime-1", "launch-a")
	if err := producer.SetManaged(accepted.AgentInstanceID, protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a"}); err != nil {
		t.Fatal(err)
	}
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.canonicalRuntimes.slots["agent-a\x00runtime-1"] = &agentRuntimeSlot{
		backend: failingResidentMessageRuntime{},
	}

	err := d.deliverIdleMessageBatch(context.Background(), "agent-a", "runtime-1", []protocol.AgentMessageProjection{{
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
	var body protocol.AgentActivityStatusBody
	if err := json.Unmarshal(got.Entries[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Detail != "runtime Message handoff unavailable (simulated crash window)" {
		t.Fatalf("failure status = %q", body.Detail)
	}
	if _, found := runner.processes.Snapshot("agent-a"); found {
		t.Fatal("provider startup-request failure retained APM launch")
	}
	producer.mu.Lock()
	state := producer.states[agentActivityProducerKey{agentID: "agent-a", agentInstanceID: accepted.AgentInstanceID}]
	producer.mu.Unlock()
	if state == nil || state.status.Status != protocol.AgentStatusInactive {
		t.Fatalf("provider startup-request failure status = %+v, want inactive", state)
	}
}

func TestObserveResidentMessageRuntimeClearsPoisonedPiSession(t *testing.T) {
	sessions := map[string]string{}
	runner := &WorkspaceDaemon{
		recordProviderSession: func(agentID, runtimeID, sessionID string) {
			key := agentID + "/" + runtimeID
			if sessionID == "" {
				delete(sessions, key)
				return
			}
			sessions[key] = sessionID
		},
	}
	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{
		Type:      agent.MessageStatus,
		SessionID: "poisoned-pi",
	})
	if sessions["agent-a/runtime-1"] != "poisoned-pi" {
		t.Fatalf("recorded session = %q", sessions["agent-a/runtime-1"])
	}
	runner.observeResidentMessageRuntime("agent-a", "runtime-1", agent.Message{
		Type:      agent.MessageError,
		SessionID: "poisoned-pi",
		Content:   "Unknown parameter: 'input[86].status'",
	})
	if _, ok := sessions["agent-a/runtime-1"]; ok {
		t.Fatalf("poisoned Pi session still recorded: %q", sessions["agent-a/runtime-1"])
	}
}

func installActivityProducerAgent(t *testing.T, producer *agentActivityProducer) {
	t.Helper()
	if err := producer.SetManaged("instance-a", protocol.AgentStatusPayload{AgentID: "agent-a", Status: protocol.AgentStatusActive}, protocol.AgentSessionPayload{AgentID: "agent-a", RuntimeGeneration: 1}); err != nil {
		t.Fatalf("SetManaged: %v", err)
	}
}
