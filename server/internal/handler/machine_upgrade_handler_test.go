package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func createMachineUpgradeSiblingRuntimes(t *testing.T, ownerID string) (string, string, string) {
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
	first, second := create("claude"), create("codex")
	// Machine Upgrade is keyed by daemon ID rather than runtime FKs, so remove
	// its test rows before the fixture tears down the owning test user.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID)
	})
	return first, second, daemonID
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

func TestMachineUpgrade_CapableHeartbeatAcceptsAndRequiresEverySiblingAttestation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)

	firstAck, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil || firstAck.PendingMachineUpgrade == nil || firstAck.PendingMachineUpgrade.ID != created.ID {
		t.Fatalf("first capable heartbeat claim = %+v err=%v", firstAck, err)
	}
	secondAck, _, err := testHandler.processHeartbeat(context.Background(), secondRuntime, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondAck.PendingMachineUpgrade != nil {
		t.Fatalf("sibling heartbeat also claimed %+v", secondAck.PendingMachineUpgrade)
	}

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

func TestMachineUpgrade_LegacyHeartbeatBootstrapsV0413AndRequiresFullTargetReregistration(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, secondRuntimeID, daemonID := createLegacyMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v0.4.14")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	secondRuntime := getMachineUpgradeRuntime(t, secondRuntimeID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_runtime_update WHERE id = $1`, created.ID)
	})

	ack, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ack.PendingMachineUpgrade != nil || ack.PendingUpdate == nil || ack.PendingUpdate.ID != created.ID || ack.PendingUpdate.TargetVersion != "v0.4.14" {
		t.Fatalf("legacy heartbeat acknowledgement = %+v, want same-ID PendingUpdate", ack)
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.Phase != MachineUpgradeStarting || op.AcceptedAt != nil || op.AcceptedGeneration == nil || *op.AcceptedGeneration != legacyMachineUpgradeAcceptanceMarker || !sameMachineRuntimeSet(op.AcceptedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("legacy claim = %+v err=%v", op, err)
	}

	report := func(status string) {
		req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/update/"+created.ID+"/result", map[string]string{"status": status})
		req = withRouteParams(req, "runtimeId", firstRuntimeID, "updateId", created.ID)
		w := httptest.NewRecorder()
		testHandler.ReportUpdateResult(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("legacy %s report = %d: %s", status, w.Code, w.Body.String())
		}
	}
	report("running")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeStaging || op.AcceptedAt == nil || op.ResolvedTarget == nil || *op.ResolvedTarget != "v0.4.14" {
		t.Fatalf("legacy running receipt = %+v", op)
	}
	report("ready_to_apply")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeHandoff {
		t.Fatalf("legacy ready receipt = %+v", op)
	}

	// One updated sibling is never enough to complete an old-protocol update.
	testHandler.attestLegacyMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), firstRuntime, "v0.4.14")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeConverging || len(op.AttestedRuntimeIDs) != 1 {
		t.Fatalf("one legacy successor attestation = %+v", op)
	}
	testHandler.attestLegacyMachineUpgradeRegistration(httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil), secondRuntime, "v0.4.14")
	op, _ = testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if op.Phase != MachineUpgradeCompleted || op.Result == nil || *op.Result != "completed" || !sameMachineRuntimeSet(op.AttestedRuntimeIDs, []string{firstRuntimeID, secondRuntimeID}) {
		t.Fatalf("legacy full successor proof = %+v", op)
	}
}

func TestMachineUpgrade_LegacyBootstrapProjectsDaemonFailure(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createLegacyMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v0.4.14")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_runtime_update WHERE id = $1`, created.ID)
	})
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemon/runtimes/"+firstRuntimeID+"/update/"+created.ID+"/result", map[string]string{
		"status": "failed", "error": "checksum mismatch",
	})
	req = withRouteParams(req, "runtimeId", firstRuntimeID, "updateId", created.ID)
	w := httptest.NewRecorder()
	testHandler.ReportUpdateResult(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy failure report = %d: %s", w.Code, w.Body.String())
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.Phase != MachineUpgradeFailed || op.ErrorCode == nil || *op.ErrorCode != "legacy_update_failed" || op.ErrorMessage == nil || *op.ErrorMessage != "checksum mismatch" {
		t.Fatalf("legacy failure projection = %+v err=%v", op, err)
	}
}

func TestMachineUpgrade_LegacyBootstrapTimesOutWithoutReceipt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstRuntimeID, _, daemonID := createLegacyMachineUpgradeSiblingRuntimes(t, testUserID)
	_, created := initiateMachineUpgrade(t, testUserID, daemonID, "v0.4.14")
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_runtime_update WHERE id = $1`, created.ID)
	})
	if _, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE machine_upgrade SET updated_at = now() - interval '121 seconds' WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	op, err := testHandler.MachineUpgradeStore.Get(context.Background(), daemonID, created.ID)
	if err != nil || op == nil || op.Phase != MachineUpgradeTimeout || op.ErrorCode == nil || *op.ErrorCode != "legacy_update_timeout" {
		t.Fatalf("legacy receipt timeout = %+v err=%v", op, err)
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
	runtime := getMachineUpgradeRuntime(t, runtimeID)
	ack, _, err := testHandler.processHeartbeat(context.Background(), runtime, false, false, "", nil)
	if err != nil || ack.PendingMachineUpgrade == nil {
		t.Fatalf("latest heartbeat ack = %+v, %v", ack, err)
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
	if err != nil || ack.PendingMachineUpgrade == nil {
		t.Fatalf("capable runtime did not receive machine action: %+v err=%v", ack, err)
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
	firstRuntime := getMachineUpgradeRuntime(t, firstRuntimeID)
	ack, _, err := testHandler.processHeartbeat(context.Background(), firstRuntime, false, false, "", nil)
	if err != nil || ack.PendingMachineUpgrade == nil {
		t.Fatalf("claim = %+v err=%v", ack, err)
	}
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
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s by non-owner = %d: %s", method, w.Code, w.Body.String())
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
	if cancelW.Code != http.StatusOK {
		t.Fatalf("cancel queued operation by workspace admin = %d: %s", cancelW.Code, cancelW.Body.String())
	}
	var cancelled MachineUpgrade
	if err := json.Unmarshal(cancelW.Body.Bytes(), &cancelled); err != nil || cancelled.Phase != MachineUpgradeCancelled || cancelled.Result == nil || *cancelled.Result != "cancelled" {
		t.Fatalf("cancel response = %+v err=%v", cancelled, err)
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

func TestMachineUpgrade_AllowsComputerOwnerOrWorkspaceOwnerAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")

	nonOwnerReq := newRequestAsUser(plainMemberID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{"target_version": "v9.9.9", "request_id": uuid.NewString()})
	nonOwnerReq = withURLParam(nonOwnerReq, "daemonId", daemonID)
	nonOwnerW := httptest.NewRecorder()
	testHandler.CreateMachineUpgrade(nonOwnerW, nonOwnerReq)
	if nonOwnerW.Code != http.StatusForbidden {
		t.Fatalf("canonical create by non-owner = %d: %s", nonOwnerW.Code, nonOwnerW.Body.String())
	}
	workspaceOwnerMemberID := createRuntimeLocalSkillTestMember(t, "owner")
	workspaceOwnerW, _ := initiateMachineUpgrade(t, workspaceOwnerMemberID, daemonID, "v9.9.9")
	if workspaceOwnerW.Code != http.StatusOK {
		t.Fatalf("canonical create by workspace owner = %d: %s", workspaceOwnerW.Code, workspaceOwnerW.Body.String())
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID); err != nil {
		t.Fatal(err)
	}
	adminMemberID := createRuntimeLocalSkillTestMember(t, "admin")
	adminW, _ := initiateMachineUpgrade(t, adminMemberID, daemonID, "v9.9.9")
	if adminW.Code != http.StatusOK {
		t.Fatalf("canonical create by workspace admin = %d: %s", adminW.Code, adminW.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `DELETE FROM machine_upgrade WHERE daemon_id = $1`, daemonID); err != nil {
		t.Fatal(err)
	}
	pinTestRuntime(t, runtimeID, "0.3.85")
	pinnedW, _ := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
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
	desktopW, _ := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
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
