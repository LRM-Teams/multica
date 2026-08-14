package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestGetAgentHealth_MapsRuntimeAndHealthEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now().Add(-20*time.Second), time.Now().Add(-15*time.Second))

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.AgentID != agentID {
		t.Fatalf("summary agent_id = %q, want %q", resp.Summary.AgentID, agentID)
	}
	if resp.Summary.RuntimeID == nil || *resp.Summary.RuntimeID != runtimeID {
		t.Fatalf("summary runtime_id = %#v, want %s", resp.Summary.RuntimeID, runtimeID)
	}
	if resp.Summary.State != agentHealthStateOnline {
		t.Fatalf("summary state = %q, want %q", resp.Summary.State, agentHealthStateOnline)
	}
	if len(resp.Events) != 1 || !resp.Events[0].Synthetic {
		t.Fatalf("expected one synthetic health event, got %+v", resp.Events)
	}
	if resp.Events[0].Type != agentHealthEventServerPing || resp.Events[0].StateAfter != agentHealthStateOnline {
		t.Fatalf("first event = %s/%s, want synthetic online event", resp.Events[0].Type, resp.Events[0].StateAfter)
	}
}

func TestGetAgentHealth_OfflineAgesIntoReconnecting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _ := createAgentHealthFixture(t, "offline", time.Now().Add(-15*time.Minute), time.Now().Add(-10*time.Minute))

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateReconnecting {
		t.Fatalf("summary state = %q, want %q", resp.Summary.State, agentHealthStateReconnecting)
	}
	if len(resp.Events) == 0 || resp.Events[0].Type != agentHealthEventProbeTimeout {
		t.Fatalf("expected synthetic probe-timeout event, got %+v", resp.Events)
	}
}

// TestGetAgentHealth_SummaryUnconditionalEventsGated is the task #908
// successor: online/health presence is unconditional for every workspace
// member (Parker: "能不能干活，全员"), but the raw health_events diagnostic
// log stays admin|owner-gated via canAccessAgentInternals.
func TestGetAgentHealth_SummaryUnconditionalEventsGated(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)
	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequestAs(ownerID, "GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth as owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var ownerResp AgentHealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ownerResp); err != nil {
		t.Fatalf("decode owner response: %v", err)
	}
	if len(ownerResp.Events) == 0 || !ownerResp.Events[0].Synthetic {
		t.Fatalf("GetAgentHealth as owner: events = %+v, want a synthetic current event", ownerResp.Events)
	}

	w = httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth as plain member: expected 200 (presence unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
	var memberResp AgentHealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &memberResp); err != nil {
		t.Fatalf("decode plain-member response: %v", err)
	}
	if memberResp.Summary.AgentID != agentID {
		t.Fatalf("GetAgentHealth as plain member: summary missing, got %+v", memberResp.Summary)
	}
	if len(memberResp.Events) != 0 {
		t.Fatalf("GetAgentHealth as plain member: events = %+v, want redacted (empty)", memberResp.Events)
	}
}

func TestAgentHealthEventStateMapping(t *testing.T) {
	tests := []struct {
		eventType string
		state     string
	}{
		{agentHealthEventServerPing, agentHealthStateOnline},
		{agentHealthEventLivenessProbe, agentHealthStateSuspectedDisconnect},
		{agentHealthEventProbeTimeout, agentHealthStateReconnecting},
		{agentHealthEventTransportRecover, agentHealthStateRecovered},
	}
	for _, tt := range tests {
		if got := agentHealthEventState(tt.eventType); got != tt.state {
			t.Fatalf("agentHealthEventState(%q) = %q, want %q", tt.eventType, got, tt.state)
		}
	}
}

func TestAgentHealthMissingRuntimeSummary_OfflineEmptyState(t *testing.T) {
	agent := dbAgentForHealthTest(t)
	summary := agentHealthMissingRuntimeSummary(agent)
	if summary.AgentID != uuidToString(agent.ID) {
		t.Fatalf("summary agent_id = %q, want %q", summary.AgentID, uuidToString(agent.ID))
	}
	if summary.State != agentHealthStateOffline || summary.ReasonCode != "runtime_missing" {
		t.Fatalf("missing runtime summary = %+v", summary)
	}
	if summary.RuntimeID != nil {
		t.Fatalf("missing runtime should return null runtime_id, got %#v", summary.RuntimeID)
	}
}

func TestGetAgentHealth_StaleOnlineRuntimeShowsSuspectedDisconnect(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Runtime row says "online" but heartbeat is older than the stale
	// threshold (150s). The health summary must not show online. (#284)
	agentID, _ := createAgentHealthFixture(t, "online",
		time.Now().Add(-3*time.Minute), // last_seen_at: 3 min ago — stale
		time.Now().Add(-2*time.Minute), // updated_at
	)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateSuspectedDisconnect {
		t.Fatalf("summary state = %q, want %q (stale online runtime must not show as online)", resp.Summary.State, agentHealthStateSuspectedDisconnect)
	}
}

func TestGetAgentHealth_VeryStaleOnlineRuntimeShowsReconnecting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Runtime row says "online" but heartbeat is older than the reconnect
	// window (5 min). Must show reconnecting, not online. (#284)
	agentID, _ := createAgentHealthFixture(t, "online",
		time.Now().Add(-7*time.Minute), // last_seen_at: 7 min ago — very stale
		time.Now().Add(-6*time.Minute),
	)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateReconnecting {
		t.Fatalf("summary state = %q, want %q (very stale runtime must show reconnecting)", resp.Summary.State, agentHealthStateReconnecting)
	}
}

func TestGetAgentHealth_FreshOnlineRuntimeStaysOnline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Runtime is genuinely online with a recent heartbeat — must stay online. (#284)
	agentID, _ := createAgentHealthFixture(t, "online",
		time.Now().Add(-10*time.Second), // last_seen_at: 10s ago — fresh
		time.Now().Add(-5*time.Second),
	)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateOnline {
		t.Fatalf("summary state = %q, want %q (fresh online runtime must stay online)", resp.Summary.State, agentHealthStateOnline)
	}
}

func TestGetAgentHealth_PrivateRuntimeBoundToAgentFollowsHeartbeat(t *testing.T) {
	// LRM-548 — channel/workspace agents often bind the owner's private
	// runtime (e.g. Grok). Presence must mirror heartbeat, not the claim
	// "runnable" predicate that still excludes private runtimes as shared
	// capacity.
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeOwnerID := createWorkspaceMemberUser(t, "Health Runtime Owner", "health-runtime-owner-"+randomID()+"@multica.test")
	agentID, runtimeID := createAgentHealthFixtureWithRuntimeAccess(t, "online",
		time.Now().Add(-10*time.Second),
		time.Now().Add(-5*time.Second),
		runtimeOwnerID,
		"private",
	)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateOnline {
		t.Fatalf("summary = %+v, want online for fresh private runtime %s bound to agent", resp.Summary, runtimeID)
	}
	if resp.Summary.RuntimeID == nil || *resp.Summary.RuntimeID != runtimeID {
		t.Fatalf("runtime_id = %v, want %s", resp.Summary.RuntimeID, runtimeID)
	}
}

func TestGetAgentHealth_PublicRuntimeOwnedByAnotherMemberStaysOnline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeOwnerID := createWorkspaceMemberUser(t, "Health Public Runtime Owner", "health-public-runtime-owner-"+randomID()+"@multica.test")
	agentID, _ := createAgentHealthFixtureWithRuntimeAccess(t, "online",
		time.Now().Add(-10*time.Second),
		time.Now().Add(-5*time.Second),
		runtimeOwnerID,
		"public",
	)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateOnline {
		t.Fatalf("summary state = %q, want %q for public fresh runtime", resp.Summary.State, agentHealthStateOnline)
	}
}

func createAgentHealthFixture(t *testing.T, status string, lastSeen, updatedAt time.Time) (agentID, runtimeID string) {
	return createAgentHealthFixtureWithRuntimeAccess(t, status, lastSeen, updatedAt, testUserID, "public")
}

func createAgentHealthFixtureWithRuntimeAccess(t *testing.T, status string, lastSeen, updatedAt time.Time, runtimeOwnerID, visibility string) (agentID, runtimeID string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at, updated_at, owner_id, visibility
		)
		VALUES ($1, NULL, $2, 'cloud', 'health-test',
			$3, '', '{}'::jsonb, $4, $5, $6, $7)
		RETURNING id
	`, testWorkspaceID, "health-runtime-"+randomID(), status, lastSeen, updatedAt, runtimeOwnerID, visibility).Scan(&runtimeID); err != nil {
		t.Fatalf("create health runtime: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "health-agent-"+randomID(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create health agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func dbAgentForHealthTest(t *testing.T) db.Agent {
	t.Helper()
	var agent db.Agent
	agent.ID = parseUUID("11111111-1111-1111-1111-111111111111")
	return agent
}

// Task #42③ "状态不撒谎": the agent list/detail surface must not pass through
// the raw agent_runtime.status column uncritically — it lags reality by up
// to ~180s (sweeper interval) and can say "online" indefinitely if the
// sweeper hasn't run yet. agentRuntimeDisplayStatus is the read-time-honest
// equivalent of runtimeConnectivity for that surface (agentHealthSummary
// already does this for the separate Activity Health tab).

func TestAgentRuntimeDisplayStatus_StaleOnlineRuntimeShowsOfflineNotComputerDisconnected(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-3 * time.Minute)), // stale (> 150s)
		UpdatedAt:  pgtimestamptz(now.Add(-2 * time.Minute)),
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusOffline {
		t.Fatalf("display status = %q, want %q (stale heartbeat must make the Agent offline)", got, agentDisplayStatusOffline)
	}
}

func TestAgentRuntimeDisplayStatus_PersistedOfflineShowsOffline(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "offline",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute)),
		UpdatedAt:  pgtimestamptz(now.Add(-6 * time.Minute)), // past reconnect window
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusOffline {
		t.Fatalf("display status = %q, want %q", got, agentDisplayStatusOffline)
	}
}

func TestAgentRuntimeDisplayStatus_FreshOnlineFollowsAgentWorkload(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second)), // fresh
		UpdatedAt:  pgtimestamptz(now.Add(-5 * time.Second)),
	}
	if got := agentRuntimeDisplayStatus("working", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now); got != agentDisplayStatusWorking {
		t.Fatalf("display status = %q, want %q for a fresh runtime with a working agent", got, agentDisplayStatusWorking)
	}
	if got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now); got != agentDisplayStatusIdle {
		t.Fatalf("display status = %q, want %q for a fresh runtime with an idle agent", got, agentDisplayStatusIdle)
	}
}

// TestAgentRuntimeDisplayStatus_Stopped is task ①'s (agent intentional-stop
// signal) read-side check: a runtime with a confirmed offline_reason must
// display as "stopped", not fall through to the generic "offline" case —
// even though UpdatedAt here is only seconds old (still inside what would
// otherwise be the Stale window).
func TestAgentRuntimeDisplayStatus_Stopped(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:        "offline",
		LastSeenAt:    pgtimestamptz(now.Add(-1 * time.Second)),
		UpdatedAt:     pgtimestamptz(now.Add(-1 * time.Second)),
		OfflineReason: pgtype.Text{String: "daemon_deregistered", Valid: true},
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusStopped {
		t.Fatalf("display status = %q, want %q for a confirmed offline_reason", got, agentDisplayStatusStopped)
	}
}

func pgtimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TestAgentRuntimeDisplayStatus_FreshStartingSinceOverridesStaleConnectivity
// keeps leftover starting_since readable: an older daemon may still have
// stamped the column, and connectivity alone would otherwise read Dead/Stale
// from before the restart. starting_since must win in that window.
func TestAgentRuntimeDisplayStatus_FreshStartingSinceOverridesStaleConnectivity(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:        "online",
		LastSeenAt:    pgtimestamptz(now.Add(-10 * time.Minute)), // long-dead by connectivity alone
		UpdatedAt:     pgtimestamptz(now.Add(-10 * time.Minute)),
		StartingSince: pgtimestamptz(now.Add(-5 * time.Second)), // just marked starting
	}
	if got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now); got != agentDisplayStatusStarting {
		t.Fatalf("display status = %q, want %q despite stale connectivity", got, agentDisplayStatusStarting)
	}
}

// TestAgentRuntimeDisplayStatus_ExpiredStartingSinceFallsThroughSafely proves
// the TTL is a genuine fallback, not a stuck state: if starting_since is
// older than agentRuntimeStartingTTL (the daemon never completed register —
// crashed again, lost the request, etc.), the runtime must NOT show
// "starting" forever. It falls through to today's ordinary connectivity-based
// tiering instead.
func TestAgentRuntimeDisplayStatus_ExpiredStartingSinceFallsThroughSafely(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:        "online",
		LastSeenAt:    pgtimestamptz(now.Add(-5 * time.Second)), // otherwise fresh/online
		UpdatedAt:     pgtimestamptz(now.Add(-5 * time.Second)),
		StartingSince: pgtimestamptz(now.Add(-90 * time.Second)), // past the 60s TTL
	}
	if got := agentRuntimeDisplayStatus("working", rt, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now); got != agentDisplayStatusWorking {
		t.Fatalf("display status = %q, want %q — expired starting_since must not override a fresh, otherwise-normal runtime", got, agentDisplayStatusWorking)
	}
}

func TestAgentRuntimeDisplayStatus_CrashedSinceShowsCrashedWhenConnectivityOnline(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second)),
		UpdatedAt:  pgtimestamptz(now.Add(-10 * time.Second)),
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtimestamptz(now.Add(-30*time.Second)), "", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusCrashed {
		t.Fatalf("display status = %q, want %q", got, agentDisplayStatusCrashed)
	}
}

func TestAgentRuntimeDisplayStatus_ProviderBlockBeatsIdleWhileOnline(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second)),
		UpdatedAt:  pgtimestamptz(now.Add(-10 * time.Second)),
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "quota", pgtimestamptz(now.Add(2*time.Hour)), now)
	if got != agentDisplayStatusBlocked {
		t.Fatalf("display status = %q, want %q", got, agentDisplayStatusBlocked)
	}
	// Unknown end (detail set, until NULL) stays locked — never invent a TTL.
	got = agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "quota", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusBlocked {
		t.Fatalf("unknown-until display status = %q, want %q", got, agentDisplayStatusBlocked)
	}
	// Expired known until unlocks.
	got = agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "quota", pgtimestamptz(now.Add(-time.Minute)), now)
	if got != agentDisplayStatusIdle {
		t.Fatalf("expired lock display status = %q, want %q", got, agentDisplayStatusIdle)
	}
	// "{}" is a serialization leftover, not quota copy — do not paint Blocked.
	got = agentRuntimeDisplayStatus("idle", rt, pgtype.Timestamptz{}, "{}", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusIdle {
		t.Fatalf("blank JSON lock detail display status = %q, want %q", got, agentDisplayStatusIdle)
	}
}

func TestAgentRuntimeDisplayStatus_WholeMachineOfflineBeatsStaleCrashFact(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute)),
		UpdatedAt:  pgtimestamptz(now.Add(-10 * time.Minute)),
	}
	got := agentRuntimeDisplayStatus("idle", rt, pgtimestamptz(now.Add(-1*time.Minute)), "", pgtype.Timestamptz{}, now)
	if got != agentDisplayStatusOffline {
		t.Fatalf("display status = %q, want %q — machine silence is offline, not crashed", got, agentDisplayStatusOffline)
	}
}

// End-to-end wiring check: attachAgentRuntimeNames is the actual code path
// the agent list/detail response goes through, not just the pure function.
// This catches the class of bug where the pure function is correct but
// never gets called with real data (e.g. wrong column read, wrong field
// assigned).
func TestAttachAgentRuntimeNames_StaleOnlineRuntimeGetsHonestDisplayStatusNotRawOnline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-3*time.Minute), // stale heartbeat
		time.Now().Add(-2*time.Minute),
	)

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimeStatus != "online" {
		t.Fatalf("RuntimeStatus = %q, want raw %q (this field must stay a passthrough for callers that need the dispatch-relevant raw value)", resps[0].RuntimeStatus, "online")
	}
	if resps[0].RuntimeDisplayStatus != agentDisplayStatusOffline {
		t.Fatalf("RuntimeDisplayStatus = %q, want %q — Computer disconnect must make the Agent offline", resps[0].RuntimeDisplayStatus, agentDisplayStatusOffline)
	}
}

// TestAttachAgentRuntimeNames_CrashedAgentShowsCrashed is the GET /agents
// wiring check Parker required for Raft status ②: seed agent.crashed_since,
// call attachAgentRuntimeNames (the only production call site of
// agentRuntimeDisplayStatus), assert RuntimeDisplayStatus == "crashed".
// Confirm-broken: temporarily skip ListAgentCrashedSinceByIDs / zero the
// map and this fails with idle; restored, it passes.
func TestAttachAgentRuntimeNames_CrashedAgentShowsCrashed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-10*time.Second),
		time.Now().Add(-10*time.Second),
	)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET crashed_since = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("seed crashed_since: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE agent SET crashed_since = NULL WHERE id = $1`, agentID)
	})

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimeDisplayStatus != agentDisplayStatusCrashed {
		t.Fatalf("RuntimeDisplayStatus = %q, want %q — attachAgentRuntimeNames must load agent.crashed_since",
			resps[0].RuntimeDisplayStatus, agentDisplayStatusCrashed)
	}
}
