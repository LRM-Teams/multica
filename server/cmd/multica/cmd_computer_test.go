package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestComputerUpgradeCommandUsesBoundComputer(t *testing.T) {
	if got, want := computerUpgradeCmd.Use, "upgrade"; got != want {
		t.Fatalf("computer upgrade use = %q, want %q", got, want)
	}
	if err := computerUpgradeCmd.Args(computerUpgradeCmd, nil); err != nil {
		t.Fatalf("computer upgrade rejects no arguments: %v", err)
	}
	if err := computerUpgradeCmd.Args(computerUpgradeCmd, []string{"daemon-id"}); err == nil {
		t.Fatal("computer upgrade accepts an explicit daemon ID")
	}
	if flag := computerUpgradeCmd.Flags().Lookup("target-version"); flag == nil {
		t.Fatal("computer upgrade is missing --target-version")
	}
	for _, retired := range []string{"wait", "output", "request-id", "download-timeout"} {
		if flag := computerUpgradeCmd.Flags().Lookup(retired); flag != nil {
			t.Fatalf("computer upgrade still exposes split/legacy flag --%s", retired)
		}
	}
}

func TestLegacyTopLevelUpdateCommandIsRemoved(t *testing.T) {
	if hasSubcommand(rootCmd, "update") {
		t.Fatal("top-level multica update must not remain alongside computer upgrade")
	}
}

// #2487/#2490: selectors scope readiness or log/doctor evidence only. Stop and
// status remain strictly machine-wide, and no command exposes a profile.
func TestComputerLifecycleCommandsAreMachineWideWithOnlyDefinedSelectors(t *testing.T) {
	for _, lc := range []*cobra.Command{computerStopCmd, computerStatusCmd} {
		if lc.Args == nil {
			t.Fatalf("%s: no Args validator (must reject positional args)", lc.Name())
		}
		if err := lc.Args(lc, []string{"/ws"}); err == nil {
			t.Fatalf("%s must reject Workspace selectors", lc.Name())
		}
	}
	for _, lc := range []*cobra.Command{computerStartCmd, computerRestartCmd, computerLogsCmd, computerDoctorCmd} {
		if err := lc.Args(lc, []string{"/ws"}); err != nil {
			t.Fatalf("%s rejects its defined Workspace selector: %v", lc.Name(), err)
		}
		if err := lc.Args(lc, []string{"profile"}); err == nil {
			t.Fatalf("%s accepts a non-/ selector that could be confused with a profile", lc.Name())
		}
	}
	for _, lc := range []*cobra.Command{computerStartCmd, computerStopCmd, computerRestartCmd, computerStatusCmd, computerLogsCmd, computerDoctorCmd} {
		if flag := lc.Flags().Lookup("profile"); flag != nil {
			t.Fatalf("%s exposes --profile; Computer is machine-wide", lc.Name())
		}
	}
}

// #2487: the hidden `daemon` group is a compatibility alias over the same
// machine-wide Computer, and computer-mode resolves profile to the default.
func TestDaemonGroupHiddenAliasAndComputerModeForcesDefaultProfile(t *testing.T) {
	if !daemonCmd.Hidden {
		t.Fatal("daemon group should be hidden (compatibility alias) per #2487")
	}

	old := computerMode
	computerMode = true
	t.Cleanup(func() { computerMode = old })

	fake := &cobra.Command{}
	fake.Flags().String("profile", "staging", "")
	if got := resolveProfile(fake); got != "" {
		t.Fatalf("computer-mode resolveProfile = %q, want machine-wide \"\"", got)
	}
}

func TestComputerModeRespectsProfileWhenNotInComputerMode(t *testing.T) {
	old := computerMode
	computerMode = false
	t.Cleanup(func() { computerMode = old })

	fake := &cobra.Command{}
	fake.Flags().String("profile", "staging", "")
	if got := resolveProfile(fake); got != "staging" {
		t.Fatalf("non-computer-mode resolveProfile = %q, want staging", got)
	}
}

// #2496: `computer` is the primary visible surface; the `daemon` group is the
// hidden deprecated alias. Computer terminology governs the CLI help.
func TestComputerIsPrimaryVisibleSurfaceAndDaemonHiddenAlias(t *testing.T) {
	if computerCmd.Hidden {
		t.Fatal("computer group must be the visible primary surface, not hidden")
	}
	if !daemonCmd.Hidden {
		t.Fatal("daemon group is the retired compatibility alias and must be hidden (#2496)")
	}
	for _, name := range []string{"start", "stop", "restart", "status", "logs", "upgrade", "doctor"} {
		if !hasSubcommand(computerCmd, name) {
			t.Fatalf("computer is missing primary subcommand %q", name)
		}
	}
}

func hasSubcommand(cmd interface{ Commands() []*cobra.Command }, name string) bool {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// The `daemon` alias is the hidden, retired compatibility surface (#2496); it
// delegates to the same Computer and is not shown in primary help.
func TestDaemonAliasHiddenCompatibilitySurface(t *testing.T) {
	if !daemonCmd.Hidden {
		t.Fatal("daemon alias must be hidden")
	}
}

func TestRetiredProfileSelfHostAndOSServiceSurfacesAreNotPublic(t *testing.T) {
	for _, name := range []string{"supervise", "install-service", "uninstall-service", "service-status"} {
		if hasSubcommand(daemonCmd, name) {
			t.Fatalf("retired daemon subcommand %q is still reachable", name)
		}
	}
	if hasSubcommand(setupCmd, "self-host") {
		t.Fatal("retired self-host setup is still reachable")
	}
	for _, name := range []string{"profile", "server-url"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil || !flag.Hidden {
			t.Fatalf("legacy root flag --%s must be hidden during compatibility cycle", name)
		}
		fake := &cobra.Command{}
		fake.Flags().String(name, "", "")
		if err := fake.Flags().Set(name, "legacy"); err != nil {
			t.Fatal(err)
		}
		if err := rejectRetiredComputerFlags(fake); err == nil {
			t.Fatalf("Computer accepted retired --%s", name)
		}
	}
}
