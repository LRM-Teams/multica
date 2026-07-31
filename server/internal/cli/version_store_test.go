package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
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
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("verifier mode = %o, want 755", info.Mode().Perm())
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
// (the normal state for an install that has never run `multica update`)
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
