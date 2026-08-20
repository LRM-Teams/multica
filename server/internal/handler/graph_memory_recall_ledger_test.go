package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Spec §3/§14/§16, brief D1/D27: the recall lifecycle ledger persists recall,
// trajectory, expansion-batch, distinct-view, submission, and version-lease
// state with server-issued identities. Storage-level triggers reject
// cross-tenant or foreign-kind references even when application validation is
// bypassed.

// recallLedgerFixture carries the canonical identities a recall binds to.
type recallLedgerFixture struct {
	workspaceID pgtype.UUID
	taskID      pgtype.UUID
	runtimeID   pgtype.UUID
	daemonID    string
	projectID   pgtype.UUID
	channelID   pgtype.UUID
}

func mustGraphMemoryRecallFixture(t *testing.T) recallLedgerFixture {
	t.Helper()
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	suffix := uuid.NewString()[:8]
	fx := recallLedgerFixture{workspaceID: workspaceID, daemonID: "daemon-" + suffix}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, daemon_id)
		VALUES ($1, $2, 'local', 'stub', $3)
		RETURNING id
	`, workspaceID, "recall-runtime-"+suffix, fx.daemonID).Scan(&fx.runtimeID); err != nil {
		t.Fatal(err)
	}
	var agentID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (id, workspace_id, name, avatar_url, runtime_mode, runtime_id)
		VALUES (gen_random_uuid(), $1, $2, 'x', 'local', $3)
		RETURNING id
	`, workspaceID, "recall-agent-"+suffix, fx.runtimeID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, reason)
		VALUES ($1, $2, 'mention')
		RETURNING id
	`, workspaceID, agentID).Scan(&fx.taskID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id
	`, workspaceID, "recall-project-"+suffix).Scan(&fx.projectID); err != nil {
		t.Fatal(err)
	}
	fx.channelID = createGraphMemoryTestChannel(t, workspaceID)
	return fx
}

// mustInsertGraphMemoryRecall writes a minimal valid recall row and returns
// its server-issued id.
func mustInsertGraphMemoryRecall(t *testing.T, fx recallLedgerFixture, traceID string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO graph_memory_recall
		  (workspace_id, task_id, daemon_id, runtime_id, graph_kind, graph_owner_id, graph_version, k, query, trace_id)
		VALUES ($1, $2, $3, $4, 'project', $5, 1, 4, 'q', $6)
		RETURNING id
	`, fx.workspaceID, fx.taskID, fx.daemonID, fx.runtimeID, fx.projectID, traceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustInsertGraphMemoryTrajectory(t *testing.T, fx recallLedgerFixture, recallID pgtype.UUID, seedIndex int) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO graph_memory_trajectory (recall_id, workspace_id, seed_index)
		VALUES ($1, $2, $3)
		RETURNING id
	`, recallID, fx.workspaceID, seedIndex).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGraphMemoryRecallLedgerSchema(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	recallID := mustInsertGraphMemoryRecall(t, fx, "trace-schema-"+uuid.NewString()[:8])

	var status, trainingMode string
	var schemaVersion int32
	if err := testPool.QueryRow(ctx, `
		SELECT status, training_mode, schema_version FROM graph_memory_recall WHERE id = $1
	`, recallID).Scan(&status, &trainingMode, &schemaVersion); err != nil {
		t.Fatalf("recall row: %v", err)
	}
	if status != "accepted" || trainingMode != "offline_capture" || schemaVersion != 1 {
		t.Fatalf("recall defaults = (%s, %s, %d), want (accepted, offline_capture, 1)", status, trainingMode, schemaVersion)
	}

	trajectoryID := mustInsertGraphMemoryTrajectory(t, fx, recallID, 0)
	var trajStatus string
	var rounds int32
	if err := testPool.QueryRow(ctx, `
		SELECT status, rounds FROM graph_memory_trajectory WHERE id = $1
	`, trajectoryID).Scan(&trajStatus, &rounds); err != nil {
		t.Fatalf("trajectory row: %v", err)
	}
	if trajStatus != "running" || rounds != 0 {
		t.Fatalf("trajectory defaults = (%s, %d), want (running, 0)", trajStatus, rounds)
	}

	var batchID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO graph_memory_expansion_batch (trajectory_id, round, candidate_ids, request_key, view_quota)
		VALUES ($1, 0, '["n1","n2"]', 'seed', 1)
		RETURNING id
	`, trajectoryID).Scan(&batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	// Distinct-view accounting: the same node viewed twice in one batch keeps
	// exactly one event; the second insert conflicts on the primary key.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_view_event (batch_id, trajectory_id, node_id) VALUES ($1, $2, 'n1')
	`, batchID, trajectoryID); err != nil {
		t.Fatalf("first view: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_view_event (batch_id, trajectory_id, node_id) VALUES ($1, $2, 'n1')
	`, batchID, trajectoryID); err == nil {
		t.Fatal("duplicate view of the same node in one batch must conflict")
	}

	// Submission: ordered, unique, subset of viewed; one per trajectory.
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_trajectory SET viewed_node_ids = '["n1","n2"]' WHERE id = $1
	`, trajectoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_submission (trajectory_id, found, summary, node_ids, payload_hash)
		VALUES ($1, true, 's', '["n2","n1"]', 'h1')
	`, trajectoryID); err != nil {
		t.Fatalf("valid submission: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_submission (trajectory_id, found, summary, node_ids, payload_hash)
		VALUES ($1, true, 's', '["n1"]', 'h1')
	`, trajectoryID); err == nil {
		t.Fatal("second submission for the same trajectory must conflict")
	}

	// Version-pin retention lease (spec §15).
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_version_lease
		  (workspace_id, graph_kind, graph_owner_id, graph_version, consumer_kind, consumer_id)
		VALUES ($1, 'project', $2, 1, 'recall', $3)
	`, fx.workspaceID, fx.projectID, recallID); err != nil {
		t.Fatalf("version lease: %v", err)
	}
}

// Spec §16: tenant/graph-kind consistency is enforced at the storage layer.
func TestGraphMemoryRecallLedgerIdentityConsistency(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	other := mustGraphMemoryRecallFixture(t)

	insertRecall := func(workspaceID, taskID, runtimeID pgtype.UUID, daemonID, graphKind string, ownerID pgtype.UUID) error {
		_, err := testPool.Exec(ctx, `
			INSERT INTO graph_memory_recall
			  (workspace_id, task_id, daemon_id, runtime_id, graph_kind, graph_owner_id, graph_version, k, query, trace_id)
			VALUES ($1, $2, $3, $4, $5, $6, 1, 4, 'q', $7)
		`, workspaceID, taskID, daemonID, runtimeID, graphKind, ownerID, "trace-"+uuid.NewString()[:8])
		return err
	}

	cases := map[string]error{
		"cross-tenant task":       insertRecall(fx.workspaceID, other.taskID, fx.runtimeID, fx.daemonID, "project", fx.projectID),
		"foreign-kind owner":      insertRecall(fx.workspaceID, fx.taskID, fx.runtimeID, fx.daemonID, "project", fx.channelID),
		"cross-workspace owner":   insertRecall(fx.workspaceID, fx.taskID, fx.runtimeID, fx.daemonID, "project", other.projectID),
		"runtime of other daemon": insertRecall(fx.workspaceID, fx.taskID, fx.runtimeID, "daemon-other", "project", fx.projectID),
		"runtime of other tenant": insertRecall(fx.workspaceID, fx.taskID, other.runtimeID, other.daemonID, "project", fx.projectID),
	}
	for name, err := range cases {
		if err == nil {
			t.Fatalf("%s: insert must be rejected by storage identity checks", name)
		}
	}
	// Sanity: the valid shape still inserts after the rejections.
	mustInsertGraphMemoryRecall(t, fx, "trace-valid-"+uuid.NewString()[:8])

	// Duplicate idempotency identity: same (workspace_id, trace_id) conflicts.
	recallID := mustInsertGraphMemoryRecall(t, fx, "trace-dup")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_recall
		  (workspace_id, task_id, daemon_id, runtime_id, graph_kind, graph_owner_id, graph_version, k, query, trace_id)
		VALUES ($1, $2, $3, $4, 'project', $5, 1, 4, 'q', 'trace-dup')
	`, fx.workspaceID, fx.taskID, fx.daemonID, fx.runtimeID, fx.projectID); err == nil {
		t.Fatal("duplicate (workspace_id, trace_id) must conflict")
	}

	// One trajectory per seed index.
	mustInsertGraphMemoryTrajectory(t, fx, recallID, 0)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_trajectory (recall_id, workspace_id, seed_index) VALUES ($1, $2, 0)
	`, recallID, fx.workspaceID); err == nil {
		t.Fatal("duplicate (recall_id, seed_index) must conflict")
	}

	// Trajectory tenant must match its recall.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_trajectory (recall_id, workspace_id, seed_index) VALUES ($1, $2, 1)
	`, recallID, other.workspaceID); err == nil {
		t.Fatal("trajectory in a different workspace than its recall must be rejected")
	}

	// Submission shape: unviewed and duplicate node ids are rejected.
	trajectoryID := mustInsertGraphMemoryTrajectory(t, fx, recallID, 1)
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_trajectory SET viewed_node_ids = '["n1"]' WHERE id = $1
	`, trajectoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_submission (trajectory_id, found, summary, node_ids, payload_hash)
		VALUES ($1, true, 's', '["n1","n9"]', 'h')
	`, trajectoryID); err == nil {
		t.Fatal("submission citing a never-viewed node must be rejected")
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_submission (trajectory_id, found, summary, node_ids, payload_hash)
		VALUES ($1, true, 's', '["n1","n1"]', 'h')
	`, trajectoryID); err == nil {
		t.Fatal("submission with duplicate node ids must be rejected")
	}

	// View events cannot attach a batch to a foreign trajectory.
	otherRecall := mustInsertGraphMemoryRecall(t, other, "trace-view-"+uuid.NewString()[:8])
	otherTrajectory := mustInsertGraphMemoryTrajectory(t, other, otherRecall, 0)
	var batchID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO graph_memory_expansion_batch (trajectory_id, round, candidate_ids, request_key, view_quota)
		VALUES ($1, 0, '["n1"]', 'seed', 1)
		RETURNING id
	`, trajectoryID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_view_event (batch_id, trajectory_id, node_id) VALUES ($1, $2, 'n1')
	`, batchID, otherTrajectory); err == nil {
		t.Fatal("view event binding a batch to a foreign trajectory must be rejected")
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_view_event (batch_id, trajectory_id, node_id) VALUES ($1, $2, 'n1')
	`, batchID, trajectoryID); err != nil {
		t.Fatalf("valid view event: %v", err)
	}
}
