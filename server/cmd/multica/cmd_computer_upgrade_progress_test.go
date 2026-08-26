package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

func TestWatchComputerUpgradeConfirmsSuccessorVersionAndConnection(t *testing.T) {
	restoreComputerUpgradeWatchSeams(t)
	computerUpgradeWatchPollInterval = time.Millisecond
	readComputerUpgradeStatus = func(context.Context, string) (computer.MachineUpgradeStatus, error) {
		return computer.MachineUpgradeStatus{
			ID: "request-a", Phase: "restarting", TargetVersion: "v2.0.0",
			Phases: []string{"accepted", "staging", "verifying", "applying", "restarting"},
		}, nil
	}
	readComputerUpgradeHandoff = func(string) (*computer.PendingMachineUpgradeHandoff, error) { return nil, nil }
	probeComputerUpgradeHealth = func(context.Context, string) map[string]any {
		return map[string]any{
			"status": "running", "pid": float64(202), "serviceGeneration": "generation-b",
			"cliVersion": "v2.0.0", "connected": true, "workspaces": []any{"workspace-a"},
		}
	}

	var output bytes.Buffer
	display := newComputerUpgradeDisplay(&output, "v1.0.0", "v2.0.0", false)
	result, err := watchComputerUpgrade(context.Background(), "endpoint", "request-a", map[string]any{
		"status": "running", "pid": float64(101), "serviceGeneration": "generation-a",
		"cliVersion": "v1.0.0", "connected": true, "workspaces": []any{"workspace-a"},
	}, display)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v2.0.0" || result.PID != "202" || result.Workspaces != 1 {
		t.Fatalf("watch result = %+v", result)
	}
	display.success(result)
	for _, want := range []string{"✓ Release downloaded", "✓ Release verified", "✓ Release installed", "✓ Computer restarted and reconnected"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("watch output missing %q:\n%s", want, output.String())
		}
	}
}

func TestWatchComputerUpgradeReturnsReportedFailure(t *testing.T) {
	restoreComputerUpgradeWatchSeams(t)
	computerUpgradeWatchPollInterval = time.Millisecond
	readComputerUpgradeStatus = func(context.Context, string) (computer.MachineUpgradeStatus, error) {
		return computer.MachineUpgradeStatus{ID: "request-a", Phase: "failed", Error: "verification_failed"}, nil
	}
	readComputerUpgradeHandoff = func(string) (*computer.PendingMachineUpgradeHandoff, error) { return nil, nil }
	probeComputerUpgradeHealth = func(context.Context, string) map[string]any { return nil }

	var output bytes.Buffer
	_, err := watchComputerUpgrade(context.Background(), "endpoint", "request-a", nil,
		newComputerUpgradeDisplay(&output, "v1.0.0", "v2.0.0", false))
	if err == nil || !strings.Contains(err.Error(), "verification_failed") {
		t.Fatalf("watch error = %v", err)
	}
}

func TestComputerUpgradeDisplayFallsBackToPlainLines(t *testing.T) {
	var output bytes.Buffer
	display := newComputerUpgradeDisplay(&output, "v1.0.0", "v2.0.0", false)
	display.update("staging", "Downloading release")
	display.update("verifying", "Verifying release")
	display.success(computerUpgradeWatchResult{Version: "v2.0.0", PID: "202", Workspaces: 2})

	got := output.String()
	for _, want := range []string{
		"Current: v1.0.0", "Target:  v2.0.0", "… Downloading release",
		"✓ Release downloaded", "✓ Release verified", "Version:    v2.0.0", "Workspaces: 2 connected",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("display output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("non-interactive output contains ANSI controls: %q", got)
	}
}

func TestComputerUpgradeDisplayUsesTTYSpinnerAndColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var output bytes.Buffer
	display := newComputerUpgradeDisplay(&output, "v1.0.0", "v2.0.0", true)
	display.update("staging", "Downloading release")
	display.update("verifying", "Verifying release")

	got := output.String()
	for _, want := range []string{"\033[1m", "\r\033[2K", "\033[36m⠋\033[0m", "\033[32m✓\033[0m Release downloaded"} {
		if !strings.Contains(got, want) {
			t.Fatalf("interactive display output missing %q: %q", want, got)
		}
	}
}

func restoreComputerUpgradeWatchSeams(t *testing.T) {
	t.Helper()
	poll := computerUpgradeWatchPollInterval
	status := readComputerUpgradeStatus
	handoff := readComputerUpgradeHandoff
	health := probeComputerUpgradeHealth
	t.Cleanup(func() {
		computerUpgradeWatchPollInterval = poll
		readComputerUpgradeStatus = status
		readComputerUpgradeHandoff = handoff
		probeComputerUpgradeHealth = health
	})
}

func TestUpgradeInstalledCopyDistinguishesAlreadyCurrent(t *testing.T) {
	var output bytes.Buffer
	display := newComputerUpgradeDisplay(&output, "v1.0.0", "v1.0.0", false)
	display.installed("v1.0.0", "/usr/local/bin/multica", true)
	rendered := output.String()
	for _, want := range []string{"already on v1.0.0", "nothing to upgrade"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("already-current output missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "installed.") {
		t.Fatalf("already-current output must not claim a fresh install:\n%s", rendered)
	}

	output.Reset()
	display.installed("v2.0.0", "/usr/local/bin/multica", false)
	if !strings.Contains(output.String(), "✓ Computer v2.0.0 installed.") {
		t.Fatalf("fresh install output = %q", output.String())
	}
}
