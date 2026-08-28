// Package problemevolution holds the contracts shared by the problem-evolution
// HTTP surface, the daemon capability, and the external evolver process.
//
// The evolution algorithm itself lives outside this repository (see
// docs/adr/0021-problem-evolution-external-evolver.md). Everything here is the
// boundary: what the server persists, what the daemon hands to the external
// program, and which events it is allowed to send back.
package problemevolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Mode selects which orchestrator a run uses.
const (
	ModeSolution              = "solution"
	ModeTaskHarnessRewardOnly = "task_harness_reward_only"
)

// Evaluator kinds. Only builtin evaluators are accepted in the first phase:
// user-supplied verifier code runs on sandbox nodes, which is a later slice.
const (
	EvaluatorKindBuiltinDeterministic = "builtin_deterministic"
	EvaluatorKindBuiltinRubric        = "builtin_rubric"
)

// Feedback bandwidth tiers. Bucketed is the default because per-round exact
// pass counts let a model binary-search the verifier across rounds.
const (
	FeedbackBandwidthBucketed = "bucketed"
	FeedbackBandwidthExact    = "exact"
)

// ScaleUnitInterval is the only score scale: every dimension and total is
// normalised to 0..1 so candidates from different evaluators stay comparable.
const ScaleUnitInterval = "unit_interval"

// SchemaVersion is the version of the score, behavior-profile, evolver-input
// and event payload structures. Bumping it requires updating the frontend zod
// schemas in the same change.
const SchemaVersion = 1

// ErrContractInvalid is the sentinel for a contract that cannot be frozen.
var ErrContractInvalid = errors.New("evaluator contract invalid")

// EvaluatorDimension is one scored axis of a contract.
type EvaluatorDimension struct {
	DimensionID string  `json:"dimension_id"`
	Name        string  `json:"name,omitempty"`
	Criteria    string  `json:"criteria,omitempty"`
	Weight      float64 `json:"weight"`
	Hard        bool    `json:"hard,omitempty"`
}

// EvaluatorInvoke tells the external evolver how to reach the evaluator. The
// indirection is what keeps hidden answers away from the evolver: it writes a
// candidate to InputPath and reads a verdict from OutputPath, never holding
// answer material itself.
type EvaluatorInvoke struct {
	Transport  string   `json:"transport"`
	Command    []string `json:"command"`
	InputPath  string   `json:"input_path"`
	OutputPath string   `json:"output_path"`
}

// EvaluatorContract is the frozen definition of "how good is this candidate".
type EvaluatorContract struct {
	SchemaVersion int                  `json:"schema_version"`
	Kind          string               `json:"kind"`
	Dimensions    []EvaluatorDimension `json:"dimensions"`
	PassThreshold float64              `json:"pass_threshold"`
	Invoke        EvaluatorInvoke      `json:"invoke"`
}

// FeedbackPolicy bounds what a later generation may learn about the evaluator.
type FeedbackPolicy struct {
	SchemaVersion int    `json:"schema_version"`
	Bandwidth     string `json:"bandwidth"`
	// IncludeDimensionBreakdown exposes per-dimension buckets. Buckets say
	// which axis is weak without saying how a hidden case was judged.
	IncludeDimensionBreakdown bool `json:"include_dimension_breakdown"`
	MaxNumericFields          int  `json:"max_numeric_fields"`
	MaxRounds                 int  `json:"max_rounds"`
}

// DefaultFeedbackPolicy is the low-bandwidth default required by the spec.
func DefaultFeedbackPolicy() FeedbackPolicy {
	return FeedbackPolicy{
		SchemaVersion:             SchemaVersion,
		Bandwidth:                 FeedbackBandwidthBucketed,
		IncludeDimensionBreakdown: true,
		MaxNumericFields:          6,
		MaxRounds:                 2,
	}
}

// Validate rejects contracts that cannot produce a comparable score.
func (c EvaluatorContract) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrContractInvalid, c.SchemaVersion)
	}
	switch c.Kind {
	case EvaluatorKindBuiltinDeterministic, EvaluatorKindBuiltinRubric:
	default:
		return fmt.Errorf("%w: unsupported kind %q", ErrContractInvalid, c.Kind)
	}
	if len(c.Dimensions) == 0 {
		return fmt.Errorf("%w: at least one dimension is required", ErrContractInvalid)
	}
	if c.PassThreshold < 0 || c.PassThreshold > 1 {
		return fmt.Errorf("%w: pass_threshold must be within 0..1", ErrContractInvalid)
	}
	seen := make(map[string]struct{}, len(c.Dimensions))
	var weightSum float64
	for _, dimension := range c.Dimensions {
		id := strings.TrimSpace(dimension.DimensionID)
		if id == "" {
			return fmt.Errorf("%w: dimension_id is required", ErrContractInvalid)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: duplicate dimension_id %q", ErrContractInvalid, id)
		}
		seen[id] = struct{}{}
		if dimension.Weight <= 0 {
			return fmt.Errorf("%w: dimension %q needs a positive weight", ErrContractInvalid, id)
		}
		weightSum += dimension.Weight
	}
	if weightSum <= 0 {
		return fmt.Errorf("%w: dimension weights must sum above zero", ErrContractInvalid)
	}
	if c.Invoke.Transport != "cli" {
		return fmt.Errorf("%w: unsupported evaluator transport %q", ErrContractInvalid, c.Invoke.Transport)
	}
	if len(c.Invoke.Command) == 0 {
		return fmt.Errorf("%w: evaluator command is required", ErrContractInvalid)
	}
	if strings.TrimSpace(c.Invoke.InputPath) == "" || strings.TrimSpace(c.Invoke.OutputPath) == "" {
		return fmt.Errorf("%w: evaluator input_path and output_path are required", ErrContractInvalid)
	}
	return nil
}

// Validate keeps a policy from silently widening the feedback channel.
func (p FeedbackPolicy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported feedback schema_version %d", ErrContractInvalid, p.SchemaVersion)
	}
	switch p.Bandwidth {
	case FeedbackBandwidthBucketed, FeedbackBandwidthExact:
	default:
		return fmt.Errorf("%w: unsupported feedback bandwidth %q", ErrContractInvalid, p.Bandwidth)
	}
	if p.MaxNumericFields <= 0 || p.MaxNumericFields > 6 {
		return fmt.Errorf("%w: max_numeric_fields must be within 1..6", ErrContractInvalid)
	}
	if p.MaxRounds < 0 || p.MaxRounds > 2 {
		return fmt.Errorf("%w: max_rounds must be within 0..2", ErrContractInvalid)
	}
	return nil
}

// ContentHash is the identity of a frozen contract. The run pins it at start
// and every evaluation re-checks it, so an edited contract is detected rather
// than silently applied mid-run.
func ContentHash(contract EvaluatorContract, policy FeedbackPolicy) (string, error) {
	canonical, err := canonicalJSON(map[string]any{
		"contract":        contract,
		"feedback_policy": policy,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalJSON serialises with sorted keys so semantically equal contracts
// hash identically regardless of field order in the request body.
func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return marshalSorted(decoded)
}

func marshalSorted(value any) ([]byte, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var builder strings.Builder
		builder.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				builder.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			builder.Write(encodedKey)
			builder.WriteByte(':')
			encodedValue, err := marshalSorted(typed[key])
			if err != nil {
				return nil, err
			}
			builder.Write(encodedValue)
		}
		builder.WriteByte('}')
		return []byte(builder.String()), nil
	case []any:
		var builder strings.Builder
		builder.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				builder.WriteByte(',')
			}
			encoded, err := marshalSorted(item)
			if err != nil {
				return nil, err
			}
			builder.Write(encoded)
		}
		builder.WriteByte(']')
		return []byte(builder.String()), nil
	default:
		return json.Marshal(typed)
	}
}
