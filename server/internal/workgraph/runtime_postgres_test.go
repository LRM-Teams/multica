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

func TestRevisionAndGraphScopedWritesPreserveRuntimeConsistency(t *testing.T) {
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
	anchorA := createWorkgraphIssue(t, ctx, workspace, agentID, 1, "Anchor A", "in_progress")
	anchorB := createWorkgraphIssue(t, ctx, workspace, agentID, 2, "Anchor B", "in_progress")
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=2 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}

	store := NewStore(testPool)
	create := func(anchor pgtype.UUID, prefix string) CreateResult {
		t.Helper()
		result, err := store.Create(ctx, CreateInput{
			WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorIssue,
			AnchorID: uuidToTestString(anchor), Admission: AdmissionGraph,
			Reason: "initial plan", ActorType: "agent", ActorID: uuidToTestString(agentID),
			IdempotencyKey: uuid.NewString(),
			Nodes: []NodeSpec{
				{TempID: "first", Title: prefix + " first", AssigneeID: uuidToTestString(agentID), Role: "worker", CompletionContract: []string{"old contract"}},
				{TempID: "second", Title: prefix + " second", AssigneeID: uuidToTestString(agentID), Role: "verifier", DependsOn: []string{"first"}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	graphA := create(anchorA.ID, "A")
	graphB := create(anchorB.ID, "B")

	if _, err := testPool.Exec(ctx, `UPDATE work_graph_node SET execution_status='succeeded' WHERE id=$1::uuid`, graphA.NodeIDs["second"]); err != nil {
		t.Fatal(err)
	}
	revised, err := store.Revise(ctx, ReviseInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		ExpectedGraphVersion: 1, Reason: "change contract and dependencies",
		ActorType: "agent", ActorID: uuidToTestString(agentID),
		Nodes: []NodeSpec{
			{TempID: "first", IssueID: graphA.IssueIDs["first"], Role: "worker", CompletionContract: []string{"new contract"}},
			{TempID: "second", IssueID: graphA.IssueIDs["second"], Role: "verifier", DependsOn: []string{"first"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	byIssue := map[string]Node{}
	for _, node := range revised.Nodes {
		byIssue[node.IssueID] = node
	}
	if got := byIssue[graphA.IssueIDs["first"]]; len(got.Completion) != 1 || got.Completion[0] != "new contract" || got.ExecutionStatus != "ready" {
		t.Fatalf("revised first node=%#v", got)
	}
	if got := byIssue[graphA.IssueIDs["second"]]; got.ExecutionStatus != "queued" || got.ReviewStatus != "unreviewed" {
		t.Fatalf("revised dependent node=%#v", got)
	}

	if _, err = store.AddArtifact(ctx, ArtifactInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		ProducerNodeID: graphB.NodeIDs["first"], Digest: uuid.NewString(), Kind: "result", Locator: "artifact://cross-graph",
	}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("cross-graph artifact err=%v, want ErrInvalidGraph", err)
	}
	artifact, err := store.AddArtifact(ctx, ArtifactInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		ProducerNodeID: graphA.NodeIDs["first"], Digest: uuid.NewString(), Kind: "result", Locator: "artifact://graph-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		VerifierNodeID: graphB.NodeIDs["second"], ArtifactRevisionID: artifact.ID,
		ScopeDigest: uuid.NewString(), Verdict: "PASS",
	}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("cross-graph verification err=%v, want ErrInvalidGraph", err)
	}

	if _, err = store.InvalidateFrom(ctx, uuidToTestString(workspace), graphA.Graph.ID, graphB.NodeIDs["first"], "wrong graph"); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("cross-graph invalidation err=%v, want ErrInvalidGraph", err)
	}
	var validity string
	if err = testPool.QueryRow(ctx, `SELECT validity_status FROM work_graph_node WHERE id=$1::uuid`, graphB.NodeIDs["first"]).Scan(&validity); err != nil {
		t.Fatal(err)
	}
	if validity != "valid" {
		t.Fatalf("cross-graph invalidation changed validity to %q", validity)
	}
}

func uuidToTestString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
