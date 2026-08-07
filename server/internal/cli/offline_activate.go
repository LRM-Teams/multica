package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ActivationAttemptEnv is passed into candidate probes so recovery never
// identifies a candidate by PID alone (design §3.3.2).
const ActivationAttemptEnv = "MULTICA_ACTIVATION_ATTEMPT_ID"

var probeStagedCandidate = probeCandidateVersion

// OfflineActivateStaged CAS-commits a staged release tag as Active after a real
// candidate --version probe (not a fake healthy). Used by offline CLI update
// and by daemon thin activate (no full register yet).
//
// Order: prepare → candidate_running → probe --version → candidate_healthy → CAS → committed.
func (s *VersionStore) OfflineActivateStaged(
	ctx context.Context,
	candidateTag string,
	attemptID string,
) (ActivationState, string, error) {
	if s == nil {
		return ActivationState{}, "", fmt.Errorf("version store is required")
	}
	staged, err := s.ResolveStagedVersion(candidateTag)
	if err != nil {
		return ActivationState{}, "", err
	}
	state, err := s.ReadActivationState()
	if err != nil {
		return ActivationState{}, "", err
	}
	if state.ActiveVersion == staged.Version {
		if _, err := s.verifyExisting(ctx, staged.Version, ""); err != nil {
			return ActivationState{}, "", fmt.Errorf("verify committed active %s: %w", staged.Version, err)
		}
		return state, staged.BinaryPath, nil
	}
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		attemptID = uuid.NewString()
	}

	if _, err := s.PrepareActivationAttempt(
		attemptID,
		state.Generation,
		state.ActiveVersion,
		staged.Version,
	); err != nil {
		return ActivationState{}, "", fmt.Errorf("prepare journal: %w", err)
	}
	abort := func(code string) {
		_, _ = s.AdvanceActivationPhase(attemptID, ActivationPhaseAborted, code)
	}

	if _, err := s.AdvanceActivationPhase(attemptID, ActivationPhaseCandidateRunning, ""); err != nil {
		abort("activate_phase_failed")
		return ActivationState{}, "", err
	}

	// Real candidate probe: run staged binary --version with attempt_id in env.
	// This is not full health+register, but it is not a no-op "fake healthy".
	if err := probeStagedCandidate(ctx, staged.BinaryPath, staged.Version, attemptID); err != nil {
		abort("candidate_probe_failed")
		return ActivationState{}, "", fmt.Errorf("candidate probe: %w", err)
	}

	if _, err := s.AdvanceActivationPhase(attemptID, ActivationPhaseCandidateHealthy, ""); err != nil {
		abort("activate_phase_failed")
		return ActivationState{}, "", err
	}

	next, err := s.CompareAndSwapActivation(ctx, state.Generation, staged.Version)
	if err != nil {
		abort("activation_cas_failed")
		return ActivationState{}, "", fmt.Errorf("CAS Active: %w", err)
	}
	if _, err := s.AdvanceActivationPhase(attemptID, ActivationPhaseCommitted, ""); err != nil {
		// CAS already rotated — journal best-effort.
		_ = err
	}

	// Best-effort prune of now-inactive version directories. The new version
	// is already active and the old one is recorded as previous; neither is
	// at risk. A prune failure must not fail the activation itself.
	if _, pruneErr := s.PruneInactiveVersions(ctx); pruneErr != nil {
		slog.Debug("version store prune failed after successful activation",
			"active_version", staged.Version,
			"previous_version", state.ActiveVersion,
			"error", pruneErr)
	}

	return next, staged.BinaryPath, nil
}

// RollbackToPreviousActive restores the retained previous generation through
// the same probe+journal+CAS path as a forward activation. It intentionally
// says nothing about a live daemon or remote Machine Upgrade convergence;
// callers must obtain that evidence separately before reporting rolled_back.
func (s *VersionStore) RollbackToPreviousActive(ctx context.Context, attemptID string) (ActivationState, string, error) {
	if s == nil {
		return ActivationState{}, "", fmt.Errorf("version store is required")
	}
	state, err := s.ReadActivationState()
	if err != nil {
		return ActivationState{}, "", err
	}
	if strings.TrimSpace(state.PreviousVersion) == "" {
		return ActivationState{}, "", errors.New("previous Active version is unavailable for rollback")
	}
	return s.OfflineActivateStaged(ctx, state.PreviousVersion, attemptID)
}

func probeCandidateVersion(ctx context.Context, binaryPath, expectedVersion, attemptID string) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	cmd.Env = append(os.Environ(), ActivationAttemptEnv+"="+attemptID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run candidate --version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if !versionOutputMatchesRelease(string(out), expectedVersion) {
		return fmt.Errorf(
			"candidate version mismatch: expected %s, got %q",
			expectedVersion,
			strings.TrimSpace(string(out)),
		)
	}
	return nil
}
