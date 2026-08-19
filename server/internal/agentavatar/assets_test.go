package agentavatar

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
	if len(assets) != PresetCount {
		t.Fatalf("asset count = %d, want %d", len(assets), PresetCount)
	}

	fallbackRoot := filepath.Join("..", "..", "..", "apps", "web", "public")
	pngMagic := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	for _, asset := range assets {
		if asset.ContentType != "image/png" {
			t.Fatalf("asset %s content type = %q", asset.Key, asset.ContentType)
		}
		if len(asset.Data) < len(pngMagic) || !bytes.Equal(asset.Data[:len(pngMagic)], pngMagic) {
			t.Fatalf("asset %s is not PNG", asset.Key)
		}
		fallback, err := os.ReadFile(filepath.Join(fallbackRoot, filepath.FromSlash(asset.Key)))
		if err != nil {
			t.Fatalf("read web fallback for %s: %v", asset.Key, err)
		}
		if !bytes.Equal(fallback, asset.Data) {
			t.Fatalf("web fallback for %s differs from embedded OSS source", asset.Key)
		}
	}

	if assets[0].URL != PublicBaseURL+"/agent-01.png" {
		t.Fatalf("first URL = %q", assets[0].URL)
	}
	if assets[len(assets)-1].URL != PublicBaseURL+"/agent-06.png" {
		t.Fatalf("last URL = %q", assets[len(assets)-1].URL)
	}
}
