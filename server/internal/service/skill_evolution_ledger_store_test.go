// SPDX-License-Identifier: Apache-2.0

package service

// Migration 492 ledger behavior against the faithful schema: run lifecycle
// through the real coordinator wiring, single-active admission, linear
// append-only pattern revisions, and workspace-scoped fail-closed reads.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

type skillEvolutionLedgerFixture struct {
	ledger      *PostgresSkillEvolutionLedger
	coordinator *skillevolution.RunCoordinator
	workspaceID string
	agentID     string
	pool        *pgxpool.Pool
}

func newSkillEvolutionLedgerFixture(t *testing.T) *skillEvolutionLedgerFixture {
	t.Helper()
	pool := bootstrapUniversalDAGProjectionSchema(t)
	ctx := context.Background()
	var workspaceID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug) VALUES ('evolution ledger test', 'evo-'||$1)
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&workspaceID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, workspaceID) })

	var ownerID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('evo-owner-'||$1, 'evo-owner-'||$1||'@multica.ai')
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&ownerID))

	var runtimeID, agentID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1::uuid,$2,'evo-runtime','local','pi','online','','{}','private',now()) RETURNING id::text`,
		workspaceID, "evo-daemon-"+uuid.NewString()[:8]).Scan(&runtimeID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,owner_id,managed_role,instructions)
		VALUES($1::uuid,$2,'Ledger agent','local','{}',$3::uuid,$4::uuid,'graph_memory_channel','managed memory') RETURNING id::text`,
		workspaceID, "evo-agent-"+uuid.NewString()[:8], runtimeID, ownerID).Scan(&agentID))

	ledger := NewPostgresSkillEvolutionLedger(pool)
	return &skillEvolutionLedgerFixture{
		ledger: ledger, coordinator: skillevolution.NewRunCoordinator(ledger),
		workspaceID: workspaceID, agentID: agentID, pool: pool,
	}
}

func (f *skillEvolutionLedgerFixture) runRecord(runID string) skillevolution.EvolutionRunRecord {
	return skillevolution.EvolutionRunRecord{
		RunID: runID, WorkspaceID: f.workspaceID, TargetAgentID: f.agentID,
		TaskType: "spreadsheet", EnvironmentMajorVersion: "v1",
		PinnedInputs: []byte(`{"dataset":"fixture"}`), CreatedByActor: "member:curator",
	}
}

func (f *skillEvolutionLedgerFixture) patternRecord(patternID string, revision int64, status skillevolution.PatternStatus) skillevolution.PatternRecord {
	now := time.Now().UTC()
	hashHex := "sha256:" + strings.Repeat("ab", 32) // the domain sha256 shape
	return skillevolution.PatternRecord{
		ContractKind: "pattern", SchemaVersion: 1,
		PatternID: patternID, Revision: revision,
		WorkspaceID:       f.workspaceID,
		EvolutionKey:      f.agentID + ":spreadsheet:v1",
		PatternKind:       skillevolution.PatternKindSuccess,
		Status:            status,
		Problem:           "dispatch retries exhaust the pool",
		Applicability:     "spreadsheet export tasks",
		RootCauseSummary:  "missing backoff between retry attempts",
		RecommendedAction: "exponential backoff on retry",
		PositiveEvidence: []skillevolution.SkillEvolutionRef{
			{Kind: skillevolution.RefEvaluationRun, ID: "eval-" + patternID, WorkspaceID: f.workspaceID},
		},
		NegativeEvidence: []skillevolution.SkillEvolutionRef{
			{Kind: skillevolution.RefPattern, ID: "counter-" + patternID, WorkspaceID: f.workspaceID},
		},
		TaskType: "spreadsheet", CreatedByActor: "member:curator",
		ContentHash: hashHex,
		CreatedAt:   now, UpdatedAt: now,
	}
}

// The full lifecycle persists through the real PostgreSQL port: the
// generated evolution key round-trips, terminal transitions stamp
// terminal_at, CAS misses conflict, and the DB trigger refuses revival.
func TestSkillEvolutionLedgerRunLifecyclePersists(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	runID := uuid.NewString()

	run, err := f.coordinator.StartRun(ctx, f.runRecord(runID))
	require.NoError(t, err)
	assert.Equal(t, skillevolution.EvolutionRunQueued, run.Status)
	assert.Equal(t, f.agentID+":"+"spreadsheet:v1",
		skillevolution.EvolutionKey{
			TargetAgentID: run.TargetAgentID, TaskType: run.TaskType,
			EnvironmentMajorVersion: run.EnvironmentMajorVersion,
		}.Body())

	for _, next := range []skillevolution.EvolutionRunStatus{
		skillevolution.EvolutionRunSnapshotting, skillevolution.EvolutionRunConsolidatingPatterns,
		skillevolution.EvolutionRunProposingCandidate, skillevolution.EvolutionRunAwaitingReview,
		skillevolution.EvolutionRunEvaluating, skillevolution.EvolutionRunAwaitingApproval,
		skillevolution.EvolutionRunCompleted,
	} {
		run, err = f.coordinator.Transition(ctx, f.workspaceID, runID, next)
		require.NoError(t, err, "transition to %s", next)
	}
	require.NotNil(t, run.TerminalAt, "the completed run is stamped terminal_at")

	// A stale CAS from is a conflict, not an overwrite.
	err = f.ledger.TransitionRun(ctx, f.workspaceID, runID,
		skillevolution.EvolutionRunAwaitingApproval, skillevolution.EvolutionRunCancelled)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// Reviving the terminal run fails on the DB trigger.
	_, err = f.coordinator.Transition(ctx, f.workspaceID, runID, skillevolution.EvolutionRunQueued)
	require.Error(t, err, "the terminal guard must refuse revival")
}

// One mutation lane admits one active run; going terminal frees the key.
func TestSkillEvolutionLedgerSingleActiveRunPerKey(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	_, err := f.coordinator.StartRun(ctx, f.runRecord(uuid.NewString()))
	require.NoError(t, err)
	_, err = f.coordinator.StartRun(ctx, f.runRecord(uuid.NewString()))
	require.ErrorIs(t, err, skillevolution.ErrActiveRunExists)

	otherLane := f.runRecord(uuid.NewString())
	otherLane.TaskType = "coding"
	_, err = f.coordinator.StartRun(ctx, otherLane)
	require.NoError(t, err, "a different task type is a different lane")
}

// Pattern revisions append linearly and never mutate: the round-trip
// serves the newest revision with its evidence, duplicate or non-linear
// revisions conflict, and direct UPDATEs hit the append-only trigger.
func TestSkillEvolutionLedgerPatternRevisionsAreAppendOnly(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	require.NoError(t, f.ledger.InsertPatternRevision(ctx, f.patternRecord("pat-ledger", 1, skillevolution.PatternStatusTentative)))
	latest, err := f.ledger.LatestPatternRevision(ctx, f.workspaceID, "pat-ledger")
	require.NoError(t, err)
	assert.Equal(t, int64(1), latest.Revision)
	assert.Equal(t, skillevolution.PatternStatusTentative, latest.Status)
	require.Len(t, latest.PositiveEvidence, 1)
	require.Len(t, latest.NegativeEvidence, 1)
	assert.Equal(t, "eval-pat-ledger", latest.PositiveEvidence[0].ID)

	require.NoError(t, f.ledger.InsertPatternRevision(ctx, f.patternRecord("pat-ledger", 2, skillevolution.PatternStatusSupported)))
	latest, err = f.ledger.LatestPatternRevision(ctx, f.workspaceID, "pat-ledger")
	require.NoError(t, err)
	assert.Equal(t, int64(2), latest.Revision, "the newest revision wins")
	assert.Equal(t, skillevolution.PatternStatusSupported, latest.Status)

	err = f.ledger.InsertPatternRevision(ctx, f.patternRecord("pat-ledger", 2, skillevolution.PatternStatusStale))
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "a duplicate revision never overwrites")
	err = f.ledger.InsertPatternRevision(ctx, f.patternRecord("pat-ledger", 5, skillevolution.PatternStatusStale))
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "a non-linear revision is a conflict")

	_, err = f.ledger.pool.Exec(ctx,
		`UPDATE skill_pattern_revision SET problem='smuggled rewrite' WHERE workspace_id=$1::uuid AND pattern_id='pat-ledger'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse in-place rewrites")
}

// Ledger reads and writes are workspace-scoped: a run does not resolve in
// another workspace and evidence from another workspace is rejected by the
// DB CHECK, not just by the contract.
func TestSkillEvolutionLedgerScopeFailsClosedAcrossWorkspaces(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	runID := uuid.NewString()
	_, err := f.coordinator.StartRun(ctx, f.runRecord(runID))
	require.NoError(t, err)

	_, err = f.ledger.GetRun(ctx, uuid.NewString(), runID)
	require.ErrorIs(t, err, skillevolution.ErrLedgerNotFound, "a run never resolves outside its workspace")

	foreign := f.patternRecord("pat-foreign", 1, skillevolution.PatternStatusTentative)
	foreign.PositiveEvidence[0].WorkspaceID = uuid.NewString()
	err = f.ledger.InsertPatternRevision(ctx, foreign)
	require.Error(t, err, "cross-workspace evidence must fail on the DB CHECK")
}

func (f *skillEvolutionLedgerFixture) manifestRecord(manifestID string, version int) skillevolution.AssertionManifest {
	hashHex := "sha256:" + strings.Repeat("cd", 32)
	return skillevolution.AssertionManifest{
		ContractKind: "assertion_manifest", SchemaVersion: 1,
		ManifestID: manifestID, Version: version,
		WorkspaceID: f.workspaceID, ManifestHash: hashHex,
		DatasetIdentity: "spreadsheet-export-dataset", DatasetVersion: "1.0.0",
		LineageSplit: "holdout:2026-09", DomainProfile: "spreadsheet",
		TaskSlices:           []byte(`["formula","style"]`),
		EvaluatorVersion:     "evaluator-1.0.0",
		ScorerVersion:        "scorer-1.0.0",
		EnvironmentKey:       "env-v1",
		RequiredCapabilities: []byte(`["xlsx.read"]`),
		DataResidency:        "eu",
		Assertions: []skillevolution.AssertionSpec{
			{AssertionID: "assert-1", Kind: "value", OracleRefHash: hashHex, Severity: "critical", Hard: true, Required: true, Tolerance: "0"},
			{AssertionID: "assert-2", Kind: "formula", OracleRefHash: hashHex, Severity: "major", Hard: false, Required: false, Tolerance: "1e-9"},
		},
		CreatedByActor: "member:curator", CreatedAt: time.Now().UTC(),
	}
}

func (f *skillEvolutionLedgerFixture) insertCandidate(t *testing.T, workspaceID, runID, candidateID string) {
	t.Helper()
	hashHex := "sha256:" + strings.Repeat("ab", 32)
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO skill_candidate (
			workspace_id, candidate_id, run_id, new_skill_name, requested_scope,
			base_artifact_hash, candidate_artifact_hash, proposed_diff_hash,
			contract_hash, contract
		) VALUES ($1::uuid, $2, $3::uuid, 'export_helper', 'agent', $4, $4, $4, $4, '{"contract_kind":"skill_candidate"}'::jsonb)`,
		workspaceID, candidateID, runID, hashHex)
	require.NoError(t, err)
}

func (f *skillEvolutionLedgerFixture) evaluationRecord(evaluationID, candidateID, manifestID string) skillevolution.EvaluationRunRecord {
	hashHex := "sha256:" + strings.Repeat("ab", 32)
	return skillevolution.EvaluationRunRecord{
		ContractKind: "evaluation_run", SchemaVersion: 1,
		EvaluationID: evaluationID, WorkspaceID: f.workspaceID,
		CandidateID: candidateID, ManifestID: manifestID, ManifestVersion: 1,
		BaseArtifactHash: hashHex, CandidateArtifactHash: hashHex,
		ManifestHash:  "sha256:" + strings.Repeat("cd", 32),
		TargetAgentID: f.agentID, TargetModelID: "model-x", ProviderID: "provider-1",
		ToolCapabilityID: "xlsx.read", RuntimeID: "runtime-1", EnvironmentKey: "env-v1",
		AssertionResults: []skillevolution.AssertionResult{
			{AssertionID: "assert-1", Result: skillevolution.AssertionPassed, EvidenceHash: hashHex},
			{AssertionID: "assert-2", Result: skillevolution.AssertionNotRun, EvidenceHash: hashHex},
		},
		Metrics:               []byte(`{"correctness":1,"safety":1}`),
		Contamination:         skillevolution.ContaminationClean,
		DecisionPolicyVersion: "policy-1",
		TerminalResult:        skillevolution.EvaluationPassed,
		TerminalReason:        "required assertions passed",
		CreatedByActor:        "member:evaluator",
		CreatedAt:             time.Now().UTC(),
	}
}

// Manifest versions are immutable: an identical replay is a no-op, the
// same version with a different payload conflicts, and a changed contract
// lands as a new version without disturbing the old one.
func TestSkillEvolutionLedgerManifestVersionsAreImmutable(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	require.NoError(t, f.ledger.InsertManifest(ctx, f.manifestRecord("man-ledger", 1)))
	fetched, err := f.ledger.GetManifest(ctx, f.workspaceID, "man-ledger", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, fetched.Version)
	assert.Equal(t, "spreadsheet-export-dataset", fetched.DatasetIdentity)
	require.Len(t, fetched.Assertions, 2)
	assert.Equal(t, "assert-1", fetched.Assertions[0].AssertionID)
	assert.True(t, fetched.Assertions[0].Hard)

	require.NoError(t, f.ledger.InsertManifest(ctx, f.manifestRecord("man-ledger", 1)),
		"an identical replay is a no-op")
	fetched, err = f.ledger.GetManifest(ctx, f.workspaceID, "man-ledger", 1)
	require.NoError(t, err)
	require.Len(t, fetched.Assertions, 2, "the replay must not duplicate assertions")

	conflicting := f.manifestRecord("man-ledger", 1)
	conflicting.ManifestHash = "sha256:" + strings.Repeat("ef", 32)
	err = f.ledger.InsertManifest(ctx, conflicting)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict,
		"the same version with a different payload never overwrites")

	next := f.manifestRecord("man-ledger", 2)
	next.Assertions = append(next.Assertions, skillevolution.AssertionSpec{
		AssertionID: "assert-3", Kind: "output_path", OracleRefHash: next.ManifestHash,
		Severity: "critical", Hard: true, Required: true,
	})
	require.NoError(t, f.ledger.InsertManifest(ctx, next))
	fetched, err = f.ledger.GetManifest(ctx, f.workspaceID, "man-ledger", 2)
	require.NoError(t, err)
	require.Len(t, fetched.Assertions, 3)
	fetched, err = f.ledger.GetManifest(ctx, f.workspaceID, "man-ledger", 1)
	require.NoError(t, err)
	require.Len(t, fetched.Assertions, 2, "version 1 is untouched by version 2")

	_, err = f.pool.Exec(ctx,
		`UPDATE skill_assertion_manifest SET dataset_identity='smuggled' WHERE workspace_id=$1::uuid AND manifest_id='man-ledger'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse manifest rewrites")
}

// Evaluation runs append with their per-assertion results, round-trip
// through GetEvaluationRun, and refuse every in-place mutation.
func TestSkillEvolutionLedgerEvaluationRunsAreAppendOnly(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	runID := uuid.NewString()
	_, err := f.coordinator.StartRun(ctx, f.runRecord(runID))
	require.NoError(t, err)
	require.NoError(t, f.ledger.InsertManifest(ctx, f.manifestRecord("man-eval", 1)))
	f.insertCandidate(t, f.workspaceID, runID, "cand-eval")

	record := f.evaluationRecord("eval-ledger", "cand-eval", "man-eval")
	require.NoError(t, f.ledger.InsertEvaluationRun(ctx, record))

	fetched, err := f.ledger.GetEvaluationRun(ctx, f.workspaceID, "eval-ledger")
	require.NoError(t, err)
	assert.Equal(t, "cand-eval", fetched.CandidateID)
	assert.Equal(t, "man-eval", fetched.ManifestID)
	assert.Equal(t, 1, fetched.ManifestVersion)
	assert.Equal(t, skillevolution.ContaminationClean, fetched.Contamination)
	assert.Equal(t, skillevolution.EvaluationPassed, fetched.TerminalResult)
	assert.JSONEq(t, `{"correctness":1,"safety":1}`, string(fetched.Metrics))
	require.Len(t, fetched.AssertionResults, 2)
	assert.Equal(t, "assert-1", fetched.AssertionResults[0].AssertionID)
	assert.Equal(t, skillevolution.AssertionPassed, fetched.AssertionResults[0].Result)

	runs, err := f.ledger.ListEvaluationRunsByCandidate(ctx, f.workspaceID, "cand-eval")
	require.NoError(t, err)
	require.Len(t, runs, 1)

	err = f.ledger.InsertEvaluationRun(ctx, f.evaluationRecord("eval-ledger", "cand-eval", "man-eval"))
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "a reused evaluation id is a conflict")

	_, err = f.pool.Exec(ctx,
		`UPDATE skill_evaluation_run SET terminal_result='failed' WHERE workspace_id=$1::uuid AND evaluation_id='eval-ledger'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse run rewrites")
	_, err = f.pool.Exec(ctx,
		`DELETE FROM skill_evaluation_run WHERE workspace_id=$1::uuid AND evaluation_id='eval-ledger'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse run deletion")
	_, err = f.pool.Exec(ctx,
		`UPDATE skill_evaluation_assertion_result SET result='fail' WHERE workspace_id=$1::uuid AND evaluation_id='eval-ledger'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse result rewrites")
}

// The evaluation plane fails closed on scope: results only resolve against
// assertions the pinned manifest declares, candidates belong to the same
// workspace, reads never cross tenants, and the DB contamination gate
// holds even if a store bug skips the contract check.
func TestSkillEvolutionLedgerEvaluationScopeFailsClosed(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	runID := uuid.NewString()
	_, err := f.coordinator.StartRun(ctx, f.runRecord(runID))
	require.NoError(t, err)
	require.NoError(t, f.ledger.InsertManifest(ctx, f.manifestRecord("man-scope", 1)))
	f.insertCandidate(t, f.workspaceID, runID, "cand-scope")

	undeclared := f.evaluationRecord("eval-undeclared", "cand-scope", "man-scope")
	undeclared.AssertionResults = append(undeclared.AssertionResults, skillevolution.AssertionResult{
		AssertionID: "assert-smuggled", Result: skillevolution.AssertionPassed,
		EvidenceHash: "sha256:" + strings.Repeat("ab", 32),
	})
	err = f.ledger.InsertEvaluationRun(ctx, undeclared)
	require.Error(t, err, "results may only reference declared assertions")

	// A second tenant with its own run and candidate: an evaluation in the
	// first workspace can never score it.
	var otherWorkspaceID string
	require.NoError(t, f.pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('evo foreign', 'evof-'||$1) RETURNING id::text`,
		uuid.NewString()[:8]).Scan(&otherWorkspaceID))
	t.Cleanup(func() { _, _ = f.pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, otherWorkspaceID) })
	otherRun := f.runRecord(uuid.NewString())
	otherRun.WorkspaceID = otherWorkspaceID
	_, err = f.coordinator.StartRun(ctx, otherRun)
	require.NoError(t, err)
	f.insertCandidate(t, otherWorkspaceID, otherRun.RunID, "cand-foreign")

	crossTenant := f.evaluationRecord("eval-cross", "cand-foreign", "man-scope")
	err = f.ledger.InsertEvaluationRun(ctx, crossTenant)
	require.Error(t, err, "the scoped candidate FK must refuse cross-tenant scoring")

	require.NoError(t, f.ledger.InsertEvaluationRun(ctx, f.evaluationRecord("eval-scope", "cand-scope", "man-scope")))
	_, err = f.ledger.GetEvaluationRun(ctx, otherWorkspaceID, "eval-scope")
	require.ErrorIs(t, err, skillevolution.ErrLedgerNotFound,
		"an evaluation never resolves outside its workspace")

	// The DB-level contamination gate: a direct insert bypassing the domain
	// contract still cannot record a contaminated pass.
	_, err = f.pool.Exec(ctx, `
		INSERT INTO skill_evaluation_run (
			workspace_id, evaluation_id, candidate_id, manifest_id, manifest_version,
			base_artifact_hash, candidate_artifact_hash, manifest_hash,
			target_agent_id, contamination_status, terminal_result, created_by_actor
		) VALUES ($1::uuid, 'eval-dirty', 'cand-scope', 'man-scope', 1, $2, $2, $2, $3::uuid, 'confirmed', 'passed', 'member:evaluator')`,
		f.workspaceID, "sha256:"+strings.Repeat("ab", 32), f.agentID)
	require.Error(t, err, "a contaminated run must never pass, even at the DB floor")
}

type decisionChain struct {
	runID, candidateID, manifestID, evaluationID string
}

func (f *skillEvolutionLedgerFixture) seedDecisionChain(t *testing.T, suffix string) decisionChain {
	t.Helper()
	ctx := context.Background()
	runID := uuid.NewString()
	runRecord := f.runRecord(runID)
	// Each chain occupies its own evolution lane so the single-active-run
	// fence (correctly) does not block the fixture's second chain.
	runRecord.TaskType = "spreadsheet-" + suffix
	_, err := f.coordinator.StartRun(ctx, runRecord)
	require.NoError(t, err)
	manifestID := "man-" + suffix
	require.NoError(t, f.ledger.InsertManifest(ctx, f.manifestRecord(manifestID, 1)))
	candidateID := "cand-" + suffix
	f.insertCandidate(t, f.workspaceID, runID, candidateID)
	evaluationID := "eval-" + suffix
	require.NoError(t, f.ledger.InsertEvaluationRun(ctx, f.evaluationRecord(evaluationID, candidateID, manifestID)))
	return decisionChain{runID: runID, candidateID: candidateID, manifestID: manifestID, evaluationID: evaluationID}
}

func (f *skillEvolutionLedgerFixture) approvalRecord(approvalID, candidateID, evaluationID, approver string) skillevolution.ApprovalRecord {
	hash := "sha256:" + strings.Repeat("ab", 32)
	return skillevolution.ApprovalRecord{
		ContractKind: "approval", SchemaVersion: 1,
		ApprovalID: approvalID, WorkspaceID: f.workspaceID,
		CandidateID: candidateID,
		EvaluationRef: skillevolution.SkillEvolutionRef{
			Kind: skillevolution.RefEvaluationRun, ID: evaluationID, WorkspaceID: f.workspaceID,
		},
		ManifestHash: hash, PolicyHash: hash, ArtifactHash: hash,
		TargetScope: "agent", Decision: skillevolution.ApprovalApproved,
		ApproverActor: approver, ApproverRole: "agent_owner",
		Reason:           "diff, evidence, and permission delta reviewed",
		RiskAcknowledged: true, AllowAutoRollback: true,
		ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now().UTC(),
	}
}

func (f *skillEvolutionLedgerFixture) deploymentRecord(deploymentID, candidateID, approvalID string) skillevolution.DeploymentRecord {
	return skillevolution.DeploymentRecord{
		ContractKind: "deployment", SchemaVersion: 1,
		DeploymentID: deploymentID, WorkspaceID: f.workspaceID,
		CandidateID: candidateID, ApprovalID: approvalID,
		TargetScope: "agent", TargetAgentID: f.agentID,
		BindingStateBefore: "unbound", BindingStateAfter: "bound",
		FromArtifactHash:      "sha256:" + strings.Repeat("ab", 32),
		ToArtifactHash:        "sha256:" + strings.Repeat("ef", 32),
		MaterializationStatus: skillevolution.MaterializationPending,
		CreatedByActor:        "activation-service",
		CreatedAt:             time.Now().UTC(),
	}
}

func (f *skillEvolutionLedgerFixture) rollbackRecord(rollbackID, deploymentID string) skillevolution.RollbackRecord {
	return skillevolution.RollbackRecord{
		ContractKind: "rollback", SchemaVersion: 1,
		RollbackID: rollbackID, WorkspaceID: f.workspaceID,
		DeploymentID: deploymentID, Trigger: skillevolution.RollbackSafetyFence,
		FromArtifactHash: "sha256:" + strings.Repeat("ef", 32),
		ToArtifactHash:   "sha256:" + strings.Repeat("ab", 32),
		InFlightPolicy:   "fenced", Actor: "fence-service",
		PolicyVersion: "policy-1", RollForwardStatus: skillevolution.RollForwardNone,
		CreatedAt: time.Now().UTC(),
	}
}

// Approvals append immutably and enforce §12.7 actor isolation: neither
// the run's proposer-side actor nor the evaluation's evaluator may
// approve, and the approval must score the candidate it names.
func TestSkillEvolutionLedgerApprovalsIsolateConflictedActors(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	chain := f.seedDecisionChain(t, "appr")

	approval := f.approvalRecord("appr-1", chain.candidateID, chain.evaluationID, "member:approver")
	require.NoError(t, f.ledger.InsertApproval(ctx, approval))
	fetched, err := f.ledger.GetApproval(ctx, f.workspaceID, "appr-1")
	require.NoError(t, err)
	assert.Equal(t, skillevolution.ApprovalApproved, fetched.Decision)
	assert.Equal(t, chain.evaluationID, fetched.EvaluationRef.ID)
	assert.True(t, fetched.ExpiresAt.After(time.Now()))

	proposer := f.approvalRecord("appr-2", chain.candidateID, chain.evaluationID, "member:curator")
	err = f.ledger.InsertApproval(ctx, proposer)
	require.ErrorIs(t, err, skillevolution.ErrApprovalActorConflict,
		"the actor that created the run cannot approve it")

	evaluator := f.approvalRecord("appr-3", chain.candidateID, chain.evaluationID, "member:evaluator")
	err = f.ledger.InsertApproval(ctx, evaluator)
	require.ErrorIs(t, err, skillevolution.ErrApprovalActorConflict,
		"the evaluator cannot approve its own evaluation")

	other := f.seedDecisionChain(t, "appr-other")
	mismatched := f.approvalRecord("appr-4", chain.candidateID, other.evaluationID, "member:approver")
	err = f.ledger.InsertApproval(ctx, mismatched)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict,
		"an approval cannot borrow another candidate's evaluation")

	rejection := f.approvalRecord("appr-5", chain.candidateID, chain.evaluationID, "member:approver")
	rejection.Decision = skillevolution.ApprovalRejected
	rejection.RiskAcknowledged = false
	rejection.ExpiresAt = time.Time{}
	require.NoError(t, f.ledger.InsertApproval(ctx, rejection),
		"rejections carry neither risk acknowledgement nor expiry")

	_, err = f.pool.Exec(ctx,
		`UPDATE skill_approval SET decision='rejected' WHERE workspace_id=$1::uuid AND approval_id='appr-1'`,
		f.workspaceID)
	require.Error(t, err, "the append-only trigger must refuse approval rewrites")
}

// Deployments only activate on unexpired approvals, resolve their
// materialization status by CAS, and never revive a terminal status.
func TestSkillEvolutionLedgerDeploymentsRequireLiveApprovals(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	chain := f.seedDecisionChain(t, "depl")

	require.NoError(t, f.ledger.InsertApproval(ctx,
		f.approvalRecord("appr-depl", chain.candidateID, chain.evaluationID, "member:approver")))
	require.NoError(t, f.ledger.InsertDeployment(ctx,
		f.deploymentRecord("depl-1", chain.candidateID, "appr-depl")))
	fetched, err := f.ledger.GetDeployment(ctx, f.workspaceID, "depl-1")
	require.NoError(t, err)
	assert.Equal(t, skillevolution.MaterializationPending, fetched.MaterializationStatus)
	assert.Equal(t, f.agentID, fetched.TargetAgentID)

	rejection := f.approvalRecord("appr-rej", chain.candidateID, chain.evaluationID, "member:approver")
	rejection.Decision = skillevolution.ApprovalRejected
	rejection.RiskAcknowledged = false
	rejection.ExpiresAt = time.Time{}
	require.NoError(t, f.ledger.InsertApproval(ctx, rejection))
	err = f.ledger.InsertDeployment(ctx, f.deploymentRecord("depl-2", chain.candidateID, "appr-rej"))
	require.ErrorIs(t, err, skillevolution.ErrApprovalNotUsable, "a rejection activates nothing")

	expired := f.approvalRecord("appr-old", chain.candidateID, chain.evaluationID, "member:approver")
	expired.CreatedAt = time.Now().Add(-48 * time.Hour)
	expired.ExpiresAt = time.Now().Add(-24 * time.Hour)
	require.NoError(t, f.ledger.InsertApproval(ctx, expired))
	err = f.ledger.InsertDeployment(ctx, f.deploymentRecord("depl-3", chain.candidateID, "appr-old"))
	require.ErrorIs(t, err, skillevolution.ErrApprovalNotUsable, "an expired approval activates nothing")

	require.NoError(t, f.ledger.TransitionDeploymentMaterialization(ctx, f.workspaceID, "depl-1",
		skillevolution.MaterializationPending, skillevolution.MaterializationConverged))
	err = f.ledger.TransitionDeploymentMaterialization(ctx, f.workspaceID, "depl-1",
		skillevolution.MaterializationConverged, skillevolution.MaterializationFailed)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "converged is terminal")
	err = f.ledger.TransitionDeploymentMaterialization(ctx, f.workspaceID, "depl-1",
		skillevolution.MaterializationPending, skillevolution.MaterializationFailed)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "a stale CAS from-status conflicts")

	_, err = f.pool.Exec(ctx,
		`UPDATE skill_deployment SET materialization_status='failed' WHERE workspace_id=$1::uuid AND deployment_id='depl-1'`,
		f.workspaceID)
	require.Error(t, err, "the terminal-materialization trigger must refuse revival")
}

// Rollbacks preserve history: only the roll-forward status ever advances,
// by CAS through the legal progression.
func TestSkillEvolutionLedgerRollbacksOnlyAdvanceRollForward(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	chain := f.seedDecisionChain(t, "roll")

	require.NoError(t, f.ledger.InsertApproval(ctx,
		f.approvalRecord("appr-roll", chain.candidateID, chain.evaluationID, "member:approver")))
	require.NoError(t, f.ledger.InsertDeployment(ctx,
		f.deploymentRecord("depl-roll", chain.candidateID, "appr-roll")))

	require.NoError(t, f.ledger.InsertRollback(ctx, f.rollbackRecord("roll-1", "depl-roll")))
	fetched, err := f.ledger.GetRollback(ctx, f.workspaceID, "roll-1")
	require.NoError(t, err)
	assert.Equal(t, skillevolution.RollForwardNone, fetched.RollForwardStatus)
	assert.Equal(t, skillevolution.RollbackSafetyFence, fetched.Trigger)

	require.NoError(t, f.ledger.SetRollForwardStatus(ctx, f.workspaceID, "roll-1",
		skillevolution.RollForwardNone, skillevolution.RollForwardPending))
	require.NoError(t, f.ledger.SetRollForwardStatus(ctx, f.workspaceID, "roll-1",
		skillevolution.RollForwardPending, skillevolution.RollForwardOpened))
	err = f.ledger.SetRollForwardStatus(ctx, f.workspaceID, "roll-1",
		skillevolution.RollForwardOpened, skillevolution.RollForwardPending)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "roll-forward never regresses")
	err = f.ledger.SetRollForwardStatus(ctx, f.workspaceID, "roll-1",
		skillevolution.RollForwardPending, skillevolution.RollForwardOpened)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict, "a stale CAS from-status conflicts")

	_, err = f.pool.Exec(ctx,
		`UPDATE skill_rollback SET from_artifact_hash='sha256:'||repeat('aa',64) WHERE workspace_id=$1::uuid AND rollback_id='roll-1'`,
		f.workspaceID)
	require.Error(t, err, "the update guard must freeze everything but roll_forward_status")
	_, err = f.pool.Exec(ctx,
		`DELETE FROM skill_rollback WHERE workspace_id=$1::uuid AND rollback_id='roll-1'`,
		f.workspaceID)
	require.Error(t, err, "rollback history is never deleted")
}

// Idempotency replays the same payload and conflicts on a different one;
// failed work claims nothing.
func TestSkillEvolutionLedgerIdempotencyReplaysSamePayloadOnly(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	runs := 0
	work := func(context.Context) (json.RawMessage, error) {
		runs++
		return json.RawMessage(`{"recorded":true}`), nil
	}
	request := skillevolution.IdempotentRequest{
		WorkspaceID: f.workspaceID, Key: "submit-candidate-42",
		RequestKind: "skill_candidate.submit",
		PayloadHash: skillevolution.HashCanonicalPayload([]byte(`{"candidate_id":"cand-1"}`)),
	}
	response, replayed, err := f.ledger.RunOnce(ctx, request, work)
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.JSONEq(t, `{"recorded":true}`, string(response))
	assert.Equal(t, 1, runs)

	response, replayed, err = f.ledger.RunOnce(ctx, request, work)
	require.NoError(t, err, "an identical replay is a no-op, not a re-run")
	assert.True(t, replayed)
	assert.JSONEq(t, `{"recorded":true}`, string(response))
	assert.Equal(t, 1, runs, "the replay must not re-execute work")

	conflicting := request
	conflicting.PayloadHash = skillevolution.HashCanonicalPayload([]byte(`{"candidate_id":"cand-2"}`))
	_, _, err = f.ledger.RunOnce(ctx, conflicting, work)
	require.ErrorIs(t, err, skillevolution.ErrIdempotencyPayloadConflict)

	failing := skillevolution.IdempotentRequest{
		WorkspaceID: f.workspaceID, Key: "submit-candidate-43",
		RequestKind: "skill_candidate.submit",
		PayloadHash: skillevolution.HashCanonicalPayload([]byte(`{"candidate_id":"cand-3"}`)),
	}
	_, _, err = f.ledger.RunOnce(ctx, failing, func(context.Context) (json.RawMessage, error) {
		return nil, errors.New("boom")
	})
	require.Error(t, err, "work failures propagate")
	_, _, err = f.ledger.RunOnce(ctx, failing, work)
	require.NoError(t, err, "a failed attempt claims nothing")
	assert.Equal(t, 2, runs)
}

// The migration 494 partial unique index fences the evolution key at the
// database level: a second active run in the same lane cannot be inserted
// even by a writer that skips the store's admission check.
func TestSkillEvolutionLedgerActiveRunIndexFencesConcurrentWriters(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()
	record := f.runRecord(uuid.NewString())
	_, err := f.coordinator.StartRun(ctx, record)
	require.NoError(t, err)

	_, err = f.pool.Exec(ctx, `
		INSERT INTO skill_evolution_run (
			id, workspace_id, target_agent_id, task_type, environment_major_version, created_by_actor
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'rogue-writer')`,
		uuid.NewString(), f.workspaceID, f.agentID, record.TaskType, record.EnvironmentMajorVersion)
	require.Error(t, err, "the partial unique index must fence the lane against rogue writers")
	require.Contains(t, err.Error(), "skill_evolution_run_single_active_key")

	require.NoError(t, f.ledger.TransitionRun(ctx, f.workspaceID, record.RunID,
		skillevolution.EvolutionRunQueued, skillevolution.EvolutionRunCancelled))
	_, err = f.coordinator.StartRun(ctx, f.runRecord(uuid.NewString()))
	require.NoError(t, err, "a terminal run frees the lane")
}

// The outbox delivers at least once: pending events drain oldest-first,
// dispatch is an idempotent CAS, and failures are observable.
func TestSkillEvolutionLedgerOutboxDispatchIsAtLeastOnce(t *testing.T) {
	f := newSkillEvolutionLedgerFixture(t)
	ctx := context.Background()

	require.NoError(t, f.ledger.InsertOutboxEvent(ctx, skillevolution.OutboxEvent{
		WorkspaceID: f.workspaceID, AggregateKind: "deployment", AggregateID: "depl-1",
		EventType: "materialize", Payload: []byte(`{"deployment_id":"depl-1"}`),
	}))
	require.NoError(t, f.ledger.InsertOutboxEvent(ctx, skillevolution.OutboxEvent{
		WorkspaceID: f.workspaceID, AggregateKind: "rollback", AggregateID: "roll-1",
		EventType: "fence", Payload: []byte(`{"rollback_id":"roll-1"}`),
	}))

	pending, err := f.ledger.ListPendingOutboxEvents(ctx, f.workspaceID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 2)
	assert.Equal(t, "materialize", pending[0].EventType)

	dispatched, err := f.ledger.MarkOutboxEventDispatched(ctx, f.workspaceID, pending[0].ID)
	require.NoError(t, err)
	assert.True(t, dispatched)
	dispatched, err = f.ledger.MarkOutboxEventDispatched(ctx, f.workspaceID, pending[0].ID)
	require.NoError(t, err)
	assert.False(t, dispatched, "re-dispatching a claimed event is a benign no-op")

	require.NoError(t, f.ledger.NoteOutboxEventFailure(ctx, f.workspaceID, pending[1].ID, "provider unreachable"))
	var attempts int
	var lastError string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT dispatch_attempts, last_error FROM skill_evolution_outbox WHERE id=$1`,
		pending[1].ID).Scan(&attempts, &lastError))
	assert.Equal(t, 1, attempts)
	assert.Equal(t, "provider unreachable", lastError)

	pending, err = f.ledger.ListPendingOutboxEvents(ctx, f.workspaceID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "only the failed event remains pending")
}
