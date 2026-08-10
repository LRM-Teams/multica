package agentavatar

import "testing"

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
	if len(assets) != LegacyPresetCount+PresetCount {
		t.Fatalf("asset count = %d, want %d", len(assets), LegacyPresetCount+PresetCount)
	}
	for index, asset := range assets {
		if len(asset.Data) == 0 {
			t.Fatalf("asset %d is empty", index)
		}
		if asset.ContentType == "" {
			t.Fatalf("asset %d has no content type", index)
		}
	}
	if assets[0].URL != LegacyURL(1) || assets[LegacyPresetCount-1].URL != LegacyURL(LegacyPresetCount) {
		t.Fatalf("legacy catalog bounds = %q .. %q", assets[0].URL, assets[LegacyPresetCount-1].URL)
	}
	if assets[0].ContentType != "image/jpeg" || assets[LegacyPresetCount].ContentType != "image/png" {
		t.Fatalf("catalog content types = %q, %q", assets[0].ContentType, assets[LegacyPresetCount].ContentType)
	}
	urls := URLs()
	for index, want := range urls {
		if got := assets[LegacyPresetCount+index].URL; got != want {
			t.Fatalf("current asset %d URL = %q, want %q", index, got, want)
		}
	}
}
