package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func reminderFireCounts(t *testing.T, reminderID string) (occurrences, receipts, deliveries, firedEvents int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM agent_reminder_occurrence WHERE reminder_id = $1),
		  (SELECT count(*) FROM channel_message WHERE external_message_id IN (
		     SELECT 'reminder_occurrence:' || id::text
		     FROM agent_reminder_occurrence WHERE reminder_id = $1
		  )),
		  (SELECT count(*)
		   FROM agent_message_delivery delivery
		   JOIN channel_message message ON message.id = delivery.message_id
		   WHERE message.external_message_id IN (
		     SELECT 'reminder_occurrence:' || id::text
		     FROM agent_reminder_occurrence WHERE reminder_id = $1
		   )),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND event_type = 'fired')`,
		reminderID).Scan(&occurrences, &receipts, &deliveries, &firedEvents); err != nil {
		t.Fatalf("load reminder fire counts: %v", err)
	}
	return
}

type capturedReminderOwnerInput struct {
	runtimeID string
	payload   protocol.ReminderOwnerInputPayload
}

type capturedReminderOwnerInputNotifier struct {
	mu     sync.Mutex
	calls  []capturedReminderOwnerInput
	result bool
}

func (n *capturedReminderOwnerInputNotifier) NotifyReminderOwnerInput(runtimeID string, payload protocol.ReminderOwnerInputPayload) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, capturedReminderOwnerInput{runtimeID: runtimeID, payload: payload})
	return n.result
}

func (n *capturedReminderOwnerInputNotifier) snapshot() []capturedReminderOwnerInput {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]capturedReminderOwnerInput(nil), n.calls...)
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
	var channelMessagesBefore, authorInboxBefore, otherInboxBefore, ownerDeliveriesBefore int
	if err := testPool.QueryRow(context.Background(),
		`SELECT
		  (SELECT count(*) FROM channel_message WHERE channel_id = $1),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $3),
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $2)`,
		fixture.channel.ID, fixture.agentIDs[0], fixture.agentIDs[1]).Scan(&channelMessagesBefore, &authorInboxBefore, &otherInboxBefore, &ownerDeliveriesBefore); err != nil {
		t.Fatal(err)
	}
	notifier := &capturedReminderOwnerInputNotifier{}
	previousNotifier := fixture.handler.ReminderOwnerInputNotifier
	fixture.handler.ReminderOwnerInputNotifier = notifier
	t.Cleanup(func() { fixture.handler.ReminderOwnerInputNotifier = previousNotifier })
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

	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d delivery:%d event:%d, want 1/0/0/1", occurrences, receipts, deliveries, firedEvents)
	}
	var channelMessagesAfter, authorInboxAfter, otherInboxAfter, ownerDeliveriesAfter, otherDeliveries int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_message WHERE channel_id = $1),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $3),
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $2),
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $3)`,
		fixture.channel.ID, fixture.agentIDs[0], fixture.agentIDs[1]).Scan(&channelMessagesAfter, &authorInboxAfter, &otherInboxAfter, &ownerDeliveriesAfter, &otherDeliveries); err != nil {
		t.Fatal(err)
	}
	publishedMu.Lock()
	gotPublishedChannelMessages := publishedChannelMessages
	publishedMu.Unlock()
	if channelMessagesAfter != channelMessagesBefore || gotPublishedChannelMessages != 0 || authorInboxAfter != authorInboxBefore || otherInboxAfter != otherInboxBefore || ownerDeliveriesAfter != ownerDeliveriesBefore || otherDeliveries != 0 {
		t.Fatalf("hard-cut fire channel_messages=%d→%d broadcasts=%d author_inbox=%d→%d other_inbox=%d→%d owner_deliveries=%d→%d other_deliveries=%d, want no changes",
			channelMessagesBefore, channelMessagesAfter, gotPublishedChannelMessages, authorInboxBefore, authorInboxAfter, otherInboxBefore, otherInboxAfter, ownerDeliveriesBefore, ownerDeliveriesAfter, otherDeliveries)
	}
	var occurrenceID, title, status string
	var fireVersion int64
	var scheduledFor, dueAt time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT occurrence.id::text, occurrence.title_snapshot, occurrence.status,
		       occurrence.fire_version, occurrence.cadence_scheduled_for, occurrence.due_at
		FROM agent_reminder_occurrence occurrence
		WHERE occurrence.reminder_id = $1`,
		reminderID).Scan(&occurrenceID, &title, &status, &fireVersion, &scheduledFor, &dueAt); err != nil {
		t.Fatal(err)
	}
	if status != "fired" || fireVersion != 1 {
		t.Fatalf("fired occurrence status=%q fire_version=%d, want fired/1", status, fireVersion)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 {
		t.Fatalf("transient owner input calls=%d, want 1", len(calls))
	}
	call := calls[0]
	if call.runtimeID != fixture.runtimeIDs[0] || call.payload.WorkspaceID != testWorkspaceID ||
		call.payload.AgentID != fixture.agentIDs[0] || call.payload.RuntimeID != fixture.runtimeIDs[0] ||
		call.payload.ReminderID != reminderID || call.payload.Version != fireVersion || call.payload.Title != title {
		t.Fatalf("transient owner input identity=%+v runtime=%q", call.payload, call.runtimeID)
	}
	if !call.payload.Anchor.Available || call.payload.Anchor.ChannelID != fixture.channel.ID ||
		call.payload.Anchor.MessageID != anchor.ID || call.payload.Anchor.ThreadRootMessageID != "" ||
		call.payload.Anchor.Target != "channel:"+fixture.channel.ID || call.payload.Anchor.ReplyTarget != "#"+fixture.channel.Name ||
		call.payload.Anchor.Excerpt != "anchor" {
		t.Fatalf("transient owner input anchor=%+v", call.payload.Anchor)
	}
	if call.payload.Occurrence.OccurrenceID != occurrenceID ||
		call.payload.Occurrence.ScheduledFor != scheduledFor.UTC().Format(time.RFC3339Nano) ||
		call.payload.Occurrence.DueAt != dueAt.UTC().Format(time.RFC3339Nano) ||
		call.payload.Occurrence.Cadence != "" || call.payload.Occurrence.Timezone != "" {
		t.Fatalf("transient owner input occurrence=%+v", call.payload.Occurrence)
	}
	var reminderStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, reminderID).Scan(&reminderStatus); err != nil || reminderStatus != "fired" {
		t.Fatalf("one-shot status = %q err=%v, want fired", reminderStatus, err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	gotOccurrences, gotReceipts, gotDeliveries, gotEvents := reminderFireCounts(t, reminderID)
	if gotOccurrences != occurrences || gotReceipts != receipts || gotDeliveries != deliveries || gotEvents != firedEvents {
		t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, deliveries, firedEvents, gotOccurrences, gotReceipts, gotDeliveries, gotEvents)
	}
	if got := len(notifier.snapshot()); got != 1 {
		t.Fatalf("retry duplicated transient owner input: calls=%d want 1", got)
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

func TestReminderOwnerInputTransportFailureIsFinalAfterCommit(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	notifier := &capturedReminderOwnerInputNotifier{result: false}
	fixture.handler.ReminderOwnerInputNotifier = notifier

	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire with rejected transient transport: %v", err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("retry committed fire: %v", err)
	}
	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("rejected transport fire counts=%d/%d/%d/%d want 1/0/0/1", occurrences, receipts, deliveries, firedEvents)
	}
	if got := len(notifier.snapshot()); got != 1 {
		t.Fatalf("rejected transport attempts=%d want 1", got)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status); err != nil || status != "fired" {
		t.Fatalf("rejected transport definition status=%q err=%v want fired", status, err)
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
	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("recovered reminder history=%d/%d/%d/%d, want 1/0/0/1", occurrences, receipts, deliveries, firedEvents)
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
	notifier := &capturedReminderOwnerInputNotifier{result: true}
	fixture.handler.ReminderOwnerInputNotifier = notifier
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
	if got := len(notifier.snapshot()); got != 0 {
		t.Fatalf("invalid attempts created %d transient owner inputs", got)
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
	if got := len(notifier.snapshot()); got != 0 {
		t.Fatalf("cancelled attempt created %d transient owner inputs", got)
	}
}

func TestReminderFireRequiresTransientOwnerInputCapability(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime
		SET metadata = '{"capabilities":["reminder_versioned_cache_v1"]}'::jsonb
		WHERE id = $1`, fixture.runtimeIDs[0]); err != nil {
		t.Fatal(err)
	}
	notifier := &capturedReminderOwnerInputNotifier{result: true}
	fixture.handler.ReminderOwnerInputNotifier = notifier

	err := fireReminderAttempt(fixture.handler, reminderID)
	if !errors.Is(err, errReminderDaemonOutdated) {
		t.Fatalf("incompatible daemon fire error=%v want daemon_outdated", err)
	}
	if occurrences, receipts, deliveries, fired := reminderFireCounts(t, reminderID); occurrences+receipts+deliveries+fired != 0 {
		t.Fatalf("incompatible daemon mutated fire state: %d/%d/%d/%d", occurrences, receipts, deliveries, fired)
	}
	if got := len(notifier.snapshot()); got != 0 {
		t.Fatalf("incompatible daemon received %d owner inputs", got)
	}
}

func TestReminderMutationAndFireRaceHasOneVersionFencedWinner(t *testing.T) {
	for _, mutation := range []string{"update", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
			anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
			reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
			notifier := &capturedReminderOwnerInputNotifier{result: true}
			fixture.handler.ReminderOwnerInputNotifier = notifier

			var version, placementGeneration int64
			if err := testPool.QueryRow(context.Background(), `SELECT version FROM agent_reminder WHERE id = $1`, reminderID).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, fixture.agentIDs[0]).Scan(&placementGeneration); err != nil {
				t.Fatal(err)
			}
			identity := daemonws.ClientIdentity{WorkspaceID: testWorkspaceID, RuntimeIDs: []string{fixture.runtimeIDs[0]}}
			payload := protocol.ReminderFireAttemptPayload{
				AgentID:             fixture.agentIDs[0],
				RuntimeID:           fixture.runtimeIDs[0],
				PlacementGeneration: placementGeneration,
				ReminderID:          reminderID,
				Version:             version,
				FiredAtClient:       time.Now().UTC().Format(time.RFC3339Nano),
			}

			start := make(chan struct{})
			fireErr := make(chan error, 1)
			mutationResult := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				<-start
				_, err := fixture.handler.HandleDaemonReminderFireAttempt(context.Background(), identity, payload)
				fireErr <- err
			}()
			go func() {
				<-start
				var req *http.Request
				if mutation == "update" {
					req = agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/update", fixture.agentIDs[0], map[string]any{
						"id": reminderID, "delay_seconds": 300,
					})
				} else {
					req = agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/cancel", fixture.agentIDs[0], map[string]any{"id": reminderID})
				}
				rec := httptest.NewRecorder()
				if mutation == "update" {
					fixture.handler.AgentTransportUpdateReminder(rec, req)
				} else {
					fixture.handler.AgentTransportCancelReminder(rec, req)
				}
				mutationResult <- rec
			}()
			close(start)
			if err := <-fireErr; err != nil {
				t.Fatalf("fire race error: %v", err)
			}
			rec := <-mutationResult

			occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
			var mutationEvents int
			mutationEventType := "updated"
			if mutation == "cancel" {
				mutationEventType = "cancelled"
			}
			if err := testPool.QueryRow(context.Background(), `
				SELECT count(*) FROM agent_reminder_lifecycle_event
				WHERE reminder_id = $1 AND event_type = $2`, reminderID, mutationEventType).Scan(&mutationEvents); err != nil {
				t.Fatal(err)
			}
			calls := len(notifier.snapshot())
			switch {
			case rec.Code == http.StatusOK:
				if occurrences != 0 || firedEvents != 0 || mutationEvents != 1 || calls != 0 {
					t.Fatalf("%s won but fire state=%d/%d mutation_events=%d inputs=%d", mutation, occurrences, firedEvents, mutationEvents, calls)
				}
			case rec.Code == http.StatusConflict:
				if occurrences != 1 || firedEvents != 1 || mutationEvents != 0 || calls != 1 {
					t.Fatalf("fire won over %s but state=%d/%d mutation_events=%d inputs=%d", mutation, occurrences, firedEvents, mutationEvents, calls)
				}
			default:
				t.Fatalf("%s race status=%d body=%s", mutation, rec.Code, rec.Body.String())
			}
			if receipts != 0 || deliveries != 0 {
				t.Fatalf("%s race created receipt/delivery=%d/%d", mutation, receipts, deliveries)
			}
		})
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

	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d delivery:%d event:%d, want 1/0/0/1 serialized winner", occurrences, receipts, deliveries, firedEvents)
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

	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("fire counts = occurrence:%d receipt:%d delivery:%d event:%d, want 1/0/0/1 serialized winner", occurrences, receipts, deliveries, firedEvents)
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

func TestRecurringReminderOfflineGapCollapsesToOneOccurrenceAndFirstFutureSlot(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "every:1h", "")
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier

	// Five ideal hourly slots elapsed while the owner was offline. The durable
	// definition still represents one current due version, not five occurrences.
	missedSlot := time.Now().UTC().Add(-5*time.Hour - 10*time.Minute)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder
		SET fire_at = $2, cadence_next_at = $2
		WHERE id = $1`, reminderID, missedSlot); err != nil {
		t.Fatal(err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire recovered overdue version: %v", err)
	}

	var status string
	var nextFire, nextCadence time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, fire_at, cadence_next_at
		FROM agent_reminder
		WHERE id = $1`, reminderID).Scan(&status, &nextFire, &nextCadence); err != nil {
		t.Fatal(err)
	}
	want := missedSlot.Add(time.Hour)
	for !want.After(time.Now().UTC()) {
		want = want.Add(time.Hour)
	}
	if status != "scheduled" || !nextFire.Equal(nextCadence) || !nextFire.Equal(want) {
		t.Fatalf("offline-gap advance status=%s fire=%s cadence=%s want=%s", status, nextFire, nextCadence, want)
	}
	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("offline-gap counts=%d/%d/%d/%d want 1/0/0/1", occurrences, receipts, deliveries, firedEvents)
	}
	if calls := notifier.snapshot(); len(calls) != 1 || calls[0].payload.Version != 1 {
		t.Fatalf("offline-gap transient attempts=%+v want one for version 1", calls)
	}
}

func TestRecurringReminderFiresEveryOccurrenceWithoutQuota(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "every:15m", "")
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier

	const fires = 4
	for i := 0; i < fires; i++ {
		if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET fire_at = now() - interval '5 seconds' WHERE id = $1`, reminderID); err != nil {
			t.Fatalf("re-arm fire #%d: %v", i+1, err)
		}
		if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
			t.Fatalf("fire #%d: %v", i+1, err)
		}
	}

	occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if occurrences != fires {
		t.Fatalf("occurrences=%d want %d", occurrences, fires)
	}
	if receipts != 0 {
		t.Fatalf("visible receipts=%d want 0", receipts)
	}
	if deliveries != 0 {
		t.Fatalf("deliveries=%d want 0", deliveries)
	}
	if firedEvents != fires {
		t.Fatalf("fired lifecycle events=%d want %d", firedEvents, fires)
	}
	if calls := notifier.snapshot(); len(calls) != fires {
		t.Fatalf("transient owner input calls=%d want %d", len(calls), fires)
	}

	// The cadence reminder must remain scheduled (still advancing), never cancelled.
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "scheduled" {
		t.Fatalf("reminder status=%s want scheduled", status)
	}

	var distinctVersions, quotaReasons int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(DISTINCT fire_version) FROM agent_reminder_occurrence WHERE reminder_id = $1),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND reason_code = 'quota_coalesced')`, reminderID).Scan(&distinctVersions, &quotaReasons); err != nil {
		t.Fatal(err)
	}
	if distinctVersions != fires || quotaReasons != 0 {
		t.Fatalf("distinct fire versions=%d quota reasons=%d, want %d/0", distinctVersions, quotaReasons, fires)
	}
}

func TestDeletedReminderAnchorFiresWithUnavailableMarker(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier
	if _, err := testPool.Exec(context.Background(), `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, anchor.ID); err != nil {
		t.Fatal(err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire deleted anchor reminder: %v", err)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 || calls[0].payload.ReminderID != reminderID || calls[0].payload.Anchor != (protocol.ReminderOwnerInputAnchor{}) {
		t.Fatalf("deleted-anchor transient input=%+v, want one input with unavailable anchor and no metadata", calls)
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
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(root.ID), stringPtr("reminder-deleted-root"), 0, channelMessageKindHint{})
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
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire deleted-root reminder: %v", err)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 || calls[0].payload.Anchor != (protocol.ReminderOwnerInputAnchor{}) {
		t.Fatalf("deleted root transient input=%+v, want unavailable anchor with no metadata", calls)
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

func TestReminderOwnerInputKeepsThreadAnchorAsPrivateReturnSurface(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	root := fixture.insertMessage(t, "user", testUserID, "root", nil)
	insertedReply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", "reply anchor", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(root.ID), stringPtr("reminder-thread-anchor"), 0, channelMessageKindHint{})
	if err != nil {
		t.Fatal(err)
	}
	reply := insertedReply.Message
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, reply.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET anchor_thread_root_message_id = $2 WHERE id = $1`, reminderID, root.ID); err != nil {
		t.Fatal(err)
	}
	notifier := &capturedReminderOwnerInputNotifier{result: true}
	fixture.handler.ReminderOwnerInputNotifier = notifier

	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire threaded reminder: %v", err)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 {
		t.Fatalf("threaded owner input calls=%d want 1", len(calls))
	}
	anchor := calls[0].payload.Anchor
	wantReplyTarget := "#" + fixture.channel.Name + ":" + root.ID[:8]
	if !anchor.Available || anchor.ChannelID != fixture.channel.ID || anchor.MessageID != reply.ID ||
		anchor.ThreadRootMessageID != root.ID || anchor.Target != "thread:"+root.ID ||
		anchor.ReplyTarget != wantReplyTarget || anchor.Excerpt != "reply anchor" {
		t.Fatalf("threaded owner input anchor=%+v want target=%q reply_target=%q", anchor, "thread:"+root.ID, wantReplyTarget)
	}
	_, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if receipts != 0 || deliveries != 0 || firedEvents != 1 {
		t.Fatalf("threaded fire receipt/delivery/lifecycle=%d/%d/%d want 0/0/1", receipts, deliveries, firedEvents)
	}
}

func TestReminderFireDoesNotDependOnInitiatorWhenItIsGone(t *testing.T) {
	for _, mode := range []string{"membership_removed", "user_deleted"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
			anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
			notifier := &capturedReminderOwnerInputNotifier{}
			fixture.handler.ReminderOwnerInputNotifier = notifier
			initiatorID := seedWorkspaceUserForTransportTargetTest(t, "reminder-initiator-"+mode+"-"+uuid.NewString())
			reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
			if _, err := testPool.Exec(context.Background(), `UPDATE agent_reminder SET initiator_user_id = $2 WHERE id = $1`, reminderID, initiatorID); err != nil {
				t.Fatal(err)
			}

			switch mode {
			case "membership_removed":
				if _, err := testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, initiatorID); err != nil {
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
			occurrences, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
			if occurrences != 1 || receipts != 0 || deliveries != 0 || firedEvents != 1 {
				t.Fatalf("fire counts = occurrence:%d receipt:%d delivery:%d event:%d, want 1/0/0/1", occurrences, receipts, deliveries, firedEvents)
			}
			calls := notifier.snapshot()
			if len(calls) != 1 || calls[0].payload.AgentID != fixture.agentIDs[0] || calls[0].payload.Anchor.Target != "channel:"+fixture.channel.ID {
				t.Fatalf("reminder owner input=%+v, want author %s channel target", calls, fixture.agentIDs[0])
			}
			if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
				t.Fatalf("idempotent retry after initiator removal: %v", err)
			}
			gotOccurrences, gotReceipts, gotDeliveries, gotEvents := reminderFireCounts(t, reminderID)
			if gotOccurrences != occurrences || gotReceipts != receipts || gotDeliveries != deliveries || gotEvents != firedEvents {
				t.Fatalf("retry duplicated fire: before=%d/%d/%d/%d after=%d/%d/%d/%d", occurrences, receipts, deliveries, firedEvents, gotOccurrences, gotReceipts, gotDeliveries, gotEvents)
			}
			if got := len(notifier.snapshot()); got != 1 {
				t.Fatalf("retry duplicated owner input: calls=%d want 1", got)
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
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, terminal_reason
		FROM agent_reminder WHERE id = $1`, reminderID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	_, receipts, deliveries, firedEvents := reminderFireCounts(t, reminderID)
	if status != "cancelled" || reason != "channel_archived" || deliveries != 0 {
		t.Fatalf("terminal state=%s reason=%s deliveries=%d", status, reason, deliveries)
	}
	if receipts != 0 || firedEvents != 0 {
		t.Fatalf("terminalized Reminder receipts=%d fired_events=%d, want 0/0", receipts, firedEvents)
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

func TestHumanReminderAnchorRequiresEligibleOwnerAgent(t *testing.T) {
	for _, mode := range []string{"membership_removed", "agent_archived"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
			anchor := fixture.insertMessage(t, "user", testUserID, "protected anchor", nil)
			reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
			reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "membership_removed":
				if _, err := testPool.Exec(context.Background(), `
					DELETE FROM channel_member
					WHERE channel_id = $1 AND workspace_id = $2
					  AND member_type = 'agent' AND member_id = $3`,
					fixture.channel.ID, testWorkspaceID, fixture.agentIDs[0]); err != nil {
					t.Fatal(err)
				}
			case "agent_archived":
				if _, err := testPool.Exec(context.Background(), `UPDATE agent SET archived_at = now() WHERE id = $1`, fixture.agentIDs[0]); err != nil {
					t.Fatal(err)
				}
			}
			request := withChannelTestWorkspaceCtx(t,
				newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil), testUserID)
			got := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `{"available":false}` {
				t.Fatalf("ineligible owner leaked anchor metadata: %s", encoded)
			}
		})
	}
}

func TestReminderThreadAnchorRequiresExactStoredReplyRoot(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	rootA := fixture.insertMessage(t, "user", testUserID, "root a", nil)
	rootB := fixture.insertMessage(t, "system", testUserID, "root b", nil)
	insertedReply, err := insertChannelMessageWithPartsExec(context.Background(), testPool,
		parseUUID(fixture.channel.ID), parseUUID(testWorkspaceID), "agent", parseUUID(fixture.agentIDs[0]),
		"Reminder Agent", "reply secret", nil, "multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(rootA.ID), stringPtr("reminder-root-mismatch"), 0, channelMessageKindHint{})
	if err != nil {
		t.Fatal(err)
	}
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, insertedReply.Message.ID, "", "")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_reminder SET anchor_thread_root_message_id = $2 WHERE id = $1`, reminderID, rootB.ID); err != nil {
		t.Fatal(err)
	}
	reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	request := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil), testUserID)
	if got := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder); got != (humanReminderAnchor{Available: false}) {
		t.Fatalf("mismatched thread root human anchor=%+v want unavailable", got)
	}
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire mismatched-root reminder: %v", err)
	}
	if calls := notifier.snapshot(); len(calls) != 1 || calls[0].payload.Anchor != (protocol.ReminderOwnerInputAnchor{}) {
		t.Fatalf("mismatched thread root transient input=%+v want unavailable anchor", calls)
	}
}

func TestReminderAuthorizedAnchorSupportsCanonicalAuthorTypes(t *testing.T) {
	for _, authorType := range []string{"user", "agent", "system"} {
		t.Run(authorType, func(t *testing.T) {
			fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
			authorID := ""
			switch authorType {
			case "user":
				authorID = testUserID
			case "agent":
				authorID = fixture.agentIDs[0]
			}
			anchor := fixture.insertMessage(t, authorType, authorID, authorType+" anchor", nil)
			reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
			reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
			if err != nil {
				t.Fatal(err)
			}
			request := withChannelTestWorkspaceCtx(t,
				newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil), testUserID)
			if got := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder); !got.Available || got.Href == nil {
				t.Fatalf("%s human anchor=%+v want available", authorType, got)
			}
			notifier := &capturedReminderOwnerInputNotifier{}
			fixture.handler.ReminderOwnerInputNotifier = notifier
			if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
				t.Fatalf("fire %s-authored anchor: %v", authorType, err)
			}
			calls := notifier.snapshot()
			if len(calls) != 1 || !calls[0].payload.Anchor.Available || calls[0].payload.Anchor.MessageID != anchor.ID ||
				calls[0].payload.Anchor.Excerpt != authorType+" anchor" {
				t.Fatalf("%s transient anchor=%+v", authorType, calls)
			}
		})
	}
}

func TestReminderAuthorizedAnchorPreservesDMReturnSurface(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET kind = 'dm' WHERE id = $1`, fixture.channel.ID); err != nil {
		t.Fatal(err)
	}
	anchor := fixture.insertMessage(t, "user", testUserID, "dm anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	reminder, err := scanAgentReminder(testPool.QueryRow(context.Background(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if err != nil {
		t.Fatal(err)
	}
	request := withChannelTestWorkspaceCtx(t,
		newRequest(http.MethodGet, "/api/agents/"+fixture.agentIDs[0]+"/reminders", nil), testUserID)
	humanAnchor := fixture.handler.safeHumanReminderAnchor(request, testUserID, reminder)
	if !humanAnchor.Available || humanAnchor.Display == nil || strings.TrimSpace(*humanAnchor.Display) == "" || strings.Contains(*humanAnchor.Display, fixture.channel.Name) {
		t.Fatalf("DM human anchor=%+v want safe peer display", humanAnchor)
	}
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire DM anchor: %v", err)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 || calls[0].payload.Anchor.Target != "channel:"+fixture.channel.ID ||
		!strings.HasPrefix(calls[0].payload.Anchor.ReplyTarget, "dm:@") || calls[0].payload.Anchor.Excerpt != "dm anchor" {
		t.Fatalf("DM transient anchor=%+v", calls)
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
		pgtype.UUID{}, pgtype.UUID{}, nil, parseUUID(anchor.ID), stringPtr("reminder-test-thread"), 0, channelMessageKindHint{})
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

func TestAgentReminderScheduleRequiresExplicitMessageIDWithoutPromptFallback(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
		protocol.DaemonCapabilityReminderTransientInput,
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

	req := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/schedule", agentID, map[string]any{
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
	bearerToken     string
	agentID         string
	channelID       string
	initiatorUserID string
	anchorMessageID string
}

func seedReminderModernTransportFixture(t *testing.T) reminderModernTransportFixture {
	t.Helper()
	ctx := context.Background()
	seedHandlerTestRuntimeCapabilities(t, []string{
		protocol.DaemonCapabilityReminderVersionedCache,
		protocol.DaemonCapabilityReminderTransientInput,
		protocol.DaemonCapabilityAgentCredentialTransport,
	})
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(initiatorUserID), "Reminder Initiator",
		"[@"+agentName+"](mention://agent/"+agentID+") schedule a reminder",
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("reminder-modern-auth"), 0)
	if err != nil {
		t.Fatalf("insert reminder modern auth trigger: %v", err)
	}
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

	return reminderModernTransportFixture{
		bearerToken:     rawToken,
		agentID:         agentID,
		channelID:       channelID,
		initiatorUserID: initiatorUserID,
		anchorMessageID: trigger.ID,
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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAgentReminderScheduleAllowsTwentySixActiveReminders(t *testing.T) {
	fixture := seedReminderModernTransportFixture(t)
	router := reminderModernTransportRouter()
	for i := 1; i <= 26; i++ {
		rec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/schedule", map[string]any{
			"title":         fmt.Sprintf("active reminder %d", i),
			"delay_seconds": 300 + i,
			"message_id":    fixture.anchorMessageID,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("schedule active reminder %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	var active int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_reminder
		WHERE agent_id = $1 AND status IN ('scheduled', 'firing')`, fixture.agentID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 26 {
		t.Fatalf("active reminders=%d want 26", active)
	}
}

func agentCredentialReminderRequest(t *testing.T, method, path, agentID string, body any) *http.Request {
	t.Helper()
	req := withChatTestWorkspaceCtx(t, newRequest(method, path, body))
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", agentID)
	return req
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
	fixture := seedReminderModernTransportFixture(t)
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

func TestAgentReminderHandlersUseAgentCredentialTransport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedReminderModernTransportFixture(t)
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
	if err := testPool.QueryRow(context.Background(), `
				SELECT initiator_user_id
				FROM agent_reminder
				WHERE id = $1`, scheduled.ID).Scan(&initiatorUserID); err != nil {
		t.Fatalf("load reminder initiator: %v", err)
	}
	if initiatorUserID != fixture.initiatorUserID || scheduled.ScheduleTimezone == nil || *scheduled.ScheduleTimezone != "UTC" {
		t.Fatalf("schedule initiator/timezone=%s/%v want=%s/UTC", initiatorUserID, scheduled.ScheduleTimezone, fixture.initiatorUserID)
	}

	listRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/list", map[string]any{"status": "active"})
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), scheduled.ID) {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	updateRec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/update", map[string]any{
		"id": scheduled.ID, "cadence": "weekly:mon,fri@10:30",
	})
	if updateRec.Code != http.StatusOK || !strings.Contains(updateRec.Body.String(), "UTC") {
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

}

func TestAgentReminderCredentialFirePreservesTimezoneWithoutInitiatorDependency(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedReminderModernTransportFixture(t)
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
	var status, cadence, timezone string
	var nextFireAt time.Time
	if err := testPool.QueryRow(context.Background(), `
				SELECT status, cadence, schedule_timezone, fire_at
				FROM agent_reminder
				WHERE id = $1`, scheduled.ID).Scan(
		&status, &cadence, &timezone, &nextFireAt,
	); err != nil {
		t.Fatalf("load modern reminder fire result: %v", err)
	}
	if status != "scheduled" || cadence != "daily@09:00" || timezone != "UTC" {
		t.Fatalf("recurrence state=%s/%s/%s, want scheduled/daily@09:00/UTC", status, cadence, timezone)
	}
	// P0: calendar locks UTC, not initiator/owner viewing timezone.
	if got := nextFireAt.UTC().Format("15:04"); got != "09:00" {
		t.Fatalf("next fire in locked UTC=%s, want 09:00", got)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := nextFireAt.In(newYork).Format("15:04"); got == "09:00" {
		t.Fatalf("next fire drifted to owner viewing timezone: %s", got)
	}
}

func TestAgentReminderCredentialTransportRejectsLegacyContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedReminderModernTransportFixture(t)
	router := reminderModernTransportRouter()
	t.Run("task token is rejected", func(t *testing.T) {
		req := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"}))
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", fixture.agentID)
		rec := httptest.NewRecorder()
		testHandler.AgentTransportListReminders(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("task-token reminder list status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("credential with inbox headers is rejected", func(t *testing.T) {
		req := newRequest(http.MethodPost, "/api/agent/reminders/schedule", map[string]any{
			"title": "must fail", "delay_seconds": 300, "message_id": fixture.anchorMessageID,
		})
		req.Header.Set("Authorization", "Bearer "+fixture.bearerToken)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		req.Header.Set("X-Agent-Inbox-Event-ID", uuid.NewString())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("credential inbox context status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("another agent anchor is denied without membership", func(t *testing.T) {
		other := seedReminderModernTransportFixture(t)
		rec := serveReminderModernTransport(t, router, fixture, "/api/agent/reminders/schedule", map[string]any{
			"title": "must fail", "delay_seconds": 300, "message_id": other.anchorMessageID,
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-agent anchor status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cross origin anchor without membership", func(t *testing.T) {
		// Product lock B: message may live outside wake origin, but agent
		// must be a member of the *anchor* channel. No membership → 403
		// (message exists; access denied), not 404.
		fixture := seedReminderModernTransportFixture(t)
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
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not a member of the anchor channel") {
			t.Fatalf("cross origin without membership status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cross origin anchor with membership", func(t *testing.T) {
		// 贝克汉姆 shape: wake on channel A, schedule against msg-id on channel B
		// where the agent is already a member → 201 and anchor_channel_id = B.
		fixture := seedReminderModernTransportFixture(t)
		otherChannelID := seedChannelForTest(t, "reminder-cross-member-"+uuid.NewString(), fixture.initiatorUserID)
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role, join_source, added_by_type)
			VALUES ($1, $2, 'agent', $3, 'manager', 'manual', 'system')
			ON CONFLICT DO NOTHING`, otherChannelID, testWorkspaceID, fixture.agentID); err != nil {
			t.Fatal(err)
		}
		otherMessage, err := testHandler.insertChannelMessage(context.Background(), parseUUID(otherChannelID), parseUUID(testWorkspaceID),
			"user", parseUUID(fixture.initiatorUserID), "Reminder Initiator", "cross channel patrol anchor",
			"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
			"title": "cross channel patrol", "delay_seconds": 300, "message_id": otherMessage.ID,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("cross origin with membership status=%d body=%s", rec.Code, rec.Body.String())
		}
		var scheduled agentReminderResponse
		if err := json.NewDecoder(rec.Body).Decode(&scheduled); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
		})
		if scheduled.AnchorChannelID != otherChannelID {
			t.Fatalf("anchor_channel_id=%q want other channel %s", scheduled.AnchorChannelID, otherChannelID)
		}
		if scheduled.AnchorMessageID == nil || *scheduled.AnchorMessageID != otherMessage.ID {
			t.Fatalf("anchor_message_id=%v want %s", scheduled.AnchorMessageID, otherMessage.ID)
		}
	})
	t.Run("credential workspace binding ignores a forged header", func(t *testing.T) {
		fixture := seedReminderModernTransportFixture(t)
		req := newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"})
		req.Header.Set("Authorization", "Bearer "+fixture.bearerToken)
		req.Header.Set("X-Workspace-ID", uuid.NewString())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("credential-bound workspace list status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestAgentReminderScheduleFallsBackToAgentOwnerWithoutHumanWakeAuthor(t *testing.T) {
	// P1: agent-authored anchor still schedules (no 403). Initiator may be
	// filled as optional audit (owner) but is not required for success.
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fixture := seedReminderModernTransportFixture(t)

	// Agent-authored *anchor* message (patrol re-anchor on own prior post).
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_message
		SET author_type = 'agent', author_id = $2, author_name = 'patrol agent'
		WHERE id = $1`, fixture.anchorMessageID, fixture.agentID); err != nil {
		t.Fatalf("agent-author anchor message: %v", err)
	}
	// Ensure agent owner is a current workspace member (testUserID is runtime owner).
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET owner_id = $2 WHERE id = $1`, fixture.agentID, testUserID); err != nil {
		t.Fatalf("set agent owner: %v", err)
	}

	rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
		"title": "patrol re-anchor every hour", "message_id": fixture.anchorMessageID, "repeat": "every:1h",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("schedule without human wake author status=%d body=%s", rec.Code, rec.Body.String())
	}
	var scheduled agentReminderResponse
	if err := json.NewDecoder(rec.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
	})
	var initiator string
	if err := testPool.QueryRow(ctx, `SELECT initiator_user_id::text FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&initiator); err != nil {
		t.Fatal(err)
	}
	if initiator != testUserID {
		t.Fatalf("initiator_user_id=%s want agent owner %s", initiator, testUserID)
	}
}

func TestAgentReminderScheduleInitiatorFromHumanAnchorMessage(t *testing.T) {
	// L2 long-term: human-authored anchor → initiator = that human, not agent.owner.
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fixture := seedReminderModernTransportFixture(t)

	// Force owner ≠ anchor human so the assertion is not accidental equality.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET owner_id = $2 WHERE id = $1`, fixture.agentID, testUserID); err != nil {
		t.Fatalf("set agent owner: %v", err)
	}
	// Anchor remains the fixture human trigger (author_type=user, author_id=initiatorUserID).
	if fixture.initiatorUserID == testUserID {
		t.Fatal("fixture initiator must differ from runtime owner for this test")
	}

	rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
		"title": "human anchored patrol", "message_id": fixture.anchorMessageID, "repeat": "every:1h",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("schedule with human anchor status=%d body=%s", rec.Code, rec.Body.String())
	}
	var scheduled agentReminderResponse
	if err := json.NewDecoder(rec.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
	})
	var initiator string
	if err := testPool.QueryRow(ctx, `SELECT initiator_user_id::text FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&initiator); err != nil {
		t.Fatal(err)
	}
	if initiator != fixture.initiatorUserID {
		t.Fatalf("initiator_user_id=%s want anchor human %s (not owner %s)", initiator, fixture.initiatorUserID, testUserID)
	}
}

func TestAgentReminderScheduleCalendarTimezoneIgnoresUserViewingTimezone(t *testing.T) {
	// P0: daily@ uses UTC (or locked schedule_timezone), never user.timezone viewing pref.
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fixture := seedReminderModernTransportFixture(t)
	// Fixture sets initiator user timezone Asia/Shanghai and owner America/New_York.
	// Calendar schedule must still lock UTC when no explicit schedule tz is passed.
	rec := serveReminderModernTransport(t, reminderModernTransportRouter(), fixture, "/api/agent/reminders/schedule", map[string]any{
		"title": "daily patrol", "message_id": fixture.anchorMessageID, "repeat": "daily@09:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("daily schedule status=%d body=%s", rec.Code, rec.Body.String())
	}
	var scheduled agentReminderResponse
	if err := json.NewDecoder(rec.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_reminder WHERE id = $1`, scheduled.ID)
	})
	var tz *string
	if err := testPool.QueryRow(ctx, `SELECT schedule_timezone FROM agent_reminder WHERE id = $1`, scheduled.ID).Scan(&tz); err != nil {
		t.Fatal(err)
	}
	if tz == nil || *tz != "UTC" {
		t.Fatalf("schedule_timezone=%v want UTC (must not use user viewing timezone Asia/Shanghai)", tz)
	}
}

func TestReminderScheduleTimezoneExplicitAndInvalid(t *testing.T) {
	if got := reminderScheduleTimezone(""); got != "UTC" {
		t.Fatalf("empty -> %q", got)
	}
	if got := reminderScheduleTimezone("Asia/Shanghai"); got != "Asia/Shanghai" {
		t.Fatalf("valid -> %q", got)
	}
	if got := reminderScheduleTimezone("Not/A/Zone"); got != "UTC" {
		t.Fatalf("invalid -> %q", got)
	}
}

func TestAgentReminderTransportLocksTimezoneAndLogsLifecycle(t *testing.T) {
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
		protocol.DaemonCapabilityReminderTransientInput,
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

	scheduleReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/schedule", agentID, map[string]any{
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
	if scheduled.Cadence == nil || *scheduled.Cadence != "daily@09:00" || scheduled.ScheduleTimezone == nil || *scheduled.ScheduleTimezone != "UTC" {
		t.Fatalf("schedule did not lock UTC calendar timezone: %+v", scheduled)
	}

	// Later initiator viewing-timezone changes cannot reinterpret an existing
	// calendar definition, including when its cadence is replaced.
	if _, err := testPool.Exec(context.Background(), `UPDATE "user" SET timezone = 'America/New_York' WHERE id = $1`, testUserID); err != nil {
		t.Fatal(err)
	}
	updateReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/update", agentID, map[string]any{
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
	if updated.ScheduleTimezone == nil || *updated.ScheduleTimezone != "UTC" {
		t.Fatalf("cadence update drifted locked timezone: %+v", updated)
	}
	for _, transition := range []struct {
		cadence     string
		wantVisible *string
	}{
		{cadence: "every:2h"},
		{cadence: "daily@08:15", wantVisible: stringPtr("UTC")},
	} {
		req := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/update", agentID, map[string]any{"id": scheduled.ID, "cadence": transition.cadence})
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

	snoozeReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/snooze", agentID, map[string]any{"id": scheduled.ID, "delay_seconds": 300})
	snoozeRec := httptest.NewRecorder()
	testHandler.AgentTransportSnoozeReminder(snoozeRec, snoozeReq)
	if snoozeRec.Code != http.StatusOK {
		t.Fatalf("snooze status=%d body=%s", snoozeRec.Code, snoozeRec.Body.String())
	}

	cancelReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/cancel", agentID, map[string]any{"id": scheduled.ID})
	cancelRec := httptest.NewRecorder()
	testHandler.AgentTransportCancelReminder(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	logReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/log", agentID, map[string]any{"id": scheduled.ID})
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
		protocol.DaemonCapabilityReminderTransientInput,
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

	scheduleReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/schedule", agentID, map[string]any{
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
		req := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/update", agentID, body)
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
	if cadence.Valid || cadenceNextAt.Valid || !timezone.Valid || timezone.String != "UTC" {
		t.Fatalf("hidden timezone lock cadence=%v timezone=%v cadence_next_at=%v", cadence, timezone, cadenceNextAt)
	}

	recurring := updateSchedule(map[string]any{"id": scheduled.ID, "cadence": "daily@08:15"})
	if recurring.Cadence == nil || *recurring.Cadence != "daily@08:15" || recurring.ScheduleTimezone == nil || *recurring.ScheduleTimezone != "UTC" {
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
	if before != [4]int{1, 0, 0, 1} {
		t.Fatalf("converted one-shot fire counts=%v, want [1 0 0 1]", before)
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
		protocol.DaemonCapabilityReminderTransientInput,
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
	scheduleReq := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/schedule", agentID, map[string]any{
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
			req := agentCredentialReminderRequest(t, http.MethodPost, "/api/agent/reminders/update", agentID, tc.body)
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
