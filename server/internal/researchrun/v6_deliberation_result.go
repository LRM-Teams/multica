package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

type V6DeliberationDelta struct {
	PositionHashes []string      `json:"position_hashes"`
	EvidenceRefs   []V6EntityRef `json:"evidence_refs"`
	ScopeHashes    []string      `json:"scope_hashes"`
}

type V6DeliberationTurn struct {
	ActorAgentID              string              `json:"actor_agent_id"`
	Statement                 string              `json:"statement"`
	Scope                     map[string]any      `json:"scope"`
	ClaimRefs                 []V6EntityRef       `json:"claim_refs"`
	EvidenceRefs              []V6EntityRef       `json:"evidence_refs"`
	Challenge                 string              `json:"challenge"`
	Concession                string              `json:"concession"`
	ProposedAction            map[string]any      `json:"proposed_action"`
	CanonicalDelta            V6DeliberationDelta `json:"canonical_delta"`
	ResolutionProposalByAgent map[string]string   `json:"resolution_proposal_by_agent"`
	NeedsExternalEvidence     bool                `json:"needs_external_evidence"`
	UnavailableParticipantIDs []string            `json:"unavailable_participant_ids"`
	ElapsedSeconds            int64               `json:"elapsed_seconds"`
	TokenCost                 int64               `json:"token_cost"`
	ToolCallCost              int                 `json:"tool_call_cost"`
}

type V6DeliberationResult struct {
	Envelope     V6IntegrationResult
	Dispute      V6DisputeProposal
	Turns        []V6DeliberationTurn
	Adjudication *V6DirectorAdjudication
}

type V6AdjudicationAssessment struct {
	PositionID   string        `json:"position_id"`
	Disposition  string        `json:"disposition"`
	Rationale    string        `json:"rationale"`
	EvidenceRefs []V6EntityRef `json:"evidence_refs"`
}

type V6DirectorAdjudication struct {
	ActorAgentID            string                     `json:"actor_agent_id"`
	DirectorIdentityVersion int                        `json:"director_identity_version"`
	Decision                string                     `json:"decision"`
	Rationale               string                     `json:"rationale"`
	Conditions              []string                   `json:"conditions"`
	ResidualUncertainty     string                     `json:"residual_uncertainty"`
	PositionAssessments     []V6AdjudicationAssessment `json:"position_assessments"`
}

func DecodeAndValidateV6DeliberationResult(raw []byte) (V6DeliberationResult, string, error) {
	if err := validateV6IntegrationRequiredShape(raw); err != nil {
		return V6DeliberationResult{}, "", err
	}
	var envelope V6IntegrationResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: decode V6 deliberation result: %v", ErrInvalidResult, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: V6 deliberation result has trailing JSON", ErrInvalidResult)
	}
	if envelope.ContractKind != "task_result" || envelope.SchemaVersion != 6 || envelope.QueryExecutions == nil || envelope.SourceCandidates == nil ||
		envelope.StatusUpdates == nil || envelope.IntegrationContributions == nil || envelope.Insights == nil || envelope.Disputes == nil || envelope.ProposedTasks == nil {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: V6 deliberation result requires the complete task_result envelope", ErrInvalidResult)
	}
	if _, err := uuid.Parse(envelope.ClientRequestID); err != nil || strings.TrimSpace(envelope.Summary) == "" || !unitInterval(envelope.Confidence) {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: V6 deliberation result identity or summary is invalid", ErrInvalidResult)
	}
	if len(envelope.QueryExecutions) != 0 || len(envelope.SourceCandidates) != 0 || len(envelope.IntegrationContributions) != 0 || len(envelope.Insights) != 0 ||
		len(envelope.Disputes) != 1 || len(envelope.ProposedTasks) != 0 || presentJSON(envelope.Divergence) || presentJSON(envelope.Report) || presentJSON(envelope.Evaluation) {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: deliberation result contains fields owned by another task kind", ErrInvalidResult)
	}
	isAdjudication := len(envelope.Disputes[0].Positions) == 1 && envelope.Disputes[0].Positions[0]["decision"] != nil
	if !isAdjudication {
		if err := envelope.Disputes[0].validate(0, map[string]struct{}{}); err != nil {
			return V6DeliberationResult{}, "", err
		}
	} else if err := validateV6Key("dispute.client_key", envelope.Disputes[0].ClientKey); err != nil {
		return V6DeliberationResult{}, "", err
	} else if err := validateV6Ref("dispute.subject", envelope.Disputes[0].Subject); err != nil {
		return V6DeliberationResult{}, "", err
	}
	if len(envelope.StatusUpdates) != 1 || envelope.StatusUpdates[0].Target.Kind != "dispute" || envelope.StatusUpdates[0].Target.Key != envelope.Disputes[0].ClientKey {
		return V6DeliberationResult{}, "", fmt.Errorf("%w: deliberation must transition exactly its assigned Dispute", ErrInvalidResult)
	}
	if err := envelope.StatusUpdates[0].validate(0); err != nil {
		return V6DeliberationResult{}, "", err
	}
	if isAdjudication {
		if _, adjudication := envelope.Disputes[0].Positions[0]["decision"]; adjudication {
			value, err := decodeV6DirectorAdjudication(envelope.Disputes[0].Positions[0])
			if err != nil {
				return V6DeliberationResult{}, "", err
			}
			canonical, err := MarshalArtifactCanonicalJSON(json.RawMessage(raw))
			if err != nil {
				return V6DeliberationResult{}, "", err
			}
			return V6DeliberationResult{Envelope: envelope, Dispute: envelope.Disputes[0], Adjudication: &value}, ArtifactContentHashFromCanonicalJSON(canonical), nil
		}
	}
	turns := make([]V6DeliberationTurn, 0, len(envelope.Disputes[0].Positions))
	seenActors := map[string]bool{}
	for index, position := range envelope.Disputes[0].Positions {
		rawPosition, err := json.Marshal(position)
		if err != nil {
			return V6DeliberationResult{}, "", err
		}
		var positionShape map[string]json.RawMessage
		if err = json.Unmarshal(rawPosition, &positionShape); err != nil {
			return V6DeliberationResult{}, "", err
		}
		if err = requireV6Fields(fmt.Sprintf("deliberation position[%d]", index), positionShape,
			"actor_agent_id", "statement", "scope", "claim_refs", "evidence_refs", "challenge", "concession", "proposed_action",
			"canonical_delta", "resolution_proposal_by_agent", "needs_external_evidence", "unavailable_participant_ids",
			"elapsed_seconds", "token_cost", "tool_call_cost"); err != nil {
			return V6DeliberationResult{}, "", err
		}
		var turn V6DeliberationTurn
		turnDecoder := json.NewDecoder(bytes.NewReader(rawPosition))
		turnDecoder.DisallowUnknownFields()
		if err = turnDecoder.Decode(&turn); err != nil {
			return V6DeliberationResult{}, "", fmt.Errorf("%w: deliberation position[%d]: %v", ErrInvalidResult, index, err)
		}
		if err = validateV6DeliberationTurn(turn); err != nil {
			return V6DeliberationResult{}, "", fmt.Errorf("position[%d]: %w", index, err)
		}
		if seenActors[turn.ActorAgentID] {
			return V6DeliberationResult{}, "", fmt.Errorf("%w: deliberation batch repeats an actor", ErrInvalidResult)
		}
		seenActors[turn.ActorAgentID] = true
		turns = append(turns, turn)
	}
	canonical, err := MarshalArtifactCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return V6DeliberationResult{}, "", err
	}
	return V6DeliberationResult{Envelope: envelope, Dispute: envelope.Disputes[0], Turns: turns}, ArtifactContentHashFromCanonicalJSON(canonical), nil
}

func decodeV6DirectorAdjudication(position map[string]any) (V6DirectorAdjudication, error) {
	raw, err := json.Marshal(position)
	if err != nil {
		return V6DirectorAdjudication{}, err
	}
	var shape map[string]json.RawMessage
	if err = json.Unmarshal(raw, &shape); err != nil {
		return V6DirectorAdjudication{}, err
	}
	if err = requireV6Fields("director adjudication", shape, "actor_agent_id", "director_identity_version", "decision", "rationale", "conditions", "residual_uncertainty", "position_assessments"); err != nil {
		return V6DirectorAdjudication{}, err
	}
	var value V6DirectorAdjudication
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("%w: director adjudication: %v", ErrInvalidResult, err)
	}
	if _, err = uuid.Parse(value.ActorAgentID); err != nil || value.DirectorIdentityVersion < 1 || strings.TrimSpace(value.Rationale) == "" || len(value.Rationale) > 32768 || value.Conditions == nil || value.PositionAssessments == nil || len(value.PositionAssessments) == 0 || len(value.ResidualUncertainty) > 32768 {
		return value, fmt.Errorf("%w: director adjudication identity or content is invalid", ErrInvalidResult)
	}
	if value.Decision != "resolved" && value.Decision != "conditionally_resolved" && value.Decision != "irreducible" {
		return value, fmt.Errorf("%w: director adjudication decision is invalid", ErrInvalidResult)
	}
	seen := map[string]bool{}
	for i, assessment := range value.PositionAssessments {
		if _, err = uuid.Parse(assessment.PositionID); err != nil || seen[assessment.PositionID] || strings.TrimSpace(assessment.Rationale) == "" || len(assessment.EvidenceRefs) == 0 {
			return value, fmt.Errorf("%w: director adjudication assessment[%d] is invalid", ErrInvalidResult, i)
		}
		seen[assessment.PositionID] = true
		if assessment.Disposition != "retained" && assessment.Disposition != "conditioned" && assessment.Disposition != "rejected" {
			return value, fmt.Errorf("%w: director adjudication assessment[%d] disposition is invalid", ErrInvalidResult, i)
		}
		if err = validateV6Refs("director adjudication evidence_refs", assessment.EvidenceRefs, 1, 128); err != nil {
			return value, err
		}
	}
	if value.Decision == "conditionally_resolved" && len(value.Conditions) == 0 {
		return value, fmt.Errorf("%w: conditional adjudication requires conditions", ErrInvalidResult)
	}
	return value, nil
}

func validateV6DeliberationTurn(turn V6DeliberationTurn) error {
	if _, err := uuid.Parse(turn.ActorAgentID); err != nil || strings.TrimSpace(turn.Statement) == "" || len(turn.Statement) > 32768 || turn.Scope == nil || turn.ProposedAction == nil {
		return fmt.Errorf("%w: deliberation turn identity or content is invalid", ErrInvalidResult)
	}
	if turn.ClaimRefs == nil || turn.EvidenceRefs == nil || turn.CanonicalDelta.PositionHashes == nil || turn.CanonicalDelta.EvidenceRefs == nil || turn.CanonicalDelta.ScopeHashes == nil ||
		turn.ResolutionProposalByAgent == nil || turn.UnavailableParticipantIDs == nil || turn.ElapsedSeconds < 0 || turn.TokenCost < 0 || turn.ToolCallCost < 0 {
		return fmt.Errorf("%w: deliberation turn misses required collections or costs", ErrInvalidResult)
	}
	if len(turn.Challenge) > 32768 || len(turn.Concession) > 32768 {
		return fmt.Errorf("%w: deliberation turn text or cost is invalid", ErrInvalidResult)
	}
	if err := validateV6Refs("deliberation claim_refs", turn.ClaimRefs, 1, 128); err != nil {
		return err
	}
	for _, ref := range turn.ClaimRefs {
		if ref.Kind != "claim" {
			return fmt.Errorf("%w: deliberation claim_refs must reference Claims", ErrInvalidResult)
		}
	}
	for _, refs := range [][]V6EntityRef{turn.EvidenceRefs, turn.CanonicalDelta.EvidenceRefs} {
		for _, ref := range refs {
			if err := validateV6Ref("deliberation evidence", ref); err != nil {
				return err
			}
		}
	}
	for _, hash := range append(append([]string{}, turn.CanonicalDelta.PositionHashes...), turn.CanonicalDelta.ScopeHashes...) {
		if !validLowerSHA256(hash) {
			return fmt.Errorf("%w: deliberation watermark hash is invalid", ErrInvalidResult)
		}
	}
	for agentID, proposalHash := range turn.ResolutionProposalByAgent {
		if _, err := uuid.Parse(agentID); err != nil || !validLowerSHA256(proposalHash) {
			return fmt.Errorf("%w: deliberation resolution proposal is invalid", ErrInvalidResult)
		}
	}
	for _, agentID := range turn.UnavailableParticipantIDs {
		if _, err := uuid.Parse(agentID); err != nil {
			return fmt.Errorf("%w: unavailable deliberation participant is invalid", ErrInvalidResult)
		}
	}
	return nil
}

func researchV6DeliberationEnvelope(result V6DeliberationResult) ResultEnvelope {
	return researchV6IntegrationEnvelope(result.Envelope)
}
