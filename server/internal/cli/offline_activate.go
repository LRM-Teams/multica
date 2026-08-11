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
	return s.offlineActivateStaged(ctx, candidateTag, attemptID, false)
}

func (s *VersionStore) offlineActivateStaged(
	ctx context.Context,
	candidateTag string,
	attemptID string,
	allowUnusablePrevious bool,
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
	if attemptID != "" {
		if existing, journalErr := s.ReadActivationJournal(); journalErr == nil && existing.AttemptID == attemptID && existing.Phase != ActivationPhaseAborted {
			if existing.CandidateTag != staged.Version {
				return ActivationState{}, "", fmt.Errorf("activation attempt %s targets %s, not %s", attemptID, existing.CandidateTag, staged.Version)
			}
			return s.RecoverActivationAttempt(ctx, attemptID)
		} else if journalErr != nil && !errors.Is(journalErr, ErrNoActivationJournal) {
			return ActivationState{}, "", journalErr
		}
	}
	if state.ActiveVersion == staged.Version {
		if _, err := s.verifyExisting(ctx, staged.Version, ""); err != nil {
			return ActivationState{}, "", fmt.Errorf("verify committed active %s: %w", staged.Version, err)
		}
		return state, staged.BinaryPath, nil
	}
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

	next, err := s.compareAndSwapActivation(
		ctx,
		state.Generation,
		staged.Version,
		allowUnusablePrevious,
	)
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

// RecoverActivationAttempt deterministically resumes one durable activation
// attempt. It never repeats a phase already committed in the journal: a
// candidate probe may be retried only while candidate_running is still the
// durable phase, and a CAS that already changed Active is repaired by advancing
// only the journal to committed.
func (s *VersionStore) RecoverActivationAttempt(ctx context.Context, attemptID string) (ActivationState, string, error) {
	if s == nil {
		return ActivationState{}, "", fmt.Errorf("version store is required")
	}
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return ActivationState{}, "", fmt.Errorf("activation recovery attempt_id is required")
	}
	attempt, err := s.ReadActivationJournal()
	if err != nil {
		return ActivationState{}, "", err
	}
	if attempt.AttemptID != attemptID {
		return ActivationState{}, "", fmt.Errorf("activation recovery identity mismatch: journal %s, want %s", attempt.AttemptID, attemptID)
	}
	if attempt.Phase == ActivationPhaseAborted {
		return ActivationState{}, "", fmt.Errorf("activation attempt %s is aborted: %s", attemptID, attempt.ErrorCode)
	}
	if attempt.BaseGeneration == ^uint64(0) {
		return ActivationState{}, "", fmt.Errorf("activation recovery generation overflow")
	}
	staged, err := s.verifyExisting(ctx, attempt.CandidateTag, "")
	if err != nil {
		return ActivationState{}, "", fmt.Errorf("verify activation recovery candidate: %w", err)
	}
	state, err := s.ReadActivationState()
	if err != nil {
		return ActivationState{}, "", err
	}

	// CAS is durable before the final journal transition. If Active already
	// names this attempt's exact candidate at base+1, no probe or CAS may run
	// again; repair only the lagging journal.
	if state.Generation == attempt.BaseGeneration+1 && state.ActiveVersion == attempt.CandidateTag {
		if attempt.Phase == ActivationPhaseCandidateHealthy {
			if _, err := s.AdvanceActivationPhase(attemptID, ActivationPhaseCommitted, ""); err != nil {
				return ActivationState{}, "", err
			}
		} else if attempt.Phase != ActivationPhaseCommitted {
			return ActivationState{}, "", fmt.Errorf("activation state committed while journal phase is %s", attempt.Phase)
		}
		return state, staged.BinaryPath, nil
	}
	if state.Generation != attempt.BaseGeneration || state.ActiveVersion != attempt.CommittedActive {
		return ActivationState{}, "", fmt.Errorf(
			"activation recovery state mismatch: generation=%d active=%s, want generation=%d active=%s",
			state.Generation, state.ActiveVersion, attempt.BaseGeneration, attempt.CommittedActive,
		)
	}
	if attempt.Phase == ActivationPhaseCommitted {
		return ActivationState{}, "", fmt.Errorf("activation journal is committed but Active did not advance")
	}

	abort := func(code string) {
		_, _ = s.AdvanceActivationPhase(attemptID, ActivationPhaseAborted, code)
	}
	if attempt.Phase == ActivationPhasePrepared || attempt.Phase == ActivationPhaseDraining {
		attempt, err = s.AdvanceActivationPhase(attemptID, ActivationPhaseCandidateRunning, "")
		if err != nil {
			return ActivationState{}, "", err
		}
	}
	if attempt.Phase == ActivationPhaseCandidateRunning {
		if err := probeStagedCandidate(ctx, staged.BinaryPath, staged.Version, attemptID); err != nil {
			abort("candidate_probe_failed")
			return ActivationState{}, "", fmt.Errorf("candidate probe: %w", err)
		}
		attempt, err = s.AdvanceActivationPhase(attemptID, ActivationPhaseCandidateHealthy, "")
		if err != nil {
			return ActivationState{}, "", err
		}
	}
	if attempt.Phase != ActivationPhaseCandidateHealthy {
		return ActivationState{}, "", fmt.Errorf("activation recovery cannot resume phase %s", attempt.Phase)
	}
	next, err := s.compareAndSwapActivation(ctx, attempt.BaseGeneration, staged.Version, false)
	if err != nil {
		abort("activation_cas_failed")
		return ActivationState{}, "", fmt.Errorf("CAS Active: %w", err)
	}
	if _, err := s.AdvanceActivationPhase(attemptID, ActivationPhaseCommitted, ""); err != nil {
		// Active already advanced. A later recovery will repair the journal from
		// the exact base+1 state rather than performing another mutation.
		return next, staged.BinaryPath, nil
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
	if state.PreviousVersionUnusable {
		return ActivationState{}, "", fmt.Errorf(
			"previous Active version %s is unusable and cannot be rolled back",
			state.PreviousVersion,
		)
	}
	return s.OfflineActivateStaged(ctx, state.PreviousVersion, attemptID)
}

// RestoreMachineUpgradeSource restores the exact source retained by one
// Machine Upgrade. Unlike a generic "previous" toggle, its accepted
// generation and source/target pair make repeated recovery idempotent and
// reject stale rollback markers.
func (s *VersionStore) RestoreMachineUpgradeSource(
	ctx context.Context,
	incumbentGeneration uint64,
	sourceVersion, targetVersion, attemptID string,
) (ActivationState, string, error) {
	if s == nil {
		return ActivationState{}, "", fmt.Errorf("version store is required")
	}
	source, err := normalizeVersionStoreTag(sourceVersion)
	if err != nil {
		return ActivationState{}, "", fmt.Errorf("rollback source: %w", err)
	}
	target, err := normalizeVersionStoreTag(targetVersion)
	if err != nil {
		return ActivationState{}, "", fmt.Errorf("rollback target: %w", err)
	}
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return ActivationState{}, "", fmt.Errorf("rollback attempt_id is required")
	}
	if source == target {
		return ActivationState{}, "", fmt.Errorf("rollback source and target must differ")
	}
	if incumbentGeneration > ^uint64(0)-2 {
		return ActivationState{}, "", fmt.Errorf("rollback generation overflow")
	}
	forwardGeneration := incumbentGeneration + 1
	restoredGeneration := incumbentGeneration + 2
	state, err := s.ReadActivationState()
	if err != nil {
		return ActivationState{}, "", err
	}

	if existing, journalErr := s.ReadActivationJournal(); journalErr == nil && existing.AttemptID == attemptID && existing.Phase != ActivationPhaseAborted {
		if existing.BaseGeneration != forwardGeneration || existing.CommittedActive != target || existing.CandidateTag != source {
			return ActivationState{}, "", fmt.Errorf("rollback activation journal identity mismatch")
		}
		state, path, err := s.RecoverActivationAttempt(ctx, attemptID)
		if err != nil {
			return ActivationState{}, "", err
		}
		if state.Generation != restoredGeneration || state.ActiveVersion != source || state.PreviousVersion != target {
			return ActivationState{}, "", fmt.Errorf("recovered rollback state does not match exact Machine Upgrade identity")
		}
		return state, path, nil
	} else if journalErr != nil && !errors.Is(journalErr, ErrNoActivationJournal) {
		return ActivationState{}, "", journalErr
	}

	if state.Generation == restoredGeneration && state.ActiveVersion == source && state.PreviousVersion == target {
		staged, err := s.verifyExisting(ctx, source, "")
		if err != nil {
			return ActivationState{}, "", fmt.Errorf("verify restored source: %w", err)
		}
		return state, staged.BinaryPath, nil
	}
	if state.Generation != forwardGeneration || state.ActiveVersion != target || state.PreviousVersion != source || state.PreviousVersionUnusable {
		return ActivationState{}, "", fmt.Errorf(
			"rollback state mismatch: generation=%d active=%s previous=%s unusable=%v, want generation=%d active=%s previous=%s",
			state.Generation, state.ActiveVersion, state.PreviousVersion, state.PreviousVersionUnusable,
			forwardGeneration, target, source,
		)
	}

	restored, path, err := s.OfflineActivateStaged(ctx, source, attemptID)
	if err != nil {
		return ActivationState{}, "", err
	}
	if restored.Generation != restoredGeneration || restored.ActiveVersion != source || restored.PreviousVersion != target {
		return ActivationState{}, "", fmt.Errorf("rollback result does not match exact Machine Upgrade identity")
	}
	return restored, path, nil
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
