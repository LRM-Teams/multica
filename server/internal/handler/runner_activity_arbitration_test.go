package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReapStaleRunnerActivityProjectsDisconnectedWithoutInventingIdle(t *testing.T) {
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
	if err := testHandler.recordWorkspaceRunnerReady(ctx, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}, "new-instance"); err != nil {
		t.Fatal(err)
	}
	var kind, detail, status string
	if err := testPool.QueryRow(ctx, `SELECT activity_kind, detail_kind FROM agent_activity_snapshot WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&kind, &detail); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if kind != protocol.ActivityKindOffline || detail != "computer_restarted" || status != protocol.AgentStatusInactive {
		t.Fatalf("ready fence = kind:%q detail:%q status:%q", kind, detail, status)
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
		ObservedAt: now, ActivityKind: protocol.ActivityKindThinking,
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
		ObservedAt: time.Now().UTC(), ActivityKind: protocol.ActivityKindWorking, ProbeID: "expired-probe",
	}})
	if err == nil {
		t.Fatal("late probe response changed an inactive launch")
	}
}
