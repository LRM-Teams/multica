package workgraph

import (
	"context"
	"encoding/json"
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
	reviewerID := pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,metadata) VALUES($1,$2,$3,'local','test','{}')`, runtimeID, workspace, "runtime-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model) VALUES($1,$2,$3,'Worker','local','{}',$4,'composer-1.5')`, agentID, workspace, "agent-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model) VALUES($1,$2,$3,'Reviewer','local','{}',$4,'composer-1.5')`, reviewerID, workspace, "reviewer-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	anchor := createGoalAnchor(t, ctx, workspace, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=1 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	in := CreateInput{WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorChannelGoal, AnchorID: uuidToTestString(anchor), Admission: AdmissionGraph, Reason: "parallel and verify", ActorType: "agent", ActorID: uuidToTestString(agentID), IdempotencyKey: uuid.NewString(), Nodes: []NodeSpec{{TempID: "build", Title: "Build", AssigneeID: uuidToTestString(agentID), Role: "worker", CompletionContract: []string{"tests pass"}}, {TempID: "verify", Title: "Verify", AssigneeID: uuidToTestString(reviewerID), Role: "verifier", ContextPolicy: "blind", DependsOn: []string{"build"}}}}
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
	if epoch.LeaseToken == "" || epoch.LeaseExpiresAt == nil {
		t.Fatalf("start epoch omitted fencing lease: %#v", epoch)
	}
	if _, err = store.FinishEpoch(ctx, FinishEpochInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: result.Graph.ID, EpochID: epoch.ID, ActorAgentID: uuidToTestString(agentID),
		Evaluation: []byte(`{"information_gain":0.5}`), Decision: "CONTINUE", LeaseToken: uuid.NewString(),
	}); !errors.Is(err, ErrGraphConflict) {
		t.Fatalf("stale epoch lease err=%v, want ErrGraphConflict", err)
	}
	finished, err := store.FinishEpoch(ctx, FinishEpochInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: result.Graph.ID, EpochID: epoch.ID, ActorAgentID: uuidToTestString(agentID),
		Evaluation: []byte(`{"information_gain":0.5}`), Decision: "CONTINUE", LeaseToken: epoch.LeaseToken,
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
	reviewerID := pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,metadata) VALUES($1,$2,$3,'local','test','{}')`, runtimeID, workspace, "runtime-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model) VALUES($1,$2,$3,'Worker','local','{}',$4,'composer-1.5')`, agentID, workspace, "agent-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model) VALUES($1,$2,$3,'Reviewer','local','{}',$4,'composer-1.5')`, reviewerID, workspace, "reviewer-"+uuid.NewString(), runtimeID); err != nil {
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
				{TempID: "second", Title: prefix + " second", AssigneeID: uuidToTestString(reviewerID), Role: "verifier", DependsOn: []string{"first"}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	graphA := create(anchorA, "A")
	graphB := create(anchorB, "B")

	if _, err := testPool.Exec(ctx, `UPDATE work_graph_node SET execution_status='succeeded',review_status='accepted',effective_completion='satisfied' WHERE id=$1::uuid`, graphA.NodeIDs["second"]); err != nil {
		t.Fatal(err)
	}
	revised, err := store.Revise(ctx, ReviseInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		ExpectedGraphVersion: 1, Reason: "change contract and dependencies",
		ActorType: "agent", ActorID: uuidToTestString(agentID),
		Nodes: []NodeSpec{
			{TempID: "second", IssueID: graphA.IssueIDs["second"], Role: "verifier", DependsOn: []string{"first"}},
			{TempID: "first", IssueID: graphA.IssueIDs["first"], Role: "worker", CompletionContract: []string{"new contract"}},
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

	// Adding an unrelated reviewed lane must not erase already accepted work
	// from the current plan. Revisions carry terminal state forward only when
	// the node contract and all of its upstream semantics are unchanged.
	if _, err = testPool.Exec(ctx, `UPDATE work_graph_node SET execution_status='succeeded',review_status='accepted',effective_completion='satisfied' WHERE id=ANY($1::uuid[])`, []string{graphA.NodeIDs["first"], graphA.NodeIDs["second"]}); err != nil {
		t.Fatal(err)
	}
	thirdIssue := createWorkgraphIssue(t, ctx, workspace, agentID, 1001, "A third", "backlog")
	fourthIssue := createWorkgraphIssue(t, ctx, workspace, reviewerID, 1002, "A fourth", "backlog")
	revised, err = store.Revise(ctx, ReviseInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: graphA.Graph.ID,
		ExpectedGraphVersion: 2, Reason: "add an unrelated reviewed lane",
		ActorType: "agent", ActorID: uuidToTestString(agentID),
		Nodes: []NodeSpec{
			{TempID: "third", IssueID: uuidToTestString(thirdIssue.ID), Role: "worker", CompletionContract: []string{"new output"}},
			{TempID: "fourth", IssueID: uuidToTestString(fourthIssue.ID), Role: "verifier", DependsOn: []string{"third"}},
			{TempID: "second", IssueID: graphA.IssueIDs["second"], Role: "verifier", DependsOn: []string{"first"}},
			{TempID: "first", IssueID: graphA.IssueIDs["first"], Role: "worker", CompletionContract: []string{"new contract"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	byIssue = map[string]Node{}
	for _, node := range revised.Nodes {
		byIssue[node.IssueID] = node
	}
	if byIssue[graphA.IssueIDs["first"]].EffectiveCompletion != "satisfied" || byIssue[graphA.IssueIDs["second"]].EffectiveCompletion != "satisfied" {
		t.Fatalf("unrelated revision revoked accepted nodes: first=%#v second=%#v", byIssue[graphA.IssueIDs["first"]], byIssue[graphA.IssueIDs["second"]])
	}
	if byIssue[uuidToTestString(thirdIssue.ID)].ExecutionStatus != "ready" || byIssue[uuidToTestString(fourthIssue.ID)].ExecutionStatus != "queued" {
		t.Fatalf("new lane frontier incorrect: third=%#v fourth=%#v", byIssue[uuidToTestString(thirdIssue.ID)], byIssue[uuidToTestString(fourthIssue.ID)])
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
		ScopeDigest: uuid.NewString(), Verdict: "PASS", ReviewerAgentID: uuidToTestString(reviewerID),
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

func TestIndependentReviewGatePassReworkAndReviewerRecycle(t *testing.T) {
	ctx := t.Context()
	workspace := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspace)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, workspace) })
	runtimeID, workerID, reviewerProfileID := pgUUID(uuid.New()), pgUUID(uuid.New()), pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_runtime(id,workspace_id,name,runtime_mode,provider,metadata) VALUES($1,$2,$3,'local','test','{}')`, runtimeID, workspace, "runtime-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model,custom_env,custom_args,mcp_config) VALUES($1,$2,$3,'Worker','local','{}',$4,'composer-1.5','{"WORKER_SECRET":"not-for-review"}','["--worker-private"]','{"private":true}')`, workerID, workspace, "worker-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model,custom_env,custom_args,mcp_config) VALUES($1,$2,$3,'Reviewer','local','{}',$4,'composer-1.5','{"REVIEW_SECRET":"not-cloned"}','["--review-private"]','{"private":true}')`, reviewerProfileID, workspace, "reviewer-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	anchor := createGoalAnchor(t, ctx, workspace, workerID)
	store := NewStore(testPool)
	ready := []string{}
	store.OnNodesReady = func(_ context.Context, _ string, issueIDs []string) { ready = append(ready, issueIDs...) }
	created, err := store.Create(ctx, CreateInput{
		WorkspaceID: uuidToTestString(workspace), AnchorKind: AnchorChannelGoal, AnchorID: uuidToTestString(anchor),
		Admission: AdmissionGraph, Reason: "implement then independently review", ActorType: "agent", ActorID: uuidToTestString(workerID), IdempotencyKey: uuid.NewString(),
		Nodes: []NodeSpec{
			{TempID: "work", Title: "Implement", AssigneeID: uuidToTestString(workerID), Role: "worker", CompletionContract: []string{"tests pass"}},
			{TempID: "review", Title: "Review", AssigneeID: uuidToTestString(reviewerProfileID), Role: "verifier", ContextPolicy: "blind", WorkerMode: WorkerModeDerivedAgent, CloneReason: "clean-context independent review", DependsOn: []string{"work"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var derivedReviewer, sourceReviewer string
	var customEnv, customArgs, mcpConfig []byte
	if err = testPool.QueryRow(ctx, `SELECT item.assignee_id::text,agent.source_agent_id::text,agent.custom_env,agent.custom_args,agent.mcp_config
		FROM issue item JOIN agent ON agent.id=item.assignee_id WHERE item.id=$1::uuid`, created.IssueIDs["review"]).Scan(&derivedReviewer, &sourceReviewer, &customEnv, &customArgs, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	if derivedReviewer == uuidToTestString(reviewerProfileID) || sourceReviewer != uuidToTestString(reviewerProfileID) {
		t.Fatalf("review clone=%q source=%q", derivedReviewer, sourceReviewer)
	}
	if string(customEnv) != `{}` || string(customArgs) != `[]` || len(mcpConfig) != 0 {
		t.Fatalf("review clone inherited private config: env=%s args=%s mcp=%s", customEnv, customArgs, mcpConfig)
	}

	artifact, err := store.AddArtifact(ctx, ArtifactInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, ProducerNodeID: created.NodeIDs["work"],
		Digest: uuid.NewString(), Kind: "patch", Locator: "artifact://review-gate/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteIssueNode(ctx, uuidToTestString(workspace), created.IssueIDs["work"]); err != nil {
		t.Fatal(err)
	}
	var producerCompletion string
	if err = testPool.QueryRow(ctx, `SELECT effective_completion FROM work_graph_node WHERE id=$1::uuid`, created.NodeIDs["work"]).Scan(&producerCompletion); err != nil {
		t.Fatal(err)
	}
	if producerCompletion != "pending" {
		t.Fatalf("producer completion before review=%q, want pending", producerCompletion)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: artifact.ID, ReviewerAgentID: uuidToTestString(reviewerProfileID), ScopeDigest: uuid.NewString(), Verdict: "PASS",
	}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("unassigned reviewer err=%v, want ErrInvalidGraph", err)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: artifact.ID, ReviewerAgentID: derivedReviewer, ScopeDigest: uuid.NewString(), Verdict: "BLOCKED",
		Findings: json.RawMessage(`["waiting for external fixture"]`), EvidenceRefs: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReconcileReady(ctx, uuidToTestString(workspace), created.Graph.ID); err != nil {
		t.Fatal(err)
	}
	var blockedExecution string
	if err = testPool.QueryRow(ctx, `SELECT execution_status FROM work_graph_node WHERE id=$1::uuid`, created.NodeIDs["review"]).Scan(&blockedExecution); err != nil {
		t.Fatal(err)
	}
	if blockedExecution != "waiting" {
		t.Fatalf("blocked reviewer execution=%q, want waiting", blockedExecution)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: artifact.ID, ReviewerAgentID: derivedReviewer, ScopeDigest: uuid.NewString(), Verdict: "PASS",
		Findings: json.RawMessage(`[]`), EvidenceRefs: json.RawMessage(`["test://pass"]`),
	}); err != nil {
		t.Fatal(err)
	}
	var verifierCompletion, producerIssueStatus string
	var archived bool
	if err = testPool.QueryRow(ctx, `SELECT effective_completion FROM work_graph_node WHERE id=$1::uuid`, created.NodeIDs["work"]).Scan(&producerCompletion); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT effective_completion FROM work_graph_node WHERE id=$1::uuid`, created.NodeIDs["review"]).Scan(&verifierCompletion); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id=$1::uuid`, created.IssueIDs["work"]).Scan(&producerIssueStatus); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM agent WHERE id=$1::uuid`, derivedReviewer).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if producerCompletion != "satisfied" || verifierCompletion != "satisfied" || producerIssueStatus != "done" || !archived {
		t.Fatalf("pass gate producer=%q verifier=%q issue=%q archived=%v", producerCompletion, verifierCompletion, producerIssueStatus, archived)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: artifact.ID, ReviewerAgentID: derivedReviewer, ScopeDigest: uuid.NewString(), Verdict: "PASS",
	}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("terminal review replay err=%v, want ErrInvalidGraph", err)
	}

	second, err := store.AddArtifact(ctx, ArtifactInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, ProducerNodeID: created.NodeIDs["work"], ArtifactID: artifact.ArtifactID,
		Digest: uuid.NewString(), Kind: "patch", Locator: "artifact://review-gate/v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	var secondReviewer string
	if err = testPool.QueryRow(ctx, `SELECT assignee_id::text FROM issue WHERE id=$1::uuid`, created.IssueIDs["review"]).Scan(&secondReviewer); err != nil {
		t.Fatal(err)
	}
	if secondReviewer == derivedReviewer {
		t.Fatal("new artifact reused archived review identity")
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: second.ID, ReviewerAgentID: secondReviewer, ScopeDigest: uuid.NewString(), Verdict: "FAIL",
		Findings: json.RawMessage(`["missing regression test"]`), EvidenceRefs: json.RawMessage(`["test://failure"]`),
	}); err != nil {
		t.Fatal(err)
	}
	var execution, rejectedArtifactValidity string
	if err = testPool.QueryRow(ctx, `SELECT execution_status,effective_completion FROM work_graph_node WHERE id=$1::uuid`, created.NodeIDs["work"]).Scan(&execution, &producerCompletion); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT validity_status FROM work_artifact_revision WHERE id=$1::uuid`, second.ID).Scan(&rejectedArtifactValidity); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id=$1::uuid`, created.IssueIDs["work"]).Scan(&producerIssueStatus); err != nil {
		t.Fatal(err)
	}
	if execution != "ready" || producerCompletion != "pending" || producerIssueStatus != "todo" || rejectedArtifactValidity != "stale" {
		t.Fatalf("rework gate execution=%q completion=%q issue=%q artifact=%q ready=%v", execution, producerCompletion, producerIssueStatus, rejectedArtifactValidity, ready)
	}
	if _, err = store.AddVerification(ctx, VerificationInput{
		WorkspaceID: uuidToTestString(workspace), GraphID: created.Graph.ID, VerifierNodeID: created.NodeIDs["review"],
		ArtifactRevisionID: second.ID, ReviewerAgentID: secondReviewer, ScopeDigest: uuid.NewString(), Verdict: "PASS",
	}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("rejected artifact reused err=%v, want ErrInvalidGraph", err)
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
	goalID := createGoalAnchor(t, ctx, workspace, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE issue SET channel_goal_id=$2,goal_required=true WHERE id=$1`, parent.ID, goalID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET issue_counter=1 WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	store := NewStore(testPool)
	result, err := store.DecomposeIssue(ctx, DecomposeInput{
		WorkspaceID: uuidToTestString(workspace), ParentIssueID: uuidToTestString(parent.ID), ActorAgentID: uuidToTestString(agentID),
		IdempotencyKey: uuid.NewString(), Reason: "parallel research and synthesis",
		Nodes: []IssuePlanNode{
			{TempID: "a", Title: "Research A", AcceptanceCriteria: []string{"evidence A recorded"}, AssigneeID: uuidToTestString(agentID), WorkerMode: WorkerModeDerivedAgent, CloneReason: "independent research lane"},
			{TempID: "b", Title: "Research B", AcceptanceCriteria: []string{"evidence B recorded"}, AssigneeID: uuidToTestString(agentID)},
			{TempID: "merge", Title: "Merge", AcceptanceCriteria: []string{"A and B reconciled"}, AssigneeID: uuidToTestString(agentID), DependsOn: []string{"a", "b"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReadyIssueIDs) != 2 {
		t.Fatalf("ready=%v, want two roots", result.ReadyIssueIDs)
	}
	if result.AgentIDs["a"] == uuidToTestString(agentID) || result.AgentIDs["a"] == "" {
		t.Fatalf("derived worker id=%q, source=%q", result.AgentIDs["a"], uuidToTestString(agentID))
	}
	var sourceID, assigneeID string
	if err = testPool.QueryRow(ctx, `SELECT source_agent_id::text FROM agent WHERE id=$1::uuid`, result.AgentIDs["a"]).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err = testPool.QueryRow(ctx, `SELECT assignee_id::text FROM issue WHERE id=$1::uuid`, result.IssueIDs["a"]).Scan(&assigneeID); err != nil {
		t.Fatal(err)
	}
	if sourceID != uuidToTestString(agentID) || assigneeID != result.AgentIDs["a"] {
		t.Fatalf("clone lineage source=%q assignee=%q", sourceID, assigneeID)
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
	var controllerIssueEvents, controllerDependencyEvents int
	if err = testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_kind='issue_created'),
		       count(*) FILTER (WHERE event_kind='dependency_changed')
		FROM goal_controller_event WHERE goal_id=$1`, goalID).Scan(&controllerIssueEvents, &controllerDependencyEvents); err != nil {
		t.Fatal(err)
	}
	if controllerIssueEvents != 3 || controllerDependencyEvents != 2 {
		t.Fatalf("Goal events issue=%d dependency=%d, want 3/2", controllerIssueEvents, controllerDependencyEvents)
	}
	var childGoalID pgtype.UUID
	var childGoalRequired bool
	var firstCriterion string
	if err = testPool.QueryRow(ctx, `
		SELECT channel_goal_id,goal_required,acceptance_criteria->>0
		FROM issue WHERE id=$1::uuid`, result.IssueIDs["a"]).Scan(&childGoalID, &childGoalRequired, &firstCriterion); err != nil {
		t.Fatal(err)
	}
	if childGoalID != goalID || !childGoalRequired || firstCriterion != "evidence A recorded" {
		t.Fatalf("child Goal contract goal=%v required=%v criterion=%q", childGoalID, childGoalRequired, firstCriterion)
	}
	var managedChildren int
	if err = testPool.QueryRow(ctx, `SELECT count(*) FROM issue_decompose_child WHERE parent_issue_id=$1`, parent.ID).Scan(&managedChildren); err != nil || managedChildren != 3 {
		t.Fatalf("managed children=%d err=%v, want 3", managedChildren, err)
	}
	foreignAgent := pgUUID(uuid.New())
	if _, err = testPool.Exec(ctx, `INSERT INTO agent(id,workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,model,max_concurrent_tasks) VALUES($1,$2,$3,'Foreign','local','{}',$4,'composer-1.5',1)`, foreignAgent, workspace, "foreign-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatal(err)
	}
	_, err = store.DecomposeIssue(ctx, DecomposeInput{
		WorkspaceID: uuidToTestString(workspace), ParentIssueID: uuidToTestString(parent.ID), ActorAgentID: uuidToTestString(agentID),
		IdempotencyKey: uuid.NewString(), Reason: "forbidden cross-agent snapshot",
		Nodes: []IssuePlanNode{
			{TempID: "foreign", Title: "Foreign", AcceptanceCriteria: []string{"verified"}, AssigneeID: uuidToTestString(foreignAgent), WorkerMode: WorkerModeDerivedAgent, CloneReason: "must not copy private memory"},
			{TempID: "local", Title: "Local", AcceptanceCriteria: []string{"verified"}, AssigneeID: uuidToTestString(agentID)},
		},
	})
	if !errors.Is(err, ErrGraphForbidden) {
		t.Fatalf("cross-agent clone err=%v, want ErrGraphForbidden", err)
	}
	if _, err = testPool.Exec(ctx, `UPDATE issue SET status='done' WHERE id=$1::uuid`, result.IssueIDs["a"]); err != nil {
		t.Fatal(err)
	}
	if err = store.ArchiveDerivedAgentForIssue(ctx, uuidToTestString(workspace), result.IssueIDs["a"]); err != nil {
		t.Fatal(err)
	}
	var archived bool
	if err = testPool.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM agent WHERE id=$1::uuid`, result.AgentIDs["a"]).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("derived worker was not archived")
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
