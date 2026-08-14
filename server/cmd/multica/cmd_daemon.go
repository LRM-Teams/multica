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

var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Control the local agent runtime daemon",
	Hidden: true, // compatibility alias for the machine-wide Computer (#2487)
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// The hidden compatibility spelling must still target the one Computer;
		// accepting a profile/custom server here would recreate the retired
		// second-resident model behind an invisible flag.
		return rejectRetiredComputerFlags(cmd)
	},
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the local agent runtime daemon",
	Long:  "Start the daemon process that polls for tasks and executes them using local agent CLIs (Claude, Codex).\nRuns in the background by default. Use --foreground to run in the current terminal.",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE:  runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE:  runDaemonStatus,
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the running daemon (stop + start)",
	RunE:  runDaemonRestart,
}

var daemonLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show daemon logs",
	RunE:  runDaemonLogs,
}

func init() {
	f := daemonStartCmd.Flags()
	f.Bool("foreground", false, "Run in the foreground instead of background")
	f.String("daemon-id", "", "Unique daemon identifier (env: MULTICA_DAEMON_ID)")
	f.String("device-name", "", "Human-readable device name (env: MULTICA_DAEMON_DEVICE_NAME)")
	f.String("runtime-name", "", "Runtime display name (env: MULTICA_AGENT_RUNTIME_NAME)")
	f.Duration("poll-interval", 0, "Task poll interval (env: MULTICA_DAEMON_POLL_INTERVAL)")
	f.Duration("heartbeat-interval", 0, "Heartbeat interval (env: MULTICA_DAEMON_HEARTBEAT_INTERVAL)")
	f.Duration("agent-timeout", 0, "Absolute per-task wall-clock cap; 0 = no cap, rely on the watchdogs (env: MULTICA_AGENT_TIMEOUT)")
	f.Duration("codex-semantic-inactivity-timeout", 0, "Codex semantic inactivity timeout (env: MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT)")
	f.Bool("no-auto-update", false, "Deprecated no-op; automatic installation is disabled and release detection remains active")
	f.Duration("auto-update-interval", 0, "Release detection interval (default 5m; detection never installs automatically)")
	f.Int64("computer-generation", 0, "Internal machine-wide Computer generation")
	_ = f.MarkHidden("computer-generation")
	f.Int("machine-attestation-source-pid", 0, "Incumbent PID this successor replaced")
	_ = f.MarkHidden("machine-attestation-source-pid")

	daemonLogsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	daemonLogsCmd.Flags().IntP("lines", "n", 50, "Number of lines to show")

	daemonStatusCmd.Flags().String("output", "table", "Output format: table or json")

	rf := daemonRestartCmd.Flags()
	rf.Bool("foreground", false, "Run in the foreground instead of background")
	rf.String("daemon-id", "", "Unique daemon identifier (env: MULTICA_DAEMON_ID)")
	rf.String("device-name", "", "Human-readable device name (env: MULTICA_DAEMON_DEVICE_NAME)")
	rf.String("runtime-name", "", "Runtime display name (env: MULTICA_AGENT_RUNTIME_NAME)")
	rf.Duration("poll-interval", 0, "Task poll interval (env: MULTICA_DAEMON_POLL_INTERVAL)")
	rf.Duration("heartbeat-interval", 0, "Heartbeat interval (env: MULTICA_DAEMON_HEARTBEAT_INTERVAL)")
	rf.Duration("agent-timeout", 0, "Absolute per-task wall-clock cap; 0 = no cap, rely on the watchdogs (env: MULTICA_AGENT_TIMEOUT)")
	rf.Duration("codex-semantic-inactivity-timeout", 0, "Codex semantic inactivity timeout (env: MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT)")
	rf.Bool("no-auto-update", false, "Deprecated no-op; automatic installation is disabled and release detection remains active")
	rf.Duration("auto-update-interval", 0, "Release detection interval (default 5m; detection never installs automatically)")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonLogsCmd)
}

// daemonDeprecatedAliasNotices prints the deprecation guidance for the hidden
// `multica daemon ...` lifecycle alias, which now delegates to the same
// machine-wide Computer. Not printed when running as `multica computer ...`.
func daemonDeprecatedAliasNotices() {
	if computerMode {
		return
	}
	fmt.Fprintln(os.Stderr, "Note: `multica daemon ...` is deprecated and will be removed in a future release. Use `multica computer ...` instead.")
}

// --- daemon start ---

func runDaemonStart(cmd *cobra.Command, args []string) error {
	daemonDeprecatedAliasNotices()
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
		if health["status"] == "running" && health["connected"] == true && normalizeAPIBaseURL(fmt.Sprint(health["server_url"])) == normalizeAPIBaseURL(target.Origin) && healthContainsWorkspace(health, workspaceID) {
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

func runDaemonRestart(cmd *cobra.Command, args []string) error {
	daemonDeprecatedAliasNotices()
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

func runDaemonStop(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
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

func runDaemonStatus(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
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
	if id, ok := health["computer_id"].(string); ok && id != "" {
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
	if version, ok := health["cli_version"].(string); ok && version != "" {
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

func runDaemonLogs(cmd *cobra.Command, args []string) error {
	daemonDeprecatedAliasNotices()
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
