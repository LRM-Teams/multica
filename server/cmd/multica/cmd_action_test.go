package main

import (
	"testing"
)

func TestActionPrepareRequiresName(t *testing.T) {
	cmd := actionPrepareCmd
	// reset flags by re-initing is hard; call RunE with empty name
	if err := cmd.Flags().Set("name", ""); err != nil {
		t.Fatal(err)
	}
	if err := runActionPrepare(cmd, nil); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestActionPrepareRejectsUnknownType(t *testing.T) {
	cmd := actionPrepareCmd
	_ = cmd.Flags().Set("name", "Bot")
	_ = cmd.Flags().Set("type", "nope")
	if err := runActionPrepare(cmd, nil); err == nil {
		t.Fatal("expected error for bad type")
	}
}
