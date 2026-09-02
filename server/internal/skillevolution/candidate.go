// SPDX-License-Identifier: Apache-2.0

package skillevolution

// SkillCandidate contract (spec §12.4): one proposal is one candidate
// targeting one skill (existing or new), one base artifact version, one
// reviewed diff. Accepted means the proposal decision only — grants,
// canaries, and scope widening live in separate approval/deployment
// records.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type SkillCandidateRecord struct {
	ContractKind           string          `json:"contract_kind"`
	SchemaVersion          int             `json:"schema_version"`
	WorkspaceID            string          `json:"workspace_id"`
	RunID                  string          `json:"run_id"`
	CandidateID            string          `json:"candidate_id"`
	TargetSkillID          string          `json:"target_skill_id"`
	NewSkillName           string          `json:"new_skill_name"`
	RequestedScope         string          `json:"requested_scope"`
	BaseArtifactHash       string          `json:"base_artifact_hash"`
	CandidateArtifactHash  string          `json:"candidate_artifact_hash"`
	ProposedDiffHash       string          `json:"proposed_diff_hash"`
	Status                 CandidateStatus `json:"status"`
	CurrentArtifactVersion int             `json:"current_artifact_version"`
	MotivatingPatterns     []string        `json:"motivating_pattern_ids"`
	ProposerActor          string          `json:"proposer_actor"`
	ProposerModelID        string          `json:"proposer_model_id"`
	ProposerPolicyVersion  string          `json:"proposer_policy_version"`
	CreatedAt              time.Time       `json:"created_at"`
}

// Validate enforces the candidate shape. A fresh proposal from a proposer
// must arrive as needs_review; the later lifecycle transitions belong to
// the evaluation/approval planes, not to the proposer.
func (c SkillCandidateRecord) Validate() error {
	if c.ContractKind != "candidate" || c.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=candidate and schema_version=1 are required", ErrInvalidContract)
	}
	for field, value := range map[string]string{
		"workspace_id": c.WorkspaceID, "run_id": c.RunID, "candidate_id": c.CandidateID,
	} {
		if err := validateOpaqueID(field, value); err != nil {
			return err
		}
	}
	// Exactly one of target skill or new skill name (DB shape check).
	hasTarget := c.TargetSkillID != ""
	hasName := c.NewSkillName != ""
	if hasTarget == hasName {
		return fmt.Errorf("%w: a candidate targets exactly one existing skill or one new skill name", ErrInvalidContract)
	}
	if hasTarget {
		if err := validateOpaqueID("target_skill_id", c.TargetSkillID); err != nil {
			return err
		}
	}
	switch c.RequestedScope {
	case "agent", "channel", "workspace":
	default:
		return fmt.Errorf("%w: requested_scope %q is invalid", ErrInvalidContract, c.RequestedScope)
	}
	if !validSHA256(c.BaseArtifactHash) || !validSHA256(c.CandidateArtifactHash) || !validSHA256(c.ProposedDiffHash) {
		return fmt.Errorf("%w: base/candidate/diff hashes must be sha256 hashes", ErrInvalidContract)
	}
	if !c.Status.Valid() {
		return fmt.Errorf("%w: candidate status %q is invalid", ErrInvalidContract, c.Status)
	}
	if c.CurrentArtifactVersion < 1 {
		return fmt.Errorf("%w: artifact version must be >= 1", ErrInvalidContract)
	}
	seen := map[string]bool{}
	for _, patternID := range c.MotivatingPatterns {
		if err := validateOpaqueID("motivating_pattern_id", patternID); err != nil {
			return err
		}
		if seen[patternID] {
			return fmt.Errorf("%w: motivating pattern %q appears twice", ErrInvalidContract, patternID)
		}
		seen[patternID] = true
	}
	if c.ProposerActor == "" || c.CreatedAt.IsZero() {
		return fmt.Errorf("%w: a candidate names its proposer and creation time", ErrInvalidContract)
	}
	return nil
}

// HashCandidateContent pins the proposal content the rejection memory
// compares: identity of what is proposed, against what base, with which
// evidence. Cosmetic wording never enters the hash.
func HashCandidateContent(c SkillCandidateRecord) string {
	payload := struct {
		Target          string   `json:"target"`
		Scope           string   `json:"scope"`
		Base            string   `json:"base"`
		Artifact        string   `json:"artifact"`
		Diff            string   `json:"diff"`
		Motivating      []string `json:"motivating"`
		ProposerModelID string   `json:"proposer_model_id"`
	}{
		Target:          map[bool]string{true: c.TargetSkillID, false: c.NewSkillName}[c.TargetSkillID != ""],
		Scope:           c.RequestedScope,
		Base:            c.BaseArtifactHash,
		Artifact:        c.CandidateArtifactHash,
		Diff:            c.ProposedDiffHash,
		Motivating:      c.MotivatingPatterns,
		ProposerModelID: c.ProposerModelID,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		// Plain value types: marshal-infallible by construction.
		return "sha256:unhashable"
	}
	return HashCanonicalPayload(payloadJSON)
}

// CandidateCoordinator drives the candidate lifecycle over the
// CandidateStore port, mirroring RunCoordinator: every transition is
// validated against the Phase 0 state machine and then CAS-applied, so a
// terminal candidate can never be revived even under racing writers (the
// DB terminal guard is the floor, this is the authority).
type CandidateCoordinator struct {
	store CandidateStore
}

func NewCandidateCoordinator(store CandidateStore) *CandidateCoordinator {
	return &CandidateCoordinator{store: store}
}

// AdmitCandidate inserts one fresh candidate. Admission is
// needs_review-only: the proposer plane produces candidates; every later
// lifecycle state belongs to the evaluation/approval planes. A replay of
// the identical candidate resolves to the stored record.
func (c *CandidateCoordinator) AdmitCandidate(ctx context.Context, record SkillCandidateRecord) (SkillCandidateRecord, error) {
	if err := record.Validate(); err != nil {
		return SkillCandidateRecord{}, err
	}
	if record.Status != CandidateStatusNeedsReview {
		return SkillCandidateRecord{}, fmt.Errorf(
			"%w: admission is needs_review only (got %s)", ErrInvalidContract, record.Status)
	}
	if err := c.store.InsertCandidate(ctx, record); err != nil {
		return SkillCandidateRecord{}, err
	}
	return c.store.GetCandidate(ctx, record.WorkspaceID, record.CandidateID)
}

// TransitionCandidate moves a candidate to next after validating the
// state machine. A same-status call is an idempotent no-op; an illegal
// edge fails closed with ErrLedgerConflict wrapping the reason; a CAS
// miss surfaces ErrLedgerConflict untouched.
func (c *CandidateCoordinator) TransitionCandidate(
	ctx context.Context, workspaceID, candidateID string, next CandidateStatus,
) (SkillCandidateRecord, error) {
	current, err := c.store.GetCandidate(ctx, workspaceID, candidateID)
	if err != nil {
		return SkillCandidateRecord{}, err
	}
	if current.Status == next {
		return current, nil
	}
	if !current.Status.CanTransition(next) {
		return SkillCandidateRecord{}, fmt.Errorf("%w: candidate %s cannot move %s -> %s",
			ErrLedgerConflict, candidateID, current.Status, next)
	}
	if err := c.store.TransitionCandidateStatus(ctx, workspaceID, candidateID, current.Status, next); err != nil {
		return SkillCandidateRecord{}, err
	}
	return c.store.GetCandidate(ctx, workspaceID, candidateID)
}
