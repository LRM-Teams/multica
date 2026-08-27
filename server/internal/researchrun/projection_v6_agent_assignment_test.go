package researchrun

import (
	"testing"

	"github.com/google/uuid"
)

func TestV6ProjectionIncludesRunScopedAgentsAndWorkAssignments(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Project V6 Agent assignments")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Coordinate the research team", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Research Manus product and market developments",
	}); err != nil {
		t.Fatal(err)
	}

	workID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,expected_result_schema_id,payload,state_version,reason
	) VALUES($1::uuid,$2::uuid,$3::uuid,'research','ready',$4::uuid,1,$5,
		'research.atomic.v1','atomic_result_submission','{}'::jsonb,1,$6)`,
		workID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.reporterID,
		"projection-agent:"+workID, "Research Manus product strategy"); err != nil {
		t.Fatal(err)
	}

	snapshot, err := run.store.ProjectionV6Snapshot(run.ctx, V6ProjectionPageRequest{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentNodes := map[string]V6ProjectionNode{}
	workNodeID := ""
	for _, node := range snapshot.Nodes {
		if node.Kind == "agent" {
			agentNodes[node.CanonicalRef.ID] = node
		}
		if node.Kind == "work_s" && node.CanonicalRef.ID == workID {
			workNodeID = node.ID
		}
	}
	if len(agentNodes) != 2 {
		t.Fatalf("Agent projection nodes=%v, want Director and run-scoped worker", agentNodes)
	}
	workerNode, ok := agentNodes[run.fixture.reporterID]
	if !ok || workerNode.Title == "" || workerNode.CatalogSummary != "Research Manus product and market developments" {
		t.Fatalf("worker Agent projection=%+v, present=%v", workerNode, ok)
	}
	if workerNode.State.Execution != "idle" {
		t.Fatalf("idle worker Agent execution=%q, want idle", workerNode.State.Execution)
	}
	if workNodeID == "" {
		t.Fatal("assigned Work projection node is missing")
	}
	assignedEdge := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == "assigned_to" && edge.FromNodeID == workNodeID && edge.ToNodeID == workerNode.ID {
			assignedEdge = true
		}
	}
	if !assignedEdge {
		t.Fatalf("projection edges=%+v, want Work assigned_to worker Agent", snapshot.Edges)
	}
	detail, err := run.store.ProjectionV6NodeDetail(
		run.ctx,
		run.fixture.workspaceID,
		run.fixture.sessionID,
		snapshot.SnapshotID,
		workNodeID,
		"brief",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.AgentRefs) != 1 || detail.AgentRefs[0].ID != run.fixture.reporterID {
		t.Fatalf("Work detail Agent refs=%+v, want assigned worker before an Attempt exists", detail.AgentRefs)
	}
}

func TestV6ProjectionIncludesPendingCreateAgentPlaceholders(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Project pending create_agent placeholders")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	outboxID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO research_v6_outbox(id,workspace_id,session_id,kind,idempotency_key,payload,status)
		VALUES($1::uuid,$2::uuid,$3::uuid,'create_agent',$4,'{"spec":{"name":"市场研究员"}}'::jsonb,'pending')`,
		outboxID, run.fixture.workspaceID, run.fixture.sessionID, "pending-agent:"+outboxID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := run.store.ProjectionV6Snapshot(run.ctx, V6ProjectionPageRequest{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var pending *V6ProjectionNode
	for index := range snapshot.Nodes {
		node := snapshot.Nodes[index]
		if node.CanonicalRef.Kind == "pending_agent" && node.CanonicalRef.ID == outboxID {
			copy := node
			pending = &copy
		}
	}
	if pending == nil {
		t.Fatalf("pending create_agent projection missing: %+v", snapshot.Nodes)
	}
	if pending.Kind != "agent" || pending.Title != "" || pending.State.Execution != "pending" {
		t.Fatalf("pending placeholder=%+v, want unnamed pending agent", pending)
	}
	belongsToGoal := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == "belongs_to" && edge.FromNodeID == pending.ID {
			belongsToGoal = true
		}
	}
	if !belongsToGoal {
		t.Fatalf("pending placeholder has no belongs_to Goal edge: %+v", snapshot.Edges)
	}
}

func TestV6ProjectionSliceDoesNotRevealDirectorCycleWork(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Hide Director cycle Work from V6 projection")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	directorWorkID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,expected_result_schema_id,payload,state_version
	) VALUES($1::uuid,$2::uuid,$3::uuid,'director','succeeded',$4::uuid,1,$5,
		'director.action.registry.v1','director_action_proposal','{}'::jsonb,1)`,
		directorWorkID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID,
		"projection-director:"+directorWorkID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := run.store.ProjectionV6Snapshot(run.ctx, V6ProjectionPageRequest{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	goalNodeID := ""
	for _, node := range snapshot.Nodes {
		if node.Kind == "goal" {
			goalNodeID = node.ID
		}
		if node.CanonicalRef.Kind == "work_item" && node.CanonicalRef.ID == directorWorkID {
			t.Fatal("default projection exposed operational Director cycle Work")
		}
	}
	if goalNodeID == "" {
		t.Fatal("V6 projection Goal node is missing")
	}

	slice, err := run.store.ProjectionV6Slice(run.ctx, V6ProjectionSliceRequest{
		WorkspaceID: run.fixture.workspaceID,
		RunID:       run.fixture.sessionID,
		SnapshotID:  snapshot.SnapshotID,
		RootNodeID:  goalNodeID,
		Depth:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range slice.Nodes {
		if node.CanonicalRef.Kind == "work_item" && node.CanonicalRef.ID == directorWorkID {
			t.Fatal("expanded Goal slice exposed operational Director cycle Work")
		}
	}
}
