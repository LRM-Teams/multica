package agentavatar

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizeSelection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "current first", raw: URL(1), want: URL(1), ok: true},
		{name: "current last", raw: URL(PresetCount), want: URL(PresetCount), ok: true},
		{name: "legacy first", raw: "/agent-avatars/human-01.jpg", want: LegacyURL(1), ok: true},
		{name: "legacy last", raw: "/agent-avatars/human-24.jpg", want: LegacyURL(24), ok: true},
		{name: "canonical legacy", raw: LegacyURL(12), want: LegacyURL(12), ok: true},
		{name: "arbitrary CDN object", raw: PublicBaseURL + "/not-a-preset.png"},
		{name: "foreign URL", raw: "https://example.com/agent-01.png"},
		{name: "whitespace", raw: " " + URL(1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := CanonicalizeSelection(test.raw)
			if ok != test.ok || got != test.want {
				t.Fatalf("CanonicalizeSelection(%q) = (%q, %v), want (%q, %v)", test.raw, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestAssetsMatchCatalog(t *testing.T) {
	t.Parallel()

	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != PresetCount {
		t.Fatalf("asset count = %d, want %d", len(assets), PresetCount)
	}
	for index, asset := range assets {
		if len(asset.Data) == 0 {
			t.Fatalf("asset %d is empty", index)
		}
		if asset.ContentType == "" {
			t.Fatalf("asset %d has no content type", index)
		}
	}
	if assets[0].ContentType != "image/png" {
		t.Fatalf("catalog content type = %q", assets[0].ContentType)
	}
	urls := URLs()
	for index, want := range urls {
		if got := assets[index].URL; got != want {
			t.Fatalf("current asset %d URL = %q, want %q", index, got, want)
		}
	}
}

func TestLegacyAssetManifestMatchesWebFallback(t *testing.T) {
	t.Parallel()

	for number := 1; number <= LegacyPresetCount; number++ {
		name := filepath.Join("..", "..", "..", "apps", "web", "public", "agent-avatars", fmt.Sprintf("human-%02d.jpg", number))
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read legacy avatar %02d: %v", number, err)
		}
		asset, err := LegacyAsset(number, data)
		if err != nil {
			t.Fatal(err)
		}
		if asset.URL != LegacyURL(number) || asset.ContentType != "image/jpeg" {
			t.Fatalf("legacy asset %02d metadata = %#v", number, asset)
		}
	}
}
