package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task #53: AgentReadiness previously trusted agent_runtime.status directly
// (rt.Status != "online"), which can read "online" for up to ~180s after the
// runtime actually went silent (sweeper lag). Both real callers
// (shouldSkipDispatch, dispatchRunOnly) would then admit dispatch against a
// runtime that's actually unreachable.
func TestAgentReadiness_StaleHeartbeatIsNotReady(t *testing.T) {
	pool := interactionDAGTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "agent-readiness-test", Slug: "agent-readiness-test", IssuePrefix: "ART",
	})
	require.NoError(t, err)

	var staleRuntimeID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at)
		VALUES ($1, $2, $3, 'local', 'claude', 'online', '', '{}'::jsonb, 'private', now() - interval '10 minutes', now() - interval '9 minutes')
		RETURNING id
	`, ws.ID, "daemon-stale-readiness", "stale-readiness-runtime").Scan(&staleRuntimeID)
	require.NoError(t, err)

	staleAgent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID: ws.ID, Name: "stale-readiness-agent", DisplayName: "Stale Readiness Agent",
		Description: "test", RuntimeMode: "local", RuntimeConfig: []byte("{}"), RuntimeID: staleRuntimeID,
		MaxConcurrentTasks: 1, Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		Model: pgtype.Text{String: "composer-1.5", Valid: true},
	})
	require.NoError(t, err)

	ready, reason, err := AgentReadiness(ctx, q, staleAgent)
	require.NoError(t, err)
	if ready {
		t.Fatalf("stale-heartbeat agent (status column still 'online'): ready = true, want false (must key off heartbeat freshness, not the raw status column); reason=%q", reason)
	}

	var freshRuntimeID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at)
		VALUES ($1, $2, $3, 'local', 'claude', 'online', '', '{}'::jsonb, 'private', now(), now())
		RETURNING id
	`, ws.ID, "daemon-fresh-readiness", "fresh-readiness-runtime").Scan(&freshRuntimeID)
	require.NoError(t, err)

	freshAgent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID: ws.ID, Name: "fresh-readiness-agent", DisplayName: "Fresh Readiness Agent",
		Description: "test", RuntimeMode: "local", RuntimeConfig: []byte("{}"), RuntimeID: freshRuntimeID,
		MaxConcurrentTasks: 1, Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		Model: pgtype.Text{String: "composer-1.5", Valid: true},
	})
	require.NoError(t, err)

	ready, _, err = AgentReadiness(ctx, q, freshAgent)
	require.NoError(t, err)
	if !ready {
		t.Fatalf("fresh-heartbeat agent: ready = false, want true")
	}
}

// TestRuntimeConnectivity_TierBoundaries pins the pure-function tiering
// (extracted from the handler package's original private copy — task #53
// consolidated the two duplicate implementations into this one, since
// AgentReadiness in this package could not import the handler package).
func TestRuntimeConnectivity_TierBoundaries(t *testing.T) {
	now := time.Now()

	fresh := db.AgentRuntime{Status: "online", LastSeenAt: pgtype.Timestamptz{Time: now.Add(-5 * time.Second), Valid: true}}
	if got := RuntimeConnectivity(fresh, now); got != RuntimeConnectivityOnline {
		t.Fatalf("fresh: got %v, want RuntimeConnectivityOnline", got)
	}

	stale := db.AgentRuntime{Status: "online", LastSeenAt: pgtype.Timestamptz{Time: now.Add(-3 * time.Minute), Valid: true}}
	if got := RuntimeConnectivity(stale, now); got != RuntimeConnectivityStale {
		t.Fatalf("stale: got %v, want RuntimeConnectivityStale", got)
	}

	dead := db.AgentRuntime{Status: "online", LastSeenAt: pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true}}
	if got := RuntimeConnectivity(dead, now); got != RuntimeConnectivityDead {
		t.Fatalf("dead: got %v, want RuntimeConnectivityDead", got)
	}
}
