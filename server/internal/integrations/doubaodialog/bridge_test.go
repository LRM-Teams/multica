package doubaodialog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeSender struct {
	outputs []FunctionCallOutput
	cancels int
}

func (s *fakeSender) SendFunctionCallOutputs(_ context.Context, outputs []FunctionCallOutput) error {
	s.outputs = append(s.outputs, outputs...)
	return nil
}

func (s *fakeSender) CancelResponse(context.Context) error {
	s.cancels++
	return nil
}

func TestMulticaToolBridgeHandlesFunctionCallAndReturnsToolOutput(t *testing.T) {
	executor := &RecordingExecutor{Result: "已创建 issue LRM-999。"}
	sender := &fakeSender{}
	bridge, err := NewMulticaToolBridge(executor, sender)
	if err != nil {
		t.Fatal(err)
	}

	handled, err := bridge.HandleServerEvent(context.Background(), ServerEvent{
		Type: EventFunctionCallArgumentsDone,
		FunctionCalls: []FunctionCall{{
			CallID:    "call-1",
			Name:      MulticaDelegateToolName,
			Arguments: `{"request":"创建一个 issue，修复登录失败"}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected function call to be handled")
	}
	if len(executor.Calls) != 1 || executor.Calls[0] != "创建一个 issue，修复登录失败" {
		t.Fatalf("executor calls = %#v", executor.Calls)
	}
	if len(sender.outputs) != 1 ||
		sender.outputs[0].CallID != "call-1" ||
		sender.outputs[0].Output != "已创建 issue LRM-999。" {
		t.Fatalf("tool outputs = %#v", sender.outputs)
	}
}

func TestMulticaToolBridgeCancelsOnASRStarted(t *testing.T) {
	sender := &fakeSender{}
	bridge, err := NewMulticaToolBridge(&RecordingExecutor{}, sender)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := bridge.HandleServerEvent(context.Background(), ServerEvent{Type: EventASRStarted})
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("ASR started should not count as Multica FC handled")
	}
	if sender.cancels != 1 {
		t.Fatalf("cancels = %d, want 1", sender.cancels)
	}
}

func TestMulticaToolBridgeIgnoresUnknownTools(t *testing.T) {
	executor := &RecordingExecutor{}
	sender := &fakeSender{}
	bridge, err := NewMulticaToolBridge(executor, sender)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := bridge.HandleServerEvent(context.Background(), ServerEvent{
		Type: EventFunctionCallArgumentsDone,
		FunctionCalls: []FunctionCall{{
			CallID:    "call-weather",
			Name:      "lookup_weather",
			Arguments: `{"city":"Beijing"}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if handled || len(executor.Calls) != 0 || len(sender.outputs) != 0 {
		t.Fatalf("unexpected handling: handled=%v calls=%v outputs=%v", handled, executor.Calls, sender.outputs)
	}
}

func TestMulticaDelegateToolSchema(t *testing.T) {
	tool := MulticaDelegateTool()
	if tool.Type != "function" || tool.Name != MulticaDelegateToolName {
		t.Fatalf("unexpected tool: %+v", tool)
	}
	var params map[string]any
	if err := json.Unmarshal(tool.Parameters, &params); err != nil {
		t.Fatal(err)
	}
	required, _ := params["required"].([]any)
	if len(required) != 1 || required[0] != "request" {
		t.Fatalf("required = %#v", required)
	}
	if !strings.Contains(tool.Description, "Multica") {
		t.Fatalf("description missing Multica cue: %q", tool.Description)
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"function":`) {
		t.Fatalf("duplex tools must be flat, got nested function: %s", encoded)
	}
}
