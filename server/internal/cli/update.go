package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// ReleaseManifestBaseURL is the exported form of releaseManifestBaseURL for the
// daemon's auto-update detection loop (which lives in a different package). It
// reads fresh on every call so an env override takes effect on the next check.
func ReleaseManifestBaseURL() string {
	return releaseManifestBaseURL()
}

func ReleaseManifestBaseURLWithOverride(serverDispatched string) string {
	return releaseManifestBaseURLWithOverride(serverDispatched)
}

// releaseManifestBaseURLWithOverride resolves the effective release feed base
// URL with the full three-layer precedence (task #815 step 2):
// serverDispatched (from the daemon's heartbeat ack) > ReleaseManifestBaseURLEnv
// > DefaultReleaseManifestBaseURL. serverDispatched is blank for every caller
// that has no daemon/server context (including an offline `computer upgrade`);
// only the daemon's release-detection loop can supply it, since only it holds a
// live connection to ask the server in the first place.
func releaseManifestBaseURLWithOverride(serverDispatched string) string {
	if v := strings.TrimSpace(serverDispatched); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(ReleaseManifestBaseURLEnv)); v != "" {
		return v
	}
	return DefaultReleaseManifestBaseURL
}

// ReleaseManifest describes one immutable Computer release. Exact-version
// manifests live at /{version}/manifest.json. Mutable environment selection is
// owned exclusively by /metainfo.json. Platform keys are "<goos>-<goarch>",
// matching runtime.GOOS/GOARCH.
type ReleaseManifest struct {
	TagName   string                  `json:"tag"`
	Version   string                  `json:"version"`
	Platforms map[string]ReleaseAsset `json:"platforms"`
}

// ReleaseMetainfo is the single mutable Computer release pointer. Production
// and test intentionally share this document so a consumer cannot accidentally
// combine independent channel files from different publication generations.
type ReleaseMetainfo struct {
	SchemaVersion int                        `json:"schema_version"`
	Environments  map[string]ReleaseManifest `json:"environments"`
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

func normalizeReleaseTag(targetVersion string) string {
	tag := strings.TrimSpace(targetVersion)
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return tag
}

// NormalizeReleaseTag is the exported form of normalizeReleaseTag for the
// daemon's auto-update detection loop.
func NormalizeReleaseTag(targetVersion string) string {
	return normalizeReleaseTag(targetVersion)
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
		return nil, fmt.Errorf("release manifest not found: %s", url)
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

func fetchReleaseMetainfo(url string) (*ReleaseMetainfo, error) {
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
		return nil, fmt.Errorf("release metainfo not found: %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release metainfo request returned %d", resp.StatusCode)
	}

	var metainfo ReleaseMetainfo
	if err := json.NewDecoder(resp.Body).Decode(&metainfo); err != nil {
		return nil, err
	}
	if metainfo.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported release metainfo schema_version %d", metainfo.SchemaVersion)
	}
	return &metainfo, nil
}

// fetchReleaseByTagWithOverride reads the immutable manifest for one exact
// version. The same server-dispatched base URL is used for every exact fetch.
func fetchReleaseByTagWithOverride(tag, serverDispatched string) (*ReleaseManifest, error) {
	baseURL := strings.TrimRight(releaseManifestBaseURLWithOverride(serverDispatched), "/")
	tag = normalizeReleaseTag(tag)
	version := strings.TrimPrefix(tag, "v")
	return fetchManifest(baseURL + "/" + version + "/manifest.json")
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

// FetchLatestReleaseWithOverride selects production from metainfo.json. The
// server-dispatched top layer of the base-URL precedence is applied to it.
func FetchLatestReleaseWithOverride(serverDispatched string) (*ReleaseManifest, error) {
	return FetchReleaseForChannelWithOverride(ReleaseChannelLatest, serverDispatched)
}

// FetchReleaseForChannelWithOverride selects one environment from the single
// mutable metainfo document. There is no channel-file fallback: a missing or
// malformed source of truth is a release-feed failure, not permission to read
// stale metadata.
func FetchReleaseForChannelWithOverride(channel ReleaseChannel, serverDispatched string) (*ReleaseManifest, error) {
	channel, err := NormalizeReleaseChannel(string(channel))
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(releaseManifestBaseURLWithOverride(serverDispatched), "/")
	metainfo, err := fetchReleaseMetainfo(baseURL + "/metainfo.json")
	if err != nil {
		return nil, err
	}
	environment := "production"
	if channel == ReleaseChannelAlpha {
		environment = "test"
	}
	selected, ok := metainfo.Environments[environment]
	if !ok {
		return nil, fmt.Errorf("release metainfo is missing %s environment", environment)
	}
	manifest := &selected
	if err := validateChannelManifest(channel, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func validateChannelManifest(channel ReleaseChannel, manifest *ReleaseManifest) error {
	if manifest == nil {
		return fmt.Errorf("%s release manifest is empty", channel)
	}
	tag := strings.TrimSpace(manifest.TagName)
	version := strings.TrimSpace(manifest.Version)
	if tag == "" || version == "" || normalizeReleaseTag(version) != normalizeReleaseTag(tag) {
		return fmt.Errorf("%s release manifest has inconsistent tag/version", channel)
	}
	switch channel {
	case ReleaseChannelLatest:
		if !IsStableReleaseVersion(tag) {
			return fmt.Errorf("latest release manifest must point to a stable vX.Y.Z version, got %q", tag)
		}
	case ReleaseChannelAlpha:
		if !IsPrereleaseVersion(tag) {
			return fmt.Errorf("alpha release manifest must point to an alpha.N, beta.N, or rc.N version, got %q", tag)
		}
	}
	return nil
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
