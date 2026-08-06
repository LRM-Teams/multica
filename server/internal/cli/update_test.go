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
		if strings.Contains(DefaultReleaseManifestBaseURL, host) {
			t.Fatalf("DefaultReleaseManifestBaseURL = %q must not point at %s", DefaultReleaseManifestBaseURL, host)
		}
	}
}

func TestReleaseManifestBaseURLUsesCanonicalCDN(t *testing.T) {
	const want = "https://cdn.leagent.me/computer"
	if DefaultReleaseManifestBaseURL != want {
		t.Fatalf("DefaultReleaseManifestBaseURL = %q, want %q", DefaultReleaseManifestBaseURL, want)
	}
}

// TestReleaseManifestBaseURLEnvOverride proves a machine can redirect the
// release feed without a new release — e.g. the default address gets blocked
// on some network's edge layer (2026-07-30 incident) and needs an immediate,
// no-rebuild fallback. Mirrors Raft Computer's
// DEFAULT_UPGRADE_BASE_URL/RAFT_COMPUTER_UPGRADE_BASE_URL shape.
func TestReleaseManifestBaseURLEnvOverride(t *testing.T) {
	t.Setenv(ReleaseManifestBaseURLEnv, "")
	if got := releaseManifestBaseURL(); got != DefaultReleaseManifestBaseURL {
		t.Fatalf("with env unset, releaseManifestBaseURL() = %q, want default %q", got, DefaultReleaseManifestBaseURL)
	}

	t.Setenv(ReleaseManifestBaseURLEnv, "https://backup.example.com/computer")
	if got := releaseManifestBaseURL(); got != "https://backup.example.com/computer" {
		t.Fatalf("with env set, releaseManifestBaseURL() = %q, want override", got)
	}

	t.Setenv(ReleaseManifestBaseURLEnv, "   ")
	if got := releaseManifestBaseURL(); got != DefaultReleaseManifestBaseURL {
		t.Fatalf("with env set to whitespace only, releaseManifestBaseURL() = %q, want default %q (not blank)", got, DefaultReleaseManifestBaseURL)
	}

	if ReleaseWebURL() != releaseManifestBaseURL() {
		t.Fatalf("ReleaseWebURL() = %q must track the same override as releaseManifestBaseURL() = %q", ReleaseWebURL(), releaseManifestBaseURL())
	}
}

// TestReleaseManifestBaseURLWithOverridePrecedence proves the three-layer
// precedence added for task #815 step 2: a server-dispatched value (arriving
// over the daemon's heartbeat ack) wins over the env var, which wins over the
// compile-time default. This is the one behavior this whole change exists to
// add, so the "server wins over env" case is asserted explicitly and not just
// implied by the other two.
func TestReleaseManifestBaseURLWithOverridePrecedence(t *testing.T) {
	t.Setenv(ReleaseManifestBaseURLEnv, "")
	if got := releaseManifestBaseURLWithOverride(""); got != DefaultReleaseManifestBaseURL {
		t.Fatalf("neither set: got %q, want default %q", got, DefaultReleaseManifestBaseURL)
	}

	t.Setenv(ReleaseManifestBaseURLEnv, "https://env-override.example.com/computer")
	if got := releaseManifestBaseURLWithOverride(""); got != "https://env-override.example.com/computer" {
		t.Fatalf("env set, server unset: got %q, want env value", got)
	}

	if got := releaseManifestBaseURLWithOverride("https://server-dispatched.example.com/computer"); got != "https://server-dispatched.example.com/computer" {
		t.Fatalf("server wins over env: got %q, want server-dispatched value", got)
	}

	t.Setenv(ReleaseManifestBaseURLEnv, "")
	if got := releaseManifestBaseURLWithOverride("https://server-dispatched.example.com/computer"); got != "https://server-dispatched.example.com/computer" {
		t.Fatalf("server set, env unset: got %q, want server-dispatched value", got)
	}

	if got := releaseManifestBaseURLWithOverride("   "); got != DefaultReleaseManifestBaseURL {
		t.Fatalf("server value whitespace-only: got %q, want default (not blank)", got)
	}

	// The zero-arg form must still behave exactly as before (no server
	// context available), so existing callers (ReleaseWebURL, the CLI's own
	// `multica update`) are unaffected by this change.
	t.Setenv(ReleaseManifestBaseURLEnv, "https://env-override.example.com/computer")
	if got := releaseManifestBaseURL(); got != "https://env-override.example.com/computer" {
		t.Fatalf("zero-arg releaseManifestBaseURL() regressed: got %q", got)
	}
}

// TestFetchLatestReleaseWithOverrideUsesServerDispatchedBaseURL proves the
// daemon-facing entry point actually threads serverDispatched through to the
// HTTP request, not just through the pure string-precedence helper tested
// above.
func TestFetchLatestReleaseWithOverrideUsesServerDispatchedBaseURL(t *testing.T) {
	want := ReleaseManifest{TagName: "v0.3.83", Version: "0.3.83"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	t.Setenv(ReleaseManifestBaseURLEnv, "https://should-not-be-used.example.com/computer")

	got, err := FetchLatestReleaseWithOverride(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TagName != want.TagName {
		t.Fatalf("got %+v, want %+v (server-dispatched base URL should have won over the env var)", got, want)
	}
}

// TestFetchReleaseByTagWithOverrideUsesServerDispatchedBaseURL mirrors
// TestFetchLatestReleaseWithOverrideUsesServerDispatchedBaseURL for the
// per-version manifest fetch. Closes a gap Barry found reviewing #1561
// (2026-07-31): FetchLatestRelease (the "check for a new version" step)
// already had a WithOverride variant threading the server-dispatched base
// URL through, but fetchReleaseByTag (the "download and stage" step) did
// not — meaning a machine relying purely on server-dispatch with no local
// env var set could see a new version at check time and then fall back to
// the compiled default at download time, silently disagreeing with itself.
func TestFetchReleaseByTagWithOverrideUsesServerDispatchedBaseURL(t *testing.T) {
	want := ReleaseManifest{TagName: "v0.3.83", Version: "0.3.83"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0.3.83/manifest.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()

	t.Setenv(ReleaseManifestBaseURLEnv, "https://should-not-be-used.example.com/computer")

	got, err := fetchReleaseByTagWithOverride("v0.3.83", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TagName != want.TagName {
		t.Fatalf("got %+v, want %+v (server-dispatched base URL should have won over the env var)", got, want)
	}
}

func TestFetchLatestReleaseFallsBackToLegacyPathOnNotFound(t *testing.T) {
	want := ReleaseManifest{TagName: "v0.3.83", Version: "0.3.83"}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/manifest.json":
			w.WriteHeader(http.StatusNotFound)
		case "/latest.json":
			_ = json.NewEncoder(w).Encode(want)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	got, err := FetchLatestReleaseWithOverride(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TagName != want.TagName {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if strings.Join(paths, ",") != "/manifest.json,/latest.json" {
		t.Fatalf("request paths = %v, want new path followed by legacy path", paths)
	}
}

func TestFetchReleaseByTagFallsBackToLegacyPathOnNotFound(t *testing.T) {
	want := ReleaseManifest{TagName: "v0.3.83", Version: "0.3.83"}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/0.3.83/manifest.json":
			w.WriteHeader(http.StatusNotFound)
		case "/v0.3.83/release.json":
			_ = json.NewEncoder(w).Encode(want)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	got, err := fetchReleaseByTagWithOverride("0.3.83", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TagName != want.TagName {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if strings.Join(paths, ",") != "/0.3.83/manifest.json,/v0.3.83/release.json" {
		t.Fatalf("request paths = %v, want new path followed by legacy path", paths)
	}
}

func TestManifestCompatibilityFallbackFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "server error", statusCode: http.StatusInternalServerError},
		{name: "malformed canonical manifest", statusCode: http.StatusOK, body: "not-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallbackRequested := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/latest.json" {
					fallbackRequested = true
					_ = json.NewEncoder(w).Encode(ReleaseManifest{TagName: "v0.3.82", Version: "0.3.82"})
					return
				}
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			if _, err := FetchLatestReleaseWithOverride(server.URL); err == nil {
				t.Fatal("expected canonical manifest failure, got nil")
			}
			if fallbackRequested {
				t.Fatal("legacy path must not hide a broken canonical manifest")
			}
		})
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
