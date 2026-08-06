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
			name:       "structured_metadata",
			deviceInfo: "ubuntu · codex-cli 0.146.0",
			metadata:   map[string]any{"device_name": "ubuntu", "version": "codex-cli 0.146.0"},
			want:       "ubuntu",
		},
		{
			name:       "no_metadata_no_compat_parse",
			deviceInfo: "ubuntu · codex-cli 0.146.0",
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

func TestAgentRuntimeResponseRequiresMetadataDeviceName(t *testing.T) {
	t.Parallel()

	rt := runtimeHealthTestRuntime(t, map[string]any{
		"device_name": "ubuntu",
		"version":     "codex-cli 0.146.0",
	})
	rt.DeviceInfo = "ubuntu · codex-cli 0.146.0"

	resp := (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceName != "ubuntu" {
		t.Fatalf("device_name: got %q want ubuntu", resp.DeviceName)
	}

	// Mutation: no metadata.device_name → empty (no device_info compat parse).
	rt = runtimeWithMetadata(t, rt, map[string]any{"version": "codex-cli 0.146.0"})
	resp = (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceName != "" {
		t.Fatalf("without metadata.device_name got %q; want empty (no compat)", resp.DeviceName)
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
