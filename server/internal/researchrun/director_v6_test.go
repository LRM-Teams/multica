package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type teamV6StoreStub struct {
	added    AddV6TeamMemberInput
	archived ArchiveV6TeamMemberInput
}

func (s *teamV6StoreStub) AddV6TeamMember(_ context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	s.added = in
	return V6TeamMember{AgentID: in.AgentID}, nil
}
func (s *teamV6StoreStub) ArchiveV6TeamMember(_ context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	s.archived = in
	return V6TeamMember{ID: in.MembershipID, State: V6TeamArchived}, nil
}

func (s *teamV6StoreStub) FindActiveV6TeamMemberByAgent(context.Context, string, string, string) (V6TeamMember, bool, error) {
	return V6TeamMember{}, false, nil
}

func TestV6TeamCapacityRules(t *testing.T) {
	store := &teamV6StoreStub{}
	_, err := (teamV6Module{store: store}).Add(context.Background(), AddV6TeamMemberInput{
		WorkspaceID: "workspace", RunID: "run", AgentID: "agent", MissionPrompt: "Investigate the assigned question.",
	})
	if err != nil || store.added.AgentID != "agent" {
		t.Fatalf("add member: input=%+v err=%v", store.added, err)
	}
}

func TestV6TeamArchiveRequiresReason(t *testing.T) {
	_, err := (teamV6Module{store: &teamV6StoreStub{}}).Archive(context.Background(), ArchiveV6TeamMemberInput{MembershipID: "membership"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

type directorV6StoreStub struct{}

func (directorV6StoreStub) AssignV6Director(_ context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{AgentID: in.AgentID}, nil
}
func (directorV6StoreStub) MarkV6DirectorUnavailable(_ context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{ID: in.AssignmentID, Status: "unavailable"}, nil
}

func TestV6DirectorAssignmentValidation(t *testing.T) {
	_, err := (directorModule{store: directorV6StoreStub{}}).Assign(context.Background(), AssignV6DirectorInput{AgentID: "agent", UserID: "user"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

func TestV6DirectorFailureValidation(t *testing.T) {
	got, err := (directorModule{store: directorV6StoreStub{}}).MarkUnavailable(context.Background(), MarkV6DirectorUnavailableInput{AssignmentID: "assignment", FailureClass: "quota", ClientRequestID: "request"})
	if err != nil || got.Status != "unavailable" {
		t.Fatalf("assignment=%+v err=%v", got, err)
	}
}

func TestV6DirectorRunLifecycleRejectsAutonomousPause(t *testing.T) {
	if target, allowed := v6DirectorRunLifecycleTarget("pause_run"); allowed || target != "" {
		t.Fatalf("pause_run target=%q allowed=%v", target, allowed)
	}
	if target, allowed := v6DirectorRunLifecycleTarget("resume_run"); !allowed || target != "running" {
		t.Fatalf("resume_run target=%q allowed=%v", target, allowed)
	}
}

func TestV6DirectorBriefIsBoundedAndPaged(t *testing.T) {
	branches := make([]any, 257)
	for i := range branches {
		branches[i] = map[string]any{
			"branch":    map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), "state_version": 1},
			"objective": "Investigate", "scope": map[string]any{}, "status": "active", "frontier_nodes": []any{}, "has_more": false,
		}
	}
	facts := DirectorBriefFacts{
		WorkspaceID: "00000000-0000-4000-8000-000000000001", RunID: "00000000-0000-4000-8000-000000000002",
		AssignmentID: "00000000-0000-4000-8000-000000000003", DirectorGeneration: 1, StateVersion: 1,
		Goal:          map[string]any{"goal_version": 1, "goal": "Research", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		DirectorState: "available", Team: []any{map[string]any{"agent_id": "00000000-0000-4000-8000-000000000004", "membership_id": "00000000-0000-4000-8000-000000000005", "state": "idle", "mission_summary": "Direct"}}, Branches: branches,
		TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{}, UnresolvedDisputes: []any{}, Steering: []any{},
	}
	brief, err := (contextCompilerModule{}).CompileDirectorBrief(facts, time.Unix(1, 0))
	if err != nil || len(brief.Pages) != 5 {
		t.Fatalf("pages=%d err=%v", len(brief.Pages), err)
	}
}

func TestV6DirectorBriefBoundsTeamMissionSummary(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Bound V6 team mission summary")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Compile a bounded team summary", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	longMission := strings.Repeat("研究", 300)
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_team_membership SET mission_prompt=$2 WHERE session_id=$1::uuid`, run.fixture.sessionID, longMission); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	facts, err := run.store.LoadDirectorBriefFacts(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, ExpectedStateVersion: stateVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Team) != 1 {
		t.Fatalf("team members=%d, want 1", len(facts.Team))
	}
	mission, ok := facts.Team[0].(map[string]any)["mission_summary"].(string)
	if !ok || len([]rune(mission)) != 512 {
		t.Fatalf("mission summary rune count=%d, want 512", len([]rune(mission)))
	}
	if _, err = (contextCompilerModule{}).CompileDirectorBrief(facts, time.Unix(1, 0)); err != nil {
		t.Fatalf("compile Director Brief with bounded team mission: %v", err)
	}
}

func TestV6DirectorBriefIncludesWorkFailureRecoveryFacts(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 Director Brief work failure recovery")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Recover failed work", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var membershipID string
	if err := run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid`, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	workItemID := uuid.NewString()
	mission := "独立复核关键来源并解释冲突。"
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item (
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,state_version,reason,attempt_count,max_attempts,terminal_reason_code,terminal_reason_detail
	) VALUES ($1::uuid,$2::uuid,$3::uuid,'research','failed',$4::uuid,1,$5,'research.finding.v1',1,$6,3,3,'attempt_budget_exhausted','连续提交未通过合同校验。')`,
		workItemID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID, "brief-failure:"+workItemID, mission); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET status='failed',failure_class='contract_rejected',diagnostics='content_layers.conclusion is required',completed_at=now() WHERE id=$1::uuid`, attemptID); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	facts, err := run.store.LoadDirectorBriefFacts(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, ExpectedStateVersion: stateVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	var failed map[string]any
	for _, raw := range facts.WorkItems {
		item := raw.(map[string]any)
		if item["id"] == workItemID {
			failed = item
			break
		}
	}
	if failed == nil {
		t.Fatal("failed Work Item missing from Director Brief")
	}
	summary, ok := failed["summary"].(string)
	if !ok {
		t.Fatalf("summary=%T, want string", failed["summary"])
	}
	for _, want := range []string{mission, "尝试 3/3", "最近尝试状态 failed", "失败分类 contract_rejected", "失败诊断 content_layers.conclusion is required", "终止原因 attempt_budget_exhausted", "连续提交未通过合同校验。"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	if _, err = (contextCompilerModule{}).CompileDirectorBrief(facts, time.Unix(1, 0)); err != nil {
		t.Fatalf("compile Director Brief with failure recovery facts: %v", err)
	}
}

func TestV6DirectorBriefIncludesAtomicResultFrontier(t *testing.T) {
	run := newTransactionRecoveryRun(t, "V6 Director Brief atomic frontier")
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	branchID, resultArtifactID, resultNodeID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	contentHash := "sha256:" + strings.Repeat("a", 64)
	clientRequestID := uuid.NewString()

	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_branch(id,workspace_id,session_id,client_key,objective,status,goal_version,state_version)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,'Investigate the source landscape','active',1,1)`, branchID, run.fixture.workspaceID, run.fixture.sessionID, "brief:"+branchID); err != nil {
		t.Fatal(err)
	}
	if err = registerV6BranchArtifactTx(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID, branchID, time.Now().UTC(), 1, map[string]any{
		"parent_branch_id": "", "objective": "Investigate the source landscape", "entry_conditions": json.RawMessage(`[]`),
		"exit_conditions": json.RawMessage(`[]`), "budget_share": 0.0, "status": "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err = registerArtifactPassportTx(run.ctx, tx, registerArtifactPassportInput{
		WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, EntityID: resultArtifactID,
		Kind: ArtifactKindResultArtifact, ProvenanceCompleteness: ArtifactProvenanceComplete,
		SchemaVersion: "research-run-v6", ContentHash: contentHash,
		AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
	}); err != nil {
		t.Fatal(err)
	}
	var artifactVersionID string
	if err = tx.QueryRow(run.ctx, `SELECT id::text FROM research_artifact_version WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=1`, run.fixture.workspaceID, run.fixture.sessionID, resultArtifactID).Scan(&artifactVersionID); err != nil {
		t.Fatal(err)
	}
	var manifestID, manifestHash string
	if err = tx.QueryRow(run.ctx, `SELECT manifest_id::text,manifest_hash FROM research_work_item_attempt WHERE id=$1::uuid`, attemptID).Scan(&manifestID, &manifestHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `UPDATE research_work_item_attempt SET result_kind='result_node',result_entity_id=$2::uuid,result_artifact_id=$3::uuid,result_hash=$4,client_request_id=$5::uuid,result_submitted_at=now() WHERE id=$1::uuid`, attemptID, resultNodeID, resultArtifactID, contentHash, clientRequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_result_artifact(id,workspace_id,session_id,attempt_id,work_item_attempt_id,orchestrator_version,result_schema_version,result,client_request_id,content_hash,accepted_at,acceptance_work_manifest_id,acceptance_work_manifest_hash,resolved_input_versions_v6,acceptance_lineage_v6)
		VALUES($1::uuid,$2::uuid,$3::uuid,NULL,$4::uuid,$5,'6','{}'::jsonb,$6,$7,now(),$8::uuid,$9,'[]'::jsonb,'[]'::jsonb)`, resultArtifactID, run.fixture.workspaceID, run.fixture.sessionID, attemptID, OrchestratorVersionV6, clientRequestID, contentHash, manifestID, manifestHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_result_node(id,workspace_id,session_id,result_artifact_id,artifact_version_id,work_item_attempt_id,
		catalog_summary,brief_summary,objective,conclusion,content,open_questions,conclusion_state,integration_state,content_hash)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'Source landscape','Ten candidate sources found','Find sources','Candidates found','Result content','["Which candidates have independent verification?","What changed after the latest release?"]'::jsonb,'accepted','unmatched',$7)`,
		resultNodeID, run.fixture.workspaceID, run.fixture.sessionID, resultArtifactID, artifactVersionID, attemptID, contentHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_node_branch(workspace_id,session_id,node_artifact_version_id,branch_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, run.fixture.workspaceID, run.fixture.sessionID, artifactVersionID, branchID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_branch_frontier(workspace_id,session_id,branch_id,node_artifact_version_id,tier,added_by_event_sequence)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'S',1)`, run.fixture.workspaceID, run.fixture.sessionID, branchID, artifactVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_node_steward_assignment(workspace_id,session_id,node_artifact_version_id,agent_id,membership_id,generation,status,reason)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,'active','accepted_result_owner')`, run.fixture.workspaceID, run.fixture.sessionID, artifactVersionID, run.fixture.agentID, membershipID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}

	frontier, hasMore, err := run.store.loadV6BranchFrontierBrief(run.ctx, run.fixture.workspaceID, run.fixture.sessionID, branchID)
	if err != nil || hasMore || len(frontier) != 1 {
		t.Fatalf("frontier=%+v hasMore=%v err=%v", frontier, hasMore, err)
	}
	item := frontier[0].(map[string]any)
	node := item["node"].(map[string]any)
	if node["kind"] != "result_s" || node["tier"] != "S" || node["id"] != resultArtifactID || node["version_id"] != artifactVersionID {
		t.Fatalf("atomic frontier node=%+v", node)
	}
	if _, legacyRevision := node["revision"]; legacyRevision {
		t.Fatalf("atomic frontier node contains non-contract revision: %+v", node)
	}
	briefSummary, ok := item["brief_summary"].(string)
	if !ok {
		t.Fatalf("atomic frontier brief_summary=%T, want string", item["brief_summary"])
	}
	for _, question := range []string{"Which candidates have independent verification?", "What changed after the latest release?"} {
		if !strings.Contains(briefSummary, question) {
			t.Fatalf("atomic frontier brief summary missing open question %q: %s", question, briefSummary)
		}
	}

	_, err = (contextCompilerModule{}).CompileDirectorBrief(DirectorBriefFacts{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AssignmentID: uuid.NewString(), DirectorGeneration: 1, StateVersion: 1, ThroughSequence: 1,
		Goal:          map[string]any{"goal_version": 1, "goal": "Research", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		DirectorState: "available", Team: []any{map[string]any{"agent_id": run.fixture.agentID, "membership_id": membershipID, "state": "idle", "mission_summary": "Direct"}},
		Branches:          []any{map[string]any{"branch": map[string]any{"id": branchID, "state_version": 1}, "objective": "Investigate the source landscape", "scope": map[string]any{}, "status": "active", "frontier_nodes": frontier, "has_more": false}},
		TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{}, UnresolvedDisputes: []any{}, Steering: []any{},
	}, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("compile Director Brief with atomic frontier: %v", err)
	}
}

func TestDirectorBriefFrontierSummaryBoundsOpenQuestions(t *testing.T) {
	questions := make([]string, 128)
	for index := range questions {
		questions[index] = strings.Repeat("问题", 300)
	}
	raw, err := json.Marshal(questions)
	if err != nil {
		t.Fatal(err)
	}
	summary := directorBriefFrontierSummary("基础摘要", raw)
	if len([]rune(summary)) > 32768 {
		t.Fatalf("summary length=%d, want <=32768", len([]rune(summary)))
	}
	if !strings.Contains(summary, "待回答问题") || !strings.Contains(summary, "问题") {
		t.Fatalf("summary does not expose open questions: %s", summary)
	}
}

type directorBriefStoreStub struct {
	acknowledged AcknowledgeV6DirectorBriefInput
}

func (*directorBriefStoreStub) LoadDirectorBriefFacts(context.Context, StartV6DirectorCycleInput) (DirectorBriefFacts, error) {
	return DirectorBriefFacts{}, nil
}
func (*directorBriefStoreStub) PersistDirectorCycle(context.Context, StartV6DirectorCycleInput, CompiledDirectorBrief) (V6DirectorCycle, error) {
	return V6DirectorCycle{}, nil
}
func (*directorBriefStoreStub) LoadDirectorBriefPage(context.Context, V6AttemptAccess, string) (V6DirectorBriefPage, error) {
	return V6DirectorBriefPage{}, nil
}
func (s *directorBriefStoreStub) AcknowledgeDirectorBriefPage(_ context.Context, in AcknowledgeV6DirectorBriefInput) error {
	s.acknowledged = in
	return nil
}

func TestV6DirectorBriefAcknowledgementDelegates(t *testing.T) {
	store := &directorBriefStoreStub{}
	in := AcknowledgeV6DirectorBriefInput{ClientRequestID: "request", PageKey: "page"}
	if err := (directorBriefModule{store: store}).Acknowledge(context.Background(), in); err != nil || store.acknowledged.PageKey != "page" {
		t.Fatalf("ack=%+v err=%v", store.acknowledged, err)
	}
}

func TestReviewedV6DirectorBriefCanBeReadAgainForRetry(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Re-read acknowledged Director Brief")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Run the retry readability test", ClientRequestID: uuid.NewString(),
		ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}

	var stateVersion, throughSequence int64
	if err := run.pool.QueryRow(run.ctx, `
		SELECT state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid
	`, run.fixture.sessionID).Scan(&stateVersion, &throughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: throughSequence, ThroughSequence: throughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var membershipID string
	if err = run.pool.QueryRow(run.ctx, `
		SELECT id::text FROM research_team_membership
		WHERE session_id=$1::uuid AND agent_id=$2::uuid AND state='idle'
		ORDER BY membership_generation DESC LIMIT 1
	`, run.fixture.sessionID, run.fixture.agentID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, cycle.WorkItemID)
	access := V6AttemptAccess{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		WorkItemID: cycle.WorkItemID, AttemptID: attemptID, AgentID: run.fixture.agentID,
	}
	page, err := run.store.LoadDirectorBriefPage(run.ctx, access, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Reviewed {
		t.Fatal("new Director Brief page is already reviewed")
	}
	if err = run.store.AcknowledgeDirectorBriefPage(run.ctx, AcknowledgeV6DirectorBriefInput{
		V6AttemptAccess: access, ClientRequestID: uuid.NewString(),
		BriefID: cycle.BriefID, BriefHash: cycle.BriefHash,
		PageKey: page.PageKey, PageHash: page.PageHash,
	}); err != nil {
		t.Fatal(err)
	}

	replayed, err := run.store.LoadDirectorBriefPage(run.ctx, access, "")
	if err != nil {
		t.Fatalf("re-read acknowledged Director Brief: %v", err)
	}
	if !replayed.Reviewed || replayed.PageKey != page.PageKey || string(replayed.Bytes) != string(page.Bytes) {
		t.Fatalf("replayed page = reviewed:%v key:%q, want reviewed page %q", replayed.Reviewed, replayed.PageKey, page.PageKey)
	}
}

func TestV6EventTriggerWaitsForMaterialRuntimeEffects(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Wait for V6 material runtime effects")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Run the material effect ordering test", ClientRequestID: uuid.NewString(),
		ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	event, err := appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_agent_creation_requested", "event-trigger-material-effect:"+uuid.NewString(), "system", "",
		map[string]any{"name": "source scout"})
	if err != nil {
		_ = tx.Rollback(run.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	outboxID := uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `
		INSERT INTO research_v6_outbox(id,workspace_id,session_id,kind,idempotency_key,payload)
		VALUES($1::uuid,$2::uuid,$3::uuid,'create_agent',$4,'{}'::jsonb)
	`, outboxID, run.fixture.workspaceID, run.fixture.sessionID, "material-effect:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	processed, err := run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 {
		t.Fatalf("processed %d event triggers while material outbox was pending", processed)
	}
	var cycles int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, run.fixture.sessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 0 {
		t.Fatalf("created %d Director cycles before material runtime effects settled", cycles)
	}

	if _, err = run.pool.Exec(run.ctx, `UPDATE research_v6_outbox SET status='delivered',updated_at=now() WHERE id=$1::uuid`, outboxID); err != nil {
		t.Fatal(err)
	}
	processed, err = run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed %d event triggers after material outbox settled, want 1", processed)
	}
	var throughSequence int64
	if err = run.pool.QueryRow(run.ctx, `
		SELECT trigger_through_sequence FROM research_director_cycle
		WHERE session_id=$1::uuid ORDER BY created_at DESC LIMIT 1
	`, run.fixture.sessionID).Scan(&throughSequence); err != nil {
		t.Fatal(err)
	}
	if throughSequence < event.Sequence {
		t.Fatalf("Director Brief watermark %d does not include material request event %d", throughSequence, event.Sequence)
	}
}

func TestV6EventTriggerRecoversBootstrapWithoutDirectorCycle(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Recover V6 bootstrap without Director cycle")
	title := "Recover bootstrap " + uuid.NewString()
	bootstrapped, _, err := run.store.BootstrapV6(run.ctx, V6BootstrapInput{
		WorkspaceID:     run.fixture.workspaceID,
		CreatedBy:       run.fixture.userID,
		DirectorAgentID: run.fixture.agentID,
		Goal:            title,
		Title:           title,
		DepthTier:       "standard",
		Language:        "Simplified Chinese",
		ClientRequestID: uuid.NewString(),
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}

	var cycles int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, bootstrapped.SessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 0 {
		t.Fatalf("bootstrap unexpectedly created %d Director cycles", cycles)
	}

	processed, err := run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed %d bootstrap triggers, want 1", processed)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, bootstrapped.SessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 1 {
		t.Fatalf("bootstrap recovery created %d Director cycles, want 1", cycles)
	}
	var triggerType string
	if err = run.pool.QueryRow(run.ctx, `
		SELECT event.event_type
		FROM research_director_cycle cycle
		JOIN research_run_event event
		  ON event.session_id=cycle.session_id AND event.sequence=cycle.trigger_from_sequence
		WHERE cycle.session_id=$1::uuid
	`, bootstrapped.SessionID).Scan(&triggerType); err != nil {
		t.Fatal(err)
	}
	if triggerType != "v6_run_bootstrapped" {
		t.Fatalf("bootstrap recovery used trigger %q, want v6_run_bootstrapped", triggerType)
	}
}

func TestV6EventTriggerRepairsAtomicResultMissingFromCoveredBrief(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Repair covered V6 atomic frontier")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Repair omitted atomic frontier", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var membershipID string
	if err := run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_team_membership WHERE session_id=$1::uuid AND agent_id=$2::uuid AND state='idle'`, run.fixture.sessionID, run.fixture.agentID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	workItemID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,lease_token,lease_expires_at,payload_schema_id,state_version)
		VALUES($1::uuid,$2::uuid,$3::uuid,'research','running',$4::uuid,1,$5,$6::uuid,now()+interval '1 minute','schema',1)`,
		workItemID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID, "frontier-repair:"+workItemID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	resultArtifactID, resultNodeID := uuid.NewString(), uuid.NewString()
	contentHash := "sha256:" + strings.Repeat("b", 64)
	clientRequestID := uuid.NewString()
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if err = registerArtifactPassportTx(run.ctx, tx, registerArtifactPassportInput{
		WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, EntityID: resultArtifactID,
		Kind: ArtifactKindResultArtifact, ProvenanceCompleteness: ArtifactProvenanceComplete,
		SchemaVersion: "research-run-v6", ContentHash: contentHash,
		AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
	}); err != nil {
		t.Fatal(err)
	}
	var artifactVersionID string
	if err = tx.QueryRow(run.ctx, `SELECT id::text FROM research_artifact_version WHERE artifact_id=$1::uuid AND version=1`, resultArtifactID).Scan(&artifactVersionID); err != nil {
		t.Fatal(err)
	}
	var manifestID, manifestHash string
	if err = tx.QueryRow(run.ctx, `SELECT manifest_id::text,manifest_hash FROM research_work_item_attempt WHERE id=$1::uuid`, attemptID).Scan(&manifestID, &manifestHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `UPDATE research_work_item_attempt SET result_kind='result_node',result_entity_id=$2::uuid,result_artifact_id=$3::uuid,result_hash=$4,client_request_id=$5::uuid,result_submitted_at=now() WHERE id=$1::uuid`, attemptID, resultNodeID, resultArtifactID, contentHash, clientRequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_result_artifact(id,workspace_id,session_id,attempt_id,work_item_attempt_id,orchestrator_version,result_schema_version,result,client_request_id,content_hash,accepted_at,acceptance_work_manifest_id,acceptance_work_manifest_hash,resolved_input_versions_v6,acceptance_lineage_v6)
		VALUES($1::uuid,$2::uuid,$3::uuid,NULL,$4::uuid,$5,'6','{}'::jsonb,$6,$7,now(),$8::uuid,$9,'[]'::jsonb,'[]'::jsonb)`, resultArtifactID, run.fixture.workspaceID, run.fixture.sessionID, attemptID, OrchestratorVersionV6, clientRequestID, contentHash, manifestID, manifestHash); err != nil {
		t.Fatal(err)
	}
	resultEvent, err := appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_result_node_accepted", "covered-without-frontier:"+uuid.NewString(), "agent", run.fixture.agentID,
		map[string]any{"artifact_version_id": artifactVersionID, "result_artifact_id": resultArtifactID, "result_node_id": resultNodeID, "work_item_id": workItemID})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}

	processed, err := run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil || processed != 1 {
		t.Fatalf("initial event trigger processed=%d err=%v", processed, err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now() WHERE session_id=$1::uuid AND kind='director'`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_director_cycle SET status='applied',completed_at=now() WHERE session_id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}

	branchID := uuid.NewString()
	materializeTx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer materializeTx.Rollback(run.ctx)
	if _, err = materializeTx.Exec(run.ctx, `INSERT INTO research_branch(id,workspace_id,session_id,client_key,objective,status,goal_version,state_version)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,'Investigate the source landscape','active',1,1)`, branchID, run.fixture.workspaceID, run.fixture.sessionID, "repair:"+branchID); err != nil {
		t.Fatal(err)
	}
	if err = registerV6BranchArtifactTx(run.ctx, materializeTx, run.fixture.workspaceID, run.fixture.sessionID, branchID, time.Now().UTC(), 1, map[string]any{
		"parent_branch_id": "", "objective": "Investigate the source landscape", "entry_conditions": json.RawMessage(`[]`),
		"exit_conditions": json.RawMessage(`[]`), "budget_share": 0.0, "status": "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = materializeTx.Exec(run.ctx, `INSERT INTO research_result_node(id,workspace_id,session_id,result_artifact_id,artifact_version_id,work_item_attempt_id,
		catalog_summary,brief_summary,objective,conclusion,content,conclusion_state,integration_state,open_questions,content_hash)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'Source landscape','Candidates found','Find sources','Candidates found','Result content','accepted','unmatched',jsonb_build_array('Which unresolved market constraint matters?'),$7)`, resultNodeID, run.fixture.workspaceID, run.fixture.sessionID, resultArtifactID, artifactVersionID, attemptID, contentHash); err != nil {
		t.Fatal(err)
	}
	if _, err = materializeTx.Exec(run.ctx, `INSERT INTO research_node_branch(workspace_id,session_id,node_artifact_version_id,branch_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, run.fixture.workspaceID, run.fixture.sessionID, artifactVersionID, branchID); err != nil {
		t.Fatal(err)
	}
	if _, err = materializeTx.Exec(run.ctx, `INSERT INTO research_branch_frontier(workspace_id,session_id,branch_id,node_artifact_version_id,tier,added_by_event_sequence) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'S',$5)`, run.fixture.workspaceID, run.fixture.sessionID, branchID, artifactVersionID, resultEvent.Sequence); err != nil {
		t.Fatal(err)
	}
	if _, err = materializeTx.Exec(run.ctx, `INSERT INTO research_node_steward_assignment(workspace_id,session_id,node_artifact_version_id,agent_id,membership_id,generation,status,reason) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,'active','accepted_result_owner')`, run.fixture.workspaceID, run.fixture.sessionID, artifactVersionID, run.fixture.agentID, membershipID); err != nil {
		t.Fatal(err)
	}
	if err = materializeTx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}

	processed, err = run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil || processed != 1 {
		t.Fatalf("frontier repair trigger processed=%d err=%v", processed, err)
	}
	var cycles int
	var latestBrief string
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, run.fixture.sessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT convert_from(p.content_bytes,'UTF8') FROM research_director_brief_page p JOIN research_director_cycle c ON c.id=p.director_cycle_id WHERE p.session_id=$1::uuid ORDER BY c.created_at DESC,p.ordinal LIMIT 1`, run.fixture.sessionID).Scan(&latestBrief); err != nil {
		t.Fatal(err)
	}
	if cycles != 2 || !strings.Contains(latestBrief, artifactVersionID) || !strings.Contains(latestBrief, `"kind":"result_s"`) || !strings.Contains(latestBrief, "Which unresolved market constraint matters?") {
		t.Fatalf("repair cycles=%d brief contains node=%v kind=%v question=%v", cycles, strings.Contains(latestBrief, artifactVersionID), strings.Contains(latestBrief, `"kind":"result_s"`), strings.Contains(latestBrief, "Which unresolved market constraint matters?"))
	}

	// A brief compiled before open-question propagation could contain the Result
	// frontier node while omitting its unresolved questions. Re-emit the material
	// event, simulate that historical page shape, and verify reconciliation creates
	// one fresh cycle whose brief restores the questions.
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now() WHERE session_id=$1::uuid AND kind='director'`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_director_cycle SET status='applied',completed_at=now() WHERE session_id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	duplicateTx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	duplicateEvent, err := appendEvent(run.ctx, duplicateTx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_result_node_accepted", "covered-without-questions:"+uuid.NewString(), "agent", run.fixture.agentID,
		map[string]any{"artifact_version_id": artifactVersionID, "result_artifact_id": resultArtifactID, "result_node_id": resultNodeID, "work_item_id": workItemID})
	if err != nil {
		_ = duplicateTx.Rollback(run.ctx)
		t.Fatal(err)
	}
	if err = duplicateTx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	processed, err = run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil || processed != 1 {
		t.Fatalf("historical cycle setup processed=%d err=%v", processed, err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now() WHERE session_id=$1::uuid AND kind='director'`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_director_cycle SET status='applied',completed_at=now() WHERE session_id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var historicalCycleID string
	if err = run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_director_cycle WHERE session_id=$1::uuid AND trigger_from_sequence=$2 ORDER BY created_at DESC LIMIT 1`, run.fixture.sessionID, duplicateEvent.Sequence).Scan(&historicalCycleID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_director_brief_page SET content_bytes=convert_to(replace(convert_from(content_bytes,'UTF8'),$2,''),'UTF8') WHERE director_cycle_id=$1::uuid`, historicalCycleID, directorBriefOpenQuestionsMarker); err != nil {
		t.Fatal(err)
	}
	processed, err = run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil || processed != 1 {
		t.Fatalf("open-question repair trigger processed=%d err=%v", processed, err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, run.fixture.sessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT convert_from(p.content_bytes,'UTF8') FROM research_director_brief_page p JOIN research_director_cycle c ON c.id=p.director_cycle_id WHERE p.session_id=$1::uuid ORDER BY c.created_at DESC,p.ordinal LIMIT 1`, run.fixture.sessionID).Scan(&latestBrief); err != nil {
		t.Fatal(err)
	}
	if cycles != 4 || !strings.Contains(latestBrief, directorBriefOpenQuestionsMarker) || !strings.Contains(latestBrief, "Which unresolved market constraint matters?") {
		t.Fatalf("open-question repair cycles=%d marker=%v question=%v", cycles, strings.Contains(latestBrief, directorBriefOpenQuestionsMarker), strings.Contains(latestBrief, "Which unresolved market constraint matters?"))
	}
}

func TestRejectedV6DirectorProposalTerminatesAndEmitsTrigger(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Reject stale V6 Director proposal")
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item
		SET kind='director',client_key=$2 WHERE id=$1::uuid`, workItemID, "director-cycle:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	submissionID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_submission(
		id,workspace_id,session_id,work_item_id,attempt_id,client_request_id,contract_kind,content_hash,envelope,status
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'director_action_proposal',$7,'{}'::jsonb,'processing')`,
		submissionID, run.fixture.workspaceID, run.fixture.sessionID, workItemID, attemptID, uuid.NewString(),
		"sha256:"+strings.Repeat("7", 64)); err != nil {
		t.Fatal(err)
	}

	reason := ErrWorkItemChanged.Error()
	if err := run.store.rejectV6DirectorProposal(run.ctx, submissionID, reason); err != nil {
		t.Fatal(err)
	}
	var submissionStatus, outcomeError, attemptStatus, workStatus string
	if err := run.pool.QueryRow(run.ctx, `SELECT s.status,s.outcome->>'error',a.status,w.status
		FROM research_v6_work_submission s
		JOIN research_work_item_attempt a ON a.id=s.attempt_id
		JOIN research_work_item w ON w.id=s.work_item_id
		WHERE s.id=$1::uuid`, submissionID).Scan(&submissionStatus, &outcomeError, &attemptStatus, &workStatus); err != nil {
		t.Fatal(err)
	}
	if submissionStatus != "rejected" || outcomeError != reason || attemptStatus != "failed" || workStatus != "failed" {
		t.Fatalf("submission=%s outcome=%q attempt=%s work=%s", submissionStatus, outcomeError, attemptStatus, workStatus)
	}
	var rejectedEvents int
	if err := run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_run_event
		WHERE session_id=$1::uuid AND event_type='v6_work_submission_rejected'
		AND payload->>'submission_id'=$2::text`, run.fixture.sessionID, submissionID).Scan(&rejectedEvents); err != nil {
		t.Fatal(err)
	}
	if rejectedEvents != 1 {
		t.Fatalf("rejection events=%d want 1", rejectedEvents)
	}
}

func TestV6DirectorCreatedWorkStartsAtVersionOne(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Create versioned V6 Work")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Create versioned work", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Investigate the assigned question",
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion, throughSequence int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version,
		COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion, &throughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: throughSequence, ThroughSequence: throughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	branchID := seedV6DirectorChildBranch(t, run, "create-versioned-work:", "Investigate the assigned question", 1)
	actionPayload, err := json.Marshal(map[string]any{
		"kind": "deep_read", "assignee_agent_id": run.fixture.reporterID, "mission": "Investigate the assigned question",
		"expected_result_schema_id": "atomic_result_submission", "payload_schema_id": "research.test.v1",
		"payload":  map[string]any{"task_specific_schema": map[string]any{"type": "object"}},
		"priority": 0.5, "max_attempts": 1, "branch_ids": []string{branchID},
	})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "create-versioned-work:" + uuid.NewString()
	if err = run.store.executeV6CreateWorkAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, cycle.ID, v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_work_item", IdempotencyKey: idempotencyKey,
		PayloadSchema: "work.create.v1", Payload: actionPayload,
	}, stateVersion); err != nil {
		t.Fatal(err)
	}
	var workStateVersion int64
	var persistedKind, taskKind string
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version,kind,payload->>'task_kind' FROM research_work_item
		WHERE session_id=$1::uuid AND idempotency_key=$2`, run.fixture.sessionID, idempotencyKey).Scan(&workStateVersion, &persistedKind, &taskKind); err != nil {
		t.Fatal(err)
	}
	if workStateVersion != 1 {
		t.Fatalf("created Work state_version=%d want 1", workStateVersion)
	}
	if persistedKind != "research" || taskKind != "deep_read" {
		t.Fatalf("created Work kind=%q task_kind=%q want research/deep_read", persistedKind, taskKind)
	}
}

func TestV6DirectorRejectsWorkWhoseBranchIsOutsideTheRun(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Reject invalid V6 Work branch scope")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Validate Work branch scope", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Investigate the assigned question",
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "invalid-branch-work:" + uuid.NewString()
	payload, err := json.Marshal(map[string]any{
		"kind": "research", "assignee_agent_id": run.fixture.reporterID,
		"mission": "Investigate the assigned question", "expected_result_schema_id": "atomic_result_submission",
		"payload_schema_id": "research.test.v1", "payload": map[string]any{"task_specific_schema": map[string]any{"type": "object"}},
		"priority": 0.5, "max_attempts": 1, "branch_ids": []string{uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = run.store.executeV6CreateWorkAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, uuid.NewString(), v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_work_item", IdempotencyKey: idempotencyKey,
		PayloadSchema: "work.create.v1", Payload: payload,
	}, stateVersion)
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "branch_ids") {
		t.Fatalf("invalid branch scope error=%v", err)
	}
	var created int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_work_item
		WHERE session_id=$1::uuid AND idempotency_key=$2`, run.fixture.sessionID, idempotencyKey).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("created invalid branch-scoped Work=%d want 0", created)
	}
}

func TestV6DirectorRejectsUnusableAtomicWorkSchemaWithActionableDiagnostic(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Reject unusable V6 atomic Work schema")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Coordinate atomic research", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Research Manus technology",
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"kind": "research", "assignee_agent_id": run.fixture.reporterID,
		"mission": "Research Manus technology", "expected_result_schema_id": "atomic_result_submission",
		"payload_schema_id": "no_op.v1", "payload": map[string]any{"reason": "technology"},
		"priority": 0.9, "max_attempts": 2, "branch_ids": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = run.store.executeV6CreateWorkAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, uuid.NewString(), v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_work_item", IdempotencyKey: "invalid-atomic:" + uuid.NewString(),
		PayloadSchema: "collaboration.create.v1", Payload: payload,
	}, stateVersion)
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "no_op.v1") || !strings.Contains(err.Error(), "payload.task_specific_schema") {
		t.Fatalf("invalid atomic Work error=%v, want actionable schema diagnostic", err)
	}
}

func TestV6DirectorCannotNoOpAfterRejectedAssignmentWithIdleWorkers(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Recover rejected V6 Agent assignment")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Recover rejected team assignment", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Research Manus technology",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,expected_result_schema_id,payload,state_version,terminal_reason_code,terminal_reason_detail
	) VALUES($1::uuid,$2::uuid,$3::uuid,'director','failed',$4::uuid,1,$5,
		'director.action.registry.v1','director_action_proposal','{}'::jsonb,1,'contract_rejected','atomic Work schema was invalid')`,
		uuid.NewString(), run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID,
		"rejected-assignment:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	action := v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "no_op", IdempotencyKey: "invalid-no-op:" + uuid.NewString(),
		PayloadSchema: "no_op.v1", Reason: "Wait for the next brief",
	}
	err := run.store.recordV6DirectorNoOp(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, uuid.NewString(), action, stateVersion, action.Reason)
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "assign Work to an idle run-scoped Agent") {
		t.Fatalf("post-rejection no-op error=%v, want assignment recovery requirement", err)
	}
}

func TestV6DirectorCannotNoOpWithFailedWorkerWork(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Recover failed V6 Agent Work")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Recover failed Agent Work", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "Research Manus technology",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,expected_result_schema_id,payload,state_version,terminal_reason_code
	) VALUES($1::uuid,$2::uuid,$3::uuid,'research','failed',$4::uuid,1,$5,
		'research.manus.v1','atomic_result_submission','{}'::jsonb,1,'attempt_budget_exhausted')`,
		uuid.NewString(), run.fixture.workspaceID, run.fixture.sessionID, run.fixture.reporterID,
		"failed-worker:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	action := v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "no_op", IdempotencyKey: "failed-worker-no-op:" + uuid.NewString(),
		PayloadSchema: "no_op.v1", Reason: "Wait for the next brief",
	}
	err := run.store.recordV6DirectorNoOp(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, uuid.NewString(), action, stateVersion, action.Reason)
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "retry or reassign failed Work") {
		t.Fatalf("failed-worker no-op error=%v, want recovery requirement", err)
	}
}
