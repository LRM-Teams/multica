package main

import (
	"reflect"
	"testing"
)

func TestBuildSuperviseServiceArgsDefaultProfile(t *testing.T) {
	got := buildSuperviseServiceArgs("")
	want := []string{"computer", "supervise"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSuperviseServiceArgs(\"\") = %v, want %v", got, want)
	}
}

func TestBuildSuperviseServiceArgsNamedProfile(t *testing.T) {
	got := buildSuperviseServiceArgs("staging")
	want := []string{"computer", "supervise", "--profile", "staging"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSuperviseServiceArgs(\"staging\") = %v, want %v", got, want)
	}
}

// fakeServiceUnitSyncer doubles as daemonServiceInstaller + daemonServiceUnitSyncer.
type fakeServiceUnitSyncer struct {
	syncCalls []struct {
		profile, exePath string
		args             []string
	}
	statusRegistered bool
}

func (f *fakeServiceUnitSyncer) Install(profile, exePath string, args []string) error {
	return nil
}
func (f *fakeServiceUnitSyncer) Uninstall(profile string) error { return nil }
func (f *fakeServiceUnitSyncer) Status(profile string) (bool, bool, string, error) {
	return f.statusRegistered, false, "fake", nil
}
func (f *fakeServiceUnitSyncer) SyncUnit(profile, exePath string, args []string) error {
	f.syncCalls = append(f.syncCalls, struct {
		profile, exePath string
		args             []string
	}{profile, exePath, args})
	return nil
}

// TestBestEffortSyncInstalledServiceUnitDelegatesToSyncer is the restart-
// honesty self-heal: supervise start (and handoff) must be able to rewrite
// the OS service unit to the Active worker path without a human reinstall.
func TestBestEffortSyncInstalledServiceUnitDelegatesToSyncer(t *testing.T) {
	prev := platformServiceInstaller
	t.Cleanup(func() { platformServiceInstaller = prev })

	fake := &fakeServiceUnitSyncer{statusRegistered: true}
	platformServiceInstaller = fake

	if err := bestEffortSyncInstalledServiceUnit("staging", "/versions/v0.4.2/multica"); err != nil {
		t.Fatalf("bestEffortSyncInstalledServiceUnit: %v", err)
	}
	if len(fake.syncCalls) != 1 {
		t.Fatalf("SyncUnit calls = %d, want 1", len(fake.syncCalls))
	}
	c := fake.syncCalls[0]
	if c.profile != "staging" || c.exePath != "/versions/v0.4.2/multica" {
		t.Fatalf("SyncUnit args = %+v", c)
	}
	if !reflect.DeepEqual(c.args, []string{"computer", "supervise", "--profile", "staging"}) {
		t.Fatalf("SyncUnit service args = %v", c.args)
	}
}

func TestBestEffortSyncInstalledServiceUnitNoInstallerNoop(t *testing.T) {
	prev := platformServiceInstaller
	t.Cleanup(func() { platformServiceInstaller = prev })
	platformServiceInstaller = nil
	if err := bestEffortSyncInstalledServiceUnit("", "/any"); err != nil {
		t.Fatalf("nil installer must no-op, got %v", err)
	}
}
