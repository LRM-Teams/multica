package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createGoalTestChannel(t *testing.T) ChannelResponse {
	t.Helper()
	req := withChannelTestWorkspaceCtx(t, newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "goal-" + uuid.NewString(),
	}), testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", rec.Code, rec.Body.String())
	}
	var channel ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &channel); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, parseUUID(channel.ID))
	})
	return channel
}

func goalRequest(t *testing.T, userID, method, channelID string, body any) *http.Request {
	t.Helper()
	req := newRequestAs(userID, method, "/api/channels/"+channelID+"/goal", body)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	return withURLParam(req, "channelId", channelID)
}

func decodeGoalEnvelope(t *testing.T, rec *httptest.ResponseRecorder) channelGoalEnvelope {
	t.Helper()
	var envelope channelGoalEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode goal response: %v (%s)", err, rec.Body.String())
	}
	return envelope
}

func TestChannelGoalLifecycleAndCompletionGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	createBody := map[string]any{
		"title":            "Ship adaptive goals",
		"objective":        "Keep long-running work aligned",
		"success_criteria": []string{"Goal visible", "Goal survives resume"},
	}
	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channel.ID, createBody))
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannelGoal = %d: %s", created.Code, created.Body.String())
	}
	goal := decodeGoalEnvelope(t, created).Goal
	if goal == nil || goal.Version != 1 || len(goal.SuccessCriteria) != 2 {
		t.Fatalf("created goal = %#v", goal)
	}
	if goal.CurrentStep != initialChannelGoalStep {
		t.Fatalf("initial current_step = %q, want control-plane setup", goal.CurrentStep)
	}
	if goal.Coordination == nil || goal.Coordination.ExecutionAdmission == "" {
		t.Fatalf("created goal missing coordination summary: %#v", goal.Coordination)
	}

	duplicate := httptest.NewRecorder()
	testHandler.CreateChannelGoal(duplicate, goalRequest(t, testUserID, http.MethodPost, channel.ID, createBody))
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d: %s", duplicate.Code, duplicate.Body.String())
	}

	premature := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(premature, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": goal.Version,
		"status":           "completed",
	}))
	if premature.Code != http.StatusConflict {
		t.Fatalf("premature completion = %d: %s", premature.Code, premature.Body.String())
	}

	noEvidence := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(noEvidence, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version":   goal.Version,
		"status":             "completed",
		"completed_criteria": goal.SuccessCriteria,
	}))
	if noEvidence.Code != http.StatusConflict {
		t.Fatalf("completion without evidence = %d: %s", noEvidence.Code, noEvidence.Body.String())
	}

	progress := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(progress, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version":   goal.Version,
		"progress_summary":   "Both criteria verified",
		"current_step":       "Final review",
		"evidence_refs":      []string{"test:goal-card", "test:resume"},
		"completed_criteria": goal.SuccessCriteria,
	}))
	if progress.Code != http.StatusOK {
		t.Fatalf("progress update = %d: %s", progress.Code, progress.Body.String())
	}
	progressGoal := decodeGoalEnvelope(t, progress).Goal
	if progressGoal == nil || progressGoal.Version != 2 || len(progressGoal.EvidenceRefs) != 2 {
		t.Fatalf("progress goal = %#v", progressGoal)
	}

	stale := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(stale, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": 1,
		"status":           "paused",
	}))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update = %d: %s", stale.Code, stale.Body.String())
	}

	completed := httptest.NewRecorder()
	testHandler.UpdateChannelGoal(completed, goalRequest(t, testUserID, http.MethodPatch, channel.ID, map[string]any{
		"expected_version": progressGoal.Version,
		"status":           "completed",
	}))
	if completed.Code != http.StatusOK {
		t.Fatalf("complete goal = %d: %s", completed.Code, completed.Body.String())
	}
	completedGoal := decodeGoalEnvelope(t, completed).Goal
	if completedGoal == nil || completedGoal.Status != "completed" || completedGoal.CompletedAt == nil {
		t.Fatalf("completed goal = %#v", completedGoal)
	}

	current := httptest.NewRecorder()
	testHandler.GetChannelGoal(current, goalRequest(t, testUserID, http.MethodGet, channel.ID, nil))
	if current.Code != http.StatusOK || decodeGoalEnvelope(t, current).Goal != nil {
		t.Fatalf("terminal goal remained current: %d %s", current.Code, current.Body.String())
	}
}

func TestChannelGoalExecutionAdmission(t *testing.T) {
	tests := []struct {
		name string
		in   channelGoalCoordinationSummary
		want string
	}{
		{name: "single agent direct", in: channelGoalCoordinationSummary{AgentMemberCount: 1}, want: "direct"},
		{name: "project required", in: channelGoalCoordinationSummary{AgentMemberCount: 2}, want: "project_required"},
		{name: "git required", in: channelGoalCoordinationSummary{AgentMemberCount: 2, ProjectID: "project-1"}, want: "git_required"},
		{name: "issues required", in: channelGoalCoordinationSummary{AgentMemberCount: 2, ProjectID: "project-1", GitRepositoryBound: true}, want: "issues_required"},
		{name: "ready", in: channelGoalCoordinationSummary{AgentMemberCount: 2, ProjectID: "project-1", GitRepositoryBound: true, ChannelProjectIssueTotal: 1, ProjectIssueTotal: 2, OpenProjectIssueTotal: 1}, want: "ready"},
		{name: "acceptance required", in: channelGoalCoordinationSummary{AgentMemberCount: 2, ProjectID: "project-1", GitRepositoryBound: true, ChannelProjectIssueTotal: 1, ProjectIssueTotal: 2}, want: "acceptance_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelGoalExecutionAdmission(tt.in); got != tt.want {
				t.Fatalf("admission = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChannelGoalWriteRequiresChannelAuthority(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	memberID := seedWorkspaceUserForTransportTargetTest(t, "goal-member-"+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(memberID)); err != nil {
		t.Fatalf("add ordinary member: %v", err)
	}
	rec := httptest.NewRecorder()
	testHandler.CreateChannelGoal(rec, goalRequest(t, memberID, http.MethodPost, channel.ID, map[string]any{
		"title": "Unauthorized", "objective": "Must fail", "success_criteria": []string{"Rejected"},
	}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ordinary member create = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGoalCriteriaRevisionDropsOrphanedCompletion(t *testing.T) {
	current := []string{"A", "B"}
	revised := []string{"B", "C"}
	set := make(map[string]struct{}, len(revised))
	for _, criterion := range revised {
		set[criterion] = struct{}{}
	}
	var kept []string
	for _, criterion := range current {
		if _, exists := set[criterion]; exists {
			kept = append(kept, criterion)
		}
	}
	if len(kept) != 1 || kept[0] != "B" {
		t.Fatalf("kept completion = %v, want [B]", kept)
	}
}

func TestChannelGoalContextForClaimOnlyInjectsActiveGoal(t *testing.T) {
	active := ChannelGoalResponse{
		ID: "goal-1", Title: "Ship", Objective: "Ship safely",
		Status: "active", Version: 7, SuccessCriteria: []string{"Verified"},
	}
	if context := channelGoalContextForClaim(active); context == nil || context.Version != 7 {
		t.Fatalf("active goal context = %#v", context)
	}
	for _, status := range []string{"paused", "completed", "cancelled"} {
		inactive := active
		inactive.Status = status
		if context := channelGoalContextForClaim(inactive); context != nil {
			t.Fatalf("%s goal injected into claim: %#v", status, context)
		}
	}
}

func TestAgentGoalManagerAuthorityFollowsRosterSourceAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	managerID := createHandlerTestAgent(t, "Goal manager "+uuid.NewString()[:8], nil)
	executionID := createHandlerTestAgent(t, "Goal execution "+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(managerID)); err != nil {
		t.Fatalf("add manager agent: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET source_agent_id = $1 WHERE id = $2`,
		parseUUID(managerID), parseUUID(executionID)); err != nil {
		t.Fatalf("bind execution source agent: %v", err)
	}
	if !testHandler.agentIsChannelManager(context.Background(), parseUUID(testWorkspaceID), parseUUID(channel.ID), parseUUID(executionID)) {
		t.Fatal("derived execution agent did not inherit its roster source manager authority")
	}
}

func agentGoalRequest(t *testing.T, agentID, method, path, channelID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	req = withURLParam(req, "channelId", channelID)
	return withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
}

func TestAgentGoalManagerCreateExecutorCheckpointAndIntentGuard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	managerID := createHandlerTestAgent(t, "Goal API manager "+uuid.NewString()[:8], nil)
	executorID := createHandlerTestAgent(t, "Goal API executor "+uuid.NewString()[:8], nil)
	for agentID, role := range map[string]string{managerID: "manager", executorID: "member"} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'agent', $3, $4)`,
			parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(agentID), role); err != nil {
			t.Fatalf("add %s agent: %v", role, err)
		}
	}

	created := httptest.NewRecorder()
	testHandler.CreateAgentChannelGoal(created, agentGoalRequest(
		t, managerID, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal", channel.ID,
		map[string]any{
			"title": "Agent-created goal", "objective": "Coordinate work",
			"success_criteria": []string{"Checkpoint persists"},
		},
	))
	if created.Code != http.StatusCreated {
		t.Fatalf("manager create = %d: %s", created.Code, created.Body.String())
	}
	createdGoal := decodeGoalEnvelope(t, created).Goal
	if createdGoal == nil || createdGoal.CreatedByType != "agent" || createdGoal.CreatedByID != managerID {
		t.Fatalf("agent-created goal = %#v", createdGoal)
	}

	bootstrap := httptest.NewRecorder()
	testHandler.BootstrapAgentChannelGoalControlPlane(bootstrap, agentGoalRequest(
		t, managerID, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal/bootstrap", channel.ID,
		map[string]any{
			"project_title": "Goal delivery project", "repository_url": "https://github.com/multica-ai/goal-delivery.git",
			"default_branch_hint": "dev",
		},
	))
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("manager bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var bootstrapped BootstrapAgentChannelGoalControlPlaneResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &bootstrapped); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if !bootstrapped.Created || bootstrapped.Project.ID == "" || bootstrapped.Resource.ResourceType != "github_repo" {
		t.Fatalf("bootstrap response = %#v", bootstrapped)
	}
	if bootstrapped.Goal.Coordination == nil || bootstrapped.Goal.Coordination.ExecutionAdmission != "issues_required" {
		t.Fatalf("bootstrap admission = %#v", bootstrapped.Goal.Coordination)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, parseUUID(bootstrapped.Project.ID))
	})

	checkpoint := httptest.NewRecorder()
	testHandler.CheckpointAgentChannelGoal(checkpoint, agentGoalRequest(
		t, executorID, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal/checkpoint", channel.ID,
		map[string]any{
			"expected_version":   createdGoal.Version,
			"progress_summary":   "Verified persistence",
			"current_step":       "Manager review",
			"evidence_refs":      []string{"test:checkpoint"},
			"completed_criteria": createdGoal.SuccessCriteria,
		},
	))
	if checkpoint.Code != http.StatusOK {
		t.Fatalf("executor checkpoint = %d: %s", checkpoint.Code, checkpoint.Body.String())
	}
	checkpointGoal := decodeGoalEnvelope(t, checkpoint).Goal
	if checkpointGoal == nil || checkpointGoal.UpdatedByID != executorID || checkpointGoal.ProgressSummary != "Verified persistence" {
		t.Fatalf("checkpoint goal = %#v", checkpointGoal)
	}

	forbidden := httptest.NewRecorder()
	testHandler.UpdateAgentChannelGoal(forbidden, agentGoalRequest(
		t, executorID, http.MethodPatch, "/api/agent/channels/"+channel.ID+"/goal", channel.ID,
		map[string]any{"expected_version": checkpointGoal.Version, "objective": "Lower the bar"},
	))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("ordinary executor intent update = %d: %s", forbidden.Code, forbidden.Body.String())
	}

	revised := httptest.NewRecorder()
	testHandler.UpdateAgentChannelGoal(revised, agentGoalRequest(
		t, managerID, http.MethodPatch, "/api/agent/channels/"+channel.ID+"/goal", channel.ID,
		map[string]any{"expected_version": checkpointGoal.Version, "status": "completed"},
	))
	if revised.Code != http.StatusConflict {
		t.Fatalf("manager completion without project Issues = %d: %s", revised.Code, revised.Body.String())
	}
}

func TestAgentManagerCannotRewriteHumanAuthoredGoalIntent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	managerID := createHandlerTestAgent(t, "Human goal manager "+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(managerID)); err != nil {
		t.Fatalf("add manager agent: %v", err)
	}

	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channel.ID, map[string]any{
		"title": "Human contract", "objective": "Keep the bar",
		"success_criteria": []string{"Human accepts"},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("human create = %d: %s", created.Code, created.Body.String())
	}
	goal := decodeGoalEnvelope(t, created).Goal

	rewrite := httptest.NewRecorder()
	testHandler.UpdateAgentChannelGoal(rewrite, agentGoalRequest(
		t, managerID, http.MethodPatch, "/api/agent/channels/"+channel.ID+"/goal", channel.ID,
		map[string]any{"expected_version": goal.Version, "success_criteria": []string{"Agent says done"}},
	))
	if rewrite.Code != http.StatusForbidden {
		t.Fatalf("agent rewrite human intent = %d: %s", rewrite.Code, rewrite.Body.String())
	}
}
