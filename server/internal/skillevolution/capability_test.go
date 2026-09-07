// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func maintainerCapability(t *testing.T) RoleCapability {
	t.Helper()
	capability, err := CapabilityForRole(EvolutionRoleMaintainer, "service:skill-maintainer")
	require.NoError(t, err)
	return capability
}

func proposerCapability(t *testing.T) RoleCapability {
	t.Helper()
	capability, err := CapabilityForRole(EvolutionRoleProposer, "service:skill-proposer")
	require.NoError(t, err)
	return capability
}

// The two roles carry independent identities and mutually exclusive write
// surfaces, and the ungrantable reads stay ungrantable even when forged
// into the map.
func TestEvolutionRoleCapabilitiesAreNarrowAndSeparate(t *testing.T) {
	maintainer := maintainerCapability(t)
	proposer := proposerCapability(t)

	assert.NotEqual(t, maintainer.ServiceIdentity, proposer.ServiceIdentity,
		"maintainer and proposer are separate principals even over one model")

	// Maintainer writes pattern proposals only.
	assert.True(t, maintainer.CanWrite(WritePatternProposal))
	assert.False(t, maintainer.CanWrite(WriteCandidateProposal))
	// Proposer writes candidate proposals only.
	assert.True(t, proposer.CanWrite(WriteCandidateProposal))
	assert.False(t, proposer.CanWrite(WritePatternProposal))

	// The proposer's navigation surface: index, rejections, patterns,
	// evidence samples.
	for _, surface := range []EvolutionReadSurface{
		ReadSkillIndex, ReadPatternIndex, ReadHistoricalOutcome, ReadRejectedHistory, ReadOutcomeSummaries, ReadEvidenceSample,
	} {
		assert.True(t, proposer.CanRead(surface), "proposer reads %s", surface)
	}

	// Hidden validation answers and provider thinking are never readable
	// — by either role, even forged into the maps.
	for _, capability := range []RoleCapability{maintainer, proposer} {
		assert.False(t, capability.CanRead(ReadHiddenValidation))
		assert.False(t, capability.CanRead(ReadProviderThinking))
	}
	forged := proposerCapability(t)
	forged.Reads[ReadProviderThinking] = true
	forged.Reads[ReadHiddenValidation] = true
	assert.False(t, forged.CanRead(ReadProviderThinking), "the ungrantable reads refuse forged grants")
	assert.False(t, forged.CanRead(ReadHiddenValidation))

	// The maintainer has no candidate navigation surface.
	assert.False(t, maintainer.CanRead(ReadSkillIndex))
	assert.False(t, maintainer.CanRead(ReadRejectedHistory))

	// Roles and identities are mandatory.
	_, err := CapabilityForRole(EvolutionRole("evaluator"), "service:x")
	assert.ErrorIs(t, err, ErrInvalidContract)
	_, err = CapabilityForRole(EvolutionRoleProposer, "")
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// The maintainer can only ever hand in pattern proposals; the draft is
// tentative by construction.
func TestMaintainerWritesPatternProposalsOnly(t *testing.T) {
	maintainer := maintainerCapability(t)
	proposer := proposerCapability(t)

	draft := PatternDraftInput{
		PatternID:         "pattern-" + uuid.NewString()[:8],
		WorkspaceID:       uuid.NewString(),
		EvolutionKey:      "agent-1:spreadsheet:env-3",
		Kind:              PatternKindFailure,
		Problem:           "Sheet export omits hidden rows",
		Applicability:     "spreadsheet export tasks",
		RootCauseSummary:  "visible-range read",
		RecommendedAction: "iterate the full row set",
		PositiveEvidence: []SkillEvolutionRef{
			{Kind: RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: "workspace-1"},
		},
		CreatedByActor: "service:skill-maintainer",
		CreatedAt:      patternTestTime,
	}
	proposal := PatternProposal{Draft: draft, ComparedRunIDs: []string{uuid.NewString()}, SubmittedAt: patternTestTime}

	record, err := SubmitPatternProposal(maintainer, proposal)
	require.NoError(t, err)
	assert.Equal(t, PatternStatusTentative, record.Status)

	// A proposer cannot write pattern proposals.
	_, err = SubmitPatternProposal(proposer, proposal)
	require.ErrorIs(t, err, ErrInvalidContract)

	// Proposals without compared runs or evidence are refused.
	noRuns := proposal
	noRuns.ComparedRunIDs = nil
	_, err = SubmitPatternProposal(maintainer, noRuns)
	assert.ErrorIs(t, err, ErrInvalidContract)

	noEvidence := proposal
	noEvidence.Draft.PositiveEvidence = nil
	noEvidence.Draft.NegativeEvidence = nil
	_, err = SubmitPatternProposal(maintainer, noEvidence)
	assert.ErrorIs(t, err, ErrInvalidContract)
}

func validCandidateRecord() SkillCandidateRecord {
	return SkillCandidateRecord{
		ContractKind:           "candidate",
		SchemaVersion:          1,
		WorkspaceID:            uuid.NewString(),
		RunID:                  uuid.NewString(),
		CandidateID:            "candidate-" + uuid.NewString()[:8],
		NewSkillName:           "spreadsheet-hidden-row-export",
		RequestedScope:         "agent",
		BaseArtifactHash:       testHash,
		CandidateArtifactHash:  testHash,
		ProposedDiffHash:       testHash,
		Status:                 CandidateStatusNeedsReview,
		CurrentArtifactVersion: 1,
		MotivatingPatterns:     []string{"pattern-1"},
		ProposerActor:          "service:skill-proposer",
		ProposerModelID:        "model-x",
		ProposerPolicyVersion:  "proposer-policy-1",
		CreatedAt:              patternTestTime,
	}
}

// The proposer submits exactly one atomic candidate or no_action — never
// both, never neither, never a non-fresh lifecycle state.
func TestProposerSubmitsOneAtomicCandidateOrNoAction(t *testing.T) {
	proposer := proposerCapability(t)
	maintainer := maintainerCapability(t)

	candidate := validCandidateRecord()
	submitted, err := SubmitCandidateProposal(proposer, ProposalSubmission{Candidate: &candidate})
	require.NoError(t, err)
	assert.Equal(t, CandidateStatusNeedsReview, submitted.Status)

	noAction, err := SubmitCandidateProposal(proposer, ProposalSubmission{NoActionReason: "no recurring pattern justifies a change"})
	require.NoError(t, err)
	require.Nil(t, noAction)

	// Both set and neither set are refused.
	both := ProposalSubmission{Candidate: &candidate, NoActionReason: "also"}
	assert.ErrorIs(t, both.Validate(), ErrInvalidContract)
	neither := ProposalSubmission{}
	assert.ErrorIs(t, neither.Validate(), ErrInvalidContract)

	// The maintainer cannot write candidates at all.
	_, err = SubmitCandidateProposal(maintainer, ProposalSubmission{Candidate: &candidate})
	require.ErrorIs(t, err, ErrInvalidContract)

	// The proposer never stamps lifecycle states beyond needs_review.
	premature := candidate
	premature.Status = CandidateStatusAccepted
	_, err = SubmitCandidateProposal(proposer, ProposalSubmission{Candidate: &premature})
	require.ErrorIs(t, err, ErrInvalidContract)

	// Candidate shape: exactly one of target skill or new name.
	bothTargets := candidate
	bothTargets.TargetSkillID = uuid.NewString()
	assert.ErrorIs(t, bothTargets.Validate(), ErrInvalidContract)
}

// Rejection memory deduplicates unchanged fingerprints; wording alone
// never bypasses it; a material change must be explained.
func TestRejectedFingerprintDedupRequiresMaterialChange(t *testing.T) {
	candidate := validCandidateRecord()
	facts := ResubmissionFacts{
		EvidenceHash:     EvidenceSetHash(candidate.MotivatingPatterns),
		EnvironmentKey:   "env-3",
		BaseArtifactHash: candidate.BaseArtifactHash,
		ProposalHash:     HashCandidateContent(candidate),
	}
	fingerprint := "fingerprint-1"
	history := []RejectedProposalMemory{{
		Fingerprint: fingerprint, EvidenceHash: facts.EvidenceHash,
		EnvironmentKey: facts.EnvironmentKey, BaseArtifactHash: facts.BaseArtifactHash,
		ProposalHash: facts.ProposalHash, RejectedAt: patternTestTime, Reason: "evaluation failed hard gate",
	}}

	// A different fingerprint is unrestricted.
	outcome, err := ScreenResubmission(history, "fingerprint-2", facts, "")
	require.NoError(t, err)
	assert.Equal(t, ScreenAllowed, outcome)

	// Same fingerprint, nothing changed: deduplicated.
	outcome, err = ScreenResubmission(history, fingerprint, facts, "")
	require.NoError(t, err)
	assert.Equal(t, ScreenDeduplicated, outcome)

	// Same fingerprint, nothing changed, new wording: still refused.
	outcome, err = ScreenResubmission(history, fingerprint, facts, "we rephrased the instructions to be friendlier")
	require.NoError(t, err)
	assert.Equal(t, ScreenWordingOnly, outcome, "re-wording cannot bypass rejection history")

	// Material change without an explanation is an error, not an open.
	changedFacts := facts
	changedFacts.BaseArtifactHash = "sha256:" + strings.Repeat("b", 64)
	_, err = ScreenResubmission(history, fingerprint, changedFacts, "")
	assert.ErrorIs(t, err, ErrInvalidContract)

	// Material change, explained: allowed.
	outcome, err = ScreenResubmission(history, fingerprint, changedFacts, "base skill received the v2 artifact; evidence re-run on new lineage")
	require.NoError(t, err)
	assert.Equal(t, ScreenAllowed, outcome)

	// Every facet counts as material: environment and evidence too.
	envChanged := facts
	envChanged.EnvironmentKey = "env-4"
	outcome, err = ScreenResubmission(history, fingerprint, envChanged, "environment major version bumped")
	require.NoError(t, err)
	assert.Equal(t, ScreenAllowed, outcome)

	evidenceChanged := facts
	evidenceChanged.EvidenceHash = EvidenceSetHash([]string{"pattern-1", "pattern-2"})
	outcome, err = ScreenResubmission(history, fingerprint, evidenceChanged, "two new independent lineages confirmed the pattern")
	require.NoError(t, err)
	assert.Equal(t, ScreenAllowed, outcome)

	// Malformed memory is refused rather than screened permissively.
	broken := history
	broken[0].ProposalHash = "not-a-hash"
	_, err = ScreenResubmission(broken, fingerprint, facts, "")
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// Budget exhaustion ends in checkpoint + no_action; the hard gates are
// never part of the budget object and cannot be traded for exploration.
func TestProposerBudgetStopsAtCheckpointWithoutCuttingGates(t *testing.T) {
	budget := DefaultExplorationBudget()
	require.NoError(t, budget.Validate())

	// Within budget: no stop.
	within := BudgetState{ToolSteps: budget.MaxToolSteps, EvidenceBytes: 1, Tokens: 1}
	assert.False(t, within.Exceeded(budget))

	// Over budget: stop with checkpoint + no_action.
	over := BudgetState{ToolSteps: budget.MaxToolSteps + 1, EvidenceBytes: 1, Tokens: 1}
	require.True(t, over.Exceeded(budget))
	submission, checkpoint, err := StopForBudget("run-1", over, budget, []string{"pattern-1"}, patternTestTime)
	require.NoError(t, err)
	require.NotNil(t, submission.NoActionReason)
	require.Nil(t, submission.Candidate)
	require.NoError(t, submission.Validate(), "the budget stop is itself a valid atomic submission")
	assert.Contains(t, submission.NoActionReason, "budget exhausted")
	assert.Contains(t, submission.NoActionReason, "hard gates untouched")
	assert.Equal(t, "budget_exhausted", checkpoint.StoppedBecause)
	assert.Equal(t, []string{"pattern-1"}, checkpoint.EvidenceRead)

	// The no_action still flows through the capability-gated submit path.
	_, err = SubmitCandidateProposal(proposerCapability(t), submission)
	require.NoError(t, err)

	// Stopping without an exceeded budget is a contract violation.
	_, _, err = StopForBudget("run-1", within, budget, nil, patternTestTime)
	assert.ErrorIs(t, err, ErrInvalidContract)

	// Budgets are positive limits, and the type carries no gate fields:
	// exploration can never buy its way past validation. (Enforced
	// structurally — the compile-time surface of ExplorationBudget.)
	invalid := ExplorationBudget{}
	assert.ErrorIs(t, invalid.Validate(), ErrInvalidContract)
}
