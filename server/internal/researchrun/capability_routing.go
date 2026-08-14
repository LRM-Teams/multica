package researchrun

import (
	"fmt"
	"sort"
)

type CapabilityObservation struct {
	WorkspaceID   string
	AttemptID     string
	AgentID       string
	ModelKey      string
	ProviderKey   string
	TaskKind      string
	DomainKey     string
	ToolKey       string
	AdapterKey    string
	Succeeded     bool
	Quality       float64
	Cost          float64
	LatencyMS     int64
	DuplicateRate float64
	FailureClass  string
}

func ValidateCapabilityObservation(observation CapabilityObservation) error {
	if observation.WorkspaceID == "" || observation.AttemptID == "" || observation.AgentID == "" || observation.TaskKind == "" || observation.DomainKey == "" {
		return fmt.Errorf("%w: capability observation identity and grouping dimensions are required", ErrInvalidContract)
	}
	if observation.Quality < 0 || observation.Quality > 1 || observation.DuplicateRate < 0 || observation.DuplicateRate > 1 || observation.Cost < 0 || observation.LatencyMS < 0 {
		return fmt.Errorf("%w: capability observation measurements are invalid", ErrInvalidContract)
	}
	if !observation.Succeeded && observation.FailureClass == "" {
		return fmt.Errorf("%w: failed capability observation requires a failure class", ErrInvalidContract)
	}
	return nil
}

type CapabilityRouteRequirement struct {
	WorkspaceID          string
	Capability           string
	TaskKind             string
	DomainKey            string
	RequiredToolKeys     []string
	RequiredSourceAccess []string
	ExcludedAgentIDs     []string
	ExcludedModelKeys    []string
	ExcludedProviderKeys []string
	ExcludedAdapterKeys  []string
	MinimumSamples       int
}

type CapabilityRouteAgent struct {
	AgentID      string
	WorkspaceID  string
	Available    bool
	Capabilities []string
	ToolKeys     []string
	SourceAccess []string
	ModelKey     string
	ProviderKey  string
	AdapterKey   string
}

type CapabilityRouteCandidate struct {
	AgentID       string
	Eligible      bool
	Reason        string
	SampleCount   int
	ObservedScore float64
}

type CapabilityRouteDecision struct {
	Candidates      []CapabilityRouteCandidate
	SelectedAgentID string
	CapabilityGap   bool
	GapReason       string
}

func RouteResearchCapability(requirement CapabilityRouteRequirement, fleet []CapabilityRouteAgent, observations []CapabilityObservation) (CapabilityRouteDecision, error) {
	if requirement.WorkspaceID == "" || requirement.Capability == "" || requirement.TaskKind == "" || requirement.DomainKey == "" || requirement.MinimumSamples <= 0 {
		return CapabilityRouteDecision{}, fmt.Errorf("%w: capability route requirement is incomplete", ErrInvalidContract)
	}
	for _, observation := range observations {
		if err := ValidateCapabilityObservation(observation); err != nil {
			return CapabilityRouteDecision{}, err
		}
		if observation.WorkspaceID != requirement.WorkspaceID {
			return CapabilityRouteDecision{}, fmt.Errorf("%w: cross-workspace capability observation", ErrInvalidContract)
		}
	}
	decision := CapabilityRouteDecision{}
	for _, agent := range fleet {
		candidate := CapabilityRouteCandidate{AgentID: agent.AgentID}
		switch {
		case agent.AgentID == "" || agent.WorkspaceID != requirement.WorkspaceID:
			candidate.Reason = "workspace_or_identity_mismatch"
		case !agent.Available:
			candidate.Reason = "agent_unavailable"
		case !containsCapabilityValue(agent.Capabilities, requirement.Capability):
			candidate.Reason = "capability_missing"
		case containsCapabilityValue(requirement.ExcludedAgentIDs, agent.AgentID) || containsCapabilityValue(requirement.ExcludedModelKeys, agent.ModelKey) || containsCapabilityValue(requirement.ExcludedProviderKeys, agent.ProviderKey) || containsCapabilityValue(requirement.ExcludedAdapterKeys, agent.AdapterKey):
			candidate.Reason = "independence_requirement_failed"
		case !containsAllCapabilityValues(agent.ToolKeys, requirement.RequiredToolKeys):
			candidate.Reason = "tool_permission_missing"
		case !containsAllCapabilityValues(agent.SourceAccess, requirement.RequiredSourceAccess):
			candidate.Reason = "source_access_missing"
		default:
			candidate.Eligible = true
			candidate.Reason = "eligible_without_sufficient_history"
			var quality float64
			for _, observation := range observations {
				if observation.AgentID == agent.AgentID && observation.TaskKind == requirement.TaskKind && observation.DomainKey == requirement.DomainKey {
					candidate.SampleCount++
					quality += observation.Quality
				}
			}
			if candidate.SampleCount >= requirement.MinimumSamples {
				candidate.ObservedScore = quality / float64(candidate.SampleCount)
				candidate.Reason = "eligible_with_grouped_history"
			}
		}
		decision.Candidates = append(decision.Candidates, candidate)
	}
	sort.Slice(decision.Candidates, func(i, j int) bool {
		left, right := decision.Candidates[i], decision.Candidates[j]
		if left.Eligible != right.Eligible {
			return left.Eligible
		}
		leftMature, rightMature := left.SampleCount >= requirement.MinimumSamples, right.SampleCount >= requirement.MinimumSamples
		if leftMature != rightMature {
			return leftMature
		}
		if leftMature && left.ObservedScore != right.ObservedScore {
			return left.ObservedScore > right.ObservedScore
		}
		return left.AgentID < right.AgentID
	})
	for _, candidate := range decision.Candidates {
		if candidate.Eligible {
			decision.SelectedAgentID = candidate.AgentID
			return decision, nil
		}
	}
	decision.CapabilityGap, decision.GapReason = true, "no_eligible_agent_for_required_capability_permissions_and_independence"
	return decision, nil
}

func containsCapabilityValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAllCapabilityValues(have, required []string) bool {
	for _, value := range required {
		if !containsCapabilityValue(have, value) {
			return false
		}
	}
	return true
}
