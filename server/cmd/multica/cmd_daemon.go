package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

func runComputerStart(cmd *cobra.Command, args []string) error {
	binding, selected, err := resolveWorkspaceBinding(args)
	if err != nil {
		return err
	}
	if selected {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		health := (&computer.Lifecycle{}).Health(ctx)
		cancel()
		if computer.Alive(health) {
			fmt.Fprintf(os.Stderr, "Computer is already running (pid %s, version %s).\n", healthValue(health, "pid"), healthValue(health, "cliVersion"))
			if err := waitWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr),
				fmt.Sprintf("Waiting for Workspace %s connection", workspaceLabel(binding)),
				func() error { return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout) }); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s Workspace %s connected.\n", terminalSuccessMark(os.Stderr), workspaceLabel(binding))
			return nil
		}
	}
	foreground, _ := cmd.Flags().GetBool("foreground")
	if foreground {
		fmt.Fprintln(os.Stderr, "Starting Computer in the foreground...")
		return run(cmd, args)
	}
	if err := runDaemonBackground(cmd); err != nil {
		return err
	}
	if selected {
		if err := waitWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr),
			fmt.Sprintf("Waiting for Workspace %s connection", workspaceLabel(binding)),
			func() error { return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout) }); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s Workspace %s connected.\n", terminalSuccessMark(os.Stderr), workspaceLabel(binding))
	}
	return nil
}

func workspaceLabel(binding computer.WorkspaceBinding) string {
	if binding.WorkspaceSlug != "" {
		return "/" + binding.WorkspaceSlug
	}
	return binding.WorkspaceID
}

func healthValue(health map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(health[key]))
	if value == "" || value == "<nil>" {
		return "unknown"
	}
	return value
}

func waitForWorkspaceReady(workspaceID string, timeout time.Duration) error {
	lifecycle := &computer.Lifecycle{}
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return fmt.Errorf("load current service environment: %w", err)
	}
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		health := lifecycle.Health(ctx)
		cancel()
		if health["status"] == "running" && health["connected"] == true && normalizeAPIBaseURL(fmt.Sprint(health["serverUrl"])) == normalizeAPIBaseURL(target.Origin) && healthContainsWorkspace(health, workspaceID) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("Computer is not connected for Workspace %s", workspaceID)
}

func runDaemonBackground(cmd *cobra.Command) error {
	lc := &computer.Lifecycle{}
	res, err := runWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr), "Starting Computer", func() (computer.StartResult, error) {
		return lc.StartBackground(computerStartOptions(cmd))
	})
	if err != nil {
		return err
	}
	if err := printComputerStartResult(res); err != nil {
		return err
	}
	cleanupStartedComputerReleaseResidue()
	return nil
}

func cleanupStartedComputerReleaseResidue() {
	installPath, installPathErr := cli.InstallPath()
	executable, executablePathErr := os.Executable()
	if installPathErr != nil || executablePathErr != nil {
		return
	}
	installInfo, installErr := os.Stat(installPath)
	executableInfo, executableErr := os.Stat(executable)
	if installErr != nil || executableErr != nil || !os.SameFile(installInfo, executableInfo) {
		return
	}
	if _, err := cli.CleanupInstalledReleaseResidue(installPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Computer started, but old release cleanup failed: %v\n", err)
	}
}

func printComputerStartResult(res computer.StartResult) error {
	if res.Started {
		fmt.Fprintf(os.Stderr, "%s Computer is running.\n", terminalSuccessMark(os.Stderr))
		fmt.Fprintf(os.Stderr, "  PID:     %d\n", res.Pid)
		fmt.Fprintf(os.Stderr, "  Version: %s\n", version)
		fmt.Fprintf(os.Stderr, "  Logs:    %s\n", res.LogPath)
		return nil
	}

	// Not ready within the startup window.
	if res.LastStatus == "starting" {
		fmt.Fprintf(os.Stderr, "Computer is still starting after %s (Agent detection / Workspace sync is taking longer than expected). Check logs:\n  %s\n", computer.StartupTimeout, res.LogPath)
		return fmt.Errorf("Computer did not become ready before the startup timeout")
	} else {
		fmt.Fprintf(os.Stderr, "Computer may not have started successfully. Check logs:\n  %s\n", res.LogPath)
		return fmt.Errorf("Computer exited or did not publish startup health")
	}
}

func computerStartOptions(cmd *cobra.Command) computer.StartOptions {
	options := computer.StartOptions{
		DaemonID:                       flagString(cmd, "daemon-id"),
		DeviceName:                     flagString(cmd, "device-name"),
		RuntimeName:                    flagString(cmd, "runtime-name"),
		PollInterval:                   flagDuration(cmd, "poll-interval"),
		HeartbeatInterval:              flagDuration(cmd, "heartbeat-interval"),
		CodexSemanticInactivityTimeout: flagDuration(cmd, "codex-semantic-inactivity-timeout"),
	}
	if cmd.Flags().Changed("agent-timeout") {
		options.AgentTimeout, _ = cmd.Flags().GetDuration("agent-timeout")
		options.AgentTimeoutSet = true
	}
	return options
}

func flagDuration(cmd *cobra.Command, name string) time.Duration {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		return 0
	}
	value, _ := cmd.Flags().GetDuration(name)
	return value
}

func runActiveComputerBinary(target string) error {
	child := exec.Command(target, os.Args[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = os.Environ()
	if err := child.Run(); err != nil {
		return fmt.Errorf("run Active Computer binary %s: %w", target, err)
	}
	return nil
}

// --- daemon restart ---

func runComputerRestart(cmd *cobra.Command, args []string) error {
	binding, selected, err := resolveWorkspaceBinding(args)
	if err != nil {
		return err // validate before stopping the one resident
	}
	lc := &computer.Lifecycle{}
	result, err := runWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr), "Restarting Computer", func() (computer.RestartResult, error) {
		return lc.Restart(computerStartOptions(cmd))
	})
	if err != nil {
		return err
	}
	if result.Stop.Running && result.Stop.Pid > 0 {
		fmt.Fprintf(os.Stderr, "  Stopped previous process (pid %d).\n", result.Stop.Pid)
		if result.Stop.GracefulFailed {
			fmt.Fprintln(os.Stderr, "  Graceful control was unavailable; forced process cleanup completed.")
		}
	}
	if err := printComputerStartResult(result.Start); err != nil {
		return err
	}
	if selected {
		if err := waitWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr),
			fmt.Sprintf("Waiting for Workspace %s connection", workspaceLabel(binding)),
			func() error { return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout) }); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s Workspace %s connected.\n", terminalSuccessMark(os.Stderr), workspaceLabel(binding))
	}
	return nil
}

// --- daemon stop ---

func runComputerStop(cmd *cobra.Command, _ []string) error {
	lc := &computer.Lifecycle{}

	res, _ := runWithTerminalProgress(os.Stderr, terminalIsInteractive(os.Stderr), "Stopping Computer", func() (computer.StopResult, error) {
		return lc.Stop(), nil
	})
	if !res.Running {
		fmt.Fprintln(os.Stderr, "Computer is already stopped.")
		return nil
	}
	if res.GracefulFailed {
		fmt.Fprintln(os.Stderr, "  Graceful control was unavailable; cleaning up processes directly.")
	}
	if res.Err != nil {
		return res.Err
	}
	if res.Stopped {
		fmt.Fprintf(os.Stderr, "%s Computer stopped (pid %d).\n", terminalSuccessMark(os.Stderr), res.Pid)
		return nil
	}

	fmt.Fprintln(os.Stderr, "Computer is still stopping. It may be finishing a running task.")
	fmt.Fprintln(os.Stderr, "Check: multica computer status")
	return nil
}

// --- daemon status ---

func runComputerStatus(cmd *cobra.Command, _ []string) error {
	lc := &computer.Lifecycle{}

	health := lc.Status()

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, health)
	}

	label := "Computer"

	switch health["status"] {
	case "running":
		printDaemonStatusReport(os.Stdout, label, health)
	case "starting":
		fmt.Fprintf(os.Stdout, "%s: starting (pid %v)\n", label, health["pid"])
	default:
		fmt.Fprintf(os.Stdout, "%s: stopped\n", label)
	}
	if connected, _ := health["connected"].(bool); !connected {
		return fmt.Errorf("Computer is disconnected")
	}
	return nil
}

// printDaemonStatusReport renders a key/value summary of the daemon health
// response. The value column is aligned to the widest label so the dynamic
// "Daemon [profile]" row stays in step with the static rows below it.
func printDaemonStatusReport(w io.Writer, label string, health map[string]any) {
	type row struct{ key, value string }
	rows := []row{
		{label, fmt.Sprintf("running (pid %v, uptime %v)", health["pid"], health["uptime"])},
	}
	if id, ok := health["computerId"].(string); ok && id != "" {
		rows = append(rows, row{"Computer ID", id})
	}
	if session, ok := health["session_present"].(bool); ok {
		rows = append(rows, row{"Session", map[bool]string{true: "present", false: "missing"}[session]})
	}
	if environment, ok := health["environment"].(string); ok && environment != "" {
		rows = append(rows, row{"Configured environment", environment})
	}
	if environment, ok := health["resident_environment"].(string); ok && environment != "" {
		rows = append(rows, row{"Resident environment", environment})
	}
	if origin, ok := health["service_origin"].(string); ok && origin != "" {
		rows = append(rows, row{"Configured origin", origin})
	}
	if origin, ok := health["resident_service_origin"].(string); ok && origin != "" {
		rows = append(rows, row{"Resident origin", origin})
	}
	if source, ok := health["package_source"].(string); ok && source != "" {
		rows = append(rows, row{"Configured package", source})
	}
	if source, ok := health["resident_package_source"].(string); ok && source != "" {
		rows = append(rows, row{"Resident package", source})
	}
	if drift, ok := health["configuration_drift"].(bool); ok {
		rows = append(rows, row{"Configuration drift", fmt.Sprint(drift)})
	}
	if connected, ok := health["connected"].(bool); ok {
		rows = append(rows, row{"Connected", fmt.Sprint(connected)})
	}
	if connections, ok := health["workspace_connections"].([]map[string]any); ok {
		rows = append(rows, row{"Workspace connections", strconv.Itoa(len(connections))})
		for i, connection := range connections {
			workspace := fmt.Sprint(connection["workspace_slug"])
			if workspace == "" {
				workspace = fmt.Sprint(connection["workspace_id"])
			} else if id := fmt.Sprint(connection["workspace_id"]); id != "" {
				workspace += " (" + id + ")"
			}
			rows = append(rows, row{
				fmt.Sprintf("Connection %d", i+1),
				fmt.Sprintf("%s / %s", connection["environment"], workspace),
			})
		}
	}
	if version, ok := health["cliVersion"].(string); ok && version != "" {
		rows = append(rows, row{"Version", version})
	}

	keyWidth := 0
	for _, r := range rows {
		if n := len(r.key); n > keyWidth {
			keyWidth = n
		}
	}
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %s\n", keyWidth+1, r.key+":", r.value)
	}
}

// --- daemon logs ---

func runComputerLogs(cmd *cobra.Command, args []string) error {
	lc := &computer.Lifecycle{}

	follow, _ := cmd.Flags().GetBool("follow")
	lines, _ := cmd.Flags().GetInt("lines")

	binding, selected, err := resolveWorkspaceBinding(args)
	if err != nil {
		return err
	}
	if selected {
		return lc.LogsForWorkspace(lines, follow, binding.WorkspaceID)
	}
	return lc.Logs(lines, follow)
}

// flagString returns a string flag value or empty string.
func flagString(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	return val
}

func shellQuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' ||
			r >= '0' && r <= '9' ||
			r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z')
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
