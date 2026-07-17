package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestDeriveSquadMemberStatus(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	online := pgtype.Text{String: "online", Valid: true}
	offline := pgtype.Text{String: "offline", Valid: true}
	missing := pgtype.Text{}

	tsAgo := func(d time.Duration) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(-d), Valid: true}
	}
	tsNone := pgtype.Timestamptz{}

	cases := []struct {
		name          string
		archived      bool
		runtimeStatus pgtype.Text
		lastSeen      pgtype.Timestamptz
		hasActiveTask bool
		want          string
	}{
		{"offline runtime wins over active task", false, offline, tsAgo(time.Hour), true, "offline"},
		{"missing runtime wins over active task", false, missing, tsNone, true, "offline"},
		{"online runtime, no task", false, online, tsAgo(2 * time.Second), false, "idle"},
		{"online runtime, active task", false, online, tsAgo(2 * time.Second), true, "working"},
		{"offline runtime, recent heartbeat", false, offline, tsAgo(2 * time.Minute), false, "offline"},
		{"offline runtime, stale heartbeat", false, offline, tsAgo(2 * time.Hour), false, "offline"},
		{"online runtime, stale heartbeat in transient window", false, online, tsAgo(3 * time.Minute), false, "unstable"},
		{"online runtime, stale heartbeat past reconnect window", false, online, tsAgo(6 * time.Minute), false, "offline"},
		{"offline runtime, no heartbeat", false, offline, tsNone, false, "offline"},
		{"no runtime row", false, missing, tsNone, false, "offline"},
		// Archived agents always report archived regardless of any leftover
		// runtime row or task — they should appear in the squad listing
		// but never look like they're still working or merely offline.
		{"archived agent with active task", true, online, tsAgo(time.Second), true, "archived"},
		{"archived agent with online runtime", true, online, tsAgo(time.Second), false, "archived"},
		{"archived agent already offline", true, offline, tsAgo(time.Hour), false, "archived"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveSquadMemberStatus(tc.archived, tc.runtimeStatus, tc.lastSeen, tc.hasActiveTask, now)
			if got != tc.want {
				t.Fatalf("deriveSquadMemberStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
