package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakePiRPCProcessScript() string {
	return `#!/bin/sh
	printf x >> "$PI_RPC_TEST_STARTS"
	turn=0
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
	      printf '{"id":"multica-stats","type":"response","command":"get_session_stats","success":true,"data":{"tokens":{"input":349000,"output":10000,"cacheRead":2600000,"total":2959000},"cost":3.348,"contextUsage":{"tokens":272000,"contextWindow":607000,"percent":44.8}}}\n'
	      ;;
	    *'"type":"get_state"'*)
	      printf '{"id":"multica-state","type":"response","command":"get_state","success":true,"data":{"autoCompactionEnabled":true}}\n'
	      ;;
	  esac
	done
	`
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
	if err := <-acceptance.Done; err != nil {
		t.Fatalf("native Message turn completion: %v", err)
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
	<-session.Result

	stats, err := b.RuntimeStats(context.Background())
	if err != nil {
		t.Fatalf("RuntimeStats: %v", err)
	}
	if stats == nil || stats.ContextPercent == nil || *stats.ContextPercent != 44.8 {
		t.Fatalf("RuntimeStats = %+v", stats)
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
