package handler

import "testing"

func TestDeviceNameFromDeviceInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "parker_fixture_ubuntu_codex",
			raw:  "ubuntu · codex-cli 0.146.0",
			want: "ubuntu",
		},
		{
			name: "claude_code_parens",
			raw:  "dev.local · 2.1.5 (Claude Code)",
			want: "dev.local",
		},
		{
			name: "os_arch_half_wins",
			raw:  "host.local · Linux (x86_64)",
			want: "Linux (x86_64)",
		},
		{
			name: "daemon_placeholder_filtered",
			raw:  "daemon abc123",
			want: "",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
		{
			name: "hostname_only",
			raw:  "just-a-hostname",
			want: "just-a-hostname",
		},
		{
			name: "ca_version_only",
			raw:  "codex-cli 0.146.0",
			want: "",
		},
		{
			name: "macos_arch",
			raw:  "macOS (arm64)",
			want: "macOS (arm64)",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deviceNameFromDeviceInfo(tc.raw); got != tc.want {
				t.Fatalf("device_name: got %q want %q", got, tc.want)
			}
		})
	}
}

// TestAgentRuntimeResponseDeviceName is mutation-capable: if the structured
// device_name derivation is removed from runtimeToResponse, the fixture reds.
func TestAgentRuntimeResponseDeviceName(t *testing.T) {
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

	// Mutation: strip the CA half — device_name must follow.
	rt.DeviceInfo = "ubuntu"
	resp = (&Handler{}).runtimeToResponse(t.Context(), rt)
	if resp.DeviceName != "ubuntu" {
		t.Fatalf("after mutation: device_name=%q; want ubuntu", resp.DeviceName)
	}

	// Mutation: remove the split entirely would leave device_name empty / wrong.
	rt.DeviceInfo = "ubuntu · codex-cli 0.146.0"
	resp = (&Handler{}).runtimeToResponse(t.Context(), rt)
	if stringsContainsCLI(resp.DeviceName) {
		t.Fatalf("device_name still contains CLI version: %q", resp.DeviceName)
	}
}

func stringsContainsCLI(s string) bool {
	return isAgentVersionLike.MatchString(s)
}
