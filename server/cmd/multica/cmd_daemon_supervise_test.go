package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunningUnderSupervision_DefaultsFalse(t *testing.T) {
	t.Setenv(superviseEnvVar, "")
	if runningUnderSupervision() {
		t.Fatalf("runningUnderSupervision() = true with %s unset, want false", superviseEnvVar)
	}
}

func TestRunningUnderSupervision_TrueOnlyWhenExactMatch(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", false},
		{"yes", false},
		{"0", false},
		{"", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(superviseEnvVar, tc.value)
			if got := runningUnderSupervision(); got != tc.want {
				t.Fatalf("runningUnderSupervision() with %s=%q = %v, want %v", superviseEnvVar, tc.value, got, tc.want)
			}
		})
	}
}

func TestBuildSuperviseConfigDefaultProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	cfg, err := buildSuperviseConfig("", "/usr/local/bin/multica", []string{"computer", "__service"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("buildSuperviseConfig: %v", err)
	}

	wantDir := filepath.Join(home, ".multica", "computer")
	if cfg.LockPath != filepath.Join(wantDir, "supervisor.lock") {
		t.Errorf("LockPath = %q, want under %q", cfg.LockPath, wantDir)
	}
	if cfg.ResolveWorkerPath == nil {
		t.Fatal("ResolveWorkerPath is nil")
	}
	gotPath, gotArgs, err := cfg.ResolveWorkerPath()
	if err != nil {
		t.Fatalf("ResolveWorkerPath(): %v", err)
	}
	if gotPath != "/usr/local/bin/multica" {
		t.Errorf("resolved path = %q, want fallback exePath", gotPath)
	}
	if got := strings.Join(gotArgs, " "); got != "computer __service" {
		t.Errorf("resolved args = %q, want %q", got, "computer __service")
	}
	if cfg.HandoffExitCode != daemonHandoffExitCode {
		t.Errorf("HandoffExitCode = %d, want %d", cfg.HandoffExitCode, daemonHandoffExitCode)
	}
	found := false
	for _, kv := range cfg.WorkerEnv {
		if kv == superviseEnvVar+"=1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("WorkerEnv = %v, want it to mark generations as supervised (%s=1)", cfg.WorkerEnv, superviseEnvVar)
	}
	if cfg.Stdout != &stdout || cfg.Stderr != &stderr {
		t.Errorf("Stdout/Stderr not wired to the given writers")
	}
}

func TestBuildSuperviseConfigNamedProfileUsesMachineWideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	defaultCfg, err := buildSuperviseConfig("", "/bin/multica", nil, nil, nil)
	if err != nil {
		t.Fatalf("buildSuperviseConfig(default): %v", err)
	}
	stagingCfg, err := buildSuperviseConfig("staging", "/bin/multica", nil, nil, nil)
	if err != nil {
		t.Fatalf("buildSuperviseConfig(staging): %v", err)
	}

	if defaultCfg.LockPath != stagingCfg.LockPath {
		t.Fatalf("legacy profile must not create a second supervisor: default=%q staging=%q", defaultCfg.LockPath, stagingCfg.LockPath)
	}
	wantStagingDir := filepath.Join(home, ".multica", "computer")
	if stagingCfg.LockPath != filepath.Join(wantStagingDir, "supervisor.lock") {
		t.Errorf("staging LockPath = %q, want under %q", stagingCfg.LockPath, wantStagingDir)
	}
}
