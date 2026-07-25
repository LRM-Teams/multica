package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type recordingReminderNotifier struct {
	starts      []protocol.DaemonAgentStartPayload
	stops       []protocol.DaemonAgentStopPayload
	projections []protocol.ReminderProjectionEvent
	order       []string
}

func (n *recordingReminderNotifier) NotifyReminderProjection(_ string, payload protocol.ReminderProjectionEvent) {
	n.projections = append(n.projections, payload)
	n.order = append(n.order, "projection")
}
func (n *recordingReminderNotifier) NotifyReminderOwnerAdded(_ string, payload protocol.DaemonAgentStartPayload) {
	n.starts = append(n.starts, payload)
	n.order = append(n.order, "start")
}
func (n *recordingReminderNotifier) NotifyReminderOwnerRemoved(_ string, payload protocol.DaemonAgentStopPayload) {
	n.stops = append(n.stops, payload)
	n.order = append(n.order, "stop")
}

// TestListWorkspaceAgentTaskSnapshot covers the agent presence snapshot endpoint:
// every active task (queued/dispatched/running) PLUS each agent's most recent
// OUTCOME task (completed/failed only). Cancelled tasks are excluded by design
// from the outcome half — they're a procedural signal, not an outcome, and
// must NOT mask a prior failure.
//
// The fixtures cover every branch the SQL must classify:
//   - actives are always returned, no dedup
//   - outcomes are deduped to "latest per agent" by completed_at
//   - the OLD 2-minute window must be irrelevant (a 5-minute-old failure is
//     still returned if it's the latest outcome)
//   - cancelled rows are NEVER returned, even when they are temporally newer
//     than a failure — this is what keeps the failed signal sticky after the
//     user cancels their queued retry
func TestListWorkspaceAgentTaskSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	// Three agents so we can verify per-agent semantics independently.
	agentA := createHandlerTestAgent(t, "snapshot-agent-a", []byte(`{}`))
	agentB := createHandlerTestAgent(t, "snapshot-agent-b", []byte(`{}`))
	agentC := createHandlerTestAgent(t, "snapshot-agent-c", []byte(`{}`))
	agentD := createHandlerTestAgent(t, "snapshot-agent-inbox-queued", []byte(`{}`))
	agentE := createHandlerTestAgent(t, "snapshot-agent-inbox-running", []byte(`{}`))

	type taskFixture struct {
		agentID     string
		status      string
		completedAt string // SQL expression; "" for NULL
		label       string
	}
	fixtures := []taskFixture{
		// Agent A — actives + a newer completed supersedes an older failed.
		{agentA, "queued", "", "A.queued"},
		{agentA, "dispatched", "", "A.dispatched"},
		{agentA, "running", "", "A.running"},
		{agentA, "failed", "now() - interval '10 minutes'", "A.old_failed"},
		{agentA, "completed", "now() - interval '30 seconds'", "A.latest_completed"},

		// Agent B — old failure with no later outcome stays visible (no
		// time window).
		{agentB, "failed", "now() - interval '5 minutes'", "B.stale_failed_kept"},

		// Agent C — failure followed by a NEWER cancelled. The cancelled
		// must be skipped by the SQL filter so the failure remains visible.
		// This is the scenario where a user fails, then cancels their
		// queued retry to debug.
		{agentC, "failed", "now() - interval '5 minutes'", "C.failure"},
		{agentC, "cancelled", "now() - interval '30 seconds'", "C.newer_cancelled_must_be_ignored"},
	}

	insertedIDs := make([]string, 0, len(fixtures))
	quickCreateContext := fmt.Sprintf(`{"type":"quick_create","workspace_id":%q,"prompt":"snapshot fixture"}`, testWorkspaceID)
	for _, f := range fixtures {
		var id string
		status := f.status
		terminalOutcome := ""
		startedAt := "NULL"
		switch f.status {
		case "queued":
			status = "pending"
		case "dispatched":
			status = "draining"
		case "running":
			status = "draining"
			startedAt = "now()"
		case "completed":
			status = "acked"
			terminalOutcome = "completed"
		case "failed":
			status = "acked"
			terminalOutcome = "failed"
		case "cancelled":
			status = "suppressed"
			terminalOutcome = "cancelled"
		}
		completedAt := "NULL"
		if f.completedAt == "" {
			completedAt = "NULL"
		} else {
			completedAt = f.completedAt
		}
		query := `INSERT INTO agent_inbox_event (
			         agent_id, runtime_id, status, priority, completed_at,
			         terminal_outcome, terminal_at, acked_at, started_at, context
			       )
			       VALUES ($1, $2, $3, 0, ` + completedAt + `,
			         NULLIF($4, ''), ` + completedAt + `, ` + completedAt + `,
			         ` + startedAt + `, $5::jsonb)
			       RETURNING id`
		if err := testPool.QueryRow(ctx, query, f.agentID, testRuntimeID, status, terminalOutcome, quickCreateContext).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", f.label, err)
		}
		insertedIDs = append(insertedIDs, id)
	}
	var malformedID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'pending', 0)
		RETURNING id
	`, agentA, testRuntimeID).Scan(&malformedID); err != nil {
		t.Fatalf("insert malformed no-source active task: %v", err)
	}
	insertedIDs = append(insertedIDs, malformedID)
	t.Cleanup(func() {
		for _, id := range insertedIDs {
			testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, id)
		}
	})

	channelID := seedChannelForTest(t, "snapshot-inbox-"+uuid.NewString(), testUserID)
	for _, agentID := range []string{agentD, agentE} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member %s: %v", agentID, err)
		}
	}
	queuedRoot := dispatchThreadMentionForTest(t, channelID, agentD, "snapshot-inbox-queued-"+uuid.NewString())
	queuedInboxEventID := latestChannelAgentInboxEventForRootForTest(t, queuedRoot.ID, agentD)
	runningRoot := dispatchThreadMentionForTest(t, channelID, agentE, "snapshot-inbox-running-"+uuid.NewString())
	runningInboxEventID := latestChannelAgentInboxEventForRootForTest(t, runningRoot.ID, agentE)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'draining',
		    claimed_at = now(),
		    started_at = now(),
		    updated_at = now()
		WHERE id = $1`, runningInboxEventID); err != nil {
		t.Fatalf("mark inbox event draining: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id,
			agent_session_id,
			inbox_event_id,
			runtime_id,
			status
		)
		SELECT workspace_id,
		       agent_session_id,
		       id,
		       $2,
		       'leased'
		FROM agent_inbox_event
		WHERE id = $1`, runningInboxEventID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("insert inbox delivery: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/agent-task-snapshot", nil)
	testHandler.ListWorkspaceAgentTaskSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListWorkspaceAgentTaskSnapshot: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Per-agent breakdown so leftover tasks from other tests in this package
	// don't pollute the assertions.
	type key struct{ agent, status string }
	counts := map[key]int{}
	for _, task := range tasks {
		if task.AgentID != agentA && task.AgentID != agentB && task.AgentID != agentC {
			if task.AgentID != agentD && task.AgentID != agentE {
				continue
			}
		}
		counts[key{task.AgentID, task.Status}]++
	}

	wantCounts := map[key]int{
		// Agent A: 3 actives + the latest outcome (completed). The older
		// failed must be excluded by DISTINCT ON.
		{agentA, "queued"}:     1,
		{agentA, "dispatched"}: 1,
		{agentA, "running"}:    1,
		{agentA, "completed"}:  1,
		// Agent B: just the failed outcome.
		{agentB, "failed"}: 1,
		// Agent C: the failed outcome must survive the temporally newer
		// cancellation — that's the whole point of excluding cancelled
		// from the outcome half.
		{agentC, "failed"}: 1,
		// Agent D/E: active new-chain inbox events must contribute to the
		// same snapshot that derives avatar/header presence. Otherwise active
		// chat work falls through to the legacy "Idle" presence word.
		{agentD, "queued"}:  1,
		{agentE, "running"}: 1,
	}
	for k, expected := range wantCounts {
		if got := counts[k]; got != expected {
			t.Errorf("agent=%s status=%s: expected %d, got %d", k.agent, k.status, expected, got)
		}
	}
	byID := map[string]AgentTaskResponse{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	queuedInboxTask, ok := byID[queuedInboxEventID]
	if !ok {
		t.Fatalf("queued inbox event %s missing from snapshot", queuedInboxEventID)
	}
	if queuedInboxTask.Kind != "chat" || queuedInboxTask.ChatSessionID == "" || queuedInboxTask.TriggerSummary == nil || strings.TrimSpace(*queuedInboxTask.TriggerSummary) == "" {
		t.Fatalf("queued inbox task = %+v, want chat task with session and trigger summary", queuedInboxTask)
	}
	if queuedInboxTask.ActorID != agentD || queuedInboxTask.ActorType != "agent" || queuedInboxTask.DisplayName == "" || queuedInboxTask.ActorStatus != "visible" || queuedInboxTask.Actor == nil || queuedInboxTask.Actor.DisplayName != queuedInboxTask.DisplayName || queuedInboxTask.Handle == nil {
		t.Fatalf("queued inbox actor identity = %+v, want stable visible agent snapshot", queuedInboxTask)
	}
	runningInboxTask, ok := byID[runningInboxEventID]
	if !ok {
		t.Fatalf("running inbox event %s missing from snapshot", runningInboxEventID)
	}
	if runningInboxTask.Status != "running" || runningInboxTask.StartedAt == nil {
		t.Fatalf("running inbox task = %+v, want running with started_at", runningInboxTask)
	}

	// The OLD failed terminal on agent A must be excluded.
	for _, task := range tasks {
		if task.AgentID == agentA && task.Status == "queued" {
			if task.ActorID != agentA || task.ActorType != "agent" || task.DisplayName == "" || task.ActorStatus != "visible" || task.Actor == nil {
				t.Fatalf("legacy active task actor identity = %+v, want populated actor snapshot", task)
			}
			break
		}
	}

	if counts[key{agentA, "failed"}] != 0 {
		t.Errorf("agent A old failed must be superseded by newer completed; got %d", counts[key{agentA, "failed"}])
	}
	if counts[key{agentA, "queued"}] != 1 {
		t.Errorf("agent A malformed no-source queued task must be excluded; queued count = %d", counts[key{agentA, "queued"}])
	}

	// No cancelled row may ever appear in the snapshot — they're filtered at
	// SQL level so the front-end's "cancel doesn't mask failure" rule lands
	// without any front-end logic.
	for _, agentID := range []string{agentA, agentB, agentC} {
		if counts[key{agentID, "cancelled"}] != 0 {
			t.Errorf("agent %s: cancelled rows must be excluded from snapshot; got %d",
				agentID, counts[key{agentID, "cancelled"}])
		}
	}
}

func TestListWorkspaceAgentTaskSnapshotIncludesActorIdentity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	visibleAgentID := createHandlerTestAgent(t, "snapshot-identity-visible-"+uuid.NewString()[:8], []byte(`{}`))
	privateAgentID := createHandlerTestAgent(t, "snapshot-identity-private-"+uuid.NewString()[:8], []byte(`{}`))
	plainMemberID := createWorkspaceMemberUser(t, "Snapshot Plain Member", "snapshot-plain-"+uuid.NewString()+"@multica.test")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET display_name = CASE id
			WHEN $1 THEN 'Snapshot Visible Agent'
			WHEN $2 THEN 'Snapshot Private Agent'
		END,
		avatar_url = CASE WHEN id = $1 THEN 'https://example.test/snapshot.png' ELSE avatar_url END
		WHERE id IN ($1, $2)`, visibleAgentID, privateAgentID); err != nil {
		t.Fatalf("update snapshot agents: %v", err)
	}

	quickCreateContext := fmt.Sprintf(`{"type":"quick_create","workspace_id":%q,"prompt":"snapshot identity"}`, testWorkspaceID)
	insertTask := func(agentID, status string, started bool) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
				workspace_id, agent_id, reason, requires_wake, status, priority,
				runtime_id, context, started_at
			)
			VALUES ($1, $2, 'quick_create', true, $3, 0, $4, $5::jsonb,
			        CASE WHEN $6 THEN now() ELSE NULL END)
			RETURNING id`, testWorkspaceID, agentID, status, handlerTestRuntimeID(t), quickCreateContext, started).Scan(&id); err != nil {
			t.Fatalf("insert snapshot identity task: %v", err)
		}
		return id
	}
	visibleTaskID := insertTask(visibleAgentID, "pending", false)
	privateTaskID := insertTask(privateAgentID, "draining", true)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id IN ($1, $2)`, visibleTaskID, privateTaskID)
	})

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/agent-task-snapshot", nil)
	testHandler.ListWorkspaceAgentTaskSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner snapshot status=%d body=%s", w.Code, w.Body.String())
	}
	var ownerTasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&ownerTasks); err != nil {
		t.Fatalf("decode owner tasks: %v", err)
	}
	visible, ok := findAgentTaskResponse(ownerTasks, visibleTaskID)
	if !ok {
		t.Fatalf("visible task %s missing", visibleTaskID)
	}
	if visible.ActorID != visibleAgentID || visible.ActorType != "agent" || visible.DisplayName != "Snapshot Visible Agent" || visible.ActorStatus != "visible" || visible.Actor == nil || visible.Actor.DisplayName != visible.DisplayName || visible.AvatarURL == nil || visible.Handle == nil {
		t.Fatalf("visible task identity = %+v, want populated actor snapshot", visible)
	}

	w = httptest.NewRecorder()
	req = newRequestAs(plainMemberID, http.MethodGet, "/api/agent-task-snapshot", nil)
	testHandler.ListWorkspaceAgentTaskSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("member snapshot status=%d body=%s", w.Code, w.Body.String())
	}
	var memberTasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&memberTasks); err != nil {
		t.Fatalf("decode member tasks: %v", err)
	}
	privateTask, ok := findAgentTaskResponse(memberTasks, privateTaskID)
	if !ok {
		t.Fatalf("private task %s missing", privateTaskID)
	}
	if privateTask.ActorID != privateAgentID || privateTask.ActorType != "agent" || privateTask.DisplayName != "Hidden agent" || privateTask.ActorStatus != "hidden" || privateTask.Actor == nil || privateTask.Actor.Status != "hidden" {
		t.Fatalf("private task identity = %+v, want hidden actor snapshot", privateTask)
	}
}

func findAgentTaskResponse(tasks []AgentTaskResponse, id string) (AgentTaskResponse, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return AgentTaskResponse{}, false
}

func TestListAgentTasksUsesSingleInboxHistory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "agent-task-inbox-history-"+uuid.NewString()[:8], []byte(`{}`))
	channelID := seedChannelForTest(t, "agent-task-inbox-history-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	root := dispatchThreadMentionForTest(t, channelID, agentID, "agent-task-inbox-source-"+uuid.NewString())
	inboxEventID := latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)
	setAgentInboxTerminalOutcomeForTest(t, inboxEventID, "replied", false)

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(chat_session_id::text, '')
		FROM agent_inbox_event
		WHERE id = $1`, inboxEventID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load inbox chat session: %v", err)
	}
	if chatSessionID == "" {
		t.Fatalf("inbox event %s has no chat_session_id", inboxEventID)
	}

	var legacyChatTaskID, legacyRadarTaskID, activeRadarTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, chat_session_id, status, priority,
			trigger_summary, created_at, started_at
		)
		VALUES ($1, $2, $3, 'draining', 0, 'legacy chat row must be hidden',
		        now() - interval '5 minutes', now() - interval '4 minutes')
		RETURNING id`, agentID, handlerTestRuntimeID(t), chatSessionID).Scan(&legacyChatTaskID); err != nil {
		t.Fatalf("insert legacy chat task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, context,
			trigger_summary, created_at, dispatched_at
		)
		VALUES ($1, $2, 'draining', 0, '{"type":"agent_radar"}'::jsonb,
		        'legacy radar row should read as historical',
		        now() - interval '10 minutes', now() - interval '9 minutes')
		RETURNING id`, agentID, handlerTestRuntimeID(t)).Scan(&legacyRadarTaskID); err != nil {
		t.Fatalf("insert legacy radar task: %v", err)
	}
	activeRadarRunID := uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, context,
			trigger_summary, created_at, dispatched_at
		)
		VALUES ($1, $2, 'draining', 0,
		        jsonb_build_object('type', 'agent_radar', 'radar_run_id', $3::text),
		        'current radar row should preserve its status',
		        now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id`, agentID, handlerTestRuntimeID(t), activeRadarRunID).Scan(&activeRadarTaskID); err != nil {
		t.Fatalf("insert current radar task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id IN ($1, $2, $3)`, legacyChatTaskID, legacyRadarTaskID, activeRadarTaskID)
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID+"/tasks", nil), "id", agentID)
	testHandler.ListAgentTasks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentTasks: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode agent tasks: %v", err)
	}
	byID := map[string]AgentTaskResponse{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	chatTask, ok := byID[legacyChatTaskID]
	if !ok || chatTask.Status != "running" || chatTask.Kind != "chat" {
		t.Fatalf("canonical chat task %s missing from agent task history: %+v", legacyChatTaskID, chatTask)
	}
	inboxTask, ok := byID[inboxEventID]
	if !ok {
		t.Fatalf("missing inbox event task %s in response: %+v", inboxEventID, tasks)
	}
	if inboxTask.Status != "completed" || inboxTask.Kind != "chat" || inboxTask.ChatSessionID != chatSessionID || inboxTask.CompletedAt == nil {
		t.Fatalf("inbox task = %+v, want completed chat row with chat_session_id + completed_at", inboxTask)
	}
	if inboxTask.TriggerSummary == nil || strings.TrimSpace(*inboxTask.TriggerSummary) == "" || strings.Contains(*inboxTask.TriggerSummary, "legacy chat") {
		t.Fatalf("inbox task trigger summary = %#v, want non-empty inbox summary", inboxTask.TriggerSummary)
	}
	legacyRadarTask, ok := byID[legacyRadarTaskID]
	if !ok {
		t.Fatalf("missing legacy radar task %s in response: %+v", legacyRadarTaskID, tasks)
	}
	if legacyRadarTask.Status != "dispatched" || legacyRadarTask.CompletedAt != nil {
		t.Fatalf("canonical radar task = %+v, want dispatched row", legacyRadarTask)
	}

	radarTask, ok := byID[activeRadarTaskID]
	if !ok {
		t.Fatalf("missing active radar task %s in response: %+v", activeRadarTaskID, tasks)
	}
	if radarTask.Status != "dispatched" || radarTask.CompletedAt != nil {
		t.Fatalf("active radar task = %+v, want dispatched row without completed_at", radarTask)
	}
}

func TestGetMemberProfile_AgentReturnsSafeRecentActivity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "recent-activity-agent", []byte(`{}`))
	longSummary := strings.Repeat("activity ", 30)

	var olderID, newerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, trigger_summary,
			terminal_outcome, created_at, started_at, completed_at, terminal_at, error
		)
		VALUES ($1, $2, 'acked', 0, $3, 'failed', now() - interval '3 minutes',
		        now() - interval '2 minutes', now() - interval '1 minute',
		        now() - interval '1 minute', 'raw stacktrace should not leak')
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), longSummary).Scan(&olderID); err != nil {
		t.Fatalf("insert older task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message (task_id, seq, type, tool, input, output)
		VALUES ($1, 1, 'tool_use', 'exec_command',
		        '{"cmd":"pnpm --filter @multica/web build --token sk-proj-abc123def456ghi789jkl012mno345"}',
		        'raw command output should not leak')
	`, olderID); err != nil {
		t.Fatalf("insert command task message: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, trigger_summary,
			created_at, started_at
		)
		VALUES ($1, $2, 'draining', 0, 'newer work item', now() - interval '30 seconds',
		        now() - interval '10 seconds')
		RETURNING id
	`, agentID, handlerTestRuntimeID(t)).Scan(&newerID); err != nil {
		t.Fatalf("insert newer task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message (task_id, seq, type, tool, input, output)
		VALUES ($1, 1, 'tool_use', 'apply_patch',
		        '{"path":"/repo/server/internal/handler/profile.go"}',
		        'raw patch output should not leak')
	`, newerID); err != nil {
		t.Fatalf("insert file task message: %v", err)
	}
	extraIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
				agent_id, runtime_id, status, priority, trigger_summary,
				created_at, completed_at
			)
			VALUES ($1, $2, 'acked', 0, $3, now() - ($4::int * interval '10 minutes'),
			        now() - ($4::int * interval '10 minutes'))
			RETURNING id
		`, agentID, handlerTestRuntimeID(t), fmt.Sprintf("fallback task %d", i+1), i+1).Scan(&id); err != nil {
			t.Fatalf("insert extra task %d: %v", i, err)
		}
		extraIDs = append(extraIDs, id)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id IN ($1, $2)`, olderID, newerID)
		for _, id := range extraIDs {
			testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, id)
		}
	})

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var profile MemberProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&profile); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if profile.MemberType != "agent" || profile.MemberID != agentID {
		t.Fatalf("profile identity = %#v, want agent %s", profile, agentID)
	}
	if profile.Role != "Agent" {
		t.Fatalf("agent profile role = %q, want Agent", profile.Role)
	}
	if profile.ProfileAccess != "full" {
		t.Fatalf("profile_access = %q, want full", profile.ProfileAccess)
	}
	items := profile.RecentActivity
	if len(items) != 5 {
		t.Fatalf("expected 5 activity items, got %d: %#v", len(items), items)
	}
	if items[0].ID != newerID || items[0].Kind != "tool_use" || items[0].Status != "running" {
		t.Fatalf("newest activity = %#v, want running file activity %s", items[0], newerID)
	}
	if items[0].Label != "Editing file" {
		t.Fatalf("newest activity projection = %#v, want Editing file", items[0])
	}
	if items[1].ID != olderID || items[1].Kind != "command" || items[1].Status != "failed" {
		t.Fatalf("older activity = %#v, want command task %s", items[1], olderID)
	}
	if items[1].Label != "Running command…" {
		t.Fatalf("command activity label = %q, want Running command…", items[1].Label)
	}
	body := w.Body.String()
	for _, leak := range []string{
		`"summary"`,
		"newer work item",
		"fallback task",
		"profile.go",
		"raw stacktrace",
		"raw command output",
		"raw patch output",
		"exec_command",
		"apply_patch",
		"pnpm --filter @multica/web build",
		"--token",
		"sk-proj-abc123def456ghi789jkl012mno345",
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("recent activity leaked %q: %s", leak, body)
		}
	}
	if items[0].OccurredAt == "" || items[1].OccurredAt == "" {
		t.Fatalf("expected occurred_at on every item: %#v", items)
	}
}

func TestGetMemberProfile_GroupManagerReturnsDisplayName(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "beckham-profile-"+uuid.NewString()[:8], []byte(`{}`))
	displayName := "贝克汉姆"
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET managed_role = 'group_manager', display_name = $2, visibility = 'private'
		WHERE id = $1
	`, agentID, displayName); err != nil {
		t.Fatalf("mark group manager: %v", err)
	}

	// ListAgents must hide the manager (LRM-233).
	listRec := httptest.NewRecorder()
	listReq := newRequest(http.MethodGet, "/api/agents", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, testUserID)
	testHandler.ListAgents(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListAgents: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed []AgentResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode ListAgents: %v", err)
	}
	for _, a := range listed {
		if a.ID == agentID {
			t.Fatalf("group manager %s leaked into ListAgents", agentID)
		}
	}

	// Member profile must still resolve the display name from DB (LRM-281).
	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile: status=%d body=%s", w.Code, w.Body.String())
	}
	var profile MemberProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.DisplayName != displayName {
		t.Fatalf("profile display_name = %q, want %q (body=%s)", profile.DisplayName, displayName, w.Body.String())
	}
}

func TestGetMemberProfile_PrivateAgentReturnsIdentityOnlyForPlainMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, _, memberID := privateAgentTestFixture(t)
	description := "Private agent identity that is already visible in a message."
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET description = $2
		WHERE id = $1
	`, agentID, description); err != nil {
		t.Fatalf("update private agent description: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, trigger_summary,
			created_at, started_at
		)
		VALUES ($1, $2, 'draining', 0, 'protected work must not leak',
		        now() - interval '30 seconds', now() - interval '10 seconds')
	`, agentID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("insert protected task: %v", err)
	}

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequestAs(memberID, http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var profile MemberProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&profile); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if profile.MemberType != "agent" || profile.MemberID != agentID {
		t.Fatalf("profile identity = %#v, want agent %s", profile, agentID)
	}
	if profile.ProfileAccess != "identity_only" {
		t.Fatalf("profile_access = %q, want identity_only", profile.ProfileAccess)
	}
	if profile.Status != nil {
		t.Fatalf("identity-only profile status = %q, want nil", *profile.Status)
	}
	if len(profile.RecentActivity) != 0 {
		t.Fatalf("identity-only profile leaked activity: %#v", profile.RecentActivity)
	}
	if profile.Description != description {
		t.Fatalf("description = %q, want %q", profile.Description, description)
	}
	body := w.Body.String()
	for _, leak := range []string{"protected work must not leak", `"status":"running"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("identity-only profile leaked %q: %s", leak, body)
		}
	}
}

func TestProjectTextActivity_ProjectsActionWithoutSummary(t *testing.T) {
	for _, content := range []string{
		"9a5-d69cd32e15e6)",
		"550e8400-e29b-41d4-a716-446655440000",
		"sk-proj-abc123def456",
		"I finished updating the profile activity summary.",
		"我已完成资料页活动摘要清理",
	} {
		item, ok := projectTextActivity(recentTaskActivityMessage{
			Type:    "text",
			Content: pgtype.Text{String: content, Valid: true},
		}, "completed")
		if !ok {
			t.Fatalf("projectTextActivity returned ok=false for %q", content)
		}
		if item.Label != "Output" {
			t.Fatalf("label = %q, want Output", item.Label)
		}
	}
}

func TestCreateAgent_GeneratesUniqueHandlesForDuplicateDisplayNames(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	displayName := "小雅"
	handle := "xiao-ya"
	cleanup := func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent
			 WHERE workspace_id = $1
			   AND (display_name = $2 OR name LIKE $3)`,
			testWorkspaceID, displayName, handle+"%",
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	body := map[string]any{
		"display_name":         displayName,
		"description":          "first description",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}

	// First call — creates the agent.
	w1 := httptest.NewRecorder()
	testHandler.CreateAgent(w1, newRequest(http.MethodPost, "/api/agents", body))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first CreateAgent: expected 201, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 AgentResponse
	if err := json.NewDecoder(w1.Body).Decode(&resp1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if resp1.ID == "" {
		t.Fatalf("first CreateAgent: no id in response: %v", resp1)
	}
	if resp1.Name != handle {
		t.Fatalf("first handle = %q, want %q", resp1.Name, handle)
	}
	if resp1.DisplayName != displayName {
		t.Fatalf("first display_name = %q, want %q", resp1.DisplayName, displayName)
	}

	// Second call — same display input is allowed. The server keeps the
	// display label and suffixes only the stable handle.
	body["description"] = "updated description"
	w2 := httptest.NewRecorder()
	testHandler.CreateAgent(w2, newRequest(http.MethodPost, "/api/agents", body))
	if w2.Code != http.StatusCreated {
		t.Fatalf("second CreateAgent with duplicate display name: expected 201, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 AgentResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp2.DisplayName != displayName {
		t.Fatalf("second display_name = %q, want %q", resp2.DisplayName, displayName)
	}
	if resp2.Name != handle+"-2" {
		t.Fatalf("second handle = %q, want %q", resp2.Name, handle+"-2")
	}
}

func TestCreateAgent_RejectsDuplicateExplicitUsername(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	handle := "qa-bot-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, handle)
	})
	body := map[string]any{
		"username":             handle,
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}

	first := httptest.NewRecorder()
	testHandler.CreateAgent(first, newRequest(http.MethodPost, "/api/agents", body))
	if first.Code != http.StatusCreated {
		t.Fatalf("first explicit username: expected 201, got %d: %s", first.Code, first.Body.String())
	}
	var created AgentResponse
	if err := json.NewDecoder(first.Body).Decode(&created); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if created.Name != handle || created.DisplayName != handle {
		t.Fatalf("created agent = %+v, want username/display_name %q", created, handle)
	}
	second := httptest.NewRecorder()
	testHandler.CreateAgent(second, newRequest(http.MethodPost, "/api/agents", body))
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate explicit username: expected 409, got %d: %s", second.Code, second.Body.String())
	}
}

func TestCreateAgent_RejectsNonASCIIExplicitUsername(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"username":             "小雅",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-ASCII username: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgent_GeneratesASCIIUsernamesFromDisplayNames(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	marker := "identity-preservation-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE workspace_id = $1 AND description = $2`, testWorkspaceID, marker)
	})

	for _, tt := range []struct {
		displayName string
		wantHandle  string
	}{
		{"阿策", "a-ce"},
		{"café", "cafe"},
		{"qa-bot", "qa-bot"},
	} {
		t.Run(tt.displayName, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
				"display_name":         tt.displayName,
				"description":          marker,
				"runtime_id":           testRuntimeID,
				"visibility":           "private",
				"max_concurrent_tasks": 1,
			}))
			if w.Code != http.StatusCreated {
				t.Fatalf("CreateAgent: expected 201, got %d: %s", w.Code, w.Body.String())
			}
			var created AgentResponse
			if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
				t.Fatalf("decode create response: %v", err)
			}
			if created.Name != tt.wantHandle {
				t.Fatalf("username = %q, want %q", created.Name, tt.wantHandle)
			}
		})
	}
}

func TestAgentRejectsLegacyNameField(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	createBody := map[string]any{
		"display_name":         "Legacy Rename Agent",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	legacyCreate := map[string]any{
		"name":                 "Legacy Rename Agent",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	legacyCreateRec := httptest.NewRecorder()
	testHandler.CreateAgent(legacyCreateRec, newRequest(http.MethodPost, "/api/agents", legacyCreate))
	if legacyCreateRec.Code != http.StatusBadRequest {
		t.Fatalf("CreateAgent legacy name: expected 400, got %d: %s", legacyCreateRec.Code, legacyCreateRec.Body.String())
	}
	createRec := httptest.NewRecorder()
	testHandler.CreateAgent(createRec, newRequest(http.MethodPost, "/api/agents", createBody))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateAgent: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created AgentResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Name == "" {
		t.Fatal("created agent missing generated handle")
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(created.ID))
	})

	req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+created.ID, map[string]any{
		"name": "Renamed Legacy Display",
	}), "id", created.ID)
	updateRec := httptest.NewRecorder()
	testHandler.UpdateAgent(updateRec, req)
	if updateRec.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent legacy name: expected 400, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
}

func TestUpdateAgent_UsernameChangesHandle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	marker := "username-update-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	createRec := httptest.NewRecorder()
	testHandler.CreateAgent(createRec, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":         "贝克汉姆",
		"description":          marker,
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateAgent: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created AgentResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(created.ID))
	})

	updateRec := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPut, "/api/agents/"+created.ID, map[string]any{
		"username": "beckham-eng",
	}), "id", created.ID)
	testHandler.UpdateAgent(updateRec, request)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated AgentResponse
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated agent: %v", err)
	}
	if updated.Name != "beckham-eng" {
		t.Fatalf("username = %q, want beckham-eng", updated.Name)
	}
	if updated.DisplayName != "贝克汉姆" {
		t.Fatalf("display_name = %q, want 贝克汉姆", updated.DisplayName)
	}
}

func TestWorkspaceAlwaysRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		settings []byte
		want     bool
	}{
		{"nil settings", nil, false},
		{"empty settings", []byte(`{}`), false},
		{"false", []byte(`{"always_redact_env": false}`), false},
		{"true", []byte(`{"always_redact_env": true}`), true},
		{"invalid json", []byte(`not json`), false},
		{"other fields only", []byte(`{"theme": "dark"}`), false},
		{"true among other fields", []byte(`{"theme": "dark", "always_redact_env": true}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspaceAlwaysRedactSecrets(tt.settings); got != tt.want {
				t.Errorf("workspaceAlwaysRedactSecrets(%q) = %v, want %v", tt.settings, got, tt.want)
			}
		})
	}
}

// rawJSONResponse decodes the raw map so we can assert the literal
// JSON shape — `custom_env` MUST be absent from the wire output, not
// merely empty, otherwise a future caller decoding into a wider struct
// could still see masked or partial values.
func rawJSONResponse(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return out
}

// TestGetAgent_ResponseHasNoCustomEnv guards the core invariant from
// MUL-2600: the generic agent resource response NEVER carries the
// custom_env field, even for the agent's owner. Only the dedicated
// env endpoint exposes secret values.
func TestGetAgent_ResponseHasNoCustomEnv(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "noenv-get-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"SECRET_KEY": "super-secret"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/agents/"+agentID, nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	raw := rawJSONResponse(t, w.Body.Bytes())
	if _, ok := raw["custom_env"]; ok {
		t.Errorf("custom_env field must not appear in agent response, got %v", raw["custom_env"])
	}
	if _, ok := raw["custom_env_redacted"]; ok {
		t.Errorf("custom_env_redacted field must not appear in agent response (use has_custom_env)")
	}
	if got, _ := raw["has_custom_env"].(bool); !got {
		t.Errorf("has_custom_env expected true, got %v", raw["has_custom_env"])
	}
	if got, _ := raw["custom_env_key_count"].(float64); got != 1 {
		t.Errorf("custom_env_key_count expected 1, got %v", raw["custom_env_key_count"])
	}

	// Sanity-check the typed shape too — the struct must not have
	// rehydrated the masked map.
	var typed AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &typed); err != nil {
		t.Fatalf("typed decode failed: %v", err)
	}
	if typed.HasCustomEnv != true {
		t.Errorf("typed.HasCustomEnv expected true")
	}
	if typed.CustomEnvKeyCount != 1 {
		t.Errorf("typed.CustomEnvKeyCount expected 1, got %d", typed.CustomEnvKeyCount)
	}
}

// TestListAgents_ResponseHasNoCustomEnv mirrors the GetAgent guard for
// the list endpoint. Same invariant: no custom_env field on the wire,
// only coarse metadata.
func TestListAgents_ResponseHasNoCustomEnv(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentName := "noenv-list-agent"
	agentID := createHandlerTestAgent(t, agentName, nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"SECRET_KEY": "super-secret", "OTHER": "y"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/agents", nil)
	w := httptest.NewRecorder()
	testHandler.ListAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rawAgents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rawAgents); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	var found map[string]any
	for _, a := range rawAgents {
		if name, _ := a["name"].(string); name == agentName {
			found = a
			break
		}
	}
	if found == nil {
		t.Fatal("agent not found in list response")
	}
	if _, ok := found["custom_env"]; ok {
		t.Errorf("custom_env must not appear in list response")
	}
	if got, _ := found["custom_env_key_count"].(float64); got != 2 {
		t.Errorf("custom_env_key_count expected 2, got %v", found["custom_env_key_count"])
	}
	if got, _ := found["has_custom_env"].(bool); !got {
		t.Errorf("has_custom_env expected true")
	}
}

func TestAgentResponseIncludesRuntimeName(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	runtimeName := "Profile Runtime " + suffix[:8]
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'local', 'claude', 'online',
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, testWorkspaceID, "runtime-name-daemon-"+suffix, runtimeName).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	agentName := "runtime-name-agent-" + suffix
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, agentName, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	getW := httptest.NewRecorder()
	getReq := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID, nil), "id", agentID)
	testHandler.GetAgent(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetAgent: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var getResp AgentResponse
	if err := json.NewDecoder(getW.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GetAgent response: %v", err)
	}
	if getResp.RuntimeName != runtimeName {
		t.Fatalf("GetAgent runtime_name = %q, want %q", getResp.RuntimeName, runtimeName)
	}
	if getResp.RuntimeStatus != "online" {
		t.Fatalf("GetAgent runtime_status = %q, want online", getResp.RuntimeStatus)
	}
	if getResp.RuntimeLastSeenAt == nil || *getResp.RuntimeLastSeenAt == "" {
		t.Fatalf("GetAgent runtime_last_seen_at missing")
	}

	listW := httptest.NewRecorder()
	testHandler.ListAgents(listW, newRequest(http.MethodGet, "/api/agents", nil))
	if listW.Code != http.StatusOK {
		t.Fatalf("ListAgents: expected 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var listResp []AgentResponse
	if err := json.NewDecoder(listW.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode ListAgents response: %v", err)
	}
	for _, agent := range listResp {
		if agent.ID == agentID {
			if agent.RuntimeName != runtimeName {
				t.Fatalf("ListAgents runtime_name = %q, want %q", agent.RuntimeName, runtimeName)
			}
			if agent.RuntimeStatus != "online" {
				t.Fatalf("ListAgents runtime_status = %q, want online", agent.RuntimeStatus)
			}
			if agent.RuntimeLastSeenAt == nil || *agent.RuntimeLastSeenAt == "" {
				t.Fatalf("ListAgents runtime_last_seen_at missing")
			}
			return
		}
	}
	t.Fatalf("agent %s missing from ListAgents response", agentID)
}

// TestGetAgentEnv_OwnerSucceedsAndAudits exercises the happy path: an
// agent owner reveals env, and the response carries the plaintext map.
// The activity_log row is checked at the end so the audit trail is
// proven to land in the same transaction window.
func TestGetAgentEnv_OwnerSucceedsAndAudits(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "env-reveal-owner-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"KEY_ONE": "v1", "KEY_TWO": "v2"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/api/agents/"+agentID+"/env", nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentEnv: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentEnvResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID != agentID {
		t.Errorf("agent_id mismatch: got %q", resp.AgentID)
	}
	expected := map[string]string{"KEY_ONE": "v1", "KEY_TWO": "v2"}
	if !reflect.DeepEqual(resp.CustomEnv, expected) {
		t.Errorf("CustomEnv mismatch: got %v, want %v", resp.CustomEnv, expected)
	}

	// Audit row must exist; keys but not values must be recorded.
	var revealedKeysJSON string
	if err := testPool.QueryRow(ctx, `
		SELECT details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_revealed'
		  AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&revealedKeysJSON); err != nil {
		t.Fatalf("no agent_env_revealed activity row found: %v", err)
	}
	if !strings.Contains(revealedKeysJSON, `"KEY_ONE"`) || !strings.Contains(revealedKeysJSON, `"KEY_TWO"`) {
		t.Errorf("expected revealed_keys to contain KEY_ONE and KEY_TWO, got: %s", revealedKeysJSON)
	}
	if strings.Contains(revealedKeysJSON, `"v1"`) || strings.Contains(revealedKeysJSON, `"v2"`) {
		t.Errorf("activity details must NOT contain env values, got: %s", revealedKeysJSON)
	}
}

// TestAgentEnv_AgentActorRejected proves the security-critical actor
// guard: even when the underlying user is a workspace owner, a request
// arriving from inside a running agent task is denied 403. This is
// the lateral-movement fix — an agent running with its owner's token
// cannot reveal a sibling agent's secrets.
func TestAgentEnv_AgentActorRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "env-target-agent", nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env = '{"K":"v"}' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	// Spin up a separate agent + task that authorises the X-Agent-ID /
	// X-Task-ID header pair resolveActor checks. The owning member of
	// the host agent is the same testUserID (workspace owner), which is
	// the exact lateral-movement shape we want to block.
	hostAgentID := createHandlerTestAgent(t, "env-host-agent", nil)
	hostTaskID := createHandlerTestTaskForAgent(t, hostAgentID)

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body any
	}{
		{"reveal", testHandler.GetAgentEnv, nil},
		{"update", testHandler.UpdateAgentEnv, map[string]any{"custom_env": map[string]string{"K": "v2"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != nil {
				method = http.MethodPut
			}
			req := newRequest(method, "/api/agents/"+targetID+"/env", tc.body)
			req = withURLParam(req, "id", targetID)
			req.Header.Set("X-Agent-ID", hostAgentID)
			req.Header.Set("X-Task-ID", hostTaskID)
			w := httptest.NewRecorder()
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 from agent actor, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAgentEnv_TaskTokenActorSource locks in the post-MUL-2600 attack
// model: an agent process that strips its identifying headers
// (X-Agent-ID / X-Task-ID) but is still authenticated by an `mat_`
// task token MUST be recognized as actor=agent and rejected on the
// env endpoint. The auth middleware sets X-Actor-Source=task_token
// from the token row; resolveActor honors that header before the
// header-pair fallback. Without this guard the lateral-movement fix
// would only block "honest" CLIs that voluntarily set both headers.
func TestAgentEnv_TaskTokenActorSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "env-tt-target-agent", nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env = '{"K":"v"}' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/agents/"+targetID+"/env", nil)
	req = withURLParam(req, "id", targetID)
	// Simulate the auth middleware's post-mat_-resolution state: the
	// only header touching actor identity is X-Actor-Source. The agent
	// process stripped X-Agent-ID and X-Task-ID, hoping to fall back
	// to the member auth path — the server-set X-Actor-Source must
	// short-circuit that escape.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Del("X-Agent-ID")
	req.Header.Del("X-Task-ID")
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-Actor-Source=task_token, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateAgentEnv_PreservesSentinelValues verifies the **** guard.
// A naive write would clobber real secrets with the masked
// placeholder; we want any key whose value comes in as **** to keep
// its stored value.
func TestUpdateAgentEnv_PreservesSentinelValues(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "env-sentinel-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"KEEP_ME":"real-secret","ALSO":"another-secret"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to seed custom_env: %v", err)
	}

	// Client sends one key with a real new value, one with **** (should
	// be preserved), and one new key that isn't in the existing map but
	// arrives as **** (must be dropped, never written as literal).
	body := map[string]any{
		"custom_env": map[string]string{
			"KEEP_ME":   "****",
			"ALSO":      "rotated",
			"PHANTOM":   "****",
			"BRAND_NEW": "fresh",
		},
	}
	req := newRequest(http.MethodPut, "/api/agents/"+agentID+"/env", body)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgentEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgentEnv: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Refetch from DB so we don't rely on the response body alone.
	var stored string
	if err := testPool.QueryRow(ctx, `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("failed to read back custom_env: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		t.Fatalf("failed to decode stored custom_env: %v", err)
	}
	want := map[string]string{
		"KEEP_ME":   "real-secret", // **** must preserve the existing value
		"ALSO":      "rotated",     // explicit overwrite
		"BRAND_NEW": "fresh",       // new addition
		// PHANTOM is intentionally absent — **** for a non-existent key
		// is dropped, never persisted as literal `****`.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored custom_env mismatch:\n got:  %v\n want: %v", got, want)
	}

	// Audit row should reflect the diff. We decode the jsonb back into a
	// typed map and compare semantically — postgres serializes jsonb with
	// canonicalised whitespace (`"added_keys": ["BRAND_NEW"]`), so a raw
	// substring match on the dense form silently fails on real database
	// output.
	var details string
	if err := testPool.QueryRow(ctx, `
		SELECT details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_updated' AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&details); err != nil {
		t.Fatalf("expected agent_env_updated activity row: %v", err)
	}
	var auditFields struct {
		AddedKeys     []string `json:"added_keys"`
		ChangedKeys   []string `json:"changed_keys"`
		PreservedKeys []string `json:"preserved_keys"`
	}
	if err := json.Unmarshal([]byte(details), &auditFields); err != nil {
		t.Fatalf("failed to decode audit details: %v (raw=%s)", err, details)
	}
	if !reflect.DeepEqual(auditFields.AddedKeys, []string{"BRAND_NEW"}) {
		t.Errorf("added_keys: got %v, want [BRAND_NEW]; raw=%s", auditFields.AddedKeys, details)
	}
	if !reflect.DeepEqual(auditFields.ChangedKeys, []string{"ALSO"}) {
		t.Errorf("changed_keys: got %v, want [ALSO]; raw=%s", auditFields.ChangedKeys, details)
	}
	if !reflect.DeepEqual(auditFields.PreservedKeys, []string{"KEEP_ME"}) {
		t.Errorf("preserved_keys: got %v, want [KEEP_ME]; raw=%s", auditFields.PreservedKeys, details)
	}
	// Audit must never contain values.
	for _, leak := range []string{"real-secret", "another-secret", "rotated", "fresh"} {
		if strings.Contains(details, leak) {
			t.Errorf("audit details leaked value %q: %s", leak, details)
		}
	}
}

func TestUpdateAgent_RejectsCustomEnvInBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "update-no-env-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"PRE":"existing"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to seed custom_env: %v", err)
	}

	// Sending custom_env via the generic PUT /api/agents/{id} must fail
	// loudly with a 400 — see the comment on the rejection in agent.go.
	// Silently dropping the field used to make scripted clients believe
	// they had rotated a secret when nothing actually happened.
	body := map[string]any{
		"description": "still updating description",
		"custom_env":  map[string]string{"INJECTED": "should-not-stick"},
	}
	req := newRequest(http.MethodPut, "/api/agents/"+agentID, body)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "custom_env") || !strings.Contains(w.Body.String(), "/env") {
		t.Errorf("error body should mention custom_env and the env endpoint; got %s", w.Body.String())
	}

	// The stored env must be untouched by the rejected request.
	var stored string
	if err := testPool.QueryRow(ctx, `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("failed to read custom_env: %v", err)
	}
	if !strings.Contains(stored, `"PRE": "existing"`) && !strings.Contains(stored, `"PRE":"existing"`) {
		t.Errorf("UpdateAgent must NOT touch custom_env; got %q", stored)
	}
	if strings.Contains(stored, "INJECTED") {
		t.Errorf("UpdateAgent should have rejected custom_env in body; got %q", stored)
	}
}

// TestMergeAgentEnv_PureFunction exercises the diff/sentinel logic
// without the DB round-trip — keeps the contract front-and-centre in
// case someone refactors the handler later.
func TestMergeAgentEnv_PureFunction(t *testing.T) {
	cases := []struct {
		name     string
		existing map[string]string
		request  map[string]string
		want     map[string]string
		audit    envAudit
	}{
		{
			name:     "preserve sentinel",
			existing: map[string]string{"A": "real"},
			request:  map[string]string{"A": "****"},
			want:     map[string]string{"A": "real"},
			audit:    envAudit{preserved: []string{"A"}},
		},
		{
			name:     "drop sentinel for missing key",
			existing: map[string]string{},
			request:  map[string]string{"A": "****"},
			want:     map[string]string{},
			audit:    envAudit{},
		},
		{
			name:     "add new key",
			existing: map[string]string{},
			request:  map[string]string{"B": "v"},
			want:     map[string]string{"B": "v"},
			audit:    envAudit{added: []string{"B"}},
		},
		{
			name:     "change existing value",
			existing: map[string]string{"B": "old"},
			request:  map[string]string{"B": "new"},
			want:     map[string]string{"B": "new"},
			audit:    envAudit{changed: []string{"B"}},
		},
		{
			name:     "remove key absent from request",
			existing: map[string]string{"B": "v"},
			request:  map[string]string{},
			want:     map[string]string{},
			audit:    envAudit{removed: []string{"B"}},
		},
		{
			name:     "noop when value unchanged",
			existing: map[string]string{"B": "same"},
			request:  map[string]string{"B": "same"},
			want:     map[string]string{"B": "same"},
			audit:    envAudit{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, audit := mergeAgentEnv(tc.existing, tc.request)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("merged map: got %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(audit, tc.audit) {
				t.Errorf("audit: got %+v, want %+v", audit, tc.audit)
			}
		})
	}
}

// Compile-time guard: AgentResponse must NOT carry the legacy env
// fields. Reintroducing them is a security regression — this test
// fails to compile rather than fails at runtime so reviewers see the
// breakage in the diff. Kept as a runtime test because the package
// boundary makes a struct-tag introspection cheap and obvious.
func TestAgentResponseShape_HasNoLegacyEnvFields(t *testing.T) {
	typ := reflect.TypeOf(AgentResponse{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		switch tag {
		case "custom_env", "custom_env_redacted", "custom_env_redacted_reason":
			t.Errorf("AgentResponse must not carry %q field (MUL-2600)", tag)
		}
	}
}

// TestUpdateAgent_RedactsMcpConfigForAgentActor closes the second leg
// of MUL-2600 review #2: an agent process with a task token (or with
// the X-Actor-Source server marker) must not be able to scrape another
// agent's mcp_config via an unrelated mutation response. Even when the
// host PAT would otherwise satisfy canManageAgent, the response body
// must come back with mcp_config redacted.
func TestUpdateAgent_RedactsMcpConfigForAgentActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// The target agent has a populated mcp_config that historically would
	// be leaked back via the UpdateAgent / ArchiveAgent / RestoreAgent
	// HTTP response.
	target := createHandlerTestAgent(t, "mut-mcp-target", []byte(`{"server":"secret-config"}`))

	// A second agent acts as the "calling" agent process whose task
	// token authenticated the request. It is registered in the same
	// workspace so resolveActor recognises X-Agent-ID as valid.
	caller := createHandlerTestAgent(t, "mut-mcp-caller", nil)
	taskID := insertHandlerTestTask(t, caller)

	desc := "trivial mutation that should NOT leak target mcp_config"
	req := newRequest(http.MethodPut, "/api/agents/"+target, map[string]any{
		"description": desc,
	})
	req = withURLParam(req, "id", target)
	// Simulate a task-token-authenticated agent request. The auth
	// middleware would normally set these; we mimic both the modern
	// path (X-Actor-Source) and the legacy header pair so the test is
	// resilient to either resolveActor branch.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", caller)
	req.Header.Set("X-Task-ID", taskID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The response contract keeps `mcp_config` always-present so clients
	// can distinguish "no config" vs "redacted" via the companion flag.
	// `json.RawMessage` of a JSON null decodes to the literal bytes
	// `null`, not Go nil — so check for "no secret-bearing content"
	// rather than `!= nil`.
	if len(resp.McpConfig) > 0 && !bytes.Equal(bytes.TrimSpace(resp.McpConfig), []byte("null")) {
		t.Errorf("UpdateAgent response leaked mcp_config to agent actor: %s", string(resp.McpConfig))
	}
	if !resp.McpConfigRedacted {
		t.Errorf("UpdateAgent response should set mcp_config_redacted=true for agent actor")
	}
}

// TestUpdateAgent_KeepsMcpConfigForMemberActor is the matching positive
// test — a normal member request (owner/admin) still receives the full
// mcp_config in the mutation response, so the redaction does not
// accidentally regress the legitimate Web admin flow.
func TestUpdateAgent_KeepsMcpConfigForMemberActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	target := createHandlerTestAgent(t, "mut-mcp-member", []byte(`{"server":"member-visible"}`))

	req := newRequest(http.MethodPut, "/api/agents/"+target, map[string]any{
		"description": "owner-visible mutation",
	})
	req = withURLParam(req, "id", target)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.McpConfig == nil {
		t.Errorf("UpdateAgent response should keep mcp_config for member actor; got nil")
	}
	if resp.McpConfigRedacted {
		t.Errorf("UpdateAgent response should NOT mark mcp_config redacted for member actor")
	}
}

// TestUpdateAgent_PreservesSkillsInResponse is the regression for #3459:
// updating only description/instructions used to return "skills": []
// because the handler skipped the skill reload that GetAgent does. The
// DB row was always preserved; the response just lied about it, which
// scared users into manually re-running `agent skills set` and risked
// scripted clients writing the empty set back. We assert (a) the
// response carries the bound skills, (b) the DB row is unchanged, and
// (c) GetAgent reports the same shape so the two endpoints don't drift.
func TestUpdateAgent_PreservesSkillsInResponse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "update-preserves-skills-agent", nil)
	skillA := insertHandlerTestSkill(t, "update-preserve-a", "alpha body")
	skillB := insertHandlerTestSkill(t, "update-preserve-b", "beta body")
	for _, sid := range []string{skillA, skillB} {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
			agentID, sid,
		); err != nil {
			t.Fatalf("attach skill %s: %v", sid, err)
		}
	}

	req := newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"description": "metadata-only update",
	})
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, s := range resp.Skills {
		gotIDs[s.ID] = true
	}
	for _, want := range []string{skillA, skillB} {
		if !gotIDs[want] {
			t.Errorf("UpdateAgent response missing skill %s; got %+v", want, resp.Skills)
		}
	}

	// Defence in depth: the junction table must be untouched too. Without
	// this check a future regression that DOES wipe agent_skill rows but
	// reloads them into the response would silently pass.
	var rowCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_skill WHERE agent_id = $1`,
		agentID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count agent_skill: %v", err)
	}
	if rowCount != 2 {
		t.Errorf("agent_skill row count: expected 2, got %d", rowCount)
	}

	// GetAgent must agree with UpdateAgent on the skill list — otherwise
	// CLI users will see one shape from the mutation and a different one
	// on the very next read.
	getReq := newRequest(http.MethodGet, "/api/agents/"+agentID, nil)
	getReq = withURLParam(getReq, "id", agentID)
	getW := httptest.NewRecorder()
	testHandler.GetAgent(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetAgent: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var getResp AgentResponse
	if err := json.NewDecoder(getW.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GetAgent: %v", err)
	}
	if len(getResp.Skills) != len(resp.Skills) {
		t.Errorf("GetAgent skill count %d != UpdateAgent skill count %d",
			len(getResp.Skills), len(resp.Skills))
	}
}

// TestArchiveRestoreAgent_PreservesSkillsInResponse is the sister
// regression for #3459: ArchiveAgent / RestoreAgent share the same
// agentToResponse path as UpdateAgent and previously also returned
// "skills": [] regardless of what was in the junction table. The
// archive/restore broadcasts are the only place where mobile clients
// learn about state flips, so an empty skills array there would propagate
// to every connected client until the next refetch.
func TestArchiveRestoreAgent_PreservesSkillsInResponse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	previousNotifier := testHandler.ReminderNotifier
	notifier := &recordingReminderNotifier{}
	testHandler.ReminderNotifier = notifier
	t.Cleanup(func() { testHandler.ReminderNotifier = previousNotifier })

	agentID := createHandlerTestAgent(t, "archive-preserves-skills-agent", nil)
	skillID := insertHandlerTestSkill(t, "archive-preserve", "body")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
		agentID, skillID,
	); err != nil {
		t.Fatalf("attach skill: %v", err)
	}

	archiveReq := newRequest(http.MethodPost, "/api/agents/"+agentID+"/archive", nil)
	archiveReq = withURLParam(archiveReq, "id", agentID)
	archiveW := httptest.NewRecorder()
	testHandler.ArchiveAgent(archiveW, archiveReq)
	if archiveW.Code != http.StatusOK {
		t.Fatalf("ArchiveAgent: expected 200, got %d: %s", archiveW.Code, archiveW.Body.String())
	}
	var archived AgentResponse
	if err := json.NewDecoder(archiveW.Body).Decode(&archived); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if len(archived.Skills) != 1 || archived.Skills[0].ID != skillID {
		t.Errorf("ArchiveAgent: expected 1 skill %s, got %+v", skillID, archived.Skills)
	}

	restoreReq := newRequest(http.MethodPost, "/api/agents/"+agentID+"/restore", nil)
	restoreReq = withURLParam(restoreReq, "id", agentID)
	restoreW := httptest.NewRecorder()
	testHandler.RestoreAgent(restoreW, restoreReq)
	if restoreW.Code != http.StatusOK {
		t.Fatalf("RestoreAgent: expected 200, got %d: %s", restoreW.Code, restoreW.Body.String())
	}
	var restored AgentResponse
	if err := json.NewDecoder(restoreW.Body).Decode(&restored); err != nil {
		t.Fatalf("decode restore: %v", err)
	}
	if len(restored.Skills) != 1 || restored.Skills[0].ID != skillID {
		t.Errorf("RestoreAgent: expected 1 skill %s, got %+v", skillID, restored.Skills)
	}
	if len(notifier.starts) != 1 || notifier.starts[0].AgentID != agentID || notifier.starts[0].LifecycleSeq < 1 || notifier.starts[0].PlacementGeneration < 1 {
		t.Fatalf("RestoreAgent owner start projection = %+v", notifier.starts)
	}
}

// insertHandlerTestTask creates an in_progress task for the given
// agent so resolveActor's GetAgentTask lookup succeeds without
// dragging the full TaskService into the test.
func insertHandlerTestTask(t *testing.T, agentID string) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'draining', 0)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t)).Scan(&taskID); err != nil {
		t.Fatalf("insert test task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
	})
	return taskID
}

// Defence-in-depth: spot-check that the package compiles a small
// fmt.Sprintf so accidental imports stay tidy.
var _ = fmt.Sprintf

func TestBindWorkspaceRadarSupervisorReplacesArchivedPriorAndCancelsOnlyItsRadar(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	q := db.New(testPool)
	suffix := uuid.NewString()
	workspace, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "radar-rebind-" + suffix, Slug: "radar-rebind-" + suffix, IssuePrefix: "RBD",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var ownerID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "rebind-owner-"+suffix, "rebind-"+suffix+"@example.test").Scan(&ownerID); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspace.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, ownerID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspace.ID, ownerID); err != nil {
		t.Fatalf("create owner membership: %v", err)
	}
	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, 'rebind-runtime', 'cloud', 'daytona', 'online',
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, workspace.ID, "rebind-daemon-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	createAgent := func(name string) db.Agent {
		t.Helper()
		agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID: workspace.ID, Name: name + "-" + suffix, DisplayName: "Wendy",
			Description: "workspace supervisor", RuntimeMode: "cloud", RuntimeConfig: []byte("{}"),
			RuntimeID: runtimeID, Visibility: "private", MaxConcurrentTasks: 1, OwnerID: ownerID,
			Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return agent
	}
	oldSupervisor := createAgent("old-wendy")
	newSupervisor := createAgent("new-wendy")
	if cancelled, err := testHandler.bindWorkspaceRadarSupervisor(ctx, workspace.ID, oldSupervisor.ID); err != nil || len(cancelled) != 0 {
		t.Fatalf("bind old supervisor = cancelled:%d err:%v", len(cancelled), err)
	}
	run, radarTask, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
		WorkspaceID: workspace.ID, AgentID: oldSupervisor.ID, TriggerKind: "scheduled",
		TriggerRef: "rebind", CooldownKey: "workspace_supervisor_radar",
		ContextSummary: "old supervisor", ScheduledFor: time.Now(), Prompt: "inspect workspace",
	})
	if err != nil {
		t.Fatalf("enqueue old supervisor Radar: %v", err)
	}
	ordinaryTask, err := q.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{
		AgentID: oldSupervisor.ID, RuntimeID: runtimeID, Priority: 0,
		Context: []byte(`{"type":"quick_create","prompt":"ordinary task"}`),
	})
	if err != nil {
		t.Fatalf("create ordinary task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, oldSupervisor.ID, ownerID); err != nil {
		t.Fatalf("archive prior supervisor: %v", err)
	}
	watermark := time.Now().Add(-2 * time.Hour).UTC()
	nextDue := time.Now().Add(45 * time.Minute).UTC()
	if _, err := testPool.Exec(ctx, `
		UPDATE workspace_radar_state
		SET last_success_at = $2, last_full_review_at = $2,
		    last_applied_scheduled_for = $2, next_due_at = $3,
		    change_version = 42, change_cursor_version = 37,
		    static_scan_cursors = '{"issues":7,"channels":3}'::jsonb,
		    static_cycle_seen = '{"issues":true}'::jsonb,
		    consecutive_failures = 2
		WHERE workspace_id = $1
	`, workspace.ID, watermark, nextDue); err != nil {
		t.Fatalf("seed prior workspace scan state: %v", err)
	}

	repairStarted := time.Now().UTC()
	cancelled, err := testHandler.bindWorkspaceRadarSupervisor(ctx, workspace.ID, newSupervisor.ID)
	if err != nil {
		t.Fatalf("rebind supervisor: %v", err)
	}
	if len(cancelled) != 1 || uuidToString(cancelled[0].ID) != uuidToString(radarTask.ID) {
		t.Fatalf("rebind cancelled tasks = %+v, want only Radar task %s", cancelled, uuidToString(radarTask.ID))
	}
	var boundAgentID pgtype.UUID
	var storedSuccess, storedFullReview, storedApplied, storedNextDue time.Time
	var storedFailures, storedChangeVersion, storedChangeCursorVersion int
	var storedStaticScanCursors, storedStaticCycleSeen bool
	if err := testPool.QueryRow(ctx, `
		SELECT supervisor_agent_id, last_success_at, last_full_review_at,
		       last_applied_scheduled_for, next_due_at, consecutive_failures,
		       change_version, change_cursor_version,
		       static_scan_cursors = '{"issues":7,"channels":3}'::jsonb,
		       static_cycle_seen = '{"issues":true}'::jsonb
		FROM workspace_radar_state WHERE workspace_id = $1
	`, workspace.ID).Scan(
		&boundAgentID, &storedSuccess, &storedFullReview, &storedApplied,
		&storedNextDue, &storedFailures, &storedChangeVersion,
		&storedChangeCursorVersion, &storedStaticScanCursors, &storedStaticCycleSeen,
	); err != nil || uuidToString(boundAgentID) != uuidToString(newSupervisor.ID) {
		t.Fatalf("bound supervisor = %s, %v; want %s", uuidToString(boundAgentID), err, uuidToString(newSupervisor.ID))
	}
	for name, got := range map[string]time.Time{
		"last_success_at":            storedSuccess,
		"last_full_review_at":        storedFullReview,
		"last_applied_scheduled_for": storedApplied,
	} {
		if got.Sub(watermark) > time.Second || watermark.Sub(got) > time.Second {
			t.Fatalf("rebound %s = %s, want preserved %s", name, got, watermark)
		}
	}
	if storedChangeVersion != 42 || storedChangeCursorVersion != 37 ||
		!storedStaticScanCursors || !storedStaticCycleSeen {
		t.Fatalf("rebound scan state = version:%d cursor:%d static:%t cycle:%t; want preserved",
			storedChangeVersion, storedChangeCursorVersion, storedStaticScanCursors, storedStaticCycleSeen)
	}
	if storedNextDue.Before(repairStarted.Add(-time.Second)) || storedNextDue.After(time.Now().Add(time.Second)) || storedFailures != 0 {
		t.Fatalf("rebound workspace recovery = next:%s failures:%d; want immediate/0", storedNextDue, storedFailures)
	}
	storedRun, err := q.GetAgentRadarRun(ctx, run.ID)
	if err != nil || storedRun.Status != "cancelled" {
		t.Fatalf("old Radar run = %+v, %v; want cancelled", storedRun, err)
	}
	storedRadarTask, err := q.GetAgentTask(ctx, radarTask.ID)
	if err != nil || storedRadarTask.Status != "suppressed" {
		t.Fatalf("old Radar task = %+v, %v; want suppressed", storedRadarTask, err)
	}
	ordinaryTask, err = q.GetAgentTask(ctx, ordinaryTask.ID)
	if err != nil || ordinaryTask.Status != "pending" {
		t.Fatalf("ordinary task = %+v, %v; want pending", ordinaryTask, err)
	}
}

func TestEnsureWindyPreservesAuthorizedSupervisorAcrossWorkspaceOwners(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	q := db.New(testPool)
	suffix := uuid.NewString()
	workspace, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "radar-stable-" + suffix, Slug: "radar-stable-" + suffix, IssuePrefix: "RST",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ownerIDs := make([]pgtype.UUID, 2)
	for i := range ownerIDs {
		if err := testPool.QueryRow(ctx, `
			INSERT INTO "user" (name, email)
			VALUES ($1, $2)
			RETURNING id
		`, fmt.Sprintf("stable-owner-%d-%s", i, suffix), fmt.Sprintf("stable-owner-%d-%s@example.test", i, suffix)).Scan(&ownerIDs[i]); err != nil {
			t.Fatalf("create owner %d: %v", i, err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
		`, workspace.ID, ownerIDs[i]); err != nil {
			t.Fatalf("create owner %d membership: %v", i, err)
		}
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspace.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = ANY($1::uuid[])`, ownerIDs)
	})
	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, 'stable-runtime', 'cloud', 'daytona', 'online',
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, workspace.ID, "stable-daemon-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	createWendy := func(index int) db.Agent {
		t.Helper()
		agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID: workspace.ID, Name: fmt.Sprintf("stable-wendy-%d-%s", index, suffix), DisplayName: "Wendy",
			Description: "workspace supervisor", RuntimeMode: "cloud", RuntimeConfig: []byte("{}"),
			RuntimeID: runtimeID, Visibility: "private", MaxConcurrentTasks: 1, OwnerID: ownerIDs[index],
			Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		})
		if err != nil {
			t.Fatalf("create Wendy %d: %v", index, err)
		}
		return agent
	}
	first := createWendy(0)
	second := createWendy(1)
	ensureForOwner := func(ownerID pgtype.UUID) {
		t.Helper()
		req := newRequestAs(uuidToString(ownerID), http.MethodPost, "/api/agents/windy?runtime_id="+uuidToString(runtimeID), nil)
		req.Header.Set("X-Workspace-ID", uuidToString(workspace.ID))
		response := httptest.NewRecorder()
		testHandler.EnsureWindy(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("EnsureWindy owner %s status = %d: %s", uuidToString(ownerID), response.Code, response.Body.String())
		}
	}
	ensureForOwner(ownerIDs[0])
	run, task, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
		WorkspaceID: workspace.ID, AgentID: first.ID, TriggerKind: "scheduled",
		TriggerRef: "stable-binding", CooldownKey: "workspace_supervisor_radar",
		ContextSummary: "preserve current supervisor", ScheduledFor: time.Now(), Prompt: "inspect workspace",
	})
	if err != nil {
		t.Fatalf("enqueue first owner Radar: %v", err)
	}
	watermark := time.Now().Add(-time.Hour).UTC()
	nextDue := time.Now().Add(time.Hour).UTC()
	if _, err := testPool.Exec(ctx, `
		UPDATE workspace_radar_state
		SET last_success_at = $2, last_full_review_at = $2,
		    last_applied_scheduled_for = $2, next_due_at = $3,
		    consecutive_failures = 3
		WHERE workspace_id = $1
	`, workspace.ID, watermark, nextDue); err != nil {
		t.Fatalf("seed current supervisor state: %v", err)
	}

	ensureForOwner(ownerIDs[1])
	refreshedSecond, err := q.GetAgent(ctx, second.ID)
	if err != nil {
		t.Fatalf("load second owner Wendy: %v", err)
	}
	if refreshedSecond.Description != windyDescription || !strings.Contains(refreshedSecond.Instructions, windyInstructionsCapabilityMarker) {
		t.Fatalf("second owner Wendy was not refreshed: description=%q instructions=%q", refreshedSecond.Description, refreshedSecond.Instructions)
	}
	var boundAgentID pgtype.UUID
	var storedNextDue, storedWatermark time.Time
	var failures int
	if err := testPool.QueryRow(ctx, `
		SELECT supervisor_agent_id, next_due_at, last_applied_scheduled_for, consecutive_failures
		FROM workspace_radar_state WHERE workspace_id = $1
	`, workspace.ID).Scan(&boundAgentID, &storedNextDue, &storedWatermark, &failures); err != nil {
		t.Fatalf("load preserved supervisor state: %v", err)
	}
	if uuidToString(boundAgentID) != uuidToString(first.ID) {
		t.Fatalf("bound supervisor = %s, want first owner Wendy %s", uuidToString(boundAgentID), uuidToString(first.ID))
	}
	if storedNextDue.Sub(nextDue) > time.Second || nextDue.Sub(storedNextDue) > time.Second ||
		storedWatermark.Sub(watermark) > time.Second || watermark.Sub(storedWatermark) > time.Second || failures != 3 {
		t.Fatalf("preserved state = next:%s watermark:%s failures:%d", storedNextDue, storedWatermark, failures)
	}
	storedRun, err := q.GetAgentRadarRun(ctx, run.ID)
	if err != nil || storedRun.Status != "queued" {
		t.Fatalf("first owner Radar run = %+v, %v; want queued", storedRun, err)
	}
	storedTask, err := q.GetAgentTask(ctx, task.ID)
	if err != nil || storedTask.Status != "pending" {
		t.Fatalf("first owner Radar task = %+v, %v; want pending", storedTask, err)
	}
}

func TestEnsureWindyRestoresArchivedWendyInsteadOfCreatingDuplicate(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, instructions, avatar_url,
			runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
			archived_at, archived_by
		)
		VALUES ($1, $2, 'Wendy', 'archived Wendy', 'instructions', '/legacy.png',
			'cloud', '{}'::jsonb, $3, 'private', 1, $4, now(), $4)
		RETURNING id
	`, testWorkspaceID, "archived_wendy_"+strings.ReplaceAll(t.Name(), "/", "_"), handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("seed archived Wendy agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	req := newRequest(http.MethodPost, "/api/agents/windy", nil)
	updated, created, err := testHandler.ensureWindyAgent(req, parseUUID(testWorkspaceID), parseUUID(testUserID), db.AgentRuntime{})
	if err != nil {
		t.Fatalf("ensureWindyAgent: %v", err)
	}
	if created {
		t.Fatal("ensureWindyAgent created a new agent instead of restoring archived Wendy")
	}
	if uuidToString(updated.ID) != agentID {
		t.Fatalf("ensureWindyAgent reused agent %q, want archived %q", uuidToString(updated.ID), agentID)
	}
	if updated.ArchivedAt.Valid {
		t.Fatal("archived Wendy was not restored")
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent
		WHERE workspace_id = $1 AND owner_id = $2 AND display_name = 'Wendy'
	`, testWorkspaceID, testUserID).Scan(&count); err != nil {
		t.Fatalf("count Wendy agents: %v", err)
	}
	if count != 1 {
		t.Fatalf("Wendy agent count = %d, want 1", count)
	}
}

func TestEnsureWindyPrefersActiveConfiguredWendy(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx := context.Background()
	activeRuntime := handlerTestRuntimeID(t)
	var activeID, archivedID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, instructions, avatar_url,
			runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
			created_at, updated_at
		)
		VALUES ($1, $2, 'Wendy', 'active Wendy', '', '/wendy.png',
			'cloud', '{}'::jsonb, $3, 'private', 1, $4, now() - interval '2 hours', now() - interval '2 hours')
		RETURNING id
	`, testWorkspaceID, "active_wendy_"+strings.ReplaceAll(t.Name(), "/", "_"), activeRuntime, testUserID).Scan(&activeID); err != nil {
		t.Fatalf("seed active Wendy: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, instructions, avatar_url,
			runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
			created_at, updated_at, archived_at, archived_by
		)
		VALUES ($1, $2, 'Wendy', 'archived newer Wendy', '', '/wendy.png',
			'cloud', '{}'::jsonb, $3, 'private', 1, $4,
			now() - interval '1 hour', now() - interval '1 hour', now(), $4)
		RETURNING id
	`, testWorkspaceID, "archived_newer_wendy_"+strings.ReplaceAll(t.Name(), "/", "_"), activeRuntime, testUserID).Scan(&archivedID); err != nil {
		t.Fatalf("seed archived Wendy: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id IN ($1, $2)`, activeID, archivedID) })

	req := newRequest(http.MethodPost, "/api/agents/windy", nil)
	updated, created, err := testHandler.ensureWindyAgent(req, parseUUID(testWorkspaceID), parseUUID(testUserID), db.AgentRuntime{})
	if err != nil {
		t.Fatalf("ensureWindyAgent: %v", err)
	}
	if created {
		t.Fatal("ensureWindyAgent created a new agent despite existing Wendy")
	}
	if uuidToString(updated.ID) != activeID {
		t.Fatalf("ensureWindyAgent chose %q, want active configured %q", uuidToString(updated.ID), activeID)
	}
}

func TestPreferWindyAgentRanksActiveConfiguredCanonicalLatest(t *testing.T) {
	base := db.Agent{ID: parseUUID("00000000-0000-0000-0000-000000000001"), OwnerID: parseUUID(testUserID), DisplayName: "Joe", CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-3 * time.Hour), Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: time.Now().Add(-3 * time.Hour), Valid: true}}
	configured := base
	configured.ID = parseUUID("00000000-0000-0000-0000-000000000002")
	configured.RuntimeID = parseUUID("99999999-9999-9999-9999-999999999999")
	if !preferWindyAgent(configured, base) {
		t.Fatal("configured Wendy should beat unconfigured legacy Wendy")
	}
	archived := configured
	archived.ID = parseUUID("00000000-0000-0000-0000-000000000003")
	archived.UpdatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	archived.ArchivedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if preferWindyAgent(archived, configured) {
		t.Fatal("archived Wendy should not beat active Wendy")
	}
	canonical := configured
	canonical.ID = parseUUID("00000000-0000-0000-0000-000000000004")
	canonical.DisplayName = windyAgentName
	if !preferWindyAgent(canonical, configured) {
		t.Fatal("canonical Wendy should beat legacy configured Wendy")
	}
}

func TestEnsureWindyRenamesLegacyWindyAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, instructions, avatar_url,
			runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, 'Windy', 'legacy windy', 'legacy instructions', '/legacy.png',
			'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, "legacy_windy_"+strings.ReplaceAll(t.Name(), "/", "_"), handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("seed legacy Windy agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	req := newRequest(http.MethodPost, "/api/agents/windy", nil)
	updated, created, err := testHandler.ensureWindyAgent(req, parseUUID(testWorkspaceID), parseUUID(testUserID), db.AgentRuntime{})
	if err != nil {
		t.Fatalf("ensureWindyAgent: %v", err)
	}
	if created {
		t.Fatal("ensureWindyAgent created a new agent instead of reusing legacy Windy")
	}
	if uuidToString(updated.ID) != agentID {
		t.Fatalf("ensureWindyAgent reused agent %q, want legacy %q", uuidToString(updated.ID), agentID)
	}
	if updated.DisplayName != windyAgentName {
		t.Fatalf("display_name = %q, want %q", updated.DisplayName, windyAgentName)
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent
		WHERE workspace_id = $1 AND owner_id = $2 AND visibility = 'private'
		  AND display_name IN ('Windy', 'Wendy')
	`, testWorkspaceID, testUserID).Scan(&count); err != nil {
		t.Fatalf("count Windy/Wendy agents: %v", err)
	}
	if count != 1 {
		t.Fatalf("Windy/Wendy private agent count = %d, want 1", count)
	}
}
