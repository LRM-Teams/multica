package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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

func TestStageReleaseBytesDoesNotTouchSiblingExecutable(t *testing.T) {
	// CUT-T1 foundation: stage lands only under versions/; a sibling "Active"
	// path that mimics today's self-replace target must stay byte-identical.
	root := t.TempDir()
	fakeExe := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(fakeExe), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("original-running-binary-v0.3.77")
	if err := os.WriteFile(fakeExe, original, 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}

	store, err := NewVersionStore(filepath.Join(root, "store"), "linux", func(context.Context, string, string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	candidate := []byte("candidate-binary-v0.3.78-payload")
	result, err := StageReleaseBytes(
		context.Background(),
		store,
		"v0.3.78",
		candidate,
		"multica_0.3.78_linux_amd64.tar.gz",
	)
	if err != nil {
		t.Fatalf("StageReleaseBytes: %v", err)
	}
	if result.Staged.Version != "v0.3.78" {
		t.Fatalf("staged version = %q", result.Staged.Version)
	}
	if _, err := os.Stat(result.Staged.BinaryPath); err != nil {
		t.Fatalf("staged binary missing: %v", err)
	}
	stagedBytes, err := os.ReadFile(result.Staged.BinaryPath)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(stagedBytes) != string(candidate) {
		t.Fatalf("staged bytes mismatch")
	}

	// Fake exe must be unchanged (no self-replace).
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatalf("read fake exe: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("executable was mutated by stage path; self-replace leak")
	}

	// Activation still empty — stage alone does not CAS.
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if state.Generation != 0 || state.ActiveVersion != "" {
		t.Fatalf("stage mutated ActivationState: %+v", state)
	}
}

func TestStageReleaseBytesRejectsNonReleaseTag(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	_, err := StageReleaseBytes(context.Background(), store, "bootstrap/v0.3.78-deadbeef", []byte("x"), "a")
	if err == nil {
		t.Fatal("expected non-release tag reject")
	}
}
