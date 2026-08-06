package daemon

import (
	"testing"
	"time"

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
