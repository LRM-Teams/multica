package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestForceRestartKillsWorkerBeforeItCanAckClaimedWork is the "not just
// code-reading" proof task #815 requires before the escalating-restart
// backstop (graceful -> grace window -> force restart) can be trusted: it
// demonstrates, with a real OS process under a real Supervisor, that
// RequestRestart() genuinely terminates a worker that is mid-task before
// that worker can ever signal completion.
//
// This test does not reach the server's lease-reclaim path directly (that
// is proven separately, at the server level, by
// TestChannelAgentInboxDrainReclaimsExpiredDelivery in
// internal/handler/channel_test.go, which is run and passing on this same
// change). Composed, the two prove the property Parker required: a task
// killed by a supervisor force-restart looks — from the server's point of
// view — exactly like any other daemon crash it already recovers from via
// lease expiry, because the worker is never given the chance to distinguish
// itself by sending a completion ack. Nothing is silently lost; it is
// delayed and retried.
func TestForceRestartKillsWorkerBeforeItCanAckClaimedWork(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, "claim")

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=claim-then-block",
		workerHelperCountEnv+"="+filepath.Join(dir, "starts"),
		workerHelperCrashEnv+"=0",
		workerHelperClaimEnv+"="+claimPath,
	)
	s, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       os.Args[0],
		WorkerArgs:       []string{"-test.run=^TestSupervisorWorkerProcess$"},
		WorkerEnv:        env,
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       25 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()

	waitForClaimFile(t, claimPath, "claimed")
	firstGeneration := waitForGenerationAtLeast(t, s, 1)

	// This is the force-restart step task #815's escalation watcher takes
	// after the daemon's own graceful path has had its grace window and the
	// worker is still mid-task: kill it and start a replacement, the same
	// mechanism a stuck/busy daemon gets force-restarted with.
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	waitForGenerationAtLeast(t, s, firstGeneration+1)

	got, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatalf("read claim file: %v", err)
	}
	if string(got) != "claimed" {
		t.Fatalf("claim file = %q after force restart, want exactly %q (worker must never get to write a completion ack)", got, "claimed")
	}

	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func waitForClaimFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("claim file %s never reached %q within timeout", path, want)
}

func waitForGenerationAtLeast(t *testing.T, s *Supervisor, want uint64) uint64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap := s.Snapshot(); snap.Generation >= want && snap.WorkerPID != 0 {
			return snap.Generation
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("supervisor generation never reached %d within timeout (last snapshot: %+v)", want, s.Snapshot())
	return 0
}
