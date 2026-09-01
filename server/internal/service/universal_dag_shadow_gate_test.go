package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// universalDAGShadowGateHarness layers everything the shadow-gate canaries
// read onto the publisher harness (454/466/467/469): the graph ledger stubs
// (rewardPolicyGraphStubDDL, migrations 419/420 shapes minus FKs the harness
// schema does not carry), then migrations 421 (reward outbox), 470 (channel
// migration gate column), 472 (training governance) and 474 (shadow gate
// registry) applied verbatim in the private schema.
type universalDAGShadowGateHarness struct {
	t   *testing.T
	ctx context.Context
	*universalDAGPublisherHarness
}

func newUniversalDAGShadowGateHarness(t *testing.T) *universalDAGShadowGateHarness {
	t.Helper()
	pub := newUniversalDAGPublisherHarness(t)
	_, err := pub.conn.Exec(pub.ctx, rewardPolicyGraphStubDDL)
	require.NoError(t, err, "create graph ledger stubs")
	for _, name := range []string{
		"421_graph_memory_rl_session.up.sql",
		"470_graph_memory_channel_migration.up.sql",
		"472_interaction_dag_training_governance.up.sql",
		"474_universal_dag_shadow_gate.up.sql",
	} {
		applyUniversalDAGMigrationFile(t, pub.ctx, pub.conn, name)
	}
	return &universalDAGShadowGateHarness{t: t, ctx: pub.ctx, universalDAGPublisherHarness: pub}
}

func (h *universalDAGShadowGateHarness) gatePool() *pgxpool.Pool { return h.pubPool }

// applyUniversalDAGMigrationFile executes one migration file inside the
// harness's private schema. Migrations in this pipeline are written
// schema-agnostic (search_path-relative), so the private-schema application
// exercises the same DDL production runs.
func applyUniversalDAGMigrationFile(t *testing.T, ctx context.Context, conn *pgxpool.Conn, name string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG shadow gate test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", name)
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration %s in private schema: %v", name, err)
	}
}

// insertShadowTask inserts one agent_inbox_event row (with the task_message
// rows 1-2 a terminal-closed segment range must cover) for the segment
// fixture to reference.
func (h *universalDAGShadowGateHarness) insertShadowTask(t *testing.T) pgtype.UUID {
	t.Helper()
	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx,
		`INSERT INTO agent_inbox_event(id, workspace_id) VALUES ($1, $2)`, taskID, h.workspace)
	require.NoError(t, err, "insert shadow task")
	for seq := 1; seq <= 2; seq++ {
		_, err = h.conn.Exec(h.ctx,
			`INSERT INTO task_message(task_id, seq, content) VALUES ($1, $2, '')`, taskID, seq)
		require.NoError(t, err, "insert shadow task message %d", seq)
	}
	return taskID
}

// insertPublishedSegment inserts one terminal-closed segment plus its
// atomic outbox row and walks both through the real lifecycle triggers
// (insert pending -> processing -> terminal); canary tests need exact
// publish_seq / content_status combinations, not the full boundary state
// machine. publishSeq <= 0 with pending/pending means the pair stays
// unpublished (NULL publish_seq).
func (h *universalDAGShadowGateHarness) insertPublishedSegment(t *testing.T, segmentID string, publishSeq int, publishStatus, contentStatus string, channelID pgtype.UUID) {
	t.Helper()
	taskID := h.insertShadowTask(t)
	// The (outbox, segment) pair is created inside one transaction: the
	// outbox's segment FK is deferrable and the segment insert trigger
	// requires the atomic pair. status has no DB default — the lifecycle
	// trigger mandates an explicit initial 'pending'.
	tx, err := h.conn.Begin(h.ctx)
	require.NoError(t, err, "begin segment pair tx")
	defer tx.Rollback(h.ctx)
	_, err = tx.Exec(h.ctx, `
		INSERT INTO interaction_dag_publish_outbox (workspace_id, segment_id, request_hash, status)
		VALUES ($1, $2, 'req', 'pending')`, h.workspace, segmentID)
	require.NoError(t, err, "insert outbox row %s", segmentID)
	_, err = tx.Exec(h.ctx, `
		INSERT INTO interaction_dag_segment (
			workspace_id, segment_id, agent_run_id, generation,
			channel_id_at_event, start_seq, end_seq,
			close_action_kind, canonical_action_id, visible_action_key,
			memory_type_at_event, graph_projection_eligible_at_event,
			trajectory_source, derivative, trainable_eligible,
			publish_status, content_status, provider_capture_status
		) VALUES (
			$1, $2, $3, 1,
			$4, 1, 2,
			'terminal', NULL, $5,
			'graph', true,
			'task_messages', false, true,
			'pending', 'pending', 'not_expected'
		)`,
		h.workspace, segmentID, taskID, channelID, segmentID+":close")
	require.NoError(t, err, "insert segment %s", segmentID)
	require.NoError(t, tx.Commit(h.ctx), "commit segment pair %s", segmentID)
	if publishStatus == "pending" && contentStatus == "pending" {
		return
	}
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment SET publish_status='processing'
		WHERE workspace_id=$1 AND segment_id=$2`, h.workspace, segmentID)
	require.NoError(t, err, "process segment %s", segmentID)
	var publishSeqVal any
	if publishSeq > 0 {
		publishSeqVal = publishSeq
	}
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment
		SET publish_status=$3, content_status=$4, publish_seq=$5, published_at=now()
		WHERE workspace_id=$1 AND segment_id=$2`,
		h.workspace, segmentID, publishStatus, contentStatus, publishSeqVal)
	require.NoError(t, err, "finalize segment %s as %s/%s", segmentID, publishStatus, contentStatus)
	outboxTerminal := publishStatus
	if outboxTerminal == "pending" {
		outboxTerminal = "published"
	}
	h.walkOutboxToTerminal(t, segmentID, outboxTerminal)
}

// walkOutboxToTerminal moves an existing pending outbox row to a terminal
// state through the lifecycle trigger (pending -> processing -> terminal).
func (h *universalDAGShadowGateHarness) walkOutboxToTerminal(t *testing.T, segmentID, terminal string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_publish_outbox
		SET status='processing', lease_owner='canary-test', lease_expires_at=now() + interval '1 minute'
		WHERE workspace_id=$1 AND segment_id=$2 AND status='pending'`, h.workspace, segmentID)
	require.NoError(t, err, "lease outbox row %s", segmentID)
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_publish_outbox
		SET status=$3, lease_owner=NULL, lease_expires_at=NULL, completed_at=now()
		WHERE workspace_id=$1 AND segment_id=$2`, h.workspace, segmentID, terminal)
	require.NoError(t, err, "finalize outbox row %s as %s", segmentID, terminal)
}

func (h *universalDAGShadowGateHarness) insertChannelAtom(t *testing.T, atomID, segmentID string, channelID pgtype.UUID) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_atom (
			workspace_id, atom_id, segment_id, body, kind, tool_trust_class,
			content_hash, visibility, channel_id, publish_seq
		) VALUES ($1, $2, $3, 'body', 'fact', 'trusted_read_only', 'hash', 'channel', $4, 1)`,
		h.workspace, atomID, segmentID, channelID)
	require.NoError(t, err, "insert atom %s", atomID)
}

// seedCleanPublishedState builds the minimal state every canary proves green:
// contiguous publish sequences, published segments/outbox rows, matching
// channel scoping and an unfenced provenance graph.
func (h *universalDAGShadowGateHarness) seedCleanPublishedState(t *testing.T) {
	t.Helper()
	h.insertPublishedSegment(t, "seg-clean-1", 1, "published", "published", h.channel)
	h.insertPublishedSegment(t, "seg-clean-2", 2, "published", "published", h.channel)
	h.insertChannelAtom(t, "atom-clean-1", "seg-clean-1", h.channel)
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO memory_source_provenance (workspace_id, source_kind, source_id, consumer_kind, consumer_id)
		VALUES ($1, 'task_output', 'seed-task', 'graph_memory_atom', 'atom-clean-1')`, h.workspace)
	require.NoError(t, err, "seed provenance")
}

// insertTrajectory seeds one recall + terminal trajectory with the given
// round count; the cost canary reads rounds and the reward outbox only.
func (h *universalDAGShadowGateHarness) insertTrajectory(t *testing.T, recallID string, rounds int, createdAt time.Time) string {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_recall (id, workspace_id, task_id, graph_kind, graph_owner_id, graph_version)
		VALUES ($1, $2, $3, 'channel', $4, 1)`,
		mustUUID(t, recallID), h.workspace, h.insertShadowTask(t), h.channel)
	require.NoError(t, err, "insert recall %s", recallID)
	trajectoryID := uuid.New().String()
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_trajectory (id, recall_id, workspace_id, seed_index, status, rounds, created_at)
		VALUES ($1, $2, $3, 0, 'found', $4, $5)`,
		mustUUID(t, trajectoryID), mustUUID(t, recallID), h.workspace, rounds, createdAt)
	require.NoError(t, err, "insert trajectory %s", trajectoryID)
	return trajectoryID
}

func (h *universalDAGShadowGateHarness) insertRewardOutbox(t *testing.T, trajectoryID string, reward float64, status string, createdAt time.Time) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_reward_outbox (workspace_id, trajectory_id, reward, status, created_at)
		VALUES ($1, $2, $3, $4, $5)`, h.workspace, mustUUID(t, trajectoryID), reward, status, createdAt)
	require.NoError(t, err, "insert reward outbox %s", trajectoryID)
}

func (h *universalDAGShadowGateHarness) shadowService() *ShadowGateService {
	return NewShadowGateService(h.pubPool)
}

// allGreenEvidence is the operator-recorded evidence snapshot of a healthy
// canary window: every canary present and OK.
func allGreenEvidence() map[ShadowCanaryName]ShadowCanaryResult {
	evidence := map[ShadowCanaryName]ShadowCanaryResult{}
	for _, canary := range AllShadowCanaries() {
		evidence[canary] = ShadowCanaryResult{Canary: canary, OK: true}
	}
	return evidence
}

func evidenceWithRed(canary ShadowCanaryName) map[ShadowCanaryName]ShadowCanaryResult {
	evidence := allGreenEvidence()
	result := evidence[canary]
	result.OK = false
	result.Count = 1
	evidence[canary] = result
	return evidence
}

// ---------------------------------------------------------------------------
// Step 1 RED: canary evidence evaluation (spec AC51)
// ---------------------------------------------------------------------------

func TestShadowGateCanariesProveGreenOnCleanWorkspace(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	require.Len(t, evidence, len(AllShadowCanaries()), "every named canary must report")
	for _, canary := range AllShadowCanaries() {
		result, ok := evidence[canary]
		require.True(t, ok, "canary %s missing from evidence", canary)
		assert.True(t, result.OK, "canary %s must be green on clean state (detail: %s)", canary, result.Detail)
	}
}

func TestShadowGateCanarySequenceGap(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	// A published sequence hole (2 -> 4) is the lost-commit corruption signal.
	h.insertPublishedSegment(t, "seg-gap", 4, "published", "published", h.channel)

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanarySequenceGap].OK, "publish_seq gap must fail the sequence canary")
	assert.Greater(t, evidence[ShadowCanarySequenceGap].Count, 0, "gap count must be reported")
	// An unpublished tail segment (NULL publish_seq) is not a gap: allocation
	// happens inside the publish transaction only.
	h.insertPublishedSegment(t, "seg-pending-tail", 0, "pending", "pending", h.channel)
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanarySequenceGap].OK)
}

func TestShadowGateCanaryOutboxLoss(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	// An unpublished pair whose outbox row dies: the publish pipeline lost a
	// durable segment irrecoverably.
	h.insertPublishedSegment(t, "seg-dead", 0, "pending", "pending", h.channel)
	h.walkOutboxToTerminal(t, "seg-dead", "dead_letter")

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryOutboxLoss].OK, "dead-letter outbox row must fail the outbox canary")
	assert.Greater(t, evidence[ShadowCanaryOutboxLoss].Count, 0)

	// Redaction failures on the outbox path are the same loss signal.
	h.insertPublishedSegment(t, "seg-redaction", 0, "pending", "pending", h.channel)
	h.walkOutboxToTerminal(t, "seg-redaction", "redaction_failed")
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryOutboxLoss].OK, "redaction_failed outbox row must fail the outbox canary")
}

func TestShadowGateCanaryCrossChannelLeak(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	// A channel-visible atom whose scope does not inherit its segment's
	// channel-at-event is a cross-channel leak.
	foreignChannel := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	h.insertChannelAtom(t, "atom-leak", "seg-clean-2", foreignChannel)

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryCrossChannelLeak].OK, "atom/segment channel mismatch must fail the leak canary")
	assert.Greater(t, evidence[ShadowCanaryCrossChannelLeak].Count, 0)

	// Atoms are write-once: the sanctioned remediation is the retraction
	// quarantine, which pulls the leaked atom out of readable state.
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO quarantined_pending_recompute (workspace_id, retraction_id, consumer_kind, consumer_id)
		VALUES ($1, gen_random_uuid(), 'graph_memory_atom', 'atom-leak')`, h.workspace)
	require.NoError(t, err)
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.True(t, evidence[ShadowCanaryCrossChannelLeak].OK, "quarantining the leaked atom restores the canary")
}

func TestShadowGateCanarySanitizerFailOpen(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	// Published-but-redaction-failed is the sanitizer fail-open signature.
	h.insertPublishedSegment(t, "seg-failopen", 3, "published", "redaction_failed", h.channel)

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanarySanitizerFailOpen].OK, "published+redaction_failed must fail the sanitizer canary")
	assert.Greater(t, evidence[ShadowCanarySanitizerFailOpen].Count, 0)
	// A redaction failure that was NOT published stays fail-closed: no
	// signal. (The published breach above cannot be reverted — segments are
	// lifecycle-managed — so the negative case runs on a clean workspace.)
	h2 := newUniversalDAGShadowGateHarness(t)
	defer h2.Close()
	h2.insertPublishedSegment(t, "seg-failclosed", 0, "redaction_failed", "redaction_failed", h2.channel)
	evidence, err = h2.shadowService().EvaluateEvidence(h2.ctx, h2.workspace)
	require.NoError(t, err)
	assert.True(t, evidence[ShadowCanarySanitizerFailOpen].OK, "unpublished redaction failure must stay green (fail-closed path)")
}

func TestShadowGateCanaryRetractionVisibility(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)

	// Fence the source of the seeded atom without quarantining the consumer:
	// the published atom would stay readable through the fence.
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO memory_source_guard (workspace_id, source_kind, source_id, retracted_at, retracted_by, reason)
		VALUES ($1, 'task_output', 'seed-task', now(), 'audit-test', 'canary')`, h.workspace)
	require.NoError(t, err)

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryRetractionVisibility].OK, "fenced source with unquarantined consumer must fail the retraction canary")
	assert.Greater(t, evidence[ShadowCanaryRetractionVisibility].Count, 0)

	// Quarantining the consumer restores visibility safety.
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO quarantined_pending_recompute (workspace_id, retraction_id, consumer_kind, consumer_id)
		VALUES ($1, gen_random_uuid(), 'graph_memory_atom', 'atom-clean-1')`, h.workspace)
	require.NoError(t, err)
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.True(t, evidence[ShadowCanaryRetractionVisibility].OK, "quarantined consumer must restore the canary")
}

func TestShadowGateCanaryCostLatencyBudget(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	now := time.Now().UTC()

	recallID := uuid.New().String()
	trajectoryID := h.insertTrajectory(t, recallID, 2, now.Add(-time.Hour))
	h.insertRewardOutbox(t, trajectoryID, 0.5, "delivered", now.Add(-time.Hour))

	evidence, err := h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.True(t, evidence[ShadowCanaryCostLatencyBudget].OK, "in-budget trajectory must stay green (detail: %s)", evidence[ShadowCanaryCostLatencyBudget].Detail)

	// P95 explore rounds breaching the shadow hard cap (6) breaks the budget.
	h.insertTrajectory(t, uuid.New().String(), ShadowCostP95RoundsCap+2, now.Add(-30*time.Minute))
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryCostLatencyBudget].OK, "P95 rounds above the shadow cap must fail the budget canary")

	// A graded reward below the floor breaks the budget.
	_, err = h.conn.Exec(h.ctx, `DELETE FROM graph_memory_trajectory`)
	require.NoError(t, err)
	badReward := uuid.New().String()
	badTrajectory := h.insertTrajectory(t, badReward, 1, now.Add(-time.Hour))
	h.insertRewardOutbox(t, badTrajectory, ShadowRewardFloor-1, "delivered", now.Add(-time.Hour))
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryCostLatencyBudget].OK, "reward below the floor must fail the budget canary")

	// A pending reward outbox row older than the delivery budget breaks the
	// budget (cost/latency of the reward pipeline itself).
	_, err = h.conn.Exec(h.ctx, `DELETE FROM graph_memory_reward_outbox`)
	require.NoError(t, err)
	stale := uuid.New().String()
	staleTrajectory := h.insertTrajectory(t, stale, 1, now.Add(-2*ShadowRewardOutboxPendingBudget))
	h.insertRewardOutbox(t, staleTrajectory, 0.5, "pending", now.Add(-2*ShadowRewardOutboxPendingBudget))
	evidence, err = h.shadowService().EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.False(t, evidence[ShadowCanaryCostLatencyBudget].OK, "stale pending reward must fail the budget canary")
}

// ---------------------------------------------------------------------------
// Step 1 RED: audited CAS gate promotion (plan Step 3, spec §15/§19)
// ---------------------------------------------------------------------------

func TestShadowGatePromotionHappyPathRecordsAuditAndFlipsRoute(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	// Absent row reads as disabled at version 0.
	status, err := svc.Gate(h.ctx, h.workspace, ShadowGateAtoms)
	require.NoError(t, err)
	assert.Equal(t, ShadowPhaseDisabled, status.Phase)
	assert.EqualValues(t, 0, status.GateVersion)

	// disabled -> shadow
	status, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateAtoms, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout phase 3",
	})
	require.NoError(t, err)
	assert.Equal(t, ShadowPhaseShadow, status.Phase)
	assert.EqualValues(t, 1, status.GateVersion)

	// The memory route boolean stays off in shadow: only "enabled" opens the
	// external read path.
	routeOn, err := NewMemoryReadGate(db.New(h.gatePool())).RouteEnabled(h.ctx, h.workspace, MemoryRouteAtoms)
	require.NoError(t, err)
	assert.False(t, routeOn, "shadow phase must not open the atoms read route")

	// shadow -> enabled with a recorded policy version
	status, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateAtoms, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 3, Evidence: allGreenEvidence(), Actor: "operator", Reason: "canary window green",
	})
	require.NoError(t, err)
	assert.Equal(t, ShadowPhaseEnabled, status.Phase)
	assert.EqualValues(t, 2, status.GateVersion)
	assert.EqualValues(t, 3, status.PolicyVersion, "policy version must be recorded on the gate row")

	routeOn, err = NewMemoryReadGate(db.New(h.gatePool())).RouteEnabled(h.ctx, h.workspace, MemoryRouteAtoms)
	require.NoError(t, err)
	assert.True(t, routeOn, "enabled phase must open the atoms read route")

	// Both transitions are recorded in the append-only audit ledger.
	transitions, err := svc.ListTransitions(h.ctx, h.workspace, 10)
	require.NoError(t, err)
	require.Len(t, transitions, 2)
	assert.Equal(t, ShadowPhaseDisabled, transitions[1].FromPhase)
	assert.Equal(t, ShadowPhaseShadow, transitions[1].ToPhase)
	assert.Equal(t, ShadowPhaseShadow, transitions[0].FromPhase)
	assert.Equal(t, ShadowPhaseEnabled, transitions[0].ToPhase)
	assert.Equal(t, "operator", transitions[0].Actor)
	assert.EqualValues(t, 3, transitions[0].PolicyVersion)
	assert.NotEmpty(t, transitions[0].Reason)

	// Status listing surfaces the workspace gate alongside the global ones.
	gates, err := svc.ListGates(h.ctx, h.workspace)
	require.NoError(t, err)
	found := false
	for _, gate := range gates {
		if gate.Gate == ShadowGateAtoms {
			found = true
			assert.Equal(t, ShadowPhaseEnabled, gate.Phase)
			assert.Equal(t, "workspace", gate.Scope)
		}
	}
	assert.True(t, found, "workspace gate must be listed")
}

func TestShadowGatePromotionRejectsPhaseSkipAndStaleCAS(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	// Skipping shadow entirely is rejected: the phase order is linear.
	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateSearchV2, To: ShadowPhaseEnabled,
		ExpectedVersion: 0, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "skip",
	})
	assert.ErrorIs(t, err, ErrShadowGatePhaseOrder)

	// A stale expected version is a CAS conflict, never a silent overwrite.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateSearchV2, To: ShadowPhaseShadow,
		ExpectedVersion: 7, Evidence: allGreenEvidence(), Actor: "operator", Reason: "stale",
	})
	assert.ErrorIs(t, err, ErrShadowGateCASConflict)

	// Neither rejected call may leave an audit row behind.
	transitions, err := svc.ListTransitions(h.ctx, h.workspace, 10)
	require.NoError(t, err)
	assert.Empty(t, transitions, "rejected promotions must not be audited as transitions")
}

func TestShadowGatePromotionRequiresRecordedEvidence(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateExplore, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: evidenceWithRed(ShadowCanarySanitizerFailOpen), Actor: "operator", Reason: "red",
	})
	assert.ErrorIs(t, err, ErrShadowGateEvidence, "a failed canary in the recorded evidence must block promotion")

	// Missing canaries in the snapshot are not recorded evidence.
	partial := allGreenEvidence()
	delete(partial, ShadowCanaryCostLatencyBudget)
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateExplore, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: partial, Actor: "operator", Reason: "partial",
	})
	assert.ErrorIs(t, err, ErrShadowGateEvidence, "every dependency canary must be present in the evidence")

	// Enabled promotions additionally require a recorded policy version.
	_, firstErr := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateExplore, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "to shadow",
	})
	require.NoError(t, firstErr)
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateExplore, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "no policy version",
	})
	assert.ErrorIs(t, err, ErrShadowGatePrerequisite, "enabled promotions must record a policy version")
}

func TestShadowGatePromotionRecordsEvidenceSnapshotOnGateRow(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateCitations, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "record evidence",
	})
	require.NoError(t, err)

	var evidenceCount int
	err = h.gatePool().QueryRow(h.ctx, `
		SELECT jsonb_array_length(COALESCE(jsonb_path_query_array(evidence, '$.keyvalue()'), '[]'::jsonb))
		FROM universal_dag_shadow_gate
		WHERE scope='workspace' AND workspace_id=$1 AND gate_name='citations'`, h.workspace).
		Scan(&evidenceCount)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, evidenceCount, len(AllShadowCanaries()),
		"the recorded evidence snapshot must carry every named canary")
}

// ---------------------------------------------------------------------------
// Step 1 RED: automatic read-path shutdown (spec AC52: canary failure closes
// dependent read/training phases, never the durable DAG writes)
// ---------------------------------------------------------------------------

func TestShadowGateAutoShutdownClosesReadPathsKeepsDAGWrites(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()
	readGate := NewMemoryReadGate(db.New(h.gatePool()))

	// Open every memory route through sanctioned promotions.
	for _, gate := range []ShadowGateName{
		ShadowGateAtoms, ShadowGateSearchV2, ShadowGateExplore, ShadowGateCitations,
		ShadowGateAtomConsolidation, ShadowGateChannelMigration,
	} {
		_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
			WorkspaceID: h.workspace, Gate: gate, To: ShadowPhaseShadow,
			ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout",
		})
		require.NoError(t, err, "promote %s to shadow", gate)
		_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
			WorkspaceID: h.workspace, Gate: gate, To: ShadowPhaseEnabled,
			ExpectedVersion: 1, PolicyVersion: 2, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout",
		})
		require.NoError(t, err, "promote %s to enabled", gate)
	}
	for route := range map[MemoryReadRoute]ShadowGateName{
		MemoryRouteAtoms: ShadowGateAtoms, MemoryRouteSearchV2: ShadowGateSearchV2,
		MemoryRouteExplore: ShadowGateExplore, MemoryRouteCitations: ShadowGateCitations,
		MemoryRouteAtomConsolidation: ShadowGateAtomConsolidation,
	} {
		on, err := readGate.RouteEnabled(h.ctx, h.workspace, route)
		require.NoError(t, err)
		require.True(t, on, "route %s must be open before shutdown", route)
	}

	// A sanitizer fail-open breach appears in the sweep evidence.
	h.insertPublishedSegment(t, "seg-breach", 3, "published", "redaction_failed", h.channel)
	evidence, err := svc.EvaluateEvidence(h.ctx, h.workspace)
	require.NoError(t, err)
	require.False(t, evidence[ShadowCanarySanitizerFailOpen].OK)

	shutdown, err := svc.AutoShutdown(h.ctx, h.workspace, evidence)
	require.NoError(t, err)
	assert.NotEmpty(t, shutdown, "the sanitizer breach must shut dependent gates down")

	// Every memory route is closed again...
	for _, route := range []MemoryReadRoute{
		MemoryRouteAtoms, MemoryRouteSearchV2, MemoryRouteExplore,
		MemoryRouteCitations, MemoryRouteAtomConsolidation,
	} {
		on, err := readGate.RouteEnabled(h.ctx, h.workspace, route)
		require.NoError(t, err)
		assert.False(t, on, "route %s must be closed after auto shutdown", route)
	}
	// ...including the channel migration flag.
	var migrationOn bool
	err = h.gatePool().QueryRow(h.ctx,
		`SELECT channel_migration_enabled FROM memory_read_phase_gate WHERE workspace_id=$1`, h.workspace).
		Scan(&migrationOn)
	require.NoError(t, err)
	assert.False(t, migrationOn, "channel migration must be closed after auto shutdown")

	// Gates are back at disabled with an auto_shutdown audit trail.
	for _, gate := range []ShadowGateName{ShadowGateAtoms, ShadowGateExplore, ShadowGateChannelMigration} {
		status, err := svc.Gate(h.ctx, h.workspace, gate)
		require.NoError(t, err)
		assert.Equal(t, ShadowPhaseDisabled, status.Phase, "gate %s must be disabled after shutdown", gate)
	}
	transitions, err := svc.ListTransitions(h.ctx, h.workspace, 50)
	require.NoError(t, err)
	autoShutdownCount := 0
	for _, transition := range transitions {
		if transition.Trigger == "auto_shutdown" && transition.ToPhase == ShadowPhaseDisabled {
			autoShutdownCount++
		}
	}
	assert.GreaterOrEqual(t, autoShutdownCount, 3, "auto shutdown transitions must be audited")

	// The durable DAG writes stay enabled: a new boundary still records its
	// segment and outbox rows (spec AC52).
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "post-shutdown-close")
	var outboxStatus string
	err = h.conn.QueryRow(h.ctx,
		`SELECT status FROM interaction_dag_publish_outbox WHERE segment_id=$1`, segmentID).Scan(&outboxStatus)
	require.NoError(t, err, "DAG write must survive read-path shutdown (outbox row for %s)", segmentID)
	assert.Equal(t, "pending", outboxStatus)
}

func TestShadowGateNoteFailureSynchronouslyDisablesDependentGates(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateSearchV2, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout",
	})
	require.NoError(t, err)

	// A sanitizer failure reported by the write path synchronously returns the
	// dependent gate to disabled (plan Step 3).
	affected, err := svc.NoteFailure(h.ctx, h.workspace, ShadowFailureSanitizer, "publisher")
	require.NoError(t, err)
	require.NotEmpty(t, affected)
	status, err := svc.Gate(h.ctx, h.workspace, ShadowGateSearchV2)
	require.NoError(t, err)
	assert.Equal(t, ShadowPhaseDisabled, status.Phase)

	transitions, err := svc.ListTransitions(h.ctx, h.workspace, 10)
	require.NoError(t, err)
	require.Len(t, transitions, 2) // promotion + failure demotion
	assert.Equal(t, "failure", transitions[0].Trigger)
	assert.Contains(t, transitions[0].Reason, "sanitizer")

	// Unknown failure kinds are rejected rather than silently ignored.
	_, err = svc.NoteFailure(h.ctx, h.workspace, ShadowFailureKind("bogus"), "publisher")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Step 1 RED: training phase order (spec §19.9/§19.10, AC68)
// ---------------------------------------------------------------------------

func TestShadowGateTrainingPhaseOrder(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	// The training gates climb the same ladder: shadow first, then enabled.
	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateTenantTraining, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "to shadow",
	})
	require.NoError(t, err)
	// Tenant training cannot enable before reward shadow is enabled.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateTenantTraining, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "premature",
	})
	assert.ErrorIs(t, err, ErrShadowGatePrerequisite)

	// Reward shadow promotion flips the global kill switches atomically:
	// shadow opens selection only, enabled opens execution too.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateRewardShadow, To: ShadowPhaseShadow,
		ExpectedVersion: 0, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "calibration",
	})
	require.NoError(t, err)
	policy, err := db.New(h.gatePool()).GetTrainingGovernancePolicy(h.ctx)
	require.NoError(t, err)
	assert.True(t, policy.SelectionEnabled, "reward shadow phase must enable selection")
	assert.False(t, policy.ExecutionEnabled, "reward shadow phase must keep execution closed")

	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateRewardShadow, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 2, Evidence: allGreenEvidence(), Actor: "operator", Reason: "calibrated",
	})
	require.NoError(t, err)
	policy, err = db.New(h.gatePool()).GetTrainingGovernancePolicy(h.ctx)
	require.NoError(t, err)
	assert.True(t, policy.ExecutionEnabled, "reward shadow enabled must open execution")

	// Tenant training additionally requires the owner-acknowledged grant
	// (Task 18 CAS): the gate can never bypass the workspace's own opt-in.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateTenantTraining, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "no grant ack",
	})
	assert.ErrorIs(t, err, ErrShadowGatePrerequisite, "pending_owner_ack grant must block tenant training")

	grant, err := db.New(h.gatePool()).GetTrainingGrantByWorkspace(h.ctx, h.workspace)
	require.NoError(t, err)
	acked, err := db.New(h.gatePool()).AckTenantTrainingGrant(h.ctx, db.AckTenantTrainingGrantParams{
		WorkspaceID: h.workspace, Actor: pgtype.Text{String: "owner", Valid: true}, ExpectedVersion: grant.TenantPolicyVersion,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, acked, "grant ack must succeed")

	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateTenantTraining, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "grant acked",
	})
	require.NoError(t, err)

	// Pooled training requires tenant training enabled AND explicit opt-in.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGatePooledTraining, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "to shadow",
	})
	require.NoError(t, err)
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGatePooledTraining, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "no opt-in",
	})
	assert.ErrorIs(t, err, ErrShadowGatePrerequisite, "pooled training must stay blocked without explicit opt-in")

	grant, err = db.New(h.gatePool()).GetTrainingGrantByWorkspace(h.ctx, h.workspace)
	require.NoError(t, err)
	optedIn, err := db.New(h.gatePool()).OptInPooledTrainingGrant(h.ctx, db.OptInPooledTrainingGrantParams{
		WorkspaceID: h.workspace, Actor: pgtype.Text{String: "owner", Valid: true}, ExpectedVersion: grant.PooledPolicyVersion,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, optedIn)

	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGatePooledTraining, To: ShadowPhaseEnabled,
		ExpectedVersion: 1, PolicyVersion: 1, Evidence: allGreenEvidence(), Actor: "operator", Reason: "opted in",
	})
	require.NoError(t, err)

	// Demoting reward shadow synchronously closes the global switches.
	_, err = svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateRewardShadow, To: ShadowPhaseDisabled,
		ExpectedVersion: 2, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollback",
	})
	require.NoError(t, err)
	policy, err = db.New(h.gatePool()).GetTrainingGovernancePolicy(h.ctx)
	require.NoError(t, err)
	assert.False(t, policy.SelectionEnabled, "reward shadow demotion must close selection")
	assert.False(t, policy.ExecutionEnabled, "reward shadow demotion must close execution")
}

// ---------------------------------------------------------------------------
// Step 4 RED: phase-order metadata is verifiable (spec §19 rollout order)
// ---------------------------------------------------------------------------

func TestShadowGateRolloutPhaseOrderIsAcyclic(t *testing.T) {
	ranks := map[ShadowGateName]int{}
	for _, gate := range AllShadowGates() {
		rank := ShadowGateRolloutRank(gate)
		require.GreaterOrEqual(t, rank, 0, "gate %s must have a rollout rank", gate)
		ranks[gate] = rank
	}
	// Every prerequisite gate activates strictly earlier in the §19 order.
	for _, gate := range AllShadowGates() {
		for _, prerequisite := range ShadowGatePrerequisites(gate) {
			assert.Less(t, ranks[prerequisite], ranks[gate],
				"prerequisite %s must outrank %s in the rollout order", prerequisite, gate)
		}
	}
	// Every gate declares at least one integrity canary dependency, and the
	// union of dependencies covers all six named canaries.
	covered := map[ShadowCanaryName]bool{}
	for _, gate := range AllShadowGates() {
		deps := ShadowGateDependencies(gate)
		require.NotEmpty(t, deps, "gate %s must declare canary dependencies", gate)
		for _, canary := range deps {
			covered[canary] = true
		}
	}
	for _, canary := range AllShadowCanaries() {
		assert.True(t, covered[canary], "canary %s is not a dependency of any gate", canary)
	}
	// The phase ladder is strictly ordered and linear.
	assert.Less(t, ShadowGatePhaseRank(ShadowPhaseDisabled), ShadowGatePhaseRank(ShadowPhaseShadow))
	assert.Less(t, ShadowGatePhaseRank(ShadowPhaseShadow), ShadowGatePhaseRank(ShadowPhaseEnabled))
}

// ---------------------------------------------------------------------------
// Step 2 RED: status/health surfacing (graph_memory_status.go)
// ---------------------------------------------------------------------------

func TestGraphMemoryStatusSurfacesShadowGatesAndCanaries(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()
	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateAtoms, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout",
	})
	require.NoError(t, err)

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	statusSvc := NewGraphMemoryStatusServiceWithPool(h.gatePool(), db.New(h.gatePool()), root)
	status, err := statusSvc.Status(h.ctx, h.workspace.String())
	require.NoError(t, err)
	require.NotNil(t, status.ShadowGates)
	found := false
	for _, gate := range status.ShadowGates {
		if gate.Gate == ShadowGateAtoms {
			found = true
			assert.Equal(t, ShadowPhaseShadow, gate.Phase)
		}
	}
	assert.True(t, found, "status must surface the promoted shadow gate")
	require.NotNil(t, status.ShadowCanaries)
	assert.Len(t, status.ShadowCanaries, len(AllShadowCanaries()), "status must surface every named canary")
}

// ---------------------------------------------------------------------------
// Sweep: the scheduler-facing composite (evaluate -> metrics -> shutdown)
// ---------------------------------------------------------------------------

func TestShadowGateSweepShutsDownOnRedCanary(t *testing.T) {
	h := newUniversalDAGShadowGateHarness(t)
	defer h.Close()
	h.seedCleanPublishedState(t)
	svc := h.shadowService()

	_, err := svc.PromoteGate(h.ctx, ShadowGatePromotion{
		WorkspaceID: h.workspace, Gate: ShadowGateCitations, To: ShadowPhaseShadow,
		ExpectedVersion: 0, Evidence: allGreenEvidence(), Actor: "operator", Reason: "rollout",
	})
	require.NoError(t, err)

	h.insertPublishedSegment(t, "seg-sweep-breach", 3, "published", "redaction_failed", h.channel)
	report, err := svc.Sweep(h.ctx, h.workspace)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.False(t, report.Canaries[ShadowCanarySanitizerFailOpen].OK)
	assert.NotEmpty(t, report.Shutdown, "a red canary must shut dependent gates during the sweep")

	status, err := svc.Gate(h.ctx, h.workspace, ShadowGateCitations)
	require.NoError(t, err)
	assert.Equal(t, ShadowPhaseDisabled, status.Phase, "sweep must demote gates with failed dependencies")

	// A green sweep is a no-op that demotes nothing.
	h2 := newUniversalDAGShadowGateHarness(t)
	defer h2.Close()
	h2.seedCleanPublishedState(t)
	svc2 := h2.shadowService()
	report, err = svc2.Sweep(h2.ctx, h2.workspace)
	require.NoError(t, err)
	assert.Empty(t, report.Shutdown, "green canaries must not shut anything down")
}
