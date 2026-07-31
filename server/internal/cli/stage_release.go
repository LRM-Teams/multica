package cli

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"runtime"
	"time"
)

// StageReleaseResult is the immutable-stage outcome for a release download.
// The current process executable is never overwritten by this path.
type StageReleaseResult struct {
	Staged    StagedVersion
	AssetName string
	Message   string
}

// DownloadAndStageRelease fetches a release asset, verifies checksums, and
// stages the binary into the VersionStore under the release tag. It never
// renames onto the running executable (CUT-T1/T2 prerequisite).
//
// serverDispatched is the same three-layer-precedence override
// FetchLatestReleaseWithOverride takes for the "check for a new version"
// step — pass "" when the caller has no server-dispatched value (e.g. the
// CLI's own `multica update`, which has no daemon connection). Callers that
// do have one (the daemon) must pass it here too, or a machine relying
// purely on server-dispatch could see a new version at check time and then
// silently fall back to the compiled default at download time.
func DownloadAndStageRelease(
	ctx context.Context,
	store *VersionStore,
	targetVersion string,
	downloadTimeout time.Duration,
	serverDispatched string,
) (StageReleaseResult, error) {
	if store == nil {
		return StageReleaseResult{}, fmt.Errorf("version store is required")
	}
	tag := normalizeReleaseTag(targetVersion)
	if !IsReleaseVersion(tag) {
		return StageReleaseResult{}, fmt.Errorf("invalid release version %q", targetVersion)
	}

	binary, assetName, err := downloadReleaseBinary(tag, downloadTimeout, serverDispatched)
	if err != nil {
		return StageReleaseResult{}, err
	}
	return StageReleaseBytes(ctx, store, tag, binary, assetName)
}

// StageReleaseBytes stages already-downloaded binary bytes into the store.
// Used by DownloadAndStageRelease and unit tests. Never touches the process exe.
func StageReleaseBytes(
	ctx context.Context,
	store *VersionStore,
	targetVersion string,
	binary []byte,
	assetName string,
) (StageReleaseResult, error) {
	if store == nil {
		return StageReleaseResult{}, fmt.Errorf("version store is required")
	}
	if len(binary) == 0 {
		return StageReleaseResult{}, fmt.Errorf("empty binary payload")
	}
	tag, err := normalizeVersionStoreTag(targetVersion)
	if err != nil {
		return StageReleaseResult{}, err
	}
	digest := bytesSHA256(binary)
	var mode fs.FileMode = 0o755
	staged, err := store.StageBinary(ctx, tag, binary, digest, mode)
	if err != nil {
		return StageReleaseResult{}, fmt.Errorf("stage release %s: %w", tag, err)
	}
	msg := fmt.Sprintf("Staged %s into version store at %s (asset %s); Active unchanged",
		staged.Version, staged.BinaryPath, assetName)
	return StageReleaseResult{
		Staged:    staged,
		AssetName: assetName,
		Message:   msg,
	}, nil
}

// downloadReleaseBinary downloads and extracts the multica binary for tag
// from the Multica release feed (#1475/#1526's manifest, not the retired
// unauthenticated GitHub API path). The manifest carries each platform's
// SHA-256 inline (ReleaseAsset.SHA256), so unlike the old GitHub-asset flow
// this needs no separate checksum-manifest fetch/parse step.
func downloadReleaseBinary(tag string, downloadTimeout time.Duration, serverDispatched string) ([]byte, string, error) {
	tag = normalizeReleaseTag(tag)
	release, err := fetchReleaseByTagWithOverride(tag, serverDispatched)
	if err != nil {
		return nil, "", fmt.Errorf("fetch release metadata: %w", err)
	}
	asset, err := findPlatformAsset(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, "", err
	}
	assetName := path.Base(asset.URL)
	timeout := updateDownloadTimeoutOrDefault(downloadTimeout)
	archiveData, err := fetchURLBytes(asset.URL, timeout)
	if err != nil {
		return nil, "", fmt.Errorf("download failed: %w", err)
	}
	if err := verifyAssetSHA256(archiveData, asset.SHA256, assetName); err != nil {
		return nil, "", fmt.Errorf("verify download: %w", err)
	}
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
		return nil, "", fmt.Errorf("extract binary: %w", err)
	}
	return binaryData, assetName, nil
}
