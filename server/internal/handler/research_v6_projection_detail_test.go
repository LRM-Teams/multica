package handler

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researcheval"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6CanonicalDetailFieldsMatchAutonomyEvaluationContract(t *testing.T) {
	if !slices.Equal(researchV6CanonicalDetailFields, researcheval.RequiredProjectionDetailFields) {
		t.Fatalf("handler detail fields = %v, evaluation contract = %v", researchV6CanonicalDetailFields, researcheval.RequiredProjectionDetailFields)
	}
}

func TestResearchV6CompatibilityProjectionCarriesCompleteCanonicalDetails(t *testing.T) {
	actor := "agent-1"
	snapshot := researchrun.RunSnapshot{
		Run: researchrun.Run{
			SessionID: "run-1", CreatedBy: "user-1", Goal: "verify the market",
			Status: researchrun.RunStatusFailed, CurrentStage: "s4_delivery",
			GoalVersion: 2, PlanVersion: 3, StateVersion: 9,
			Stats:      researchrun.RunStats{AcceptedResults: 1, ClaimsCreated: 1},
			StopReason: "quality gate failed", LastProgressAt: time.Unix(1_700_000_000, 0).UTC(),
		},
		Contract: researchrun.ResearchContract{Goal: "verify the market"},
		Method: &researchrun.ResearchMethod{
			GoalVersion: 2, PlanVersion: 3, DecisionQuestion: "enter market?",
			AnalysisMethods: []string{"triangulation"}, CreatedByTaskID: "task-plan", CreatedByAgentID: actor,
		},
		Questions: []researchrun.Question{{
			ID: "question-1", Kind: researchrun.QuestionKindGap, Question: "is demand verified?",
			Status: researchrun.QuestionStatusAnswered, Required: true, AnswerClaimID: "claim-1",
			CreatedByTaskID: "task-1", TerminalExplanation: "claim accepted",
		}},
		Tasks: []researchrun.Task{{
			ID: "task-1", QuestionID: "question-1", Kind: researchrun.TaskKindVerify,
			Objective: "verify demand", RequiredCapability: "source verification",
			ExpectedResult: "verified demand evidence", AcceptanceCriteria: json.RawMessage(`{"minimum_sources":2}`),
			Status: researchrun.TaskStatusFailed, AssignedAgentID: actor, AttemptCount: 1, MaxAttempts: 3,
			TerminalReason: "source unavailable",
		}},
		Attempts: []researchrun.Attempt{{
			ID: "attempt-1", TaskID: "task-1", AttemptNumber: 1, AssignedAgentID: actor,
			DispatchKey: "dispatch-1", Status: researchrun.AttemptStatusFailed,
			ExecutionTarget: researchrun.ExecutionTarget{Adapter: "agent_inbox", AgentID: actor, RuntimeID: "runtime-1", Provider: "codex", Model: "model-1"},
			ResultHash:      "sha256:result", FailureClass: "runtime_lost", SourceFailureReason: "queued_expired",
			Diagnostics: "runtime disappeared", PendingFailure: "runtime_lost", PendingRetryable: true,
			DispatchedAt: time.Unix(1_700_000_010, 0).UTC(),
		}},
		Claims: []researchrun.Claim{{
			ID: "claim-1", ProducedByTaskID: "task-1", Text: "demand is not verified",
			Significance: "blocks market entry", Confidence: 0.7, Status: researchrun.ClaimStatusDisputed,
			EvidenceStandardKey: "two-independent-sources", Resolution: "requires another source",
			Evidence: []researchrun.ClaimEvidence{{ObservationID: "observation-1", Relation: "supports", Strength: 0.8, VerificationStatus: "verified"}},
		}},
		Gate: researchrun.GateResult{Findings: []researchrun.GateFinding{{Code: "coverage_low", Severity: "blocking", Message: "insufficient coverage"}}},
	}

	legacyNodes, legacyEdges := projectRunV2Graph(snapshot)
	nodeIDs := make(map[string]string, len(legacyNodes))
	nodes := make([]researchV6ProjectionNode, 0, len(legacyNodes))
	for _, legacy := range legacyNodes {
		mapped, err := mapResearchV6Node(snapshot.Run.SessionID, legacy)
		if err != nil {
			t.Fatal(err)
		}
		nodeIDs[legacy.ID] = mapped.ID
		nodes = append(nodes, mapped)
	}
	edges := make([]researchV6ProjectionEdge, 0, len(legacyEdges))
	for _, legacy := range legacyEdges {
		edges = append(edges, researchV6ProjectionEdge{FromNodeID: nodeIDs[legacy.FromNodeID], ToNodeID: nodeIDs[legacy.ToNodeID]})
	}
	enrichResearchV6TopologyDetails(nodes, edges)

	byKind := make(map[string]map[string]any, len(nodes))
	for _, node := range nodes {
		detail, ok := node.Detail.(map[string]any)
		if !ok {
			t.Fatalf("node %s detail type = %T", node.ID, node.Detail)
		}
		assertResearchV6DetailComplete(t, node.EntityKind, detail)
		byKind[node.EntityKind] = detail
	}
	for _, kind := range []string{"goal", runGraphKindQuestion, runGraphKindTask, runGraphKindAttempt, runGraphKindClaim, runGraphKindGate} {
		if byKind[kind] == nil {
			t.Fatalf("missing compatibility node kind %q", kind)
		}
	}

	if byKind[runGraphKindTask]["objective"] != "verify demand" {
		t.Fatalf("task objective = %#v", byKind[runGraphKindTask]["objective"])
	}
	if byKind[runGraphKindQuestion]["actor"] != actor {
		t.Fatalf("question actor = %#v", byKind[runGraphKindQuestion]["actor"])
	}
	attemptResult, _ := byKind[runGraphKindAttempt]["result"].(map[string]any)
	if byKind[runGraphKindAttempt]["actor"] != actor || attemptResult["result_hash"] != "sha256:result" {
		t.Fatalf("attempt detail = %#v", byKind[runGraphKindAttempt])
	}
	claimEvidence, _ := byKind[runGraphKindClaim]["evidence"].([]any)
	if len(claimEvidence) != 1 || claimEvidence[0].(map[string]any)["observation_id"] != "observation-1" {
		t.Fatalf("claim evidence = %#v", byKind[runGraphKindClaim]["evidence"])
	}
	gateDecision, _ := byKind[runGraphKindGate]["decision"].(map[string]any)
	if gateDecision["passed"] != false {
		t.Fatalf("gate decision = %#v", gateDecision)
	}
	if byKind[runGraphKindTask]["upstream"] == researchV6DetailNotApplicable || byKind[runGraphKindTask]["downstream"] == researchV6DetailNotApplicable {
		t.Fatalf("task topology = upstream %#v downstream %#v", byKind[runGraphKindTask]["upstream"], byKind[runGraphKindTask]["downstream"])
	}
}

func assertResearchV6DetailComplete(t *testing.T, kind string, detail map[string]any) {
	t.Helper()
	for _, field := range researchV6CanonicalDetailFields {
		value, exists := detail[field]
		if !exists || value == nil || value == "" {
			t.Fatalf("%s detail field %q = %#v", kind, field, value)
		}
	}
}

func TestCanonicalResearchV6DetailPreservesUnknownPayloadWithoutFabricatingSemantics(t *testing.T) {
	detail := canonicalResearchV6Detail("generic", "", nil, map[string]any{"kind": "future_kind", "opaque": "fact"})
	if detail["kind"] != "future_kind" || detail["opaque"] != "fact" {
		t.Fatalf("generic payload not preserved: %#v", detail)
	}
	if detail["purpose"] != researchV6DetailNotApplicable || detail["objective"] != researchV6DetailNotApplicable {
		t.Fatalf("generic detail fabricated semantics: %#v", detail)
	}
}
