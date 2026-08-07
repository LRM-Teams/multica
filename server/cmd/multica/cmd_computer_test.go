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
}

// #2487: the Computer run/stop/restart/status/logs lifecycle is machine-wide
// and never profile-scoped.
func TestComputerLifecycleCommandsAreMachineWideAndAcceptNoArgs(t *testing.T) {
	for _, lc := range []*cobra.Command{computerStartCmd, computerStopCmd, computerRestartCmd, computerStatusCmd, computerLogsCmd} {
		if lc.Args == nil {
			t.Fatalf("%s: no Args validator (must reject positional args)", lc.Name())
		}
		if err := lc.Args(lc, []string{"foo"}); err == nil {
			t.Fatalf("%s must reject positional args (no workspace/profile selection)", lc.Name())
		}
		// Machine-wide: no --profile flag at all.
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
