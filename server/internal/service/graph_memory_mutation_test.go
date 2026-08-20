package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Nil pool fails closed with the spec's graph_store_unavailable marker.
func TestGraphMutationCoordinatorNilPool(t *testing.T) {
	c := NewGraphMutationCoordinator(nil)
	err := c.WithGraphLock(context.Background(), "ws", "project", "p1", func(ctx context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "graph_store_unavailable") {
		t.Fatalf("nil pool err = %v", err)
	}
}

// --- Advisory-lock shape against a real Postgres (skipped without DATABASE_URL) ---

func graphMutationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return pool
}

// Same-graph contenders serialize on the advisory lock; a different graph
// is not blocked; fn errors propagate and roll back.
func TestGraphMutationCoordinatorLockShape(t *testing.T) {
	pool := graphMutationTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	c := NewGraphMutationCoordinator(pool)

	// Hold the lock briefly in the background.
	holding := make(chan struct{})
	release := make(chan struct{})
	heldDone := make(chan error, 1)
	go func() {
		heldDone <- c.WithGraphLock(ctx, "ws", "project", "p1", func(ctx context.Context) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	// Same triple: blocks until the holder commits — a short-deadline
	// attempt times out instead of entering fn.
	shortCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	entered := atomic.Bool{}
	err := c.WithGraphLock(shortCtx, "ws", "project", "p1", func(ctx context.Context) error {
		entered.Store(true)
		return nil
	})
	if err == nil {
		t.Fatal("contending lock must block and fail under a deadline")
	}
	if entered.Load() {
		t.Fatal("fn ran without holding the graph lock")
	}

	// Different triple: not blocked by the held lock.
	if err := c.WithGraphLock(ctx, "ws", "project", "p2", func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("different graph must not block: %v", err)
	}

	// After release the same triple proceeds.
	close(release)
	if err := <-heldDone; err != nil {
		t.Fatalf("holder: %v", err)
	}
	ran := false
	if err := c.WithGraphLock(ctx, "ws", "project", "p1", func(ctx context.Context) error { ran = true; return nil }); err != nil {
		t.Fatalf("post-release lock: %v", err)
	}
	if !ran {
		t.Fatal("fn did not run after the lock was released")
	}

	// fn errors propagate.
	sentinel := errors.New("boom")
	if err := c.WithGraphLock(ctx, "ws", "project", "p1", func(ctx context.Context) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("fn error = %v, want %v", err, sentinel)
	}
}
