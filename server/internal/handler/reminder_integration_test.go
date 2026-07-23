package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	gotOccurrences, gotReceipts, gotTasks, gotEvents := reminderFireCounts(t, reminderID)
	if gotOccurrences != occurrences || gotReceipts != receipts || gotTasks != tasks || gotEvents != firedEvents {
		t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, tasks, firedEvents, gotOccurrences, gotReceipts, gotTasks, gotEvents)
	}
}

func TestFireReminderOccurrenceFailsClosedWhenExistingTaskIsNonTerminal(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")

	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'queued', 0)
		RETURNING id`, fixture.agentIDs[0], fixture.runtimeIDs[0]).Scan(&taskID); err != nil {
		t.Fatalf("seed impossible reminder task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
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
	if occurrences != 1 || receipts != 1 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want one serialized winner each", occurrences, receipts, tasks, firedEvents)
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
	if occurrences != 1 || receipts != 1 || tasks != 1 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d task:%d event:%d, want one serialized winner each", occurrences, receipts, tasks, firedEvents)
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
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
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

			if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
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
	if err := testPool.QueryRow(context.Background(), `SELECT chat_session_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
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

func TestAgentReminderTransportLocksTimezoneAndLogsLifecycle(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
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
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
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
	if err := fireReminderAttempt(testHandler, scheduled.ID); err != nil {
		t.Fatalf("fire converted one-shot: %v", err)
	}
	before := [4]int{}
	before[0], before[1], before[2], before[3] = reminderFireCounts(t, scheduled.ID)
	if before != [4]int{1, 1, 1, 1} {
		t.Fatalf("converted one-shot fire counts=%v, want [1 1 1 1]", before)
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
