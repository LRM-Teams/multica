package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func createMachineUpgradeSiblingRuntimes(t *testing.T, ownerID string) (string, string, string) {
	t.Helper()
	return createMachineUpgradeRuntimesWithProviders(t, ownerID, "claude", "codex")
}

func createMachineUpgradeRuntimesWithProviders(t *testing.T, ownerID string, firstProvider, secondProvider string) (string, string, string) {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "machine-upgrade-" + uuid.NewString()
	create := func(provider string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, owner_id, last_seen_at
			) VALUES ($1, $2, $3, 'local', $4, 'online', 'test machine',
				'{"capabilities":["machine_upgrade_v1"]}'::jsonb, $5, now())
			RETURNING id`, testWorkspaceID, daemonID, provider+"-"+uuid.NewString(), provider, ownerID).Scan(&id); err != nil {
			t.Fatalf("create machine upgrade runtime: %v", err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id) })
		return id
	}
	first, second := create(firstProvider), create(secondProvider)
	// Machine Upgrade is keyed by daemon ID rather than runtime FKs, so remove
	// its test rows before the fixture tears down the owning test user.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID)
	})
	return first, second, daemonID
}

func bindMachineUpgradeWorkspace(t *testing.T, daemonID, workspaceID, ownerID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, $4, TRUE)`, daemonID, workspaceID, ownerID, "machine-upgrade-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, daemonID, workspaceID)
	})
}

func createLegacyMachineUpgradeSiblingRuntimes(t *testing.T, ownerID string) (string, string, string) {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "legacy-machine-upgrade-" + uuid.NewString()
	create := func(provider string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, owner_id, last_seen_at
			) VALUES ($1, $2, $3, 'local', $4, 'online', 'test machine',
				' {"cli_version":"v0.4.13"}'::jsonb, $5, now())
			RETURNING id`, testWorkspaceID, daemonID, provider+"-"+uuid.NewString(), provider, ownerID).Scan(&id); err != nil {
			t.Fatalf("create legacy machine upgrade runtime: %v", err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id) })
		return id
	}
	first, second := create("claude"), create("codex")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID)
	})
	return first, second, daemonID
}

func getMachineUpgradeRuntime(t *testing.T, runtimeID string) db.AgentRuntime {
	t.Helper()
	id, err := uuid.Parse(runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func initiateMachineUpgrade(t *testing.T, userID, daemonID, target string) (*httptest.ResponseRecorder, MachineUpgrade) {
	t.Helper()
	req := newRequestAsUser(userID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": target,
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	testHandler.CreateMachineUpgrade(w, req)
	if w.Code != http.StatusOK {
		return w, MachineUpgrade{}
	}
	var op MachineUpgrade
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	return w, op
}

func TestMachineUpgrade_CanonicalRouteSharesOneDaemonOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO computer_identity_owner (daemon_id, user_id) VALUES ($1, $2)`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})

	firstW, first := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	if firstW.Code != http.StatusOK || first.Phase != MachineUpgradeQueued {
		t.Fatalf("first machine upgrade = %d %+v", firstW.Code, first)
	}
	secondW, _ := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	if secondW.Code != http.StatusConflict {
		t.Fatalf("distinct request for same daemon = %d: %s", secondW.Code, secondW.Body.String())
	}
	var conflict struct {
		Operation MachineUpgrade `json:"operation"`
	}
	if err := json.Unmarshal(secondW.Body.Bytes(), &conflict); err != nil || conflict.Operation.ID != first.ID {
		t.Fatalf("same daemon conflict = %+v err=%v, want canonical %q", conflict, err, first.ID)
	}
	if op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, first.ID); err != nil || op == nil || op.Phase != MachineUpgradeQueued {
		t.Fatalf("canonical machine operation = %+v err=%v", op, err)
	}
	var legacyIntentCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM daemon_runtime_update_intent WHERE runtime_id = ANY($1::text[]::uuid[])`,
		[]string{firstRuntimeID, secondRuntimeID}).Scan(&legacyIntentCount); err != nil {
		t.Fatal(err)
	}
	if legacyIntentCount != 0 {
		t.Fatalf("canonical route created %d legacy runtime intents", legacyIntentCount)
	}

	conflictW, _ := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	if conflictW.Code != http.StatusConflict {
		t.Fatalf("different target conflict = %d: %s", conflictW.Code, conflictW.Body.String())
	}
}

func TestMachineUpgrade_CanonicalRouteReplaysRequestIDAndSupportsLookup(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	requestID := "machine-upgrade-request-" + uuid.NewString()
	create := func() *httptest.ResponseRecorder {
		req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
			"target_version": "v9.9.9",
			"request_id":     requestID,
		})
		req = withURLParam(req, "daemonId", daemonID)
		w := httptest.NewRecorder()
		testHandler.CreateMachineUpgrade(w, req)
		return w
	}
	firstW := create()
	if firstW.Code != http.StatusOK {
		t.Fatalf("canonical create = %d: %s", firstW.Code, firstW.Body.String())
	}
	var first MachineUpgrade
	if err := json.Unmarshal(firstW.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	secondW := create()
	if secondW.Code != http.StatusOK {
		t.Fatalf("canonical replay = %d: %s", secondW.Code, secondW.Body.String())
	}
	var second MachineUpgrade
	if err := json.Unmarshal(secondW.Body.Bytes(), &second); err != nil || second.ID != first.ID {
		t.Fatalf("canonical replay = %+v err=%v, want %s", second, err, first.ID)
	}
	conflictReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v9.9.9",
		"request_id":     "different-" + requestID,
	})
	conflictReq = withURLParam(conflictReq, "daemonId", daemonID)
	conflictW := httptest.NewRecorder()
	testHandler.CreateMachineUpgrade(conflictW, conflictReq)
	if conflictW.Code != http.StatusConflict {
		t.Fatalf("distinct canonical request = %d: %s", conflictW.Code, conflictW.Body.String())
	}

	getReq := newRequestAsUser(testUserID, http.MethodGet, "/api/daemons/"+daemonID+"/upgrades/"+first.ID, nil)
	getReq = withURLParams(getReq, "daemonId", daemonID, "upgradeId", first.ID)
	getW := httptest.NewRecorder()
	testHandler.GetMachineUpgrade(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("canonical lookup = %d: %s", getW.Code, getW.Body.String())
	}
}

func TestMachineUpgrade_CapableHeartbeatDoesNotClaimConnectSocketUpgrade(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)

	firstAck, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if firstAck.PendingMachineUpgrade != nil {
		t.Fatalf("capable heartbeat claimed connect-socket upgrade %+v", firstAck.PendingMachineUpgrade)
	}
	secondAck, _, err := testHandler.processHeartbeat(context.Background(), secondRuntime, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondAck.PendingMachineUpgrade != nil {
		t.Fatalf("sibling heartbeat claimed connect-socket upgrade %+v", secondAck.PendingMachineUpgrade)
	}
	if op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID); err != nil || op == nil || op.Phase != MachineUpgradeQueued {
		t.Fatalf("connect-socket operation left queued = %+v err=%v", op, err)
	}
}

func TestMachineUpgrade_AcceptFromQueuedCompletesWithoutHeartbeatClaim(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)

	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/machine-upgrades/"+created.ID+"/accept", map[string]string{
		"generation_id": "generation-a",
		"cli_version":   "v9.9.9",
	})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.Phase != MachineUpgradeConverging || op.AcceptedGeneration == nil || *op.AcceptedGeneration != "generation-a" || !sameMachineRuntimeSet(op.AcceptedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("accepted operation = %+v err=%v", op, err)
	}

	// One correct registration cannot complete a machine operation.
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), firstRuntime, "v9.9.9", "generation-a")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeConverging {
		t.Fatalf("one sibling attestation completed operation: %+v", op)
	}
	// A wrong version and stale generation are ignored rather than counted.
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), secondRuntime, "v9.9.8", "generation-a")
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), secondRuntime, "v9.9.9", "generation-stale")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeConverging || len(op.AttestedRuntimeIDs) != 1 {
		t.Fatalf("invalid attestation advanced operation: %+v", op)
	}

	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), secondRuntime, "v9.9.9", "generation-a")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeCompleted || op.Result == nil || *op.Result != "completed" || !sameMachineRuntimeSet(op.AttestedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("full sibling convergence = %+v", op)
	}
}

func TestMachineUpgrade_AcceptSnapshotsEveryConnectedWorkspaceRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, siblingWorkspaceID, testUserID)
	createSibling := func(provider string) string {
		var id string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, owner_id, last_seen_at
			) VALUES ($1, $2, $3, 'local', $4, 'online', 'test machine',
				'{"capabilities":["machine_upgrade_v1"]}'::jsonb, $5, now())
			RETURNING id`, siblingWorkspaceID, daemonID, provider+"-"+uuid.NewString(), provider, testUserID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id) })
		return id
	}
	thirdRuntimeID := createSibling("claude")
	fourthRuntimeID := createSibling("codex")
	localHandler := *testHandler
	localHandler.Bus = events.New()
	publishedWorkspaces := make([]string, 0)
	localHandler.Bus.Subscribe(protocol.EventComputerUpdated, func(event events.Event) {
		publishedWorkspaces = append(publishedWorkspaces, event.WorkspaceID)
		payload, ok := event.Payload.(map[string]any)
		if !ok || payload["computer_id"] != daemonID {
			t.Fatalf("computer projection payload = %#v", event.Payload)
		}
	})

	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/machine-upgrades/"+created.ID+"/accept", map[string]string{
		"generation_id": "generation-a",
		"cli_version":   "v9.9.9",
	})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	localHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil {
		t.Fatalf("accepted operation = %+v err=%v", op, err)
	}
	if !sameMachineRuntimeSet(op.AcceptedWorkspaceIDs, []string{testWorkspaceID, siblingWorkspaceID}) {
		t.Fatalf("accepted Workspaces = %v", op.AcceptedWorkspaceIDs)
	}
	if !sameMachineRuntimeSet(op.AcceptedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID, thirdRuntimeID, fourthRuntimeID}) {
		t.Fatalf("accepted Runtimes = %v", op.AcceptedRuntimeIDs)
	}
	if !sameMachineRuntimeSet(publishedWorkspaces, []string{testWorkspaceID, siblingWorkspaceID}) || len(publishedWorkspaces) != 2 {
		t.Fatalf("upgrade projections = %v, want one per active Workspace connection", publishedWorkspaces)
	}
}

func TestMachineUpgrade_ComputerAttestationRequiresExactAcceptedRuntimeSet(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := testHandler.MachineUpgradeStore.Accept(
		context.Background(), daemonID, created.ID, "generation-a", "v9.9.9", "v9.9.9",
	)
	if err != nil {
		t.Fatalf("accept machine upgrade: %v", err)
	}

	if _, err := testHandler.MachineUpgradeStore.AttestComputer(
		context.Background(), daemonID, created.ID, "generation-a", "v9.9.9",
		[]string{firstRuntimeID}, accepted.AcceptedWorkspaceIDs,
	); !errors.Is(err, errMachineUpgradeAttestationRejected) {
		t.Fatalf("incomplete Computer runtime proof error = %v, want attestation rejection", err)
	}
	stored, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || stored.Phase == MachineUpgradeCompleted {
		t.Fatalf("incomplete Computer proof completed operation: %+v err=%v", stored, err)
	}

	completed, err := testHandler.MachineUpgradeStore.AttestComputer(
		context.Background(), daemonID, created.ID, "generation-a", "v9.9.9",
		[]string{firstRuntimeID, secondRuntimeID}, accepted.AcceptedWorkspaceIDs,
	)
	if err != nil || completed.Phase != MachineUpgradeCompleted ||
		!sameMachineRuntimeSet(completed.AttestedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("exact Computer proof = %+v err=%v", completed, err)
	}
	replayed, err := testHandler.MachineUpgradeStore.AttestComputer(
		context.Background(), daemonID, created.ID, "generation-a", "v9.9.9",
		[]string{secondRuntimeID, firstRuntimeID}, accepted.AcceptedWorkspaceIDs,
	)
	if err != nil || replayed.ID != completed.ID || replayed.Phase != MachineUpgradeCompleted {
		t.Fatalf("completed Computer proof replay = %+v err=%v", replayed, err)
	}
}

func TestMachineUpgrade_ComputerAttestationAllowsRetiredProviderGap(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	claudeID, retiredID, daemonID := createMachineUpgradeRuntimesWithProviders(t, testUserID, "claude", "antigravity")
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, claudeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := testHandler.MachineUpgradeStore.Accept(
		context.Background(), daemonID, created.ID, "generation-retired", "v9.9.9", "v9.9.9",
	)
	if err != nil {
		t.Fatalf("accept machine upgrade: %v", err)
	}
	if !sameMachineRuntimeSet(accepted.AcceptedRuntimeIDs, []string{claudeID, retiredID}) {
		t.Fatalf("accepted Runtimes = %v", accepted.AcceptedRuntimeIDs)
	}

	completed, err := testHandler.MachineUpgradeStore.AttestComputer(
		context.Background(), daemonID, created.ID, "generation-retired", "v9.9.9",
		[]string{claudeID}, accepted.AcceptedWorkspaceIDs,
	)
	if err != nil || completed.Phase != MachineUpgradeCompleted ||
		!sameMachineRuntimeSet(completed.AttestedRuntimeIDs, []string{claudeID}) {
		t.Fatalf("retired-provider Computer proof = %+v err=%v", completed, err)
	}
}

func TestMachineUpgrade_TakeoverReceiptDoesNotCASComputerGeneration(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO computer_identity_owner (daemon_id, user_id) VALUES ($1, $2)`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.10")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := testHandler.MachineUpgradeStore.Accept(
		context.Background(), daemonID, created.ID, "generation-a", "v9.9.9", "v9.9.10",
	)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err = testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeVerifying, "", "")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err = testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeHandoff, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_generation (daemon_id, generation) VALUES ($1, 66)
		ON CONFLICT (daemon_id) DO UPDATE SET generation=66`, daemonID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_generation WHERE daemon_id=$1`, daemonID)
	})

	request := func(predecessor, candidate int64, workspaceIDs []string) *httptest.ResponseRecorder {
		req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/computer/machine-upgrades/"+created.ID+"/takeover", map[string]any{
			"daemon_id": daemonID, "generation_id": "generation-a", "cli_version": "v9.9.10",
			"predecessor_computer_generation": predecessor, "candidate_computer_generation": candidate,
			"workspace_ids": workspaceIDs,
		})
		req.Header.Set("X-Computer-Generation", strconv.FormatInt(candidate, 10))
		req = withURLParam(req, "upgradeId", created.ID)
		w := httptest.NewRecorder()
		testHandler.CommitComputerMachineUpgradeTakeover(w, req)
		return w
	}

	if w := request(0, 0, nil); w.Code != http.StatusOK {
		t.Fatalf("incomplete identity receipt status=%d body=%s", w.Code, w.Body.String())
	}
	var generation int64
	if err := testPool.QueryRow(context.Background(), `SELECT generation FROM computer_generation WHERE daemon_id=$1`, daemonID).Scan(&generation); err != nil || generation != 66 {
		t.Fatalf("takeover receipt changed generation=%d err=%v", generation, err)
	}

	if w := request(66, 67, accepted.AcceptedWorkspaceIDs); w.Code != http.StatusOK {
		t.Fatalf("takeover receipt status=%d body=%s accepted=%+v", w.Code, w.Body.String(), accepted)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT generation FROM computer_generation WHERE daemon_id=$1`, daemonID).Scan(&generation); err != nil || generation != 66 {
		t.Fatalf("takeover receipt CAS'd generation=%d err=%v", generation, err)
	}
	if w := request(66, 67, accepted.AcceptedWorkspaceIDs); w.Code != http.StatusOK {
		t.Fatalf("takeover receipt replay status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMachineUpgrade_LegacyHeartbeatDoesNotBootstrapPendingUpdate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createLegacyMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v0.4.14")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)

	ack, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ack.PendingUpdate != nil || ack.PendingMachineUpgrade != nil {
		t.Fatalf("pre-0.4.14 heartbeat still received an upgrade carrier %+v", ack)
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.Phase != MachineUpgradeQueued || op.AcceptedGeneration != nil {
		t.Fatalf("pre-0.4.14 heartbeat claimed machine upgrade = %+v err=%v", op, err)
	}
}

func TestMachineUpgradeLatestResolvesOnlyAtCapableDelivery(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	requestID := "machine-upgrade-latest-" + uuid.NewString()
	createReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{"request_id": requestID, "target_version": "latest"})
	createReq = withURLParam(createReq, "daemonId", daemonID)
	createW := httptest.NewRecorder()
	testHandler.CreateMachineUpgrade(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("create latest = %d: %s", createW.Code, createW.Body.String())
	}
	var created MachineUpgrade
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/machine-upgrades/"+created.ID+"/accept", map[string]string{
		"generation_id":   "generation-latest",
		"cli_version":     "v9.9.9",
		"resolved_target": "v10.0.0",
	})
	acceptReq = withRouteParams(acceptReq, "runtimeId", runtimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept resolved latest = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.ResolvedTarget == nil || *op.ResolvedTarget != "v10.0.0" || op.Phase != MachineUpgradeStaging {
		t.Fatalf("latest operation = %+v, %v", op, err)
	}
}

func TestMachineUpgrade_CapabilityAndManagedSetPreventUnsafeCompletion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = $1`, firstRuntimeID); err != nil {
		t.Fatal(err)
	}
	firstRuntime = getMachineUpgradeRuntime(t, firstRuntimeID)
	ack, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil || ack.PendingMachineUpgrade != nil {
		t.Fatalf("unsupported runtime received machine action: %+v err=%v", ack, err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET metadata = '{"capabilities":["machine_upgrade_v1"]}'::jsonb WHERE id = $1`, firstRuntimeID); err != nil {
		t.Fatal(err)
	}
	firstRuntime = getMachineUpgradeRuntime(t, firstRuntimeID)
	ack, _, err = testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil || ack.PendingMachineUpgrade != nil {
		t.Fatalf("capable connect-socket runtime claimed heartbeat upgrade: %+v err=%v", ack, err)
	}

	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/", map[string]string{"generation_id": "generation-b", "cli_version": "v9.9.9"})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	// A changed managed set has no authority to finish the earlier snapshot.
	var extraID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'extra', 'local', 'pi', 'online', 'test machine', '{"capabilities":["machine_upgrade_v1"]}'::jsonb, $3, now()) RETURNING id`, testWorkspaceID, daemonID, testUserID).Scan(&extraID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, extraID) })
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/", nil), firstRuntime, "v9.9.9", "generation-b")
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/", nil), secondRuntime, "v9.9.9", "generation-b")
	op, _ := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeConverging || len(op.AttestedRuntimeIDs) != 0 {
		t.Fatalf("changed managed set completed or recorded attestations: %+v", op)
	}
}

func TestMachineUpgrade_NewTargetProgressesDurablyBeforeHandoff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/", map[string]string{"generation_id": "generation-stage", "cli_version": "v9.9.9"})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	op, _ := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeStaging || op.ResolvedTarget == nil || *op.ResolvedTarget != "v10.0.0" {
		t.Fatalf("new target accept = %+v", op)
	}
	progress := func(phase MachineUpgradePhase) {
		req := newRequestAsUser(testUserID, http.MethodPost, "/", map[string]any{"phase": phase})
		req = withRouteParams(req, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
		w := httptest.NewRecorder()
		testHandler.ReportMachineUpgradeProgress(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("progress %s = %d: %s", phase, w.Code, w.Body.String())
		}
	}
	progress(MachineUpgradeVerifying)
	progress(MachineUpgradeHandoff)
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeHandoff || op.Result != nil {
		t.Fatalf("handoff must not complete: %+v", op)
	}
	// A typed staging failure is terminal; success has no daemon report path.
	req := newRequestAsUser(testUserID, http.MethodPost, "/", map[string]any{"phase": MachineUpgradeFailed, "error_code": "stage_failed", "error_message": "checksum mismatch"})
	req = withRouteParams(req, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	w := httptest.NewRecorder()
	testHandler.ReportMachineUpgradeProgress(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failure = %d: %s", w.Code, w.Body.String())
	}
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeFailed || op.Result == nil || *op.Result != "failed" || op.ErrorCode == nil || *op.ErrorCode != "stage_failed" {
		t.Fatalf("typed failure = %+v", op)
	}
}

func TestMachineUpgrade_HandoffCompletesOnlyWithSuccessorSiblingSet(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/", map[string]string{"generation_id": "generation-successor", "cli_version": "v9.9.9"})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	for _, phase := range []MachineUpgradePhase{MachineUpgradeVerifying, MachineUpgradeHandoff} {
		if _, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, phase, "", ""); err != nil {
			t.Fatalf("progress %s: %v", phase, err)
		}
	}
	if replayed, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeVerifying, "", ""); err != nil || replayed == nil || replayed.Phase != MachineUpgradeHandoff {
		t.Fatalf("lost verifying report replay = %+v, %v", replayed, err)
	}
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/", nil), firstRuntime, "v10.0.0", "generation-successor")
	op, _ := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeConverging {
		t.Fatalf("first successor attestation = %+v", op)
	}
	testHandler.attestMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/", nil), secondRuntime, "v10.0.0", "generation-successor")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeCompleted || op.Result == nil || *op.Result != "completed" {
		t.Fatalf("full successor convergence = %+v", op)
	}
	if late, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeFailed, "late_failure", "lost response"); err != nil || late == nil || late.Phase != MachineUpgradeCompleted {
		t.Fatalf("late terminal report changed completion = %+v, %v", late, err)
	}
}

func TestMachineUpgradeRollbackRequiresRestoredFullSiblingSet(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/machine-upgrades/"+created.ID+"/accept", map[string]string{
		"generation_id":   "generation-target",
		"cli_version":     "v9.9.9",
		"resolved_target": "v10.0.0",
	})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	for _, phase := range []MachineUpgradePhase{MachineUpgradeVerifying, MachineUpgradeHandoff} {
		if _, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, phase, "", ""); err != nil {
			t.Fatalf("advance %s: %v", phase, err)
		}
	}
	rollback, err := testHandler.MachineUpgradeStore.BeginRollback(context.Background(), daemonID, created.ID, "generation-rollback", "candidate_takeover_failed", "candidate did not bind")
	if err != nil || rollback == nil || rollback.Phase != MachineUpgradeRollbackPending {
		t.Fatalf("begin rollback = %+v, %v", rollback, err)
	}
	rollbackReplay, err := testHandler.MachineUpgradeStore.BeginRollback(context.Background(), daemonID, created.ID, "generation-rollback", "candidate_takeover_failed", "candidate did not bind")
	if err != nil || rollbackReplay == nil || rollbackReplay.Phase != MachineUpgradeRollbackPending {
		t.Fatalf("begin rollback replay = %+v, %v", rollbackReplay, err)
	}
	testHandler.attestMachineUpgradeRollbackRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), firstRuntime, "v9.9.9", "generation-rollback")
	op, _ := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeRollbackPending || len(op.RollbackRuntimeIDs) != 1 {
		t.Fatalf("partial rollback proof = %+v", op)
	}
	testHandler.attestMachineUpgradeRollbackRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), secondRuntime, "v9.9.9", "generation-rollback")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeRolledBack || op.Result == nil || *op.Result != "rolled_back" || !sameMachineRuntimeSet(op.RollbackRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("restored rollback proof = %+v", op)
	}
	replayedRollback, err := testHandler.MachineUpgradeStore.AttestRollback(context.Background(), daemonID, created.ID, "generation-rollback", firstRuntimeID, "v9.9.9", []string{firstRuntimeID, secondRuntimeID})
	if err != nil || replayedRollback == nil || replayedRollback.Phase != MachineUpgradeRolledBack {
		t.Fatalf("rolled-back proof replay = %+v, %v", replayedRollback, err)
	}
	receiptReq := newRequestAsUser(testUserID, http.MethodGet, "/api/daemon/runtimes/"+firstRuntimeID+"/machine-upgrades/"+created.ID, nil)
	receiptReq = withRouteParams(receiptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	receiptW := httptest.NewRecorder()
	testHandler.GetDaemonMachineUpgrade(receiptW, receiptReq)
	if receiptW.Code != http.StatusOK {
		t.Fatalf("daemon terminal receipt = %d: %s", receiptW.Code, receiptW.Body.String())
	}
	var receipt MachineUpgrade
	if err := json.Unmarshal(receiptW.Body.Bytes(), &receipt); err != nil || receipt.ID != created.ID || receipt.Phase != MachineUpgradeRolledBack {
		t.Fatalf("daemon terminal receipt = %+v err=%v", receipt, err)
	}
}

func TestMachineUpgradeRollbackAllowsRetiredProviderGap(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	claudeID, retiredID, daemonID := createMachineUpgradeRuntimesWithProviders(t, testUserID, "claude", "antigravity")
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	claudeRuntime := getMachineUpgradeRuntime(t, claudeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), claudeRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := testHandler.MachineUpgradeStore.Accept(
		context.Background(), daemonID, created.ID, "generation-target", "v9.9.9", "v10.0.0",
	)
	if err != nil {
		t.Fatalf("accept machine upgrade: %v", err)
	}
	if !sameMachineRuntimeSet(accepted.AcceptedRuntimeIDs, []string{claudeID, retiredID}) {
		t.Fatalf("accepted Runtimes = %v", accepted.AcceptedRuntimeIDs)
	}
	for _, phase := range []MachineUpgradePhase{MachineUpgradeVerifying, MachineUpgradeHandoff} {
		if _, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, phase, "", ""); err != nil {
			t.Fatalf("advance %s: %v", phase, err)
		}
	}
	if _, err := testHandler.MachineUpgradeStore.BeginRollback(
		context.Background(), daemonID, created.ID, "generation-rollback", "candidate_takeover_failed", "candidate did not bind",
	); err != nil {
		t.Fatalf("begin rollback: %v", err)
	}

	rolledBack, err := testHandler.MachineUpgradeStore.AttestRollback(
		context.Background(), daemonID, created.ID, "generation-rollback", claudeID, "v9.9.9", []string{claudeID},
	)
	if err != nil || rolledBack == nil || rolledBack.Phase != MachineUpgradeRolledBack ||
		!sameMachineRuntimeSet(rolledBack.RollbackRuntimeIDs, []string{claudeID}) {
		t.Fatalf("retired-provider rollback proof = %+v err=%v", rolledBack, err)
	}
}

func TestMachineUpgradeFailedRollbackRetainsGenerationAndIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	acceptReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/machine-upgrades/"+created.ID+"/accept", map[string]string{
		"generation_id": "generation-target", "cli_version": "v9.9.9", "resolved_target": "v10.0.0",
	})
	acceptReq = withRouteParams(acceptReq, "runtimeId", firstRuntimeID, "upgradeId", created.ID)
	acceptW := httptest.NewRecorder()
	testHandler.AcceptMachineUpgrade(acceptW, acceptReq)
	if acceptW.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", acceptW.Code, acceptW.Body.String())
	}
	for _, phase := range []MachineUpgradePhase{MachineUpgradeVerifying, MachineUpgradeHandoff} {
		if _, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, phase, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testHandler.MachineUpgradeStore.BeginRollback(context.Background(), daemonID, created.ID, "generation-rollback", "candidate_takeover_failed", "candidate did not bind"); err != nil {
		t.Fatal(err)
	}
	failed, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeFailed, "rollback_restore_failed", "source binary unavailable")
	if err != nil || failed == nil || failed.Phase != MachineUpgradeFailed || failed.Result == nil || *failed.Result != "failed" || failed.RollbackGeneration == nil || *failed.RollbackGeneration != "generation-rollback" {
		t.Fatalf("failed rollback = %+v, %v", failed, err)
	}
	replayed, err := testHandler.MachineUpgradeStore.Progress(context.Background(), daemonID, created.ID, MachineUpgradeFailed, "rollback_restore_failed", "lost response replay")
	if err != nil || replayed == nil || replayed.Phase != MachineUpgradeFailed || replayed.ErrorCode == nil || *replayed.ErrorCode != "rollback_restore_failed" {
		t.Fatalf("failed rollback replay = %+v, %v", replayed, err)
	}
}

func TestMachineUpgrade_ProjectsCanonicalOperationToEverySiblingRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")

	req := newRequestAsUser(testUserID, http.MethodGet, "/api/runtimes", nil)
	w := httptest.NewRecorder()
	testHandler.ListAgentRuntimes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list runtimes = %d: %s", w.Code, w.Body.String())
	}
	var runtimes []AgentRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &runtimes); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{firstRuntimeID, secondRuntimeID} {
		var found *AgentRuntimeResponse
		for i := range runtimes {
			if runtimes[i].ID == id {
				found = &runtimes[i]
				break
			}
		}
		if found == nil || found.MachineUpgrade == nil {
			t.Fatalf("runtime %s missing machine projection: %+v", id, found)
		}
		if found.MachineUpgrade.ID != created.ID || found.MachineUpgrade.RequestedTarget != "v9.9.9" || found.MachineUpgrade.Phase != MachineUpgradeQueued {
			t.Fatalf("runtime %s projection = %+v", id, found.MachineUpgrade)
		}
	}
}

func TestMachineUpgrade_CanonicalAuthorizationAndCancellationBoundary(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")

	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := newRequestAsUser(plainMemberID, method, "/api/daemons/"+daemonID+"/upgrades/"+created.ID, nil)
		req = withURLParams(req, "daemonId", daemonID, "upgradeId", created.ID)
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			testHandler.GetMachineUpgrade(w, req)
		} else {
			testHandler.CancelMachineUpgrade(w, req)
		}
		want := http.StatusOK
		if method == http.MethodDelete {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatalf("%s by non-owner = %d: %s, want %d", method, w.Code, w.Body.String(), want)
		}
	}

	adminMemberID := createRuntimeLocalSkillTestMember(t, "admin")
	getReq := newRequestAsUser(adminMemberID, http.MethodGet, "/api/daemons/"+daemonID+"/upgrades/"+created.ID, nil)
	getReq = withURLParams(getReq, "daemonId", daemonID, "upgradeId", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetMachineUpgrade(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get queued operation by workspace admin = %d: %s", getW.Code, getW.Body.String())
	}

	cancelReq := newRequestAsUser(adminMemberID, http.MethodDelete, "/api/daemons/"+daemonID+"/upgrades/"+created.ID, nil)
	cancelReq = withURLParams(cancelReq, "daemonId", daemonID, "upgradeId", created.ID)
	cancelW := httptest.NewRecorder()
	testHandler.CancelMachineUpgrade(cancelW, cancelReq)
	if cancelW.Code != http.StatusForbidden {
		t.Fatalf("cancel queued operation by workspace admin = %d: %s", cancelW.Code, cancelW.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID); err != nil {
		t.Fatal(err)
	}
	_, accepted := initiateMachineUpgrade(t, testUserID, daemonID, "v10.0.0")
	if _, err := testPool.Exec(context.Background(), `UPDATE machine_upgrade SET phase = 'starting', result = NULL WHERE id = $1`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	cancelReq = newRequestAsUser(testUserID, http.MethodDelete, "/api/daemons/"+daemonID+"/upgrades/"+accepted.ID, nil)
	cancelReq = withURLParams(cancelReq, "daemonId", daemonID, "upgradeId", accepted.ID)
	cancelW = httptest.NewRecorder()
	testHandler.CancelMachineUpgrade(cancelW, cancelReq)
	if cancelW.Code != http.StatusConflict {
		t.Fatalf("cancel accepted operation = %d: %s", cancelW.Code, cancelW.Body.String())
	}
}

func TestMachineUpgrade_AllowsOnlyComputerOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	computerOwnerID := createRuntimeLocalSkillTestMember(t, "member")
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = $1 WHERE daemon_id = $2`, computerOwnerID, daemonID); err != nil {
		t.Fatal(err)
	}
	workspaceAdminID := createRuntimeLocalSkillTestMember(t, "admin")
	for label, workspaceManagerID := range map[string]string{
		"Workspace owner": testUserID,
		"Workspace admin": workspaceAdminID,
	} {
		nonOwnerReq := newRequestAsUser(workspaceManagerID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{"target_version": "v9.9.9", "request_id": uuid.NewString()})
		nonOwnerReq = withURLParam(nonOwnerReq, "daemonId", daemonID)
		nonOwnerW := httptest.NewRecorder()
		testHandler.CreateMachineUpgrade(nonOwnerW, nonOwnerReq)
		if nonOwnerW.Code != http.StatusForbidden {
			t.Fatalf("canonical create by non-owner %s = %d: %s", label, nonOwnerW.Code, nonOwnerW.Body.String())
		}
	}
	computerOwnerW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if computerOwnerW.Code != http.StatusOK {
		t.Fatalf("canonical create by Computer owner = %d: %s", computerOwnerW.Code, computerOwnerW.Body.String())
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID); err != nil {
		t.Fatal(err)
	}
	pinTestRuntime(t, runtimeID, "0.3.85")
	pinnedW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if pinnedW.Code != http.StatusConflict {
		t.Fatalf("canonical create on pinned runtime = %d: %s", pinnedW.Code, pinnedW.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(pinnedW.Body.Bytes(), &body); err != nil || body["code"] != "runtime_pinned" {
		t.Fatalf("pinned response = %s err=%v", pinnedW.Body.String(), err)
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET pinned_version = NULL, metadata = '{"launched_by":"desktop","capabilities":["machine_upgrade_v1"]}'::jsonb WHERE id = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	desktopW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if desktopW.Code != http.StatusConflict {
		t.Fatalf("canonical create on desktop-managed runtime = %d: %s", desktopW.Code, desktopW.Body.String())
	}
	if err := json.Unmarshal(desktopW.Body.Bytes(), &body); err != nil || body["code"] != "desktop_managed" {
		t.Fatalf("desktop response = %s err=%v", desktopW.Body.String(), err)
	}
}

func TestMachineUpgrade_RuntimeCompatibilityAdaptersShareAndCancelCanonicalOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)

	createReq := newRequestAsUser(testUserID, http.MethodPost, "/api/runtimes/"+firstRuntimeID+"/update", map[string]string{"target_version": "v9.9.9"})
	createReq = withURLParam(createReq, "runtimeId", firstRuntimeID)
	createW := httptest.NewRecorder()
	testHandler.InitiateUpdate(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("compatibility create = %d: %s", createW.Code, createW.Body.String())
	}
	var created UpdateRequest
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.RuntimeID != firstRuntimeID || created.Status != UpdateQueued {
		t.Fatalf("compatibility create response = %+v", created)
	}
	if op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID); err != nil || op == nil || op.Phase != MachineUpgradeQueued {
		t.Fatalf("canonical operation = %+v err=%v", op, err)
	}
	var legacyIntentCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM daemon_runtime_update_intent WHERE runtime_id = ANY($1::text[]::uuid[])`,
		[]string{firstRuntimeID, secondRuntimeID}).Scan(&legacyIntentCount); err != nil {
		t.Fatal(err)
	}
	if legacyIntentCount != 0 {
		t.Fatalf("compatibility adapter created %d legacy runtime intents", legacyIntentCount)
	}

	getReq := newRequestAsUser(testUserID, http.MethodGet, "/api/runtimes/"+secondRuntimeID+"/update/"+created.ID, nil)
	getReq = withURLParams(getReq, "runtimeId", secondRuntimeID, "updateId", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetUpdate(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("sibling compatibility get = %d: %s", getW.Code, getW.Body.String())
	}
	var sibling UpdateRequest
	if err := json.Unmarshal(getW.Body.Bytes(), &sibling); err != nil {
		t.Fatal(err)
	}
	if sibling.ID != created.ID || sibling.RuntimeID != secondRuntimeID || sibling.Status != UpdateQueued {
		t.Fatalf("sibling compatibility projection = %+v", sibling)
	}

	cancelReq := newRequestAsUser(testUserID, http.MethodDelete, "/api/runtimes/"+secondRuntimeID+"/update-intent", nil)
	cancelReq = withURLParam(cancelReq, "runtimeId", secondRuntimeID)
	cancelW := httptest.NewRecorder()
	testHandler.CancelUpdateIntent(cancelW, cancelReq)
	if cancelW.Code != http.StatusOK {
		t.Fatalf("compatibility cancel = %d: %s", cancelW.Code, cancelW.Body.String())
	}
	if op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID); err != nil || op == nil || op.Phase != MachineUpgradeCancelled {
		t.Fatalf("cancelled canonical operation = %+v err=%v", op, err)
	}

	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		req := newRequestAsUser(plainMemberID, method, "/api/runtimes/"+firstRuntimeID+"/update", map[string]string{"target_version": "v10.0.0"})
		if method == http.MethodGet {
			req = newRequestAsUser(plainMemberID, method, "/api/runtimes/"+firstRuntimeID+"/update/"+created.ID, nil)
		}
		if method == http.MethodDelete {
			req = newRequestAsUser(plainMemberID, method, "/api/runtimes/"+firstRuntimeID+"/update-intent", nil)
		}
		req = withURLParams(req, "runtimeId", firstRuntimeID, "updateId", created.ID)
		w := httptest.NewRecorder()
		switch method {
		case http.MethodPost:
			testHandler.InitiateUpdate(w, req)
		case http.MethodGet:
			testHandler.GetUpdate(w, req)
		case http.MethodDelete:
			testHandler.CancelUpdateIntent(w, req)
		}
		if w.Code != http.StatusForbidden {
			t.Fatalf("compatibility %s by non-owner = %d: %s", method, w.Code, w.Body.String())
		}
	}
}

func TestMachineUpgrade_DispatchesComputerUpgradeToOneLiveBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO computer_identity_owner (daemon_id, user_id) VALUES ($1, $2)`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, siblingWorkspaceID, testUserID)

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := r.URL.Query().Get("workspace")
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: workspaceID})
	}))
	t.Cleanup(server.Close)

	dialReady := func(workspaceID string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?workspace="+workspaceID, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		ready, err := json.Marshal(protocol.Message{
			Type: protocol.EventWorkspaceRunnerReady,
			Payload: mustMarshalJSON(protocol.WorkspaceRunnerReadyPayload{
				WorkspaceID: workspaceID, DaemonInstanceID: "instance-" + workspaceID,
				ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAgentProcess},
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for hub.WorkspaceRunnerConnectionCount(daemonID, workspaceID) != 1 {
			if time.Now().After(deadline) {
				t.Fatalf("Binding %s did not become ready", workspaceID)
			}
			time.Sleep(time.Millisecond)
		}
		return conn
	}
	firstConn := dialReady(testWorkspaceID)
	secondConn := dialReady(siblingWorkspaceID)

	local := *testHandler
	local.DaemonHub = hub
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v9.9.9",
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	local.CreateMachineUpgrade(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create machine upgrade = %d: %s", w.Code, w.Body.String())
	}

	readUpgrade := func(conn *websocket.Conn) (protocol.ComputerUpgradePayload, bool) {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return protocol.ComputerUpgradePayload{}, false
		}
		var message protocol.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != protocol.EventComputerUpgrade {
			t.Fatalf("unexpected Binding frame %q", message.Type)
		}
		var payload protocol.ComputerUpgradePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		return payload, true
	}
	first, firstOK := readUpgrade(firstConn)
	second, secondOK := readUpgrade(secondConn)
	if firstOK == secondOK {
		t.Fatalf("live Binding delivery first=%v second=%v, want exactly one current socket", firstOK, secondOK)
	}
	got := first
	if secondOK {
		got = second
	}
	if got.Operation() == "" || got.TargetVersion != "v9.9.9" {
		t.Fatalf("computer:upgrade payload = %+v", got)
	}
}

func TestMachineUpgrade_DispatchesComputerUpgradeToNextLiveBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO computer_identity_owner (daemon_id, user_id) VALUES ($1, $2)`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	firstWorkspaceID, secondWorkspaceID := testWorkspaceID, siblingWorkspaceID
	if firstWorkspaceID > secondWorkspaceID {
		firstWorkspaceID, secondWorkspaceID = secondWorkspaceID, firstWorkspaceID
	}
	bindMachineUpgradeWorkspace(t, daemonID, firstWorkspaceID, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, secondWorkspaceID, testUserID)

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: r.URL.Query().Get("workspace")})
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?workspace="+secondWorkspaceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ready, err := json.Marshal(protocol.Message{
		Type: protocol.EventWorkspaceRunnerReady,
		Payload: mustMarshalJSON(protocol.WorkspaceRunnerReadyPayload{
			WorkspaceID: secondWorkspaceID, DaemonInstanceID: "instance-" + secondWorkspaceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAgentProcess},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount(daemonID, secondWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("live Binding did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	local := *testHandler
	local.DaemonHub = hub
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v9.9.9",
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	local.CreateMachineUpgrade(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create machine upgrade = %d: %s", w.Code, w.Body.String())
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("live Binding did not receive computer:upgrade: %v", err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil || message.Type != protocol.EventComputerUpgrade {
		t.Fatalf("live Binding frame = %+v err=%v", message, err)
	}
}
