//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func init() {
	platformServiceInstaller = windowsServiceInstaller{}
}

// windowsServiceInstaller registers `multica daemon supervise` as a Windows
// Scheduled Task with a logon trigger, run at limited (standard user)
// privileges — not a Windows Service, which would require an admin-elevated
// install. schtasks has no plain "update" verb, so /Create /F (force) is
// used for both fresh installs and idempotent reinstalls: it silently
// replaces an existing task with the same name rather than erroring.
type windowsServiceInstaller struct{}

func windowsTaskName(profile string) string {
	if profile == "" {
		return "MulticaDaemon"
	}
	return "MulticaDaemon-" + profile
}

func (windowsServiceInstaller) Install(profile, exePath string, args []string) error {
	task := windowsTaskName(profile)

	taskRun := quoteWindowsArg(exePath)
	for _, a := range args {
		taskRun += " " + quoteWindowsArg(a)
	}

	// /F forces overwrite of an existing task with this name (idempotent
	// reinstall). /RL LIMITED runs with the current standard-user token, no
	// admin elevation. /SC ONLOGON triggers at the user's next interactive
	// logon — combined with the immediate /Run below so the caller doesn't
	// have to log out and back in to see it running.
	createArgs := []string{
		"/Create", "/F",
		"/TN", task,
		"/TR", taskRun,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
	}
	if out, err := exec.Command("schtasks", createArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Create failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// The ONLOGON trigger doesn't fire until the next logon; run it now so
	// Status() right after Install sees an actually-running process, same
	// as the darwin/linux installers' immediate (re)start.
	if out, err := exec.Command("schtasks", "/Run", "/TN", task).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Run failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (windowsServiceInstaller) Uninstall(profile string) error {
	task := windowsTaskName(profile)
	out, err := exec.Command("schtasks", "/Delete", "/TN", task, "/F").CombinedOutput()
	if err != nil {
		if schtasksTaskNotFound(string(out)) {
			return nil
		}
		return fmt.Errorf("schtasks /Delete failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (windowsServiceInstaller) Status(profile string) (registered, running bool, detail string, err error) {
	task := windowsTaskName(profile)
	out, cmdErr := exec.Command("schtasks", "/Query", "/TN", task, "/FO", "LIST", "/V").CombinedOutput()
	output := string(out)
	if cmdErr != nil {
		if schtasksTaskNotFound(output) {
			return false, false, "not installed", nil
		}
		return false, false, "", fmt.Errorf("schtasks /Query failed: %w: %s", cmdErr, strings.TrimSpace(output))
	}

	status := schtasksListField(output, "Status:")
	// schtasks reports "Running" while the task's process is alive, and
	// "Ready"/"Queued"/etc. otherwise — only "Running" means the daemon
	// supervisor process is actually up right now.
	return true, strings.EqualFold(status, "Running"), "status=" + status, nil
}

// schtasksTaskNotFound recognizes schtasks's "no task by that name" error
// text. schtasks has no dedicated exit code for this case, so text matching
// is the only option; it errors in the current console code page, which is
// English on every locale we ship to, so this doesn't need i18n handling.
func schtasksTaskNotFound(output string) bool {
	return strings.Contains(output, "ERROR: The system cannot find the file specified") ||
		strings.Contains(output, "cannot find the specified task")
}

// schtasksListField extracts the value of a "Key:      Value" line from
// schtasks's `/FO LIST` output. Returns "" if the key isn't present.
func schtasksListField(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// quoteWindowsArg wraps an argument in double quotes for a schtasks /TR
// command line, escaping any embedded double quotes. schtasks parses /TR as
// a single command-line string it hands to CreateProcess, so a path or flag
// containing a space must be quoted the same way the Windows C runtime's
// argv parser expects.
func quoteWindowsArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
