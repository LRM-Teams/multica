package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// singleConnHandler returns a *Handler wired to a dedicated pgxpool.Pool
// capped at maxConns, otherwise identical to testHandler. This is how the
// #1803 attachAgentRuntimeNames deadlock (an open rows cursor from one
// Query() plus a second connection acquired before Close()) reproduces
// deterministically instead of only showing up under real concurrent load:
// with maxConns=1, the second acquire has zero spare connections the moment
// the first row is scanned, guaranteed, every run.
func singleConnHandler(t *testing.T, maxConns int32) *Handler {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse constrained pool config: %v", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create constrained pool: %v", err)
	}
	t.Cleanup(pool.Close)
	h := *testHandler
	h.DB = pool
	h.Queries = db.New(pool)
	h.TxStarter = pool
	return &h
}

// TestListSupervisedAgentDMChannels_SingleConnPoolDoesNotDeadlock pins the
// fix for listSupervisedAgentDMChannels calling agentDMParticipants (a
// second h.DB.Query) from inside the outer rows.Next() loop while that
// outer cursor is still open. Confirm-broken: reverting the fix (calling
// agentDMParticipants before the outer rows.Close()) makes this hang until
// the context deadline and silently drop the matching row (agentDMParticipants
// returns nil on error, so len(participants) != 2 skips it), even though the
// row exists.
func TestListSupervisedAgentDMChannels_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "dm-deadlock-a", []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "dm-deadlock-b", []byte("[]"))
	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	channel, created := testHandler.createDMChannel(ctx, nil, testWorkspaceID, testUserID, canonical, []dmMember{
		{memberType: "agent", memberID: parseUUID(firstAgentID)},
		{memberType: "agent", memberID: parseUUID(secondAgentID)},
	})
	if !created {
		t.Fatal("create supervised A2A DM channel failed")
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channel.ID) })

	h := singleConnHandler(t, 1)
	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	out := h.listSupervisedAgentDMChannels(reqCtx, testWorkspaceID, testUserID)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("listSupervisedAgentDMChannels took %s with a single-connection pool — cursor held open across a second Query() acquire (pool deadlock)", elapsed)
	}
	var found *DMItem
	for i := range out {
		if out[i].ID == channel.ID {
			found = &out[i]
		}
	}
	if found == nil || len(found.Participants) != 2 {
		t.Fatalf("listSupervisedAgentDMChannels = %+v, want channel %s with 2 participants", out, channel.ID)
	}
}

// TestListAgentRecentActivity_SingleConnPoolDoesNotDeadlock pins the fix for
// listAgentRecentActivity calling attachRecentExecutionActivity (which does
// its own h.DB.Query for task_message) from inside the outer rows.Next()
// loop. Confirm-broken: reverting the fix hangs until the context deadline
// and returns the activity item without its attached tool_use detail
// (attachRecentExecutionActivity silently returns on error).
func TestListAgentRecentActivity_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "recent-activity-deadlock", []byte(`{}`))

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority, trigger_summary, created_at, started_at)
		VALUES ($1, $2, 'draining', 0, 'deadlock test task', now() - interval '10 seconds', now() - interval '5 seconds')
		RETURNING id
	`, agentID, handlerTestRuntimeID(t)).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message (task_id, seq, type, tool, input, output)
		VALUES ($1, 1, 'tool_use', 'apply_patch', '{"path":"/repo/x.go"}', 'patch applied')
	`, taskID); err != nil {
		t.Fatalf("insert task message: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, taskID) })

	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}

	h := singleConnHandler(t, 1)
	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	items, err := h.listAgentRecentActivity(reqCtx, agent, 10)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("listAgentRecentActivity: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("listAgentRecentActivity took %s with a single-connection pool — cursor held open across a second Query() acquire (pool deadlock)", elapsed)
	}
	var found *AgentRecentActivityItem
	for i := range items {
		if items[i].ID == taskID {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatalf("listAgentRecentActivity = %+v, want an item for task %s", items, taskID)
	}
	if found.Kind != "tool_use" {
		t.Fatalf("item for task %s = %+v, want attached tool_use detail (kind=tool_use), got kind=%q — attachRecentExecutionActivity did not run",
			taskID, found, found.Kind)
	}
}

// TestListAgentReminders_SingleConnPoolDoesNotDeadlock pins the fix for
// ListAgentReminders calling safeHumanReminderAnchor (a second h.DB.QueryRow)
// from inside the outer rows.Next() loop. Confirm-broken: reverting the fix
// hangs until the context deadline and the response comes back with the
// reminder present but its anchor unresolved (safeHumanReminderAnchor
// returns Available:false on error).
func TestListAgentReminders_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "reminder anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	h := singleConnHandler(t, 1)
	req := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=scheduled", nil)
	req = withURLParam(req, "id", fixture.agentIDs[0])
	reqCtx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	req = req.WithContext(reqCtx)

	recorder := httptest.NewRecorder()
	start := time.Now()
	h.ListAgentReminders(recorder, req)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("ListAgentReminders took %s with a single-connection pool — cursor held open across a second QueryRow() acquire (pool deadlock)", elapsed)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListAgentReminders status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response humanReminderPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Definitions) != 1 || response.Definitions[0].ID != reminderID {
		t.Fatalf("response.Definitions = %+v, want exactly reminder %s", response.Definitions, reminderID)
	}
	if !response.Definitions[0].Anchor.Available {
		t.Fatalf("reminder anchor not resolved: %+v", response.Definitions[0].Anchor)
	}
}

// TestListAgentReminders_FiredLoop_SingleConnPoolDoesNotDeadlock covers the
// SECOND independent loop in ListAgentReminders (status=fired, occurrence
// history) — a separate code path from the scheduled-loop test above, fixed
// by the same PR but not previously covered by a single-conn-pool test
// (Alice's review note on #1812, non-blocking at the time; Parker asked for
// it to land in this PR rather than a follow-up card).
func TestListAgentReminders_FiredLoop_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "fired reminder anchor", nil)
	ctx := context.Background()

	var reminderID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
			anchor_message_id, fire_at, status
		) VALUES ($1, $2, $3, $4, $5, $6, now() - interval '1 hour', 'fired')
		RETURNING id
	`, testWorkspaceID, fixture.agentIDs[0], testUserID, "fired deadlock test reminder",
		fixture.channel.ID, anchor.ID).Scan(&reminderID); err != nil {
		t.Fatalf("seed fired reminder: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_reminder WHERE id = $1`, reminderID) })

	var occurrenceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_reminder_occurrence (
			reminder_id, workspace_id, agent_id, fire_version, cadence_scheduled_for, due_at,
			status, title_snapshot, fired_at
		) VALUES ($1, $2, $3, 1, now() - interval '1 hour', now() - interval '1 hour',
		          'fired', 'fired deadlock test reminder', now() - interval '1 hour')
		RETURNING id
	`, reminderID, testWorkspaceID, fixture.agentIDs[0]).Scan(&occurrenceID); err != nil {
		t.Fatalf("seed fired occurrence: %v", err)
	}

	h := singleConnHandler(t, 1)
	req := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil)
	req = withURLParam(req, "id", fixture.agentIDs[0])
	reqCtx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	req = req.WithContext(reqCtx)

	recorder := httptest.NewRecorder()
	start := time.Now()
	h.ListAgentReminders(recorder, req)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("ListAgentReminders(status=fired) took %s with a single-connection pool — cursor held open across a second QueryRow() acquire (pool deadlock)", elapsed)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListAgentReminders status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response humanReminderPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Occurrences) != 1 || response.Occurrences[0].ID != occurrenceID {
		t.Fatalf("response.Occurrences = %+v, want exactly occurrence %s", response.Occurrences, occurrenceID)
	}
	if !response.Occurrences[0].Anchor.Available {
		t.Fatalf("fired occurrence anchor not resolved: %+v", response.Occurrences[0].Anchor)
	}
}
