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
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const DefaultUpdateDownloadTimeout = 120 * time.Second

// ReleaseManifestBaseURL is the read-only, publicly served release feed. It
// is hosted alongside the app (not GitHub) because the CLI/daemon have no
// credential that can read the private LRM-Teams/multica repo: an
// unauthenticated GitHub Releases API/asset request from a bare install
// always 404s. Caddy serves this path from an immutable per-version
// directory tree that CI populates on tag; see deploy/aliyun/Caddyfile.
const ReleaseManifestBaseURL = "https://cdn.leagent.me/computer"
const ReleaseWebURL = "https://cdn.leagent.me/computer"
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

// ReleaseManifest is the schema published at {ReleaseManifestBaseURL}/latest.json
// (the promoted pointer) and at {ReleaseManifestBaseURL}/{tag}/release.json
// (the immutable per-version copy written before promotion). Platforms keys
// are "<goos>-<goarch>", matching runtime.GOOS/GOARCH.
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

type GitHubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
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
// published manifest. Unlike the old GitHub-asset-name scan, there is no
// legacy-name fallback: this manifest format was introduced with the
// /downloads feed, so every entry it ever contains uses this one shape.
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

// fetchManifest GETs and JSON-decodes a ReleaseManifest from url. Shared by
// FetchLatestRelease (the promoted "latest.json" pointer) and
// fetchReleaseByTag (an immutable per-version "release.json").
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release manifest request returned %d", resp.StatusCode)
	}

	var manifest ReleaseManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// fetchReleaseByTag fetches the immutable per-version manifest for tag. Used
// by UpdateViaDownload, which may target a specific version (a server-pushed
// update or a rollback) rather than whatever is currently latest.
func fetchReleaseByTag(tag string) (*ReleaseManifest, error) {
	return fetchManifest(ReleaseManifestBaseURL + "/" + tag + "/release.json")
}

// FetchLatestRelease fetches the promoted "latest" release manifest from the
// Multica release feed.
func FetchLatestRelease() (*ReleaseManifest, error) {
	return fetchManifest(ReleaseManifestBaseURL + "/latest.json")
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

// UpdateViaDownload downloads the latest release binary from GitHub and replaces
// the current executable in-place. Returns the combined output message and any error.
func UpdateViaDownload(targetVersion string) (string, error) {
	return UpdateViaDownloadWithTimeout(targetVersion, DefaultUpdateDownloadTimeout)
}

// UpdateViaDownloadWithTimeout downloads the latest release binary with a caller-selected timeout.
func UpdateViaDownloadWithTimeout(targetVersion string, downloadTimeout time.Duration) (string, error) {
	// Determine current binary path.
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	targetPath, err := updateTargetPath(exePath)
	if err != nil {
		return "", err
	}

	tag := normalizeReleaseTag(targetVersion)
	release, err := fetchReleaseByTag(tag)
	if err != nil {
		return "", fmt.Errorf("fetch release metadata: %w", err)
	}
	asset, err := findPlatformAsset(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	assetName := path.Base(asset.URL)

	// Buffer the archive into memory so we can verify the full SHA-256
	// before writing anything to disk. Release archives are ~10–30 MB; the
	// extraction code already buffers zip archives in full (random access
	// requirement), so this is not a new memory cost on Windows. For tar.gz
	// it adds a single in-RAM copy, which is preferable to running the
	// untrusted bytes through gzip+tar extraction before the SHA-256 check.
	timeout := updateDownloadTimeoutOrDefault(downloadTimeout)
	archiveData, err := fetchURLBytes(asset.URL, timeout)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if err := verifyAssetSHA256(archiveData, asset.SHA256, assetName); err != nil {
		// Do NOT extract or replace; the next poll tick will retry. A
		// corrupted asset is rare enough that retrying through the same
		// CDN is the right default; persistent failures will surface in
		// the daemon log.
		return "", fmt.Errorf("verify download: %w", err)
	}

	// Extract the binary from the archive.
	binaryName := "multica"
	if runtime.GOOS == "windows" {
		binaryName = "multica.exe"
	}
	var binaryData []byte
	if runtime.GOOS == "windows" {
		binaryData, err = extractBinaryFromZip(bytes.NewReader(archiveData), binaryName)
	} else {
		binaryData, err = extractBinaryFromTarGz(bytes.NewReader(archiveData), binaryName)
	}
	if err != nil {
		return "", fmt.Errorf("extract binary: %w", err)
	}

	// Atomic replace: write to temp file, then rename over the original.
	dir := filepath.Dir(targetPath)
	tmpFile, err := os.CreateTemp(dir, "multica-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(binaryData); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Preserve original file permissions.
	info, err := os.Stat(targetPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("stat original binary: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp file: %w", err)
	}

	// Replace the original binary. On Windows this moves the running executable
	// aside first; on Unix a plain rename over the running inode is fine.
	if err := replaceBinary(tmpPath, targetPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("replace binary: %w", err)
	}

	return fmt.Sprintf("Downloaded %s and replaced %s", assetName, targetPath), nil
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
