package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type capturedAgentRestartNotifier struct {
	mu          sync.Mutex
	workspaceID string
	computerID  string
	eventType   string
	commandID   string
	payload     any
}

type rejectingAgentRestartNotifier struct{}

func (rejectingAgentRestartNotifier) NotifyAgentRestartCommand(string, string, string, string, any) bool {
	return false
}

func (n *capturedAgentRestartNotifier) NotifyAgentRestartCommand(workspaceID, computerID, eventType, commandID string, payload any) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.workspaceID = workspaceID
	n.computerID = computerID
	n.eventType = eventType
	n.commandID = commandID
	n.payload = payload
	return true
}

func (n *capturedAgentRestartNotifier) snapshot() (string, string, string, string, any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.workspaceID, n.computerID, n.eventType, n.commandID, n.payload
}

func TestAgentRestartContractPure(t *testing.T) {
	for _, test := range []struct {
		mode   AgentRestartMode
		action agentRestartStorageKind
	}{
		{mode: agentRestartModeRestart, action: agentRestartStorageRestart},
		{mode: agentRestartModeSession, action: agentRestartStorageSession},
		{mode: agentRestartModeFull, action: agentRestartStorageFull},
	} {
		action, ok := agentRestartStorageForMode(test.mode)
		if !ok || action != test.action || agentRestartModeForStorage(action) != test.mode {
			t.Fatalf("Raft mode %q mapped to %q, want %q", test.mode, action, test.action)
		}
	}
	if _, ok := agentRestartStorageForMode("delete_agent"); ok {
		t.Fatal("unknown mode was accepted")
	}
	if !workspaceRunnerAgentProcessCapabilityPresent([]string{"other", "workspace_runner_agent_process_v1"}) {
		t.Fatal("Workspace Runner Agent process capability was not detected")
	}
	if workspaceRunnerAgentProcessCapabilityPresent([]string{"other"}) {
		t.Fatal("missing Workspace Runner Agent process capability was accepted")
	}
	if !workspaceRunnerResetCapabilityPresent([]string{"other", "workspace_runner_agent_reset_workspace_v1"}) {
		t.Fatal("Workspace Runner reset capability was not detected")
	}
}

func TestAgentRestartPreflightExposesThreeRaftModes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	insertRunningAgentRestartExecution(t, agentID, runtimeID)

	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/reset", nil),
		"id", agentID,
	)
	testHandler.GetAgentRestart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentRestartPreflight
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	// Raft exposes exactly three immediate modes. Busy execution is handled by
	// the stop fence, not by a fourth scheduling mode.
	restart := response.Actions[agentRestartModeRestart]
	if !restart.Supported || restart.DisabledReason != "" {
		t.Fatalf("restart preflight = %+v", restart)
	}
	resetSession := response.Actions[agentRestartModeSession]
	if !resetSession.Supported || resetSession.DisabledReason != "" {
		t.Fatalf("session preflight = %+v", resetSession)
	}
	full := response.Actions[agentRestartModeFull]
	if !full.Supported || full.DisabledReason != "" {
		t.Fatalf("full reset preflight = %+v", full)
	}
	// Fixture provider is "restart-test" — not ForceKillable → false.
	if response.ProviderCapabilities.ForceRestart {
		t.Fatalf("provider_capabilities.force_restart=%v, want false for non-ForceKillable fixture provider", response.ProviderCapabilities.ForceRestart)
	}
}

func TestAgentRestartPreflightRejectsLegacyHeartbeatOnlyDaemon(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime
		SET metadata = jsonb_build_object('capabilities', '["retired_composite_capability"]'::jsonb)
		WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("downgrade runtime capabilities: %v", err)
	}
	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/reset", nil),
		"id", agentID,
	)
	testHandler.GetAgentRestart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentRestartPreflight
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	for _, mode := range []AgentRestartMode{agentRestartModeRestart, agentRestartModeSession, agentRestartModeFull} {
		state := response.Actions[mode]
		if state.Supported || state.DisabledReason != "unsupported_runtime_capability" {
			t.Fatalf("%s preflight = %+v", mode, state)
		}
	}

	create := invokeCreateAgentRestart(t, agentID, "81818181-8181-4181-8181-818181818181", agentRestartStorageRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "unsupported_runtime_capability") {
		t.Fatalf("legacy daemon restart create status=%d body=%s", create.Code, create.Body.String())
	}
}

func TestAgentRestartPreflightProviderCapabilitiesFollowProvider(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// cursor is in forceRestartResidentConstructors and implements ForceKill.
	agentID, _ := createAgentRestartFixtureWithProvider(t, true, "cursor")
	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/reset", nil),
		"id", agentID,
	)
	testHandler.GetAgentRestart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentRestartPreflight
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	caps := response.ProviderCapabilities
	if !caps.ForceRestart || !caps.CanonicalResident || !caps.CustomModelID || !caps.ModelSelection {
		t.Fatalf("provider_capabilities=%+v, want force_restart+canonical_resident+custom_model_id+model_selection for cursor", caps)
	}
	if caps.NeedsInlineSystemPrompt {
		t.Fatalf("cursor must not need inline system prompt, got %+v", caps)
	}
}

// TestAgentRestartCreateIsIdempotentAndForceRestartsBusyAgent pins #62/#112:
// plain restart on a busy agent is immediate/running (idempotent create).
// All three actions force-immediate when busy — see
// TestAgentRestartAllActionsForceImmediateWhenBusy.
func TestAgentRestartCreateIsIdempotentAndForceRestartsBusyAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	insertRunningAgentRestartExecution(t, agentID, runtimeID)

	// #112: full_reset on busy is also immediate (no longer agent_active reject).
	// Covered in TestAgentRestartAllActionsForceImmediateWhenBusy.

	key := uuid.NewString()
	first := invokeCreateAgentRestart(t, agentID, key, agentRestartStorageRestart)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	var firstOperation AgentRestartOperation
	if err := json.Unmarshal(first.Body.Bytes(), &firstOperation); err != nil {
		t.Fatalf("decode first operation: %v", err)
	}
	if firstOperation.Status != agentRestartRunning || firstOperation.StartedAt == nil {
		t.Fatalf("restart on busy agent = %+v, want immediate/running (busy must not block plain restart)", firstOperation)
	}

	replay := invokeCreateAgentRestart(t, agentID, key, agentRestartStorageRestart)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayOperation AgentRestartOperation
	if err := json.Unmarshal(replay.Body.Bytes(), &replayOperation); err != nil {
		t.Fatalf("decode replay operation: %v", err)
	}
	if replayOperation.ID != firstOperation.ID {
		t.Fatalf("replay operation id=%s want=%s", replayOperation.ID, firstOperation.ID)
	}

	mismatch := invokeCreateAgentRestart(t, agentID, key, agentRestartStorageSession)
	if mismatch.Code != http.StatusConflict || !containsResponseBody(mismatch, "another operation") {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_restart_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count restart operations: %v", err)
	}
	if count != 1 {
		t.Fatalf("operation count=%d want=1", count)
	}
}

// TestAgentRestartAllModesStopAnActiveRun pins the three-mode contract: there
// is no separate scheduling mode; each request begins with the stop fence.
func TestAgentRestartAllActionsForceImmediateWhenBusy(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, action := range []agentRestartStorageKind{
		agentRestartStorageRestart,
		agentRestartStorageSession,
		agentRestartStorageFull,
	} {
		t.Run(string(action), func(t *testing.T) {
			agentID, runtimeID := createAgentRestartFixture(t, true)
			insertRunningAgentRestartExecution(t, agentID, runtimeID)

			rec := invokeCreateAgentRestart(t, agentID, uuid.NewString(), action)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
			}
			var operation AgentRestartOperation
			if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
				t.Fatalf("decode operation: %v", err)
			}
			if operation.Status != agentRestartRunning || operation.StartedAt == nil {
				t.Fatalf("%s on busy agent = %+v, want running", action, operation)
			}
		})
	}
}

func TestAgentRestartConcurrentDuplicateRequestReturnsOneOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	key := uuid.NewString()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			responses <- invokeCreateAgentRestart(
				t, agentID, key, agentRestartStorageSession,
			)
		}()
	}
	close(start)
	workers.Wait()
	close(responses)

	var operationID string
	for response := range responses {
		if response.Code != http.StatusAccepted {
			t.Fatalf("concurrent create status=%d body=%s", response.Code, response.Body.String())
		}
		var operation AgentRestartOperation
		if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
			t.Fatalf("decode concurrent operation: %v", err)
		}
		if operationID == "" {
			operationID = operation.ID
		} else if operation.ID != operationID {
			t.Fatalf("concurrent operation id=%s want=%s", operation.ID, operationID)
		}
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_restart_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count restart operations: %v", err)
	}
	if count != 1 {
		t.Fatalf("operation count=%d want=1", count)
	}
}

func TestAgentRestartIdleActionsStartImmediately(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, action := range []agentRestartStorageKind{
		agentRestartStorageRestart,
		agentRestartStorageSession,
		agentRestartStorageFull,
	} {
		t.Run(string(action), func(t *testing.T) {
			agentID, _ := createAgentRestartFixture(t, true)
			rec := invokeCreateAgentRestart(t, agentID, uuid.NewString(), action)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
			}
			var operation AgentRestartOperation
			if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
				t.Fatalf("decode operation: %v", err)
			}
			if operation.Status != agentRestartRunning || operation.StartedAt == nil {
				t.Fatalf("idle operation = %+v", operation)
			}
		})
	}
}

// TestAgentRestartCreateDispatchesImmediateOperationToDaemon pins the
// public seam: even an idle restart begins with Raft's discrete agent:stop
// fence, never a product-level composite restart payload.
func TestAgentRestartCreateDispatchesImmediateOperationToDaemon(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })
	rec := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	workspaceID, computerID, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("command payload = %T, want Agent stop", payload)
	}
	if eventType != protocol.EventDaemonAgentStop || commandID != operation.ID {
		t.Fatalf("command event=%q command=%q payload=%+v", eventType, commandID, stop)
	}
	if stop.AgentID != agentID || stop.LaunchID == "" {
		t.Fatalf("restart stop fence = %+v", stop)
	}
	if workspaceID != testWorkspaceID || computerID != "agent-restart-test-daemon" {
		t.Fatalf("command route = workspace %q computer %q", workspaceID, computerID)
	}
}

func TestAgentRestartRestartAdvancesStopThenStartThenActive(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	const providerSessionID = "provider-session-before-restart"
	var oldLaunchID string
	if err := testPool.QueryRow(context.Background(), `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&oldLaunchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'agent-restart-test-daemon', 'runner-instance', $4, 'active')
	`, testWorkspaceID, agentID, runtimeID, oldLaunchID); err != nil {
		t.Fatal(err)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	runnerHandler := *testHandler
	runnerHandler.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"agent-restart-test-daemon/" + testWorkspaceID + "/runner-instance": true,
	}}
	sessionFrame, err := json.Marshal(protocol.AgentSessionPayload{
		AgentID: agentID, LaunchID: oldLaunchID, ProviderSessionID: providerSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runnerHandler.HandleWorkspaceRunnerFrame(context.Background(), identity, "runner-instance", protocol.EventAgentSession, sessionFrame); err != nil {
		t.Fatalf("record live provider session: %v", err)
	}
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID || stop.AgentID != agentID || stop.LaunchID != oldLaunchID {
		t.Fatalf("first restart command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}

	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: uuid.NewString(), Status: protocol.AgentStatusInactive}); err != nil || handled {
		t.Fatalf("stale inactive advanced stop fence: handled=%v err=%v", handled, err)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: oldLaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance inactive handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	start, ok := payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.AgentID != agentID || start.RuntimeID != runtimeID || start.LaunchID == oldLaunchID || start.Config.SessionID != providerSessionID {
		t.Fatalf("replacement restart command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	desired, err := testHandler.loadRunnerDesiredLaunches(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	reconnectActions := reduceRunnerLaunches(desired, nil)
	if len(reconnectActions) != 1 {
		t.Fatalf("restart reconnect actions=%+v, want one start", reconnectActions)
	}
	reconnectStart, ok := reconnectActions[0].payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || reconnectActions[0].eventType != protocol.EventDaemonAgentStart || reconnectStart.LaunchID != start.LaunchID || reconnectStart.StartDispatchID != operation.ID || reconnectStart.Config.SessionID != providerSessionID {
		t.Fatalf("restart reconnect start=%+v, want persisted session %q", reconnectActions, providerSessionID)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance active handled=%v err=%v", handled, err)
	}
	finished, err := getAgentRestartOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentRestartSucceeded || finished.FinishedAt == nil {
		t.Fatalf("finished restart = %+v err=%v", finished, err)
	}
}

func TestAgentRestartSessionClearsSessionThenStartsFresh(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	var chatSessionID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, session_id, runtime_id)
		VALUES ($1, $2, $3, 'restart session reset', 'provider-session-before-reset', $4)
		RETURNING id::text
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, chat_session_id, status, priority, session_id)
		VALUES ($1, $2, $3, 'completed', 0, 'provider-session-before-reset')
	`, agentID, runtimeID, chatSessionID); err != nil {
		t.Fatal(err)
	}
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageSession)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("session reset first payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance session stop handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	start, ok := payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.Config.SessionID != "" {
		t.Fatalf("fresh session start event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	var chatProviderSessionID, inboxProviderSessionID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT chat.session_id, inbox.session_id
		FROM chat_session chat
		JOIN agent_inbox_event inbox ON inbox.chat_session_id = chat.id
		WHERE chat.id = $1
	`, chatSessionID).Scan(&chatProviderSessionID, &inboxProviderSessionID); err != nil {
		t.Fatal(err)
	}
	if chatProviderSessionID != nil || inboxProviderSessionID != nil {
		t.Fatalf("provider sessions after session reset = chat:%v inbox:%v, want both NULL", chatProviderSessionID, inboxProviderSessionID)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance session active handled=%v err=%v", handled, err)
	}
	finished, err := getAgentRestartOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentRestartSucceeded {
		t.Fatalf("finished session reset = %+v err=%v", finished, err)
	}
}

func TestAgentRestartFullResetWaitsForWorkspaceResultBeforeFreshStart(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageFull)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID {
		t.Fatalf("full reset stop event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance full reset stop handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	reset, ok := payload.(protocol.WorkspaceRunnerAgentResetWorkspacePayload)
	if !ok || eventType != protocol.EventDaemonAgentResetWorkspace || commandID != operation.ID || reset.OperationID != operation.ID || reset.AgentID != agentID {
		t.Fatalf("full reset command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if err := testHandler.recordAgentWorkspaceResetResult(context.Background(), identity, protocol.WorkspaceRunnerAgentResetWorkspaceResultPayload{
		OperationID: operation.ID, AgentID: agentID, Status: protocol.AgentResetWorkspaceSucceeded,
	}); err != nil {
		t.Fatalf("record workspace reset result: %v", err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	start, ok := payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.Config.SessionID != "" {
		t.Fatalf("fresh replacement command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance full reset active handled=%v err=%v", handled, err)
	}
	finished, err := getAgentRestartOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentRestartSucceeded {
		t.Fatalf("finished full reset = %+v err=%v", finished, err)
	}
}

func TestWorkspaceRunnerActiveStatusCompletesRestartOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	identity := daemonws.ClientIdentity{
		DaemonID:    "agent-restart-test-daemon",
		WorkspaceID: testWorkspaceID,
		RuntimeIDs:  []string{runtimeID},
	}
	_, _, _, _, firstPayload := notifier.snapshot()
	stop, ok := firstPayload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first restart payload=%T, want stop", firstPayload)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	var launchID string
	if err := testPool.QueryRow(context.Background(), `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&launchID); err != nil {
		t.Fatal(err)
	}
	status := protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, status); err != nil {
			t.Fatalf("record active status attempt %d: %v", attempt+1, err)
		}
	}
	finished, err := getAgentRestartOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentRestartSucceeded || finished.FinishedAt == nil {
		t.Fatalf("finished operation = %+v, %v", finished, err)
	}
}

func TestAgentRestartUnavailableRunnerRemainsResumableUntilReady(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = rejectingAgentRestartNotifier{}
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageFull)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Status != agentRestartRunning || operation.Step != agentRestartStepStopping || operation.FinishedAt != nil {
		t.Fatalf("unavailable Runner operation should remain resumable = %+v", operation)
	}
	notifier := &capturedAgentRestartNotifier{}
	testHandler.AgentRestartNotifier = notifier
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID}
	if err := testHandler.resumeAgentRestartOperations(context.Background(), identity); err != nil {
		t.Fatalf("resume restart operation on Runner ready: %v", err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID || stop.LaunchID == "" {
		t.Fatalf("resumed command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
}

func TestAgentRestartReconnectRedrivesWorkspaceResetWithSameOperationID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageFull)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("full reset first payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance full reset stop handled=%v err=%v", handled, err)
	}

	redriven := &capturedAgentRestartNotifier{}
	testHandler.AgentRestartNotifier = redriven
	if err := testHandler.resumeAgentRestartOperations(context.Background(), identity); err != nil {
		t.Fatalf("redrive workspace reset after reconnect: %v", err)
	}
	_, _, eventType, commandID, payload := redriven.snapshot()
	reset, ok := payload.(protocol.WorkspaceRunnerAgentResetWorkspacePayload)
	if !ok || eventType != protocol.EventDaemonAgentResetWorkspace || commandID != operation.ID || reset.OperationID != operation.ID || reset.AgentID != agentID {
		t.Fatalf("redriven reset event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
}

func TestAgentRestartReconnectReusesPersistedFreshStartFence(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageSession)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance session stop handled=%v err=%v", handled, err)
	}
	_, _, _, _, payload = notifier.snapshot()
	firstStart, ok := payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok {
		t.Fatalf("session replacement payload=%T, want start", payload)
	}

	desired, err := testHandler.loadRunnerDesiredLaunches(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	actions := reduceRunnerLaunches(desired, nil)
	if len(actions) != 1 || actions[0].eventType != protocol.EventDaemonAgentStart {
		t.Fatalf("reconnect actions=%+v, want one start", actions)
	}
	redriven, ok := actions[0].payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || redriven.LaunchID != firstStart.LaunchID || redriven.StartDispatchID != operation.ID || redriven.Config.SessionID != "" {
		t.Fatalf("redriven start=%+v, first=%+v operation=%s", actions[0].payload, firstStart, operation.ID)
	}
}

func TestAgentRestartCommandTimeoutFailsBusinessRecordOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_restart_operation SET started_at = now() - interval '3 minutes' WHERE id = $1
	`, operation.ID); err != nil {
		t.Fatal(err)
	}
	count, err := SweepTimedOutAgentRestartOperations(context.Background(), testPool)
	if err != nil || count < 1 {
		t.Fatalf("timeout sweep count=%d err=%v", count, err)
	}
	got, err := getAgentRestartOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || got == nil || got.Status != agentRestartFailed || got.Step != "timeout" || got.FinishedAt == nil {
		t.Fatalf("timed-out operation = %+v, %v", got, err)
	}
}

func TestTimedOutResetStartKeepsExplicitFreshSessionOnReconcile(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageSession)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first reset payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_restart_operation SET started_at = now() - interval '3 minutes' WHERE id = $1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepTimedOutAgentRestartOperations(context.Background(), testPool); err != nil {
		t.Fatal(err)
	}
	desired, err := testHandler.loadRunnerDesiredLaunches(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, launch := range desired {
		if launch.agentID == agentID {
			if launch.sessionID != "" || launch.startDispatchID != operation.ID {
				t.Fatalf("timed-out reset desired launch=%+v, want persisted explicit fresh session", launch)
			}
			return
		}
	}
	t.Fatal("timed-out reset desired launch not found")
}

func TestTimedOutWorkspaceResetDoesNotResumePreResetDesiredLaunch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	notifier := &capturedAgentRestartNotifier{}
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageFull)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentRestartOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first full-reset payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_restart_operation SET started_at = now() - interval '3 minutes' WHERE id = $1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepTimedOutAgentRestartOperations(context.Background(), testPool); err != nil {
		t.Fatal(err)
	}
	desired, err := testHandler.loadRunnerDesiredLaunches(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, launch := range desired {
		if launch.agentID == agentID {
			t.Fatalf("timed-out workspace reset resumed pre-reset desired launch: %+v", launch)
		}
	}
}

func TestAgentRestartRunningOperationOverlaysExistingAgentHealth(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageSession)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/health", nil),
		"id", agentID,
	)
	testHandler.GetAgentHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.Summary.State != "restarting" ||
		response.Summary.ReasonCode != "agent_restart_session" {
		t.Fatalf("health summary = %+v", response.Summary)
	}
}

func TestAgentRestartRejectsPlainMemberAndIncapableRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, false)
	memberID := createAgentRestartMember(t, "member")

	memberPreflight := httptest.NewRecorder()
	memberReq := withURLParam(
		newRequestAs(memberID, http.MethodGet, "/api/agents/"+agentID+"/reset", nil),
		"id", agentID,
	)
	testHandler.GetAgentRestart(memberPreflight, memberReq)
	if memberPreflight.Code != http.StatusForbidden {
		t.Fatalf("member preflight status=%d body=%s", memberPreflight.Code, memberPreflight.Body.String())
	}

	ownerPreflight := httptest.NewRecorder()
	ownerReq := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/reset", nil),
		"id", agentID,
	)
	testHandler.GetAgentRestart(ownerPreflight, ownerReq)
	if ownerPreflight.Code != http.StatusOK {
		t.Fatalf("owner preflight status=%d body=%s", ownerPreflight.Code, ownerPreflight.Body.String())
	}
	var response AgentRestartPreflight
	if err := json.Unmarshal(ownerPreflight.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode owner preflight: %v", err)
	}
	for action, got := range response.Actions {
		if got.Supported || got.DisabledReason != "unsupported_runtime_capability" {
			t.Fatalf("%s unsupported preflight=%+v", action, got)
		}
	}

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "unsupported_runtime_capability") {
		t.Fatalf("incapable create status=%d body=%s", create.Code, create.Body.String())
	}
}

// TestAgentRestartCreateRejectsStaleHeartbeatWithoutCreatingAnOperation pins
// task #52's primary defense (Parker: "reject up-front" is the main path,
// the dispatch timeout is only the backstop for the narrow race after this
// check passes): a runtime whose last_seen_at is stale must be refused at
// create time — not accepted and left to time out two minutes later. No
// operation row means no window where the agent's health shows "restarting"
// for a machine we already know is unreachable.
func TestAgentRestartCreateRejectsStaleHeartbeatWithoutCreatingAnOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET last_seen_at = now() - interval '10 minutes' WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("stale last_seen_at: %v", err)
	}

	create := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "agent_runtime_offline") {
		t.Fatalf("stale-heartbeat create status=%d body=%s, want 409 agent_runtime_offline", create.Code, create.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_restart_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected zero operation rows for a rejected create, got %d", count)
	}
}

func TestAgentRestartNoRuntimeIsUnsupported(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, supported, reason, err := testHandler.agentRestartRuntimeSupport(
		context.Background(), db.Agent{},
	)
	if err != nil {
		t.Fatalf("missing-runtime support check: %v", err)
	}
	if supported || reason != "agent_runtime_missing" {
		t.Fatalf("missing-runtime support=(%t, %q)", supported, reason)
	}
}

func createAgentRestartFixture(t *testing.T, capable bool) (agentID, runtimeID string) {
	t.Helper()
	return createAgentRestartFixtureWithProvider(t, capable, "restart-test")
}

func createAgentRestartFixtureWithProvider(t *testing.T, capable bool, provider string) (agentID, runtimeID string) {
	t.Helper()
	capabilities := `[]`
	if capable {
		capabilities = `["workspace_runner_agent_process_v1","workspace_runner_agent_reset_workspace_v1"]`
	}
	ctx := context.Background()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at, daemon_id
		)
		VALUES (
			$1, $2, 'local', $3, 'online',
			'', jsonb_build_object('capabilities', $4::jsonb), $5, 'private', now(), 'agent-restart-test-daemon'
		)
		RETURNING id
	`, testWorkspaceID, "restart-runtime-"+randomID(), provider, capabilities, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create restart runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, 'Restart test', 'local', $3, 1, $4, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "restart-agent-"+randomID(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create restart agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_restart_operation WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func insertRunningAgentRestartExecution(t *testing.T, agentID, runtimeID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source,
			workspace_id, runtime_id, agent_id, status
		)
		VALUES ($1, 'inbox', $2, 'chat', $3, $4, $5, 'running')
	`, uuid.NewString(), uuid.NewString(), testWorkspaceID, runtimeID, agentID); err != nil {
		t.Fatalf("create running execution: %v", err)
	}
}

func createAgentRestartMember(t *testing.T, role string) string {
	t.Helper()
	userID := uuid.NewString()
	email := "agent-restart-" + userID + "@multica.test"
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, 'Agent restart member', $2)
	`, userID, email); err != nil {
		t.Fatalf("create restart member: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("add restart member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func invokeCreateAgentRestart(t *testing.T, agentID, idempotencyKey string, action agentRestartStorageKind) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/"+agentID+"/reset", map[string]any{
		"mode": agentRestartModeForStorage(action),
	})
	req.Header.Set("Idempotency-Key", idempotencyKey)
	req = withURLParam(req, "id", agentID)
	testHandler.ResetAgent(rec, req)
	return rec
}

func containsResponseBody(rec *httptest.ResponseRecorder, want string) bool {
	return strings.Contains(rec.Body.String(), want)
}
