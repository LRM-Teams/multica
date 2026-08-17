package researchrun

import (
	"net"
	"net/url"
	"strings"
)

const v6ActivationCandidate = "research-run-v6"

type V6ActivationRequirement string

const (
	V6RequirementMigrations           V6ActivationRequirement = "canonical_migrations_up_down_up"
	V6RequirementSchemaHash           V6ActivationRequirement = "director_schema_hash"
	V6RequirementLegacyGolden         V6ActivationRequirement = "v1_v5_golden_compatibility"
	V6RequirementNineEnvelopes        V6ActivationRequirement = "strict_nine_envelopes"
	V6RequirementRecoveryMatrix       V6ActivationRequirement = "transaction_recovery_matrix"
	V6RequirementSingleSuccessorRace  V6ActivationRequirement = "single_successor_race"
	V6RequirementDirectorContext      V6ActivationRequirement = "director_context_bound_and_rotation"
	V6RequirementTeamLimit            V6ActivationRequirement = "dynamic_team_hard_limit_50"
	V6RequirementKnowledgeGraph       V6ActivationRequirement = "promotion_assimilation_and_challenge"
	V6RequirementDiscussion           V6ActivationRequirement = "discussion_dispute_and_stale_input"
	V6RequirementSteering             V6ActivationRequirement = "steering_and_goal_cascade"
	V6RequirementProjectionRebuild    V6ActivationRequirement = "projection_rebuild_hash"
	V6RequirementProjectionScale      V6ActivationRequirement = "projection_large_graph_50k_s"
	V6RequirementGraphClients         V6ActivationRequirement = "web_desktop_graph_clients"
	V6RequirementReportSandboxWeb     V6ActivationRequirement = "html_report_sandbox_web"
	V6RequirementReportSandboxDesktop V6ActivationRequirement = "html_report_sandbox_desktop"
	V6RequirementReportOrigin         V6ActivationRequirement = "report_origin_isolated"
	V6RequirementBuiltinDocs          V6ActivationRequirement = "builtin_skill_and_source_map"
	V6RequirementRollback             V6ActivationRequirement = "default_rollback_to_v5_drilled"
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

type V6ReportOriginEvidence struct {
	V6GateEvidence
	ReportOrigin       string
	ApplicationOrigins []string
}

func (e V6ReportOriginEvidence) ready() bool {
	return e.V6GateEvidence.ready() && ValidateV6ReportOrigin(e.ReportOrigin, e.ApplicationOrigins)
}

// ValidateV6ReportOrigin is shared by the runtime URL issuer and activation
// audit so evidence cannot approve a weaker origin policy than production uses.
func ValidateV6ReportOrigin(rawReportOrigin string, rawApplicationOrigins []string) bool {
	if len(rawApplicationOrigins) == 0 {
		return false
	}
	reportOrigin, ok := canonicalHTTPSOrigin(rawReportOrigin)
	if !ok {
		return false
	}
	for _, raw := range rawApplicationOrigins {
		applicationOrigin, valid := canonicalHTTPSOrigin(raw)
		if !valid || applicationOrigin == reportOrigin {
			return false
		}
	}
	return true
}

func canonicalHTTPSOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if host == "" || (port != "" && port != "443") {
		if host == "" {
			return "", false
		}
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, true
}

type V6ActivationEvidence struct {
	Migrations, SchemaHash, LegacyGolden, NineEnvelopes V6GateEvidence
	RecoveryMatrix, SingleSuccessorRace                 V6GateEvidence
	DirectorContext, TeamLimit                          V6GateEvidence
	KnowledgeGraph, Discussion, Steering                V6GateEvidence
	ProjectionRebuild, ProjectionScale, GraphClients    V6GateEvidence
	ReportSandboxWeb, ReportSandboxDesktop              V6GateEvidence
	ReportOrigin                                        V6ReportOriginEvidence
	BuiltinDocs                                         V6GateEvidence
	Rollback                                            V6RollbackEvidence
}

type V6ActivationDecision struct {
	CurrentDefault, CandidateVersion, RollbackVersion string
	ActivationAllowed                                 bool
	Missing                                           []V6ActivationRequirement
}

// AssessV6Activation is an audit only. It cannot modify the supported decoder
// list or the new-Run default. A release controller must separately consume a
// persisted, reviewed Decision after every exit has current versioned evidence.
func AssessV6Activation(e V6ActivationEvidence) V6ActivationDecision {
	checks := []struct {
		requirement V6ActivationRequirement
		ready       bool
	}{
		{V6RequirementMigrations, e.Migrations.ready()},
		{V6RequirementSchemaHash, e.SchemaHash.ready()},
		{V6RequirementLegacyGolden, e.LegacyGolden.ready()},
		{V6RequirementNineEnvelopes, e.NineEnvelopes.ready()},
		{V6RequirementRecoveryMatrix, e.RecoveryMatrix.ready()},
		{V6RequirementSingleSuccessorRace, e.SingleSuccessorRace.ready()},
		{V6RequirementDirectorContext, e.DirectorContext.ready()},
		{V6RequirementTeamLimit, e.TeamLimit.ready()},
		{V6RequirementKnowledgeGraph, e.KnowledgeGraph.ready()},
		{V6RequirementDiscussion, e.Discussion.ready()},
		{V6RequirementSteering, e.Steering.ready()},
		{V6RequirementProjectionRebuild, e.ProjectionRebuild.ready()},
		{V6RequirementProjectionScale, e.ProjectionScale.ready()},
		{V6RequirementGraphClients, e.GraphClients.ready()},
		{V6RequirementReportSandboxWeb, e.ReportSandboxWeb.ready()},
		{V6RequirementReportSandboxDesktop, e.ReportSandboxDesktop.ready()},
		{V6RequirementReportOrigin, e.ReportOrigin.ready()},
		{V6RequirementBuiltinDocs, e.BuiltinDocs.ready()},
		{V6RequirementRollback, e.Rollback.ready() && e.Rollback.PreviousVersion == OrchestratorVersionV5},
	}
	missing := make([]V6ActivationRequirement, 0, len(checks))
	for _, check := range checks {
		if !check.ready {
			missing = append(missing, check.requirement)
		}
	}
	return V6ActivationDecision{CurrentDefault: OrchestratorVersion, CandidateVersion: v6ActivationCandidate, RollbackVersion: e.Rollback.PreviousVersion, ActivationAllowed: len(missing) == 0, Missing: missing}
}
