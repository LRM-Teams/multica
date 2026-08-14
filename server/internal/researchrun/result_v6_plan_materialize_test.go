package researchrun

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestPrepareResearchV6PlanMaterializationResolvesCanonicalGraphAndTasks(t *testing.T) {
	plan, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, validResearchV6PlanFixture()))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	command, err := prepareResearchV6PlanMaterialization(sessionID, uuid.NewString(), uuid.NewString(), 7, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Result.Plan.Questions) != 1 || len(command.Result.Plan.Tasks) != 1 || len(command.InquiryGraph.Hypotheses) != 1 || len(command.InquiryGraph.Branches) != 1 || len(command.InquiryGraph.Edges) != 1 || len(command.Targets) != 1 {
		t.Fatalf("incomplete materialization: %+v", command)
	}
	hypothesisID := command.InquiryGraph.Hypotheses[0].ID
	if uuid.Validate(hypothesisID) != nil || command.Targets[0].EntityID != hypothesisID || command.Targets[0].TaskClientKey != "task.discover" {
		t.Fatalf("unresolved target: %+v", command.Targets[0])
	}
	if command.InquiryGraph.Edges[0].To.ID != hypothesisID || command.InquiryGraph.Hypotheses[0].QuestionID == "" {
		t.Fatalf("unresolved graph endpoints: %+v", command.InquiryGraph)
	}
	var criteria map[string]any
	if err = json.Unmarshal(command.Result.Plan.Tasks[0].AcceptanceCriteria, &criteria); err != nil {
		t.Fatal(err)
	}
	if plans, ok := criteria["search_plans"].([]any); !ok || len(plans) != 1 {
		t.Fatalf("search plan was not attached to task contract: %s", command.Result.Plan.Tasks[0].AcceptanceCriteria)
	}

	replay, err := prepareResearchV6PlanMaterialization(sessionID, command.InquiryGraph.AttemptID, command.InquiryGraph.AgentID, 7, plan)
	if err != nil {
		t.Fatal(err)
	}
	if replay.InquiryGraph.Hypotheses[0].ID != hypothesisID || replay.InquiryGraph.Edges[0].ID != command.InquiryGraph.Edges[0].ID {
		t.Fatalf("replay changed canonical identities")
	}
}

func TestPrepareResearchV6PlanMaterializationOrdersParentBranchesFirst(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	fixture["branches"] = []any{
		map[string]any{"client_key": "b.child", "parent_branch_key": "b.parent", "objective": "Child", "entry_conditions": []any{"open"}, "exit_conditions": []any{"done"}, "budget_share": 0.3},
		map[string]any{"client_key": "b.parent", "objective": "Parent", "entry_conditions": []any{"open"}, "exit_conditions": []any{"done"}, "budget_share": 0.4},
	}
	plan, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	command, err := prepareResearchV6PlanMaterialization(uuid.NewString(), uuid.NewString(), uuid.NewString(), 1, plan)
	if err != nil {
		t.Fatal(err)
	}
	if command.InquiryGraph.Branches[0].ClientKey != "b.parent" || command.InquiryGraph.Branches[1].ParentBranchID != command.InquiryGraph.Branches[0].ID {
		t.Fatalf("branches are not persistence ordered: %+v", command.InquiryGraph.Branches)
	}
}
