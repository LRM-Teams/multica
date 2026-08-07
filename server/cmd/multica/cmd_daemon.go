package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Control the local agent runtime daemon",
	Hidden: true, // compatibility alias for the machine-wide Computer (#2487)
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
	f.Bool("no-auto-update", false, "Deprecated no-op; upgrades are explicit Machine Upgrade operations")
	f.Duration("auto-update-interval", 0, "Deprecated no-op; periodic release polling is disabled")

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
	rf.Bool("no-auto-update", false, "Deprecated no-op; upgrades are explicit Machine Upgrade operations")
	rf.Duration("auto-update-interval", 0, "Deprecated no-op; periodic release polling is disabled")

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

func runDaemonStart(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
	foreground, _ := cmd.Flags().GetBool("foreground")
	if foreground {
		return runDaemonForeground(cmd)
	}
	return runDaemonBackground(cmd)
}

func runDaemonBackground(cmd *cobra.Command) error {
	profile := resolveProfile(cmd)
	lc := &computer.Lifecycle{Profile: profile}

	// Check if the Computer is already running.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := computer.ProbeHealth(ctx, computer.HealthPort(profile))
	if computer.Alive(health) {
		label := "daemon"
		if profile != "" {
			label = fmt.Sprintf("daemon [%s]", profile)
		}
		pid, _ := health["pid"].(float64)
		return fmt.Errorf("%s is already running (pid %v). Use 'daemon restart' to restart it", label, int(pid))
	}

	// Build child args: daemon start --foreground + forwarded flags.
	args := buildDaemonStartArgs(cmd)

	res, err := lc.StartBackground(args)
	if err != nil {
		return err
	}

	if res.Started {
		if profile != "" {
			fmt.Fprintf(os.Stderr, "Daemon [%s] started (pid %d, version %s)\n", profile, res.Pid, version)
		} else {
			fmt.Fprintf(os.Stderr, "Daemon started (pid %d, version %s)\n", res.Pid, version)
		}
		fmt.Fprintf(os.Stderr, "Logs: %s\n", res.LogPath)
		return nil
	}

	// Not ready within the startup window.
	if res.LastStatus == "starting" {
		fmt.Fprintf(os.Stderr, "Daemon is still starting after %s (agent detection / workspace sync is taking longer than expected). Check logs:\n  %s\n", computer.StartupTimeout, res.LogPath)
	} else {
		fmt.Fprintf(os.Stderr, "Daemon may not have started successfully. Check logs:\n  %s\n", res.LogPath)
	}
	return nil
}

// buildDaemonStartArgs constructs args for the background child process.
func buildDaemonStartArgs(cmd *cobra.Command) []string {
	args := []string{"daemon", "start", "--foreground"}

	if v := flagString(cmd, "daemon-id"); v != "" {
		args = append(args, "--daemon-id", v)
	}
	if v := flagString(cmd, "device-name"); v != "" {
		args = append(args, "--device-name", v)
	}
	if v := flagString(cmd, "runtime-name"); v != "" {
		args = append(args, "--runtime-name", v)
	}
	if d, _ := cmd.Flags().GetDuration("poll-interval"); d > 0 {
		args = append(args, "--poll-interval", d.String())
	}
	if d, _ := cmd.Flags().GetDuration("heartbeat-interval"); d > 0 {
		args = append(args, "--heartbeat-interval", d.String())
	}
	// Forward agent-timeout when explicitly set, including an explicit 0
	// (= no cap), so it can override an environment MULTICA_AGENT_TIMEOUT.
	if cmd.Flags().Changed("agent-timeout") {
		d, _ := cmd.Flags().GetDuration("agent-timeout")
		args = append(args, "--agent-timeout", d.String())
	}
	if d, _ := cmd.Flags().GetDuration("codex-semantic-inactivity-timeout"); d > 0 {
		args = append(args, "--codex-semantic-inactivity-timeout", d.String())
	}
	if b, _ := cmd.Flags().GetBool("no-auto-update"); b {
		args = append(args, "--no-auto-update")
	}
	if d, _ := cmd.Flags().GetDuration("auto-update-interval"); d > 0 {
		args = append(args, "--auto-update-interval", d.String())
	}

	// Forward global persistent flags.
	if v, _ := cmd.Flags().GetString("server-url"); v != "" {
		args = append(args, "--server-url", v)
	}
	if v := resolveProfile(cmd); v != "" {
		args = append(args, "--profile", v)
	}

	return args
}

func runDaemonForeground(cmd *cobra.Command) error {
	util.EnsureHiddenConsole()

	profile := resolveProfile(cmd)

	// Preflight (task #815): every freshly started worker generation checks
	// whether it's already on the VersionStore's committed Active version.
	// No task has been claimed yet at this point, so handing off here is
	// always safe: nothing to drain.
	if target, err := resolveVersionHandoffTarget(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: version handoff check failed, continuing on running binary: %v\n", err)
	} else if target != "" {
		if runningUnderSupervision() {
			// The external supervisor (task #815) is watching for exactly
			// this exit code and re-resolves which binary to run next
			// itself — see buildSuperviseConfig's ResolveWorkerPath. No
			// task has been claimed yet, so this is always safe: nothing to
			// drain.
			os.Exit(daemonHandoffExitCode)
		}
		// Outside supervision, nothing is watching to catch a worker that
		// exits and doesn't come back — spawning a replacement unattended is
		// worse than just staying on the current binary and saying so
		// (Parker's call, task #815). Fall through and start normally.
		fmt.Fprintf(os.Stderr, "Note: a newer version is staged (%s). Run `multica daemon restart` to apply it.\n", target)
	}

	serverURL := cli.FlagOrEnv(cmd, "server-url", "MULTICA_SERVER_URL", "")
	if serverURL == "" {
		if c, err := cli.LoadCLIConfigForProfile(profile); err == nil && c.ServerURL != "" {
			serverURL = c.ServerURL
		}
	}
	overrides := daemon.Overrides{
		ServerURL:   serverURL,
		DaemonID:    flagString(cmd, "daemon-id"),
		DeviceName:  flagString(cmd, "device-name"),
		RuntimeName: flagString(cmd, "runtime-name"),
		Profile:     profile,
		HealthPort:  computer.HealthPort(profile),
	}
	if d, _ := cmd.Flags().GetDuration("poll-interval"); d > 0 {
		overrides.PollInterval = d
	}
	if d, _ := cmd.Flags().GetDuration("heartbeat-interval"); d > 0 {
		overrides.HeartbeatInterval = d
	}
	// Distinguish "flag not passed" from an explicit `--agent-timeout 0` so a
	// user can turn off an env-configured cap from the CLI.
	if cmd.Flags().Changed("agent-timeout") {
		d, _ := cmd.Flags().GetDuration("agent-timeout")
		overrides.AgentTimeout = &d
	}
	if d, _ := cmd.Flags().GetDuration("codex-semantic-inactivity-timeout"); d > 0 {
		overrides.CodexSemanticInactivityTimeout = d
	}
	if b, _ := cmd.Flags().GetBool("no-auto-update"); b {
		overrides.DisableAutoUpdate = true
	}
	if d, _ := cmd.Flags().GetDuration("auto-update-interval"); d > 0 {
		overrides.AutoUpdateCheckInterval = d
	}

	cfg, err := daemon.LoadConfig(overrides)
	if err != nil {
		return err
	}
	cfg.CLIVersion = version
	controlToken, err := ensureMachineUpgradeControlToken(profile)
	if err != nil {
		return err
	}
	cfg.LocalControlToken = controlToken
	// Set by the Electron Desktop app when it spawns the CLI so the server
	// can mark those runtimes as "managed" and hide CLI self-update UI.
	cfg.LaunchedBy = os.Getenv("MULTICA_LAUNCHED_BY")

	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()

	logger := logger_pkg.NewLogger("daemon")
	d := daemon.New(cfg, logger)

	// Write PID file so "daemon stop" can find us. Best-effort: a resident
	// whose state directory cannot be created simply runs without a PID file,
	// exactly as before.
	lc := &computer.Lifecycle{Profile: profile}
	cleanupPID := func() {}
	if computer.RootDir(profile) != "" {
		cleanup, err := lc.PublishPID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
		} else {
			cleanupPID = cleanup
		}
	}
	defer cleanupPID()

	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	// Check if the daemon needs to restart after a CLI update.
	if restartBin := d.RestartBinary(); restartBin != "" {
		// Point the OS service unit at the staged Active binary before we exit
		// (and before any later systemd restart). Without this, install-service
		// may still have ExecStart=/…/versions/vOLD/… after vOLD is deleted,
		// and systemd returns 203/EXEC in a crash-loop (s144 2026-08-04).
		if err := bestEffortSyncInstalledServiceUnit(profile, restartBin); err != nil {
			logger.Warn("could not rewrite OS service unit to staged binary; re-run `multica daemon install-service` if the next OS restart fails",
				"path", restartBin, "error", err)
		}
		if runningUnderSupervision() {
			logger.Info("restarting daemon with updated binary via supervisor handoff", "path", restartBin)
			// Runtimes were already deregistered by triggerRestart() before
			// handoff. The supervisor-spawned successor re-registers on
			// startup; do not duplicate cleanup here.
			os.Exit(daemonHandoffExitCode)
		}
		// A standalone Machine Upgrade cannot end at stage-and-stop. Wait for
		// the incumbent's control listener to disappear, then launch the exact
		// committed binary as a detached foreground daemon. Its startup bind is
		// the local exclusive-ownership gate; server-side completion still waits
		// for the same journal generation plus every accepted runtime to attest.
		if err := spawnDetachedDaemonBinary(restartBin, profile, d.MachineUpgradeTarget()); err != nil {
			if rollbackStateErr := d.BeginMachineUpgradeRollback(err); rollbackStateErr != nil {
				return fmt.Errorf("record detached takeover rollback: %w", rollbackStateErr)
			}
			if recoveryErr := rollbackDetachedMachineUpgrade(profile, d); recoveryErr != nil {
				return fmt.Errorf("start detached machine-upgrade successor: %w; rollback recovery: %v", err, recoveryErr)
			}
			return fmt.Errorf("start detached machine-upgrade successor: %w; previous Active generation restored", err)
		}
		logger.Info("started detached machine-upgrade successor", "path", restartBin)
	}

	return nil
}

func rollbackDetachedMachineUpgrade(profile string, d *daemon.Daemon) error {
	if d == nil {
		return errors.New("daemon is required for detached rollback")
	}
	store, err := cli.OpenVersionStore("")
	if err != nil {
		return fmt.Errorf("open version store: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, _, err := store.RollbackToPreviousActive(ctx, "machine-upgrade-rollback")
	if err != nil {
		return fmt.Errorf("restore previous Active: %w", err)
	}
	incumbent, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve incumbent binary: %w", err)
	}
	if err := spawnDetachedDaemonBinary(incumbent, profile, state.ActiveVersion); err != nil {
		return fmt.Errorf("start restored incumbent: %w", err)
	}
	return nil
}

// --- daemon restart ---

func runDaemonRestart(cmd *cobra.Command, args []string) error {
	daemonDeprecatedAliasNotices()
	profile := resolveProfile(cmd)
	lc := &computer.Lifecycle{Profile: profile}

	// Stop if running (graceful shutdown with kill fallback), then start fresh.
	res := lc.Stop()
	if res.Err != nil {
		return res.Err
	}
	if res.Running && res.Pid > 0 {
		fmt.Fprintf(os.Stderr, "Stopping daemon (pid %d)...\n", res.Pid)
	}

	return runDaemonStart(cmd, args)
}

// --- daemon stop ---

func runDaemonStop(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
	profile := resolveProfile(cmd)
	lc := &computer.Lifecycle{Profile: profile}

	label := "Daemon"
	if profile != "" {
		label = fmt.Sprintf("Daemon [%s]", profile)
	}

	res := lc.Stop()
	if !res.Running {
		fmt.Fprintf(os.Stderr, "%s is not running.\n", label)
		return nil
	}
	if res.Err != nil {
		return res.Err
	}
	if res.Stopped {
		fmt.Fprintf(os.Stderr, "Stopping daemon (pid %d)...\n", res.Pid)
		fmt.Fprintln(os.Stderr, "Daemon stopped.")
		return nil
	}

	fmt.Fprintln(os.Stderr, "Daemon is still stopping. It may be finishing a running task.")
	return nil
}

// --- daemon status ---

func runDaemonStatus(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
	profile := resolveProfile(cmd)
	lc := &computer.Lifecycle{Profile: profile}

	health := lc.Status()

	// Report the configured Workspace Bindings as part of truthful status.
	if bs, err := computer.NewBindingsStore(computer.RootDir("")).AllActive(); err == nil {
		health["bindings"] = len(bs)
	}

	// Surface the Computer's state layout so a Desktop-facing adapter can
	// learn the health port and log path from the module instead of
	// duplicating the layout rules.
	health["health_port"] = computer.HealthPort(profile)
	health["log_path"] = computer.LogPath(profile)
	health["pid_path"] = computer.PIDPath(profile)

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, health)
	}

	label := "Daemon"
	if profile != "" {
		label = fmt.Sprintf("Daemon [%s]", profile)
	}

	switch health["status"] {
	case "running":
		printDaemonStatusReport(os.Stdout, label, health)
	case "starting":
		fmt.Fprintf(os.Stdout, "%s: starting (pid %v)\n", label, health["pid"])
	default:
		fmt.Fprintf(os.Stdout, "%s: stopped\n", label)
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
	if version, ok := health["cli_version"].(string); ok && version != "" {
		rows = append(rows, row{"Version", version})
	}
	if agents, ok := health["agents"].([]any); ok && len(agents) > 0 {
		parts := make([]string, len(agents))
		for i, a := range agents {
			parts[i] = fmt.Sprint(a)
		}
		rows = append(rows, row{"Agents", strings.Join(parts, ", ")})
	}
	if ws, ok := health["workspaces"].([]any); ok {
		rows = append(rows, row{"Workspaces", strconv.Itoa(len(ws))})
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

func runDaemonLogs(cmd *cobra.Command, _ []string) error {
	daemonDeprecatedAliasNotices()
	profile := resolveProfile(cmd)
	lc := &computer.Lifecycle{Profile: profile}

	follow, _ := cmd.Flags().GetBool("follow")
	lines, _ := cmd.Flags().GetInt("lines")

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
