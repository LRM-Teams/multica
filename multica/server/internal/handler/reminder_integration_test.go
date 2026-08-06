package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func seedDueManagedPatrol(t *testing.T, fixture channelAgentRuntimeFixture) string {
	return seedDueManagedPatrolWithActiveIssue(t, fixture, true)
}

func seedDueManagedPatrolWithActiveIssue(t *testing.T, fixture channelAgentRuntimeFixture, active bool) string {
	t.Helper()
	t.Skip("retired with channel_member manager-role cutover")
	if len(fixture.agentIDs) < 1 {
		t.Fatal("managed reminder fixture requires a manager")
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE channel_member SET role = 'manager'
		 WHERE member_type = 'agent' AND member_id = $1 AND channel_id = $2`,
		fixture.agentIDs[0], fixture.channel.ID); err != nil {
		t.Fatalf("bind group manager channel: %v", err)
	}
	if active {
		issueID := createCommentTriggerPreviewIssue(t, "managed patrol active "+uuid.NewString(), "", "")
		anchor := fixture.insertMessage(t, "user", testUserID, "managed patrol issue anchor", nil)
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, $4)
		`, issueID, testWorkspaceID, fixture.channel.ID, anchor.ID); err != nil {
			t.Fatalf("link active issue to managed group: %v", err)
		}
	}
	due := time.Now().UTC().Add(-time.Minute)
	var reminderID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  fire_at, cadence, cadence_next_at, origin_kind, managed_kind,
		  origin_key
		) VALUES (
		  $1, $2, $3, 'managed patrol', $4, $5, NULL, NULL,
		  'group_manager_auto', 'patrol',
		  'patrol:' || gen_random_uuid()::text
		)
		RETURNING id`, testWorkspaceID, fixture.agentIDs[0], testUserID,
		fixture.channel.ID, due).Scan(&reminderID); err != nil {
		t.Fatalf("seed managed reminder: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_reminder_lifecycle_event (
		  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
		  next_fire_at, title_snapshot, cadence_snapshot, resulting_state
		)
		SELECT id, workspace_id, agent_id, 'scheduled', 'system', agent_id,
		       fire_at, title, cadence, 'scheduled'
		FROM agent_reminder WHERE id = $1`, reminderID); err != nil {
		t.Fatalf("seed managed reminder lifecycle: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, reminderID)
	})
	return reminderID
}

func managedPatrolDelayForStep(step int16) time.Duration {
	switch step {
	case 0:
		return 15 * time.Minute
	case 1:
		return 30 * time.Minute
	case 2:
		return 45 * time.Minute
	default:
		return time.Hour
	}
}

func managedPatrolStepForDelaySeconds(delaySeconds *int64) (int16, error) {
	if delaySeconds == nil {
		return 0, errors.New("retired managed patrol delay is required")
	}
	switch *delaySeconds {
	case 900:
		return 0, nil
	case 1800:
		return 1, nil
	case 2700:
		return 2, nil
	case 3600:
		return 3, nil
	default:
		return 0, errors.New("retired managed patrol delay is invalid")
	}
}

func fireReminderAttempt(h *Handler, reminderID string) error {
	var agentID, workspaceID, runtimeID string
	var version, placementGeneration int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.agent_id, reminder.workspace_id, agent.runtime_id, reminder.version
		FROM agent_reminder reminder
		JOIN agent ON agent.id = reminder.agent_id
		WHERE reminder.id = $1`, reminderID).Scan(&agentID, &workspaceID, &runtimeID, &version); err != nil {
		return err
	}
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, agentID).Scan(&placementGeneration); err != nil {
		return err
	}
	_, err := h.HandleDaemonReminderFireAttempt(context.Background(), daemonws.ClientIdentity{
		WorkspaceID: workspaceID,
		RuntimeIDs:  []string{runtimeID},
	}, protocol.ReminderFireAttemptPayload{
		AgentID:             agentID,
		RuntimeID:           runtimeID,
		PlacementGeneration: placementGeneration,
		ReminderID:          reminderID,
		Version:             version,
		FiredAtClient:       time.Now().UTC().Format(time.RFC3339Nano),
	})
	return err
}

func reminderFireCounts(t *testing.T, reminderID string) (occurrences, receipts, tasks, firedEvents int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM agent_reminder_occurrence WHERE reminder_id = $1),
		  (SELECT count(*) FROM channel_message WHERE external_message_id IN (
		     SELECT 'reminder_occurrence:' || id::text
		     FROM agent_reminder_occurrence WHERE reminder_id = $1
		  )),
		  (SELECT count(*) FROM agent_inbox_event WHERE id IN (
		     SELECT fired_task_id FROM agent_reminder_occurrence WHERE reminder_id = $1
		  )),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND event_type = 'fired')`,
		reminderID).Scan(&occurrences, &receipts, &tasks, &firedEvents); err != nil {
		t.Fatalf("load reminder fire counts: %v", err)
	}
	return
}

func waitForReminderChannelRowLock(t *testing.T, channelID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := testPool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var one int
		err = probe.QueryRow(context.Background(), `SELECT 1 FROM channel WHERE id = $1 FOR UPDATE NOWAIT`, channelID).Scan(&one)
		_ = probe.Rollback(context.Background())
		if err == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return
		}
		t.Fatalf("probe reminder channel lock: %v", err)
	}
	t.Fatal("reminder fire did not acquire the channel row lock")
}

type reminderOnboardingResult struct {
	active  bool
	updated int64
	err     error
}

func terminalizeActiveChannelOnboardingForReminderRace(onboardingID, channelID, agentID string) reminderOnboardingResult {
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		return reminderOnboardingResult{err: err}
	}
	defer tx.Rollback(context.Background())
	active, err := channelOnboardingGenerationActiveTx(context.Background(), tx,
		parseUUID(onboardingID), parseUUID(channelID), parseUUID(agentID), true)
	if err != nil || !active {
		return reminderOnboardingResult{active: active, err: err}
	}
	tag, err := tx.Exec(context.Background(), `
		UPDATE channel_agent_onboarding
		SET status = 'skipped', terminal_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, onboardingID)
	if err == nil {
		err = tx.Commit(context.Background())
	}
	return reminderOnboardingResult{active: active, updated: tag.RowsAffected(), err: err}
}

func TestDaemonReminderFireAttemptIsIdempotentAcrossConnections(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{status: "offline"}, {}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	var channelMessagesBefore int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM channel_message WHERE channel_id = $1`,
		fixture.channel.ID).Scan(&channelMessagesBefore); err != nil {
		t.Fatal(err)
	}
	var publishedMu sync.Mutex
	publishedChannelMessages := 0
	fixture.handler.Bus.Subscribe(protocol.EventChannelMessage, func(event events.Event) {
		message, ok := event.Payload.(ChannelMessageResponse)
		if !ok || message.ChannelID != fixture.channel.ID {
			return
		}
		publishedMu.Lock()
		publishedChannelMessages++
		publishedMu.Unlock()
	})

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- fireReminderAttempt(fixture.handler, reminderID)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("submit daemon reminder fire attempt: %v", err)
		}
	}

	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1/0/1/1", occurrences, receipts, tasks, firedEvents)
	}
	var channelMessagesAfter, otherTasks int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_message WHERE channel_id = $1),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2)`,
		fixture.channel.ID, fixture.agentIDs[1]).Scan(&channelMessagesAfter, &otherTasks); err != nil {
		t.Fatal(err)
	}
	publishedMu.Lock()
	gotPublishedChannelMessages := publishedChannelMessages
	publishedMu.Unlock()
	if channelMessagesAfter != channelMessagesBefore || gotPublishedChannelMessages != 0 || otherTasks != 0 {
		t.Fatalf("receipt suppression channel_messages=%d→%d broadcasts=%d other_tasks=%d, want zero deltas",
			channelMessagesBefore, channelMessagesAfter, gotPublishedChannelMessages, otherTasks)
	}
	var receiptNull, taskPresent, anchorAvailable bool
	var occurrenceID, title, prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.receipt_message_id IS NULL, occurrence.fired_task_id IS NOT NULL,
		       occurrence.anchor_available, occurrence.id::text, occurrence.title_snapshot,
		       prompt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1`,
		reminderID).Scan(&receiptNull, &taskPresent, &anchorAvailable, &occurrenceID, &title, &prompt); err != nil {
		t.Fatal(err)
	}
	if !receiptNull || !taskPresent || !anchorAvailable {
		t.Fatalf("fired occurrence receipt_null=%v task_present=%v anchor_available=%v, want true/true/true",
			receiptNull, taskPresent, anchorAvailable)
	}
	for _, want := range []string{
		"A self-scheduled reminder is due.",
		"Reminder id: " + reminderID,
		"Occurrence id: " + occurrenceID,
		"Reminder title: " + title,
		"Current message id: " + anchor.ID,
		"Anchor message excerpt: anchor",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("directed wake prompt missing %q: %q", want, prompt)
		}
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status); err != nil || status != "fired" {
		t.Fatalf("one-shot status = %q err=%v, want fired", status, err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	gotOccurrences, gotReceipts, gotTasks, gotEvents := reminderFireCounts(t, reminderID)
	if gotOccurrences != occurrences || gotReceipts != receipts || gotTasks != tasks || gotEvents != firedEvents {
		t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, tasks, firedEvents, gotOccurrences, gotReceipts, gotTasks, gotEvents)
	}
	historyReq := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil),
		testUserID)
	historyReq = withURLParam(historyReq, "id", fixture.agentIDs[0])
	historyRec := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("list fired reminder history: status=%d body=%s", historyRec.Code, historyRec.Body.String())
	}
	var history humanReminderPage
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Occurrences) != 1 || history.Occurrences[0].ReminderID != reminderID ||
		history.Occurrences[0].Status != "fired" {
		t.Fatalf("fired reminder history = %+v, want one fired occurrence", history.Occurrences)
	}
}

func TestManagedPatrolWakesForActiveIssueAndArmsControlledDialFallbackWithoutHistory(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrol(t, fixture)
	var before time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT fire_at FROM agent_reminder WHERE id = $1`, reminderID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire managed patrol: %v", err)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("managed patrol counts = %d/%d/%d/%d, want 1/0/1/1",
			occurrences, receipts, tasks, firedEvents)
	}
	var status, occurrenceReason string
	var next, last time.Time
	var cadence *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, reminder.fire_at, reminder.fired_at,
		       reminder.cadence, lifecycle.reason_code
		FROM agent_reminder reminder
		JOIN agent_reminder_occurrence occurrence
		  ON occurrence.reminder_id = reminder.id
		JOIN agent_reminder_lifecycle_event lifecycle
		  ON lifecycle.occurrence_id = occurrence.id
		 AND lifecycle.event_type = 'fired'
		WHERE reminder.id = $1`, reminderID).Scan(&status, &next, &last, &cadence, &occurrenceReason); err != nil {
		t.Fatal(err)
	}
	delay := time.Until(next)
	if status != "scheduled" || !next.After(before) || last.IsZero() || cadence != nil ||
		delay < 30*time.Minute-time.Minute || delay > 30*time.Minute+time.Minute ||
		occurrenceReason != "patrol_controlled_dial_fallback_rearmed" {
		t.Fatalf("managed patrol state status=%s before=%s next=%s delay=%s last=%s cadence=%v reason=%s",
			status, before, next, delay, last, cadence, occurrenceReason)
	}

	historyReq := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil),
		testUserID)
	historyReq = withURLParam(historyReq, "id", fixture.agentIDs[0])
	historyRec := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("list managed reminder history: status=%d body=%s", historyRec.Code, historyRec.Body.String())
	}
	var history humanReminderPage
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	for _, occurrence := range history.Occurrences {
		if occurrence.ReminderID == reminderID {
			t.Fatalf("managed patrol leaked into human history: %+v", occurrence)
		}
	}
}

func TestManagedPatrolWakesForMessageOpenLoopWithoutIssue(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	root := fixture.insertMessage(
		t,
		"user",
		testUserID,
		"Please research whether the provider timeout changed and send me the conclusion tomorrow.",
		nil,
	)
	if _, err := fixture.handler.insertChannelMessageWithParts(
		context.Background(),
		parseUUID(fixture.channel.ID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(fixture.agentIDs[1]),
		fixture.agentNames[1],
		"I am investigating and will report tomorrow.",
		nil,
		"multica",
		nil,
		pgtype.UUID{},
		parseUUID(root.ID),
		strPtr("patrol-open-loop-thread"),
		0,
	); err != nil {
		t.Fatal(err)
	}
	reminderID := seedDueManagedPatrolWithActiveIssue(t, fixture, false)

	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire message-backed managed patrol: %v", err)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("message-backed patrol counts = %d/%d/%d/%d, want 1/0/1/1",
			occurrences, receipts, tasks, firedEvents)
	}
	var prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT prompt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1
	`, reminderID).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Active issue candidates (0)",
		"Recent group/thread evidence (2, chronological)",
		"Please research whether the provider timeout changed",
		"thread_root=" + root.ID,
		"I am investigating and will report tomorrow",
		"research discussion can lack a conclusion even when no issue exists",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("message-backed patrol prompt missing %q: %q", want, prompt)
		}
	}
}

func TestManagedPatrolWithoutOpenLoopContextBecomesDormantWithoutAgentTask(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrolWithActiveIssue(t, fixture, false)

	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire dormant managed patrol: %v", err)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 0 || firedEvents != 0 {
		t.Fatalf("dormant patrol counts = %d/%d/%d/%d, want 1/0/0/0",
			occurrences, receipts, tasks, firedEvents)
	}

	var reminderStatus, occurrenceStatus, reason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, occurrence.status, occurrence.terminal_reason
		FROM agent_reminder reminder
		JOIN agent_reminder_occurrence occurrence ON occurrence.reminder_id = reminder.id
		WHERE reminder.id = $1
	`, reminderID).Scan(&reminderStatus, &occurrenceStatus, &reason); err != nil {
		t.Fatal(err)
	}
	if reminderStatus != "fired" || occurrenceStatus != "cancelled" || reason != "patrol_no_open_loop_context_dormant" {
		t.Fatalf("dormant patrol state=%s/%s/%s, want fired/cancelled/patrol_no_open_loop_context_dormant",
			reminderStatus, occurrenceStatus, reason)
	}

	request := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=scheduled", nil),
		testUserID)
	request = withURLParam(request, "id", fixture.agentIDs[0])
	recorder := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list dormant managed patrol: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page humanReminderPage
	if err := json.NewDecoder(recorder.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Definitions) != 1 {
		t.Fatalf("dormant patrol definitions=%+v, want one visible managed definition", page.Definitions)
	}
	definition := page.Definitions[0]
	if definition.ID != reminderID || definition.Status != "fired" ||
		definition.NextFireAt != nil || definition.LastFireAt == nil ||
		definition.OriginKind != "group_manager_auto" ||
		definition.ManagedKind == nil || *definition.ManagedKind != "patrol" {
		t.Fatalf("dormant patrol projection=%+v, want visible dormant row without a false next fire", definition)
	}
}

func TestManagedPatrolDormantMessageRearmDoesNotPostponeScheduledTimer(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrolWithActiveIssue(t, fixture, false)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET status = 'fired',
		    current_occurrence_id = NULL,
		    terminal_reason = NULL,
		    fired_task_id = NULL,
		    version = version + 1
		WHERE id = $1
	`, reminderID); err != nil {
		t.Fatal(err)
	}
	notifier := &recordingReminderNotifier{}
	fixture.handler.ReminderNotifier = notifier
	changedEvents := captureReminderChangedEvents(t, fixture.handler, fixture.agentIDs[0])

	first := fixture.insertMessage(t, "user", testUserID, "Please research the provider timeout and report a conclusion.", nil)
	var status, reason string
	var firstFireAt time.Time
	var firstVersion int64
	var details map[string]any
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, reminder.fire_at, reminder.version,
		       lifecycle.reason_code, lifecycle.details
		FROM agent_reminder reminder
		JOIN agent_reminder_lifecycle_event lifecycle
		  ON lifecycle.reminder_id = reminder.id
		 AND lifecycle.reason_code = 'patrol_open_loop_message_rearm'
		WHERE reminder.id = $1
		ORDER BY lifecycle.created_at DESC
		LIMIT 1
	`, reminderID).Scan(&status, &firstFireAt, &firstVersion, &reason, &details); err != nil {
		t.Fatal(err)
	}
	delay := time.Until(firstFireAt)
	if status != "scheduled" || reason != "patrol_open_loop_message_rearm" ||
		delay < 14*time.Minute || delay > 16*time.Minute ||
		details["message_id"] != first.ID {
		t.Fatalf("message rearm status=%s delay=%s reason=%s details=%v", status, delay, reason, details)
	}
	if !reflect.DeepEqual(notifier.order, []string{"start", "projection"}) ||
		len(notifier.starts) != 1 || len(notifier.projections) != 1 {
		t.Fatalf("message rearm notifier order=%v starts=%d projections=%d, want [start projection]/1/1",
			notifier.order, len(notifier.starts), len(notifier.projections))
	}
	if len(*changedEvents) != 1 {
		t.Fatalf("message rearm human invalidations=%d, want 1", len(*changedEvents))
	}
	projection := notifier.projections[0]
	if projection.EventType != "upsert" || projection.ReminderID != reminderID ||
		projection.Version != firstVersion || projection.Terminal || projection.FireAt == "" {
		t.Fatalf("message rearm projection=%+v, want live non-terminal upsert version %d", projection, firstVersion)
	}

	fixture.insertMessage(t, "agent", fixture.agentIDs[1], "I am investigating now.", nil)
	var secondFireAt time.Time
	var secondVersion int64
	var rearmEvents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.fire_at, reminder.version,
		       (
		         SELECT count(*)
		         FROM agent_reminder_lifecycle_event lifecycle
		         WHERE lifecycle.reminder_id = reminder.id
		           AND lifecycle.reason_code = 'patrol_open_loop_message_rearm'
		       )
		FROM agent_reminder reminder
		WHERE reminder.id = $1
	`, reminderID).Scan(&secondFireAt, &secondVersion, &rearmEvents); err != nil {
		t.Fatal(err)
	}
	if !secondFireAt.Equal(firstFireAt) || secondVersion != firstVersion || rearmEvents != 1 {
		t.Fatalf("scheduled timer was postponed fire_at=%s/%s version=%d/%d events=%d",
			firstFireAt, secondFireAt, firstVersion, secondVersion, rearmEvents)
	}
	if !reflect.DeepEqual(notifier.order, []string{"start", "projection"}) ||
		len(notifier.starts) != 1 || len(notifier.projections) != 1 {
		t.Fatalf("scheduled timer emitted duplicate notifier order=%v starts=%d projections=%d",
			notifier.order, len(notifier.starts), len(notifier.projections))
	}
	if len(*changedEvents) != 1 {
		t.Fatalf("scheduled timer emitted duplicate human invalidations=%d", len(*changedEvents))
	}
}

func TestQuickCreateGroupThreadCommitRearmsDormantManagedPatrolLive(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	root := fixture.insertMessage(t, "user", testUserID, "Create an issue from this thread.", nil)
	reminderID := seedDueManagedPatrolWithActiveIssue(t, fixture, false)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET status = 'fired',
		    current_occurrence_id = NULL,
		    terminal_reason = NULL,
		    fired_task_id = NULL,
		    version = version + 1
		WHERE id = $1
	`, reminderID); err != nil {
		t.Fatal(err)
	}

	notifier := &recordingReminderNotifier{}
	fixture.handler.ReminderNotifier = notifier
	fixture.handler.TaskService = &service.TaskService{
		PrepareCanonicalChannelMessageCommit: fixture.handler.prepareCanonicalChannelMessageCommit,
	}

	ctx := context.Background()
	tx, err := fixture.handler.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var messageID string
	var messageSeq int64
	threadID := "quick-create-patrol-" + uuid.NewString()
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel_message (
		  channel_id, workspace_id, author_type, author_id, author_name,
		  content, parts, source, client_message_id, thread_root_message_id,
		  thread_id, trigger_depth
		) VALUES (
		  $1, $2, 'agent', $3, 'Quick Create Agent',
		  'Created issue MUL-1 from this thread.', '[]'::jsonb, 'multica',
		  $4, $5, $6, 1
		)
		RETURNING id::text, seq
	`, fixture.channel.ID, testWorkspaceID, fixture.agentIDs[1],
		"quick-create-return:"+uuid.NewString(), root.ID, threadID).Scan(&messageID, &messageSeq); err != nil {
		t.Fatal(err)
	}
	afterCommit, err := fixture.handler.TaskService.PrepareCanonicalChannelMessageCommit(
		ctx,
		tx,
		service.CanonicalChannelMessage{
			ID:                  parseUUID(messageID),
			WorkspaceID:         parseUUID(testWorkspaceID),
			ChannelID:           parseUUID(fixture.channel.ID),
			ThreadRootMessageID: parseUUID(root.ID),
			ThreadID:            pgtype.Text{String: threadID, Valid: true},
			AuthorType:          "agent",
			Seq:                 messageSeq,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if afterCommit == nil {
		t.Fatal("quick-create group/thread message did not prepare a patrol after-commit publication")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	afterCommit(ctx)

	var status, reason string
	var version int64
	var details map[string]any
	if err := testPool.QueryRow(ctx, `
		SELECT reminder.status, reminder.version, lifecycle.reason_code, lifecycle.details
		FROM agent_reminder reminder
		JOIN agent_reminder_lifecycle_event lifecycle
		  ON lifecycle.reminder_id = reminder.id
		 AND lifecycle.reason_code = 'patrol_open_loop_message_rearm'
		WHERE reminder.id = $1
		ORDER BY lifecycle.created_at DESC
		LIMIT 1
	`, reminderID).Scan(&status, &version, &reason, &details); err != nil {
		t.Fatal(err)
	}
	if status != "scheduled" || reason != "patrol_open_loop_message_rearm" ||
		details["message_id"] != messageID {
		t.Fatalf("quick-create rearm status=%s reason=%s details=%v", status, reason, details)
	}
	if !reflect.DeepEqual(notifier.order, []string{"start", "projection"}) ||
		len(notifier.projections) != 1 ||
		notifier.projections[0].ReminderID != reminderID ||
		notifier.projections[0].Version != version ||
		notifier.projections[0].EventType != "upsert" {
		t.Fatalf("quick-create live notifier order=%v projections=%+v", notifier.order, notifier.projections)
	}
}

func TestManagedPatrolMessageRearmDoesNotPublishOnCommitFailure(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrolWithActiveIssue(t, fixture, false)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET status = 'fired',
		    current_occurrence_id = NULL,
		    terminal_reason = NULL,
		    fired_task_id = NULL,
		    version = version + 1
		WHERE id = $1
	`, reminderID); err != nil {
		t.Fatal(err)
	}
	var beforeVersion int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT version FROM agent_reminder WHERE id = $1`, reminderID).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}

	notifier := &recordingReminderNotifier{}
	h := *fixture.handler
	h.ReminderNotifier = notifier
	h.TxStarter = commitFailingTxStarter{base: fixture.handler.TxStarter}
	content := "commit failure must not publish a patrol projection " + uuid.NewString()
	if _, err := h.insertChannelMessageWithParts(
		context.Background(),
		parseUUID(fixture.channel.ID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		content,
		nil,
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err == nil || !strings.Contains(err.Error(), "injected radar commit failure") {
		t.Fatalf("commit-failing message insert error=%v, want injected commit failure", err)
	}
	if len(notifier.starts) != 0 || len(notifier.projections) != 0 || len(notifier.order) != 0 {
		t.Fatalf("commit-failed rearm notified start/projection/order=%d/%d/%v, want none",
			len(notifier.starts), len(notifier.projections), notifier.order)
	}

	var status string
	var afterVersion int64
	var messageCount, rearmEvents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT reminder.status, reminder.version,
		       (SELECT count(*) FROM channel_message WHERE channel_id = $2 AND content = $3),
		       (
		         SELECT count(*)
		         FROM agent_reminder_lifecycle_event lifecycle
		         WHERE lifecycle.reminder_id = reminder.id
		           AND lifecycle.reason_code = 'patrol_open_loop_message_rearm'
		       )
		FROM agent_reminder reminder
		WHERE reminder.id = $1
	`, reminderID, fixture.channel.ID, content).Scan(&status, &afterVersion, &messageCount, &rearmEvents); err != nil {
		t.Fatal(err)
	}
	if status != "fired" || afterVersion != beforeVersion || messageCount != 0 || rearmEvents != 0 {
		t.Fatalf("commit-failed state=%s version=%d/%d messages=%d rearm_events=%d, want fired/unchanged/0/0",
			status, afterVersion, beforeVersion, messageCount, rearmEvents)
	}
}

func TestManagedPatrolDelayStepsStayInsideOneHourTaskWindow(t *testing.T) {
	tests := []struct {
		step int16
		want time.Duration
	}{
		{step: 0, want: 15 * time.Minute},
		{step: 1, want: 30 * time.Minute},
		{step: 2, want: 45 * time.Minute},
		{step: 3, want: time.Hour},
		{step: 4, want: time.Hour},
	}
	for _, tt := range tests {
		if got := managedPatrolDelayForStep(tt.step); got != tt.want {
			t.Fatalf("managed patrol step %d delay=%s, want %s", tt.step, got, tt.want)
		}
	}
	for _, tt := range []struct {
		delay int64
		step  int16
	}{
		{delay: 900, step: 0},
		{delay: 1800, step: 1},
		{delay: 2700, step: 2},
		{delay: 3600, step: 3},
	} {
		delay := tt.delay
		got, err := managedPatrolStepForDelaySeconds(&delay)
		if err != nil || got != tt.step {
			t.Fatalf("managed patrol delay %d step=%d err=%v, want %d", delay, got, err, tt.step)
		}
	}
	for _, delay := range []int64{899, 1200, 3601} {
		if _, err := managedPatrolStepForDelaySeconds(&delay); err == nil {
			t.Fatalf("managed patrol invalid delay %d accepted", delay)
		}
	}
}

func TestManagedPatrolFallbackUsesControlledDialWithoutBlockedOverride(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrol(t, fixture)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE issue
		SET status = 'blocked'
		WHERE id = (
		  SELECT issue_id FROM issue_source_message
		  WHERE channel_id = $1
		  ORDER BY created_at DESC
		  LIMIT 1
		)
	`, fixture.channel.ID); err != nil {
		t.Fatalf("block managed patrol issue: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET fire_at = now() - interval '1 minute', managed_backoff_step = 3,
		    version = version + 1
		WHERE id = $1
	`, reminderID); err != nil {
		t.Fatalf("make blocked patrol due: %v", err)
	}

	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire blocked managed patrol: %v", err)
	}
	var next time.Time
	var step int16
	if err := testPool.QueryRow(context.Background(), `
		SELECT fire_at, managed_backoff_step
		FROM agent_reminder WHERE id = $1
	`, reminderID).Scan(&next, &step); err != nil {
		t.Fatal(err)
	}
	delay := time.Until(next)
	if step != 3 || delay < 59*time.Minute || delay > 61*time.Minute {
		t.Fatalf("blocked patrol next step/delay=%d/%s, want 3/about 60m", step, delay)
	}
}

func TestManagedPatrolWakePromptUsesOpenLoopEvidenceAndControlledReminderDial(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	reminderID := seedDueManagedPatrol(t, fixture)
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire managed patrol: %v", err)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("managed patrol counts = %d/%d/%d/%d, want 1/0/1/1",
			occurrences, receipts, tasks, firedEvents)
	}
	var prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT prompt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1`, reminderID).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Human instructions override this patrol mechanism",
		"Only evidence rows with author=user carry human instruction authority",
		"managed open-loop patrol",
		"evidence candidates, not conclusions",
		"Active issue candidates",
		"Recent group/thread evidence",
		"Your recent outbound DM reminders",
		"question/request can be unanswered",
		"verbal commitment can lack the promised action",
		"research discussion can lack a conclusion",
		"Busy chat without a real next step is not progress",
		"quiet work that is proceeding normally is not stalled",
		"privately DM the responsible person",
		"Do not publicly chase in the group",
		"do not repeat it",
		"per-pair messaging budgets",
		"multica reminder snooze --id <reminder-id> --delay-seconds <seconds>",
		"exactly one of 900, 1800, 2700, or 3600",
		"Never create, cancel, or mutate any other patrol reminder",
		"server has already armed a bounded fallback",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("managed patrol prompt missing %q: %q", want, prompt)
		}
	}
	for _, obsolete := range []string{
		"900 to 28800 seconds",
		"server owns the 15/30/45/60-minute patrol schedule",
		"do not snooze, reschedule, or create another patrol reminder",
		"DM a human/member target",
		"two unanswered private/thread attempts",
		"Routing policy:",
		"Use 900 when any issue is blocked",
		"reset this same reminder to 15 minutes on real issue progress",
	} {
		if strings.Contains(prompt, obsolete) {
			t.Fatalf("managed patrol prompt retained obsolete mechanical route %q: %q", obsolete, prompt)
		}
	}
}

func TestFireReminderOccurrenceFailsClosedWhenExistingTaskIsNonTerminal(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'pending', 0)
		RETURNING id`, fixture.agentIDs[0], fixture.runtimeIDs[0]).Scan(&taskID); err != nil {
		t.Fatalf("seed impossible reminder task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
	})

	var occurrenceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_reminder_occurrence (
			reminder_id, workspace_id, agent_id, cadence_scheduled_for, due_at,
			status, title_snapshot, claimed_at, fired_task_id
		)
		SELECT id, workspace_id, agent_id, fire_at, fire_at,
		       'claimed', title, now(), $2
		FROM agent_reminder
		WHERE id = $1
		RETURNING id`, reminderID, taskID).Scan(&occurrenceID); err != nil {
		t.Fatalf("seed impossible reminder occurrence: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_reminder
		SET status = 'firing', current_occurrence_id = $2, updated_at = now()
		WHERE id = $1`, reminderID, occurrenceID); err != nil {
		t.Fatalf("seed impossible reminder definition: %v", err)
	}

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	reminder, err := scanAgentReminder(tx.QueryRow(ctx, `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 FOR UPDATE`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	err = fixture.handler.fireReminderOccurrenceWithTx(ctx, tx, reminder, parseUUID(occurrenceID))
	if err == nil || !strings.Contains(err.Error(), "reminder occurrence invariant violation") {
		t.Fatalf("fire impossible occurrence error = %v, want invariant violation", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var occurrenceStatus, reminderStatus string
	var currentOccurrenceID, firedTaskID string
	var firedAt *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT occurrence.status, occurrence.fired_task_id, occurrence.fired_at,
		       reminder.status, reminder.current_occurrence_id
		FROM agent_reminder_occurrence occurrence
		JOIN agent_reminder reminder ON reminder.id = occurrence.reminder_id
		WHERE occurrence.id = $1`, occurrenceID).Scan(
		&occurrenceStatus, &firedTaskID, &firedAt, &reminderStatus, &currentOccurrenceID,
	); err != nil {
		t.Fatal(err)
	}
	if occurrenceStatus != "claimed" || firedTaskID != taskID || firedAt != nil ||
		reminderStatus != "firing" || currentOccurrenceID != occurrenceID {
		t.Fatalf("impossible state mutated: occurrence=%s task=%s fired_at=%v reminder=%s current=%s",
			occurrenceStatus, firedTaskID, firedAt, reminderStatus, currentOccurrenceID)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 0 {
		t.Fatalf("fail-closed counts = occurrence:%d receipt:%d task:%d event:%d, want 1/0/1/0",
			occurrences, receipts, tasks, firedEvents)
	}
}

func TestReminderOwnerLifecycleReplayFencesRuntimeMigrationAndCompactsAckedHistory(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	agentID := fixture.agentIDs[0]
	runtimeA := fixture.runtimeIDs[0]
	var runtimeB string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', 'reminder lifecycle migration target', $3, now())
		RETURNING id`, testWorkspaceID, "reminder-lifecycle-target-"+uuid.NewString(), []byte(`{"capabilities":["reminder_versioned_cache_v1"]}`)).Scan(&runtimeB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeB)
	})
	identityA := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeA}}
	identityB := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeB}}

	initial, cursors, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identityA, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeA: 0},
	})
	if err != nil || len(initial) != 1 || initial[0].AgentID != agentID || initial[0].EventType != "start" || initial[0].PlacementGeneration < 1 {
		t.Fatalf("initial lifecycle = %+v cursors=%v err=%v", initial, cursors, err)
	}
	initialGeneration := initial[0].PlacementGeneration
	quiet, quietCursors, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identityA, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeA: cursors[runtimeA]},
	})
	if err != nil || len(quiet) != 0 || quietCursors[runtimeA] != cursors[runtimeA] {
		t.Fatalf("lifecycle without scheduled owner = %+v cursors=%v err=%v", quiet, quietCursors, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, runtimeB); err != nil {
		t.Fatal(err)
	}

	oldRuntime, oldCursors, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identityA, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeA: cursors[runtimeA]},
	})
	if err != nil || len(oldRuntime) != 1 || oldRuntime[0].EventType != "stop" || oldRuntime[0].PlacementGeneration <= initialGeneration {
		t.Fatalf("old-runtime replay = %+v cursors=%v err=%v", oldRuntime, oldCursors, err)
	}
	newRuntime, newCursors, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identityB, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeB: 0},
	})
	if err != nil || len(newRuntime) != 1 || newRuntime[0].EventType != "start" || newRuntime[0].PlacementGeneration != oldRuntime[0].PlacementGeneration {
		t.Fatalf("new-runtime replay = %+v cursors=%v err=%v", newRuntime, newCursors, err)
	}

	_, err = fixture.handler.HandleDaemonReminderSnapshot(context.Background(), identityA, protocol.ReminderSnapshotRequestPayload{
		AgentID: agentID, RuntimeID: runtimeA, PlacementGeneration: initialGeneration,
	})
	var ownerGone *daemonws.ReminderOwnerGoneError
	if !errors.As(err, &ownerGone) || ownerGone.RuntimeID != runtimeA || ownerGone.PlacementGeneration != oldRuntime[0].PlacementGeneration {
		t.Fatalf("stale owner snapshot err = %#v", err)
	}
	current, err := fixture.handler.HandleDaemonReminderSnapshot(context.Background(), identityB, protocol.ReminderSnapshotRequestPayload{
		AgentID: agentID, RuntimeID: runtimeB, PlacementGeneration: newRuntime[0].PlacementGeneration,
	})
	if err != nil || current.RuntimeID != runtimeB || current.PlacementGeneration != newRuntime[0].PlacementGeneration {
		t.Fatalf("current owner snapshot = %+v err=%v", current, err)
	}

	if err := fixture.handler.HandleDaemonReminderOwnerLifecycleAck(context.Background(), identityA, protocol.DaemonAgentLifecycleAckPayload{
		RuntimeCursors: map[string]int64{runtimeA: oldCursors[runtimeA]},
	}); err != nil {
		t.Fatal(err)
	}
	var oldHistory int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1 AND runtime_id = $2`, agentID, runtimeA).Scan(&oldHistory); err != nil {
		t.Fatal(err)
	}
	if oldHistory != 1 {
		t.Fatalf("acked old-runtime history rows = %d, want one latest checkpoint", oldHistory)
	}
	if _, _, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identityA, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeB: 0},
	}); err == nil {
		t.Fatal("cross-runtime lifecycle replay accepted")
	}
}

func TestReminderCurrentOwnerCheckpointRecoversAckedDefinitionToOccurrenceHistory(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	agentID := fixture.agentIDs[0]
	runtimeID := fixture.runtimeIDs[0]
	identity := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	anchor := fixture.insertMessage(t, "user", testUserID, "acked reminder recovery anchor", nil)
	reminderID := seedDueReminder(t, agentID, fixture.channel.ID, anchor.ID, "", "")

	var generation, lifecycleCursor, projectionCursor int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(max(placement_generation), 0), COALESCE(max(seq), 0)
		FROM agent_reminder_daemon_owner_event
		WHERE agent_id = $1 AND runtime_id = $2`, agentID, runtimeID).Scan(&generation, &lifecycleCursor); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(max(seq), 0)
		FROM agent_reminder_daemon_projection_event
		WHERE reminder_id = $1 AND runtime_id = $2`, reminderID, runtimeID).Scan(&projectionCursor); err != nil {
		t.Fatal(err)
	}
	if generation < 1 || lifecycleCursor < 1 || projectionCursor < 1 {
		t.Fatalf("seed owner/projection generation=%d lifecycle=%d projection=%d", generation, lifecycleCursor, projectionCursor)
	}
	if err := fixture.handler.HandleDaemonReminderProjectionAck(context.Background(), identity, protocol.ReminderProjectionAckPayload{
		RuntimeCursors: map[string]int64{runtimeID: projectionCursor},
	}); err != nil {
		t.Fatal(err)
	}
	var remainingProjectionRows int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_reminder_daemon_projection_event
		WHERE reminder_id = $1 AND runtime_id = $2 AND seq <= $3`,
		reminderID, runtimeID, projectionCursor).Scan(&remainingProjectionRows); err != nil || remainingProjectionRows != 0 {
		t.Fatalf("acked reminder projections=%d err=%v, want zero", remainingProjectionRows, err)
	}

	checkpoints, cursors, err := fixture.handler.HandleDaemonReminderOwnerLifecycle(context.Background(), identity, protocol.DaemonAgentLifecycleRequestPayload{
		RuntimeCursors: map[string]int64{runtimeID: lifecycleCursor},
	})
	if err != nil || len(checkpoints) != 1 || checkpoints[0].EventType != "start" ||
		checkpoints[0].AgentID != agentID || checkpoints[0].PlacementGeneration != generation ||
		cursors[runtimeID] != lifecycleCursor {
		t.Fatalf("authoritative owner checkpoint=%+v cursors=%v err=%v", checkpoints, cursors, err)
	}
	snapshot, err := fixture.handler.HandleDaemonReminderSnapshot(context.Background(), identity, protocol.ReminderSnapshotRequestPayload{
		AgentID: agentID, RuntimeID: runtimeID, PlacementGeneration: generation,
	})
	if err != nil || snapshot.ProjectionWatermark != projectionCursor || len(snapshot.Reminders) != 1 ||
		snapshot.Reminders[0].ReminderID != reminderID {
		t.Fatalf("acked reminder snapshot=%+v err=%v", snapshot, err)
	}
	if _, err := fixture.handler.HandleDaemonReminderFireAttempt(context.Background(), identity, protocol.ReminderFireAttemptPayload{
		AgentID: agentID, RuntimeID: runtimeID, PlacementGeneration: generation,
		ReminderID: reminderID, Version: snapshot.Reminders[0].Version,
		FiredAtClient: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("recovered reminder history=%d/%d/%d/%d, want 1/0/1/1", occurrences, receipts, tasks, firedEvents)
	}
}

func TestReminderProjectionReplaySharesPlacementGenerationAndAllowsZeroAck(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	agentID := fixture.agentIDs[0]
	runtimeID := fixture.runtimeIDs[0]
	identity := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, fixture.agentIDs[1], runtimeID); err != nil {
		t.Fatal(err)
	}

	// A newly registered runtime with no timer mutations must still be able to
	// durably ACK cursor zero. Otherwise reconnect repeats an empty replay
	// forever and never advances to snapshots.
	if err := fixture.handler.HandleDaemonReminderProjectionAck(context.Background(), identity, protocol.ReminderProjectionAckPayload{
		RuntimeCursors: map[string]int64{runtimeID: 0},
	}); err != nil {
		t.Fatalf("zero projection ack: %v", err)
	}

	anchor := fixture.insertMessage(t, "user", testUserID, "projection generation anchor", nil)
	reminderID := seedDueReminder(t, agentID, fixture.channel.ID, anchor.ID, "", "")
	var generation int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT max(placement_generation)
		FROM agent_reminder_daemon_owner_event
		WHERE agent_id = $1 AND runtime_id = $2`, agentID, runtimeID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	forcedEvents, forcedEnd, err := fixture.handler.HandleDaemonReminderProjection(context.Background(), identity, protocol.ReminderProjectionRequestPayload{
		RuntimeCursors:       map[string]int64{runtimeID: 0},
		RuntimeResidencies:   map[string][]protocol.ReminderRuntimeResidency{runtimeID: {{AgentID: agentID, PlacementGeneration: generation}}},
		RuntimeResetRequired: map[string]bool{runtimeID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	forcedReset, ok := forcedEnd.RuntimeResets[runtimeID]
	if len(forcedEvents) != 0 || !ok || forcedReset.ProjectionWatermark < 1 || len(forcedReset.Owners) != 1 || len(forcedReset.Owners[0].Reminders) != 1 || forcedReset.Owners[0].Reminders[0].ReminderID != reminderID {
		t.Fatalf("forced canonical reset with zero ack events=%+v end=%+v", forcedEvents, forcedEnd)
	}
	events, end, err := fixture.handler.HandleDaemonReminderProjection(context.Background(), identity, protocol.ReminderProjectionRequestPayload{
		RuntimeCursors: map[string]int64{runtimeID: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	var projection *protocol.ReminderProjectionEvent
	for i := range events {
		if events[i].ReminderID == reminderID {
			projection = &events[i]
			break
		}
	}
	if projection == nil {
		t.Fatalf("replay omitted reminder %s: %+v", reminderID, events)
	}
	if projection.PlacementGeneration != generation || generation < 1 {
		t.Fatalf("projection generation=%d owner generation=%d", projection.PlacementGeneration, generation)
	}
	if end.RuntimeCursors[runtimeID] < projection.Seq {
		t.Fatalf("replay watermark=%d before projection=%d", end.RuntimeCursors[runtimeID], projection.Seq)
	}
	if err := fixture.handler.HandleDaemonReminderProjectionAck(context.Background(), identity, protocol.ReminderProjectionAckPayload{
		RuntimeCursors: map[string]int64{runtimeID: end.RuntimeCursors[runtimeID]},
	}); err != nil {
		t.Fatalf("projection ack: %v", err)
	}
	var remaining int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_reminder_daemon_projection_event
		WHERE runtime_id = $1 AND seq <= $2`, runtimeID, end.RuntimeCursors[runtimeID]).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("acked projection rows=%d err=%v", remaining, err)
	}
	resetEvents, resetEnd, err := fixture.handler.HandleDaemonReminderProjection(context.Background(), identity, protocol.ReminderProjectionRequestPayload{
		RuntimeCursors:     map[string]int64{runtimeID: 0},
		RuntimeResidencies: map[string][]protocol.ReminderRuntimeResidency{runtimeID: {{AgentID: agentID, PlacementGeneration: generation}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resetEvents) != 0 {
		t.Fatalf("cursor-loss reset replayed GC events: %+v", resetEvents)
	}
	reset, ok := resetEnd.RuntimeResets[runtimeID]
	if !ok || reset.ProjectionWatermark != end.RuntimeCursors[runtimeID] || len(reset.Owners) != 1 {
		t.Fatalf("cursor-loss reset=%+v end=%+v", reset, resetEnd)
	}
	if reset.Owners[0].AgentID != agentID || reset.Owners[0].Terminal || len(reset.Owners[0].Reminders) != 1 || reset.Owners[0].Reminders[0].ReminderID != reminderID {
		t.Fatalf("cursor-loss reset owner=%+v", reset.Owners[0])
	}
	if reset.Owners[0].AgentID == fixture.agentIDs[1] {
		t.Fatal("server injected an unsubmitted resident owner")
	}
}

func TestReminderRuntimeResetDefinitionsAndWatermarkShareOneOrderBoundary(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	agentID := fixture.agentIDs[0]
	runtimeID := fixture.runtimeIDs[0]
	identity := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	anchor := fixture.insertMessage(t, "user", testUserID, "runtime reset boundary", nil)
	reminderID := seedDueReminder(t, agentID, fixture.channel.ID, anchor.ID, "", "")
	events, firstEnd, err := fixture.handler.HandleDaemonReminderProjection(context.Background(), identity, protocol.ReminderProjectionRequestPayload{RuntimeCursors: map[string]int64{runtimeID: 0}})
	if err != nil || len(events) == 0 {
		t.Fatalf("seed projection events=%d err=%v", len(events), err)
	}
	if err := fixture.handler.HandleDaemonReminderProjectionAck(context.Background(), identity, protocol.ReminderProjectionAckPayload{RuntimeCursors: firstEnd.RuntimeCursors}); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, agentID).Scan(&generation); err != nil {
		t.Fatal(err)
	}

	holder, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 210))`, runtimeID); err != nil {
		t.Fatal(err)
	}
	type resetResult struct {
		reset protocol.ReminderRuntimeReset
		err   error
	}
	resetDone := make(chan resetResult, 1)
	go func() {
		reset, err := fixture.handler.buildReminderRuntimeReset(context.Background(), identity, runtimeID, 0, []protocol.ReminderRuntimeResidency{{AgentID: agentID, PlacementGeneration: generation}}, false)
		resetDone <- resetResult{reset: reset, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting int
		if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reset did not reach runtime advisory boundary")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mutationDone := make(chan error, 1)
	go func() {
		tx, err := testPool.Begin(context.Background())
		if err == nil {
			_, err = tx.Exec(context.Background(), `SELECT 1 FROM agent WHERE id = $1 FOR UPDATE`, agentID)
		}
		if err == nil {
			_, err = tx.Exec(context.Background(), `UPDATE agent_reminder SET title = 'after reset boundary', version = version + 1, updated_at = now() WHERE id = $1`, reminderID)
		}
		if err == nil {
			err = tx.Commit(context.Background())
		} else if tx != nil {
			_ = tx.Rollback(context.Background())
		}
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		t.Fatalf("mutation crossed locked reset owner before boundary release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := holder.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := <-resetDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
	if result.reset.ProjectionWatermark != firstEnd.RuntimeCursors[runtimeID] || len(result.reset.Owners) != 1 || len(result.reset.Owners[0].Reminders) != 1 || result.reset.Owners[0].Reminders[0].Version != 1 {
		t.Fatalf("reset returned mixed definitions/watermark: first=%d reset=%+v", firstEnd.RuntimeCursors[runtimeID], result.reset)
	}
	replay, replayEnd, err := fixture.handler.HandleDaemonReminderProjection(context.Background(), identity, protocol.ReminderProjectionRequestPayload{RuntimeCursors: map[string]int64{runtimeID: result.reset.ProjectionWatermark}})
	if err != nil {
		t.Fatal(err)
	}
	var dbVersion int64
	if err := testPool.QueryRow(context.Background(), `SELECT version FROM agent_reminder WHERE id = $1`, reminderID).Scan(&dbVersion); err != nil {
		t.Fatal(err)
	}
	if dbVersion != 2 || replayEnd.RuntimeCursors[runtimeID] <= result.reset.ProjectionWatermark {
		t.Fatalf("post-reset DB/replay truth version=%d end=%+v reset=%+v", dbVersion, replayEnd, result.reset)
	}
	found := false
	for _, event := range replay {
		if event.ReminderID == reminderID && event.Version == dbVersion {
			found = true
		}
	}
	if !found {
		t.Fatalf("post-reset replay omitted DB truth version=%d events=%+v", dbVersion, replay)
	}
}

func TestUpdateAgentMoveWithActiveReminderFailsClosedForIncapableRuntime(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {omitCapability: true}})

	// task #(machine-lock, 2026-08-02): newChannelAgentRuntimeFixture's
	// runtimes default to runtime_mode='cloud', daemon_id=NULL — each its
	// own unshareable machine. This test's concern (capability gating on a
	// same-machine runtime move) is orthogonal to machine identity, so
	// co-locate the two runtimes on one synthetic daemon rather than
	// changing the shared fixture (other tests using it don't move agents
	// between its runtimes and shouldn't need to care).
	// (workspace_id, daemon_id, provider) is unique, and both runtimes here
	// default to provider 'pi' — give the second a distinct provider so
	// sharing a daemon_id doesn't collide, matching how a real machine runs
	// more than one provider.
	sharedDaemonID := "reminder-move-capability-daemon-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET runtime_mode = 'local', daemon_id = $2 WHERE id = $1
	`, fixture.runtimeIDs[0], sharedDaemonID); err != nil {
		t.Fatalf("colocate reminder move-capability runtime 0: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET runtime_mode = 'local', daemon_id = $2, provider = 'pi-incapable' WHERE id = $1
	`, fixture.runtimeIDs[1], sharedDaemonID); err != nil {
		t.Fatalf("colocate reminder move-capability runtime 1: %v", err)
	}

	anchor := fixture.insertMessage(t, "user", testUserID, "move capability anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+fixture.agentIDs[0], map[string]any{
		"runtime_id": fixture.runtimeIDs[1],
	}), "id", fixture.agentIDs[0])
	w := httptest.NewRecorder()
	fixture.handler.UpdateAgent(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "daemon_outdated") {
		t.Fatalf("incapable move status=%d body=%s", w.Code, w.Body.String())
	}
	var runtimeID, status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent.runtime_id::text, reminder.status
		FROM agent
		JOIN agent_reminder reminder ON reminder.agent_id = agent.id
		WHERE agent.id = $1 AND reminder.id = $2`, fixture.agentIDs[0], reminderID).Scan(&runtimeID, &status); err != nil {
		t.Fatal(err)
	}
	if runtimeID != fixture.runtimeIDs[0] || status != "scheduled" {
		t.Fatalf("rejected move mutated runtime/status = %s/%s", runtimeID, status)
	}
}

func TestDaemonReminderFireAttemptRejectsStaleCancelledAndCrossOwner(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	var version int64
	if err := testPool.QueryRow(context.Background(), `SELECT version FROM agent_reminder WHERE id = $1`, reminderID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	identity := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{fixture.runtimeIDs[0]}}
	var placementGeneration int64
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, fixture.agentIDs[0]).Scan(&placementGeneration); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []protocol.ReminderFireAttemptPayload{
		{AgentID: fixture.agentIDs[0], RuntimeID: fixture.runtimeIDs[0], PlacementGeneration: placementGeneration, ReminderID: reminderID, Version: version + 1},
		{AgentID: fixture.agentIDs[1], RuntimeID: fixture.runtimeIDs[0], PlacementGeneration: placementGeneration, ReminderID: reminderID, Version: version},
	} {
		_, _ = fixture.handler.HandleDaemonReminderFireAttempt(context.Background(), identity, payload)
	}
	if occurrences, receipts, tasks, fired := reminderFireCounts(t, reminderID); occurrences+receipts+tasks+fired != 0 {
		t.Fatalf("invalid attempts mutated fire state: %d/%d/%d/%d", occurrences, receipts, tasks, fired)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET status = 'cancelled', version = version + 1 WHERE id = $1`, reminderID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.handler.HandleDaemonReminderFireAttempt(context.Background(), identity, protocol.ReminderFireAttemptPayload{
		AgentID: fixture.agentIDs[0], RuntimeID: fixture.runtimeIDs[0], PlacementGeneration: placementGeneration, ReminderID: reminderID, Version: version + 1,
	}); err != nil {
		t.Fatal(err)
	}
	if occurrences, receipts, tasks, fired := reminderFireCounts(t, reminderID); occurrences+receipts+tasks+fired != 0 {
		t.Fatalf("cancelled attempt mutated fire state: %d/%d/%d/%d", occurrences, receipts, tasks, fired)
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
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
	go func() { fireDone <- fireReminderAttempt(fixture.handler, reminderID) }()
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
	go func() { fireDone <- fireReminderAttempt(fixture.handler, reminderID) }()
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
	go func() { fireDone <- fireReminderAttempt(fixture.handler, reminderID) }()
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
		var ownerGone *daemonws.ReminderOwnerGoneError
		if !errors.As(err, &ownerGone) {
			t.Fatalf("fire after agent archive commit = %v, want owner gone", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fire did not resume after agent archive committed")
	}
	var definitionStatus, definitionReason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, terminal_reason
		FROM agent_reminder
		WHERE id = $1`, reminderID).Scan(&definitionStatus, &definitionReason); err != nil {
		t.Fatal(err)
	}
	if definitionStatus != "cancelled" || definitionReason != "agent_archived" {
		t.Fatalf("archived owner reminder state=%s/%s, want cancelled/agent_archived", definitionStatus, definitionReason)
	}
	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 0 || receipts != 0 || tasks != 0 || firedEvents != 0 {
		t.Fatalf("archived owner fire counts = occurrence:%d receipt:%d task:%d event:%d, want all zero", occurrences, receipts, tasks, firedEvents)
	}
}

func TestReminderFireFirstSerializesAgainstActiveChannelOnboarding(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	var onboardingID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2 AND status IN ('pending', 'claimed')`,
		fixture.channel.ID, fixture.agentIDs[0]).Scan(&onboardingID); err != nil {
		t.Fatal(err)
	}

	// Hold the agent row so the production fire transaction pauses after taking
	// channel but before membership. The old joined onboarding FOR UPDATE could
	// then own membership while waiting for channel, producing a real cycle.
	agentBlocker, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer agentBlocker.Rollback(context.Background())
	if err := agentBlocker.QueryRow(context.Background(), `SELECT 1 FROM agent WHERE id = $1 FOR UPDATE`, fixture.agentIDs[0]).Scan(new(int)); err != nil {
		t.Fatal(err)
	}

	fireDone := make(chan error, 1)
	go func() { fireDone <- fireReminderAttempt(fixture.handler, reminderID) }()
	waitForReminderChannelRowLock(t, fixture.channel.ID)

	activeDone := make(chan reminderOnboardingResult, 1)
	go func() {
		activeDone <- terminalizeActiveChannelOnboardingForReminderRace(onboardingID, fixture.channel.ID, fixture.agentIDs[0])
	}()
	select {
	case result := <-activeDone:
		t.Fatalf("active onboarding crossed reminder's channel lock: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	if err := agentBlocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fireDone:
		if err != nil {
			t.Fatalf("fire after agent blocker release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reminder fire deadlocked with active channel onboarding")
	}
	select {
	case result := <-activeDone:
		if result.err != nil || !result.active || result.updated != 1 {
			t.Fatalf("active onboarding result = %+v, want active single-row terminalization", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active channel onboarding did not resume after reminder fire committed")
	}

	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1/0/1/1 serialized winner", occurrences, receipts, tasks, firedEvents)
	}
}

func TestActiveChannelOnboardingFirstSerializesAgainstReminderFire(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	var onboardingID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2 AND status IN ('pending', 'claimed')`,
		fixture.channel.ID, fixture.agentIDs[0]).Scan(&onboardingID); err != nil {
		t.Fatal(err)
	}

	agentBlocker, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer agentBlocker.Rollback(context.Background())
	if err := agentBlocker.QueryRow(context.Background(), `SELECT 1 FROM agent WHERE id = $1 FOR UPDATE`, fixture.agentIDs[0]).Scan(new(int)); err != nil {
		t.Fatal(err)
	}

	// The production onboarding writer takes channel first, then pauses on the
	// externally held agent row. Reminder fire must wait at channel rather than
	// acquiring a later eligibility row and forming a reverse cycle.
	activeDone := make(chan reminderOnboardingResult, 1)
	go func() {
		activeDone <- terminalizeActiveChannelOnboardingForReminderRace(onboardingID, fixture.channel.ID, fixture.agentIDs[0])
	}()
	waitForReminderChannelRowLock(t, fixture.channel.ID)

	fireDone := make(chan error, 1)
	go func() { fireDone <- fireReminderAttempt(fixture.handler, reminderID) }()
	select {
	case err := <-fireDone:
		t.Fatalf("reminder fire crossed active onboarding's channel lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := agentBlocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-activeDone:
		if result.err != nil || !result.active || result.updated != 1 {
			t.Fatalf("active onboarding result = %+v, want active single-row terminalization", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active channel onboarding deadlocked before reminder fire")
	}
	select {
	case err := <-fireDone:
		if err != nil {
			t.Fatalf("fire after active onboarding commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reminder fire did not resume after active onboarding committed")
	}

	occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1/0/1/1 serialized winner", occurrences, receipts, tasks, firedEvents)
	}
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire deleted anchor reminder: %v", err)
	}
	var available, receiptNull bool
	var occurrenceID, title, prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.anchor_available, occurrence.receipt_message_id IS NULL,
		       occurrence.id::text, occurrence.title_snapshot, prompt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1`,
		reminderID).Scan(&available, &receiptNull, &occurrenceID, &title, &prompt); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Reminder id: " + reminderID,
		"Occurrence id: " + occurrenceID,
		"Reminder title: " + title,
		"Anchor message: unavailable (deleted).",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("deleted-anchor directed wake prompt missing %q: %q", want, prompt)
		}
	}
	if available || !receiptNull {
		t.Fatalf("deleted anchor available=%v receipt_null=%v prompt=%q", available, receiptNull, prompt)
	}
	historyReq := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil),
		testUserID)
	historyReq = withURLParam(historyReq, "id", fixture.agentIDs[0])
	historyRec := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("list deleted-anchor history: status=%d body=%s", historyRec.Code, historyRec.Body.String())
	}
	var history humanReminderPage
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Occurrences) != 1 || history.Occurrences[0].Anchor.Available ||
		history.Occurrences[0].Anchor.Kind != nil || history.Occurrences[0].Anchor.Display != nil ||
		history.Occurrences[0].Anchor.Href != nil {
		t.Fatalf("deleted-anchor history = %+v, want unavailable without metadata", history.Occurrences)
	}
}

func TestDeletedReminderThreadRootHidesAnchorEverywhere(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	root := fixture.insertMessage(t, "user", testUserID, "root secret anchor", nil)
	insertedReply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", "reply secret anchor", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(root.ID), stringPtr("reminder-deleted-root"), 0)
	if err != nil {
		t.Fatal(err)
	}
	reply := insertedReply.Message
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, reply.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET anchor_thread_root_message_id = $2 WHERE id = $1`, reminderID, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, root.ID); err != nil {
		t.Fatal(err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire deleted-root reminder: %v", err)
	}
	var available, receiptNull bool
	var prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.anchor_available, occurrence.receipt_message_id IS NULL, prompt.content
		FROM agent_reminder_occurrence occurrence
		JOIN chat_message prompt ON prompt.task_id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1`, reminderID).Scan(&available, &receiptNull, &prompt); err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{root.ID, reply.ID, fixture.channel.Name, "root secret anchor", "reply secret anchor"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("deleted root leaked %q in prompt=%q", leaked, prompt)
		}
	}
	if available || !receiptNull || !strings.Contains(prompt, "Anchor message: unavailable") {
		t.Fatalf("deleted root projection available=%v receipt_null=%v prompt=%q", available, receiptNull, prompt)
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

			if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
				t.Fatalf("fire after initiator removal: %v", err)
			}
			occurrences, receipts, tasks, firedEvents := reminderFireCounts(t, reminderID)
			if occurrences != 1 || receipts != 0 || tasks != 1 || firedEvents != 1 {
				t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want 1/0/1/1", occurrences, receipts, tasks, firedEvents)
			}
			var taskInitiatorID, sessionCreatorID string
			if err := testPool.QueryRow(context.Background(), `
				SELECT task.initiator_user_id, session.creator_id
				FROM agent_reminder_occurrence occurrence
				JOIN agent_inbox_event task ON task.id = occurrence.fired_task_id
				JOIN chat_session session ON session.id = task.chat_session_id
				WHERE occurrence.reminder_id = $1`, reminderID).Scan(&taskInitiatorID, &sessionCreatorID); err != nil {
				t.Fatal(err)
			}
			if taskInitiatorID != testUserID || sessionCreatorID != testUserID {
				t.Fatalf("fallback task/session creator = %s/%s, want current agent owner %s", taskInitiatorID, sessionCreatorID, testUserID)
			}
			if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
				t.Fatalf("idempotent retry after initiator removal: %v", err)
			}
			gotOccurrences, gotReceipts, gotTasks, gotEvents := reminderFireCounts(t, reminderID)
			if gotOccurrences != occurrences || gotReceipts != receipts || gotTasks != tasks || gotEvents != firedEvents {
				t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, tasks, firedEvents, gotOccurrences, gotReceipts, gotTasks, gotEvents)
			}
		})
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire archived channel reminder: %v", err)
	}
	var status, reason string
	var tasks int
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, terminal_reason,
		       (SELECT count(*) FROM agent_inbox_event WHERE id IN (
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
	wantDisplay := "#" + fixture.channel.Name
	if definition.Anchor.DisplayName == nil || *definition.Anchor.DisplayName != wantDisplay || definition.Anchor.Display == nil || *definition.Anchor.Display != wantDisplay {
		t.Fatalf("anchor display name = display_name:%v display:%v, want %q", definition.Anchor.DisplayName, definition.Anchor.Display, wantDisplay)
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
	insertedReply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", "thread anchor reply", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(anchor.ID), stringPtr("reminder-test-thread"), 0)
	if err != nil {
		t.Fatal(err)
	}
	reply := insertedReply.Message
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
	wantThreadDisplay := "Thread in #" + fixture.channel.Name
	if threadAnchor.DisplayName == nil || *threadAnchor.DisplayName != wantThreadDisplay || threadAnchor.Display == nil || *threadAnchor.Display != wantThreadDisplay {
		t.Fatalf("thread anchor display name = display_name:%v display:%v, want %q", threadAnchor.DisplayName, threadAnchor.Display, wantThreadDisplay)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET name = '' WHERE id = $1`, fixture.channel.ID); err != nil {
		t.Fatal(err)
	}
	unnamedAnchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
	if unnamedAnchor.DisplayName == nil || *unnamedAnchor.DisplayName != "Thread in # Unnamed channel" {
		t.Fatalf("unnamed channel anchor display name = %+v, want explicit placeholder", unnamedAnchor)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET name = $2 WHERE id = $1`, fixture.channel.ID, fixture.channel.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET kind = 'dm' WHERE id = $1`, fixture.channel.ID); err != nil {
		t.Fatal(err)
	}
	dmAnchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
	dmEncoded, err := json.Marshal(dmAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if dmAnchor.DisplayName == nil || !strings.HasPrefix(*dmAnchor.DisplayName, "Thread in ") || strings.Contains(*dmAnchor.DisplayName, "direct message") || strings.Contains(string(dmEncoded), fixture.channel.Name) {
		t.Fatalf("DM anchor did not expose a readable conversation name without leaking canonical channel name: %s", dmEncoded)
	}
}

func TestListAgentRemindersReturnsManagedPatrolInActiveListWithoutHistory(t *testing.T) {
	t.Skip("retired with channel_member manager-role cutover")
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_member
		SET role = 'manager'
		WHERE member_type = 'agent' AND member_id = $1 AND channel_id = $2
	`, fixture.agentIDs[0], fixture.channel.ID); err != nil {
		t.Fatalf("bind managed group manager: %v", err)
	}
	var reminderID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  fire_at, fired_at, origin_kind, managed_kind, origin_key
		) VALUES (
		  $1, $2, $3, '群巡检', $4::uuid, now() + interval '1 hour',
		  now() - interval '20 minutes', 'group_manager_auto', 'patrol',
		  'patrol:' || ($4::uuid)::text
		)
		RETURNING id
	`, testWorkspaceID, fixture.agentIDs[0], testUserID, fixture.channel.ID).Scan(&reminderID); err != nil {
		t.Fatalf("seed active managed patrol: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, reminderID)
	})
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_reminder_occurrence (
		  reminder_id, workspace_id, agent_id, cadence_scheduled_for, due_at,
		  status, title_snapshot, fired_at
		)
		SELECT id, workspace_id, agent_id, fired_at, fired_at,
		       'fired', title, fired_at
		FROM agent_reminder
		WHERE id = $1
	`, reminderID); err != nil {
		t.Fatalf("seed managed patrol occurrence: %v", err)
	}

	scheduledRequest := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=scheduled", nil)
	scheduledRequest = withURLParam(scheduledRequest, "id", fixture.agentIDs[0])
	scheduledRecorder := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(scheduledRecorder, scheduledRequest)
	if scheduledRecorder.Code != http.StatusOK {
		t.Fatalf("list active managed patrol status=%d body=%s", scheduledRecorder.Code, scheduledRecorder.Body.String())
	}
	var scheduled humanReminderPage
	if err := json.NewDecoder(scheduledRecorder.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	if len(scheduled.Definitions) != 1 || len(scheduled.Occurrences) != 0 {
		t.Fatalf("managed active projection=%+v, want one definition and no history", scheduled)
	}
	definition := scheduled.Definitions[0]
	if definition.ID != reminderID || definition.OriginKind != "group_manager_auto" ||
		definition.ManagedKind == nil || *definition.ManagedKind != "patrol" ||
		definition.LastFireAt == nil || definition.ScheduleKind != "one_shot" {
		t.Fatalf("managed patrol definition=%+v", definition)
	}
	if !definition.Anchor.Available || definition.Anchor.Kind == nil ||
		*definition.Anchor.Kind != "channel" || definition.Anchor.Href == nil ||
		strings.Contains(*definition.Anchor.Href, "?message=") {
		t.Fatalf("channel-only managed patrol anchor=%+v", definition.Anchor)
	}

	historyRequest := newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders?status=fired", nil)
	historyRequest = withURLParam(historyRequest, "id", fixture.agentIDs[0])
	historyRecorder := httptest.NewRecorder()
	fixture.handler.ListAgentReminders(historyRecorder, historyRequest)
	if historyRecorder.Code != http.StatusOK {
		t.Fatalf("list managed patrol history status=%d body=%s", historyRecorder.Code, historyRecorder.Body.String())
	}
	var history humanReminderPage
	if err := json.NewDecoder(historyRecorder.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Occurrences) != 0 {
		t.Fatalf("managed patrol leaked into human history: %+v", history.Occurrences)
	}
}

func TestAgentReminderScheduleRequiresExplicitMessageIDWithoutPromptFallback(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
	agentID := agentIDForTask(t, taskID)
	message, err := testHandler.insertChannelMessage(context.Background(), parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester", "explicit reminder anchor", "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var chatSessionID string
	if err := testPool.QueryRow(context.Background(), `SELECT chat_session_id FROM agent_inbox_event WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'user', $2, $3)`, chatSessionID, "Current message id: "+message.ID, taskID); err != nil {
		t.Fatal(err)
	}

	req := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/schedule", taskID, agentID, map[string]any{
		"title": "must not infer anchor", "delay_seconds": 300,
	})
	rec := httptest.NewRecorder()
	testHandler.AgentTransportScheduleReminder(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "message_id is required") {
		t.Fatalf("missing explicit message_id status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reminders int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_reminder WHERE agent_id = $1`, agentID).Scan(&reminders); err != nil {
		t.Fatal(err)
	}
	if reminders != 0 {
		t.Fatalf("missing explicit message_id created %d reminders, want 0", reminders)
	}
}

type reminderModernTransportFixture struct {
	actorSource      string
	bearerToken      string
	agentID          string
	channelID        string
	initiatorUserID  string
	anchorMessageID  string
	inboxEventID     string
	deliveryID       string
	deliveryLeaseKey string
}

func seedReminderModernTransportFixture(t *testing.T, actorSource string) reminderModernTransportFixture {
	t.Helper()
	ctx := context.Background()
	capabilities := []string{protocol.DaemonCapabilityReminderVersionedCache}
	if actorSource == "agent_credential" {
		capabilities = append(capabilities, protocol.DaemonCapabilityAgentCredentialTransport)
	}
	seedHandlerTestRuntimeCapabilities(t, capabilities)
	seedHandlerTestRuntimeOwner(t, testUserID)

	initiatorUserID := seedWorkspaceUserForTransportTargetTest(t, "reminder_initiator_"+uuid.NewString()[:8])
	if _, err := testPool.Exec(ctx, `UPDATE "user" SET timezone = 'Asia/Shanghai' WHERE id = $1`, initiatorUserID); err != nil {
		t.Fatalf("set reminder initiator timezone: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE "user" SET timezone = 'America/New_York' WHERE id = $1`, testUserID); err != nil {
		t.Fatalf("set runtime owner timezone: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE "user" SET timezone = NULL WHERE id = $1`, testUserID)
	})

	agentName := "Reminder Modern Auth " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "reminder-modern-auth-"+uuid.NewString(), initiatorUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed reminder agent channel member: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("reminder modern auth channel not found")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(initiatorUserID), "Reminder Initiator",
		"[@"+agentName+"](mention://agent/"+agentID+") schedule a reminder",
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("reminder-modern-auth"), 0)
	if err != nil {
		t.Fatalf("insert reminder modern auth trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, trigger, parseUUID(initiatorUserID))
	var targetEventID string
	if err := testPool.QueryRow(ctx, `
		SELECT id
		FROM agent_inbox_event
		WHERE source_message_id = $1
		  AND agent_id = $2
		  AND status = 'pending'
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, trigger.ID, agentID).Scan(&targetEventID); err != nil {
		t.Fatalf("load reminder modern auth inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET created_at = now() - interval '1 day'
		WHERE id = $1`, targetEventID); err != nil {
		t.Fatalf("prioritize reminder modern auth inbox event: %v", err)
	}

	runtimeID := handlerTestRuntimeID(t)
	drainReq := newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil,
		testWorkspaceID, "reminder-modern-auth-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain reminder modern auth inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode reminder modern auth drain: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("reminder modern auth drain events=%d body=%s", len(drainResp.Events), drainRec.Body.String())
	}
	event := drainResp.Events[0]
	if event.ID != targetEventID || event.AgentID != agentID {
		t.Fatalf("drained event=%s agent=%s, want event=%s agent=%s", event.ID, event.AgentID, targetEventID, agentID)
	}
	bearerToken := event.Task.AuthToken
	if actorSource == "agent_inbox_token" {
		if bearerToken == "" {
			t.Fatal("agent inbox fixture did not mint a delivery token")
		}
	} else {
		rawToken, err := auth.GenerateAgentCredentialToken()
		if err != nil {
			t.Fatalf("generate reminder agent credential: %v", err)
		}
		if _, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
			TokenHash:   auth.HashToken(rawToken),
			TokenPrefix: tokenPrefixForTest(rawToken),
			AgentID:     parseUUID(agentID),
			WorkspaceID: parseUUID(testWorkspaceID),
			UserID:      parseUUID(testUserID),
			ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		}); err != nil {
			t.Fatalf("create reminder agent credential: %v", err)
		}
		bearerToken = rawToken
	}

	return reminderModernTransportFixture{
		actorSource:      actorSource,
		bearerToken:      bearerToken,
		agentID:          agentID,
		channelID:        channelID,
		initiatorUserID:  initiatorUserID,
		anchorMessageID:  trigger.ID,
		inboxEventID:     event.ID,
		deliveryID:       event.DeliveryID,
		deliveryLeaseKey: event.LeaseToken,
	}
}

func reminderModernTransportRouter() http.Handler {
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/reminders/schedule", testHandler.AgentTransportScheduleReminder)
		r.Post("/api/agent/reminders/list", testHandler.AgentTransportListReminders)
		r.Post("/api/agent/reminders/update", testHandler.AgentTransportUpdateReminder)
		r.Post("/api/agent/reminders/snooze", testHandler.AgentTransportSnoozeReminder)
		r.Post("/api/agent/reminders/log", testHandler.AgentTransportReminderLog)
		r.Post("/api/agent/reminders/cancel", testHandler.AgentTransportCancelReminder)
	})
	return router
}

func serveReminderModernTransport(t *testing.T, router http.Handler, fixture reminderModernTransportFixture, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(http.MethodPost, path, body)
	req.Header.Set("Authorization", "Bearer "+fixture.bearerToken)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-Agent-Inbox-Event-ID", fixture.inboxEventID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.deliveryID)
	if fixture.actorSource == "agent_credential" {
		req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.deliveryLeaseKey)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func seedManagedPatrolForModernTransport(t *testing.T, fixture reminderModernTransportFixture) string {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member SET role = 'manager'
		WHERE member_type = 'agent' AND member_id = $1 AND channel_id = $2`,
		fixture.agentID, fixture.channelID); err != nil {
		t.Fatalf("bind modern fixture group manager: %v", err)
	}
	issueID := createCommentTriggerPreviewIssue(t, "managed reminder control "+uuid.NewString(), "", "")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, $4)
	`, issueID, testWorkspaceID, fixture.channelID, fixture.anchorMessageID); err != nil {
		t.Fatalf("link modern managed patrol issue: %v", err)
	}
	var reminderID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  anchor_message_id, fire_at, fired_at, origin_kind, managed_kind,
		  origin_key
		) VALUES (
		  $1, $2, $3, '群巡检', $4, $5, now() + interval '1 hour',
		  now() - interval '1 hour', 'group_manager_auto', 'patrol',
		  'patrol:' || ($4::uuid)::text
		)
		RETURNING id`, testWorkspaceID, fixture.agentID, fixture.initiatorUserID,
		fixture.channelID, fixture.anchorMessageID).Scan(&reminderID); err != nil {
		t.Fatalf("seed modern managed patrol: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
		  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
		  next_fire_at, title_snapshot, resulting_state, reason_code
		)
		SELECT id, workspace_id, agent_id, 'scheduled', 'system', agent_id,
		       fire_at, title, 'scheduled', 'test_patrol_seeded'
		FROM agent_reminder
		WHERE id = $1`, reminderID); err != nil {
		t.Fatalf("seed modern managed patrol lifecycle: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE origin_key = 'patrol:' || $1::text`, fixture.channelID)
	})
	return reminderID
}

func setReminderModernTransportInitiator(t *testing.T, fixture reminderModernTransportFixture, userID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_message
		SET author_id = $2
		WHERE id = $1`, fixture.anchorMessageID, userID); err != nil {
		t.Fatalf("change modern transport initiator: %v", err)
	}
}

func TestAgentReminderUpsertPublishesAuthoritativeOwnerBeforeProjection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedReminderModernTransportFixture(t, "agent_inbox_token")
	previousNotifier := testHandler.ReminderNotifier
	notifier := &recordingReminderNotifier{}
	testHandler.ReminderNotifier = notifier
	t.Cleanup(func() { testHandler.ReminderNotifier = previousNotifier })

	scheduleRec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
		"title": "owner before projection", "delay_seconds": 300, "message_id": fixture.anchorMessageID,
	})
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

	if !reflect.DeepEqual(notifier.order, []string{"start", "projection"}) {
		t.Fatalf("reminder notifier order=%v, want [start projection]", notifier.order)
	}
	if len(notifier.starts) != 1 || len(notifier.projections) != 1 {
		t.Fatalf("reminder notifier start/projection=%d/%d", len(notifier.starts), len(notifier.projections))
	}
	start := notifier.starts[0]
	projection := notifier.projections[0]
	if start.AgentID != fixture.agentID || start.RuntimeID != projection.RuntimeID ||
		start.PlacementGeneration < 1 || start.PlacementGeneration != projection.PlacementGeneration ||
		projection.AgentID != fixture.agentID || projection.ReminderID != scheduled.ID || projection.Terminal {
		t.Fatalf("owner/projection mismatch start=%+v projection=%+v", start, projection)
	}
}

func TestReminderNaturalLanguageMutationAuthorizationAndManagedPatrolReEnable(t *testing.T) {
	t.Skip("retired with channel_member manager-role cutover")
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedReminderModernTransportFixture(t, "agent_credential")
	router := reminderModernTransportRouter()
	outsiderID := seedWorkspaceUserForTransportTargetTest(t, "reminder-outsider-"+uuid.NewString())

	t.Run("ordinary reminder belongs to its initiating member", func(t *testing.T) {
		scheduleRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/schedule", map[string]any{
			"title": "initiator only reminder", "delay_seconds": 1800, "message_id": fixture.anchorMessageID,
		})
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

		setReminderModernTransportInitiator(t, fixture, outsiderID)
		for _, path := range []string{
			"/api/agent/reminders/update",
			"/api/agent/reminders/snooze",
			"/api/agent/reminders/cancel",
		} {
			rec := serveReminderModernTransport(t, router, fixture, path, map[string]any{
				"id": scheduled.ID, "delay_seconds": 1800,
			})
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "only the reminder initiator") {
				t.Fatalf("ordinary outsider mutation path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
			}
		}

		setReminderModernTransportInitiator(t, fixture, fixture.initiatorUserID)
		cancelRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/cancel", map[string]any{"id": scheduled.ID})
		if cancelRec.Code != http.StatusOK {
			t.Fatalf("ordinary initiator cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
		}
	})

	t.Run("managed patrol requires group authority and re-enables as one new definition", func(t *testing.T) {
		setReminderModernTransportInitiator(t, fixture, fixture.initiatorUserID)
		reminderID := seedManagedPatrolForModernTransport(t, fixture)
		var originalLastFire time.Time
		if err := testPool.QueryRow(context.Background(), `SELECT fired_at FROM agent_reminder WHERE id = $1`, reminderID).Scan(&originalLastFire); err != nil {
			t.Fatal(err)
		}

		setReminderModernTransportInitiator(t, fixture, outsiderID)
		for _, mutation := range []struct {
			path string
			body map[string]any
		}{
			{path: "/api/agent/reminders/update", body: map[string]any{"id": reminderID, "delay_seconds": 1800}},
			{path: "/api/agent/reminders/snooze", body: map[string]any{"id": reminderID, "delay_seconds": 1800}},
			{path: "/api/agent/reminders/cancel", body: map[string]any{"id": reminderID}},
		} {
			rec := serveReminderModernTransport(t, router, fixture, mutation.path, mutation.body)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "channel creator or workspace owner/admin") {
				t.Fatalf("managed outsider mutation path=%s status=%d body=%s", mutation.path, rec.Code, rec.Body.String())
			}
		}

		setReminderModernTransportInitiator(t, fixture, testUserID)
		for _, delay := range []int{899, 1200, 7200} {
			rec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/update", map[string]any{
				"id": reminderID, "delay_seconds": delay,
			})
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "delay_seconds 900, 1800, 2700, or 3600") {
				t.Fatalf("managed patrol guardrail delay=%d status=%d body=%s", delay, rec.Code, rec.Body.String())
			}
		}
		updateRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/update", map[string]any{
			"id": reminderID, "delay_seconds": 2700,
		})
		if updateRec.Code != http.StatusOK {
			t.Fatalf("managed creator update status=%d body=%s", updateRec.Code, updateRec.Body.String())
		}
		var issueID string
		var selectedStep int16
		if err := testPool.QueryRow(context.Background(), `
			SELECT source.issue_id, reminder.managed_backoff_step
			FROM agent_reminder reminder
			JOIN issue_source_message source
			  ON source.workspace_id = reminder.workspace_id
			 AND source.channel_id = reminder.anchor_channel_id
			WHERE reminder.id = $1
			ORDER BY source.created_at DESC
			LIMIT 1`, reminderID).Scan(&issueID, &selectedStep); err != nil {
			t.Fatal(err)
		}
		if selectedStep != 2 {
			t.Fatalf("managed creator selected step=%d, want 2 for 45m", selectedStep)
		}

		if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'blocked' WHERE id = $1`, issueID); err != nil {
			t.Fatal(err)
		}
		blockedLongRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/snooze", map[string]any{
			"id": reminderID, "delay_seconds": 1800,
		})
		if blockedLongRec.Code != http.StatusOK {
			t.Fatalf("blocked managed patrol long choice status=%d body=%s", blockedLongRec.Code, blockedLongRec.Body.String())
		}
		blockedShortRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/snooze", map[string]any{
			"id": reminderID, "delay_seconds": 900,
		})
		if blockedShortRec.Code != http.StatusOK {
			t.Fatalf("blocked managed patrol 15m choice status=%d body=%s", blockedShortRec.Code, blockedShortRec.Body.String())
		}

		if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'done' WHERE id = $1`, issueID); err != nil {
			t.Fatal(err)
		}
		dormantRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/snooze", map[string]any{
			"id": reminderID, "delay_seconds": 900,
		})
		if dormantRec.Code != http.StatusOK {
			t.Fatalf("message-backed managed patrol choice status=%d body=%s", dormantRec.Code, dormantRec.Body.String())
		}
		if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'todo' WHERE id = $1`, issueID); err != nil {
			t.Fatal(err)
		}

		cancelRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/cancel", map[string]any{"id": reminderID})
		if cancelRec.Code != http.StatusOK {
			t.Fatalf("managed creator cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
		}
		reEnableRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/snooze", map[string]any{
			"id": reminderID, "delay_seconds": 1800,
		})
		if reEnableRec.Code != http.StatusOK {
			t.Fatalf("managed creator re-enable status=%d body=%s", reEnableRec.Code, reEnableRec.Body.String())
		}
		var reEnabled agentReminderResponse
		if err := json.NewDecoder(reEnableRec.Body).Decode(&reEnabled); err != nil {
			t.Fatal(err)
		}
		if reEnabled.ID == reminderID || reEnabled.Status != "scheduled" {
			t.Fatalf("re-enabled patrol=%+v, want new scheduled definition", reEnabled)
		}
		var oldStatus string
		var activeCount, reEnableEvents int
		var reEnabledLastFire time.Time
		if err := testPool.QueryRow(context.Background(), `
			SELECT
			  (SELECT status FROM agent_reminder WHERE id = $1),
			  (
			    SELECT count(*)
			    FROM agent_reminder
			    WHERE origin_key = 'patrol:' || $3::text
			      AND status IN ('scheduled', 'firing')
			  ),
			  (SELECT fired_at FROM agent_reminder WHERE id = $2),
			  (
			    SELECT count(*)
			    FROM agent_reminder_lifecycle_event
			    WHERE reminder_id = $2
			      AND reason_code = 're_enabled_by_natural_language'
			  )
			`, reminderID, reEnabled.ID, fixture.channelID).Scan(
			&oldStatus, &activeCount, &reEnabledLastFire, &reEnableEvents,
		); err != nil {
			t.Fatal(err)
		}
		if oldStatus != "cancelled" || activeCount != 1 || reEnableEvents != 1 ||
			!reEnabledLastFire.Equal(originalLastFire) {
			t.Fatalf("re-enable old=%s active=%d last=%s events=%d",
				oldStatus, activeCount, reEnabledLastFire, reEnableEvents)
		}
	})
}

func TestAgentReminderHandlersAcceptModernAgentTransportSources(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, actorSource := range []string{"agent_inbox_token", "agent_credential"} {
		t.Run(actorSource, func(t *testing.T) {
			fixture := seedReminderModernTransportFixture(t, actorSource)
			router := reminderModernTransportRouter()

			scheduleRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/schedule", map[string]any{
				"title": "modern auth reminder", "repeat": "daily@09:00", "message_id": fixture.anchorMessageID,
			})
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
			var initiatorUserID string
			var activityRows, activityTaskRows int
			if err := testPool.QueryRow(context.Background(), `
				SELECT initiator_user_id
				FROM agent_reminder
				WHERE id = $1`, scheduled.ID).Scan(&initiatorUserID); err != nil {
				t.Fatalf("load reminder initiator: %v", err)
			}
			if initiatorUserID != fixture.initiatorUserID || scheduled.ScheduleTimezone == nil || *scheduled.ScheduleTimezone != "Asia/Shanghai" {
				t.Fatalf("schedule initiator/timezone=%s/%v want=%s/Asia/Shanghai", initiatorUserID, scheduled.ScheduleTimezone, fixture.initiatorUserID)
			}

			listRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/list", map[string]any{"status": "active"})
			if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), scheduled.ID) {
				t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
			}
			updateRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/update", map[string]any{
				"id": scheduled.ID, "cadence": "weekly:mon,fri@10:30",
			})
			if updateRec.Code != http.StatusOK || !strings.Contains(updateRec.Body.String(), "Asia/Shanghai") {
				t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
			}
			snoozeRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/snooze", map[string]any{
				"id": scheduled.ID, "delay_seconds": 300,
			})
			if snoozeRec.Code != http.StatusOK {
				t.Fatalf("snooze status=%d body=%s", snoozeRec.Code, snoozeRec.Body.String())
			}
			logRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/log", map[string]any{"id": scheduled.ID})
			if logRec.Code != http.StatusOK || !strings.Contains(logRec.Body.String(), `"event_type":"snoozed"`) {
				t.Fatalf("log status=%d body=%s", logRec.Code, logRec.Body.String())
			}
			cancelRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/cancel", map[string]any{"id": scheduled.ID})
			if cancelRec.Code != http.StatusOK {
				t.Fatalf("cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
			}

			if err := testPool.QueryRow(context.Background(), `
				SELECT count(*), count(*) FILTER (WHERE task_id IS NOT NULL)
				FROM agent_activity_event
				WHERE agent_id = $1
				  AND details->>'reminder_id' = $2
				  AND event_type IN ('reminder_scheduled', 'reminder_updated', 'reminder_snoozed', 'reminder_cancelled')`,
				fixture.agentID, scheduled.ID).Scan(&activityRows, &activityTaskRows); err != nil {
				t.Fatalf("load reminder activity rows: %v", err)
			}
			if activityRows != 4 || activityTaskRows != 0 {
				t.Fatalf("modern reminder activity rows/task rows=%d/%d, want 4/0", activityRows, activityTaskRows)
			}
		})
	}
}

func TestAgentReminderModernTransportFireFallsBackToOwnerWithoutTimezoneDrift(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, actorSource := range []string{"agent_inbox_token", "agent_credential"} {
		t.Run(actorSource, func(t *testing.T) {
			fixture := seedReminderModernTransportFixture(t, actorSource)
			scheduleRec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
				"title": "modern auth recurring fire", "repeat": "daily@09:00", "message_id": fixture.anchorMessageID,
			})
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

			due := time.Now().UTC().Add(-time.Minute)
			if _, err := testPool.Exec(context.Background(), `
				UPDATE agent_reminder
				SET fire_at = $2, cadence_next_at = $2
				WHERE id = $1`, scheduled.ID, due); err != nil {
				t.Fatalf("make modern reminder due: %v", err)
			}
			tx, err := testPool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(context.Background(), `
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
VALUES ($1, $2, 'user', $3, 'member')
ON CONFLICT (channel_id, member_type, member_id) DO NOTHING`,
				fixture.channelID, testWorkspaceID, testUserID); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatalf("seed replacement channel member: %v", err)
			}
			if _, err := tx.Exec(context.Background(), `
UPDATE channel_member SET role = 'member'
WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
				fixture.channelID, fixture.initiatorUserID); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatalf("demote initiator channel owner: %v", err)
			}
			if _, err := tx.Exec(context.Background(), `
UPDATE channel_member SET role = 'owner'
WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
				fixture.channelID, testUserID); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatalf("promote replacement channel owner: %v", err)
			}
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatalf("transfer channel ownership: %v", err)
			}
			if _, err := testPool.Exec(context.Background(), `
DELETE FROM member
WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, fixture.initiatorUserID); err != nil {
				t.Fatalf("remove reminder initiator membership: %v", err)
			}

			if err := fireReminderAttempt(testHandler, scheduled.ID); err != nil {
				t.Fatalf("fire modern reminder after initiator removal: %v", err)
			}
			var taskInitiatorID, status, cadence, timezone string
			var nextFireAt time.Time
			if err := testPool.QueryRow(context.Background(), `
				SELECT task.initiator_user_id, reminder.status, reminder.cadence,
				       reminder.schedule_timezone, reminder.fire_at
				FROM agent_reminder reminder
				JOIN agent_reminder_occurrence occurrence ON occurrence.reminder_id = reminder.id
				JOIN agent_inbox_event task ON task.id = occurrence.fired_task_id
				WHERE reminder.id = $1`, scheduled.ID).Scan(
				&taskInitiatorID, &status, &cadence, &timezone, &nextFireAt,
			); err != nil {
				t.Fatalf("load modern reminder fire result: %v", err)
			}
			if taskInitiatorID != testUserID {
				t.Fatalf("fallback task creator=%s, want current owner %s", taskInitiatorID, testUserID)
			}
			if status != "scheduled" || cadence != "daily@09:00" || timezone != "Asia/Shanghai" {
				t.Fatalf("recurrence state=%s/%s/%s, want scheduled/daily@09:00/Asia/Shanghai", status, cadence, timezone)
			}
			shanghai, err := time.LoadLocation("Asia/Shanghai")
			if err != nil {
				t.Fatal(err)
			}
			newYork, err := time.LoadLocation("America/New_York")
			if err != nil {
				t.Fatal(err)
			}
			if got := nextFireAt.In(shanghai).Format("15:04"); got != "09:00" {
				t.Fatalf("next fire in locked timezone=%s, want 09:00", got)
			}
			if got := nextFireAt.In(newYork).Format("15:04"); got == "09:00" {
				t.Fatalf("next fire drifted to owner timezone: %s", got)
			}
		})
	}
}

func TestAgentReminderModernTransportSourcesRemainFailClosed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Run("expired delivery", func(t *testing.T) {
		fixture := seedReminderModernTransportFixture(t, "agent_inbox_token")
		if _, err := testPool.Exec(context.Background(), `
			UPDATE agent_event_delivery
			SET lease_expires_at = now() - interval '1 second'
			WHERE id = $1`, fixture.deliveryID); err != nil {
			t.Fatal(err)
		}
		rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
			"title": "must fail", "delay_seconds": 300, "message_id": fixture.anchorMessageID,
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expired delivery status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cross agent event", func(t *testing.T) {
		credential := seedReminderModernTransportFixture(t, "agent_credential")
		other := seedReminderModernTransportFixture(t, "agent_credential")
		credential.inboxEventID = other.inboxEventID
		credential.deliveryID = other.deliveryID
		credential.deliveryLeaseKey = other.deliveryLeaseKey
		rec := serveReminderModernTransport(t, reminderModernTransportRouter(), credential, "/api/agent/reminders/schedule", map[string]any{
			"title": "must fail", "delay_seconds": 300, "message_id": other.anchorMessageID,
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross agent event status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cross origin anchor", func(t *testing.T) {
		fixture := seedReminderModernTransportFixture(t, "agent_credential")
		foreignChannelID := seedChannelForTest(t, "reminder-foreign-origin-"+uuid.NewString(), fixture.initiatorUserID)
		foreignMessage, err := testHandler.insertChannelMessage(context.Background(), parseUUID(foreignChannelID), parseUUID(testWorkspaceID),
			"user", parseUUID(fixture.initiatorUserID), "Reminder Initiator", "wrong origin",
			"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
			"title": "must fail", "delay_seconds": 300, "message_id": foreignMessage.ID,
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross origin status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cross workspace", func(t *testing.T) {
		fixture := seedReminderModernTransportFixture(t, "agent_inbox_token")
		req := newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"})
		req.Header.Set("X-Actor-Source", "agent_inbox_token")
		req.Header.Set("X-Agent-ID", fixture.agentID)
		req.Header.Set("X-Agent-Inbox-Event-ID", fixture.inboxEventID)
		req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.deliveryID)
		req = req.WithContext(middleware.SetMemberContext(req.Context(), uuid.NewString(), db.Member{}))
		rec := httptest.NewRecorder()
		testHandler.AgentTransportListReminders(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross workspace status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestAgentReminderTransportLocksTimezoneAndLogsLifecycle(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
	agentID := agentIDForTask(t, taskID)
	changed := captureReminderChangedEvents(t, testHandler, agentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_inbox_event SET initiator_user_id = $2 WHERE id = $1`, taskID, testUserID); err != nil {
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
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
	agentID := agentIDForTask(t, taskID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_inbox_event SET initiator_user_id = $2 WHERE id = $1`, taskID, testUserID); err != nil {
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
	if err := fireReminderAttempt(testHandler, scheduled.ID); err != nil {
		t.Fatalf("fire converted one-shot: %v", err)
	}
	before := [4]int{}
	before[0], before[1], before[2], before[3] = reminderFireCounts(t, scheduled.ID)
	if before != [4]int{1, 0, 1, 1} {
		t.Fatalf("converted one-shot fire counts=%v, want [1 0 1 1]", before)
	}
	if err := fireReminderAttempt(testHandler, scheduled.ID); err != nil {
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

func TestAgentReminderUpdateRejectsMultipleMutations(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
	agentID := agentIDForTask(t, taskID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_inbox_event SET initiator_user_id = $2 WHERE id = $1`, taskID, testUserID); err != nil {
		t.Fatal(err)
	}
	message, err := testHandler.insertChannelMessage(context.Background(), parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester", "single mutation anchor", "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	scheduleReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/schedule", taskID, agentID, map[string]any{
		"title": "single mutation", "delay_seconds": 300, "message_id": message.ID,
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

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{name: "missing", body: map[string]any{"id": scheduled.ID}},
		{name: "title and delay", body: map[string]any{"id": scheduled.ID, "title": "renamed", "delay_seconds": 300}},
		{name: "title and fire at", body: map[string]any{"id": scheduled.ID, "title": "renamed", "fire_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)}},
		{name: "title and cadence", body: map[string]any{"id": scheduled.ID, "title": "renamed", "cadence": "every:2h"}},
		{name: "delay and fire at", body: map[string]any{"id": scheduled.ID, "delay_seconds": 300, "fire_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)}},
		{name: "delay and cadence", body: map[string]any{"id": scheduled.ID, "delay_seconds": 300, "cadence": "every:2h"}},
		{name: "fire at and cadence", body: map[string]any{"id": scheduled.ID, "fire_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), "cadence": "every:2h"}},
		{name: "empty title and delay", body: map[string]any{"id": scheduled.ID, "title": "", "delay_seconds": 300}},
		{name: "empty title and fire at", body: map[string]any{"id": scheduled.ID, "title": "", "fire_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)}},
		{name: "empty title and cadence", body: map[string]any{"id": scheduled.ID, "title": "", "cadence": "every:2h"}},
		{name: "zero delay and fire at", body: map[string]any{"id": scheduled.ID, "delay_seconds": 0, "fire_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)}},
		{name: "zero delay and cadence", body: map[string]any{"id": scheduled.ID, "delay_seconds": 0, "cadence": "every:2h"}},
		{name: "empty fire at and cadence", body: map[string]any{"id": scheduled.ID, "fire_at": "", "cadence": "every:2h"}},
		{name: "empty title", body: map[string]any{"id": scheduled.ID, "title": ""}},
		{name: "zero delay", body: map[string]any{"id": scheduled.ID, "delay_seconds": 0}},
		{name: "negative delay", body: map[string]any{"id": scheduled.ID, "delay_seconds": -1}},
		{name: "empty fire at", body: map[string]any{"id": scheduled.ID, "fire_at": ""}},
		{name: "invalid fire at", body: map[string]any{"id": scheduled.ID, "fire_at": "not-a-time"}},
		{name: "empty cadence", body: map[string]any{"id": scheduled.ID, "cadence": ""}},
		{name: "invalid cadence", body: map[string]any{"id": scheduled.ID, "cadence": "monthly@09:00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/update", taskID, agentID, tc.body)
			rec := httptest.NewRecorder()
			testHandler.AgentTransportUpdateReminder(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	var title string
	var lifecycleCount int
	if err := testPool.QueryRow(context.Background(), `SELECT title FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1`, scheduled.ID).Scan(&lifecycleCount); err != nil {
		t.Fatal(err)
	}
	if title != "single mutation" || lifecycleCount != 1 {
		t.Fatalf("rejected updates mutated reminder: title=%q lifecycle=%d", title, lifecycleCount)
	}
}
