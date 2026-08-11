package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestReleaseDetectionLoopSkipsWhenIntervalDisabled(t *testing.T) {
	// An unconfigured daemon (interval <= 0) must not start a detection
	// loop that blocks on a timer/ticker; it returns immediately.
	done := make(chan struct{})
	go func() {
		(&Daemon{cfg: Config{ReleaseDetectionInterval: 0}}).releaseDetectionLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("releaseDetectionLoop scheduled a delay/ticker when interval is disabled")
	}
}

// TestReleaseDetectionRecordsNewerTarget covers the core detection semantic:
// with a real updateObservation and a fake release feed advertising a newer
// release, checkForNewerRelease must record that release as TargetVersion on the
// observation (which the heartbeat then reports to the server -> frontend).
func TestReleaseDetectionRecordsNewerTarget(t *testing.T) {
	// Fake release feed serving a newer release than the running CLI version.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metainfo.json" {
			http.Error(w, "wrong release document", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"version":"9.9.10","tag":"v9.9.10","platforms":{}},"test":{"version":"9.9.11-alpha.1","tag":"v9.9.11-alpha.1","platforms":{}}}}`))
	}))
	defer srv.Close()
	t.Setenv(cli.ReleaseManifestBaseURLEnv, srv.URL)

	coordinator := newUpdateObservationCoordinator(Config{
		ServerBaseURL: "https://api.multica.ai",
		CLIVersion:    "v9.9.9",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	d := &Daemon{
		cfg:               Config{CLIVersion: "v9.9.9"},
		updateObservation: coordinator,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	d.checkForNewerRelease(context.Background())

	obs := coordinator.Snapshot()
	if obs.TargetVersion != "v9.9.10" || obs.Phase != "waiting" || obs.LastOutcome != "update_available" {
		t.Fatalf("detection observation = %+v, want waiting/update_available for v9.9.10", obs)
	}
}

func TestReleaseDetectionLoopReportsRepeatedChecksWithoutMutation(t *testing.T) {
	var checks atomic.Int64
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks.Add(1)
		if r.URL.Path != "/metainfo.json" {
			http.Error(w, "wrong release document", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"version":"9.9.10","tag":"v9.9.10","platforms":{}},"test":{"version":"9.9.11-alpha.1","tag":"v9.9.11-alpha.1","platforms":{}}}}`))
	}))
	defer feed.Close()
	t.Setenv(cli.ReleaseManifestBaseURLEnv, feed.URL)

	coordinator := newUpdateObservationCoordinator(Config{
		CLIVersion:                   "v9.9.9",
		ReleaseDetectionConfigSource: "auto_detect",
		ReleaseDetectionInterval:     100 * time.Millisecond,
		ReleaseDetectionInitialDelay: time.Millisecond,
		UpdateObservationPath:        filepath.Join(t.TempDir(), "daemon-update-status.json"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var mutations atomic.Int64
	d := &Daemon{
		cfg: Config{
			CLIVersion:                   "v9.9.9",
			ReleaseDetectionInterval:     100 * time.Millisecond,
			ReleaseDetectionInitialDelay: time.Millisecond,
		},
		updateObservation: coordinator,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runUpdateFn: func(string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		verifyUpdatedBinaryFn: func(string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		activateStagedFn: func(context.Context, string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		cancelFunc: func() { mutations.Add(1) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		d.releaseDetectionLoop(ctx)
		close(done)
	}()
	for checks.Load() < 3 && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	for coordinator.Snapshot().LastOutcome != "update_available" && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	if got := checks.Load(); got < 3 {
		t.Fatalf("detection checks = %d, want initial plus repeated interval ticks", got)
	}
	observation := coordinator.Snapshot()
	if observation.TargetVersion != "v9.9.10" || observation.LastOutcome != "update_available" {
		t.Fatalf("detection observation = %+v, want v9.9.10 update_available", observation)
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("detect-only loop performed %d release mutations", got)
	}
}

func TestReleaseDetectionReadsTestFromCanonicalMetainfo(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/metainfo.json" {
			http.Error(w, "wrong channel", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"version":"9.9.9","tag":"v9.9.9","platforms":{}},"test":{"version":"10.0.0-alpha.2","tag":"v10.0.0-alpha.2","platforms":{}}}}`))
	}))
	defer srv.Close()
	t.Setenv(cli.ReleaseManifestBaseURLEnv, srv.URL)
	coordinator := newUpdateObservationCoordinator(Config{CLIVersion: "v10.0.0-alpha.1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := &Daemon{
		cfg:               Config{CLIVersion: "v10.0.0-alpha.1", ReleaseChannel: "alpha"},
		updateObservation: coordinator,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.checkForNewerRelease(context.Background())
	if got := coordinator.Snapshot().TargetVersion; got != "v10.0.0-alpha.2" {
		t.Fatalf("TargetVersion = %q", got)
	}
	if len(paths) != 1 || paths[0] != "/metainfo.json" {
		t.Fatalf("paths = %v", paths)
	}
}
