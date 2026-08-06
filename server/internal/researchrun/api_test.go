package researchrun

import (
	"reflect"
	"testing"
)

func TestResearchRunInterfaceIsFixedUseCaseBoundary(t *testing.T) {
	contract := reflect.TypeOf((*ResearchRun)(nil)).Elem()
	want := []string{
		"Archive",
		"Cancel",
		"Confirm",
		"Create",
		"ListFleetMembers",
		"NodeCommand",
		"Pause",
		"ReconcileDue",
		"Resume",
		"Snapshot",
		"Steer",
		"SubmitResult",
	}
	got := make([]string, 0, contract.NumMethod())
	for i := range contract.NumMethod() {
		got = append(got, contract.Method(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResearchRun methods=%v, want fixed use-case boundary %v", got, want)
	}
}
