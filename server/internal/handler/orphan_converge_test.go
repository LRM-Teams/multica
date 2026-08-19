package handler

import (
	"testing"

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
