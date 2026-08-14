package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const IntegrationMergePolicyVersionV1 = "research-integration-merge-v1"

type IntegrationMergeEntityKind string

const (
	IntegrationMergeClaim      IntegrationMergeEntityKind = "claim"
	IntegrationMergeQuestion   IntegrationMergeEntityKind = "question"
	IntegrationMergeHypothesis IntegrationMergeEntityKind = "hypothesis"
)

type IntegrationMergeEntity struct {
	ID                  string
	Kind                IntegrationMergeEntityKind
	Status              string
	SemanticFingerprint string
	ScopeKey            string
	MethodKey           string
	TimeKey             string
	Accessible          bool
}

// IntegrationMergeSignals are server-computed similarity facts. Agent prose
// cannot substitute for these bounded signals.
type IntegrationMergeSignals struct {
	SemanticSimilarity float64
	LexicalSimilarity  float64
	EntityOverlap      float64
}

type IntegrationMergeIntent struct {
	PolicyVersion string
	Left          IntegrationMergeEntity
	Right         IntegrationMergeEntity
	Signals       IntegrationMergeSignals
	Rationale     string
}

type IntegrationMergeDecision struct {
	PolicyVersion string
	LeftID        string
	RightID       string
	Disposition   string
	Reasons       []string
	Fingerprint   string
}

// EvaluateIntegrationMergeCandidate produces a deterministic, symmetric
// decision. It only proposes a merge; applying it remains a canonical write
// that must revalidate the referenced entities and current state.
func EvaluateIntegrationMergeCandidate(intent IntegrationMergeIntent) (IntegrationMergeDecision, error) {
	if intent.PolicyVersion != IntegrationMergePolicyVersionV1 ||
		!validIntegrationMergeEntity(intent.Left) || !validIntegrationMergeEntity(intent.Right) ||
		intent.Left.ID == intent.Right.ID || !validIntegrationMergeSignals(intent.Signals) ||
		strings.TrimSpace(intent.Rationale) != intent.Rationale || substantiveRuneCount(intent.Rationale) < 8 || len(intent.Rationale) > 4096 {
		return IntegrationMergeDecision{}, fmt.Errorf("%w: Integration merge candidate is invalid", ErrInvalidContract)
	}

	left, right := intent.Left, intent.Right
	if right.ID < left.ID {
		left, right = right, left
	}
	reasons := integrationMergeRejectionReasons(left, right, intent.Signals)
	disposition := "propose_merge"
	if len(reasons) != 0 {
		disposition = "reject"
	}
	decision := IntegrationMergeDecision{
		PolicyVersion: intent.PolicyVersion,
		LeftID:        left.ID,
		RightID:       right.ID,
		Disposition:   disposition,
		Reasons:       reasons,
	}
	encoded, err := json.Marshal(struct {
		Decision  IntegrationMergeDecision
		Left      IntegrationMergeEntity
		Right     IntegrationMergeEntity
		Signals   IntegrationMergeSignals
		Rationale string
	}{decision, left, right, intent.Signals, intent.Rationale})
	if err != nil {
		return IntegrationMergeDecision{}, err
	}
	digest := sha256.Sum256(encoded)
	decision.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return decision, nil
}

func integrationMergeRejectionReasons(left, right IntegrationMergeEntity, signals IntegrationMergeSignals) []string {
	reasons := make([]string, 0, 8)
	if left.Kind != right.Kind {
		reasons = append(reasons, "different_kind")
	}
	if !left.Accessible || !right.Accessible {
		reasons = append(reasons, "inaccessible_input")
	}
	if integrationMergeTerminal(left.Status) || integrationMergeTerminal(right.Status) {
		reasons = append(reasons, "terminal_input")
	}
	if left.ScopeKey != right.ScopeKey {
		reasons = append(reasons, "scope_mismatch")
	}
	if left.MethodKey != right.MethodKey {
		reasons = append(reasons, "method_mismatch")
	}
	if left.TimeKey != right.TimeKey {
		reasons = append(reasons, "time_mismatch")
	}
	exactFingerprint := left.SemanticFingerprint == right.SemanticFingerprint
	nearDuplicate := signals.SemanticSimilarity >= 0.92 && signals.LexicalSimilarity >= 0.65 && signals.EntityOverlap >= 0.50
	if !exactFingerprint && !nearDuplicate {
		reasons = append(reasons, "below_similarity_threshold")
	}
	sort.Strings(reasons)
	return reasons
}

func validIntegrationMergeEntity(entity IntegrationMergeEntity) bool {
	if _, err := uuid.Parse(entity.ID); err != nil {
		return false
	}
	switch entity.Kind {
	case IntegrationMergeClaim, IntegrationMergeQuestion, IntegrationMergeHypothesis:
	default:
		return false
	}
	return validIntegrationMergeStatus(entity.Kind, entity.Status) && validIntegrationMergeHash(entity.SemanticFingerprint) &&
		validIntegrationMergeToken(entity.ScopeKey, 512) && validOptionalIntegrationMergeToken(entity.MethodKey, 512) &&
		validOptionalIntegrationMergeToken(entity.TimeKey, 512)
}

func validIntegrationMergeStatus(kind IntegrationMergeEntityKind, status string) bool {
	switch kind {
	case IntegrationMergeClaim:
		return status == "proposed" || status == "supported" || status == "disputed" ||
			status == "refuted" || status == "superseded" || status == "unresolved"
	case IntegrationMergeQuestion:
		return status == "open" || status == "in_progress" || status == "answered" ||
			status == "unresolved" || status == "obsolete"
	case IntegrationMergeHypothesis:
		return status == "proposed" || status == "investigating" || status == "supported" ||
			status == "weakened" || status == "refuted" || status == "conditional" || status == "obsolete"
	default:
		return false
	}
}

func validIntegrationMergeSignals(signals IntegrationMergeSignals) bool {
	for _, value := range []float64{signals.SemanticSimilarity, signals.LexicalSimilarity, signals.EntityOverlap} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return false
		}
	}
	return true
}

func integrationMergeTerminal(status string) bool {
	switch status {
	case "obsolete", "refuted", "superseded":
		return true
	default:
		return false
	}
}

func validIntegrationMergeHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validIntegrationMergeToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}

func validOptionalIntegrationMergeToken(value string, limit int) bool {
	return value == "" || validIntegrationMergeToken(value, limit)
}
