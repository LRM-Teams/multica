package workgraph

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCreateGraphAtomicallyCreatesIssuesAndSchedulesOnlyRoots(t *testing.T) {
	ctx := t.Context()
	workspace := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspace)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, workspace) })
	runtimeID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,metadata) VALUES($1,$2,$3,'local','test','{}')`, runtimeID, workspace, "runtime-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model) VALUES($1,$2,$3,'Worker','local','{}',$4,'composer-1.5')`, agentID, workspace, "agent-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	anchor := createWorkgraphIssue(t, ctx, workspace, agentID, 1, "Root", "in_progress")
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=1 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	in := CreateInput{WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorIssue, AnchorID: uuidToTestString(anchor.ID), Admission: AdmissionGraph, Reason: "parallel and verify", ActorType: "agent", ActorID: uuidToTestString(agentID), IdempotencyKey: uuid.NewString(), Nodes: []NodeSpec{{TempID: "build", Title: "Build", AssigneeID: uuidToTestString(agentID), Role: "worker", CompletionContract: []string{"tests pass"}}, {TempID: "verify", Title: "Verify", AssigneeID: uuidToTestString(agentID), Role: "verifier", ContextPolicy: "blind", DependsOn: []string{"build"}}}}
	store := NewStore(testPool)
	result, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IssueIDs) != 2 || len(result.NodeIDs) != 2 {
		t.Fatalf("mappings=%#v %#v", result.IssueIDs, result.NodeIDs)
	}
	statuses := map[string]string{}
	for _, node := range result.Graph.Nodes {
		statuses[node.Role] = node.ExecutionStatus
	}
	if statuses["worker"] != "ready" || statuses["verifier"] != "queued" {
		t.Fatalf("statuses=%v", statuses)
	}
	replay, err := store.Create(ctx, in)
	if err != nil || !replay.Replayed || replay.Graph.ID != result.Graph.ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	changed := in
	changed.Reason = "different"
	if _, err = store.Create(ctx, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed retry err=%v", err)
	}
}

func uuidToTestString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
