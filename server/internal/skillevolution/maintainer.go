// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Role capabilities for the evolution planes (spec §12.5/§12.7, plan
// Slice 3.2). A maintainer induces pattern proposals from compared
// trajectories; a proposer reads the index/rejection history/pattern
// surface and submits exactly one atomic candidate or no_action. Both
// roles carry an independent service identity even when the underlying
// model is the same, and neither role can ever read hidden validation
// answers or provider thinking — those surfaces are structurally absent
// from the capability maps, not merely switched off.

import (
	"fmt"
	"time"
)

// EvolutionRole names the two agentic roles of the evolution planes.
type EvolutionRole string

const (
	EvolutionRoleMaintainer EvolutionRole = "maintainer"
	EvolutionRoleProposer   EvolutionRole = "proposer"
)

func (r EvolutionRole) Valid() bool {
	return r == EvolutionRoleMaintainer || r == EvolutionRoleProposer
}

// EvolutionReadSurface is one narrow read grant. ReadHiddenValidation and
// ReadProviderThinking exist only so CanRead can refuse them by name.
type EvolutionReadSurface string

const (
	ReadSkillIndex        EvolutionReadSurface = "skill_index"
	ReadPatternIndex      EvolutionReadSurface = "pattern_index"
	ReadHistoricalOutcome EvolutionReadSurface = "historical_outcome"
	ReadRejectedHistory   EvolutionReadSurface = "rejected_history"
	ReadOutcomeSummaries  EvolutionReadSurface = "outcome_summaries"
	ReadObservableRun     EvolutionReadSurface = "observable_run"
	ReadEvidenceSample    EvolutionReadSurface = "evidence_sample"

	// ReadHiddenValidation and ReadProviderThinking are ungrantable: no
	// role, flag, or scope widening ever turns them on.
	ReadHiddenValidation EvolutionReadSurface = "hidden_validation"
	ReadProviderThinking EvolutionReadSurface = "provider_thinking"
)

// EvolutionWriteSurface is one narrow write grant.
type EvolutionWriteSurface string

const (
	WritePatternProposal   EvolutionWriteSurface = "pattern_proposal"
	WriteCandidateProposal EvolutionWriteSurface = "candidate_proposal"
)

// RoleCapability is the frozen tool surface of one role instance.
type RoleCapability struct {
	Role            EvolutionRole
	ServiceIdentity string
	Reads           map[EvolutionReadSurface]bool
	Writes          map[EvolutionWriteSurface]bool
}

// CapabilityForRole builds the narrow surface. The identity is mandatory
// and role-specific: "maintainer" and "proposer" are separate principals
// even over the same model.
func CapabilityForRole(role EvolutionRole, serviceIdentity string) (RoleCapability, error) {
	if !role.Valid() {
		return RoleCapability{}, fmt.Errorf("%w: unknown evolution role %q", ErrInvalidContract, role)
	}
	if serviceIdentity == "" {
		return RoleCapability{}, fmt.Errorf("%w: %s needs its own service identity", ErrInvalidContract, role)
	}
	capability := RoleCapability{
		Role:            role,
		ServiceIdentity: serviceIdentity,
		Reads:           map[EvolutionReadSurface]bool{},
		Writes:          map[EvolutionWriteSurface]bool{},
	}
	switch role {
	case EvolutionRoleMaintainer:
		// The maintainer compares success/failure trajectories and
		// outcome summaries to draft pattern proposals. It has no
		// candidate surface at all.
		capability.Reads[ReadPatternIndex] = true
		capability.Reads[ReadOutcomeSummaries] = true
		capability.Reads[ReadObservableRun] = true
		capability.Reads[ReadEvidenceSample] = true
		capability.Writes[WritePatternProposal] = true
	case EvolutionRoleProposer:
		// The proposer navigates the index, rejections, and patterns,
		// then submits one atomic candidate or no_action.
		capability.Reads[ReadSkillIndex] = true
		capability.Reads[ReadPatternIndex] = true
		capability.Reads[ReadHistoricalOutcome] = true
		capability.Reads[ReadRejectedHistory] = true
		capability.Reads[ReadOutcomeSummaries] = true
		capability.Reads[ReadEvidenceSample] = true
		capability.Writes[WriteCandidateProposal] = true
	}
	return capability, nil
}

// CanRead reports one read grant. The ungrantable surfaces answer false
// even if someone forged them into the map.
func (c RoleCapability) CanRead(surface EvolutionReadSurface) bool {
	switch surface {
	case ReadHiddenValidation, ReadProviderThinking:
		return false
	default:
		return c.Reads[surface]
	}
}

// CanWrite reports one write grant.
func (c RoleCapability) CanWrite(surface EvolutionWriteSurface) bool {
	return c.Writes[surface]
}

// PatternProposal is what a maintainer submits after comparing success
// and failure trajectories: a tentative pattern draft plus the runs it
// compared. The maintainer never writes ledger rows directly — the
// orchestrator applies proposals.
type PatternProposal struct {
	Draft          PatternDraftInput
	ComparedRunIDs []string
	SubmittedAt    time.Time
}

func (p PatternProposal) Validate() error {
	if len(p.ComparedRunIDs) == 0 {
		return fmt.Errorf("%w: a pattern proposal names the runs it compared", ErrInvalidContract)
	}
	for _, runID := range p.ComparedRunIDs {
		if err := validateOpaqueID("compared_run_id", runID); err != nil {
			return err
		}
	}
	if len(p.Draft.PositiveEvidence) == 0 && len(p.Draft.NegativeEvidence) == 0 {
		return fmt.Errorf("%w: a pattern proposal carries outcome-backed evidence", ErrInvalidContract)
	}
	if p.SubmittedAt.IsZero() {
		return fmt.Errorf("%w: a proposal carries its submission time", ErrInvalidContract)
	}
	return nil
}

// SubmitPatternProposal checks the maintainer capability and turns the
// proposal into a (tentative) pattern draft record. Capability refused →
// error; no silent fallback to an unprivileged write.
func SubmitPatternProposal(capability RoleCapability, proposal PatternProposal) (PatternRecord, error) {
	if !capability.CanWrite(WritePatternProposal) {
		return PatternRecord{}, fmt.Errorf("%w: role %q cannot write pattern proposals",
			ErrInvalidContract, capability.Role)
	}
	if err := proposal.Validate(); err != nil {
		return PatternRecord{}, err
	}
	return DraftTentativePattern(proposal.Draft)
}
