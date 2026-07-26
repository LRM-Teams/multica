package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	versionStoreSchemaVersion = 1
	versionMetadataName       = "version.json"
	activationStateName       = "activation.json"
	activationLockName        = "activation.lock"
)

var ErrActivationConflict = errors.New("activation generation conflict")

type BinaryVerifier func(ctx context.Context, binaryPath, expectedVersion string) error

type VersionStore struct {
	root     string
	goos     string
	verifier BinaryVerifier
}

type StagedVersion struct {
	Version    string
	BinaryPath string
	SHA256     string
}

type ActivationState struct {
	SchemaVersion   int       `json:"schema_version"`
	ActiveVersion   string    `json:"active_version"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	Generation      uint64    `json:"generation"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type versionMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	BinarySHA256  string `json:"binary_sha256"`
}

func NewVersionStore(root, goos string, verifier BinaryVerifier) (*VersionStore, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return nil, errors.New("version store root is required")
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if verifier == nil {
		verifier = VerifyStagedBinaryVersion
	}
	store := &VersionStore{
		root:     root,
		goos:     goos,
		verifier: verifier,
	}
	if err := os.MkdirAll(store.VersionsRoot(), 0o755); err != nil {
		return nil, fmt.Errorf("create versions root: %w", err)
	}
	return store, nil
}

func DefaultVersionStoreRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "multica"), nil
}

func (s *VersionStore) Root() string {
	return s.root
}

func (s *VersionStore) VersionsRoot() string {
	return filepath.Join(s.root, "versions")
}

func (s *VersionStore) binaryName() string {
	if s.goos == "windows" {
		return "multica.exe"
	}
	return "multica"
}

func normalizeVersionStoreTag(version string) (string, error) {
	tag := normalizeReleaseTag(version)
	if !IsReleaseVersion(tag) {
		return "", fmt.Errorf("invalid release version %q", version)
	}
	if filepath.Base(tag) != tag || strings.ContainsAny(tag, `/\`) {
		return "", fmt.Errorf("invalid release version path %q", version)
	}
	return tag, nil
}

func (s *VersionStore) stagedVersion(version string) (StagedVersion, error) {
	tag, err := normalizeVersionStoreTag(version)
	if err != nil {
		return StagedVersion{}, err
	}
	return StagedVersion{
		Version:    tag,
		BinaryPath: filepath.Join(s.VersionsRoot(), tag, s.binaryName()),
	}, nil
}

func normalizeSHA256(expected string) (string, error) {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if len(expected) != sha256.Size*2 {
		return "", fmt.Errorf("invalid SHA-256 length %d", len(expected))
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return "", fmt.Errorf("invalid SHA-256: %w", err)
	}
	return expected, nil
}

func bytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeSyncedFile(path string, data []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (s *VersionStore) StageBinary(
	ctx context.Context,
	version string,
	binary []byte,
	expectedBinarySHA256 string,
	mode fs.FileMode,
) (StagedVersion, error) {
	staged, err := s.stagedVersion(version)
	if err != nil {
		return StagedVersion{}, err
	}
	expectedDigest, err := normalizeSHA256(expectedBinarySHA256)
	if err != nil {
		return StagedVersion{}, err
	}
	actualDigest := bytesSHA256(binary)
	if actualDigest != expectedDigest {
		return StagedVersion{}, fmt.Errorf(
			"checksum mismatch for staged %s: expected %s, got %s",
			staged.Version,
			expectedDigest,
			actualDigest,
		)
	}
	if mode.Perm() == 0 {
		mode = 0o755
	}

	if existing, err := s.verifyExisting(ctx, staged.Version, expectedDigest); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return StagedVersion{}, err
	}

	tempDir, err := os.MkdirTemp(s.VersionsRoot(), ".stage-"+staged.Version+"-*")
	if err != nil {
		return StagedVersion{}, fmt.Errorf("create stage directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tempBinary := filepath.Join(tempDir, s.binaryName())
	if err := writeSyncedFile(tempBinary, binary, mode.Perm()); err != nil {
		return StagedVersion{}, fmt.Errorf("write staged binary: %w", err)
	}
	if err := os.Chmod(tempBinary, mode.Perm()); err != nil {
		return StagedVersion{}, fmt.Errorf("chmod staged binary: %w", err)
	}
	if err := s.verifier(ctx, tempBinary, staged.Version); err != nil {
		return StagedVersion{}, fmt.Errorf("verify staged binary %s: %w", staged.Version, err)
	}

	metadata := versionMetadata{
		SchemaVersion: versionStoreSchemaVersion,
		Version:       staged.Version,
		BinarySHA256:  expectedDigest,
	}
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return StagedVersion{}, fmt.Errorf("encode version metadata: %w", err)
	}
	metadataJSON = append(metadataJSON, '\n')
	if err := writeSyncedFile(filepath.Join(tempDir, versionMetadataName), metadataJSON, 0o644); err != nil {
		return StagedVersion{}, fmt.Errorf("write version metadata: %w", err)
	}
	if err := syncDirPath(tempDir); err != nil {
		return StagedVersion{}, fmt.Errorf("sync stage directory: %w", err)
	}

	finalDir := filepath.Dir(staged.BinaryPath)
	if err := os.Rename(tempDir, finalDir); err != nil {
		if existing, verifyErr := s.verifyExisting(ctx, staged.Version, expectedDigest); verifyErr == nil {
			return existing, nil
		}
		return StagedVersion{}, fmt.Errorf("publish immutable version %s: %w", staged.Version, err)
	}
	if err := syncDirPath(s.VersionsRoot()); err != nil {
		return StagedVersion{}, fmt.Errorf("sync versions root: %w", err)
	}
	staged.SHA256 = expectedDigest
	return staged, nil
}

func (s *VersionStore) verifyExisting(
	ctx context.Context,
	version string,
	expectedDigest string,
) (StagedVersion, error) {
	staged, err := s.stagedVersion(version)
	if err != nil {
		return StagedVersion{}, err
	}
	metadataPath := filepath.Join(filepath.Dir(staged.BinaryPath), versionMetadataName)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return StagedVersion{}, err
	}
	var metadata versionMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return StagedVersion{}, fmt.Errorf("read staged metadata for %s: %w", staged.Version, err)
	}
	if metadata.SchemaVersion != versionStoreSchemaVersion || metadata.Version != staged.Version {
		return StagedVersion{}, fmt.Errorf("invalid staged metadata for %s", staged.Version)
	}
	if expectedDigest != "" && metadata.BinarySHA256 != expectedDigest {
		return StagedVersion{}, fmt.Errorf(
			"immutable version conflict for %s: existing checksum %s, requested %s",
			staged.Version,
			metadata.BinarySHA256,
			expectedDigest,
		)
	}
	actualDigest, err := fileSHA256(staged.BinaryPath)
	if err != nil {
		return StagedVersion{}, fmt.Errorf("hash staged binary %s: %w", staged.Version, err)
	}
	if actualDigest != metadata.BinarySHA256 {
		return StagedVersion{}, fmt.Errorf(
			"staged binary integrity mismatch for %s: metadata %s, actual %s",
			staged.Version,
			metadata.BinarySHA256,
			actualDigest,
		)
	}
	if err := s.verifier(ctx, staged.BinaryPath, staged.Version); err != nil {
		return StagedVersion{}, fmt.Errorf("verify existing staged binary %s: %w", staged.Version, err)
	}
	staged.SHA256 = metadata.BinarySHA256
	return staged, nil
}

func (s *VersionStore) activationStatePath() string {
	return filepath.Join(s.root, activationStateName)
}

func (s *VersionStore) ReadActivationState() (ActivationState, error) {
	state, err := s.readActivationState()
	if err != nil {
		return ActivationState{}, err
	}
	return state, nil
}

func (s *VersionStore) readActivationState() (ActivationState, error) {
	data, err := os.ReadFile(s.activationStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return ActivationState{SchemaVersion: versionStoreSchemaVersion}, nil
	}
	if err != nil {
		return ActivationState{}, fmt.Errorf("read activation state: %w", err)
	}
	var state ActivationState
	if err := json.Unmarshal(data, &state); err != nil {
		return ActivationState{}, fmt.Errorf("decode activation state: %w", err)
	}
	if state.SchemaVersion != versionStoreSchemaVersion {
		return ActivationState{}, fmt.Errorf("unsupported activation schema %d", state.SchemaVersion)
	}
	if state.ActiveVersion != "" {
		active, err := normalizeVersionStoreTag(state.ActiveVersion)
		if err != nil || active != state.ActiveVersion {
			return ActivationState{}, fmt.Errorf("invalid active version %q", state.ActiveVersion)
		}
	}
	if state.PreviousVersion != "" {
		previous, err := normalizeVersionStoreTag(state.PreviousVersion)
		if err != nil || previous != state.PreviousVersion {
			return ActivationState{}, fmt.Errorf("invalid previous version %q", state.PreviousVersion)
		}
	}
	return state, nil
}

func (s *VersionStore) CompareAndSwapActivation(
	ctx context.Context,
	expectedGeneration uint64,
	activeVersion string,
	previousVersion string,
) (ActivationState, error) {
	active, err := normalizeVersionStoreTag(activeVersion)
	if err != nil {
		return ActivationState{}, fmt.Errorf("active version: %w", err)
	}
	previous := ""
	if strings.TrimSpace(previousVersion) != "" {
		previous, err = normalizeVersionStoreTag(previousVersion)
		if err != nil {
			return ActivationState{}, fmt.Errorf("previous version: %w", err)
		}
		if previous == active {
			return ActivationState{}, errors.New("active and previous versions must differ")
		}
	}
	if _, err := s.verifyExisting(ctx, active, ""); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ActivationState{}, fmt.Errorf("active version %s is not staged", active)
		}
		return ActivationState{}, err
	}
	if previous != "" {
		if _, err := s.verifyExisting(ctx, previous, ""); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ActivationState{}, fmt.Errorf("previous version %s is not staged", previous)
			}
			return ActivationState{}, err
		}
	}

	lockFile, err := os.OpenFile(
		filepath.Join(s.root, activationLockName),
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return ActivationState{}, fmt.Errorf("open activation lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockExclusiveContext(ctx, lockFile); err != nil {
		return ActivationState{}, fmt.Errorf("lock activation state: %w", err)
	}
	defer unlockExclusive(lockFile)

	current, err := s.readActivationState()
	if err != nil {
		return ActivationState{}, err
	}
	if current.Generation != expectedGeneration {
		return ActivationState{}, fmt.Errorf(
			"%w: expected %d, current %d",
			ErrActivationConflict,
			expectedGeneration,
			current.Generation,
		)
	}
	next := ActivationState{
		SchemaVersion:   versionStoreSchemaVersion,
		ActiveVersion:   active,
		PreviousVersion: previous,
		Generation:      current.Generation + 1,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := s.writeActivationState(next); err != nil {
		return ActivationState{}, err
	}
	return next, nil
}

func (s *VersionStore) writeActivationState(state ActivationState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode activation state: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.root, ".activation-*.tmp")
	if err != nil {
		return fmt.Errorf("create activation temp file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod activation temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write activation temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync activation temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close activation temp file: %w", err)
	}
	if err := replaceFileAtomic(tempPath, s.activationStatePath()); err != nil {
		return fmt.Errorf("replace activation state: %w", err)
	}
	cleanup = false
	if err := syncDirPath(s.root); err != nil {
		return fmt.Errorf("sync version store root: %w", err)
	}
	return nil
}

func VerifyStagedBinaryVersion(
	ctx context.Context,
	binaryPath string,
	expectedVersion string,
) error {
	command := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s --version: %w", binaryPath, err)
	}
	if !versionOutputMatchesRelease(string(output), expectedVersion) {
		return fmt.Errorf(
			"binary version mismatch: expected %s, got %q",
			expectedVersion,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func versionOutputMatchesRelease(output, expectedVersion string) bool {
	expected, err := normalizeVersionStoreTag(expectedVersion)
	if err != nil {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 2 || fields[0] != "multica" {
		return false
	}
	actual, err := normalizeVersionStoreTag(fields[1])
	return err == nil && actual == expected
}
