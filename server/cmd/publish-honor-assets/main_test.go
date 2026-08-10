package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/honorassets"
)

func TestEmbeddedCatalogContainsEveryHonorAsset(t *testing.T) {
	t.Parallel()

	assets, err := honorassets.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != honorassets.AssetCount {
		t.Fatalf("embedded asset count = %d, want %d", len(assets), honorassets.AssetCount)
	}

	wantKeys := map[string]bool{
		"honor-assets/v1/users/user-honor-level-01.webp":   false,
		"honor-assets/v1/users/user-honor-level-80.webp":   false,
		"honor-assets/v1/agents/agent-honor-level-01.webp": false,
		"honor-assets/v1/agents/agent-honor-level-30.webp": false,
		"honor-assets/v1/honor-center-orbit.webp":          false,
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
