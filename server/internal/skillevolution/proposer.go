// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Skill Proposer mechanics (spec §12.5, plan Slice 3.2): one atomic
// submission per run (a single candidate or no_action), rejection memory
// that deduplicates unchanged fingerprints (wording never bypasses it),
// and an exploration budget that stops at checkpoint/no_action without
// ever touching the hard gates.

import (
	"fmt"
	"sort"
	"time"
)

// ProposalSubmission is atomic: exactly one candidate proposal OR one
// no_action with a reason. Anything else is refused.
type ProposalSubmission struct {
	Candidate      *SkillCandidateRecord
	NoActionReason string
}

func (s ProposalSubmission) Validate() error {
	hasCandidate := s.Candidate != nil
	hasNoAction := s.NoActionReason != ""
	if hasCandidate == hasNoAction {
		return fmt.Errorf("%w: a submission is exactly one candidate or one no_action", ErrInvalidContract)
	}
	if hasCandidate {
		return s.Candidate.Validate()
	}
	return nil
}

// SubmitCandidateProposal checks the proposer capability and validates
// the atomic submission. A fresh candidate must arrive as needs_review —
// the proposer never stamps any later lifecycle state.
func SubmitCandidateProposal(capability RoleCapability, submission ProposalSubmission) (*SkillCandidateRecord, error) {
	if !capability.CanWrite(WriteCandidateProposal) {
		return nil, fmt.Errorf("%w: role %q cannot write candidate proposals",
			ErrInvalidContract, capability.Role)
	}
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	if submission.Candidate != nil && submission.Candidate.Status != CandidateStatusNeedsReview {
		return nil, fmt.Errorf("%w: a proposer submits needs_review candidates only (got %s)",
			ErrInvalidContract, submission.Candidate.Status)
	}
	return submission.Candidate, nil
}

// ResubmissionScreenOutcome decides whether a new evaluation run may
// start against the rejection memory.
type ResubmissionScreenOutcome string

const (
	ScreenAllowed      ResubmissionScreenOutcome = "allowed"
	ScreenDeduplicated ResubmissionScreenOutcome = "deduplicated"
	ScreenWordingOnly  ResubmissionScreenOutcome = "wording_only"
)

// RejectedProposalMemory is the durable memory of one rejected proposal
// fingerprint. The hashes it carries are exactly the facets a material
// change can touch: evidence set, environment, base artifact, and the
// proposal content itself.
type RejectedProposalMemory struct {
	Fingerprint      string
	EvidenceHash     string
	EnvironmentKey   string
	BaseArtifactHash string
	ProposalHash     string
	RejectedAt       time.Time
	Reason           string
}

func (m RejectedProposalMemory) Validate() error {
	if m.Fingerprint == "" || !validSHA256(m.EvidenceHash) || !validSHA256(m.ProposalHash) {
		return fmt.Errorf("%w: rejection memory carries fingerprint and content hashes", ErrInvalidContract)
	}
	if m.RejectedAt.IsZero() || m.Reason == "" {
		return fmt.Errorf("%w: rejection memory carries its time and reason", ErrInvalidContract)
	}
	return nil
}

// ScreenResubmission applies the spec rule: a rejected fingerprint is
// deduplicated by default; only a material change (evidence,
// environment, base artifact, or proposal content) may open a new
// evaluation run, and it must be explained. Re-wording alone never
// bypasses the rejection history.
func ScreenResubmission(
	history []RejectedProposalMemory,
	fingerprint string,
	facts ResubmissionFacts,
	changeStatement string,
) (ResubmissionScreenOutcome, error) {
	if fingerprint == "" {
		return "", fmt.Errorf("%w: screening needs the proposal fingerprint", ErrInvalidContract)
	}
	if err := facts.Validate(); err != nil {
		return "", err
	}
	var latest *RejectedProposalMemory
	for i := range history {
		if err := history[i].Validate(); err != nil {
			return "", err
		}
		if history[i].Fingerprint != fingerprint {
			continue
		}
		if latest == nil || history[i].RejectedAt.After(latest.RejectedAt) {
			latest = &history[i]
		}
	}
	if latest == nil {
		return ScreenAllowed, nil
	}
	materialChange := facts.EvidenceHash != latest.EvidenceHash ||
		facts.EnvironmentKey != latest.EnvironmentKey ||
		facts.BaseArtifactHash != latest.BaseArtifactHash ||
		facts.ProposalHash != latest.ProposalHash
	switch {
	case !materialChange && changeStatement != "":
		return ScreenWordingOnly, nil
	case !materialChange:
		return ScreenDeduplicated, nil
	case changeStatement == "":
		return "", fmt.Errorf("%w: a materially changed resubmission must explain the change", ErrInvalidContract)
	default:
		return ScreenAllowed, nil
	}
}

// ResubmissionFacts are the current submission's comparable facets.
type ResubmissionFacts struct {
	EvidenceHash     string
	EnvironmentKey   string
	BaseArtifactHash string
	ProposalHash     string
}

func (f ResubmissionFacts) Validate() error {
	if !validSHA256(f.EvidenceHash) || !validSHA256(f.BaseArtifactHash) || !validSHA256(f.ProposalHash) {
		return fmt.Errorf("%w: screening facts carry content hashes", ErrInvalidContract)
	}
	return nil
}

// EvidenceSetHash derives the evidence facet from the motivating pattern
// ids (order-insensitive): the evidence behind a proposal is what it
// learned from, not how it is worded.
func EvidenceSetHash(motivatingPatternIDs []string) string {
	sorted := append([]string(nil), motivatingPatternIDs...)
	sort.Strings(sorted)
	hash := HashCanonicalPayload([]byte(fmt.Sprintf("%q", sorted)))
	return hash
}

// ExplorationBudget bounds a proposer run's exploration. Budgets may only
// shrink sampling and navigation — never the hard gates or minimum
// validation, which live in the evaluation plane and are structurally not
// part of this type.
type ExplorationBudget struct {
	MaxToolSteps     int
	MaxEvidenceBytes int64
	MaxTokens        int64
}

func DefaultExplorationBudget() ExplorationBudget {
	return ExplorationBudget{MaxToolSteps: 24, MaxEvidenceBytes: 256 * 1024, MaxTokens: 65536}
}

func (b ExplorationBudget) Validate() error {
	if b.MaxToolSteps <= 0 || b.MaxEvidenceBytes <= 0 || b.MaxTokens <= 0 {
		return fmt.Errorf("%w: budgets are positive limits", ErrInvalidContract)
	}
	return nil
}

// BudgetState is the accumulated spend of one proposer run.
type BudgetState struct {
	ToolSteps     int
	EvidenceBytes int64
	Tokens        int64
}

// Exceeded reports whether the state left the budget envelope.
func (s BudgetState) Exceeded(budget ExplorationBudget) bool {
	return s.ToolSteps > budget.MaxToolSteps ||
		s.EvidenceBytes > budget.MaxEvidenceBytes ||
		s.Tokens > budget.MaxTokens
}

// BudgetCheckpoint is the resume state an interrupted exploration
// persists. It carries what was already read so the next attempt starts
// from the checkpoint instead of re-spending budget; it deliberately
// carries no partial candidate — a candidate is only ever submitted
// whole through the atomic path.
type BudgetCheckpoint struct {
	RunID          string
	ToolStepsUsed  int
	EvidenceRead   []string
	StoppedBecause string
	RecordedAt     time.Time
}

// StopForBudget converts an exhausted budget into the mandated response:
// a no_action submission plus a resumable checkpoint. Hard gates are not
// consulted here and cannot be reduced by this path.
func StopForBudget(runID string, state BudgetState, budget ExplorationBudget, checkpointEvidence []string, at time.Time) (ProposalSubmission, BudgetCheckpoint, error) {
	if err := budget.Validate(); err != nil {
		return ProposalSubmission{}, BudgetCheckpoint{}, err
	}
	if !state.Exceeded(budget) {
		return ProposalSubmission{}, BudgetCheckpoint{}, fmt.Errorf(
			"%w: stopping for budget requires an exceeded budget", ErrInvalidContract)
	}
	if runID == "" || at.IsZero() {
		return ProposalSubmission{}, BudgetCheckpoint{}, fmt.Errorf("%w: a checkpoint names its run and time", ErrInvalidContract)
	}
	reason := fmt.Sprintf("exploration budget exhausted (steps %d/%d, evidence %d/%d bytes, tokens %d/%d): checkpoint saved, hard gates untouched",
		state.ToolSteps, budget.MaxToolSteps, state.EvidenceBytes, budget.MaxEvidenceBytes, state.Tokens, budget.MaxTokens)
	return ProposalSubmission{NoActionReason: reason},
		BudgetCheckpoint{
			RunID: runID, ToolStepsUsed: state.ToolSteps, EvidenceRead: checkpointEvidence,
			StoppedBecause: "budget_exhausted", RecordedAt: at,
		}, nil
}
