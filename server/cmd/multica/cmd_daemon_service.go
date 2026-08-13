package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/computer"
)

// daemonServiceInstaller registers `multica daemon supervise` with the OS's
// per-user service manager so it starts automatically at login and is
// restarted by the OS if it exits — task #11, the other half of the "zero
// manual steps" goal #41 started (a restart can now pick up a staged
// update; this means the machine doesn't need a human to press restart at
// all). Implemented per-platform: LaunchAgent (darwin), systemd --user
// (linux), a logon Scheduled Task (windows). All three are per-user
// installs requiring no root/admin privileges, matching #680's "no sudo"
// direction. System-wide (boots without any user logged in) is a distinct,
// deliberately out-of-scope tier for this task — see the package doc
// comment on the per-platform implementations for the known gap on headless
// servers with no interactive login session.
type daemonServiceInstaller interface {
	// Install writes/overwrites the service definition, registers it with
	// the OS service manager, and starts it. Must be idempotent: calling
	// Install twice (e.g. after a binary path changes) must cleanly replace
	// the prior registration rather than erroring or double-registering.
	Install(profile, exePath string, args []string) error
	// Uninstall stops the service if running and removes its registration.
	// Must not error if the service was never installed.
	Uninstall(profile string) error
	// Status reports whether the service is registered with the OS service
	// manager and, separately, whether it is actually running. These can
	// differ — e.g. registered but the process crashed and the manager
	// hasn't relaunched it yet — so callers must check both, not just one.
	Status(profile string) (registered, running bool, detail string, err error)
}

// platformServiceInstaller is set by exactly one of cmd_daemon_service_darwin.go,
// cmd_daemon_service_linux.go, or cmd_daemon_service_windows.go's init(), per
// the build-tagged file the compiler selects for the target OS.
var platformServiceInstaller daemonServiceInstaller

// daemonServiceUnitSyncer is an optional capability on platform installers that
// can rewrite the OS service definition to a new binary path without restarting
// the running process. Linux implements this (systemd unit + clear ExecStart
// drop-ins) so a post-update handoff does not leave systemd pinned to a deleted
// versioned path. Other platforms may omit it.
type daemonServiceUnitSyncer interface {
	SyncUnit(profile, exePath string, args []string) error
}

// bestEffortSyncInstalledServiceUnit rewrites the installed service to exePath
// when the platform supports SyncUnit and a unit is present. Failures are
// returned for the caller to log; they must not block the update handoff.
func bestEffortSyncInstalledServiceUnit(profile, exePath string) error {
	if platformServiceInstaller == nil {
		return nil
	}
	syncer, ok := platformServiceInstaller.(daemonServiceUnitSyncer)
	if !ok {
		return nil
	}
	return syncer.SyncUnit(profile, exePath, buildSuperviseServiceArgs(profile))
}

var daemonInstallServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Install the daemon as a per-user OS service (starts automatically at login)",
	Long: "Registers `multica daemon supervise` with the OS's per-user service manager " +
		"(LaunchAgent on macOS, systemd --user on Linux, a logon Scheduled Task on Windows) " +
		"so it starts automatically when you log in and is restarted by the OS if it exits. " +
		"Requires no administrator/root privileges. Re-running replaces an existing " +
		"installation cleanly (e.g. after the CLI moved to a new install path).\n\n" +
		"Known gap: this is a per-user install tied to an interactive login session. " +
		"A headless server with no one ever logging in locally will not start the " +
		"service this way — that needs a system-level install, which is a separate, " +
		"not-yet-implemented tier.",
	RunE: runDaemonInstallService,
}

var daemonUninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Remove the per-user OS service installed by install-service",
	RunE:  runDaemonUninstallService,
}

var daemonServiceStatusCmd = &cobra.Command{
	Use:   "service-status",
	Short: "Show whether the per-user OS service is installed and running",
	RunE:  runDaemonServiceStatus,
}

func init() {
	// OS-service commands are intentionally not registered. The Computer is a
	// detached resident and does not promise login/reboot autostart (#2496).
}

// buildSuperviseServiceArgs builds the argv the OS service manager should
// launch: the supervise subcommand plus a forwarded --profile, mirroring how
// buildDaemonStartArgs forwards flags for the background-start path. The
// service always launches the plain `daemon supervise` invocation — any
// other daemon flags (poll interval, agent timeout, etc.) are picked up from
// environment/config the same way a manually-run supervise process already
// reads them, so the installed service doesn't need to duplicate every flag.
func buildSuperviseServiceArgs(profile string) []string {
	args := []string{"daemon", "supervise"}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func runDaemonInstallService(cmd *cobra.Command, _ []string) error {
	if platformServiceInstaller == nil {
		return fmt.Errorf("service install is not supported on this platform")
	}
	profile := resolveProfile(cmd)

	// Point the OS service at the on-PATH Computer. Upgrade replaces that
	// same file, so the unit must not pin a versions/<tag> path.
	exePath, err := computer.LaunchBinary()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	args := buildSuperviseServiceArgs(profile)
	if err := platformServiceInstaller.Install(profile, exePath, args); err != nil {
		return fmt.Errorf("install service: %w", err)
	}

	registered, running, detail, err := platformServiceInstaller.Status(profile)
	if err != nil {
		return fmt.Errorf("verify service after install: %w", err)
	}
	if !registered {
		return fmt.Errorf("service install reported success but the service is not registered with the OS (%s)", detail)
	}
	if !running {
		return fmt.Errorf("service is registered but not running after install (%s) — check the supervisor log", detail)
	}

	fmt.Fprintf(os.Stderr, "Service installed and running (%s).\n", detail)
	return nil
}

func runDaemonUninstallService(cmd *cobra.Command, _ []string) error {
	if platformServiceInstaller == nil {
		return fmt.Errorf("service install is not supported on this platform")
	}
	profile := resolveProfile(cmd)
	if err := platformServiceInstaller.Uninstall(profile); err != nil {
		return fmt.Errorf("uninstall service: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Service uninstalled.")
	return nil
}

func runDaemonServiceStatus(cmd *cobra.Command, _ []string) error {
	if platformServiceInstaller == nil {
		fmt.Fprintln(os.Stdout, "Service: not supported on this platform")
		return nil
	}
	profile := resolveProfile(cmd)
	registered, running, detail, err := platformServiceInstaller.Status(profile)
	if err != nil {
		return fmt.Errorf("check service status: %w", err)
	}
	switch {
	case registered && running:
		fmt.Fprintf(os.Stdout, "Service: registered and running (%s)\n", detail)
	case registered && !running:
		fmt.Fprintf(os.Stdout, "Service: registered but NOT running (%s)\n", detail)
	default:
		fmt.Fprintln(os.Stdout, "Service: not installed")
	}
	return nil
}
