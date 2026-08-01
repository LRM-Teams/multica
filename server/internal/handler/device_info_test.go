package handler

import (
	"encoding/json"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDeviceNameFromRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		deviceInfo string
		metadata   map[string]any
		want       string
	}{
		{
			name:       "structured_metadata_wins",
			deviceInfo: "ubuntu · codex-cli 0.146.0",
			metadata:   map[string]any{"device_name": "ubuntu", "version": "codex-cli 0.146.0"},
			want:       "ubuntu",
		},
		{
			name:       "structured_ignores_glued_noise",
			deviceInfo: "anything · whatever",
			metadata:   map[string]any{"device_name": "s144"},
			want:       "s144",
		},
		{
			name:       "legacy_glue_left_half",
			deviceInfo: "ubuntu · codex-cli 0.146.0",
			metadata:   map[string]any{"version": "codex-cli 0.146.0"},
			want:       "ubuntu",
		},
		{
			name:       "legacy_claude_parens",
			deviceInfo: "dev.local · 2.1.5 (Claude Code)",
			metadata:   map[string]any{"version": "2.1.5 (Claude Code)"},
			want:       "dev.local",
		},
		{
			name:       "legacy_name_only",
			deviceInfo: "host.local",
			metadata:   map[string]any{},
			want:       "host.local",
		},
		{
			name:       "legacy_version_only",
			deviceInfo: "codex-cli 0.146.0",
			metadata:   map[string]any{"version": "codex-cli 0.146.0"},
			want:       "",
		},
		{
			name:       "empty",
			deviceInfo: "",
			metadata:   nil,
			want:       "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var meta any
			if tc.metadata != nil {
				meta = tc.metadata
			}
			if got := deviceNameFromRuntime(tc.deviceInfo, meta); got != tc.want {
				t.Fatalf("device_name: got %q want %q", got, tc.want)
			}
		})
	}
}

// Mutation-capable: if registration stops persisting metadata.device_name and
// response stops reading it, this fixture reds.
func TestAgentRuntimeResponsePrefersMetadataDeviceName(t *testing.T) {
	t.Parallel()

	rt := runtimeHealthTestRuntime(t, map[string]any{
		"device_name": "ubuntu",
		"version":     "codex-cli 0.146.0",
	})
	rt.DeviceInfo = "ubuntu · codex-cli 0.146.0"

	resp := (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceInfo != rt.DeviceInfo {
		t.Fatalf("device_info must stay composite: got %q", resp.DeviceInfo)
	}
	if resp.DeviceName != "ubuntu" {
		t.Fatalf("device_name: got %q want ubuntu", resp.DeviceName)
	}

	// Mutation: wipe structured field — legacy glue inverse must still work.
	rt = runtimeWithMetadata(t, rt, map[string]any{"version": "codex-cli 0.146.0"})
	resp = (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceName != "ubuntu" {
		t.Fatalf("legacy fallback device_name: got %q want ubuntu", resp.DeviceName)
	}
}

func runtimeWithMetadata(t *testing.T, rt db.AgentRuntime, metadata map[string]any) db.AgentRuntime {
	t.Helper()
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	rt.Metadata = data
	return rt
}
