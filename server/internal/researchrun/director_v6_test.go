package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestV6DirectorBriefSurvivesItsOwnOperationalEvents(t *testing.T) {
	cycleID := "00000000-0000-4000-8000-000000000001"
	workID := "00000000-0000-4000-8000-000000000002"
	briefID := "00000000-0000-4000-8000-000000000003"
	for _, test := range []struct {
		name, eventType, payload string
		want                     bool
	}{
		{name: "cycle created", eventType: "v6_director_cycle_created", payload: `{"cycle_id":"` + cycleID + `","work_item_id":"` + workID + `","brief_id":"` + briefID + `"}`, want: true},
		{name: "dispatch prepared", eventType: "v6_work_item_dispatch_prepared", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "dispatch completed", eventType: "v6_work_item_dispatched", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "attempt recovered", eventType: "v6_work_item_recovered", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "brief acknowledged", eventType: "v6_director_brief_page_acknowledged", payload: `{"brief_id":"` + briefID + `"}`, want: true},
		{name: "proposal received", eventType: "v6_work_submission_received", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "other work", eventType: "v6_work_item_dispatched", payload: `{"work_item_id":"00000000-0000-4000-8000-000000000009"}`},
		{name: "material event", eventType: "v6_branch_created", payload: `{"work_item_id":"` + workID + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isV6DirectorCycleOperationalEvent(test.eventType, json.RawMessage(test.payload), cycleID, workID, briefID); got != test.want {
				t.Fatalf("allowed=%v want=%v", got, test.want)
			}
		})
	}
}

type teamV6StoreStub struct {
	added    AddV6TeamMemberInput
	archived ArchiveV6TeamMemberInput
}

func (s *teamV6StoreStub) AddV6TeamMember(_ context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	s.added = in
	return V6TeamMember{AgentID: in.AgentID}, nil
}
func (s *teamV6StoreStub) ArchiveV6TeamMember(_ context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	s.archived = in
	return V6TeamMember{ID: in.MembershipID, State: V6TeamArchived}, nil
}

func (s *teamV6StoreStub) FindActiveV6TeamMemberByAgent(context.Context, string, string, string) (V6TeamMember, bool, error) {
	return V6TeamMember{}, false, nil
}

func TestV6TeamCapacityRules(t *testing.T) {
	store := &teamV6StoreStub{}
	_, err := (teamV6Module{store: store}).Add(context.Background(), AddV6TeamMemberInput{
		WorkspaceID: "workspace", RunID: "run", AgentID: "agent", MissionPrompt: "Investigate the assigned question.",
	})
	if err != nil || store.added.AgentID != "agent" {
		t.Fatalf("add member: input=%+v err=%v", store.added, err)
	}
}

func TestV6TeamArchiveRequiresReason(t *testing.T) {
	_, err := (teamV6Module{store: &teamV6StoreStub{}}).Archive(context.Background(), ArchiveV6TeamMemberInput{MembershipID: "membership"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

type directorV6StoreStub struct{}

func (directorV6StoreStub) AssignV6Director(_ context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{AgentID: in.AgentID}, nil
}
func (directorV6StoreStub) MarkV6DirectorUnavailable(_ context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{ID: in.AssignmentID, Status: "unavailable"}, nil
}

func TestV6DirectorAssignmentValidation(t *testing.T) {
	_, err := (directorModule{store: directorV6StoreStub{}}).Assign(context.Background(), AssignV6DirectorInput{AgentID: "agent", UserID: "user"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

func TestV6DirectorFailureValidation(t *testing.T) {
	got, err := (directorModule{store: directorV6StoreStub{}}).MarkUnavailable(context.Background(), MarkV6DirectorUnavailableInput{AssignmentID: "assignment", FailureClass: "quota", ClientRequestID: "request"})
	if err != nil || got.Status != "unavailable" {
		t.Fatalf("assignment=%+v err=%v", got, err)
	}
}

func TestV6DirectorBriefIsBoundedAndPaged(t *testing.T) {
	branches := make([]any, 257)
	for i := range branches {
		branches[i] = map[string]any{
			"branch":    map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), "state_version": 1},
			"objective": "Investigate", "scope": map[string]any{}, "status": "active", "frontier_nodes": []any{}, "has_more": false,
		}
	}
	facts := DirectorBriefFacts{
		WorkspaceID: "00000000-0000-4000-8000-000000000001", RunID: "00000000-0000-4000-8000-000000000002",
		AssignmentID: "00000000-0000-4000-8000-000000000003", DirectorGeneration: 1, StateVersion: 1,
		Goal:          map[string]any{"goal_version": 1, "goal": "Research", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		DirectorState: "available", Team: []any{map[string]any{"agent_id": "00000000-0000-4000-8000-000000000004", "membership_id": "00000000-0000-4000-8000-000000000005", "state": "idle", "mission_summary": "Direct"}}, Branches: branches,
		TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{}, UnresolvedDisputes: []any{}, Steering: []any{},
	}
	brief, err := (contextCompilerModule{}).CompileDirectorBrief(facts, time.Unix(1, 0))
	if err != nil || len(brief.Pages) != 5 {
		t.Fatalf("pages=%d err=%v", len(brief.Pages), err)
	}
}

type directorBriefStoreStub struct {
	acknowledged AcknowledgeV6DirectorBriefInput
}

func (*directorBriefStoreStub) LoadDirectorBriefFacts(context.Context, StartV6DirectorCycleInput) (DirectorBriefFacts, error) {
	return DirectorBriefFacts{}, nil
}
func (*directorBriefStoreStub) PersistDirectorCycle(context.Context, StartV6DirectorCycleInput, CompiledDirectorBrief) (V6DirectorCycle, error) {
	return V6DirectorCycle{}, nil
}
func (*directorBriefStoreStub) LoadDirectorBriefPage(context.Context, V6AttemptAccess, string) (V6DirectorBriefPage, error) {
	return V6DirectorBriefPage{}, nil
}
func (s *directorBriefStoreStub) AcknowledgeDirectorBriefPage(_ context.Context, in AcknowledgeV6DirectorBriefInput) error {
	s.acknowledged = in
	return nil
}

func TestV6DirectorBriefAcknowledgementDelegates(t *testing.T) {
	store := &directorBriefStoreStub{}
	in := AcknowledgeV6DirectorBriefInput{ClientRequestID: "request", PageKey: "page"}
	if err := (directorBriefModule{store: store}).Acknowledge(context.Background(), in); err != nil || store.acknowledged.PageKey != "page" {
		t.Fatalf("ack=%+v err=%v", store.acknowledged, err)
	}
}
