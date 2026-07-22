package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func captureReminderChangedEvents(t *testing.T, h *Handler, agentID string) *[]events.Event {
	t.Helper()
	captured := []events.Event{}
	h.Bus.Subscribe(protocol.EventAgentReminderChanged, func(event events.Event) {
		payload, ok := event.Payload.(agentReminderChangedPayload)
		if ok && payload.AgentID == agentID {
			captured = append(captured, event)
		}
	})
	return &captured
}

func seedDueReminder(t *testing.T, agentID, channelID, messageID, cadence, timezone string) string {
	t.Helper()
	due := time.Now().UTC().Add(-time.Minute)
	var reminderID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
			anchor_message_id, fire_at, cadence, schedule_timezone, cadence_next_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::timestamptz, NULLIF($8::text, ''), NULLIF($9::text, ''),
		          CASE WHEN $8::text = '' THEN NULL ELSE $7::timestamptz END)
		RETURNING id`, testWorkspaceID, agentID, testUserID, "check reminder "+uuid.NewString()[:8],
		channelID, messageID, due, cadence, timezone).Scan(&reminderID); err != nil {
		t.Fatalf("seed reminder: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			next_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot, resulting_state
		)
		SELECT id, workspace_id, agent_id, 'scheduled', 'agent', agent_id,
		       fire_at, title, cadence, schedule_timezone, 'scheduled'
		FROM agent_reminder WHERE id = $1`, reminderID); err != nil {
		t.Fatalf("seed reminder lifecycle: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, reminderID)
	})
	return reminderID
}

func reminderFireCounts(t *testing.T, reminderID string) (occurrences, receipts, tasks, firedEvents int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM agent_reminder_occurrence WHERE reminder_id = $1),
		  (SELECT count(*) FROM channel_message WHERE external_message_id LIKE 'reminder_occurrence:%' AND id IN (
		     SELECT receipt_message_id FROM agent_reminder_occurrence WHERE reminder_id = $1
		  )),
		  (SELECT count(*) FROM agent_task_queue WHERE id IN (
		     SELECT fired_task_id FROM agent_reminder_occurrence WHERE reminder_id = $1
		  )),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND event_type = 'fired')`,
		reminderID).Scan(&occurrences, &receipts, &tasks, &firedEvents); err != nil {
		t.Fatalf("load reminder fire counts: %v", err)
	}
	return
}

func TestFireDueReminderOccurrenceIsIdempotentAcrossSchedulers(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{status: "offline"}, {}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	readReq := newRequest(http.MethodPost, "/api/channels/"+fixture.channel.ID+"/read", nil)
	readReq = withChannelTestWorkspaceCtx(t, readReq, testUserID)
	readReq = withURLParam(readReq, "channelId", fixture.channel.ID)
	readRec := httptest.NewRecorder()
	fixture.handler.MarkChannelRead(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("mark reminder channel read: status=%d body=%s", readRec.Code, readRec.Body.String())
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- fixture.handler.FireDueReminders(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("fire due reminders: %v", err)
		}
	}

	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 1 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1 each", occurrences, receipts, tasks, firedEvents)
	}
	var otherTasks, receiptInboxEvents int
	var receiptAuthorType string
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM agent_task_queue WHERE agent_id = $1),
		  (SELECT count(*) FROM agent_inbox_event WHERE source_message_id IN (
		     SELECT receipt_message_id FROM agent_reminder_occurrence WHERE reminder_id = $2
		  )),
		  (SELECT author_type FROM channel_message WHERE id = (
		     SELECT receipt_message_id FROM agent_reminder_occurrence WHERE reminder_id = $2
		  ))`, fixture.agentIDs[1], reminderID).Scan(&otherTasks, &receiptInboxEvents, &receiptAuthorType); err != nil {
		t.Fatal(err)
	}
	if otherTasks != 0 || receiptInboxEvents != 0 || receiptAuthorType != "system" {
		t.Fatalf("receipt projection author=%q other_tasks=%d inbox_events=%d, want system/0/0", receiptAuthorType, otherTasks, receiptInboxEvents)
	}
	listed := listedChannelForUser(t, fixture.channel.ID, testUserID)
	if listed == nil || listed.RealUnreadCount != 0 || listed.UnreadCount != 0 {
		t.Fatalf("system reminder receipt counted as unread: %+v", listed)
	}
	searchReq := newRequest(http.MethodGet, "/api/channels/"+fixture.channel.ID+"/messages/search?q="+url.QueryEscape("check reminder")+"&limit=10", nil)
	searchReq = withChannelTestWorkspaceCtx(t, searchReq, testUserID)
	searchReq = withURLParam(searchReq, "channelId", fixture.channel.ID)
	searchRec := httptest.NewRecorder()
	fixture.handler.SearchChannelMessages(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search reminder receipt: status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchBody ChannelMessageSearchResponse
	if err := json.NewDecoder(searchRec.Body).Decode(&searchBody); err != nil {
		t.Fatal(err)
	}
	if searchBody.Total != 0 || len(searchBody.Results) != 0 {
		t.Fatalf("system reminder receipt leaked into search: %+v", searchBody)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status); err != nil || status != "fired" {
		t.Fatalf("one-shot status = %q err=%v, want fired", status, err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	gotOccurrences, gotReceipts, gotTasks, gotEvents := reminderFireCounts(t, reminderID)
	if gotOccurrences != occurrences || gotReceipts != receipts || gotTasks != tasks || gotEvents != firedEvents {
		t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, tasks, firedEvents, gotOccurrences, gotReceipts, gotTasks, gotEvents)
	}
}

func TestReminderFireTerminalizesWhenAgentRemovedFromAnchorChannel(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`,
		fixture.channel.ID, testWorkspaceID, fixture.agentIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire after membership removal: %v", err)
	}
	assertReminderEligibilityTerminalized(t, reminderID, "agent_removed_from_anchor_channel")
}

func TestReminderFireSerializesAgainstConcurrentMembershipRemoval(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	removeTx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer removeTx.Rollback(context.Background())
	if _, err := removeTx.Exec(context.Background(), `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`,
		fixture.channel.ID, testWorkspaceID, fixture.agentIDs[0]); err != nil {
		t.Fatal(err)
	}

	fireDone := make(chan error, 1)
	go func() { fireDone <- fixture.handler.FireDueReminders(context.Background()) }()
	select {
	case err := <-fireDone:
		t.Fatalf("fire crossed uncommitted membership removal: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := removeTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fireDone:
		if err != nil {
			t.Fatalf("fire after membership removal commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fire did not resume after membership removal committed")
	}
	assertReminderEligibilityTerminalized(t, reminderID, "agent_removed_from_anchor_channel")
}

func TestReminderFireSerializesAgainstConcurrentChannelArchive(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	archiveTx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer archiveTx.Rollback(context.Background())
	if _, err := archiveTx.Exec(context.Background(), `
		UPDATE channel
		SET archived_at = now(), archived_by = $2, updated_at = now()
		WHERE id = $1`, fixture.channel.ID, testUserID); err != nil {
		t.Fatal(err)
	}

	fireDone := make(chan error, 1)
	go func() { fireDone <- fixture.handler.FireDueReminders(context.Background()) }()
	select {
	case err := <-fireDone:
		t.Fatalf("fire crossed uncommitted channel archive: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := archiveTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fireDone:
		if err != nil {
			t.Fatalf("fire after channel archive commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fire did not resume after channel archive committed")
	}
	assertReminderEligibilityTerminalized(t, reminderID, "channel_archived")
}

func TestReminderFireSerializesAgainstConcurrentAgentArchive(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	archiveTx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer archiveTx.Rollback(context.Background())
	if _, err := archiveTx.Exec(context.Background(), `
		UPDATE agent
		SET archived_at = now(), archived_by = $2, updated_at = now()
		WHERE id = $1`, fixture.agentIDs[0], testUserID); err != nil {
		t.Fatal(err)
	}

	fireDone := make(chan error, 1)
	go func() { fireDone <- fixture.handler.FireDueReminders(context.Background()) }()
	select {
	case err := <-fireDone:
		t.Fatalf("fire crossed uncommitted agent archive: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := archiveTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fireDone:
		if err != nil {
			t.Fatalf("fire after agent archive commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fire did not resume after agent archive committed")
	}
	assertReminderEligibilityTerminalized(t, reminderID, "agent_archived")
}

func assertReminderEligibilityTerminalized(t *testing.T, reminderID, wantReason string) {
	t.Helper()
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 0 || firedEvents != 0 {
		t.Fatalf("eligibility terminal counts = occurrence:%d receipt:%d task:%d fired_event:%d, want 1/0/0/0", occurrences, receipts, tasks, firedEvents)
	}
	var definitionStatus, definitionReason, occurrenceStatus, occurrenceReason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, reminder.terminal_reason, occurrence.status, occurrence.terminal_reason
		FROM agent_reminder reminder
		JOIN agent_reminder_occurrence occurrence ON occurrence.reminder_id = reminder.id
		WHERE reminder.id = $1`, reminderID).Scan(&definitionStatus, &definitionReason, &occurrenceStatus, &occurrenceReason); err != nil {
		t.Fatal(err)
	}
	if definitionStatus != "cancelled" || occurrenceStatus != "cancelled" || definitionReason != wantReason || occurrenceReason != definitionReason {
		t.Fatalf("eligibility terminal state definition=%s/%s occurrence=%s/%s want_reason=%s", definitionStatus, definitionReason, occurrenceStatus, occurrenceReason, wantReason)
	}
}

func TestRecurringReminderFireAdvancesFromCadenceAndSnoozeSlot(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	changed := captureReminderChangedEvents(t, fixture.handler, fixture.agentIDs[0])
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "every:15m", "")
	var originalSlot time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT cadence_next_at FROM agent_reminder WHERE id = $1`, reminderID).Scan(&originalSlot); err != nil {
		t.Fatal(err)
	}
	// Model a snooze of only the current occurrence: fire_at moves while the
	// cadence slot remains the original immutable recurrence position.
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET fire_at = now() - interval '5 seconds' WHERE id = $1`, reminderID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire recurring reminder: %v", err)
	}
	var status string
	var nextFire, nextCadence time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT status, fire_at, cadence_next_at FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status, &nextFire, &nextCadence); err != nil {
		t.Fatal(err)
	}
	want := originalSlot.Add(15 * time.Minute)
	for !want.After(time.Now().UTC()) {
		want = want.Add(15 * time.Minute)
	}
	if status != "scheduled" || !nextFire.Equal(nextCadence) || !nextFire.Equal(want) {
		t.Fatalf("recurring advance status=%s fire=%s cadence=%s want=%s", status, nextFire, nextCadence, want)
	}
	if len(*changed) != 1 {
		t.Fatalf("reminder fire changed events=%d, want 1", len(*changed))
	}

	request := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil)
	request = withURLParam(request, "id", fixture.agentIDs[0])
	recorder := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list fired reminders status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page humanReminderPage
	if err := json.NewDecoder(recorder.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Occurrences) != 1 || page.Occurrences[0].Status != "fired" || page.Occurrences[0].DefinitionStatus != "scheduled" {
		t.Fatalf("recurring occurrence/definition status layering = %+v", page.Occurrences)
	}
}

func TestDeletedReminderAnchorFiresWithUnavailableMarker(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, anchor.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire deleted anchor reminder: %v", err)
	}
	var available bool
	var prompt, receipt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.anchor_available, prompt.content, receipt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		JOIN channel_message receipt ON receipt.id = occurrence.receipt_message_id
		WHERE occurrence.reminder_id = $1`, reminderID).Scan(&available, &prompt, &receipt); err != nil {
		t.Fatal(err)
	}
	if available || !strings.Contains(prompt, "Anchor message: unavailable") || !strings.HasSuffix(receipt, " · Anchor unavailable") {
		t.Fatalf("deleted anchor available=%v prompt=%q receipt=%q", available, prompt, receipt)
	}
}

func TestDeletedReminderThreadRootHidesAnchorEverywhere(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	root := fixture.insertMessage(t, "user", testUserID, "root secret anchor", nil)
	reply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", "reply secret anchor", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(root.ID), stringPtr("reminder-deleted-root"), 0)
	if err != nil {
		t.Fatal(err)
	}
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, reply.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET anchor_thread_root_message_id = $2 WHERE id = $1`, reminderID, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, root.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire deleted-root reminder: %v", err)
	}
	var available bool
	var prompt, receipt, parts string
	var receiptThreadRoot *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.anchor_available, prompt.content, receipt.content,
		       receipt.parts::text, receipt.thread_root_message_id::text
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		JOIN channel_message receipt ON receipt.id = occurrence.receipt_message_id
		WHERE occurrence.reminder_id = $1`, reminderID).Scan(&available, &prompt, &receipt, &parts, &receiptThreadRoot); err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{root.ID, reply.ID, fixture.channel.Name, "root secret anchor", "reply secret anchor"} {
		if strings.Contains(prompt, leaked) || strings.Contains(parts, leaked) {
			t.Fatalf("deleted root leaked %q in prompt=%q parts=%s", leaked, prompt, parts)
		}
	}
	if available || receiptThreadRoot != nil || !strings.Contains(prompt, "Anchor message: unavailable") || !strings.HasSuffix(receipt, " · Anchor unavailable") || !strings.Contains(parts, `"anchor_available": false`) {
		t.Fatalf("deleted root projection available=%v thread_root=%v prompt=%q receipt=%q parts=%s", available, receiptThreadRoot, prompt, receipt, parts)
	}
	reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	request := withChannelTestWorkspaceCtx(t, newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil), testUserID)
	if anchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder); anchor.Available || anchor.Kind != nil || anchor.Display != nil || anchor.Href != nil {
		t.Fatalf("deleted root human anchor = %+v, want unavailable without metadata", anchor)
	}
}

func TestReminderFireFallsBackToCurrentAgentOwnerWhenInitiatorIsGone(t *testing.T) {
	for _, mode := range []string{"membership_removed", "user_deleted"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
			anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
			initiatorID := seedWorkspaceUserForTransportTargetTest(t, "reminder-initiator-"+mode+"-"+uuid.NewString())
			reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
			if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET initiator_user_id = $2 WHERE id = $1`, reminderID, initiatorID); err != nil {
				t.Fatal(err)
			}

			// Reproduce the stale session edge: the schedule-time initiator once
			// owned the canonical channel session, then that association vanished
			// before the occurrence fired.
			session, err := fixture.handler.ensureChannelAgentSession(context.Background(), fixture.channel, parseUUID(fixture.agentIDs[0]), parseUUID(initiatorID))
			if err != nil {
				t.Fatalf("seed initiator session: %v", err)
			}
			switch mode {
			case "membership_removed":
				if _, err := testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, initiatorID); err != nil {
					t.Fatal(err)
				}
				if _, err := testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, session.ID); err != nil {
					t.Fatal(err)
				}
			case "user_deleted":
				if _, err := testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, initiatorID); err != nil {
					t.Fatal(err)
				}
			}

			if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
				t.Fatalf("fire after initiator removal: %v", err)
			}
			occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
			if occurrences != 1 || receipts != 1 || tasks != 1 || firedEvents != 1 {
				t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1 each", occurrences, receipts, tasks, firedEvents)
			}
			var taskInitiatorID, sessionCreatorID string
			if err := testPool.QueryRow(context.Background(), `
				SELECT task.initiator_user_id, session.creator_id
				FROM agent_reminder_occurrence occurrence
				JOIN agent_task_queue task ON task.id = occurrence.fired_task_id
				JOIN chat_session session ON session.id = task.chat_session_id
				WHERE occurrence.reminder_id = $1`, reminderID).Scan(&taskInitiatorID, &sessionCreatorID); err != nil {
				t.Fatal(err)
			}
			if taskInitiatorID != testUserID || sessionCreatorID != testUserID {
				t.Fatalf("fallback task/session creator = %s/%s, want current agent owner %s", taskInitiatorID, sessionCreatorID, testUserID)
			}
			if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
				t.Fatalf("idempotent retry after initiator removal: %v", err)
			}
			gotOccurrences, gotReceipts, gotTasks, gotEvents := reminderFireCounts(t, reminderID)
			if gotOccurrences != occurrences || gotReceipts != receipts || gotTasks != tasks || gotEvents != firedEvents {
				t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, tasks, firedEvents, gotOccurrences, gotReceipts, gotTasks, gotEvents)
			}
		})
	}
}

func TestRecoverStaleFiringWithDurableTaskNeverRearmsWake(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatal(err)
	}
	var occurrenceID, taskID string
	if err := testPool.QueryRow(context.Background(), `SELECT id, fired_task_id FROM agent_reminder_occurrence WHERE reminder_id = $1`, reminderID).Scan(&occurrenceID, &taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND event_type = 'fired'`, reminderID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder_occurrence
		SET status = 'claimed', claimed_at = now() - interval '10 minutes', fired_at = NULL
		WHERE id = $1`, occurrenceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET status = 'firing', current_occurrence_id = $2, fired_at = NULL
		WHERE id = $1`, reminderID, occurrenceID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue WHERE agent_id = $1`, fixture.agentIDs[0]).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.RecoverStuckFiringReminders(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after int
	var status, persistedTaskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, occurrence.fired_task_id,
		       (SELECT count(*) FROM agent_task_queue WHERE agent_id = $2)
		FROM agent_reminder reminder
		JOIN agent_reminder_occurrence occurrence ON occurrence.reminder_id = reminder.id
		WHERE reminder.id = $1`, reminderID, fixture.agentIDs[0]).Scan(&status, &persistedTaskID, &after); err != nil {
		t.Fatal(err)
	}
	if before != after || persistedTaskID != taskID || status != "fired" {
		t.Fatalf("recovery rearmed or failed: before=%d after=%d task=%s/%s status=%s", before, after, persistedTaskID, taskID, status)
	}
}

func TestArchivedReminderChannelTerminalizesWithoutWake(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	changed := captureReminderChangedEvents(t, fixture.handler, fixture.agentIDs[0])
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET archived_at = now() WHERE id = $1`, fixture.channel.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire archived channel reminder: %v", err)
	}
	var status, reason string
	var tasks int
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, terminal_reason,
		       (SELECT count(*) FROM agent_task_queue WHERE id IN (
		          SELECT fired_task_id FROM agent_reminder_occurrence WHERE reminder_id = $1
		       ))
		FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status, &reason, &tasks); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || reason != "channel_archived" || tasks != 0 {
		t.Fatalf("terminal state=%s reason=%s tasks=%d", status, reason, tasks)
	}
	if len(*changed) != 1 {
		t.Fatalf("terminalize changed events=%d, want 1", len(*changed))
	}
}

func TestHumanReminderAnchorDenialOmitsRawMetadata(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest("GET", "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil)
	anchorResponse := fixture.handler.safeHumanReminderAnchor(request, uuid.NewString(), reminder)
	encoded, err := json.Marshal(anchorResponse)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"available":false}` {
		t.Fatalf("denied anchor leaked metadata: %s", encoded)
	}
}

func TestListAgentRemindersReturnsLayeredSafeProjection(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "daily@09:00", "Asia/Shanghai")
	request := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=scheduled", nil)
	request = withURLParam(request, "id", fixture.agentIDs[0])
	recorder := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list reminders status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response humanReminderPage
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Definitions) != 1 || len(response.Occurrences) != 0 {
		t.Fatalf("unexpected layered response: %+v", response)
	}
	definition := response.Definitions[0]
	if definition.ID != reminderID || definition.ScheduleKind != "recurring" || definition.Cadence == nil || *definition.Cadence != "daily@09:00" || definition.ScheduleTimezone == nil || *definition.ScheduleTimezone != "Asia/Shanghai" {
		t.Fatalf("unexpected definition: %+v", definition)
	}
	var workspaceSlug string
	if err := testPool.QueryRow(context.Background(), `SELECT slug FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&workspaceSlug); err != nil {
		t.Fatal(err)
	}
	wantHref := "/" + workspaceSlug + "/channels/" + fixture.channel.ID + "?message=" + anchor.ID
	if !definition.Anchor.Available || definition.Anchor.Href == nil || *definition.Anchor.Href != wantHref {
		t.Fatalf("missing safe anchor href: got=%+v want=%s", definition.Anchor, wantHref)
	}
	encoded, err := json.Marshal(definition.Anchor)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"channel_id", "message_id", "thread_root_message_id", "target"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("authorized anchor leaked raw navigation field %q: %s", forbidden, encoded)
		}
	}
	if response.Realtime.EventType != protocol.EventAgentReminderChanged || response.Realtime.Scope != "agent" || response.Realtime.ID != fixture.agentIDs[0] || response.Realtime.Payload != "agent_id" {
		t.Fatalf("unexpected reminder realtime contract: %+v", response.Realtime)
	}
	reply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", "thread anchor reply", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(anchor.ID), stringPtr("reminder-test-thread"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET anchor_message_id = $2, anchor_thread_root_message_id = $3
		WHERE id = $1`, reminderID, reply.ID, anchor.ID); err != nil {
		t.Fatal(err)
	}
	reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	threadAnchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
	wantThreadHref := "/" + workspaceSlug + "/channels/" + fixture.channel.ID + "?thread=" + anchor.ID + "&message=" + reply.ID
	if !threadAnchor.Available || threadAnchor.Kind == nil || *threadAnchor.Kind != "thread" || threadAnchor.Href == nil || *threadAnchor.Href != wantThreadHref {
		t.Fatalf("thread anchor did not deep-link to authorized root: %+v", threadAnchor)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET kind = 'dm' WHERE id = $1`, fixture.channel.ID); err != nil {
		t.Fatal(err)
	}
	dmAnchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
	dmEncoded, err := json.Marshal(dmAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if dmAnchor.Display == nil || *dmAnchor.Display != "Thread in direct message" || strings.Contains(string(dmEncoded), fixture.channel.Name) {
		t.Fatalf("DM anchor leaked canonical channel name: %s", dmEncoded)
	}
}

func TestAgentReminderTransportLocksTimezoneAndLogsLifecycle(t *testing.T) {
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	changed := captureReminderChangedEvents(t, testHandler, agentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_task_queue SET initiator_user_id = $2 WHERE id = $1`, taskID, testUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE "user" SET timezone = 'Asia/Shanghai' WHERE id = $1`, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE "user" SET timezone = NULL WHERE id = $1`, testUserID)
	})
	message, err := testHandler.insertChannelMessage(context.Background(), parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester", "reminder transport anchor", "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	scheduleReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/schedule", taskID, agentID, map[string]any{
		"title": "daily check", "repeat": "daily@09:00", "message_id": message.ID,
	})
	scheduleRec := httptest.NewRecorder()
	testHandler.AgentTransportScheduleReminder(scheduleRec, scheduleReq)
	if scheduleRec.Code != http.StatusCreated {
		t.Fatalf("schedule status=%d body=%s", scheduleRec.Code, scheduleRec.Body.String())
	}
	var scheduled agentReminderResponse
	if err := json.NewDecoder(scheduleRec.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
	})
	if scheduled.Cadence == nil || *scheduled.Cadence != "daily@09:00" || scheduled.ScheduleTimezone == nil || *scheduled.ScheduleTimezone != "Asia/Shanghai" {
		t.Fatalf("schedule did not lock initiator timezone: %+v", scheduled)
	}

	// Later initiator timezone changes cannot reinterpret an existing calendar
	// definition, including when its cadence is replaced.
	if _, err := testPool.Exec(context.Background(), `UPDATE "user" SET timezone = 'America/New_York' WHERE id = $1`, testUserID); err != nil {
		t.Fatal(err)
	}
	updateReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/update", taskID, agentID, map[string]any{
		"id": scheduled.ID, "cadence": "weekly:mon,fri@10:30",
	})
	updateRec := httptest.NewRecorder()
	testHandler.AgentTransportUpdateReminder(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	var updated agentReminderResponse
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.ScheduleTimezone == nil || *updated.ScheduleTimezone != "Asia/Shanghai" {
		t.Fatalf("cadence update drifted locked timezone: %+v", updated)
	}
	for _, transition := range []struct {
		cadence     string
		wantVisible *string
	}{
		{cadence: "every:2h"},
		{cadence: "daily@08:15", wantVisible: stringPtr("Asia/Shanghai")},
	} {
		req := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/update", taskID, agentID, map[string]any{"id": scheduled.ID, "cadence": transition.cadence})
		rec := httptest.NewRecorder()
		testHandler.AgentTransportUpdateReminder(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("transition %s status=%d body=%s", transition.cadence, rec.Code, rec.Body.String())
		}
		var response agentReminderResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if (response.ScheduleTimezone == nil) != (transition.wantVisible == nil) || (transition.wantVisible != nil && *response.ScheduleTimezone != *transition.wantVisible) {
			t.Fatalf("transition %s timezone=%v want=%v", transition.cadence, response.ScheduleTimezone, transition.wantVisible)
		}
	}

	snoozeReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/snooze", taskID, agentID, map[string]any{"id": scheduled.ID, "delay_seconds": 300})
	snoozeRec := httptest.NewRecorder()
	testHandler.AgentTransportSnoozeReminder(snoozeRec, snoozeReq)
	if snoozeRec.Code != http.StatusOK {
		t.Fatalf("snooze status=%d body=%s", snoozeRec.Code, snoozeRec.Body.String())
	}

	cancelReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/cancel", taskID, agentID, map[string]any{"id": scheduled.ID})
	cancelRec := httptest.NewRecorder()
	testHandler.AgentTransportCancelReminder(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	logReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/log", taskID, agentID, map[string]any{"id": scheduled.ID})
	logRec := httptest.NewRecorder()
	testHandler.AgentTransportReminderLog(logRec, logReq)
	if logRec.Code != http.StatusOK {
		t.Fatalf("log status=%d body=%s", logRec.Code, logRec.Body.String())
	}
	var logBody struct {
		Events []agentReminderLifecycleResponse `json:"events"`
	}
	if err := json.NewDecoder(logRec.Body).Decode(&logBody); err != nil {
		t.Fatal(err)
	}
	want := []string{"scheduled", "updated", "updated", "updated", "snoozed", "cancelled"}
	if len(logBody.Events) != len(want) {
		t.Fatalf("lifecycle events=%+v", logBody.Events)
	}
	for i, eventType := range want {
		if logBody.Events[i].EventType != eventType {
			t.Fatalf("event[%d]=%s, want %s", i, logBody.Events[i].EventType, eventType)
		}
	}
	if len(*changed) != 6 {
		t.Fatalf("schedule/update/snooze/cancel changed events=%d, want 6", len(*changed))
	}
	for _, event := range *changed {
		encoded, err := json.Marshal(event.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != `{"agent_id":"`+agentID+`"}` {
			t.Fatalf("reminder invalidate payload leaked metadata: %s", encoded)
		}
	}
}

func TestAgentReminderExplicitTimeUpdateConvertsRecurrenceToOneShotAndRetainsTimezoneLock(t *testing.T) {
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_task_queue SET initiator_user_id = $2 WHERE id = $1`, taskID, testUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE "user" SET timezone = 'Asia/Shanghai' WHERE id = $1`, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE "user" SET timezone = NULL WHERE id = $1`, testUserID)
	})
	message, err := testHandler.insertChannelMessage(context.Background(), parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester", "one-shot conversion anchor", "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	scheduleReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/schedule", taskID, agentID, map[string]any{
		"title": "timezone-preserving one-shot", "repeat": "daily@09:00", "message_id": message.ID,
	})
	scheduleRec := httptest.NewRecorder()
	testHandler.AgentTransportScheduleReminder(scheduleRec, scheduleReq)
	if scheduleRec.Code != http.StatusCreated {
		t.Fatalf("schedule status=%d body=%s", scheduleRec.Code, scheduleRec.Body.String())
	}
	var scheduled agentReminderResponse
	if err := json.NewDecoder(scheduleRec.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
	})

	updateSchedule := func(body map[string]any) agentReminderResponse {
		t.Helper()
		req := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/update", taskID, agentID, body)
		rec := httptest.NewRecorder()
		testHandler.AgentTransportUpdateReminder(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update %+v status=%d body=%s", body, rec.Code, rec.Body.String())
		}
		var response agentReminderResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	oneShot := updateSchedule(map[string]any{"id": scheduled.ID, "delay_seconds": 300})
	if oneShot.Cadence != nil || oneShot.CadenceNextAt != nil || oneShot.ScheduleTimezone != nil {
		t.Fatalf("explicit time update exposed recurrence state: %+v", oneShot)
	}
	var cadence, timezone pgtype.Text
	var cadenceNextAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT cadence, schedule_timezone, cadence_next_at FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&cadence, &timezone, &cadenceNextAt); err != nil {
		t.Fatal(err)
	}
	if cadence.Valid || cadenceNextAt.Valid || !timezone.Valid || timezone.String != "Asia/Shanghai" {
		t.Fatalf("hidden timezone lock cadence=%v timezone=%v cadence_next_at=%v", cadence, timezone, cadenceNextAt)
	}

	recurring := updateSchedule(map[string]any{"id": scheduled.ID, "cadence": "daily@08:15"})
	if recurring.Cadence == nil || *recurring.Cadence != "daily@08:15" || recurring.ScheduleTimezone == nil || *recurring.ScheduleTimezone != "Asia/Shanghai" {
		t.Fatalf("calendar recurrence did not reuse original timezone: %+v", recurring)
	}
	updateSchedule(map[string]any{"id": scheduled.ID, "delay_seconds": 300})
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET fire_at = now() - interval '1 second' WHERE id = $1`, scheduled.ID); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("fire converted one-shot: %v", err)
	}
	before := [4]int{}
	before[0], before[1], before[2], before[3] = reminderFireCounts(t, scheduled.ID)
	if before != [4]int{1, 1, 1, 1} {
		t.Fatalf("converted one-shot fire counts=%v, want [1 1 1 1]", before)
	}
	if err := testHandler.FireDueReminders(context.Background()); err != nil {
		t.Fatalf("retry converted one-shot: %v", err)
	}
	after := [4]int{}
	after[0], after[1], after[2], after[3] = reminderFireCounts(t, scheduled.ID)
	if after != before {
		t.Fatalf("converted one-shot fired more than once: before=%v after=%v", before, after)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&status); err != nil || status != "fired" {
		t.Fatalf("converted one-shot status=%q err=%v, want fired", status, err)
	}
}
