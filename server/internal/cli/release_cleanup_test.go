package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupInstalledReleaseResidueRemovesOnlyInactiveReleaseFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installPath := filepath.Join(home, ".local", "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath, []byte("active"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+".prev", []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	machineRoot, err := MachineStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	legacyBinary := filepath.Join(machineRoot, "versions", "v1.0.0", "multica")
	stagedBinary := filepath.Join(machineRoot, "upgrade-staging", "v2.0.0", "multica")
	for path, content := range map[string]string{legacyBinary: "legacy", stagedBinary: "staged"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	result, err := CleanupInstalledReleaseResidue(installPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.PreviousExecutable || !result.LegacyVersions || !result.UpgradeStaging {
		t.Fatalf("cleanup result = %+v", result)
	}
	if got, err := os.ReadFile(installPath); err != nil || string(got) != "active" {
		t.Fatalf("active executable = %q, %v", got, err)
	}
	for _, path := range []string{installPath + ".prev", filepath.Join(machineRoot, "versions"), filepath.Join(machineRoot, "upgrade-staging")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("release residue still exists at %s: %v", path, err)
		}
	}

	result, err = CleanupInstalledReleaseResidue(installPath)
	if err != nil {
		t.Fatal(err)
	}
	if result != (ReleaseCleanupResult{}) {
		t.Fatalf("idempotent cleanup result = %+v", result)
	}
}

func TestCleanupInstalledReleaseResidueRejectsActiveVersionStoreBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	machineRoot, err := MachineStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	installPath := filepath.Join(machineRoot, "versions", "v1.0.0", "multica")
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath, []byte("active"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := CleanupInstalledReleaseResidue(installPath); err == nil {
		t.Fatal("cleanup accepted an active executable inside the legacy version store")
	}
	if _, err := os.Stat(installPath); err != nil {
		t.Fatalf("guard removed active executable: %v", err)
	}
}
