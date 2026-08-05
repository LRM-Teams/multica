package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelectAgentRequiresMatchingIdleCapability(t *testing.T) {
	members := []FleetMember{
		{AgentID: "busy-validator", Role: "validator", Status: "active"},
		{AgentID: "idle-validator", Role: "validator", Status: "active"},
		{AgentID: "scout", Role: "scout", Status: "active"},
	}
	got := selectAgent(Task{Kind: TaskKindVerify, RequiredCapability: "validator"}, members, map[string]int{"busy-validator": 1})
	if got != "idle-validator" {
		t.Fatalf("selectAgent=%q", got)
	}
}

func TestBusyMatchingAgentIsCapacityPressureNotMissingCapability(t *testing.T) {
	task := Task{Kind: TaskKindVerify, RequiredCapability: "validator"}
	members := []FleetMember{{AgentID: "busy-validator", Role: "validator", Status: "active"}}
	if got := selectAgent(task, members, map[string]int{"busy-validator": 1}); got != "" {
		t.Fatalf("selectAgent=%q", got)
	}
	if !hasActiveCapability(task, members) {
		t.Fatal("busy matching member must keep the capability available")
	}
}

func TestProjectionRetryDelayUsesDurableAttemptCount(t *testing.T) {
	if got := projectionRetryDelay(0); got != time.Second {
		t.Fatalf("first delay=%s", got)
	}
	if got := projectionRetryDelay(20); got != 256*time.Second {
		t.Fatalf("capped delay=%s", got)
	}
}

func TestTaskRetryBackoffIsBounded(t *testing.T) {
	if got := taskRetryBackoff(1); got != 5*time.Second {
		t.Fatalf("first retry=%s", got)
	}
	if got := taskRetryBackoff(99); got != 320*time.Second {
		t.Fatalf("capped retry=%s", got)
	}
}

func TestMissingResultCapabilitiesAcceptsActiveSpecialistRole(t *testing.T) {
	result := ResultEnvelope{Plan: &PlanProposal{Tasks: []TaskProposal{
		{ClientKey: "patents", RequiredCapability: "patent_scout"},
		{ClientKey: "verify", RequiredCapability: "validator"},
	}}}
	members := []FleetMember{
		{AgentID: "specialist", Role: "patent_scout", Status: "active"},
		{AgentID: "validator", Role: "validator", Status: "active"},
	}
	if missing := missingResultCapabilities(result, members); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
	members[0].Status = "pending_prompt_review"
	missing := missingResultCapabilities(result, members)
	if len(missing) != 1 || missing[0] != "patent_scout" {
		t.Fatalf("missing=%v", missing)
	}
}

func TestRemediationRoutesMarginalGainToEvidenceReplan(t *testing.T) {
	kind, objective, capability := remediationTask(GateResult{Findings: []GateFinding{{Code: "marginal_gain_not_saturated"}}})
	if kind != TaskKindReplan || capability != "lead" || !strings.Contains(objective, "smallest evidence-producing") {
		t.Fatalf("kind=%s capability=%s objective=%q", kind, capability, objective)
	}
}

func TestRemediationRoutesReportQualityFailureToSynthesis(t *testing.T) {
	kind, objective, capability := remediationTask(GateResult{Findings: []GateFinding{{Code: "quality_evaluation_failed"}}})
	if kind != TaskKindSynthesize || capability != "reporter" || !strings.Contains(objective, "report") {
		t.Fatalf("kind=%s capability=%s objective=%q", kind, capability, objective)
	}
}

func TestTerminalRemediationFailureStopsAfterInitialPlanExhaustsAttempts(t *testing.T) {
	run := Run{GoalVersion: 1, PlanVersion: 1}
	tasks := []Task{{
		ID: "plan-1", Kind: TaskKindPlan, Status: TaskStatusBlocked,
		GoalVersion: 1, PlanVersion: 1, TerminalReason: "result_not_submitted",
	}}
	reason, failed := terminalRemediationFailure(run, tasks, TaskKindReplan, "repair plan")
	if !failed || !strings.Contains(reason, "plan-1") || !strings.Contains(reason, "result_not_submitted") {
		t.Fatalf("failed=%v reason=%q", failed, reason)
	}
}

func TestTerminalRemediationFailureStopsSameFailedControlTaskFromRepeating(t *testing.T) {
	run := Run{GoalVersion: 2, PlanVersion: 3}
	tasks := []Task{
		{ID: "plan-ok", Kind: TaskKindPlan, Status: TaskStatusSucceeded, GoalVersion: 2, PlanVersion: 3},
		{ID: "replan-failed", ClientKey: "control:replan:2:3:1", Kind: TaskKindReplan, Objective: "repair evidence", Status: TaskStatusBlocked, GoalVersion: 2, PlanVersion: 3, TerminalReason: "inbox_task_failed"},
	}
	reason, failed := terminalRemediationFailure(run, tasks, TaskKindReplan, "repair evidence")
	if !failed || !strings.Contains(reason, "replan-failed") {
		t.Fatalf("failed=%v reason=%q", failed, reason)
	}
}

func TestTerminalRemediationFailureAllowsSucceededOrOlderVersionWork(t *testing.T) {
	run := Run{GoalVersion: 2, PlanVersion: 3}
	tasks := []Task{
		{ID: "old-failure", ClientKey: "control:replan:1:1:1", Kind: TaskKindReplan, Objective: "repair evidence", Status: TaskStatusBlocked, GoalVersion: 1, PlanVersion: 1},
		{ID: "current-success", ClientKey: "control:replan:2:3:1", Kind: TaskKindReplan, Objective: "repair evidence", Status: TaskStatusSucceeded, GoalVersion: 2, PlanVersion: 3},
	}
	if reason, failed := terminalRemediationFailure(run, tasks, TaskKindReplan, "repair evidence"); failed {
		t.Fatalf("succeeded/old remediation blocked progress: %q", reason)
	}
}

func TestTaskPromptCarriesOnlyDurableSubmissionProtocol(t *testing.T) {
	run := Run{SessionID: "session-1", Goal: "Investigate", GoalVersion: 2, PlanVersion: 3, OrchestratorVersion: OrchestratorVersionV1}
	task := Task{ID: "task-1", Kind: TaskKindDiscover, Objective: "Find evidence", ExpectedResult: "research_evidence_v1"}
	attempt := Attempt{ID: "attempt-1", DispatchKey: "dispatch-1"}
	prompt, err := buildTaskPrompt(run, task, attempt, RunSnapshot{Contract: ResearchContract{
		Language: "zh", Scope: []byte(`{"market":"CN"}`), SourcePolicy: []byte(`{"prefer_primary":true}`),
	}}, []FleetMember{{Role: "scout", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"multica research task-result session-1 task-1 attempt-1",
		"Do not use graph-append, source-upsert, report-patch, or stage-eval",
		"every quote must occur exactly in that snapshot",
		"Active fleet roles: `scout`",
		"Contract language: zh",
		`Contract scope: ` + "`{\"market\":\"CN\"}`",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q:\n%s", required, prompt)
		}
	}
}

func TestTaskPromptV1IsPinned(t *testing.T) {
	run := Run{SessionID: "session-1", Goal: "Investigate", GoalVersion: 2, PlanVersion: 3, OrchestratorVersion: OrchestratorVersionV1}
	task := Task{ID: "task-1", Kind: TaskKindDiscover, Objective: "Find evidence", ExpectedResult: "research_evidence_v1"}
	attempt := Attempt{ID: "attempt-1", DispatchKey: "dispatch-1"}
	prompt, err := buildTaskPrompt(run, task, attempt, RunSnapshot{Contract: ResearchContract{
		Language: "zh", Scope: []byte(`{"market":"CN"}`), SourcePolicy: []byte(`{"prefer_primary":true}`),
	}}, []FleetMember{{Role: "scout", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(prompt))
	if got, want := hex.EncodeToString(sum[:]), "d8c7121001c1b211782d934e306ebab5dc385e06a3acdb65d04694cd3a1fa489"; got != want {
		t.Fatalf("research-run-v1 prompt changed: got %s; introduce a new orchestrator version for behavioral changes", got)
	}
}

func TestTaskPromptV2CarriesAndPinsReportQualityContract(t *testing.T) {
	run := Run{SessionID: "session-2", Goal: "Investigate deeply", GoalVersion: 1, PlanVersion: 1, DepthTier: "deep", OrchestratorVersion: OrchestratorVersionV2}
	task := Task{ID: "task-2", Kind: TaskKindSynthesize, Objective: "Produce report", RequiredCapability: "reporter", ExpectedResult: "research_report_v2"}
	attempt := Attempt{ID: "attempt-2", DispatchKey: "dispatch-2"}
	prompt, err := buildTaskPrompt(run, task, attempt, RunSnapshot{Contract: ResearchContract{Language: "zh"}}, []FleetMember{{Role: "reporter", Status: "active"}, {Role: "validator", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"answer_claim_key",
		"discover/deep_read/verify/counter_search=research_evidence_v2",
		"synthesize=reporter; quality_gate=validator; citation_audit=validator",
		"both audit tasks directly depend on a synthesize task",
		"citations:[{id,index,source_id,label,quote,locator}]",
		"claims=[{claim_key,section_id,anchor_quote}]",
		"at least 7 sections, 160 substantive characters per section, and 160 in the conclusion",
		"dimension_findings",
		"reviewed_claim_keys covering every report Claim",
		"report written by another Agent",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q:\n%s", required, prompt)
		}
	}
	sum := sha256.Sum256([]byte(prompt))
	if got, want := hex.EncodeToString(sum[:]), "a188db9f78c4413823f8e6060030244fc6d97db8e25cc8a9d1131dce5549fac8"; got != want {
		t.Fatalf("research-run-v2 prompt changed: got %s; introduce a new orchestrator version for behavioral changes", got)
	}
}

func TestTaskPromptV3CarriesAcceptedGeneralResearchMethod(t *testing.T) {
	run := Run{SessionID: "session-3", Goal: "Choose a deployment architecture", GoalVersion: 1, PlanVersion: 2, DepthTier: "standard", OrchestratorVersion: OrchestratorVersionV3}
	task := Task{ID: "task-3", Kind: TaskKindCounterSearch, Objective: "Find evidence that reverses the current ranking", RequiredCapability: "validator", ExpectedResult: "research_evidence_v3", GoalVersion: 1, PlanVersion: 2}
	attempt := Attempt{ID: "attempt-3", DispatchKey: "dispatch-3"}
	method := &ResearchMethod{
		GoalVersion: 1, PlanVersion: 2,
		DecisionQuestion:        "Which architecture meets the workload and recovery constraints?",
		MethodRationale:         "Compare measured behavior under the same workload and test failure assumptions.",
		AnalysisMethods:         []string{"Controlled comparison", "Failure-mode analysis"},
		EvidenceRequirements:    []string{"Comparable workload measurements"},
		InclusionCriteria:       []string{"Same workload boundary"},
		ExclusionCriteria:       []string{"Unreproducible anecdotes"},
		SourceStrategy:          []string{"Measurements and operational records"},
		CounterevidenceStrategy: []string{"Search for recovery failures that reverse the ranking"},
		StoppingConditions:      []string{"Material failure modes are verified or unresolved explicitly"},
		Uncertainties:           []string{"Future traffic distribution"},
		PlanningRisks:           []string{"Benchmark mismatch"},
	}
	prompt, err := buildTaskPrompt(run, task, attempt, RunSnapshot{Contract: ResearchContract{Language: "zh"}, Method: method}, []FleetMember{{Role: "validator", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"schema_version=3",
		"plan.method={decision_question,method_rationale,analysis_methods,evidence_requirements,counterevidence_strategy,stopping_conditions}",
		"Which architecture meets the workload and recovery constraints?",
		"Controlled comparison",
		"Every non-plan task inherits the accepted method exactly",
		"Do not impose academic publication protocols",
		"counter_search=research_evidence_v3",
		"Counter-search follows the accepted falsification conditions",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q:\n%s", required, prompt)
		}
	}
}

func TestTaskPromptRejectsUnsupportedOrchestratorVersion(t *testing.T) {
	_, err := buildTaskPrompt(Run{OrchestratorVersion: "research-run-v999"}, Task{}, Attempt{}, RunSnapshot{}, nil)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err=%v", err)
	}
}
