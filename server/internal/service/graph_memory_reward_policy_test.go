// SPDX-License-Identifier: Apache-2.0

package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// rewardPolicyGraphStubDDL creates the graph dive tables (migrations 419/420
// shapes minus the agent-runtime/provider FKs that the publisher harness does
// not carry). Migrations 421 (sessions + outbox), 472 (training governance)
// and 473 (reward revisions) are then applied verbatim in the private schema.
const rewardPolicyGraphStubDDL = `
CREATE TABLE IF NOT EXISTS graph_memory_recall (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid        NOT NULL,
  task_id        uuid        NOT NULL,
  daemon_id      text        NOT NULL DEFAULT '',
  runtime_id     uuid,
  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id uuid        NOT NULL,
  graph_version  integer     NOT NULL CHECK (graph_version >= 1),
  status         text        NOT NULL DEFAULT 'accepted'
    CHECK (status IN ('accepted', 'exploring', 'explore_terminal', 'dive_queued', 'diving', 'completed', 'judge_failed', 'failed')),
  training_mode  text        NOT NULL DEFAULT 'offline_capture'
    CHECK (training_mode IN ('offline_capture', 'online_rl', 'offline_rl')),
  k              integer     NOT NULL DEFAULT 3 CHECK (k BETWEEN 1 AND 64),
  query          text        NOT NULL DEFAULT '',
  trace_id       text        NOT NULL DEFAULT '',
  schema_version integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  terminal_at    timestamptz
);
CREATE TABLE IF NOT EXISTS graph_memory_trajectory (
  id                 uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  recall_id          uuid        NOT NULL,
  workspace_id       uuid        NOT NULL,
  seed_index          integer     NOT NULL DEFAULT 0 CHECK (seed_index >= 0),
  status             text        NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'found', 'miss', 'error', 'budget', 'timeout')),
  error_kind         text        NOT NULL DEFAULT '',
  summary            text        NOT NULL DEFAULT '',
  viewed_node_ids    jsonb       NOT NULL DEFAULT '[]'::jsonb,
  submitted_node_ids jsonb,
  rounds             integer     NOT NULL DEFAULT 0 CHECK (rounds >= 0),
  model              text        NOT NULL DEFAULT '',
  runtime_meta       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  artifact_ref       text        NOT NULL DEFAULT '',
  schema_version      integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  terminal_at        timestamptz
);
-- Migration 420's trajectory columns (dive grading + reward), applied here as
-- stub growth because the publisher harness does not carry migration 420.
ALTER TABLE graph_memory_trajectory
  ADD COLUMN dive_status        text NOT NULL DEFAULT ''
    CHECK (dive_status IN ('', 'graded', 'bypassed', 'judge_failed')),
  ADD COLUMN score_relevance    double precision CHECK (score_relevance BETWEEN 0 AND 1),
  ADD COLUMN score_groundedness double precision CHECK (score_groundedness BETWEEN 0 AND 1),
  ADD COLUMN score_completeness double precision CHECK (score_completeness BETWEEN 0 AND 1),
  ADD COLUMN overall_score      double precision CHECK (overall_score BETWEEN 0 AND 1),
  ADD COLUMN reward             double precision;
CREATE TABLE IF NOT EXISTS graph_memory_dive_job (
  id               uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  recall_id        uuid        NOT NULL UNIQUE,
  workspace_id     uuid        NOT NULL,
  trace_id         text        NOT NULL DEFAULT '',
  graph_kind       text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id   uuid        NOT NULL,
  graph_version    integer     NOT NULL CHECK (graph_version >= 1),
  status           text        NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  attempts         integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts     integer     NOT NULL DEFAULT 4 CHECK (max_attempts >= 1),
  leased_by        text        NOT NULL DEFAULT '',
  lease_expires_at timestamptz,
  incomplete       boolean     NOT NULL DEFAULT false,
  error_kind       text        NOT NULL DEFAULT '',
  last_error       text        NOT NULL DEFAULT '',
  model            text        NOT NULL DEFAULT '',
  result           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  schema_version    integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  terminal_at      timestamptz
);
-- Migration 472 swaps agent_inbox_event's reason CHECK for the
-- training_replay-extended list; the publisher harness's bare table needs
-- the column first (mirrors the training harness stub).
ALTER TABLE agent_inbox_event
  ADD COLUMN IF NOT EXISTS reason text;

CREATE TABLE IF NOT EXISTS graph_memory_version_lease (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid        NOT NULL,
  graph_kind     text        NOT NULL,
  graph_owner_id uuid        NOT NULL,
  graph_version  integer     NOT NULL CHECK (graph_version >= 1),
  consumer_kind  text        NOT NULL CHECK (consumer_kind IN ('recall', 'dive', 'export', 'backtest')),
  consumer_id    uuid        NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  released_at    timestamptz
);
`

// rewardPolicyHarness: publisher harness (454+464+466+467) plus the graph
// stubs, then migrations 421, 472 and 473 applied verbatim in the private
// schema (graph stubs first, mirroring the training harness recipe).
type rewardPolicyHarness struct {
	*retractionHarness
}

func newRewardPolicyHarness(t *testing.T) *rewardPolicyHarness {
	t.Helper()
	h := &rewardPolicyHarness{retractionHarness: newRetractionHarness(t)}
	_, err := h.conn.Exec(h.ctx, rewardPolicyGraphStubDDL)
	require.NoError(t, err, "create graph reward stubs")
	applyRewardPolicyMigrations(t, h,
		"421_graph_memory_rl_session.up.sql",
		"472_interaction_dag_training_governance.up.sql",
		"473_graph_memory_reward_revision.up.sql")
	return h
}

func applyRewardPolicyMigrations(t *testing.T, h *rewardPolicyHarness, names ...string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate reward policy migrations")
	for _, name := range names {
		path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", name)
		migration, err := os.ReadFile(path)
		require.NoError(t, err, "read migration %s", name)
		_, err = h.conn.Exec(h.ctx, string(migration))
		require.NoError(t, err, "apply migration %s in private schema", name)
	}
}

// seedRewardRecall inserts one recall with k terminal trajectories in the
// given statuses and returns (recallID, trajectoryIDs ordered by seed).
func (h *rewardPolicyHarness) seedRewardRecall(t *testing.T, trainingMode string, statuses ...string) (string, []string) {
	t.Helper()
	recallID := uuid.NewString()
	// The dive lease validates the graph owner against the project table.
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO project (id, workspace_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, h.workspace, h.workspace)
	require.NoError(t, err, "seed owner project")
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_recall (id, workspace_id, task_id, daemon_id, graph_kind,
		                                  graph_owner_id, graph_version, status, training_mode, k, query, trace_id)
		VALUES ($1, $2, gen_random_uuid(), 'daemon-x', 'project', $2, 1, 'exploring', $3, $4,
		        'reward policy fixture query', $5)
	`, recallID, h.workspace, trainingMode, len(statuses), "trace-reward-"+recallID[:8])
	require.NoError(t, err, "seed recall")
	ids := make([]string, 0, len(statuses))
	for i, status := range statuses {
		id := uuid.NewString()
		_, err := h.conn.Exec(h.ctx, `
			INSERT INTO graph_memory_trajectory (id, recall_id, workspace_id, seed_index, status, summary, rounds, terminal_at)
			VALUES ($1, $2, $3, $4, $5, 'fixture summary', 3, now())
		`, id, recallID, h.workspace, i, status)
		require.NoError(t, err, "seed trajectory %d", i)
		ids = append(ids, id)
	}
	return recallID, ids
}

// leaseDiveJob enqueues and leases the recall's dive job for workerID.
func (h *rewardPolicyHarness) leaseDiveJob(t *testing.T, recallID, workerID string) *GraphMemoryDiveJob {
	t.Helper()
	dive := NewGraphMemoryDiveService(h.pubPool)
	if _, err := dive.EnqueueIfBarrierMet(h.ctx, recallID); err != nil {
		t.Fatal(err)
	}
	job, err := dive.Lease(h.ctx, workerID, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, job)
	return job
}

func (h *rewardPolicyHarness) openRewardSession(t *testing.T, trajectoryID string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_rl_session (workspace_id, trajectory_id, recall_id, status, generation, session_id, proxy_key, opened_at)
		SELECT $2, t.id, t.recall_id, 'open', 1, 'sess-' || left(t.id::text, 8), 'pk-' || left(t.id::text, 8), now()
		FROM graph_memory_trajectory t WHERE t.id = $1::uuid
	`, trajectoryID, h.workspace)
	require.NoError(t, err, "seed open rl session")
}

func (h *rewardPolicyHarness) trajectoryReward(t *testing.T, trajectoryID string) (reward *float64, status string, revision int) {
	t.Helper()
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT reward, reward_status, reward_revision FROM graph_memory_trajectory WHERE id = $1
	`, trajectoryID).Scan(&reward, &status, &revision))
	return
}

func (h *rewardPolicyHarness) rewardRecords(t *testing.T, trajectoryID string) []struct {
	Kind       string
	Revision   int
	Status     string
	Value      *float64
	Components map[string]any
	Policy     string
	Manifest   string
} {
	t.Helper()
	rows, err := h.conn.Query(h.ctx, `
		SELECT reward_kind, revision, status, value, components, policy_version, input_manifest_hash
		FROM graph_memory_reward_record WHERE trajectory_id = $1 ORDER BY revision
	`, trajectoryID)
	require.NoError(t, err)
	defer rows.Close()
	var out []struct {
		Kind       string
		Revision   int
		Status     string
		Value      *float64
		Components map[string]any
		Policy     string
		Manifest   string
	}
	for rows.Next() {
		var rec struct {
			Kind       string
			Revision   int
			Status     string
			Value      *float64
			Components map[string]any
			Policy     string
			Manifest   string
		}
		var components []byte
		require.NoError(t, rows.Scan(&rec.Kind, &rec.Revision, &rec.Status, &rec.Value, &components, &rec.Policy, &rec.Manifest))
		require.NoError(t, json.Unmarshal(components, &rec.Components))
		out = append(out, rec)
	}
	require.NoError(t, rows.Err())
	return out
}

func (h *rewardPolicyHarness) seedRewardRecord(t *testing.T, trajectoryID string, revision int, status string, value *float64, manifestHash string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_reward_record
		  (workspace_id, trajectory_id, reward_kind, revision, status, value,
		   components, policy_version, input_manifest_hash)
		VALUES ($1, $2, 'explore', $3, $4, $5, '{}', $6, $7)
	`, h.workspace, trajectoryID, revision, status, value, memorygraph.ExploreRewardPolicyVersion, manifestHash)
	require.NoError(t, err, "seed reward record rev %d", revision)
}

func (h *rewardPolicyHarness) outboxCount(t *testing.T, trajectoryID string) int {
	return h.countRows(t, `SELECT count(*) FROM graph_memory_reward_outbox WHERE trajectory_id = $1`, trajectoryID)
}

func (h *rewardPolicyHarness) diveService() *GraphMemoryDiveService {
	return NewGraphMemoryDiveService(h.pubPool)
}

func (h *rewardPolicyHarness) wsID() string { return h.workspace.String() }

// TestGraphMemoryRewardPolicy_GradedRewardWritesImmutableRecord: a graded
// dive result writes revision 1 of an immutable available record with its raw
// components, the policy version and the judged input-manifest hash, and a
// matching trajectory projection. Replaying the identical result is
// idempotent (same revision, no second row, value untouched).
func TestGraphMemoryRewardPolicy_GradedRewardWritesImmutableRecord(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	recallID, trajs := h.seedRewardRecall(t, "offline_rl", "found", "miss")
	job := h.leaseDiveJob(t, recallID, "worker-1")

	res := &memorygraph.DiveResult{
		Scores: []memorygraph.DiveTrajectoryScore{
			{TrajectoryID: trajs[0], Relevance: 0.9, Groundedness: 0.8, Completeness: 0.7},
			{TrajectoryID: trajs[1], Relevance: 0.4, Groundedness: 0.5, Completeness: 0.6},
		},
	}
	applied, err := h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", res, 0.1)
	require.NoError(t, err)
	require.True(t, applied)

	// reward = min-dimension overall − w_round × server rounds (rounds = 3).
	for i, want := range []float64{0.7 - 0.3, 0.4 - 0.3} {
		reward, status, revision := h.trajectoryReward(t, trajs[i])
		require.NotNil(t, reward, "graded reward must be numeric")
		assert.InDelta(t, want, *reward, 1e-9)
		assert.Equal(t, "graded", status)
		assert.Equal(t, 1, revision)
	}
	records := h.rewardRecords(t, trajs[0])
	require.Len(t, records, 1)
	assert.Equal(t, "explore", records[0].Kind)
	assert.Equal(t, "available", records[0].Status)
	require.NotNil(t, records[0].Value)
	assert.InDelta(t, 0.4, *records[0].Value, 1e-9)
	assert.Equal(t, memorygraph.ExploreRewardPolicyVersion, records[0].Policy)
	assert.NotEmpty(t, records[0].Manifest, "input manifest hash recorded")
	assert.InDelta(t, 0.9, numericComponent(t, records[0], "relevance"), 1e-9)
	assert.InDelta(t, 0.8, numericComponent(t, records[0], "groundedness"), 1e-9)
	assert.InDelta(t, 0.7, numericComponent(t, records[0], "completeness"), 1e-9)
	assert.InDelta(t, 0.7, numericComponent(t, records[0], "overall"), 1e-9)
	assert.InDelta(t, 0.1, numericComponent(t, records[0], "w_round"), 1e-9)
	assert.InDelta(t, 3, numericComponent(t, records[0], "rounds"), 1e-9)

	// Identical replay: same revision, single record, value untouched.
	applied, err = h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", res, 0.1)
	require.NoError(t, err)
	require.True(t, applied)
	records = h.rewardRecords(t, trajs[0])
	require.Len(t, records, 1, "idempotent replay must not add a revision")
	assert.Equal(t, 1, records[0].Revision)
	_, status, revision := h.trajectoryReward(t, trajs[0])
	assert.Equal(t, "graded", status)
	assert.Equal(t, 1, revision)
}

// TestGraphMemoryRewardPolicy_JudgeFailureIsUnavailableNotZero: a terminal
// judge infrastructure failure records reward_status=unavailable with a NULL
// value (never a numeric 0), and the unavailable trajectories never receive
// an outbox row (spec 14.2/A46: no zero-value reward is delivered).
func TestGraphMemoryRewardPolicy_JudgeFailureIsUnavailableNotZero(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	recallID, trajs := h.seedRewardRecall(t, "online_rl", "found", "miss")
	// Sessions exist and would happily accept an outbox row: the unavailable
	// status alone must keep the reward out of the outbox.
	for _, tr := range trajs {
		h.openRewardSession(t, tr)
	}
	job := h.leaseDiveJob(t, recallID, "worker-1")
	// One attempt only, so a retryable worker failure terminalizes.
	_, err := h.conn.Exec(h.ctx, `
		UPDATE graph_memory_dive_job SET max_attempts = 1 WHERE id = $1
	`, job.ID)
	require.NoError(t, err)
	terminal, err := h.diveService().Fail(h.ctx, job.ID, "worker-1", "infra", "model endpoint 503", true)
	require.NoError(t, err)
	require.True(t, terminal)

	for _, tr := range trajs {
		reward, status, revision := h.trajectoryReward(t, tr)
		assert.Nil(t, reward, "judge failure must not synthesize reward 0 (A46)")
		assert.Equal(t, "unavailable", status)
		assert.Equal(t, 1, revision)
		records := h.rewardRecords(t, tr)
		require.Len(t, records, 1)
		assert.Equal(t, "unavailable", records[0].Status)
		assert.Nil(t, records[0].Value)
		// The reward pipeline never enqueues an unavailable value.
		rl := NewGraphMemoryRLSessionService(h.pubPool, nil, nil)
		assert.Error(t, rl.EnqueueReward(h.ctx, tr, "explore", 1, 0),
			"unavailable reward must not enter the outbox")
		assert.Zero(t, h.outboxCount(t, tr))
	}
}

// TestGraphMemoryRewardPolicy_BudgetViolationDeterministicNegative: the
// explore agent's own budget violation receives a deterministic negative
// reward (never 0), classified as such in ledger and projection.
func TestGraphMemoryRewardPolicy_BudgetViolationDeterministicNegative(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	recallID, trajs := h.seedRewardRecall(t, "online_rl", "budget")
	job := h.leaseDiveJob(t, recallID, "worker-1")

	res := &memorygraph.DiveResult{
		Bypassed: []memorygraph.DiveRunInput{{TrajectoryID: trajs[0], Status: "budget", Rounds: 3}},
	}
	applied, err := h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", res, 0.1)
	require.NoError(t, err)
	require.True(t, applied)

	reward, status, revision := h.trajectoryReward(t, trajs[0])
	require.NotNil(t, reward)
	want := memorygraph.DeterministicViolationReward(0.1, 3)
	assert.InDelta(t, want, *reward, 1e-9)
	assert.Less(t, *reward, 0.0, "budget violation must be a negative reward")
	assert.Equal(t, "deterministic", status)
	assert.Equal(t, 1, revision)
	records := h.rewardRecords(t, trajs[0])
	require.Len(t, records, 1)
	assert.Equal(t, "available", records[0].Status, "deterministic negative is a usable RL value")
	require.NotNil(t, records[0].Value)
	assert.InDelta(t, want, *records[0].Value, 1e-9)
	assert.Equal(t, "deterministic", stringComponent(t, records[0], "source"))
	assert.Equal(t, "budget", stringComponent(t, records[0], "violation"))
}

// TestGraphMemoryRewardPolicy_RevisionConflictOnInconsistentReplay: the same
// identity (trajectory, kind) judged again under the SAME input manifest must
// replay the identical value; a different value is a conflict and never
// overwrites the record (spec 14.4: replay consistency, A48).
func TestGraphMemoryRewardPolicy_RevisionConflictOnInconsistentReplay(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	_, trajs := h.seedRewardRecall(t, "offline_rl", "found")
	traj, err := util.ParseUUID(trajs[0])
	require.NoError(t, err)

	base := TrajectoryRewardRecord{
		WorkspaceID:       h.workspace,
		TrajectoryID:      traj,
		RewardKind:        "explore",
		Status:            "available",
		Value:             ptrFloat64(0.5),
		Components:        memorygraph.RewardComponents{Source: "graded", Overall: 0.6, WRound: 0.1, Rounds: 1},
		PolicyVersion:     memorygraph.ExploreRewardPolicyVersion,
		InputManifestHash: "manifest-A",
	}
	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	rev, err := RecordTrajectoryRewardTx(h.ctx, tx, base)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(h.ctx))
	assert.Equal(t, 1, rev)

	// Consistent replay: idempotent, same revision.
	tx, err = h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	rev, err = RecordTrajectoryRewardTx(h.ctx, tx, base)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(h.ctx))
	assert.Equal(t, 1, rev, "consistent replay is idempotent")
	assert.Len(t, h.rewardRecords(t, trajs[0]), 1)

	// Conflicting replay: same manifest, different value -> error, no
	// overwrite, no extra revision.
	conflicting := base
	conflicting.Value = ptrFloat64(0.9)
	tx, err = h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	_, err = RecordTrajectoryRewardTx(h.ctx, tx, conflicting)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRewardRevisionConflict), "want ErrRewardRevisionConflict, got %v", err)
	require.NoError(t, tx.Rollback(h.ctx))
	records := h.rewardRecords(t, trajs[0])
	require.Len(t, records, 1)
	assert.InDelta(t, 0.5, *records[0].Value, 1e-9, "conflicting replay must not overwrite")
}

// TestGraphMemoryRewardPolicy_ReevaluationCreatesNewRevision: a re-evaluation
// under a different input manifest creates revision 2 while revision 1 stays
// intact (revisions never overwrite consumed history, A48).
func TestGraphMemoryRewardPolicy_ReevaluationCreatesNewRevision(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	recallID, trajs := h.seedRewardRecall(t, "offline_rl", "found")
	job := h.leaseDiveJob(t, recallID, "worker-1")

	first := &memorygraph.DiveResult{Scores: []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajs[0], Relevance: 0.9, Groundedness: 0.9, Completeness: 0.9},
	}}
	applied, err := h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", first, 0.1)
	require.NoError(t, err)
	require.True(t, applied)

	second := &memorygraph.DiveResult{Scores: []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajs[0], Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}}
	applied, err = h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", second, 0.1)
	require.NoError(t, err)
	require.True(t, applied)

	records := h.rewardRecords(t, trajs[0])
	require.Len(t, records, 2, "re-evaluation adds a revision")
	assert.Equal(t, 1, records[0].Revision)
	assert.InDelta(t, 0.9-0.3, *records[0].Value, 1e-9, "revision 1 keeps its own value")
	assert.Equal(t, 2, records[1].Revision)
	assert.InDelta(t, 0.5-0.3, *records[1].Value, 1e-9)
	_, status, revision := h.trajectoryReward(t, trajs[0])
	assert.Equal(t, "graded", status)
	assert.Equal(t, 2, revision, "projection tracks the latest revision")
}

// TestGraphMemoryRewardPolicy_OutboxExactlyOncePerRevision: the outbox keys
// delivery identity by (trajectory, reward_kind, revision): replays insert
// nothing, a second revision inserts exactly one more row, and claims carry
// the full identity (spec 14.4, Task 19 Step 5).
func TestGraphMemoryRewardPolicy_OutboxExactlyOncePerRevision(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	_, trajs := h.seedRewardRecall(t, "online_rl", "found")
	h.openRewardSession(t, trajs[0])
	rl := NewGraphMemoryRLSessionService(h.pubPool, nil, nil)

	// Ledger revisions the enqueue validates against (write-once records).
	v1, v2 := 0.5, 0.9
	h.seedRewardRecord(t, trajs[0], 1, "available", &v1, "manifest-outbox-1")
	h.seedRewardRecord(t, trajs[0], 2, "available", &v2, "manifest-outbox-2")

	require.NoError(t, rl.EnqueueReward(h.ctx, trajs[0], "explore", 1, 0.5))
	require.NoError(t, rl.EnqueueReward(h.ctx, trajs[0], "explore", 1, 0.5))
	assert.Equal(t, 1, h.outboxCount(t, trajs[0]), "same identity enqueues exactly once")

	require.NoError(t, rl.EnqueueReward(h.ctx, trajs[0], "explore", 2, 0.9))
	assert.Equal(t, 2, h.outboxCount(t, trajs[0]), "a new revision adds exactly one row")

	claimed, err := rl.ClaimPending(h.ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	byRevision := map[int]arealrlPendingIdentity{}
	for _, c := range claimed {
		assert.Equal(t, "explore", c.RewardKind)
		byRevision[c.RewardRevision] = arealrlPendingIdentity{reward: c.Reward}
	}
	assert.InDelta(t, 0.5, byRevision[1].reward, 1e-9)
	assert.InDelta(t, 0.9, byRevision[2].reward, 1e-9)
}

type arealrlPendingIdentity struct{ reward float64 }

// TestGraphMemoryRewardPolicy_SelectionAndExportExcludeUnavailable: a
// judge-failed (unavailable) trajectory is never selectable for training
// while a graded sibling remains selectable, and the offline export
// eligibility matrix excludes it (Task 19 Step 3).
func TestGraphMemoryRewardPolicy_SelectionAndExportExcludeUnavailable(t *testing.T) {
	h := newRewardPolicyHarness(t)
	defer h.Close()
	recallID, trajs := h.seedRewardRecall(t, "offline_rl", "found", "found")

	// Trajectory 0 grades normally; trajectory 1 ends judge_failed.
	job := h.leaseDiveJob(t, recallID, "worker-1")
	res := &memorygraph.DiveResult{Scores: []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajs[0], Relevance: 0.9, Groundedness: 0.9, Completeness: 0.9},
	}}
	applied, err := h.diveService().ApplyDiveResult(h.ctx, job.ID, "worker-1", res, 0.1)
	require.NoError(t, err)
	require.True(t, applied)
	completed, err := h.diveService().Complete(h.ctx, job.ID, "worker-1", false, []byte(`{"scores":1}`))
	require.NoError(t, err)
	require.True(t, completed)

	_, err = h.conn.Exec(h.ctx, `
		UPDATE graph_memory_trajectory
		SET reward = NULL, reward_status = 'unavailable', dive_status = 'judge_failed', reward_revision = 1
		WHERE id = $1
	`, trajs[1])
	require.NoError(t, err)

	// The offline export matrix excludes the unavailable trajectory.
	eligible, reason := ClassifyOfflineExportEligibility("offline_rl", "judge_failed", true, true, false)
	assert.False(t, eligible)
	assert.Equal(t, GraphMemoryOfflineReasonJudgeFailed, reason)

	// Training selection: policy + grant open, then the graded trajectory
	// alone is a candidate; the unavailable one never appears.
	gov := NewTrainingGovernanceService(h.pubPool, nil)
	// The harness applies migration 472 after the workspace exists, so a
	// backfilled grant row is already present; bootstrap owns a fresh one.
	_, err = h.conn.Exec(h.ctx, `
		DELETE FROM interaction_dag_training_grant WHERE workspace_id = $1
	`, h.workspace)
	require.NoError(t, err)
	_, err = gov.BootstrapNewWorkspaceGrant(h.ctx, h.wsID(), false, "test:operator")
	require.NoError(t, err)
	_, err = gov.AckTenantGrant(h.ctx, h.wsID(), "user:owner", 0)
	require.NoError(t, err)
	on := true
	_, err = gov.SetTrainingPolicy(h.ctx, TrainingPolicyPatch{SelectionEnabled: &on}, "test:operator")
	require.NoError(t, err)
	q := db.New(h.pubPool)
	ws, err := util.ParseUUID(h.wsID())
	require.NoError(t, err)
	candidates, err := q.ListTrainingGraphTrajectoryCandidates(h.ctx, db.ListTrainingGraphTrajectoryCandidatesParams{
		WorkspaceID: ws, LimitCount: 10,
	})
	require.NoError(t, err)
	require.Len(t, candidates, 1, "only the graded trajectory is selectable")
	assert.Equal(t, trajs[0], candidates[0].ItemKey)
	assert.NotEqual(t, trajs[1], candidates[0].ItemKey, "unavailable reward never trains")
}

// --- helpers ------------------------------------------------------------

func numericComponent(t *testing.T, rec struct {
	Kind       string
	Revision   int
	Status     string
	Value      *float64
	Components map[string]any
	Policy     string
	Manifest   string
}, key string) float64 {
	t.Helper()
	v, ok := rec.Components[key]
	require.True(t, ok, "component %q present", key)
	num, ok := v.(float64)
	require.True(t, ok, "component %q numeric, got %T", key, v)
	return num
}

func stringComponent(t *testing.T, rec struct {
	Kind       string
	Revision   int
	Status     string
	Value      *float64
	Components map[string]any
	Policy     string
	Manifest   string
}, key string) string {
	t.Helper()
	v, ok := rec.Components[key]
	require.True(t, ok, "component %q present", key)
	s, ok := v.(string)
	require.True(t, ok)
	return s
}

func ptrFloat64(v float64) *float64 { return &v }
