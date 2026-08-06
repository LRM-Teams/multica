package doubaodialog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const pcm16kChunkBytes = 640 // 20ms @ 16kHz mono s16le

// TestLiveFunctionCallToMultica synthesizes spoken user audio via Duplex TTS,
// loops it back as input (official ingress is audio), waits for
// delegate_work_to_multica_agent, and creates a real Multica issue.
func TestLiveFunctionCallToMultica(t *testing.T) {
	if strings.TrimSpace(os.Getenv("DOUBAO_DIALOG_LIVE")) == "" {
		t.Skip("set DOUBAO_DIALOG_LIVE=1 and DOUBAO_DIALOG_API_KEY to run live FC smoke")
	}
	if _, err := exec.LookPath("multica"); err != nil {
		t.Skip("multica CLI required for live Multica delegate")
	}

	cfg := ConfigFromEnv()
	// Keep the spoken prompt short and tool-forward; ASR mangles English brand names.
	spoken := "请调用工具，帮我创建一个开发任务 issue。"
	pcm16k, err := synthesizeSpeechPCM16k(cfg, spoken)
	if err != nil {
		t.Fatalf("synthesize user audio: %v", err)
	}
	if len(pcm16k) < pcm16kChunkBytes {
		t.Fatalf("synthesized audio too short: %d bytes", len(pcm16k))
	}
	t.Logf("synthesized user audio bytes=%d", len(pcm16k))

	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	session, err := client.OpenSession(ctx, DefaultSessionConfig(
		cfg.Model,
		cfg.Voice,
		DefaultDialogInstructions(),
		[]Tool{MulticaDelegateTool()},
	))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close(context.Background())

	created, err := session.Recv(ctx)
	if err != nil {
		t.Fatalf("recv session.created: %v", err)
	}
	if created.Type != EventSessionCreated || created.SessionID == "" {
		t.Fatalf("first event = type=%q sid=%q want session.created", created.Type, created.SessionID)
	}
	t.Logf("session ok id=%s logid=%s", created.SessionID, session.LogID())

	for _, chunk := range chunkAudio(pcm16k, pcm16kChunkBytes) {
		if err := session.SendAudio(ctx, chunk); err != nil {
			t.Fatalf("send audio: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := session.CommitAudio(ctx); err != nil {
		t.Fatalf("commit audio: %v", err)
	}

	silenceCtx, stopSilence := context.WithCancel(ctx)
	defer stopSilence()
	go sendSilence(silenceCtx, session)

	executor := &CLIIssueExecutor{t: t}
	bridge, err := NewMulticaToolBridge(executor, session)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(75 * time.Second)
	handledFC := false
	for time.Now().Before(deadline) && !handledFC {
		recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
		event, err := session.Recv(recvCtx)
		recvCancel()
		if err != nil {
			if strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "i/o timeout") {
				continue
			}
			t.Fatalf("recv: %v (logid=%s)", err, session.LogID())
		}
		t.Logf("event type=%s fc=%d transcript=%q err=%q", event.Type, len(event.FunctionCalls), trimRunes(event.Transcript+event.Delta+event.Text, 60), event.ErrorMessage)
		if event.Type == EventError {
			t.Fatalf("provider error: %s (logid=%s)", event.ErrorMessage, session.LogID())
		}
		ok, err := bridge.HandleServerEvent(ctx, event)
		if err != nil {
			t.Fatalf("bridge: %v", err)
		}
		if ok {
			handledFC = true
		}
	}
	stopSilence()
	if !handledFC {
		t.Fatalf("timed out waiting for %s (logid=%s)", MulticaDelegateToolName, session.LogID())
	}
	if len(executor.Calls) == 0 || strings.TrimSpace(executor.IssueID) == "" {
		t.Fatalf("expected Multica issue create; calls=%v issue=%q", executor.Calls, executor.IssueID)
	}
	t.Logf("live FC→Multica ok issue=%s request=%q", executor.IssueID, executor.Calls[0])
}

func synthesizeSpeechPCM16k(cfg Config, text string) ([]byte, error) {
	client, err := New(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// No tools — this session is only for client-driven TTS to obtain PCM.
	session, err := client.OpenSession(ctx, DefaultSessionConfig(cfg.Model, cfg.Voice, "你只负责朗读。", nil))
	if err != nil {
		return nil, err
	}
	defer session.Close(context.Background())

	if _, err := session.Recv(ctx); err != nil {
		return nil, err
	}
	if err := session.SendSpeechText(ctx, text); err != nil {
		return nil, err
	}

	var pcm24k []byte
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
		event, err := session.Recv(recvCtx)
		recvCancel()
		if err != nil {
			if strings.Contains(err.Error(), "deadline") {
				break
			}
			return nil, err
		}
		if len(event.Audio) > 0 {
			pcm24k = append(pcm24k, event.Audio...)
		}
		if event.Type == EventOutputAudioDone || event.Type == EventResponseDone {
			break
		}
		if event.Type == EventError {
			return nil, fmt.Errorf("tts error: %s", event.ErrorMessage)
		}
	}
	if len(pcm24k) == 0 {
		return nil, fmt.Errorf("tts returned no audio")
	}
	return downsample24kTo16k(pcm24k), nil
}

func downsample24kTo16k(pcm24k []byte) []byte {
	// s16le mono: keep 2 of every 3 samples (24k → 16k).
	if len(pcm24k) < 6 {
		return nil
	}
	out := make([]byte, 0, len(pcm24k)*2/3)
	for i := 0; i+5 < len(pcm24k); i += 6 {
		out = append(out, pcm24k[i], pcm24k[i+1], pcm24k[i+2], pcm24k[i+3])
	}
	return out
}

func sendSilence(ctx context.Context, session *Session) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	silence := make([]byte, pcm16kChunkBytes)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = session.SendAudio(ctx, silence)
		}
	}
}

func chunkAudio(data []byte, size int) [][]byte {
	if size <= 0 || len(data) == 0 {
		return nil
	}
	chunks := make([][]byte, 0, (len(data)+size-1)/size)
	for len(data) > 0 {
		n := size
		if n > len(data) {
			n = len(data)
		}
		chunks = append(chunks, data[:n])
		data = data[n:]
	}
	return chunks
}

func trimRunes(value string, max int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

// CLIIssueExecutor creates a real Multica issue for Spike acceptance evidence.
type CLIIssueExecutor struct {
	t       *testing.T
	Calls   []string
	IssueID string
}

func (e *CLIIssueExecutor) Delegate(ctx context.Context, request string) (string, error) {
	request = strings.TrimSpace(request)
	e.Calls = append(e.Calls, request)

	cmd := exec.CommandContext(ctx, "multica", "issue", "create",
		"--title", "LRM-945 Spike FC 验收",
		"--status", "backlog",
		"--priority", "low",
		"--parent", "0019f494-c4ab-4046-9332-7684c8dae06e",
		"--allow-duplicate",
		"--description-stdin",
		"--output", "json",
	)
	cmd.Stdin = strings.NewReader(
		"## Spike live FC\n\n" +
			"自动创建于豆包 Duplex Function Call → Multica 通联验收。\n\n" +
			"delegate request:\n" + request + "\n",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("multica issue create: %w\n%s", err, redactSecrets(string(out)))
	}
	var created struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return "", fmt.Errorf("parse issue create json: %w", err)
	}
	e.IssueID = created.ID
	if created.Identifier != "" {
		e.t.Logf("created issue %s (%s)", created.Identifier, created.ID)
		return "已经在 Multica 创建 issue " + created.Identifier + "，请在看板查看。", nil
	}
	if created.ID != "" {
		return "已经在 Multica 创建任务，请在看板查看。", nil
	}
	return "", fmt.Errorf("multica issue create returned empty id")
}

func redactSecrets(value string) string {
	key := strings.TrimSpace(os.Getenv(EnvAPIKey))
	if key == "" {
		return value
	}
	return strings.ReplaceAll(value, key, "<redacted>")
}
