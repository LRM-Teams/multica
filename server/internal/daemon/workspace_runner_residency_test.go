package daemon

import "testing"

// TestAgentResidencyStoreClearThenStaleWriteResurrects pins the bug that
// stopManagedAgent's second residency.clear used to paper over: a write that
// belongs to a launch already superseded by a Stop (same startStopEpoch the
// Stop cleared) must not resurrect residency after clear runs. Before the
// epoch guard, clear deleted the entry outright and a late rememberFailure
// (e.g. provider startup losing the race with Stop) simply recreated it.
func TestAgentResidencyStoreClearThenStaleWriteResurrects(t *testing.T) {
	store := newAgentResidencyStore(nil)
	store.rememberIdle("agent-a", "runtime-a", "launch-1", "dispatch-1", 5)
	store.clear("agent-a")
	if _, ok := store.get("agent-a"); ok {
		t.Fatal("clear should remove residency")
	}

	// Provider startup for the just-stopped launch loses the race with Stop
	// and records its terminal failure after clear already ran. It carries
	// the same startStopEpoch (5) the launch started with.
	store.rememberFailure("agent-a", "runtime-a", "launch-1", 5, managedRuntimeFailureRuntime, "boom", "")

	if res, ok := store.get("agent-a"); ok {
		t.Fatalf("stale write after clear resurrected residency: %+v", res)
	}
}

// TestAgentResidencyStoreClearThenNewerEpochWriteAccepted pins the other half
// of the guard: a write strictly newer than the epoch clear tombstoned at is
// a legitimate restart and must be accepted, not dropped along with the
// stale writes.
func TestAgentResidencyStoreClearThenNewerEpochWriteAccepted(t *testing.T) {
	store := newAgentResidencyStore(nil)
	store.rememberIdle("agent-a", "runtime-a", "launch-1", "dispatch-1", 5)
	store.clear("agent-a")

	store.rememberLaunch("agent-a", "runtime-a", "launch-2", "dispatch-2", 6)

	res, ok := store.get("agent-a")
	if !ok {
		t.Fatal("newer-epoch write after clear was dropped, want accepted")
	}
	if res.launchID != "launch-2" || res.startStopEpoch != 6 {
		t.Fatalf("residency after newer write = %+v, want launch-2 at epoch 6", res)
	}
}

// TestAgentResidencyStoreRememberLaunchBackfillsEpochForTombstone pins the
// rememberLaunch decision: it takes and stores startStopEpoch (a partial
// field update, not a full overwrite) rather than leaving it unguarded.
// rememberLaunch can be the *only* write a launch ever gets before Stop
// clears it -- e.g. Stop lands before provider startup ever reaches
// rememberIdle/rememberFailure -- so clear's tombstone is only accurate if
// rememberLaunch itself recorded the epoch.
func TestAgentResidencyStoreRememberLaunchBackfillsEpochForTombstone(t *testing.T) {
	store := newAgentResidencyStore(nil)
	store.rememberLaunch("agent-a", "runtime-a", "launch-1", "dispatch-1", 3)

	res, ok := store.get("agent-a")
	if !ok || res.startStopEpoch != 3 {
		t.Fatalf("rememberLaunch residency = %+v (ok=%v), want startStopEpoch=3", res, ok)
	}

	store.clear("agent-a")

	// A late write belonging to that same launch (its own epoch, 3) must not
	// resurrect it -- this only holds if clear's tombstone came from the
	// epoch rememberLaunch itself recorded.
	store.rememberFailure("agent-a", "runtime-a", "launch-1", 3, managedRuntimeFailureSpawn, "boom", "")
	if res, ok := store.get("agent-a"); ok {
		t.Fatalf("stale write at rememberLaunch's own epoch resurrected residency: %+v", res)
	}

	// A genuinely new launch (a strictly newer epoch) is accepted.
	store.rememberLaunch("agent-a", "runtime-a", "launch-2", "dispatch-2", 4)
	if res, ok := store.get("agent-a"); !ok || res.launchID != "launch-2" {
		t.Fatalf("new launch after clear = %+v (ok=%v), want launch-2 accepted", res, ok)
	}
}
