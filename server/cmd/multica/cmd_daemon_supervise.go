package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
)

// superviseEnvVar marks a worker process as running under external
// supervision (task #815), inherited by every generation the supervisor
// spawns. runDaemonForeground checks it to decide how to complete a version
// handoff: exit with daemonHandoffExitCode and let the supervisor resolve
// and start the next generation (supervised), or fall back to spawning the
// target itself as a detached background daemon (standalone — see
// spawnDetachedDaemonBinary).
const superviseEnvVar = "MULTICA_DAEMON_SUPERVISED"

// daemonHandoffExitCode is the sentinel exit code a supervised worker uses
// to signal "restart me, possibly onto a different binary" — a deliberate,
// expected exit, not a crash and not a permanent stop. 75 is sysexits.h's
// EX_TEMPFAIL ("temporary failure, please retry"), which matches the
// intent closely enough to reuse rather than inventing an arbitrary number.
const daemonHandoffExitCode = 75

func runningUnderSupervision() bool {
	return os.Getenv(superviseEnvVar) == "1"
}

// versionEscalationGraceWindow is the supervisor-level backstop: how long the
// running daemon may stay on a version other than the VersionStore's
// committed Active before the external supervisor force-restarts it. This is
// deliberately far longer than the daemon's own internal T_hard
// (stagedUpdateHardDrainTotal, 15m) — the daemon's own graceful path is the
// routine mechanism and should resolve the overwhelming majority of
// upgrades; this is a rarely-hit backstop for a machine that never has a
// large enough idle gap, not a routine restart trigger.
const versionEscalationGraceWindow = 2 * time.Hour

const versionEscalationPollInterval = time.Minute

var daemonSuperviseCmd = &cobra.Command{
	Use:   "supervise",
	Short: "Run the daemon under an external process supervisor",
	Long: "Runs the daemon as a supervised worker process: an OS process manager " +
		"(launchd/systemd/Scheduled Task) keeps this `supervise` process alive, " +
		"and it in turn keeps the daemon alive across crashes and, if the daemon " +
		"stays too busy to ever restart itself onto a newly staged version, force-" +
		"restarts it (task #815). Intended to be registered as a background " +
		"service rather than run interactively.",
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
	f.Bool("no-auto-update", false, "Deprecated no-op; upgrades are explicit Machine Upgrade operations")
	f.Duration("auto-update-interval", 0, "Deprecated no-op; periodic release polling is disabled")

	daemonCmd.AddCommand(daemonSuperviseCmd)
}

// buildSuperviseConfig builds the supervisor.Config for running the daemon as
// a supervised worker under the given profile. workerArgs is normally
// buildDaemonStartArgs(cmd) — the same "daemon start --foreground ..."
// invocation the background-start path already uses, so the supervised
// worker behaves identically to a normal foreground daemon, whichever binary
// ends up running it.
//
// ResolveWorkerPath (not a static WorkerPath) is what makes this land a busy
// machine onto a newly staged version: it's called fresh before every
// generation — the first one, a crash-restart, a force-restart, and a
// handoff-restart alike — so the supervisor never has to be told a path
// across the process boundary; it just re-derives "what's Active right now"
// itself. WorkerEnv marks every spawned generation as supervised, so
// runDaemonForeground's handoff sites know to exit with HandoffExitCode
// rather than spawning a sibling themselves.
func buildSuperviseConfig(profile, exePath string, workerArgs []string, stdout, stderr io.Writer) (supervisor.Config, error) {
	dir := daemonDirForProfile(profile)
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

// resolveSupervisedWorkerPath resolves the VersionStore's committed Active
// version to its staged binary, falling back to fallbackPath (the
// supervise process's own executable) whenever there's nothing valid to
// hand off to yet — no committed Active version, an unreadable store, or a
// staged binary missing from disk (e.g. GC'd). None of those are errors:
// they just mean "run the bootstrap binary for now", which itself re-checks
// on every one of its own generations via resolveVersionHandoffTarget.
func resolveSupervisedWorkerPath(fallbackPath string, workerArgs []string) (string, []string, error) {
	store, err := cli.OpenVersionStore("")
	if err != nil {
		return fallbackPath, workerArgs, nil
	}
	target, err := resolveActiveStagedBinary(store)
	if err != nil || target == "" {
		return fallbackPath, workerArgs, nil
	}
	return target, workerArgs, nil
}

// versionWatcherState tracks how long the running worker's reported version
// has continuously differed from the VersionStore's committed Active
// version, and decides when that divergence has outlasted the grace window.
//
// Zero value is ready to use: no divergence observed yet.
type versionWatcherState struct {
	diverged        bool
	firstDivergedAt time.Time
}

// observe records one poll's (activeVersion, runningVersion) pair at time
// now and reports whether the caller should force a restart.
//
//   - No committed Active version, or Active matches the running version:
//     divergence (if any) is cleared. Never forces.
//   - Active differs from running: starts (or continues) the grace-window
//     clock from the moment divergence was first observed. Forces once now
//     is at least graceWindow past that moment, and keeps forcing on every
//     later observation while still diverged — safe because
//     supervisor.RequestRestart coalesces repeated requests.
//   - Unknown running version (health check unreachable, e.g. mid-restart):
//     a no-op that neither resets nor advances the clock, so a flaky health
//     check can't indefinitely postpone forcing a genuinely stuck daemon.
func (s *versionWatcherState) observe(now time.Time, activeVersion, runningVersion string, graceWindow time.Duration) bool {
	if activeVersion == "" {
		s.diverged = false
		s.firstDivergedAt = time.Time{}
		return false
	}
	if runningVersion == "" {
		if !s.diverged {
			return false
		}
		return now.Sub(s.firstDivergedAt) >= graceWindow
	}
	if handoffVersionsMatch(activeVersion, runningVersion) {
		s.diverged = false
		s.firstDivergedAt = time.Time{}
		return false
	}
	if !s.diverged {
		s.diverged = true
		s.firstDivergedAt = now
		return false
	}
	return now.Sub(s.firstDivergedAt) >= graceWindow
}

// runVersionEscalationWatcher polls the VersionStore's committed Active
// version against the supervised daemon's live reported version and, once
// they've diverged for longer than graceWindow, force-restarts the worker
// via sup.RequestRestart(). This is the task #815 backstop for a daemon that
// stays too busy to ever find a large enough idle gap for its own graceful
// waitForSafeRestartWithWindows to complete (see abandonStagedUpdatePathA):
// the daemon's own graceful path is always tried first and is the routine
// mechanism; this only fires after it has had graceWindow (deliberately far
// longer than the daemon's internal T_hard) to succeed on its own.
//
// A forced restart can kill an in-flight task; that task is recovered via
// the server's existing lease-expiry reclaim
// (ReclaimExpiredAgentInboxDeliveries*, see internal/handler/agent_inbox.go)
// the same way any other daemon crash already is.
func runVersionEscalationWatcher(
	ctx context.Context,
	sup *supervisor.Supervisor,
	store *cli.VersionStore,
	healthPort int,
	pollInterval, graceWindow time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var state versionWatcherState
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			activeVersion := ""
			if st, err := store.ReadActivationState(); err != nil {
				logger.Warn("version escalation watcher: failed to read activation state", "error", err)
			} else {
				activeVersion = st.ActiveVersion
			}

			runningVersion := ""
			hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			health := checkDaemonHealthOnPort(hctx, healthPort)
			cancel()
			if v, ok := health["cli_version"].(string); ok {
				runningVersion = v
			}

			if state.observe(time.Now(), activeVersion, runningVersion, graceWindow) {
				logger.Warn("daemon stuck on stale version past the escalation grace window; forcing restart",
					"active_version", activeVersion, "running_version", runningVersion, "grace_window", graceWindow)
				if err := sup.RequestRestart(); err != nil {
					logger.Error("version escalation watcher: force restart request failed", "error", err)
				}
			}
		}
	}
}

func runDaemonSupervise(cmd *cobra.Command, _ []string) error {
	profile := resolveProfile(cmd)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	dir := daemonDirForProfile(profile)
	if dir == "" {
		return fmt.Errorf("resolve daemon directory for profile %q", profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create daemon directory: %w", err)
	}

	// The supervised worker's stdout/stderr go to the same log file
	// `daemon logs` already reads, so log behavior is unchanged whether or
	// not the daemon happens to be running under supervision.
	logPath := daemonLogPathForProfile(profile)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	cfg, err := buildSuperviseConfig(profile, exePath, buildDaemonStartArgs(cmd), logFile, logFile)
	if err != nil {
		return err
	}

	sup, err := supervisor.New(cfg)
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	logger := logger_pkg.NewLogger("daemon-supervisor")

	// Self-heal OS service unit before the first generation: a pre-#2094
	// handoff can leave systemd ExecStart (or a drop-in) pinned to a deleted
	// version path while VersionStore Active already points at the new
	// binary (s144 0.4.1→0.4.2: unit still nailing v0.4.1). The outgoing
	// binary never ran bestEffortSyncInstalledServiceUnit; the NEW supervise
	// process must rewrite unit → resolved worker path so the next OS
	// restart does not 203/EXEC. Failures must not block start.
	workerPath, _, resolveErr := resolveSupervisedWorkerPath(exePath, buildDaemonStartArgs(cmd))
	if resolveErr != nil {
		workerPath = exePath
	}
	if workerPath == "" {
		workerPath = exePath
	}
	if err := bestEffortSyncInstalledServiceUnit(profile, workerPath); err != nil {
		logger.Warn("could not rewrite OS service unit to Active worker path on supervise start; re-run `multica daemon install-service` if a later OS restart fails",
			"path", workerPath, "error", err)
	}

	logger.Info("starting supervised daemon", "worker_path", workerPath, "profile", profile)

	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()

	if store, err := cli.OpenVersionStore(""); err != nil {
		logger.Warn("version escalation watcher disabled: failed to open version store", "error", err)
	} else {
		go runVersionEscalationWatcher(ctx, sup, store, healthPortForProfile(profile),
			versionEscalationPollInterval, versionEscalationGraceWindow, logger)
	}

	return sup.Run(ctx)
}

// handoffVersionsMatch compares two version strings ignoring an optional "v"
// prefix, matching the loose comparison the daemon's own self-update flow
// already uses (see daemon.versionStringsMatch).
func handoffVersionsMatch(a, b string) bool {
	normalize := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	a, b = normalize(a), normalize(b)
	return a != "" && a == b
}

// resolveActiveStagedBinary resolves the VersionStore's committed Active
// version to its staged binary path, verifying the binary actually exists
// on disk. Returns "" (no error) when there's nothing valid to hand off to:
// no committed Active version, an unresolvable tag, or a staged binary
// missing from disk (e.g. GC'd) — none of these are error conditions.
func resolveActiveStagedBinary(store *cli.VersionStore) (string, error) {
	if store == nil {
		return "", nil
	}
	state, err := store.ReadActivationState()
	if err != nil {
		return "", err
	}
	if state.ActiveVersion == "" {
		return "", nil
	}
	staged, err := store.ResolveStagedVersion(state.ActiveVersion)
	if err != nil {
		// Not a well-formed release tag — nothing sane to hand off to.
		return "", nil
	}
	if _, statErr := os.Stat(staged.BinaryPath); statErr != nil {
		return "", nil
	}
	return staged.BinaryPath, nil
}

// resolveVersionHandoffBinary compares the VersionStore's committed Active
// version against the currently running binary's version. If they differ and
// the Active version's staged binary is actually present on disk, it returns
// that binary's path so the caller can hand off to it before doing any
// daemon work (claiming tasks, holding leases, etc.) — always safe, since a
// process that hasn't started working yet has nothing to drain.
//
// Returns "" (no error) when no handoff is needed or possible: no committed
// Active version yet, Active already matches the running binary, or the
// staged binary is missing (e.g. GC'd) and cannot be handed off to safely.
//
// Used by the standalone (non-supervised) preflight, where "already on
// Active" must be checked explicitly — there's no supervisor re-resolving a
// path on every generation, so this process only wants to actually hand off
// when it needs to.
func resolveVersionHandoffBinary(store *cli.VersionStore, runningVersion string) (string, error) {
	if store == nil {
		return "", nil
	}
	state, err := store.ReadActivationState()
	if err != nil {
		return "", err
	}
	if state.ActiveVersion == "" || handoffVersionsMatch(state.ActiveVersion, runningVersion) {
		return "", nil
	}
	return resolveActiveStagedBinary(store)
}

// resolveVersionHandoffTarget opens the default VersionStore and resolves
// whether this running binary needs to hand off to a different, already-
// staged Active version. Returns "" when no handoff is needed. A VersionStore
// open failure is reported to the caller rather than treated as "no handoff
// needed" — the caller decides whether to warn-and-continue (this must never
// block a daemon from starting).
func resolveVersionHandoffTarget() (string, error) {
	store, err := cli.OpenVersionStore("")
	if err != nil {
		return "", err
	}
	return resolveVersionHandoffBinary(store, version)
}
