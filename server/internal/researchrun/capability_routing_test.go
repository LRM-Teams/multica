package researchrun

import "testing"

func capabilityRequirement() CapabilityRouteRequirement {
	return CapabilityRouteRequirement{WorkspaceID: "w1", Capability: "validator", TaskKind: "verify", DomainKey: "finance", RequiredToolKeys: []string{"search"}, RequiredSourceAccess: []string{"public"}, MinimumSamples: 2}
}

func TestRouteResearchCapabilityAppliesHardConstraintsBeforeHistory(t *testing.T) {
	requirement := capabilityRequirement()
	requirement.ExcludedProviderKeys = []string{"same-provider"}
	fleet := []CapabilityRouteAgent{
		{AgentID: "high-but-conflicted", WorkspaceID: "w1", Available: true, Capabilities: []string{"validator"}, ToolKeys: []string{"search"}, SourceAccess: []string{"public"}, ProviderKey: "same-provider"},
		{AgentID: "eligible", WorkspaceID: "w1", Available: true, Capabilities: []string{"validator"}, ToolKeys: []string{"search"}, SourceAccess: []string{"public"}, ProviderKey: "independent"},
	}
	observations := []CapabilityObservation{{WorkspaceID: "w1", AttemptID: "x1", AgentID: "high-but-conflicted", TaskKind: "verify", DomainKey: "finance", Succeeded: true, Quality: 1}}
	decision, err := RouteResearchCapability(requirement, fleet, observations)
	if err != nil || decision.SelectedAgentID != "eligible" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestRouteResearchCapabilityRequiresEnoughGroupedSamplesForRanking(t *testing.T) {
	requirement := capabilityRequirement()
	fleet := []CapabilityRouteAgent{
		{AgentID: "a", WorkspaceID: "w1", Available: true, Capabilities: []string{"validator"}, ToolKeys: []string{"search"}, SourceAccess: []string{"public"}},
		{AgentID: "b", WorkspaceID: "w1", Available: true, Capabilities: []string{"validator"}, ToolKeys: []string{"search"}, SourceAccess: []string{"public"}},
	}
	observations := []CapabilityObservation{
		{WorkspaceID: "w1", AttemptID: "a1", AgentID: "a", TaskKind: "verify", DomainKey: "finance", Succeeded: true, Quality: .9},
		{WorkspaceID: "w1", AttemptID: "b1", AgentID: "b", TaskKind: "verify", DomainKey: "finance", Succeeded: true, Quality: .7},
		{WorkspaceID: "w1", AttemptID: "b2", AgentID: "b", TaskKind: "verify", DomainKey: "finance", Succeeded: true, Quality: .7},
	}
	decision, err := RouteResearchCapability(requirement, fleet, observations)
	if err != nil || decision.SelectedAgentID != "b" {
		t.Fatalf("single success must not outrank sufficient sample: %+v err=%v", decision, err)
	}
}

func TestRouteResearchCapabilityReturnsExplicitGap(t *testing.T) {
	decision, err := RouteResearchCapability(capabilityRequirement(), []CapabilityRouteAgent{{AgentID: "a", WorkspaceID: "w1", Available: false}}, nil)
	if err != nil || !decision.CapabilityGap || decision.SelectedAgentID != "" || decision.GapReason == "" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
