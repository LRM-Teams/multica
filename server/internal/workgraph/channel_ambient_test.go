package workgraph

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTouchChannelAmbientDebouncesAndClaimsAfterDelay(t *testing.T) {
	ctx := t.Context()
	prev := AmbientDebounce
	AmbientDebounce = 50 * time.Millisecond
	t.Cleanup(func() { AmbientDebounce = prev })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy Ambient")

	store := NewStore(testPool)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("touch ambient: %v", err)
	}

	watches, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim before debounce: %v", err)
	}
	if len(watches) != 0 {
		t.Fatalf("claimed %d before debounce, want 0", len(watches))
	}

	time.Sleep(80 * time.Millisecond)
	watches, token, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after debounce: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("claimed %d after debounce, want 1", len(watches))
	}
	if !watches[0].ChannelID.Valid || watches[0].ChannelID != channelID {
		t.Fatalf("claimed channel = %v, want %v", watches[0].ChannelID, channelID)
	}

	if err := store.MarkChannelAmbientReviewed(ctx, channelID, token, watches[0].LastHumanMessageAt, pgtype.UUID{}); err != nil {
		t.Fatalf("mark reviewed: %v", err)
	}

	watches, _, err = store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after review: %v", err)
	}
	if len(watches) != 0 {
		t.Fatalf("claimed %d after clean review, want 0", len(watches))
	}
}

func TestTouchChannelAmbientResetsDebounceOnNewMessage(t *testing.T) {
	ctx := t.Context()
	prev := AmbientDebounce
	AmbientDebounce = 100 * time.Millisecond
	t.Cleanup(func() { AmbientDebounce = prev })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy Ambient Reset")

	store := NewStore(testPool)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("first touch: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("second touch: %v", err)
	}

	watches, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim mid debounce: %v", err)
	}
	if len(watches) != 0 {
		t.Fatalf("claimed %d while debounce reset, want 0", len(watches))
	}

	time.Sleep(120 * time.Millisecond)
	watches, _, err = store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after reset debounce: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("claimed %d after reset debounce, want 1", len(watches))
	}
}

func createWorkgraphAgent(t *testing.T, ctx context.Context, workspaceID, agentID pgtype.UUID, name string) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		WITH runtime AS (
			INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider)
			VALUES ($1, $2, 'local', 'test')
			RETURNING id
		)
		INSERT INTO agent (id, workspace_id, name, runtime_mode, runtime_config, runtime_id)
		SELECT $3, $1, $4, 'local', '{}'::jsonb, runtime.id
		FROM runtime
	`, workspaceID, name+" runtime", agentID, name); err != nil {
		t.Fatalf("create agent %q: %v", name, err)
	}
}

// #1 debounce starvation: continuous chatter < debounce apart must still become
// due once the max-staleness cap is reached. Uses past timestamps so it is
// deterministic (no sleeps racing the debounce).
func TestTouchChannelAmbientMaxWaitCapForcesReview(t *testing.T) {
	ctx := t.Context()
	prevD, prevM := AmbientDebounce, AmbientMaxWait
	AmbientDebounce = 2 * time.Minute
	AmbientMaxWait = 3 * time.Minute
	t.Cleanup(func() { AmbientDebounce = prevD; AmbientMaxWait = prevM })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy MaxWait")

	store := NewStore(testPool)
	// Streak starts 5 min ago; chatter every minute (< 2 min debounce).
	base := time.Now().Add(-5 * time.Minute)
	for i := 0; i <= 4; i++ {
		if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
	}
	// Without the cap review_not_before would be (base+4m)+2m = now+1m (not due).
	// With the cap it is base+maxWait = base+3m = now-2m (due).
	watches, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("claimed %d, want 1 (max-staleness cap must force the busy channel due)", len(watches))
	}
}

// #2 a running review keeps its claim (no duplicate review) and a successful
// completion clears dirty precisely.
func TestChannelAmbientRunningThenReconcileSuccessClears(t *testing.T) {
	ctx := t.Context()
	prev := AmbientDebounce
	AmbientDebounce = 20 * time.Millisecond
	t.Cleanup(func() { AmbientDebounce = prev })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy Running")

	store := NewStore(testPool)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("touch: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	watches, token, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil || len(watches) != 1 {
		t.Fatalf("claim: watches=%d err=%v", len(watches), err)
	}
	runID := pgUUID(uuid.New())
	if err := store.MarkChannelAmbientRunning(ctx, channelID, token, watches[0].LastHumanMessageAt, runID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	// A running review must not be re-claimed (no duplicate ambient reviews).
	again, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim while running: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("claimed %d while running, want 0", len(again))
	}
	// Successful completion clears dirty.
	if err := store.ReconcileChannelAmbientRun(ctx, runID, true); err != nil {
		t.Fatalf("reconcile success: %v", err)
	}
	assertAmbientDirty(t, ctx, channelID, false)
	done, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after success: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("claimed %d after successful review, want 0", len(done))
	}
}

// #2 a failed review must not be lost: dirty stays set and the row is re-claimed
// after the retry backoff (last_reviewed is never advanced by a failure).
func TestChannelAmbientReconcileFailureReArms(t *testing.T) {
	ctx := t.Context()
	prevD, prevB := AmbientDebounce, AmbientRetryBackoff
	AmbientDebounce = 20 * time.Millisecond
	AmbientRetryBackoff = 20 * time.Millisecond
	t.Cleanup(func() { AmbientDebounce = prevD; AmbientRetryBackoff = prevB })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy Fail")

	store := NewStore(testPool)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("touch: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	watches, token, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil || len(watches) != 1 {
		t.Fatalf("claim: watches=%d err=%v", len(watches), err)
	}
	runID := pgUUID(uuid.New())
	if err := store.MarkChannelAmbientRunning(ctx, channelID, token, watches[0].LastHumanMessageAt, runID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := store.ReconcileChannelAmbientRun(ctx, runID, false); err != nil {
		t.Fatalf("reconcile failure: %v", err)
	}
	assertAmbientDirty(t, ctx, channelID, true)
	time.Sleep(40 * time.Millisecond)
	retry, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after failure: %v", err)
	}
	if len(retry) != 1 {
		t.Fatalf("claimed %d after failed review, want 1 (review must be retried)", len(retry))
	}
}

// #2 a review whose run never completes (daemon gone) is re-armed by the stale
// reclaim so it is not lost forever.
func TestReclaimStaleChannelAmbientReArms(t *testing.T) {
	ctx := t.Context()
	prevD, prevS := AmbientDebounce, AmbientClaimStaleAfter
	AmbientDebounce = 20 * time.Millisecond
	AmbientClaimStaleAfter = 30 * time.Millisecond
	t.Cleanup(func() { AmbientDebounce = prevD; AmbientClaimStaleAfter = prevS })

	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	wendyID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)
	createWorkgraphAgent(t, ctx, workspaceID, wendyID, "Wendy Stale")

	store := NewStore(testPool)
	if err := store.TouchChannelAmbient(ctx, workspaceID, channelID, wendyID, pgtype.UUID{}, time.Now()); err != nil {
		t.Fatalf("touch: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	watches, token, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil || len(watches) != 1 {
		t.Fatalf("claim: watches=%d err=%v", len(watches), err)
	}
	runID := pgUUID(uuid.New())
	if err := store.MarkChannelAmbientRunning(ctx, channelID, token, watches[0].LastHumanMessageAt, runID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // exceed AmbientClaimStaleAfter
	reclaimed, err := store.ReclaimStaleChannelAmbient(ctx)
	if err != nil {
		t.Fatalf("reclaim stale: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed %d stale rows, want 1", reclaimed)
	}
	retry, _, err := store.ClaimDueChannelAmbient(ctx, 10)
	if err != nil {
		t.Fatalf("claim after reclaim: %v", err)
	}
	if len(retry) != 1 {
		t.Fatalf("claimed %d after stale reclaim, want 1", len(retry))
	}
}

func assertAmbientDirty(t *testing.T, ctx context.Context, channelID pgtype.UUID, want bool) {
	t.Helper()
	var dirty bool
	if err := testPool.QueryRow(ctx, `SELECT dirty FROM wendy_channel_ambient WHERE channel_id = $1`, channelID).Scan(&dirty); err != nil {
		t.Fatalf("read dirty: %v", err)
	}
	if dirty != want {
		t.Fatalf("dirty = %v, want %v", dirty, want)
	}
}
