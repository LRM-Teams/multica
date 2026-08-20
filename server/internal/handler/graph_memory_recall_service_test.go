package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §3/§5, brief D2/D4, acceptance A1/A14/A22: the server recall service
// resolves the canonical task, profile, routing/graph identity, training
// mode, and K entirely server-side; caller-supplied scope/version/profile/
// training fields are diagnostics only. TTT disabled forces K=1 without
// erasing the saved concurrency.

// fakeRecallSeeder returns fixed round-0 seed candidates.
type fakeRecallSeeder struct {
	ids []string
}

func (f fakeRecallSeeder) Seeds(_ context.Context, _ string, _ int, _ string, _ memorygraph.GraphView) ([]string, error) {
	return f.ids, nil
}

// mustGraphMemoryGraphDir creates the canonical on-disk graph for one scope
// with an initialized v1 store and returns its directory.
func mustGraphMemoryGraphDir(t *testing.T, root, workspaceID string, kind memorygraph.GraphDirKind, ownerID pgtype.UUID) string {
	t.Helper()
	dir, err := memorygraph.EnsureScopedDir(root, workspaceID, kind, util.UUIDToString(ownerID))
	if err != nil {
		t.Fatal(err)
	}
	if err := memorygraph.NewStore(dir).Init(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mustGraphMemoryGraphProfile upserts the workspace profile in graph mode
// with the given TTT switch and saved K.
func mustGraphMemoryGraphProfile(t *testing.T, workspaceID pgtype.UUID, tttEnabled bool, savedK int) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO graph_memory_profile (workspace_id, memory_type, ttt_enabled, explore_agents)
		VALUES ($1, 'graph', $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET memory_type = 'graph', ttt_enabled = $2, explore_agents = $3
	`, workspaceID, tttEnabled, savedK); err != nil {
		t.Fatal(err)
	}
}

func mustGraphMemoryTaskScope(t *testing.T, taskID pgtype.UUID, column string, value pgtype.UUID) {
	t.Helper()
	q := "UPDATE agent_inbox_event SET " + column + " = $2 WHERE id = $1"
	if _, err := testPool.Exec(context.Background(), q, taskID, value); err != nil {
		t.Fatal(err)
	}
}

func mustGraphMemoryEnvDispatchRunAgent(t *testing.T, fx recallLedgerFixture, mode string, terminal bool) {
	t.Helper()
	ctx := context.Background()
	var runID pgtype.UUID
	if terminal {
		// failed_preflight is terminal without the frozen-snapshot FK the
		// completed/failed_timeout statuses require.
		if err := testPool.QueryRow(ctx, `
			INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode, status)
			VALUES ($1, $2, true, 'failed_preflight')
			RETURNING run_id
		`, fx.projectID, fx.workspaceID).Scan(&runID); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := testPool.QueryRow(ctx, `
			INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode)
			VALUES ($1, $2, true)
			RETURNING run_id
		`, fx.projectID, fx.workspaceID).Scan(&runID); err != nil {
			t.Fatal(err)
		}
	}
	var agentID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT agent_id FROM agent_inbox_event WHERE id = $1`, fx.taskID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO env_dispatch_run_agent
		  (run_id, source_agent_id, execution_agent_id, runtime_id, pi_session_id, training_mode, capture_boundary)
		VALUES ($1, $2, $2, $3, $4, $5, 'turn')
	`, runID, agentID, fx.runtimeID, "pi-"+uuid.NewString()[:8], mode); err != nil {
		t.Fatal(err)
	}
}

func newGraphMemoryRecallServiceForTest(root string) *service.GraphMemoryRecallService {
	return service.NewGraphMemoryRecallService(
		testPool, service.LoadGraphMemoryLimits(func(string) string { return "" }),
		root, "legacy", fakeRecallSeeder{ids: []string{"n1", "n2"}},
	)
}

func graphMemoryRecallRequestForFixture(fx recallLedgerFixture, traceID string) service.GraphMemoryRecallRequest {
	return service.GraphMemoryRecallRequest{
		WorkspaceID: util.UUIDToString(fx.workspaceID),
		TaskID:      util.UUIDToString(fx.taskID),
		DaemonID:    fx.daemonID,
		RuntimeID:   util.UUIDToString(fx.runtimeID),
		Query:       "q",
		TraceID:     traceID,
	}
}

func TestGraphMemoryRecallResolveKAndCallerAuthority(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	root := t.TempDir()
	mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)

	// Channel task routed into the project lineage (spec §4): the recall must
	// resolve the project graph from the server-side binding, never from the
	// caller's claimed scope.
	if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $2 WHERE id = $1`, fx.channelID, fx.projectID); err != nil {
		t.Fatal(err)
	}
	mustGraphMemoryTaskScope(t, fx.taskID, "channel_id", fx.channelID)

	svc := newGraphMemoryRecallServiceForTest(root)

	// TTT disabled: effective K=1 even though the saved concurrency is 6 and
	// the caller asks for 99 (A1, A14).
	mustGraphMemoryGraphProfile(t, fx.workspaceID, false, 6)
	req := graphMemoryRecallRequestForFixture(fx, "trace-k-"+uuid.NewString()[:8])
	req.CallerK = 99
	req.CallerGraphKind = "channel"
	req.CallerGraphOwnerID = util.UUIDToString(fx.channelID)
	req.CallerGraphVersion = 77
	req.CallerTrainingMode = "online_rl"
	plan, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if plan.K != 1 {
		t.Fatalf("ttt_enabled=false must force K=1, got %d", plan.K)
	}
	if plan.GraphKind != "project" || plan.GraphOwnerID != util.UUIDToString(fx.projectID) {
		t.Fatalf("graph identity = %s/%s, want project/%s (caller hints are diagnostics only)", plan.GraphKind, plan.GraphOwnerID, util.UUIDToString(fx.projectID))
	}
	if plan.GraphVersion != 1 {
		t.Fatalf("pinned version = %d, want 1 (caller version ignored)", plan.GraphVersion)
	}
	if plan.TrainingMode != "offline_capture" {
		t.Fatalf("training mode = %s, want offline_capture (caller mode ignored)", plan.TrainingMode)
	}
	if !plan.GraphView.AllowProject || plan.GraphView.ChannelID != util.UUIDToString(fx.channelID) {
		t.Fatalf("graph view = %+v, want project+channel visibility", plan.GraphView)
	}

	// Durable ledger: one recall row, one trajectory (K=1), round-0 seed
	// batch with the retrieval candidates, and a version-pin lease.
	var status, trainingMode string
	var k int32
	if err := testPool.QueryRow(ctx, `
		SELECT status, training_mode, k FROM graph_memory_recall WHERE id = $1
	`, plan.RecallID).Scan(&status, &trainingMode, &k); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" || trainingMode != "offline_capture" || k != 1 {
		t.Fatalf("recall row = (%s, %s, %d), want (accepted, offline_capture, 1)", status, trainingMode, k)
	}
	var trajCount, batchCount, leaseCount int
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1),
		  (SELECT count(*) FROM graph_memory_expansion_batch b
		     JOIN graph_memory_trajectory tr ON tr.id = b.trajectory_id
		     WHERE tr.recall_id = $1 AND b.round = 0 AND b.request_key = 'seed'),
		  (SELECT count(*) FROM graph_memory_version_lease WHERE consumer_kind = 'recall' AND consumer_id = $1)
	`, plan.RecallID).Scan(&trajCount, &batchCount, &leaseCount); err != nil {
		t.Fatal(err)
	}
	if trajCount != 1 || batchCount != 1 || leaseCount != 1 {
		t.Fatalf("ledger shape = (%d trajectories, %d seed batches, %d leases), want (1, 1, 1)", trajCount, batchCount, leaseCount)
	}

	// Idempotent replay: the same trace id returns the same recall without
	// duplicating ledger rows.
	replay, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("replay Begin: %v", err)
	}
	if replay.RecallID != plan.RecallID {
		t.Fatalf("replay recall id = %s, want %s", replay.RecallID, plan.RecallID)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1
	`, plan.RecallID).Scan(&trajCount); err != nil {
		t.Fatal(err)
	}
	if trajCount != 1 {
		t.Fatalf("replay must not add trajectories, got %d", trajCount)
	}

	// TTT enabled: the saved per-recall K=6 takes effect (A1), with six
	// independent trajectories and six seed batches.
	mustGraphMemoryGraphProfile(t, fx.workspaceID, true, 6)
	req2 := graphMemoryRecallRequestForFixture(fx, "trace-k6-"+uuid.NewString()[:8])
	plan2, err := svc.Begin(ctx, req2)
	if err != nil {
		t.Fatalf("Begin ttt: %v", err)
	}
	if plan2.K != 6 {
		t.Fatalf("ttt_enabled=true must use saved K=6, got %d", plan2.K)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1),
		  (SELECT count(*) FROM graph_memory_expansion_batch b
		     JOIN graph_memory_trajectory tr ON tr.id = b.trajectory_id
		     WHERE tr.recall_id = $1 AND b.round = 0)
	`, plan2.RecallID).Scan(&trajCount, &batchCount); err != nil {
		t.Fatal(err)
	}
	if trajCount != 6 || batchCount != 6 {
		t.Fatalf("ttt ledger shape = (%d trajectories, %d seed batches), want (6, 6)", trajCount, batchCount)
	}
	var seedIDs string
	if err := testPool.QueryRow(ctx, `
		SELECT b.candidate_ids::text FROM graph_memory_expansion_batch b
		JOIN graph_memory_trajectory tr ON tr.id = b.trajectory_id
		WHERE tr.recall_id = $1 AND b.round = 0 LIMIT 1
	`, plan2.RecallID).Scan(&seedIDs); err != nil {
		t.Fatal(err)
	}
	if seedIDs != `["n1", "n2"]` && seedIDs != `["n1","n2"]` {
		t.Fatalf("seed batch candidates = %s, want the retrieval seeds", seedIDs)
	}
}

// Spec §5: memory-agent training behavior resolves from the invoking agent's
// env-dispatch mode; ordinary recalls capture offline; mode "none" disables
// Graph work for that agent.
func TestGraphMemoryRecallTrainingModeResolution(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	newScopedFixture := func(t *testing.T) (recallLedgerFixture, string) {
		fx := mustGraphMemoryRecallFixture(t)
		root := t.TempDir()
		mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
		if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $2 WHERE id = $1`, fx.channelID, fx.projectID); err != nil {
			t.Fatal(err)
		}
		mustGraphMemoryTaskScope(t, fx.taskID, "channel_id", fx.channelID)
		mustGraphMemoryGraphProfile(t, fx.workspaceID, false, 4)
		return fx, root
	}

	// Ordinary non-env-dispatch recall: offline capture.
	fx, root := newScopedFixture(t)
	svc := newGraphMemoryRecallServiceForTest(root)
	plan, err := svc.Begin(ctx, graphMemoryRecallRequestForFixture(fx, "trace-tm-plain-"+uuid.NewString()[:8]))
	if err != nil {
		t.Fatalf("ordinary Begin: %v", err)
	}
	if plan.TrainingMode != "offline_capture" {
		t.Fatalf("ordinary recall training mode = %s, want offline_capture", plan.TrainingMode)
	}

	// Active env-dispatch run with offline_rl: trajectories persist for
	// offline export without opening AReaL sessions.
	fx2, root2 := newScopedFixture(t)
	mustGraphMemoryEnvDispatchRunAgent(t, fx2, "offline_rl", false)
	svc2 := newGraphMemoryRecallServiceForTest(root2)
	plan2, err := svc2.Begin(ctx, graphMemoryRecallRequestForFixture(fx2, "trace-tm-offline-"+uuid.NewString()[:8]))
	if err != nil {
		t.Fatalf("offline_rl Begin: %v", err)
	}
	if plan2.TrainingMode != "offline_rl" {
		t.Fatalf("env-dispatch offline_rl resolved as %s", plan2.TrainingMode)
	}

	// Env-dispatch mode none: Graph disabled for the invoking agent — no
	// recall row, no trajectories, no work.
	fx3, root3 := newScopedFixture(t)
	mustGraphMemoryEnvDispatchRunAgent(t, fx3, "none", false)
	svc3 := newGraphMemoryRecallServiceForTest(root3)
	if _, err := svc3.Begin(ctx, graphMemoryRecallRequestForFixture(fx3, "trace-tm-none-"+uuid.NewString()[:8])); !errors.Is(err, service.ErrGraphMemoryRecallDisabled) {
		t.Fatalf("mode none must disable recall, got %v", err)
	}

	// A terminal run no longer governs the agent: back to offline capture.
	fx4, root4 := newScopedFixture(t)
	mustGraphMemoryEnvDispatchRunAgent(t, fx4, "online_rl", true)
	svc4 := newGraphMemoryRecallServiceForTest(root4)
	plan4, err := svc4.Begin(ctx, graphMemoryRecallRequestForFixture(fx4, "trace-tm-term-"+uuid.NewString()[:8]))
	if err != nil {
		t.Fatalf("terminal-run Begin: %v", err)
	}
	if plan4.TrainingMode != "offline_capture" {
		t.Fatalf("terminal run must not steer training mode, got %s", plan4.TrainingMode)
	}
}

// Spec §1/§3: a recall failure never fails the business task. The non-fatal
// wrapper maps every resolution failure to a no-work outcome.
func TestGraphMemoryRecallFailureNonFatal(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	root := t.TempDir()
	svc := newGraphMemoryRecallServiceForTest(root)

	// Unknown task: hard resolution error from Begin...
	if _, err := svc.Begin(ctx, graphMemoryRecallRequestForFixture(fx, "trace-nf-"+uuid.NewString()[:8])); err == nil {
		t.Fatal("Begin without a resolvable graph scope must fail")
	}
	// ...but the non-fatal wrapper never propagates it.
	out := svc.TryBegin(ctx, graphMemoryRecallRequestForFixture(fx, "trace-nf-"+uuid.NewString()[:8]))
	if out.Plan != nil || out.Reason == "" {
		t.Fatalf("TryBegin on unscoped task = %+v, want nil plan with a reason", out)
	}

	// Legacy-mode workspace: Graph recall disabled, non-fatal.
	mustGraphMemoryTaskScope(t, fx.taskID, "channel_id", fx.channelID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'legacy')
	`, fx.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Begin(ctx, graphMemoryRecallRequestForFixture(fx, "trace-nf2-"+uuid.NewString()[:8])); !errors.Is(err, service.ErrGraphMemoryRecallDisabled) {
		t.Fatalf("legacy workspace Begin must fail with disabled, got %v", err)
	}
	out = svc.TryBegin(ctx, graphMemoryRecallRequestForFixture(fx, "trace-nf2-"+uuid.NewString()[:8]))
	if out.Plan != nil || out.Reason == "" {
		t.Fatalf("TryBegin on legacy workspace = %+v, want nil plan with a reason", out)
	}

	// Cross-tenant runtime identity fails closed: a runtime row belonging to
	// another workspace/daemon pair is never accepted.
	other := mustGraphMemoryRecallFixture(t)
	req := graphMemoryRecallRequestForFixture(fx, "trace-nf3-"+uuid.NewString()[:8])
	req.RuntimeID = util.UUIDToString(other.runtimeID)
	req.DaemonID = other.daemonID
	if _, err := svc.Begin(ctx, req); !errors.Is(err, service.ErrGraphMemoryRecallIdentity) {
		t.Fatalf("foreign runtime Begin must fail with identity error, got %v", err)
	}

}
