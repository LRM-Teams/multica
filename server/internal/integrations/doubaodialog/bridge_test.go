package doubaodialog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestMulticaToolBridgeReturnsSpeakableErrorForUnknownTools(t *testing.T) {
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
	if !handled || len(executor.Calls) != 0 {
		t.Fatalf("unexpected handling: handled=%v calls=%v", handled, executor.Calls)
	}
	if len(sender.outputs) != 1 ||
		sender.outputs[0].CallID != "call-weather" ||
		!strings.Contains(sender.outputs[0].Output, "lookup_weather") {
		t.Fatalf("expected speakable unknown-tool output, got %#v", sender.outputs)
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

func TestWebSearchAndFetchToolsHandled(t *testing.T) {
	web := &RecordingWebToolkit{
		SearchResult: "北京今天多云，气温 28 度。",
		FetchResult:  "页面说今天多云。",
	}
	sender := &fakeSender{}
	bridge, err := NewMulticaToolBridgeWithWeb(&RecordingExecutor{}, web, sender)
	if err != nil {
		t.Fatal(err)
	}

	handled, err := bridge.HandleServerEvent(context.Background(), ServerEvent{
		Type: EventFunctionCallArgumentsDone,
		FunctionCalls: []FunctionCall{{
			CallID:    "call-search",
			Name:      WebSearchToolName,
			Arguments: `{"query":"北京 今天天气"}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || len(web.Searches) != 1 || web.Searches[0] != "北京 今天天气" {
		t.Fatalf("search handling failed: handled=%v searches=%v", handled, web.Searches)
	}
	if len(sender.outputs) != 1 || sender.outputs[0].Output != "北京今天多云，气温 28 度。" {
		t.Fatalf("search outputs = %#v", sender.outputs)
	}

	sender.outputs = nil
	handled, err = bridge.HandleServerEvent(context.Background(), ServerEvent{
		Type: EventFunctionCallArgumentsDone,
		FunctionCalls: []FunctionCall{{
			CallID:    "call-fetch",
			Name:      WebFetchToolName,
			Arguments: `{"url":"https://example.com/weather"}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || len(web.Fetches) != 1 {
		t.Fatalf("fetch handling failed: handled=%v fetches=%v", handled, web.Fetches)
	}
	if len(sender.outputs) != 1 || sender.outputs[0].Output != "页面说今天多云。" {
		t.Fatalf("fetch outputs = %#v", sender.outputs)
	}
}

func TestDefaultDialogToolsIncludesWebLookup(t *testing.T) {
	tools := DefaultDialogTools()
	if len(tools) != 4 {
		t.Fatalf("tools=%d want 4", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"function":`) {
			t.Fatalf("duplex tools must be flat: %s", encoded)
		}
	}
	for _, want := range []string{MulticaDelegateToolName, MulticaChannelContextToolName, WebSearchToolName, WebFetchToolName} {
		if !names[want] {
			t.Fatalf("missing tool %s in %#v", want, names)
		}
	}
	if !strings.Contains(DefaultDialogInstructions(), WebSearchToolName) {
		t.Fatal("instructions should mention web_search")
	}
	instr := DefaultDialogInstructions()
	if !strings.Contains(instr, "先正常闲聊") {
		t.Fatal("instructions should prefer chat before tools")
	}
	if !strings.Contains(instr, "不要一开口就调用") {
		t.Fatal("instructions should forbid immediate tool use on connect")
	}
	if !strings.Contains(WebSearchTool().Description, "不要在通话刚开始时主动搜索") {
		t.Fatal("web_search tool should discourage proactive search")
	}
	if !strings.Contains(instr, "继续保持通话可听可说") {
		t.Fatal("instructions should keep duplex open during web tools")
	}
}

func TestMulticaToolBridgeHandlesChannelContext(t *testing.T) {
	executor := &RecordingExecutor{}
	sender := &fakeSender{}
	bridge, err := NewMulticaToolBridge(executor, sender)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := bridge.HandleServerEvent(context.Background(), ServerEvent{
		Type: EventFunctionCallArgumentsDone,
		FunctionCalls: []FunctionCall{{
			CallID:    "call-channel",
			Name:      MulticaChannelContextToolName,
			Arguments: `{"action":"search","channel_id":"channel-1","query":"发布"}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || len(sender.outputs) != 1 || sender.outputs[0].Output != "search channel-1 发布" {
		t.Fatalf("unexpected channel context result: handled=%v outputs=%#v", handled, sender.outputs)
	}
}

func TestComposeDialogInstructionsIncludesAgentContext(t *testing.T) {
	got := ComposeDialogInstructions(DefaultDialogInstructions(), []string{
		"Agent identity\nYou are 贝克汉姆.",
		"Recent DM context\n用户：修登录",
	})
	if !strings.Contains(got, DefaultDialogInstructions()) {
		t.Fatal("composed instructions missing base dialog rules")
	}
	if !strings.Contains(got, "被叫 Agent") {
		t.Fatal("composed instructions missing agent-context preamble")
	}
	if !strings.Contains(got, "You are 贝克汉姆.") {
		t.Fatal("composed instructions missing agent identity")
	}
	if !strings.Contains(got, "用户：修登录") {
		t.Fatal("composed instructions missing recent DM")
	}
	if !strings.Contains(got, "不要以空白会话开场") {
		t.Fatal("composed instructions missing anti-amnesia cue")
	}
}

func TestRejectPrivateHost(t *testing.T) {
	if err := rejectPrivateHost("127.0.0.1"); err == nil {
		t.Fatal("expected loopback blocked")
	}
	if err := rejectPrivateHost("10.0.0.1"); err == nil {
		t.Fatal("expected private blocked")
	}
	if err := rejectPrivateHost("example.com"); err != nil {
		t.Fatalf("public host should pass: %v", err)
	}
}

func TestHTTPWebToolkitSearchHTMLFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	mux.HandleFunc("/html/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
			<div class="result results_links"><div>
				<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fweather">北京今日天气</a>
				<a class="result__snippet">晴，气温 28 度</a>
			</div></div>
		`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tools := &HTTPWebToolkit{
		Client:    server.Client(),
		SearchURL: server.URL + "/",
		HTMLURL:   server.URL + "/html/",
	}
	out, err := tools.Search(context.Background(), "北京天气")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "北京今日天气") || !strings.Contains(out, "example.com/weather") {
		t.Fatalf("unexpected html fallback output: %q", out)
	}
}
