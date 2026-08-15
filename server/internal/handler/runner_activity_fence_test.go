package handler

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunnerActivityFingerprintBindsWholeProducerFact(t *testing.T) {
	base := protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-1",
		ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: time.Unix(1, 0),
		ActivityKind: protocol.ActivityKindWorking, DetailKind: "model_response_started",
	}}
	first, err := runnerActivityFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := runnerActivityFingerprint(base)
	if err != nil || replay != first {
		t.Fatalf("same fact fingerprint = %q, %v; want %q", replay, err, first)
	}
	changed := base
	changed.Snapshot.DetailKind = "running_command"
	updated, err := runnerActivityFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if updated == first {
		t.Fatal("different Activity envelope retained the producer fact fingerprint")
	}
	probeReply := base
	probeReply.Snapshot.ProbeID = "probe-1"
	probeFingerprint, err := runnerActivityFingerprint(probeReply)
	if err != nil || probeFingerprint != first {
		t.Fatalf("probe correlation changed producer fact fingerprint = %q, %v; want %q", probeFingerprint, err, first)
	}
}
