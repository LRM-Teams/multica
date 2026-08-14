package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const ScreeningPolicyVersionV1 = "research-screening-v1"

type ScreeningPolicy struct {
	Version  string
	Criteria []ScreeningCriterion
}

type ScreeningCriterion struct {
	ID   string
	Kind string
	Text string
}

type ScreeningAssessment struct {
	CandidateID          string
	Disposition          string
	MatchedCriterionIDs  []string
	Reason               string
	CanonicalCandidateID string
	ReviewerKind         string
	ReviewerID           string
	ReviewedAt           time.Time
	Facts                []ScreeningFact
}

type ScreeningFact struct {
	Kind    string
	Value   string
	Locator string
}

type ValidatedScreeningDecision struct {
	Assessment  ScreeningAssessment
	Fingerprint string
}

// ValidateScreeningDecision turns one screening judgment into a bounded,
// deterministic fact. The caller persists the returned normalized assessment
// and fingerprint; prose alone is never enough to accept or exclude a source.
func ValidateScreeningDecision(policy ScreeningPolicy, assessment ScreeningAssessment) (ValidatedScreeningDecision, error) {
	criteria, err := validateScreeningPolicy(policy)
	if err != nil {
		return ValidatedScreeningDecision{}, err
	}
	if !validScreeningToken(assessment.CandidateID, 512) || !validScreeningToken(assessment.ReviewerID, 512) || assessment.ReviewedAt.IsZero() {
		return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision identity is invalid", ErrInvalidContract)
	}
	if assessment.ReviewerKind != "agent" && assessment.ReviewerKind != "user" && assessment.ReviewerKind != "system" {
		return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision reviewer kind is invalid", ErrInvalidContract)
	}
	if strings.TrimSpace(assessment.Reason) != assessment.Reason || substantiveRuneCount(assessment.Reason) < 8 || len(assessment.Reason) > 4096 {
		return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision reason is not auditable", ErrInvalidContract)
	}
	if len(assessment.MatchedCriterionIDs) > 128 || len(assessment.Facts) == 0 || len(assessment.Facts) > 128 {
		return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision evidence is empty or oversized", ErrInvalidContract)
	}
	normalized := assessment
	normalized.ReviewedAt = assessment.ReviewedAt.UTC()
	normalized.MatchedCriterionIDs = append([]string(nil), assessment.MatchedCriterionIDs...)
	sort.Strings(normalized.MatchedCriterionIDs)
	seenCriteria := map[string]bool{}
	inclusions, exclusions := 0, 0
	for _, criterionID := range normalized.MatchedCriterionIDs {
		criterion, ok := criteria[criterionID]
		if !ok || seenCriteria[criterionID] {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision references unknown or duplicate criterion", ErrInvalidContract)
		}
		seenCriteria[criterionID] = true
		if criterion.Kind == "inclusion" {
			inclusions++
		} else {
			exclusions++
		}
	}
	normalized.Facts = append([]ScreeningFact(nil), assessment.Facts...)
	sort.Slice(normalized.Facts, func(i, j int) bool {
		if normalized.Facts[i].Kind != normalized.Facts[j].Kind {
			return normalized.Facts[i].Kind < normalized.Facts[j].Kind
		}
		if normalized.Facts[i].Locator != normalized.Facts[j].Locator {
			return normalized.Facts[i].Locator < normalized.Facts[j].Locator
		}
		return normalized.Facts[i].Value < normalized.Facts[j].Value
	})
	seenFacts := map[string]bool{}
	hasDuplicateFact := false
	for _, fact := range normalized.Facts {
		if !validScreeningFact(fact) {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision fact is invalid", ErrInvalidContract)
		}
		key := fact.Kind + "\x00" + fact.Locator + "\x00" + fact.Value
		if seenFacts[key] {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision repeats a fact", ErrInvalidContract)
		}
		seenFacts[key] = true
		if fact.Kind == "content_hash" || fact.Kind == "canonical_url" {
			hasDuplicateFact = true
		}
	}
	switch assessment.Disposition {
	case "accepted":
		if inclusions == 0 || exclusions != 0 || assessment.CanonicalCandidateID != "" {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: accepted candidate lacks a clean inclusion match", ErrInvalidContract)
		}
	case "excluded":
		if exclusions == 0 || assessment.CanonicalCandidateID != "" {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: excluded candidate lacks an exclusion match", ErrInvalidContract)
		}
	case "duplicate":
		if len(normalized.MatchedCriterionIDs) != 0 || !validScreeningToken(assessment.CanonicalCandidateID, 512) || assessment.CanonicalCandidateID == assessment.CandidateID || !hasDuplicateFact {
			return ValidatedScreeningDecision{}, fmt.Errorf("%w: duplicate candidate lacks canonical identity evidence", ErrInvalidContract)
		}
	default:
		return ValidatedScreeningDecision{}, fmt.Errorf("%w: Screening Decision disposition is invalid", ErrInvalidContract)
	}
	fingerprintInput := struct {
		Policy     ScreeningPolicy
		Assessment ScreeningAssessment
	}{Policy: normalizedScreeningPolicy(policy), Assessment: normalized}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return ValidatedScreeningDecision{}, err
	}
	hash := sha256.Sum256(encoded)
	return ValidatedScreeningDecision{Assessment: normalized, Fingerprint: fmt.Sprintf("sha256:%x", hash)}, nil
}

func validateScreeningPolicy(policy ScreeningPolicy) (map[string]ScreeningCriterion, error) {
	if policy.Version != ScreeningPolicyVersionV1 || len(policy.Criteria) < 2 || len(policy.Criteria) > 256 {
		return nil, fmt.Errorf("%w: Screening Policy version or size is invalid", ErrInvalidContract)
	}
	criteria := make(map[string]ScreeningCriterion, len(policy.Criteria))
	inclusions, exclusions := 0, 0
	for _, criterion := range policy.Criteria {
		if !validScreeningToken(criterion.ID, 160) || strings.TrimSpace(criterion.Text) != criterion.Text || substantiveRuneCount(criterion.Text) < 4 || len(criterion.Text) > 4096 {
			return nil, fmt.Errorf("%w: Screening criterion is invalid", ErrInvalidContract)
		}
		if criterion.Kind != "inclusion" && criterion.Kind != "exclusion" {
			return nil, fmt.Errorf("%w: Screening criterion kind is invalid", ErrInvalidContract)
		}
		if _, exists := criteria[criterion.ID]; exists {
			return nil, fmt.Errorf("%w: Screening criterion ID is duplicated", ErrInvalidContract)
		}
		criteria[criterion.ID] = criterion
		if criterion.Kind == "inclusion" {
			inclusions++
		} else {
			exclusions++
		}
	}
	if inclusions == 0 || exclusions == 0 {
		return nil, fmt.Errorf("%w: Screening Policy needs inclusion and exclusion criteria", ErrInvalidContract)
	}
	return criteria, nil
}

func normalizedScreeningPolicy(policy ScreeningPolicy) ScreeningPolicy {
	normalized := policy
	normalized.Criteria = append([]ScreeningCriterion(nil), policy.Criteria...)
	sort.Slice(normalized.Criteria, func(i, j int) bool { return normalized.Criteria[i].ID < normalized.Criteria[j].ID })
	return normalized
}

func validScreeningToken(value string, limit int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= limit
}

func validScreeningFact(fact ScreeningFact) bool {
	if !validScreeningToken(fact.Value, 8192) || len(fact.Locator) > 2048 || strings.TrimSpace(fact.Locator) != fact.Locator {
		return false
	}
	switch fact.Kind {
	case "snippet", "metadata", "manual_review":
		return validScreeningToken(fact.Locator, 2048)
	case "content_hash":
		if !strings.HasPrefix(fact.Value, "sha256:") || len(fact.Value) != len("sha256:")+sha256.Size*2 {
			return false
		}
		digest := strings.TrimPrefix(fact.Value, "sha256:")
		if digest != strings.ToLower(digest) {
			return false
		}
		decoded, err := hex.DecodeString(digest)
		return err == nil && len(decoded) == sha256.Size
	case "canonical_url":
		parsed, err := url.Parse(fact.Value)
		if err != nil || parsed.User != nil {
			return false
		}
		canonical, err := CanonicalURL(fact.Value)
		return err == nil && canonical == fact.Value
	default:
		return false
	}
}
