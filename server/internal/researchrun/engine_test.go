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

func TestTaskPromptRejectsUnsupportedOrchestratorVersion(t *testing.T) {
	_, err := buildTaskPrompt(Run{OrchestratorVersion: "research-run-v999"}, Task{}, Attempt{}, RunSnapshot{}, nil)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err=%v", err)
	}
}
