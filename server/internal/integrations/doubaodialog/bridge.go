package doubaodialog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// MulticaExecutor runs the Multica-side action for a dialog function call.
// Production wiring should reuse VoiceCallAgentBridge semantics (DM transcript →
// agent wake). The Spike ships a RecordingExecutor for demos without a live server.
type MulticaExecutor interface {
	Delegate(ctx context.Context, request string) (resultSpeech string, err error)
}

type channelContextExecutor interface {
	ChannelContext(ctx context.Context, action, channelID, query string) (string, error)
}

// RecordingExecutor records delegate calls and returns a fixed spoken result.
type RecordingExecutor struct {
	Result string
	mu     sync.Mutex
	Calls  []string
}

func (e *RecordingExecutor) ChannelContext(_ context.Context, action, channelID, query string) (string, error) {
	return strings.TrimSpace(strings.Join([]string{action, channelID, query}, " ")), nil
}

func (e *RecordingExecutor) Delegate(_ context.Context, request string) (string, error) {
	request = strings.TrimSpace(request)
	if request == "" {
		return "", fmt.Errorf("multica delegate request is required")
	}
	e.mu.Lock()
	e.Calls = append(e.Calls, request)
	e.mu.Unlock()
	result := strings.TrimSpace(e.Result)
	if result == "" {
		result = "已经帮你在 Multica 里开好任务了。"
	}
	return result, nil
}

// SessionSender is the subset of Session used by the tool bridge.
type SessionSender interface {
	SendFunctionCallOutputs(ctx context.Context, outputs []FunctionCallOutput) error
	CancelResponse(ctx context.Context) error
}

// MulticaToolBridge maps Duplex function-call events onto Multica / web tools
// and feeds spoken results back into the dialog session.
type MulticaToolBridge struct {
	executor MulticaExecutor
	web      WebToolkit
	sender   SessionSender
	inFlight sync.Map
}

func NewMulticaToolBridge(executor MulticaExecutor, sender SessionSender) (*MulticaToolBridge, error) {
	return NewMulticaToolBridgeWithWeb(executor, DefaultHTTPWebToolkit(), sender)
}

func NewMulticaToolBridgeWithWeb(executor MulticaExecutor, web WebToolkit, sender SessionSender) (*MulticaToolBridge, error) {
	if executor == nil {
		return nil, fmt.Errorf("multica dialog tool bridge requires an executor")
	}
	if sender == nil {
		return nil, fmt.Errorf("multica dialog tool bridge requires a session sender")
	}
	if web == nil {
		web = DefaultHTTPWebToolkit()
	}
	return &MulticaToolBridge{executor: executor, web: web, sender: sender}, nil
}

type delegateArguments struct {
	Request string `json:"request"`
}

type webSearchArguments struct {
	Query string `json:"query"`
}

type webFetchArguments struct {
	URL string `json:"url"`
}

type channelContextArguments struct {
	Action    string `json:"action"`
	ChannelID string `json:"channel_id"`
	Query     string `json:"query"`
}

// HandleServerEvent processes one inbound Duplex event.
// Returns true when the event was a known function call that was handled.
func (b *MulticaToolBridge) HandleServerEvent(ctx context.Context, event ServerEvent) (bool, error) {
	if b == nil {
		return false, fmt.Errorf("multica dialog tool bridge is nil")
	}
	switch event.Type {
	case EventASRStarted:
		// Best-effort barge-in: cancel in-flight model speech when user starts talking.
		_ = b.sender.CancelResponse(ctx)
		return false, nil
	case EventFunctionCallArgumentsDone:
		if len(event.FunctionCalls) == 0 {
			return false, fmt.Errorf("function call event missing items")
		}
		handled := false
		for _, call := range event.FunctionCalls {
			ok, err := b.handleFunctionCall(ctx, call)
			if err != nil {
				return handled, err
			}
			handled = handled || ok
		}
		return handled, nil
	default:
		return false, nil
	}
}

func (b *MulticaToolBridge) handleFunctionCall(ctx context.Context, call FunctionCall) (bool, error) {
	name := strings.TrimSpace(call.Name)
	callID := strings.TrimSpace(call.CallID)
	if callID == "" {
		return false, fmt.Errorf("function call missing call_id")
	}
	switch name {
	case MulticaDelegateToolName:
		return b.runAndReturn(ctx, callID, name, func() (string, error) {
			var args delegateArguments
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return "", fmt.Errorf("parse %s arguments: %w", MulticaDelegateToolName, err)
			}
			request := strings.TrimSpace(args.Request)
			if request == "" {
				return "", fmt.Errorf("%s request is required", MulticaDelegateToolName)
			}
			return b.executor.Delegate(ctx, request)
		})
	case WebSearchToolName:
		return b.runAndReturn(ctx, callID, name, func() (string, error) {
			var args webSearchArguments
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return "", fmt.Errorf("parse %s arguments: %w", WebSearchToolName, err)
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", fmt.Errorf("%s query is required", WebSearchToolName)
			}
			return b.web.Search(ctx, query)
		})
	case MulticaChannelContextToolName:
		return b.runAndReturn(ctx, callID, name, func() (string, error) {
			var args channelContextArguments
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return "", fmt.Errorf("parse %s arguments: %w", name, err)
			}
			executor, ok := b.executor.(channelContextExecutor)
			if !ok {
				return "", errors.New("Multica channel context is unavailable")
			}
			return executor.ChannelContext(ctx, strings.TrimSpace(args.Action), strings.TrimSpace(args.ChannelID), strings.TrimSpace(args.Query))
		})
	case WebFetchToolName:
		return b.runAndReturn(ctx, callID, name, func() (string, error) {
			var args webFetchArguments
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return "", fmt.Errorf("parse %s arguments: %w", WebFetchToolName, err)
			}
			pageURL := strings.TrimSpace(args.URL)
			if pageURL == "" {
				return "", fmt.Errorf("%s url is required", WebFetchToolName)
			}
			return b.web.Fetch(ctx, pageURL)
		})
	default:
		failText := fmt.Sprintf("当前通话不支持工具 %s，请换个说法，或让我用 web_search 查事实、用派活工具开任务。", name)
		if _, loaded := b.inFlight.LoadOrStore(callID, struct{}{}); loaded {
			return true, nil
		}
		defer b.inFlight.Delete(callID)
		if sendErr := b.sender.SendFunctionCallOutputs(ctx, []FunctionCallOutput{{
			CallID: callID,
			Output: failText,
		}}); sendErr != nil {
			return true, fmt.Errorf("unknown tool %s and could not return tool output: %w", name, sendErr)
		}
		return true, nil
	}
}

func (b *MulticaToolBridge) runAndReturn(ctx context.Context, callID, name string, run func() (string, error)) (bool, error) {
	if _, loaded := b.inFlight.LoadOrStore(callID, struct{}{}); loaded {
		return true, nil
	}
	defer b.inFlight.Delete(callID)

	result, err := run()
	if err != nil {
		failText := fmt.Sprintf("%s 失败：%s", name, err.Error())
		if sendErr := b.sender.SendFunctionCallOutputs(ctx, []FunctionCallOutput{{
			CallID: callID,
			Output: failText,
		}}); sendErr != nil {
			return true, fmt.Errorf("%s failed (%v) and could not return tool output: %w", name, err, sendErr)
		}
		return true, err
	}
	if err := b.sender.SendFunctionCallOutputs(ctx, []FunctionCallOutput{{
		CallID: callID,
		Output: result,
	}}); err != nil {
		return true, fmt.Errorf("return %s tool output: %w", name, err)
	}
	return true, nil
}
