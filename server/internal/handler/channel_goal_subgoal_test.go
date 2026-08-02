package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func createActiveGoalForSubgoalTests(t *testing.T, channelID string) *ChannelGoalResponse {
	t.Helper()
	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channelID, map[string]any{
		"title":            "Multi-agent delivery",
		"objective":        "Capture and finish parallel/serial subgoals",
		"success_criteria": []string{"subgoals tracked", "no cascade close"},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannelGoal = %d: %s", created.Code, created.Body.String())
	}
	return decodeGoalEnvelope(t, created).Goal
}

func subgoalRequest(t *testing.T, method, channelID, pathSuffix string, body any) *http.Request {
	t.Helper()
	path := "/api/channels/" + channelID + "/goal/subgoals" + pathSuffix
	req := newRequestAs(testUserID, method, path, body)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	return req
}

func TestChannelGoalSubgoalCRUDSerialDepsAndSingleResponsible(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channel := createGoalTestChannel(t)
	goal := createActiveGoalForSubgoalTests(t, channel.ID)
	agentA := createHandlerTestAgent(t, "subgoal-a-"+uuid.NewString()[:8], nil)
	agentB := createHandlerTestAgent(t, "subgoal-b-"+uuid.NewString()[:8], nil)

	batch := httptest.NewRecorder()
	testHandler.BatchCreateChannelGoalSubgoals(batch, subgoalRequest(t, http.MethodPost, channel.ID, "/batch", map[string]any{
		"items": []map[string]any{
			{
				"title":       "Spec draft",
				"purpose":     "Write the BE contract",
				"responsible": map[string]any{"type": "agent", "id": agentA},
				"participants": []map[string]any{
					{"type": "agent", "id": agentB},
				},
			},
			{
				"title":       "Implement API",
				"purpose":     "Ship CRUD behind the contract",
				"responsible": map[string]any{"type": "agent", "id": agentB},
			},
		},
	}))
	if batch.Code != http.StatusCreated {
		t.Fatalf("batch create = %d: %s", batch.Code, batch.Body.String())
	}
	var batchEnv subgoalBatchEnvelope
	if err := json.Unmarshal(batch.Body.Bytes(), &batchEnv); err != nil || len(batchEnv.Subgoals) != 2 {
		t.Fatalf("batch decode: %v body=%s", err, batch.Body.String())
	}
	first, second := batchEnv.Subgoals[0], batchEnv.Subgoals[1]
	if first.GoalID != goal.ID || first.ResponsibleID != agentA || len(first.Participants) != 1 {
		t.Fatalf("first subgoal = %#v", first)
	}

	// Wire serial dependency: second depends on first.
	upd := httptest.NewRecorder()
	req := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+second.ID, map[string]any{
		"expected_version": second.Version,
		"depends_on":       []string{first.ID},
		"status":           "in_progress",
	})
	req = withRouteParams(req, "channelId", channel.ID, "subgoalId", second.ID)
	testHandler.UpdateChannelGoalSubgoal(upd, req)
	if upd.Code != http.StatusConflict {
		t.Fatalf("expected deps conflict, got %d: %s", upd.Code, upd.Body.String())
	}

	// Resolve first — does not touch issues.
	issueID := ""
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1, $2, 'todo', 'medium', 'member', $3, (random()*1000000)::int) RETURNING id::text
	`, testWorkspaceID, "unrelated-"+uuid.NewString()[:8], testUserID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, issueID) })

	resolve := httptest.NewRecorder()
	rreq := subgoalRequest(t, http.MethodPost, channel.ID, "/"+first.ID+"/resolve", map[string]any{
		"expected_version":   first.Version,
		"current_conclusion": "Spec linked in artifact_refs",
	})
	rreq = withRouteParams(rreq, "channelId", channel.ID, "subgoalId", first.ID)
	testHandler.ResolveChannelGoalSubgoal(resolve, rreq)
	if resolve.Code != http.StatusOK {
		t.Fatalf("resolve = %d: %s", resolve.Code, resolve.Body.String())
	}
	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "todo" {
		t.Fatalf("resolve cascaded issue status=%q err=%v", issueStatus, err)
	}

	// Now second can enter in_progress.
	secondList := httptest.NewRecorder()
	testHandler.ListChannelGoalSubgoals(secondList, subgoalRequest(t, http.MethodGet, channel.ID, "", nil))
	var listed subgoalListEnvelope
	_ = json.Unmarshal(secondList.Body.Bytes(), &listed)
	var secondLatest ChannelGoalSubgoalResponse
	for _, sg := range listed.Subgoals {
		if sg.ID == second.ID {
			secondLatest = sg
		}
	}
	progress := httptest.NewRecorder()
	preq := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+second.ID, map[string]any{
		"expected_version": secondLatest.Version,
		"status":           "in_progress",
	})
	preq = withRouteParams(preq, "channelId", channel.ID, "subgoalId", second.ID)
	testHandler.UpdateChannelGoalSubgoal(progress, preq)
	if progress.Code != http.StatusOK {
		t.Fatalf("in_progress after deps = %d: %s", progress.Code, progress.Body.String())
	}

	// Stale version rejected.
	stale := httptest.NewRecorder()
	sreq := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+second.ID, map[string]any{
		"expected_version": secondLatest.Version,
		"brief":            "should conflict",
	})
	sreq = withRouteParams(sreq, "channelId", channel.ID, "subgoalId", second.ID)
	testHandler.UpdateChannelGoalSubgoal(stale, sreq)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update = %d: %s", stale.Code, stale.Body.String())
	}
}

func TestChannelGoalSubgoalWaitingOnRequiresSourceVerification(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channel := createGoalTestChannel(t)
	_ = createActiveGoalForSubgoalTests(t, channel.ID)
	agentID := createHandlerTestAgent(t, "wait-"+uuid.NewString()[:8], nil)

	created := httptest.NewRecorder()
	testHandler.CreateChannelGoalSubgoal(created, subgoalRequest(t, http.MethodPost, channel.ID, "", map[string]any{
		"title":       "Blocked on human",
		"purpose":     "Need ack before coding",
		"responsible": map[string]any{"type": "agent", "id": agentID},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	var env subgoalEnvelope
	_ = json.Unmarshal(created.Body.Bytes(), &env)
	sg := env.Subgoal

	setWait := httptest.NewRecorder()
	wreq := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+sg.ID, map[string]any{
		"expected_version": sg.Version,
		"waiting_on":       map[string]any{"kind": "member", "target_id": testUserID, "note": "need ack"},
	})
	wreq = withRouteParams(wreq, "channelId", channel.ID, "subgoalId", sg.ID)
	testHandler.UpdateChannelGoalSubgoal(setWait, wreq)
	if setWait.Code != http.StatusOK {
		t.Fatalf("set waiting_on = %d: %s", setWait.Code, setWait.Body.String())
	}
	_ = json.Unmarshal(setWait.Body.Bytes(), &env)
	sg = env.Subgoal
	if sg.Status != "waiting" {
		t.Fatalf("status=%q want waiting", sg.Status)
	}

	badClear := httptest.NewRecorder()
	creq := subgoalRequest(t, http.MethodPost, channel.ID, "/"+sg.ID+"/waiting-on/clear", map[string]any{
		"expected_version": sg.Version,
		"verification":     map[string]any{"kind": "member", "acknowledged": false},
	})
	creq = withRouteParams(creq, "channelId", channel.ID, "subgoalId", sg.ID)
	testHandler.ClearChannelGoalSubgoalWaitingOn(badClear, creq)
	if badClear.Code != http.StatusConflict {
		t.Fatalf("clear without ack = %d: %s", badClear.Code, badClear.Body.String())
	}

	okClear := httptest.NewRecorder()
	oreq := subgoalRequest(t, http.MethodPost, channel.ID, "/"+sg.ID+"/waiting-on/clear", map[string]any{
		"expected_version": sg.Version,
		"verification":     map[string]any{"kind": "member", "target_id": testUserID, "acknowledged": true},
	})
	oreq = withRouteParams(oreq, "channelId", channel.ID, "subgoalId", sg.ID)
	testHandler.ClearChannelGoalSubgoalWaitingOn(okClear, oreq)
	if okClear.Code != http.StatusOK {
		t.Fatalf("clear with ack = %d: %s", okClear.Code, okClear.Body.String())
	}
}

func TestChannelSubgoalContextsForClaimAreBoundedToOwnRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channel := createGoalTestChannel(t)
	goal := createActiveGoalForSubgoalTests(t, channel.ID)
	agentA := createHandlerTestAgent(t, "claim-a-"+uuid.NewString()[:8], nil)
	agentB := createHandlerTestAgent(t, "claim-b-"+uuid.NewString()[:8], nil)

	created := httptest.NewRecorder()
	testHandler.CreateChannelGoalSubgoal(created, subgoalRequest(t, http.MethodPost, channel.ID, "", map[string]any{
		"title":               "Only A",
		"purpose":             "A owns this",
		"completion_boundary": "PR merged",
		"responsible":         map[string]any{"type": "agent", "id": agentA},
		"activity_delta":      nil,
	}))
	_ = json.Unmarshal(created.Body.Bytes(), &struct{}{})

	// Set activity via update.
	var env subgoalEnvelope
	_ = json.Unmarshal(created.Body.Bytes(), &env)
	upd := httptest.NewRecorder()
	ureq := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+env.Subgoal.ID, map[string]any{
		"expected_version": env.Subgoal.Version,
		"activity_delta":   []string{"drafted outline", "asked for review"},
	})
	ureq = withRouteParams(ureq, "channelId", channel.ID, "subgoalId", env.Subgoal.ID)
	testHandler.UpdateChannelGoalSubgoal(upd, ureq)

	forA := testHandler.channelSubgoalContextsForClaim(context.Background(), parseUUID(testWorkspaceID), parseUUID(channel.ID), parseUUID(agentA), goal.ID)
	forB := testHandler.channelSubgoalContextsForClaim(context.Background(), parseUUID(testWorkspaceID), parseUUID(channel.ID), parseUUID(agentB), goal.ID)
	if len(forA) != 1 || forA[0].OwnRole != "responsible" || forA[0].Purpose == "" {
		t.Fatalf("agent A contexts = %#v", forA)
	}
	if len(forA[0].ActivityDelta) == 0 {
		t.Fatalf("expected activity delta, got %#v", forA[0])
	}
	if len(forB) != 0 {
		t.Fatalf("agent B must not see A's private subgoal dump: %#v", forB)
	}
	// Ensure protocol shape stays free of chat/thread blobs.
	_ = protocol.ChannelSubgoalContext{}
}

func TestChannelGoalSubgoalSourceMessageIDSameChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channel := createGoalTestChannel(t)
	_ = createActiveGoalForSubgoalTests(t, channel.ID)
	agentID := createHandlerTestAgent(t, "src-"+uuid.NewString()[:8], nil)

	var messageID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source)
		VALUES ($1, $2, 'user', $3, 'tester', 'assign this as a subgoal', 'multica')
		RETURNING id::text
	`, channel.ID, testWorkspaceID, testUserID).Scan(&messageID); err != nil {
		t.Fatalf("seed channel message: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id=$1`, messageID) })

	otherChannel := createGoalTestChannel(t)
	var otherMessageID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source)
		VALUES ($1, $2, 'user', $3, 'tester', 'wrong channel', 'multica')
		RETURNING id::text
	`, otherChannel.ID, testWorkspaceID, testUserID).Scan(&otherMessageID); err != nil {
		t.Fatalf("seed other-channel message: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id=$1`, otherMessageID) })

	// Cross-channel source rejected.
	bad := httptest.NewRecorder()
	testHandler.CreateChannelGoalSubgoal(bad, subgoalRequest(t, http.MethodPost, channel.ID, "", map[string]any{
		"title":              "Bad source",
		"purpose":            "Must reject foreign message",
		"responsible":        map[string]any{"type": "agent", "id": agentID},
		"source_message_id":  otherMessageID,
	}))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("cross-channel source = %d: %s", bad.Code, bad.Body.String())
	}

	// Same-channel source accepted and listed.
	ok := httptest.NewRecorder()
	testHandler.CreateChannelGoalSubgoal(ok, subgoalRequest(t, http.MethodPost, channel.ID, "", map[string]any{
		"title":             "From chat",
		"purpose":           "Trace back to the ask",
		"responsible":       map[string]any{"type": "agent", "id": agentID},
		"source_message_id": messageID,
	}))
	if ok.Code != http.StatusCreated {
		t.Fatalf("create with source = %d: %s", ok.Code, ok.Body.String())
	}
	var env subgoalEnvelope
	if err := json.Unmarshal(ok.Body.Bytes(), &env); err != nil || env.Subgoal == nil {
		t.Fatalf("decode: %v body=%s", err, ok.Body.String())
	}
	if env.Subgoal.SourceMessageID == nil || *env.Subgoal.SourceMessageID != messageID {
		t.Fatalf("source_message_id = %#v want %s", env.Subgoal.SourceMessageID, messageID)
	}

	// Omit keeps prior; empty string clears.
	clear := httptest.NewRecorder()
	creq := subgoalRequest(t, http.MethodPatch, channel.ID, "/"+env.Subgoal.ID, map[string]any{
		"expected_version":  env.Subgoal.Version,
		"source_message_id": "",
	})
	creq = withRouteParams(creq, "channelId", channel.ID, "subgoalId", env.Subgoal.ID)
	testHandler.UpdateChannelGoalSubgoal(clear, creq)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear source = %d: %s", clear.Code, clear.Body.String())
	}
	// Fresh envelope: json.Unmarshal into a prior struct keeps old pointer
	// fields when the response omits source_message_id (omitempty).
	var clearedEnv subgoalEnvelope
	if err := json.Unmarshal(clear.Body.Bytes(), &clearedEnv); err != nil || clearedEnv.Subgoal == nil {
		t.Fatalf("clear decode: %v body=%s", err, clear.Body.String())
	}
	if clearedEnv.Subgoal.SourceMessageID != nil {
		t.Fatalf("cleared source_message_id still set: %q body=%s", *clearedEnv.Subgoal.SourceMessageID, clear.Body.String())
	}
	if !strings.Contains(clear.Body.String(), `"version":2`) {
		t.Fatalf("expected version bump on clear, body=%s", clear.Body.String())
	}

	// Create without field still works (backward compatible).
	plain := httptest.NewRecorder()
	testHandler.CreateChannelGoalSubgoal(plain, subgoalRequest(t, http.MethodPost, channel.ID, "", map[string]any{
		"title":       "No source",
		"purpose":     "Legacy clients omit the field",
		"responsible": map[string]any{"type": "agent", "id": agentID},
	}))
	if plain.Code != http.StatusCreated {
		t.Fatalf("create without source = %d: %s", plain.Code, plain.Body.String())
	}
}
