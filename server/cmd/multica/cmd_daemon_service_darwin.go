//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/multica-ai/multica/server/internal/computer"
)

func init() {
	platformServiceInstaller = darwinServiceInstaller{}
}

// darwinServiceInstaller registers `multica daemon supervise` as a per-user
// LaunchAgent (~/Library/LaunchAgents), loaded into the gui/<uid> domain via
// the modern launchctl bootstrap/bootout verbs (the legacy `load`/`unload`
// verbs are deprecated and unreliable about re-loading an already-loaded
// label). RunAtLoad + KeepAlive means launchd starts it at login and
// relaunches it if it ever exits, without requiring an admin password —
// LaunchAgents (unlike LaunchDaemons) run in the user's own session.
type darwinServiceInstaller struct{}

// launchAgentLabel is also the launchd service name used in bootstrap/
// bootout/print (gui/<uid>/<label>) and the plist filename.
func launchAgentLabel(profile string) string {
	if profile == "" {
		return "com.multica.daemon"
	}
	return "com.multica.daemon." + profile
}

func launchAgentPlistPath(profile string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel(profile)+".plist"), nil
}

func launchdGUIDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

const launchAgentPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{range .Args}}		<string>{{.}}</string>
{{end}}	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

func (darwinServiceInstaller) Install(profile, exePath string, args []string) error {
	label := launchAgentLabel(profile)
	plistPath, err := launchAgentPlistPath(profile)
	if err != nil {
		return err
	}

	dir := computer.RootDir(profile)
	if dir == "" {
		return fmt.Errorf("resolve daemon directory for profile %q", profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create daemon directory: %w", err)
	}
	// Deliberately not computer.LogPath: that file is the daemon
	// WORKER's own log (supervise redirects each spawned generation's
	// stdout/stderr there already, see buildSuperviseConfig). This is the
	// supervise PROCESS's own stdout/stderr — startup/shutdown/escalation
	// messages that would otherwise go to launchd's default /dev/null.
	logPath := filepath.Join(dir, "supervisor.log")

	tmplData := struct {
		Label   string
		Args    []string
		LogPath string
	}{
		Label:   label,
		Args:    append([]string{exePath}, args...),
		LogPath: logPath,
	}

	tmpl, err := template.New("plist").Parse(launchAgentPlistTemplate)
	if err != nil {
		return fmt.Errorf("parse plist template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tmplData); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(plistPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", plistPath, err)
	}

	domain := launchdGUIDomain()
	// Idempotent reinstall: bootout any existing registration for this
	// label first. bootout errors (e.g. it wasn't loaded yet) are expected
	// and ignored — bootstrap below is what actually needs to succeed.
	_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// bootstrap with RunAtLoad queues a start but doesn't guarantee it has
	// happened by the time we return; kickstart -k forces an immediate
	// (re)start so Status() right after Install sees a real running process
	// rather than racing launchd's own scheduling.
	if out, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (darwinServiceInstaller) Uninstall(profile string) error {
	label := launchAgentLabel(profile)
	domain := launchdGUIDomain()
	_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()

	plistPath, err := launchAgentPlistPath(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist %s: %w", plistPath, err)
	}
	return nil
}

func (darwinServiceInstaller) Status(profile string) (registered, running bool, detail string, err error) {
	plistPath, perr := launchAgentPlistPath(profile)
	if perr != nil {
		return false, false, "", perr
	}
	if _, statErr := os.Stat(plistPath); os.IsNotExist(statErr) {
		return false, false, "not installed", nil
	}

	label := launchAgentLabel(profile)
	domain := launchdGUIDomain()
	out, cmdErr := exec.Command("launchctl", "print", domain+"/"+label).CombinedOutput()
	if cmdErr != nil {
		// The plist file exists but launchd doesn't know about this label —
		// e.g. removed via `launchctl bootout` without deleting the plist.
		return true, false, "plist present but not loaded in launchd", nil
	}

	output := string(out)
	state := launchctlPrintField(output, "state = ")
	return true, state == "running", "state=" + state, nil
}

// launchctlPrintField extracts the value of a "key = value" line from
// `launchctl print`'s plain-text output (it has no machine-readable format
// flag). Returns "" if the key isn't present.
func launchctlPrintField(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
