package cli

import "testing"

func TestOfficialCloudOriginsAreSplit(t *testing.T) {
	if OfficialCloudAPIURL != "https://api.leagent.me" {
		t.Fatalf("OfficialCloudAPIURL = %q", OfficialCloudAPIURL)
	}
	if OfficialCloudAppURL != "https://www.leagent.me" {
		t.Fatalf("OfficialCloudAppURL = %q", OfficialCloudAppURL)
	}
}

func TestCanonicalizeOfficialCloudAPIURL(t *testing.T) {
	for _, legacy := range []string{"https://leagent.me", "https://www.leagent.me/"} {
		if got := CanonicalizeOfficialCloudAPIURL(legacy); got != OfficialCloudAPIURL {
			t.Errorf("CanonicalizeOfficialCloudAPIURL(%q) = %q, want %q", legacy, got, OfficialCloudAPIURL)
		}
	}

	const testURL = "https://api.test.leagent.me"
	if got := CanonicalizeOfficialCloudAPIURL(testURL); got != testURL {
		t.Fatalf("test URL must remain configurable: got %q", got)
	}
}
