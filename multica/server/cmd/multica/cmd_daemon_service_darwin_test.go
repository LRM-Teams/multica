//go:build darwin

package main

import "testing"

func TestLaunchAgentLabelDefaultProfile(t *testing.T) {
	if got, want := launchAgentLabel(""), "com.multica.daemon"; got != want {
		t.Fatalf("launchAgentLabel(\"\") = %q, want %q", got, want)
	}
}

func TestLaunchAgentLabelNamedProfile(t *testing.T) {
	if got, want := launchAgentLabel("staging"), "com.multica.daemon.staging"; got != want {
		t.Fatalf("launchAgentLabel(\"staging\") = %q, want %q", got, want)
	}
}

func TestLaunchAgentPlistPathIsolatesProfiles(t *testing.T) {
	defaultPath, err := launchAgentPlistPath("")
	if err != nil {
		t.Fatalf("launchAgentPlistPath(\"\"): %v", err)
	}
	stagingPath, err := launchAgentPlistPath("staging")
	if err != nil {
		t.Fatalf("launchAgentPlistPath(\"staging\"): %v", err)
	}
	if defaultPath == stagingPath {
		t.Fatalf("default and named profile plist paths must differ, both got %q", defaultPath)
	}
}

func TestLaunchctlPrintFieldExtractsState(t *testing.T) {
	sample := "gui/501/com.multica.daemon = {\n\tactive count = 1\n\tstate = running\n\tprogram = /tmp/multica\n}\n"
	if got, want := launchctlPrintField(sample, "state = "), "running"; got != want {
		t.Fatalf("launchctlPrintField(state) = %q, want %q", got, want)
	}
}

func TestLaunchctlPrintFieldMissingKeyReturnsEmpty(t *testing.T) {
	sample := "gui/501/com.multica.daemon = {\n\tactive count = 1\n}\n"
	if got := launchctlPrintField(sample, "state = "); got != "" {
		t.Fatalf("launchctlPrintField(missing state) = %q, want empty", got)
	}
}
