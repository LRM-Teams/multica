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
	if !workspaceDaemonAgentProcessCapabilityPresent([]string{"other", "workspace_daemon_agent_process_v1"}) {
		t.Fatal("Workspace Runner Agent process capability was not detected")
	}
	if workspaceDaemonAgentProcessCapabilityPresent([]string{"other"}) {
		t.Fatal("missing Workspace Runner Agent process capability was accepted")
	}
	if !workspaceDaemonResetCapabilityPresent([]string{"other", "workspace_daemon_agent_reset_workspace_v1"}) {
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
func TestAgentRestartCreateForceRestartsBusyAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	insertRunningAgentRestartExecution(t, agentID, runtimeID)

	first := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
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
	second := invokeCreateAgentRestart(t, agentID, uuid.NewString(), agentRestartStorageRestart)
	if second.Code != http.StatusConflict {
		t.Fatalf("second create status=%d body=%s, want conflict", second.Code, second.Body.String())
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
	stop, ok := payload.(protocol.AgentStopPayload)
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
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	runnerHandler := *testHandler
	runnerHandler.runnerObservations = newRunnerObservationStore()
	runnerHandler.runnerActivityCursor = newRunnerActivityCursorStore()
	runnerHandler.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"agent-restart-test-daemon/" + testWorkspaceID + "/runner-instance": true,
	}}
	runnerHandler.observations().putStatus(testWorkspaceID, "agent-restart-test-daemon", "runner-instance", agentID, runtimeID, oldLaunchID, protocol.AgentStatusActive)
	previousObservations := testHandler.runnerObservations
	testHandler.runnerObservations = runnerHandler.runnerObservations
	t.Cleanup(func() { testHandler.runnerObservations = previousObservations })
	sessionFrame, err := json.Marshal(protocol.AgentSessionPayload{
		AgentID: agentID, LaunchID: oldLaunchID, ProviderSessionID: providerSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runnerHandler.HandleWorkspaceDaemonFrame(context.Background(), identity, "runner-instance", protocol.EventAgentSession, sessionFrame); err != nil {
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
	stop, ok := payload.(protocol.AgentStopPayload)
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
	start, ok := payload.(protocol.AgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.AgentID != agentID || start.RuntimeID != runtimeID || start.LaunchID == oldLaunchID || start.Config.SessionID != providerSessionID {
		t.Fatalf("replacement restart command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance active handled=%v err=%v", handled, err)
	}
	if _, ok := currentAgentRestart(t, agentID); ok {
		t.Fatal("finished restart left an in-flight operation")
	}
}

func TestAgentRestartReplacementInactiveRequiresFreshExplicitStart(t *testing.T) {
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
	_, _, _, _, payload := notifier.snapshot()
	stop := payload.(protocol.AgentStopPayload)
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	_, _, _, _, payload = notifier.snapshot()
	failedStart := payload.(protocol.AgentStartPayload)
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: failedStart.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("record replacement failure handled=%v err=%v", handled, err)
	}
	if testHandler.restarts().has(agentID) {
		t.Fatal("failed replacement remained an active restart")
	}
	start := invokeAgentLifecycleAction(t, agentID, "start")
	if start.Code != http.StatusAccepted {
		t.Fatalf("explicit start status=%d body=%s", start.Code, start.Body.String())
	}
	var nextLaunchID, nextDispatchID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT launch_id::text, start_dispatch_id::text
		FROM agent_runner_launch_projection WHERE agent_id = $1
	`, agentID).Scan(&nextLaunchID, &nextDispatchID); err != nil {
		t.Fatal(err)
	}
	if nextLaunchID == failedStart.LaunchID || nextDispatchID == failedStart.StartDispatchID {
		t.Fatalf("explicit Start replayed failed identity: launch=%q dispatch=%q", nextLaunchID, nextDispatchID)
	}
}

func TestAgentManualStopCancelsStartingRestartAndTargetsReplacement(t *testing.T) {
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
	_, _, _, _, payload := notifier.snapshot()
	oldStop := payload.(protocol.AgentStopPayload)
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: oldStop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	_, _, _, _, payload = notifier.snapshot()
	replacement := payload.(protocol.AgentStartPayload)

	stop := invokeAgentLifecycleAction(t, agentID, "stop")
	if stop.Code != http.StatusAccepted {
		t.Fatalf("manual stop status=%d body=%s", stop.Code, stop.Body.String())
	}
	_, _, eventType, _, payload := notifier.snapshot()
	manualStop := payload.(protocol.AgentStopPayload)
	if eventType != protocol.EventDaemonAgentStop || manualStop.LaunchID != replacement.LaunchID {
		t.Fatalf("manual stop targeted %+v, want replacement %q", manualStop, replacement.LaunchID)
	}
	if testHandler.restarts().has(agentID) {
		t.Fatal("manual Stop left restart active")
	}
}

func TestAgentManualStopFailsWhenRunnerIsUnavailable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = rejectingAgentRestartNotifier{}
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	stop := invokeAgentLifecycleAction(t, agentID, "stop")
	if stop.Code != http.StatusConflict || !containsResponseBody(stop, "agent_runtime_offline") {
		t.Fatalf("manual stop status=%d body=%s, want 409 agent_runtime_offline", stop.Code, stop.Body.String())
	}
}

func TestExplicitStartKeepsDesiredLaunchWhenRunnerIsUnavailable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	previous := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = rejectingAgentRestartNotifier{}
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previous })

	start := invokeAgentLifecycleAction(t, agentID, "start")
	if start.Code != http.StatusAccepted {
		t.Fatalf("explicit Start status=%d body=%s", start.Code, start.Body.String())
	}
	var launchID, dispatchID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT launch_id::text, start_dispatch_id::text
		FROM agent_runner_launch_projection WHERE agent_id = $1
	`, agentID).Scan(&launchID, &dispatchID); err != nil {
		t.Fatal(err)
	}
	if launchID == "" || dispatchID == "" {
		t.Fatalf("unavailable Start did not retain desired identities: launch=%q dispatch=%q", launchID, dispatchID)
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
	stop, ok := payload.(protocol.AgentStopPayload)
	if !ok {
		t.Fatalf("session reset first payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance session stop handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	start, ok := payload.(protocol.AgentStartPayload)
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
	if _, ok := currentAgentRestart(t, agentID); ok {
		t.Fatal("finished session reset left an in-flight operation")
	}
}

func TestAgentRestartSessionInactiveCannotClearAfterLifecycleCancellation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentRestartFixture(t, true)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runner_launch_projection
		SET provider_session_id = 'provider-session-kept'
		WHERE agent_id = $1 AND runtime_id = $2
	`, agentID, runtimeID); err != nil {
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
	_, _, _, _, payload := notifier.snapshot()
	stop := payload.(protocol.AgentStopPayload)
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}

	testHandler.restarts().lifecycleMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{
			AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive,
		})
		done <- err
	}()
	testHandler.restarts().finish(agentID)
	testHandler.restarts().lifecycleMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("cancelled session restart status: %v", err)
	}

	var sessionID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(provider_session_id, '')
		FROM agent_runner_launch_projection
		WHERE agent_id = $1 AND runtime_id = $2
	`, agentID, runtimeID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if sessionID != "provider-session-kept" {
		t.Fatalf("cancelled session restart cleared provider session: %q", sessionID)
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
	stop, ok := payload.(protocol.AgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID {
		t.Fatalf("full reset stop event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance full reset stop handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	reset, ok := payload.(protocol.AgentWorkspaceResetPayload)
	if !ok || eventType != protocol.EventDaemonAgentResetWorkspace || commandID != operation.ID || reset.OperationID != operation.ID || reset.AgentID != agentID {
		t.Fatalf("full reset command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if err := testHandler.recordAgentWorkspaceResetResult(context.Background(), identity, protocol.AgentWorkspaceResetResultPayload{
		OperationID: operation.ID, AgentID: agentID, Status: protocol.AgentResetWorkspaceSucceeded,
	}); err != nil {
		t.Fatalf("record workspace reset result: %v", err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	start, ok := payload.(protocol.AgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.Config.SessionID != "" {
		t.Fatalf("fresh replacement command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance full reset active handled=%v err=%v", handled, err)
	}
	if _, ok := currentAgentRestart(t, agentID); ok {
		t.Fatal("finished full reset left an in-flight operation")
	}
}

func TestWorkspaceDaemonActiveStatusCompletesRestartOperation(t *testing.T) {
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
	stop, ok := firstPayload.(protocol.AgentStopPayload)
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
	if _, ok := currentAgentRestart(t, agentID); ok {
		t.Fatal("finished restart left an in-flight operation")
	}
}

func TestAgentRestartUnavailableRunnerDoesNotFabricateCompletion(t *testing.T) {
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
		t.Fatalf("unavailable Runner operation should stay running = %+v", operation)
	}
	notifier := &capturedAgentRestartNotifier{}
	testHandler.AgentRestartNotifier = notifier
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID}
	if err := testHandler.recordWorkspaceDaemonReady(context.Background(), identity, "runner-instance", nil); err != nil {
		t.Fatalf("Runner ready after undelivered restart: %v", err)
	}
	if _, _, eventType, _, payload := notifier.snapshot(); eventType != "" || payload != nil {
		t.Fatalf("ready redrove undelivered restart event=%q payload=%+v", eventType, payload)
	}
}

func TestAgentRestartReadyDoesNotRedriveWorkspaceReset(t *testing.T) {
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
	stop, ok := payload.(protocol.AgentStopPayload)
	if !ok {
		t.Fatalf("full reset first payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance full reset stop handled=%v err=%v", handled, err)
	}

	redriven := &capturedAgentRestartNotifier{}
	testHandler.AgentRestartNotifier = redriven
	if err := testHandler.recordWorkspaceDaemonReady(context.Background(), identity, "runner-instance", nil); err != nil {
		t.Fatalf("Runner ready during workspace reset: %v", err)
	}
	if _, _, eventType, _, payload := redriven.snapshot(); eventType != "" || payload != nil {
		t.Fatalf("ready redrove workspace reset event=%q payload=%+v", eventType, payload)
	}
}

func TestAgentRestartReconcileDoesNotRedriveStart(t *testing.T) {
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
	_, _, _, _, payload := notifier.snapshot()
	stop := payload.(protocol.AgentStopPayload)
	identity := daemonws.ClientIdentity{DaemonID: "agent-restart-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentRestartFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance session stop handled=%v err=%v", handled, err)
	}
	if !testHandler.restarts().has(agentID) {
		t.Fatal("session restart left the in-flight store")
	}
	if !testHandler.restartAgentsOnActiveOperation()[agentID] {
		t.Fatal("in-flight restart was visible to reconcile")
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
		response.Summary.ReasonCode != "agent_restart" {
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
	if _, ok := currentAgentRestart(t, agentID); ok {
		t.Fatal("rejected create left an in-flight restart")
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
		capabilities = `["workspace_daemon_agent_process_v1","workspace_daemon_agent_reset_workspace_v1"]`
	}
	ctx := context.Background()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, daemon_id)
		VALUES (
			$1,  $2,  'local',  $3,  'online', 
			'',  jsonb_build_object('capabilities', $4::jsonb),  'private',  now(),  'agent-restart-test-daemon'
		)
		RETURNING id
	`, testWorkspaceID, "restart-runtime-"+randomID(), provider, capabilities).Scan(&runtimeID); err != nil {
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
		testHandler.restarts().finish(agentID)
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

func invokeCreateAgentRestart(t *testing.T, agentID, _ string, action agentRestartStorageKind) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/"+agentID+"/reset", map[string]any{
		"mode": agentRestartModeForStorage(action),
	})
	req = withURLParam(req, "id", agentID)
	testHandler.ResetAgent(rec, req)
	return rec
}

func invokeAgentLifecycleAction(t *testing.T, agentID, action string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodPost, "/api/agents/"+agentID+"/"+action, nil),
		"id", agentID,
	)
	if action == "start" {
		testHandler.StartAgent(rec, req)
	} else {
		testHandler.StopAgent(rec, req)
	}
	return rec
}

func currentAgentRestart(t *testing.T, agentID string) (activeAgentRestartState, bool) {
	t.Helper()
	return testHandler.restarts().get(agentID)
}

func containsResponseBody(rec *httptest.ResponseRecorder, want string) bool {
	return strings.Contains(rec.Body.String(), want)
}
