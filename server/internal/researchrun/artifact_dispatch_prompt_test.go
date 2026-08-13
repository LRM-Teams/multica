package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvaluationPrivatePromptOnlyForGraderTasks(t *testing.T) {
	run := Run{SessionID: "session", Goal: "goal", DepthTier: "standard", OrchestratorVersion: OrchestratorVersionV1, GoalVersion: 1, PlanVersion: 1}
	attempt := Attempt{ID: "attempt", DispatchKey: "dispatch"}
	snapshot := RunSnapshot{
		Contract: ResearchContract{Language: "English"},
		EvaluationPrivate: []EvaluationPrivateContext{{
			ID: "eval-1", Stage: "delivery", Findings: json.RawMessage(`[{"code":"hidden-rubric"}]`),
		}},
	}
	graderTask := Task{ID: "grader", Kind: TaskKindQualityGate, GoalVersion: 1, PlanVersion: 1}
	graderPrompt, err := buildTaskPrompt(run, graderTask, attempt, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(graderPrompt, "hidden-rubric") || !strings.Contains(graderPrompt, "Evaluation-private grader context") {
		t.Fatal("grader prompt missing private context")
	}
	subjectTask := Task{ID: "subject", Kind: TaskKindDiscover, GoalVersion: 1, PlanVersion: 1}
	subjectPrompt, err := buildTaskPrompt(run, subjectTask, attempt, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(subjectPrompt, "hidden-rubric") {
		t.Fatal("evaluated subject prompt leaked private context")
	}
}

func TestManifestPrincipalHeaderRoundTrip(t *testing.T) {
	members := []FleetMember{
		{AgentID: "agent-lead", Role: "lead", Status: "active", IsLead: true},
		{AgentID: "agent-scout", Role: "scout", Status: "inactive"},
	}
	encoded, err := encodeManifestPrincipalHeader(members)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeManifestPrincipalHeader(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(members) {
		t.Fatalf("decoded members=%d want=%d", len(decoded), len(members))
	}
	for i := range members {
		if decoded[i].AgentID != members[i].AgentID || decoded[i].Role != members[i].Role ||
			decoded[i].Status != members[i].Status || decoded[i].IsLead != members[i].IsLead {
			t.Fatalf("decoded[%d]=%+v want=%+v", i, decoded[i], members[i])
		}
	}
}

func TestManifestFilteredPromptChangesDispatchRequestHash(t *testing.T) {
	run := Run{
		SessionID:   "20000000-0000-4000-8000-000000000001",
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Goal:        "goal", DepthTier: "standard", OrchestratorVersion: OrchestratorVersionV1,
		GoalVersion: 1, PlanVersion: 1,
	}
	task := Task{
		ID:        "30000000-0000-4000-8000-000000000003",
		SessionID: run.SessionID, WorkspaceID: run.WorkspaceID,
		Kind: TaskKindDiscover, GoalVersion: 1, PlanVersion: 1,
		ExpectedResult: "research_evidence_v1",
		Objective:      "discover",
	}
	attempt := Attempt{
		ID:        "40000000-0000-4000-8000-000000000004",
		SessionID: run.SessionID, WorkspaceID: run.WorkspaceID,
		TaskID: task.ID, DispatchKey: "dispatch-key",
	}
	members := []FleetMember{{AgentID: "agent-1", Role: "scout", Status: "active"}}
	live := RunSnapshot{
		Run:          run,
		Contract:     ResearchContract{Language: "English", Audience: "team", Freshness: "recent"},
		Sources:      []SourceSnapshotView{{ID: "source-a"}, {ID: "source-b"}},
		Observations: []Observation{{ID: "obs-a"}},
		Claims:       []Claim{{ID: "claim-a"}},
	}
	filtered := filterRunSnapshotByManifest(live, map[string]struct{}{
		"source-a": {}, "obs-a": {}, "claim-a": {},
	})
	livePrompt, err := buildTaskPrompt(run, task, attempt, live, members)
	if err != nil {
		t.Fatal(err)
	}
	manifestPrompt, err := buildTaskPrompt(run, task, attempt, filtered, members)
	if err != nil {
		t.Fatal(err)
	}
	liveRequest := DispatchRequest{
		Run: run, Task: task, AttemptID: attempt.ID, AgentID: "agent-1",
		Prompt: livePrompt, Key: "dispatch-key",
	}
	manifestRequest := liveRequest
	manifestRequest.Prompt = manifestPrompt
	liveHash, err := HashDispatchRequest(liveRequest)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash, err := HashDispatchRequest(manifestRequest)
	if err != nil {
		t.Fatal(err)
	}
	if liveHash == manifestHash {
		t.Fatal("expected manifest-filtered prompt to change request hash")
	}
	if err = verifyManifestPromptShadow(livePrompt, manifestPrompt, live, filtered); err != nil {
		t.Fatalf("expected filter delta to allow prompt change: %v", err)
	}
}

func TestManifestFilteredPromptMatchesLiveWhenCountsEqual(t *testing.T) {
	run := Run{
		SessionID:   "20000000-0000-4000-8000-000000000002",
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Goal:        "goal", DepthTier: "standard", OrchestratorVersion: OrchestratorVersionV1,
		GoalVersion: 1, PlanVersion: 1,
	}
	task := Task{
		ID:        "30000000-0000-4000-8000-000000000005",
		SessionID: run.SessionID, WorkspaceID: run.WorkspaceID,
		Kind: TaskKindPlan, GoalVersion: 1, PlanVersion: 1,
		ExpectedResult: "research_plan_v1", Objective: "plan",
	}
	attempt := Attempt{ID: "40000000-0000-4000-8000-000000000006", TaskID: task.ID, DispatchKey: "key"}
	members := []FleetMember{{AgentID: "agent-1", Role: "lead", Status: "active"}}
	snapshot := RunSnapshot{
		Run:      run,
		Contract: ResearchContract{Language: "English", Audience: "team", Freshness: "recent"},
	}
	first, err := buildTaskPrompt(run, task, attempt, snapshot, members)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildTaskPrompt(run, task, attempt, snapshot, members)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyManifestPromptShadow(first, second, snapshot, snapshot); err != nil {
		t.Fatalf("identical prompts should pass shadow: %v", err)
	}
}

func TestManifestOwnedPromptDoesNotRequireCallerShadow(t *testing.T) {
	snapshot := RunSnapshot{Claims: []Claim{{ID: "claim-1"}}}
	if err := verifyManifestPromptShadow("", "manifest-owned prompt", snapshot, snapshot); err != nil {
		t.Fatalf("manifest-owned prompt: %v", err)
	}
}

func TestEncodeDispatchRequestEmbedsManifestPrompt(t *testing.T) {
	request := DispatchRequest{
		Run: Run{SessionID: "session-1", WorkspaceID: "workspace-1"},
		Task: Task{
			ID: "task-1", AcceptanceCriteria: json.RawMessage(`{}`),
			Kind: TaskKindDiscover, ExpectedResult: "research_evidence_v1",
		},
		AttemptID: "attempt-1", AgentID: "agent-1",
		Prompt: "manifest-bound prompt bytes", Key: "dispatch-key",
	}
	encoded, hash, err := encodeDispatchRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || len(encoded) == 0 {
		t.Fatalf("encoded=%q hash=%q", encoded, hash)
	}
	var decoded DispatchRequest
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Prompt != request.Prompt || decoded.RequestHash != hash {
		t.Fatalf("decoded=%+v", decoded)
	}
}
