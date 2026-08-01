// Command doubao-dialog-spike is the LRM-945 Demo harness for Doubao Realtime
// Duplex + Multica tool bridge. It does not replace production RTC VoiceChat.
//
// Usage:
//
//	# Offline bridge proof (no Key):
//	go run ./cmd/doubao-dialog-spike --dry-run-bridge
//
//	# Live session (requires DOUBAO_DIALOG_API_KEY):
//	DOUBAO_DIALOG_API_KEY=... go run ./cmd/doubao-dialog-spike --listen 30s
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/doubaodialog"
)

func main() {
	dryRun := flag.Bool("dry-run-bridge", false, "exercise Multica tool bridge without dialing Doubao")
	listen := flag.Duration("listen", 0, "after session.created, listen for events this long (needs API key)")
	request := flag.String("request", "创建一个 issue，修复登录失败", "delegate request used by --dry-run-bridge")
	flag.Parse()

	if *dryRun {
		if err := runDryBridge(*request); err != nil {
			fmt.Fprintf(os.Stderr, "dry-run-bridge failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("dry-run-bridge ok: Multica FC → executor → conversation.item.create payload path verified")
		return
	}

	cfg := doubaodialog.ConfigFromEnv()
	if err := cfg.ValidateForDial(); err != nil {
		fmt.Fprintf(os.Stderr, "blocked: %v\n", err)
		fmt.Fprintf(os.Stderr, "open Dialog product keys in Volcengine console, then set %s (see docs/superpowers/plans/2026-08-01-doubao-realtime-duplex-spike.md)\n", doubaodialog.EnvAPIKey)
		fmt.Fprintf(os.Stderr, "offline path: go run ./cmd/doubao-dialog-spike --dry-run-bridge\n")
		os.Exit(2)
	}

	if *listen <= 0 {
		*listen = 15 * time.Second
	}
	if err := runLive(cfg, *listen); err != nil {
		fmt.Fprintf(os.Stderr, "live spike failed: %v\n", err)
		os.Exit(1)
	}
}

func runDryBridge(request string) error {
	sender := &captureSender{}
	executor := &doubaodialog.RecordingExecutor{Result: "已经在 Multica 创建 issue（Spike dry-run）。"}
	bridge, err := doubaodialog.NewMulticaToolBridge(executor, sender)
	if err != nil {
		return err
	}
	handled, err := bridge.HandleServerEvent(context.Background(), doubaodialog.ServerEvent{
		Type: doubaodialog.EventFunctionCallArgumentsDone,
		FunctionCalls: []doubaodialog.FunctionCall{{
			CallID:    "spike-call-1",
			Name:      doubaodialog.MulticaDelegateToolName,
			Arguments: fmt.Sprintf(`{"request":%q}`, request),
		}},
	})
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("function call was not handled")
	}
	if len(executor.Calls) != 1 || executor.Calls[0] != strings.TrimSpace(request) {
		return fmt.Errorf("unexpected executor calls: %#v", executor.Calls)
	}
	if len(sender.outputs) != 1 || sender.outputs[0].CallID != "spike-call-1" {
		return fmt.Errorf("unexpected tool outputs: %#v", sender.outputs)
	}
	fmt.Printf("tool output: %s\n", sender.outputs[0].Output)
	return nil
}

func runLive(cfg doubaodialog.Config, listen time.Duration) error {
	client, err := doubaodialog.New(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), listen+10*time.Second)
	defer cancel()

	session, err := client.OpenSession(ctx, doubaodialog.DefaultSessionConfig(
		cfg.Model,
		cfg.Voice,
		doubaodialog.DefaultDialogInstructions(),
		[]doubaodialog.Tool{doubaodialog.MulticaDelegateTool()},
	))
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	defer session.Close(context.Background())

	executor := &doubaodialog.RecordingExecutor{
		Result: "已经帮你把任务交给 Multica Agent 了，请在当前私聊查看进度。",
	}
	bridge, err := doubaodialog.NewMulticaToolBridge(executor, session)
	if err != nil {
		return err
	}

	fmt.Printf("connected logid=%s; listening %s for ASR/TTS/FC (speak into a client that streams PCM16k, or wait for server events)\n",
		session.LogID(), listen)

	deadline := time.Now().Add(listen)
	for time.Now().Before(deadline) {
		recvCtx, recvCancel := context.WithTimeout(ctx, time.Until(deadline))
		event, err := session.Recv(recvCtx)
		recvCancel()
		if err != nil {
			if ctx.Err() != nil || strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "i/o timeout") {
				break
			}
			return err
		}
		fmt.Printf("event type=%s session_id=%s text=%q audio_bytes=%d fc=%d\n",
			event.Type, event.SessionID, trim(event.Text+event.Delta+event.Transcript, 80), len(event.Audio), len(event.FunctionCalls))
		if event.Type == doubaodialog.EventError {
			return fmt.Errorf("provider error: %s", event.ErrorMessage)
		}
		handled, err := bridge.HandleServerEvent(ctx, event)
		if err != nil {
			return fmt.Errorf("bridge: %w", err)
		}
		if handled {
			fmt.Printf("multica delegate calls=%v\n", executor.Calls)
		}
	}
	fmt.Println("listen window ended")
	return nil
}

type captureSender struct {
	outputs []doubaodialog.FunctionCallOutput
}

func (s *captureSender) SendFunctionCallOutputs(_ context.Context, outputs []doubaodialog.FunctionCallOutput) error {
	s.outputs = append(s.outputs, outputs...)
	return nil
}

func (s *captureSender) CancelResponse(context.Context) error { return nil }

func trim(value string, max int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "…"
}
