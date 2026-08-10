package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestComputerConnectionProjectionDoesNotDependOnAgentRuntime(t *testing.T) {
	now := time.Now().UTC()
	connected := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now, Valid: true}, now)
	if !connected.Connected || connected.LastSeen == nil {
		t.Fatalf("fresh zero-Agent Computer projection = %+v", connected)
	}

	stale := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true}, now)
	if stale.Connected {
		t.Fatalf("stale Computer heartbeat reported connected: %+v", stale)
	}
}
