package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestReleaseManifestBaseURLIsNotGitHub is a regression guard: an
// unauthenticated request from a bare CLI/daemon install to the private
// LRM-Teams/multica repo's GitHub API/asset hosts always 404s (proven against
// the live repo before this feed existed), so the release source must never
// silently point back at github.com or api.github.com.
func TestReleaseManifestBaseURLIsNotGitHub(t *testing.T) {
	for _, host := range []string{"github.com", "api.github.com", "raw.githubusercontent.com"} {
		if strings.Contains(ReleaseManifestBaseURL, host) {
			t.Fatalf("ReleaseManifestBaseURL = %q must not point at %s", ReleaseManifestBaseURL, host)
		}
	}
}

func TestFetchManifestParsesPublishedShape(t *testing.T) {
	want := ReleaseManifest{
		TagName: "v0.3.81",
		Version: "0.3.81",
		Platforms: map[string]ReleaseAsset{
			"linux-amd64": {URL: "https://example/v0.3.81/multica-cli-0.3.81-linux-amd64.tar.gz", SHA256: "deadbeef"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	got, err := fetchManifest(server.URL + "/latest.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TagName != want.TagName || got.Version != want.Version {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.Platforms["linux-amd64"] != want.Platforms["linux-amd64"] {
		t.Fatalf("platforms mismatch: got %+v", got.Platforms)
	}
}

func TestFetchManifestFailsClosedOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := fetchManifest(server.URL + "/latest.json"); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestPlatformKey(t *testing.T) {
	if got, want := platformKey("darwin", "arm64"), "darwin-arm64"; got != want {
		t.Fatalf("platformKey = %q, want %q", got, want)
	}
}

func TestFindPlatformAsset(t *testing.T) {
	t.Run("finds the matching platform entry", func(t *testing.T) {
		manifest := &ReleaseManifest{
			TagName: "v1.2.3",
			Platforms: map[string]ReleaseAsset{
				"darwin-amd64": {URL: "https://example/multica-cli-1.2.3-darwin-amd64.tar.gz", SHA256: "aaaa"},
				"linux-amd64":  {URL: "https://example/multica-cli-1.2.3-linux-amd64.tar.gz", SHA256: "bbbb"},
			},
		}

		got, err := findPlatformAsset(manifest, "darwin", "amd64")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.URL != "https://example/multica-cli-1.2.3-darwin-amd64.tar.gz" || got.SHA256 != "aaaa" {
			t.Fatalf("asset mismatch: got %+v", got)
		}
	})

	t.Run("returns error when platform is absent", func(t *testing.T) {
		manifest := &ReleaseManifest{TagName: "v1.2.3", Platforms: map[string]ReleaseAsset{
			"linux-amd64": {URL: "https://example/x.tar.gz", SHA256: "cccc"},
		}}
		_, err := findPlatformAsset(manifest, "windows", "amd64")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestIsReleaseVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"bare release", "0.1.13", true},
		{"v-prefixed release", "v0.1.13", true},
		{"surrounding whitespace", "  v0.1.13  ", true},
		{"dev describe", "v0.2.15-235-gdaf0e935", false},
		{"dirty dev describe", "v0.2.15-235-gdaf0e935-dirty", false},
		{"empty", "", false},
		{"two components", "0.1", false},
		{"four components", "0.1.2.3", false},
		{"non-numeric", "1.0.x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReleaseVersion(tt.in); got != tt.want {
				t.Fatalf("IsReleaseVersion(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name            string
		latest, current string
		want            bool
	}{
		{"patch bump", "v0.1.14", "v0.1.13", true},
		{"minor bump", "v0.2.0", "v0.1.99", true},
		{"major bump", "v1.0.0", "v0.99.99", true},
		{"same version", "v0.1.13", "v0.1.13", false},
		{"older latest", "v0.1.12", "v0.1.13", false},
		{"mixed v prefix", "0.1.14", "v0.1.13", true},
		{"current is dev describe → unparseable → false", "v0.1.14", "v0.1.13-5-gabcdef0", false},
		{"latest is dev describe → unparseable → false", "v0.1.14-1-gabcdef0", "v0.1.13", false},
		{"latest unparseable → false", "garbage", "v0.1.13", false},
		{"current unparseable → false", "v0.1.14", "garbage", false},
		{"empty latest", "", "v0.1.13", false},
		{"empty current", "v0.1.14", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNewerVersion(tt.latest, tt.current); got != tt.want {
				t.Fatalf("IsNewerVersion(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestBrewUpdateConfiguredIgnoresLegacyUpstreamTap(t *testing.T) {
	t.Setenv("MULTICA_BREW_PACKAGE", "")
	if IsBrewUpdateConfigured() {
		t.Fatal("empty MULTICA_BREW_PACKAGE should not configure brew updates")
	}

	t.Setenv("MULTICA_BREW_PACKAGE", "lrm-teams/tap/multica")
	if !IsBrewUpdateConfigured() {
		t.Fatal("LRM tap should configure brew updates")
	}

	t.Setenv("MULTICA_BREW_PACKAGE", "  "+LegacyBrewPackage+"  ")
	if IsBrewUpdateConfigured() {
		t.Fatal("legacy upstream tap must be ignored")
	}
}

func TestUpdateViaBrewRejectsLegacyUpstreamTap(t *testing.T) {
	t.Setenv("MULTICA_BREW_PACKAGE", LegacyBrewPackage)

	out, err := UpdateViaBrew()
	if err == nil {
		t.Fatal("expected error for legacy upstream tap")
	}
	if out != "" {
		t.Fatalf("output = %q, want empty", out)
	}
	if !strings.Contains(err.Error(), "legacy upstream tap") {
		t.Fatalf("error = %v, want legacy upstream tap", err)
	}
}

func TestVerifyAssetSHA256(t *testing.T) {
	data := []byte("hello multica")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])

	t.Run("accepts matching sha", func(t *testing.T) {
		if err := verifyAssetSHA256(data, good, "asset.tar.gz"); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("accepts uppercase expected hex", func(t *testing.T) {
		if err := verifyAssetSHA256(data, strings.ToUpper(good), "asset.tar.gz"); err != nil {
			t.Fatalf("expected ok with uppercase expected, got %v", err)
		}
	})

	t.Run("rejects mismatched sha", func(t *testing.T) {
		err := verifyAssetSHA256([]byte("tampered"), good, "asset.tar.gz")
		if err == nil {
			t.Fatal("expected mismatch error")
		}
		if !strings.Contains(err.Error(), "asset.tar.gz") {
			t.Fatalf("error should name the asset: %v", err)
		}
	})

	t.Run("rejects empty expected", func(t *testing.T) {
		if err := verifyAssetSHA256(data, "", "asset.tar.gz"); err == nil {
			t.Fatal("expected error for empty expected sha")
		}
	})
}

func TestUpdateDownloadTimeoutOrDefault(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name:    "uses default for zero",
			timeout: 0,
			want:    DefaultUpdateDownloadTimeout,
		},
		{
			name:    "uses default for negative",
			timeout: -1 * time.Second,
			want:    DefaultUpdateDownloadTimeout,
		},
		{
			name:    "keeps explicit timeout",
			timeout: 10 * time.Minute,
			want:    10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := updateDownloadTimeoutOrDefault(tt.timeout)
			if got != tt.want {
				t.Fatalf("timeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestUpdateTargetPathUsesStableBrewSymlink(t *testing.T) {
	got := updateTargetPathFromResolved("/opt/homebrew/Cellar/multica/0.3.35/bin/multica")
	want := "/opt/homebrew/bin/multica"
	if got != want {
		t.Fatalf("update target = %q, want %q", got, want)
	}
}

func TestUpdateTargetPathKeepsNonBrewExecutable(t *testing.T) {
	got := updateTargetPathFromResolved("/Users/frank/.local/bin/multica")
	want := "/Users/frank/.local/bin/multica"
	if got != want {
		t.Fatalf("update target = %q, want %q", got, want)
	}
}
