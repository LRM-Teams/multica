package daemon

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentActivityProductionUsesOnlyTypedMessageObservationSeams(t *testing.T) {
	producer, err := os.ReadFile("agent_activity_producer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"func (p *agentActivityProducer) Publish(",
		"PublishForManagedAgent",
		"PublishEntryForManagedAgent",
		"PublishHoldEntry",
		"EnsureManagedAgent",
	} {
		if strings.Contains(string(producer), forbidden) {
			t.Fatalf("legacy Activity production seam remains: %q", forbidden)
		}
	}

	allowed := map[string]bool{
		"workspace_daemon_activity.go":      true,
		"workspace_daemon_agent_process.go": true,
	}
	err = filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), ".Observe(AgentObservation") && !allowed[filepath.Base(path)] {
			t.Errorf("%s publishes Activity outside a Message observation seam", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRaftStartingActivityHasOneBroadcastCallSite(t *testing.T) {
	callSites := make([]string, 0, 1)
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), ".broadcastActivity(") {
			callSites = append(callSites, filepath.Base(path))
		}
		for _, retired := range []string{"publishManagedAgentStartActivity", "observeRuntimeStarting", "publishManagedProviderSpawn"} {
			if strings.Contains(string(raw), retired) {
				t.Errorf("%s retains retired pre-Raft Starting seam %q", path, retired)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(callSites) != 1 || callSites[0] != "workspace_daemon_agent_process.go" {
		t.Fatalf("broadcastActivity production call sites = %v, want only Raft startAgentNow", callSites)
	}
}
