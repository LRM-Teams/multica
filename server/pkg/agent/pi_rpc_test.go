package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewPiRPCBackendImplementsResidentMessagePreparation(t *testing.T) {
	backend := NewPiRPCBackend(Config{})
	if _, ok := backend.(ResidentMessagePreparation); !ok {
		t.Fatal("PiRPCBackend must prepare context outside native Message acceptance")
	}
}

func TestBuildResidentTurnCaptureRedactsProviderCredentials(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	records := []piCaptureRecord{
		{Kind: "provider_request", At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"model":"test","api_key":"secret","nested":{"authorization":"Bearer secret","ok":true}}`)},
		{Kind: "turn_end", At: "2026-08-12T00:00:01Z", Message: json.RawMessage(`{"role":"assistant","api_key":"secret","content":[{"type":"text","text":"done"}],"stopReason":"stop"}`)},
	}

	capture, err := buildResidentTurnCapture(binding, 1, 1, records)
	if err != nil {
		t.Fatalf("buildResidentTurnCapture: %v", err)
	}
	if len(capture.ProviderCalls) != 1 {
		t.Fatalf("provider calls: got %d, want 1", len(capture.ProviderCalls))
	}
	request := string(capture.ProviderCalls[0].RawProviderRequest)
	if strings.Contains(request, "secret") || strings.Contains(request, "api_key") || strings.Contains(request, "authorization") {
		t.Fatalf("redacted request still contains credentials: %s", request)
	}
	if response := string(capture.ProviderCalls[0].FinalAssistantMessage); strings.Contains(response, "secret") || strings.Contains(response, "api_key") {
		t.Fatalf("redacted response still contains credentials: %s", response)
	}
	if capture.PayloadHash == "" || capture.CaptureBatchID == "" || capture.TurnID == "" {
		t.Fatalf("capture identity/integrity missing: %+v", capture)
	}
}

func TestResidentTurnCaptureIdentityRetainsBoundaryForGapReporting(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	capture := residentTurnCaptureIdentity(binding, 2)
	if capture.TurnID == "" || capture.CaptureBatchID == "" || capture.CaptureBoundary != binding.CaptureBoundary {
		t.Fatalf("capture identity = %+v, want turn, batch, and current boundary", capture)
	}
	if capture.Complete {
		t.Fatal("identity shell without provider records must not be complete")
	}
}

func TestNewPiCaptureExtensionIsReadOnlyFinalRequestAndFinalMessageOnly(t *testing.T) {
	extPath, logPath, err := newPiCaptureExtension()
	if err != nil {
		t.Fatalf("newPiCaptureExtension: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(extPath)
		_ = os.Remove(logPath)
	})
	source, err := os.ReadFile(extPath)
	if err != nil {
		t.Fatalf("read capture extension: %v", err)
	}
	text := string(source)
	for _, want := range []string{"before_provider_request", "turn_end", "record(\"provider_request\"", "record(\"turn_end\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("capture extension missing final-boundary hook %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{
		"message_update", "text_delta", "sse", "EventSource",
		"event.payload =", "event.message =", "registerTool", "pi.send",
	} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("capture extension must stay read-only / non-SSE; found %q:\n%s", forbidden, text)
		}
	}
}

func TestBuildResidentTurnCapturePreservesTypedAssistantBlocks(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	finalMessage := `{"role":"assistant","blocks":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"hello"},{"type":"toolCall","id":"tool-1","name":"multica","arguments":{"command":"message send"}}],"stopReason":"toolUse"}`
	capture, err := buildResidentTurnCapture(binding, 1, 1, []piCaptureRecord{
		{Kind: "provider_request", At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"synthetic-model","messages":[]}`)},
		{Kind: "turn_end", At: "2026-08-12T00:00:01Z", Message: json.RawMessage(finalMessage)},
	})
	if err != nil {
		t.Fatalf("buildResidentTurnCapture: %v", err)
	}
	if len(capture.ProviderCalls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(capture.ProviderCalls))
	}
	var decoded struct {
		Role   string `json:"role"`
		Blocks []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(capture.ProviderCalls[0].FinalAssistantMessage, &decoded); err != nil {
		t.Fatalf("decode final assistant message: %v", err)
	}
	if decoded.Role != "assistant" || len(decoded.Blocks) != 3 {
		t.Fatalf("typed blocks = %+v, want assistant with thinking/text/toolCall", decoded)
	}
	gotTypes := []string{decoded.Blocks[0].Type, decoded.Blocks[1].Type, decoded.Blocks[2].Type}
	if gotTypes[0] != "thinking" || gotTypes[1] != "text" || gotTypes[2] != "toolCall" {
		t.Fatalf("block types = %v, want [thinking text toolCall]", gotTypes)
	}
	if capture.ProviderCalls[0].StopReason != "toolUse" || !capture.ProviderCalls[0].ResponseComplete {
		t.Fatalf("call metadata = %+v, want complete toolUse", capture.ProviderCalls[0])
	}
}

func TestBuildResidentTurnCaptureNormalizesContentArrayIntoTypedBlocks(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	capture, err := buildResidentTurnCapture(binding, 1, 1, []piCaptureRecord{
		{Kind: "provider_request", At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m"}`)},
		{Kind: "turn_end", At: "2026-08-12T00:00:01Z", Message: json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}`)},
	})
	if err != nil {
		t.Fatalf("buildResidentTurnCapture: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(capture.ProviderCalls[0].FinalAssistantMessage, &decoded); err != nil {
		t.Fatalf("decode final message: %v", err)
	}
	blocks, ok := decoded["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("final assistant message must expose typed blocks, got %s", capture.ProviderCalls[0].FinalAssistantMessage)
	}
	if _, hasContent := decoded["content"]; hasContent {
		t.Fatalf("final assistant message must not retain raw content arrays: %s", capture.ProviderCalls[0].FinalAssistantMessage)
	}
}

func TestBuildResidentTurnCaptureRejectsAmbiguousRequests(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	_, err := buildResidentTurnCapture(binding, 1, 1, []piCaptureRecord{
		{Kind: "provider_request", At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m","attempt":1}`)},
		{Kind: "provider_request", At: "2026-08-12T00:00:00.500Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m","attempt":2}`)},
		{Kind: "turn_end", At: "2026-08-12T00:00:01Z", Message: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}],"stopReason":"stop"}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous Pi capture") {
		t.Fatalf("buildResidentTurnCapture error = %v, want ambiguous Pi capture", err)
	}
}

func TestBuildResidentTurnCaptureRejectsProviderRequestWithoutFinalAssistantMessage(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	_, err := buildResidentTurnCapture(binding, 1, 1, []piCaptureRecord{
		{Kind: PiCaptureEventProviderRequest, At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m"}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "provider request has no final assistant message") {
		t.Fatalf("buildResidentTurnCapture error = %v, want incomplete provider request error", err)
	}
}

func TestBuildResidentTurnCaptureIgnoresSSEChunkRecords(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-1"}
	capture, err := buildResidentTurnCapture(binding, 1, 1, []piCaptureRecord{
		{Kind: "provider_request", At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m"}`)},
		{Kind: "message_update", At: "2026-08-12T00:00:00.200Z", Payload: json.RawMessage(`{"type":"text_delta","delta":"partial sse"}`)},
		{Kind: "sse_chunk", At: "2026-08-12T00:00:00.300Z", Payload: json.RawMessage(`{"data":"stream"}`)},
		{Kind: "turn_end", At: "2026-08-12T00:00:01Z", Message: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"final"}],"stopReason":"stop"}`)},
	})
	if err != nil {
		t.Fatalf("buildResidentTurnCapture: %v", err)
	}
	if len(capture.ProviderCalls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(capture.ProviderCalls))
	}
	encoded := string(capture.ProviderCalls[0].FinalAssistantMessage) + string(capture.ProviderCalls[0].RawProviderRequest)
	for _, forbidden := range []string{"partial sse", "sse_chunk", "text_delta", `"data":"stream"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("capture retained SSE material %q: %s", forbidden, encoded)
		}
	}
}

func TestReadPiCaptureRecordsStartsAtTrustedTurnOffset(t *testing.T) {
	binding := PiRunBinding{PiRunIdentity: PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}, SessionID: "pi-session", CaptureBoundary: "boundary-2"}
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	historicalRequest := piCaptureRecord{Kind: PiCaptureEventProviderRequest, At: "2026-08-12T00:00:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m","call":"historical"}`)}
	historicalFinal := piCaptureRecord{Kind: PiCaptureEventTurnEnd, At: "2026-08-12T00:00:01Z", Message: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"old"}],"stopReason":"stop"}`)}
	writePiCaptureLines(t, path, false, historicalRequest, historicalFinal)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat capture log: %v", err)
	}
	currentRequest := piCaptureRecord{Kind: PiCaptureEventProviderRequest, At: "2026-08-12T00:01:00Z", Payload: json.RawMessage(`{"provider":"synthetic","model":"m","call":"current"}`)}
	currentFinal := piCaptureRecord{Kind: PiCaptureEventTurnEnd, At: "2026-08-12T00:01:01Z", Message: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"new"}],"stopReason":"stop"}`)}
	writePiCaptureLines(t, path, true, currentRequest, currentFinal)

	records, err := readPiCaptureRecords(path, info.Size())
	if err != nil {
		t.Fatalf("readPiCaptureRecords: %v", err)
	}
	capture, err := buildResidentTurnCapture(binding, 2, 2, records)
	if err != nil {
		t.Fatalf("buildResidentTurnCapture: %v", err)
	}
	if len(capture.ProviderCalls) != 1 {
		t.Fatalf("provider calls = %d, want 1 current call", len(capture.ProviderCalls))
	}
	if !strings.Contains(string(capture.ProviderCalls[0].RawProviderRequest), `"call":"current"`) {
		t.Fatalf("expected current request, got %s", capture.ProviderCalls[0].RawProviderRequest)
	}
	if strings.Contains(string(capture.ProviderCalls[0].RawProviderRequest), "historical") {
		t.Fatalf("historical call leaked into capture: %s", capture.ProviderCalls[0].RawProviderRequest)
	}
}

func writePiCaptureLines(t *testing.T, path string, appendLines bool, records ...piCaptureRecord) {
	t.Helper()
	flags := os.O_CREATE | os.O_WRONLY
	if appendLines {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		t.Fatalf("open capture log: %v", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatalf("encode capture record: %v", err)
		}
	}
}

func fakePiRPCProcessScript() string {
	return `#!/bin/sh
	printf x >> "$PI_RPC_TEST_STARTS"
	turn=0
	context_percent="${PI_RPC_TEST_CONTEXT_PERCENT:-44.8}"
	while IFS= read -r line; do
	  case "$line" in
	    *'"id":"multica-message-notice"'*)
	      if [ -n "$PI_RPC_TEST_NOTICE_INPUT" ]; then printf '%s' "$line" > "$PI_RPC_TEST_NOTICE_INPUT"; fi
	      printf '{"id":"multica-message-notice","type":"response","command":"prompt","success":true}\n'
	      if [ -n "$PI_RPC_TEST_NOTICE_MODE" ]; then
	        printf '{"type":"agent_end","messages":[{"role":"assistant","model":"test-pi","usage":{"input":2,"output":3}}]}\n'
	      fi
	      ;;
	    *'"id":"multica-message-input"'*)
	      if [ -n "$PI_RPC_TEST_MESSAGE_INPUT" ]; then printf '%s' "$line" > "$PI_RPC_TEST_MESSAGE_INPUT"; fi
	      printf '{"id":"multica-message-input","type":"response","command":"prompt","success":true}\n'
	      printf '{"type":"agent_start"}\n'
	      if [ -n "$PI_RPC_TEST_MESSAGE_ERROR" ]; then
	        printf '{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"Connection error."}]}\n'
	      else
	        printf '{"type":"tool_execution_start","toolCallId":"tool-1","toolName":"bash","args":{"command":"pwd"}}\n'
	        printf '{"type":"tool_execution_end","toolCallId":"tool-1","result":"/tmp"}\n'
	        printf '{"type":"agent_end","messages":[]}\n'
	      fi
	      ;;
	    *'"type":"prompt"'*)
	      turn=$((turn + 1))
	      printf '{"id":"multica-turn","type":"response","command":"prompt","success":true}\n'
	      printf '{"type":"agent_start"}\n'
	      printf '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Pi reply"}}\n'
	      if [ "$turn" -eq 2 ] && [ -n "$PI_RPC_TEST_SECOND_STARTED" ]; then
	        : > "$PI_RPC_TEST_SECOND_STARTED"
	        if [ -n "$PI_RPC_TEST_NOTICE_MODE" ]; then continue; fi
	        while [ ! -f "$PI_RPC_TEST_RELEASE_SECOND" ]; do sleep 0.01; done
	      fi
	      printf '{"type":"agent_end","messages":[{"role":"assistant","model":"test-pi","usage":{"input":2,"output":3}}]}\n'
	      ;;
	    *'"type":"compact"'*)
	      printf '{"id":"multica-compact","type":"response","command":"compact","success":true,"data":{"summary":"compacted summary","tokensBefore":5000,"tokensAfter":1200}}\n'
	      ;;
	    *'"type":"set_auto_compaction"'*)
	      printf '{"id":"multica-autocompact","type":"response","command":"set_auto_compaction","success":true}\n'
	      ;;
	    *'"type":"get_session_stats"'*)
	      printf '{"id":"multica-stats","type":"response","command":"get_session_stats","success":true,"data":{"tokens":{"input":349000,"output":10000,"cacheRead":2600000,"total":2959000},"cost":3.348,"contextUsage":{"tokens":272000,"contextWindow":607000,"percent":%s}}}\n' "$context_percent"
	      ;;
	    *'"type":"get_state"'*)
	      printf '{"id":"multica-state","type":"response","command":"get_state","success":true,"data":{"autoCompactionEnabled":true}}\n'
	      ;;
	  esac
	done
	`
}

func TestFormatResidentMessageBatchCarriesDeliveryContract(t *testing.T) {
	prompt, err := formatResidentMessageBatch([]ResidentMessage{{
		ID: "message-1", Target: "dm:@alice", Seq: 1, Content: "hello", RuntimeContext: "## Current Task Initiator\n\nAlice",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"multica message send --target <target>",
		"multica message react --message-id <id>",
		"Final assistant output is not delivered",
		"Do not run Issue commands unless",
		`"target":"dm:@alice"`,
		`"runtime_context":"## Current Task Initiator\n\nAlice"`,
		"scoped only to its own Message",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("resident Message prompt missing %q\n--- prompt ---\n%s", want, prompt)
		}
	}
}

func TestPiRPCBackendAcceptsIdleMessageBatchAtNativePromptBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	inputPath := filepath.Join(dir, "message-input.json")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{
		"PI_RPC_TEST_STARTS":        filepath.Join(dir, "starts"),
		"PI_RPC_TEST_MESSAGE_INPUT": inputPath,
	}})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "initialize", ExecOptions{Cwd: dir, ResumeSessionID: filepath.Join(dir, "session.jsonl")})
	if err != nil {
		t.Fatalf("initialize Pi RPC: %v", err)
	}
	waitPiRPCResult(t, session, filepath.Join(dir, "session.jsonl"))
	acceptance, err := b.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-1", Target: "channel:one", ReplyTarget: "#one", Seq: 7, Content: "concrete body", PartsJSON: json.RawMessage(`[{"type":"text","text":"concrete body"}]`),
	}})
	if err != nil {
		t.Fatalf("AcceptMessageBatch: %v", err)
	}
	if acceptance.Done == nil {
		t.Fatal("AcceptMessageBatch returned no native turn completion receipt")
	}
	var activity []Message
	activityDone := make(chan struct{})
	go func() {
		defer close(activityDone)
		for message := range acceptance.Messages {
			activity = append(activity, message)
		}
	}()
	if err := <-acceptance.Done; err != nil {
		t.Fatalf("native Message turn completion: %v", err)
	}
	<-activityDone
	if len(activity) != 3 || activity[0].Type != MessageStatus || activity[1].Type != MessageToolUse || activity[1].Tool != "bash" || activity[1].Input["command"] != "pwd" || activity[2].Type != MessageToolResult {
		t.Fatalf("native Message activity = %+v, want status, tool use, and tool result", activity)
	}
	waitForPiRPCTestPath(t, inputPath)
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"multica-message-input", "message-1", "#one", "concrete body"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("native Pi input %s does not contain %q", raw, want)
		}
	}
	if strings.Contains(string(raw), "channel:one") {
		t.Fatalf("native Pi input exposed internal target: %s", raw)
	}
}

func TestPiRPCBackendStartsForFirstIdleMessageBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	inputPath := filepath.Join(dir, "message-input.json")
	b := newPiRPCBackend(Config{ExecutablePath: path, ResidentOptions: ExecOptions{Cwd: dir}, Env: map[string]string{
		"PI_RPC_TEST_STARTS":        filepath.Join(dir, "starts"),
		"PI_RPC_TEST_MESSAGE_INPUT": inputPath,
	}})
	t.Cleanup(b.Close)

	acceptance, err := b.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-1", Target: "dm:user-1", Seq: 1, Content: "first idle message",
	}})
	if err != nil {
		t.Fatalf("AcceptMessageBatch: %v", err)
	}
	select {
	case err := <-acceptance.Done:
		if err != nil {
			t.Fatalf("first idle Message turn: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first idle Message turn")
	}
	waitForPiRPCTestPath(t, inputPath)
}

func TestPiRPCBackendReportsAssistantStopReasonErrorForIdleMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	b := newPiRPCBackend(Config{ExecutablePath: path, ResidentOptions: ExecOptions{Cwd: dir}, Env: map[string]string{
		"PI_RPC_TEST_STARTS":        filepath.Join(dir, "starts"),
		"PI_RPC_TEST_MESSAGE_ERROR": "1",
	}})
	t.Cleanup(b.Close)

	acceptance, err := b.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-1", Target: "dm:user-1", Seq: 1, Content: "hello",
	}})
	if err != nil {
		t.Fatalf("AcceptMessageBatch: %v", err)
	}
	select {
	case err := <-acceptance.Done:
		if err == nil || err.Error() != "Connection error." {
			t.Fatalf("idle Message completion error = %v, want Connection error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failed idle Message turn")
	}
}

func TestPiRPCBackendQueuesContentFreeNoticeAtBusySafePoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	noticePath := filepath.Join(dir, "message-notice.json")
	secondStarted := filepath.Join(dir, "second-started")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{
		"PI_RPC_TEST_STARTS":         filepath.Join(dir, "starts"),
		"PI_RPC_TEST_NOTICE_INPUT":   noticePath,
		"PI_RPC_TEST_NOTICE_MODE":    "1",
		"PI_RPC_TEST_SECOND_STARTED": secondStarted,
	}})
	t.Cleanup(b.Close)

	first, err := b.Execute(context.Background(), "initialize", ExecOptions{Cwd: dir, ResumeSessionID: filepath.Join(dir, "session.jsonl")})
	if err != nil {
		t.Fatalf("initialize Pi RPC: %v", err)
	}
	waitPiRPCResult(t, first, filepath.Join(dir, "session.jsonl"))
	busy, err := b.Execute(context.Background(), "busy turn", ExecOptions{Cwd: dir, ResumeSessionID: filepath.Join(dir, "session.jsonl")})
	if err != nil {
		t.Fatalf("start busy Pi RPC turn: %v", err)
	}
	waitForPiRPCTestPath(t, secondStarted)

	err = b.AcceptPendingNotice(context.Background(), ResidentPendingNotice{
		TotalPending: 3,
		ChangedTargets: []ResidentPendingTarget{
			{Target: "channel:one", PendingCount: 2},
			{Target: "dm:two", PendingCount: 1},
		},
	})
	if err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	waitPiRPCResult(t, busy, filepath.Join(dir, "session.jsonl"))
	waitForPiRPCTestPath(t, noticePath)
	raw, err := os.ReadFile(noticePath)
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		ID                string `json:"id"`
		StreamingBehavior string `json:"streamingBehavior"`
		Message           string `json:"message"`
	}
	if err := json.Unmarshal(raw, &command); err != nil {
		t.Fatalf("decode native Pi Notice: %v", err)
	}
	if command.ID != "multica-message-notice" || command.StreamingBehavior != "steer" {
		t.Fatalf("native Pi Notice command = %+v", command)
	}
	for _, want := range []string{`"total_pending":3`, `"changed_targets"`, `"pending_count":2`, "channel:one", "dm:two"} {
		if !strings.Contains(command.Message, want) {
			t.Fatalf("native Pi Notice %s does not contain %q", command.Message, want)
		}
	}
	for _, forbidden := range []string{"secret body", `"parts"`, `"attachment"`} {
		if strings.Contains(command.Message, forbidden) {
			t.Fatalf("native Pi Notice leaked forbidden content %q: %s", forbidden, command.Message)
		}
	}
}

func TestPiRPCBackendReusesOneChildForCompatibleTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	starts := filepath.Join(dir, "starts")
	sessionPath := filepath.Join(dir, "session.jsonl")
	secondStarted := filepath.Join(dir, "second-started")
	releaseSecond := filepath.Join(dir, "release-second")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{
		"PI_RPC_TEST_STARTS":         starts,
		"PI_RPC_TEST_SECOND_STARTED": secondStarted,
		"PI_RPC_TEST_RELEASE_SECOND": releaseSecond,
	}})
	t.Cleanup(b.Close)

	published := make(chan struct{})
	releasePublisher := make(chan struct{})
	var firstPublish sync.Once
	b.afterResultPublishForTest = func() {
		firstPublish.Do(func() {
			close(published)
			<-releasePublisher
		})
	}

	first, err := b.Execute(context.Background(), "first", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute(%q): %v", "first", err)
	}
	waitPiRPCResult(t, first, sessionPath)
	<-published
	if b.running.Load() {
		close(releasePublisher)
		t.Fatal("terminal result published before Pi RPC turn admission was released")
	}

	second, err := b.Execute(context.Background(), "second", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute(%q) immediately after terminal result: %v", "second", err)
	}
	waitForPiRPCTestPath(t, secondStarted)

	// Let turn 1's publisher return while turn 2 owns admission. Its deferred
	// fallback must be inert after the explicit pre-publication release;
	// otherwise it clears turn 2's flag and admits an overlapping third turn.
	close(releasePublisher)
	select {
	case _, ok := <-first.Result:
		if ok {
			t.Fatal("unexpected second terminal result from first turn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first turn publisher to exit")
	}
	if !b.running.Load() {
		t.Fatal("first turn deferred release clobbered active second-turn admission")
	}
	if _, err := b.Execute(context.Background(), "third", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath}); err == nil || !strings.Contains(err.Error(), ErrPiRPCTurnBusy.Error()) {
		t.Fatalf("overlapping third Execute error = %v, want busy error", err)
	}

	if err := os.WriteFile(releaseSecond, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitPiRPCResult(t, second, sessionPath)

	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("Pi RPC children started = %d, want 1", got)
	}
}

func waitForPiRPCTestPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitPiRPCResult(t *testing.T, session *Session, wantSessionID string) {
	t.Helper()
	select {
	case got := <-session.Result:
		if got.Status != "completed" || got.Output != "Pi reply" || got.SessionID != wantSessionID {
			t.Fatalf("result = %+v", got)
		}
		if usage := got.Usage["test-pi"]; usage.InputTokens != 2 || usage.OutputTokens != 3 {
			t.Fatalf("usage = %+v", got.Usage)
		}
		if got.RuntimeStats == nil || got.RuntimeStats.ContextPercent == nil || *got.RuntimeStats.ContextPercent != 44.8 || got.RuntimeStats.AutoCompactionEnabled == nil || !*got.RuntimeStats.AutoCompactionEnabled {
			t.Fatalf("runtime stats = %+v", got.RuntimeStats)
		}
		return
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Pi RPC turn")
	}
}

func TestPiRPCBackendRejectsConcurrentTurn(t *testing.T) {
	b := newPiRPCBackend(Config{})
	b.running.Store(true)
	if _, err := b.Execute(context.Background(), "prompt", ExecOptions{}); err == nil || !strings.Contains(err.Error(), ErrPiRPCTurnBusy.Error()) {
		t.Fatalf("concurrent Execute error = %v, want busy error", err)
	}
}

func TestPiRPCBackendCompact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	starts := filepath.Join(dir, "starts")
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{"PI_RPC_TEST_STARTS": starts}})
	t.Cleanup(b.Close)

	// Start with one prompt turn to establish the process.
	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-session.Result

	// Compact between turns.
	result, err := b.Compact(context.Background(), "compact after segment")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.TokensBefore != 5000 || result.TokensAfter != 1200 {
		t.Fatalf("Compact result = %+v", result)
	}
	if result.Summary != "compacted summary" {
		t.Fatalf("Compact summary = %q", result.Summary)
	}

	// A second turn after compaction still works.
	session2, err := b.Execute(context.Background(), "second", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute after compact: %v", err)
	}
	got := <-session2.Result
	if got.Status != "completed" {
		t.Fatalf("second turn status = %q", got.Status)
	}
}

func TestPiRPCBackendCompactsAfterCompletedTurnNotBeforeAccept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{
		"PI_RPC_TEST_STARTS":          filepath.Join(dir, "starts"),
		"PI_RPC_TEST_CONTEXT_PERCENT": "61.0",
	}})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "initialize", ExecOptions{Cwd: dir, ResumeSessionID: filepath.Join(dir, "session.jsonl")})
	if err != nil {
		t.Fatalf("initialize Pi RPC: %v", err)
	}
	var lifecycle []Message
	for msg := range session.Messages {
		if msg.Type == MessageCompactionStarted || msg.Type == MessageCompactionFinished {
			lifecycle = append(lifecycle, msg)
		}
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("initialize result = %+v", result)
	}
	if len(lifecycle) != 2 || lifecycle[0].Type != MessageCompactionStarted || lifecycle[1].Type != MessageCompactionFinished || lifecycle[1].Content != "compacted summary" {
		t.Fatalf("post-turn lifecycle = %+v, want started then finished with summary", lifecycle)
	}
	var prep []Message
	if err := b.PrepareMessageInput(context.Background(), func(message Message) {
		prep = append(prep, message)
	}); err != nil {
		t.Fatalf("PrepareMessageInput: %v", err)
	}
	if len(prep) != 0 {
		t.Fatalf("PrepareMessageInput must not compact, got %+v", prep)
	}
}

func TestPiRPCBackendSetAutoCompaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-session.Result

	if err := b.SetAutoCompaction(context.Background(), false); err != nil {
		t.Fatalf("SetAutoCompaction(false): %v", err)
	}
}

func TestPiRPCBackendRuntimeStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("Execute result = %+v", result)
	}

	stats, err := b.RuntimeStats(context.Background())
	if err != nil {
		t.Fatalf("RuntimeStats: %v", err)
	}
	if stats == nil || stats.ContextPercent == nil || *stats.ContextPercent != 44.8 {
		t.Fatalf("RuntimeStats = %+v", stats)
	}
}

func TestWaitPiRPCResponsePrefersBufferedAckOverTerminalEvent(t *testing.T) {
	for i := 0; i < 100; i++ {
		turn := &piRPCTurn{
			response: make(chan piRPCResponse, 1),
			done:     make(chan piRPCCompletion, 1),
		}
		turn.response <- piRPCResponse{ID: "multica-turn", Success: true}
		turn.done <- piRPCCompletion{}

		response, completion, ok := waitPiRPCResponse(context.Background(), turn, "multica-turn")
		if !ok || completion != nil || response.ID != "multica-turn" {
			t.Fatalf("iteration %d: response=%+v completion=%+v ok=%v, want buffered ACK", i, response, completion, ok)
		}
	}
}

// fakePiRPCHungAckScript starts fine (so ensureProcess returns immediately,
// same as the real pi backend's non-blocking startup) but never responds to
// anything on stdin, including the initial "prompt" command. This simulates
// a real pi process wedged before it acknowledges the prompt it was just
// given — the exact narrow window task #65 is about.
func fakePiRPCHungAckScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  :
done
`
}

// TestPiRPCBackendForceKillDuringInitialAckActuallyKillsNotHang pins task
// #65: waitPiRPCResponse only selected on turn.response and ctx.Done(), not
// turn.done. ForceKill() killing the process during the initial prompt-ack
// wait doesn't cancel ctx — it only makes readEvents' reader loop hit EOF and
// push a completion onto turn.done, which nothing was listening to. The turn
// would hang until ctx's own deadline (which may not exist at all, per
// MULTICA_AGENT_TIMEOUT=0), not until the process was actually killed.
//
// Mirrors the acceptance shape from task #62's cursor/grok handshake tests:
// asserts the process is actually killed and the turn actually terminates,
// not just that some error eventually comes back.
func TestPiRPCBackendForceKillDuringInitialAckActuallyKillsNotHang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCHungAckScript()))
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path})
	t.Cleanup(b.Close)

	execDone := make(chan struct{})
	var execResult Result
	go func() {
		defer close(execDone)
		s, err := b.Execute(context.Background(), "prompt", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
		if err != nil {
			return
		}
		execResult = <-s.Result
	}()

	// Give Execute() time to actually write the prompt command and start
	// blocking on waitPiRPCResponse before we try to interrupt it.
	time.Sleep(200 * time.Millisecond)

	killErr := make(chan error, 1)
	go func() {
		killErr <- b.ForceKill()
	}()

	select {
	case err := <-killErr:
		if err != nil {
			t.Fatalf("ForceKill during the initial ack wait returned an error: %v — it should actually kill the process, not fail", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForceKill() did not return promptly during the initial ack wait")
	}

	select {
	case <-execDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Execute()'s goroutine never returned after ForceKill() during the initial ack wait — waitPiRPCResponse is still stuck on turn.done never being observed")
	}
	if execResult.Status != "failed" {
		t.Fatalf("result status = %q, want failed (initial ack wait was force-killed)", execResult.Status)
	}
	if !strings.Contains(execResult.Error, AgentForceKilledMarker) {
		t.Fatalf("result error = %q, want it to contain %q", execResult.Error, AgentForceKilledMarker)
	}
}

func TestPiRPCBackendFreshRunIdentityAndResidentTurnReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	starts := filepath.Join(dir, "starts")
	backend := newPiRPCBackend(Config{ExecutablePath: path, ResidentOptions: ExecOptions{Cwd: dir}, Env: map[string]string{
		"PI_RPC_TEST_STARTS": starts,
	}})
	t.Cleanup(backend.Close)

	identity := PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	bound, err := backend.BindRunIdentity(identity)
	if err != nil {
		t.Fatalf("bind run identity: %v", err)
	}
	if bound.SessionID == "" || bound.CaptureBoundary == "" {
		t.Fatalf("bound identity = %+v, want fresh session and capture boundary", bound)
	}
	previousBoundary := bound.CaptureBoundary
	for turn := 1; turn <= 2; turn++ {
		acceptance, err := backend.AcceptMessageBatch(context.Background(), []ResidentMessage{{
			ID: fmt.Sprintf("message-%d", turn), Target: "channel:one", Seq: int64(turn), Content: "hello",
		}})
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if err := <-acceptance.Done; err != nil {
			t.Fatalf("turn %d completion: %v", turn, err)
		}
		if err := backend.SettleRunTurn(identity); err != nil {
			t.Fatalf("turn %d settlement: %v", turn, err)
		}
		settled, err := backend.BindRunIdentity(identity)
		if err != nil {
			t.Fatalf("turn %d inspect settled binding: %v", turn, err)
		}
		if settled.SessionID != bound.SessionID {
			t.Fatalf("turn %d settlement changed Pi session: first=%+v settled=%+v", turn, bound, settled)
		}
		if settled.CaptureBoundary == previousBoundary {
			t.Fatalf("turn %d settlement did not advance capture boundary: %+v", turn, settled)
		}
		previousBoundary = settled.CaptureBoundary
	}
	rebound, err := backend.BindRunIdentity(identity)
	if err != nil {
		t.Fatalf("idempotent bind after resident process start: %v", err)
	}
	if rebound.SessionID != bound.SessionID {
		t.Fatalf("idempotent bind changed Pi session: first=%+v rebound=%+v", bound, rebound)
	}
	if _, err := backend.BindRunIdentity(PiRunIdentity{RunID: "run-other", RunAgentID: identity.RunAgentID}); err == nil {
		t.Fatal("resident backend accepted a different run identity")
	} else if !errors.Is(err, ErrPiRPCRunIdentityRequiresFreshSession) {
		t.Fatalf("different run identity error = %v, want fresh-session sentinel", err)
	}
	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("resident Pi children = %d, want one reused child", got)
	}

	other := newPiRPCBackend(Config{ExecutablePath: path, ResidentOptions: ExecOptions{Cwd: dir}, Env: map[string]string{
		"PI_RPC_TEST_STARTS": starts,
	}})
	t.Cleanup(other.Close)
	otherBound, err := other.BindRunIdentity(PiRunIdentity{RunID: "run-2", RunAgentID: "run-agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if otherBound.SessionID == bound.SessionID || otherBound.CaptureBoundary == bound.CaptureBoundary {
		t.Fatalf("dispatches reused Pi identity: first=%+v second=%+v", bound, otherBound)
	}
}

func TestPiRPCBackendPrepareRunStartsBoundProcessWithoutAgentTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	startsPath := filepath.Join(dir, "starts")
	messageInputPath := filepath.Join(dir, "message-input.json")
	b := newPiRPCBackend(Config{ExecutablePath: path, ResidentOptions: ExecOptions{Cwd: dir}, Env: map[string]string{
		"PI_RPC_TEST_STARTS": startsPath, "PI_RPC_TEST_MESSAGE_INPUT": messageInputPath,
	}})
	t.Cleanup(b.Close)

	identity := PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	binding, err := b.PrepareRun(context.Background(), identity)
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if binding.PiRunIdentity != identity || binding.SessionID == "" || binding.CaptureBoundary == "" {
		t.Fatalf("binding = %+v, want complete native run identity", binding)
	}
	waitForPiRPCTestPath(t, startsPath)
	if _, err := os.Stat(messageInputPath); !os.IsNotExist(err) {
		t.Fatalf("preflight started an agent turn: message input stat err=%v", err)
	}
	alive, known := b.RuntimeAlive()
	if !known || !alive {
		t.Fatalf("prepared runtime alive=(%v,%v), want known and alive", alive, known)
	}
}

func TestPiRPCBackendPrepareRunRejectsNativeStartFailureBeforeTurn(t *testing.T) {
	b := newPiRPCBackend(Config{ExecutablePath: filepath.Join(t.TempDir(), "missing-pi")})
	t.Cleanup(b.Close)

	binding, err := b.PrepareRun(context.Background(), PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"})
	if err == nil || !strings.Contains(err.Error(), "pi executable not found") {
		t.Fatalf("PrepareRun binding=%+v err=%v, want deterministic native start failure", binding, err)
	}
	if b.running.Load() {
		t.Fatal("failed preflight left an active agent turn")
	}
}
