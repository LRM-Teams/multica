// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var patternTestTime = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func patternDraftInput() PatternDraftInput {
	return PatternDraftInput{
		PatternID:         "pattern-" + uuid.NewString()[:8],
		WorkspaceID:       uuid.NewString(),
		EvolutionKey:      "agent-1:spreadsheet:env-3",
		Kind:              PatternKindFailure,
		Problem:           "Sheet export omits hidden rows",
		Applicability:     "spreadsheet export tasks with filtered rows",
		RootCauseSummary:  "export reads the visible range instead of the full row set",
		RecommendedAction: "iterate the full row set before formatting",
		TaskType:          "spreadsheet",
		EnvironmentKey:    "env-3",
		ToolCapabilityID:  "xlsx-writer",
		GeneratorVersion:  "maintainer-1",
		PolicyVersion:     DefaultPatternConsolidationPolicy().PolicyVersion,
		PositiveEvidence: []SkillEvolutionRef{
			{Kind: RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: "workspace-1"},
		},
		CreatedByActor: "maintainer:run-1",
		CreatedAt:      patternTestTime,
	}
}

func observation(polarity EvidencePolarity, lineage string) PatternEvidenceObservation {
	return PatternEvidenceObservation{
		Ref:            SkillEvolutionRef{Kind: RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: "workspace-1"},
		Polarity:       polarity,
		LineageID:      lineage,
		TaskType:       "spreadsheet",
		EnvironmentKey: "env-3",
		RecordedAt:     patternTestTime,
	}
}

// The first revision of a pattern is always tentative, no matter how much
// evidence the drafter claims (spec §12.4).
func TestPatternDraftIsAlwaysTentative(t *testing.T) {
	input := patternDraftInput()
	// A single draft may claim several evidence refs — still tentative.
	input.PositiveEvidence = append(input.PositiveEvidence,
		SkillEvolutionRef{Kind: RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: "workspace-1"})

	record, err := DraftTentativePattern(input)
	require.NoError(t, err)
	assert.Equal(t, PatternStatusTentative, record.Status)
	assert.Equal(t, int64(1), record.Revision)
	assert.NotEqual(t, "sha256:pending", record.ContentHash)
	require.NoError(t, record.Validate())
}

// Status upgrades follow the versioned policy: two independent lineages
// support, workbook copies never double-count, negative evidence blocks
// the upgrade and eventually contradicts or refutes.
func TestPatternStatusFollowsEvidencePolicy(t *testing.T) {
	policy := DefaultPatternConsolidationPolicy()
	require.NoError(t, policy.Validate())

	draft, err := DraftTentativePattern(patternDraftInput())
	require.NoError(t, err)

	// One lineage — even observed many times — stays tentative.
	one := []PatternEvidenceObservation{
		observation(EvidencePositive, "workbook-a"),
		observation(EvidencePositive, "workbook-a"), // copy/rename of the same workbook
		observation(EvidencePositive, "workbook-a"),
	}
	tally, err := TallyEvidence(one)
	require.NoError(t, err)
	assert.Equal(t, 1, tally.PositiveCount(), "workbook copies deduplicate to one vote")
	status, _ := policy.EvaluateStatus(tally, draft.PatternKind)
	assert.Equal(t, PatternStatusTentative, status)

	// A second independent lineage upgrades to supported.
	two := append(one, observation(EvidencePositive, "workbook-b"))
	tally, err = TallyEvidence(two)
	require.NoError(t, err)
	status, rationale := policy.EvaluateStatus(tally, draft.PatternKind)
	assert.Equal(t, PatternStatusSupported, status)
	assert.Contains(t, rationale, "2 independent positive")

	upgraded, rationale, err := ReevaluatePattern(draft, two, policy, "maintainer:run-2", patternTestTime)
	require.NoError(t, err)
	assert.Equal(t, PatternStatusSupported, upgraded.Status)
	assert.Equal(t, int64(2), upgraded.Revision)
	assert.Contains(t, rationale, "2 independent positive")

	// Unattributed evidence never counts as independent.
	unattributed := append(two, observation(EvidencePositive, ""))
	tally, err = TallyEvidence(unattributed)
	require.NoError(t, err)
	assert.Equal(t, 2, tally.PositiveCount())

	// Negative evidence blocks the upgrade of a fresh tentative pattern.
	blocked := append(one, observation(EvidenceNegative, "workbook-c"))
	tally, err = TallyEvidence(blocked)
	require.NoError(t, err)
	status, _ = policy.EvaluateStatus(tally, draft.PatternKind)
	assert.Equal(t, PatternStatusTentative, status, "negative evidence blocks the upgrade")

	// Positive + two independent negative lineages contradicts.
	contradicted := append(two,
		observation(EvidenceNegative, "workbook-c"),
		observation(EvidenceNegative, "workbook-d"))
	tally, err = TallyEvidence(contradicted)
	require.NoError(t, err)
	status, _ = policy.EvaluateStatus(tally, draft.PatternKind)
	assert.Equal(t, PatternStatusContradicted, status)
	demoted, _, err := ReevaluatePattern(upgraded, contradicted, policy, "maintainer:run-3", patternTestTime)
	require.NoError(t, err)
	assert.Equal(t, PatternStatusContradicted, demoted.Status)

	// Only negative evidence at threshold refutes; refutation is terminal
	// and cannot be resurrected on the same pattern id.
	refuting := []PatternEvidenceObservation{
		observation(EvidenceNegative, "workbook-c"),
		observation(EvidenceNegative, "workbook-d"),
	}
	tally, err = TallyEvidence(refuting)
	require.NoError(t, err)
	status, _ = policy.EvaluateStatus(tally, draft.PatternKind)
	assert.Equal(t, PatternStatusRefuted, status)

	refutedDraft := draft
	refutedDraft.Status = PatternStatusRefuted
	_, _, err = ReevaluatePattern(refutedDraft, two, policy, "maintainer:run-4", patternTestTime)
	require.ErrorIs(t, err, ErrInvalidContract, "terminal statuses never resurrect")
}

// The policy itself is data with hard floors: one lineage can never be
// configured to support a pattern.
func TestPatternPolicyRefusesSingleLineageSupport(t *testing.T) {
	loose := DefaultPatternConsolidationPolicy()
	loose.MinIndependentPositiveLineages = 1
	assert.ErrorIs(t, loose.Validate(), ErrInvalidContract,
		"a policy that lets one lineage support a pattern is invalid")
}

// Dedup recall is deterministic and stable under copy/rename/whitespace
// variants, and separate for different environments.
func TestPatternFingerprintRecall(t *testing.T) {
	input := PatternFingerprintInput{
		TaskType:       "spreadsheet",
		PatternKind:    PatternKindFailure,
		EnvironmentKey: "env-3",
		ToolCapability: "xlsx-writer",
		Problem:        "Sheet export  omits   hidden rows",
		Applicability:  "export tasks",
		RootCause:      "visible-range read",
	}

	fingerprint, err := PatternFingerprint(input)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(fingerprint, "sha256:"), fingerprint)

	// Case/whitespace variants fingerprint identically.
	variant := input
	variant.Problem = "  sheet EXPORT omits hidden rows "
	same, err := PatternFingerprint(variant)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, same)

	// A different environment is a different recall bucket.
	other := input
	other.EnvironmentKey = "env-4"
	different, err := PatternFingerprint(other)
	require.NoError(t, err)
	assert.NotEqual(t, fingerprint, different)

	// The coarse lineage scope ignores wording entirely.
	scopeA, err := PatternLineageScope(input)
	require.NoError(t, err)
	scopeB, err := PatternLineageScope(variant)
	require.NoError(t, err)
	assert.Equal(t, scopeA, scopeB)

	invalid := input
	invalid.PatternKind = PatternKind("lucky")
	_, err = PatternFingerprint(invalid)
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// Merges only ever happen inside one fingerprint bucket: agreeing
// semantics merge (reversibly), divergent root causes conflict without
// overwriting, and differing problems only link.
func TestPatternMergeNeverOverwritesConflicts(t *testing.T) {
	base := patternDraftInput()
	a, err := DraftTentativePattern(base)
	require.NoError(t, err)
	fingerprint, err := PatternFingerprint(PatternFingerprintInput{
		TaskType:       a.TaskType,
		PatternKind:    a.PatternKind,
		EnvironmentKey: a.EnvironmentKey,
		ToolCapability: a.ToolCapabilityID,
		Problem:        a.Problem,
		Applicability:  a.Applicability,
		RootCause:      a.RootCauseSummary,
	})
	require.NoError(t, err)

	// Same fingerprint, same semantics: merge, evidence unions (shared
	// refs deduplicate, distinct refs survive).
	bInput := base
	bInput.PatternID = "pattern-" + uuid.NewString()[:8]
	bInput.PositiveEvidence = append(bInput.PositiveEvidence,
		SkillEvolutionRef{Kind: RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: "workspace-1"})
	b, err := DraftTentativePattern(bInput)
	require.NoError(t, err)
	plan, err := PlanPatternMerge(a, b, fingerprint)
	require.NoError(t, err)
	assert.Equal(t, MergeActionMerge, plan.Action)
	assert.Equal(t, a.PatternID, plan.SurvivingPatternID)
	assert.Equal(t, b.PatternID, plan.AbsorbedPatternID)
	assert.Len(t, plan.MergedPositive, 2,
		"the union keeps both sides' distinct evidence and dedups the shared ref")
	assert.Equal(t, a.PositiveEvidence[0], plan.MergedPositive[0])

	// Same fingerprint and problem, divergent root cause: conflict, both
	// demoted, nothing overwritten.
	cInput := base
	cInput.PatternID = "pattern-" + uuid.NewString()[:8]
	cInput.RootCauseSummary = "formatting pass overwrites the row cursor"
	c, err := DraftTentativePattern(cInput)
	require.NoError(t, err)
	plan, err = PlanPatternMerge(a, c, fingerprint)
	require.NoError(t, err)
	assert.Equal(t, MergeActionConflict, plan.Action)
	assert.Equal(t, []string{c.PatternID}, plan.ConflictsWith)
	assert.True(t, plan.DemoteToTentative)
	assert.NotEmpty(t, plan.ApplicabilityNarrowing)
	assert.Contains(t, plan.Reason, "no overwrite")

	// Different problems in the same scope: link, never merge.
	dInput := base
	dInput.PatternID = "pattern-" + uuid.NewString()[:8]
	dInput.Problem = "Chart axis labels lose their locale format"
	d, err := DraftTentativePattern(dInput)
	require.NoError(t, err)
	plan, err = PlanPatternMerge(a, d, fingerprint)
	require.NoError(t, err)
	assert.Equal(t, MergeActionLink, plan.Action)
	assert.Empty(t, plan.MergedPositive)

	// No fingerprint, no merge: similarity alone never merges.
	_, err = PlanPatternMerge(a, b, "")
	assert.ErrorIs(t, err, ErrInvalidContract)

	// Terminal patterns never merge.
	terminal := a
	terminal.Status = PatternStatusRefuted
	_, err = PlanPatternMerge(terminal, b, fingerprint)
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// Content hashes pin semantics: equal meaning yields equal hashes, and
// status changes alone change the hash.
func TestPatternContentHashPinsSemantics(t *testing.T) {
	input := patternDraftInput()
	a, err := DraftTentativePattern(input)
	require.NoError(t, err)

	same, err := DraftTentativePattern(input)
	require.NoError(t, err)
	assert.Equal(t, a.ContentHash, same.ContentHash)

	policy := DefaultPatternConsolidationPolicy()
	upgraded, _, err := ReevaluatePattern(a, []PatternEvidenceObservation{
		observation(EvidencePositive, "workbook-a"),
		observation(EvidencePositive, "workbook-b"),
	}, policy, "maintainer:run-9", patternTestTime)
	require.NoError(t, err)
	assert.NotEqual(t, a.ContentHash, upgraded.ContentHash,
		"a status change is a content change")
}
