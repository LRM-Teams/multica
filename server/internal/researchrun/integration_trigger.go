package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

const IntegrationTriggerPolicyVersionV1 = "research-integration-trigger-v1"

type IntegrationTriggerPolicy struct {
	Version                string
	MinimumDistinctAgents  int
	ResultsPerRound        int
	MinimumInformationGain float64
	MaximumCompletedRounds int
	MaximumTotalCostUnits  int64
}

type AcceptedIntegrationResultRef struct {
	ResultID              string
	TaskID                string
	AttemptID             string
	AgentID               string
	ArtifactPassportID    string
	ArtifactVersionID     string
	ArtifactContentHash   string
	AcceptedEventSequence int64
}

type IntegrationTriggerSnapshot struct {
	WorkspaceID                        string
	SessionID                          string
	GoalVersion                        int64
	PlanVersion                        int64
	ThroughEventSequence               int64
	LastIntegratedThroughEventSequence int64
	CompletedRounds                    int
	ReservedCostUnits                  int64
	EstimatedRoundCostUnits            int64
	ActiveRound                        bool
	PendingDelivery                    bool
	BlockingContradiction              bool
	InformationGain                    float64
	AcceptedResults                    []AcceptedIntegrationResultRef
}

type IntegrationTriggerReason string

const (
	IntegrationTriggerNoNewResults       IntegrationTriggerReason = "no_new_results"
	IntegrationTriggerRoundActive        IntegrationTriggerReason = "round_active"
	IntegrationTriggerInsufficientAgents IntegrationTriggerReason = "insufficient_agents"
	IntegrationTriggerThresholdNotMet    IntegrationTriggerReason = "threshold_not_met"
	IntegrationTriggerBudgetExhausted    IntegrationTriggerReason = "budget_exhausted"
	IntegrationTriggerResultThreshold    IntegrationTriggerReason = "result_threshold"
	IntegrationTriggerInformationGain    IntegrationTriggerReason = "information_gain"
	IntegrationTriggerContradiction      IntegrationTriggerReason = "blocking_contradiction"
	IntegrationTriggerDelivery           IntegrationTriggerReason = "pending_delivery"
)

type IntegrationTriggerDecision struct {
	ShouldTrigger bool
	Reason        IntegrationTriggerReason
	RoundKey      string
	InputHash     string
	Inputs        []AcceptedIntegrationResultRef
}

// DecideIntegrationTrigger creates the deterministic scheduling fact for one
// canonical event watermark. A positive decision freezes every accepted Result
// version consumed by the round; retrying the same snapshot yields the same
// RoundKey and cannot create a second logical Integration Round.
func DecideIntegrationTrigger(policy IntegrationTriggerPolicy, snapshot IntegrationTriggerSnapshot) (IntegrationTriggerDecision, error) {
	if err := validateIntegrationTriggerPolicy(policy); err != nil {
		return IntegrationTriggerDecision{}, err
	}
	inputs, err := validateAndNormalizeIntegrationInputs(snapshot)
	if err != nil {
		return IntegrationTriggerDecision{}, err
	}
	decision := IntegrationTriggerDecision{Inputs: inputs}
	if len(inputs) == 0 {
		decision.Reason = IntegrationTriggerNoNewResults
		return decision, nil
	}
	if snapshot.ActiveRound {
		decision.Reason = IntegrationTriggerRoundActive
		return decision, nil
	}
	if snapshot.CompletedRounds >= policy.MaximumCompletedRounds ||
		snapshot.ReservedCostUnits > policy.MaximumTotalCostUnits-snapshot.EstimatedRoundCostUnits {
		decision.Reason = IntegrationTriggerBudgetExhausted
		return decision, nil
	}
	agents := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		agents[input.AgentID] = struct{}{}
	}
	if len(agents) < policy.MinimumDistinctAgents {
		decision.Reason = IntegrationTriggerInsufficientAgents
		return decision, nil
	}
	switch {
	case snapshot.BlockingContradiction:
		decision.Reason = IntegrationTriggerContradiction
	case snapshot.PendingDelivery:
		decision.Reason = IntegrationTriggerDelivery
	case len(inputs) >= policy.ResultsPerRound:
		decision.Reason = IntegrationTriggerResultThreshold
	case snapshot.InformationGain >= policy.MinimumInformationGain:
		decision.Reason = IntegrationTriggerInformationGain
	default:
		decision.Reason = IntegrationTriggerThresholdNotMet
		return decision, nil
	}
	frozen := struct {
		Policy               IntegrationTriggerPolicy
		WorkspaceID          string
		SessionID            string
		GoalVersion          int64
		PlanVersion          int64
		ThroughEventSequence int64
		Inputs               []AcceptedIntegrationResultRef
	}{
		Policy: policy, WorkspaceID: snapshot.WorkspaceID,
		SessionID: snapshot.SessionID, GoalVersion: snapshot.GoalVersion,
		PlanVersion: snapshot.PlanVersion, ThroughEventSequence: snapshot.ThroughEventSequence,
		Inputs: inputs,
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return IntegrationTriggerDecision{}, err
	}
	digest := sha256.Sum256(encoded)
	decision.ShouldTrigger = true
	decision.InputHash = fmt.Sprintf("sha256:%x", digest)
	decision.RoundKey = fmt.Sprintf("integration:%s:%d:%s", snapshot.SessionID, snapshot.ThroughEventSequence, decision.InputHash)
	return decision, nil
}

func validateIntegrationTriggerPolicy(policy IntegrationTriggerPolicy) error {
	if policy.Version != IntegrationTriggerPolicyVersionV1 ||
		policy.MinimumDistinctAgents < 2 || policy.MinimumDistinctAgents > 64 ||
		policy.ResultsPerRound < policy.MinimumDistinctAgents || policy.ResultsPerRound > 512 ||
		math.IsNaN(policy.MinimumInformationGain) || math.IsInf(policy.MinimumInformationGain, 0) ||
		policy.MinimumInformationGain <= 0 || policy.MinimumInformationGain > 1 ||
		policy.MaximumCompletedRounds < 1 || policy.MaximumCompletedRounds > 10_000 ||
		policy.MaximumTotalCostUnits < 1 {
		return fmt.Errorf("%w: Integration Trigger Policy is invalid", ErrInvalidContract)
	}
	return nil
}

func validateAndNormalizeIntegrationInputs(snapshot IntegrationTriggerSnapshot) ([]AcceptedIntegrationResultRef, error) {
	if !validIntegrationIdentity(snapshot.WorkspaceID) || !validIntegrationIdentity(snapshot.SessionID) ||
		snapshot.GoalVersion < 1 || snapshot.PlanVersion < 1 || snapshot.ThroughEventSequence < 1 ||
		snapshot.LastIntegratedThroughEventSequence < 0 ||
		snapshot.LastIntegratedThroughEventSequence > snapshot.ThroughEventSequence ||
		snapshot.CompletedRounds < 0 || snapshot.ReservedCostUnits < 0 || snapshot.EstimatedRoundCostUnits < 1 ||
		math.IsNaN(snapshot.InformationGain) || math.IsInf(snapshot.InformationGain, 0) ||
		snapshot.InformationGain < 0 || snapshot.InformationGain > 1 || len(snapshot.AcceptedResults) > 4096 {
		return nil, fmt.Errorf("%w: Integration Trigger snapshot is invalid", ErrInvalidContract)
	}
	inputs := make([]AcceptedIntegrationResultRef, 0, len(snapshot.AcceptedResults))
	seenResults := make(map[string]struct{}, len(snapshot.AcceptedResults))
	seenSequences := make(map[int64]struct{}, len(snapshot.AcceptedResults))
	for _, input := range snapshot.AcceptedResults {
		if input.AcceptedEventSequence < 1 || input.AcceptedEventSequence > snapshot.ThroughEventSequence ||
			!validIntegrationIdentity(input.ResultID) || !validIntegrationIdentity(input.TaskID) ||
			!validIntegrationIdentity(input.AttemptID) || !validIntegrationIdentity(input.AgentID) ||
			!validIntegrationIdentity(input.ArtifactPassportID) || !validIntegrationIdentity(input.ArtifactVersionID) ||
			!validLowerSHA256(input.ArtifactContentHash) {
			return nil, fmt.Errorf("%w: accepted Integration Result reference is invalid", ErrInvalidContract)
		}
		if _, exists := seenResults[input.ResultID]; exists {
			return nil, fmt.Errorf("%w: Integration Result is duplicated", ErrInvalidContract)
		}
		if _, exists := seenSequences[input.AcceptedEventSequence]; exists {
			return nil, fmt.Errorf("%w: Integration acceptance sequence is duplicated", ErrInvalidContract)
		}
		seenResults[input.ResultID] = struct{}{}
		seenSequences[input.AcceptedEventSequence] = struct{}{}
		if input.AcceptedEventSequence <= snapshot.LastIntegratedThroughEventSequence {
			continue
		}
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].AcceptedEventSequence != inputs[j].AcceptedEventSequence {
			return inputs[i].AcceptedEventSequence < inputs[j].AcceptedEventSequence
		}
		return inputs[i].ResultID < inputs[j].ResultID
	})
	return inputs, nil
}

func validIntegrationIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value
}

func validLowerSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	digest := strings.TrimPrefix(value, "sha256:")
	if digest != strings.ToLower(digest) {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size
}
