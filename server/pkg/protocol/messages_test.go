package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentMessageWireFieldsFollowTheirBoundaryContracts(t *testing.T) {
	delivery := AgentDeliverPayload{
		AgentID:    "agent-1",
		Target:     "channel:1",
		Seq:        7,
		DeliveryID: "delivery-1",
		RunID:      "run-1",
		RunAgentID: "run-agent-1",
		Message: AgentMessageProjection{
			ID:      "message-1",
			Target:  "channel:1",
			Seq:     7,
			Content: "hello",
		},
	}
	raftValues := []any{
		delivery,
		AgentDeliverAckPayload{AgentID: "agent-1", DeliveryID: "delivery-1", Seq: 7, Traceparent: "trace"},
	}
	var raftEncoded strings.Builder
	for _, value := range raftValues {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		raftEncoded.Write(data)
	}
	raftWire := raftEncoded.String()
	for _, field := range []string{`"agentId"`, `"deliveryId"`, `"traceparent"`, `"runId"`, `"runAgentId"`} {
		if !strings.Contains(raftWire, field) {
			t.Fatalf("Raft payloads %s do not contain %s", raftWire, field)
		}
	}
	for _, field := range []string{`"agent_id"`, `"delivery_id"`} {
		if strings.Contains(raftWire, field) {
			t.Fatalf("Raft payloads %s contain project API field %s", raftWire, field)
		}
	}

}

func TestNormalizeChatOutputActionRequiresMessageSend(t *testing.T) {
	action, err := NormalizeChatOutputAction("message_send")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(message_send): %v", err)
	}
	if action != ChatOutputActionMessageSend {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageSend)
	}
	action, err = NormalizeChatOutputAction("send")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(send): %v", err)
	}
	if action != ChatOutputActionMessageSend {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageSend)
	}
	action, err = NormalizeChatOutputAction("react")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(react): %v", err)
	}
	if action != ChatOutputActionMessageReact {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageReact)
	}
	action, err = NormalizeChatOutputAction("message_react")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(message_react): %v", err)
	}
	if action != ChatOutputActionMessageReact {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageReact)
	}
	if _, err := NormalizeChatOutputAction("send_channel_message"); err == nil {
		t.Fatal("NormalizeChatOutputAction accepted legacy send_channel_message")
	}
}
