package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// machineUpgradeTakeover owns the complete detached-candidate coordination
// boundary. Daemon startup sees one deep-module operation: prepare locally,
// then wait until the incumbent commits the server generation handoff. The
// candidate cannot reach heartbeat, registration, or WebSocket startup while
// that wait is closed.
type machineUpgradeTakeover struct {
	mu        sync.Mutex
	ready     atomic.Bool
	committed chan struct{}
	commit    sync.Once
}

func newMachineUpgradeTakeover() *machineUpgradeTakeover {
	return &machineUpgradeTakeover{committed: make(chan struct{})}
}

func (t *machineUpgradeTakeover) isReady() bool {
	return t != nil && t.ready.Load()
}

func (t *machineUpgradeTakeover) markCommitted() {
	if t == nil {
		return
	}
	t.commit.Do(func() { close(t.committed) })
}

func (t *machineUpgradeTakeover) waitForCommit(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("detached takeover coordinator is unavailable")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.committed:
		return nil
	}
}

// prepare proves only local facts: exact journal lineage, committed Active
// binary, configured Workspace bindings, and the expected provider/runtime
// shape. It deliberately performs no server request.
func (t *machineUpgradeTakeover) prepare(d *Daemon) error {
	if t == nil || d == nil || !d.cfg.DetachedMachineUpgradeCandidate {
		return fmt.Errorf("daemon is not a detached machine-upgrade candidate")
	}
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		return err
	}
	if journal == nil || (journal.Phase != "handoff" && journal.Phase != "takeover_committed") {
		return fmt.Errorf("detached candidate has no active handoff journal")
	}
	if strings.TrimSpace(journal.ID) == "" || strings.TrimSpace(journal.Generation) == "" ||
		journal.PredecessorComputerGeneration <= 0 || d.cfg.ComputerGeneration <= journal.PredecessorComputerGeneration {
		return fmt.Errorf("detached candidate handoff identity is incomplete")
	}
	if !daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) {
		return fmt.Errorf("detached candidate version %s does not match target %s", d.cfg.CLIVersion, journal.TargetVersion)
	}
	state, err := readMachineUpgradeActivationState()
	if err != nil {
		return fmt.Errorf("read committed Active: %w", err)
	}
	if !journal.IncumbentGenerationKnown || state.Generation != journal.IncumbentGeneration+1 ||
		!daemonVersionsMatch(state.ActiveVersion, journal.TargetVersion) {
		return fmt.Errorf("detached candidate is not the exact committed Active generation")
	}
	bindings, err := d.configuredWorkspaceBindings()
	if err != nil {
		return err
	}
	for _, workspaceID := range journal.WorkspaceIDs {
		if _, ok := bindings[workspaceID]; !ok {
			return fmt.Errorf("accepted Workspace %s has no local Computer binding", workspaceID)
		}
	}
	if len(journal.RuntimeIDs) != len(journal.WorkspaceIDs)*len(d.cfg.Agents) {
		return fmt.Errorf("accepted Runtime set does not match local provider and Workspace shape")
	}
	t.ready.Store(true)
	if d.logger != nil {
		d.logger.Info("detached Machine Upgrade candidate is locally verified; waiting for generation takeover",
			"upgrade_id", journal.ID,
			"predecessor_computer_generation", journal.PredecessorComputerGeneration,
			"candidate_computer_generation", d.cfg.ComputerGeneration,
			"target_version", journal.TargetVersion,
			"runtime_count", len(journal.RuntimeIDs),
			"workspace_count", len(journal.WorkspaceIDs))
	}
	if journal.Phase == "takeover_committed" {
		t.markCommitted()
	}
	return nil
}

// MachineUpgradeTakeoverProof is the complete local identity handoff between
// one standalone incumbent and the exact detached candidate it spawned. The
// accepted Runtime and Workspace sets come from the durable operation receipt;
// the candidate values come from the process that owns the local control port.
type MachineUpgradeTakeoverProof struct {
	UpgradeID                         string   `json:"upgrade_id"`
	Generation                        string   `json:"generation"`
	DaemonID                          string   `json:"daemon_id"`
	PredecessorComputerGeneration     int64    `json:"predecessor_computer_generation"`
	PredecessorVersionStoreGeneration uint64   `json:"predecessor_version_store_generation"`
	CandidateComputerGeneration       int64    `json:"candidate_computer_generation"`
	CandidatePID                      int      `json:"candidate_pid"`
	TargetVersion                     string   `json:"target_version"`
	RuntimeIDs                        []string `json:"runtime_ids"`
	WorkspaceIDs                      []string `json:"workspace_ids"`
	Phase                             string   `json:"phase"`
}

// MachineUpgradeTakeoverExpectation returns the incumbent-owned half of a
// detached handoff. The launcher adds the child PID and newly allocated
// Computer generation before asking the candidate to commit.
func (d *Daemon) MachineUpgradeTakeoverExpectation() (MachineUpgradeTakeoverProof, error) {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		return MachineUpgradeTakeoverProof{}, err
	}
	if journal == nil || journal.Phase != "handoff" {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached machine upgrade requires a handoff journal")
	}
	if strings.TrimSpace(journal.ID) == "" || strings.TrimSpace(journal.Generation) == "" ||
		journal.PredecessorComputerGeneration <= 0 {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached machine upgrade handoff identity is incomplete")
	}
	if journal.PredecessorComputerGeneration != d.cfg.ComputerGeneration ||
		!daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion) {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached machine upgrade predecessor identity is stale")
	}
	runtimeIDs := append([]string(nil), journal.RuntimeIDs...)
	workspaceIDs := append([]string(nil), journal.WorkspaceIDs...)
	sort.Strings(runtimeIDs)
	sort.Strings(workspaceIDs)
	return MachineUpgradeTakeoverProof{
		UpgradeID:                         journal.ID,
		Generation:                        journal.Generation,
		DaemonID:                          d.cfg.DaemonID,
		PredecessorComputerGeneration:     journal.PredecessorComputerGeneration,
		PredecessorVersionStoreGeneration: journal.IncumbentGeneration,
		TargetVersion:                     journal.TargetVersion,
		RuntimeIDs:                        runtimeIDs,
		WorkspaceIDs:                      workspaceIDs,
		Phase:                             "handoff",
	}, nil
}

func (d *Daemon) machineUpgradeTakeoverProof() (MachineUpgradeTakeoverProof, error) {
	if !d.cfg.DetachedMachineUpgradeCandidate {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("daemon is not a detached machine-upgrade candidate")
	}
	if d.machineUpgradeTakeover == nil || !d.machineUpgradeTakeover.isReady() {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached candidate local proof is not ready")
	}
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		return MachineUpgradeTakeoverProof{}, err
	}
	if journal == nil || (journal.Phase != "handoff" && journal.Phase != "takeover_committed" && journal.Phase != "candidate_ready") {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached candidate has no active handoff journal")
	}
	runtimeIDs := append([]string(nil), journal.RuntimeIDs...)
	workspaceIDs := append([]string(nil), journal.WorkspaceIDs...)
	sort.Strings(runtimeIDs)
	sort.Strings(workspaceIDs)
	phase := journal.Phase
	if phase == "handoff" {
		phase = "takeover_ready"
	}
	return MachineUpgradeTakeoverProof{
		UpgradeID:                         journal.ID,
		Generation:                        journal.Generation,
		DaemonID:                          d.cfg.DaemonID,
		PredecessorComputerGeneration:     journal.PredecessorComputerGeneration,
		PredecessorVersionStoreGeneration: journal.IncumbentGeneration,
		CandidateComputerGeneration:       d.cfg.ComputerGeneration,
		CandidatePID:                      os.Getpid(),
		TargetVersion:                     d.cfg.CLIVersion,
		RuntimeIDs:                        runtimeIDs,
		WorkspaceIDs:                      workspaceIDs,
		Phase:                             phase,
	}, nil
}

// ValidateMachineUpgradeTakeoverProof compares the complete local handoff
// identity. Both the incumbent launcher and the candidate control endpoint use
// this one contract so their acceptance rules cannot drift.
func ValidateMachineUpgradeTakeoverProof(expected, actual MachineUpgradeTakeoverProof) error {
	if expected.UpgradeID != actual.UpgradeID {
		return fmt.Errorf("upgrade operation mismatch")
	}
	if expected.Generation != actual.Generation {
		return fmt.Errorf("handoff generation mismatch")
	}
	if expected.DaemonID != actual.DaemonID {
		return fmt.Errorf("daemon identity mismatch")
	}
	if expected.PredecessorComputerGeneration != actual.PredecessorComputerGeneration {
		return fmt.Errorf("predecessor Computer generation mismatch")
	}
	if expected.PredecessorVersionStoreGeneration != actual.PredecessorVersionStoreGeneration {
		return fmt.Errorf("predecessor VersionStore generation mismatch")
	}
	if expected.CandidateComputerGeneration != actual.CandidateComputerGeneration {
		return fmt.Errorf("candidate Computer generation mismatch")
	}
	if expected.CandidatePID != actual.CandidatePID {
		return fmt.Errorf("candidate PID mismatch")
	}
	if !daemonVersionsMatch(expected.TargetVersion, actual.TargetVersion) {
		return fmt.Errorf("target version mismatch")
	}
	if !sameStringSet(expected.RuntimeIDs, actual.RuntimeIDs) {
		return fmt.Errorf("accepted Runtime set mismatch")
	}
	if !sameStringSet(expected.WorkspaceIDs, actual.WorkspaceIDs) {
		return fmt.Errorf("accepted Workspace set mismatch")
	}
	if expected.Phase != actual.Phase {
		return fmt.Errorf("durable takeover phase mismatch")
	}
	return nil
}

// localMachineUpgradeTakeoverHandler commits the one remote ownership change.
// The candidate has proved its binary and local configuration but has not yet
// contacted server registration endpoints. Only the incumbent, holding the
// profile control token and exact child identity, can authorize the atomic
// predecessor-to-candidate Computer generation CAS.
func (d *Daemon) localMachineUpgradeTakeoverHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !d.localControlAuthorized(r) {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return
		}
		if d.machineUpgradeTakeover == nil {
			http.Error(w, "detached takeover coordinator is unavailable", http.StatusServiceUnavailable)
			return
		}
		d.machineUpgradeTakeover.mu.Lock()
		defer d.machineUpgradeTakeover.mu.Unlock()

		var expected MachineUpgradeTakeoverProof
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&expected); err != nil {
			http.Error(w, "invalid takeover proof", http.StatusBadRequest)
			return
		}
		actual, err := d.machineUpgradeTakeoverProof()
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		// Replays after the server CAS are local and idempotent. The candidate
		// resumes normal startup from the durable takeover_committed marker.
		if actual.Phase == "takeover_committed" || actual.Phase == "candidate_ready" {
			expected.Phase = actual.Phase
			if err := ValidateMachineUpgradeTakeoverProof(expected, actual); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			d.machineUpgradeTakeover.markCommitted()
			writeTakeoverJSON(w, actual)
			return
		}
		if expected.Phase != "takeover_ready" || actual.Phase != "takeover_ready" {
			http.Error(w, "detached takeover is not awaiting incumbent commit", http.StatusConflict)
			return
		}
		if err := ValidateMachineUpgradeTakeoverProof(expected, actual); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if d.client == nil {
			http.Error(w, "machine upgrade client is unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := d.client.CommitComputerUpgradeTakeover(r.Context(), actual.DaemonID, actual.UpgradeID, actual.Generation, actual.TargetVersion,
			actual.PredecessorComputerGeneration, actual.CandidateComputerGeneration, actual.RuntimeIDs, actual.WorkspaceIDs); err != nil {
			http.Error(w, "server takeover commit failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if d.logger != nil {
			d.logger.Info("server committed detached Machine Upgrade generation takeover",
				"upgrade_id", actual.UpgradeID,
				"predecessor_computer_generation", actual.PredecessorComputerGeneration,
				"candidate_computer_generation", actual.CandidateComputerGeneration)
		}
		journal, err := d.currentMachineUpgradeJournal()
		if err != nil || journal == nil || journal.Phase != "handoff" {
			http.Error(w, "reload takeover journal after server commit", http.StatusInternalServerError)
			return
		}
		journal.Phase = "takeover_committed"
		if err := d.writeMachineUpgradeJournal(journal); err != nil {
			http.Error(w, "persist takeover commit: "+err.Error(), http.StatusInternalServerError)
			return
		}
		d.machineUpgradeTakeover.markCommitted()
		if d.logger != nil {
			d.logger.Info("detached Machine Upgrade candidate released to authenticated startup", "upgrade_id", actual.UpgradeID)
		}
		actual.Phase = "takeover_committed"
		writeTakeoverJSON(w, actual)
	}
}

func writeTakeoverJSON(w http.ResponseWriter, proof MachineUpgradeTakeoverProof) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(proof)
}
