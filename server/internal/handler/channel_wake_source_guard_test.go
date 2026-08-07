package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelWakeSourcesAvoidDroppedAmbientPendingTable(t *testing.T) {
	files := []string{
		"channel_ambient_wake.go",
		"channel_ambient_gate.go",
		"channel.go",
	}
	for _, name := range files {
		path := filepath.Join(".", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(body), "channel_ambient_pending_wake") {
			t.Fatalf("%s still references dropped table channel_ambient_pending_wake", name)
		}
	}
}

func TestChannelHumanWakeDispatchBuildMarkerPresent(t *testing.T) {
	body, err := os.ReadFile("channel.go")
	if err != nil {
		t.Fatalf("read channel.go: %v", err)
	}
	if !strings.Contains(string(body), `channel human wake dispatch restored`) {
		t.Fatal("missing production binary grep marker for human wake restore")
	}
}
