package doubaodialog

import (
	"context"
	"encoding/json"
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

// RecordingExecutor records delegate calls and returns a fixed spoken result.
type RecordingExecutor struct {
	Result string
	mu     sync.Mutex
	Calls  []string
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

// MulticaToolBridge maps Duplex function-call events onto Multica and feeds
// spoken results back into the dialog session.
type MulticaToolBridge struct {
	executor MulticaExecutor
	sender   SessionSender
	inFlight sync.Map
}

func NewMulticaToolBridge(executor MulticaExecutor, sender SessionSender) (*MulticaToolBridge, error) {
	if executor == nil {
		return nil, fmt.Errorf("multica dialog tool bridge requires an executor")
	}
	if sender == nil {
		return nil, fmt.Errorf("multica dialog tool bridge requires a session sender")
	}
	return &MulticaToolBridge{executor: executor, sender: sender}, nil
}

type delegateArguments struct {
	Request string `json:"request"`
}

// HandleServerEvent processes one inbound Duplex event.
// Returns true when the event was a Multica function call that was handled.
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
	if name != MulticaDelegateToolName {
		return false, nil
	}
	if _, loaded := b.inFlight.LoadOrStore(callID, struct{}{}); loaded {
		return true, nil
	}
	defer b.inFlight.Delete(callID)

	var args delegateArguments
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return true, fmt.Errorf("parse %s arguments: %w", MulticaDelegateToolName, err)
	}
	request := strings.TrimSpace(args.Request)
	if request == "" {
		return true, fmt.Errorf("%s request is required", MulticaDelegateToolName)
	}

	result, err := b.executor.Delegate(ctx, request)
	if err != nil {
		failText := fmt.Sprintf("Multica 执行失败：%s", err.Error())
		if sendErr := b.sender.SendFunctionCallOutputs(ctx, []FunctionCallOutput{{
			CallID: callID,
			Output: failText,
		}}); sendErr != nil {
			return true, fmt.Errorf("delegate failed (%v) and could not return tool output: %w", err, sendErr)
		}
		return true, err
	}
	if err := b.sender.SendFunctionCallOutputs(ctx, []FunctionCallOutput{{
		CallID: callID,
		Output: result,
	}}); err != nil {
		return true, fmt.Errorf("return multica tool output: %w", err)
	}
	return true, nil
}
