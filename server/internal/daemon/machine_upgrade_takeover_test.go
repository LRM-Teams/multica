package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV2DetachedSuccessorStartsWithoutWaitingForCommit(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	d := New(Config{
		CLIVersion:                      "v10.0.0",
		DaemonID:                        "daemon-1",
		WorkspaceID:                     "11111111-1111-1111-1111-111111111111",
		ComputerGeneration:              12,
		LocalControlToken:               "profile-secret",
		DetachedMachineUpgradeCandidate: true,
		MachineUpgradeTakeoverProtocol:  MachineUpgradeTakeoverProtocolV2,
	}, nil)
	if err := d.writeMachineUpgradeJournal(&machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		PredecessorComputerGeneration: 11, WorkspaceIDs: []string{"11111111-1111-1111-1111-111111111111"},
		Phase: "handoff",
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- d.startDetachedMachineUpgrade(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("v2 successor waited for commit")
	}
	select {
	case <-d.machineUpgradeTakeover.committed:
		t.Fatal("v2 successor closed commit wait without a commit POST")
	default:
	}
}

func TestLegacyDetachedSuccessorIsReleasedByCommitPOST(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	d := New(Config{
		CLIVersion:                      "v10.0.0",
		DaemonID:                        "computer-1",
		WorkspaceID:                     "11111111-1111-1111-1111-111111111111",
		ComputerGeneration:              12,
		DetachedMachineUpgradeCandidate: true,
		LocalControlToken:               "profile-secret",
	}, nil)
	if err := d.writeMachineUpgradeJournal(&machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		IncumbentGeneration: 1, PredecessorComputerGeneration: 11,
		RuntimeIDs: []string{
			"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		},
		WorkspaceIDs: []string{"11111111-1111-1111-1111-111111111111"},
		Phase:        "handoff",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- d.startDetachedMachineUpgrade(ctx) }()
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("legacy successor started without commit")
		}
		t.Fatalf("legacy successor exited early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	d.machineUpgradeTakeover.ready.Store(true)
	rec := httptest.NewRecorder()
	d.healthHandler(time.Now()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var health HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil || health.MachineUpgradeTakeover == nil {
		t.Fatalf("health = %+v err=%v", health, err)
	}
	body, err := json.Marshal(health.MachineUpgradeTakeover)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/machine-upgrade-takeover/commit", bytes.NewReader(body))
	req.Header.Set("X-Multica-Control-Token", "profile-secret")
	committed := httptest.NewRecorder()
	d.localMachineUpgradeTakeoverHandler().ServeHTTP(committed, req)
	if committed.Code != http.StatusOK {
		t.Fatalf("legacy commit status=%d body=%s", committed.Code, committed.Body.String())
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("legacy successor was not released by commit POST")
	}
}

func TestV2CommitPOSTRemainsIdempotentAfterSuccessorIsLive(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	d := New(Config{
		CLIVersion:                      "v10.0.0",
		DaemonID:                        "daemon-1",
		WorkspaceID:                     "11111111-1111-1111-1111-111111111111",
		ComputerGeneration:              12,
		LocalControlToken:               "profile-secret",
		DetachedMachineUpgradeCandidate: true,
		MachineUpgradeTakeoverProtocol:  MachineUpgradeTakeoverProtocolV2,
	}, nil)
	if err := d.writeMachineUpgradeJournal(&machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		PredecessorComputerGeneration: 11, WorkspaceIDs: []string{"11111111-1111-1111-1111-111111111111"},
		Phase: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.startDetachedMachineUpgrade(context.Background()); err != nil {
		t.Fatal(err)
	}

	expected := MachineUpgradeTakeoverProof{
		UpgradeID: "upgrade-1", Generation: "generation-a", ComputerID: "daemon-1",
		PredecessorComputerGeneration: 11, CandidateComputerGeneration: 12,
		CandidatePID: os.Getpid(), TargetVersion: "v10.0.0",
		WorkspaceIDs: []string{"11111111-1111-1111-1111-111111111111"}, Phase: "takeover_ready",
	}
	body, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/machine-upgrade-takeover/commit", bytes.NewReader(body))
	req.Header.Set("X-Multica-Control-Token", "profile-secret")
	rec := httptest.NewRecorder()
	d.localMachineUpgradeTakeoverHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit after live v2 successor status=%d body=%s", rec.Code, rec.Body.String())
	}
	replay := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/machine-upgrade-takeover/commit", bytes.NewReader(body))
	req2.Header.Set("X-Multica-Control-Token", "profile-secret")
	d.localMachineUpgradeTakeoverHandler().ServeHTTP(replay, req2)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay commit status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestV2DetachedSuccessorNotifiesUpgradeCompleteOnHandoffJournal(t *testing.T) {
	root := t.TempDir()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return filepath.Join(root, "store"), nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })

	attested := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/computer/machine-upgrades/upgrade-1/attest" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		attested++
		_ = json.NewEncoder(w).Encode(map[string]string{"phase": "completed"})
	}))
	defer upstream.Close()

	workspaceID := "11111111-1111-1111-1111-111111111111"
	runtimeID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	d := New(Config{
		CLIVersion:                      "v10.0.0",
		DaemonID:                        "daemon-1",
		WorkspaceID:                     workspaceID,
		ComputerGeneration:              12,
		DetachedMachineUpgradeCandidate: true,
		MachineUpgradeTakeoverProtocol:  MachineUpgradeTakeoverProtocolV2,
	}, nil)
	d.client = NewClient(upstream.URL)
	d.workspaces = map[string]*workspaceState{
		workspaceID: newWorkspaceState(workspaceID, []string{runtimeID}),
	}
	if err := d.writeMachineUpgradeJournal(&machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-a", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0",
		PredecessorComputerGeneration: 11, RuntimeIDs: []string{runtimeID},
		WorkspaceIDs: []string{workspaceID}, Phase: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.startDetachedMachineUpgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.attestComputerMachineUpgrade(context.Background(), []string{workspaceID}); err != nil {
		t.Fatal(err)
	}
	if attested != 1 {
		t.Fatalf("upgrade-complete notify calls = %d, want 1", attested)
	}
	stored, err := d.currentMachineUpgradeJournal()
	if err != nil || stored == nil || stored.Phase != "candidate_ready" {
		t.Fatalf("journal after notify = %+v err=%v", stored, err)
	}
}
