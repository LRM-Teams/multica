package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testBinaryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func testVersionStore(t *testing.T, verifier BinaryVerifier) *VersionStore {
	t.Helper()
	store, err := NewVersionStore(t.TempDir(), "linux", verifier)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}
	return store
}

func TestVersionStoreRejectsInvalidReleaseTag(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	for _, version := range []string{"", "../v0.3.73", "v0.3.73/worker", "latest", "v0.3.73-rc1"} {
		t.Run(strings.ReplaceAll(version, "/", "_"), func(t *testing.T) {
			_, err := store.StageBinary(
				context.Background(),
				version,
				[]byte("binary"),
				testBinaryDigest([]byte("binary")),
				0o755,
			)
			if err == nil {
				t.Fatalf("StageBinary(%q) unexpectedly succeeded", version)
			}
		})
	}
}

func TestVersionStoreStageVerifiesBeforeImmutablePublish(t *testing.T) {
	wantErr := errors.New("wrong binary version")
	store := testVersionStore(t, func(context.Context, string, string) error {
		return wantErr
	})
	data := []byte("not really multica")

	_, err := store.StageBinary(
		context.Background(),
		"v0.3.73",
		data,
		testBinaryDigest(data),
		0o755,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("StageBinary error = %v, want %v", err, wantErr)
	}

	finalDir := filepath.Join(store.VersionsRoot(), "v0.3.73")
	if _, statErr := os.Stat(finalDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed stage published final dir: stat error = %v", statErr)
	}
	entries, readErr := os.ReadDir(store.VersionsRoot())
	if readErr != nil {
		t.Fatalf("ReadDir versions root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed stage left partial entries: %v", entries)
	}
}

func TestVersionStoreStageRejectsChecksumMismatchBeforeVerify(t *testing.T) {
	var verifierCalls atomic.Int32
	store := testVersionStore(t, func(context.Context, string, string) error {
		verifierCalls.Add(1)
		return nil
	})

	_, err := store.StageBinary(
		context.Background(),
		"0.3.73",
		[]byte("binary"),
		strings.Repeat("0", sha256.Size*2),
		0o755,
	)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("StageBinary error = %v, want checksum mismatch", err)
	}
	if got := verifierCalls.Load(); got != 0 {
		t.Fatalf("verifier calls = %d, want 0", got)
	}
}

func TestVersionStoreStageIsImmutableAndIdempotent(t *testing.T) {
	store := testVersionStore(t, func(_ context.Context, path, version string) error {
		if version != "v0.3.73" {
			t.Fatalf("verifier version = %q", version)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("verifier stat: %v", err)
		}
		// NTFS has no exec bit: os.Chmod on Windows can only toggle the
		// read-only attribute, so a 0o755 chmod reads back as 0o666 (any
		// write bit requested -> fully writable) rather than round-tripping
		// the exact Unix bits (task #79). This is a real, documented Go
		// platform difference — StageBinary's caller never reads the mode
		// back in production (Windows resolves whether a file is
		// executable from its extension/PE header, not chmod bits), so the
		// meaningful assertion here is "the chmod call took effect and the
		// file didn't end up read-only", expressed per-platform rather than
		// skipped outright.
		wantMode := os.FileMode(0o755)
		if runtime.GOOS == "windows" {
			wantMode = 0o666
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("verifier mode = %o, want %o", info.Mode().Perm(), wantMode)
		}
		return nil
	})
	original := []byte("multica-v0.3.73")

	first, err := store.StageBinary(
		context.Background(),
		"0.3.73",
		original,
		testBinaryDigest(original),
		0o755,
	)
	if err != nil {
		t.Fatalf("first StageBinary: %v", err)
	}
	second, err := store.StageBinary(
		context.Background(),
		"v0.3.73",
		original,
		testBinaryDigest(original),
		0o755,
	)
	if err != nil {
		t.Fatalf("second StageBinary: %v", err)
	}
	if first != second {
		t.Fatalf("idempotent stage paths differ: %q != %q", first, second)
	}

	got, err := os.ReadFile(first.BinaryPath)
	if err != nil {
		t.Fatalf("ReadFile staged binary: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("staged bytes = %q, want %q", got, original)
	}

	different := []byte("different-binary-same-tag")
	_, err = store.StageBinary(
		context.Background(),
		"v0.3.73",
		different,
		testBinaryDigest(different),
		0o755,
	)
	if err == nil || !strings.Contains(err.Error(), "immutable version conflict") {
		t.Fatalf("different same-tag stage error = %v, want immutable version conflict", err)
	}
	got, err = os.ReadFile(first.BinaryPath)
	if err != nil {
		t.Fatalf("ReadFile staged binary after conflict: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("immutable binary changed to %q", got)
	}
}

func TestVersionStoreConcurrentSameVersionStagePublishesOnce(t *testing.T) {
	var verifierCalls atomic.Int32
	store := testVersionStore(t, func(context.Context, string, string) error {
		verifierCalls.Add(1)
		return nil
	})
	data := []byte("multica-v0.3.73")
	digest := testBinaryDigest(data)

	const workers = 16
	results := make(chan StagedVersion, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			staged, err := store.StageBinary(
				context.Background(),
				"v0.3.73",
				data,
				digest,
				0o755,
			)
			if err != nil {
				errs <- err
				return
			}
			results <- staged
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent StageBinary: %v", err)
	}
	var path string
	for result := range results {
		if path == "" {
			path = result.BinaryPath
		} else if result.BinaryPath != path {
			t.Errorf("staged path = %q, want %q", result.BinaryPath, path)
		}
	}
	if got := verifierCalls.Load(); got < 1 {
		t.Fatalf("verifier calls = %d, want at least 1", got)
	}

	entries, err := os.ReadDir(store.VersionsRoot())
	if err != nil {
		t.Fatalf("ReadDir versions root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "v0.3.73" {
		t.Fatalf("versions entries = %v, want only v0.3.73", entries)
	}
}

func TestVersionStoreActivationStateUsesGenerationCAS(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.72", "v0.3.73"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}

	first, err := store.CompareAndSwapActivation(
		context.Background(),
		0,
		"v0.3.72",
	)
	if err != nil {
		t.Fatalf("first CompareAndSwapActivation: %v", err)
	}
	if first.Generation != 1 || first.ActiveVersion != "v0.3.72" || first.PreviousVersion != "" {
		t.Fatalf("first activation = %+v", first)
	}

	_, err = store.CompareAndSwapActivation(
		context.Background(),
		1,
		"v0.3.72",
	)
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("same-version CAS error = %v, want already active", err)
	}
	unchanged, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState after same-version CAS: %v", err)
	}
	if unchanged != first {
		t.Fatalf("same-version CAS changed state: got %+v, want %+v", unchanged, first)
	}

	_, err = store.CompareAndSwapActivation(
		context.Background(),
		0,
		"v0.3.73",
	)
	if !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("stale CAS error = %v, want ErrActivationConflict", err)
	}

	second, err := store.CompareAndSwapActivation(
		context.Background(),
		1,
		"0.3.73",
	)
	if err != nil {
		t.Fatalf("second CompareAndSwapActivation: %v", err)
	}
	if second.Generation != 2 || second.ActiveVersion != "v0.3.73" || second.PreviousVersion != "v0.3.72" {
		t.Fatalf("second activation = %+v", second)
	}

	got, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if got != second {
		t.Fatalf("ReadActivationState = %+v, want %+v", got, second)
	}

	rollback, err := store.CompareAndSwapActivation(
		context.Background(),
		2,
		"v0.3.72",
	)
	if err != nil {
		t.Fatalf("rollback CompareAndSwapActivation: %v", err)
	}
	if rollback.Generation != 3 ||
		rollback.ActiveVersion != "v0.3.72" ||
		rollback.PreviousVersion != "v0.3.73" {
		t.Fatalf("rollback activation = %+v", rollback)
	}
}

func TestVersionStoreActivationRejectsUnstagedVersion(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	_, err := store.CompareAndSwapActivation(
		context.Background(),
		0,
		"v0.3.73",
	)
	if err == nil || !strings.Contains(err.Error(), "is not staged") {
		t.Fatalf("CompareAndSwapActivation error = %v, want not staged", err)
	}
	state, readErr := store.ReadActivationState()
	if readErr != nil {
		t.Fatalf("ReadActivationState after rejected CAS: %v", readErr)
	}
	if state.Generation != 0 || state.ActiveVersion != "" {
		t.Fatalf("rejected CAS changed state: %+v", state)
	}
}

func TestVersionStoreActivationFailsClosedOnCorruptManifest(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	data := []byte("multica-v0.3.73")
	if _, err := store.StageBinary(
		context.Background(),
		"v0.3.73",
		data,
		testBinaryDigest(data),
		0o755,
	); err != nil {
		t.Fatalf("StageBinary: %v", err)
	}

	corrupt := []byte(`{"schema_version":1,"active_version":"v0.3.72"`)
	if err := os.WriteFile(store.activationStatePath(), corrupt, 0o600); err != nil {
		t.Fatalf("write corrupt activation manifest: %v", err)
	}

	_, err := store.CompareAndSwapActivation(
		context.Background(),
		0,
		"v0.3.73",
	)
	if err == nil || !strings.Contains(err.Error(), "decode activation state") {
		t.Fatalf("CompareAndSwapActivation error = %v, want decode activation state", err)
	}
	got, readErr := os.ReadFile(store.activationStatePath())
	if readErr != nil {
		t.Fatalf("read activation manifest after rejected CAS: %v", readErr)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("corrupt activation manifest was overwritten: %q", got)
	}
}

func TestVersionStoreActivationRejectsSemanticCorruption(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	tests := []struct {
		name  string
		state ActivationState
	}{
		{
			name: "generation zero with active",
			state: ActivationState{
				SchemaVersion: versionStoreSchemaVersion,
				ActiveVersion: "v0.3.72",
			},
		},
		{
			name: "generation one without active",
			state: ActivationState{
				SchemaVersion: versionStoreSchemaVersion,
				Generation:    1,
			},
		},
		{
			name: "generation one with predecessor",
			state: ActivationState{
				SchemaVersion:   versionStoreSchemaVersion,
				ActiveVersion:   "v0.3.73",
				PreviousVersion: "v0.3.72",
				Generation:      1,
			},
		},
		{
			name: "later generation without predecessor",
			state: ActivationState{
				SchemaVersion: versionStoreSchemaVersion,
				ActiveVersion: "v0.3.73",
				Generation:    2,
			},
		},
		{
			name: "active equals predecessor",
			state: ActivationState{
				SchemaVersion:   versionStoreSchemaVersion,
				ActiveVersion:   "v0.3.73",
				PreviousVersion: "v0.3.73",
				Generation:      2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.state)
			if err != nil {
				t.Fatalf("Marshal activation state: %v", err)
			}
			if err := os.WriteFile(store.activationStatePath(), data, 0o600); err != nil {
				t.Fatalf("write activation state: %v", err)
			}
			if _, err := store.ReadActivationState(); err == nil ||
				!strings.Contains(err.Error(), "invalid activation state") {
				t.Fatalf("ReadActivationState error = %v, want invalid activation state", err)
			}
		})
	}
}

func TestVersionStoreActivationRejectsGenerationOverflow(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.72", "v0.3.73"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}
	current := ActivationState{
		SchemaVersion:   versionStoreSchemaVersion,
		ActiveVersion:   "v0.3.72",
		PreviousVersion: "v0.3.73",
		Generation:      math.MaxUint64,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.writeActivationState(current); err != nil {
		t.Fatalf("seed maximum generation: %v", err)
	}

	_, err := store.CompareAndSwapActivation(
		context.Background(),
		math.MaxUint64,
		"v0.3.73",
	)
	if err == nil || !strings.Contains(err.Error(), "generation overflow") {
		t.Fatalf("CompareAndSwapActivation error = %v, want generation overflow", err)
	}
	got, readErr := store.ReadActivationState()
	if readErr != nil {
		t.Fatalf("ReadActivationState after overflow: %v", readErr)
	}
	if got != current {
		t.Fatalf("overflow changed activation state: got %+v, want %+v", got, current)
	}
}

func TestVersionStoreConcurrentActivationCASHasOneWinner(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.72", "v0.3.73"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}

	type result struct {
		state ActivationState
		err   error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, version := range []string{"v0.3.72", "v0.3.73"} {
		version := version
		go func() {
			<-start
			state, err := store.CompareAndSwapActivation(
				context.Background(),
				0,
				version,
			)
			results <- result{state: state, err: err}
		}()
	}
	close(start)

	var successes, conflicts int
	for i := 0; i < 2; i++ {
		got := <-results
		switch {
		case got.err == nil:
			successes++
			if got.state.Generation != 1 {
				t.Errorf("winner generation = %d, want 1", got.state.Generation)
			}
		case errors.Is(got.err, ErrActivationConflict):
			conflicts++
		default:
			t.Errorf("unexpected CAS error: %v", got.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS results: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}

	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("final generation = %d, want 1", state.Generation)
	}
}

func TestVersionOutputMatchesRelease(t *testing.T) {
	for _, output := range []string{
		"multica 0.3.73",
		"multica 0.3.73 (commit: abcdef0, built: now)",
		"multica v0.3.73\n",
	} {
		if !versionOutputMatchesRelease(output, "v0.3.73") {
			t.Fatalf("versionOutputMatchesRelease(%q) = false", output)
		}
	}
	for _, output := range []string{
		"",
		"multica 0.3.72",
		"other 0.3.73",
		"multica v0.3.73-1-gabcdef",
	} {
		if versionOutputMatchesRelease(output, "v0.3.73") {
			t.Fatalf("versionOutputMatchesRelease(%q) = true", output)
		}
	}
}

// Negative case: a fresh VersionStore that has never activated anything
// (the normal state for an install that has never run `multica computer upgrade`)
// must report ok=false without error, so callers fall back to their own
// default binary resolution.
func TestVersionStoreActiveBinaryPathNoActivationYet(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	path, ok, err := store.ActiveBinaryPath()
	if err != nil {
		t.Fatalf("ActiveBinaryPath: %v", err)
	}
	if ok {
		t.Fatalf("ActiveBinaryPath ok = true with no activation, path = %q", path)
	}
	if path != "" {
		t.Fatalf("ActiveBinaryPath path = %q, want empty", path)
	}
}

// Positive case: after staging + activating a version, ActiveBinaryPath must
// resolve to that staged binary's on-disk path (task #41 — this is the
// signal `daemon restart` was previously ignoring, always re-exec'ing
// whatever binary invoked the command instead).
func TestVersionStoreActiveBinaryPathReturnsStagedActive(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	data := []byte("multica-v0.3.88")
	staged, err := store.StageBinary(
		context.Background(),
		"v0.3.88",
		data,
		testBinaryDigest(data),
		0o755,
	)
	if err != nil {
		t.Fatalf("StageBinary: %v", err)
	}
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.88"); err != nil {
		t.Fatalf("CompareAndSwapActivation: %v", err)
	}

	path, ok, err := store.ActiveBinaryPath()
	if err != nil {
		t.Fatalf("ActiveBinaryPath: %v", err)
	}
	if !ok {
		t.Fatalf("ActiveBinaryPath ok = false, want true")
	}
	if path != staged.BinaryPath {
		t.Fatalf("ActiveBinaryPath = %q, want %q", path, staged.BinaryPath)
	}
}

// If activation.json points at a version whose staged binary has since been
// removed from disk, ActiveBinaryPath must report ok=false (not an error) so
// callers fall back rather than exec a path that doesn't exist.
func TestVersionStoreActiveBinaryPathMissingStagedFileFallsBackCleanly(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	data := []byte("multica-v0.3.88")
	staged, err := store.StageBinary(
		context.Background(),
		"v0.3.88",
		data,
		testBinaryDigest(data),
		0o755,
	)
	if err != nil {
		t.Fatalf("StageBinary: %v", err)
	}
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.88"); err != nil {
		t.Fatalf("CompareAndSwapActivation: %v", err)
	}
	if err := os.Remove(staged.BinaryPath); err != nil {
		t.Fatalf("remove staged binary: %v", err)
	}

	path, ok, err := store.ActiveBinaryPath()
	if err != nil {
		t.Fatalf("ActiveBinaryPath: %v", err)
	}
	if ok {
		t.Fatalf("ActiveBinaryPath ok = true for a removed staged binary, path = %q", path)
	}
}

// helper: stage + activate a version in one call.
func stageAndActivate(t *testing.T, store *VersionStore, version string) {
	t.Helper()
	data := []byte("multica-" + version)
	if _, err := store.StageBinary(
		context.Background(),
		version,
		data,
		testBinaryDigest(data),
		0o755,
	); err != nil {
		t.Fatalf("stage %s: %v", version, err)
	}
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("read activation state: %v", err)
	}
	if _, err := store.CompareAndSwapActivation(
		context.Background(),
		state.Generation,
		version,
	); err != nil {
		t.Fatalf("activate %s: %v", version, err)
	}
}

func listVersionDirs(t *testing.T, store *VersionStore) []string {
	t.Helper()
	entries, err := os.ReadDir(store.VersionsRoot())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestPruneInactiveVersions_RemovesOldLeavesActiveAndPrevious tests that after
// successive upgrades, prune reclaims older-than-previous dirs but keeps both
// Active and Previous (so a lagging OS service unit can still ExecStart the
// just-superseded path for one cycle).
func TestPruneInactiveVersions_RemovesOldLeavesActiveAndPrevious(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	// Simulate two consecutive upgrades.
	stageAndActivate(t, store, "v0.3.90")
	stageAndActivate(t, store, "v0.3.91")
	stageAndActivate(t, store, "v0.3.92")

	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("read activation state: %v", err)
	}
	if state.ActiveVersion != "v0.3.92" {
		t.Fatalf("expected active v0.3.92, got %s", state.ActiveVersion)
	}
	if state.PreviousVersion != "v0.3.91" {
		t.Fatalf("expected previous v0.3.91, got %s", state.PreviousVersion)
	}

	// All three should be present before prune.
	dirs := listVersionDirs(t, store)
	if len(dirs) != 3 {
		t.Fatalf("expected 3 version dirs before prune, got %d: %v", len(dirs), dirs)
	}

	result, err := store.PruneInactiveVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneInactiveVersions: %v", err)
	}
	if len(result.RemovedVersions) != 1 || result.RemovedVersions[0] != "v0.3.90" {
		t.Fatalf("expected only v0.3.90 removed, got %v", result.RemovedVersions)
	}

	// After prune, Active + Previous remain; older-than-previous is gone.
	dirs = listVersionDirs(t, store)
	want := map[string]bool{"v0.3.91": true, "v0.3.92": true}
	if len(dirs) != 2 {
		t.Fatalf("expected active+previous after prune, got %v", dirs)
	}
	for _, d := range dirs {
		if !want[d] {
			t.Fatalf("unexpected dir after prune: %s (all=%v)", d, dirs)
		}
	}

	// The active binary must still exist and be readable.
	activePath, ok, err := store.ActiveBinaryPath()
	if err != nil || !ok {
		t.Fatalf("ActiveBinaryPath after prune: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active binary missing after prune: %v", err)
	}
	// Previous binary must still exist (OS unit lag safety).
	prevStaged, err := store.ResolveStagedVersion(state.PreviousVersion)
	if err != nil {
		t.Fatalf("ResolveStagedVersion(previous): %v", err)
	}
	if _, err := os.Stat(prevStaged.BinaryPath); err != nil {
		t.Fatalf("previous binary missing after prune: %v", err)
	}
}

// TestPruneInactiveVersions_NeverDeletesPrevious is the guard for the
// Previous keep: even when surrounded by junk versions, Previous survives.
func TestPruneInactiveVersions_NeverDeletesPrevious(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	stageAndActivate(t, store, "v0.3.90")
	stageAndActivate(t, store, "v0.3.91")

	state, _ := store.ReadActivationState()
	if state.PreviousVersion != "v0.3.90" {
		t.Fatalf("expected previous v0.3.90, got %s", state.PreviousVersion)
	}

	_, err := store.PruneInactiveVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneInactiveVersions: %v", err)
	}

	prevDir := filepath.Join(store.VersionsRoot(), state.PreviousVersion)
	if _, err := os.Stat(prevDir); err != nil {
		t.Fatalf("previous version directory was deleted: %v", err)
	}
}

// TestPruneInactiveVersions_NeverDeletesActive is the guard test (negative):
// it verifies that the active version is never removed, even when surrounded
// by old versions. This test MUST fail if the protection check is removed.
//
// Mutation check: remove the `name == state.ActiveVersion` continue in
// PruneInactiveVersions → this test fails because the active binary is
// deleted.
func TestPruneInactiveVersions_NeverDeletesActive(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	stageAndActivate(t, store, "v0.3.90")
	stageAndActivate(t, store, "v0.3.91")

	state, _ := store.ReadActivationState()
	activeVersion := state.ActiveVersion // v0.3.91

	_, err := store.PruneInactiveVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneInactiveVersions: %v", err)
	}

	// The active version directory must still exist.
	activeDir := filepath.Join(store.VersionsRoot(), activeVersion)
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("active version directory was deleted: %v", err)
	}
	// The active binary must still exist.
	activePath, ok, err := store.ActiveBinaryPath()
	if err != nil || !ok {
		t.Fatalf("ActiveBinaryPath after prune: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active binary was deleted: %v", err)
	}
	// Activation state must be unchanged.
	stateAfter, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("read activation state after prune: %v", err)
	}
	if stateAfter.ActiveVersion != activeVersion {
		t.Fatalf("active version changed from %s to %s", activeVersion, stateAfter.ActiveVersion)
	}
}

// TestPruneInactiveVersions_NoActiveVersionIsNoop verifies that prune does
// nothing when there is no active version — we don't know what's live, so we
// don't delete anything.
func TestPruneInactiveVersions_NoActiveVersionIsNoop(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	// Stage two versions but don't activate either.
	data := []byte("multica-v0.3.90")
	if _, err := store.StageBinary(
		context.Background(),
		"v0.3.90",
		data,
		testBinaryDigest(data),
		0o755,
	); err != nil {
		t.Fatalf("stage v0.3.90: %v", err)
	}
	data2 := []byte("multica-v0.3.91")
	if _, err := store.StageBinary(
		context.Background(),
		"v0.3.91",
		data2,
		testBinaryDigest(data2),
		0o755,
	); err != nil {
		t.Fatalf("stage v0.3.91: %v", err)
	}

	result, err := store.PruneInactiveVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneInactiveVersions: %v", err)
	}
	if len(result.RemovedVersions) != 0 {
		t.Fatalf("expected 0 removed when no active version, got %v", result.RemovedVersions)
	}

	// All staged versions must still be present.
	dirs := listVersionDirs(t, store)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 version dirs preserved, got %d: %v", len(dirs), dirs)
	}
}

// TestPruneInactiveVersions_SkipsStageTempDirs verifies that hidden temp
// directories (left behind by interrupted staging) are not touched by prune —
// they are owned by the staging code's own cleanup.
func TestPruneInactiveVersions_SkipsStageTempDirs(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })

	stageAndActivate(t, store, "v0.3.90")
	stageAndActivate(t, store, "v0.3.91")

	// Simulate a leftover staging temp directory.
	tempDir := filepath.Join(store.VersionsRoot(), ".stage-v0.3.92-abcd1234")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "partial"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := store.PruneInactiveVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneInactiveVersions: %v", err)
	}

	// The temp directory must still exist — prune doesn't own it.
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("temp directory was deleted by prune: %v", err)
	}
}

// TestRenamePublishWithRetry_SucceedsImmediatelyWithoutRetrying confirms the
// common (Unix, or a Windows machine that isn't racing a lock) case pays no
// retry cost at all.
func TestRenamePublishWithRetry_SucceedsImmediatelyWithoutRetrying(t *testing.T) {
	calls := 0
	restore := stubOSRename(t, func(oldpath, newpath string) error {
		calls++
		return nil
	})
	defer restore()

	if err := renamePublishWithRetry(context.Background(), "old", "new"); err != nil {
		t.Fatalf("renamePublishWithRetry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry needed)", calls)
	}
}

// TestRenamePublishWithRetry_RetriesTransientFailureThenSucceeds is the
// concrete shape of the bug this exists to fix: os.Rename fails a few times
// (Windows image lock / AV on-close scan still releasing) then succeeds once
// the lock clears — this must be absorbed, not surfaced as a hard failure.
func TestRenamePublishWithRetry_RetriesTransientFailureThenSucceeds(t *testing.T) {
	calls := 0
	const failuresBeforeSuccess = 3 // well within renamePublishRetryAttempts
	restore := stubOSRename(t, func(oldpath, newpath string) error {
		calls++
		if calls <= failuresBeforeSuccess {
			return fmt.Errorf("rename %s -> %s: Access is denied", oldpath, newpath)
		}
		return nil
	})
	defer restore()

	start := time.Now()
	if err := renamePublishWithRetry(context.Background(), "old", "new"); err != nil {
		t.Fatalf("renamePublishWithRetry: %v", err)
	}
	elapsed := time.Since(start)
	if calls != failuresBeforeSuccess+1 {
		t.Fatalf("calls = %d, want %d", calls, failuresBeforeSuccess+1)
	}
	// 100+200+400 = 700ms of backoff before the 4th (successful) call.
	if elapsed < 700*time.Millisecond {
		t.Fatalf("elapsed = %v, expected at least 700ms of backoff before success", elapsed)
	}
}

// TestRenamePublishWithRetry_GivesUpAfterMaxAttemptsAndReturnsLastError is
// the other half of the contract Parker asked for explicitly: this layer
// must have a ceiling and hand back a real error once exhausted — it does
// NOT retry forever, that's the outer (UpdateIntentStore) layer's job.
func TestRenamePublishWithRetry_GivesUpAfterMaxAttemptsAndReturnsLastError(t *testing.T) {
	calls := 0
	sentinelErr := errors.New("rename: Access is denied (attempt N)")
	restore := stubOSRename(t, func(oldpath, newpath string) error {
		calls++
		return sentinelErr
	})
	defer restore()

	start := time.Now()
	err := renamePublishWithRetry(context.Background(), "old", "new")
	elapsed := time.Since(start)
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("err = %v, want %v", err, sentinelErr)
	}
	if calls != renamePublishRetryAttempts+1 {
		t.Fatalf("calls = %d, want %d (1 initial + %d retries)", calls, renamePublishRetryAttempts+1, renamePublishRetryAttempts)
	}
	// 100+200+400+800+1600 = 3100ms total — this is the ceiling Parker asked
	// for explicitly ("内层最多耗多久"): long enough to cover a real AV scan,
	// short enough not to duplicate the outer minutes-scale backoff.
	if elapsed < 3*time.Second {
		t.Fatalf("elapsed = %v, expected roughly 3.1s of total backoff before giving up", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("elapsed = %v, retry ceiling should not run away past ~3.1s", elapsed)
	}
}

// TestRenamePublishWithRetry_StopsOnContextCancellation ensures a cancelled
// context (e.g. the daemon shutting down mid-update) doesn't strand the
// retry loop waiting out its full ~3.1s backoff budget for nothing.
func TestRenamePublishWithRetry_StopsOnContextCancellation(t *testing.T) {
	calls := 0
	restore := stubOSRename(t, func(oldpath, newpath string) error {
		calls++
		return errors.New("rename: Access is denied")
	})
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := renamePublishWithRetry(ctx, "old", "new")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected an error after cancellation, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, should have stopped shortly after cancellation, not run the full backoff", elapsed)
	}
	if calls >= renamePublishRetryAttempts+1 {
		t.Fatalf("calls = %d, should have stopped before exhausting all attempts", calls)
	}
}

// TestVersionStoreStageBinary_SurvivesTransientRenameFailure is the actual
// end-to-end shape of the 2026-08-01/02 incident: the publish rename fails a
// couple of times (Windows image lock / AV on-close scan) before the lock
// clears. StageBinary as a whole must still succeed — this proves the retry
// is actually wired into the real call path, not just correct in isolation.
func TestVersionStoreStageBinary_SurvivesTransientRenameFailure(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	data := []byte("multica binary contents")

	renameCalls := 0
	restore := stubOSRename(t, func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls <= 2 {
			return fmt.Errorf("rename %s -> %s: Access is denied", oldpath, newpath)
		}
		return os.Rename(oldpath, newpath)
	})
	defer restore()

	staged, err := store.StageBinary(context.Background(), "v0.3.95", data, testBinaryDigest(data), 0o755)
	if err != nil {
		t.Fatalf("StageBinary should survive a transient rename failure: %v", err)
	}
	if staged.Version != "v0.3.95" {
		t.Fatalf("staged.Version = %q, want v0.3.95", staged.Version)
	}
	if renameCalls < 3 {
		t.Fatalf("renameCalls = %d, expected at least 3 (2 failures + 1 success)", renameCalls)
	}
	if _, err := os.Stat(staged.BinaryPath); err != nil {
		t.Fatalf("staged binary should exist on disk after a successful retry: %v", err)
	}
}

func stubOSRename(t *testing.T, fn func(oldpath, newpath string) error) func() {
	t.Helper()
	original := osRename
	osRename = fn
	return func() { osRename = original }
}
