package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ActivationAttemptEnv is passed into candidate probes so recovery never
// identifies a candidate by PID alone (design §3.3.2).
const ActivationAttemptEnv = "MULTICA_ACTIVATION_ATTEMPT_ID"

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
	if err := probeCandidateVersion(ctx, staged.BinaryPath, staged.Version, attemptID); err != nil {
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
	return next, staged.BinaryPath, nil
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
