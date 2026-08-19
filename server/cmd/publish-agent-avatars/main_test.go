package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/agentavatar"
)

func TestEmbeddedCatalogContainsEveryAgentAvatar(t *testing.T) {
	t.Parallel()

	assets, err := agentavatar.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != agentavatar.PresetCount {
		t.Fatalf("embedded asset count = %d, want %d", len(assets), agentavatar.PresetCount)
	}

	wantKeys := map[string]bool{
		"agent-avatars/v3/agent-01.png": false,
		"agent-avatars/v3/agent-06.png": false,
	}
	for _, asset := range assets {
		if _, ok := wantKeys[asset.Key]; ok {
			wantKeys[asset.Key] = true
		}
	}
	for key, found := range wantKeys {
		if !found {
			t.Fatalf("embedded publisher catalog is missing %s", key)
		}
	}
}
