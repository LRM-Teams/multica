package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// buildTestTarGz returns a minimal .tar.gz containing one regular file
// entry named binaryName with the given content — enough for
// extractBinaryFromTarGz to find and return it.
func buildTestTarGz(t *testing.T, binaryName string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: binaryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestDownloadReleaseBinaryUsesManifestInlineSHA256 proves downloadReleaseBinary
// (task #815's combine: swap the download source from the retired GitHub-API
// two-step checksum-manifest flow to #1475/#1526's single manifest with an
// inline SHA-256 per platform) actually fetches, verifies, and extracts
// correctly end-to-end — not just that it compiles against the new type.
func TestDownloadReleaseBinaryUsesManifestInlineSHA256(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("archive format under test is tar.gz (non-Windows path)")
	}
	binaryName := "multica"
	content := []byte("fake-multica-binary-payload")
	archive := buildTestTarGz(t, binaryName, content)
	sum := sha256.Sum256(archive)
	expectedHex := hex.EncodeToString(sum[:])
	assetFileName := "multica-cli-1.2.3-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.2.3/manifest.json":
			manifest := ReleaseManifest{
				TagName: "v1.2.3",
				Version: "1.2.3",
				Platforms: map[string]ReleaseAsset{
					platformKey(runtime.GOOS, runtime.GOARCH): {
						URL:    server.URL + "/" + assetFileName,
						SHA256: expectedHex,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(manifest)
		case "/" + assetFileName:
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv(ReleaseManifestBaseURLEnv, server.URL)

	got, assetName, err := downloadReleaseBinary("v1.2.3", DefaultUpdateDownloadTimeout, "")
	if err != nil {
		t.Fatalf("downloadReleaseBinary: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("extracted binary = %q, want %q", got, content)
	}
	if assetName != assetFileName {
		t.Fatalf("assetName = %q, want %q", assetName, assetFileName)
	}
}

// TestDownloadReleaseBinaryRejectsChecksumMismatch proves a manifest whose
// inline SHA256 doesn't match the actual archive bytes fails closed instead
// of silently staging a corrupted/tampered binary.
func TestDownloadReleaseBinaryRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("archive format under test is tar.gz (non-Windows path)")
	}
	archive := buildTestTarGz(t, "multica", []byte("payload"))
	assetFileName := "multica-cli-1.2.3-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.2.3/manifest.json":
			manifest := ReleaseManifest{
				TagName: "v1.2.3",
				Version: "1.2.3",
				Platforms: map[string]ReleaseAsset{
					platformKey(runtime.GOOS, runtime.GOARCH): {
						URL:    server.URL + "/" + assetFileName,
						SHA256: "deadbeef00000000000000000000000000000000000000000000000000000000",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(manifest)
		case "/" + assetFileName:
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv(ReleaseManifestBaseURLEnv, server.URL)

	if _, _, err := downloadReleaseBinary("v1.2.3", DefaultUpdateDownloadTimeout, ""); err == nil {
		t.Fatal("expected checksum verification to fail, got nil error")
	}
}

func TestWriteReleaseScratchDoesNotTouchInstallPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	install := filepath.Join(home, ".local", "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(install), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("original-running-binary")
	if err := os.WriteFile(install, original, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("candidate-binary-v0.3.78")
	staged, err := WriteReleaseScratch("v0.3.78", candidate)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(staged)
	if err != nil || string(got) != string(candidate) {
		t.Fatalf("staged = %q, %v", got, err)
	}
	still, err := os.ReadFile(install)
	if err != nil || string(still) != string(original) {
		t.Fatal("install path was mutated by scratch stage")
	}
}

func TestWriteReleaseScratchRejectsNonReleaseTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := WriteReleaseScratch("bootstrap/v0.3.78-deadbeef", []byte("x")); err == nil {
		t.Fatal("expected non-release tag reject")
	}
}
