package daemon

import (
	"reflect"
	"testing"
)

func TestAgentAttachmentEventValidation(t *testing.T) {
	valid := AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-1", RuntimeID: "runtime-1",
		AttachmentGeneration: 2, LifecycleSeq: 3,
	}
	for _, test := range []struct {
		name  string
		event AgentAttachmentEvent
		valid bool
	}{
		{name: "attach", event: valid, valid: true},
		{name: "detach", event: func() AgentAttachmentEvent { event := valid; event.Kind = AgentAttachmentEventDetach; return event }(), valid: true},
		{name: "unknown kind", event: func() AgentAttachmentEvent { event := valid; event.Kind = "start"; return event }()},
		{name: "missing Agent", event: func() AgentAttachmentEvent { event := valid; event.AgentID = ""; return event }()},
		{name: "missing Runtime", event: func() AgentAttachmentEvent { event := valid; event.RuntimeID = ""; return event }()},
		{name: "missing Attachment generation", event: func() AgentAttachmentEvent { event := valid; event.AttachmentGeneration = 0; return event }()},
		{name: "missing lifecycle sequence", event: func() AgentAttachmentEvent { event := valid; event.LifecycleSeq = 0; return event }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.event.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate(): %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("Validate() accepted invalid event")
			}
		})
	}
}

func TestAgentAttachmentRuntimeSetValidation(t *testing.T) {
	valid := AgentAttachmentRuntimeSet{WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1", "runtime-2"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	for _, scope := range []AgentAttachmentRuntimeSet{
		{},
		{WorkspaceID: "workspace-1", RuntimeIDs: []string{""}},
		{WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1", "runtime-1"}},
	} {
		if err := scope.Validate(); err == nil {
			t.Fatalf("Validate() accepted invalid Runtime set: %+v", scope)
		}
	}
}

func TestAgentAttachmentValidation(t *testing.T) {
	attachment := AgentAttachment{
		WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1,
	}
	if err := attachment.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	attachment.AttachmentGeneration = 0
	if err := attachment.Validate(); err == nil {
		t.Fatal("Validate() accepted a missing Attachment generation")
	}
}

func TestAgentAttachmentChangeKindsAreSemantic(t *testing.T) {
	want := []AgentAttachmentChangeKind{
		AgentAttachmentUnchanged,
		AgentAttachmentAttached,
		AgentAttachmentMoved,
		AgentAttachmentDetached,
	}
	seen := make(map[AgentAttachmentChangeKind]struct{}, len(want))
	for _, kind := range want {
		if err := kind.Validate(); err != nil {
			t.Fatalf("%q.Validate(): %v", kind, err)
		}
		seen[kind] = struct{}{}
	}
	if len(seen) != len(want) {
		t.Fatalf("change kinds are not distinct: %v", want)
	}
	if err := AgentAttachmentChangeKind("newer_generation").Validate(); err == nil {
		t.Fatal("generation-comparison result leaked into semantic change kinds")
	}
}

func TestAgentAttachmentTypesKeepIdentityDomainsSeparate(t *testing.T) {
	for _, value := range []any{AgentAttachment{}, AgentAttachmentEvent{}, AgentAttachmentChange{}, AgentAttachmentRecoveryCursor{}} {
		typeOf := reflect.TypeOf(value)
		for _, forbidden := range []string{
			"LaunchID", "ProcessID", "ProcessInstanceID", "SessionID", "ActivityID", "MessageID", "DeliveryID",
		} {
			if _, found := typeOf.FieldByName(forbidden); found {
				t.Fatalf("%s reuses forbidden identity %s", typeOf.Name(), forbidden)
			}
		}
	}
	if _, found := reflect.TypeOf(AgentAttachmentEvent{}).FieldByName("WorkspaceID"); found {
		t.Fatal("AgentAttachmentEvent accepts caller-controlled Workspace scope")
	}
}
