package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// validateProjectStatus must accept the five DB-backed statuses and reject
// anything else with a message that lists the valid values. `project create`,
// `project update`, and `project status` all share it (#3925: `--status active`
// used to reach the server and 500 on the CHECK constraint).
func TestValidateProjectStatus(t *testing.T) {
	for _, s := range validProjectStatuses {
		if err := validateProjectStatus(s); err != nil {
			t.Errorf("status %q should be valid, got: %v", s, err)
		}
	}
	err := validateProjectStatus("active")
	if err == nil {
		t.Fatal("status \"active\" should be rejected")
	}
	if !strings.Contains(err.Error(), "planned") {
		t.Errorf("error should list valid statuses, got: %v", err)
	}
}

// newProjectResourceUpdateTestCmd mirrors the flag surface of
// projectResourceUpdateCmd so unit tests can exercise the shortcut-flag plumbing
// without spinning up a server.
func newProjectResourceUpdateTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "update"}
	c.Flags().String("local-path", "", "")
	c.Flags().String("daemon-id", "", "")
	c.Flags().String("ref-label", "", "")
	c.Flags().String("ref", "", "")
	c.Flags().String("label", "", "")
	c.Flags().Bool("clear-label", false, "")
	c.Flags().Int32("position", 0, "")
	c.Flags().String("output", "json", "")
	return c
}

// TestBuildResourceRefFromFlagsLocalDirectoryMerges covers the same merge
// behavior for local_directory: partial edits keep unmentioned fields from the
// existing ref.
func TestBuildResourceRefFromFlagsLocalDirectoryMerges(t *testing.T) {
	t.Run("ref-label only edit preserves existing path + daemon", func(t *testing.T) {
		cmd := newProjectResourceUpdateTestCmd()
		_ = cmd.Flags().Set("ref-label", "renamed")
		existing := map[string]any{
			"local_path": "/Users/foo/work/a",
			"daemon_id":  "d1",
			"label":      "old",
		}
		ref, has, err := buildResourceRefFromFlags(cmd, "local_directory", existing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !has {
			t.Fatalf("expected has=true")
		}
		if ref["local_path"] != "/Users/foo/work/a" {
			t.Errorf("local_path missing after merge: %v", ref["local_path"])
		}
		if ref["daemon_id"] != "d1" {
			t.Errorf("daemon_id missing after merge: %v", ref["daemon_id"])
		}
		if ref["label"] != "renamed" {
			t.Errorf("label not overridden: %v", ref["label"])
		}
	})

	t.Run("local-path only without existing daemon fails", func(t *testing.T) {
		cmd := newProjectResourceUpdateTestCmd()
		_ = cmd.Flags().Set("local-path", "/Users/foo/work/b")
		_, _, err := buildResourceRefFromFlags(cmd, "local_directory", nil)
		if err == nil {
			t.Fatalf("expected error when daemon_id is missing from both flags and existing ref")
		}
	})

	t.Run("ref-label cleared on empty input", func(t *testing.T) {
		cmd := newProjectResourceUpdateTestCmd()
		_ = cmd.Flags().Set("ref-label", "")
		existing := map[string]any{
			"local_path": "/Users/foo/work/a",
			"daemon_id":  "d1",
			"label":      "to-clear",
		}
		ref, has, err := buildResourceRefFromFlags(cmd, "local_directory", existing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !has {
			t.Fatalf("expected has=true")
		}
		if _, ok := ref["label"]; ok {
			t.Errorf("expected embedded label to be cleared, got %v", ref["label"])
		}
	})
}
