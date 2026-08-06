package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/turntransport"
)

func TestAgentRuntimeTurnCoordinatorBindsCanonicalD1D2D3Contracts(t *testing.T) {
	root := t.TempDir()
	request := testAgentRuntimeTurnRequest(t, root)
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())

	turn, err := coordinator.Begin(request)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if turn.SlotKey != (agentRuntimeTurnSlotKey{AgentID: request.AgentID, RuntimeID: request.RuntimeID}) {
		t.Fatalf("slot key = %#v", turn.SlotKey)
	}
	if turn.AgentID != request.AgentID || turn.RuntimeID != request.RuntimeID {
		t.Fatalf("provider identity = agent %q runtime %q", turn.AgentID, turn.RuntimeID)
	}
	if turn.PriorSessionID != request.PriorSessionID ||
		turn.RuntimeStateGeneration != request.RuntimeStateGeneration {
		t.Fatalf("canonical runtime state = session %q generation %d",
			turn.PriorSessionID, turn.RuntimeStateGeneration)
	}
	if got, want := turn.Workspace.AgentRoot, agentworkspace.Root(root, request.WorkspaceID, request.AgentID); got != want {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
	if turn.WorkDir != turn.Workspace.AgentRoot {
		t.Fatalf("provider workdir = %q, want %q", turn.WorkDir, turn.Workspace.AgentRoot)
	}
	for _, path := range []string{turn.Workspace.AgentRoot} {
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("prepared directory %s: info=%v err=%v", path, info, statErr)
		}
	}

	if _, exists := turn.StableEnvironment["MULTICA_TASK_ID"]; exists {
		t.Fatal("stable provider environment contains current turn id")
	}
	if _, exists := turn.StableEnvironment["MULTICA_EXECUTION_ID"]; exists {
		t.Fatal("stable provider environment contains current execution id")
	}
	if _, exists := turn.StableEnvironment["MULTICA_AGENT_INBOX_LEASE_TOKEN"]; exists {
		t.Fatal("stable provider environment contains current lease")
	}
	wrapperDir := filepath.Dir(turn.WrapperPath)
	if got := turn.StableEnvironment["PATH"]; got != wrapperDir+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("stable PATH = %q", got)
	}

	envelopePath := filepath.Join(filepath.Dir(wrapperDir), "current-turn.json")
	rawEnvelope, err := os.ReadFile(envelopePath)
	if err != nil {
		t.Fatalf("read current-turn envelope: %v", err)
	}
	var envelope struct {
		TurnID      string            `json:"turn_id"`
		Generation  string            `json:"generation"`
		TokenFile   string            `json:"token_file"`
		Environment map[string]string `json:"environment"`
	}
	if err := json.Unmarshal(rawEnvelope, &envelope); err != nil {
		t.Fatalf("decode current-turn envelope: %v", err)
	}
	if envelope.TurnID != request.TurnID ||
		envelope.Environment["MULTICA_EXECUTION_ID"] != request.TurnID {
		t.Fatalf("bound envelope = %#v", envelope)
	}
	if envelope.Generation != turn.binding.Generation || envelope.TokenFile != turn.binding.TokenFile {
		t.Fatalf("binding = %#v, envelope = %#v", turn.binding, envelope)
	}
	tokenFile := turn.binding.TokenFile
	if raw, readErr := os.ReadFile(tokenFile); readErr != nil || string(raw) != request.Token {
		t.Fatalf("bound token read=%q err=%v", raw, readErr)
	}

	durableFile := filepath.Join(turn.Workspace.AgentRoot, "MEMORY.md")
	if err := os.WriteFile(durableFile, []byte("durable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := turn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := turn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := os.Stat(envelopePath); !os.IsNotExist(err) {
		t.Fatalf("envelope after close error = %v, want not exist", err)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Fatalf("token after close error = %v, want not exist", err)
	}
	if raw, err := os.ReadFile(durableFile); err != nil || string(raw) != "durable" {
		t.Fatalf("durable workspace read=%q err=%v", raw, err)
	}
	if _, err := os.Stat(turn.WrapperPath); err != nil {
		t.Fatalf("fixed wrapper removed at terminal: %v", err)
	}
}

func TestAgentRuntimeTurnCoordinatorRejectsConcurrentSameSlot(t *testing.T) {
	root := t.TempDir()
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	firstRequest := testAgentRuntimeTurnRequest(t, root)
	first, err := coordinator.Begin(firstRequest)
	if err != nil {
		t.Fatalf("Begin(first): %v", err)
	}

	secondRequest := testAgentRuntimeTurnRequest(t, root)
	secondRequest.WorkspaceID = firstRequest.WorkspaceID
	secondRequest.AgentID = firstRequest.AgentID
	secondRequest.RuntimeID = firstRequest.RuntimeID
	secondRequest.Environment["MULTICA_WORKSPACE_ID"] = firstRequest.WorkspaceID
	secondRequest.Environment["MULTICA_AGENT_ID"] = firstRequest.AgentID
	if _, err := coordinator.Begin(secondRequest); !errors.Is(err, errAgentRuntimeTurnBusy) {
		t.Fatalf("Begin(concurrent) error = %v, want busy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}
	second, err := coordinator.Begin(secondRequest)
	if err != nil {
		t.Fatalf("Begin(after close): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}
}

func TestAgentRuntimeTurnCoordinatorStripsLegacyCredentialTransportBeforeSplit(t *testing.T) {
	// Barry #1274 CODE BLOCK: legacy agentEnv may still carry MULTICA_TOKEN_FILE
	// for the CLI wrapper. Begin must strip before D3 SplitEnvironment so
	// production does not fail-closed; provider stable env never sees secrets;
	// Bind still writes request.Token for the wrapper.
	root := t.TempDir()
	request := testAgentRuntimeTurnRequest(t, root)
	request.Environment["MULTICA_TOKEN"] = "mat_must_not_enter_provider"
	request.Environment["MULTICA_TOKEN_FILE"] = "/tmp/must-not-enter-provider"
	request.Environment[turntransport.EnvelopePathEnv] = "/tmp/envelope-must-not-enter"

	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	turn, err := coordinator.Begin(request)
	if err != nil {
		t.Fatalf("Begin with legacy credential keys: %v", err)
	}
	defer turn.Close()

	for _, key := range []string{"MULTICA_TOKEN", "MULTICA_TOKEN_FILE", turntransport.EnvelopePathEnv} {
		if _, ok := turn.StableEnvironment[key]; ok {
			t.Fatalf("stable provider env leaked %s", key)
		}
	}
	raw, readErr := os.ReadFile(turn.binding.TokenFile)
	if readErr != nil || string(raw) != request.Token {
		t.Fatalf("Bind token file = %q err=%v, want request.Token", raw, readErr)
	}
}

func TestAgentRuntimeTurnCoordinatorRejectsInvalidRequestWithoutOccupyingSlot(t *testing.T) {
	root := t.TempDir()
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	request := testAgentRuntimeTurnRequest(t, root)
	request.Token = ""
	if _, err := coordinator.Begin(request); err == nil || !strings.Contains(err.Error(), "turn token is required") {
		t.Fatalf("Begin(empty token) error = %v", err)
	}

	request.Token = "token-retry"
	turn, err := coordinator.Begin(request)
	if err != nil {
		t.Fatalf("Begin(retry): %v", err)
	}
	if err := turn.Close(); err != nil {
		t.Fatalf("Close(retry): %v", err)
	}
}

func TestAgentRuntimeTurnCoordinatorFailedPrepareReleasesSlot(t *testing.T) {
	root := t.TempDir()
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	request := testAgentRuntimeTurnRequest(t, root)
	agentRoot := agentworkspace.Root(root, request.WorkspaceID, request.AgentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	blockingRuntimePath := filepath.Join(agentRoot, "runtime")
	if err := os.WriteFile(blockingRuntimePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.Begin(request); err == nil ||
		!strings.Contains(err.Error(), "prepare stable agent CLI transport") {
		t.Fatalf("Begin(blocked transport) error = %v", err)
	}
	if err := os.Remove(blockingRuntimePath); err != nil {
		t.Fatal(err)
	}
	turn, err := coordinator.Begin(request)
	if err != nil {
		t.Fatalf("Begin(retry): %v", err)
	}
	if err := turn.Close(); err != nil {
		t.Fatalf("Close(retry): %v", err)
	}
}

func TestAgentRuntimeTurnCoordinatorConcurrentBeginHasSingleWinner(t *testing.T) {
	root := t.TempDir()
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	base := testAgentRuntimeTurnRequest(t, root)

	const attempts = 16
	start := make(chan struct{})
	var winners atomic.Int64
	var unexpected atomic.Value
	var winnerMu sync.Mutex
	var winner *agentRuntimeTurn
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := base
			request.TurnID = uuid.NewString()
			request.Environment = cloneEnvironment(base.Environment)
			request.Environment["MULTICA_EXECUTION_ID"] = request.TurnID
			request.Environment["MULTICA_RUN_ID"] = request.TurnID
			got, err := coordinator.Begin(request)
			switch {
			case err == nil:
				winners.Add(1)
				winnerMu.Lock()
				winner = got
				winnerMu.Unlock()
			case errors.Is(err, errAgentRuntimeTurnBusy):
			default:
				unexpected.Store(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if err, _ := unexpected.Load().(error); err != nil {
		t.Fatalf("unexpected Begin error: %v", err)
	}
	if got := winners.Load(); got != 1 {
		t.Fatalf("successful Begin count = %d, want 1", got)
	}
	winnerMu.Lock()
	defer winnerMu.Unlock()
	if winner == nil {
		t.Fatal("missing winning turn")
	}
	if err := winner.Close(); err != nil {
		t.Fatalf("Close(winner): %v", err)
	}
}

func TestD4AgentRuntimeTurnSeamIsActivatedForD6(t *testing.T) {
	entryRaw, err := os.ReadFile("canonical_chat_entry.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entryRaw), ".agentRuntimeTurns.Begin(") {
		t.Fatal("D6-1b must call agentRuntimeTurns.Begin from the production chat entry")
	}
}

func TestPrependPathKeepsFixedWrapperFirstWithoutDuplicates(t *testing.T) {
	separator := string(os.PathListSeparator)
	if got, want := prependPath("/managed/bin", "/usr/bin"+separator+"/managed/bin"+separator+"/bin"),
		"/managed/bin"+separator+"/usr/bin"+separator+"/bin"; got != want {
		t.Fatalf("prependPath = %q, want %q", got, want)
	}
}

func testAgentRuntimeTurnRequest(t *testing.T, root string) agentRuntimeTurnRequest {
	t.Helper()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	turnID := uuid.NewString()
	return agentRuntimeTurnRequest{
		WorkspaceID:            workspaceID,
		AgentID:                agentID,
		RuntimeID:              runtimeID,
		TurnID:                 turnID,
		PriorSessionID:         "session-a",
		RuntimeStateGeneration: 7,
		MulticaBinary:          filepath.Join(root, "bin", "multica"),
		Token:                  "token-a",
		Environment: map[string]string{
			"MULTICA_SERVER_URL":                     "https://example.test",
			"MULTICA_WORKSPACE_ID":                   workspaceID,
			"MULTICA_AGENT_ID":                       agentID,
			"MULTICA_AGENT_NAME":                     "agent-a",
			"MULTICA_EXECUTION_ID":                   turnID,
			"MULTICA_RUN_ID":                         turnID,
			"MULTICA_QUICK_CREATE_SOURCE_MESSAGE_ID": "message-a",
			"PATH":                                   "/usr/bin",
		},
	}
}

func agentRuntimeTurnTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAgentRuntimeTurnsUseDistinctAgentWorkdirs(t *testing.T) {
	root := t.TempDir()
	coordinator := newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger())
	// Two Agents in the same workspace must retain distinct canonical cwd paths.
	requestA := testAgentRuntimeTurnRequest(t, root)
	requestB := testAgentRuntimeTurnRequest(t, root)
	requestB.WorkspaceID = requestA.WorkspaceID
	requestB.Environment["MULTICA_WORKSPACE_ID"] = requestA.WorkspaceID
	turnA, err := coordinator.Begin(requestA)
	if err != nil {
		t.Fatalf("Begin(agent A): %v", err)
	}
	defer turnA.Close()
	turnB, err := coordinator.Begin(requestB)
	if err != nil {
		t.Fatalf("Begin(agent B): %v", err)
	}
	defer turnB.Close()

	wantA := agentworkspace.Root(root, requestA.WorkspaceID, requestA.AgentID)
	if turnA.WorkDir != wantA {
		t.Fatalf("agent A workdir = %q, want %q", turnA.WorkDir, wantA)
	}
	wantB := agentworkspace.Root(root, requestB.WorkspaceID, requestB.AgentID)
	if turnB.WorkDir != wantB {
		t.Fatalf("agent B workdir = %q, want %q", turnB.WorkDir, wantB)
	}
	if turnA.WorkDir == turnB.WorkDir {
		t.Fatalf("different Agents unexpectedly share cwd %q", turnA.WorkDir)
	}
}
