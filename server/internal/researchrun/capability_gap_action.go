package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const CapabilityGapActionPolicyV1 = "research-capability-gap-action-v1"

type CapabilityGapAgent struct {
	AgentID              string
	WorkspaceID          string
	State                string
	HasCapability        bool
	IndependenceEligible bool
	ActivationAllowed    bool
	ConfigurationAllowed bool
	MissingToolKeys      []string
	MissingSourceAccess  []string
}

type CapabilityGapActionInput struct {
	PolicyVersion            string
	WorkspaceID              string
	RunID                    string
	TargetID                 string
	RequiredCapability       string
	RequiredToolKeys         []string
	RequiredSourceAccess     []string
	AllowedToolKeys          []string
	AllowedSourceAccess      []string
	ExistingAgents           []CapabilityGapAgent
	AllowAgentCreation       bool
	CurrentAgentCount        int
	MaximumAgents            int
	RequestedFormationBudget float64
	RemainingBudget          float64
	Reason                   string
}

type CapabilityGapActionDecision struct {
	Action         string
	AgentID        string
	Reason         string
	ActionKey      string
	MissingTools   []string
	MissingSources []string
	Fingerprint    string
}

// DecideCapabilityGapAction bridges a proven routing gap to one explicit next
// action. It never activates, configures, or creates an Agent itself.
func DecideCapabilityGapAction(input CapabilityGapActionInput) (CapabilityGapActionDecision, error) {
	normalized, err := normalizeCapabilityGapInput(input)
	if err != nil {
		return CapabilityGapActionDecision{}, err
	}
	for _, agent := range normalized.ExistingAgents {
		if agent.State == "active" && capabilityGapAgentReady(agent) {
			return CapabilityGapActionDecision{}, fmt.Errorf("%w: capability gap is stale because an eligible Agent exists", ErrControlTargetChanged)
		}
	}

	decision := CapabilityGapActionDecision{}
	for _, agent := range normalized.ExistingAgents {
		if agent.State == "inactive" && capabilityGapAgentReady(agent) && agent.ActivationAllowed {
			decision.Action, decision.AgentID, decision.Reason = "activate_existing", agent.AgentID, "inactive_eligible_agent"
			break
		}
	}
	if decision.Action == "" {
		for _, agent := range normalized.ExistingAgents {
			if agent.State != "unavailable" && agent.HasCapability && agent.IndependenceEligible && agent.ConfigurationAllowed &&
				(len(agent.MissingToolKeys) > 0 || len(agent.MissingSourceAccess) > 0) &&
				capabilityGapContainsAll(normalized.AllowedToolKeys, agent.MissingToolKeys) &&
				capabilityGapContainsAll(normalized.AllowedSourceAccess, agent.MissingSourceAccess) {
				decision.Action, decision.AgentID, decision.Reason = "configure_existing", agent.AgentID, "authorized_configuration_closes_gap"
				decision.MissingTools = append([]string(nil), agent.MissingToolKeys...)
				decision.MissingSources = append([]string(nil), agent.MissingSourceAccess...)
				break
			}
		}
	}
	if decision.Action == "" {
		decision = capabilityGapFormationOrBlock(normalized)
	}
	identity, err := json.Marshal(struct {
		PolicyVersion      string
		WorkspaceID        string
		RunID              string
		TargetID           string
		RequiredCapability string
		Action             string
		AgentID            string
		MissingTools       []string
		MissingSources     []string
	}{normalized.PolicyVersion, normalized.WorkspaceID, normalized.RunID, normalized.TargetID, normalized.RequiredCapability, decision.Action, decision.AgentID, decision.MissingTools, decision.MissingSources})
	if err != nil {
		return CapabilityGapActionDecision{}, err
	}
	digest := sha256.Sum256(identity)
	decision.ActionKey = fmt.Sprintf("sha256:%x", digest)
	audit, err := json.Marshal(struct {
		Input    CapabilityGapActionInput
		Decision CapabilityGapActionDecision
	}{normalized, decision})
	if err != nil {
		return CapabilityGapActionDecision{}, err
	}
	auditDigest := sha256.Sum256(audit)
	decision.Fingerprint = fmt.Sprintf("sha256:%x", auditDigest)
	return decision, nil
}

func capabilityGapFormationOrBlock(input CapabilityGapActionInput) CapabilityGapActionDecision {
	decision := CapabilityGapActionDecision{Action: "blocked"}
	switch {
	case !input.AllowAgentCreation:
		decision.Reason = "contract_disallows_agent_creation"
	case input.CurrentAgentCount >= input.MaximumAgents:
		decision.Reason = "agent_limit_reached"
	case input.RequestedFormationBudget > input.RemainingBudget:
		decision.Reason = "insufficient_budget"
	case !capabilityGapContainsAll(input.AllowedToolKeys, input.RequiredToolKeys):
		decision.Reason = "required_tool_not_authorized"
	case !capabilityGapContainsAll(input.AllowedSourceAccess, input.RequiredSourceAccess):
		decision.Reason = "required_source_access_not_authorized"
	default:
		decision.Action = "propose_team_formation"
		decision.Reason = "authorized_formation_candidate"
	}
	return decision
}

func normalizeCapabilityGapInput(input CapabilityGapActionInput) (CapabilityGapActionInput, error) {
	if input.PolicyVersion != CapabilityGapActionPolicyV1 || !validCapabilityGapUUID(input.WorkspaceID) ||
		!validCapabilityGapUUID(input.RunID) || !validCapabilityGapUUID(input.TargetID) ||
		!validCapabilityGapToken(input.RequiredCapability, 160) || input.CurrentAgentCount < 0 || input.MaximumAgents < 0 ||
		input.RequestedFormationBudget < 0 || input.RemainingBudget < 0 ||
		strings.TrimSpace(input.Reason) != input.Reason || substantiveRuneCount(input.Reason) < 8 || len(input.Reason) > 4096 || len(input.ExistingAgents) > 1024 {
		return CapabilityGapActionInput{}, fmt.Errorf("%w: capability gap action input is invalid", ErrInvalidContract)
	}
	var err error
	if input.RequiredToolKeys, err = normalizeCapabilityGapValues(input.RequiredToolKeys, 64); err != nil {
		return CapabilityGapActionInput{}, err
	}
	if input.RequiredSourceAccess, err = normalizeCapabilityGapValues(input.RequiredSourceAccess, 64); err != nil {
		return CapabilityGapActionInput{}, err
	}
	if input.AllowedToolKeys, err = normalizeCapabilityGapValues(input.AllowedToolKeys, 256); err != nil {
		return CapabilityGapActionInput{}, err
	}
	if input.AllowedSourceAccess, err = normalizeCapabilityGapValues(input.AllowedSourceAccess, 256); err != nil {
		return CapabilityGapActionInput{}, err
	}
	input.ExistingAgents = append([]CapabilityGapAgent(nil), input.ExistingAgents...)
	seenAgents := map[string]struct{}{}
	for index := range input.ExistingAgents {
		agent := &input.ExistingAgents[index]
		if !validCapabilityGapUUID(agent.AgentID) || agent.WorkspaceID != input.WorkspaceID ||
			(agent.State != "active" && agent.State != "inactive" && agent.State != "unavailable") {
			return CapabilityGapActionInput{}, fmt.Errorf("%w: capability gap Agent is invalid", ErrInvalidContract)
		}
		if _, duplicate := seenAgents[agent.AgentID]; duplicate {
			return CapabilityGapActionInput{}, fmt.Errorf("%w: duplicate capability gap Agent", ErrInvalidContract)
		}
		seenAgents[agent.AgentID] = struct{}{}
		if agent.MissingToolKeys, err = normalizeCapabilityGapValues(agent.MissingToolKeys, 64); err != nil {
			return CapabilityGapActionInput{}, err
		}
		if agent.MissingSourceAccess, err = normalizeCapabilityGapValues(agent.MissingSourceAccess, 64); err != nil {
			return CapabilityGapActionInput{}, err
		}
	}
	sort.Slice(input.ExistingAgents, func(i, j int) bool {
		left, right := input.ExistingAgents[i], input.ExistingAgents[j]
		leftMissing := len(left.MissingToolKeys) + len(left.MissingSourceAccess)
		rightMissing := len(right.MissingToolKeys) + len(right.MissingSourceAccess)
		if leftMissing != rightMissing {
			return leftMissing < rightMissing
		}
		return left.AgentID < right.AgentID
	})
	return input, nil
}

func capabilityGapAgentReady(agent CapabilityGapAgent) bool {
	return agent.HasCapability && agent.IndependenceEligible && len(agent.MissingToolKeys) == 0 && len(agent.MissingSourceAccess) == 0
}

func normalizeCapabilityGapValues(values []string, limit int) ([]string, error) {
	if len(values) > limit {
		return nil, fmt.Errorf("%w: capability gap value list exceeds limit", ErrInvalidContract)
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if !validCapabilityGapToken(value, 160) || index > 0 && result[index-1] == value {
			return nil, fmt.Errorf("%w: capability gap value list is invalid", ErrInvalidContract)
		}
	}
	return result, nil
}

func capabilityGapContainsAll(have, required []string) bool {
	values := make(map[string]struct{}, len(have))
	for _, value := range have {
		values[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func validCapabilityGapUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validCapabilityGapToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}
