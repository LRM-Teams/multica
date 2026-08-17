package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunnerObservationStoreKeepsLiveResidencyOnCurrentInstance(t *testing.T) {
	store := newRunnerObservationStore()
	if !store.acceptStatus("ws", "daemon", "instance", "agent", "runtime", "launch-1", protocol.AgentStatusActive) {
		t.Fatal("first active status rejected")
	}
	if store.acceptStatus("ws", "daemon", "instance", "agent", "runtime", "launch-2", protocol.AgentStatusActive) {
		t.Fatal("same-instance replacement launch overwrote a live launch")
	}
	if !store.acceptStatus("ws", "daemon", "instance", "agent", "runtime", "launch-1", protocol.AgentStatusInactive) {
		t.Fatal("inactive status for current launch rejected")
	}
	if !store.acceptStatus("ws", "daemon", "instance", "agent", "runtime-2", "launch-2", protocol.AgentStatusActive) {
		t.Fatal("replacement launch after inactive rejected")
	}
	obs, ok := store.get("ws", "agent")
	if !ok || obs.launchID != "launch-2" || obs.runtimeID != "runtime-2" || obs.status != protocol.AgentStatusActive {
		t.Fatalf("replacement observation=%+v ok=%v", obs, ok)
	}
}

func TestRunnerObservationStoreAcceptsSessionOnlyOnCurrentLaunch(t *testing.T) {
	store := newRunnerObservationStore()
	if _, ok := store.acceptStartAck("ws", "daemon", "instance", "agent", "runtime", "launch-1"); !ok {
		t.Fatal("start ack rejected")
	}
	if !store.acceptSession("ws", "daemon", "instance", "agent", "launch-1", "session-1") {
		t.Fatal("session on accepted launch rejected")
	}
	if store.acceptSession("ws", "daemon", "instance", "agent", "launch-other", "session-2") {
		t.Fatal("stale session accepted")
	}
	obs, ok := store.get("ws", "agent")
	if !ok || obs.sessionID != "session-1" || obs.status != "accepted" {
		t.Fatalf("session observation=%+v ok=%v", obs, ok)
	}
}

func TestRunnerObservationStoreDiesWithInstance(t *testing.T) {
	store := newRunnerObservationStore()
	store.putStatus("ws", "daemon", "old", "agent", "runtime", "launch-1", protocol.AgentStatusActive)
	store.forgetOtherInstances("ws", "daemon", "new")
	if _, ok := store.get("ws", "agent"); ok {
		t.Fatal("prior instance observation survived replacement ready")
	}
	store.putStatus("ws", "daemon", "new", "agent", "runtime", "launch-1", protocol.AgentStatusActive)
	store.forgetInstance("ws", "daemon", "new")
	if _, ok := store.get("ws", "agent"); ok {
		t.Fatal("current instance observation survived disconnect")
	}
}
