// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 21: the audited shadow-rollout gate service (spec 15/16/19,
// AC51/52). Nine named gates share one linear phase ladder
// (disabled -> shadow -> enabled); every move is a CAS transition recorded
// in the append-only universal_dag_gate_transition ledger. Canary failure or
// a synchronous provider/ACL/sanitizer/deletion failure demotes dependent
// read/training phases back to disabled — the durable DAG writes are never
// touched by a shutdown.

// ShadowGateName identifies one rollout gate. The six memory routes are
// workspace-scoped; the three training gates are global singletons.
type ShadowGateName string

const (
	ShadowGateAtoms             ShadowGateName = "atoms"
	ShadowGateSearchV2          ShadowGateName = "search_v2"
	ShadowGateExplore           ShadowGateName = "explore"
	ShadowGateCitations         ShadowGateName = "citations"
	ShadowGateAtomConsolidation ShadowGateName = "atom_consolidation"
	ShadowGateChannelMigration  ShadowGateName = "channel_migration"
	ShadowGateRewardShadow      ShadowGateName = "reward_shadow"
	ShadowGateTenantTraining    ShadowGateName = "tenant_training"
	ShadowGatePooledTraining    ShadowGateName = "pooled_training"
)

// ShadowGatePhase is the linear ladder every gate climbs.
type ShadowGatePhase string

const (
	ShadowPhaseDisabled ShadowGatePhase = "disabled"
	ShadowPhaseShadow   ShadowGatePhase = "shadow"
	ShadowPhaseEnabled  ShadowGatePhase = "enabled"
)

// ShadowCanaryName identifies one of the six named shadow canaries (AC51).
type ShadowCanaryName string

const (
	ShadowCanarySequenceGap          ShadowCanaryName = "sequence_gap"
	ShadowCanaryOutboxLoss           ShadowCanaryName = "outbox_loss"
	ShadowCanaryCrossChannelLeak     ShadowCanaryName = "cross_channel_leak"
	ShadowCanarySanitizerFailOpen    ShadowCanaryName = "sanitizer_fail_open"
	ShadowCanaryRetractionVisibility ShadowCanaryName = "retraction_visibility"
	ShadowCanaryCostLatencyBudget    ShadowCanaryName = "cost_latency_budget"
)

// Shadow budget thresholds (spec 9.7 shadow bootstrap hard caps). P95
// trajectory rounds must stay within the explore hard cap; graded rewards
// must stay above the floor; the reward outbox must not lag past the pending
// delivery budget.
const (
	ShadowCostP95RoundsCap          = 6
	ShadowRewardFloor               = -1.0
	ShadowRewardOutboxPendingBudget = time.Hour
)

// ShadowFailureKind names the synchronous failure sources that return a
// dependent gate to disabled without waiting for the next canary sweep.
type ShadowFailureKind string

const (
	ShadowFailureProvider  ShadowFailureKind = "provider"
	ShadowFailureACL       ShadowFailureKind = "acl"
	ShadowFailureSanitizer ShadowFailureKind = "sanitizer"
	ShadowFailureDeletion  ShadowFailureKind = "deletion"
)

// shadowGateTriggerManual/autoShutdown/failure mirror the migration 474
// closed trigger set.
const (
	shadowGateTriggerManual      = "manual"
	shadowGateTriggerAutoShutown = "auto_shutdown"
	shadowGateTriggerFailure     = "failure"
)

var (
	// ErrShadowGateCASConflict: the caller's expected version no longer
	// matches the gate row; the transition affected zero rows.
	ErrShadowGateCASConflict = errors.New("shadow gate CAS conflict")
	// ErrShadowGatePhaseOrder: the requested move violates the linear ladder
	// (skipping shadow, or a no-op).
	ErrShadowGatePhaseOrder = errors.New("shadow gate phase order violation")
	// ErrShadowGatePrerequisite: a prerequisite gate, grant or policy version
	// is not in the required state.
	ErrShadowGatePrerequisite = errors.New("shadow gate prerequisite not met")
	// ErrShadowGateEvidence: the recorded evidence snapshot is incomplete or
	// carries a failed canary.
	ErrShadowGateEvidence = errors.New("shadow gate evidence incomplete or failed")
	// ErrShadowGateUnknown: the gate or failure kind is not in the closed set.
	ErrShadowGateUnknown = errors.New("unknown shadow gate or failure kind")
)

// shadowGateGlobalWorkspaceID is the nil-uuid sentinel that keys global-scope
// gate rows (migration 474 scope pairing CHECK).
var shadowGateGlobalWorkspaceID = pgtype.UUID{Bytes: uuid.UUID{}, Valid: true}

// AllShadowGates returns the closed gate set in rollout order.
func AllShadowGates() []ShadowGateName {
	return []ShadowGateName{
		ShadowGateAtoms, ShadowGateSearchV2, ShadowGateCitations, ShadowGateExplore,
		ShadowGateAtomConsolidation, ShadowGateChannelMigration,
		ShadowGateRewardShadow, ShadowGateTenantTraining, ShadowGatePooledTraining,
	}
}

// AllShadowCanaries returns the closed canary set.
func AllShadowCanaries() []ShadowCanaryName {
	return []ShadowCanaryName{
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak,
		ShadowCanarySanitizerFailOpen, ShadowCanaryRetractionVisibility, ShadowCanaryCostLatencyBudget,
	}
}

// shadowGateCanaryDependencies maps every gate to the canaries whose green
// windows it depends on. Every memory route depends on the retraction
// canary because the DB CHECK (any route on => retraction_canary_ok) makes
// that evidence mandatory anyway.
var shadowGateCanaryDependencies = map[ShadowGateName][]ShadowCanaryName{
	ShadowGateAtoms: {
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak,
		ShadowCanarySanitizerFailOpen, ShadowCanaryRetractionVisibility,
	},
	ShadowGateSearchV2: {
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak,
		ShadowCanarySanitizerFailOpen, ShadowCanaryRetractionVisibility,
	},
	ShadowGateCitations: {
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak,
		ShadowCanarySanitizerFailOpen, ShadowCanaryRetractionVisibility,
	},
	ShadowGateExplore: {
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak,
		ShadowCanarySanitizerFailOpen, ShadowCanaryRetractionVisibility,
		ShadowCanaryCostLatencyBudget,
	},
	ShadowGateAtomConsolidation: {
		ShadowCanarySequenceGap, ShadowCanaryOutboxLoss, ShadowCanarySanitizerFailOpen,
		ShadowCanaryRetractionVisibility, ShadowCanaryCostLatencyBudget,
	},
	ShadowGateChannelMigration: {
		ShadowCanaryOutboxLoss, ShadowCanaryCrossChannelLeak, ShadowCanarySanitizerFailOpen,
		ShadowCanaryRetractionVisibility,
	},
	ShadowGateRewardShadow:   {ShadowCanaryOutboxLoss, ShadowCanaryCostLatencyBudget},
	ShadowGateTenantTraining: {ShadowCanaryCostLatencyBudget},
	ShadowGatePooledTraining: {ShadowCanaryCostLatencyBudget},
}

// shadowGatePrerequisites maps the training gates to the gates that must
// already be enabled (spec 19.9/19.10, AC68).
var shadowGatePrerequisites = map[ShadowGateName][]ShadowGateName{
	ShadowGateTenantTraining: {ShadowGateRewardShadow},
	ShadowGatePooledTraining: {ShadowGateTenantTraining},
}

// shadowFailureCanaries maps each synchronous failure kind to the canaries
// whose integrity it invalidates.
var shadowFailureCanaries = map[ShadowFailureKind][]ShadowCanaryName{
	ShadowFailureProvider:  {ShadowCanaryOutboxLoss, ShadowCanaryCostLatencyBudget},
	ShadowFailureACL:       {ShadowCanaryCrossChannelLeak},
	ShadowFailureSanitizer: {ShadowCanarySanitizerFailOpen},
	ShadowFailureDeletion:  {ShadowCanaryRetractionVisibility},
}

// ShadowGateDependencies returns the named canaries a promotion of gate
// must record green evidence for.
func ShadowGateDependencies(gate ShadowGateName) []ShadowCanaryName {
	return append([]ShadowCanaryName(nil), shadowGateCanaryDependencies[gate]...)
}

// ShadowGatePrerequisites returns the gates that must be enabled before
// gate itself can reach enabled.
func ShadowGatePrerequisites(gate ShadowGateName) []ShadowGateName {
	return append([]ShadowGateName(nil), shadowGatePrerequisites[gate]...)
}

// ShadowGatePhaseRank returns the ladder ordinal of a phase.
func ShadowGatePhaseRank(phase ShadowGatePhase) int {
	switch phase {
	case ShadowPhaseShadow:
		return 1
	case ShadowPhaseEnabled:
		return 2
	default:
		return 0
	}
}

// ShadowGateRolloutRank returns the spec 19 activation order of a gate.
// Memory routes activate in the 19.3-19.7 order; training gates follow the
// 19.9/19.10 sub-order (reward shadow before tenant selection before pooled
// opt-in). Prerequisite gates always rank strictly lower.
func ShadowGateRolloutRank(gate ShadowGateName) int {
	switch gate {
	case ShadowGateAtoms:
		return 30
	case ShadowGateSearchV2:
		return 40
	case ShadowGateCitations:
		return 45
	case ShadowGateExplore:
		return 50
	case ShadowGateAtomConsolidation:
		return 60
	case ShadowGateChannelMigration:
		return 70
	case ShadowGateRewardShadow:
		return 90
	case ShadowGateTenantTraining:
		return 95
	case ShadowGatePooledTraining:
		return 100
	default:
		return -1
	}
}

// ShadowCanaryResult is one canary's verdict. Detail is a bounded,
// content-free explanation (counts and rule names only).
type ShadowCanaryResult struct {
	Canary ShadowCanaryName `json:"canary"`
	OK     bool             `json:"ok"`
	Count  int              `json:"count"`
	Detail string           `json:"detail,omitempty"`
}

// ShadowGateStatus is the governance view of one gate row.
type ShadowGateStatus struct {
	Scope         string          `json:"scope"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	Gate          ShadowGateName  `json:"gate"`
	Phase         ShadowGatePhase `json:"phase"`
	GateVersion   int64           `json:"gate_version"`
	PolicyVersion int64           `json:"policy_version"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// ShadowGateTransition is one audit ledger row.
type ShadowGateTransition struct {
	TransitionID  int64           `json:"transition_id"`
	Scope         string          `json:"scope"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	Gate          ShadowGateName  `json:"gate"`
	FromPhase     ShadowGatePhase `json:"from_phase"`
	ToPhase       ShadowGatePhase `json:"to_phase"`
	Reason        string          `json:"reason"`
	Trigger       string          `json:"trigger"`
	PolicyVersion int64           `json:"policy_version"`
	Actor         string          `json:"actor"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ShadowGatePromotion is one audited CAS transition request.
type ShadowGatePromotion struct {
	WorkspaceID     pgtype.UUID
	Gate            ShadowGateName
	To              ShadowGatePhase
	ExpectedVersion int64
	PolicyVersion   int64
	Evidence        map[ShadowCanaryName]ShadowCanaryResult
	Actor           string
	Reason          string
}

// ShadowGateSweepReport is the scheduler-facing composite of one workspace
// sweep: the evaluated canaries and the gates the sweep shut down.
type ShadowGateSweepReport struct {
	WorkspaceID string                                  `json:"workspace_id"`
	Canaries    map[ShadowCanaryName]ShadowCanaryResult `json:"canaries"`
	Shutdown    []ShadowGateStatus                      `json:"shutdown,omitempty"`
}

// ShadowGateService owns the phase registry, the evidence evaluation and the
// audited transitions.
type ShadowGateService struct {
	pool    *pgxpool.Pool
	metrics *obsmetrics.BusinessMetrics
}

// NewShadowGateService constructs the gate service without metrics.
func NewShadowGateService(pool *pgxpool.Pool) *ShadowGateService {
	return &ShadowGateService{pool: pool}
}

// NewShadowGateServiceWithMetrics additionally records canary/phase/transition
// metrics (nil metrics are tolerated by every recorder).
func NewShadowGateServiceWithMetrics(pool *pgxpool.Pool, bm *obsmetrics.BusinessMetrics) *ShadowGateService {
	return &ShadowGateService{pool: pool, metrics: bm}
}

// shadowGateScope resolves a gate's registry scope.
func shadowGateScope(gate ShadowGateName) string {
	switch gate {
	case ShadowGateRewardShadow, ShadowGateTenantTraining, ShadowGatePooledTraining:
		return "global"
	default:
		return "workspace"
	}
}

// shadowGateKey resolves the registry workspace key (sentinel for global).
func shadowGateKey(gate ShadowGateName, workspaceID pgtype.UUID) (string, pgtype.UUID, error) {
	scope := shadowGateScope(gate)
	if scope == "global" {
		return scope, shadowGateGlobalWorkspaceID, nil
	}
	if !workspaceID.Valid {
		return scope, workspaceID, fmt.Errorf("shadow gate %s requires a workspace", gate)
	}
	return scope, workspaceID, nil
}

// ---------------------------------------------------------------------------
// Evidence evaluation (AC51)
// ---------------------------------------------------------------------------

// EvaluateEvidence proves the six named canaries against verifiable DB
// state. Every check is read-only; a canary is red when the state it guards
// has drifted, not when a job happens to be behind.
func (s *ShadowGateService) EvaluateEvidence(ctx context.Context, workspaceID pgtype.UUID) (map[ShadowCanaryName]ShadowCanaryResult, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	if !workspaceID.Valid {
		return nil, fmt.Errorf("shadow gate evidence requires a workspace")
	}
	evidence := make(map[ShadowCanaryName]ShadowCanaryResult, len(AllShadowCanaries()))

	gaps, err := s.countSequenceGaps(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sequence gap canary: %w", err)
	}
	evidence[ShadowCanarySequenceGap] = ShadowCanaryResult{
		Canary: ShadowCanarySequenceGap, OK: gaps == 0, Count: gaps,
		Detail: "publish_seq holes in interaction_dag_segment",
	}

	losses, err := s.countOutboxLosses(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("outbox loss canary: %w", err)
	}
	evidence[ShadowCanaryOutboxLoss] = ShadowCanaryResult{
		Canary: ShadowCanaryOutboxLoss, OK: losses == 0, Count: losses,
		Detail: "dead_letter or redaction_failed publish outbox rows",
	}

	leaks, err := s.countCrossChannelLeaks(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("cross-channel leak canary: %w", err)
	}
	evidence[ShadowCanaryCrossChannelLeak] = ShadowCanaryResult{
		Canary: ShadowCanaryCrossChannelLeak, OK: leaks == 0, Count: leaks,
		Detail: "channel-visible atoms whose scope differs from the segment's channel_at_event",
	}

	failOpen, err := s.countSanitizerFailOpen(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sanitizer fail-open canary: %w", err)
	}
	evidence[ShadowCanarySanitizerFailOpen] = ShadowCanaryResult{
		Canary: ShadowCanarySanitizerFailOpen, OK: failOpen == 0, Count: failOpen,
		Detail: "segments published while their content stayed redaction_failed",
	}

	visibility, err := s.countRetractionVisibilityBreaches(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("retraction visibility canary: %w", err)
	}
	evidence[ShadowCanaryRetractionVisibility] = ShadowCanaryResult{
		Canary: ShadowCanaryRetractionVisibility, OK: visibility == 0, Count: visibility,
		Detail: "published consumers of fenced sources missing from the quarantine",
	}

	budget, detail, err := s.costLatencyBudget(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("cost/latency budget canary: %w", err)
	}
	evidence[ShadowCanaryCostLatencyBudget] = ShadowCanaryResult{
		Canary: ShadowCanaryCostLatencyBudget, OK: budget, Detail: detail,
	}
	return evidence, nil
}

// countSequenceGaps: publish_seq values are allocated as MAX+1 inside the
// publish transaction, so any hole between allocated sequences is a lost
// commit. Unpublished tail rows (NULL publish_seq) are expected and ignored.
func (s *ShadowGateService) countSequenceGaps(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	var gaps int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT publish_seq,
			       lead(publish_seq) OVER (ORDER BY publish_seq) AS next_seq
			FROM interaction_dag_segment
			WHERE workspace_id = $1 AND publish_seq IS NOT NULL
		) seq
		WHERE next_seq IS NOT NULL AND next_seq - publish_seq > 1`,
		workspaceID).Scan(&gaps)
	return gaps, err
}

// countOutboxLosses: terminal dead-letter rows and redaction failures on the
// publish outbox are durable-loss signals the retry loop will not recover.
func (s *ShadowGateService) countOutboxLosses(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	var losses int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_publish_outbox
		WHERE workspace_id = $1 AND status IN ('dead_letter', 'redaction_failed')`,
		workspaceID).Scan(&losses)
	return losses, err
}

// countCrossChannelLeaks: a channel-visible atom must inherit the channel
// scope of the segment that produced it (spec 3/12). Atoms already pulled
// out of readable state by a retraction quarantine cannot leak anymore.
func (s *ShadowGateService) countCrossChannelLeaks(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	var leaks int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_atom atom
		JOIN interaction_dag_segment segment
		  ON segment.workspace_id = atom.workspace_id
		 AND segment.segment_id = atom.segment_id
		WHERE atom.workspace_id = $1
		  AND atom.visibility = 'channel'
		  AND atom.channel_id IS DISTINCT FROM segment.channel_id_at_event
		  AND NOT EXISTS (
			SELECT 1 FROM quarantined_pending_recompute quarantine
			WHERE quarantine.workspace_id = atom.workspace_id
			  AND quarantine.consumer_kind = 'graph_memory_atom'
			  AND quarantine.consumer_id = atom.atom_id)`,
		workspaceID).Scan(&leaks)
	return leaks, err
}

// countSanitizerFailOpen: publishing a segment whose sanitized payload failed
// redaction is the fail-open signature (spec 9).
func (s *ShadowGateService) countSanitizerFailOpen(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	var failOpen int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment
		WHERE workspace_id = $1
		  AND publish_status = 'published'
		  AND content_status = 'redaction_failed'`,
		workspaceID).Scan(&failOpen)
	return failOpen, err
}

// countRetractionVisibilityBreaches: every published consumer of a fenced
// source must sit in the quarantined reverse-provenance closure (Task 8A);
// an unquarantined consumer would stay readable through the fence.
func (s *ShadowGateService) countRetractionVisibilityBreaches(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	var breaches int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM memory_source_provenance prov
		JOIN memory_source_guard guard
		  ON guard.workspace_id = prov.workspace_id
		 AND guard.source_kind = prov.source_kind
		 AND guard.source_id = prov.source_id
		WHERE prov.workspace_id = $1
		  AND guard.retracted_at IS NOT NULL
		  AND NOT EXISTS (
			SELECT 1 FROM quarantined_pending_recompute quarantine
			WHERE quarantine.workspace_id = prov.workspace_id
			  AND quarantine.consumer_kind = prov.consumer_kind
			  AND quarantine.consumer_id = prov.consumer_id)`,
		workspaceID).Scan(&breaches)
	return breaches, err
}

// costLatencyBudget proves the shadow cost/latency envelope: P95 explore
// rounds within the hard cap, graded rewards above the floor, and the reward
// outbox draining within its pending budget (spec 9.7).
func (s *ShadowGateService) costLatencyBudget(ctx context.Context, workspaceID pgtype.UUID) (bool, string, error) {
	var p95Rounds, rewardMin, oldestPendingSeconds float64
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY trajectory.rounds), 0)
		     FROM graph_memory_trajectory trajectory
		     WHERE trajectory.workspace_id = $1
		       AND trajectory.status IN ('found', 'miss')) AS p95_rounds,
		  (SELECT COALESCE(min(outbox.reward), 0)
		     FROM graph_memory_reward_outbox outbox
		     WHERE outbox.workspace_id = $1) AS reward_min,
		  (SELECT COALESCE(EXTRACT(EPOCH FROM (now() - min(outbox.created_at))), 0)
		     FROM graph_memory_reward_outbox outbox
		     WHERE outbox.workspace_id = $1 AND outbox.status = 'pending') AS oldest_pending`,
		workspaceID).Scan(&p95Rounds, &rewardMin, &oldestPendingSeconds)
	if err != nil {
		return false, "", err
	}
	switch {
	case p95Rounds > ShadowCostP95RoundsCap:
		return false, fmt.Sprintf("p95_rounds %.2f exceeds cap %d", p95Rounds, ShadowCostP95RoundsCap), nil
	case rewardMin < ShadowRewardFloor:
		return false, fmt.Sprintf("min reward %.2f below floor %.2f", rewardMin, ShadowRewardFloor), nil
	case oldestPendingSeconds > ShadowRewardOutboxPendingBudget.Seconds():
		return false, fmt.Sprintf("oldest pending reward %.0fs exceeds budget %.0fs",
			oldestPendingSeconds, ShadowRewardOutboxPendingBudget.Seconds()), nil
	}
	return true, fmt.Sprintf("p95_rounds %.2f, reward_min %.2f, oldest_pending %.0fs",
		p95Rounds, rewardMin, oldestPendingSeconds), nil
}

// ---------------------------------------------------------------------------
// Gate inspection
// ---------------------------------------------------------------------------

// Gate reads one gate's status; an absent row is the DB-default disabled at
// version 0 (the promotion path registers rows on first move).
func (s *ShadowGateService) Gate(ctx context.Context, workspaceID pgtype.UUID, gate ShadowGateName) (ShadowGateStatus, error) {
	if s == nil || s.pool == nil {
		return ShadowGateStatus{}, fmt.Errorf("shadow gate service not configured")
	}
	if ShadowGateRolloutRank(gate) < 0 {
		return ShadowGateStatus{}, fmt.Errorf("%w: %s", ErrShadowGateUnknown, gate)
	}
	scope, key, err := shadowGateKey(gate, workspaceID)
	if err != nil {
		return ShadowGateStatus{}, err
	}
	row, err := db.New(s.pool).GetUniversalDAGShadowGate(ctx, db.GetUniversalDAGShadowGateParams{
		Scope: scope, WorkspaceID: key, GateName: string(gate),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shadowGateSynthesizedStatus(scope, key, gate), nil
		}
		return ShadowGateStatus{}, fmt.Errorf("shadow gate read: %w", err)
	}
	return shadowGateRowStatus(row), nil
}

// ListGates returns the workspace's gates plus the global training gates.
func (s *ShadowGateService) ListGates(ctx context.Context, workspaceID pgtype.UUID) ([]ShadowGateStatus, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	if !workspaceID.Valid {
		return nil, fmt.Errorf("shadow gate listing requires a workspace")
	}
	rows, err := db.New(s.pool).ListUniversalDAGShadowGatesForWorkspace(ctx, workspaceID)
	if err != nil {
		if undefinedTable(err) {
			return nil, nil // pre-474 schema: nothing registered yet
		}
		return nil, fmt.Errorf("shadow gate listing: %w", err)
	}
	gates := make([]ShadowGateStatus, 0, len(rows))
	for _, row := range rows {
		gates = append(gates, shadowGateRowStatus(row))
	}
	return gates, nil
}

// ListTransitions returns the most recent audit rows visible to one
// workspace (its own scope plus the global training gates).
func (s *ShadowGateService) ListTransitions(ctx context.Context, workspaceID pgtype.UUID, limit int32) ([]ShadowGateTransition, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	if !workspaceID.Valid {
		return nil, fmt.Errorf("shadow gate transitions require a workspace")
	}
	rows, err := db.New(s.pool).ListUniversalDAGGateTransitions(ctx, db.ListUniversalDAGGateTransitionsParams{
		WorkspaceID: workspaceID, LimitRows: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("shadow gate transitions: %w", err)
	}
	transitions := make([]ShadowGateTransition, 0, len(rows))
	for _, row := range rows {
		transitions = append(transitions, ShadowGateTransition{
			TransitionID: row.TransitionID, Scope: row.Scope,
			WorkspaceID: shadowGateWorkspaceLabel(row.Scope, row.WorkspaceID),
			Gate:        ShadowGateName(row.GateName),
			FromPhase:   ShadowGatePhase(row.FromPhase), ToPhase: ShadowGatePhase(row.ToPhase),
			Reason: row.Reason, Trigger: row.Trigger, PolicyVersion: row.PolicyVersion,
			Actor: row.Actor, CreatedAt: row.CreatedAt.Time,
		})
	}
	return transitions, nil
}

func shadowGateRowStatus(row db.UniversalDagShadowGate) ShadowGateStatus {
	return ShadowGateStatus{
		Scope:         row.Scope,
		WorkspaceID:   shadowGateWorkspaceLabel(row.Scope, row.WorkspaceID),
		Gate:          ShadowGateName(row.GateName),
		Phase:         ShadowGatePhase(row.Phase),
		GateVersion:   row.GateVersion,
		PolicyVersion: row.PolicyVersion,
		UpdatedBy:     row.UpdatedBy.String,
		UpdatedAt:     row.UpdatedAt.Time,
	}
}

func shadowGateSynthesizedStatus(scope string, key pgtype.UUID, gate ShadowGateName) ShadowGateStatus {
	return ShadowGateStatus{
		Scope:       scope,
		WorkspaceID: shadowGateWorkspaceLabel(scope, key),
		Gate:        gate,
		Phase:       ShadowPhaseDisabled,
	}
}

// shadowGateWorkspaceLabel hides the global sentinel from API responses.
func shadowGateWorkspaceLabel(scope string, workspaceID pgtype.UUID) string {
	if scope == "global" {
		return ""
	}
	return workspaceID.String()
}

// ---------------------------------------------------------------------------
// Audited CAS promotion (plan Step 3, spec 15)
// ---------------------------------------------------------------------------

// PromoteGate moves one gate along the linear ladder in a single audited
// transaction: register if absent, lock, validate phase order / evidence /
// prerequisites, apply the route or training side effects, CAS the row and
// append the transition. A stale expected version is ErrShadowGateCASConflict
// and leaves no audit row.
func (s *ShadowGateService) PromoteGate(ctx context.Context, p ShadowGatePromotion) (ShadowGateStatus, error) {
	return s.transitionGate(ctx, p, shadowGateTriggerManual)
}

// transitionGate is the single audited CAS path shared by manual promotions,
// canary auto-shutdowns and synchronous failure demotions; only the audit
// trigger (and whether upward validation applies) differs.
func (s *ShadowGateService) transitionGate(ctx context.Context, p ShadowGatePromotion, trigger string) (ShadowGateStatus, error) {
	if s == nil || s.pool == nil {
		return ShadowGateStatus{}, fmt.Errorf("shadow gate service not configured")
	}
	if ShadowGateRolloutRank(p.Gate) < 0 {
		return ShadowGateStatus{}, fmt.Errorf("%w: %s", ErrShadowGateUnknown, p.Gate)
	}
	if p.To != ShadowPhaseDisabled && p.To != ShadowPhaseShadow && p.To != ShadowPhaseEnabled {
		return ShadowGateStatus{}, fmt.Errorf("%w: %s", ErrShadowGateUnknown, p.To)
	}
	if p.Actor == "" {
		return ShadowGateStatus{}, fmt.Errorf("shadow gate promotion requires an actor")
	}
	scope, key, err := shadowGateKey(p.Gate, p.WorkspaceID)
	if err != nil {
		return ShadowGateStatus{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ShadowGateStatus{}, fmt.Errorf("shadow gate promotion tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)

	if _, err := qtx.RegisterUniversalDAGShadowGate(ctx, db.RegisterUniversalDAGShadowGateParams{
		Scope: scope, WorkspaceID: key, GateName: string(p.Gate),
		UpdatedBy: pgtype.Text{String: p.Actor, Valid: true},
	}); err != nil {
		return ShadowGateStatus{}, fmt.Errorf("register shadow gate: %w", err)
	}
	row, err := qtx.GetUniversalDAGShadowGateForUpdate(ctx, db.GetUniversalDAGShadowGateForUpdateParams{
		Scope: scope, WorkspaceID: key, GateName: string(p.Gate),
	})
	if err != nil {
		return ShadowGateStatus{}, fmt.Errorf("lock shadow gate: %w", err)
	}
	from := ShadowGatePhase(row.Phase)
	if err := validateShadowGateMove(from, p); err != nil {
		return ShadowGateStatus{}, err
	}
	if ShadowGatePhaseRank(p.To) > ShadowGatePhaseRank(from) {
		// Upward moves need the recorded evidence and prerequisites; demotions
		// only need the audited CAS.
		if err := validateShadowGateEvidence(p); err != nil {
			return ShadowGateStatus{}, err
		}
		if err := s.validatePrerequisitesTx(ctx, qtx, p); err != nil {
			return ShadowGateStatus{}, err
		}
	}
	if err := s.applyGateSideEffectsTx(ctx, tx, qtx, p, from); err != nil {
		return ShadowGateStatus{}, err
	}

	evidenceJSON, err := json.Marshal(p.Evidence)
	if err != nil {
		return ShadowGateStatus{}, fmt.Errorf("encode evidence snapshot: %w", err)
	}
	moved, err := qtx.TransitionUniversalDAGShadowGate(ctx, db.TransitionUniversalDAGShadowGateParams{
		ToPhase: string(p.To), PolicyVersion: p.PolicyVersion,
		Evidence: evidenceJSON, UpdatedBy: pgtype.Text{String: p.Actor, Valid: true},
		Scope: scope, WorkspaceID: key, GateName: string(p.Gate),
		ExpectedVersion: p.ExpectedVersion, FromPhase: string(from),
	})
	if err != nil {
		return ShadowGateStatus{}, fmt.Errorf("transition shadow gate: %w", err)
	}
	if moved == 0 {
		return ShadowGateStatus{}, ErrShadowGateCASConflict
	}
	if err := qtx.InsertUniversalDAGGateTransition(ctx, db.InsertUniversalDAGGateTransitionParams{
		Scope: scope, WorkspaceID: key, GateName: string(p.Gate),
		FromPhase: string(from), ToPhase: string(p.To),
		Reason: p.Reason, Trigger: trigger,
		Evidence: evidenceJSON, PolicyVersion: p.PolicyVersion, Actor: p.Actor,
	}); err != nil {
		return ShadowGateStatus{}, fmt.Errorf("audit shadow gate transition: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ShadowGateStatus{}, fmt.Errorf("commit shadow gate transition: %w", err)
	}

	status := ShadowGateStatus{
		Scope: scope, WorkspaceID: shadowGateWorkspaceLabel(scope, key), Gate: p.Gate,
		Phase: p.To, GateVersion: p.ExpectedVersion + 1, PolicyVersion: p.PolicyVersion,
		UpdatedBy: p.Actor, UpdatedAt: time.Now().UTC(),
	}
	if s.metrics != nil {
		s.metrics.RecordShadowGateTransition(string(p.Gate), string(from), string(p.To), trigger)
		s.metrics.SetShadowGatePhase(scope, string(p.Gate), string(p.To))
	}
	return status, nil
}

// validateShadowGateMove enforces the linear ladder: an upward move must be
// exactly one rank; the only downward move is a full demotion to disabled.
func validateShadowGateMove(from ShadowGatePhase, p ShadowGatePromotion) error {
	if from == p.To {
		return fmt.Errorf("%w: %s is already %s", ErrShadowGatePhaseOrder, p.Gate, from)
	}
	fromRank, toRank := ShadowGatePhaseRank(from), ShadowGatePhaseRank(p.To)
	upward := toRank > fromRank
	if upward && toRank != fromRank+1 {
		return fmt.Errorf("%w: %s -> %s skips the shadow phase", ErrShadowGatePhaseOrder, from, p.To)
	}
	if !upward && p.To != ShadowPhaseDisabled {
		return fmt.Errorf("%w: %s -> %s is not a sanctioned move", ErrShadowGatePhaseOrder, from, p.To)
	}
	return nil
}

// validateShadowGateEvidence: the operator-recorded snapshot must carry every
// dependency canary green; enabled promotions must also record a policy
// version (spec 15: named tests/canary windows and policy versions recorded).
func validateShadowGateEvidence(p ShadowGatePromotion) error {
	for _, canary := range shadowGateCanaryDependencies[p.Gate] {
		result, ok := p.Evidence[canary]
		if !ok {
			return fmt.Errorf("%w: canary %s missing from the recorded evidence", ErrShadowGateEvidence, canary)
		}
		if !result.OK {
			return fmt.Errorf("%w: canary %s failed its window", ErrShadowGateEvidence, canary)
		}
	}
	if p.To == ShadowPhaseEnabled && p.PolicyVersion < 1 {
		return fmt.Errorf("%w: enabled promotions must record a policy version", ErrShadowGatePrerequisite)
	}
	return nil
}

// validatePrerequisitesTx: prerequisite gates must be enabled, and the
// training gates additionally require the workspace's own Task 18 grant
// state — the gate can never bypass the owner acknowledgement.
func (s *ShadowGateService) validatePrerequisitesTx(ctx context.Context, qtx *db.Queries, p ShadowGatePromotion) error {
	if p.To != ShadowPhaseEnabled {
		return nil
	}
	for _, prerequisite := range shadowGatePrerequisites[p.Gate] {
		scope, key, err := shadowGateKey(prerequisite, p.WorkspaceID)
		if err != nil {
			return err
		}
		row, err := qtx.GetUniversalDAGShadowGate(ctx, db.GetUniversalDAGShadowGateParams{
			Scope: scope, WorkspaceID: key, GateName: string(prerequisite),
		})
		if err != nil || ShadowGatePhase(row.Phase) != ShadowPhaseEnabled {
			return fmt.Errorf("%w: %s requires gate %s enabled", ErrShadowGatePrerequisite, p.Gate, prerequisite)
		}
	}
	switch p.Gate {
	case ShadowGateTenantTraining:
		grant, err := qtx.GetTrainingGrantByWorkspace(ctx, p.WorkspaceID)
		if err != nil || grant.TenantStatus != "active" {
			return fmt.Errorf("%w: tenant training requires an owner-acknowledged grant", ErrShadowGatePrerequisite)
		}
	case ShadowGatePooledTraining:
		grant, err := qtx.GetTrainingGrantByWorkspace(ctx, p.WorkspaceID)
		if err != nil || grant.PooledStatus != "active" {
			return fmt.Errorf("%w: pooled training requires an explicit opt-in", ErrShadowGatePrerequisite)
		}
	}
	return nil
}

// applyGateSideEffectsTx keeps the readers' authorities coherent inside the
// same audited transaction: memory routes flip their memory_read_phase_gate
// boolean (with the retraction canary evidence the DB CHECK demands), and
// the reward shadow gate flips the global training switches.
func (s *ShadowGateService) applyGateSideEffectsTx(ctx context.Context, tx pgx.Tx, qtx *db.Queries, p ShadowGatePromotion, from ShadowGatePhase) error {
	switch p.Gate {
	case ShadowGateAtoms, ShadowGateSearchV2, ShadowGateExplore, ShadowGateCitations,
		ShadowGateAtomConsolidation, ShadowGateChannelMigration:
		return applyMemoryRoutePhaseTx(ctx, tx, p.WorkspaceID, p.Gate, p.To == ShadowPhaseEnabled)
	case ShadowGateRewardShadow:
		selection := p.To == ShadowPhaseShadow || p.To == ShadowPhaseEnabled
		execution := p.To == ShadowPhaseEnabled
		if _, err := qtx.UpdateTrainingGovernancePolicy(ctx, db.UpdateTrainingGovernancePolicyParams{
			SelectionEnabled: pgtype.Bool{Bool: selection, Valid: true},
			ExecutionEnabled: pgtype.Bool{Bool: execution, Valid: true},
			UpdatedBy:        pgtype.Text{String: p.Actor, Valid: true},
		}); err != nil {
			return fmt.Errorf("flip training switches: %w", err)
		}
	}
	return nil
}

// applyMemoryRoutePhaseTx flips one memory route boolean. Enabling also sets
// retraction_canary_ok (the promotion evidence proved the canary green and
// the DB CHECK requires the flag); demotion only closes the route.
func applyMemoryRoutePhaseTx(ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, gate ShadowGateName, enabled bool) error {
	qtx := db.New(tx)
	if _, err := qtx.InsertMemoryReadPhaseGate(ctx, workspaceID); err != nil {
		return fmt.Errorf("register phase gate: %w", err)
	}
	if gate == ShadowGateChannelMigration {
		if err := qtx.SetMemoryReadPhaseGateChannelMigration(ctx, db.SetMemoryReadPhaseGateChannelMigrationParams{
			WorkspaceID: workspaceID, ChannelMigrationEnabled: enabled,
		}); err != nil {
			return fmt.Errorf("set channel migration route: %w", err)
		}
		return nil
	}
	current, err := qtx.GetMemoryReadPhaseGate(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("read phase gate: %w", err)
	}
	next := db.SetMemoryReadPhaseGateParams{
		WorkspaceID:              workspaceID,
		AtomsEnabled:             current.AtomsEnabled,
		SearchV2Enabled:          current.SearchV2Enabled,
		ExploreEnabled:           current.ExploreEnabled,
		CitationsEnabled:         current.CitationsEnabled,
		AtomConsolidationEnabled: current.AtomConsolidationEnabled,
		RetractionCanaryOk:       current.RetractionCanaryOk,
	}
	switch gate {
	case ShadowGateAtoms:
		next.AtomsEnabled = enabled
	case ShadowGateSearchV2:
		next.SearchV2Enabled = enabled
	case ShadowGateExplore:
		next.ExploreEnabled = enabled
	case ShadowGateCitations:
		next.CitationsEnabled = enabled
	case ShadowGateAtomConsolidation:
		next.AtomConsolidationEnabled = enabled
	}
	if enabled {
		next.RetractionCanaryOk = true
	}
	if _, err := qtx.SetMemoryReadPhaseGate(ctx, next); err != nil {
		return fmt.Errorf("set memory routes: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Automatic read-path shutdown (AC52) and synchronous failure demotion
// ---------------------------------------------------------------------------

// AutoShutdown demotes every gate whose dependency canaries failed back to
// disabled. It only closes read/training phases — the durable DAG write path
// (boundary recording, outbox, publish) is untouched.
func (s *ShadowGateService) AutoShutdown(ctx context.Context, workspaceID pgtype.UUID, evidence map[ShadowCanaryName]ShadowCanaryResult) ([]ShadowGateStatus, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	if !workspaceID.Valid {
		return nil, fmt.Errorf("shadow gate auto shutdown requires a workspace")
	}
	failed := map[ShadowCanaryName]bool{}
	for canary, result := range evidence {
		if !result.OK {
			failed[canary] = true
		}
	}
	if len(failed) == 0 {
		return nil, nil
	}
	gates, err := s.ListGates(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var shutdown []ShadowGateStatus
	rewardShadowDown := false
	for _, gate := range gates {
		if gate.Phase == ShadowPhaseDisabled {
			continue
		}
		affected := false
		for _, canary := range shadowGateCanaryDependencies[gate.Gate] {
			if failed[canary] {
				affected = true
				break
			}
		}
		// The training gates ride on reward shadow: when it shuts down, they
		// lose their prerequisite and demote with it.
		if !affected && (gate.Gate == ShadowGateTenantTraining || gate.Gate == ShadowGatePooledTraining) && rewardShadowDown {
			affected = true
		}
		if !affected {
			continue
		}
		if gate.Gate == ShadowGateRewardShadow {
			rewardShadowDown = true
		}
		demoted, err := s.demoteGate(ctx, workspaceID, gate.Gate, gate.GateVersion,
			"auto_shutdown:canary-failure", shadowGateTriggerAutoShutown, "shadow-gate-sweep")
		if err != nil {
			return shutdown, err
		}
		shutdown = append(shutdown, demoted)
	}
	return shutdown, nil
}

// NoteFailure synchronously demotes every gate guarding the failed
// integrity dimension (plan Step 3: provider, ACL, sanitizer or deletion
// failure returns dependent gates to disabled immediately).
func (s *ShadowGateService) NoteFailure(ctx context.Context, workspaceID pgtype.UUID, kind ShadowFailureKind, actor string) ([]ShadowGateStatus, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	canaries, ok := shadowFailureCanaries[kind]
	if !ok {
		return nil, fmt.Errorf("%w: failure kind %s", ErrShadowGateUnknown, kind)
	}
	if actor == "" {
		return nil, fmt.Errorf("shadow gate failure note requires an actor")
	}
	evidence := map[ShadowCanaryName]ShadowCanaryResult{}
	for _, canary := range canaries {
		evidence[canary] = ShadowCanaryResult{Canary: canary, OK: false, Count: 1, Detail: "synchronous " + string(kind) + " failure"}
	}
	gates, err := s.ListGates(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var demoted []ShadowGateStatus
	for _, gate := range gates {
		if gate.Phase == ShadowPhaseDisabled {
			continue
		}
		affected := false
		for _, canary := range shadowGateCanaryDependencies[gate.Gate] {
			if _, hit := evidence[canary]; hit {
				affected = true
				break
			}
		}
		if !affected {
			continue
		}
		status, err := s.demoteGate(ctx, workspaceID, gate.Gate, gate.GateVersion,
			"failure:"+string(kind), shadowGateTriggerFailure, actor)
		if err != nil {
			return demoted, err
		}
		demoted = append(demoted, status)
	}
	return demoted, nil
}

// demoteGate is the audited full demotion to disabled (CAS on the current
// version; a racing writer surfaces as ErrShadowGateCASConflict).
func (s *ShadowGateService) demoteGate(ctx context.Context, workspaceID pgtype.UUID, gate ShadowGateName, expectedVersion int64, reason, trigger, actor string) (ShadowGateStatus, error) {
	return s.transitionGate(ctx, ShadowGatePromotion{
		WorkspaceID: workspaceID, Gate: gate, To: ShadowPhaseDisabled,
		ExpectedVersion: expectedVersion, Actor: actor, Reason: reason,
	}, trigger)
}

// ---------------------------------------------------------------------------
// Scheduler sweep
// ---------------------------------------------------------------------------

// Sweep is the scheduler composite: evaluate the six canaries, record their
// metrics, and auto-shutdown dependent gates when any canary is red.
func (s *ShadowGateService) Sweep(ctx context.Context, workspaceID pgtype.UUID) (*ShadowGateSweepReport, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow gate service not configured")
	}
	evidence, err := s.EvaluateEvidence(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if s.metrics != nil {
		for _, result := range evidence {
			s.metrics.RecordShadowGateCanary(string(result.Canary), result.OK)
		}
	}
	report := &ShadowGateSweepReport{
		WorkspaceID: workspaceID.String(),
		Canaries:    evidence,
	}
	shutdown, err := s.AutoShutdown(ctx, workspaceID, evidence)
	if err != nil {
		return report, err
	}
	report.Shutdown = shutdown
	if s.metrics != nil {
		if gates, listErr := s.ListGates(ctx, workspaceID); listErr == nil {
			for _, gate := range gates {
				s.metrics.SetShadowGatePhase(gate.Scope, string(gate.Gate), string(gate.Phase))
			}
		}
	}
	return report, nil
}
