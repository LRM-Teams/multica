package researchrun

import "strings"

const v6ActivationCandidate = "research-run-v6"

type V6ActivationRequirement string

const (
	V6RequirementMigrations     V6ActivationRequirement = "canonical_migrations"
	V6RequirementContract       V6ActivationRequirement = "strict_nine_envelope_contract"
	V6RequirementWorkItems      V6ActivationRequirement = "work_item_recovery"
	V6RequirementDirector       V6ActivationRequirement = "director_brief_and_rotation"
	V6RequirementTeam           V6ActivationRequirement = "dynamic_team_and_director_failure"
	V6RequirementKnowledgeGraph V6ActivationRequirement = "tier_absorption_and_challenge"
	V6RequirementDiscussion     V6ActivationRequirement = "discussion_dispute_and_stale_input"
	V6RequirementSteering       V6ActivationRequirement = "steering_and_goal_cascade"
	V6RequirementProjection     V6ActivationRequirement = "projection_snapshot_delta_slice_and_scale"
	V6RequirementGraphClients   V6ActivationRequirement = "web_desktop_graph_clients"
	V6RequirementReportSandbox  V6ActivationRequirement = "html_report_sandbox"
	V6RequirementCompatibility  V6ActivationRequirement = "v1_v5_compatibility"
	V6RequirementRollback       V6ActivationRequirement = "rollback_previous_version"
)

type V6GateEvidence struct {
	Passed     bool
	EvidenceID string
	Revision   string
}

func (e V6GateEvidence) ready() bool {
	return e.Passed && strings.TrimSpace(e.EvidenceID) != "" && strings.TrimSpace(e.Revision) != ""
}

type V6RollbackEvidence struct {
	V6GateEvidence
	PreviousVersion string
}

type V6ActivationEvidence struct {
	Migrations     V6GateEvidence
	Contract       V6GateEvidence
	WorkItems      V6GateEvidence
	Director       V6GateEvidence
	Team           V6GateEvidence
	KnowledgeGraph V6GateEvidence
	Discussion     V6GateEvidence
	Steering       V6GateEvidence
	Projection     V6GateEvidence
	GraphClients   V6GateEvidence
	ReportSandbox  V6GateEvidence
	Compatibility  V6GateEvidence
	Rollback       V6RollbackEvidence
}

type V6ActivationDecision struct {
	CurrentDefault    string
	CandidateVersion  string
	RollbackVersion   string
	ActivationAllowed bool
	Missing           []V6ActivationRequirement
}

// AssessV6Activation is an audit only. It cannot modify the supported decoder
// list or the new-Run default, even when every evidence item is present.
func AssessV6Activation(evidence V6ActivationEvidence) V6ActivationDecision {
	checks := []struct {
		requirement V6ActivationRequirement
		ready       bool
	}{
		{V6RequirementMigrations, evidence.Migrations.ready()},
		{V6RequirementContract, evidence.Contract.ready()},
		{V6RequirementWorkItems, evidence.WorkItems.ready()},
		{V6RequirementDirector, evidence.Director.ready()},
		{V6RequirementTeam, evidence.Team.ready()},
		{V6RequirementKnowledgeGraph, evidence.KnowledgeGraph.ready()},
		{V6RequirementDiscussion, evidence.Discussion.ready()},
		{V6RequirementSteering, evidence.Steering.ready()},
		{V6RequirementProjection, evidence.Projection.ready()},
		{V6RequirementGraphClients, evidence.GraphClients.ready()},
		{V6RequirementReportSandbox, evidence.ReportSandbox.ready()},
		{V6RequirementCompatibility, evidence.Compatibility.ready()},
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
