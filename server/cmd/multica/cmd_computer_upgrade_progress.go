package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

const computerUpgradeWatchTimeout = 7 * time.Minute

var (
	computerUpgradeWatchPollInterval = 100 * time.Millisecond
	readComputerUpgradeStatus        = computer.RequestMachineUpgradeStatus
	readComputerUpgradeHandoff       = computer.ReadPendingMachineUpgradeHandoff
	probeComputerUpgradeHealth       = func(ctx context.Context, endpoint string) map[string]any {
		return (&computer.Lifecycle{ServiceEndpoint: endpoint}).Health(ctx)
	}
)

type computerUpgradeWatchResult struct {
	Version    string
	PID        string
	Workspaces int
}

type computerUpgradeDisplay struct {
	out         io.Writer
	interactive bool
	color       bool
	frame       int
	activePhase string
	activeText  string
	completed   map[string]bool
	startedAt   time.Time
}

func newComputerUpgradeDisplay(out io.Writer, currentVersion, targetVersion string, interactive bool) *computerUpgradeDisplay {
	display := &computerUpgradeDisplay{
		out: out, interactive: interactive,
		color: interactive && os.Getenv("NO_COLOR") == "", completed: make(map[string]bool), startedAt: time.Now(),
	}
	if display.color {
		fmt.Fprintln(out, "\033[1mUpgrading Multica Computer\033[0m")
	} else {
		fmt.Fprintln(out, "Upgrading Multica Computer")
	}
	fmt.Fprintf(out, "  Current: %s\n", currentVersion)
	fmt.Fprintf(out, "  Target:  %s\n\n", targetVersion)
	return display
}

func stdoutIsInteractive() bool {
	return terminalIsInteractive(os.Stdout)
}

func (display *computerUpgradeDisplay) update(phase, text string) {
	if display.completed[phase] {
		return
	}
	if phase != display.activePhase {
		display.finishActive()
		display.activePhase = phase
		display.activeText = text
		if !display.interactive {
			fmt.Fprintf(display.out, "… %s\n", text)
			return
		}
	}
	if display.interactive {
		spinner := terminalSpinnerFrames[display.frame%len(terminalSpinnerFrames)]
		if display.color {
			spinner = "\033[36m" + spinner + "\033[0m"
		}
		fmt.Fprintf(display.out, "\r\033[2K%s %s", spinner, display.activeText)
		display.frame++
	}
}

func (display *computerUpgradeDisplay) finishActive() {
	if display.activePhase == "" {
		return
	}
	if display.interactive {
		fmt.Fprint(display.out, "\r\033[2K")
	}
	check := "✓"
	if display.color {
		check = "\033[32m✓\033[0m"
	}
	fmt.Fprintf(display.out, "%s %s\n", check, completedUpgradeStep(display.activePhase, display.activeText))
	display.completed[display.activePhase] = true
	display.activePhase = ""
	display.activeText = ""
}

func completedUpgradeStep(phase, fallback string) string {
	switch phase {
	case "requesting":
		return "Upgrade request accepted"
	case "accepted", "running":
		return "Computer accepted the upgrade"
	case "staging":
		return "Release downloaded"
	case "verifying":
		return "Release verified"
	case "applying":
		return "Release installed"
	case "restarting":
		return "Computer restarted and reconnected"
	default:
		return fallback
	}
}

func (display *computerUpgradeDisplay) success(result computerUpgradeWatchResult) {
	display.finishActive()
	check := "✓"
	if display.color {
		check = "\033[32m✓\033[0m"
	}
	fmt.Fprintf(display.out, "\n%s Upgrade complete in %s\n", check, time.Since(display.startedAt).Round(100*time.Millisecond))
	fmt.Fprintf(display.out, "  Version:    %s\n", result.Version)
	fmt.Fprintf(display.out, "  PID:        %s\n", result.PID)
	fmt.Fprintf(display.out, "  Workspaces: %d connected\n", result.Workspaces)
}

func (display *computerUpgradeDisplay) accepted(requestID, targetVersion string) {
	display.finishActive()
	fmt.Fprintf(display.out, "  Target:  %s\n", targetVersion)
	fmt.Fprintf(display.out, "  Request: %s\n", requestID)
	fmt.Fprintln(display.out, "  The Computer will download, verify, and restart automatically.")
	fmt.Fprintln(display.out, "  Check: multica computer status")
}

func (display *computerUpgradeDisplay) installed(version, binaryPath string, alreadyCurrent bool) {
	display.clear()
	if alreadyCurrent {
		fmt.Fprintf(display.out, "\n✓ Computer is already on %s — nothing to upgrade.\n", version)
	} else {
		fmt.Fprintf(display.out, "\n✓ Computer %s installed.\n", version)
	}
	fmt.Fprintln(display.out, "  Computer state: stopped")
	fmt.Fprintf(display.out, "  Binary:         %s\n", binaryPath)
	fmt.Fprintln(display.out, "  Next: multica computer start")
}

func (display *computerUpgradeDisplay) clear() {
	if display.interactive && display.activePhase != "" {
		fmt.Fprint(display.out, "\r\033[2K")
	}
	display.activePhase = ""
	display.activeText = ""
}

func watchComputerUpgrade(
	ctx context.Context,
	endpoint, requestID string,
	initialHealth map[string]any,
	display *computerUpgradeDisplay,
) (computerUpgradeWatchResult, error) {
	initialPID := healthValue(initialHealth, "pid")
	initialGeneration := healthValue(initialHealth, "serviceGeneration")
	requireConnection, _ := initialHealth["connected"].(bool)
	targetVersion := ""
	sawRestart := false
	ticker := time.NewTicker(computerUpgradeWatchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return computerUpgradeWatchResult{}, fmt.Errorf("wait for Computer upgrade: %w", ctx.Err())
		case <-ticker.C:
		}

		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		status, statusErr := readComputerUpgradeStatus(probeCtx, endpoint)
		cancel()
		if statusErr == nil && status.ID == requestID {
			if status.TargetVersion != "" && status.TargetVersion != "latest" {
				targetVersion = status.TargetVersion
			}
			for _, completedPhase := range status.Phases {
				if completedPhase == status.Phase || completedPhase == "done" || completedPhase == "failed" {
					continue
				}
				if text := activeUpgradeStep(completedPhase); text != "" {
					display.update(completedPhase, text)
					display.finishActive()
				}
			}
			switch status.Phase {
			case "failed":
				return computerUpgradeWatchResult{}, fmt.Errorf("Computer upgrade failed (%s); check `multica computer logs`", status.Error)
			case "done":
				version := status.NewVersion
				if version == "" {
					version = healthValue(initialHealth, "cliVersion")
				}
				return computerUpgradeWatchResult{Version: version, PID: initialPID, Workspaces: healthWorkspaceCount(initialHealth)}, nil
			case "accepted", "running":
				display.update(status.Phase, "Waiting for Computer to begin")
			case "staging":
				display.update(status.Phase, "Downloading release")
			case "verifying":
				display.update(status.Phase, "Verifying release")
			case "applying":
				display.update(status.Phase, "Installing release")
			case "restarting":
				sawRestart = true
				display.update(status.Phase, "Restarting Computer and reconnecting Workspaces")
			}
		}

		if handoff, err := readComputerUpgradeHandoff(computer.RootDir("")); err == nil && handoff != nil && handoff.RequestID == requestID {
			if handoff.TargetVersion != "" {
				targetVersion = handoff.TargetVersion
			}
			if handoff.Phase == computer.MachineUpgradePhaseStartingTarget || handoff.Phase == computer.MachineUpgradePhaseTargetReady {
				sawRestart = true
				display.update("restarting", "Restarting Computer and reconnecting Workspaces")
			}
		}

		probeCtx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
		health := probeComputerUpgradeHealth(probeCtx, endpoint)
		cancel()
		if !computer.Alive(health) {
			if sawRestart {
				display.update("restarting", "Restarting Computer and reconnecting Workspaces")
			}
			continue
		}
		generationChanged := healthValue(health, "serviceGeneration") != initialGeneration
		version := healthValue(health, "cliVersion")
		versionMatches := targetVersion != "" && sameComputerVersion(version, targetVersion)
		connected, _ := health["connected"].(bool)
		if sawRestart && generationChanged && versionMatches && (!requireConnection || connected) {
			return computerUpgradeWatchResult{
				Version: version, PID: healthValue(health, "pid"), Workspaces: healthWorkspaceCount(health),
			}, nil
		}
		if sawRestart && generationChanged && targetVersion != "" && !versionMatches {
			return computerUpgradeWatchResult{}, fmt.Errorf("Computer restarted on %s instead of target %s; the upgrade may have rolled back (check `multica computer logs`)", version, targetVersion)
		}
	}
}

func activeUpgradeStep(phase string) string {
	switch phase {
	case "accepted", "running":
		return "Waiting for Computer to begin"
	case "staging":
		return "Downloading release"
	case "verifying":
		return "Verifying release"
	case "applying":
		return "Installing release"
	case "restarting":
		return "Restarting Computer and reconnecting Workspaces"
	default:
		return ""
	}
}

func sameComputerVersion(left, right string) bool {
	normalize := func(value string) string {
		return strings.TrimPrefix(strings.TrimSpace(value), "v")
	}
	return normalize(left) == normalize(right)
}

func healthWorkspaceCount(health map[string]any) int {
	switch workspaces := health["workspaces"].(type) {
	case []any:
		return len(workspaces)
	case []string:
		return len(workspaces)
	default:
		return 0
	}
}
