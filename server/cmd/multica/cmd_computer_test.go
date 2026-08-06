package main

import "testing"

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
