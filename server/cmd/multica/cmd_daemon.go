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
			return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout)
		}
	}
	foreground, _ := cmd.Flags().GetBool("foreground")
	if foreground {
		return runComputerResident(cmd, args)
	}
	if err := runDaemonBackground(cmd); err != nil {
		return err
	}
	if selected {
		return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout)
	}
	return nil
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
	res, err := lc.StartBackground(computerStartOptions(cmd))
	if err != nil {
		return err
	}
	return printComputerStartResult(res)
}

func printComputerStartResult(res computer.StartResult) error {
	if res.Started {
		fmt.Fprintf(os.Stderr, "Computer started (pid %d, version %s)\n", res.Pid, version)
		fmt.Fprintf(os.Stderr, "Logs: %s\n", res.LogPath)
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
	result, err := lc.Restart(computerStartOptions(cmd))
	if err != nil {
		return err
	}
	if result.Stop.Running && result.Stop.Pid > 0 {
		fmt.Fprintf(os.Stderr, "Stopping Computer (pid %d)...\n", result.Stop.Pid)
	}
	if err := printComputerStartResult(result.Start); err != nil {
		return err
	}
	if selected {
		return waitForWorkspaceReady(binding.WorkspaceID, computer.StartupTimeout)
	}
	return nil
}

// --- daemon stop ---

func runComputerStop(cmd *cobra.Command, _ []string) error {
	lc := &computer.Lifecycle{}
	label := "Computer"

	res := lc.Stop()
	if !res.Running {
		fmt.Fprintf(os.Stderr, "%s is not running.\n", label)
		return nil
	}
	if res.Err != nil {
		return res.Err
	}
	if res.Stopped {
		fmt.Fprintf(os.Stderr, "Stopping Computer (pid %d)...\n", res.Pid)
		fmt.Fprintln(os.Stderr, "Computer stopped.")
		return nil
	}

	fmt.Fprintln(os.Stderr, "Computer is still stopping. It may be finishing a running task.")
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
