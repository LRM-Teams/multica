// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

var testTime = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func validPatternRecord() PatternRecord {
	return PatternRecord{
		ContractKind:      "pattern",
		SchemaVersion:     1,
		PatternID:         "pattern-1",
		Revision:          1,
		WorkspaceID:       "workspace-1",
		EvolutionKey:      "workspace-1|agent-1|spreadsheet|v1",
		PatternKind:       PatternKindSuccess,
		Status:            PatternStatusTentative,
		Problem:           "pivoted ranges lose formulas",
		Applicability:     "spreadsheet rewrite tasks",
		RootCauseSummary:  "agent rewrote cells instead of editing",
		RecommendedAction: "apply targeted cell edits",
		PositiveEvidence: []SkillEvolutionRef{{
			Kind: RefPattern, ID: "segment-1", WorkspaceID: "workspace-1",
		}},
		NegativeEvidence: []SkillEvolutionRef{{
			Kind: RefPattern, ID: "segment-2", WorkspaceID: "workspace-1",
		}},
		TaskType:         "spreadsheet",
		SourceModelID:    "model-a",
		TargetModelID:    "model-b",
		ProviderID:       "provider-1",
		ToolCapabilityID: "workbook",
		RuntimeID:        "runtime-1",
		EnvironmentKey:   "env-1",
		GeneratorVersion: "maintainer-1",
		PolicyVersion:    "policy-1",
		ContentHash:      testHash,
		CreatedByActor:   "pattern-maintainer",
		CreatedAt:        testTime,
		UpdatedByActor:   "pattern-maintainer",
		UpdatedAt:        testTime,
	}
}

func TestPatternRecordContract(t *testing.T) {
	pattern := validPatternRecord()
	require.NoError(t, pattern.Validate())

	badHash := validPatternRecord()
	badHash.ContentHash = "md5:zz"
	assert.ErrorIs(t, badHash.Validate(), ErrInvalidContract)

	supported := validPatternRecord()
	supported.Status = PatternStatusSupported
	supported.PositiveEvidence = nil
	assert.ErrorIs(t, supported.Validate(), ErrInvalidContract)

	dup := validPatternRecord()
	dup.PositiveEvidence = append(dup.PositiveEvidence, dup.PositiveEvidence[0])
	assert.ErrorIs(t, dup.Validate(), ErrInvalidContract)

	unknownKind := validPatternRecord()
	unknownKind.PatternKind = "lucky"
	assert.ErrorIs(t, unknownKind.Validate(), ErrInvalidContract)
}

func TestPatternStatusStateMachine(t *testing.T) {
	happyPath := []PatternStatus{
		PatternStatusTentative, PatternStatusSupported,
	}
	for i := 0; i+1 < len(happyPath); i++ {
		require.True(t, happyPath[i].CanTransition(happyPath[i+1]),
			"%s -> %s must be legal", happyPath[i], happyPath[i+1])
	}
	demote := []PatternStatus{PatternStatusSupported, PatternStatusTentative}
	require.True(t, demote[0].CanTransition(demote[1]))
	refute := []PatternStatus{PatternStatusSupported, PatternStatusRefuted}
	require.True(t, refute[0].CanTransition(refute[1]))
	assert.True(t, PatternStatusRefuted.Terminal())
	assert.True(t, PatternStatusStale.Terminal())
	assert.False(t, PatternStatusRefuted.CanTransition(PatternStatusTentative),
		"refuted recovery must be a new revision, not a status flip")
	assert.False(t, PatternStatusTentative.CanTransition(PatternStatusSupported) == false)
}

func TestCandidateStatusStateMachine(t *testing.T) {
	happyPath := []CandidateStatus{
		CandidateStatusNeedsReview, CandidateStatusShadow, CandidateStatusEvaluating,
		CandidateStatusAccepted,
	}
	for i := 0; i+1 < len(happyPath); i++ {
		require.True(t, happyPath[i].CanTransition(happyPath[i+1]),
			"%s -> %s must be legal", happyPath[i], happyPath[i+1])
	}
	assert.True(t, CandidateStatusAccepted.CanTransition(CandidateStatusSuperseded))
	assert.True(t, CandidateStatusEvaluating.CanTransition(CandidateStatusRejected))
	assert.True(t, CandidateStatusRejected.Terminal())
	assert.False(t, CandidateStatusRejected.CanTransition(CandidateStatusEvaluating),
		"a materially changed proposal is a new candidate, not a status flip")
	assert.False(t, CandidateStatusWithdrawn.CanTransition(CandidateStatusNeedsReview))
}

func TestEvolutionRunStateMachine(t *testing.T) {
	happyPath := []EvolutionRunStatus{
		EvolutionRunQueued,
		EvolutionRunSnapshotting,
		EvolutionRunConsolidatingPatterns,
		EvolutionRunProposingCandidate,
		EvolutionRunAwaitingReview,
		EvolutionRunEvaluating,
		EvolutionRunAwaitingApproval,
		EvolutionRunCompleted,
	}
	for i := 0; i+1 < len(happyPath); i++ {
		require.True(t, happyPath[i].CanTransition(happyPath[i+1]),
			"%s -> %s must be legal", happyPath[i], happyPath[i+1])
	}
	require.True(t, EvolutionRunSnapshotting.CanTransition(EvolutionRunNoAction))
	require.True(t, EvolutionRunAwaitingReview.CanTransition(EvolutionRunRejected))
	for _, active := range happyPath[:len(happyPath)-1] {
		require.True(t, active.CanTransition(EvolutionRunFenced),
			"safety fence must be reachable from %s", active)
		require.True(t, active.CanTransition(EvolutionRunCancelled))
		assert.False(t, active.Terminal())
	}
	assert.True(t, EvolutionRunCompleted.Terminal())
	assert.True(t, EvolutionRunNoAction.Terminal())
	assert.False(t, EvolutionRunCompleted.CanTransition(EvolutionRunQueued))
	assert.False(t, EvolutionRunQueued.CanTransition(EvolutionRunCompleted),
		"runs cannot skip the pipeline")
}

func validEvaluationRun() EvaluationRunRecord {
	return EvaluationRunRecord{
		ContractKind:          "evaluation_run",
		SchemaVersion:         1,
		EvaluationID:          "eval-1",
		WorkspaceID:           "workspace-1",
		CandidateID:           "candidate-1",
		ManifestID:            "manifest-1",
		ManifestVersion:       1,
		BaseArtifactHash:      testHash,
		CandidateArtifactHash: testHash,
		ManifestHash:          testHash,
		TargetAgentID:         "agent-1",
		TargetModelID:         "model-b",
		ProviderID:            "provider-1",
		ToolCapabilityID:      "tool-1",
		RuntimeID:             "runtime-1",
		EnvironmentKey:        "env-1",
		AssertionResults: []AssertionResult{
			{AssertionID: "assert-1", Result: AssertionPassed, EvidenceHash: testHash},
			{AssertionID: "assert-2", Result: AssertionNotRun, EvidenceHash: testHash},
		},
		Metrics:               json.RawMessage(`{"correctness":1}`),
		Contamination:         ContaminationClean,
		DecisionPolicyVersion: "policy-1",
		TerminalResult:        EvaluationPassed,
		TerminalReason:        "all required assertions passed",
		CreatedByActor:        "member:evaluator",
		CreatedAt:             testTime,
	}
}

func validAssertionManifest() AssertionManifest {
	return AssertionManifest{
		ContractKind:         "assertion_manifest",
		SchemaVersion:        1,
		ManifestID:           "manifest-1",
		Version:              1,
		WorkspaceID:          "workspace-1",
		ManifestHash:         testHash,
		DatasetIdentity:      "spreadsheet-export-v3",
		DatasetVersion:       "3.2.0",
		LineageSplit:         "holdout:2026-09",
		DomainProfile:        "spreadsheet",
		TaskSlices:           json.RawMessage(`["formula","style"]`),
		EvaluatorVersion:     "evaluator-1.4.0",
		ScorerVersion:        "scorer-1.1.0",
		EnvironmentKey:       "env-1",
		RequiredCapabilities: json.RawMessage(`["xlsx.read"]`),
		DataResidency:        "eu",
		Assertions: []AssertionSpec{
			{AssertionID: "assert-1", Kind: "value", OracleRefHash: testHash, Severity: "critical", Hard: true, Required: true, Tolerance: "0"},
			{AssertionID: "assert-2", Kind: "formula", OracleRefHash: testHash, Severity: "major", Hard: false, Required: false, Tolerance: "1e-9"},
		},
		CreatedByActor: "member:curator",
		CreatedAt:      testTime,
	}
}

func TestAssertionManifestContract(t *testing.T) {
	manifest := validAssertionManifest()
	require.NoError(t, manifest.Validate())

	duplicate := validAssertionManifest()
	duplicate.Assertions = append(duplicate.Assertions, duplicate.Assertions[0])
	assert.ErrorIs(t, duplicate.Validate(), ErrInvalidContract)

	badOracle := validAssertionManifest()
	badOracle.Assertions[0].OracleRefHash = "not-a-hash"
	assert.ErrorIs(t, badOracle.Validate(), ErrInvalidContract)

	zeroVersion := validAssertionManifest()
	zeroVersion.Version = 0
	assert.ErrorIs(t, zeroVersion.Validate(), ErrInvalidContract)

	empty := validAssertionManifest()
	empty.Assertions = nil
	assert.ErrorIs(t, empty.Validate(), ErrInvalidContract)

	badKind := validAssertionManifest()
	badKind.Assertions[1].Kind = ""
	assert.ErrorIs(t, badKind.Validate(), ErrInvalidContract)
}

func TestEvaluationRunContract(t *testing.T) {
	run := validEvaluationRun()
	require.NoError(t, run.Validate())

	contaminated := validEvaluationRun()
	contaminated.Contamination = ContaminationConfirmed
	assert.ErrorIs(t, contaminated.Validate(), ErrInvalidContract,
		"a contaminated run must never pass")

	duplicate := validEvaluationRun()
	duplicate.AssertionResults = append(duplicate.AssertionResults, duplicate.AssertionResults[0])
	assert.ErrorIs(t, duplicate.Validate(), ErrInvalidContract)

	badResult := validEvaluationRun()
	badResult.AssertionResults[0].Result = "maybe"
	assert.ErrorIs(t, badResult.Validate(), ErrInvalidContract)

	badEvidence := validEvaluationRun()
	badEvidence.AssertionResults[0].EvidenceHash = ""
	assert.ErrorIs(t, badEvidence.Validate(), ErrInvalidContract)
}

func validApprovalRecord() ApprovalRecord {
	return ApprovalRecord{
		ContractKind:     "approval",
		SchemaVersion:    1,
		ApprovalID:       "approval-1",
		WorkspaceID:      "workspace-1",
		CandidateID:      "candidate-1",
		EvaluationRef:    SkillEvolutionRef{Kind: RefEvaluationRun, ID: "eval-1", WorkspaceID: "workspace-1"},
		ManifestHash:     testHash,
		PolicyHash:       testHash,
		ArtifactHash:     testHash,
		TargetScope:      "agent",
		Decision:         ApprovalApproved,
		ApproverActor:    "curator-1",
		ApproverRole:     "curator",
		Reason:           "gate passed on all dimensions",
		RiskAcknowledged: true,
		ExpiresAt:        testTime.Add(24 * time.Hour),
		CreatedAt:        testTime,
	}
}

func TestApprovalRecordContract(t *testing.T) {
	approval := validApprovalRecord()
	require.NoError(t, approval.Validate())

	noRisk := validApprovalRecord()
	noRisk.RiskAcknowledged = false
	assert.ErrorIs(t, noRisk.Validate(), ErrInvalidContract)

	expired := validApprovalRecord()
	expired.ExpiresAt = testTime
	assert.ErrorIs(t, expired.Validate(), ErrInvalidContract)

	wrongRef := validApprovalRecord()
	wrongRef.EvaluationRef.Kind = RefPattern
	assert.ErrorIs(t, wrongRef.Validate(), ErrInvalidContract)

	rejected := validApprovalRecord()
	rejected.Decision = ApprovalRejected
	rejected.RiskAcknowledged = false
	rejected.ExpiresAt = time.Time{}
	require.NoError(t, rejected.Validate(),
		"rejections do not need risk acknowledgement or expiry")
}

func validDeploymentRecord() DeploymentRecord {
	return DeploymentRecord{
		ContractKind:          "deployment",
		SchemaVersion:         1,
		DeploymentID:          "deploy-1",
		WorkspaceID:           "workspace-1",
		CandidateID:           "candidate-1",
		ApprovalID:            "approval-1",
		TargetScope:           "agent",
		TargetAgentID:         "agent-1",
		BindingStateBefore:    "unbound",
		BindingStateAfter:     "bound:skill-1",
		FromArtifactHash:      testHash,
		ToArtifactHash:        testHash,
		MaterializationStatus: MaterializationPending,
		CreatedByActor:        "activation-service",
		CreatedAt:             testTime,
	}
}

func TestDeploymentRecordContract(t *testing.T) {
	deployment := validDeploymentRecord()
	require.NoError(t, deployment.Validate())

	workspace := validDeploymentRecord()
	workspace.TargetScope = "workspace"
	workspace.TargetAgentID = "agent-1"
	assert.ErrorIs(t, workspace.Validate(), ErrInvalidContract,
		"workspace scope must not carry an agent target")

	missingTarget := validDeploymentRecord()
	missingTarget.TargetAgentID = ""
	assert.ErrorIs(t, missingTarget.Validate(), ErrInvalidContract)

	badMaterialization := validDeploymentRecord()
	badMaterialization.MaterializationStatus = "done"
	assert.ErrorIs(t, badMaterialization.Validate(), ErrInvalidContract)
}

func validRollbackRecord() RollbackRecord {
	otherHash := "sha256:" + strings.Repeat("b", 64)
	return RollbackRecord{
		ContractKind:      "rollback",
		SchemaVersion:     1,
		RollbackID:        "rollback-1",
		WorkspaceID:       "workspace-1",
		DeploymentID:      "deploy-1",
		Trigger:           RollbackSafetyFence,
		FromArtifactHash:  testHash,
		ToArtifactHash:    otherHash,
		InFlightPolicy:    "fenced",
		Actor:             "fence-service",
		PolicyVersion:     "policy-1",
		RollForwardStatus: RollForwardPending,
		CreatedAt:         testTime,
	}
}

func TestRollbackRecordContract(t *testing.T) {
	rollback := validRollbackRecord()
	require.NoError(t, rollback.Validate())

	sameArtifact := validRollbackRecord()
	sameArtifact.ToArtifactHash = sameArtifact.FromArtifactHash
	assert.ErrorIs(t, sameArtifact.Validate(), ErrInvalidContract,
		"rollback must actually move the artifact pointer")

	badTrigger := validRollbackRecord()
	badTrigger.Trigger = "vibes"
	assert.ErrorIs(t, badTrigger.Validate(), ErrInvalidContract)
}

func TestDecodeStrictContractRejectsUnknownAndTrailing(t *testing.T) {
	payload, err := json.Marshal(validPatternRecord())
	require.NoError(t, err)

	var decoded PatternRecord
	require.NoError(t, DecodeStrictContract(payload, &decoded))
	assert.Equal(t, validPatternRecord(), decoded)

	unknown := strings.TrimSuffix(string(payload), "}") + `,"leak":"yes"}`
	err = DecodeStrictContract([]byte(unknown), &decoded)
	require.ErrorIs(t, err, ErrInvalidContract)
	assert.Contains(t, err.Error(), "unknown field")

	err = DecodeStrictContract(append(payload, []byte(" 42")...), &decoded)
	require.ErrorIs(t, err, ErrInvalidContract)
	assert.Contains(t, err.Error(), "trailing")

	oversize := append([]byte(`{"contract_kind":"pattern"}`), make([]byte, maxContractBytes)...)
	err = DecodeStrictContract(oversize, &decoded)
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// TestPublicMemoryRefEnumerationStaysClosed pins the spec §12.3 invariant:
// this milestone adds no public MemoryRef kind, and internal evolution refs
// are not resolvable through the public reference surface.
func TestPublicMemoryRefEnumerationStaysClosed(t *testing.T) {
	assert.Equal(t, memorygraph.MemoryRefKind("graph_node"), memorygraph.MemoryRefGraphNode)
	assert.Equal(t, memorygraph.MemoryRefKind("staging_atom"), memorygraph.MemoryRefStagingAtom)

	require.NoError(t, memorygraph.ValidateMemoryRef(memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "node-1"}))
	require.NoError(t, memorygraph.ValidateMemoryRef(memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, SegmentID: "seg-1", AtomID: "atom-1"}))

	for _, kind := range []RefKind{RefSkillCandidate, RefAssertionManifest, RefEvaluationRun, RefApproval} {
		public := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefKind(kind), NodeID: "node-1"}
		assert.Error(t, memorygraph.ValidateMemoryRef(public),
			"evolution ref kind %q must not validate as a public MemoryRef", kind)
	}
}

func TestMaterializationAndRollForwardStateMachines(t *testing.T) {
	assert.True(t, MaterializationPending.CanTransitionTo(MaterializationConverged))
	assert.True(t, MaterializationPending.CanTransitionTo(MaterializationFenced))
	assert.True(t, MaterializationFailed.CanTransitionTo(MaterializationConverged))
	assert.True(t, MaterializationFailed.CanTransitionTo(MaterializationFenced))
	assert.False(t, MaterializationConverged.CanTransitionTo(MaterializationFailed),
		"converged is terminal")
	assert.False(t, MaterializationFenced.CanTransitionTo(MaterializationConverged),
		"fenced is terminal")
	assert.False(t, MaterializationPending.CanTransitionTo(MaterializationPending))

	assert.True(t, RollForwardNone.CanTransitionTo(RollForwardPending))
	assert.True(t, RollForwardNone.CanTransitionTo(RollForwardOpened))
	assert.True(t, RollForwardPending.CanTransitionTo(RollForwardSuperseded))
	assert.True(t, RollForwardOpened.CanTransitionTo(RollForwardSuperseded))
	assert.False(t, RollForwardOpened.CanTransitionTo(RollForwardPending),
		"roll-forward never regresses")
	assert.False(t, RollForwardSuperseded.CanTransitionTo(RollForwardOpened),
		"superseded is terminal")
}

func TestIdempotencyRequestContract(t *testing.T) {
	valid := IdempotentRequest{
		WorkspaceID: "workspace-1", Key: "submit-1",
		RequestKind: "skill_candidate.submit",
		PayloadHash: HashCanonicalPayload([]byte(`{"candidate_id":"cand-1"}`)),
	}
	require.NoError(t, valid.Validate())
	assert.True(t, strings.HasPrefix(valid.PayloadHash, "sha256:"))
	assert.Len(t, valid.PayloadHash, len("sha256:")+64)

	badHash := valid
	badHash.PayloadHash = "not-a-hash"
	assert.ErrorIs(t, badHash.Validate(), ErrInvalidContract)

	emptyKind := valid
	emptyKind.RequestKind = ""
	assert.ErrorIs(t, emptyKind.Validate(), ErrInvalidContract)

	emptyKey := valid
	emptyKey.Key = ""
	assert.ErrorIs(t, emptyKey.Validate(), ErrInvalidContract)
}
