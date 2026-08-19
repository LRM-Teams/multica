package agentavatar

import (
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
		{name: "prior v2 first", raw: PriorPublicBaseURL + "/agent-01.png", want: PriorPublicBaseURL + "/agent-01.png", ok: true},
		{name: "prior v2 last", raw: PriorPublicBaseURL + "/agent-15.png", want: PriorPublicBaseURL + "/agent-15.png", ok: true},
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

func TestURLsMatchCatalog(t *testing.T) {
	t.Parallel()

	urls := URLs()
	if len(urls) != PresetCount {
		t.Fatalf("URL count = %d, want %d", len(urls), PresetCount)
	}
	if urls[0] != PublicBaseURL+"/agent-01.png" {
		t.Fatalf("first URL = %q", urls[0])
	}
	if urls[len(urls)-1] != PublicBaseURL+"/agent-15.png" {
		t.Fatalf("last URL = %q", urls[len(urls)-1])
	}
}
