package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type DivergenceTrigger string

const (
	DivergenceOpening          DivergenceTrigger = "opening"
	DivergenceMaterialSurprise DivergenceTrigger = "material_surprise"
	DivergenceLowGain          DivergenceTrigger = "low_gain"
	DivergencePreDelivery      DivergenceTrigger = "pre_delivery"
)

type DivergenceContextInput struct {
	ContractVersion    int
	PlanVersion        int
	Contract           json.RawMessage
	Method             json.RawMessage
	SafetyLimits       json.RawMessage
	BudgetLimits       json.RawMessage
	KnownFactIDs       []string
	ConclusionIDs      []string
	RankingIDs         []string
	AgentEvaluationIDs []string
}

type DivergenceContext struct {
	ContractVersion int             `json:"contract_version"`
	PlanVersion     int             `json:"plan_version"`
	Contract        json.RawMessage `json:"contract"`
	Method          json.RawMessage `json:"method"`
	SafetyLimits    json.RawMessage `json:"safety_limits"`
	BudgetLimits    json.RawMessage `json:"budget_limits"`
	KnownFactIDs    []string        `json:"known_fact_ids"`
	ContextHash     string          `json:"context_hash"`
}

// BuildDivergenceContext deliberately has no output fields for conclusions,
// rankings, or Agent evaluations. Non-empty forbidden inputs fail closed so a
// caller cannot accidentally anchor the isolated pass.
func BuildDivergenceContext(input DivergenceContextInput) (DivergenceContext, error) {
	if input.ContractVersion <= 0 || input.PlanVersion <= 0 || len(input.Contract) == 0 || len(input.Method) == 0 {
		return DivergenceContext{}, fmt.Errorf("%w: divergence context requires current Contract and Method", ErrInvalidContract)
	}
	if len(input.ConclusionIDs) > 0 || len(input.RankingIDs) > 0 || len(input.AgentEvaluationIDs) > 0 {
		return DivergenceContext{}, fmt.Errorf("%w: isolated divergence context contains forbidden anchoring inputs", ErrInvalidContract)
	}
	context := DivergenceContext{
		ContractVersion: input.ContractVersion, PlanVersion: input.PlanVersion,
		Contract: input.Contract, Method: input.Method, SafetyLimits: input.SafetyLimits,
		BudgetLimits: input.BudgetLimits, KnownFactIDs: uniqueSortedDivergenceStrings(input.KnownFactIDs),
	}
	canonical, err := json.Marshal(struct {
		ContractVersion int             `json:"contract_version"`
		PlanVersion     int             `json:"plan_version"`
		Contract        json.RawMessage `json:"contract"`
		Method          json.RawMessage `json:"method"`
		SafetyLimits    json.RawMessage `json:"safety_limits"`
		BudgetLimits    json.RawMessage `json:"budget_limits"`
		KnownFactIDs    []string        `json:"known_fact_ids"`
	}{context.ContractVersion, context.PlanVersion, context.Contract, context.Method, context.SafetyLimits, context.BudgetLimits, context.KnownFactIDs})
	if err != nil {
		return DivergenceContext{}, fmt.Errorf("%w: canonicalize divergence context: %v", ErrInvalidContract, err)
	}
	hash := sha256.Sum256(canonical)
	context.ContextHash = "sha256:" + hex.EncodeToString(hash[:])
	return context, nil
}

type DivergenceCandidateDisposition struct {
	CandidateID     string
	HighImpact      bool
	Selected        bool
	RejectionReason string
}

type DivergencePassRecord struct {
	Trigger               DivergenceTrigger
	ContractVersion       int
	PlanVersion           int
	ContextHash           string
	CandidateDispositions []DivergenceCandidateDisposition
}

func ValidateDivergencePass(record DivergencePassRecord) error {
	if !validDivergenceTrigger(record.Trigger) || record.ContractVersion <= 0 || record.PlanVersion <= 0 || len(record.ContextHash) != len("sha256:")+64 || !strings.HasPrefix(record.ContextHash, "sha256:") {
		return fmt.Errorf("%w: divergence pass identity is invalid", ErrInvalidContract)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(record.ContextHash, "sha256:")); err != nil {
		return fmt.Errorf("%w: divergence context hash is invalid", ErrInvalidContract)
	}
	seen := make(map[string]struct{}, len(record.CandidateDispositions))
	for _, disposition := range record.CandidateDispositions {
		if disposition.CandidateID == "" {
			return fmt.Errorf("%w: divergence candidate id is required", ErrInvalidContract)
		}
		if _, exists := seen[disposition.CandidateID]; exists {
			return fmt.Errorf("%w: duplicate divergence candidate %q", ErrInvalidContract, disposition.CandidateID)
		}
		seen[disposition.CandidateID] = struct{}{}
		if !disposition.Selected && disposition.RejectionReason == "" {
			return fmt.Errorf("%w: rejected divergence candidate %q requires a reason", ErrInvalidContract, disposition.CandidateID)
		}
	}
	return nil
}

func CheckPreDeliveryDivergence(goalVersion, planVersion int, passes []DivergencePassRecord) error {
	for index := len(passes) - 1; index >= 0; index-- {
		pass := passes[index]
		if pass.Trigger != DivergencePreDelivery || pass.ContractVersion != goalVersion || pass.PlanVersion != planVersion {
			continue
		}
		if err := ValidateDivergencePass(pass); err != nil {
			return err
		}
		for _, disposition := range pass.CandidateDispositions {
			if disposition.HighImpact && !disposition.Selected && disposition.RejectionReason == "" {
				return fmt.Errorf("%w: high-impact divergence candidate %q is unhandled", ErrInvalidTransition, disposition.CandidateID)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: current Contract and Plan lack a pre-delivery divergence pass", ErrInvalidTransition)
}

func validDivergenceTrigger(trigger DivergenceTrigger) bool {
	switch trigger {
	case DivergenceOpening, DivergenceMaterialSurprise, DivergenceLowGain, DivergencePreDelivery:
		return true
	default:
		return false
	}
}

func uniqueSortedDivergenceStrings(values []string) []string {
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
