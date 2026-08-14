package researchrun

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func validTaskInquiryTargetsInput() BindTaskInquiryTargetsInput {
	return BindTaskInquiryTargetsInput{
		WorkspaceID:          "11111111-1111-4111-8111-111111111111",
		SessionID:            "22222222-2222-4222-8222-222222222222",
		AttemptID:            "33333333-3333-4333-8333-333333333333",
		AgentID:              "44444444-4444-4444-8444-444444444444",
		IdempotencyKey:       "bind-targets:1",
		ExpectedStateVersion: 7,
		Targets: []TaskInquiryTarget{
			{TaskID: "66666666-6666-4666-8666-666666666666", Kind: InquiryKindBranch, EntityID: "88888888-8888-4888-8888-888888888888"},
			{TaskID: "55555555-5555-4555-8555-555555555555", Kind: InquiryKindQuestion, EntityID: "77777777-7777-4777-8777-777777777777"},
		},
	}
}

func TestTaskInquiryTargetValidateBind(t *testing.T) {
	module := taskInquiryTargetModule{}
	if err := module.ValidateBind(validTaskInquiryTargetsInput()); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*BindTaskInquiryTargetsInput)
	}{
		{name: "empty", mutate: func(in *BindTaskInquiryTargetsInput) { in.Targets = nil }},
		{name: "state version", mutate: func(in *BindTaskInquiryTargetsInput) { in.ExpectedStateVersion = 0 }},
		{name: "unknown kind", mutate: func(in *BindTaskInquiryTargetsInput) { in.Targets[0].Kind = "goal" }},
		{name: "unresolved task", mutate: func(in *BindTaskInquiryTargetsInput) { in.Targets[0].TaskID = "task-1" }},
		{name: "unresolved entity", mutate: func(in *BindTaskInquiryTargetsInput) { in.Targets[0].EntityID = "branch-1" }},
		{name: "duplicate", mutate: func(in *BindTaskInquiryTargetsInput) { in.Targets = append(in.Targets, in.Targets[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validTaskInquiryTargetsInput()
			test.mutate(&in)
			if err := module.ValidateBind(in); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("error=%v, want ErrInvalidContract", err)
			}
		})
	}
}

func TestTaskInquiryTargetsEventPayloadIsCanonical(t *testing.T) {
	in := validTaskInquiryTargetsInput()
	payload := taskInquiryTargetsEventPayload(in)
	if got, want := payload.Targets[0].TaskID, "55555555-5555-4555-8555-555555555555"; got != want {
		t.Fatalf("first task=%s want=%s", got, want)
	}
	reversed := validTaskInquiryTargetsInput()
	reversed.Targets[0], reversed.Targets[1] = reversed.Targets[1], reversed.Targets[0]
	if !reflect.DeepEqual(payload, taskInquiryTargetsEventPayload(reversed)) {
		t.Fatal("semantically identical bindings must produce identical payloads")
	}
	if in.Targets[0].TaskID != "66666666-6666-4666-8666-666666666666" {
		t.Fatal("canonicalisation mutated caller input")
	}
}

// This source-level recovery guard complements the shared integration matrix
// without requiring a local PostgreSQL service.
func TestBindTaskInquiryTargetsTransactionRecovery(t *testing.T) {
	source, err := os.ReadFile("postgres_task_inquiry_target.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := inspectTransactionBoundaryCalls(t, source, "BindTaskInquiryTargets")
	if len(calls.direct) != 0 || calls.runner["beginResearchTx"] != 1 || calls.runner["commitResearchTx"] != 2 {
		t.Fatalf("transaction boundaries=%+v", calls)
	}
}

func TestSelectiveSteeringStateLoaderUsesCurrentCanonicalVersions(t *testing.T) {
	source, err := os.ReadFile("postgres_task_inquiry_target.go")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(source)
	for _, required := range []string{
		"SELECT state_version,goal_version,plan_version FROM research_session",
		"creator.goal_version=$3 AND creator.plan_version=$4",
		"target.goal_version=$3 AND target.plan_version=$4",
		"task.goal_version=$3 AND task.plan_version=$4",
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("selective steering loader missing %q", required)
		}
	}
}
