package problemevolution

import (
	"slices"
	"testing"
)

func validHarnessSpec() HarnessSpec {
	return HarnessSpec{
		SchemaVersion: SchemaVersion,
		HarnessID:     "h1",
		Scope:         HarnessScopeRun,
		DeclaredTools: []string{"read_file", "run_tests"},
		EntryStepID:   "plan",
		MaxSteps:      6,
		MaxRepairs:    1,
		Steps: []HarnessStep{
			{StepID: "plan", Kind: "think"},
			{StepID: "inspect", Kind: "tool", Tool: "read_file", Needs: []string{"plan"}},
			{StepID: "verify", Kind: "tool", Tool: "run_tests", Needs: []string{"inspect"}},
		},
	}
}

func TestStaticGatePassesAValidSpec(t *testing.T) {
	result := StaticGate(validHarnessSpec(), DefaultFeedbackPolicy())
	if !result.Passed {
		t.Fatalf("valid spec was rejected: %v", result.Reasons)
	}
}

func TestStaticGateRejectsSecretAndNetworkAccess(t *testing.T) {
	cases := map[string]struct {
		mutate func(HarnessSpec) HarnessSpec
		reason string
	}{
		"declared secret tool": {
			func(spec HarnessSpec) HarnessSpec {
				spec.DeclaredTools = append(spec.DeclaredTools, "hidden_answer")
				return spec
			},
			GateReasonSecretRequested,
		},
		"step reaching the network": {
			func(spec HarnessSpec) HarnessSpec {
				spec.Steps = append(spec.Steps, HarnessStep{StepID: "fetch", Kind: "tool", Tool: "http"})
				return spec
			},
			GateReasonNetworkRequested,
		},
		"requires network flag": {
			func(spec HarnessSpec) HarnessSpec {
				spec.RequiresNetk = true
				return spec
			},
			GateReasonNetworkRequested,
		},
		"writing its own score": {
			func(spec HarnessSpec) HarnessSpec {
				spec.DeclaredTools = append(spec.DeclaredTools, "score_override")
				return spec
			},
			GateReasonSelfScoreRequested,
		},
	}
	for name, testCase := range cases {
		result := StaticGate(testCase.mutate(validHarnessSpec()), DefaultFeedbackPolicy())
		if result.Passed {
			t.Fatalf("%s: spec passed the gate", name)
		}
		if !slices.Contains(result.Reasons, testCase.reason) {
			t.Fatalf("%s: reasons = %v, want %q", name, result.Reasons, testCase.reason)
		}
	}
}

func TestStaticGateRejectsStructuralProblems(t *testing.T) {
	cases := map[string]struct {
		mutate func(HarnessSpec) HarnessSpec
		reason string
	}{
		"undeclared tool": {
			func(spec HarnessSpec) HarnessSpec {
				spec.Steps = append(spec.Steps, HarnessStep{StepID: "extra", Kind: "tool", Tool: "write_file"})
				return spec
			},
			GateReasonUndeclaredTool,
		},
		"duplicate step id": {
			func(spec HarnessSpec) HarnessSpec {
				spec.Steps = append(spec.Steps, HarnessStep{StepID: "plan", Kind: "think"})
				return spec
			},
			GateReasonDuplicateStepID,
		},
		"unknown entry step": {
			func(spec HarnessSpec) HarnessSpec {
				spec.EntryStepID = "nowhere"
				return spec
			},
			GateReasonUnknownEntryStep,
		},
		"no steps": {
			func(spec HarnessSpec) HarnessSpec {
				spec.Steps = nil
				return spec
			},
			GateReasonNoSteps,
		},
		"unbounded budget": {
			func(spec HarnessSpec) HarnessSpec {
				spec.MaxSteps = 0
				return spec
			},
			GateReasonBudgetUnbounded,
		},
		"workspace scope": {
			func(spec HarnessSpec) HarnessSpec {
				spec.Scope = "workspace"
				return spec
			},
			GateReasonScopeNotRun,
		},
	}
	for name, testCase := range cases {
		result := StaticGate(testCase.mutate(validHarnessSpec()), DefaultFeedbackPolicy())
		if result.Passed {
			t.Fatalf("%s: spec passed the gate", name)
		}
		if !slices.Contains(result.Reasons, testCase.reason) {
			t.Fatalf("%s: reasons = %v, want %q", name, result.Reasons, testCase.reason)
		}
	}
}

func TestStaticGateRejectsRepairsBeyondPolicy(t *testing.T) {
	spec := validHarnessSpec()
	policy := DefaultFeedbackPolicy()
	spec.MaxRepairs = policy.MaxRounds + 1
	result := StaticGate(spec, policy)
	// Repeated repair against the same reward is exactly how a harness starts
	// guessing the verifier, so the policy bound is a gate, not advice.
	if result.Passed || !slices.Contains(result.Reasons, GateReasonTooManyRepairs) {
		t.Fatalf("reasons = %v, want %q", result.Reasons, GateReasonTooManyRepairs)
	}
}

func TestStaticGateReasonsAreDeduplicatedAndOrdered(t *testing.T) {
	spec := validHarnessSpec()
	spec.DeclaredTools = append(spec.DeclaredTools, "http", "network")
	result := StaticGate(spec, DefaultFeedbackPolicy())
	count := 0
	for _, reason := range result.Reasons {
		if reason == GateReasonNetworkRequested {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("network reason appeared %d times, want 1: %v", count, result.Reasons)
	}
	if !slices.IsSorted(result.Reasons) {
		t.Fatalf("reasons are not in a stable order: %v", result.Reasons)
	}
}

func TestShortlistHarnessesOnlyExecutesGatePassers(t *testing.T) {
	proposals := []HarnessProposal{
		{CandidateRef: "gated-out", Gate: StaticGateResult{Passed: false}, PriorScore: 0.99},
		{CandidateRef: "good", Gate: StaticGateResult{Passed: true}, PriorScore: 0.5},
	}
	shortlisted, rejected := ShortlistHarnesses(proposals, 2)
	// A high pre-score must not buy a way past the gate.
	if !slices.Contains(shortlisted, "good") || slices.Contains(shortlisted, "gated-out") {
		t.Fatalf("shortlisted = %v, want only the gate-passing proposal", shortlisted)
	}
	if !slices.Contains(rejected, "gated-out") {
		t.Fatalf("rejected = %v, want the gated-out proposal", rejected)
	}
}

func TestShortlistHarnessesRespectsExecuteCount(t *testing.T) {
	proposals := []HarnessProposal{
		{CandidateRef: "a", Gate: StaticGateResult{Passed: true}, PriorScore: 0.9},
		{CandidateRef: "b", Gate: StaticGateResult{Passed: true}, PriorScore: 0.8},
		{CandidateRef: "c", Gate: StaticGateResult{Passed: true}, PriorScore: 0.7},
	}
	shortlisted, rejected := ShortlistHarnesses(proposals, 1)
	if len(shortlisted) != 1 || shortlisted[0] != "a" {
		t.Fatalf("shortlisted = %v, want [a]", shortlisted)
	}
	if len(rejected) != 2 {
		t.Fatalf("rejected = %v, want two entries", rejected)
	}
}

func TestShortlistHarnessesPrefersSmallerToolSurfaceOnTies(t *testing.T) {
	proposals := []HarnessProposal{
		{
			CandidateRef: "wide",
			Gate:         StaticGateResult{Passed: true},
			PriorScore:   0.5,
			Spec:         HarnessSpec{DeclaredTools: []string{"a", "b", "c", "d"}},
		},
		{
			CandidateRef: "narrow",
			Gate:         StaticGateResult{Passed: true},
			PriorScore:   0.5,
			Spec:         HarnessSpec{DeclaredTools: []string{"a"}},
		},
	}
	shortlisted, _ := ShortlistHarnesses(proposals, 1)
	if len(shortlisted) != 1 || shortlisted[0] != "narrow" {
		t.Fatalf("shortlisted = %v, want [narrow]", shortlisted)
	}
}

func TestBenchmarkModeMatchesPublishedProcedure(t *testing.T) {
	config := BenchmarkMode()
	// The published JIT procedure generates several harnesses, selects one, and
	// executes it once without low-reward repair; anything else is not
	// comparable to those numbers.
	if config.ExecuteCount != 1 || config.AllowRepair {
		t.Fatalf("benchmark mode = %+v, want a single execution without repair", config)
	}
	if config.Proposals < 2 {
		t.Fatalf("benchmark mode proposals = %d, want several", config.Proposals)
	}
}

func TestValidateHarnessBudget(t *testing.T) {
	if err := ValidateHarnessBudget(4, 2); err != nil {
		t.Fatalf("expected the default budget to be valid, got %v", err)
	}
	for _, testCase := range [][2]int{{0, 1}, {MaxHarnessProposals + 1, 1}, {4, 0}, {2, 3}} {
		if err := ValidateHarnessBudget(testCase[0], testCase[1]); err == nil {
			t.Fatalf("budget %v was accepted", testCase)
		}
	}
}

func TestModeBDefaultsToDisabled(t *testing.T) {
	t.Setenv(ModeBFlagEnv, "")
	if ModeBEnabled() {
		t.Fatal("mode B is enabled without the flag set")
	}
	t.Setenv(ModeBFlagEnv, "true")
	if !ModeBEnabled() {
		t.Fatal("mode B stayed disabled with the flag set")
	}
	t.Setenv(ModeBFlagEnv, "maybe")
	if ModeBEnabled() {
		t.Fatal("an unparsable flag value enabled mode B")
	}
}
