package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

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
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		return MachineUpgradeTakeoverProof{}, err
	}
	if journal == nil || (journal.Phase != "handoff" && journal.Phase != "candidate_ready") {
		return MachineUpgradeTakeoverProof{}, fmt.Errorf("detached candidate has no active handoff journal")
	}
	runtimeIDs := d.allRuntimeIDs()
	workspaceIDs := d.workspaceRunnerWorkspaceIDs()
	sort.Strings(runtimeIDs)
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
		Phase:                             journal.Phase,
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

// localMachineUpgradeTakeoverHandler is the second phase of a detached
// handoff. The candidate has already acquired the exclusive loopback listener
// and registered its complete managed set. Only the incumbent, holding the
// profile control token and exact child identity, may authorize remote
// completion. A successful response is emitted after candidate_ready is
// persisted locally.
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
		d.machineUpgradeTakeoverMu.Lock()
		defer d.machineUpgradeTakeoverMu.Unlock()

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
		// A replay after the server acknowledgement but before the incumbent saw
		// the response is safe: compare the complete identity and return the
		// already durable candidate_ready proof without attesting twice.
		if actual.Phase == "candidate_ready" {
			expected.Phase = actual.Phase
			if err := ValidateMachineUpgradeTakeoverProof(expected, actual); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			d.releaseClaimBarrier()
			writeTakeoverJSON(w, actual)
			return
		}
		if expected.Phase != "handoff" || actual.Phase != "handoff" {
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
		if err := d.client.AttestComputerUpgrade(r.Context(), actual.DaemonID, actual.UpgradeID, actual.Generation, actual.TargetVersion, actual.RuntimeIDs, actual.WorkspaceIDs); err != nil {
			http.Error(w, "server takeover attestation failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := d.markMachineUpgradeCandidateReady(); err != nil {
			http.Error(w, "persist takeover commit: "+err.Error(), http.StatusInternalServerError)
			return
		}
		d.releaseClaimBarrier()
		actual.Phase = "candidate_ready"
		writeTakeoverJSON(w, actual)
	}
}

func writeTakeoverJSON(w http.ResponseWriter, proof MachineUpgradeTakeoverProof) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(proof)
}
