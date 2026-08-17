//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitNameDefaultProfile(t *testing.T) {
	if got, want := systemdUnitName(""), "multica-daemon.service"; got != want {
		t.Fatalf("systemdUnitName(\"\") = %q, want %q", got, want)
	}
}

func TestSystemdUnitNameNamedProfile(t *testing.T) {
	if got, want := systemdUnitName("staging"), "multica-daemon-staging.service"; got != want {
		t.Fatalf("systemdUnitName(\"staging\") = %q, want %q", got, want)
	}
}

func TestSystemdUserUnitPathIsolatesProfiles(t *testing.T) {
	defaultPath, err := systemdUserUnitPath("")
	if err != nil {
		t.Fatalf("systemdUserUnitPath(\"\"): %v", err)
	}
	stagingPath, err := systemdUserUnitPath("staging")
	if err != nil {
		t.Fatalf("systemdUserUnitPath(\"staging\"): %v", err)
	}
	if defaultPath == stagingPath {
		t.Fatalf("default and named profile unit paths must differ, both got %q", defaultPath)
	}
}

func TestDropInOverridesExecStart(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "s144-style override",
			body: "[Service]\nExecStart=\nExecStart=/home/andong3/.local/share/multica/versions/v0.4.0/multica daemon supervise\n",
			want: true,
		},
		{
			name: "environment only",
			body: "[Service]\nEnvironment=MULTICA_AGENT_WORKSPACE_QUOTA_BYTES=0\n",
			want: false,
		},
		{
			name: "case insensitive key",
			body: "[Service]\nexecstart=/old/multica daemon supervise\n",
			want: true,
		},
		{
			name: "commented out",
			body: "[Service]\n# ExecStart=/old/multica\nEnvironment=FOO=1\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dropInOverridesExecStart([]byte(tc.body)); got != tc.want {
				t.Fatalf("dropInOverridesExecStart = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClearSystemdExecStartDropInsRemovesOnlyExecStartPins is the s144
// regression: install-service rewrote the main unit to v0.4.1 while a drop-in
// still forced v0.4.0 → 203/EXEC. Clearing must remove ExecStart pins and keep
// unrelated Environment drop-ins.
func TestClearSystemdExecStartDropInsRemovesOnlyExecStartPins(t *testing.T) {
	dir := t.TempDir()
	dropInDir := filepath.Join(dir, "multica-daemon.service.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatal(err)
	}
	execStartPin := filepath.Join(dropInDir, "override.conf")
	quotaKeep := filepath.Join(dropInDir, "quota.conf")
	if err := os.WriteFile(execStartPin, []byte("[Service]\nExecStart=\nExecStart=/ghost/v0.4.0/multica daemon supervise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quotaKeep, []byte("[Service]\nEnvironment=MULTICA_AGENT_WORKSPACE_QUOTA_BYTES=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := clearSystemdExecStartDropIns(dropInDir)
	if err != nil {
		t.Fatalf("clearSystemdExecStartDropIns: %v", err)
	}
	if len(removed) != 1 || removed[0] != execStartPin {
		t.Fatalf("removed = %v, want only %q", removed, execStartPin)
	}
	if _, err := os.Stat(execStartPin); !os.IsNotExist(err) {
		t.Fatalf("ExecStart drop-in still present: %v", err)
	}
	if _, err := os.Stat(quotaKeep); err != nil {
		t.Fatalf("Environment drop-in must be preserved: %v", err)
	}
	// .d dir remains because quota.conf is still there
	if _, err := os.Stat(dropInDir); err != nil {
		t.Fatalf("drop-in dir should remain while non-ExecStart fragments exist: %v", err)
	}
}

func TestClearSystemdExecStartDropInsRemovesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	dropInDir := filepath.Join(dir, "multica-daemon.service.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(dropInDir, "override.conf")
	if err := os.WriteFile(pin, []byte("ExecStart=/ghost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clearSystemdExecStartDropIns(dropInDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dropInDir); !os.IsNotExist(err) {
		t.Fatalf("empty drop-in dir should be removed, err=%v", err)
	}
}

func TestWriteSystemdUnitFileRendersExecStart(t *testing.T) {
	dir := t.TempDir()
	unitPath := filepath.Join(dir, "multica-daemon.service")
	if err := writeSystemdUnitFile(unitPath, "/opt/multica/versions/v0.4.1/multica", []string{"computer", "supervise"}); err != nil {
		t.Fatalf("writeSystemdUnitFile: %v", err)
	}
	body, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "ExecStart=/opt/multica/versions/v0.4.1/multica computer supervise") {
		t.Fatalf("unit body missing expected ExecStart:\n%s", got)
	}
	if !strings.Contains(got, "Restart=always") {
		t.Fatalf("unit body missing Restart=always:\n%s", got)
	}
}
