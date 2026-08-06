package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DefaultUpdateDownloadTimeout = 120 * time.Second

// DefaultReleaseManifestBaseURL is the read-only, publicly served release
// feed. It is hosted alongside the app (not GitHub) because the CLI/daemon
// have no credential that can read the private LRM-Teams/multica repo: an
// unauthenticated GitHub Releases API/asset request from a bare install
// always 404s.
//
// release.yml publishes the feed to OSS and verifies the same bytes through
// this CDN URL before completing a release. Consumers never depend on the
// storage provider's bucket hostname.
const DefaultReleaseManifestBaseURL = "https://cdn.leagent.me/computer"

// ReleaseManifestBaseURLEnv overrides DefaultReleaseManifestBaseURL when set,
// with no rebuild/release required. Exists because the default address is a
// single fixed domain: if it gets blocked on some machine's network edge (the
// 2026-07-30 incident) or needs to move before a new release ships, this is
// the only way to redirect an already-installed CLI/daemon. Mirrors Raft
// Computer's DEFAULT_UPGRADE_BASE_URL / RAFT_COMPUTER_UPGRADE_BASE_URL shape.
const ReleaseManifestBaseURLEnv = "MULTICA_RELEASE_MANIFEST_BASE_URL"

// releaseManifestBaseURL resolves the effective release feed base URL:
// ReleaseManifestBaseURLEnv when set to a non-blank value, DefaultReleaseManifestBaseURL
// otherwise. Read fresh on every call (not cached at package init) so a
// changed env var takes effect on the next update check without a restart
// where the caller already re-execs per check (the CLI does; the daemon's
// auto-update poll loop does too).
func releaseManifestBaseURL() string {
	return releaseManifestBaseURLWithOverride("")
}

// releaseManifestBaseURLWithOverride resolves the effective release feed base
// URL with the full three-layer precedence (task #815 step 2):
// serverDispatched (from the daemon's heartbeat ack) > ReleaseManifestBaseURLEnv
// > DefaultReleaseManifestBaseURL. serverDispatched is blank for every caller
// that has no daemon/server context (the CLI's own `multica update`,
// ReleaseWebURL); only the daemon's auto-update loop can supply it, since only
// it holds a live connection to ask the server in the first place.
func releaseManifestBaseURLWithOverride(serverDispatched string) string {
	if v := strings.TrimSpace(serverDispatched); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(ReleaseManifestBaseURLEnv)); v != "" {
		return v
	}
	return DefaultReleaseManifestBaseURL
}

// ReleaseWebURL is the human-facing form of releaseManifestBaseURL(), for
// error messages that point a user at the release source. Tracks the same
// override so a redirected machine's error text matches where it actually
// looked.
func ReleaseWebURL() string {
	return releaseManifestBaseURL()
}

// OfficialCloudAPIHost is the hostname of Multica's hosted cloud API — the
// single source of truth for "is this server the official cloud" checks
// (daemon/config.go's officialCloudHost mirrors this) and for `multica
// setup`'s default ServerURL (cmd_setup.go). Task #29 (domain unification,
// 2026-07-31) found no Caddy/infra routing anywhere in this repo for
// api.leagent.me, and the backend is still not reachable there. Flipping this
// before the backend is actually routed would break `multica setup` for every
// new install. Flip this one constant to "api.leagent.me" once infra confirms
// it's routed and
// has a valid cert; every caller picks it up automatically.
const OfficialCloudAPIHost = "api.multica.ai"

// OfficialCloudAPIURL is OfficialCloudAPIHost as a full https:// base URL,
// for callers that need a URL rather than a bare host.
const OfficialCloudAPIURL = "https://" + OfficialCloudAPIHost

const LegacyBrewPackage = "multica-ai/tap/multica"

// BrewPackage returns the optional Homebrew package name to upgrade. It is
// intentionally not defaulted: the public installer must not fall back to the
// old upstream tap when the LRM repo is the release source.
func BrewPackage() string {
	return strings.TrimSpace(os.Getenv("MULTICA_BREW_PACKAGE"))
}

// IsLegacyBrewPackage reports whether pkg points at the old upstream tap that
// is not authoritative for LRM-Teams/multica releases.
func IsLegacyBrewPackage(pkg string) bool {
	return strings.EqualFold(strings.TrimSpace(pkg), LegacyBrewPackage)
}

// IsBrewUpdateConfigured reports whether the current environment has an
// explicit Homebrew package to use for CLI updates. The old upstream tap is
// intentionally ignored so LRM builds fall back to the repo release assets.
func IsBrewUpdateConfigured() bool {
	pkg := BrewPackage()
	return pkg != "" && !IsLegacyBrewPackage(pkg)
}

// ReleaseManifest is the schema used by both generations of the release feed.
// The canonical paths are /manifest.json and /{version}/manifest.json. During
// the staged migration, clients fall back on 404 to the existing /latest.json
// and /{tag}/release.json paths. Platform keys are "<goos>-<goarch>", matching
// runtime.GOOS/GOARCH.
type ReleaseManifest struct {
	TagName   string                  `json:"tag"`
	Version   string                  `json:"version"`
	Platforms map[string]ReleaseAsset `json:"platforms"`
}

// ReleaseAsset is one platform's archive: a direct URL plus the SHA-256 the
// CI publish job computed from GoReleaser's checksums.txt at publish time.
// Carrying the checksum inline (rather than a separate manifest asset, as
// GitHub Releases required) means one fetch proves both "what to download"
// and "what it must hash to" — no second round trip before the archive.
type ReleaseAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// IsReleaseVersion reports whether v looks like a tagged release version
// (e.g. "0.1.13", "v0.1.13") rather than a dev build (e.g. an empty version
// or a `git describe`–style "v0.2.15-235-gdaf0e935"). The auto-update poller
// uses this to skip self-update for source builds, where downgrading to a
// public release would clobber unreleased changes.
func IsReleaseVersion(v string) bool {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// IsNewerVersion reports whether latest is strictly newer than current. Both
// arguments may carry an optional "v" prefix; non-numeric tails are ignored
// (a 4th component, pre-release tag, etc.). Returns false if either side
// cannot be parsed — the caller treats that as "stay on current".
func IsNewerVersion(latest, current string) bool {
	l, ok := parseReleaseVersion(latest)
	if !ok {
		return false
	}
	c, ok := parseReleaseVersion(current)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseReleaseVersion extracts the three numeric components of v. Returns
// (parts, true) on success; (_, false) when v is missing, malformed, or
// carries any non-numeric tail (a dev-describe suffix, a 4th component, a
// pre-release tag, etc.). The strict shape is intentional: this is the only
// parser used by IsNewerVersion, and the autoUpdateLoop must never silently
// downgrade a developer build to a public release just because the
// dev-describe patch happened to look numeric after trimming.
func parseReleaseVersion(v string) ([3]int, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" {
			return [3]int{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return [3]int{}, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func normalizeReleaseTag(targetVersion string) string {
	tag := strings.TrimSpace(targetVersion)
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return tag
}

// platformKey is the Platforms map key the CI publish job uses for a given
// OS/arch pair — see scripts/publish-release-manifest.sh.
func platformKey(goos, goarch string) string {
	return goos + "-" + goarch
}

// findPlatformAsset looks up the archive + checksum for goos/goarch in a
// published manifest. There is no legacy-name fallback: this manifest format
// was introduced with the /downloads feed, so every entry it ever contains
// uses this one shape.
func findPlatformAsset(manifest *ReleaseManifest, goos, goarch string) (*ReleaseAsset, error) {
	key := platformKey(goos, goarch)
	asset, ok := manifest.Platforms[key]
	if !ok {
		return nil, fmt.Errorf("no published release asset for platform %q", key)
	}
	return &asset, nil
}

// verifyAssetSHA256 returns nil when the SHA-256 of data matches the lowercase
// hex expected value, or an error otherwise. The error includes both digests
// so a corrupted asset is diagnosable from the log without re-downloading.
func verifyAssetSHA256(data []byte, expectedHex, assetName string) error {
	if expectedHex == "" {
		return fmt.Errorf("empty expected checksum for %q", assetName)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expectedHex) {
		return fmt.Errorf("checksum mismatch for %q: expected %s, got %s", assetName, expectedHex, actual)
	}
	return nil
}

var errReleaseManifestNotFound = errors.New("release manifest not found")

// fetchManifest GETs and JSON-decodes a ReleaseManifest from url.
func fetchManifest(url string) (*ReleaseManifest, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", errReleaseManifestNotFound, url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release manifest request returned %d", resp.StatusCode)
	}

	var manifest ReleaseManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// fetchManifestWithNotFoundFallback reads the new path first and falls back
// only when it is explicitly absent. Network failures, server errors, and
// malformed manifests fail closed so a broken canonical feed cannot be hidden
// by a stale compatibility copy.
func fetchManifestWithNotFoundFallback(primaryURL, fallbackURL string) (*ReleaseManifest, error) {
	manifest, err := fetchManifest(primaryURL)
	if err == nil {
		return manifest, nil
	}
	if !errors.Is(err, errReleaseManifestNotFound) {
		return nil, err
	}
	return fetchManifest(fallbackURL)
}

// fetchReleaseByTagWithOverride prefers the canonical immutable manifest and
// falls back to the existing tagged release path while deployed daemons move
// through the naming migration. The same server-dispatched base URL is used
// for both attempts.
func fetchReleaseByTagWithOverride(tag, serverDispatched string) (*ReleaseManifest, error) {
	baseURL := strings.TrimRight(releaseManifestBaseURLWithOverride(serverDispatched), "/")
	tag = normalizeReleaseTag(tag)
	version := strings.TrimPrefix(tag, "v")
	return fetchManifestWithNotFoundFallback(
		baseURL+"/"+version+"/manifest.json",
		baseURL+"/"+tag+"/release.json",
	)
}

// FetchReleaseByTagWithOverride is the exported form of
// fetchReleaseByTagWithOverride, used by the daemon's pin-install path.
func FetchReleaseByTagWithOverride(tag, serverDispatched string) (*ReleaseManifest, error) {
	return fetchReleaseByTagWithOverride(tag, serverDispatched)
}

// FetchLatestRelease fetches the promoted release manifest.
func FetchLatestRelease() (*ReleaseManifest, error) {
	return FetchLatestReleaseWithOverride("")
}

// FetchLatestReleaseWithOverride prefers the canonical root manifest and
// falls back to latest.json while deployed daemons move through the naming
// migration. The server-dispatched top layer of the base-URL precedence is
// applied to both attempts.
func FetchLatestReleaseWithOverride(serverDispatched string) (*ReleaseManifest, error) {
	baseURL := strings.TrimRight(releaseManifestBaseURLWithOverride(serverDispatched), "/")
	return fetchManifestWithNotFoundFallback(
		baseURL+"/manifest.json",
		baseURL+"/latest.json",
	)
}

// knownBrewPrefixes lists the install roots Homebrew uses on each platform.
// Order is irrelevant — the prefixes do not nest.
var knownBrewPrefixes = []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"}

// MatchKnownBrewPrefix returns the Homebrew prefix whose Cellar contains path,
// or "" if path is not under a known Cellar. It is the offline equivalent of
// `brew --prefix`: callers reach for it when `brew --prefix` is unavailable
// (brew not on PATH) but the binary's path still betrays its install root.
func MatchKnownBrewPrefix(path string) string {
	path = filepath.ToSlash(path)
	for _, prefix := range knownBrewPrefixes {
		if strings.HasPrefix(path, prefix+"/Cellar/") {
			return prefix
		}
	}
	return ""
}

// IsBrewInstall checks whether the running multica binary was installed via Homebrew.
func IsBrewInstall() bool {
	exePath, err := os.Executable()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolved = exePath
	}

	brewPrefix := GetBrewPrefix()
	if brewPrefix != "" && strings.HasPrefix(resolved, brewPrefix) {
		return true
	}

	return MatchKnownBrewPrefix(resolved) != ""
}

// GetBrewPrefix returns the Homebrew prefix by running `brew --prefix`, or empty string.
func GetBrewPrefix() string {
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func updateTargetPath(exePath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve symlink: %w", err)
	}
	return updateTargetPathFromResolved(resolved), nil
}

func updateTargetPathFromResolved(resolved string) string {
	if prefix := MatchKnownBrewPrefix(resolved); prefix != "" {
		return prefix + "/bin/multica"
	}
	return resolved
}

// UpdateViaBrew runs the explicitly configured Homebrew upgrade package.
// Returns the combined output and any error.
func UpdateViaBrew() (string, error) {
	pkg := BrewPackage()
	if pkg == "" {
		return "", fmt.Errorf("Homebrew package is not configured; set MULTICA_BREW_PACKAGE or use direct download")
	}
	if IsLegacyBrewPackage(pkg) {
		return "", fmt.Errorf("Homebrew package %q is the legacy upstream tap; use direct LRM release download", pkg)
	}
	cmd := exec.Command("brew", "upgrade", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("brew upgrade failed: %w", err)
	}
	return string(out), nil
}

func updateDownloadTimeoutOrDefault(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultUpdateDownloadTimeout
	}
	return timeout
}

// fetchURLBytes does a GET with the given timeout and returns the response
// body in full. Used for the checksum manifest (tiny) and the release
// archive (single-digit MB). The checksum verification path requires buffered
// bytes so streaming would just push the buffer into the caller anyway.
func fetchURLBytes(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: updateDownloadTimeoutOrDefault(timeout)}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// extractBinaryFromTarGz reads a .tar.gz stream and returns the contents of the
// named file entry.
func extractBinaryFromTarGz(r io.Reader, name string) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("binary %q not found in archive", name)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		// Match the binary name (may be prefixed with a directory).
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read binary: %w", err)
			}
			return data, nil
		}
	}
}

// extractBinaryFromZip reads a .zip stream and returns the contents of the
// named file entry. The zip format requires random access, so the full archive
// is buffered in memory.
func extractBinaryFromZip(r io.Reader, name string) ([]byte, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read zip data: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) == name && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry: %w", err)
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, fmt.Errorf("read binary: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", name)
}
