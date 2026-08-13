package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
)

// superviseEnvVar marks a worker process as running under external
// supervision (task #815), inherited by every generation the supervisor
// spawns.
const superviseEnvVar = "MULTICA_DAEMON_SUPERVISED"

// daemonHandoffExitCode is the sentinel exit code a supervised worker uses
// to signal "restart me". 75 is sysexits.h EX_TEMPFAIL.
const daemonHandoffExitCode = 75

func runningUnderSupervision() bool {
	return os.Getenv(superviseEnvVar) == "1"
}

var daemonSuperviseCmd = &cobra.Command{
	Use:   "supervise",
	Short: "Run the daemon under an external process supervisor",
	Long: "Runs the daemon as a supervised worker process: an OS process manager " +
		"(launchd/systemd/Scheduled Task) keeps this `supervise` process alive, " +
		"and it in turn keeps the daemon alive across crashes. Intended to be " +
		"registered as a background service rather than run interactively.",
	RunE: runDaemonSupervise,
}

func init() {
	f := daemonSuperviseCmd.Flags()
	f.String("daemon-id", "", "Unique daemon identifier (env: MULTICA_DAEMON_ID)")
	f.String("device-name", "", "Human-readable device name (env: MULTICA_DAEMON_DEVICE_NAME)")
	f.String("runtime-name", "", "Runtime display name (env: MULTICA_AGENT_RUNTIME_NAME)")
	f.Duration("poll-interval", 0, "Task poll interval (env: MULTICA_DAEMON_POLL_INTERVAL)")
	f.Duration("heartbeat-interval", 0, "Heartbeat interval (env: MULTICA_DAEMON_HEARTBEAT_INTERVAL)")
	f.Duration("agent-timeout", 0, "Absolute per-task wall-clock cap; 0 = no cap, rely on the watchdogs (env: MULTICA_AGENT_TIMEOUT)")
	f.Duration("codex-semantic-inactivity-timeout", 0, "Codex semantic inactivity timeout (env: MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT)")
	f.Bool("no-auto-update", false, "Deprecated no-op; automatic installation is disabled and release detection remains active")
	f.Duration("auto-update-interval", 0, "Release detection interval (default 5m; detection never installs automatically)")
}

func buildSuperviseConfig(profile, exePath string, workerArgs []string, stdout, stderr io.Writer) (supervisor.Config, error) {
	dir := computer.RootDir(profile)
	if dir == "" {
		return supervisor.Config{}, fmt.Errorf("resolve daemon directory for profile %q", profile)
	}
	return supervisor.Config{
		LockPath: filepath.Join(dir, "supervisor.lock"),
		ResolveWorkerPath: func() (string, []string, error) {
			return resolveSupervisedWorkerPath(exePath, workerArgs)
		},
		WorkerEnv:       append(append([]string(nil), os.Environ()...), superviseEnvVar+"=1"),
		HandoffExitCode: daemonHandoffExitCode,
		Stdout:          stdout,
		Stderr:          stderr,
	}, nil
}

func resolveSupervisedWorkerPath(fallbackPath string, workerArgs []string) (string, []string, error) {
	return fallbackPath, workerArgs, nil
}

func runDaemonSupervise(cmd *cobra.Command, _ []string) error {
	profile := resolveProfile(cmd)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	dir := computer.RootDir(profile)
	if dir == "" {
		return fmt.Errorf("resolve daemon directory for profile %q", profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create daemon directory: %w", err)
	}

	logPath := computer.LogPath(profile)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	cfg, err := buildSuperviseConfig(profile, exePath, computer.ResidentArgs(computerStartOptions(cmd)), logFile, logFile)
	if err != nil {
		return err
	}

	sup, err := supervisor.New(cfg)
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	logger := logger_pkg.NewLogger("daemon-supervisor")
	if err := bestEffortSyncInstalledServiceUnit(profile, exePath); err != nil {
		logger.Warn("could not rewrite OS service unit on supervise start; re-run `multica daemon install-service` if a later OS restart fails",
			"path", exePath, "error", err)
	}

	logger.Info("starting supervised daemon", "worker_path", exePath, "profile", profile)

	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()

	return sup.Run(ctx)
}
