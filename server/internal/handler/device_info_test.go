package handler

import "testing"

func TestSplitDeviceInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		raw            string
		wantDeviceName string
		wantRuntimeVer string
	}{
		{
			name:           "parker_fixture_ubuntu_codex",
			raw:            "ubuntu · codex-cli 0.146.0",
			wantDeviceName: "ubuntu",
			wantRuntimeVer: "codex-cli 0.146.0",
		},
		{
			name:           "darwin_claude",
			raw:            "darwin · claude-code 1.2.3",
			wantDeviceName: "darwin",
			wantRuntimeVer: "claude-code 1.2.3",
		},
		{
			name:           "no_separator_legacy",
			raw:            "just-a-hostname",
			wantDeviceName: "just-a-hostname",
			wantRuntimeVer: "",
		},
		{
			name:           "empty",
			raw:            "",
			wantDeviceName: "",
			wantRuntimeVer: "",
		},
		{
			name:           "whitespace_trimmed",
			raw:            "  ubuntu · codex-cli 0.146.0  ",
			wantDeviceName: "ubuntu",
			wantRuntimeVer: "codex-cli 0.146.0",
		},
		{
			name:           "separator_only_version_empty",
			raw:            "ubuntu · ",
			wantDeviceName: "ubuntu",
			wantRuntimeVer: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotVer := splitDeviceInfo(tc.raw)
			if gotName != tc.wantDeviceName {
				t.Fatalf("device_name: got %q want %q", gotName, tc.wantDeviceName)
			}
			if gotVer != tc.wantRuntimeVer {
				t.Fatalf("runtime_version: got %q want %q", gotVer, tc.wantRuntimeVer)
			}
		})
	}
}

// TestAgentRuntimeResponseSplitsDeviceInfo is mutation-capable: if the
// structured split is removed from runtimeToResponse, the fixture reds.
func TestAgentRuntimeResponseSplitsDeviceInfo(t *testing.T) {
	t.Parallel()

	const glued = "ubuntu · codex-cli 0.146.0"
	rt := runtimeHealthTestRuntime(t, map[string]any{"version": "1.0.0"})
	rt.DeviceInfo = glued

	resp := (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceInfo != glued {
		t.Fatalf("device_info must stay composite for old clients: got %q", resp.DeviceInfo)
	}
	if resp.DeviceName != "ubuntu" {
		t.Fatalf("device_name: got %q want %q (OS row must not show CLI)", resp.DeviceName, "ubuntu")
	}
	if resp.RuntimeVersion != "codex-cli 0.146.0" {
		t.Fatalf("runtime_version: got %q want %q", resp.RuntimeVersion, "codex-cli 0.146.0")
	}

	// Mutation: strip the separator half — structured fields must follow.
	rt.DeviceInfo = "ubuntu"
	resp = (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceName != "ubuntu" || resp.RuntimeVersion != "" {
		t.Fatalf("after mutation: device_name=%q runtime_version=%q; want ubuntu / empty",
			resp.DeviceName, resp.RuntimeVersion)
	}
}
