package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── acpClient: ACP JSON-RPC 2.0 transport ──

type acpPromptResult struct {
	stopReason string
	usage      TokenUsage
}

type acpClient struct {
	cfg          Config
	stdin        interface{ Write([]byte) (int, error) }
	writeMu      sync.Mutex // serialises stdin.Write calls across goroutines
	mu           sync.Mutex
	nextID       int
	pending      map[int]*pendingRPC
	sessionID    string
	onMessage    func(Message)
	onPromptDone func(acpPromptResult)
	// acceptNotification can drop ACP session updates before dispatching to
	// handlers that mutate client state such as usage or pending tool calls.
	acceptNotification func(updateType string) bool

	// pendingTools buffers the args for tool calls whose input streams in
	// across multiple ACP tool_call_update messages (kimi does this —
	// tokens from the LLM arrive one at a time, and each update carries
	// the cumulative args JSON so far). We defer emitting MessageToolUse
	// until we either see status=completed/failed or have a full arg set,
	// so the UI never sees a half-written command like `{"comma`.
	toolMu       sync.Mutex
	pendingTools map[string]*pendingToolCall

	toolFailureMu sync.Mutex
	toolFailure   *acpToolCallFailure

	usageMu sync.Mutex
	usage   TokenUsage

	runtimeStatsMu sync.Mutex
	runtimeStats   *RuntimeTokenStats
	firstUpdateMu  sync.Mutex
	firstUpdateAt  time.Time
}

// pendingToolCall buffers state for a tool call while its arguments
// are streaming in. One entry per ACP toolCallId.
type pendingToolCall struct {
	toolName string         // already mapped via acpToolNameFromTitle
	input    map[string]any // from rawInput when the agent sends it up front
	argsText string         // accumulated `content[].text` args (kimi, cumulative)
	emitted  bool           // whether we've already sent MessageToolUse
}

// acpToolCallFailure preserves the provider's canonical failed-tool frame.
// Some ACP runtimes still answer session/prompt successfully after emitting
// one of these frames, so the prompt response alone is not a truthful turn
// outcome.
type acpToolCallFailure struct {
	ToolCallID string
	ToolName   string
	Message    string
}

func (e *acpToolCallFailure) Error() string {
	return e.Message
}

// writeLine serialises concurrent JSON-RPC writes so request() (main
// goroutine) and handleAgentRequest() (reader goroutine) don't
// interleave frames. The pipe itself is atomic for small writes, but
// we also want deterministic ordering under contention.
func (c *acpClient) writeLine(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.stdin.Write(data)
	return err
}

func (c *acpClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, result, err := c.beginRequest(method, params)
	if err != nil {
		return nil, err
	}
	return c.awaitRequest(ctx, id, result)
}

func (c *acpClient) awaitRequest(ctx context.Context, id int, result <-chan rpcResult) (json.RawMessage, error) {
	select {
	case res := <-result:
		return res.result, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// beginRequest writes one JSON-RPC request and returns its eventual response
// channel. Resident adapters use the successful native write as the bounded
// handoff receipt when the provider protocol only answers at end-of-turn.
func (c *acpClient) beginRequest(method string, params any) (int, <-chan rpcResult, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	pr := &pendingRPC{ch: make(chan rpcResult, 1), method: method}
	c.pending[id] = pr
	c.mu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return 0, nil, err
	}
	data = append(data, '\n')
	if err := c.writeLine(data); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return 0, nil, fmt.Errorf("write %s: %w", method, err)
	}
	return id, pr.ch, nil
}

func (c *acpClient) closeAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, pr := range c.pending {
		pr.ch <- rpcResult{err: err}
		delete(c.pending, id)
	}
}

func (c *acpClient) handleLine(line string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}

	// Agent → client request: has id + method (no result / error yet).
	// Kimi uses this for session/request_permission; if we don't answer,
	// the agent blocks for 300s and the task hangs. Some ACP servers don't send
	// these when launched with HERMES_YOLO_MODE=1, but we still handle
	// the case generically for any future ACP backend we bolt on.
	if _, hasID := raw["id"]; hasID {
		if _, hasResult := raw["result"]; hasResult {
			c.handleResponse(raw)
			return
		}
		if _, hasError := raw["error"]; hasError {
			c.handleResponse(raw)
			return
		}
		if _, hasMethod := raw["method"]; hasMethod {
			c.handleAgentRequest(raw)
			return
		}
	}

	// Notification (no id, has method) — session updates from the ACP server.
	if _, hasMethod := raw["method"]; hasMethod {
		c.handleNotification(raw)
	}
}

// handleAgentRequest replies to JSON-RPC requests the agent sends
// us (agent → client direction). The only one we care about today is
// `session/request_permission`: the daemon is headless and cannot
// actually prompt a user, so we auto-approve every action. ACP
// providers choose their own option IDs, so the reply must echo an
// allow option from the request rather than inventing an ID.
func (c *acpClient) handleAgentRequest(raw map[string]json.RawMessage) {
	var method string
	_ = json.Unmarshal(raw["method"], &method)

	rawID, ok := raw["id"]
	if !ok {
		return
	}

	var resp map[string]any
	switch method {
	case "session/request_permission":
		optionID, err := selectACPPermissionOption(raw["params"])
		if err != nil {
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(rawID),
				"error": map[string]any{
					"code":    -32602,
					"message": err.Error(),
				},
			}
			c.cfg.Logger.Warn("rejecting invalid agent permission request", "method", method, "error", err)
		} else {
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(rawID),
				"result": map[string]any{
					"outcome": map[string]any{
						"outcome":  "selected",
						"optionId": optionID,
					},
				},
			}
			c.cfg.Logger.Debug("auto-approved agent permission request", "method", method, "option_id", optionID)
		}
	default:
		// Unknown agent→client method — reply with standard "method
		// not found" so the agent doesn't block waiting for us. Better
		// than silence: the agent can decide how to proceed.
		resp = map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(rawID),
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found: " + method,
			},
		}
		c.cfg.Logger.Debug("unhandled agent→client request", "method", method)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		c.cfg.Logger.Warn("marshal agent-request response", "method", method, "error", err)
		return
	}
	data = append(data, '\n')
	if err := c.writeLine(data); err != nil {
		c.cfg.Logger.Warn("write agent-request response", "method", method, "error", err)
	}
}

type acpPermissionRequestParams struct {
	Options []struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// selectACPPermissionOption returns an actual option ID offered by the
// provider. Prefer a session-scoped approval to avoid repeated prompts,
// then fall back to one-shot approval. If the provider offers no typed
// allow option, fail closed instead of guessing a provider-private ID or
// accidentally selecting a rejection.
func selectACPPermissionOption(raw json.RawMessage) (string, error) {
	var params acpPermissionRequestParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", fmt.Errorf("invalid session/request_permission params")
	}
	for _, kind := range []string{"allow_always", "allow_once"} {
		for _, option := range params.Options {
			if option.Kind == kind && option.OptionID != "" {
				return option.OptionID, nil
			}
		}
	}
	return "", fmt.Errorf("session/request_permission has no supported allow option")
}

// acpRPCError is a JSON-RPC error frame returned by the agent process.
// It renders exactly like the flat string handleResponse used to build
// with fmt.Errorf, so logs and surfaced task errors are unchanged, but
// keeps the code and message structured so callers can branch on the
// error class (see isACPSessionNotFound) instead of parsing text.
type acpRPCError struct {
	Method  string
	Code    int
	Message string
	Data    string
}

func (e *acpRPCError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("%s: %s (code=%d, data=%s)", e.Method, e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("%s: %s (code=%d)", e.Method, e.Message, e.Code)
}

// isACPSessionNotFound reports whether err is the agent rejecting a
// session id it no longer knows. Runtimes signal this with codes and
// wording that vary — some servers say "Session not found" under -32603
// (Internal error), Kiro puts "No session found with id ..." in
// `data` under -32603, kimi-cli raises invalid_params (-32602)
// with {"session_id": "Session not found"} in `data` for every
// unknown-session path (src/kimi_cli/acp/server.py), and Grok
// session/load returns -32603 Path not found / FS_NOT_FOUND when the
// id belongs to another cwd. Neither the code nor the text alone is
// discriminating, so both are matched.
func isACPSessionNotFound(err error) bool {
	var rpcErr *acpRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if rpcErr.Code != -32603 && rpcErr.Code != -32602 {
		return false
	}
	text := strings.ToLower(rpcErr.Message + " " + rpcErr.Data)
	return strings.Contains(text, "session not found") ||
		strings.Contains(text, "no session found") ||
		strings.Contains(text, "path not found") ||
		strings.Contains(text, "fs_not_found") ||
		strings.Contains(text, "no such file or directory")
}

func (c *acpClient) handleResponse(raw map[string]json.RawMessage) {
	var id int
	if err := json.Unmarshal(raw["id"], &id); err != nil {
		// Try float (JSON numbers are floats by default).
		var fid float64
		if err := json.Unmarshal(raw["id"], &fid); err != nil {
			return
		}
		id = int(fid)
	}

	c.mu.Lock()
	pr, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if !ok {
		return
	}

	if errData, hasErr := raw["error"]; hasErr {
		var rpcErr struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(errData, &rpcErr)
		// JSON-RPC `data` carries the provider-specific reason (e.g. Kiro
		// returns "No session found with id" for code=-32603). Surface it
		// in the wrapped error so daemon logs / UI can show *why* the
		// agent failed instead of a bare "Internal error". `data` may be
		// any JSON value: render strings unquoted, everything else as raw
		// JSON.
		detail := ""
		if len(rpcErr.Data) > 0 && string(rpcErr.Data) != "null" {
			var s string
			if err := json.Unmarshal(rpcErr.Data, &s); err == nil {
				detail = s
			} else {
				detail = string(rpcErr.Data)
			}
		}
		pr.ch <- rpcResult{err: &acpRPCError{Method: pr.method, Code: rpcErr.Code, Message: rpcErr.Message, Data: detail}}
	} else {
		// If this is a prompt response, extract usage and stop reason.
		if pr.method == "session/prompt" {
			c.extractPromptResult(raw["result"])
		}
		pr.ch <- rpcResult{result: raw["result"]}
	}
}

func (c *acpClient) extractPromptResult(data json.RawMessage) {
	var resp struct {
		StopReason string `json:"stopReason"`
		Usage      *struct {
			InputTokens      int64 `json:"inputTokens"`
			OutputTokens     int64 `json:"outputTokens"`
			TotalTokens      int64 `json:"totalTokens"`
			ThoughtTokens    int64 `json:"thoughtTokens"`
			CachedReadTokens int64 `json:"cachedReadTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return
	}

	pr := acpPromptResult{
		stopReason: resp.StopReason,
	}
	if resp.Usage != nil {
		pr.usage = TokenUsage{
			InputTokens:     resp.Usage.InputTokens,
			OutputTokens:    resp.Usage.OutputTokens,
			CacheReadTokens: resp.Usage.CachedReadTokens,
		}
	}

	if c.onPromptDone != nil {
		c.onPromptDone(pr)
	}
}

func (c *acpClient) handleNotification(raw map[string]json.RawMessage) {
	var method string
	_ = json.Unmarshal(raw["method"], &method)

	if method != "session/update" && method != "session/notification" {
		return
	}

	var params struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if p, ok := raw["params"]; ok {
		_ = json.Unmarshal(p, &params)
	}
	if len(params.Update) == 0 {
		return
	}

	updateType, updateData := normalizeACPUpdate(params.Update)
	firstUpdateAt := c.markFirstUpdate()
	if c.acceptNotification != nil && !c.acceptNotification(updateType) {
		return
	}

	switch updateType {
	case "agent_message_chunk":
		c.handleAgentMessage(updateData, firstUpdateAt)
	case "agent_thought_chunk":
		c.handleAgentThought(updateData, firstUpdateAt)
	case "tool_call":
		c.handleToolCallStart(updateData, firstUpdateAt)
	case "tool_call_update":
		c.handleToolCallUpdate(updateData, firstUpdateAt)
	case "usage_update":
		c.handleUsageUpdate(updateData)
	case "turn_end":
		c.extractPromptResult(updateData)
	}
}

func (c *acpClient) resetFirstUpdate() {
	c.firstUpdateMu.Lock()
	c.firstUpdateAt = time.Time{}
	c.firstUpdateMu.Unlock()
}

func (c *acpClient) markFirstUpdate() time.Time {
	now := time.Now().UTC()
	c.firstUpdateMu.Lock()
	defer c.firstUpdateMu.Unlock()
	if c.firstUpdateAt.IsZero() {
		c.firstUpdateAt = now
		return now
	}
	return c.firstUpdateAt
}

func normalizeACPUpdate(data json.RawMessage) (string, json.RawMessage) {
	var updateType struct {
		SessionUpdate string `json:"sessionUpdate"`
		Type          string `json:"type"`
	}
	_ = json.Unmarshal(data, &updateType)
	if updateType.SessionUpdate != "" {
		return normalizeACPUpdateType(updateType.SessionUpdate), data
	}
	if updateType.Type != "" {
		return normalizeACPUpdateType(updateType.Type), data
	}

	// Some ACP implementations serialize enum variants as an externally
	// tagged object: {"agentMessageChunk": {"content": ...}}.
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper) == 1 {
		for k, v := range wrapper {
			return normalizeACPUpdateType(k), v
		}
	}

	return "", data
}

func normalizeACPUpdateType(t string) string {
	key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(t), "_", ""), "-", ""))
	switch key {
	case "agentmessagechunk":
		return "agent_message_chunk"
	case "agentthoughtchunk":
		return "agent_thought_chunk"
	case "toolcall":
		return "tool_call"
	case "toolcallupdate":
		return "tool_call_update"
	case "usageupdate":
		return "usage_update"
	case "turnend", "endturn":
		return "turn_end"
	default:
		return ""
	}
}

func (c *acpClient) handleAgentMessage(data json.RawMessage, providerEvent ...time.Time) {
	providerEventAt := firstACPEventTime(providerEvent)
	var msg struct {
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Content.Text == "" {
		return
	}
	if c.onMessage != nil {
		c.onMessage(Message{Type: MessageText, Content: msg.Content.Text, ProviderEventAt: providerEventAt})
	}
}

func (c *acpClient) handleAgentThought(data json.RawMessage, providerEvent ...time.Time) {
	providerEventAt := firstACPEventTime(providerEvent)
	var msg struct {
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Content.Text == "" {
		return
	}
	if c.onMessage != nil {
		c.onMessage(Message{Type: MessageThinking, Content: msg.Content.Text, ProviderEventAt: providerEventAt})
	}
}

func (c *acpClient) handleToolCallStart(data json.RawMessage, providerEvent ...time.Time) {
	providerEventAt := firstACPEventTime(providerEvent)
	var msg struct {
		ToolCallID string            `json:"toolCallId"`
		Name       string            `json:"name"`
		Title      string            `json:"title"`
		Kind       string            `json:"kind"`
		RawInput   map[string]any    `json:"rawInput"`
		Input      map[string]any    `json:"input"`
		Parameters map[string]any    `json:"parameters"`
		Content    []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	toolName := acpToolNameFromTitle(msg.Title, msg.Kind)
	if toolName == "" {
		toolName = msg.Name
	}
	rawInput := msg.RawInput
	if rawInput == nil {
		rawInput = msg.Input
	}
	if rawInput == nil {
		rawInput = msg.Parameters
	}

	// ACP's tool_call is the provider's authoritative start boundary. Emit
	// it immediately, even when arguments are streamed separately. The
	// initial input may be empty; later frames enrich the result, but never
	// create a second start event.
	if rawInput == nil {
		rawInput = map[string]any{}
	}
	c.trackTool(msg.ToolCallID, &pendingToolCall{
		toolName: toolName,
		input:    rawInput,
		argsText: extractACPToolCallText(msg.Content),
		emitted:  true,
	})
	if c.onMessage != nil {
		c.onMessage(Message{
			Type:   MessageToolUse,
			Tool:   toolName,
			CallID: msg.ToolCallID,
			Input:  rawInput,
			ProviderEventAt: providerEventAt,
		})
	}
}

func (c *acpClient) handleToolCallUpdate(data json.RawMessage, providerEvent ...time.Time) {
	providerEventAt := firstACPEventTime(providerEvent)
	var msg struct {
		ToolCallID string            `json:"toolCallId"`
		Status     string            `json:"status"`
		Name       string            `json:"name"`
		Title      string            `json:"title"`
		Kind       string            `json:"kind"`
		RawInput   map[string]any    `json:"rawInput"`
		Input      map[string]any    `json:"input"`
		Parameters map[string]any    `json:"parameters"`
		RawOutput  string            `json:"rawOutput"`
		Output     string            `json:"output"`
		Content    []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	rawInput := msg.RawInput
	if rawInput == nil {
		rawInput = msg.Input
	}
	if rawInput == nil {
		rawInput = msg.Parameters
	}
	title := msg.Title
	if title == "" {
		title = msg.Name
	}

	// Mid-stream: only buffer updates. Kimi emits many of these per
	// tool call, each carrying the cumulative args JSON so far.
	if msg.Status != "completed" && msg.Status != "failed" {
		pending := c.getPendingTool(msg.ToolCallID)
		if pending == nil {
			toolName := acpToolNameFromTitle(title, msg.Kind)
			pending = &pendingToolCall{
				toolName: toolName,
				input:    rawInput,
				argsText: extractACPToolCallText(msg.Content),
				emitted:  rawInput != nil,
			}
			c.trackTool(msg.ToolCallID, pending)
			if pending.emitted && c.onMessage != nil {
				c.onMessage(Message{
					Type:   MessageToolUse,
					Tool:   toolName,
					CallID: msg.ToolCallID,
					Input:  rawInput,
					ProviderEventAt: providerEventAt,
				})
			}
		} else if text := extractACPToolCallText(msg.Content); text != "" {
			// Kimi streams the full cumulative args on every frame; overwrite
			// rather than concatenate. This also enriches a start emitted with
			// an empty input without duplicating the start event.
			pending.argsText = text
		}
		return
	}

	// Completion: emit any deferred MessageToolUse first, then the result.
	pending := c.takePendingTool(msg.ToolCallID)
	c.emitDeferredToolUse(pending, msg.ToolCallID, title, msg.Kind, rawInput)

	output := msg.RawOutput
	if output == "" {
		output = msg.Output
	}
	if output == "" {
		output = extractACPToolCallText(msg.Content)
	}

	// Resolve tool name + Input for tool_result. #103: Activity backfill
	// (#1853) needs non-empty Input on MessageToolResult. Cursor ACP uses
	// acpClient — previously completed frames dropped Input entirely
	// (stream-json #1931 never ran on this path).
	toolName := acpToolNameFromTitle(title, msg.Kind)
	if pending != nil && pending.toolName != "" {
		toolName = pending.toolName
	}
	input := rawInput
	if len(input) == 0 && pending != nil {
		if len(pending.input) > 0 {
			input = pending.input
		} else if pending.argsText != "" {
			input = parseToolArgsJSON(pending.argsText)
		}
	}
	enrichTag := "none"
	if pathFromMap(input) != "" {
		if len(rawInput) > 0 && pathFromMap(rawInput) != "" {
			enrichTag = "args_path" // completed frame rawInput
		} else {
			enrichTag = "started_fallback"
		}
	}
	if strings.TrimSpace(os.Getenv("MULTICA_DEBUG_TOOL_RESULT_INPUT")) == "1" && c.cfg.Logger != nil {
		c.cfg.Logger.Info(
			"tool_result input enrich path",
			"tool", toolName,
			"call_id", msg.ToolCallID,
			"write_input_enrich", enrichTag,
			"decode_enrich", "acp_client",
			"input_empty", len(input) == 0,
			"input_has_path", pathFromMap(input) != "",
			"input_key_count", len(input),
		)
	}

	if msg.Status == "failed" {
		failureText := strings.TrimSpace(output)
		if failureText == "" {
			failureText = "tool call failed"
		}
		c.recordToolCallFailure(&acpToolCallFailure{
			ToolCallID: msg.ToolCallID,
			ToolName:   toolName,
			Message:    failureText,
		})
	}
	if c.onMessage != nil {
		c.onMessage(Message{
			Type:   MessageToolResult,
			Tool:   toolName,
			CallID: msg.ToolCallID,
			Input:  input,
			Output: output,
			ProviderEventAt: providerEventAt,
		})
	}
}

func firstACPEventTime(values []time.Time) time.Time {
	if len(values) > 0 {
		return values[0]
	}
	return time.Time{}
}

func (c *acpClient) resetToolCallFailure() {
	c.toolFailureMu.Lock()
	c.toolFailure = nil
	c.toolFailureMu.Unlock()
}

func (c *acpClient) recordToolCallFailure(failure *acpToolCallFailure) {
	if failure == nil {
		return
	}
	c.toolFailureMu.Lock()
	defer c.toolFailureMu.Unlock()
	if c.toolFailure == nil {
		c.toolFailure = failure
	}
}

func (c *acpClient) takeToolCallFailure() *acpToolCallFailure {
	c.toolFailureMu.Lock()
	defer c.toolFailureMu.Unlock()
	failure := c.toolFailure
	c.toolFailure = nil
	return failure
}

// trackTool stores pending-tool state for a given callID. Lazy-inits
// the map so zero-value acpClient values (common in tests) don't
// panic on the first tool call.
func (c *acpClient) trackTool(callID string, p *pendingToolCall) {
	c.toolMu.Lock()
	defer c.toolMu.Unlock()
	if c.pendingTools == nil {
		c.pendingTools = make(map[string]*pendingToolCall)
	}
	c.pendingTools[callID] = p
}

// getPendingTool returns the pending entry (may be nil) without
// removing it. Safe to call on a zero-value acpClient.
func (c *acpClient) getPendingTool(callID string) *pendingToolCall {
	c.toolMu.Lock()
	defer c.toolMu.Unlock()
	if c.pendingTools == nil {
		return nil
	}
	return c.pendingTools[callID]
}

// takePendingTool removes and returns the pending entry, or nil if
// none was tracked (e.g. the tool completed before we saw its start,
// or we missed the start frame).
func (c *acpClient) takePendingTool(callID string) *pendingToolCall {
	c.toolMu.Lock()
	defer c.toolMu.Unlock()
	if c.pendingTools == nil {
		return nil
	}
	p := c.pendingTools[callID]
	delete(c.pendingTools, callID)
	return p
}

// emitDeferredToolUse emits a buffered MessageToolUse right before the
// matching MessageToolResult. Handles three cases:
//   - tool already emitted on tool_call → skip
//   - kimi tool with streamed args → parse accumulated JSON as Input
//   - unknown tool (completed arrived without a start frame) →
//     synthesize minimal info from the update's own fields
func (c *acpClient) emitDeferredToolUse(
	p *pendingToolCall,
	callID, updateTitle, updateKind string,
	updateRawInput map[string]any,
) {
	if p != nil && p.emitted {
		return
	}

	var toolName string
	var input map[string]any

	switch {
	case p != nil && p.input != nil:
		// Pre-buffered rawInput path — shouldn't happen because we set
		// emitted=true in that case, but handle defensively.
		toolName = p.toolName
		input = p.input
	case p != nil:
		toolName = p.toolName
		input = parseToolArgsJSON(p.argsText)
	default:
		// No record of the start frame — fall back to the update's own
		// title/kind/rawInput so the UI at least sees the tool name.
		toolName = acpToolNameFromTitle(updateTitle, updateKind)
		input = updateRawInput
	}

	if c.onMessage == nil {
		return
	}
	c.onMessage(Message{
		Type:   MessageToolUse,
		Tool:   toolName,
		CallID: callID,
		Input:  input,
	})
}

// parseToolArgsJSON turns kimi's accumulated args string into the
// structured map the UI expects under Message.Input. Kimi sends args
// as a JSON-encoded object (`{"command":"echo hi"}`), so a full JSON
// parse recovers the original tool-arg shape. On malformed input
// (streaming glitch, non-JSON tool) we preserve the raw text under a
// `text` key so the UI still has something to render.
func parseToolArgsJSON(argsText string) map[string]any {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsText), &m); err == nil {
		return m
	}
	return map[string]any{"text": argsText}
}

// extractACPToolCallText concatenates the rendered text of every ACP
// block in a tool_call / tool_call_update's `content` array.
//
// Handles the two block types kimi emits:
//   - {type:"content", content:{type:"text", text:"..."}} — plain text
//     (shell output, tool args). Text is concatenated verbatim.
//   - {type:"diff", path, oldText, newText} — FileEdit output. Rendered
//     as a minimal unified-diff header so the UI distinguishes writes
//     from reads without needing a diff viewer.
//
// Terminal blocks ({type:"terminal", terminalId}) reference a remote
// terminal the client would normally subscribe to via terminal/output;
// we don't advertise terminal capability so we never receive those in
// practice, but if one slips through we skip it (nothing useful to
// surface from a bare ID).
func extractACPToolCallText(blocks []json.RawMessage) string {
	var b strings.Builder
	appendPiece := func(piece string) {
		if piece == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(piece)
	}
	for _, raw := range blocks {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil {
			continue
		}
		switch kind.Type {
		case "content":
			var outer struct {
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(raw, &outer); err != nil || len(outer.Content) == 0 {
				continue
			}
			var inner struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(outer.Content, &inner); err != nil {
				continue
			}
			if inner.Type != "text" {
				continue
			}
			appendPiece(inner.Text)
		case "diff":
			var diff struct {
				Path    string `json:"path"`
				OldText string `json:"oldText"`
				NewText string `json:"newText"`
			}
			if err := json.Unmarshal(raw, &diff); err != nil || diff.Path == "" {
				continue
			}
			// Keep it tiny — a full unified diff can be huge and we're
			// really just recording "this tool wrote to this file".
			// The UI can re-read the file if it needs the actual content.
			var piece strings.Builder
			piece.WriteString("--- ")
			piece.WriteString(diff.Path)
			piece.WriteString("\n+++ ")
			piece.WriteString(diff.Path)
			if diff.OldText == "" {
				piece.WriteString("\n(new file, ")
				piece.WriteString(strconv.Itoa(len(diff.NewText)))
				piece.WriteString(" bytes)")
			} else {
				piece.WriteString("\n(edited: ")
				piece.WriteString(strconv.Itoa(len(diff.OldText)))
				piece.WriteString(" → ")
				piece.WriteString(strconv.Itoa(len(diff.NewText)))
				piece.WriteString(" bytes)")
			}
			appendPiece(piece.String())
		default:
			// terminal blocks, image blocks, unknown future types —
			// ignore. We have no way to inline-render them.
		}
	}
	return b.String()
}

func (c *acpClient) handleUsageUpdate(data json.RawMessage) {
	var msg struct {
		Used  int64 `json:"used"`
		Size  int64 `json:"size"`
		Usage struct {
			InputTokens      int64 `json:"inputTokens"`
			OutputTokens     int64 `json:"outputTokens"`
			TotalTokens      int64 `json:"totalTokens"`
			CachedReadTokens int64 `json:"cachedReadTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	if msg.Size > 0 && msg.Used >= 0 {
		percent := float64(msg.Used) * 100 / float64(msg.Size)
		contextTokens, contextWindow := msg.Used, msg.Size
		c.runtimeStatsMu.Lock()
		c.runtimeStats = &RuntimeTokenStats{
			Provider:       "acp",
			TotalTokens:    contextTokens,
			ContextTokens:  &contextTokens,
			ContextWindow:  &contextWindow,
			ContextPercent: &percent,
		}
		c.runtimeStatsMu.Unlock()
	}

	c.usageMu.Lock()
	// Usage updates from ACP are cumulative snapshots, so take the latest.
	if msg.Usage.InputTokens > c.usage.InputTokens {
		c.usage.InputTokens = msg.Usage.InputTokens
	}
	if msg.Usage.OutputTokens > c.usage.OutputTokens {
		c.usage.OutputTokens = msg.Usage.OutputTokens
	}
	if msg.Usage.CachedReadTokens > c.usage.CacheReadTokens {
		c.usage.CacheReadTokens = msg.Usage.CachedReadTokens
	}
	c.usageMu.Unlock()
}

func (c *acpClient) currentRuntimeStats() *RuntimeTokenStats {
	c.runtimeStatsMu.Lock()
	defer c.runtimeStatsMu.Unlock()
	if c.runtimeStats == nil {
		return nil
	}
	copy := *c.runtimeStats
	return &copy
}

// ── Helpers ──

// extractACPSessionID pulls `sessionId` out of a session/new or
// session/resume response. Shared by all ACP backends (kimi, kiro,
// and anything else that follows the standard ACP schema).
func extractACPSessionID(result json.RawMessage) string {
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return ""
	}
	return r.SessionID
}

// extractACPCurrentModelID pulls the model selected by the ACP runtime out of
// a session/new or session/resume response. Some ACP servers return this when they use
// its own default model, so token usage can still be attributed to a real model
// even when Multica did not pass an explicit agent.model override.
func extractACPCurrentModelID(result json.RawMessage) string {
	var r struct {
		Models struct {
			CurrentModelID      string `json:"currentModelId"`
			CurrentModelIDSnake string `json:"current_model_id"`
		} `json:"models"`
		CurrentModelID      string `json:"currentModelId"`
		CurrentModelIDSnake string `json:"current_model_id"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return ""
	}
	for _, candidate := range []string{
		r.Models.CurrentModelID,
		r.Models.CurrentModelIDSnake,
		r.CurrentModelID,
		r.CurrentModelIDSnake,
	} {
		if model := strings.TrimSpace(candidate); model != "" {
			return model
		}
	}
	return ""
}

// resolveResumedSessionID picks which session id we should treat as live
// after a `session/resume` round-trip. ACP servers
// return the canonical sessionId in the response — when the local
// state.db has been wiped, the server silently creates a brand-new
// session and returns its new id rather than failing. If we keep using
// our requested id in that case, every subsequent session/prompt is
// addressed to a session the server doesn't know about and fails with
// JSON-RPC -32603. Returns (chosenID, changed). When the response is
// malformed or omits sessionId we fall back to the requested id so the
// happy path keeps working against older / non-conforming servers.
func resolveResumedSessionID(requested string, response json.RawMessage) (string, bool) {
	got := extractACPSessionID(response)
	if got == "" {
		return requested, false
	}
	return got, got != requested
}

// buildACPSessionParams constructs the params map for the ACP `session/new`
// request. The `model` field is only included when non-empty so the ACP server falls
// back to its default only when no explicit model was configured.
//
// mcpServers should be the ACP-shaped array produced by buildACPMcpServers
// from the agent's mcp_config; a nil slice is normalised to an empty array
// so the wire request always carries the field (ACP requires it).
func buildACPSessionParams(cwd, model string, mcpServers []any) map[string]any {
	if mcpServers == nil {
		mcpServers = []any{}
	}
	params := map[string]any{
		"cwd":        cwd,
		"mcpServers": mcpServers,
	}
	if model != "" {
		params["model"] = model
	}
	return params
}

// buildACPMcpServers translates an agent's Claude-style mcp_config
// (`{"mcpServers": {"<name>": {...}}}`) into the array shape that ACP's
// `session/new` and `session/load` requests expect.
//
// Each Claude-style entry maps to one of:
//
//   - Stdio:  `{name, command, args, env: [{name,value}, ...]}` —
//     when the entry has a `command` field. No `type` field is emitted;
//     ACP treats untagged entries as stdio.
//   - HTTP / SSE: `{type, name, url, headers: [{name,value}, ...]}` —
//     when the entry has a `url` field. `type` defaults to "http"; Claude's
//     "sse" and "streamable-http" / "http_streamable" aliases are accepted.
//
// Empty / null input returns an empty slice — the launch proceeds with no
// MCP servers (the existing default for ACP backends). Malformed top-level
// JSON returns an error so the launch fails closed, mirroring codex's
// `renderCodexMcpServersBlock` contract. Individual entries that have
// neither `command` nor `url` are skipped with a warning rather than
// failing the whole launch, so a single bad entry can't kill the agent.
//
// Output entries are sorted by name and each entry's env / headers are
// sorted by key, so the wire request is deterministic across reruns —
// useful for tests, log diffs, and reproducibility.
func buildACPMcpServers(raw json.RawMessage, logger *slog.Logger) ([]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []any{}, nil
	}
	var parsed struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return nil, fmt.Errorf("parse mcp_config json: %w", err)
	}
	if len(parsed.McpServers) == 0 {
		return []any{}, nil
	}

	names := make([]string, 0, len(parsed.McpServers))
	for name := range parsed.McpServers {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]any, 0, len(names))
	for _, name := range names {
		entry, err := convertACPMcpServer(name, parsed.McpServers[name])
		if err != nil {
			if logger != nil {
				logger.Warn("skipping invalid mcp_config entry", "name", name, "error", err)
			}
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// convertACPMcpServer converts a single Claude-style entry into the ACP
// McpServer wire shape. Returns an error for entries that can't be
// classified (no command and no url).
func convertACPMcpServer(name string, raw json.RawMessage) (map[string]any, error) {
	var entry struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("parse entry: %w", err)
	}

	command := strings.TrimSpace(entry.Command)
	url := strings.TrimSpace(entry.URL)

	if command != "" {
		args := entry.Args
		if args == nil {
			args = []string{}
		}
		envArr := make([]map[string]any, 0, len(entry.Env))
		for _, k := range sortedStringMapKeys(entry.Env) {
			envArr = append(envArr, map[string]any{
				"name":  k,
				"value": entry.Env[k],
			})
		}
		return map[string]any{
			"name":    name,
			"command": command,
			"args":    args,
			"env":     envArr,
		}, nil
	}

	if url != "" {
		t := strings.ToLower(strings.TrimSpace(entry.Type))
		switch t {
		case "sse":
			t = "sse"
		case "", "http", "streamable-http", "http_streamable":
			t = "http"
		default:
			// Unknown remote transport — degrade to "http" rather than fail.
			// ACP servers that don't recognise the type will reject the
			// session/new request and surface a real error to the user.
			t = "http"
		}
		headerArr := make([]map[string]any, 0, len(entry.Headers))
		for _, k := range sortedStringMapKeys(entry.Headers) {
			headerArr = append(headerArr, map[string]any{
				"name":  k,
				"value": entry.Headers[k],
			})
		}
		return map[string]any{
			"type":    t,
			"name":    name,
			"url":     url,
			"headers": headerArr,
		}, nil
	}

	return nil, fmt.Errorf("entry has neither command nor url")
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// acpMcpTransportCapabilities reports which remote MCP transports the ACP
// runtime advertised in its `initialize` response. Stdio is always
// supported (it's the baseline transport and the spec does not gate it),
// so it's not represented here.
type acpMcpTransportCapabilities struct {
	HTTP bool
	SSE  bool
}

// extractACPMcpCapabilities reads `agentCapabilities.mcpCapabilities.http`
// and `.sse` out of an ACP `initialize` response. Missing or false fields
// stay false, matching the spec default: the runtime must opt-in to
// remote MCP transports. Unparseable responses degrade to "neither
// supported" so we fail closed on remote entries.
//
// See https://agentclientprotocol.com/protocol/initialization — clients
// MUST NOT send `mcpServers` entries with a type the agent did not
// advertise support for.
func extractACPMcpCapabilities(result json.RawMessage) acpMcpTransportCapabilities {
	var r struct {
		AgentCapabilities struct {
			McpCapabilities struct {
				HTTP bool `json:"http"`
				SSE  bool `json:"sse"`
			} `json:"mcpCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return acpMcpTransportCapabilities{}
	}
	return acpMcpTransportCapabilities{
		HTTP: r.AgentCapabilities.McpCapabilities.HTTP,
		SSE:  r.AgentCapabilities.McpCapabilities.SSE,
	}
}

// filterACPMcpServersByCapability drops remote MCP entries whose transport
// the runtime didn't advertise in its initialize response. Stdio entries
// (no `type` field) always pass through.
//
// Sending an http/sse entry to a runtime that doesn't support it is a
// protocol violation per the ACP spec, and Kimi observed in
// practice reject the whole session/new request with a JSON-RPC error.
// Dropping the offending entries with a warning lets the rest of the
// session start and surfaces the problem in the daemon log instead of
// tanking every task on that agent.
func filterACPMcpServersByCapability(
	servers []any,
	caps acpMcpTransportCapabilities,
	backend string,
	logger *slog.Logger,
) []any {
	if len(servers) == 0 {
		return servers
	}
	filtered := make([]any, 0, len(servers))
	for _, raw := range servers {
		entry, ok := raw.(map[string]any)
		if !ok {
			filtered = append(filtered, raw)
			continue
		}
		transport, _ := entry["type"].(string)
		switch transport {
		case "http":
			if !caps.HTTP {
				if logger != nil {
					logger.Warn("dropping http MCP server: runtime did not advertise mcpCapabilities.http",
						"backend", backend, "name", entry["name"])
				}
				continue
			}
		case "sse":
			if !caps.SSE {
				if logger != nil {
					logger.Warn("dropping sse MCP server: runtime did not advertise mcpCapabilities.sse",
						"backend", backend, "name", entry["name"])
				}
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// acpToolNameFromTitle extracts a tool name from the ACP tool call title.
// ACP titles look like "terminal: ls -la", "read: /path/to/file", etc.
// Some titles have no colon (e.g. "execute code").
func acpToolNameFromTitle(title string, kind string) string {
	// Check exact-match titles first (no colon).
	switch title {
	case "execute code":
		return "execute_code"
	}

	// Try to extract the tool name from before the first colon.
	if idx := strings.Index(title, ":"); idx > 0 {
		name := strings.TrimSpace(title[:idx])
		// Map common ACP title prefixes back to tool names.
		// Some titles include mode info like "patch (replace)", so check prefix.
		switch {
		case name == "terminal":
			return "terminal"
		case name == "read":
			return "read_file"
		case name == "write":
			return "write_file"
		case strings.HasPrefix(name, "patch"):
			return "patch"
		case name == "search":
			return "search_files"
		case name == "web search":
			return "web_search"
		case name == "extract":
			return "web_extract"
		case name == "delegate":
			return "delegate_task"
		case name == "analyze image":
			return "vision_analyze"
		}
		return name
	}

	// Fall back to kind.
	switch kind {
	case "read":
		return "read_file"
	case "edit":
		return "write_file"
	case "execute":
		return "terminal"
	case "search":
		return "search_files"
	case "fetch":
		return "web_search"
	case "think":
		return "thinking"
	default:
		// Preserve a non-empty title when we can't classify it: kimi
		// emits bare titles like "Shell" or "Read file" without any
		// `kind`, so returning an empty string here drops the tool
		// name entirely before kimiToolNameFromTitle can map it.
		// Titles with a colon never reach
		// this branch with a non-empty title.
		if title != "" {
			return title
		}
		return kind
	}
}

// ── Provider-error sniffing ──
//
// ACP agents (kimi, kiro, …) all have the same failure mode:
// session/prompt reports stopReason=end_turn even when the underlying
// HTTP call to the configured LLM endpoint returned an error — the
// actionable detail only appears on stderr (e.g.
// `⚠️ API call failed (attempt 1/3): BadRequestError [HTTP 400]` and
// `Error: HTTP 400: Error code: 400 - {'detail': "The '...' model
// is not supported when using Codex with a ChatGPT account."}`).
// The sniffer scans for those patterns so the daemon can surface a
// real failure instead of a generic "empty output".
//
// Parameterised by provider name so ACP adapters can share
// the transport: the regexes match format-level signals (HTTP status,
// error-kind tags, "API call failed" banner) that both runtimes emit.
//
// The sniffer distinguishes *transient* per-attempt warnings (e.g.
// "API call failed (attempt 1/3): RateLimitError [HTTP 429]" — followed
// by a successful retry) from *terminal* exhausted failures (e.g.
// "API call failed after 3 retries: ..." or "❌ ... Non-retryable"):
// `message()` returns whichever was last seen, while `terminalMessage()`
// returns non-empty only when a terminal-failure marker was matched.
// Promotion to status="failed" must use `terminalMessage()`, otherwise
// a successful retry following an early per-attempt warning would be
// wrongly marked as failed.
type acpProviderErrorSniffer struct {
	provider string
	mu       sync.Mutex
	remains  []byte   // buffer for a partial trailing line across writes
	lines    []string // captured error lines, bounded
	seen     map[string]bool
	terminal bool // sticky: at least one line matched acpTerminalErrorRe
}

// acpErrorHeaderRe matches the first line of an API-error block.
// ACP agents typically prefix these with ⚠️ / ❌ and include an HTTP
// status code or a non-retryable-error tag.
var acpErrorHeaderRe = regexp.MustCompile(`(?:⚠️|❌|\[ERROR\]).*(?:BadRequestError|AuthenticationError|RateLimitError|HTTP [0-9]{3}|Non-retryable|API call failed)`)

// acpErrorDetailRe pulls the most useful single-line messages out of
// the subsequent lines of the error block (the one whose "Error:" or
// "Details:" tag actually spells out what happened).
var acpErrorDetailRe = regexp.MustCompile(`(?:Error:|detail:|Details:)\s*(.+)`)

// acpTerminalErrorRe matches markers that only appear when the
// adapter has *given up* on the upstream call — either after
// exhausting retries ("after N retries"), or because the error is
// classified as non-retryable up front (Non-retryable, BadRequest /
// Authentication errors, ❌ / [ERROR] log levels). Per-attempt
// warnings ("(attempt 1/3)") deliberately do NOT match this pattern.
var acpTerminalErrorRe = regexp.MustCompile(`(?:❌|\[ERROR\]|after \d+ retr|Non-retryable|BadRequestError|AuthenticationError)`)

// acpAgentOutputTerminalRe matches the synthetic agent-text turn that
// ACP adapters inject when they exhaust retries against
// the upstream LLM ("API call failed after 3 retries: HTTP 429..."),
// surfaced via session/update agent_message_chunk and ending up in the
// final output buffer. Per-attempt warnings (which only go to stderr
// and use "(attempt N/M)" phrasing) won't match.
var acpAgentOutputTerminalRe = regexp.MustCompile(`API call failed after \d+ retr(?:y|ies)`)

const acpMaxErrorLines = 8

// newACPProviderErrorSniffer returns a sniffer that tags its messages
// with the given provider name (e.g. "kiro", "kimi") so failure
// strings make it obvious which runtime produced the error.
func newACPProviderErrorSniffer(provider string) *acpProviderErrorSniffer {
	return &acpProviderErrorSniffer{provider: provider, seen: map[string]bool{}}
}

// Write implements io.Writer so the sniffer can sit behind an
// io.MultiWriter next to the normal stderr log forwarder.
func (s *acpProviderErrorSniffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data := append(s.remains, p...)
	// Keep the final partial line (no trailing newline) for the
	// next write so multi-line error blocks aren't split.
	nl := strings.LastIndexByte(string(data), '\n')
	var complete string
	if nl < 0 {
		s.remains = append(s.remains[:0], data...)
		return len(p), nil
	}
	complete = string(data[:nl])
	s.remains = append(s.remains[:0], data[nl+1:]...)

	for _, line := range strings.Split(complete, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !(acpErrorHeaderRe.MatchString(line) || acpErrorDetailRe.MatchString(line)) {
			continue
		}
		if acpTerminalErrorRe.MatchString(line) {
			s.terminal = true
		}
		if s.seen[line] {
			continue
		}
		s.seen[line] = true
		s.lines = append(s.lines, line)
		if len(s.lines) > acpMaxErrorLines {
			s.lines = s.lines[len(s.lines)-acpMaxErrorLines:]
		}
	}
	return len(p), nil
}

// message returns a single-line summary suitable for the task
// error field. Prefers the most specific "Error:" / "detail:"
// fragment; falls back to the first captured header line; empty
// when nothing useful was seen.
//
// NOTE: a non-empty message() can describe a *transient* per-attempt
// warning that was followed by a successful retry. Code that flips
// task status to "failed" must instead use terminalMessage() — see
// the type doc above.
func (s *acpProviderErrorSniffer) message() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.messageLocked()
}

// terminalMessage returns the same single-line summary as message()
// but only when the sniffer has seen at least one line matching
// acpTerminalErrorRe — i.e. the adapter has given up retrying. This
// is the signal callers should use to decide whether to promote a
// run from "completed" to "failed". Returns empty if all captured
// lines look like transient retry warnings.
func (s *acpProviderErrorSniffer) terminalMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.terminal {
		return ""
	}
	return s.messageLocked()
}

// messageLocked is the lock-held implementation shared by message()
// and terminalMessage(). Caller must hold s.mu.
func (s *acpProviderErrorSniffer) messageLocked() string {
	prefix := s.provider + " provider error: "
	for _, line := range s.lines {
		if m := acpErrorDetailRe.FindStringSubmatch(line); m != nil {
			detail := strings.TrimSpace(m[1])
			if detail != "" {
				return prefix + detail
			}
		}
	}
	for _, line := range s.lines {
		if acpErrorHeaderRe.MatchString(line) {
			return prefix + line
		}
	}
	return ""
}

// promoteACPResultOnProviderError flips finalStatus to "failed" if
// either (a) the stderr sniffer captured a terminal-failure marker,
// (b) the adapter injected a synthetic "API call failed after N
// retries..." turn into the agent text stream, or (c) output was
// empty AND the sniffer captured anything at all (no real result to
// fall back on, even from a transient-only sequence). Returns the
// updated (status, error) pair; callers should overwrite their
// locals with the result.
//
// This is the shared post-processing step for ACP adapters.
// Without it, runs that exhaust retries against the upstream LLM
// (HTTP 429, expired token, …) silently report as "completed"
// because session/prompt still ends with stopReason=end_turn — see
// GitHub multica#1952.
func promoteACPResultOnProviderError(finalStatus, finalError, finalOutput string, sniffer *acpProviderErrorSniffer) (string, string) {
	if finalStatus != "completed" {
		return finalStatus, finalError
	}
	if msg := sniffer.terminalMessage(); msg != "" {
		return "failed", msg
	}
	if acpAgentOutputTerminalRe.MatchString(finalOutput) {
		msg := sniffer.message()
		if msg == "" {
			msg = sniffer.provider + " provider error: " + acpAgentOutputTerminalRe.FindString(finalOutput)
		}
		return "failed", msg
	}
	if finalOutput == "" {
		if msg := sniffer.message(); msg != "" {
			return "failed", msg
		}
	}
	return finalStatus, finalError
}
