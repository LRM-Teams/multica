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
	"math"
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
	machineMutationLockName   = "machine-upgrade.lock"
	launcherStateName         = "launcher.json"
	launcherLockName          = "launcher.lock"
)

var ErrActivationConflict = errors.New("activation generation conflict")

type BinaryVerifier func(ctx context.Context, binaryPath, expectedVersion string) error

type VersionStore struct {
	root     string
	goos     string
	verifier BinaryVerifier
}

// WithMachineMutationLock serializes a complete offline Machine Upgrade,
// including staging and activation. CompareAndSwapActivation alone protects
// Active, but is too narrow to prevent two offline invocations from racing
// through target resolution and release mutation as separate operations.
func (s *VersionStore) WithMachineMutationLock(ctx context.Context, fn func() error) error {
	if s == nil {
		return errors.New("version store is nil")
	}
	if fn == nil {
		return errors.New("machine mutation callback is required")
	}
	lockFile, err := os.OpenFile(filepath.Join(s.root, machineMutationLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open machine mutation lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockExclusiveContext(ctx, lockFile); err != nil {
		return fmt.Errorf("lock machine mutation: %w", err)
	}
	defer unlockExclusive(lockFile)
	return fn()
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

type launcherState struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
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

// ResolveStagedVersion returns the immutable staged path for a release tag
// without requiring the binary to already exist on disk.
func (s *VersionStore) ResolveStagedVersion(version string) (StagedVersion, error) {
	return s.stagedVersion(version)
}

// ActiveBinaryPath returns the staged binary path for the current Active
// version recorded in activation.json, verifying the file actually exists on
// disk. ok is false (with a nil error) when no version has ever been
// activated on this machine — the normal state for an install that has never
// run `multica update`. Callers should fall back to their own default binary
// resolution in that case.
func (s *VersionStore) ActiveBinaryPath() (path string, ok bool, err error) {
	state, err := s.ReadActivationState()
	if err != nil {
		return "", false, err
	}
	if state.ActiveVersion == "" {
		return "", false, nil
	}
	staged, err := s.stagedVersion(state.ActiveVersion)
	if err != nil {
		return "", false, err
	}
	if _, statErr := os.Stat(staged.BinaryPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, statErr
	}
	return staged.BinaryPath, true, nil
}

// RememberLauncherPath stores the stable user-facing executable used to enter
// the VersionStore. Version-specific binaries are explicitly rejected: they
// are disposable implementation files and must never become lifecycle state.
func (s *VersionStore) RememberLauncherPath(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("stable launcher path must be absolute")
	}
	if insidePath(s.VersionsRoot(), path) {
		return fmt.Errorf("stable launcher path must not be inside the version store")
	}
	if err := s.validateLauncherFile(path); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(s.root, launcherLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open launcher lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockExclusiveContext(context.Background(), lockFile); err != nil {
		return fmt.Errorf("lock launcher state: %w", err)
	}
	defer unlockExclusive(lockFile)
	if current, ok, err := s.LauncherPath(); err != nil {
		return err
	} else if ok {
		if current != path {
			return fmt.Errorf("stable launcher is already %s; refusing to replace it with %s", current, path)
		}
		return nil
	}
	data, err := json.MarshalIndent(launcherState{SchemaVersion: 1, Path: path}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.root, ".launcher-*.tmp")
	if err != nil {
		return fmt.Errorf("create launcher state: %w", err)
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
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFileAtomic(tempPath, filepath.Join(s.root, launcherStateName)); err != nil {
		return fmt.Errorf("replace launcher state: %w", err)
	}
	cleanup = false
	return syncDirPath(s.root)
}

func (s *VersionStore) validateLauncherFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat stable launcher: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("stable launcher must be a regular file")
	}
	if s.goos != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("stable launcher is not executable")
	}
	return nil
}

// LauncherPath returns the stable executable entrypoint. It fails closed when
// the state points into versions/ or the launcher has been removed.
func (s *VersionStore) LauncherPath() (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.root, launcherStateName))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var state launcherState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", false, fmt.Errorf("decode launcher state: %w", err)
	}
	state.Path = filepath.Clean(strings.TrimSpace(state.Path))
	if state.SchemaVersion != 1 || !filepath.IsAbs(state.Path) || insidePath(s.VersionsRoot(), state.Path) {
		return "", false, fmt.Errorf("invalid stable launcher state")
	}
	if err := s.validateLauncherFile(state.Path); err != nil {
		return "", false, err
	}
	return state.Path, true, nil
}

func insidePath(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// OpenVersionStore opens (or creates) the default user VersionStore root used by
// daemon update staging. Verifier defaults to VerifyStagedBinaryVersion.
func OpenVersionStore(root string) (*VersionStore, error) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = DefaultVersionStoreRoot()
		if err != nil {
			return nil, err
		}
	}
	return NewVersionStore(root, runtime.GOOS, nil)
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

// renamePublishRetryAttempts/renamePublishRetryBaseDelay bound how long
// renamePublishWithRetry will keep retrying a failed publish rename.
//
// This absorbs a Windows-specific race, not a general reliability concern:
// StageBinary executes the freshly-staged binary (s.verifier, just above the
// caller of this function) to confirm its version, then immediately renames
// its parent directory into place. On Unix this is always safe — a process
// holding a file open never blocks rename/unlink. On Windows it isn't: the OS
// can hold the image file locked for a short window after the process exits
// while it tears down, and antivirus (Defender's on-execute + on-close
// scanning is the common case) routinely opens its own handle to a
// just-executed .exe for a similarly short window. Both are sub-second to
// low-single-digit-second phenomena in practice, not multi-second stalls —
// this is why the ceiling here is ~3s, not the minutes-scale backoff
// UpdateIntentStore uses one layer up (handler/runtime_update_intent.go).
//
// The two retry layers serve different failures and must not be confused:
// this one absorbs a transient OS/AV lock during a single already-running
// attempt; UpdateIntentStore's backoff redelivers the *trigger* to a target
// that wasn't reachable/available at all. If this retry ceiling is exceeded,
// StageBinary fails normally and the outer layer takes over on its own
// schedule — it does not wait for this one to give up before deciding
// whether to try again.
const (
	renamePublishRetryAttempts  = 5
	renamePublishRetryBaseDelay = 100 * time.Millisecond
)

// osRename is os.Rename behind an indirection so tests can simulate a
// transient lock (fail N times, then succeed) without needing a real
// Windows file lock — mirrors the fetchLatestRelease-style override pattern
// used elsewhere in this codebase (auto_update.go) for the same reason.
var osRename = os.Rename

// renamePublishWithRetry retries osRename with exponential backoff
// (100ms, 200ms, 400ms, 800ms, 1600ms — ~3.1s total across up to
// renamePublishRetryAttempts retries after the initial attempt) to absorb a
// transient Windows file lock. See the constants above for why.
func renamePublishWithRetry(ctx context.Context, oldpath, newpath string) error {
	err := osRename(oldpath, newpath)
	if err == nil {
		return nil
	}
	delay := renamePublishRetryBaseDelay
	for attempt := 0; attempt < renamePublishRetryAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		if err = osRename(oldpath, newpath); err == nil {
			return nil
		}
		delay *= 2
	}
	return err
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
	if err := renamePublishWithRetry(ctx, tempDir, finalDir); err != nil {
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
	if err := validateActivationState(state); err != nil {
		return ActivationState{}, err
	}
	return state, nil
}

func validateActivationState(state ActivationState) error {
	if state.SchemaVersion != versionStoreSchemaVersion {
		return fmt.Errorf(
			"invalid activation state: unsupported schema %d",
			state.SchemaVersion,
		)
	}
	if state.ActiveVersion != "" {
		active, err := normalizeVersionStoreTag(state.ActiveVersion)
		if err != nil || active != state.ActiveVersion {
			return fmt.Errorf(
				"invalid activation state: invalid active version %q",
				state.ActiveVersion,
			)
		}
	}
	if state.PreviousVersion != "" {
		previous, err := normalizeVersionStoreTag(state.PreviousVersion)
		if err != nil || previous != state.PreviousVersion {
			return fmt.Errorf(
				"invalid activation state: invalid previous version %q",
				state.PreviousVersion,
			)
		}
	}
	switch {
	case state.Generation == 0 &&
		(state.ActiveVersion != "" || state.PreviousVersion != ""):
		return errors.New("invalid activation state: generation zero must be empty")
	case state.Generation == 1 &&
		(state.ActiveVersion == "" || state.PreviousVersion != ""):
		return errors.New(
			"invalid activation state: first generation requires active without predecessor",
		)
	case state.Generation >= 2 &&
		(state.ActiveVersion == "" || state.PreviousVersion == ""):
		return errors.New(
			"invalid activation state: later generation requires active and predecessor",
		)
	case state.ActiveVersion != "" &&
		state.ActiveVersion == state.PreviousVersion:
		return errors.New("invalid activation state: active and predecessor must differ")
	}
	return nil
}

func (s *VersionStore) CompareAndSwapActivation(
	ctx context.Context,
	expectedGeneration uint64,
	activeVersion string,
) (ActivationState, error) {
	active, err := normalizeVersionStoreTag(activeVersion)
	if err != nil {
		return ActivationState{}, fmt.Errorf("active version: %w", err)
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
	if current.Generation == math.MaxUint64 {
		return ActivationState{}, errors.New("activation generation overflow")
	}
	if current.ActiveVersion == active {
		return ActivationState{}, fmt.Errorf("active version %s is already active", active)
	}
	if _, err := s.verifyExisting(ctx, active, ""); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ActivationState{}, fmt.Errorf("active version %s is not staged", active)
		}
		return ActivationState{}, err
	}
	previous := current.ActiveVersion
	if previous != "" {
		if _, err := s.verifyExisting(ctx, previous, ""); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ActivationState{}, fmt.Errorf("previous version %s is not staged", previous)
			}
			return ActivationState{}, err
		}
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
	if err := validateActivationState(state); err != nil {
		return err
	}
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

// PruneResult records what PruneInactiveVersions removed.
type PruneResult struct {
	// RemovedVersions is the list of version tags whose directories were
	// deleted (binary + metadata + directory itself).
	RemovedVersions []string
}

// PruneInactiveVersions removes version directories under versions/ that are
// neither the currently active version nor the recorded previous version.
// It is safe to call after a successful activation: leftover staged-but-
// never-activated directories and older-than-previous releases are reclaimed.
//
// PreviousVersion is retained on purpose (s144 2026-08-04): OS service units
// (systemd ExecStart, LaunchAgent ProgramArguments, etc.) may still point at
// the just-superseded version path for one restart cycle after Active flips.
// Deleting that path immediately produces status=203/EXEC crash-loops when
// the unit was not rewritten before prune (e.g. upgrading across a binary
// that lacks handoff unit sync). Keep previous until the next activation
// advances it away.
//
// The active version — the one activation.json points at — is never touched.
// If activation.json does not exist or has no active version, nothing is
// pruned (we don't know what's live, so we don't delete anything).
func (s *VersionStore) PruneInactiveVersions(ctx context.Context) (PruneResult, error) {
	state, err := s.ReadActivationState()
	if err != nil {
		return PruneResult{}, fmt.Errorf("read activation state: %w", err)
	}
	if state.ActiveVersion == "" {
		// No active version → don't touch anything.
		return PruneResult{}, nil
	}

	keep := map[string]struct{}{state.ActiveVersion: {}}
	if state.PreviousVersion != "" && state.PreviousVersion != state.ActiveVersion {
		keep[state.PreviousVersion] = struct{}{}
	}

	entries, err := os.ReadDir(s.VersionsRoot())
	if err != nil {
		return PruneResult{}, fmt.Errorf("list versions root: %w", err)
	}

	// Resolve keep dirs once for symlink-safe comparison.
	keepReal := make(map[string]struct{}, len(keep))
	for tag := range keep {
		dir := filepath.Join(s.VersionsRoot(), tag)
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			// Unresolvable keep tag — still skip by name below.
			continue
		}
		keepReal[real] = struct{}{}
	}

	var removed []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip Active and Previous by tag name.
		if _, ok := keep[name]; ok {
			continue
		}
		// Skip hidden temp directories (stage leftovers like .stage-vX.Y.Z-*).
		if strings.HasPrefix(name, ".") {
			continue
		}

		dirPath := filepath.Join(s.VersionsRoot(), name)

		// Belt-and-suspenders: resolve the real path and compare it to keep
		// dirs. Catches symlinks / case-insensitive filesystems where name
		// is not in keep but still resolves to Active or Previous.
		realPath, err := filepath.EvalSymlinks(dirPath)
		if err != nil {
			// Can't resolve — skip rather than risk deleting the wrong thing.
			continue
		}
		if _, ok := keepReal[realPath]; ok {
			continue
		}

		if err := os.RemoveAll(dirPath); err != nil {
			return PruneResult{RemovedVersions: removed}, fmt.Errorf("remove old version %s: %w", name, err)
		}
		removed = append(removed, name)
	}

	if len(removed) > 0 {
		if err := syncDirPath(s.VersionsRoot()); err != nil {
			return PruneResult{RemovedVersions: removed}, fmt.Errorf("sync versions root after prune: %w", err)
		}
	}

	return PruneResult{RemovedVersions: removed}, nil
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
