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

type capturedAgentLifecycleNotifier struct {
	mu          sync.Mutex
	workspaceID string
	daemonID    string
	eventType   string
	commandID   string
	payload     any
}

type rejectingAgentLifecycleNotifier struct{}

func (rejectingAgentLifecycleNotifier) NotifyAgentLifecycleCommand(string, string, string, string, any) bool {
	return false
}

func (n *capturedAgentLifecycleNotifier) NotifyAgentLifecycleCommand(workspaceID, daemonID, eventType, commandID string, payload any) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.workspaceID = workspaceID
	n.daemonID = daemonID
	n.eventType = eventType
	n.commandID = commandID
	n.payload = payload
	return true
}

func (n *capturedAgentLifecycleNotifier) snapshot() (string, string, string, string, any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.workspaceID, n.daemonID, n.eventType, n.commandID, n.payload
}

func TestAgentLifecycleContractPure(t *testing.T) {
	for _, action := range []AgentLifecycleActionKind{
		agentLifecycleRestart,
		agentLifecycleResetSessionRestart,
		agentLifecycleFullResetRestart,
	} {
		if !validAgentLifecycleAction(action) {
			t.Fatalf("valid action %q was rejected", action)
		}
	}
	if validAgentLifecycleAction("delete_agent") {
		t.Fatal("unknown action was accepted")
	}
	if agentLifecycleInitialStep(agentLifecycleScheduled) != "waiting_for_current_run" {
		t.Fatal("scheduled operation did not expose the drain wait step")
	}
	if agentLifecycleInitialStep(agentLifecycleRunning) != agentLifecycleStepStopping {
		t.Fatal("running operation did not expose the stopping step")
	}
	if !agentLifecycleCapabilityPresent([]string{"other", "workspace_runner_agent_reset_workspace_v1"}) {
		t.Fatal("lifecycle capability was not detected")
	}
	if agentLifecycleCapabilityPresent([]string{"other"}) {
		t.Fatal("missing lifecycle capability was accepted")
	}
	if !agentSessionResetCapabilityPresent([]string{"other", "agent_session_reset_v1"}) {
		t.Fatal("session reset capability was not detected")
	}
}

func TestAgentLifecyclePreflightIsPerActionAndFullResetIsIdleOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	insertRunningAgentLifecycleExecution(t, agentID, runtimeID)

	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/lifecycle", nil),
		"id", agentID,
	)
	testHandler.GetAgentLifecycle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentLifecyclePreflight
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	// Task #62 follow-up: plain restart bypasses the busy-check entirely
	// #112: all three actions preview as immediate (even while active).
	// after_current_run is never advertised — it never auto-dispatches.
	restart := response.Actions[agentLifecycleRestart]
	if !restart.Supported || restart.ExecutionMode != agentLifecycleImmediate || restart.DisabledReason != "" {
		t.Fatalf("restart preflight = %+v", restart)
	}
	resetSession := response.Actions[agentLifecycleResetSessionRestart]
	if !resetSession.Supported || resetSession.ExecutionMode != agentLifecycleImmediate || resetSession.DisabledReason != "" {
		t.Fatalf("reset_session_restart preflight = %+v", resetSession)
	}
	full := response.Actions[agentLifecycleFullResetRestart]
	if !full.Supported || full.ExecutionMode != agentLifecycleImmediate || full.DisabledReason != "" {
		t.Fatalf("full reset preflight = %+v", full)
	}
	// Fixture provider is "lifecycle-test" — not ForceKillable → false.
	if response.ProviderCapabilities.ForceRestart {
		t.Fatalf("provider_capabilities.force_restart=%v, want false for non-ForceKillable fixture provider", response.ProviderCapabilities.ForceRestart)
	}
}

func TestAgentLifecyclePreflightRejectsLegacyHeartbeatOnlyDaemon(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime
		SET metadata = jsonb_build_object('capabilities', '["agent_lifecycle_actions_v1"]'::jsonb)
		WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("downgrade runtime capabilities: %v", err)
	}
	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/lifecycle", nil),
		"id", agentID,
	)
	testHandler.GetAgentLifecycle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentLifecyclePreflight
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	for _, action := range []AgentLifecycleActionKind{agentLifecycleRestart, agentLifecycleResetSessionRestart, agentLifecycleFullResetRestart} {
		state := response.Actions[action]
		if state.Supported || state.DisabledReason != "unsupported_runtime_capability" {
			t.Fatalf("%s preflight = %+v", action, state)
		}
	}

	create := invokeCreateAgentLifecycle(t, agentID, "81818181-8181-4181-8181-818181818181", agentLifecycleRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "unsupported_runtime_capability") {
		t.Fatalf("legacy daemon restart create status=%d body=%s", create.Code, create.Body.String())
	}
}

func TestAgentLifecyclePreflightProviderCapabilitiesFollowProvider(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// cursor is in forceRestartResidentConstructors and implements ForceKill.
	agentID, _ := createAgentLifecycleFixtureWithProvider(t, true, "cursor")
	rec := httptest.NewRecorder()
	req := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/lifecycle", nil),
		"id", agentID,
	)
	testHandler.GetAgentLifecycle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentLifecyclePreflight
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

// TestAgentLifecycleCreateIsIdempotentAndForceRestartsBusyAgent pins #62/#112:
// plain restart on a busy agent is immediate/running (idempotent create).
// All three actions force-immediate when busy — see
// TestAgentLifecycleAllActionsForceImmediateWhenBusy.
func TestAgentLifecycleCreateIsIdempotentAndForceRestartsBusyAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	insertRunningAgentLifecycleExecution(t, agentID, runtimeID)

	// #112: full_reset on busy is also immediate (no longer agent_active reject).
	// Covered in TestAgentLifecycleAllActionsForceImmediateWhenBusy.

	key := uuid.NewString()
	first := invokeCreateAgentLifecycle(t, agentID, key, agentLifecycleRestart)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	var firstOperation AgentLifecycleOperation
	if err := json.Unmarshal(first.Body.Bytes(), &firstOperation); err != nil {
		t.Fatalf("decode first operation: %v", err)
	}
	if firstOperation.Status != agentLifecycleRunning ||
		firstOperation.ExecutionMode != agentLifecycleImmediate ||
		firstOperation.StartedAt == nil {
		t.Fatalf("restart on busy agent = %+v, want immediate/running (busy must not block plain restart)", firstOperation)
	}

	replay := invokeCreateAgentLifecycle(t, agentID, key, agentLifecycleRestart)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayOperation AgentLifecycleOperation
	if err := json.Unmarshal(replay.Body.Bytes(), &replayOperation); err != nil {
		t.Fatalf("decode replay operation: %v", err)
	}
	if replayOperation.ID != firstOperation.ID {
		t.Fatalf("replay operation id=%s want=%s", replayOperation.ID, firstOperation.ID)
	}

	mismatch := invokeCreateAgentLifecycle(t, agentID, key, agentLifecycleResetSessionRestart)
	if mismatch.Code != http.StatusConflict || !containsResponseBody(mismatch, "another operation") {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_lifecycle_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count lifecycle operations: %v", err)
	}
	if count != 1 {
		t.Fatalf("operation count=%d want=1", count)
	}
}

// TestAgentLifecycleAllActionsForceImmediateWhenBusy pins #112: never create
// after_current_run/scheduled lifecycle ops (they never auto-fire). Restart,
// reset_session_restart, and full_reset_restart are all immediate when busy.
func TestAgentLifecycleAllActionsForceImmediateWhenBusy(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, action := range []AgentLifecycleActionKind{
		agentLifecycleRestart,
		agentLifecycleResetSessionRestart,
		agentLifecycleFullResetRestart,
	} {
		t.Run(string(action), func(t *testing.T) {
			agentID, runtimeID := createAgentLifecycleFixture(t, true)
			insertRunningAgentLifecycleExecution(t, agentID, runtimeID)

			rec := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), action)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
			}
			var operation AgentLifecycleOperation
			if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
				t.Fatalf("decode operation: %v", err)
			}
			if operation.Status != agentLifecycleRunning ||
				operation.ExecutionMode != agentLifecycleImmediate ||
				operation.StartedAt == nil {
				t.Fatalf("%s on busy agent = %+v, want immediate/running (no scheduled)", action, operation)
			}
		})
	}
}

func TestAgentLifecycleConcurrentDuplicateRequestReturnsOneOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, true)
	key := uuid.NewString()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			responses <- invokeCreateAgentLifecycle(
				t, agentID, key, agentLifecycleResetSessionRestart,
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
		var operation AgentLifecycleOperation
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
		SELECT count(*) FROM agent_lifecycle_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count lifecycle operations: %v", err)
	}
	if count != 1 {
		t.Fatalf("operation count=%d want=1", count)
	}
}

func TestAgentLifecycleIdleActionsStartImmediately(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, action := range []AgentLifecycleActionKind{
		agentLifecycleRestart,
		agentLifecycleResetSessionRestart,
		agentLifecycleFullResetRestart,
	} {
		t.Run(string(action), func(t *testing.T) {
			agentID, _ := createAgentLifecycleFixture(t, true)
			rec := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), action)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
			}
			var operation AgentLifecycleOperation
			if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
				t.Fatalf("decode operation: %v", err)
			}
			if operation.Status != agentLifecycleRunning ||
				operation.ExecutionMode != agentLifecycleImmediate ||
				operation.StartedAt == nil {
				t.Fatalf("idle operation = %+v", operation)
			}
		})
	}
}

// TestAgentLifecycleCreateDispatchesImmediateOperationToDaemon pins the
// public seam: even an idle restart begins with Raft's discrete agent:stop
// fence, never a product-level composite lifecycle payload.
func TestAgentLifecycleCreateDispatchesImmediateOperationToDaemon(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, true)
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })
	rec := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(rec.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	workspaceID, daemonID, eventType, commandID, payload := notifier.snapshot()
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
	if workspaceID != testWorkspaceID || daemonID != "agent-lifecycle-test-daemon" {
		t.Fatalf("command route = workspace %q daemon %q", workspaceID, daemonID)
	}
}

func TestAgentLifecycleRestartAdvancesStopThenStartThenActive(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	var oldLaunchID string
	if err := testPool.QueryRow(context.Background(), `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&oldLaunchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES ($1, $2, $3, 'agent-lifecycle-test-daemon', 'runner-instance', $4, 'active')
	`, testWorkspaceID, agentID, runtimeID, oldLaunchID); err != nil {
		t.Fatal(err)
	}
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })

	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID || stop.AgentID != agentID || stop.LaunchID != oldLaunchID {
		t.Fatalf("first restart command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}

	identity := daemonws.ClientIdentity{DaemonID: "agent-lifecycle-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: uuid.NewString(), Status: protocol.AgentStatusInactive}); err != nil || handled {
		t.Fatalf("stale inactive advanced stop fence: handled=%v err=%v", handled, err)
	}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: oldLaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance inactive handled=%v err=%v", handled, err)
	}
	_, _, eventType, commandID, payload = notifier.snapshot()
	start, ok := payload.(protocol.WorkspaceRunnerAgentStartPayload)
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.AgentID != agentID || start.RuntimeID != runtimeID || start.LaunchID == oldLaunchID || start.SessionID != nil {
		t.Fatalf("replacement restart command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance active handled=%v err=%v", handled, err)
	}
	finished, err := getAgentLifecycleOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentLifecycleSucceeded || finished.FinishedAt == nil {
		t.Fatalf("finished restart = %+v err=%v", finished, err)
	}
}

func TestAgentLifecycleFullResetWaitsForWorkspaceResultBeforeFreshStart(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })

	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleFullResetRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID {
		t.Fatalf("full reset stop event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-lifecycle-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
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
	if !ok || eventType != protocol.EventDaemonAgentStart || commandID != operation.ID || start.SessionID == nil || *start.SessionID != "" {
		t.Fatalf("fresh replacement command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: start.LaunchID, Status: protocol.AgentStatusActive}); err != nil || !handled {
		t.Fatalf("advance full reset active handled=%v err=%v", handled, err)
	}
	finished, err := getAgentLifecycleOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentLifecycleSucceeded {
		t.Fatalf("finished full reset = %+v err=%v", finished, err)
	}
}

func TestWorkspaceRunnerActiveStatusCompletesLifecycleOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	identity := daemonws.ClientIdentity{
		DaemonID:    "agent-lifecycle-test-daemon",
		WorkspaceID: testWorkspaceID,
		RuntimeIDs:  []string{runtimeID},
	}
	_, _, _, _, firstPayload := notifier.snapshot()
	stop, ok := firstPayload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first lifecycle payload=%T, want stop", firstPayload)
	}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	var launchID string
	if err := testPool.QueryRow(context.Background(), `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&launchID); err != nil {
		t.Fatal(err)
	}
	status := protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, status); err != nil {
			t.Fatalf("record active status attempt %d: %v", attempt+1, err)
		}
	}
	finished, err := getAgentLifecycleOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || finished == nil || finished.Status != agentLifecycleSucceeded || finished.FinishedAt == nil {
		t.Fatalf("finished operation = %+v, %v", finished, err)
	}
}

func TestAgentLifecycleUnavailableRunnerRemainsResumableUntilReady(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, true)
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = rejectingAgentLifecycleNotifier{}
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleFullResetRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Status != agentLifecycleRunning || operation.Step != agentLifecycleStepStopping || operation.FinishedAt != nil {
		t.Fatalf("unavailable Runner operation should remain resumable = %+v", operation)
	}
	notifier := &capturedAgentLifecycleNotifier{}
	testHandler.AgentLifecycleNotifier = notifier
	identity := daemonws.ClientIdentity{DaemonID: "agent-lifecycle-test-daemon", WorkspaceID: testWorkspaceID}
	if err := testHandler.resumeAgentLifecycleOperations(context.Background(), identity); err != nil {
		t.Fatalf("resume lifecycle operation on Runner ready: %v", err)
	}
	_, _, eventType, commandID, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok || eventType != protocol.EventDaemonAgentStop || commandID != operation.ID || stop.LaunchID == "" {
		t.Fatalf("resumed command event=%q command=%q payload=%+v", eventType, commandID, payload)
	}
}

func TestAgentLifecycleCommandTimeoutFailsBusinessRecordOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, true)
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_lifecycle_operation SET started_at = now() - interval '3 minutes' WHERE id = $1
	`, operation.ID); err != nil {
		t.Fatal(err)
	}
	count, err := SweepTimedOutAgentLifecycleOperations(context.Background(), testPool)
	if err != nil || count < 1 {
		t.Fatalf("timeout sweep count=%d err=%v", count, err)
	}
	got, err := getAgentLifecycleOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil || got == nil || got.Status != agentLifecycleFailed || got.Step != "timeout" || got.FinishedAt == nil {
		t.Fatalf("timed-out operation = %+v, %v", got, err)
	}
}

func TestTimedOutResetStartKeepsExplicitFreshSessionOnReconcile(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleResetSessionRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first reset payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-lifecycle-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_lifecycle_operation SET started_at = now() - interval '3 minutes' WHERE id = $1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepTimedOutAgentLifecycleOperations(context.Background(), testPool); err != nil {
		t.Fatal(err)
	}
	desired, err := testHandler.loadRunnerDesiredLaunches(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, launch := range desired {
		if launch.agentID == agentID {
			if launch.sessionID == nil || *launch.sessionID != "" || launch.startDispatchID != operation.ID {
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
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	notifier := &capturedAgentLifecycleNotifier{}
	previous := testHandler.AgentLifecycleNotifier
	testHandler.AgentLifecycleNotifier = notifier
	t.Cleanup(func() { testHandler.AgentLifecycleNotifier = previous })
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleFullResetRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, payload := notifier.snapshot()
	stop, ok := payload.(protocol.WorkspaceRunnerAgentStopPayload)
	if !ok {
		t.Fatalf("first full-reset payload=%T, want stop", payload)
	}
	identity := daemonws.ClientIdentity{DaemonID: "agent-lifecycle-test-daemon", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if handled, err := testHandler.advanceAgentLifecycleFromStatus(context.Background(), identity, protocol.AgentStatusPayload{AgentID: agentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil || !handled {
		t.Fatalf("advance stop handled=%v err=%v", handled, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_lifecycle_operation SET started_at = now() - interval '3 minutes' WHERE id = $1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepTimedOutAgentLifecycleOperations(context.Background(), testPool); err != nil {
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

func TestAgentLifecycleRunningOperationOverlaysExistingAgentHealth(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, true)
	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleResetSessionRestart)
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
		response.Summary.ReasonCode != "agent_lifecycle_reset_session_restart" {
		t.Fatalf("health summary = %+v", response.Summary)
	}
}

func TestAgentLifecycleRejectsPlainMemberAndIncapableRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentLifecycleFixture(t, false)
	memberID := createAgentLifecycleMember(t, "member")

	memberPreflight := httptest.NewRecorder()
	memberReq := withURLParam(
		newRequestAs(memberID, http.MethodGet, "/api/agents/"+agentID+"/lifecycle", nil),
		"id", agentID,
	)
	testHandler.GetAgentLifecycle(memberPreflight, memberReq)
	if memberPreflight.Code != http.StatusForbidden {
		t.Fatalf("member preflight status=%d body=%s", memberPreflight.Code, memberPreflight.Body.String())
	}

	ownerPreflight := httptest.NewRecorder()
	ownerReq := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/lifecycle", nil),
		"id", agentID,
	)
	testHandler.GetAgentLifecycle(ownerPreflight, ownerReq)
	if ownerPreflight.Code != http.StatusOK {
		t.Fatalf("owner preflight status=%d body=%s", ownerPreflight.Code, ownerPreflight.Body.String())
	}
	var response AgentLifecyclePreflight
	if err := json.Unmarshal(ownerPreflight.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode owner preflight: %v", err)
	}
	for action, got := range response.Actions {
		if got.Supported || got.DisabledReason != "unsupported_runtime_capability" {
			t.Fatalf("%s unsupported preflight=%+v", action, got)
		}
	}

	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "unsupported_runtime_capability") {
		t.Fatalf("incapable create status=%d body=%s", create.Code, create.Body.String())
	}
}

// TestAgentLifecycleCreateRejectsStaleHeartbeatWithoutCreatingAnOperation pins
// task #52's primary defense (Parker: "reject up-front" is the main path,
// the dispatch timeout is only the backstop for the narrow race after this
// check passes): a runtime whose last_seen_at is stale must be refused at
// create time — not accepted and left to time out two minutes later. No
// operation row means no window where the agent's health shows "restarting"
// for a machine we already know is unreachable.
func TestAgentLifecycleCreateRejectsStaleHeartbeatWithoutCreatingAnOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET last_seen_at = now() - interval '10 minutes' WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("stale last_seen_at: %v", err)
	}

	create := invokeCreateAgentLifecycle(t, agentID, uuid.NewString(), agentLifecycleRestart)
	if create.Code != http.StatusConflict || !containsResponseBody(create, "agent_runtime_offline") {
		t.Fatalf("stale-heartbeat create status=%d body=%s, want 409 agent_runtime_offline", create.Code, create.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_lifecycle_operation WHERE agent_id = $1
	`, agentID).Scan(&count); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected zero operation rows for a rejected create, got %d", count)
	}
}

func TestAgentLifecycleNoRuntimeIsUnsupported(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, supported, reason, err := testHandler.agentLifecycleRuntimeSupport(
		context.Background(), db.Agent{},
	)
	if err != nil {
		t.Fatalf("missing-runtime support check: %v", err)
	}
	if supported || reason != "agent_runtime_missing" {
		t.Fatalf("missing-runtime support=(%t, %q)", supported, reason)
	}
}

func createAgentLifecycleFixture(t *testing.T, capable bool) (agentID, runtimeID string) {
	t.Helper()
	return createAgentLifecycleFixtureWithProvider(t, capable, "lifecycle-test")
}

func createAgentLifecycleFixtureWithProvider(t *testing.T, capable bool, provider string) (agentID, runtimeID string) {
	t.Helper()
	capabilities := `[]`
	if capable {
		capabilities = `["workspace_runner_attachment_v1","workspace_runner_agent_reset_workspace_v1","agent_session_reset_v1"]`
	}
	ctx := context.Background()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at, daemon_id
		)
		VALUES (
			$1, $2, 'local', $3, 'online',
			'', jsonb_build_object('capabilities', $4::jsonb), $5, 'private', now(), 'agent-lifecycle-test-daemon'
		)
		RETURNING id
	`, testWorkspaceID, "lifecycle-runtime-"+randomID(), provider, capabilities, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create lifecycle runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, 'Lifecycle test', 'local', $3, 1, $4, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "lifecycle-agent-"+randomID(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create lifecycle agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_lifecycle_operation WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func insertRunningAgentLifecycleExecution(t *testing.T, agentID, runtimeID string) {
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

func createAgentLifecycleMember(t *testing.T, role string) string {
	t.Helper()
	userID := uuid.NewString()
	email := "agent-lifecycle-" + userID + "@multica.test"
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, 'Agent lifecycle member', $2)
	`, userID, email); err != nil {
		t.Fatalf("create lifecycle member: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("add lifecycle member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func invokeCreateAgentLifecycle(t *testing.T, agentID, idempotencyKey string, action AgentLifecycleActionKind) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/"+agentID+"/lifecycle", map[string]any{
		"action_kind": action,
	})
	req.Header.Set("Idempotency-Key", idempotencyKey)
	req = withURLParam(req, "id", agentID)
	testHandler.CreateAgentLifecycleOperation(rec, req)
	return rec
}

func containsResponseBody(rec *httptest.ResponseRecorder, want string) bool {
	return strings.Contains(rec.Body.String(), want)
}
