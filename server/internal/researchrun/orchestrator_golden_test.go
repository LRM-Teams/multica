package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type orchestratorContractGolden struct {
	Version              string `json:"version"`
	PromptSHA256         string `json:"prompt_sha256"`
	AcceptedPlanSHA256   string `json:"accepted_plan_sha256"`
	NewerSchemaErrorText string `json:"newer_schema_error_text"`
}

func TestOrchestratorContractsMatchGoldenFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden/orchestrator_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []orchestratorContractGolden
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatalf("golden versions=%d want=5", len(fixtures))
	}
	seen := map[string]bool{}
	for _, fixture := range fixtures {
		t.Run(fixture.Version, func(t *testing.T) {
			if seen[fixture.Version] {
				t.Fatalf("duplicate golden version %q", fixture.Version)
			}
			seen[fixture.Version] = true
			prompt, err := goldenTaskPrompt(fixture.Version)
			if err != nil {
				t.Fatal(err)
			}
			promptSum := sha256.Sum256([]byte(prompt))
			promptHash := hex.EncodeToString(promptSum[:])
			if promptHash != fixture.PromptSHA256 {
				t.Errorf("prompt hash=%s want=%s; old orchestrator prompts are immutable", promptHash, fixture.PromptSHA256)
			}

			result, task := goldenPlanResult(t, fixture.Version)
			resultRaw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			_, resultHash, err := DecodeAndValidateResultForVersion(fixture.Version, resultRaw, task, DefaultRunConfig("standard"))
			if err != nil {
				t.Fatalf("accepted golden plan failed validation: %v", err)
			}
			if resultHash != fixture.AcceptedPlanSHA256 {
				t.Errorf("accepted plan hash=%s want=%s", resultHash, fixture.AcceptedPlanSHA256)
			}

			result.SchemaVersion++
			newerRaw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = DecodeAndValidateResultForVersion(fixture.Version, newerRaw, task, DefaultRunConfig("standard")); err == nil || !strings.Contains(err.Error(), fixture.NewerSchemaErrorText) {
				t.Fatalf("newer schema error=%v want text %q", err, fixture.NewerSchemaErrorText)
			}
		})
	}
	for _, version := range []string{OrchestratorVersionV1, OrchestratorVersionV2, OrchestratorVersionV3, OrchestratorVersionV4, OrchestratorVersionV5} {
		if !seen[version] {
			t.Errorf("missing golden fixture for %s", version)
		}
	}
}

func goldenTaskPrompt(version string) (string, error) {
	suffix := strings.TrimPrefix(version, "research-run-")
	run := Run{
		SessionID: "golden-session", Goal: "Choose an operating model from verified evidence",
		GoalVersion: 2, PlanVersion: 3, DepthTier: "standard", OrchestratorVersion: version,
	}
	task := Task{
		ID: "golden-task", Kind: TaskKindSynthesize, Objective: "Integrate the verified findings into a decision report",
		RequiredCapability: "reporter", ExpectedResult: "research_report_" + suffix, GoalVersion: 2, PlanVersion: 3,
	}
	if version == OrchestratorVersionV5 {
		task.AcceptanceCriteria = json.RawMessage(`{"remediation":{"target_findings":[{"metadata":{"defects":[{"client_key":"defect-boundary","required_change":"retain the verified operating boundary"}]}}]}}`)
	}
	method := &ResearchMethod{
		GoalVersion: 2, PlanVersion: 3,
		DecisionQuestion:        "Which operating model satisfies the verified constraints?",
		MethodRationale:         "Compare traceable evidence under the same boundary and investigate decision-reversing failures.",
		AnalysisMethods:         []string{"Constraint comparison", "Failure-mode analysis"},
		EvidenceRequirements:    []string{"Traceable observations for every material claim"},
		InclusionCriteria:       []string{"Evidence inside the declared operating boundary"},
		ExclusionCriteria:       []string{"Unverifiable summaries"},
		SourceStrategy:          []string{"Controlling records and reproducible measurements"},
		CounterevidenceStrategy: []string{"Search for failures that reverse the ranking"},
		StoppingConditions:      []string{"Required claims are verified or explicitly unresolved"},
		Uncertainties:           []string{"Future workload distribution"},
		PlanningRisks:           []string{"Incomparable measurements"},
		CreatedByTaskID:         "golden-plan-task", CreatedByAgentID: "golden-lead",
	}
	if version == OrchestratorVersionV4 || version == OrchestratorVersionV5 {
		method.EvidenceStandards = []EvidenceStandard{{
			ClientKey: "verified-boundary", Purpose: "Establish the operating boundary",
			MinimumIndependentSources: 1, RequiredSourceTraits: []string{"official_record"},
			MinimumStrength: 0.8, MinimumDirectness: 0.9, MinimumMethodFit: 0.9,
		}}
	}
	if version == OrchestratorVersionV1 || version == OrchestratorVersionV2 {
		method = nil
	}
	snapshot := RunSnapshot{
		Contract: ResearchContract{
			GoalVersion: 2, Goal: run.Goal, Scope: json.RawMessage(`{"region":"CN","period":"2025"}`),
			Audience: "technical decision maker", Freshness: "as of 2025-08-01", Language: "zh-CN",
			SourcePolicy: json.RawMessage(`{"prefer_primary":true}`),
		},
		Method: method,
		Questions: []Question{{
			ID: "golden-question", ClientKey: "operating-boundary", Kind: QuestionKindDimension,
			Question: "What operating boundary controls the decision?", Required: true, Status: QuestionStatusAnswered,
			Coverage: 1, GoalVersion: 2, PlanVersion: 3,
		}},
		Sources:      []SourceSnapshotView{{ID: "golden-source"}},
		Observations: []Observation{{ID: "golden-observation"}},
		Claims:       []Claim{{ID: "golden-claim", ClientKey: "boundary-claim", Text: "The verified boundary applies.", Status: ClaimStatusSupported}},
	}
	return buildTaskPrompt(run, task, Attempt{ID: "golden-attempt", DispatchKey: "golden-dispatch"}, snapshot, []FleetMember{
		{AgentID: "golden-lead", Role: "lead", Status: "active", IsLead: true},
		{AgentID: "golden-reporter", Role: "reporter", Status: "active"},
		{AgentID: "golden-validator", Role: "validator", Status: "active"},
	})
}

func goldenPlanResult(t *testing.T, version string) (ResultEnvelope, Task) {
	t.Helper()
	var result ResultEnvelope
	switch version {
	case OrchestratorVersionV1:
		result = validPlanResult(t)
	case OrchestratorVersionV2:
		result = validPlanResult(t)
		result.SchemaVersion = 2
		result.Plan.Tasks[0].ExpectedResult = "research_evidence_v2"
		result.Plan.Tasks = append(result.Plan.Tasks,
			TaskProposal{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write report", RequiredCapability: "reporter", ExpectedResult: "research_report_v2", Priority: 0.7, DependsOn: []string{"discover-1"}},
			TaskProposal{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Review report", RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
			TaskProposal{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit citations", RequiredCapability: "validator", ExpectedResult: "research_citation_audit_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
		)
	case OrchestratorVersionV3:
		result = validV3PlanResult(t)
	case OrchestratorVersionV4:
		result = validV4PlanResult(t)
	case OrchestratorVersionV5:
		result = validV4PlanResult(t)
		result.SchemaVersion = 5
		for index := range result.Plan.Tasks {
			result.Plan.Tasks[index].ExpectedResult = translateResultKind(result.Plan.Tasks[index].ExpectedResult, "_v4", "_v5")
		}
	default:
		t.Fatalf("unsupported golden version %q", version)
	}
	suffix := strings.TrimPrefix(version, "research-run-")
	return result, Task{Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_" + suffix}
}
