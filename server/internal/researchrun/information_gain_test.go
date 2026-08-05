package researchrun

import (
	"math"
	"testing"
)

func TestMeasuredInformationGainRejectsDuplicateBatch(t *testing.T) {
	state := researchGraphState{
		Questions: 2, RequiredQuestions: 1, VerifiedRequiredCoverage: 0.8,
		VerifiedAnsweredRequiredQuestions: 1, SourceSnapshots: 4, VerifiedIndependentSources: 2,
		Observations: 6, Claims: 3, ResolvedClaims: 1, EvidenceLinks: 5,
		VerifiedEvidenceLinks: 4, VerifiedContradictions: 1,
	}
	if gain := measuredInformationGain(state, state, TaskKindDiscover); gain.Score != 0 {
		t.Fatalf("gain=%+v", gain)
	}
}

func TestMeasuredInformationGainRewardsVerifiedAnswerAndEvidenceUpgrade(t *testing.T) {
	before := researchGraphState{
		Questions: 1, RequiredQuestions: 1, SourceSnapshots: 1, Observations: 1,
		Claims: 1, EvidenceLinks: 1,
	}
	after := before
	after.VerifiedRequiredCoverage = 0.9
	after.VerifiedAnsweredRequiredQuestions = 1
	after.VerifiedIndependentSources = 1
	after.VerifiedEvidenceLinks = 1
	gain := measuredInformationGain(before, after, TaskKindVerify)
	if gain.Score < 0.7 || gain.VerifiedCoverage == 0 || gain.AnsweredQuestions == 0 || gain.VerifiedEvidence == 0 {
		t.Fatalf("gain=%+v", gain)
	}
}

func TestMeasuredInformationGainRewardsCounterevidenceAndResolution(t *testing.T) {
	before := researchGraphState{Claims: 2, EvidenceLinks: 2}
	after := before
	after.ResolvedClaims = 1
	after.VerifiedContradictions = 1
	gain := measuredInformationGain(before, after, TaskKindCounterSearch)
	if math.Abs(gain.Score-0.15) > 1e-9 || gain.Counterevidence == 0 || gain.ClaimResolution == 0 {
		t.Fatalf("gain=%+v", gain)
	}
}

func TestMeasuredInformationGainRawNoveltyDiminishesAsGraphGrows(t *testing.T) {
	initial := measuredInformationGain(researchGraphState{}, researchGraphState{SourceSnapshots: 1, Observations: 1, Claims: 1}, TaskKindDiscover)
	mature := measuredInformationGain(
		researchGraphState{SourceSnapshots: 9, Observations: 9, Claims: 9},
		researchGraphState{SourceSnapshots: 10, Observations: 10, Claims: 10},
		TaskKindDiscover,
	)
	if initial.Score <= mature.Score || initial.Score < 0.049 || mature.Score > 0.006 {
		t.Fatalf("initial=%+v mature=%+v", initial, mature)
	}
}

func TestMeasuredInformationGainRecognizesVerifiedClaimAdjudication(t *testing.T) {
	before := researchGraphState{Claims: 1, ClaimAdjudicationHash: "before"}
	after := researchGraphState{Claims: 1, ClaimAdjudicationHash: "after"}
	verified := measuredInformationGain(before, after, TaskKindVerify)
	discovery := measuredInformationGain(before, after, TaskKindDiscover)
	if verified.ClaimAdjudication != 0.10 || verified.Score != 0.10 || discovery.ClaimAdjudication != 0 {
		t.Fatalf("verified=%+v discovery=%+v", verified, discovery)
	}
}
