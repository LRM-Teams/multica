package daemon

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentObservationValidationMatrix(t *testing.T) {
	at := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	attachment := AgentAttachmentObservationData{RuntimeID: "runtime-1", AttachmentGeneration: 2}
	launch := AgentLaunchObservationData{RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"}
	runtime := AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 3}
	tool := runtime
	tool.ToolName = "read_file"
	tool.ToolCallID = "call-1"
	valid := []AgentObservation{
		{AgentID: "agent-1", Kind: AgentObservationAttached, Data: attachment, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationLaunchAccepted, Data: launch, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeReady, Data: runtime, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeWorking, Data: runtime, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeThinking, Data: runtime, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeTool, Data: tool, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1", HandoffID: "handoff-1", MessageCount: 2}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", NewMessageCount: 2, ReasonCode: "local_pending"}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationError, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ReasonCode: "provider_failed"}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationStopped, Data: AgentStopObservationData{RuntimeID: "runtime-1", ReasonCode: "requested"}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationDetached, Data: attachment, At: at},
	}
	for _, observation := range valid {
		if err := observation.Validate(); err != nil {
			t.Fatalf("%q Validate(): %v", observation.Kind, err)
		}
	}

	invalid := []AgentObservation{
		{},
		{AgentID: "agent-1", Kind: AgentObservationAttached, Data: attachment},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationAttached, Data: attachment, At: at},
		{AgentID: "agent-1", Kind: AgentObservationLaunchAccepted, Data: launch, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeReady, Data: launch, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeReady, Data: AgentRuntimeObservationData{RuntimeID: "runtime-1", RuntimeGeneration: 1}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationRuntimeTool, Data: runtime, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1", HandoffID: "handoff-1"}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", ReasonCode: ""}, At: at},
		{AgentID: "agent-1", LaunchID: "launch-1", Kind: AgentObservationDetached, Data: attachment, At: at},
	}
	for index, observation := range invalid {
		if err := observation.Validate(); err == nil {
			t.Fatalf("invalid observation %d accepted: %+v", index, observation)
		}
	}
}

func TestAgentObservationTypesExcludePresentationAndSensitiveFields(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(AgentObservation{}),
		reflect.TypeOf(AgentAttachmentObservationData{}),
		reflect.TypeOf(AgentLaunchObservationData{}),
		reflect.TypeOf(AgentRuntimeObservationData{}),
		reflect.TypeOf(AgentMessageAcceptanceObservationData{}),
		reflect.TypeOf(AgentFreshnessHoldObservationData{}),
		reflect.TypeOf(AgentErrorObservationData{}),
		reflect.TypeOf(AgentStopObservationData{}),
	}
	for _, typeOf := range types {
		for index := 0; index < typeOf.NumField(); index++ {
			field := strings.ToLower(typeOf.Field(index).Name)
			for _, forbidden := range []string{
				"activity", "narrative", "title", "text", "entry", "content", "body", "prompt", "tooloutput", "credential", "stderr",
			} {
				if strings.Contains(field, forbidden) {
					t.Fatalf("%s.%s exposes forbidden presentation or sensitive field", typeOf.Name(), typeOf.Field(index).Name)
				}
			}
		}
	}
}

func TestAgentRuntimeObservationKeepsRuntimeIdentitiesDistinct(t *testing.T) {
	typeOf := reflect.TypeOf(AgentRuntimeObservationData{})
	for _, field := range []string{"RuntimeID", "ProcessInstanceID", "ProviderSessionID", "TurnID", "RuntimeGeneration"} {
		if _, found := typeOf.FieldByName(field); !found {
			t.Fatalf("AgentRuntimeObservationData missing distinct %s", field)
		}
	}
}
