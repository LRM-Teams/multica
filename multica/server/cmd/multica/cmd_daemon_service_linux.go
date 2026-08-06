//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func init() {
	platformServiceInstaller = linuxServiceInstaller{}
}

// linuxServiceInstaller registers `multica daemon supervise` as a systemd
// --user unit (~/.config/systemd/user), enabled and started via `systemctl
// --user enable --now`. This is a per-user, login-session-scoped install: it
// starts when the user's systemd --user instance starts (normally at first
// login) and stops when their last session ends, unless the user has also
// run `loginctl enable-linger` for their own account — that's a distinct,
// separate opt-in this command deliberately does not perform (it edits
// /var/lib/systemd/linger and, depending on polkit config, can require a
// privilege prompt; out of scope for a "no special privileges" per-user
// install). A genuinely headless server with linger disabled and no
// interactive login will not run this service — the same known gap called
// out in cmd_daemon_service.go's --help text.
type linuxServiceInstaller struct{}

func systemdUnitName(profile string) string {
	if profile == "" {
		return "multica-daemon.service"
	}
	return "multica-daemon-" + profile + ".service"
}

func systemdUserUnitPath(profile string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdUnitName(profile)), nil
}

const systemdUnitTemplate = `[Unit]
Description=Multica daemon supervisor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.ExecStart}}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`

func (linuxServiceInstaller) Install(profile, exePath string, args []string) error {
	unitPath, err := systemdUserUnitPath(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create systemd user unit directory: %w", err)
	}

	execStart := shellQuoteArg(exePath)
	for _, a := range args {
		execStart += " " + shellQuoteArg(a)
	}

	tmpl, err := template.New("unit").Parse(systemdUnitTemplate)
	if err != nil {
		return fmt.Errorf("parse unit template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ ExecStart string }{ExecStart: execStart}); err != nil {
		return fmt.Errorf("render unit: %w", err)
	}
	if err := os.WriteFile(unitPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write unit %s: %w", unitPath, err)
	}

	unit := systemdUnitName(profile)
	// daemon-reload picks up the (possibly changed) unit file content before
	// enable/restart act on it — required for idempotent reinstall to
	// actually apply an updated ExecStart, not just restart the old one.
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user enable failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// restart (not start): idempotent whether or not it was already running,
	// and picks up a changed ExecStart on a reinstall rather than leaving
	// the old process running against the new unit file.
	if out, err := exec.Command("systemctl", "--user", "restart", unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user restart failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (linuxServiceInstaller) Uninstall(profile string) error {
	unit := systemdUnitName(profile)
	// stop/disable errors are expected and ignored when the unit was never
	// installed or is already stopped; the unit file removal below is what
	// must actually succeed (or already be absent).
	_ = exec.Command("systemctl", "--user", "stop", unit).Run()
	_ = exec.Command("systemctl", "--user", "disable", unit).Run()

	unitPath, err := systemdUserUnitPath(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file %s: %w", unitPath, err)
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func (linuxServiceInstaller) Status(profile string) (registered, running bool, detail string, err error) {
	unitPath, perr := systemdUserUnitPath(profile)
	if perr != nil {
		return false, false, "", perr
	}
	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		return false, false, "not installed", nil
	}

	unit := systemdUnitName(profile)
	activeOut, _ := exec.Command("systemctl", "--user", "is-active", unit).Output()
	active := strings.TrimSpace(string(activeOut))
	enabledOut, _ := exec.Command("systemctl", "--user", "is-enabled", unit).Output()
	enabled := strings.TrimSpace(string(enabledOut))

	return true, active == "active", fmt.Sprintf("active=%s enabled=%s", active, enabled), nil
}
