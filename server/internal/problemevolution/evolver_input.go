package problemevolution

import (
	"fmt"
	"path"
	"strings"
)

// Default workdir layout handed to the external evolver. Paths are relative to
// the daemon-owned workdir; the evolver may only write inside it.
const (
	DefaultArtifactDir       = "artifacts"
	DefaultCandidateManifest = "candidates.json"
	DefaultEvaluatorInput    = "eval/request.json"
	DefaultEvaluatorOutput   = "eval/result.json"
	InputFileName            = "input.json"
)

// Artifact size ceilings. A candidate exceeding them is marked
// artifact_too_large rather than silently truncated.
const (
	MaxArtifactBytes      int64 = 16 * 1024 * 1024
	MaxBatchArtifactBytes int64 = 128 * 1024 * 1024
)

// ProblemAttachment is an input file staged into the workdir by the daemon.
type ProblemAttachment struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	ContentHash string `json:"content_hash,omitempty"`
}

// ProblemSpec is the task the evolver must solve.
type ProblemSpec struct {
	Statement    string              `json:"statement"`
	ArtifactType string              `json:"artifact_type"`
	Constraints  []string            `json:"constraints,omitempty"`
	Attachments  []ProblemAttachment `json:"attachments,omitempty"`
}

// EvaluatorRef is the non-secret view of the frozen contract. Hidden answers
// are never part of it: the evolver reaches scoring through Invoke.
type EvaluatorRef struct {
	ContractID    string               `json:"contract_id"`
	ContentHash   string               `json:"content_hash"`
	Kind          string               `json:"kind"`
	Dimensions    []EvaluatorDimension `json:"dimensions"`
	PassThreshold float64              `json:"pass_threshold"`
	Invoke        EvaluatorInvoke      `json:"invoke"`
}

// BudgetConfig bounds one evolver invocation.
type BudgetConfig struct {
	Candidates              int `json:"candidates"`
	MaxModelCalls           int `json:"max_model_calls"`
	MaxParallel             int `json:"max_parallel"`
	CandidateTimeoutSeconds int `json:"candidate_timeout_seconds"`
	BatchTimeoutSeconds     int `json:"batch_timeout_seconds"`
}

// ModelConfig selects the execution model for the evolver.
type ModelConfig struct {
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"thinking_level,omitempty"`
}

// ParentFeedback is the projected, safe summary of one parent candidate.
//
// It deliberately carries a projection rather than a raw total: an exact score
// handed back every round is a gradient the evolver can climb toward the
// verifier instead of toward a correct answer.
type ParentFeedback struct {
	CandidateRef  string             `json:"candidate_id"`
	Projection    FeedbackProjection `json:"projection"`
	FailureClass  string             `json:"failure_class,omitempty"`
	ChangeSummary string             `json:"change_summary,omitempty"`
	// RoundsUsed and RepairAllowed tell the evolver, up front, whether another
	// repair attempt on this parent will be accepted at all.
	RoundsUsed    int  `json:"rounds_used"`
	RepairAllowed bool `json:"repair_allowed"`
}

// FeedbackBundle is everything a later generation may learn about earlier ones.
type FeedbackBundle struct {
	Parents              []ParentFeedback `json:"parents,omitempty"`
	SharedWeakDimensions []string         `json:"shared_weak_dimensions,omitempty"`
	Policy               FeedbackPolicy   `json:"policy"`
}

// OutputConfig tells the evolver where to place results.
type OutputConfig struct {
	ArtifactDir       string `json:"artifact_dir"`
	CandidateManifest string `json:"candidate_manifest"`
}

// EvolverInput is the input.json contract (spec §19.3.1).
type EvolverInput struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	Mode          string         `json:"mode"`
	Generation    int            `json:"generation"`
	Problem       ProblemSpec    `json:"problem"`
	Evaluator     EvaluatorRef   `json:"evaluator"`
	Budget        BudgetConfig   `json:"budget"`
	Model         ModelConfig    `json:"model"`
	Feedback      FeedbackBundle `json:"feedback"`
	// Seeds carries the search seed only. The blind-validation seed is
	// deliberately withheld: an evolver that knew it could tune to the final
	// check instead of generalising.
	Seeds  SeedPair     `json:"seeds"`
	Output OutputConfig `json:"output"`
}

// DefaultBudget is the `standard` depth tier scaled to one batch.
func DefaultBudget() BudgetConfig {
	return BudgetConfig{
		Candidates:              4,
		MaxModelCalls:           100,
		MaxParallel:             2,
		CandidateTimeoutSeconds: 600,
		BatchTimeoutSeconds:     1800,
	}
}

// Validate guards the input before it is handed to a subprocess.
func (i EvolverInput) Validate() error {
	if i.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported input schema_version %d", i.SchemaVersion)
	}
	if strings.TrimSpace(i.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	if i.Mode != ModeSolution && i.Mode != ModeTaskHarnessRewardOnly {
		return fmt.Errorf("unsupported mode %q", i.Mode)
	}
	if strings.TrimSpace(i.Problem.Statement) == "" {
		return fmt.Errorf("problem statement is required")
	}
	if i.Budget.Candidates <= 0 {
		return fmt.Errorf("budget candidates must be positive")
	}
	if i.Budget.MaxModelCalls <= 0 {
		return fmt.Errorf("budget max_model_calls must be positive")
	}
	if i.Budget.BatchTimeoutSeconds <= 0 {
		return fmt.Errorf("budget batch_timeout_seconds must be positive")
	}
	if strings.TrimSpace(i.Evaluator.ContentHash) == "" {
		return fmt.Errorf("evaluator content_hash is required")
	}
	if err := i.Feedback.Policy.Validate(); err != nil {
		return err
	}
	if !IsRelativeContainedPath(i.Output.ArtifactDir) {
		return fmt.Errorf("artifact_dir must be a relative path inside the workdir")
	}
	return nil
}

// IsRelativeContainedPath rejects absolute paths and any traversal that would
// let the evolver declare an artifact outside its workdir.
func IsRelativeContainedPath(candidate string) bool {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" || path.IsAbs(trimmed) || strings.HasPrefix(trimmed, "\\") {
		return false
	}
	if strings.Contains(trimmed, "\\") {
		return false
	}
	if volumePrefixed(trimmed) {
		return false
	}
	cleaned := path.Clean(trimmed)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "." {
		return false
	}
	return true
}

// volumePrefixed catches Windows-style drive prefixes such as `C:` that path
// treats as a relative path on Unix.
func volumePrefixed(candidate string) bool {
	if len(candidate) < 2 {
		return false
	}
	first := candidate[0]
	isLetter := (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
	return isLetter && candidate[1] == ':'
}

// ArtifactRelativePath resolves an evolver-declared artifact path against the
// configured artifact dir, rejecting anything that escapes it.
func ArtifactRelativePath(artifactDir, declared string) (string, error) {
	if !IsRelativeContainedPath(declared) {
		return "", fmt.Errorf("artifact path %q escapes the workdir", declared)
	}
	cleaned := path.Clean(declared)
	dir := path.Clean(artifactDir)
	if cleaned != dir && !strings.HasPrefix(cleaned, dir+"/") {
		return "", fmt.Errorf("artifact path %q is outside %q", declared, artifactDir)
	}
	return cleaned, nil
}
