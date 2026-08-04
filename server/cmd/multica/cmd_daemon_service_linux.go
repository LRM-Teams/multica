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

// systemdUserDropInDir is ~/.config/systemd/user/<unit>.d — drop-ins here
// merge over the main unit and silently win for keys like ExecStart.
func systemdUserDropInDir(profile string) (string, error) {
	unitPath, err := systemdUserUnitPath(profile)
	if err != nil {
		return "", err
	}
	return unitPath + ".d", nil
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

// dropInOverridesExecStart reports whether a systemd drop-in fragment
// redefines ExecStart. Systemd merges drop-ins over the main unit, so a
// stale ExecStart= here (e.g. after a versioned binary path was deleted)
// produces status=203/EXEC crash-loops even when the main unit was rewritten
// by install-service. Matching is line-oriented and case-insensitive on the
// key, matching systemd's unit parser for this assignment form.
func dropInOverridesExecStart(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "ExecStart") {
			return true
		}
	}
	return false
}

// clearSystemdExecStartDropIns removes any drop-in fragments under
// <unit>.d that override ExecStart, preserving unrelated drop-ins
// (Environment=, LimitNOFILE=, etc.). Empty .d dirs are removed.
// homeRoot is the systemd user config root (.../systemd/user); when empty,
// the live user path is resolved. Exposed for tests with a temp HOME tree.
func clearSystemdExecStartDropIns(dropInDir string) (removed []string, err error) {
	entries, err := os.ReadDir(dropInDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read drop-in dir %s: %w", dropInDir, err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		// systemd only loads *.conf drop-ins.
		if !strings.HasSuffix(ent.Name(), ".conf") {
			continue
		}
		path := filepath.Join(dropInDir, ent.Name())
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return removed, fmt.Errorf("read drop-in %s: %w", path, readErr)
		}
		if !dropInOverridesExecStart(body) {
			continue
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return removed, fmt.Errorf("remove ExecStart drop-in %s: %w", path, rmErr)
		}
		removed = append(removed, path)
	}
	// Best-effort: drop empty .d so reinstalls don't leave a husk.
	if remaining, _ := os.ReadDir(dropInDir); len(remaining) == 0 {
		_ = os.Remove(dropInDir)
	}
	return removed, nil
}

func writeSystemdUnitFile(unitPath, exePath string, args []string) error {
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
	return nil
}

// syncSystemdUnitFile rewrites the main unit to exePath and clears any
// ExecStart-pinning drop-ins, then daemon-reloads. It does NOT restart the
// unit — safe to call from a live supervised daemon before a version handoff
// so the next systemd restart does not 203/EXEC on a deleted version path.
func syncSystemdUnitFile(profile, exePath string, args []string) error {
	unitPath, err := systemdUserUnitPath(profile)
	if err != nil {
		return err
	}
	// Only rewrite when a unit is already installed (or about to be by Install).
	if err := writeSystemdUnitFile(unitPath, exePath, args); err != nil {
		return err
	}
	dropInDir, err := systemdUserDropInDir(profile)
	if err != nil {
		return err
	}
	if _, err := clearSystemdExecStartDropIns(dropInDir); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (linuxServiceInstaller) Install(profile, exePath string, args []string) error {
	// Idempotent reinstall must clear stale ExecStart drop-ins before (or
	// with) rewriting the main unit. Observed production failure (s144):
	// main unit pointed at v0.4.1 while override.conf still forced
	// …/versions/v0.4.0/multica → 203/EXEC after the old path was deleted.
	if err := syncSystemdUnitFile(profile, exePath, args); err != nil {
		return err
	}

	unit := systemdUnitName(profile)
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
	// Also remove ExecStart drop-ins (and empty .d) so a later reinstall
	// cannot revive a ghost path from leftover fragments.
	if dropInDir, dErr := systemdUserDropInDir(profile); dErr == nil {
		_, _ = clearSystemdExecStartDropIns(dropInDir)
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

// SyncUnit rewrites the installed unit to exePath and clears ExecStart drop-ins
// without restarting the service. Used after a CLI self-update so systemd's next
// restart targets the staged Active binary instead of a deleted version path.
// No-op when the unit is not installed.
func (linuxServiceInstaller) SyncUnit(profile, exePath string, args []string) error {
	unitPath, err := systemdUserUnitPath(profile)
	if err != nil {
		return err
	}
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return nil
	}
	return syncSystemdUnitFile(profile, exePath, args)
}
