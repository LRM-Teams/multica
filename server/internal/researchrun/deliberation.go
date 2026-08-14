package researchrun

import (
	"fmt"
	"reflect"
	"sort"
)

type DeliberationStatus string

const (
	DeliberationActive           DeliberationStatus = "active"
	DeliberationConsensus        DeliberationStatus = "consensus_proposed"
	DeliberationAwaitingEvidence DeliberationStatus = "awaiting_external_evidence"
	DeliberationDeadlocked       DeliberationStatus = "deadlocked"
)

type DeliberationWatermark struct {
	PositionHashes []string
	EvidenceIDs    []string
	ScopeHashes    []string
}

type DeliberationLimits struct {
	MaximumRounds           int
	MaximumNoProgressRounds int
	MaximumElapsedSeconds   int64
	MaximumTokens           int64
	MaximumToolCalls        int
}

type DeliberationState struct {
	DisputeID           string
	DirectorAgentID     string
	ParticipantAgentIDs []string
	Round               int
	NoProgressRounds    int
	ElapsedSeconds      int64
	TokensUsed          int64
	ToolCallsUsed       int
	Status              DeliberationStatus
	Watermark           DeliberationWatermark
}

type DeliberationTurnInput struct {
	ActorAgentID              string
	ClaimedProgress           string
	NextWatermark             DeliberationWatermark
	ResolutionProposalByAgent map[string]string
	NeedsExternalEvidence     bool
	UnavailableParticipantIDs []string
	ElapsedSeconds            int64
	TokenCost                 int64
	ToolCallCost              int
}

type LeadAdjudicationTask struct {
	TaskKey         string
	DisputeID       string
	AssignedAgentID string
	Reason          string
}

type DeliberationTransition struct {
	State                DeliberationState
	CanonicalProgress    bool
	LeadAdjudicationTask *LeadAdjudicationTask
}

func AdvanceDeliberation(state DeliberationState, turn DeliberationTurnInput, limits DeliberationLimits) (DeliberationTransition, error) {
	if err := validateDeliberationInput(state, turn, limits); err != nil {
		return DeliberationTransition{}, err
	}
	next := state
	next.Round++
	next.ElapsedSeconds += turn.ElapsedSeconds
	next.TokensUsed += turn.TokenCost
	next.ToolCallsUsed += turn.ToolCallCost
	progress := !reflect.DeepEqual(normalizeWatermark(state.Watermark), normalizeWatermark(turn.NextWatermark))
	if progress {
		next.NoProgressRounds = 0
	} else {
		next.NoProgressRounds++
	}
	next.Watermark = normalizeWatermark(turn.NextWatermark)

	if consensusProposal(turn.ResolutionProposalByAgent, state.ParticipantAgentIDs) {
		next.Status = DeliberationConsensus
		return DeliberationTransition{State: next, CanonicalProgress: progress}, nil
	}
	if turn.NeedsExternalEvidence {
		next.Status = DeliberationAwaitingEvidence
		return DeliberationTransition{State: next, CanonicalProgress: progress}, nil
	}

	reason := ""
	switch {
	case len(turn.UnavailableParticipantIDs) > 0:
		reason = "participant_unavailable"
	case next.NoProgressRounds >= limits.MaximumNoProgressRounds:
		reason = "no_canonical_progress"
	case next.Round >= limits.MaximumRounds:
		reason = "round_budget_exhausted"
	case next.ElapsedSeconds >= limits.MaximumElapsedSeconds:
		reason = "time_budget_exhausted"
	case next.TokensUsed >= limits.MaximumTokens:
		reason = "token_budget_exhausted"
	case next.ToolCallsUsed >= limits.MaximumToolCalls:
		reason = "tool_budget_exhausted"
	}
	if reason == "" {
		next.Status = DeliberationActive
		return DeliberationTransition{State: next, CanonicalProgress: progress}, nil
	}
	next.Status = DeliberationDeadlocked
	return DeliberationTransition{
		State:             next,
		CanonicalProgress: progress,
		LeadAdjudicationTask: &LeadAdjudicationTask{
			TaskKey:         "lead-adjudication:" + state.DisputeID,
			DisputeID:       state.DisputeID,
			AssignedAgentID: state.DirectorAgentID,
			Reason:          reason,
		},
	}, nil
}

func validateDeliberationInput(state DeliberationState, turn DeliberationTurnInput, limits DeliberationLimits) error {
	if state.DisputeID == "" || state.DirectorAgentID == "" || len(state.ParticipantAgentIDs) < 2 {
		return fmt.Errorf("%w: deliberation identity, director, and participants are required", ErrInvalidContract)
	}
	if state.Status != DeliberationActive {
		return fmt.Errorf("%w: deliberation is not active", ErrInvalidTransition)
	}
	if limits.MaximumRounds <= 0 || limits.MaximumNoProgressRounds <= 0 || limits.MaximumElapsedSeconds <= 0 || limits.MaximumTokens <= 0 || limits.MaximumToolCalls <= 0 {
		return fmt.Errorf("%w: deliberation limits must be positive", ErrInvalidContract)
	}
	if turn.ElapsedSeconds < 0 || turn.TokenCost < 0 || turn.ToolCallCost < 0 {
		return fmt.Errorf("%w: deliberation cost cannot be negative", ErrInvalidContract)
	}
	if !deliberationContainsString(state.ParticipantAgentIDs, turn.ActorAgentID) {
		return fmt.Errorf("%w: turn actor is not a deliberation participant", ErrInvalidContract)
	}
	for _, unavailableID := range turn.UnavailableParticipantIDs {
		if !deliberationContainsString(state.ParticipantAgentIDs, unavailableID) {
			return fmt.Errorf("%w: unavailable Agent is not a participant", ErrInvalidContract)
		}
	}
	return nil
}

func consensusProposal(proposals map[string]string, participants []string) bool {
	if len(proposals) == 0 {
		return false
	}
	shared := ""
	for _, participantID := range participants {
		proposal := proposals[participantID]
		if proposal == "" {
			return false
		}
		if shared == "" {
			shared = proposal
		} else if proposal != shared {
			return false
		}
	}
	return true
}

func normalizeWatermark(watermark DeliberationWatermark) DeliberationWatermark {
	watermark.PositionHashes = uniqueSortedDeliberationValues(watermark.PositionHashes)
	watermark.EvidenceIDs = uniqueSortedDeliberationValues(watermark.EvidenceIDs)
	watermark.ScopeHashes = uniqueSortedDeliberationValues(watermark.ScopeHashes)
	return watermark
}

func uniqueSortedDeliberationValues(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func deliberationContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
