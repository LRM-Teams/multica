package researchrun

import "strings"

const v6ActivationCandidate = "research-run-v6"

// V6ActivationRequirement names one production fact that must be proven before
// the immutable V6 protocol may accept its first Run. The order below is the
// canonical audit order returned to operators and tests.
type V6ActivationRequirement string

const (
	V6RequirementInquiry          V6ActivationRequirement = "inquiry"
	V6RequirementCorpus           V6ActivationRequirement = "corpus"
	V6RequirementIntegration      V6ActivationRequirement = "integration"
	V6RequirementDispute          V6ActivationRequirement = "dispute"
	V6RequirementPortfolio        V6ActivationRequirement = "portfolio"
	V6RequirementTeamFormation    V6ActivationRequirement = "team_formation"
	V6RequirementReportEvaluation V6ActivationRequirement = "report_evaluation"
	V6RequirementDecoder          V6ActivationRequirement = "production_decoder"
	V6RequirementPersistence      V6ActivationRequirement = "canonical_persistence"
	V6RequirementRecovery         V6ActivationRequirement = "recovery"
	V6RequirementProjection       V6ActivationRequirement = "projection_snapshot_delta_resume_slice_detail"
	V6RequirementSystemEvaluation V6ActivationRequirement = "system_evaluation_hidden_oracle"
	V6RequirementShadowTraffic    V6ActivationRequirement = "shadow_traffic_comparison"
	V6RequirementRollback         V6ActivationRequirement = "rollback_previous_version"
)

// V6GateEvidence identifies a reviewed, versioned result. Passed without a
// durable evidence identity is not readiness evidence.
type V6GateEvidence struct {
	Passed     bool
	EvidenceID string
	Revision   string
}

func (e V6GateEvidence) ready() bool {
	return e.Passed && strings.TrimSpace(e.EvidenceID) != "" && strings.TrimSpace(e.Revision) != ""
}

// V6RollbackEvidence proves both the executable rollback exercise and the
// exact immutable version to which new Runs will return.
type V6RollbackEvidence struct {
	V6GateEvidence
	PreviousVersion string
}

// V6ActivationEvidence is assembled by the release audit. It is deliberately
// independent of runtime configuration: creating a Run cannot self-attest any
// of these production facts.
type V6ActivationEvidence struct {
	Inquiry          V6GateEvidence
	Corpus           V6GateEvidence
	Integration      V6GateEvidence
	Dispute          V6GateEvidence
	Portfolio        V6GateEvidence
	TeamFormation    V6GateEvidence
	ReportEvaluation V6GateEvidence
	Decoder          V6GateEvidence
	Persistence      V6GateEvidence
	Recovery         V6GateEvidence
	Projection       V6GateEvidence
	SystemEvaluation V6GateEvidence
	ShadowTraffic    V6GateEvidence
	Rollback         V6RollbackEvidence
}

// V6ActivationDecision is an audit result, not a runtime version selector.
// ActivationAllowed never changes OrchestratorVersion or the supported decoder
// list; the eventual activation change must still do that explicitly.
type V6ActivationDecision struct {
	CurrentDefault    string
	CandidateVersion  string
	RollbackVersion   string
	ActivationAllowed bool
	Missing           []V6ActivationRequirement
}

// AssessV6Activation evaluates every E-K/N production exit without short
// circuiting, so one audit reports the complete deterministic gap set.
func AssessV6Activation(evidence V6ActivationEvidence) V6ActivationDecision {
	checks := []struct {
		requirement V6ActivationRequirement
		ready       bool
	}{
		{V6RequirementInquiry, evidence.Inquiry.ready()},
		{V6RequirementCorpus, evidence.Corpus.ready()},
		{V6RequirementIntegration, evidence.Integration.ready()},
		{V6RequirementDispute, evidence.Dispute.ready()},
		{V6RequirementPortfolio, evidence.Portfolio.ready()},
		{V6RequirementTeamFormation, evidence.TeamFormation.ready()},
		{V6RequirementReportEvaluation, evidence.ReportEvaluation.ready()},
		{V6RequirementDecoder, evidence.Decoder.ready()},
		{V6RequirementPersistence, evidence.Persistence.ready()},
		{V6RequirementRecovery, evidence.Recovery.ready()},
		{V6RequirementProjection, evidence.Projection.ready()},
		{V6RequirementSystemEvaluation, evidence.SystemEvaluation.ready()},
		{V6RequirementShadowTraffic, evidence.ShadowTraffic.ready()},
		{V6RequirementRollback, evidence.Rollback.ready() && evidence.Rollback.PreviousVersion == OrchestratorVersionV5},
	}

	missing := make([]V6ActivationRequirement, 0, len(checks))
	for _, check := range checks {
		if !check.ready {
			missing = append(missing, check.requirement)
		}
	}
	return V6ActivationDecision{
		CurrentDefault:    OrchestratorVersion,
		CandidateVersion:  v6ActivationCandidate,
		RollbackVersion:   evidence.Rollback.PreviousVersion,
		ActivationAllowed: len(missing) == 0,
		Missing:           missing,
	}
}
