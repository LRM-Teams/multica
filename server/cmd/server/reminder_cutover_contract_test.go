package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReminderCutoverHasNoLegacyProductionPaths(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	forbidden := []string{
		"ReminderOverdueScanJob",
		"ProcessOverdueReminders",
		"reminder_fired",
		"Reminder fired:",
		"提醒已触发：",
		"parseReminderSystemEvent",
		"ReminderSystemEventContent",
		"message.system_event.reminder",
		"quota_coalesced",
		"group_manager_auto",
		"receipt_message_id",
		`r.Delete("/messages/{messageId}",`,
	}

	for _, root := range []string{"server", "packages", "apps"} {
		rootPath := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "migrations", "node_modules", ".next", "dist", "coverage":
					return filepath.SkipDir
				}
				return nil
			}

			name := entry.Name()
			if strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") {
				return nil
			}
			switch filepath.Ext(name) {
			case ".go", ".ts", ".tsx", ".json", ".sql":
			default:
				return nil
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			source := string(raw)
			for _, marker := range forbidden {
				if strings.Contains(source, marker) {
					rel, relErr := filepath.Rel(repoRoot, path)
					if relErr != nil {
						return relErr
					}
					t.Errorf("legacy Reminder marker %q remains in %s", marker, rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan production source root %s: %v", root, err)
		}
	}
}
