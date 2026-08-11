package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHandleMachineUpgradeAcceptsOnlyExactRunningVersion(t *testing.T) {
	accepted := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/runtime-1/machine-upgrades/upgrade-1/accept" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		accepted <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	d := &Daemon{
		cfg:    Config{CLIVersion: "v9.9.9"},
		client: NewClient(server.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.client.SetToken("test-token")
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{ID: "upgrade-1", TargetVersion: "9.9.9"})
	select {
	case got := <-accepted:
		if got["cli_version"] != "v9.9.9" || got["generation_id"] == "" {
			t.Fatalf("accept payload = %#v", got)
		}
	default:
		t.Fatal("exact running version was not accepted")
	}
}

func TestHandleMachineUpgradeAlreadyCurrentAttestsEveryWorkspaceConnection(t *testing.T) {
	previousDetect := detectAgentVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "codex-cli 9.9.9", nil }
	t.Cleanup(func() { detectAgentVersion = previousDetect })

	var attested map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			generation := body["generation_id"]
			_ = json.NewEncoder(w).Encode(MachineUpgradeReceipt{
				ID:                   "upgrade-1",
				Phase:                "converging",
				AcceptedGeneration:   &generation,
				AcceptedRuntimeIDs:   []string{"runtime-1"},
				AcceptedWorkspaceIDs: []string{"workspace-1", "workspace-2"},
			})
		case r.URL.Path == "/api/daemon/starting":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/daemon/register":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			workspaceID, _ := body["workspace_id"].(string)
			_ = json.NewEncoder(w).Encode(RegisterResponse{Runtimes: []Runtime{{
				ID: "runtime-1", WorkspaceID: workspaceID, Name: "Codex", Provider: "codex", Status: "online",
			}}})
		case r.URL.Path == "/api/daemon/computer/machine-upgrades/upgrade-1/attest":
			if err := json.NewDecoder(r.Body).Decode(&attested); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	d := &Daemon{
		cfg: Config{
			CLIVersion: "v9.9.9", DaemonID: "computer-1",
			Agents: map[string]AgentEntry{"codex": {Path: "codex"}},
		},
		client:        NewClient(server.URL),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
		workspaces: map[string]*workspaceState{
			"workspace-1": newWorkspaceState("workspace-1", []string{"runtime-1"}),
			"workspace-2": newWorkspaceState("workspace-2", []string{"runtime-2"}),
		},
	}
	d.client.SetToken("test-token")
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{ID: "upgrade-1", TargetVersion: "v9.9.9"})

	if attested == nil {
		t.Fatal("already-current Machine Upgrade did not emit Computer-level attestation")
	}
	if got := attested["daemon_id"]; got != "computer-1" {
		t.Fatalf("daemon_id = %#v", got)
	}
	workspaceIDs, _ := attested["workspace_ids"].([]any)
	if len(workspaceIDs) != 2 || workspaceIDs[0] != "workspace-1" || workspaceIDs[1] != "workspace-2" {
		t.Fatalf("workspace_ids = %#v", attested["workspace_ids"])
	}
}

func TestMachineUpgradeJournalRestoresHandoffGeneration(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &Daemon{cfg: Config{CLIVersion: "v10.0.0"}, logger: logger}
	journal := &machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-successor", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0", RuntimeIDs: []string{"runtime-1", "runtime-2"}, Phase: "handoff",
	}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		t.Fatal(err)
	}
	successor := New(Config{CLIVersion: "v10.0.0"}, logger)
	if got := successor.machineUpgradeGenerationID(); got != "generation-successor" {
		t.Fatalf("successor generation = %q, want journal generation", got)
	}
}

func TestCurrentMachineUpgradeJournalAllowsCleanMachine(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	d := &Daemon{}
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		t.Fatalf("clean machine without journal directory: %v", err)
	}
	if journal != nil {
		t.Fatalf("clean machine journal = %+v, want nil", journal)
	}
}

func TestMachineUpgradeCandidateReadyIsDurableAndIdempotent(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	d := &Daemon{cfg: Config{CLIVersion: "v10.0.0"}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	journal := &machineUpgradeJournal{ID: "upgrade-1", Generation: "generation-successor", TargetVersion: "v10.0.0", Phase: "handoff"}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := d.markMachineUpgradeCandidateReady(); err != nil {
		t.Fatal(err)
	}
	if err := d.markMachineUpgradeCandidateReady(); err != nil {
		t.Fatalf("candidate-ready replay: %v", err)
	}
	stored, err := d.currentMachineUpgradeJournal()
	if err != nil || stored == nil || stored.Phase != "candidate_ready" {
		t.Fatalf("candidate journal = %+v, %v", stored, err)
	}
}

func TestInterruptedMachineUpgradeRecoveryResumesOnlyIncompletePhases(t *testing.T) {
	for _, tc := range []struct {
		name           string
		phase          string
		runningVersion string
		wantStage      int
		wantVerify     int
		wantActivate   int
		wantRestart    bool
		wantPhase      string
		detached       bool
	}{
		{name: "before staging", phase: "accepted", runningVersion: "v9.9.9", wantStage: 1, wantVerify: 1, wantActivate: 1, wantRestart: true, wantPhase: "handoff"},
		{name: "after staging", phase: "staged", runningVersion: "v9.9.9", wantVerify: 1, wantActivate: 1, wantRestart: true, wantPhase: "handoff"},
		{name: "after Active commit", phase: "handoff", runningVersion: "v10.0.0", wantPhase: "handoff"},
		{name: "during detached candidate startup", phase: "handoff", runningVersion: "v10.0.0", wantPhase: "handoff", detached: true},
		{name: "after local attestation", phase: "candidate_ready", runningVersion: "v10.0.0", wantPhase: "candidate_ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			previousRoot := versionStoreRootFn
			versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
			t.Cleanup(func() { versionStoreRootFn = previousRoot })
			stageCalls, verifyCalls, activateCalls, restartCalls := 0, 0, 0, 0
			d := &Daemon{
				cfg:    Config{CLIVersion: tc.runningVersion, ComputerGeneration: 2, DetachedMachineUpgradeCandidate: tc.detached},
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				machineUpgradeStageFn: func(string) (string, error) {
					stageCalls++
					return "staged", nil
				},
				machineUpgradeVerifyFn: func(string, string) (string, error) {
					verifyCalls++
					return "v10.0.0", nil
				},
				activateStagedFn: func(context.Context, string, string) (string, error) {
					activateCalls++
					return "/versions/v10.0.0/multica", nil
				},
				cancelFunc: func() { restartCalls++ },
			}
			journal := &machineUpgradeJournal{
				ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
				PredecessorComputerGeneration: 1, RuntimeIDs: []string{"runtime-1"}, Phase: tc.phase,
			}
			if err := d.writeMachineUpgradeJournal(journal); err != nil {
				t.Fatal(err)
			}
			restarted, err := d.recoverInterruptedMachineUpgrade(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if restarted != tc.wantRestart || stageCalls != tc.wantStage || verifyCalls != tc.wantVerify || activateCalls != tc.wantActivate || restartCalls != btoi(tc.wantRestart) {
				t.Fatalf("recovery restarted=%v stage=%d verify=%d activate=%d restart=%d", restarted, stageCalls, verifyCalls, activateCalls, restartCalls)
			}
			stored, err := d.currentMachineUpgradeJournal()
			if err != nil || stored == nil || stored.Phase != tc.wantPhase {
				t.Fatalf("recovery journal=%+v err=%v", stored, err)
			}
			if stored.TargetRestartAttempts != btoi(tc.wantRestart) {
				t.Fatalf("target restart attempts=%d, want %d", stored.TargetRestartAttempts, btoi(tc.wantRestart))
			}
		})
	}
}

func TestForwardRecoveryBoundsTargetRestarts(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	activateCalls, restartCalls := 0, 0
	d := &Daemon{
		cfg:    Config{CLIVersion: "v9.9.9"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		activateStagedFn: func(context.Context, string, string) (string, error) {
			activateCalls++
			return "", nil
		},
		cancelFunc: func() { restartCalls++ },
	}
	journal := &machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		RuntimeIDs: []string{"runtime-1"}, Phase: "handoff", TargetRestartAttempts: machineUpgradeMaxRestartAttempts,
	}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		t.Fatal(err)
	}
	if restarted, err := d.recoverInterruptedMachineUpgrade(context.Background()); err == nil || restarted {
		t.Fatalf("exhausted recovery restarted=%v err=%v", restarted, err)
	}
	if activateCalls != 0 || restartCalls != 0 {
		t.Fatalf("exhausted recovery activate=%d restart=%d", activateCalls, restartCalls)
	}
	stored, err := d.currentMachineUpgradeJournal()
	if err != nil || stored == nil || stored.TargetRestartAttempts != machineUpgradeMaxRestartAttempts {
		t.Fatalf("retained marker=%+v err=%v", stored, err)
	}
}

func TestRollbackPendingRecoveryRestoresOnceAndBoundsRestarts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		runningVersion  string
		restartAttempts int
		wantRestore     int
		wantRestart     bool
		wantError       bool
		detached        bool
	}{
		{name: "detached target restores exact source and restarts", runningVersion: "v10.0.0", wantRestore: 1, wantRestart: true, detached: true},
		{name: "restored source continues to registration proof", runningVersion: "v9.9.9", restartAttempts: 1},
		{name: "exhausted rollback remains retained for operator recovery", runningVersion: "v10.0.0", restartAttempts: machineUpgradeMaxRestartAttempts, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			previousRoot := versionStoreRootFn
			versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
			t.Cleanup(func() { versionStoreRootFn = previousRoot })
			restoreCalls, restartCalls := 0, 0
			d := &Daemon{
				cfg:    Config{CLIVersion: tc.runningVersion, DetachedMachineUpgradeCandidate: tc.detached},
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				machineUpgradeRollbackFn: func(context.Context, *machineUpgradeJournal) (string, error) {
					restoreCalls++
					return "/versions/v9.9.9/multica", nil
				},
				cancelFunc: func() { restartCalls++ },
			}
			journal := &machineUpgradeJournal{
				ID: "upgrade-1", Generation: "generation-a", RollbackGeneration: "rollback-a",
				SourceVersion: "v9.9.9", TargetVersion: "v10.0.0", IncumbentGeneration: 7,
				RuntimeIDs: []string{"runtime-1"}, Phase: "rollback_pending", RollbackRestartAttempts: tc.restartAttempts,
			}
			if err := d.writeMachineUpgradeJournal(journal); err != nil {
				t.Fatal(err)
			}
			restarted, err := d.recoverInterruptedMachineUpgrade(context.Background())
			if (err != nil) != tc.wantError {
				t.Fatalf("recovery err=%v, wantError=%v", err, tc.wantError)
			}
			if restarted != tc.wantRestart || restoreCalls != tc.wantRestore || restartCalls != btoi(tc.wantRestart) {
				t.Fatalf("recovery restarted=%v restores=%d restarts=%d err=%v", restarted, restoreCalls, restartCalls, err)
			}
			stored, readErr := d.currentMachineUpgradeJournal()
			if readErr != nil || stored == nil || stored.Phase != "rollback_pending" {
				t.Fatalf("rollback marker=%+v err=%v", stored, readErr)
			}
			wantAttempts := tc.restartAttempts + btoi(tc.wantRestart)
			if stored.RollbackRestartAttempts != wantAttempts {
				t.Fatalf("rollback attempts=%d, want %d", stored.RollbackRestartAttempts, wantAttempts)
			}
		})
	}
}

func TestMachineUpgradeJournalClearsOnlyForMatchingTerminalReceiptAndLiveSet(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	generation := "generation-a"
	targetVersion := "v10.0.0"
	serverReceipt := MachineUpgradeReceipt{
		ID: "upgrade-1", Phase: "completed", AcceptedGeneration: &generation,
		ResolvedTarget:     &targetVersion,
		AcceptedRuntimeIDs: []string{"runtime-1"}, AttestedRuntimeIDs: []string{"runtime-1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/runtime-1/machine-upgrades/"+serverReceipt.ID {
			t.Fatalf("receipt path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(serverReceipt)
	}))
	defer server.Close()
	d := &Daemon{
		cfg:    Config{CLIVersion: "v10.0.0", ComputerGeneration: 2},
		client: NewClient(server.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces: map[string]*workspaceState{"workspace-1": {runtimeIDs: []string{"runtime-1"}}},
	}
	d.client.SetToken("test-token")
	journal := &machineUpgradeJournal{
		ID: "upgrade-1", Generation: generation, SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		PredecessorComputerGeneration: 1, RuntimeIDs: []string{"runtime-1"}, Phase: "candidate_ready",
	}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		t.Fatal(err)
	}

	serverReceipt.AcceptedGeneration = stringPtr("stale-generation")
	if err := d.reconcileMachineUpgradeTerminalJournal(context.Background()); err == nil {
		t.Fatal("stale terminal receipt must be rejected")
	}
	if retained, err := d.currentMachineUpgradeJournal(); err != nil || retained == nil {
		t.Fatalf("stale receipt removed marker=%+v err=%v", retained, err)
	}

	serverReceipt.AcceptedGeneration = &generation
	if err := d.reconcileMachineUpgradeTerminalJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cleared, err := d.currentMachineUpgradeJournal(); err != nil || cleared != nil {
		t.Fatalf("matching terminal receipt marker=%+v err=%v", cleared, err)
	}

	sourceVersion := "v9.9.9"
	rollbackGeneration := "rollback-a"
	serverReceipt = MachineUpgradeReceipt{
		ID: "upgrade-2", Phase: "rolled_back", SourceVersion: &sourceVersion,
		AcceptedRuntimeIDs: []string{"runtime-1"}, RollbackGeneration: &rollbackGeneration,
		RollbackRuntimeIDs: []string{"runtime-1"},
	}
	d.cfg.CLIVersion = sourceVersion
	rollbackJournal := &machineUpgradeJournal{
		ID: "upgrade-2", Generation: "generation-b", RollbackGeneration: rollbackGeneration,
		SourceVersion: sourceVersion, TargetVersion: targetVersion, RuntimeIDs: []string{"runtime-1"}, Phase: "rollback_pending",
	}
	if err := d.writeMachineUpgradeJournal(rollbackJournal); err != nil {
		t.Fatal(err)
	}
	if err := d.reconcileMachineUpgradeTerminalJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cleared, err := d.currentMachineUpgradeJournal(); err != nil || cleared != nil {
		t.Fatalf("matching rollback receipt marker=%+v err=%v", cleared, err)
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestResolveMachineUpgradeLatestAtDelivery(t *testing.T) {
	previous := fetchMachineUpgradeRelease
	fetchMachineUpgradeRelease = func(channel cli.ReleaseChannel, override string) (*cli.ReleaseManifest, error) {
		if channel != cli.ReleaseChannelLatest || override != "" {
			t.Fatalf("channel=%s override=%q", channel, override)
		}
		return &cli.ReleaseManifest{TagName: "v10.0.0"}, nil
	}
	t.Cleanup(func() { fetchMachineUpgradeRelease = previous })
	resolved, err := resolveMachineUpgradeTarget("latest")
	if err != nil || resolved != "v10.0.0" {
		t.Fatalf("latest resolution = %q, %v", resolved, err)
	}
}

func TestResolveMachineUpgradeUsesConfiguredAlphaChannel(t *testing.T) {
	previous := fetchMachineUpgradeRelease
	fetchMachineUpgradeRelease = func(channel cli.ReleaseChannel, override string) (*cli.ReleaseManifest, error) {
		if channel != cli.ReleaseChannelAlpha || override != "https://feed.example/computer" {
			t.Fatalf("channel=%s override=%q", channel, override)
		}
		return &cli.ReleaseManifest{TagName: "v10.1.0-alpha.4"}, nil
	}
	t.Cleanup(func() { fetchMachineUpgradeRelease = previous })
	resolved, err := resolveMachineUpgradeTargetForChannel("latest", "alpha", "https://feed.example/computer")
	if err != nil || resolved != "v10.1.0-alpha.4" {
		t.Fatalf("alpha resolution = %q, %v", resolved, err)
	}
}

func TestHandleMachineUpgradeDifferentVersionStartsWithServerAcceptance(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{
		cfg:    Config{CLIVersion: "v9.9.9"},
		client: NewClient(server.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{ID: "upgrade-1", TargetVersion: "v10.0.0"})
	if !called {
		t.Fatal("different target was not accepted before the durable stage path")
	}
}

func TestHandleMachineUpgradeActivatesAcceptedTargetWhenUpdateObservationIsStale(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), "store")
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return storeRoot, nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	previousOpen := openVersionStoreFn
	openVersionStoreFn = func(root string) (*cli.VersionStore, error) {
		return cli.NewVersionStore(root, "linux", func(context.Context, string, string) error { return nil })
	}
	t.Cleanup(func() { openVersionStoreFn = previousOpen })

	previousStage := downloadAndStageReleaseFn
	downloadAndStageReleaseFn = func(
		ctx context.Context,
		store *cli.VersionStore,
		targetVersion string,
		_ time.Duration,
		_ string,
	) (cli.StageReleaseResult, error) {
		candidate := []byte("#!/bin/sh\necho 'multica 0.4.17'\n")
		return cli.StageReleaseBytes(ctx, store, targetVersion, candidate, "asset.tar.gz")
	}
	t.Cleanup(func() { downloadAndStageReleaseFn = previousStage })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accept") {
			_ = json.NewEncoder(w).Encode(MachineUpgradeReceipt{
				ID:                 "upgrade-1",
				RequestedTarget:    "v0.4.17",
				Phase:              "staging",
				AcceptedGeneration: stringPtr("generation-a"),
				AcceptedRuntimeIDs: []string{"runtime-1"},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	observation := newTestUpdateObservationCoordinator(t, filepath.Join(t.TempDir(), "daemon-update-status.json"))
	if err := observation.Transition(func(current *protocol.DaemonUpdateObservation) {
		current.TargetVersion = "v0.4.13"
	}); err != nil {
		t.Fatalf("persist stale update observation: %v", err)
	}

	d := &Daemon{
		cfg:               Config{CLIVersion: "v0.4.16"},
		client:            NewClient(server.URL),
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		updateObservation: observation,
	}
	d.client.SetToken("test-token")
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{
		ID:            "upgrade-1",
		TargetVersion: "v0.4.17",
	})

	store, err := openVersionStoreFn(storeRoot)
	if err != nil {
		t.Fatalf("open version store: %v", err)
	}
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("read activation state: %v", err)
	}
	if state.ActiveVersion != "v0.4.17" {
		t.Fatalf("active version = %q, want the accepted target v0.4.17", state.ActiveVersion)
	}
}

func TestMachineUpgradeBusyMachineDoesNotStageOrChangeBarrier(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	var progressPhase string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accept") {
			_ = json.NewEncoder(w).Encode(MachineUpgradeReceipt{ID: "upgrade-1", RequestedTarget: "v10.0.0", Phase: "staging", AcceptedGeneration: stringPtr("generation-a"), AcceptedRuntimeIDs: []string{"runtime-1"}})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		progressPhase = body["phase"]
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{cfg: Config{CLIVersion: "v9.9.9"}, client: NewClient(server.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.client.SetToken("test-token")
	d.activeTasks.Store(1)
	// Context loss during the graceful drain is a hard handoff failure: it
	// cannot concurrently reopen claims and continue to activation.
	d.machineUpgradeWait = func(context.Context, time.Duration) error { return context.Canceled }
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{ID: "upgrade-1", TargetVersion: "v10.0.0"})
	if progressPhase != "failed" {
		t.Fatalf("busy operation progress = %q, want failed", progressPhase)
	}
	if d.pauseClaims {
		t.Fatal("busy operation left claim barrier set")
	}
}

func TestMachineUpgradeBusyHandoffWaitsTenSecondsThenForcesOnlyManagedProcess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	backend := &canonicalRuntimeTestBackend{}
	gracefulCancelled := false
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: backend, running: true}
	d := &Daemon{
		canonicalRuntimes: pool,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		machineUpgradeNow: func() time.Time { return now },
		machineUpgradeWait: func(ctx context.Context, delay time.Duration) error {
			if !gracefulCancelled {
				return fmt.Errorf("managed task was not asked to stop before force deadline")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			now = now.Add(delay)
			return nil
		},
	}
	d.activeTasks.Store(1)
	d.registerMachineUpgradeTask(1, func() { gracefulCancelled = true })
	if err := d.beginMachineUpgradeHandoff(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := now.Sub(time.Unix(1_700_000_000, 0)); elapsed != machineUpgradeGracefulDrain {
		t.Fatalf("force elapsed = %s, want %s", elapsed, machineUpgradeGracefulDrain)
	}
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("managed backend force kills = %d, want 1", got)
	}
	if !d.pauseClaims {
		t.Fatal("successful busy handoff released claim barrier")
	}
}

func TestMachineUpgradeBusyHandoffNeverForcesUnownedBackend(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: &canonicalRuntimeNonForceKillableTestBackend{}, running: true}
	now := time.Unix(1_700_000_010, 0)
	d := &Daemon{
		canonicalRuntimes: pool,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		machineUpgradeNow: func() time.Time { return now },
		machineUpgradeWait: func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		},
	}
	d.activeTasks.Store(1)
	if err := d.beginMachineUpgradeHandoff(context.Background()); err == nil {
		t.Fatal("unowned backend force path unexpectedly succeeded")
	}
}

func TestMachineUpgradeHandoffFailsClosedWhenClaimIsStillInFlightAtDeadline(t *testing.T) {
	now := time.Unix(1_700_000_020, 0)
	d := &Daemon{
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		machineUpgradeNow: func() time.Time { return now },
		machineUpgradeWait: func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		},
	}
	if !d.tryEnterClaim() {
		t.Fatal("pre-barrier claim was not admitted")
	}

	err := d.beginMachineUpgradeHandoff(context.Background())
	if err == nil || !strings.Contains(err.Error(), "claim") {
		t.Fatalf("handoff error = %v, want an in-flight claim failure", err)
	}
	if d.pauseClaims {
		t.Fatal("failed handoff left the claim barrier set")
	}
	if d.claimsInFlight != 1 {
		t.Fatalf("claimsInFlight = %d, want the admitted claim to remain accounted", d.claimsInFlight)
	}

	d.exitClaim()
}

func stringPtr(value string) *string { return &value }
