package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Training governance API coverage (Task 18, spec 14.1): the owner/admin
// gate on every route, the explicit CAS grant transitions, the global
// switches, and one full manifest lifecycle (select -> export -> execute ->
// consume) over a real published, rewarded segment in the faithful handler
// schema. Selection/exclusion semantics live in the service test matrix;
// these tests pin the HTTP surface.

// trainingGovWorkspace builds a dedicated workspace with a dedicated owner
// and the shared test user as a PLAIN member, plus the migration-472 grant
// row (pending_owner_ack, exactly like the migration backfill).
func trainingGovWorkspace(t *testing.T) (workspaceID pgtype.UUID, ownerID string) {
	t.Helper()
	workspaceID = createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	// The handler schema migrates before any workspace exists, so the 472
	// backfill cannot cover this workspace; bootstrap it the way a new
	// workspace would (tenant default off).
	ctx := context.Background()
	var owner string
	if err := testPool.QueryRow(ctx, `
		SELECT user_id::text FROM member
		WHERE workspace_id=$1 AND role='owner' LIMIT 1`, workspaceID).Scan(&owner); err != nil {
		t.Fatalf("load training governance owner: %v", err)
	}
	if _, err := testHandler.TrainingGovernance.BootstrapNewWorkspaceGrant(ctx,
		workspaceID.String(), false, "test:bootstrap"); err != nil {
		t.Fatalf("bootstrap training grant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM interaction_dag_training_grant WHERE workspace_id=$1`, workspaceID)
	})
	return workspaceID, owner
}

// mustAckTrainingGrant guarantees the workspace has an ACTIVE tenant grant:
// the handler schema migrates before any workspace exists, so the 472
// backfill cannot cover fixture workspaces — bootstrap one first (exactly
// the new-workspace default), then ack at version 0. Re-acks on an already
// active grant are tolerated.
func mustAckTrainingGrant(t *testing.T, gov *service.TrainingGovernanceService, ctx context.Context, workspaceID string) {
	t.Helper()
	_, err := gov.AckTenantGrant(ctx, workspaceID, "test", 0)
	if err == nil || errors.Is(err, service.ErrTrainingGrantVersion) {
		return
	}
	if !errors.Is(err, service.ErrTrainingGrantNotFound) {
		t.Fatalf("ack tenant grant: %v", err)
	}
	if _, err := gov.BootstrapNewWorkspaceGrant(ctx, workspaceID, false, "test:bootstrap"); err != nil {
		t.Fatalf("bootstrap training grant: %v", err)
	}
	if _, err := gov.AckTenantGrant(ctx, workspaceID, "test", 0); err != nil &&
		!errors.Is(err, service.ErrTrainingGrantVersion) {
		t.Fatalf("ack tenant grant after bootstrap: %v", err)
	}
}

// trainingGovRequest builds a request as userID for one training route.
func trainingGovRequest(t *testing.T, method, path, userID string, body any, params ...string) *http.Request {
	t.Helper()
	return withRouteParams(newRequestAs(userID, method, path, body), params...)
}

// trainingGovCall invokes one handler method directly with route params.
func trainingGovCall(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, path, userID string, body any, params ...string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, trainingGovRequest(t, method, path, userID, body, params...))
	return rec
}

// restoreTrainingPolicy snaps the global singleton policy back after a test
// toggles it, so later handler tests start from the shipped defaults. The
// reward policy version is monotonic by design and is NOT restored.
func restoreTrainingPolicy(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	policy, err := testHandler.TrainingGovernance.TrainingPolicy(ctx)
	if err != nil {
		t.Fatalf("read training policy: %v", err)
	}
	selection, execution := policy.SelectionEnabled, policy.ExecutionEnabled
	agentCap, channelCap, workspaceCap := policy.PerAgentSampleCap, policy.PerChannelSampleCap, policy.PerWorkspaceSampleCap
	t.Cleanup(func() {
		_, err := testHandler.TrainingGovernance.SetTrainingPolicy(context.Background(),
			service.TrainingPolicyPatch{
				SelectionEnabled: &selection, ExecutionEnabled: &execution,
				PerAgentSampleCap: &agentCap, PerChannelSampleCap: &channelCap,
				PerWorkspaceSampleCap: &workspaceCap,
			}, "test:restore")
		if err != nil {
			t.Errorf("restore training policy: %v", err)
		}
	})
}

// seedPublishedTrainableSegment drives the REAL canonical pipeline in the
// faithful handler schema: agent + inbox task + task message, one visible
// message boundary, one publish transaction, then the step reward that makes
// the segment reward-available. Returns the segment id and the agent id.
func seedPublishedTrainableSegment(t *testing.T, workspaceID pgtype.UUID, label string) (segmentID, agentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, NULL, $2, 'cloud', 'training_gov_test', 'online', 'training governance fixture', '{}'::jsonb, now())
		RETURNING id::text`, workspaceID, "training-gov-runtime-"+label).Scan(&runtimeID); err != nil {
		t.Fatalf("seed training runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, instructions)
		VALUES ($1, $2, $3, 'local', '{}'::jsonb, $4::uuid, 'training governance fixture agent')
		RETURNING id::text`, workspaceID, "training-gov-agent-"+label,
		"Training Governance "+label, runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("seed training agent: %v", err)
	}
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, channel_id, agent_id, reason)
		VALUES ($1, $2, $3, $4::uuid, 'training')`, taskID, workspaceID, channelID, agentID); err != nil {
		t.Fatalf("seed training source task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message (task_id, seq, content, input, output, type)
		VALUES ($1, 1, $2, $3::jsonb, $4, 'user')`,
		taskID, "synthetic training governance payload "+label,
		`{"f":"`+label+`"}`, "synthetic training output"); err != nil {
		t.Fatalf("seed training task message: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin boundary tx: %v", err)
	}
	boundary, err := service.NewUniversalInteractionDAG().RecordBoundaryTx(ctx, db.New(tx), tx, service.DAGBoundaryInput{
		WorkspaceID:  workspaceID,
		Task:         db.AgentInboxEvent{ID: taskID, WorkspaceID: workspaceID, ChannelID: channelID},
		BoundaryKind: service.DAGBoundaryVisible, CloseActionKind: service.DAGCloseMessage,
		EndSeq: 1, ActionKey: taskID.String() + ":" + label, ChannelID: channelID,
		ActionID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		RouteGeneration: 1, MemoryTypeAtEvent: "graph",
	})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("record training boundary: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit training boundary: %v", err)
	}
	segmentID = boundary.SegmentID

	// Publish through the real transaction; the shared schema accumulates
	// pending rows from earlier tests, so drain until THIS segment lands.
	publisher := service.NewInteractionDAGPublisher(testPool)
	for attempt := 0; ; attempt++ {
		published, err := publisher.PublishClaim(ctx, 10)
		if err != nil {
			t.Fatalf("publish claim: %v", err)
		}
		var status string
		if err := testPool.QueryRow(ctx,
			`SELECT content_status FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).Scan(&status); err != nil {
			t.Fatalf("load training segment: %v", err)
		}
		if status == "published" {
			break
		}
		if published == 0 || attempt >= 200 {
			t.Fatalf("training segment %s never published (status=%s)", segmentID, status)
		}
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_step_reward (segment_id, seq, score, rationale)
		VALUES ($1, 1, 0.75, 'training governance handler fixture')`, segmentID); err != nil {
		t.Fatalf("seed training step reward: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		// Best-effort, leaves-first: training rows, DAG rows, then the
		// fixture identities. The dedicated workspace cleanup removes
		// whatever remains unreachable.
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_step_reward WHERE segment_id=$1`, segmentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_training_sample WHERE workspace_id=$1`, workspaceID)
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_training_execution WHERE manifest_id IN (SELECT manifest_id FROM interaction_dag_training_manifest WHERE workspace_id=$1)`, workspaceID)
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_training_manifest WHERE workspace_id=$1`, workspaceID)
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_publish_outbox WHERE workspace_id=$1`, workspaceID)
		_, _ = testPool.Exec(cctx, `DELETE FROM interaction_dag_segment WHERE workspace_id=$1`, workspaceID)
		_, _ = testPool.Exec(cctx, `DELETE FROM task_message WHERE task_id=$1`, taskID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_inbox_event WHERE workspace_id=$1 AND (agent_id=$2::uuid OR reason='training_replay')`, workspaceID, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_session WHERE agent_id=$1::uuid`, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent WHERE id=$1::uuid`, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_runtime WHERE id=$1::uuid`, runtimeID)
	})
	return segmentID, agentID
}

// decodeTrainingGovernance reads the {grant, policy} response shape.
func decodeTrainingGovernance(t *testing.T, rec *httptest.ResponseRecorder) (grant service.TrainingGrant, policy service.TrainingPolicy) {
	t.Helper()
	var resp struct {
		Grant  service.TrainingGrant  `json:"grant"`
		Policy service.TrainingPolicy `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode training governance response %q: %v", rec.Body.String(), err)
	}
	return resp.Grant, resp.Policy
}

// The owner/admin gate plus the explicit CAS grant transitions: a plain
// member never reaches the grant; acknowledgement with a stale version is a
// 409; the successful ack activates tenant training; pooled requires its own
// opt-in; revocation reports its blast radius and blocks re-ack.
func TestTrainingGrantHandlerOwnerGateAckOptInRevoke(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID, ownerID := trainingGovWorkspace(t)
	base := "/api/workspaces/" + workspaceID.String() + "/training/grant"
	put := func(userID string, body any) *httptest.ResponseRecorder {
		return trainingGovCall(t, testHandler.UpdateTrainingGrant, http.MethodPut, base, userID, body, "id", workspaceID.String())
	}

	// Plain member: 403 on both surfaces.
	if rec := trainingGovCall(t, testHandler.GetTrainingGrant, http.MethodGet, base, testUserID, nil, "id", workspaceID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("member GET grant = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := put(testUserID, map[string]any{"purpose": "tenant", "action": "ack", "expected_version": 0}); rec.Code != http.StatusForbidden {
		t.Fatalf("member PUT grant = %d: %s", rec.Code, rec.Body.String())
	}

	// Owner GET: the backfill shape (pending_owner_ack) and the shipped
	// switch defaults.
	rec := trainingGovCall(t, testHandler.GetTrainingGrant, http.MethodGet, base, ownerID, nil, "id", workspaceID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("owner GET grant = %d: %s", rec.Code, rec.Body.String())
	}
	grant, policy := decodeTrainingGovernance(t, rec)
	if grant.TenantStatus != "pending_owner_ack" || grant.TenantPolicyVersion != 0 {
		t.Fatalf("bootstrap grant = %+v, want pending_owner_ack v0", grant)
	}
	if grant.PooledStatus != "disabled" {
		t.Fatalf("pooled status = %q, want disabled by default", grant.PooledStatus)
	}
	if policy.SelectionEnabled || policy.ExecutionEnabled {
		t.Fatalf("shipped policy must default both switches off: %+v", policy)
	}

	// Stale CAS: 409, grant untouched.
	if rec := put(ownerID, map[string]any{"purpose": "tenant", "action": "ack", "expected_version": 7}); rec.Code != http.StatusConflict {
		t.Fatalf("stale ack = %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid purpose/action combinations: 400.
	for _, body := range []map[string]any{
		{"purpose": "global", "action": "ack", "expected_version": 0},
		{"purpose": "tenant", "action": "opt_in", "expected_version": 0},
		{"purpose": "pooled", "action": "ack", "expected_version": 0},
	} {
		if rec := put(ownerID, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid %+v = %d: %s", body, rec.Code, rec.Body.String())
		}
	}

	// Successful ack: pending_owner_ack -> active at version 1.
	rec = put(ownerID, map[string]any{"purpose": "tenant", "action": "ack", "expected_version": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner ack = %d: %s", rec.Code, rec.Body.String())
	}
	grant, _ = decodeTrainingGovernance(t, rec)
	if grant.TenantStatus != "active" || grant.TenantPolicyVersion != 1 {
		t.Fatalf("acked grant = %+v, want active v1", grant)
	}

	// Pooled stays disabled until its own explicit opt-in.
	rec = put(ownerID, map[string]any{"purpose": "pooled", "action": "opt_in", "expected_version": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("pooled opt-in = %d: %s", rec.Code, rec.Body.String())
	}
	grant, _ = decodeTrainingGovernance(t, rec)
	if grant.PooledStatus != "active" || grant.PooledPolicyVersion != 1 {
		t.Fatalf("pooled grant = %+v, want active v1", grant)
	}

	// Tenant revocation reports the (empty) blast radius.
	rec = put(ownerID, map[string]any{"purpose": "tenant", "action": "revoke"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant revoke = %d: %s", rec.Code, rec.Body.String())
	}
	var revokeResp struct {
		Invalidated    int64 `json:"invalidated"`
		RevokedSamples int64 `json:"revoked_samples"`
		DeletionLedger int64 `json:"deletion_ledger_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revokeResp); err != nil {
		t.Fatalf("decode revoke report: %v", err)
	}
	if revokeResp.Invalidated != 0 || revokeResp.RevokedSamples != 0 || revokeResp.DeletionLedger != 0 {
		t.Fatalf("revoke report = %+v, want all zero", revokeResp)
	}

	// Revocation is visible on the read surface.
	rec = trainingGovCall(t, testHandler.GetTrainingGrant, http.MethodGet, base, ownerID, nil, "id", workspaceID.String())
	grant, _ = decodeTrainingGovernance(t, rec)
	if grant.TenantStatus != "revoked" {
		t.Fatalf("post-revoke grant = %+v, want revoked", grant)
	}

	// Re-activation after revoke requires a FRESH explicit acknowledgement
	// at the current version (never silent): revoked -> active, version+1.
	rec = put(ownerID, map[string]any{"purpose": "tenant", "action": "ack", "expected_version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit re-ack after revoke = %d: %s", rec.Code, rec.Body.String())
	}
	grant, _ = decodeTrainingGovernance(t, rec)
	if grant.TenantStatus != "active" || grant.TenantPolicyVersion != 2 {
		t.Fatalf("re-acked grant = %+v, want active v2", grant)
	}
}

// The global switch surface: defaults off, member gate, cap validation and
// the monotonic reward policy version.
func TestInteractionDAGTrainingPolicyHandler(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	restoreTrainingPolicy(t)
	workspaceID, ownerID := trainingGovWorkspace(t)
	base := "/api/workspaces/" + workspaceID.String() + "/training/policy"

	if rec := trainingGovCall(t, testHandler.GetTrainingPolicyRoute, http.MethodGet, base, testUserID, nil, "id", workspaceID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("member GET policy = %d: %s", rec.Code, rec.Body.String())
	}
	// Pin both switches off (the migration 472 default) so the read
	// assertion does not depend on which earlier test toggled the global
	// singleton; the off-by-default migration state itself is structural.
	off := false
	if _, err := testHandler.TrainingGovernance.SetTrainingPolicy(context.Background(),
		service.TrainingPolicyPatch{SelectionEnabled: &off, ExecutionEnabled: &off}, "test:reset"); err != nil {
		t.Fatalf("pin switches off: %v", err)
	}
	rec := trainingGovCall(t, testHandler.GetTrainingPolicyRoute, http.MethodGet, base, ownerID, nil, "id", workspaceID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("owner GET policy = %d: %s", rec.Code, rec.Body.String())
	}
	policy := decodeTrainingPolicy(t, rec)
	if policy.SelectionEnabled || policy.ExecutionEnabled {
		t.Fatalf("policy with switches pinned off = %+v", policy)
	}

	// Caps must be positive; unknown fields are rejected.
	if rec := trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, ownerID,
		map[string]any{"per_agent_sample_cap": 0}, "id", workspaceID.String()); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero cap = %d: %s", rec.Code, rec.Body.String())
	}
	badField := map[string]any{"selection_enabledX": true}
	if rec := trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, ownerID,
		badField, "id", workspaceID.String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, testUserID,
		map[string]any{"selection_enabled": true}, "id", workspaceID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("member PUT policy = %d: %s", rec.Code, rec.Body.String())
	}

	// Enable both switches with explicit caps; the response reflects them.
	on := true
	rec = trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, ownerID,
		map[string]any{"selection_enabled": on, "execution_enabled": on,
			"per_agent_sample_cap": 50, "per_channel_sample_cap": 500, "per_workspace_sample_cap": 5000},
		"id", workspaceID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("owner PUT policy = %d: %s", rec.Code, rec.Body.String())
	}
	policy = decodeTrainingPolicy(t, rec)
	if !policy.SelectionEnabled || !policy.ExecutionEnabled ||
		policy.PerAgentSampleCap != 50 || policy.PerChannelSampleCap != 500 || policy.PerWorkspaceSampleCap != 5000 {
		t.Fatalf("updated policy = %+v", policy)
	}

	// Reward policy version is monotonic: it only ever rises, so a lower
	// value never rewinds it (baseline-relative because the version is a
	// global monotonic counter shared across the suite).
	bumped := policy.RewardPolicyVersion + 2
	rec = trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, ownerID,
		map[string]any{"reward_policy_version": bumped}, "id", workspaceID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("reward version bump = %d: %s", rec.Code, rec.Body.String())
	}
	policy = decodeTrainingPolicy(t, rec)
	if policy.RewardPolicyVersion != bumped {
		t.Fatalf("reward version = %d, want %d", policy.RewardPolicyVersion, bumped)
	}
	rec = trainingGovCall(t, testHandler.UpdateTrainingPolicyRoute, http.MethodPut, base, ownerID,
		map[string]any{"reward_policy_version": bumped - 1}, "id", workspaceID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("reward version rewind attempt = %d: %s", rec.Code, rec.Body.String())
	}
	policy = decodeTrainingPolicy(t, rec)
	if policy.RewardPolicyVersion != bumped {
		t.Fatalf("reward version rewound to %d, want monotonic %d", policy.RewardPolicyVersion, bumped)
	}
}

func decodeTrainingPolicy(t *testing.T, rec *httptest.ResponseRecorder) service.TrainingPolicy {
	t.Helper()
	var resp struct {
		Policy service.TrainingPolicy `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode training policy response %q: %v", rec.Body.String(), err)
	}
	return resp.Policy
}

// One full manifest lifecycle over the HTTP surface with a real published,
// rewarded segment: member 403s, validation 400s, the disabled kill switch
// 503, selection 201, export 200, execution gated on the second switch, the
// distinct replay task, terminal consume, and the post-terminal 409s.
func TestInteractionDAGTrainingManifestHandlerExportConsumeLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	restoreTrainingPolicy(t)
	ctx := context.Background()
	workspaceID, ownerID := trainingGovWorkspace(t)
	ws := workspaceID.String()
	segmentID, agentID := seedPublishedTrainableSegment(t, workspaceID, "manifest-lifecycle")

	// Activate the tenant grant via the HTTP ack.
	if rec := trainingGovCall(t, testHandler.UpdateTrainingGrant, http.MethodPut,
		"/api/workspaces/"+ws+"/training/grant", ownerID,
		map[string]any{"purpose": "tenant", "action": "ack", "expected_version": 0},
		"id", ws); rec.Code != http.StatusOK {
		t.Fatalf("tenant ack = %d: %s", rec.Code, rec.Body.String())
	}

	manifestBase := "/api/workspaces/" + ws + "/training/manifests"

	// Pin the selection switch off: the kill-switch 503 below must not
	// depend on which earlier test left the global singleton enabled.
	selectionOff := false
	if _, err := testHandler.TrainingGovernance.SetTrainingPolicy(ctx,
		service.TrainingPolicyPatch{SelectionEnabled: &selectionOff}, "test:reset"); err != nil {
		t.Fatalf("pin selection off: %v", err)
	}

	// Plain member and validation shapes on the selection route.
	if rec := trainingGovCall(t, testHandler.CreateTrainingManifest, http.MethodPost, manifestBase, testUserID,
		map[string]any{"purpose": "tenant"}, "id", ws); rec.Code != http.StatusForbidden {
		t.Fatalf("member POST manifest = %d: %s", rec.Code, rec.Body.String())
	}
	for _, body := range []map[string]any{
		{"purpose": "global"},
		{"purpose": "tenant", "family": "tensors"},
	} {
		if rec := trainingGovCall(t, testHandler.CreateTrainingManifest, http.MethodPost, manifestBase, ownerID,
			body, "id", ws); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid selection %+v = %d: %s", body, rec.Code, rec.Body.String())
		}
	}

	// Global kill switch off: selection is 503 fail-closed.
	if rec := trainingGovCall(t, testHandler.CreateTrainingManifest, http.MethodPost, manifestBase, ownerID,
		map[string]any{"purpose": "tenant", "family": "segments"}, "id", ws); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled selection = %d: %s", rec.Code, rec.Body.String())
	}

	// Enable selection; selection now freezes the segment snapshot.
	on := true
	if _, err := testHandler.TrainingGovernance.SetTrainingPolicy(ctx,
		service.TrainingPolicyPatch{SelectionEnabled: &on}, "test:operator"); err != nil {
		t.Fatalf("enable selection: %v", err)
	}
	rec := trainingGovCall(t, testHandler.CreateTrainingManifest, http.MethodPost, manifestBase, ownerID,
		map[string]any{"purpose": "tenant", "family": "segments", "limit": 5}, "id", ws)
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner POST manifest = %d: %s", rec.Code, rec.Body.String())
	}
	var manifest service.TrainingManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Status != "selected" || manifest.ItemCount != 1 || manifest.Purpose != "tenant" {
		t.Fatalf("manifest = %+v, want selected tenant with 1 item", manifest)
	}
	if len(manifest.Items) != 1 || manifest.Items[0].Key != segmentID ||
		manifest.Items[0].RewardStatus != "available" || manifest.Items[0].Hash == "" {
		t.Fatalf("manifest items = %+v, want the rewarded segment with hash", manifest.Items)
	}

	// List and detail surfaces.
	rec = trainingGovCall(t, testHandler.ListTrainingManifestsRoute, http.MethodGet,
		manifestBase+"?purpose=tenant", ownerID, nil, "id", ws)
	if rec.Code != http.StatusOK {
		t.Fatalf("list manifests = %d: %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Manifests []*service.TrainingManifest `json:"manifests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil || len(listResp.Manifests) != 1 {
		t.Fatalf("decode manifest list: err=%v manifests=%+v", err, listResp.Manifests)
	}
	rec = trainingGovCall(t, testHandler.GetTrainingManifestRoute, http.MethodGet,
		manifestBase+"/"+manifest.ManifestID, ownerID, nil, "id", ws, "manifestId", manifest.ManifestID)
	if rec.Code != http.StatusOK {
		t.Fatalf("get manifest = %d: %s", rec.Code, rec.Body.String())
	}
	var detail service.TrainingManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || len(detail.Items) != 1 {
		t.Fatalf("decode manifest detail: err=%v items=%d", err, len(detail.Items))
	}

	// Export: member 403, then the exactly-once selected -> exported.
	exportPath := manifestBase + "/" + manifest.ManifestID + "/export"
	if rec := trainingGovCall(t, testHandler.ExportTrainingManifestRoute, http.MethodPost,
		exportPath, testUserID, nil, "id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusForbidden {
		t.Fatalf("member export = %d: %s", rec.Code, rec.Body.String())
	}
	rec = trainingGovCall(t, testHandler.ExportTrainingManifestRoute, http.MethodPost,
		exportPath, ownerID, nil, "id", ws, "manifestId", manifest.ManifestID)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner export = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Status != "exported" {
		t.Fatalf("exported manifest = %q (%v)", detail.Status, err)
	}
	// Second export: 409 state conflict.
	if rec := trainingGovCall(t, testHandler.ExportTrainingManifestRoute, http.MethodPost,
		exportPath, ownerID, nil, "id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusConflict {
		t.Fatalf("double export = %d: %s", rec.Code, rec.Body.String())
	}

	// Execution stays blocked while the second switch is off.
	executePath := manifestBase + "/" + manifest.ManifestID + "/execute"
	if rec := trainingGovCall(t, testHandler.BeginTrainingExecutionRoute, http.MethodPost,
		executePath, ownerID, map[string]any{"agent_id": agentID},
		"id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("execution with switch off = %d: %s", rec.Code, rec.Body.String())
	}

	// Enable execution: the distinct replay task appears.
	if _, err := testHandler.TrainingGovernance.SetTrainingPolicy(ctx,
		service.TrainingPolicyPatch{ExecutionEnabled: &on}, "test:operator"); err != nil {
		t.Fatalf("enable execution: %v", err)
	}
	rec = trainingGovCall(t, testHandler.BeginTrainingExecutionRoute, http.MethodPost,
		executePath, ownerID, map[string]any{"agent_id": agentID},
		"id", ws, "manifestId", manifest.ManifestID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner execute = %d: %s", rec.Code, rec.Body.String())
	}
	var execution service.TrainingExecution
	if err := json.Unmarshal(rec.Body.Bytes(), &execution); err != nil {
		t.Fatalf("decode execution: %v", err)
	}
	if execution.TrainingTaskID == "" || execution.ManifestID != manifest.ManifestID || execution.Status != "started" {
		t.Fatalf("execution = %+v, want started with replay task", execution)
	}
	var replayReason string
	if err := testPool.QueryRow(ctx,
		`SELECT reason FROM agent_inbox_event WHERE id=$1`, execution.TrainingTaskID).Scan(&replayReason); err != nil {
		t.Fatalf("load replay task: %v", err)
	}
	if replayReason != "training_replay" {
		t.Fatalf("replay task reason = %q, want training_replay", replayReason)
	}

	// Selection-audit and deletion-ledger read surfaces for the owner.
	if rec := trainingGovCall(t, testHandler.ListTrainingDeletionLedgerRoute, http.MethodGet,
		"/api/workspaces/"+ws+"/training/deletion-ledger", ownerID, nil, "id", ws); rec.Code != http.StatusOK {
		t.Fatalf("deletion ledger = %d: %s", rec.Code, rec.Body.String())
	}
	rec = trainingGovCall(t, testHandler.AuditTrainingSelectionRoute, http.MethodGet,
		"/api/workspaces/"+ws+"/training/selection-audit", ownerID, nil, "id", ws)
	if rec.Code != http.StatusOK {
		t.Fatalf("selection audit = %d: %s", rec.Code, rec.Body.String())
	}
	var auditResp struct {
		Segments []service.TrainingSelectionAudit `json:"segments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("decode selection audit: %v", err)
	}
	found := false
	for _, entry := range auditResp.Segments {
		if entry.SegmentID == segmentID {
			found = true
			if entry.Reason != "already_sampled" {
				t.Fatalf("selected segment audit reason = %q, want already_sampled", entry.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("selection audit does not cover segment %s: %+v", segmentID, auditResp.Segments)
	}

	// Terminal consume, then the exactly-once 409s.
	consumePath := manifestBase + "/" + manifest.ManifestID + "/consume"
	if rec := trainingGovCall(t, testHandler.ConsumeTrainingExecutionRoute, http.MethodPost,
		consumePath, ownerID, nil, "id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusOK {
		t.Fatalf("owner consume = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := trainingGovCall(t, testHandler.ConsumeTrainingExecutionRoute, http.MethodPost,
		consumePath, ownerID, nil, "id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusConflict {
		t.Fatalf("double consume = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := trainingGovCall(t, testHandler.BeginTrainingExecutionRoute, http.MethodPost,
		executePath, ownerID, map[string]any{"agent_id": agentID},
		"id", ws, "manifestId", manifest.ManifestID); rec.Code != http.StatusConflict {
		t.Fatalf("execute after consume = %d: %s", rec.Code, rec.Body.String())
	}

	// Re-selection after consumption finds nothing new: 409 no new samples.
	if rec := trainingGovCall(t, testHandler.CreateTrainingManifest, http.MethodPost, manifestBase, ownerID,
		map[string]any{"purpose": "tenant", "family": "segments"}, "id", ws); rec.Code != http.StatusConflict {
		t.Fatalf("re-selection of consumed sample = %d: %s", rec.Code, rec.Body.String())
	}
}
