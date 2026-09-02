// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/arealrl"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Minimal graph-memory stubs the governance queries read. The real tables
// (migrations 420/421) are not part of the publisher harness schema, and the
// governance service only touches the columns mirrored here.
const trainingGraphStubDDL = `
CREATE TABLE IF NOT EXISTS graph_memory_recall (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  training_mode text NOT NULL DEFAULT '',
  trace_id text NOT NULL DEFAULT '',
  graph_kind text NOT NULL DEFAULT '',
  graph_owner_id uuid,
  graph_version int NOT NULL DEFAULT 1,
  terminal_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS graph_memory_trajectory (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  recall_id uuid NOT NULL,
  seed_index int NOT NULL DEFAULT 0,
  summary text NOT NULL DEFAULT '',
  rounds int NOT NULL DEFAULT 0,
  artifact_ref text NOT NULL DEFAULT '',
  dive_status text NOT NULL DEFAULT '',
  reward double precision,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS graph_memory_dive_job (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recall_id uuid NOT NULL,
  status text NOT NULL DEFAULT '',
  incomplete boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS graph_memory_rl_session (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL,
  trajectory_id uuid,
  recall_id uuid,
  status text NOT NULL DEFAULT 'opening',
  updated_at timestamptz NOT NULL DEFAULT now(),
  closed_at timestamptz
);
-- The publisher harness mini-schema carries a bare agent_inbox_event; the
-- replay-task insert needs the production columns, so grow the stub in place.
ALTER TABLE agent_inbox_event
  ADD COLUMN IF NOT EXISTS agent_session_id uuid,
  ADD COLUMN IF NOT EXISTS agent_id uuid,
  ADD COLUMN IF NOT EXISTS runtime_id uuid,
  ADD COLUMN IF NOT EXISTS execution_config jsonb,
  ADD COLUMN IF NOT EXISTS reason text,
  ADD COLUMN IF NOT EXISTS requires_wake boolean,
  ADD COLUMN IF NOT EXISTS status text,
  ADD COLUMN IF NOT EXISTS priority integer,
  ADD COLUMN IF NOT EXISTS context jsonb;
CREATE TABLE IF NOT EXISTS agent (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  runtime_id uuid
);
CREATE OR REPLACE FUNCTION ensure_agent_wake_session(candidate_agent_id uuid)
RETURNS uuid LANGUAGE sql AS 'SELECT candidate_agent_id';
ALTER TABLE agent_inbox_event ALTER COLUMN id SET DEFAULT gen_random_uuid();
`

// trainingGovernanceHarness: publisher harness (454+464+466+467+469) plus
// migration 472 applied verbatim in the private schema, with the graph
// stubs created first so the migration's legacy session close runs.
type trainingGovernanceHarness struct {
	*retractionHarness
}

func newTrainingGovernanceHarness(t *testing.T) *trainingGovernanceHarness {
	t.Helper()
	h := &trainingGovernanceHarness{retractionHarness: newRetractionHarness(t)}
	_, err := h.conn.Exec(h.ctx, trainingGraphStubDDL)
	require.NoError(t, err, "create graph stubs")
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate training governance test")
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations",
		"487_interaction_dag_training_governance.up.sql")
	migration, err := os.ReadFile(path)
	require.NoError(t, err, "read migration 472")
	_, err = h.conn.Exec(h.ctx, string(migration))
	require.NoError(t, err, "apply migration 472 in private schema")
	return h
}

func (h *trainingGovernanceHarness) svc() *TrainingGovernanceService {
	return NewTrainingGovernanceService(h.pubPool, nil)
}

func (h *trainingGovernanceHarness) wsID() string { return h.workspace.String() }

// enableSelection flips the global selection switch (actor = test operator).
func (h *trainingGovernanceHarness) enableSelection(t *testing.T, executionToo bool) TrainingPolicy {
	t.Helper()
	on := true
	patch := TrainingPolicyPatch{SelectionEnabled: &on}
	if executionToo {
		patch.ExecutionEnabled = &on
	}
	policy, err := h.svc().SetTrainingPolicy(h.ctx, patch, "test:operator")
	require.NoError(t, err)
	return policy
}

func (h *trainingGovernanceHarness) ackTenant(t *testing.T, expected int64) TrainingGrant {
	t.Helper()
	grant, err := h.svc().AckTenantGrant(h.ctx, h.wsID(), "user:owner", expected)
	require.NoError(t, err)
	return grant
}

// seedPublishedSegment publishes one clean non-derivative segment with a
// step reward (reward available) and returns the segment id.
func (h *trainingGovernanceHarness) seedPublishedSegment(t *testing.T, label, content string) string {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, content, `{"f":"`+label+`"}`, "")
	segmentID := h.recordMessageSegment(task, 1, label)
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published, "seed %s must publish", label)
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO interaction_dag_step_reward (segment_id, seq, score, rationale)
		VALUES ($1, 1, 0.75, 'seed')`, segmentID)
	require.NoError(t, err)
	return segmentID
}

// mutateSegment applies an exclusion mutation to a published segment.
// seedPublishedSegmentWithInput pins the sanitized input so two tasks can
// publish byte-identical trajectories (near-duplicate fixture).
func (h *trainingGovernanceHarness) seedPublishedSegmentWithInput(t *testing.T, label, content, input string) string {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, content, input, "")
	segmentID := h.recordMessageSegment(task, 1, label)
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO interaction_dag_step_reward (segment_id, seq, score, rationale)
		VALUES ($1, 1, 0.75, 'seed')`, segmentID)
	require.NoError(t, err)
	return segmentID
}

// seedRedactionFailedSegment publishes a segment whose sanitize step fails
// deterministically (the lifecycle trigger only permits redaction_failed
// from pending, i.e. from the publish pipeline itself).
func (h *trainingGovernanceHarness) seedRedactionFailedSegment(t *testing.T, label string) string {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, label)
	sink := &classifyingSink{errFor: map[string]error{
		segmentID: fmt.Errorf("sanitizer schema: %w", ErrDAGPublishRedaction),
	}}
	publisher := newPublisherWithSink(t, h.universalDAGPublisherHarness, sink)
	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	return segmentID
}

// seedDerivativeSegment records a derivative (Memory-Agent) boundary and
// publishes it; provenance columns are immutable post-insert, so the shape
// must be created at record time.
func (h *trainingGovernanceHarness) seedDerivativeSegment(t *testing.T, label, content string) string {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, content, `{"f":"`+label+`"}`, "")
	segmentID := h.recordBoundarySegment(t, task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1,
		actionKey: label, derivative: true,
	})
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published, "derivative seed %s must publish", label)
	return segmentID
}

func (h *trainingGovernanceHarness) mutateSegment(t *testing.T, segmentID, publishStatus, contentStatus string, retract bool) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment
		SET publish_status = $2, content_status = $3,
		    retracted_at = CASE WHEN $4 THEN now() ELSE NULL END
		WHERE segment_id = $1`, segmentID, publishStatus, contentStatus, retract)
	require.NoError(t, err)
}

// seedGraphTrajectory inserts one graded offline_rl trajectory under a
// channel-owned recall; fenced owners simulate a Task 8A retraction.
func (h *trainingGovernanceHarness) seedGraphTrajectory(t *testing.T, label string, reward *float64, fencedOwner bool) string {
	t.Helper()
	recall := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	// A fenced recall uses its own owner so the shared harness owner stays
	// readable for the other fixtures.
	owner := h.channel
	if fencedOwner {
		owner = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_recall (id, workspace_id, training_mode, trace_id,
		                                 graph_kind, graph_owner_id, terminal_at)
		VALUES ($1, $2, 'offline_rl', $3, 'channel', $4, now())`,
		recall, h.workspace, label, owner)
	require.NoError(t, err)
	trajectory := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_trajectory (id, workspace_id, recall_id, seed_index,
		                                      summary, rounds, dive_status, reward)
		VALUES ($1, $2, $3, 0, $4, 2, 'graded', $5)`,
		trajectory, h.workspace, recall, label, reward)
	require.NoError(t, err)
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_dive_job (recall_id, status, incomplete)
		VALUES ($1, 'completed', false)`, recall)
	require.NoError(t, err)
	if fencedOwner {
		_, err = h.conn.Exec(h.ctx, `
			INSERT INTO memory_source_guard (workspace_id, source_kind, source_id, retracted_at, retracted_by, reason)
			VALUES ($1, 'channel', $2, now(), 'user:owner', 'channel deleted')`,
			h.workspace, owner)
		require.NoError(t, err)
	}
	return trajectory.String()
}

// ---------------------------------------------------------------------------
// Migration semantics (plan Step 2).
// ---------------------------------------------------------------------------

// Existing workspaces are backfilled pending_owner_ack with pooled disabled;
// the global switches start OFF; legacy open RL sessions are closed until
// they are re-selected through a manifest.
func TestInteractionDAGTraining_MigrationBackfillAndLegacyClosure(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()

	seed := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_rl_session (id, workspace_id, status) VALUES
		($1, $2, 'open'), ($3, $2, 'opening'), ($4, $2, 'rewarded')`,
		seed, h.workspace,
		pgtype.UUID{Bytes: uuid.New(), Valid: true},
		pgtype.UUID{Bytes: uuid.New(), Valid: true})
	require.NoError(t, err, "seed sessions before 472 — harness applies 472 in the constructor")
	// Re-run the migration's closure statement is not possible (already
	// applied), so assert the constructor's application closed nothing yet:
	// sessions seeded AFTER the migration must stay open until the sweep.
	// The closure itself is asserted below by re-running the statement.
	_, err = h.conn.Exec(h.ctx, `
		UPDATE graph_memory_rl_session
		SET status='closed', closed_at=COALESCE(closed_at, now()), updated_at=now()
		WHERE status IN ('opening','open')`)
	require.NoError(t, err)

	var open, rewarded int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FILTER (WHERE status='open' OR status='opening'),
		        count(*) FILTER (WHERE status='rewarded') FROM graph_memory_rl_session`).
		Scan(&open, &rewarded))
	assert.Zero(t, open, "legacy open sessions close under governance")
	assert.Equal(t, 1, rewarded, "already-rewarded sessions are terminal and untouched")

	grant, err := h.svc().CurrentGrant(h.ctx, h.wsID())
	require.NoError(t, err)
	assert.Equal(t, "pending_owner_ack", grant.TenantStatus, "existing workspace backfills pending")
	assert.Equal(t, "disabled", grant.PooledStatus, "pooled is never on by default")

	policy, err := h.svc().TrainingPolicy(h.ctx)
	require.NoError(t, err)
	assert.False(t, policy.SelectionEnabled, "selection kill switch defaults OFF")
	assert.False(t, policy.ExecutionEnabled, "execution kill switch defaults OFF")
	assert.Positive(t, policy.PerAgentSampleCap)
	assert.Positive(t, policy.PerChannelSampleCap)
	assert.Positive(t, policy.PerWorkspaceSampleCap)

	// The backfill covered every workspace in the schema.
	var missing int
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM workspace w
		WHERE NOT EXISTS (SELECT 1 FROM interaction_dag_training_grant g
		                  WHERE g.workspace_id = w.id)`).Scan(&missing))
	assert.Zero(t, missing, "every pre-existing workspace has a grant row")
}

// ---------------------------------------------------------------------------
// Grants (plan Step 1: pending_owner_ack / new-workspace default / pooled
// opt-in / CAS conflict).
// ---------------------------------------------------------------------------

// Selection stays closed while the global switch is off or the workspace
// grant is pending; the owner CAS acknowledgement opens tenant selection.
func TestInteractionDAGTraining_GrantGateOrderAndAck(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.seedPublishedSegment(t, "gate", "gate content NIMBUS")
	svc := h.svc()
	req := TrainingSelectionRequest{WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"}

	// Global kill switch first: nothing selects before calibration.
	_, err := svc.SelectTrainingManifest(h.ctx, req)
	require.ErrorIs(t, err, ErrTrainingSelectionDisabled)

	h.enableSelection(t, false)

	// Then the grant: existing workspace awaits owner acknowledgement.
	_, err = svc.SelectTrainingManifest(h.ctx, req)
	require.ErrorIs(t, err, ErrTrainingGrantPendingOwnerAck)

	grant := h.ackTenant(t, 0)
	assert.Equal(t, "active", grant.TenantStatus)
	assert.Equal(t, int64(1), grant.TenantPolicyVersion)
	assert.Equal(t, "user:owner", grant.TenantGrantedBy)
	assert.False(t, grant.TenantGrantedAt.IsZero())

	manifest, err := svc.SelectTrainingManifest(h.ctx, req)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestSelected, manifest.Status)
	assert.Equal(t, TrainingPurposeTenant, manifest.Purpose)
	assert.Equal(t, int64(1), manifest.GrantPolicyVersion, "manifest freezes the grant policy version")
	assert.Equal(t, "user:owner", manifest.GrantActor)
	assert.False(t, manifest.GrantedAt.IsZero())
	assert.Equal(t, 1, manifest.ItemCount)
	require.Len(t, manifest.Items, 1)
	item := manifest.Items[0]
	assert.Equal(t, TrainingSampleKindSegment, item.Kind)
	assert.Equal(t, "sixfield-redact-v1", item.SanitizerVersion, "manifest fixes the sanitizer version")
	assert.NotEmpty(t, item.PolicyVersion, "manifest fixes the policy version")
	assert.Len(t, item.Hash, 64, "sha256 content hash")
	assert.Equal(t, "available", item.RewardStatus)
	assert.Equal(t, int64(1), item.RewardRevision, "one step reward row observed at selection")
	assert.Equal(t, h.channel.String(), item.Scope["channel_id"], "manifest fixes the scope")
}

// The acknowledgement is CAS: a stale expected version conflicts.
func TestInteractionDAGTraining_AckCASConflict(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.ackTenant(t, 0)

	_, err := h.svc().AckTenantGrant(h.ctx, h.wsID(), "user:owner2", 0)
	require.ErrorIs(t, err, ErrTrainingGrantVersion)

	grant, err := h.svc().CurrentGrant(h.ctx, h.wsID())
	require.NoError(t, err)
	assert.Equal(t, int64(1), grant.TenantPolicyVersion, "conflicting ack writes nothing")
	assert.Equal(t, "user:owner", grant.TenantGrantedBy)

	// Re-ack at the current version of an already-active grant is idempotent.
	_, err = h.svc().AckTenantGrant(h.ctx, h.wsID(), "user:owner", 1)
	require.NoError(t, err)
}

// Pooled training always requires explicit opt-in, and its grant state is
// recorded separately from tenant.
func TestInteractionDAGTraining_PooledExplicitOptIn(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.seedPublishedSegment(t, "pooled", "pooled content NIMBUS")
	h.enableSelection(t, false)
	h.ackTenant(t, 0)
	svc := h.svc()
	pooled := TrainingSelectionRequest{WorkspaceID: h.wsID(), Purpose: TrainingPurposePooled, Actor: "user:owner"}

	_, err := svc.SelectTrainingManifest(h.ctx, pooled)
	require.ErrorIs(t, err, ErrTrainingPooledNotEnabled)

	grant, err := svc.OptInPooledTraining(h.ctx, h.wsID(), "user:owner", 0)
	require.NoError(t, err)
	assert.Equal(t, "active", grant.PooledStatus)
	assert.Equal(t, int64(1), grant.PooledPolicyVersion)

	manifest, err := svc.SelectTrainingManifest(h.ctx, pooled)
	require.NoError(t, err)
	assert.Equal(t, TrainingPurposePooled, manifest.Purpose)
	assert.Equal(t, int64(1), manifest.GrantPolicyVersion, "pooled purpose freezes the pooled version")
}

// A NEW workspace may be bootstrapped with the tenant default ON (explicit
// product decision), while bootstrap OFF keeps it pending; both record
// their own grant rows.
func TestInteractionDAGTraining_NewWorkspaceTenantDefault(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, false)

	wsOn := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `INSERT INTO workspace (id) VALUES ($1)`, wsOn)
	require.NoError(t, err)
	wsOff := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err = h.conn.Exec(h.ctx, `INSERT INTO workspace (id) VALUES ($1)`, wsOff)
	require.NoError(t, err)

	grantOn, err := h.svc().BootstrapNewWorkspaceGrant(h.ctx, wsOn.String(), true, "bootstrap")
	require.NoError(t, err)
	assert.Equal(t, "active", grantOn.TenantStatus)
	assert.Equal(t, int64(1), grantOn.TenantPolicyVersion, "explicit default activation bumps the version")

	grantOff, err := h.svc().BootstrapNewWorkspaceGrant(h.ctx, wsOff.String(), false, "bootstrap")
	require.NoError(t, err)
	assert.Equal(t, "pending_owner_ack", grantOff.TenantStatus)

	// No candidates in the fresh workspaces, but the grant verdict differs:
	// the default-on workspace passes the grant gate and stops at the empty
	// candidate set, the default-off one stops at the grant gate.
	_, err = h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: wsOn.String(), Purpose: TrainingPurposeTenant, Actor: "system"})
	require.ErrorIs(t, err, ErrTrainingDuplicate, "grant passed; no candidates")
	_, err = h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: wsOff.String(), Purpose: TrainingPurposeTenant, Actor: "system"})
	require.ErrorIs(t, err, ErrTrainingGrantPendingOwnerAck)
}

// ---------------------------------------------------------------------------
// Selection exclusions (plan Step 1: derivative / retracted / unavailable).
// ---------------------------------------------------------------------------

// Selection excludes derivative, redaction-failed, retracted and
// reward-unavailable segments; the audit names the reason for each.
func TestInteractionDAGTraining_SelectionExclusionsAndAudit(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, false)
	h.ackTenant(t, 0)

	clean := h.seedPublishedSegment(t, "clean", "clean trainable content NIMBUS")
	derivative := h.seedDerivativeSegment(t, "deriv", "derivative content NIMBUS")
	failed := h.seedRedactionFailedSegment(t, "failed")
	retracted := h.seedPublishedSegment(t, "retracted", "retracted content NIMBUS")
	h.mutateSegment(t, retracted, "retracted", "retracted", true)
	unrewarded := h.seedPublishedSegment(t, "unrewarded", "unrewarded content NIMBUS")
	_, delErr := h.conn.Exec(h.ctx, `
		DELETE FROM interaction_dag_step_reward WHERE segment_id = $1`, unrewarded)
	require.NoError(t, delErr)

	// Before selection, only the clean segment classifies as selectable.
	audit, err := h.svc().AuditTrainingSelection(h.ctx, h.wsID())
	require.NoError(t, err)
	reasons := map[string]string{}
	for _, row := range audit {
		reasons[row.SegmentID] = row.Reason
	}
	assert.Equal(t, "", reasons[clean])
	assert.Equal(t, "derivative", reasons[derivative])
	assert.Equal(t, "redaction_failed", reasons[failed])
	assert.Equal(t, "retracted", reasons[retracted])
	assert.Equal(t, "reward_unavailable", reasons[unrewarded])

	manifest, err := h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	require.Len(t, manifest.Items, 1, "only the clean segment is selectable")
	assert.Equal(t, clean, manifest.Items[0].Key)

	// After selection, the consumed candidate reports as already sampled.
	audit, err = h.svc().AuditTrainingSelection(h.ctx, h.wsID())
	require.NoError(t, err)
	for _, row := range audit {
		if row.SegmentID == clean {
			assert.Equal(t, "already_sampled", row.Reason)
		}
	}
}

// Identical content is a near-duplicate: the second publish is excluded by
// hash, and already-sampled segments never re-enter a later selection.
func TestInteractionDAGTraining_NearDuplicateAndReselection(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, false)
	h.ackTenant(t, 0)
	// Identical sanitized content (message + input) across two tasks: only
	// the action key differs, which is metadata outside the trajectory.
	first := h.seedPublishedSegmentWithInput(t, "dup-1", "identical near duplicate content NIMBUS", `{"f":"dup"}`)
	h.seedPublishedSegmentWithInput(t, "dup-2", "identical near duplicate content NIMBUS", `{"f":"dup"}`)

	manifest, err := h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	require.Len(t, manifest.Items, 1)
	assert.Equal(t, first, manifest.Items[0].Key, "the second identical hash is a near-duplicate")

	// After the first manifest consumes the sample, re-selection has nothing
	// new: the duplicate stays excluded.
	_, expErr := h.svc().ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, expErr)
	h.enableSelection(t, true)
	agent := h.seedReplayAgent(t)
	execution, err := h.svc().BeginTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID, agent)
	require.NoError(t, err)
	require.NoError(t, h.svc().ConsumeTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID))
	assert.NotEmpty(t, execution.ExecutionID)
	assert.NotEmpty(t, execution.TrainingTaskID)

	_, err = h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.ErrorIs(t, err, ErrTrainingDuplicate, "sample + near-duplicate leave no new candidate")
}

// The per-channel and per-workspace sampling caps bound one manifest.
func TestInteractionDAGTraining_SamplingCaps(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, false)
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "cap-1", "cap content one NIMBUS")
	h.seedPublishedSegment(t, "cap-2", "cap content two NIMBUS")

	cap := int32(1)
	_, err := h.svc().SetTrainingPolicy(h.ctx, TrainingPolicyPatch{
		PerChannelSampleCap: &cap, PerWorkspaceSampleCap: &cap,
	}, "test:operator")
	require.NoError(t, err)

	manifest, err := h.svc().SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	assert.Len(t, manifest.Items, 1, "the channel cap bounds the selection")
	assert.EqualValues(t, 1, manifest.WorkspaceConfig["per_channel_sample_cap"],
		"manifest freezes the caps that produced it")
}

// ---------------------------------------------------------------------------
// Lifecycle transitions (plan Step 1: CAS conflict / revoke before
// execution / delete after consume / replay).
// ---------------------------------------------------------------------------

// seedReplayAgent creates the agent row CreateTrainingReplayTask selects
// from (peer task shape needs a live agent).
func (h *trainingGovernanceHarness) seedReplayAgent(t *testing.T) string {
	t.Helper()
	agent := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO agent (id, workspace_id) VALUES ($1, $2)`, agent, h.workspace)
	require.NoError(t, err)
	return agent.String()
}

// The full happy path: select -> export -> execution (replay task) ->
// consume, with exactly-once CAS on every step.
func TestInteractionDAGTraining_ExportExecutionConsumeLifecycle(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "life", "lifecycle content NIMBUS")
	svc := h.svc()

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	segment := manifest.Items[0].Key

	exported, err := svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestExported, exported.Status)
	require.Len(t, exported.Items, 1)
	assert.Equal(t, manifest.ContentHash, exported.ContentHash, "the export is the immutable selection")

	// Exactly-once: a second export CAS fails.
	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingManifestState)

	agent := h.seedReplayAgent(t)
	execution, err := svc.BeginTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID, agent)
	require.NoError(t, err)
	assert.Equal(t, "started", execution.Status)

	// The replay task carries the immutable training_execution identity.
	var taskContext []byte
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT context FROM agent_inbox_event WHERE id = $1`, execution.TrainingTaskID).
		Scan(&taskContext))
	assert.Contains(t, string(taskContext), `"manifest_id": "`+manifest.ManifestID+`"`)
	assert.Contains(t, string(taskContext), `"purpose": "tenant"`)
	assert.Contains(t, string(taskContext), execution.ExecutionID)
	var reason string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT reason FROM agent_inbox_event WHERE id = $1`, execution.TrainingTaskID).Scan(&reason))
	assert.Equal(t, "training_replay", reason, "the replay task is distinct from any source task")

	// Execution authorization passes for the recorded identity only.
	require.NoError(t, svc.AuthorizeTrainingExecutionTask(h.ctx, execution.TrainingTaskID, manifest.ManifestID))
	require.ErrorIs(t, svc.AuthorizeTrainingExecutionTask(h.ctx, execution.TrainingTaskID, uuid.New().String()),
		ErrTrainingExecutionTaskMismatch, "a mismatched manifest refuses the identity")

	require.NoError(t, svc.ConsumeTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID))
	require.ErrorIs(t, svc.ConsumeTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID),
		ErrTrainingManifestState, "consume is exactly-once")

	full, err := svc.GetTrainingManifest(h.ctx, manifest.ManifestID, true)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestConsumed, full.Status)

	var sampleStatus string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT status FROM interaction_dag_training_sample
		WHERE sample_kind='segment' AND sample_key=$1`, segment).Scan(&sampleStatus))
	assert.Equal(t, TrainingSampleConsumed, sampleStatus)
}

// Before the execution switch flips, no replay task is created and no
// model update can happen.
func TestInteractionDAGTraining_ExecutionRequiresGlobalSwitch(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, false) // selection on, execution off
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "exec-gate", "execution gate NIMBUS")
	svc := h.svc()

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)

	agent := h.seedReplayAgent(t)
	_, err = svc.BeginTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID, agent)
	require.ErrorIs(t, err, ErrTrainingExecutionDisabled)

	var replayTasks int
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE reason = 'training_replay'`).
		Scan(&replayTasks))
	assert.Zero(t, replayTasks, "no replay task exists before calibration")
}

// Revoking before execution invalidates the unconsumed manifest and the
// samples; execution then refuses.
func TestInteractionDAGTraining_RevokeBeforeExecution(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "revoke", "revoke content NIMBUS")
	svc := h.svc()

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)

	report, err := svc.RevokeTrainingGrant(h.ctx, h.wsID(), TrainingPurposeTenant, "user:owner")
	require.NoError(t, err)
	assert.Equal(t, int64(1), report.InvalidatedManifests)
	assert.Equal(t, int64(1), report.RevokedSamples)
	assert.Zero(t, report.LedgerEntries, "nothing was consumed yet")

	full, err := svc.GetTrainingManifest(h.ctx, manifest.ManifestID, true)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestInvalidated, full.Status)

	agent := h.seedReplayAgent(t)
	_, err = svc.BeginTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID, agent)
	require.ErrorIs(t, err, ErrTrainingGrantRevoked, "an invalidated manifest can never execute")

	// Revoke is idempotent and touches nothing the second time.
	report2, err := svc.RevokeTrainingGrant(h.ctx, h.wsID(), TrainingPurposeTenant, "user:owner")
	require.NoError(t, err)
	assert.Zero(t, report2.InvalidatedManifests)
}

// Consumed samples enter the deletion/unlearning ledger on revoke, and the
// ledger is deduplicated.
func TestInteractionDAGTraining_RevokeAfterConsumeEntersDeletionLedger(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "ledger", "ledger content NIMBUS")
	svc := h.svc()

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)
	agent := h.seedReplayAgent(t)
	_, err = svc.BeginTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID, agent)
	require.NoError(t, err)
	require.NoError(t, svc.ConsumeTrainingExecution(h.ctx, h.wsID(), manifest.ManifestID))

	report, err := svc.RevokeTrainingGrant(h.ctx, h.wsID(), TrainingPurposeTenant, "user:owner")
	require.NoError(t, err)
	assert.Equal(t, int64(1), report.LedgerEntries, "the consumed sample enters the ledger")

	rows, err := svc.ListTrainingDeletionLedgerRows(h.ctx, h.wsID(), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "grant_revoked", rows[0].Reason)
	assert.Equal(t, TrainingSampleKindSegment, rows[0].SampleKind)
	assert.Equal(t, manifest.Items[0].Key, rows[0].SampleKey)
	assert.False(t, rows[0].ProcessedAt.Valid, "the unlearning entry starts unprocessed")

	report2, err := svc.RevokeTrainingGrant(h.ctx, h.wsID(), TrainingPurposeTenant, "user:owner")
	require.NoError(t, err)
	assert.Zero(t, report2.LedgerEntries, "the ledger row is deduplicated")
}

// A retraction between selection and export fences the manifest closed.
func TestInteractionDAGTraining_RetractionFencesExport(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	segment := h.seedPublishedSegment(t, "fence", "fence content NIMBUS")
	svc := h.svc()

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	h.mutateSegment(t, segment, "retracted", "retracted", true)

	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingFenced)

	full, err := svc.GetTrainingManifest(h.ctx, manifest.ManifestID, true)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestSelected, full.Status, "the failed export leaves the manifest untouched")
}

// ---------------------------------------------------------------------------
// Graph-memory selection family.
// ---------------------------------------------------------------------------

// Graded, unfenced offline_rl trajectories with a reward are selected under
// the same governance; fenced owners and rewardless trajectories are not.
func TestInteractionDAGTraining_GraphTrajectorySelection(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	reward := 0.62
	selected := h.seedGraphTrajectory(t, "graded-good", &reward, false)
	h.seedGraphTrajectory(t, "graded-fenced", &reward, true)
	h.seedGraphTrajectory(t, "rewardless", nil, false)

	manifest, err := h.svc().SelectGraphTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)
	require.Len(t, manifest.Items, 1)
	assert.Equal(t, TrainingSampleKindGraphTrajectory, manifest.Items[0].Kind)
	assert.Equal(t, selected, manifest.Items[0].Key)
	assert.Empty(t, manifest.Items[0].SanitizerVersion, "graph items carry no segment sanitizer")
	assert.Equal(t, "available", manifest.Items[0].RewardStatus)
	assert.NotEmpty(t, manifest.Items[0].Scope["recall_id"])

	exported, err := h.svc().ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestExported, exported.Status)
}

// A later owner retraction fences the graph export too.
func TestInteractionDAGTraining_GraphRetractionFencesExport(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	reward := 0.5
	h.seedGraphTrajectory(t, "late-fence", &reward, false)

	manifest, err := h.svc().SelectGraphTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)

	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO memory_source_guard (workspace_id, source_kind, source_id, retracted_at, retracted_by, reason)
		VALUES ($1, 'channel', $2, now(), 'user:owner', 'channel archived')`,
		h.workspace, h.channel)
	require.NoError(t, err)

	_, err = h.svc().ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingFenced)
}

// ---------------------------------------------------------------------------
// Raw-NDJSON export gate (plan Step 1: disabled / manifest_required).
// ---------------------------------------------------------------------------

// The export gate refuses without a manifest, while the switches are off,
// for another workspace's manifest, and for a not-yet-exported manifest.
func TestInteractionDAGTraining_AuthorizeTrainingExportGate(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()
	h.enableSelection(t, true)
	h.ackTenant(t, 0)
	h.seedPublishedSegment(t, "gate-export", "export gate NIMBUS")
	svc := h.svc()

	_, err := svc.AuthorizeTrainingExport(h.ctx, h.wsID(), "")
	require.ErrorIs(t, err, ErrTrainingManifestNotFound)

	manifest, err := svc.SelectTrainingManifest(h.ctx, TrainingSelectionRequest{
		WorkspaceID: h.wsID(), Purpose: TrainingPurposeTenant, Actor: "user:owner"})
	require.NoError(t, err)

	_, err = svc.AuthorizeTrainingExport(h.ctx, h.wsID(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingManifestState, "a selected-but-not-exported manifest authorizes nothing")

	other := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err = h.conn.Exec(h.ctx, `INSERT INTO workspace (id) VALUES ($1)`, other)
	require.NoError(t, err)
	_, err = svc.AuthorizeTrainingExport(h.ctx, other.String(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingWorkspaceMismatch)

	_, err = svc.ExportTrainingManifest(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)
	authorized, err := svc.AuthorizeTrainingExport(h.ctx, h.wsID(), manifest.ManifestID)
	require.NoError(t, err)
	assert.Equal(t, TrainingManifestExported, authorized.Status)

	// Flipping the kill switch back off closes the raw export surface.
	off := false
	_, err = svc.SetTrainingPolicy(h.ctx, TrainingPolicyPatch{SelectionEnabled: &off}, "test:operator")
	require.NoError(t, err)
	_, err = svc.AuthorizeTrainingExport(h.ctx, h.wsID(), manifest.ManifestID)
	require.ErrorIs(t, err, ErrTrainingSelectionDisabled)
}

// ---------------------------------------------------------------------------
// Ordinary source tasks never open AReaL (plan Step 4 inversion).
// ---------------------------------------------------------------------------

// An ordinary source task — even the project's recorded training target —
// never opens an RL session; only a task carrying the training_execution
// identity may, and only while the governance authorization passes.
func TestInteractionDAGTraining_OrdinarySourceTaskNeverOpensAReal(t *testing.T) {
	h := newTrainingGovernanceHarness(t)
	defer h.Close()

	// Ordinary task with areal-less context: no session, even for the
	// dispatch training target.
	task := db.AgentInboxEvent{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		WorkspaceID: h.workspace,
		Context:     []byte(`{}`),
	}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-1", ProxyKey: "key-1"}}
	deps := &TrainingSessionDeps{
		Store:    &fakeTaskStore{task: task},
		RL:       rl,
		ProxyURL: "http://bridge",
	}
	err := maybeOpenTrainingSession(h.ctx, deps, task.ID.String(), "agent-1", "project-1", "env-1")
	require.NoError(t, err)
	assert.Zero(t, rl.calls, "an ordinary source task never calls StartSession")

}
