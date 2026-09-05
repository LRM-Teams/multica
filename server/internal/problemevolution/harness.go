package problemevolution

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ModeBFlagEnv gates mode B. Task-harness runs stay behind a flag because they
// only make sense once a deployment has hidden-answer secrets and a sandbox to
// run untrusted harnesses in.
const ModeBFlagEnv = "MULTICA_PROBLEM_EVOLUTION_MODE_B"

// JIT defaults from the spec: generate N, execute K. Generating more than are
// executed is the point — selection happens before the expensive step.
const (
	DefaultHarnessProposals = 4
	DefaultHarnessShortlist = 2
	MaxHarnessProposals     = 8
)

// Harness scope. A winning harness belongs to the run that produced it; calling
// it a general capability gain would be a claim the evidence does not support.
const (
	HarnessScopeRun = "run"
)

// Static gate outcomes.
const (
	GateReasonMissingID          = "missing_harness_id"
	GateReasonNoSteps            = "no_steps"
	GateReasonTooManySteps       = "too_many_steps"
	GateReasonUndeclaredTool     = "undeclared_tool"
	GateReasonForbiddenTool      = "forbidden_tool"
	GateReasonNetworkRequested   = "network_requested"
	GateReasonSecretRequested    = "secret_requested"
	GateReasonBudgetUnbounded    = "budget_unbounded"
	GateReasonDuplicateStepID    = "duplicate_step_id"
	GateReasonUnknownEntryStep   = "unknown_entry_step"
	GateReasonSchemaVersion      = "unsupported_schema_version"
	GateReasonScopeNotRun        = "scope_not_run"
	GateReasonTooManyRepairs     = "repair_rounds_exceed_policy"
	GateReasonSelfScoreRequested = "self_score_requested"
)

// MaxHarnessSteps bounds a harness plan so a proposal cannot describe an
// unbounded pipeline the platform then has to babysit.
const MaxHarnessSteps = 24

// forbiddenHarnessTools are capabilities a task harness may never request. A
// harness that can read the hidden answer or reach the network is not solving
// the task, it is escaping the evaluation.
var forbiddenHarnessTools = map[string]string{
	"secret_read":       GateReasonSecretRequested,
	"hidden_answer":     GateReasonSecretRequested,
	"evaluator_write":   GateReasonSelfScoreRequested,
	"score_override":    GateReasonSelfScoreRequested,
	"network":           GateReasonNetworkRequested,
	"http":              GateReasonNetworkRequested,
	"outbound_internet": GateReasonNetworkRequested,
}

// HarnessStep is one declared action in a proposed harness.
type HarnessStep struct {
	StepID string   `json:"step_id"`
	Kind   string   `json:"kind"`
	Tool   string   `json:"tool,omitempty"`
	Needs  []string `json:"needs,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// HarnessSpec is the structured contract a proposed harness must satisfy before
// the platform is willing to execute it.
type HarnessSpec struct {
	SchemaVersion  int           `json:"schema_version"`
	HarnessID      string        `json:"harness_id"`
	Scope          string        `json:"scope"`
	Rationale      string        `json:"rationale,omitempty"`
	DeclaredTools  []string      `json:"declared_tools"`
	DeclaredSkills []string      `json:"declared_skills,omitempty"`
	EntryStepID    string        `json:"entry_step_id"`
	Steps          []HarnessStep `json:"steps"`
	MaxSteps       int           `json:"max_steps"`
	MaxRepairs     int           `json:"max_repairs"`
	RequiresNetk   bool          `json:"requires_network,omitempty"`
}

// StaticGateResult is the platform's verdict on a proposal, before execution.
type StaticGateResult struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons,omitempty"`
}

// ModeBEnabled reports whether task-harness runs are available.
func ModeBEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(ModeBFlagEnv))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

// StaticGate checks a proposal against the platform's non-negotiables. It runs
// before execution because the cheapest way to stop a harness from reading the
// hidden answer is to never start it.
func StaticGate(spec HarnessSpec, policy FeedbackPolicy) StaticGateResult {
	var reasons []string
	add := func(reason string) {
		reasons = append(reasons, reason)
	}
	if spec.SchemaVersion != SchemaVersion {
		add(GateReasonSchemaVersion)
	}
	if strings.TrimSpace(spec.HarnessID) == "" {
		add(GateReasonMissingID)
	}
	if spec.Scope != HarnessScopeRun {
		add(GateReasonScopeNotRun)
	}
	if spec.RequiresNetk {
		add(GateReasonNetworkRequested)
	}
	if spec.MaxSteps <= 0 || spec.MaxSteps > MaxHarnessSteps {
		add(GateReasonBudgetUnbounded)
	}
	if policy.MaxRounds > 0 && spec.MaxRepairs > policy.MaxRounds {
		add(GateReasonTooManyRepairs)
	}
	declared := make(map[string]struct{}, len(spec.DeclaredTools))
	for _, tool := range spec.DeclaredTools {
		normalized := strings.ToLower(strings.TrimSpace(tool))
		if normalized == "" {
			continue
		}
		if reason, forbidden := forbiddenHarnessTools[normalized]; forbidden {
			add(reason)
			continue
		}
		declared[normalized] = struct{}{}
	}
	if len(spec.Steps) == 0 {
		add(GateReasonNoSteps)
	}
	if len(spec.Steps) > MaxHarnessSteps {
		add(GateReasonTooManySteps)
	}
	seen := make(map[string]struct{}, len(spec.Steps))
	for _, step := range spec.Steps {
		stepID := strings.TrimSpace(step.StepID)
		if stepID == "" {
			add(GateReasonMissingID)
			continue
		}
		if _, duplicate := seen[stepID]; duplicate {
			add(GateReasonDuplicateStepID)
		}
		seen[stepID] = struct{}{}
		tool := strings.ToLower(strings.TrimSpace(step.Tool))
		if tool == "" {
			continue
		}
		if reason, forbidden := forbiddenHarnessTools[tool]; forbidden {
			add(reason)
			continue
		}
		// A step using a tool the proposal never declared would make the
		// declared list meaningless as a review surface.
		if _, ok := declared[tool]; !ok {
			add(GateReasonUndeclaredTool)
		}
	}
	if spec.EntryStepID != "" {
		if _, ok := seen[strings.TrimSpace(spec.EntryStepID)]; !ok {
			add(GateReasonUnknownEntryStep)
		}
	} else if len(spec.Steps) > 0 {
		add(GateReasonUnknownEntryStep)
	}
	return StaticGateResult{Passed: len(reasons) == 0, Reasons: dedupeReasons(reasons)}
}

func dedupeReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(reasons))
	unique := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		unique = append(unique, reason)
	}
	sort.Strings(unique)
	return unique
}

// HarnessProposal is one gated proposal awaiting shortlist.
type HarnessProposal struct {
	CandidateRef string
	Spec         HarnessSpec
	Gate         StaticGateResult
	// PriorScore is an optional cheap pre-score (for example a static rubric on
	// the plan). It orders proposals but never replaces execution.
	PriorScore float64
}

// ShortlistHarnesses picks which proposals are worth executing.
//
// Only gate-passing proposals are eligible: executing a proposal that failed a
// static gate would mean the gate was advisory, which is not what a gate is.
func ShortlistHarnesses(proposals []HarnessProposal, executeCount int) (shortlisted, rejected []string) {
	if executeCount <= 0 {
		executeCount = DefaultHarnessShortlist
	}
	eligible := make([]HarnessProposal, 0, len(proposals))
	for _, proposal := range proposals {
		if !proposal.Gate.Passed {
			rejected = append(rejected, proposal.CandidateRef)
			continue
		}
		eligible = append(eligible, proposal)
	}
	sort.SliceStable(eligible, func(left, right int) bool {
		if eligible[left].PriorScore != eligible[right].PriorScore {
			return eligible[left].PriorScore > eligible[right].PriorScore
		}
		// Fewer declared tools wins ties: a smaller surface is easier to review
		// and has less to go wrong during execution.
		leftTools := len(eligible[left].Spec.DeclaredTools)
		rightTools := len(eligible[right].Spec.DeclaredTools)
		if leftTools != rightTools {
			return leftTools < rightTools
		}
		return eligible[left].CandidateRef < eligible[right].CandidateRef
	})
	for index, proposal := range eligible {
		if index < executeCount {
			shortlisted = append(shortlisted, proposal.CandidateRef)
		} else {
			rejected = append(rejected, proposal.CandidateRef)
		}
	}
	return shortlisted, rejected
}

// BenchmarkModeConfig is the configuration used when a run is meant to be
// comparable to published JIT numbers: generate several, select one, execute
// once, and do not repair on a low reward.
type BenchmarkModeConfig struct {
	Proposals    int  `json:"proposals"`
	ExecuteCount int  `json:"execute_count"`
	AllowRepair  bool `json:"allow_repair"`
}

// BenchmarkMode is the closest configuration to the original JIT procedure.
func BenchmarkMode() BenchmarkModeConfig {
	return BenchmarkModeConfig{Proposals: DefaultHarnessProposals, ExecuteCount: 1, AllowRepair: false}
}

// ValidateHarnessBudget checks the proposal counts a run asks for.
func ValidateHarnessBudget(proposals, executeCount int) error {
	if proposals <= 0 || proposals > MaxHarnessProposals {
		return fmt.Errorf("harness proposals must be within 1..%d", MaxHarnessProposals)
	}
	if executeCount <= 0 || executeCount > proposals {
		return fmt.Errorf("execute_count must be within 1..%d", proposals)
	}
	return nil
}
