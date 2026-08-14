package researchrun

import (
	"errors"
	"testing"
)

const (
	capabilityGapWorkspaceID = "10000000-0000-4000-8000-000000000001"
	capabilityGapRunID       = "20000000-0000-4000-8000-000000000001"
	capabilityGapTargetID    = "30000000-0000-4000-8000-000000000001"
	capabilityGapAgentA      = "40000000-0000-4000-8000-000000000001"
	capabilityGapAgentB      = "40000000-0000-4000-8000-000000000002"
)

func capabilityGapFixture() CapabilityGapActionInput {
	return CapabilityGapActionInput{
		PolicyVersion: CapabilityGapActionPolicyV1,
		WorkspaceID:   capabilityGapWorkspaceID, RunID: capabilityGapRunID, TargetID: capabilityGapTargetID,
		RequiredCapability: "research_verification", RequiredToolKeys: []string{"search"}, RequiredSourceAccess: []string{"public"},
		AllowedToolKeys: []string{"browser", "search"}, AllowedSourceAccess: []string{"public"},
		AllowAgentCreation: true, CurrentAgentCount: 2, MaximumAgents: 4, RequestedFormationBudget: 10, RemainingBudget: 20,
		Reason: "No active Agent currently satisfies capability and permission constraints.",
	}
}

func TestDecideCapabilityGapActionActivatesExistingAgent(t *testing.T) {
	input := capabilityGapFixture()
	input.ExistingAgents = []CapabilityGapAgent{{AgentID: capabilityGapAgentA, WorkspaceID: capabilityGapWorkspaceID, State: "inactive", HasCapability: true, IndependenceEligible: true, ActivationAllowed: true}}
	decision, err := DecideCapabilityGapAction(input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "activate_existing" || decision.AgentID != capabilityGapAgentA || len(decision.ActionKey) != 71 || len(decision.Fingerprint) != 71 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestDecideCapabilityGapActionConfiguresLeastChangeAgent(t *testing.T) {
	input := capabilityGapFixture()
	input.ExistingAgents = []CapabilityGapAgent{
		{AgentID: capabilityGapAgentB, WorkspaceID: capabilityGapWorkspaceID, State: "active", HasCapability: true, IndependenceEligible: true, ConfigurationAllowed: true, MissingToolKeys: []string{"browser", "search"}},
		{AgentID: capabilityGapAgentA, WorkspaceID: capabilityGapWorkspaceID, State: "active", HasCapability: true, IndependenceEligible: true, ConfigurationAllowed: true, MissingToolKeys: []string{"search"}},
	}
	decision, err := DecideCapabilityGapAction(input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "configure_existing" || decision.AgentID != capabilityGapAgentA || len(decision.MissingTools) != 1 || decision.MissingTools[0] != "search" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestDecideCapabilityGapActionProposesFormation(t *testing.T) {
	decision, err := DecideCapabilityGapAction(capabilityGapFixture())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "propose_team_formation" || decision.Reason != "authorized_formation_candidate" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestDecideCapabilityGapActionBlocksExplicitly(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*CapabilityGapActionInput)
		want   string
	}{
		"contract":          {func(input *CapabilityGapActionInput) { input.AllowAgentCreation = false }, "contract_disallows_agent_creation"},
		"limit":             {func(input *CapabilityGapActionInput) { input.CurrentAgentCount = input.MaximumAgents }, "agent_limit_reached"},
		"budget":            {func(input *CapabilityGapActionInput) { input.RemainingBudget = 1 }, "insufficient_budget"},
		"tool permission":   {func(input *CapabilityGapActionInput) { input.AllowedToolKeys = nil }, "required_tool_not_authorized"},
		"source permission": {func(input *CapabilityGapActionInput) { input.AllowedSourceAccess = nil }, "required_source_access_not_authorized"},
	} {
		t.Run(name, func(t *testing.T) {
			input := capabilityGapFixture()
			tc.mutate(&input)
			decision, err := DecideCapabilityGapAction(input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != "blocked" || decision.Reason != tc.want {
				t.Fatalf("decision=%+v want=%s", decision, tc.want)
			}
		})
	}
}

func TestDecideCapabilityGapActionRejectsStaleGap(t *testing.T) {
	input := capabilityGapFixture()
	input.ExistingAgents = []CapabilityGapAgent{{AgentID: capabilityGapAgentA, WorkspaceID: capabilityGapWorkspaceID, State: "active", HasCapability: true, IndependenceEligible: true}}
	if _, err := DecideCapabilityGapAction(input); !errors.Is(err, ErrControlTargetChanged) {
		t.Fatalf("err=%v want ErrControlTargetChanged", err)
	}
}

func TestDecideCapabilityGapActionIsOrderStable(t *testing.T) {
	input := capabilityGapFixture()
	input.RequiredToolKeys = []string{"search", "browser"}
	input.AllowedToolKeys = []string{"search", "browser"}
	input.ExistingAgents = []CapabilityGapAgent{
		{AgentID: capabilityGapAgentB, WorkspaceID: capabilityGapWorkspaceID, State: "inactive", HasCapability: true, IndependenceEligible: true, ActivationAllowed: true},
		{AgentID: capabilityGapAgentA, WorkspaceID: capabilityGapWorkspaceID, State: "inactive", HasCapability: true, IndependenceEligible: true, ActivationAllowed: true},
	}
	first, err := DecideCapabilityGapAction(input)
	if err != nil {
		t.Fatal(err)
	}
	input.RequiredToolKeys[0], input.RequiredToolKeys[1] = input.RequiredToolKeys[1], input.RequiredToolKeys[0]
	input.ExistingAgents[0], input.ExistingAgents[1] = input.ExistingAgents[1], input.ExistingAgents[0]
	second, err := DecideCapabilityGapAction(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentID != capabilityGapAgentA || first.Fingerprint != second.Fingerprint {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestDecideCapabilityGapActionRejectsInvalidInput(t *testing.T) {
	for name, mutate := range map[string]func(*CapabilityGapActionInput){
		"cross workspace Agent": func(input *CapabilityGapActionInput) {
			input.ExistingAgents = []CapabilityGapAgent{{AgentID: capabilityGapAgentA, WorkspaceID: capabilityGapRunID, State: "inactive"}}
		},
		"duplicate permission": func(input *CapabilityGapActionInput) { input.AllowedToolKeys = []string{"search", "search"} },
		"invalid budget":       func(input *CapabilityGapActionInput) { input.RemainingBudget = -1 },
		"weak reason":          func(input *CapabilityGapActionInput) { input.Reason = "gap" },
	} {
		t.Run(name, func(t *testing.T) {
			input := capabilityGapFixture()
			mutate(&input)
			if _, err := DecideCapabilityGapAction(input); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
