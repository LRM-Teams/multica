package cli

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"
)

// WriteReleaseScratch writes already-downloaded bytes to ephemeral staging.
// Computer never executes this path.
func WriteReleaseScratch(targetVersion string, binary []byte) (string, error) {
	tag := normalizeReleaseTag(targetVersion)
	if !IsReleaseVersion(tag) {
		return "", fmt.Errorf("invalid release version %q", targetVersion)
	}
	if len(binary) == 0 {
		return "", fmt.Errorf("empty binary payload")
	}
	root, err := MachineStateRoot()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(root, "upgrade-staging", tag, installBinaryName())
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, binary, 0o755); err != nil {
		return "", err
	}
	return dest, nil
}

// StageReleaseScratch downloads a release into an ephemeral staging file.
func StageReleaseScratch(targetVersion string, downloadTimeout time.Duration, serverDispatched string) (string, error) {
	tag := normalizeReleaseTag(targetVersion)
	if !IsReleaseVersion(tag) {
		return "", fmt.Errorf("invalid release version %q", targetVersion)
	}
	binary, _, err := downloadReleaseBinary(tag, downloadTimeout, serverDispatched)
	if err != nil {
		return "", err
	}
	return WriteReleaseScratch(tag, binary)
}

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
