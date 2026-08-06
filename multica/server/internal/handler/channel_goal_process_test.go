package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func decodeProcessEnvelope(t *testing.T, rec *httptest.ResponseRecorder) channelGoalProcessEnvelope {
	t.Helper()
	var envelope channelGoalProcessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode process response: %v (%s)", err, rec.Body.String())
	}
	return envelope
}

func decodeProcessListEnvelope(t *testing.T, rec *httptest.ResponseRecorder) channelGoalProcessListEnvelope {
	t.Helper()
	var envelope channelGoalProcessListEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode process list: %v (%s)", err, rec.Body.String())
	}
	return envelope
}

func processRequest(t *testing.T, userID, method, channelID, agentID string, body any) *http.Request {
	t.Helper()
	path := "/api/channels/" + channelID + "/goal/process"
	if agentID != "" {
		path += "/" + agentID
	}
	req := newRequestAs(userID, method, path, body)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	if agentID != "" {
		return withRouteParams(req, "channelId", channelID, "agentId", agentID)
	}
	return withURLParam(req, "channelId", channelID)
}

func createActiveGoalForProcessTests(t *testing.T, channelID string) *ChannelGoalResponse {
	t.Helper()
	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channelID, map[string]any{
		"title": "Process docs", "objective": "Track manager long-form process",
		"success_criteria": []string{"Per-manager markdown persists"},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create goal = %d: %s", created.Code, created.Body.String())
	}
	return decodeGoalEnvelope(t, created).Goal
}

func TestChannelGoalProcessMarkdownCRUDAndIsolation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	managerA := createHandlerTestAgent(t, "Process mgr A "+uuid.NewString()[:8], nil)
	managerB := createHandlerTestAgent(t, "Process mgr B "+uuid.NewString()[:8], nil)
	for _, agentID := range []string{managerA, managerB} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'agent', $3, 'manager')`,
			parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(agentID)); err != nil {
			t.Fatalf("add manager: %v", err)
		}
	}

	missingGoal := httptest.NewRecorder()
	testHandler.ListChannelGoalProcesses(missingGoal, processRequest(t, testUserID, http.MethodGet, channel.ID, "", nil))
	if missingGoal.Code != http.StatusNotFound {
		t.Fatalf("list without goal = %d: %s", missingGoal.Code, missingGoal.Body.String())
	}

	goal := createActiveGoalForProcessTests(t, channel.ID)
	progressBefore := goal.ProgressSummary

	emptyList := httptest.NewRecorder()
	testHandler.ListChannelGoalProcesses(emptyList, processRequest(t, testUserID, http.MethodGet, channel.ID, "", nil))
	if emptyList.Code != http.StatusOK {
		t.Fatalf("empty list = %d: %s", emptyList.Code, emptyList.Body.String())
	}
	listed := decodeProcessListEnvelope(t, emptyList)
	if listed.GoalID != goal.ID || len(listed.Processes) != 0 {
		t.Fatalf("empty list envelope = %#v", listed)
	}

	missingDoc := httptest.NewRecorder()
	testHandler.GetChannelGoalProcess(missingDoc, processRequest(t, testUserID, http.MethodGet, channel.ID, managerA, nil))
	if missingDoc.Code != http.StatusNotFound {
		t.Fatalf("missing process = %d: %s", missingDoc.Code, missingDoc.Body.String())
	}

	created := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(created, processRequest(t, testUserID, http.MethodPut, channel.ID, managerA, map[string]any{
		"content": "# A plan\n- step 1", "expected_version": 0,
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create process = %d: %s", created.Code, created.Body.String())
	}
	docA := decodeProcessEnvelope(t, created).Process
	if docA == nil || docA.ManagerAgentID != managerA || docA.Version != 1 || docA.GoalID != goal.ID {
		t.Fatalf("created process = %#v", docA)
	}

	createdB := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(createdB, processRequest(t, testUserID, http.MethodPut, channel.ID, managerB, map[string]any{
		"content": "# B plan", "expected_version": 0,
	}))
	if createdB.Code != http.StatusCreated {
		t.Fatalf("create process B = %d: %s", createdB.Code, createdB.Body.String())
	}

	updated := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(updated, processRequest(t, testUserID, http.MethodPut, channel.ID, managerA, map[string]any{
		"content": "# A plan\n- step 1\n- step 2", "expected_version": 1,
	}))
	if updated.Code != http.StatusOK {
		t.Fatalf("update process = %d: %s", updated.Code, updated.Body.String())
	}
	docA2 := decodeProcessEnvelope(t, updated).Process
	if docA2 == nil || docA2.Version != 2 || docA2.Content != "# A plan\n- step 1\n- step 2" {
		t.Fatalf("updated process = %#v", docA2)
	}

	stale := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(stale, processRequest(t, testUserID, http.MethodPut, channel.ID, managerA, map[string]any{
		"content": "stale", "expected_version": 1,
	}))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale process write = %d: %s", stale.Code, stale.Body.String())
	}

	all := httptest.NewRecorder()
	testHandler.ListChannelGoalProcesses(all, processRequest(t, testUserID, http.MethodGet, channel.ID, "", nil))
	if all.Code != http.StatusOK || len(decodeProcessListEnvelope(t, all).Processes) != 2 {
		t.Fatalf("list after writes = %d %s", all.Code, all.Body.String())
	}

	// Process writes must not mutate authoritative short-status fields.
	current := httptest.NewRecorder()
	testHandler.GetChannelGoal(current, goalRequest(t, testUserID, http.MethodGet, channel.ID, nil))
	currentGoal := decodeGoalEnvelope(t, current).Goal
	if currentGoal == nil || currentGoal.ProgressSummary != progressBefore || currentGoal.Version != goal.Version {
		t.Fatalf("process write mutated goal short status: before=%#v after=%#v", goal, currentGoal)
	}
}

func TestChannelGoalProcessWriteAuthAndNonManagerTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	_ = createActiveGoalForProcessTests(t, channel.ID)
	managerID := createHandlerTestAgent(t, "Auth mgr "+uuid.NewString()[:8], nil)
	memberAgentID := createHandlerTestAgent(t, "Auth member "+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager'), ($1, $2, 'agent', $4, 'member')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(managerID), parseUUID(memberAgentID)); err != nil {
		t.Fatalf("seed agents: %v", err)
	}

	memberID := seedWorkspaceUserForTransportTargetTest(t, "goal-process-member-"+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(memberID)); err != nil {
		t.Fatalf("add ordinary member: %v", err)
	}

	forbidden := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(forbidden, processRequest(t, memberID, http.MethodPut, channel.ID, managerID, map[string]any{
		"content": "nope", "expected_version": 0,
	}))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("ordinary member put = %d: %s", forbidden.Code, forbidden.Body.String())
	}

	badTarget := httptest.NewRecorder()
	testHandler.PutChannelGoalProcess(badTarget, processRequest(t, testUserID, http.MethodPut, channel.ID, memberAgentID, map[string]any{
		"content": "not a manager", "expected_version": 0,
	}))
	if badTarget.Code != http.StatusBadRequest {
		t.Fatalf("non-manager target = %d: %s", badTarget.Code, badTarget.Body.String())
	}
}

func TestAgentChannelGoalProcessOwnWriteAndCrossManagerGuard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	_ = createActiveGoalForProcessTests(t, channel.ID)
	managerA := createHandlerTestAgent(t, "Agent process A "+uuid.NewString()[:8], nil)
	managerB := createHandlerTestAgent(t, "Agent process B "+uuid.NewString()[:8], nil)
	executor := createHandlerTestAgent(t, "Agent process exec "+uuid.NewString()[:8], nil)
	for agentID, role := range map[string]string{managerA: "manager", managerB: "manager", executor: "member"} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'agent', $3, $4)`,
			parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(agentID), role); err != nil {
			t.Fatalf("add %s: %v", role, err)
		}
	}

	own := httptest.NewRecorder()
	testHandler.PutAgentChannelGoalProcess(own, agentGoalRequest(
		t, managerA, http.MethodPut, "/api/agent/channels/"+channel.ID+"/goal/process", channel.ID,
		map[string]any{"content": "## A working notes", "expected_version": 0},
	))
	if own.Code != http.StatusCreated {
		t.Fatalf("manager own put = %d: %s", own.Code, own.Body.String())
	}
	doc := decodeProcessEnvelope(t, own).Process
	if doc == nil || doc.ManagerAgentID != managerA {
		t.Fatalf("own process keyed incorrectly: %#v", doc)
	}

	cross := httptest.NewRecorder()
	req := agentGoalRequest(
		t, managerA, http.MethodPut, "/api/agent/channels/"+channel.ID+"/goal/process/"+managerB, channel.ID,
		map[string]any{"content": "hijack", "expected_version": 0},
	)
	// withURLParam replaces the whole chi route ctx — set both params together.
	req = withRouteParams(req, "channelId", channel.ID, "agentId", managerB)
	testHandler.PutAgentChannelGoalProcess(cross, req)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-manager put = %d: %s", cross.Code, cross.Body.String())
	}

	executorPut := httptest.NewRecorder()
	testHandler.PutAgentChannelGoalProcess(executorPut, agentGoalRequest(
		t, executor, http.MethodPut, "/api/agent/channels/"+channel.ID+"/goal/process", channel.ID,
		map[string]any{"content": "executor", "expected_version": 0},
	))
	if executorPut.Code != http.StatusForbidden {
		t.Fatalf("executor put = %d: %s", executorPut.Code, executorPut.Body.String())
	}

	// Checkpoint short status must leave process markdown untouched.
	checkpoint := httptest.NewRecorder()
	testHandler.CheckpointAgentChannelGoal(checkpoint, agentGoalRequest(
		t, executor, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal/checkpoint", channel.ID,
		map[string]any{
			"expected_version": 1, "progress_summary": "Short status only",
			"current_step": "Continue", "blocker": "", "evidence_refs": []string{}, "completed_criteria": []string{},
		},
	))
	if checkpoint.Code != http.StatusOK {
		t.Fatalf("checkpoint = %d: %s", checkpoint.Code, checkpoint.Body.String())
	}
	get := httptest.NewRecorder()
	testHandler.GetChannelGoalProcess(get, processRequest(t, testUserID, http.MethodGet, channel.ID, managerA, nil))
	kept := decodeProcessEnvelope(t, get).Process
	if kept == nil || kept.Content != "## A working notes" || kept.Version != 1 {
		t.Fatalf("checkpoint mutated process markdown: %#v", kept)
	}
}
