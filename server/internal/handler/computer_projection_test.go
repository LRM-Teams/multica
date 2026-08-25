package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestComputerConnectionProjectionDoesNotDependOnAgentRuntime(t *testing.T) {
	now := time.Now().UTC()
	connected := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now, Valid: true}, true, false, "ubuntu-build-host", "linux", "0.4.24-alpha.91")
	if !connected.Connected || connected.LastSeen == nil {
		t.Fatalf("fresh zero-Agent Computer projection = %+v", connected)
	}
	if connected.DeviceName != "ubuntu-build-host" || connected.OS != "linux" || connected.CLIVersion != "0.4.24-alpha.91" {
		t.Fatalf("zero-Agent Computer metadata = %+v", connected)
	}
	wire, err := json.Marshal(connected)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"deviceName":"ubuntu-build-host"`) || !strings.Contains(string(wire), `"cliVersion":"0.4.24-alpha.91"`) {
		t.Fatalf("Computer metadata JSON is not camelCase: %s", wire)
	}
	if strings.Contains(string(wire), `"device_name"`) || strings.Contains(string(wire), `"cli_version"`) {
		t.Fatalf("Computer metadata JSON contains snake_case fields: %s", wire)
	}
	if !strings.Contains(string(wire), `"runtimes":[]`) {
		t.Fatalf("zero-Runtime Computer must encode runtimes as an empty array: %s", wire)
	}

	disconnected := computerConnectionProjection("computer-1", "user-1", pgtype.Timestamptz{Time: now, Valid: true}, false, false, "ubuntu-build-host", "linux", "0.4.24-alpha.91")
	if disconnected.Connected {
		t.Fatalf("DaemonCore socket down must report disconnected even with a fresh last_seen: %+v", disconnected)
	}
}
