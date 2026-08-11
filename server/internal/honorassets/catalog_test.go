package honorassets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAssetsMatchPublishedCatalog(t *testing.T) {
	t.Parallel()

	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != AssetCount {
		t.Fatalf("asset count = %d, want %d", len(assets), AssetCount)
	}

	byKey := make(map[string]Asset, len(assets))
	fallbackRoot := filepath.Join("..", "..", "..", "apps", "web", "public")
	for _, asset := range assets {
		if asset.ContentType != "image/webp" {
			t.Fatalf("asset %s content type = %q", asset.Key, asset.ContentType)
		}
		if len(asset.Data) < 12 || !bytes.Equal(asset.Data[:4], []byte("RIFF")) || !bytes.Equal(asset.Data[8:12], []byte("WEBP")) {
			t.Fatalf("asset %s is not WebP", asset.Key)
		}
		fallback, err := os.ReadFile(filepath.Join(fallbackRoot, filepath.FromSlash(asset.Key)))
		if err != nil {
			t.Fatalf("read web fallback for %s: %v", asset.Key, err)
		}
		if !bytes.Equal(fallback, asset.Data) {
			t.Fatalf("web fallback for %s differs from embedded OSS source", asset.Key)
		}
		byKey[asset.Key] = asset
	}

	for key, wantURL := range map[string]string{
		"honor-assets/v1/users/user-honor-level-01.webp":   "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-01.webp",
		"honor-assets/v1/users/user-honor-level-80.webp":   "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-80.webp",
		"honor-assets/v1/agents/agent-honor-level-01.webp": "https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-01.webp",
		"honor-assets/v1/agents/agent-honor-level-30.webp": "https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-30.webp",
		"honor-assets/v1/honor-center-orbit.webp":          "https://cdn.leagent.me/honor-assets/v1/honor-center-orbit.webp",
	} {
		asset, ok := byKey[key]
		if !ok {
			t.Fatalf("missing catalog key %s", key)
		}
		if asset.URL != wantURL {
			t.Fatalf("asset %s URL = %q, want %q", key, asset.URL, wantURL)
		}
	}
}
