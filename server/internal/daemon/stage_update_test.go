package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestRunStageUpdateDoesNotTouchSiblingExecutable(t *testing.T) {
	// CUT-T1: stage path must not self-replace a sibling "running" binary.
	root := t.TempDir()
	fakeExe := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(fakeExe), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("original-running-v0.3.77")
	if err := os.WriteFile(fakeExe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	prevStage := stageReleaseFn
	stageReleaseFn = func(targetVersion string, _ time.Duration, _ string) (string, error) {
		return cli.WriteReleaseScratch(targetVersion, []byte("candidate-v0.3.78"))
	}
	t.Cleanup(func() { stageReleaseFn = prevStage })

	d := &Daemon{
		cfg:    Config{CLIVersion: "v0.3.77"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	msg, err := d.runStageUpdate("v0.3.78")
	if err != nil {
		t.Fatalf("runStageUpdate: %v", err)
	}
	if !strings.Contains(msg, "Staged") {
		t.Fatalf("message = %q", msg)
	}
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("sibling executable was mutated — self-replace leak")
	}
}

func TestVerifyStagedBinaryUsesScratchPathFromRunStageUpdateStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scratch fixture is a POSIX --version script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	prevStage := stageReleaseFn
	stageReleaseFn = func(targetVersion string, _ time.Duration, _ string) (string, error) {
		return cli.WriteReleaseScratch(targetVersion, []byte("#!/bin/sh\necho 'multica "+targetVersion+"'\n"))
	}
	t.Cleanup(func() { stageReleaseFn = prevStage })

	d := &Daemon{
		cfg:    Config{CLIVersion: "v0.4.16"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	status, err := d.runStageUpdate("v0.4.17")
	if err != nil {
		t.Fatalf("runStageUpdate: %v", err)
	}
	if _, statErr := os.Stat(status); statErr == nil {
		t.Fatalf("runStageUpdate returned an existing path %q; handleMachineUpgrade would then verify a status string only if this is not a file", status)
	}

	if _, err := d.verifyStagedBinary("v0.4.17", status); err != nil {
		t.Fatalf("verifyStagedBinary with runStageUpdate status %q: %v", status, err)
	}
}

func TestRunStageUpdateRejectsStatusStringAsScratch(t *testing.T) {
	prevStage := stageReleaseFn
	stageReleaseFn = func(string, time.Duration, string) (string, error) {
		return "Staged v0.4.17 at /tmp/not-a-file; PATH unchanged", nil
	}
	t.Cleanup(func() { stageReleaseFn = prevStage })

	d := &Daemon{
		cfg:    Config{CLIVersion: "v0.4.16"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if _, err := d.runStageUpdate("v0.4.17"); err == nil {
		t.Fatal("status string was accepted as staged path")
	}
	if d.stagedUpgradePath != "" {
		t.Fatalf("stagedUpgradePath = %q, want empty after rejected status string", d.stagedUpgradePath)
	}
}

// TestRunStageUpdateUsesServerDispatchedBaseURLWithNoEnvVarFallback proves
// the "check for a new version" step and the "download and stage" step
// converge on the SAME base URL when a machine relies purely on
// server-dispatch (task #815's whole promise) with no local env var set —
// closing a gap Barry found reviewing #1561 (2026-07-31): downloadReleaseBinary
// only ever saw the env var/compiled-default layer, never the
// server-dispatched one, so a machine with no env var configured would see a
// new version at check time and then silently fall back to the wrong base
// URL at download time.
func TestRunStageUpdateUsesServerDispatchedBaseURLWithNoEnvVarFallback(t *testing.T) {
	t.Setenv(cli.ReleaseManifestBaseURLEnv, "") // explicitly no env var fallback

	root := t.TempDir()
	t.Setenv("HOME", root)

	var gotServerDispatched string
	prevStage := stageReleaseFn
	stageReleaseFn = func(targetVersion string, _ time.Duration, serverDispatched string) (string, error) {
		gotServerDispatched = serverDispatched
		return cli.WriteReleaseScratch(targetVersion, []byte("candidate-v0.3.78"))
	}
	t.Cleanup(func() { stageReleaseFn = prevStage })

	d := &Daemon{
		cfg:    Config{CLIVersion: "v0.3.77"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// Same mechanism a real heartbeat ack uses to populate the release-source
	// cache consumed by explicit Machine Upgrade staging.
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{
		RuntimeID:              "rt-1",
		Status:                 "ok",
		ReleaseManifestBaseURL: "https://server-dispatched.example.com/computer",
	})

	if _, err := d.runStageUpdate("v0.3.78"); err != nil {
		t.Fatalf("runStageUpdate: %v", err)
	}
	if gotServerDispatched != "https://server-dispatched.example.com/computer" {
		t.Fatalf("downloadAndStageReleaseFn's serverDispatched = %q, want the heartbeat-cached value (check and download must converge on the same base URL)", gotServerDispatched)
	}
}

func TestCommitStagedActivationSwapsInstallPathAndKeepsPrev(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	install, err := cli.DefaultInstallPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(install), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install, []byte("old-path-computer"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged, err := cli.WriteReleaseScratch("v0.4.17", []byte("new-path-computer"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		cfg:               Config{CLIVersion: "v0.4.16"},
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		stagedUpgradePath: staged,
	}
	got, err := d.commitStagedActivation(context.Background(), "upgrade-1", "v0.4.17")
	if err != nil {
		t.Fatal(err)
	}
	if got != install {
		t.Fatalf("restart path = %q, want install path %q", got, install)
	}
	current, err := os.ReadFile(install)
	if err != nil || string(current) != "new-path-computer" {
		t.Fatalf("install path = %q, %v", current, err)
	}
	prev, err := os.ReadFile(install + ".prev")
	if err != nil || string(prev) != "old-path-computer" {
		t.Fatalf("prev = %q, %v", prev, err)
	}
}

func TestWaitForSafeRestartPathAAtHardDeadline(t *testing.T) {
	// D6 path A: T_hard without drain → failed drain_timeout, no restart.
	var reports []map[string]any
	var restartCalls atomic.Int32
	d := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cancelFunc: func() {
			restartCalls.Add(1)
		},
		// Force CAS to fail so path A is the only exit even if barrier is free.
		activateStagedFn: func(context.Context, string, string) (string, error) {
			return "", errors.New("forced activate fail")
		},
	}
	// Hold a synthetic "claim" so barrier never drains? Actually path A also
	// fires when activate fails after idle. Use hard window only:
	// opportunistic 20ms, hardExtra 30ms, interval 10ms; activate fails → path A.
	// We need reportUpdateResult to not panic — client nil.
	// abandonStagedUpdatePathA calls reportUpdateResult which needs client.
	// Stub activate fail after trySetClaimBarrier succeeds (idle).
	// Simpler: hard deadline with activeTasks so never idle, then hard fires.

	d.activeTasks.Store(1) // never idle
	// report needs client — use a discarded local that fails soft
	// reportUpdateResult with nil client may panic — check.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Avoid network: override by short-circuit abandon via hard deadline with
	// nil client — read reportUpdateResult.
	_ = reports
	restarted := d.waitForSafeRestartWithWindows(
		ctx,
		"rt-1",
		"upd-1",
		"v0.3.78",
		"Staged v0.3.78 into version store",
		20*time.Millisecond,
		40*time.Millisecond,
		10*time.Minute, // slow ticker so hard fires first path
	)
	if restarted {
		t.Fatal("expected no restart on T_hard path A")
	}
	if restartCalls.Load() != 0 {
		t.Fatalf("restart calls = %d, want 0", restartCalls.Load())
	}
}
