package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGetNotePeriodBriefPlanEmpty(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Plan empty")
	agentID := createHandlerTestAgent(t, "Notes Assistant Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, agentID, pageID)

	req := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	rec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get empty plan = %d: %s", rec.Code, rec.Body.String())
	}
	var resp notePeriodBriefPlanResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Plan != nil {
		t.Fatalf("expected no plan, got %+v", resp.Plan)
	}
}

func TestPutAndGetNotePeriodBriefPlanRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Plan roundtrip")
	agentID := createHandlerTestAgent(t, "Notes Assistant Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, agentID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Plan")

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":      sessionID,
		"context_note_page_id": pageID,
		"window":               "week",
		"date":                 "2026-09-03",
		"collector_agent_ids":  []string{collectorA},
		"focus":                "只整理 ~/multica",
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}
	var saved notePeriodBriefPlanResponse
	if err := json.NewDecoder(putRec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if saved.Plan == nil || saved.Plan.Window != "week" || saved.Plan.Focus != "只整理 ~/multica" {
		t.Fatalf("put plan = %+v", saved.Plan)
	}
	if len(saved.Plan.CollectorAgentIDs) != 1 || saved.Plan.CollectorAgentIDs[0] != collectorA {
		t.Fatalf("put collectors = %#v", saved.Plan.CollectorAgentIDs)
	}

	get := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get plan = %d: %s", getRec.Code, getRec.Body.String())
	}
	var loaded notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if loaded.Plan == nil || loaded.Plan.Window != "week" || loaded.Plan.Focus != "只整理 ~/multica" {
		t.Fatalf("get plan = %+v", loaded.Plan)
	}

	patch := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     sessionID,
		"collector_agent_ids": []string{collectorA},
		"focus":               "只要 ubuntu",
	})
	patchRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(patchRec, patch)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch plan = %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var patched notePeriodBriefPlanResponse
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Plan == nil || patched.Plan.Window != "week" || patched.Plan.Focus != "只要 ubuntu" {
		t.Fatalf("speech and chips must share one plan: %+v", patched.Plan)
	}
}

func TestDeleteNotePeriodBriefPlanDismissesTheCard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Plan cancel")
	agentID := createHandlerTestAgent(t, "Notes Assistant Plan Cancel "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, agentID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Plan Cancel")

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     sessionID,
		"window":              "week",
		"collector_agent_ids": []string{collectorA},
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	del := newRequest(http.MethodDelete, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	delRec := httptest.NewRecorder()
	testHandler.DeleteNotePeriodBriefPlan(delRec, del)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete plan = %d: %s", delRec.Code, delRec.Body.String())
	}
	var cleared notePeriodBriefPlanResponse
	if err := json.NewDecoder(delRec.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if cleared.Plan != nil {
		t.Fatalf("delete must clear the plan, got %+v", cleared.Plan)
	}

	get := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getRec, get)
	var loaded notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if loaded.Plan != nil {
		t.Fatalf("plan card must stay gone, got %+v", loaded.Plan)
	}
}

func TestDeleteNotePeriodBriefPlanStopsALiveCollect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	pageID := insertPeriodBriefFixtureDraft(t, "Plan cancel live")
	agentID := createHandlerTestAgent(t, "Notes Assistant Plan Stop "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, agentID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Plan Stop")

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     sessionID,
		"window":              "week",
		"collector_agent_ids": []string{collectorA},
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	synthID := createHandlerTestAgent(t, "Plan Stop Synth "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(ctx, parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "plan-stop draft")
	runID := insertPeriodBriefFixtureRun(t, pageID, uuidToString(folderID), synthID, draftID, "collecting", time.Now())
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM note_period_brief_run WHERE id = $1`, runID)
	})

	del := newRequest(http.MethodDelete, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	delRec := httptest.NewRecorder()
	testHandler.DeleteNotePeriodBriefPlan(delRec, del)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete plan = %d: %s", delRec.Code, delRec.Body.String())
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&status); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("live collect must stop, status=%q", status)
	}
}

func TestAgentNotePeriodBriefPlanRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Agent plan")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Agent Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Agent Plan")

	put := withAgentCredentialPrincipal(
		newRequest(http.MethodPut, "/api/agent/notes/period-briefs/plan", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": pageID,
			"window":               "day",
			"date":                 "2026-09-03",
			"collector_agent_ids":  []string{collectorA},
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	putRec := httptest.NewRecorder()
	testHandler.PutAgentNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("agent put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	get := withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/period-briefs/plan?chat_session_id="+sessionID, nil),
		assistantID, testWorkspaceID, testUserID,
	)
	getRec := httptest.NewRecorder()
	testHandler.GetAgentNotePeriodBriefPlan(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("agent get plan = %d: %s", getRec.Code, getRec.Body.String())
	}
	var loaded notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if loaded.Plan == nil || loaded.Plan.Window != "day" || len(loaded.Plan.CollectorAgentIDs) != 1 {
		t.Fatalf("agent plan = %+v", loaded.Plan)
	}
}

func TestCreateNotePeriodBriefStartsFromSessionPlan(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 2 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	pageID := insertPeriodBriefFixtureDraft(t, "Start from plan")
	assistantID := createHandlerTestAgent(t, "Notes Assistant From Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop From Plan")
	injectPeriodBriefCollectorPackMarkdown(t, collectorA, "## Work groups\n\n### Ready\n- injected pack\n")

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     sessionID,
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"collector_agent_ids": []string{collectorA},
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	start := newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"agent_id":             assistantID,
		"context_note_page_id": pageID,
		"chat_session_id":      sessionID,
		"from_chat":            true,
		"timezone":             "UTC",
	})
	startRec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(startRec, start)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start from plan = %d: %s", startRec.Code, startRec.Body.String())
	}
	var runCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("start from plan must create a run, count=%d", runCount)
	}

	get := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getRec, get)
	var after notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&after); err != nil {
		t.Fatalf("decode after start: %v", err)
	}
	if after.Plan != nil {
		t.Fatalf("successful start must consume the current plan, got %+v", after.Plan)
	}
}

func TestPeriodBriefSpeechOpensPlanCardWithoutStarting(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Run("写汇报", func(t *testing.T) {
		periodBriefSpeechOpensPlanCard(t, "写汇报", false)
	})
	t.Run("下面重新采集，重新给我写一个汇报", func(t *testing.T) {
		periodBriefSpeechOpensPlanCard(t, "下面重新采集，重新给我写一个汇报", true)
	})
}

func periodBriefSpeechOpensPlanCard(t *testing.T, text string, softConfirm bool) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 2 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	pageID := insertPeriodBriefFixtureDraft(t, "Recollect plan card")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Recollect Card "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Recollect Card")
	injectPeriodBriefCollectorPackMarkdown(t, collectorA, "## Work groups\n\n### Ready\n- injected pack\n")
	day := time.Now().UTC().Format("2006-01-02")

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     sessionID,
		"window":              "day",
		"date":                day,
		"collector_agent_ids": []string{collectorA},
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	start := newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"agent_id":             assistantID,
		"context_note_page_id": pageID,
		"chat_session_id":      sessionID,
		"from_chat":            true,
		"timezone":             "UTC",
	})
	startRec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(startRec, start)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("first start = %d: %s", startRec.Code, startRec.Body.String())
	}

	ask := sendNoteBubbleChat(t, sessionID, text)
	if ask.Pending {
		t.Fatalf("%q must stay on the platform path, not wake the assistant", text)
	}
	getAsk := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getAskRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getAskRec, getAsk)
	var pending notePeriodBriefPlanResponse
	if err := json.NewDecoder(getAskRec.Body).Decode(&pending); err != nil {
		t.Fatalf("decode pending plan: %v", err)
	}
	if softConfirm {
		if pending.Plan == nil || pending.Plan.Status != periodBriefPromptStatusAwaitingIntent {
			t.Fatalf("%q must soft-confirm first, got %+v", text, pending.Plan)
		}
		yes := sendNoteBubbleChat(t, sessionID, "是")
		if yes.Pending {
			t.Fatalf("confirm yes must open the plan card, not wake the assistant")
		}
	} else if pending.Plan == nil || pending.Plan.Status != periodBriefPromptStatusClarifying {
		t.Fatalf("%q must open the clarifying card directly, got %+v", text, pending.Plan)
	}

	get := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getRec, get)
	var loaded notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if loaded.Plan == nil || loaded.Plan.Window != "day" || len(loaded.Plan.CollectorAgentIDs) != 1 || loaded.Plan.CollectorAgentIDs[0] != collectorA {
		t.Fatalf("after yes, %q must restore the last plan card, got %+v", text, loaded.Plan)
	}

	var runCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("%q must not start a new collect, count=%d", text, runCount)
	}

	activeRec := httptest.NewRecorder()
	testHandler.GetActiveNotePeriodBrief(activeRec, newRequest(http.MethodGet, "/api/notes/period-briefs/active?page_id="+pageID, nil))
	var active struct {
		Run *notePeriodBriefActiveResponse `json:"run"`
	}
	if err := json.NewDecoder(activeRec.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}
	if active.Run != nil && periodBriefRunLocksComposerStatus(active.Run.Status) {
		t.Fatalf("%q must not lock a new run, active=%+v", text, active.Run)
	}
}

func TestStartAgentNotePeriodBriefUsesSessionPlan(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 2 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	pageID := insertPeriodBriefFixtureDraft(t, "Agent start from plan")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Agent From Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop Agent From Plan")
	injectPeriodBriefCollectorPackMarkdown(t, collectorA, "## Work groups\n\n### Ready\n- injected pack\n")

	put := withAgentCredentialPrincipal(
		newRequest(http.MethodPut, "/api/agent/notes/period-briefs/plan", map[string]any{
			"chat_session_id":     sessionID,
			"window":              "day",
			"date":                time.Now().UTC().Format("2006-01-02"),
			"collector_agent_ids": []string{collectorA},
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	putRec := httptest.NewRecorder()
	testHandler.PutAgentNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("agent put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	start := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": pageID,
			"timezone":             "UTC",
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	startRec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(startRec, start)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("agent start from plan = %d: %s", startRec.Code, startRec.Body.String())
	}
	var resp startAgentNotePeriodBriefResponse
	if err := json.NewDecoder(startRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if !resp.Dispatched || resp.RunID == "" {
		t.Fatalf("start response = %+v", resp)
	}
}

func TestAgentNotePeriodBriefPlanBarePutCreatesVisibleCard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Bare plan card")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Bare Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)

	put := withAgentCredentialPrincipal(
		newRequest(http.MethodPut, "/api/agent/notes/period-briefs/plan", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": pageID,
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	putRec := httptest.NewRecorder()
	testHandler.PutAgentNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("bare agent put plan = %d: %s", putRec.Code, putRec.Body.String())
	}
	var saved notePeriodBriefPlanResponse
	if err := json.NewDecoder(putRec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode bare put: %v", err)
	}
	if saved.Plan == nil {
		t.Fatal("bare plan PUT must create a visible card, got plan null")
	}

	get := newRequest(http.MethodGet, "/api/notes/period-briefs/plan?chat_session_id="+sessionID, nil)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePeriodBriefPlan(getRec, get)
	var loaded notePeriodBriefPlanResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if loaded.Plan == nil {
		t.Fatal("human GET must see the card created by a bare agent plan PUT")
	}
}

func TestFormatPeriodBriefCurrentPlanBoard(t *testing.T) {
	none := formatPeriodBriefCurrentPlanBoard(nil)
	for _, want := range []string{
		"<period_brief_current_plan>",
		"status: none",
		"notes period-brief plan",
		"chat_session_id",
		"Do not emit chat XML",
		"plan object",
	} {
		if !strings.Contains(none, want) {
			t.Fatalf("empty board missing %q:\n%s", want, none)
		}
	}
	draft := formatPeriodBriefCurrentPlanBoard(&notePeriodBriefPlanJSON{
		Window:            "week",
		CollectorAgentIDs: []string{"c1"},
	})
	for _, want := range []string{
		"status: draft",
		"window: week",
		"collectors: c1",
		"Do not call start",
		"开始采集",
	} {
		if !strings.Contains(draft, want) {
			t.Fatalf("draft board missing %q:\n%s", want, draft)
		}
	}
}

func TestBuildNoteChatWakePrefixIncludesCurrentPlan(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	pageID := insertPeriodBriefFixtureDraft(t, "Wake plan")
	agentID := createHandlerTestAgent(t, "Wake Plan "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, agentID, pageID)

	prefix := testHandler.buildNoteChatWakePrefix(context.Background(), parseUUID(sessionID))
	if !strings.Contains(prefix, "<period_brief_current_plan>") || !strings.Contains(prefix, "status: none") {
		t.Fatalf("expected empty current-plan board:\n%s", prefix)
	}
	if !strings.Contains(prefix, "chat_session_id: "+sessionID) {
		t.Fatalf("wake must name this bubble chat_session_id so plan needs no DB lookup:\n%s", prefix)
	}
	if strings.Contains(prefix, "<period_brief_compose_card>") {
		t.Fatalf("wake must not teach compose-card fences:\n%s", prefix)
	}

	put := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id": sessionID,
		"window":          "month",
	})
	putRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}
	withPlan := testHandler.buildNoteChatWakePrefix(context.Background(), parseUUID(sessionID))
	if !strings.Contains(withPlan, "status: draft") || !strings.Contains(withPlan, "window: month") {
		t.Fatalf("expected draft current-plan board:\n%s", withPlan)
	}
}
