package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentInboxDrainSerializesSameAgentAndKeepsDifferentAgentsConcurrent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	// Dedicated runtime: shared handlerTestRuntimeID accumulates pending wakes
	// from other tests, which can lease concurrently with this fixture and
	// falsely fail the same-agent serialization assertion.
	runtimeID := createClaimReclaimRuntime(t, ctx, "inbox-serial-runtime-"+uuid.NewString())
	firstAgentID := createHandlerTestAgentOnRuntime(t, "Inbox Serial Agent A "+uuid.NewString()[:8], runtimeID)
	firstChannelID := seedChannelForTest(t, "inbox-serial-a-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, firstChannelID, testWorkspaceID, firstAgentID); err != nil {
		t.Fatalf("seed first agent member: %v", err)
	}
	for i := 0; i < 2; i++ {
		createProductInboxEventForRuntime(t, runtimeID, firstAgentID, firstChannelID)
	}

	drain := func(daemonID string) (DrainAgentInboxResponse, error) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, daemonID)
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			return DrainAgentInboxResponse{}, fmt.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return DrainAgentInboxResponse{}, err
		}
		return resp, nil
	}

	start := make(chan struct{})
	results := make(chan DrainAgentInboxResponse, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			resp, err := drain(fmt.Sprintf("inbox-serial-racer-%d", i))
			if err != nil {
				errs <- err
				return
			}
			results <- resp
		}(i)
	}
	close(start)

	var firstDelivery AgentInboxEventResponse
	leased := 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent drain failed: %v", err)
		case resp := <-results:
			if len(resp.Events) > 1 {
				t.Fatalf("drain returned %d events, want at most 1", len(resp.Events))
			}
			if len(resp.Events) == 1 {
				leased++
				firstDelivery = resp.Events[0]
			}
		}
	}
	if leased != 1 || firstDelivery.AgentID != firstAgentID {
		t.Fatalf("same-agent concurrent drains leased %d events, want exactly 1 for %s", leased, firstAgentID)
	}

	var draining, pending, activeDeliveries int
	if err := testPool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE e.status = 'draining'),
			count(*) FILTER (WHERE e.status = 'pending'),
			count(*) FILTER (
				WHERE d.status IN ('leased', 'processing') AND d.lease_expires_at > now()
			)
		FROM agent_inbox_event e
		LEFT JOIN agent_event_delivery d ON d.inbox_event_id = e.id
		WHERE e.agent_id = $1`, firstAgentID).Scan(&draining, &pending, &activeDeliveries); err != nil {
		t.Fatalf("load first-agent lease states: %v", err)
	}
	if draining != 1 || pending != 1 || activeDeliveries != 1 {
		t.Fatalf("first-agent states draining=%d pending=%d active=%d, want 1/1/1", draining, pending, activeDeliveries)
	}

	secondAgentID := createHandlerTestAgentOnRuntime(t, "Inbox Serial Agent B "+uuid.NewString()[:8], runtimeID)
	secondChannelID := seedChannelForTest(t, "inbox-serial-b-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, secondChannelID, testWorkspaceID, secondAgentID); err != nil {
		t.Fatalf("seed second agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, secondAgentID, secondChannelID)

	secondResp, err := drain("inbox-serial-other-agent")
	if err != nil {
		t.Fatalf("drain different agent: %v", err)
	}
	if len(secondResp.Events) != 1 || secondResp.Events[0].AgentID != secondAgentID {
		t.Fatalf("different-agent drain = %+v, want one event for %s while first agent is active", secondResp.Events, secondAgentID)
	}

	ack := func(event AgentInboxEventResponse) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/ack", AckAgentInboxEventRequest{
			DeliveryID:  event.DeliveryID,
			LeaseToken:  event.LeaseToken,
			SeenUpToSeq: event.SeqTo,
		}, testWorkspaceID, "inbox-serial-ack")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.AckAgentInboxEvent(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ack event %s: status=%d body=%s", event.ID, rec.Code, rec.Body.String())
		}
	}
	ack(secondResp.Events[0])
	ack(firstDelivery)

	nextResp, err := drain("inbox-serial-next")
	if err != nil {
		t.Fatalf("drain queued first-agent event: %v", err)
	}
	if len(nextResp.Events) != 1 || nextResp.Events[0].AgentID != firstAgentID || nextResp.Events[0].ID == firstDelivery.ID {
		t.Fatalf("queued first-agent drain = %+v, want the second event after release", nextResp.Events)
	}
}

func TestAgentInboxDrainPrioritizesPendingWakeAcrossRuntimes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	olderRuntimeID := createClaimReclaimRuntime(t, ctx, "cross-runtime FIFO older "+uuid.NewString())
	currentRuntimeID := createClaimReclaimRuntime(t, ctx, "cross-runtime FIFO current "+uuid.NewString())
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, currentRuntimeID, "cross-runtime FIFO "+uuid.NewString())

	older, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(olderRuntimeID),
		IssueID:   parseUUID(issueID),
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("create older cross-runtime event: %v", err)
	}
	newer, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(currentRuntimeID),
		IssueID:   parseUUID(issueID),
		Priority:  100,
	})
	if err != nil {
		t.Fatalf("create newer cross-runtime event: %v", err)
	}
	base := time.Now().Add(-time.Minute)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET created_at = CASE id
		  WHEN $1 THEN $3::timestamptz
		  ELSE $3::timestamptz + interval '1 second'
		END
		WHERE id IN ($1, $2)`, older.ID, newer.ID, base); err != nil {
		t.Fatalf("order cross-runtime events: %v", err)
	}

	currentRuntime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(currentRuntimeID))
	if err != nil {
		t.Fatalf("load current runtime: %v", err)
	}
	highPriorityDelivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, currentRuntime)
	if err != nil {
		t.Fatalf("lease newer high-priority event: %v", err)
	}
	if highPriorityDelivery.InboxEventID != newer.ID {
		t.Fatalf(
			"leased event %s, want newer high-priority event %s",
			uuidToString(highPriorityDelivery.InboxEventID),
			uuidToString(newer.ID),
		)
	}

	olderRuntime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(olderRuntimeID))
	if err != nil {
		t.Fatalf("load older runtime: %v", err)
	}
	if delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, olderRuntime); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("older low-priority event bypassed active high-priority event: delivery=%+v err=%v", delivery, err)
	}

	settleClaimedInboxEventForTest(t, uuidToString(newer.ID))

	// LRM-927: the remaining event is still pinned to olderRuntime, but the
	// agent lives on currentRuntime. Drain on the stale runtime heals that pin
	// (moves event+session) and returns no lease — it must not 403 on ensure.
	if delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, olderRuntime); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale runtime should heal then return no lease: delivery=%+v err=%v", delivery, err)
	}

	delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, currentRuntime)
	if err != nil {
		t.Fatalf("lease healed low-priority event on current runtime: %v", err)
	}
	if delivery.InboxEventID != older.ID {
		t.Fatalf(
			"leased event %s, want remaining low-priority event %s (healed onto current runtime)",
			uuidToString(delivery.InboxEventID),
			uuidToString(older.ID),
		)
	}
}

func TestAgentWakeAdmissionIsFIFOAndSerialAcrossLegacyQueueAndInbox(t *testing.T) {
	t.Skip("obsolete transitional queue/inbox admission contract; migration 223 has one inbox source")

	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	type wakeFixture struct {
		runtimeID string
		agentID   string
		taskID    string
		eventID   string
	}
	newFixture := func(t *testing.T, inboxFirst bool) wakeFixture {
		t.Helper()
		runtimeID := createClaimReclaimRuntime(t, ctx, "Unified wake runtime "+uuid.NewString()[:8])
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Unified wake agent "+uuid.NewString()[:8])
		var sessionID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_session (
			  workspace_id, agent_id, runtime_id, scope, status
			)
			VALUES ($1, $2, $3, 'direct_chat', 'active')
			RETURNING id
		`, testWorkspaceID, agentID, runtimeID).Scan(&sessionID); err != nil {
			t.Fatalf("create agent session: %v", err)
		}

		base := time.Now().Add(-5 * time.Minute)
		taskCreatedAt, eventCreatedAt := base, base.Add(time.Second)
		taskPriority, eventPriority := 0, 100
		if inboxFirst {
			taskCreatedAt, eventCreatedAt = base.Add(time.Second), base
			taskPriority, eventPriority = 100, 0
		}
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
			  agent_id, runtime_id, issue_id, status, priority, created_at
			)
			VALUES ($1, $2, $3, 'pending', $4, $5)
			RETURNING id
		`, agentID, runtimeID, issueID, taskPriority, taskCreatedAt).Scan(&taskID); err != nil {
			t.Fatalf("create legacy wake: %v", err)
		}
		var eventID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
			  workspace_id, agent_session_id, runtime_id, agent_id,
			  reason, requires_wake, status, priority, seq_from, seq_to, created_at
			)
			VALUES ($1, $2, $3, $4, 'dm', true, 'pending', $5, 1, 1, $6)
			RETURNING id
		`, testWorkspaceID, sessionID, runtimeID, agentID, eventPriority, eventCreatedAt).Scan(&eventID); err != nil {
			t.Fatalf("create inbox wake: %v", err)
		}
		return wakeFixture{runtimeID: runtimeID, agentID: agentID, taskID: taskID, eventID: eventID}
	}
	loadRuntime := func(t *testing.T, runtimeID string) db.AgentRuntime {
		t.Helper()
		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}
		return runtime
	}
	expectNoInboxLease := func(t *testing.T, runtime db.AgentRuntime) {
		t.Helper()
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("inbox lease error = %v, want no rows", err)
		}
	}

	t.Run("legacy wake older", func(t *testing.T) {
		fixture := newFixture(t, false)
		runtime := loadRuntime(t, fixture.runtimeID)

		expectNoInboxLease(t, runtime)
		task, err := claimInboxEventForRuntimeForTest(ctx, fixture.runtimeID)
		if err != nil || task == nil || uuidToString(task.ID) != fixture.taskID {
			t.Fatalf("legacy claim task=%+v err=%v, want %s", task, err, fixture.taskID)
		}
		expectNoInboxLease(t, runtime)

		settleClaimedInboxEventForTest(t, fixture.taskID)
		delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		if err != nil || uuidToString(delivery.InboxEventID) != fixture.eventID {
			t.Fatalf("inbox lease after legacy completion=%+v err=%v, want event %s", delivery, err, fixture.eventID)
		}
	})

	t.Run("inbox wake older", func(t *testing.T) {
		fixture := newFixture(t, true)
		runtime := loadRuntime(t, fixture.runtimeID)

		task, err := claimInboxEventForRuntimeForTest(ctx, fixture.runtimeID)
		if err != nil || task != nil {
			t.Fatalf("legacy claim before older inbox task=%+v err=%v, want nil", task, err)
		}
		delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		if err != nil || uuidToString(delivery.InboxEventID) != fixture.eventID {
			t.Fatalf("older inbox lease=%+v err=%v, want event %s", delivery, err, fixture.eventID)
		}
		task, err = claimInboxEventForRuntimeForTest(ctx, fixture.runtimeID)
		if err != nil || task != nil {
			t.Fatalf("legacy claim during active inbox task=%+v err=%v, want nil", task, err)
		}

		if _, err := testPool.Exec(ctx, `
			UPDATE agent_event_delivery
			SET status = 'acked', acked_at = now(), updated_at = now()
			WHERE id = $1
		`, delivery.ID); err != nil {
			t.Fatalf("ack inbox delivery: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			UPDATE agent_inbox_event
			SET status = 'acked', acked_at = now(), updated_at = now()
			WHERE id = $1
		`, fixture.eventID); err != nil {
			t.Fatalf("ack inbox wake: %v", err)
		}
		task, err = claimInboxEventForRuntimeForTest(ctx, fixture.runtimeID)
		if err != nil || task == nil || uuidToString(task.ID) != fixture.taskID {
			t.Fatalf("legacy claim after inbox ack task=%+v err=%v, want %s", task, err, fixture.taskID)
		}
	})
}

// A renewed delivery is still the same provider run, but a reclaimed delivery
// starts a new run for the same source event. Usage replay must overwrite only
// the same execution/model row and retain both true runs.
func TestAgentInboxUsageExecutionIdentitySurvivesRenewAndReclaim(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "Inbox Ledger Agent "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "inbox-ledger-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

	drain := func() AgentInboxEventResponse {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "inbox-ledger-daemon")
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("drain inbox: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Events) != 1 {
			t.Fatalf("decode drain=%v events=%+v", err, resp.Events)
		}
		return resp.Events[0]
	}
	start := func(event AgentInboxEventResponse, executionID string) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/execution", AgentInboxExecutionRequest{
			DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, ExecutionID: executionID,
		}, testWorkspaceID, "inbox-ledger-daemon")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.StartAgentInboxExecution(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("start execution: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	report := func(event AgentInboxEventResponse, executionID string, tokens int64) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/usage", ReportAgentInboxUsageRequest{
			DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, ExecutionID: executionID,
			Usage: []AgentUsagePayload{{Provider: "openrouter", Model: "deepseek-v4-flash", InputTokens: tokens}},
		}, testWorkspaceID, "inbox-ledger-daemon")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.ReportAgentInboxUsage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("report usage: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	first := drain()
	// Renewal retains delivery ownership but does not mint a new execution.
	renewReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+first.ID+"/renew", RenewAgentInboxEventRequest{DeliveryID: first.DeliveryID, LeaseToken: first.LeaseToken}, testWorkspaceID, "inbox-ledger-daemon")
	renewReq = withURLParam(renewReq, "eventId", first.ID)
	renewRec := httptest.NewRecorder()
	testHandler.RenewAgentInboxEvent(renewRec, renewReq)
	if renewRec.Code != http.StatusOK {
		t.Fatalf("renew: status=%d body=%s", renewRec.Code, renewRec.Body.String())
	}
	firstExecution := uuid.NewString()
	start(first, firstExecution)
	start(first, firstExecution) // start retry is idempotent for this run UUID.
	report(first, firstExecution, 17)
	report(first, firstExecution, 17) // usage report replay is idempotent.

	if _, err := testPool.Exec(ctx, `UPDATE agent_event_delivery SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, first.DeliveryID); err != nil {
		t.Fatalf("expire first delivery: %v", err)
	}
	second := drain()
	if second.ID != first.ID || second.DeliveryID == first.DeliveryID {
		t.Fatalf("reclaim got event=%s delivery=%s, want same event/new delivery from %s", second.ID, second.DeliveryID, first.DeliveryID)
	}
	// The first provider run may finish after its lease has been reclaimed.
	// Its already-persisted execution remains reportable and idempotent.
	report(first, firstExecution, 17)
	secondExecution := uuid.NewString()
	start(second, secondExecution)
	report(second, secondExecution, 23)

	var executionCount, usageCount int
	var tokens int64
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(input_tokens), 0)::bigint
		FROM agent_usage
		WHERE execution_id IN ($1, $2)`, firstExecution, secondExecution).Scan(&usageCount, &tokens); err != nil {
		t.Fatalf("load usage rows: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_execution
		WHERE source_kind = 'inbox' AND source_event_id = $1`, first.ID).Scan(&executionCount); err != nil {
		t.Fatalf("load execution rows: %v", err)
	}
	if executionCount != 2 || usageCount != 2 || tokens != 40 {
		t.Fatalf("execution/usage state executions=%d usages=%d tokens=%d, want 2/2/40", executionCount, usageCount, tokens)
	}
}

func TestIssueInboxReclaimPreservesLogicalAttemptAndFencesTerminalWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "issue inbox fence runtime "+uuid.NewString())
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "issue inbox fence "+uuid.NewString())
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id`, testWorkspaceID, "issue inbox attribution "+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create attribution project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $2 WHERE id = $1`, issueID, projectID); err != nil {
		t.Fatalf("bind issue project: %v", err)
	}
	task, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		IssueID:   parseUUID(issueID),
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("create issue inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1 OR parent_task_id = $1`, task.ID)
	})

	drain := func() AgentInboxEventResponse {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "issue-inbox-fence-daemon")
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("drain issue inbox: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || len(response.Events) != 1 {
			t.Fatalf("decode issue drain=%v events=%+v", err, response.Events)
		}
		return response.Events[0]
	}
	start := func(event AgentInboxEventResponse, executionID string, wantStatus int) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/execution", AgentInboxExecutionRequest{
			DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, ExecutionID: executionID,
		}, testWorkspaceID, "issue-inbox-fence-daemon")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.StartAgentInboxExecution(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("start issue execution: status=%d body=%s, want %d", rec.Code, rec.Body.String(), wantStatus)
		}
	}

	first := drain()
	firstExecutionID := uuid.NewString()
	start(first, firstExecutionID, http.StatusOK)
	var source, executionIssueID, executionProjectID string
	if err := testPool.QueryRow(ctx, `
		SELECT source, issue_id::text, project_id::text
		FROM agent_execution
		WHERE id = $1`, firstExecutionID).Scan(&source, &executionIssueID, &executionProjectID); err != nil {
		t.Fatalf("load issue execution attribution: %v", err)
	}
	if source != "issue" || executionIssueID != issueID || executionProjectID != projectID {
		t.Fatalf("issue execution attribution = source:%q issue:%s project:%s", source, executionIssueID, executionProjectID)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, first.DeliveryID); err != nil {
		t.Fatalf("expire first issue delivery: %v", err)
	}
	second := drain()
	if second.ID != first.ID || second.DeliveryID == first.DeliveryID {
		t.Fatalf("reclaim got event=%s delivery=%s, want same event/new delivery", second.ID, second.DeliveryID)
	}
	var attempt int32
	if err := testPool.QueryRow(ctx, `SELECT attempt FROM agent_inbox_event WHERE id = $1`, task.ID).Scan(&attempt); err != nil {
		t.Fatalf("load logical attempt after reclaim: %v", err)
	}
	if attempt != 1 {
		t.Fatalf("logical attempt after transport reclaim = %d, want 1", attempt)
	}

	staleExecutionID := uuid.NewString()
	start(first, staleExecutionID, http.StatusConflict)
	var staleExecutionCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_execution WHERE id = $1`, staleExecutionID).Scan(&staleExecutionCount); err != nil {
		t.Fatalf("count stale execution: %v", err)
	}
	if staleExecutionCount != 0 {
		t.Fatalf("stale delivery created %d provider executions, want 0", staleExecutionCount)
	}

	staleFailReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+first.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    first.DeliveryID,
		LeaseToken:    first.LeaseToken,
		Error:         "stale runtime disconnect",
		FailureReason: "runtime_offline",
	}, testWorkspaceID, "issue-inbox-fence-daemon")
	staleFailReq = withURLParam(staleFailReq, "eventId", first.ID)
	staleFailRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(staleFailRec, staleFailReq)
	if staleFailRec.Code != http.StatusConflict {
		t.Fatalf("stale issue fail: status=%d body=%s, want 409", staleFailRec.Code, staleFailRec.Body.String())
	}

	staleCompleteReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+first.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: first.DeliveryID,
		LeaseToken: first.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: "stale issue completion",
		},
	}, testWorkspaceID, "issue-inbox-fence-daemon")
	staleCompleteReq = withURLParam(staleCompleteReq, "eventId", first.ID)
	staleCompleteRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(staleCompleteRec, staleCompleteReq)
	if staleCompleteRec.Code != http.StatusConflict {
		t.Fatalf("stale issue complete: status=%d body=%s, want 409", staleCompleteRec.Code, staleCompleteRec.Body.String())
	}
	var eventStatus, currentDeliveryStatus, terminalOutcome string
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.terminal_outcome, '')
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.id = $2
		WHERE e.id = $1`, task.ID, second.DeliveryID).Scan(&eventStatus, &currentDeliveryStatus, &terminalOutcome); err != nil {
		t.Fatalf("load state after stale completion: %v", err)
	}
	if eventStatus != "draining" || currentDeliveryStatus != "leased" || terminalOutcome != "" {
		t.Fatalf("state after stale completion = event:%q delivery:%q terminal:%q", eventStatus, currentDeliveryStatus, terminalOutcome)
	}

	secondExecutionID := uuid.NewString()
	start(second, secondExecutionID, http.StatusOK)
	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+second.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    second.DeliveryID,
		LeaseToken:    second.LeaseToken,
		Error:         "runtime disconnected",
		FailureReason: "runtime_offline",
	}, testWorkspaceID, "issue-inbox-fence-daemon")
	failReq = withURLParam(failReq, "eventId", second.ID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail current issue delivery: status=%d body=%s", failRec.Code, failRec.Body.String())
	}
	var parentAttempt, childAttempt int32
	if err := testPool.QueryRow(ctx, `
		SELECT parent.attempt, child.attempt
		FROM agent_inbox_event parent
		JOIN agent_inbox_event child ON child.parent_task_id = parent.id
		WHERE parent.id = $1`, task.ID).Scan(&parentAttempt, &childAttempt); err != nil {
		t.Fatalf("load retry lineage after provider failure: %v", err)
	}
	if parentAttempt != 1 || childAttempt != 2 {
		t.Fatalf("retry lineage attempts = parent:%d child:%d, want 1/2", parentAttempt, childAttempt)
	}
}

func TestAgentInboxProviderStartHoldsDeliveryFenceThroughExecutionInsert(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "provider start fence runtime "+uuid.NewString())
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "provider start fence "+uuid.NewString())
	task, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		IssueID:   parseUUID(issueID),
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("create provider-start event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, task.ID) })

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "provider-start-fence-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain provider-start event: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil || len(drainResp.Events) != 1 {
		t.Fatalf("decode provider-start drain=%v events=%+v", err, drainResp.Events)
	}
	event := drainResp.Events[0]

	blockTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin execution-insert blocker: %v", err)
	}
	defer blockTx.Rollback(context.Background())
	if _, err := blockTx.Exec(ctx, `LOCK TABLE agent_execution IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock execution table: %v", err)
	}

	startRec := httptest.NewRecorder()
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/execution", AgentInboxExecutionRequest{
			DeliveryID:  event.DeliveryID,
			LeaseToken:  event.LeaseToken,
			ExecutionID: uuid.NewString(),
		}, testWorkspaceID, "provider-start-fence-daemon")
		req = withURLParam(req, "eventId", event.ID)
		testHandler.StartAgentInboxExecution(startRec, req)
	}()

	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err := testPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query ILIKE '%INSERT INTO agent_execution%'
			)`).Scan(&blocked); err != nil {
			t.Fatalf("inspect blocked provider start: %v", err)
		}
		if blocked {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("provider start did not reach the blocked execution insert")
		}
		time.Sleep(10 * time.Millisecond)
	}

	reclaimCtx, cancelReclaim := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancelReclaim()
	if _, err := testPool.Exec(reclaimCtx, `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, event.DeliveryID); err == nil {
		t.Fatal("delivery mutation crossed provider-start fence while execution insert was blocked")
	}

	if err := blockTx.Rollback(ctx); err != nil {
		t.Fatalf("release execution-insert blocker: %v", err)
	}
	select {
	case <-startDone:
	case <-time.After(3 * time.Second):
		t.Fatal("provider start did not finish after execution table unlock")
	}
	if startRec.Code != http.StatusOK {
		t.Fatalf("provider start after unlock: status=%d body=%s", startRec.Code, startRec.Body.String())
	}
}

func TestAgentInboxCompletionRollsBackDeliveryAndTaskWhenExecutionFinalizationFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "completion atomic runtime "+uuid.NewString())
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "completion atomic "+uuid.NewString())
	task, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		IssueID:   parseUUID(issueID),
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("create completion-atomic event: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "completion-atomic-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain completion-atomic event: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil || len(drainResp.Events) != 1 {
		t.Fatalf("decode completion-atomic drain=%v events=%+v", err, drainResp.Events)
	}
	event := drainResp.Events[0]
	executionID := uuid.NewString()
	startReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/execution", AgentInboxExecutionRequest{
		DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, ExecutionID: executionID,
	}, testWorkspaceID, "completion-atomic-daemon")
	startReq = withURLParam(startReq, "eventId", event.ID)
	startRec := httptest.NewRecorder()
	testHandler.StartAgentInboxExecution(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start completion-atomic execution: status=%d body=%s", startRec.Code, startRec.Body.String())
	}

	blockTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin execution-finalization blocker: %v", err)
	}
	defer blockTx.Rollback(context.Background())
	if _, err := blockTx.Exec(ctx, `LOCK TABLE agent_execution IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock execution table: %v", err)
	}

	completeBody := CompleteAgentInboxEventRequest{
		DeliveryID:  event.DeliveryID,
		LeaseToken:  event.LeaseToken,
		ExecutionID: executionID,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: "atomic completion",
		},
	}
	completeReq := newDaemonTokenRequest(
		http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/complete", completeBody,
		testWorkspaceID, "completion-atomic-daemon",
	)
	completeReq = withURLParam(completeReq, "eventId", event.ID)
	completeCtx, cancelComplete := context.WithTimeout(completeReq.Context(), 250*time.Millisecond)
	completeReq = completeReq.WithContext(completeCtx)
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeRec, completeReq)
	cancelComplete()
	if completeRec.Code != http.StatusInternalServerError {
		t.Fatalf("blocked completion status=%d body=%s, want 500", completeRec.Code, completeRec.Body.String())
	}
	if err := blockTx.Rollback(ctx); err != nil {
		t.Fatalf("release execution-finalization blocker: %v", err)
	}

	var eventStatus, deliveryStatus, terminalOutcome, executionStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.terminal_outcome, ''), x.status
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.id = $2
		JOIN agent_execution x ON x.id = $3
		WHERE e.id = $1`,
		task.ID, event.DeliveryID, executionID,
	).Scan(&eventStatus, &deliveryStatus, &terminalOutcome, &executionStatus); err != nil {
		t.Fatalf("load rolled-back completion state: %v", err)
	}
	if eventStatus != "draining" || deliveryStatus != "leased" || terminalOutcome != "" || executionStatus != "running" {
		t.Fatalf(
			"rolled-back completion = event:%q delivery:%q terminal:%q execution:%q, want draining/leased/empty/running",
			eventStatus, deliveryStatus, terminalOutcome, executionStatus,
		)
	}

	retryReq := newDaemonTokenRequest(
		http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/complete", completeBody,
		testWorkspaceID, "completion-atomic-daemon",
	)
	retryReq = withURLParam(retryReq, "eventId", event.ID)
	retryRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry completion after rollback: status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.terminal_outcome, ''), x.status
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.id = $2
		JOIN agent_execution x ON x.id = $3
		WHERE e.id = $1`,
		task.ID, event.DeliveryID, executionID,
	).Scan(&eventStatus, &deliveryStatus, &terminalOutcome, &executionStatus); err != nil {
		t.Fatalf("load completed retry state: %v", err)
	}
	if eventStatus != "acked" || deliveryStatus != "acked" || terminalOutcome != "completed" || executionStatus != "completed" {
		t.Fatalf(
			"completed retry = event:%q delivery:%q terminal:%q execution:%q, want acked/acked/completed/completed",
			eventStatus, deliveryStatus, terminalOutcome, executionStatus,
		)
	}
}

func TestAgentInboxFailureRollsBackDeliveryAndTaskWhenExecutionFinalizationFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "failure atomic runtime "+uuid.NewString())
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "failure atomic "+uuid.NewString())
	task, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		IssueID:   parseUUID(issueID),
		Priority:  0,
	})
	if err != nil {
		t.Fatalf("create failure-atomic event: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "failure-atomic-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain failure-atomic event: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil || len(drainResp.Events) != 1 {
		t.Fatalf("decode failure-atomic drain=%v events=%+v", err, drainResp.Events)
	}
	event := drainResp.Events[0]
	executionID := uuid.NewString()
	startReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/execution", AgentInboxExecutionRequest{
		DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, ExecutionID: executionID,
	}, testWorkspaceID, "failure-atomic-daemon")
	startReq = withURLParam(startReq, "eventId", event.ID)
	startRec := httptest.NewRecorder()
	testHandler.StartAgentInboxExecution(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start failure-atomic execution: status=%d body=%s", startRec.Code, startRec.Body.String())
	}

	blockTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin failure-finalization blocker: %v", err)
	}
	defer blockTx.Rollback(context.Background())
	if _, err := blockTx.Exec(ctx, `LOCK TABLE agent_execution IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock execution table: %v", err)
	}

	failBody := FailAgentInboxEventRequest{
		DeliveryID:    event.DeliveryID,
		LeaseToken:    event.LeaseToken,
		Error:         "runtime disconnected during atomic failure",
		FailureReason: "runtime_offline",
	}
	failReq := newDaemonTokenRequest(
		http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/fail", failBody,
		testWorkspaceID, "failure-atomic-daemon",
	)
	failReq = withURLParam(failReq, "eventId", event.ID)
	failCtx, cancelFail := context.WithTimeout(failReq.Context(), 250*time.Millisecond)
	failReq = failReq.WithContext(failCtx)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	cancelFail()
	if failRec.Code != http.StatusInternalServerError {
		t.Fatalf("blocked failure status=%d body=%s, want 500", failRec.Code, failRec.Body.String())
	}
	if err := blockTx.Rollback(ctx); err != nil {
		t.Fatalf("release failure-finalization blocker: %v", err)
	}

	var eventStatus, deliveryStatus, terminalOutcome, executionStatus string
	var retryChildren int
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.terminal_outcome, ''), x.status,
		       (SELECT count(*) FROM agent_inbox_event child WHERE child.parent_task_id = e.id)
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.id = $2
		JOIN agent_execution x ON x.id = $3
		WHERE e.id = $1`,
		task.ID, event.DeliveryID, executionID,
	).Scan(&eventStatus, &deliveryStatus, &terminalOutcome, &executionStatus, &retryChildren); err != nil {
		t.Fatalf("load rolled-back failure state: %v", err)
	}
	if eventStatus != "draining" || deliveryStatus != "leased" || terminalOutcome != "" || executionStatus != "running" || retryChildren != 0 {
		t.Fatalf(
			"rolled-back failure = event:%q delivery:%q terminal:%q execution:%q children:%d",
			eventStatus, deliveryStatus, terminalOutcome, executionStatus, retryChildren,
		)
	}

	retryReq := newDaemonTokenRequest(
		http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/fail", failBody,
		testWorkspaceID, "failure-atomic-daemon",
	)
	retryReq = withURLParam(retryReq, "eventId", event.ID)
	retryRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry failure after rollback: status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.terminal_outcome, ''), x.status,
		       (SELECT count(*) FROM agent_inbox_event child WHERE child.parent_task_id = e.id)
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.id = $2
		JOIN agent_execution x ON x.id = $3
		WHERE e.id = $1`,
		task.ID, event.DeliveryID, executionID,
	).Scan(&eventStatus, &deliveryStatus, &terminalOutcome, &executionStatus, &retryChildren); err != nil {
		t.Fatalf("load failed retry state: %v", err)
	}
	if eventStatus != "acked" || deliveryStatus != "acked" || terminalOutcome != "failed" || executionStatus != "failed" || retryChildren != 1 {
		t.Fatalf(
			"failed retry = event:%q delivery:%q terminal:%q execution:%q children:%d",
			eventStatus, deliveryStatus, terminalOutcome, executionStatus, retryChildren,
		)
	}
}
