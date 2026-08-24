package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// After a server restart the in-memory observation store is empty. Frames
// from the current ready connection whose launch matches the durable desired
// projection must rebuild the observation instead of being rejected forever.

func newRestartRecoveryFixture(t *testing.T, suffix string) (Handler, daemonws.ClientIdentity, string, string, string) {
	t.Helper()
	identity := daemonws.ClientIdentity{DaemonID: "daemon-recovery-" + suffix, WorkspaceID: testWorkspaceID}
	runtimeID := seedMachineLockedRuntime(t, identity.DaemonID, "recovery-"+suffix)
	agentID := createHandlerTestAgentOnRuntime(t, "recovery-"+suffix+"-"+uuid.NewString()[:8], runtimeID)
	var launchID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&launchID); err != nil {
		t.Fatalf("load desired launch projection: %v", err)
	}
	const daemonInstanceID = "instance-after-restart"
	h := *testHandler
	// Fresh stores simulate a restarted server process.
	h.runnerObservations = newRunnerObservationStore()
	h.runnerActivityCursor = newRunnerActivityCursorStore()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		identity.DaemonID + "/" + identity.WorkspaceID + "/" + daemonInstanceID: true,
	}}
	return h, identity, daemonInstanceID, agentID, launchID
}

func TestRunnerActivityRecoversObservationAfterServerRestart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	h, identity, daemonInstanceID, agentID, launchID := newRestartRecoveryFixture(t, "activity")
	activity := protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: launchID, DaemonInstanceID: daemonInstanceID,
		ClientSequence: 1, ProducerFactID: "post-restart-heartbeat",
		ObservedAt: time.Now().UTC(), DetailKind: "idle",
	}}
	if err := h.recordRunnerActivity(ctx, identity, daemonInstanceID, activity); err != nil {
		t.Fatalf("activity frame after restart: %v", err)
	}
	obs, ok := h.observations().get(testWorkspaceID, agentID)
	if !ok || obs.status != protocol.AgentStatusActive || obs.launchID != launchID || obs.daemonInstanceID != daemonInstanceID {
		t.Fatalf("recovered observation=%+v ok=%v", obs, ok)
	}
	var gotLaunch string
	if err := testPool.QueryRow(ctx, `
		SELECT launch_id FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&gotLaunch); err != nil {
		t.Fatalf("load recovered snapshot: %v", err)
	}
	if gotLaunch != launchID {
		t.Fatalf("recovered snapshot launch = %q, want %q", gotLaunch, launchID)
	}
}

func TestRunnerSessionRecoversObservationAfterServerRestart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	h, identity, daemonInstanceID, agentID, launchID := newRestartRecoveryFixture(t, "session")
	sessionID := "session-" + uuid.NewString()
	if err := h.recordRunnerSession(ctx, identity, daemonInstanceID, protocol.AgentSessionPayload{
		AgentID: agentID, LaunchID: launchID, ProviderSessionID: sessionID,
	}); err != nil {
		t.Fatalf("session frame after restart: %v", err)
	}
	obs, ok := h.observations().get(testWorkspaceID, agentID)
	if !ok || obs.sessionID != sessionID || obs.status != protocol.AgentStatusActive {
		t.Fatalf("recovered observation=%+v ok=%v", obs, ok)
	}
	var persisted string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(provider_session_id, '') FROM agent_runner_launch_projection
		WHERE agent_id = $1`, agentID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != sessionID {
		t.Fatalf("persisted provider session = %q, want %q", persisted, sessionID)
	}
}

func TestRunnerObservationRecoveryRejectsUndesiredLaunch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	h, identity, daemonInstanceID, agentID, _ := newRestartRecoveryFixture(t, "reject")
	bogusLaunch := uuid.NewString()
	if err := h.recordRunnerActivity(ctx, identity, daemonInstanceID, protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: bogusLaunch, DaemonInstanceID: daemonInstanceID,
		ClientSequence: 1, ProducerFactID: "bogus-launch",
		ObservedAt: time.Now().UTC(), DetailKind: "idle",
	}}); err == nil {
		t.Fatal("activity with undesired launch must stay rejected")
	}
	if err := h.recordRunnerSession(ctx, identity, daemonInstanceID, protocol.AgentSessionPayload{
		AgentID: agentID, LaunchID: bogusLaunch, ProviderSessionID: "session-" + uuid.NewString(),
	}); err == nil {
		t.Fatal("session with undesired launch must stay rejected")
	}
	if _, ok := h.observations().get(testWorkspaceID, agentID); ok {
		t.Fatal("rejected frames must not seed an observation")
	}
}

func TestRunnerObservationRecoveryRequiresCurrentRunnerInstance(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	h, identity, _, agentID, launchID := newRestartRecoveryFixture(t, "stale")
	// Frames from an instance that is not the current ready connection must
	// not rebuild residency, even when the launch matches the projection.
	const staleInstance = "instance-stale"
	if err := h.recordRunnerActivity(ctx, identity, staleInstance, protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: launchID, DaemonInstanceID: staleInstance,
		ClientSequence: 1, ProducerFactID: "stale-instance",
		ObservedAt: time.Now().UTC(), DetailKind: "idle",
	}}); err == nil {
		t.Fatal("activity from a non-current instance must stay rejected")
	}
	if _, ok := h.observations().get(testWorkspaceID, agentID); ok {
		t.Fatal("stale instance must not seed an observation")
	}
}
