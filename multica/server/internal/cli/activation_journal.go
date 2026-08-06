package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Activation journal file under the VersionStore root. Durable attempt state for
// two-phase prepare → candidate → CAS (design §3.3). Supervisor is sole writer.
const activationJournalName = "activation-attempt.json"

// ActivationAttemptPhase is the exact phase set for an in-flight cutover.
type ActivationAttemptPhase string

const (
	ActivationPhasePrepared         ActivationAttemptPhase = "prepared"
	ActivationPhaseDraining         ActivationAttemptPhase = "draining"
	ActivationPhaseCandidateRunning ActivationAttemptPhase = "candidate_running"
	ActivationPhaseCandidateHealthy ActivationAttemptPhase = "candidate_healthy"
	ActivationPhaseCommitted        ActivationAttemptPhase = "committed"
	ActivationPhaseAborted          ActivationAttemptPhase = "aborted"
)

// NonTerminalActivationPhases are phases that require a held claim barrier.
var NonTerminalActivationPhases = []ActivationAttemptPhase{
	ActivationPhasePrepared,
	ActivationPhaseDraining,
	ActivationPhaseCandidateRunning,
	ActivationPhaseCandidateHealthy,
}

// ActivationAttempt is the durable journal for one activation cutover.
type ActivationAttempt struct {
	SchemaVersion   int                    `json:"schema_version"`
	AttemptID       string                 `json:"attempt_id"`
	BaseGeneration  uint64                 `json:"base_generation"`
	CommittedActive string                 `json:"committed_active"`
	CandidateTag    string                 `json:"candidate_tag"`
	Phase           ActivationAttemptPhase `json:"phase"`
	ErrorCode       string                 `json:"error_code,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

var (
	ErrNoActivationJournal      = errors.New("no activation journal")
	ErrInvalidActivationJournal = errors.New("invalid activation journal")
	ErrActivationPhaseOrder     = errors.New("invalid activation phase transition")
)

func (s *VersionStore) activationJournalPath() string {
	return filepath.Join(s.root, activationJournalName)
}

// ReadActivationJournal returns the current journal, or ErrNoActivationJournal
// when the file is absent.
func (s *VersionStore) ReadActivationJournal() (ActivationAttempt, error) {
	data, err := os.ReadFile(s.activationJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return ActivationAttempt{}, ErrNoActivationJournal
	}
	if err != nil {
		return ActivationAttempt{}, fmt.Errorf("read activation journal: %w", err)
	}
	var attempt ActivationAttempt
	if err := json.Unmarshal(data, &attempt); err != nil {
		return ActivationAttempt{}, fmt.Errorf("%w: decode: %v", ErrInvalidActivationJournal, err)
	}
	if err := validateActivationAttempt(attempt); err != nil {
		return ActivationAttempt{}, err
	}
	return attempt, nil
}

// PrepareActivationAttempt writes a new journal in phase prepared. Fails if a
// non-terminal journal already exists (at most one in-flight attempt).
func (s *VersionStore) PrepareActivationAttempt(
	attemptID string,
	baseGeneration uint64,
	committedActive string,
	candidateTag string,
) (ActivationAttempt, error) {
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return ActivationAttempt{}, errors.New("attempt_id is required")
	}
	candidate, err := normalizeVersionStoreTag(candidateTag)
	if err != nil {
		return ActivationAttempt{}, fmt.Errorf("candidate_tag: %w", err)
	}
	committed := ""
	if committedActive != "" {
		committed, err = normalizeVersionStoreTag(committedActive)
		if err != nil {
			return ActivationAttempt{}, fmt.Errorf("committed_active: %w", err)
		}
	}
	if committed == candidate {
		return ActivationAttempt{}, errors.New("candidate_tag must differ from committed_active")
	}

	if existing, err := s.ReadActivationJournal(); err == nil {
		if isNonTerminalActivationPhase(existing.Phase) {
			return ActivationAttempt{}, fmt.Errorf(
				"activation attempt already in progress: %s phase=%s",
				existing.AttemptID,
				existing.Phase,
			)
		}
		// Terminal journals may be overwritten by a new prepare.
	} else if !errors.Is(err, ErrNoActivationJournal) {
		return ActivationAttempt{}, err
	}

	if _, err := s.verifyExisting(context.Background(), candidate, ""); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ActivationAttempt{}, fmt.Errorf("candidate version %s is not staged", candidate)
		}
		return ActivationAttempt{}, err
	}

	next := ActivationAttempt{
		SchemaVersion:   versionStoreSchemaVersion,
		AttemptID:       attemptID,
		BaseGeneration:  baseGeneration,
		CommittedActive: committed,
		CandidateTag:    candidate,
		Phase:           ActivationPhasePrepared,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := s.writeActivationJournal(next); err != nil {
		return ActivationAttempt{}, err
	}
	return next, nil
}

// AdvanceActivationPhase fsyncs the journal to nextPhase with optional error_code
// (used on abort). Enforces the allowed transition graph.
func (s *VersionStore) AdvanceActivationPhase(
	attemptID string,
	nextPhase ActivationAttemptPhase,
	errorCode string,
) (ActivationAttempt, error) {
	current, err := s.ReadActivationJournal()
	if err != nil {
		return ActivationAttempt{}, err
	}
	if current.AttemptID != attemptID {
		return ActivationAttempt{}, fmt.Errorf(
			"activation attempt_id mismatch: journal %s, want %s",
			current.AttemptID,
			attemptID,
		)
	}
	if !activationPhaseTransitionAllowed(current.Phase, nextPhase) {
		return ActivationAttempt{}, fmt.Errorf(
			"%w: %s → %s",
			ErrActivationPhaseOrder,
			current.Phase,
			nextPhase,
		)
	}
	current.Phase = nextPhase
	current.ErrorCode = errorCode
	current.UpdatedAt = time.Now().UTC()
	if err := s.writeActivationJournal(current); err != nil {
		return ActivationAttempt{}, err
	}
	return current, nil
}

// ClearActivationJournal removes a terminal journal. Non-terminal journals refuse.
func (s *VersionStore) ClearActivationJournal() error {
	current, err := s.ReadActivationJournal()
	if errors.Is(err, ErrNoActivationJournal) {
		return nil
	}
	if err != nil {
		return err
	}
	if isNonTerminalActivationPhase(current.Phase) {
		return fmt.Errorf(
			"refuse to clear non-terminal activation journal phase=%s attempt=%s",
			current.Phase,
			current.AttemptID,
		)
	}
	if err := os.Remove(s.activationJournalPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove activation journal: %w", err)
	}
	return syncDirPath(s.root)
}

func (s *VersionStore) writeActivationJournal(attempt ActivationAttempt) error {
	if err := validateActivationAttempt(attempt); err != nil {
		return err
	}
	data, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode activation journal: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.root, ".activation-attempt-*.tmp")
	if err != nil {
		return fmt.Errorf("create activation journal temp: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod activation journal temp: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write activation journal temp: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync activation journal temp: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close activation journal temp: %w", err)
	}
	if err := replaceFileAtomic(tempPath, s.activationJournalPath()); err != nil {
		return fmt.Errorf("replace activation journal: %w", err)
	}
	cleanup = false
	if err := syncDirPath(s.root); err != nil {
		return fmt.Errorf("sync version store root: %w", err)
	}
	return nil
}

func validateActivationAttempt(a ActivationAttempt) error {
	if a.SchemaVersion != versionStoreSchemaVersion {
		return fmt.Errorf("%w: unsupported schema %d", ErrInvalidActivationJournal, a.SchemaVersion)
	}
	if strings.TrimSpace(a.AttemptID) == "" {
		return fmt.Errorf("%w: missing attempt_id", ErrInvalidActivationJournal)
	}
	if a.CandidateTag == "" {
		return fmt.Errorf("%w: missing candidate_tag", ErrInvalidActivationJournal)
	}
	if _, err := normalizeVersionStoreTag(a.CandidateTag); err != nil {
		return fmt.Errorf("%w: candidate_tag: %v", ErrInvalidActivationJournal, err)
	}
	if a.CommittedActive != "" {
		if _, err := normalizeVersionStoreTag(a.CommittedActive); err != nil {
			return fmt.Errorf("%w: committed_active: %v", ErrInvalidActivationJournal, err)
		}
	}
	if !isKnownActivationPhase(a.Phase) {
		return fmt.Errorf("%w: unknown phase %q", ErrInvalidActivationJournal, a.Phase)
	}
	if a.Phase == ActivationPhaseAborted && a.ErrorCode == "" {
		// allow empty on legacy; preferred callers set drain_timeout etc.
	}
	return nil
}

func isKnownActivationPhase(p ActivationAttemptPhase) bool {
	switch p {
	case ActivationPhasePrepared,
		ActivationPhaseDraining,
		ActivationPhaseCandidateRunning,
		ActivationPhaseCandidateHealthy,
		ActivationPhaseCommitted,
		ActivationPhaseAborted:
		return true
	default:
		return false
	}
}

func isNonTerminalActivationPhase(p ActivationAttemptPhase) bool {
	switch p {
	case ActivationPhasePrepared,
		ActivationPhaseDraining,
		ActivationPhaseCandidateRunning,
		ActivationPhaseCandidateHealthy:
		return true
	default:
		return false
	}
}

// activationPhaseTransitionAllowed encodes the exact graph from design §3.3.
// Terminal phases only accept identity (no-op not used) — advances from terminal
// are rejected so a new Prepare must start a fresh attempt.
func activationPhaseTransitionAllowed(from, to ActivationAttemptPhase) bool {
	if from == to {
		return false
	}
	switch from {
	case ActivationPhasePrepared:
		return to == ActivationPhaseDraining ||
			to == ActivationPhaseCandidateRunning ||
			to == ActivationPhaseAborted
	case ActivationPhaseDraining:
		return to == ActivationPhaseCandidateRunning ||
			to == ActivationPhaseAborted
	case ActivationPhaseCandidateRunning:
		return to == ActivationPhaseCandidateHealthy ||
			to == ActivationPhaseAborted
	case ActivationPhaseCandidateHealthy:
		return to == ActivationPhaseCommitted ||
			to == ActivationPhaseAborted
	default:
		return false
	}
}


