package handler

import (
	"encoding/json"
	"testing"
)

func TestTaskCompleteRequestAcceptsInternalOutput(t *testing.T) {
	var req TaskCompleteRequest
	wire := []byte(`{"output":"","internal_output":{"decision":"SILENT","confidence":0.1}}`)
	if err := json.Unmarshal(wire, &req); err != nil {
		t.Fatalf("unmarshal completion request: %v", err)
	}
	if got := string(req.InternalOutput); got != `{"decision":"SILENT","confidence":0.1}` {
		t.Fatalf("internal output = %s", got)
	}
}
