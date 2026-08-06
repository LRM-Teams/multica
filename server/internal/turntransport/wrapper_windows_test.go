//go:build windows

package turntransport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsWrapperIsCmdNotExtensionlessShim guards the root cause of the
// "How do you want to open this file?" popup: on Windows the credential CLI
// wrapper must be a .cmd batch, never a bare extensionless #!/bin/sh shim that
// ShellExecute offers to open in an arbitrary app.
func TestWindowsWrapperIsCmdNotExtensionlessShim(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transport")
	binaryPath := filepath.Join(t.TempDir(), "multica.exe")
	if err := os.WriteFile(binaryPath, []byte("%!MZ"), 0o700); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}

	transport, err := Prepare(root, binaryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if transport.WrapperPath() == "" || filepath.Ext(transport.WrapperPath()) != ".cmd" {
		t.Fatalf("wrapper path = %q, want a .cmd file", transport.WrapperPath())
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "multica")); !os.IsNotExist(err) {
		t.Fatalf("extensionless shim exists at %q; it must not be written on Windows", filepath.Join(root, "bin", "multica"))
	}

	raw, err := os.ReadFile(transport.WrapperPath())
	if err != nil {
		t.Fatalf("ReadFile wrapper: %v", err)
	}
	body := string(raw)
	if strings.HasPrefix(body, "#!") {
		t.Fatalf("wrapper starts with shebang, want cmd.exe batch: %q", body)
	}
	if !strings.Contains(body, "call") || !strings.Contains(body, binaryPath) {
		t.Fatalf("wrapper does not call the real exe: %q", body)
	}
	if !strings.Contains(body, EnvelopePathEnv) {
		t.Fatalf("wrapper does not set %s: %q", EnvelopePathEnv, body)
	}
}
