package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestComputerConnectionProjectionDoesNotDependOnAgentRuntime(t *testing.T) {
	now := time.Now().UTC()
	connected := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now, Valid: true}, true, false)
	if !connected.Connected || connected.LastSeen == nil {
		t.Fatalf("fresh zero-Agent Computer projection = %+v", connected)
	}

	disconnected := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now, Valid: true}, false, false)
	if disconnected.Connected {
		t.Fatalf("DaemonCore socket down must report disconnected even with a fresh last_seen: %+v", disconnected)
	}
}
