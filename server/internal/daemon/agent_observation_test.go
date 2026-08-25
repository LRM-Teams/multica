package daemon

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentObservationValidationMatrix(t *testing.T) {
	at := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	runtime := AgentRuntimeObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", RuntimeGeneration: 3}
	stage := AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}
	tool := AgentRuntimeStageObservationData{RuntimeID: "runtime-1", ToolName: "read_file", ToolCallID: "call-1"}
	valid := []AgentObservation{
		{AgentID: "agent-1", Kind: AgentObservationRuntimeReady, Data: runtime, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeWorking, Data: stage, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeThinking, Data: stage, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeTool, Data: tool, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeDiagnostic, Data: AgentRuntimeDiagnosticObservationData{RuntimeID: "runtime-1", Name: "warning", Kind: "provider", Detail: "details"}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{RuntimeID: "runtime-1"}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", NewMessageCount: 2, ReasonCode: "local_pending"}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationDraftSent, Data: AgentDraftSentObservationData{RuntimeID: "runtime-1", Target: "#one"}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationError, Data: AgentErrorObservationData{RuntimeID: "runtime-1", ProcessInstanceID: "process-1", ReasonCode: "provider_failed", Message: "provider unavailable"}, At: at},
	}
	for index := range valid {
		valid[index].AgentInstanceID = "instance-1"
	}
	for _, observation := range valid {
		if err := observation.Validate(); err != nil {
			t.Fatalf("%q Validate(): %v", observation.Kind, err)
		}
	}

	invalid := []AgentObservation{
		{},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeThinking, Data: stage, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeReady, Data: stage, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeReady, Data: AgentRuntimeObservationData{RuntimeID: "runtime-1", RuntimeGeneration: 1}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationRuntimeTool, Data: runtime, At: at},
		{AgentID: "agent-1", Kind: AgentObservationMessageBodyAccepted, Data: AgentMessageAcceptanceObservationData{}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationFreshnessHeld, Data: AgentFreshnessHoldObservationData{RuntimeID: "runtime-1", Target: "channel:one", ReasonCode: ""}, At: at},
		{AgentID: "agent-1", Kind: AgentObservationDraftSent, Data: AgentDraftSentObservationData{RuntimeID: "runtime-1"}, At: at},
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
		reflect.TypeOf(AgentRuntimeObservationData{}),
		reflect.TypeOf(AgentRuntimeStageObservationData{}),
		reflect.TypeOf(AgentRuntimeDiagnosticObservationData{}),
		reflect.TypeOf(AgentMessageAcceptanceObservationData{}),
		reflect.TypeOf(AgentFreshnessHoldObservationData{}),
		reflect.TypeOf(AgentDraftSentObservationData{}),
		reflect.TypeOf(AgentErrorObservationData{}),
	}
	for _, typeOf := range types {
		for index := 0; index < typeOf.NumField(); index++ {
			field := strings.ToLower(typeOf.Field(index).Name)
			for _, forbidden := range []string{
				"activity", "title", "text", "entry", "content", "body", "prompt", "tooloutput", "credential", "stderr",
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
