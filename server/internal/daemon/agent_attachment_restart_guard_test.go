package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionHasNoReminderAgentManagerFacade(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"reminderAgent", "reminderAgents"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("%s still contains obsolete Agent Attachment facade name %q", entry.Name(), forbidden)
			}
		}
	}
}
