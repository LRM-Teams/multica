package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type integrationModule struct{}

type assimilationDecisionCommand struct {
	ResultArtifactID    string
	Routing             string
	RelatedArtifactIDs  []string
	ConflictingClaimIDs []string
	Rationale           string
}

func (integrationModule) ValidateAssimilationDecision(command assimilationDecisionCommand) error {
	if _, err := uuid.Parse(command.ResultArtifactID); err != nil {
		return fmt.Errorf("%w: Assimilation Check result is unresolved", ErrInvalidContract)
	}
	if strings.TrimSpace(command.Rationale) == "" || len(command.Rationale) > 32768 {
		return fmt.Errorf("%w: Assimilation Check requires bounded rationale", ErrInvalidContract)
	}
	if err := validateResolvedUUIDSet("related artifact", command.RelatedArtifactIDs, 256); err != nil {
		return err
	}
	if err := validateResolvedUUIDSet("conflicting Claim", command.ConflictingClaimIDs, 128); err != nil {
		return err
	}
	for _, relatedID := range command.RelatedArtifactIDs {
		if relatedID == command.ResultArtifactID {
			return fmt.Errorf("%w: Assimilation Check cannot compare a Result with itself", ErrInvalidContract)
		}
	}
	switch command.Routing {
	case "no_related_artifacts":
		if len(command.RelatedArtifactIDs) != 0 || len(command.ConflictingClaimIDs) != 0 {
			return fmt.Errorf("%w: no-related routing cannot cite related state", ErrInvalidContract)
		}
	case "peer_synthesis":
		if len(command.RelatedArtifactIDs) == 0 || len(command.ConflictingClaimIDs) != 0 {
			return fmt.Errorf("%w: peer synthesis requires related artifacts without a material conflict", ErrInvalidContract)
		}
	case "open_dispute":
		if len(command.RelatedArtifactIDs) == 0 || len(command.ConflictingClaimIDs) == 0 {
			return fmt.Errorf("%w: dispute routing requires related artifacts and conflicting Claims", ErrInvalidContract)
		}
	default:
		return fmt.Errorf("%w: unknown Assimilation routing %q", ErrInvalidContract, command.Routing)
	}
	return nil
}

type integrationParticipant struct {
	AgentID       string
	Availability  string
	AbsenceReason string
}

type integrationArtifactState struct {
	ID            string
	Kind          string
	ContentHash   string
	TaskID        string
	BranchID      string
	AuthorAgentID string
	InsightLevel  int
	Status        string
	Accessible    bool
}

type integrationRoundContext struct {
	RoundID              string
	ThroughEventSequence int64
	StateVersion         int64
	Artifacts            []integrationArtifactState
	Participants         []integrationParticipant
}

type integrationContributionCommand struct {
	ClientKey           string
	IntegrationRoundID  string
	AuthorAgentID       string
	ComparedArtifactIDs []string
	CommonFindings      []string
	UniqueFindings      []string
	Conflicts           []string
	Scope               json.RawMessage
	Omissions           []string
	ProposedInsights    []string
	FollowUpQuestions   []QuestionProposal
}

type insightProposalCommand struct {
	ClientKey     string
	Title         string
	Summary       string
	InputIDs      []string
	Relation      string
	Scope         json.RawMessage
	SemanticValue string
}

type integrationBatchCommand struct {
	RoundID               string
	ExpectedEventSequence int64
	ExpectedStateVersion  int64
	Contributions         []integrationContributionCommand
	Insights              []insightProposalCommand
}

type acceptedInsightDerivation struct {
	ClientKey      string
	Level          int
	IdempotencyKey string
	InputIDs       []string
	InputHashes    []string
	Relation       string
	Scope          json.RawMessage
	SemanticValue  string
}

type integrationBatchValidation struct {
	Derivations []acceptedInsightDerivation
}

func (integrationModule) ValidateBatch(command integrationBatchCommand, context integrationRoundContext) (integrationBatchValidation, error) {
	if _, err := uuid.Parse(context.RoundID); err != nil || command.RoundID != context.RoundID {
		return integrationBatchValidation{}, fmt.Errorf("%w: Integration Round is unresolved", ErrInvalidContract)
	}
	if context.ThroughEventSequence < 1 || context.StateVersion < 1 || command.ExpectedEventSequence != context.ThroughEventSequence || command.ExpectedStateVersion != context.StateVersion {
		return integrationBatchValidation{}, fmt.Errorf("%w: Integration Round input watermark changed", ErrControlTargetChanged)
	}
	artifacts, err := indexIntegrationArtifacts(context.Artifacts)
	if err != nil {
		return integrationBatchValidation{}, err
	}
	participants, err := indexIntegrationParticipants(context.Participants)
	if err != nil {
		return integrationBatchValidation{}, err
	}
	if len(command.Contributions) > 64 || len(command.Insights) > 64 {
		return integrationBatchValidation{}, fmt.Errorf("%w: Integration batch exceeds frozen V6 limits", ErrInvalidContract)
	}

	seenContributions := map[string]struct{}{}
	contributingAgents := map[string]struct{}{}
	for _, contribution := range command.Contributions {
		if err := validateKey("integration_contribution.client_key", contribution.ClientKey); err != nil {
			return integrationBatchValidation{}, err
		}
		if _, duplicate := seenContributions[contribution.ClientKey]; duplicate {
			return integrationBatchValidation{}, fmt.Errorf("%w: duplicate Integration Contribution key", ErrInvalidContract)
		}
		seenContributions[contribution.ClientKey] = struct{}{}
		participant, ok := participants[contribution.AuthorAgentID]
		if !ok || participant.Availability != "available" {
			return integrationBatchValidation{}, fmt.Errorf("%w: unavailable Agent cannot author an Integration Contribution", ErrInvalidContract)
		}
		if _, duplicate := contributingAgents[contribution.AuthorAgentID]; duplicate {
			return integrationBatchValidation{}, fmt.Errorf("%w: Agent repeated its Integration Contribution", ErrInvalidContract)
		}
		contributingAgents[contribution.AuthorAgentID] = struct{}{}
		if contribution.IntegrationRoundID != context.RoundID || !isIntegrationJSONObject(contribution.Scope) || len(contribution.ComparedArtifactIDs) == 0 {
			return integrationBatchValidation{}, fmt.Errorf("%w: Integration Contribution is not bound to its Round", ErrInvalidContract)
		}
		if err := validateContributionText(contribution); err != nil {
			return integrationBatchValidation{}, err
		}
		authoredInput := false
		seenArtifacts := map[string]struct{}{}
		for _, artifactID := range contribution.ComparedArtifactIDs {
			artifact, ok := artifacts[artifactID]
			if !ok || !artifact.Accessible || !integrationInputAccepted(artifact.Status) {
				return integrationBatchValidation{}, fmt.Errorf("%w: Contribution references unavailable or unaccepted Artifact", ErrInvalidContract)
			}
			if _, duplicate := seenArtifacts[artifactID]; duplicate {
				return integrationBatchValidation{}, fmt.Errorf("%w: Contribution repeats an Artifact", ErrInvalidContract)
			}
			seenArtifacts[artifactID] = struct{}{}
			authoredInput = authoredInput || artifact.AuthorAgentID == contribution.AuthorAgentID
		}
		if !authoredInput {
			return integrationBatchValidation{}, fmt.Errorf("%w: Contribution author has no accepted input in the comparison", ErrInvalidContract)
		}
	}

	validation := integrationBatchValidation{}
	seenInsights := map[string]struct{}{}
	seenIdentities := map[string]struct{}{}
	for _, insight := range command.Insights {
		derivation, err := validateInsightProposal(insight, artifacts)
		if err != nil {
			return integrationBatchValidation{}, err
		}
		if _, duplicate := seenInsights[insight.ClientKey]; duplicate {
			return integrationBatchValidation{}, fmt.Errorf("%w: duplicate Insight key", ErrInvalidContract)
		}
		seenInsights[insight.ClientKey] = struct{}{}
		if _, duplicate := seenIdentities[derivation.IdempotencyKey]; duplicate {
			return integrationBatchValidation{}, fmt.Errorf("%w: duplicate Insight derivation identity", ErrInvalidContract)
		}
		seenIdentities[derivation.IdempotencyKey] = struct{}{}
		validation.Derivations = append(validation.Derivations, derivation)
	}
	sort.Slice(validation.Derivations, func(i, j int) bool { return validation.Derivations[i].ClientKey < validation.Derivations[j].ClientKey })
	return validation, nil
}

func indexIntegrationArtifacts(values []integrationArtifactState) (map[string]integrationArtifactState, error) {
	result := make(map[string]integrationArtifactState, len(values))
	for _, artifact := range values {
		if _, err := uuid.Parse(artifact.ID); err != nil || !validIntegrationHash(artifact.ContentHash) || artifact.InsightLevel < 0 {
			return nil, fmt.Errorf("%w: Integration input Artifact is invalid", ErrInvalidContract)
		}
		if artifact.TaskID == "" && artifact.BranchID == "" {
			return nil, fmt.Errorf("%w: Integration input has no Task or Branch provenance", ErrInvalidContract)
		}
		for _, id := range []string{artifact.TaskID, artifact.BranchID, artifact.AuthorAgentID} {
			if id != "" {
				if _, err := uuid.Parse(id); err != nil {
					return nil, fmt.Errorf("%w: Integration input provenance is unresolved", ErrInvalidContract)
				}
			}
		}
		if _, duplicate := result[artifact.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Integration input Artifact", ErrInvalidContract)
		}
		result[artifact.ID] = artifact
	}
	return result, nil
}

func indexIntegrationParticipants(values []integrationParticipant) (map[string]integrationParticipant, error) {
	result := make(map[string]integrationParticipant, len(values))
	for _, participant := range values {
		if _, err := uuid.Parse(participant.AgentID); err != nil {
			return nil, fmt.Errorf("%w: Integration participant is unresolved", ErrInvalidContract)
		}
		switch participant.Availability {
		case "available":
			if participant.AbsenceReason != "" {
				return nil, fmt.Errorf("%w: available participant cannot have absence reason", ErrInvalidContract)
			}
		case "offline", "exited", "access_denied":
			if strings.TrimSpace(participant.AbsenceReason) == "" {
				return nil, fmt.Errorf("%w: unavailable participant requires absence reason", ErrInvalidContract)
			}
		default:
			return nil, fmt.Errorf("%w: Integration participant availability is invalid", ErrInvalidContract)
		}
		if _, duplicate := result[participant.AgentID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Integration participant", ErrInvalidContract)
		}
		result[participant.AgentID] = participant
	}
	return result, nil
}

func validateContributionText(contribution integrationContributionCommand) error {
	if len(contribution.CommonFindings)+len(contribution.UniqueFindings)+len(contribution.Conflicts) == 0 {
		return fmt.Errorf("%w: Integration Contribution has no comparison findings", ErrInvalidContract)
	}
	for name, values := range map[string][]string{"common_findings": contribution.CommonFindings, "unique_findings": contribution.UniqueFindings, "conflicts": contribution.Conflicts, "omissions": contribution.Omissions, "proposed_insights": contribution.ProposedInsights} {
		if err := validateStringList("integration_contribution."+name, values); err != nil {
			return err
		}
	}
	seenQuestions := map[string]struct{}{}
	for _, question := range contribution.FollowUpQuestions {
		if err := validateQuestion(question, seenQuestions); err != nil {
			return err
		}
	}
	return nil
}

func validateInsightProposal(insight insightProposalCommand, artifacts map[string]integrationArtifactState) (acceptedInsightDerivation, error) {
	if err := validateKey("insight.client_key", insight.ClientKey); err != nil {
		return acceptedInsightDerivation{}, err
	}
	if strings.TrimSpace(insight.Title) == "" || len(insight.Title) > 4096 || strings.TrimSpace(insight.Summary) == "" || len(insight.Summary) > 32768 || !isIntegrationJSONObject(insight.Scope) {
		return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight %q content or scope is invalid", ErrInvalidContract, insight.ClientKey)
	}
	switch insight.Relation {
	case "integrates", "explains", "conditions", "resolves", "distinguishes":
	default:
		return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight relation is invalid", ErrInvalidContract)
	}
	switch insight.SemanticValue {
	case "new_explanation", "deduplication", "conflict_resolution", "hypothesis_change", "frontier_change", "report_change", "lossless_compression":
	default:
		return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight has no declared semantic value", ErrInvalidContract)
	}
	if len(insight.InputIDs) < 2 || len(insight.InputIDs) > 128 {
		return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight requires two to 128 inputs", ErrInvalidContract)
	}
	seenInputs := map[string]struct{}{}
	taskOrigins, branchOrigins := map[string]struct{}{}, map[string]struct{}{}
	inputIDs, hashes := append([]string(nil), insight.InputIDs...), make([]string, 0, len(insight.InputIDs))
	level := 1
	for _, inputID := range insight.InputIDs {
		artifact, ok := artifacts[inputID]
		if !ok || !artifact.Accessible || !integrationInputAccepted(artifact.Status) {
			return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight references unavailable or unaccepted input", ErrInvalidContract)
		}
		if artifact.Kind != "claim" && artifact.Kind != "insight" || artifact.Kind == "claim" && artifact.InsightLevel != 0 || artifact.Kind == "insight" && artifact.InsightLevel < 1 {
			return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight input kind or level is invalid", ErrInvalidContract)
		}
		if _, duplicate := seenInputs[inputID]; duplicate {
			return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight repeats an input", ErrInvalidContract)
		}
		seenInputs[inputID] = struct{}{}
		if artifact.TaskID != "" {
			taskOrigins[artifact.TaskID] = struct{}{}
		}
		if artifact.BranchID != "" {
			branchOrigins[artifact.BranchID] = struct{}{}
		}
		if artifact.InsightLevel+1 > level {
			level = artifact.InsightLevel + 1
		}
		hashes = append(hashes, artifact.ContentHash)
	}
	if len(taskOrigins) < 2 && len(branchOrigins) < 2 {
		return acceptedInsightDerivation{}, fmt.Errorf("%w: Insight inputs must span distinct Tasks or Branches", ErrInvalidContract)
	}
	sort.Strings(inputIDs)
	sort.Strings(hashes)
	canonicalScope, _ := canonicalIntegrationJSON(insight.Scope)
	identityBytes, _ := json.Marshal(map[string]any{"input_hashes": hashes, "relation": insight.Relation, "scope": json.RawMessage(canonicalScope)})
	sum := sha256.Sum256(identityBytes)
	return acceptedInsightDerivation{ClientKey: insight.ClientKey, Level: level, IdempotencyKey: hex.EncodeToString(sum[:]), InputIDs: inputIDs, InputHashes: hashes, Relation: insight.Relation, Scope: canonicalScope, SemanticValue: insight.SemanticValue}, nil
}

func integrationInputAccepted(status string) bool {
	return status == "accepted" || status == "supported"
}

func validIntegrationHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func isIntegrationJSONObject(raw json.RawMessage) bool {
	_, err := canonicalIntegrationJSON(raw)
	return err == nil
}

func canonicalIntegrationJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, fmt.Errorf("not an object")
	}
	return json.Marshal(value)
}

func validateResolvedUUIDSet(name string, values []string, limit int) error {
	if len(values) > limit {
		return fmt.Errorf("%w: %s set exceeds limit", ErrInvalidContract, name)
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: %s is unresolved", ErrInvalidContract, name)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidContract, name)
		}
		seen[value] = struct{}{}
	}
	return nil
}
