package researchrun

import (
	"errors"
	"testing"
)

func TestApplyFrozenCoreContextReplacesLiveValuesAndPreservesListOrder(t *testing.T) {
	live := RunSnapshot{
		Run:       Run{SessionID: "run-1", Goal: "live goal"},
		Contract:  ResearchContract{Goal: "live contract"},
		Method:    &ResearchMethod{DecisionQuestion: "live method"},
		Questions: []Question{{ID: "q2", Question: "live q2"}, {ID: "q1", Question: "live q1"}, {ID: "new", Question: "post-dispatch"}},
		Tasks:     []Task{{ID: "t2", Objective: "live t2"}, {ID: "t1", Objective: "live t1"}, {ID: "new", Objective: "post-dispatch"}},
	}
	frozen, err := applyFrozenCoreContext(live, frozenCoreContext{
		Run:       &Run{SessionID: "run-1", Goal: "frozen goal"},
		Contract:  &ResearchContract{Goal: "frozen contract"},
		Method:    &ResearchMethod{DecisionQuestion: "frozen method"},
		Questions: map[string]Question{"q1": {ID: "q1", Question: "frozen q1"}, "q2": {ID: "q2", Question: "frozen q2"}},
		Tasks:     map[string]Task{"t1": {ID: "t1", Objective: "frozen t1"}, "t2": {ID: "t2", Objective: "frozen t2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Run.Goal != "frozen goal" || frozen.Contract.Goal != "frozen contract" || frozen.Method.DecisionQuestion != "frozen method" {
		t.Fatalf("core=%+v %+v %+v", frozen.Run, frozen.Contract, frozen.Method)
	}
	if len(frozen.Questions) != 2 || frozen.Questions[0].ID != "q2" || frozen.Questions[0].Question != "frozen q2" || frozen.Questions[1].ID != "q1" {
		t.Fatalf("questions=%+v", frozen.Questions)
	}
	if len(frozen.Tasks) != 2 || frozen.Tasks[0].ID != "t2" || frozen.Tasks[0].Objective != "frozen t2" || frozen.Tasks[1].ID != "t1" {
		t.Fatalf("tasks=%+v", frozen.Tasks)
	}
}

func TestApplyFrozenCoreContextRejectsUnorderableFrozenEntity(t *testing.T) {
	_, err := applyFrozenCoreContext(RunSnapshot{}, frozenCoreContext{
		Run: &Run{SessionID: "run-1"}, Contract: &ResearchContract{},
		Questions: map[string]Question{"missing": {ID: "missing"}}, Tasks: map[string]Task{},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err=%v", err)
	}
}
