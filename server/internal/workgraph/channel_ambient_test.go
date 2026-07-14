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
