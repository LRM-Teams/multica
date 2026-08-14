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

const ExplorationCandidateGenerationPolicyV1 = "research-exploration-candidate-generation-v1"

type ExplorationNeedKind string

const (
	ExplorationNeedRequiredQuestion ExplorationNeedKind = "required_question_unanswered"
	ExplorationNeedEvidenceGap      ExplorationNeedKind = "evidence_gap"
	ExplorationNeedBlockingDispute  ExplorationNeedKind = "blocking_dispute"
	ExplorationNeedHypothesisChange ExplorationNeedKind = "hypothesis_changed"
	ExplorationNeedInsightStale     ExplorationNeedKind = "insight_stale"
	ExplorationNeedHomogeneous      ExplorationNeedKind = "homogeneous_sources"
	ExplorationNeedMethodGap        ExplorationNeedKind = "method_gap"
)

type ExplorationCandidateScoreFacts struct {
	DecisionImpact             float64
	UncertaintyReduction       float64
	ExpectedInformationGain    float64
	Novelty                    float64
	ExpectedSuccessProbability float64
	DuplicateOverlap           float64
	DependencyProviderRisk     float64
}

type ExplorationCandidateCostFacts struct {
	Time  float64
	Token float64
	Tool  float64
	Human float64
}

type ExplorationNeedSignal struct {
	SignalID                string
	Kind                    ExplorationNeedKind
	TargetID                string
	TargetKind              string
	SourceFamilyKey         string
	MethodKey               string
	PerspectiveKey          string
	CounterevidenceRequired bool
	ReasonableImpactPath    bool
	HardConstraintFailures  []string
	Score                   ExplorationCandidateScoreFacts
	Cost                    ExplorationCandidateCostFacts
	Rationale               string
}

type GeneratedExplorationCandidate struct {
	CandidateKey             string
	SignalID                 string
	TargetID                 string
	TargetKind               string
	TaskKind                 string
	RequiredCapability       string
	Purpose                  string
	SourceFamilyKey          string
	MethodKey                string
	PerspectiveKey           string
	DivergenceProbe          bool
	ReasonableImpactPath     bool
	ProtectsHighImpactTarget bool
	HardConstraintFailures   []string
	Score                    ExplorationCandidateScoreFacts
	RequiredQuestionCoverage float64
	DisputeDiscrimination    float64
	SourceIndependenceGain   float64
	MethodRequirement        float64
	Cost                     ExplorationCandidateCostFacts
}

type ExplorationCandidateGenerationResult struct {
	PolicyVersion string
	Candidates    []GeneratedExplorationCandidate
	Fingerprint   string
}

// GenerateExplorationCandidates converts canonical need signals into bounded,
// deterministic work candidates. It never accepts free-form Agent task kinds
// and does not select or dispatch the generated work.
func GenerateExplorationCandidates(policyVersion string, signals []ExplorationNeedSignal) (ExplorationCandidateGenerationResult, error) {
	if policyVersion != ExplorationCandidateGenerationPolicyV1 || len(signals) > 4096 {
		return ExplorationCandidateGenerationResult{}, fmt.Errorf("%w: candidate generation request is invalid", ErrInvalidContract)
	}
	ordered := append([]ExplorationNeedSignal(nil), signals...)
	for index := range ordered {
		ordered[index].HardConstraintFailures = append([]string(nil), ordered[index].HardConstraintFailures...)
		sort.Strings(ordered[index].HardConstraintFailures)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SignalID < ordered[j].SignalID })
	seenSignals := make(map[string]struct{}, len(ordered))
	candidates := make([]GeneratedExplorationCandidate, 0, len(ordered)*2)
	seenCandidates := make(map[string]struct{})
	for _, signal := range ordered {
		if !validExplorationNeedSignal(signal) {
			return ExplorationCandidateGenerationResult{}, fmt.Errorf("%w: exploration need signal is invalid", ErrInvalidContract)
		}
		if _, duplicate := seenSignals[signal.SignalID]; duplicate {
			return ExplorationCandidateGenerationResult{}, fmt.Errorf("%w: duplicate exploration need signal", ErrInvalidContract)
		}
		seenSignals[signal.SignalID] = struct{}{}
		for _, spec := range explorationCandidateSpecs(signal) {
			candidate, err := generatedExplorationCandidate(signal, spec)
			if err != nil {
				return ExplorationCandidateGenerationResult{}, err
			}
			if _, duplicate := seenCandidates[candidate.CandidateKey]; duplicate {
				continue
			}
			seenCandidates[candidate.CandidateKey] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CandidateKey < candidates[j].CandidateKey })
	result := ExplorationCandidateGenerationResult{PolicyVersion: policyVersion, Candidates: candidates}
	encoded, err := json.Marshal(struct {
		PolicyVersion string
		Signals       []ExplorationNeedSignal
		Candidates    []GeneratedExplorationCandidate
	}{policyVersion, ordered, candidates})
	if err != nil {
		return ExplorationCandidateGenerationResult{}, err
	}
	digest := sha256.Sum256(encoded)
	result.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return result, nil
}

type explorationCandidateSpec struct {
	taskKind           string
	capability         string
	purpose            string
	divergence         bool
	protectsHighImpact bool
}

func explorationCandidateSpecs(signal ExplorationNeedSignal) []explorationCandidateSpec {
	switch signal.Kind {
	case ExplorationNeedRequiredQuestion:
		return []explorationCandidateSpec{{"discover", "research_discovery", "answer_required_question", false, true}}
	case ExplorationNeedEvidenceGap:
		specs := []explorationCandidateSpec{{"verify", "research_verification", "close_evidence_gap", false, true}}
		if signal.CounterevidenceRequired {
			specs = append(specs, explorationCandidateSpec{"counter_search", "research_counterevidence", "counterevidence", false, true})
		}
		return specs
	case ExplorationNeedBlockingDispute:
		return []explorationCandidateSpec{{"verify", "research_dispute_discrimination", "distinguish_dispute_positions", false, true}}
	case ExplorationNeedHypothesisChange:
		return []explorationCandidateSpec{
			{"verify", "research_hypothesis_testing", "retest_changed_hypothesis", false, true},
			{"counter_search", "research_counterevidence", "challenge_changed_hypothesis", false, true},
		}
	case ExplorationNeedInsightStale:
		return []explorationCandidateSpec{{"synthesize", "research_integration", "reintegrate_stale_insight", false, false}}
	case ExplorationNeedHomogeneous:
		return []explorationCandidateSpec{{"discover", "research_discovery", "divergent_source_probe", true, false}}
	case ExplorationNeedMethodGap:
		return []explorationCandidateSpec{{"verify", "research_method_validation", "validate_method_gap", false, true}}
	default:
		return nil
	}
}

func generatedExplorationCandidate(signal ExplorationNeedSignal, spec explorationCandidateSpec) (GeneratedExplorationCandidate, error) {
	identity, err := json.Marshal(struct {
		PolicyVersion   string
		TargetID        string
		TaskKind        string
		Purpose         string
		SourceFamilyKey string
		MethodKey       string
		PerspectiveKey  string
	}{ExplorationCandidateGenerationPolicyV1, signal.TargetID, spec.taskKind, spec.purpose, signal.SourceFamilyKey, signal.MethodKey, signal.PerspectiveKey})
	if err != nil {
		return GeneratedExplorationCandidate{}, err
	}
	digest := sha256.Sum256(identity)
	return GeneratedExplorationCandidate{
		CandidateKey:             fmt.Sprintf("sha256:%x", digest),
		SignalID:                 signal.SignalID,
		TargetID:                 signal.TargetID,
		TargetKind:               signal.TargetKind,
		TaskKind:                 spec.taskKind,
		RequiredCapability:       spec.capability,
		Purpose:                  spec.purpose,
		SourceFamilyKey:          signal.SourceFamilyKey,
		MethodKey:                signal.MethodKey,
		PerspectiveKey:           signal.PerspectiveKey,
		DivergenceProbe:          spec.divergence,
		ReasonableImpactPath:     signal.ReasonableImpactPath,
		ProtectsHighImpactTarget: spec.protectsHighImpact,
		HardConstraintFailures:   append([]string(nil), signal.HardConstraintFailures...),
		Score:                    signal.Score,
		RequiredQuestionCoverage: boolScore(signal.Kind == ExplorationNeedRequiredQuestion),
		DisputeDiscrimination:    boolScore(signal.Kind == ExplorationNeedBlockingDispute),
		SourceIndependenceGain:   boolScore(signal.Kind == ExplorationNeedHomogeneous),
		MethodRequirement:        boolScore(signal.Kind == ExplorationNeedMethodGap),
		Cost:                     signal.Cost,
	}, nil
}

func validExplorationNeedSignal(signal ExplorationNeedSignal) bool {
	if _, err := uuid.Parse(signal.SignalID); err != nil {
		return false
	}
	if _, err := uuid.Parse(signal.TargetID); err != nil {
		return false
	}
	if !validExplorationNeedKind(signal.Kind) || !validExplorationTargetKind(signal.TargetKind) || !validExplorationNeedTarget(signal.Kind, signal.TargetKind) ||
		!validOptionalExplorationCandidateToken(signal.SourceFamilyKey, 512) ||
		!validOptionalExplorationCandidateToken(signal.MethodKey, 512) ||
		!validOptionalExplorationCandidateToken(signal.PerspectiveKey, 512) ||
		strings.TrimSpace(signal.Rationale) != signal.Rationale || substantiveRuneCount(signal.Rationale) < 8 || len(signal.Rationale) > 4096 ||
		len(signal.HardConstraintFailures) > 64 {
		return false
	}
	if signal.CounterevidenceRequired && signal.Kind != ExplorationNeedEvidenceGap ||
		signal.Kind == ExplorationNeedHomogeneous && (signal.SourceFamilyKey == "" || signal.PerspectiveKey == "") ||
		signal.Kind == ExplorationNeedMethodGap && signal.MethodKey == "" {
		return false
	}
	for _, value := range []float64{signal.Score.DecisionImpact, signal.Score.UncertaintyReduction, signal.Score.ExpectedInformationGain, signal.Score.Novelty, signal.Score.ExpectedSuccessProbability, signal.Score.DuplicateOverlap, signal.Score.DependencyProviderRisk} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return false
		}
	}
	for _, value := range []float64{signal.Cost.Time, signal.Cost.Token, signal.Cost.Tool, signal.Cost.Human} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	seenFailures := map[string]struct{}{}
	for _, failure := range signal.HardConstraintFailures {
		if !validExplorationCandidateToken(failure, 160) {
			return false
		}
		if _, duplicate := seenFailures[failure]; duplicate {
			return false
		}
		seenFailures[failure] = struct{}{}
	}
	return true
}

func validExplorationNeedKind(kind ExplorationNeedKind) bool {
	switch kind {
	case ExplorationNeedRequiredQuestion, ExplorationNeedEvidenceGap, ExplorationNeedBlockingDispute,
		ExplorationNeedHypothesisChange, ExplorationNeedInsightStale, ExplorationNeedHomogeneous, ExplorationNeedMethodGap:
		return true
	default:
		return false
	}
}

func validExplorationTargetKind(kind string) bool {
	switch kind {
	case "question", "claim", "hypothesis", "dispute", "insight", "branch":
		return true
	default:
		return false
	}
}

func validExplorationNeedTarget(kind ExplorationNeedKind, targetKind string) bool {
	switch kind {
	case ExplorationNeedRequiredQuestion:
		return targetKind == "question"
	case ExplorationNeedEvidenceGap:
		return targetKind == "question" || targetKind == "claim"
	case ExplorationNeedBlockingDispute:
		return targetKind == "dispute"
	case ExplorationNeedHypothesisChange:
		return targetKind == "hypothesis"
	case ExplorationNeedInsightStale:
		return targetKind == "insight"
	case ExplorationNeedHomogeneous:
		return targetKind == "question" || targetKind == "branch"
	case ExplorationNeedMethodGap:
		return targetKind == "question" || targetKind == "claim" || targetKind == "hypothesis" || targetKind == "branch"
	default:
		return false
	}
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func validExplorationCandidateToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}

func validOptionalExplorationCandidateToken(value string, limit int) bool {
	return value == "" || validExplorationCandidateToken(value, limit)
}
