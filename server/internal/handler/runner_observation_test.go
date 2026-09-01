package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunnerObservationStoreTracksCurrentInstanceRuntime(t *testing.T) {
	store := newRunnerObservationStore()
	if !store.acceptStatus("ws", "daemon", "instance", "agent", "runtime", protocol.AgentStatusActive) {
		t.Fatal("first active status rejected")
	}
	if !store.acceptStatus("ws", "daemon", "instance", "agent", "runtime-2", protocol.AgentStatusActive) {
		t.Fatal("current instance runtime update rejected")
	}
	obs, ok := store.get("ws", "agent")
	if !ok || obs.runtimeID != "runtime-2" || obs.status != protocol.AgentStatusActive {
		t.Fatalf("replacement observation=%+v ok=%v", obs, ok)
	}
}

func TestRunnerObservationStoreAcceptsSessionOnlyOnCurrentInstance(t *testing.T) {
	store := newRunnerObservationStore()
	if _, ok := store.acceptStartAck("ws", "daemon", "instance", "agent", "runtime"); !ok {
		t.Fatal("start ack rejected")
	}
	if !store.acceptSession("ws", "daemon", "instance", "agent", "session-1") {
		t.Fatal("session on current instance rejected")
	}
	if store.acceptSession("ws", "daemon", "old-instance", "agent", "session-2") {
		t.Fatal("old instance session accepted")
	}
	obs, ok := store.get("ws", "agent")
	if !ok || obs.sessionID != "session-1" || obs.status != "accepted" {
		t.Fatalf("session observation=%+v ok=%v", obs, ok)
	}
}

func TestRunnerObservationStoreDiesWithInstance(t *testing.T) {
	store := newRunnerObservationStore()
	store.putStatus("ws", "daemon", "old", "agent", "runtime", protocol.AgentStatusActive)
	store.forgetOtherInstances("ws", "daemon", "new")
	if _, ok := store.get("ws", "agent"); ok {
		t.Fatal("prior instance observation survived replacement ready")
	}
	store.putStatus("ws", "daemon", "new", "agent", "runtime", protocol.AgentStatusActive)
	store.forgetInstance("ws", "daemon", "new")
	if _, ok := store.get("ws", "agent"); ok {
		t.Fatal("current instance observation survived disconnect")
	}
}

func TestRunnerObservationStoreReplacesInstanceFromReadySnapshot(t *testing.T) {
	store := newRunnerObservationStore()
	store.putStatus("ws", "daemon", "instance", "stale-agent", "runtime-old", protocol.AgentStatusActive)
	store.replaceInstance("ws", "daemon", "instance", []runnerObservedAgent{{
		workspaceID: "ws", daemonID: "daemon", daemonInstanceID: "instance",
		agentID: "running-agent", runtimeID: "runtime-new", status: protocol.AgentStatusActive,
	}})
	if _, ok := store.get("ws", "stale-agent"); ok {
		t.Fatal("stale same-instance observation survived ready snapshot replacement")
	}
	obs, ok := store.get("ws", "running-agent")
	if !ok || obs.runtimeID != "runtime-new" || obs.status != protocol.AgentStatusActive {
		t.Fatalf("ready snapshot observation=%+v ok=%v", obs, ok)
	}
}
