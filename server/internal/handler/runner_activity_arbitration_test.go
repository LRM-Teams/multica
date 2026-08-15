package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerInactiveLaunchAcceptsOnlyStoppedActivityAndFencesReplacement(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	identity := daemonws.ClientIdentity{DaemonID: "daemon-stop", WorkspaceID: testWorkspaceID}
	oldRuntimeID := seedMachineLockedRuntime(t, identity.DaemonID, "terminal-old")
	newRuntimeID := seedMachineLockedRuntime(t, identity.DaemonID, "terminal-new")
	agentID := createHandlerTestAgentOnRuntime(t, "terminal-stop-"+uuid.NewString()[:8], oldRuntimeID)
	const daemonInstanceID = "instance-stop"
	oldLaunchID := "launch-old-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')`, testWorkspaceID, agentID, oldRuntimeID, identity.DaemonID, daemonInstanceID, oldLaunchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, newRuntimeID); err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		identity.DaemonID + "/" + identity.WorkspaceID + "/" + daemonInstanceID: true,
	}}
	writeFrame := func(eventType string, payload any) error {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return h.HandleWorkspaceRunnerFrame(ctx, identity, daemonInstanceID, eventType, raw)
	}
	if err := writeFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID: agentID, LaunchID: oldLaunchID, Status: protocol.AgentStatusInactive,
	}); err != nil {
		t.Fatalf("record inactive old launch: %v", err)
	}
	now := time.Now().UTC()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_probe (
			workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id, probe_id, sent_at, deadline_at
		) VALUES ($1, $2, $3, $4, $5, 'probe-before-stop', $6, $7)`,
		testWorkspaceID, agentID, identity.DaemonID, daemonInstanceID, oldLaunchID, now, now.Add(runnerActivityProbeWindow)); err != nil {
		t.Fatal(err)
	}
	stopped := protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: oldLaunchID, DaemonInstanceID: daemonInstanceID,
		ClientSequence: 1, ProducerFactID: "old-stopped", ObservedAt: now,
		// Deliberately wrong: detailKind, not daemon presentation state, owns
		// the server's lifecycle reduction.
		ActivityKind: protocol.ActivityKindWorking, DetailKind: "stopped",
	}}
	if err := writeFrame(protocol.EventAgentActivity, stopped); err != nil {
		t.Fatalf("record terminal Activity after inactive status: %v", err)
	}
	var gotRuntime, gotLaunch, gotKind, gotDetail string
	if err := testPool.QueryRow(ctx, `
		SELECT runtime_id::text, launch_id, activity_kind, detail_kind FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&gotRuntime, &gotLaunch, &gotKind, &gotDetail); err != nil {
		t.Fatal(err)
	}
	if gotRuntime != oldRuntimeID || gotLaunch != oldLaunchID || gotKind != protocol.ActivityKindOffline || gotDetail != "stopped" {
		t.Fatalf("terminal snapshot = runtime:%q launch:%q kind:%q detail:%q", gotRuntime, gotLaunch, gotKind, gotDetail)
	}
	var probes int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	if probes != 0 {
		t.Fatalf("terminal stop retained %d pending probes", probes)
	}
	nonterminal := stopped
	nonterminal.Snapshot.ClientSequence++
	nonterminal.Snapshot.ProducerFactID = "late-working"
	nonterminal.Snapshot.ActivityKind = protocol.ActivityKindWorking
	nonterminal.Snapshot.DetailKind = "message_received"
	if err := writeFrame(protocol.EventAgentActivity, nonterminal); err == nil {
		t.Fatal("inactive launch accepted non-terminal Activity")
	}

	var newLaunchID, newDispatchID string
	if err := testPool.QueryRow(ctx, `
		SELECT launch_id::text, start_dispatch_id::text
		FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&newLaunchID, &newDispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_activity_launch
		SET runtime_id = $4, launch_id = $3, status = 'active', last_client_sequence = 0,
		    last_producer_fact_id = ''
		WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID, newLaunchID, newRuntimeID); err != nil {
		t.Fatal(err)
	}
	current := protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: newLaunchID, DaemonInstanceID: daemonInstanceID,
		ClientSequence: 1, ProducerFactID: "new-idle", ObservedAt: time.Now().UTC(),
		ActivityKind: protocol.ActivityKindOnline, DetailKind: "idle",
	}}
	if err := writeFrame(protocol.EventAgentActivity, current); err != nil {
		t.Fatalf("record replacement Activity: %v", err)
	}
	if err := writeFrame(protocol.EventAgentStartAck, protocol.AgentStartAckPayload{
		AgentID: agentID, LaunchID: newLaunchID, StartDispatchID: newDispatchID,
		QueueState: protocol.AgentStartQueueRunning,
	}); err != nil {
		t.Fatalf("record duplicate replacement ACK: %v", err)
	}
	if err := writeFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID: agentID, LaunchID: newLaunchID, Status: protocol.AgentStatusActive,
	}); err != nil {
		t.Fatalf("record duplicate replacement active status: %v", err)
	}
	staleCurrent := current
	staleCurrent.Snapshot.ProducerFactID = "stale-new-working"
	staleCurrent.Snapshot.ActivityKind = protocol.ActivityKindWorking
	staleCurrent.Snapshot.DetailKind = "message_received"
	if err := writeFrame(protocol.EventAgentActivity, staleCurrent); err == nil {
		t.Fatal("duplicate ACK/status reset the replacement Activity fence")
	}
	stopped.Snapshot.ClientSequence++
	stopped.Snapshot.ProducerFactID = "late-old-stopped"
	if err := writeFrame(protocol.EventAgentActivity, stopped); err == nil {
		t.Fatal("replacement launch accepted old terminal Activity")
	}
	if err := testPool.QueryRow(ctx, `
		SELECT runtime_id::text, launch_id, activity_kind, detail_kind FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&gotRuntime, &gotLaunch, &gotKind, &gotDetail); err != nil {
		t.Fatal(err)
	}
	if gotRuntime != newRuntimeID || gotLaunch != newLaunchID || gotKind != protocol.ActivityKindOnline || gotDetail != "idle" {
		t.Fatalf("replacement snapshot changed by stale stop = runtime:%q launch:%q kind:%q detail:%q", gotRuntime, gotLaunch, gotKind, gotDetail)
	}
}

func TestReapStaleRunnerActivityMarksAgentOfflineForComputerDisconnect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "stale-runner-"+uuid.NewString()[:8], nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	launchID := "launch-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'daemon-1', 'instance-1', $4, 'active')`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_snapshot (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, client_sequence, producer_fact_id, activity_kind, observed_at, received_at)
		VALUES ($1, $2, $3, 'daemon-1', 'instance-1', $4, 1, 'fact-1', 'working', $5, $5)`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID, now.Add(-runnerActivityStaleAfter-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.ReapStaleRunnerActivity(ctx, now); err != nil {
		t.Fatal(err)
	}
	var kind, detail, launchStatus string
	if err := testPool.QueryRow(ctx, `SELECT activity_kind, detail_kind FROM agent_activity_snapshot WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&kind, &detail); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&launchStatus); err != nil {
		t.Fatal(err)
	}
	if kind != protocol.ActivityKindOffline || detail != "machine_disconnected" || launchStatus != protocol.AgentStatusInactive {
		t.Fatalf("stale projection = kind:%q detail:%q launch:%q", kind, detail, launchStatus)
	}
}

func TestWorkspaceRunnerReadyFencesPriorDaemonInstanceAgentsOffline(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "ready-fence-"+uuid.NewString()[:8], nil)
	launchID := "launch-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'daemon-1', 'old-instance', $4, 'active')`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_snapshot (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, client_sequence, producer_fact_id, activity_kind, observed_at)
		VALUES ($1, $2, $3, 'daemon-1', 'old-instance', $4, 1, 'fact-1', 'online', now())`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.recordWorkspaceRunnerReady(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "new-instance", nil); err != nil {
		t.Fatal(err)
	}
	var kind, detail, status, instanceID string
	if err := testPool.QueryRow(ctx, `SELECT activity_kind, detail_kind FROM agent_activity_snapshot WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&kind, &detail); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status, daemon_instance_id FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status, &instanceID); err != nil {
		t.Fatal(err)
	}
	if kind != protocol.ActivityKindOffline || detail != "computer_restarted" {
		t.Fatalf("ready Activity = kind:%q detail:%q", kind, detail)
	}
	if status != protocol.AgentStatusActive || instanceID != "old-instance" {
		t.Fatalf("ready must not persist residency; leftover launch = status:%q instance:%q", status, instanceID)
	}
	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/new-instance": true,
	}}
	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceRunnerFrame(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "new-instance", protocol.EventAgentStatus, active); err != nil {
		t.Fatalf("replacement agent:status: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status, daemon_instance_id FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status, &instanceID); err != nil {
		t.Fatal(err)
	}
	if status != protocol.AgentStatusActive || instanceID != "new-instance" {
		t.Fatalf("replacement launch = status:%q instance:%q", status, instanceID)
	}
}

func TestWorkspaceRunnerReadyKeepsSameInstanceRunningLaunchActive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "ready-same-"+uuid.NewString()[:8], nil)
	launchID := "launch-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'daemon-1', 'instance-1', $4, 'active')`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	// Same-process reconnect reports ready before it can replay agent:status.
	// An empty runningAgents list must not deactivate that still-live launch.
	if err := testHandler.recordWorkspaceRunnerReady(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "instance-1", nil); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != protocol.AgentStatusActive {
		t.Fatalf("same-instance ready status=%q, want active so reconnect can replay agent:status", status)
	}
	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceRunnerFrame(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "instance-1", protocol.EventAgentStatus, active); err != nil {
		t.Fatalf("replayed agent:status after same-instance ready: %v", err)
	}
}

func TestRunnerActivityProbeResponseMustMatchPendingProbe(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "probe-runner-"+uuid.NewString()[:8], nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	launchID := "launch-" + uuid.NewString()
	probeID := "probe-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'daemon-1', 'instance-1', $4, 'active')`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	base := protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: launchID, DaemonInstanceID: "instance-1", ClientSequence: 1, ProducerFactID: "fact-1",
		ObservedAt: now, ActivityKind: protocol.ActivityKindThinking, DetailKind: "thinking_started",
	}}
	if err := testHandler.recordRunnerActivity(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "instance-1", base); err != nil {
		t.Fatalf("record original Activity: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_probe (workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id, probe_id, sent_at, deadline_at)
		VALUES ($1, $2, 'daemon-1', 'instance-1', $3, $4, $5, $6)`, testWorkspaceID, agentID, launchID, probeID, now, now.Add(runnerActivityProbeWindow)); err != nil {
		t.Fatal(err)
	}
	probeReply := base
	probeReply.Snapshot.ProbeID = probeID
	err := testHandler.recordRunnerActivity(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "instance-1", probeReply)
	if err != nil {
		t.Fatal(err)
	}
	var probes int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	if probes != 0 {
		t.Fatalf("matched probe was not cleared: %d", probes)
	}
}

func TestRunnerActivityRejectsLateProbeAfterLaunchWasFenced(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "late-probe-"+uuid.NewString()[:8], nil)
	launchID := "launch-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'daemon-1', 'instance-1', $4, 'inactive')`, testWorkspaceID, agentID, handlerTestRuntimeID(t), launchID); err != nil {
		t.Fatal(err)
	}
	err := testHandler.recordRunnerActivity(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "instance-1", protocol.AgentActivityPayload{Snapshot: protocol.AgentActivitySnapshot{
		AgentID: agentID, LaunchID: launchID, DaemonInstanceID: "instance-1", ClientSequence: 1, ProducerFactID: "late-fact",
		ObservedAt: time.Now().UTC(), ActivityKind: protocol.ActivityKindWorking, DetailKind: "model_response_started", ProbeID: "expired-probe",
	}})
	if err == nil {
		t.Fatal("late probe response changed an inactive launch")
	}
}
