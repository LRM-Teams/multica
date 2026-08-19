package handler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRuntimeDeviceName(t *testing.T) {
	mk := func(meta string, devInfo string) db.AgentRuntime {
		return db.AgentRuntime{Metadata: []byte(meta), DeviceInfo: devInfo}
	}
	cases := []struct {
		name     string
		rt       db.AgentRuntime
		expected string
	}{
		{"device_name in metadata wins", mk(`{"device_name":"s146-jianghp3","version":"0.84.1"}`, "ubuntu · 0.84.1"), "s146-jianghp3"},
		{"fallback to device_info host prefix", mk(`{"version":"0.84.1"}`, "ubuntu · 0.84.1"), "ubuntu"},
		{"fallback to device_info plain", mk(``, "myhost"), "myhost"},
		{"empty metadata + empty device_info -> empty", mk(``, ""), ""},
		{"bad metadata json falls back", mk(`{not-json`, "hostabc · 1.0"), "hostabc"},
		{"blank device_name falls back", mk(`{"device_name":"  ","version":"1"}`, "hostabc · 1.0"), "hostabc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runtimeDeviceName(c.rt); got != c.expected {
				t.Fatalf("runtimeDeviceName() = %q, want %q", got, c.expected)
			}
		})
	}
}

// daemonAliveByRunner: live WS presence is authoritative; heartbeat window is
// only the fallback when the Hub is unavailable.
func TestDaemonAliveByRunner(t *testing.T) {
	now := time.Now()
	mkHB := func(daemon string, age time.Duration) db.DaemonHeartbeat {
		return db.DaemonHeartbeat{DaemonID: daemon, LastSeenAt: pgtype.Timestamptz{Time: now.Add(-age), Valid: true}}
	}
	beats := []db.DaemonHeartbeat{
		mkHB("old-dead", 10*time.Minute),
		mkHB("old-live-hb", 1*time.Minute),
	}

	t.Run("no hub uses heartbeat window", func(t *testing.T) {
		h := &Handler{} // DaemonHub nil
		if h.daemonAliveByRunner(context.Background(), "old-live-hb", "ws", beats) != true {
			t.Fatalf("recent heartbeat should count as alive without hub")
		}
		if h.daemonAliveByRunner(context.Background(), "old-dead", "ws", beats) != false {
			t.Fatalf("stale heartbeat should count as dead without hub")
		}
	})

	t.Run("hub presence overrides heartbeat", func(t *testing.T) {
		hub := daemonws.NewHub()
		h := &Handler{DaemonHub: hub}
		// No Runner socket is registered for these daemons: even a fresh
		// heartbeat must not make them "alive" for convergence. A live second
		// machine holds a socket and is skipped by HasWorkspaceRunner (covered
		// by daemonws unit tests); here we assert the authoritative override.
		if h.daemonAliveByRunner(context.Background(), "old-live-hb", "ws", beats) != false {
			t.Fatalf("hub without Runner should report dead even with fresh heartbeat")
		}
	})
}
