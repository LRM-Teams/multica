package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func testDaemonUpdateObservation(sessionID string, revision int64) protocol.DaemonUpdateObservation {
	return protocol.DaemonUpdateObservation{
		SessionID:                  sessionID,
		Revision:                   revision,
		ObservedAt:                 time.Date(2026, 7, 27, 0, 0, int(revision), 0, time.UTC).Format(time.RFC3339Nano),
		AutoUpdateEffectiveEnabled: true,
		ConfigSource:               "env_enabled",
		CheckIntervalSeconds:       21600,
		Phase:                      "waiting",
		LastOutcome:                "never_checked",
	}
}

func TestDaemonUpdateObservationSessionRevisionMonotonicity(t *testing.T) {
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "update-observation-" + uuid.NewString()
	sessionOne := uuid.NewString()
	sessionTwo := uuid.NewString()
	t.Cleanup(func() {
		if err := testHandler.Queries.DeleteDaemonUpdateStatus(ctx, db.DeleteDaemonUpdateStatusParams{
			WorkspaceID: workspaceID,
			DaemonID:    daemonID,
		}); err != nil {
			t.Errorf("cleanup daemon update status: %v", err)
		}
	})

	first := testDaemonUpdateObservation(sessionOne, 1)
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &first); err != nil {
		t.Fatalf("register first session: %v", err)
	}
	registered, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load registered status: %v", err)
	}
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &first); err != nil {
		t.Fatalf("retry identical register: %v", err)
	}
	registeredRetry, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load retried register: %v", err)
	}
	if !registeredRetry.UpdatedAt.Time.Equal(registered.UpdatedAt.Time) {
		t.Fatalf("identical register wrote status: before=%s after=%s", registered.UpdatedAt.Time, registeredRetry.UpdatedAt.Time)
	}

	runtime := db.AgentRuntime{
		ID:          parseUUID(uuid.NewString()),
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
	}
	second := testDaemonUpdateObservation(sessionOne, 2)
	second.Phase = "checking"
	second.AttemptSource = "auto"
	second.LastAttemptAt = "2026-07-27T00:00:02Z"
	testHandler.advanceDaemonUpdateObservation(ctx, runtime, &second)

	stored, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load revision two: %v", err)
	}
	if stored.Revision != 2 || stored.Phase != "checking" {
		t.Fatalf("stored revision two = %+v", stored)
	}
	updatedAt := stored.UpdatedAt.Time
	payloadHash := stored.PayloadHash

	// Duplicate same revision and payload is a zero-write retry.
	testHandler.advanceDaemonUpdateObservation(ctx, runtime, &second)
	duplicate, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load duplicate: %v", err)
	}
	if !duplicate.UpdatedAt.Time.Equal(updatedAt) || duplicate.PayloadHash != payloadHash {
		t.Fatalf("duplicate heartbeat wrote status: before=%s after=%s", updatedAt, duplicate.UpdatedAt.Time)
	}

	// Lower revision, conflicting same revision, and old session are all
	// status-only rejects; none may regress the stored projection.
	stale := testDaemonUpdateObservation(sessionOne, 1)
	testHandler.advanceDaemonUpdateObservation(ctx, runtime, &stale)
	conflict := second
	conflict.Phase = "updating"
	testHandler.advanceDaemonUpdateObservation(ctx, runtime, &conflict)
	oldSession := testDaemonUpdateObservation(uuid.NewString(), 99)
	testHandler.advanceDaemonUpdateObservation(ctx, runtime, &oldSession)
	unchanged, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load unchanged status: %v", err)
	}
	if unchanged.Revision != 2 || unchanged.Phase != "checking" || unchanged.PayloadHash != payloadHash {
		t.Fatalf("out-of-order heartbeat changed status = %+v", unchanged)
	}

	// Authenticated register is the session-adoption boundary.
	successor := testDaemonUpdateObservation(sessionTwo, 1)
	successor.LastOutcome = "interrupted"
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &successor); err != nil {
		t.Fatalf("adopt successor session: %v", err)
	}
	adopted, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("load adopted status: %v", err)
	}
	if uuidToString(adopted.SessionID) != sessionTwo || adopted.Revision != 1 || adopted.LastOutcome != "interrupted" {
		t.Fatalf("adopted successor = %+v", adopted)
	}

	// A downgraded/old daemon clears the projection during register.
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, nil); err != nil {
		t.Fatalf("clear old-daemon projection: %v", err)
	}
	if _, err := testHandler.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	}); err != pgx.ErrNoRows {
		t.Fatalf("status after old-daemon register err = %v, want pgx.ErrNoRows", err)
	}
}

func TestDaemonUpdateObservationRegisterRejectsSameRevisionConflict(t *testing.T) {
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "update-observation-conflict-" + uuid.NewString()
	sessionID := uuid.NewString()
	t.Cleanup(func() {
		_ = testHandler.Queries.DeleteDaemonUpdateStatus(ctx, db.DeleteDaemonUpdateStatusParams{
			WorkspaceID: workspaceID,
			DaemonID:    daemonID,
		})
	})

	first := testDaemonUpdateObservation(sessionID, 1)
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &first); err != nil {
		t.Fatalf("register first payload: %v", err)
	}
	conflict := first
	conflict.Phase = "updating"
	if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &conflict); err != errDaemonUpdateObservationConflict {
		t.Fatalf("same-revision conflict err = %v", err)
	}
}

func TestNormalizeDaemonUpdateObservationRejectsUnknownErrorCode(t *testing.T) {
	observation := testDaemonUpdateObservation(uuid.NewString(), 1)
	observation.LastOutcome = "update_failed"
	observation.ErrorCode = "future_unreviewed_error"
	if _, err := normalizeDaemonUpdateObservation(observation); err == nil {
		t.Fatal("accepted an unknown error code")
	}
}

func TestDaemonUpdateObservationRegisterAcceptsCurrentDaemonOutcomes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	for _, tc := range []struct {
		name   string
		adjust func(*protocol.DaemonUpdateObservation)
	}{
		{
			name: "explicit only",
			adjust: func(observation *protocol.DaemonUpdateObservation) {
				observation.AutoUpdateEffectiveEnabled = false
				observation.ConfigSource = "deprecated_noop"
				observation.IneligibleReason = "explicit_only"
				observation.Phase = "disabled"
				observation.LastOutcome = "explicit_only"
			},
		},
		{
			name: "detect only update available",
			adjust: func(observation *protocol.DaemonUpdateObservation) {
				observation.AutoUpdateEffectiveEnabled = false
				observation.ConfigSource = "auto_detect"
				observation.IneligibleReason = ""
				observation.Phase = "waiting"
				observation.LastOutcome = "update_available"
				observation.TargetVersion = "v0.4.24-alpha.11"
			},
		},
		{
			name: "pinned",
			adjust: func(observation *protocol.DaemonUpdateObservation) {
				observation.LastOutcome = "pinned"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemonID := "update-observation-current-outcome-" + uuid.NewString()
			t.Cleanup(func() {
				_ = testHandler.Queries.DeleteDaemonUpdateStatus(ctx, db.DeleteDaemonUpdateStatusParams{
					WorkspaceID: workspaceID,
					DaemonID:    daemonID,
				})
			})
			observation := testDaemonUpdateObservation(uuid.NewString(), 1)
			tc.adjust(&observation)
			if err := testHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &observation); err != nil {
				t.Fatalf("register observation: %v", err)
			}
		})
	}
}

func TestDeleteDaemonUpdateStatusIfOrphanPreservesSharedDaemonUntilLastRuntime(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	queries := db.New(tx)
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "update-observation-orphan-" + uuid.NewString()

	var runtimeID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata
		)
		VALUES ($1, $2, 'Update status orphan test', 'local', 'test', 'offline', '', '{}'::jsonb)
		RETURNING id
	`, workspaceID, daemonID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO daemon_update_status (
			workspace_id, daemon_id, session_id, revision, observed_at,
			auto_update_effective_enabled, config_source,
			check_interval_seconds, phase, last_outcome, payload_hash
		)
		VALUES ($1, $2, $3, 1, now(), true, 'env_enabled', 21600, 'waiting',
			'never_checked', repeat('a', 64))
	`, workspaceID, daemonID, parseUUID(uuid.NewString())); err != nil {
		t.Fatalf("insert update status: %v", err)
	}
	deleteArgs := db.DeleteDaemonUpdateStatusIfOrphanParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	}
	if err := queries.DeleteDaemonUpdateStatusIfOrphan(ctx, deleteArgs); err != nil {
		t.Fatalf("delete while runtime exists: %v", err)
	}
	if _, err := queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams(deleteArgs)); err != nil {
		t.Fatalf("status removed while runtime exists: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("delete runtime: %v", err)
	}
	if err := queries.DeleteDaemonUpdateStatusIfOrphan(ctx, deleteArgs); err != nil {
		t.Fatalf("delete orphan status: %v", err)
	}
	if _, err := queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams(deleteArgs)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("status after last runtime deletion err = %v, want pgx.ErrNoRows", err)
	}
}

func TestDaemonUpdateStatusRegisterSerializesWithLastRuntimeCleanup(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "update-observation-register-delete-race-" + uuid.NewString()
	runtimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")

	registrationTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin registration transaction: %v", err)
	}
	defer registrationTx.Rollback(context.Background())
	if err := lockDaemonRegistration(ctx, registrationTx, testWorkspaceID, daemonID); err != nil {
		t.Fatalf("lock registration transaction: %v", err)
	}
	var lockClassID, lockObjectID int64
	var lockObjectSubID int32
	if err := registrationTx.QueryRow(ctx, `
		SELECT classid::bigint, objid::bigint, objsubid::int
		FROM pg_locks
		WHERE pid = pg_backend_pid() AND locktype = 'advisory' AND granted
	`).Scan(&lockClassID, &lockObjectID, &lockObjectSubID); err != nil {
		t.Fatalf("inspect registration advisory lock: %v", err)
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		req := newRequest(http.MethodDelete, "/api/runtimes/"+runtimeID, nil)
		req = withURLParam(req, "runtimeId", runtimeID)
		testHandler.DeleteAgentRuntime(recorder, req)
		deleteDone <- recorder
	}()

	waitDeadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case recorder := <-deleteDone:
			t.Fatalf("last-runtime cleanup crossed registration lock: status=%d body=%s", recorder.Code, recorder.Body.String())
		default:
		}
		var waiterCount int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_locks
			WHERE locktype = 'advisory'
			  AND NOT granted
			  AND classid::bigint = $1
			  AND objid::bigint = $2
			  AND objsubid::int = $3
		`, lockClassID, lockObjectID, lockObjectSubID).Scan(&waiterCount); err != nil {
			t.Fatalf("inspect last-runtime cleanup advisory waiter: %v", err)
		}
		if waiterCount > 0 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("last-runtime cleanup never waited on the registration advisory lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	registrationHandler := *testHandler
	registrationHandler.Queries = testHandler.Queries.WithTx(registrationTx)
	registrationHandler.DB = registrationTx
	observation := testDaemonUpdateObservation(uuid.NewString(), 1)
	if err := registrationHandler.registerDaemonUpdateObservation(ctx, workspaceID, daemonID, &observation); err != nil {
		t.Fatalf("adopt status in registration transaction: %v", err)
	}
	if err := registrationTx.Commit(ctx); err != nil {
		t.Fatalf("commit registration transaction: %v", err)
	}

	recorder := <-deleteDone
	if recorder.Code != http.StatusOK {
		t.Fatalf("last-runtime cleanup after registration commit: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var runtimeCount, statusCount int
	if err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM agent_runtime WHERE id = $1),
			(SELECT count(*) FROM daemon_update_status WHERE workspace_id = $2 AND daemon_id = $3)
	`, runtimeID, testWorkspaceID, daemonID).Scan(&runtimeCount, &statusCount); err != nil {
		t.Fatalf("inspect serialized register/delete result: %v", err)
	}
	if runtimeCount != 0 || statusCount != 0 {
		t.Fatalf("serialized last-runtime cleanup left split truth: runtime=%d status=%d", runtimeCount, statusCount)
	}
}
