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
	anchor := createGoalAnchor(t, ctx, workspace, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=1 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	in := CreateInput{WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorChannelGoal, AnchorID: uuidToTestString(anchor), Admission: AdmissionGraph, Reason: "parallel and verify", ActorType: "agent", ActorID: uuidToTestString(agentID), IdempotencyKey: uuid.NewString(), Nodes: []NodeSpec{{TempID: "build", Title: "Build", AssigneeID: uuidToTestString(agentID), Role: "worker", CompletionContract: []string{"tests pass"}}, {TempID: "verify", Title: "Verify", AssigneeID: uuidToTestString(agentID), Role: "verifier", ContextPolicy: "blind", DependsOn: []string{"build"}}}}
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
	epoch, err := store.StartEpoch(ctx, StartEpochInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: result.Graph.ID, ActorAgentID: uuidToTestString(agentID),
		Contract: []byte(`{"objective":"test one bounded loop"}`), Budget: []byte(`{"max_tasks":2}`),
	})
	if err != nil || epoch.Number != 1 || epoch.Status != "running" {
		t.Fatalf("start epoch=%#v err=%v", epoch, err)
	}
	finished, err := store.FinishEpoch(ctx, FinishEpochInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: result.Graph.ID, EpochID: epoch.ID, ActorAgentID: uuidToTestString(agentID),
		Evaluation: []byte(`{"information_gain":0.5}`), Decision: "CONTINUE",
	})
	if err != nil || finished.Status != "committed" {
		t.Fatalf("finish epoch=%#v err=%v", finished, err)
	}
	next, err := store.StartEpoch(ctx, StartEpochInput{WorkspaceID: uuidToTestString(workspace), GraphID: result.Graph.ID, ActorAgentID: uuidToTestString(agentID)})
	if err != nil || next.Number != 2 {
		t.Fatalf("next epoch=%#v err=%v", next, err)
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
	anchorA := createGoalAnchor(t, ctx, workspace, agentID)
	anchorB := createGoalAnchor(t, ctx, workspace, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=2 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}

	store := NewStore(testPool)
	create := func(anchor pgtype.UUID, prefix string) CreateResult {
		t.Helper()
		result, err := store.Create(ctx, CreateInput{
			WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorChannelGoal,
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
	graphA := create(anchorA, "A")
	graphB := create(anchorB, "B")

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

func TestDecomposeIssueCreatesParallelRootsAndParkedJoin(t *testing.T) {
	ctx := t.Context()
	workspace := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspace)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, workspace) })
	runtimeID, agentID := pgUUID(uuid.New()), pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,metadata) VALUES($1,$2,$3,'local','test','{}')`, runtimeID, workspace, "runtime-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model,max_concurrent_tasks) VALUES($1,$2,$3,'Worker','local','{}',$4,'composer-1.5',6)`, agentID, workspace, "agent-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	parent := createWorkgraphIssue(t, ctx, workspace, agentID, 1, "Parent", "in_progress")
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=1 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	store := NewStore(testPool)
	result, err := store.DecomposeIssue(ctx, DecomposeInput{
		WorkspaceID: uuidToTestString(workspace), ParentIssueID: uuidToTestString(parent.ID), ActorAgentID: uuidToTestString(agentID),
		IdempotencyKey: uuid.NewString(), Reason: "parallel research and synthesis",
		Nodes: []IssuePlanNode{
			{TempID: "a", Title: "Research A", AssigneeID: uuidToTestString(agentID)},
			{TempID: "b", Title: "Research B", AssigneeID: uuidToTestString(agentID)},
			{TempID: "merge", Title: "Merge", AssigneeID: uuidToTestString(agentID), DependsOn: []string{"a", "b"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReadyIssueIDs) != 2 {
		t.Fatalf("ready=%v, want two roots", result.ReadyIssueIDs)
	}
	var status string
	if err = testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id=$1::uuid`, result.IssueIDs["merge"]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "backlog" {
		t.Fatalf("join status=%q, want backlog", status)
	}
	var dependencies int
	if err = testPool.QueryRow(ctx, `SELECT count(*) FROM issue_dependency WHERE issue_id=$1::uuid`, result.IssueIDs["merge"]).Scan(&dependencies); err != nil {
		t.Fatal(err)
	}
	if dependencies != 2 {
		t.Fatalf("dependencies=%d, want 2", dependencies)
	}
}

func uuidToTestString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

func createGoalAnchor(t *testing.T, ctx context.Context, workspaceID, creatorAgentID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var userID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT user_id FROM member WHERE workspace_id=$1 ORDER BY created_at LIMIT 1`, workspaceID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	channelID, goalID := pgUUID(uuid.New()), pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO channel(id,workspace_id,name,kind,created_by) VALUES($1,$2,$3,'group',$4)`, channelID, workspaceID, "goal-"+uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO channel_goal(id,workspace_id,channel_id,title,objective,success_criteria,created_by_type,created_by_id,updated_by_type,updated_by_id) VALUES($1,$2,$3,'Goal','Deliver evidence','["verified"]','agent',$4,'agent',$4)`, goalID, workspaceID, channelID, creatorAgentID); err != nil {
		t.Fatal(err)
	}
	return goalID
}
