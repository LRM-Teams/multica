// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Training governance (spec 14.1). Published, sanitized, non-derivative
// trajectories are never consumed by training directly: selection goes
// through a grant-bound manifest whose every transition rechecks the global
// shadow/calibration switches, the workspace grant, the retraction fence and
// the reward status, and moves exactly once via compare-and-set.

// Training purposes. Tenant-local and pooled training are configured and
// granted independently; pooled always requires explicit owner/admin
// opt-in.
const (
	TrainingPurposeTenant = "tenant"
	TrainingPurposePooled = "pooled"
)

// Per-sample lifecycle states (spec 14.1):
// eligible -> selected -> exported -> execution_started -> consumed, plus the
// terminal retracted | revoked.
const (
	TrainingSampleEligible         = "eligible"
	TrainingSampleSelected         = "selected"
	TrainingSampleExported         = "exported"
	TrainingSampleExecutionStarted = "execution_started"
	TrainingSampleConsumed         = "consumed"
	TrainingSampleRetracted        = "retracted"
	TrainingSampleRevoked          = "revoked"
)

// Manifest lifecycle states. A manifest is born from selection and may be
// invalidated by a grant revoke before it is consumed.
const (
	TrainingManifestSelected         = "selected"
	TrainingManifestExported         = "exported"
	TrainingManifestExecutionStarted = "execution_started"
	TrainingManifestConsumed         = "consumed"
	TrainingManifestInvalidated      = "invalidated"
)

// Sample kinds: one manifest schema governs both training-data families.
const (
	TrainingSampleKindSegment         = "segment"
	TrainingSampleKindGraphTrajectory = "graph_trajectory"
)

// Sentinel errors. Callers match with errors.Is; the handler layer maps them
// to HTTP codes.
var (
	// ErrTrainingSelectionDisabled: the global reward shadow/calibration kill
	// switch has not enabled selection yet.
	ErrTrainingSelectionDisabled = errors.New("training selection is globally disabled")
	// ErrTrainingExecutionDisabled: replay execution is not calibrated yet;
	// no replay task and no model update may happen.
	ErrTrainingExecutionDisabled = errors.New("training execution is globally disabled")
	// ErrTrainingGrantPendingOwnerAck: an existing workspace stays closed
	// until an owner/admin acknowledges the grant via CAS.
	ErrTrainingGrantPendingOwnerAck = errors.New("training grant awaits owner acknowledgement")
	// ErrTrainingGrantRevoked: the grant was revoked.
	ErrTrainingGrantRevoked = errors.New("training grant revoked")
	// ErrTrainingPooledNotEnabled: pooled training needs explicit opt-in.
	ErrTrainingPooledNotEnabled = errors.New("pooled training requires explicit opt-in")
	// ErrTrainingGrantNotFound: no grant row exists for the workspace.
	ErrTrainingGrantNotFound = errors.New("training grant not found")
	// ErrTrainingGrantVersion: CAS version mismatch on the grant transition.
	ErrTrainingGrantVersion = errors.New("training grant version conflict")
	// ErrTrainingManifestNotFound: unknown manifest id.
	ErrTrainingManifestNotFound = errors.New("training manifest not found")
	// ErrTrainingManifestState: the manifest or one of its samples is not in
	// the state the transition requires (exactly-once CAS failed).
	ErrTrainingManifestState = errors.New("training manifest state conflict")
	// ErrTrainingFenced: a selected source was retracted (Task 8A fence)
	// between two transitions.
	ErrTrainingFenced = errors.New("training sample retracted")
	// ErrTrainingRewardUnavailable: the reward disappeared between two
	// transitions.
	ErrTrainingRewardUnavailable = errors.New("training reward unavailable")
	// ErrTrainingDuplicate: selection found no new samples because every
	// candidate was already sampled or near-duplicate.
	ErrTrainingDuplicate = errors.New("training selection found no new samples")
	// ErrTrainingWorkspaceMismatch: the manifest belongs to another workspace.
	ErrTrainingWorkspaceMismatch = errors.New("training manifest belongs to another workspace")
	// ErrTrainingExecutionTaskMismatch: the task is not the recorded
	// execution identity of the manifest.
	ErrTrainingExecutionTaskMismatch = errors.New("training execution task mismatch")
)

// TrainingPolicy is the global singleton: both kill switches default OFF at
// migration 472 and only move through SetTrainingPolicy.
type TrainingPolicy struct {
	SelectionEnabled      bool  `json:"selection_enabled"`
	ExecutionEnabled      bool  `json:"execution_enabled"`
	RewardPolicyVersion   int64 `json:"reward_policy_version"`
	PerAgentSampleCap     int32 `json:"per_agent_sample_cap"`
	PerChannelSampleCap   int32 `json:"per_channel_sample_cap"`
	PerWorkspaceSampleCap int32 `json:"per_workspace_sample_cap"`
}

// TrainingPolicyPatch carries optional fields; nil pointers keep the current
// value. RewardPolicyVersion is monotonic (GREATEST) server-side.
type TrainingPolicyPatch struct {
	SelectionEnabled      *bool
	ExecutionEnabled      *bool
	RewardPolicyVersion   *int64
	PerAgentSampleCap     *int32
	PerChannelSampleCap   *int32
	PerWorkspaceSampleCap *int32
}

// TrainingGrant is the per-workspace tenant/pooled grant pair.
type TrainingGrant struct {
	GrantID             string    `json:"grant_id"`
	WorkspaceID         string    `json:"workspace_id"`
	TenantStatus        string    `json:"tenant_status"`
	TenantPolicyVersion int64     `json:"tenant_policy_version"`
	TenantGrantedBy     string    `json:"tenant_granted_by,omitempty"`
	TenantGrantedAt     time.Time `json:"tenant_granted_at,omitempty"`
	PooledStatus        string    `json:"pooled_status"`
	PooledPolicyVersion int64     `json:"pooled_policy_version"`
	PooledGrantedBy     string    `json:"pooled_granted_by,omitempty"`
	PooledGrantedAt     time.Time `json:"pooled_granted_at,omitempty"`
}

// Status returns the grant status for the purpose.
func (g TrainingGrant) Status(purpose string) string {
	if purpose == TrainingPurposePooled {
		return g.PooledStatus
	}
	return g.TenantStatus
}

// Version returns the CAS version for the purpose.
func (g TrainingGrant) Version(purpose string) int64 {
	if purpose == TrainingPurposePooled {
		return g.PooledPolicyVersion
	}
	return g.TenantPolicyVersion
}

// Actor returns the granting actor for the purpose.
func (g TrainingGrant) Actor(purpose string) string {
	if purpose == TrainingPurposePooled {
		return g.PooledGrantedBy
	}
	return g.TenantGrantedBy
}

// GrantedAt returns the grant timestamp for the purpose.
func (g TrainingGrant) GrantedAt(purpose string) time.Time {
	if purpose == TrainingPurposePooled {
		return g.PooledGrantedAt
	}
	return g.TenantGrantedAt
}

// TrainingManifestItem is one fixed sample inside a manifest snapshot.
type TrainingManifestItem struct {
	Kind             string         `json:"kind"`
	Key              string         `json:"key"`
	Hash             string         `json:"hash"`
	SanitizerVersion string         `json:"sanitizer_version,omitempty"`
	PolicyVersion    string         `json:"policy_version,omitempty"`
	Scope            map[string]any `json:"scope"`
	RewardStatus     string         `json:"reward_status"`
	RewardRevision   int64          `json:"reward_revision"`
}

// TrainingManifest is the immutable selection snapshot handed to exporters
// and training executions.
type TrainingManifest struct {
	ManifestID          string                 `json:"manifest_id"`
	WorkspaceID         string                 `json:"workspace_id"`
	Purpose             string                 `json:"purpose"`
	GrantID             string                 `json:"grant_id"`
	GrantPolicyVersion  int64                  `json:"grant_policy_version"`
	GrantActor          string                 `json:"grant_actor"`
	GrantedAt           time.Time              `json:"granted_at"`
	WorkspaceConfig     map[string]any         `json:"workspace_config"`
	RewardPolicyVersion int64                  `json:"reward_policy_version"`
	ItemCount           int                    `json:"item_count"`
	ContentHash         string                 `json:"content_hash"`
	Status              string                 `json:"status"`
	Items               []TrainingManifestItem `json:"items,omitempty"`
}

// TrainingExecution is the distinct replay/training task identity that may
// open an AReaL session and receive a reward.
type TrainingExecution struct {
	ExecutionID    string    `json:"execution_id"`
	ManifestID     string    `json:"manifest_id"`
	TrainingTaskID string    `json:"training_task_id,omitempty"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
}

// TrainingSelectionRequest selects new samples into a manifest.
type TrainingSelectionRequest struct {
	WorkspaceID string
	Purpose     string // tenant|pooled
	Actor       string // requesting principal, recorded on the selection audit
	Limit       int    // optional per-selection cap (policy caps still apply)
}

// TrainingRevokeReport summarizes a grant revocation.
type TrainingRevokeReport struct {
	InvalidatedManifests int64
	RevokedSamples       int64
	LedgerEntries        int64
}

// TrainingGovernanceService owns grant/policy state and every manifest
// transition. All reads and writes go through the pool; `now` is injectable
// for tests.
type TrainingGovernanceService struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewTrainingGovernanceService constructs the governance service. A nil now
// defaults to time.Now UTC.
func NewTrainingGovernanceService(pool *pgxpool.Pool, now func() time.Time) *TrainingGovernanceService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &TrainingGovernanceService{pool: pool, now: now}
}

// ---------------------------------------------------------------------------
// Global policy.
// ---------------------------------------------------------------------------

// TrainingPolicy reads the singleton policy row.
func (s *TrainingGovernanceService) TrainingPolicy(ctx context.Context) (TrainingPolicy, error) {
	row, err := db.New(s.pool).GetTrainingGovernancePolicy(ctx)
	if err != nil {
		return TrainingPolicy{}, fmt.Errorf("training policy read: %w", err)
	}
	return trainingPolicyFromRow(row), nil
}

// SetTrainingPolicy applies a partial patch to the global switches/caps.
func (s *TrainingGovernanceService) SetTrainingPolicy(ctx context.Context, patch TrainingPolicyPatch, actor string) (TrainingPolicy, error) {
	row, err := db.New(s.pool).UpdateTrainingGovernancePolicy(ctx, db.UpdateTrainingGovernancePolicyParams{
		SelectionEnabled:      pgTypeBool(patch.SelectionEnabled),
		ExecutionEnabled:      pgTypeBool(patch.ExecutionEnabled),
		RewardPolicyVersion:   pgTypeInt8(patch.RewardPolicyVersion),
		PerAgentSampleCap:     pgTypeInt4(patch.PerAgentSampleCap),
		PerChannelSampleCap:   pgTypeInt4(patch.PerChannelSampleCap),
		PerWorkspaceSampleCap: pgTypeInt4(patch.PerWorkspaceSampleCap),
		UpdatedBy:             pgtype.Text{String: actor, Valid: actor != ""},
	})
	if err != nil {
		return TrainingPolicy{}, fmt.Errorf("training policy update: %w", err)
	}
	return trainingPolicyFromRow(row), nil
}

func trainingPolicyFromRow(row db.InteractionDagTrainingPolicy) TrainingPolicy {
	return TrainingPolicy{
		SelectionEnabled:      row.SelectionEnabled,
		ExecutionEnabled:      row.ExecutionEnabled,
		RewardPolicyVersion:   row.RewardPolicyVersion,
		PerAgentSampleCap:     row.PerAgentSampleCap,
		PerChannelSampleCap:   row.PerChannelSampleCap,
		PerWorkspaceSampleCap: row.PerWorkspaceSampleCap,
	}
}

// ---------------------------------------------------------------------------
// Grants.
// ---------------------------------------------------------------------------

// CurrentGrant loads the workspace grant. Existing workspaces always have a
// row after migration 472; a missing row means the workspace predates the
// backfill and must be bootstrapped explicitly.
func (s *TrainingGovernanceService) CurrentGrant(ctx context.Context, workspaceID string) (TrainingGrant, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant: workspace id: %w", err)
	}
	row, err := db.New(s.pool).GetTrainingGrantByWorkspace(ctx, ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrainingGrant{}, ErrTrainingGrantNotFound
	}
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant read: %w", err)
	}
	return trainingGrantFromRow(row), nil
}

// BootstrapNewWorkspaceGrant creates the grant row for a NEW workspace with
// the product default the caller resolved (spec 14.1: new workspaces MAY
// default tenant on; existing ones may not). It never overwrites an existing
// row.
func (s *TrainingGovernanceService) BootstrapNewWorkspaceGrant(ctx context.Context, workspaceID string, tenantDefaultOn bool, actor string) (TrainingGrant, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant bootstrap: workspace id: %w", err)
	}
	status := "pending_owner_ack"
	if tenantDefaultOn {
		status = "active"
	}
	params := db.InsertTrainingGrantParams{WorkspaceID: ws, TenantStatus: status, TenantPolicyVersion: 0}
	if tenantDefaultOn {
		params.TenantPolicyVersion = 1
		params.TenantGrantedBy = pgtype.Text{String: actor, Valid: actor != ""}
		params.TenantGrantedAt = pgTimestamptz(s.now())
	}
	row, err := db.New(s.pool).InsertTrainingGrant(ctx, params)
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant bootstrap: %w", err)
	}
	return trainingGrantFromRow(row), nil
}

// AckTenantGrant is the owner/admin CAS acknowledgement
// pending_owner_ack/revoked -> active. expectedVersion must match the
// current tenant policy version.
func (s *TrainingGovernanceService) AckTenantGrant(ctx context.Context, workspaceID, actor string, expectedVersion int64) (TrainingGrant, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant ack: workspace id: %w", err)
	}
	affected, err := db.New(s.pool).AckTenantTrainingGrant(ctx, db.AckTenantTrainingGrantParams{
		WorkspaceID: ws, Actor: pgtype.Text{String: actor, Valid: true}, ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("training grant ack: %w", err)
	}
	if affected == 0 {
		current, cerr := s.CurrentGrant(ctx, workspaceID)
		if cerr != nil {
			return TrainingGrant{}, cerr
		}
		if current.TenantPolicyVersion != expectedVersion {
			return TrainingGrant{}, ErrTrainingGrantVersion
		}
		switch current.TenantStatus {
		case "revoked":
			return TrainingGrant{}, ErrTrainingGrantRevoked
		case "active":
			// Already active at the same version: idempotent re-ack.
			return current, nil
		}
		return TrainingGrant{}, ErrTrainingGrantVersion
	}
	return s.CurrentGrant(ctx, workspaceID)
}

// OptInPooledTraining is the explicit pooled opt-in (disabled/revoked ->
// active) with the same CAS discipline.
func (s *TrainingGovernanceService) OptInPooledTraining(ctx context.Context, workspaceID, actor string, expectedVersion int64) (TrainingGrant, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("pooled opt-in: workspace id: %w", err)
	}
	affected, err := db.New(s.pool).OptInPooledTrainingGrant(ctx, db.OptInPooledTrainingGrantParams{
		WorkspaceID: ws, Actor: pgtype.Text{String: actor, Valid: true}, ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return TrainingGrant{}, fmt.Errorf("pooled opt-in: %w", err)
	}
	if affected == 0 {
		current, cerr := s.CurrentGrant(ctx, workspaceID)
		if cerr != nil {
			return TrainingGrant{}, cerr
		}
		if current.PooledPolicyVersion != expectedVersion {
			return TrainingGrant{}, ErrTrainingGrantVersion
		}
		switch current.PooledStatus {
		case "revoked":
			return TrainingGrant{}, ErrTrainingGrantRevoked
		case "active":
			return current, nil
		}
		return TrainingGrant{}, ErrTrainingPooledNotEnabled
	}
	return s.CurrentGrant(ctx, workspaceID)
}

// RevokeTrainingGrant revokes the purpose's grant: unconsumed manifests of
// that purpose are invalidated, unconsumed samples flip to revoked, and
// already-consumed samples enter the deletion/unlearning ledger.
func (s *TrainingGovernanceService) RevokeTrainingGrant(ctx context.Context, workspaceID, purpose, actor string) (TrainingRevokeReport, error) {
	if purpose != TrainingPurposeTenant && purpose != TrainingPurposePooled {
		return TrainingRevokeReport{}, fmt.Errorf("training revoke: unknown purpose %q", purpose)
	}
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return TrainingRevokeReport{}, fmt.Errorf("training revoke: workspace id: %w", err)
	}
	var report TrainingRevokeReport
	err = s.withTx(ctx, func(tx *db.Queries) error {
		var revoked int64
		var err error
		if purpose == TrainingPurposeTenant {
			revoked, err = tx.RevokeTenantTrainingGrant(ctx, ws)
		} else {
			revoked, err = tx.RevokePooledTrainingGrant(ctx, ws)
		}
		if err != nil {
			return fmt.Errorf("training revoke: %w", err)
		}
		if revoked == 0 {
			// Revoking an already-revoked/disabled grant is idempotent and
			// touches nothing.
			return nil
		}
		report.InvalidatedManifests, err = tx.InvalidateTrainingManifestsOnRevoke(ctx, db.InvalidateTrainingManifestsOnRevokeParams{
			WorkspaceID: ws, Purpose: purpose,
		})
		if err != nil {
			return fmt.Errorf("training revoke: invalidate manifests: %w", err)
		}
		ids, err := tx.ListTrainingManifestIDsForPurpose(ctx, db.ListTrainingManifestIDsForPurposeParams{
			WorkspaceID: ws, Purpose: purpose,
		})
		if err != nil {
			return fmt.Errorf("training revoke: list manifests: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		report.RevokedSamples, err = tx.RevokeTrainingSamplesForManifests(ctx, ids)
		if err != nil {
			return fmt.Errorf("training revoke: revoke samples: %w", err)
		}
		report.LedgerEntries, err = tx.EnqueueTrainingDeletionLedgerRows(ctx, db.EnqueueTrainingDeletionLedgerRowsParams{
			ManifestIds: ids,
			Purpose:     pgtype.Text{String: purpose, Valid: true},
			Reason:      "grant_revoked", RequestedBy: actor,
		})
		if err != nil {
			return fmt.Errorf("training revoke: deletion ledger: %w", err)
		}
		return nil
	})
	if err != nil {
		return TrainingRevokeReport{}, err
	}
	return report, nil
}

func trainingGrantFromRow(row db.InteractionDagTrainingGrant) TrainingGrant {
	g := TrainingGrant{
		GrantID:             util.UUIDToString(row.GrantID),
		WorkspaceID:         util.UUIDToString(row.WorkspaceID),
		TenantStatus:        row.TenantStatus,
		TenantPolicyVersion: row.TenantPolicyVersion,
		PooledStatus:        row.PooledStatus,
		PooledPolicyVersion: row.PooledPolicyVersion,
	}
	if row.TenantGrantedBy.Valid {
		g.TenantGrantedBy = row.TenantGrantedBy.String
	}
	if row.TenantGrantedAt.Valid {
		g.TenantGrantedAt = row.TenantGrantedAt.Time.UTC()
	}
	if row.PooledGrantedBy.Valid {
		g.PooledGrantedBy = row.PooledGrantedBy.String
	}
	if row.PooledGrantedAt.Valid {
		g.PooledGrantedAt = row.PooledGrantedAt.Time.UTC()
	}
	return g
}

// ---------------------------------------------------------------------------
// Selection.
// ---------------------------------------------------------------------------

// requireSelectionOpen checks the global kill switch and the purpose grant
// in one place so every selection/transition reuses the same verdict.
func (s *TrainingGovernanceService) requireSelectionOpen(ctx context.Context, q *db.Queries, ws pgtype.UUID, purpose string) (TrainingGrant, TrainingPolicy, error) {
	policy, err := q.GetTrainingGovernancePolicy(ctx)
	if err != nil {
		return TrainingGrant{}, TrainingPolicy{}, fmt.Errorf("training selection: policy: %w", err)
	}
	if !policy.SelectionEnabled {
		return TrainingGrant{}, TrainingPolicy{}, ErrTrainingSelectionDisabled
	}
	grantRow, err := q.GetTrainingGrantByWorkspace(ctx, ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrainingGrant{}, TrainingPolicy{}, ErrTrainingGrantNotFound
	}
	if err != nil {
		return TrainingGrant{}, TrainingPolicy{}, fmt.Errorf("training selection: grant: %w", err)
	}
	grant := trainingGrantFromRow(grantRow)
	switch purpose {
	case TrainingPurposeTenant:
		switch grant.TenantStatus {
		case "pending_owner_ack":
			return TrainingGrant{}, TrainingPolicy{}, ErrTrainingGrantPendingOwnerAck
		case "revoked":
			return TrainingGrant{}, TrainingPolicy{}, ErrTrainingGrantRevoked
		}
	case TrainingPurposePooled:
		switch grant.PooledStatus {
		case "disabled":
			return TrainingGrant{}, TrainingPolicy{}, ErrTrainingPooledNotEnabled
		case "revoked":
			return TrainingGrant{}, TrainingPolicy{}, ErrTrainingGrantRevoked
		}
	default:
		return TrainingGrant{}, TrainingPolicy{}, fmt.Errorf("training selection: unknown purpose %q", purpose)
	}
	return grant, trainingPolicyFromRow(policy), nil
}

// SelectTrainingManifest selects published Universal DAG segments into a new
// manifest. Excluded (retracted/redaction-failed/derivative/reward
// unavailable/already sampled/near-duplicate/capped) candidates simply do
// not appear; the caps from the global policy bound the per-agent, per-
// channel and per-workspace sample counts.
func (s *TrainingGovernanceService) SelectTrainingManifest(ctx context.Context, req TrainingSelectionRequest) (*TrainingManifest, error) {
	ws, err := util.ParseUUID(req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("training selection: workspace id: %w", err)
	}
	var manifest *TrainingManifest
	err = s.withTx(ctx, func(tx *db.Queries) error {
		grant, policy, err := s.requireSelectionOpen(ctx, tx, ws, req.Purpose)
		if err != nil {
			return err
		}
		candidates, err := tx.ListTrainingSegmentCandidates(ctx, db.ListTrainingSegmentCandidatesParams{
			WorkspaceID: ws, LimitCount: trainingSelectionFetchCap(req.Limit, policy),
		})
		if err != nil {
			return fmt.Errorf("training selection: candidates: %w", err)
		}
		items, agentCounts, channelCounts := make([]TrainingManifestItem, 0, len(candidates)), map[string]int{}, map[string]int{}
		seenHashes := map[string]bool{}
		for _, c := range candidates {
			if policy.PerWorkspaceSampleCap > 0 && len(items) >= int(policy.PerWorkspaceSampleCap) {
				break
			}
			if seenHashes[c.ItemHash] {
				continue // near-duplicate inside one batch
			}
			agentKey := util.UUIDToString(c.RunAgentID)
			if policy.PerAgentSampleCap > 0 && agentCounts[agentKey] >= int(policy.PerAgentSampleCap) {
				continue
			}
			channelKey := util.UUIDToString(c.ChannelIDAtEvent)
			if policy.PerChannelSampleCap > 0 && channelCounts[channelKey] >= int(policy.PerChannelSampleCap) {
				continue
			}
			scope := map[string]any{}
			if c.ProjectIDAtEvent.Valid {
				scope["project_id"] = util.UUIDToString(c.ProjectIDAtEvent)
			}
			if c.ChannelIDAtEvent.Valid {
				scope["channel_id"] = channelKey
			}
			if c.RunAgentID.Valid {
				scope["agent_id"] = agentKey
			}
			if c.TaskID != "" {
				scope["task_id"] = c.TaskID
			}
			if c.RunID.Valid {
				scope["run_id"] = util.UUIDToString(c.RunID)
			}
			items = append(items, TrainingManifestItem{
				Kind: TrainingSampleKindSegment, Key: c.ItemKey, Hash: c.ItemHash,
				SanitizerVersion: c.SanitizerVersion, PolicyVersion: c.PolicyVersion,
				Scope: scope, RewardStatus: "available", RewardRevision: c.RewardRevision,
			})
			seenHashes[c.ItemHash] = true
			if agentKey != "" {
				agentCounts[agentKey]++
			}
			if channelKey != "" {
				channelCounts[channelKey]++
			}
		}
		if len(items) == 0 {
			return ErrTrainingDuplicate
		}
		manifest, err = s.commitSelection(ctx, tx, ws, req, grant, policy, items, TrainingSampleKindSegment)
		return err
	})
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

// SelectGraphTrainingManifest selects graded, unfenced graph-memory explore
// trajectories (offline_rl recalls with completed dive jobs and an available
// reward) into a manifest under the same governance.
func (s *TrainingGovernanceService) SelectGraphTrainingManifest(ctx context.Context, req TrainingSelectionRequest) (*TrainingManifest, error) {
	ws, err := util.ParseUUID(req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("graph training selection: workspace id: %w", err)
	}
	var manifest *TrainingManifest
	err = s.withTx(ctx, func(tx *db.Queries) error {
		grant, policy, err := s.requireSelectionOpen(ctx, tx, ws, req.Purpose)
		if err != nil {
			return err
		}
		candidates, err := tx.ListTrainingGraphTrajectoryCandidates(ctx, db.ListTrainingGraphTrajectoryCandidatesParams{
			WorkspaceID: ws, LimitCount: trainingSelectionFetchCap(req.Limit, policy),
		})
		if err != nil {
			return fmt.Errorf("graph training selection: candidates: %w", err)
		}
		items, workspaceCount := make([]TrainingManifestItem, 0, len(candidates)), 0
		seenHashes := map[string]bool{}
		for _, c := range candidates {
			if policy.PerWorkspaceSampleCap > 0 && workspaceCount >= int(policy.PerWorkspaceSampleCap) {
				break
			}
			if seenHashes[c.ItemHash] {
				continue
			}
			items = append(items, TrainingManifestItem{
				Kind: TrainingSampleKindGraphTrajectory, Key: c.ItemKey, Hash: c.ItemHash,
				Scope: map[string]any{
					"recall_id":      util.UUIDToString(c.RecallID),
					"graph_kind":     c.GraphKind,
					"graph_owner_id": util.UUIDToString(c.GraphOwnerID),
					"seed_index":     c.SeedIndex,
				},
				RewardStatus: "available",
			})
			seenHashes[c.ItemHash] = true
			workspaceCount++
		}
		if len(items) == 0 {
			return ErrTrainingDuplicate
		}
		manifest, err = s.commitSelection(ctx, tx, ws, req, grant, policy, items, TrainingSampleKindGraphTrajectory)
		return err
	})
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

// trainingSelectionFetchCap bounds the candidate scan: the caller's limit,
// else a workspace-cap-derived window.
func trainingSelectionFetchCap(limit int, policy TrainingPolicy) int32 {
	if limit > 0 {
		if int32(limit) < policy.PerWorkspaceSampleCap || policy.PerWorkspaceSampleCap <= 0 {
			return int32(limit)
		}
	}
	if policy.PerWorkspaceSampleCap > 0 {
		return policy.PerWorkspaceSampleCap
	}
	return 500
}

// commitSelection writes the manifest, its items and the eligible->selected
// sample transition atomically. Exactly-once: if any sample is not in the
// eligible state (or claimed by another manifest), the transaction fails
// with ErrTrainingManifestState and nothing persists.
func (s *TrainingGovernanceService) commitSelection(
	ctx context.Context, tx *db.Queries, ws pgtype.UUID, req TrainingSelectionRequest,
	grant TrainingGrant, policy TrainingPolicy, items []TrainingManifestItem, kind string,
) (*TrainingManifest, error) {
	workspaceConfig, err := json.Marshal(map[string]any{
		"per_agent_sample_cap":     policy.PerAgentSampleCap,
		"per_channel_sample_cap":   policy.PerChannelSampleCap,
		"per_workspace_sample_cap": policy.PerWorkspaceSampleCap,
	})
	if err != nil {
		return nil, fmt.Errorf("training selection: workspace config: %w", err)
	}
	grantRow, err := tx.GetTrainingGrantByWorkspace(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("training selection: grant reload: %w", err)
	}
	row, err := tx.InsertTrainingManifest(ctx, db.InsertTrainingManifestParams{
		WorkspaceID: ws, Purpose: req.Purpose, GrantID: grantRow.GrantID,
		GrantPolicyVersion: grant.Version(req.Purpose), GrantActor: grant.Actor(req.Purpose),
		GrantedAt:       pgTimestamptz(grant.GrantedAt(req.Purpose).UTC()),
		WorkspaceConfig: workspaceConfig, RewardPolicyVersion: policy.RewardPolicyVersion,
		ItemCount: int32(len(items)), ContentHash: trainingContentHash(items),
	})
	if err != nil {
		return nil, fmt.Errorf("training selection: insert manifest: %w", err)
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		scopeJSON, mErr := json.Marshal(item.Scope)
		if mErr != nil {
			return nil, fmt.Errorf("training selection: scope: %w", mErr)
		}
		if err := tx.InsertTrainingManifestItem(ctx, db.InsertTrainingManifestItemParams{
			ManifestID: row.ManifestID, ItemKind: item.Kind, ItemKey: item.Key, ItemHash: item.Hash,
			SanitizerVersion: item.SanitizerVersion, PolicyVersion: item.PolicyVersion,
			Scope: scopeJSON, RewardStatus: item.RewardStatus, RewardRevision: item.RewardRevision,
		}); err != nil {
			return nil, fmt.Errorf("training selection: insert item: %w", err)
		}
		if item.Kind == kind {
			keys = append(keys, item.Key)
		}
	}
	if _, err := tx.InsertTrainingSamplesEligible(ctx, db.InsertTrainingSamplesEligibleParams{
		SampleKind: kind, WorkspaceID: ws, ManifestID: row.ManifestID, SampleKeys: keys,
	}); err != nil {
		return nil, fmt.Errorf("training selection: eligible samples: %w", err)
	}
	affected, err := tx.CASTrainingSamplesStateMany(ctx, db.CASTrainingSamplesStateManyParams{
		SampleKind: kind, SampleKeys: keys, ManifestID: row.ManifestID,
		FromStatus: TrainingSampleEligible, ToStatus: TrainingSampleSelected,
	})
	if err != nil {
		return nil, fmt.Errorf("training selection: select samples: %w", err)
	}
	if affected != int64(len(keys)) {
		return nil, ErrTrainingManifestState
	}
	return &TrainingManifest{
		ManifestID: util.UUIDToString(row.ManifestID), WorkspaceID: util.UUIDToString(ws),
		Purpose: row.Purpose, GrantID: util.UUIDToString(row.GrantID),
		GrantPolicyVersion: row.GrantPolicyVersion, GrantActor: row.GrantActor,
		GrantedAt: row.GrantedAt.Time.UTC(), WorkspaceConfig: map[string]any{
			"per_agent_sample_cap":     policy.PerAgentSampleCap,
			"per_channel_sample_cap":   policy.PerChannelSampleCap,
			"per_workspace_sample_cap": policy.PerWorkspaceSampleCap,
		},
		RewardPolicyVersion: row.RewardPolicyVersion, ItemCount: len(items),
		ContentHash: row.ContentHash, Status: row.Status, Items: items,
	}, nil
}

// trainingContentHash binds the whole selection: the ordered hashes of every
// item plus their identities.
func trainingContentHash(items []TrainingManifestItem) string {
	digest := sha256.New()
	for _, item := range items {
		digest.Write([]byte(item.Kind))
		digest.Write([]byte{0})
		digest.Write([]byte(item.Key))
		digest.Write([]byte{0})
		digest.Write([]byte(item.Hash))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// ---------------------------------------------------------------------------
// Transitions: export, execution, consume.
// ---------------------------------------------------------------------------

// ExportTrainingManifest rechecks grant/switch/fence/reward and moves the
// manifest selected -> exported exactly once, returning the full snapshot
// (with items) for the exporter to serialize.
func (s *TrainingGovernanceService) ExportTrainingManifest(ctx context.Context, workspaceID, manifestID string) (*TrainingManifest, error) {
	ws, manifest, err := s.loadManifest(ctx, workspaceID, manifestID)
	if err != nil {
		return nil, err
	}
	err = s.withTx(ctx, func(tx *db.Queries) error {
		if _, _, err := s.requireSelectionOpen(ctx, tx, ws, manifest.purpose); err != nil {
			return err
		}
		if err := s.requireSamplesUnfenced(ctx, tx, ws, manifest); err != nil {
			return err
		}
		affected, err := tx.CASTrainingManifestState(ctx, db.CASTrainingManifestStateParams{
			ManifestID: manifest.manifestUUID, FromStatus: TrainingManifestSelected, ToStatus: TrainingManifestExported,
		})
		if err != nil {
			return fmt.Errorf("training export: manifest CAS: %w", err)
		}
		if affected == 0 {
			return ErrTrainingManifestState
		}
		if _, err := tx.CASTrainingSamplesStateMany(ctx, db.CASTrainingSamplesStateManyParams{
			SampleKind: manifest.itemKind, SampleKeys: manifest.itemKeys, ManifestID: manifest.manifestUUID,
			FromStatus: TrainingSampleSelected, ToStatus: TrainingSampleExported,
		}); err != nil {
			return fmt.Errorf("training export: samples CAS: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	full, err := s.GetTrainingManifest(ctx, manifestID, true)
	if err != nil {
		return nil, err
	}
	return full, nil
}

// BeginTrainingExecution moves exported -> execution_started, records the
// execution identity, and creates the distinct replay/training task carrying
// that identity in its context. Execution additionally requires the global
// execution switch (reward calibration): before it flips, no replay task
// exists and no model update can happen.
func (s *TrainingGovernanceService) BeginTrainingExecution(ctx context.Context, workspaceID, manifestID, agentID string) (*TrainingExecution, error) {
	ws, manifest, err := s.loadManifest(ctx, workspaceID, manifestID)
	if err != nil {
		return nil, err
	}
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return nil, fmt.Errorf("training execution: agent id: %w", err)
	}
	var execution *TrainingExecution
	err = s.withTx(ctx, func(tx *db.Queries) error {
		if _, _, err := s.requireSelectionOpen(ctx, tx, ws, manifest.purpose); err != nil {
			return err
		}
		policy, err := tx.GetTrainingGovernancePolicy(ctx)
		if err != nil {
			return fmt.Errorf("training execution: policy: %w", err)
		}
		if !policy.ExecutionEnabled {
			return ErrTrainingExecutionDisabled
		}
		if err := s.requireSamplesUnfenced(ctx, tx, ws, manifest); err != nil {
			return err
		}
		affected, err := tx.CASTrainingManifestState(ctx, db.CASTrainingManifestStateParams{
			ManifestID: manifest.manifestUUID, FromStatus: TrainingManifestExported, ToStatus: TrainingManifestExecutionStarted,
		})
		if err != nil {
			return fmt.Errorf("training execution: manifest CAS: %w", err)
		}
		if affected == 0 {
			return ErrTrainingManifestState
		}
		if _, err := tx.CASTrainingSamplesStateMany(ctx, db.CASTrainingSamplesStateManyParams{
			SampleKind: manifest.itemKind, SampleKeys: manifest.itemKeys, ManifestID: manifest.manifestUUID,
			FromStatus: TrainingSampleExported, ToStatus: TrainingSampleExecutionStarted,
		}); err != nil {
			return fmt.Errorf("training execution: samples CAS: %w", err)
		}
		executionCtx, err := json.Marshal(map[string]any{
			"training_execution": map[string]any{
				"execution_id": "pending",
				"manifest_id":  manifestID,
				"purpose":      manifest.purpose,
			},
		})
		if err != nil {
			return fmt.Errorf("training execution: context: %w", err)
		}
		task, err := tx.CreateTrainingReplayTask(ctx, db.CreateTrainingReplayTaskParams{
			AgentID: agentUUID, Context: executionCtx, Priority: 5,
		})
		if err != nil {
			return fmt.Errorf("training execution: create replay task: %w", err)
		}
		execRow, err := tx.InsertTrainingExecution(ctx, db.InsertTrainingExecutionParams{
			ManifestID: manifest.manifestUUID, TrainingTaskID: util.UUIDToString(task.ID),
		})
		if err != nil {
			return fmt.Errorf("training execution: insert identity: %w", err)
		}
		// Stamp the real execution id onto the task context so the open hook
		// can authorize against the immutable identity.
		if _, err := tx.StampTrainingReplayTaskExecution(ctx, db.StampTrainingReplayTaskExecutionParams{
			ID: task.ID, ExecutionID: util.UUIDToString(execRow.ExecutionID),
		}); err != nil {
			return fmt.Errorf("training execution: stamp task context: %w", err)
		}
		execution = &TrainingExecution{
			ExecutionID: util.UUIDToString(execRow.ExecutionID), ManifestID: manifestID,
			TrainingTaskID: util.UUIDToString(task.ID), Status: execRow.Status,
			StartedAt: execRow.StartedAt.Time.UTC(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return execution, nil
}

// ConsumeTrainingExecution records the terminal consumed state of the
// replay execution exactly once.
func (s *TrainingGovernanceService) ConsumeTrainingExecution(ctx context.Context, workspaceID, manifestID string) error {
	_, manifest, err := s.loadManifest(ctx, workspaceID, manifestID)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *db.Queries) error {
		affected, err := tx.CASConsumeTrainingExecution(ctx, manifest.manifestUUID)
		if err != nil {
			return fmt.Errorf("training consume: execution CAS: %w", err)
		}
		if affected == 0 {
			return ErrTrainingManifestState
		}
		manifestCAS, err := tx.CASTrainingManifestState(ctx, db.CASTrainingManifestStateParams{
			ManifestID: manifest.manifestUUID, FromStatus: TrainingManifestExecutionStarted, ToStatus: TrainingManifestConsumed,
		})
		if err != nil {
			return fmt.Errorf("training consume: manifest CAS: %w", err)
		}
		if manifestCAS == 0 {
			return ErrTrainingManifestState
		}
		if _, err := tx.CASTrainingSamplesStateMany(ctx, db.CASTrainingSamplesStateManyParams{
			SampleKind: manifest.itemKind, SampleKeys: manifest.itemKeys, ManifestID: manifest.manifestUUID,
			FromStatus: TrainingSampleExecutionStarted, ToStatus: TrainingSampleConsumed,
		}); err != nil {
			return fmt.Errorf("training consume: samples CAS: %w", err)
		}
		return nil
	})
}

// AuthorizeTrainingExecutionTask verifies that taskID is the recorded
// execution identity of an execution_started manifest whose grant and
// switches are still open. Called by the session-open hook before any
// StartSession RPC.
func (s *TrainingGovernanceService) AuthorizeTrainingExecutionTask(ctx context.Context, taskID, manifestID string) error {
	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		return fmt.Errorf("training execution task: task id: %w", err)
	}
	exec, err := db.New(s.pool).GetTrainingExecutionByTask(ctx, taskUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTrainingExecutionTaskMismatch
	}
	if err != nil {
		return fmt.Errorf("training execution task: %w", err)
	}
	if manifestID != "" && util.UUIDToString(exec.ManifestID) != manifestID {
		return ErrTrainingExecutionTaskMismatch
	}
	if exec.Status != "started" {
		return ErrTrainingManifestState
	}
	manifestRow, err := db.New(s.pool).GetTrainingManifest(ctx, exec.ManifestID)
	if err != nil {
		return fmt.Errorf("training execution task: manifest: %w", err)
	}
	if manifestRow.Status != TrainingManifestExecutionStarted {
		return ErrTrainingManifestState
	}
	if _, _, err := s.requireSelectionOpen(ctx, db.New(s.pool), manifestRow.WorkspaceID, manifestRow.Purpose); err != nil {
		return err
	}
	return nil
}

// AuthorizeTrainingExport is the raw-NDJSON route gate: a resolver may only
// serialize rows under an exported (or later) manifest while the switches
// and the grant stay open.
func (s *TrainingGovernanceService) AuthorizeTrainingExport(ctx context.Context, workspaceID, manifestID string) (*TrainingManifest, error) {
	ws, manifest, err := s.loadManifest(ctx, workspaceID, manifestID)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.requireSelectionOpen(ctx, db.New(s.pool), ws, manifest.purpose); err != nil {
		return nil, err
	}
	switch manifest.status {
	case TrainingManifestExported, TrainingManifestExecutionStarted, TrainingManifestConsumed:
		full, err := s.GetTrainingManifest(ctx, manifestID, true)
		if err != nil {
			return nil, err
		}
		return full, nil
	case TrainingManifestInvalidated:
		return nil, ErrTrainingGrantRevoked
	default:
		return nil, ErrTrainingManifestState
	}
}

// GetTrainingManifest loads one manifest, optionally with its items.
func (s *TrainingGovernanceService) GetTrainingManifest(ctx context.Context, manifestID string, withItems bool) (*TrainingManifest, error) {
	if strings.TrimSpace(manifestID) == "" {
		return nil, ErrTrainingManifestNotFound
	}
	id, err := util.ParseUUID(manifestID)
	if err != nil {
		return nil, fmt.Errorf("training manifest: id: %w", err)
	}
	row, err := db.New(s.pool).GetTrainingManifest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTrainingManifestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("training manifest read: %w", err)
	}
	out := &TrainingManifest{
		ManifestID: util.UUIDToString(row.ManifestID), WorkspaceID: util.UUIDToString(row.WorkspaceID),
		Purpose: row.Purpose, GrantID: util.UUIDToString(row.GrantID),
		GrantPolicyVersion: row.GrantPolicyVersion, GrantActor: row.GrantActor,
		GrantedAt:           row.GrantedAt.Time.UTC(),
		RewardPolicyVersion: row.RewardPolicyVersion, ItemCount: int(row.ItemCount),
		ContentHash: row.ContentHash, Status: row.Status,
	}
	_ = json.Unmarshal(row.WorkspaceConfig, &out.WorkspaceConfig)
	if withItems {
		items, err := db.New(s.pool).ListTrainingManifestItems(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("training manifest items: %w", err)
		}
		out.Items = make([]TrainingManifestItem, 0, len(items))
		for _, item := range items {
			im := TrainingManifestItem{
				Kind: item.ItemKind, Key: item.ItemKey, Hash: item.ItemHash,
				RewardStatus: item.RewardStatus, RewardRevision: item.RewardRevision,
			}
			if item.SanitizerVersion.Valid {
				im.SanitizerVersion = item.SanitizerVersion.String
			}
			if item.PolicyVersion.Valid {
				im.PolicyVersion = item.PolicyVersion.String
			}
			im.Scope = map[string]any{}
			_ = json.Unmarshal(item.Scope, &im.Scope)
			out.Items = append(out.Items, im)
		}
	}
	return out, nil
}

// ListTrainingManifests lists the workspace's manifests (all purposes when
// purpose is empty), newest first.
func (s *TrainingGovernanceService) ListTrainingManifests(ctx context.Context, workspaceID, purpose string, limit int) ([]*TrainingManifest, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("training manifests: workspace id: %w", err)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.New(s.pool).ListTrainingManifests(ctx, db.ListTrainingManifestsParams{
		WorkspaceID: ws, Purpose: purpose, LimitCount: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("training manifests list: %w", err)
	}
	out := make([]*TrainingManifest, 0, len(rows))
	for _, row := range rows {
		m := &TrainingManifest{
			ManifestID: util.UUIDToString(row.ManifestID), WorkspaceID: util.UUIDToString(row.WorkspaceID),
			Purpose: row.Purpose, GrantID: util.UUIDToString(row.GrantID),
			GrantPolicyVersion: row.GrantPolicyVersion, GrantActor: row.GrantActor,
			GrantedAt:           row.GrantedAt.Time.UTC(),
			RewardPolicyVersion: row.RewardPolicyVersion, ItemCount: int(row.ItemCount),
			ContentHash: row.ContentHash, Status: row.Status,
		}
		m.WorkspaceConfig = map[string]any{}
		_ = json.Unmarshal(row.WorkspaceConfig, &m.WorkspaceConfig)
		out = append(out, m)
	}
	return out, nil
}

// ListTrainingDeletionLedgerRows exposes the deletion/unlearning ledger for
// the workspace (audit surface for owners/admins).
func (s *TrainingGovernanceService) ListTrainingDeletionLedgerRows(ctx context.Context, workspaceID string, limit int) ([]db.InteractionDagTrainingDeletionLedger, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("training deletion ledger: workspace id: %w", err)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return db.New(s.pool).ListTrainingDeletionLedgerRows(ctx, db.ListTrainingDeletionLedgerRowsParams{
		WorkspaceID: ws, LimitCount: int32(limit),
	})
}

// ---------------------------------------------------------------------------
// Internal helpers.
// ---------------------------------------------------------------------------

// loadedTrainingManifest carries the DB identity of a manifest plus the
// derived homogeneous sample kind/keys for the CAS transitions. Mixed-kind
// manifests are not produced by selection; if that ever changes, the
// per-kind CAS must be called per kind.
type loadedTrainingManifest struct {
	manifestUUID pgtype.UUID
	workspaceID  string
	purpose      string
	status       string
	itemKind     string
	itemKeys     []string
}

func (s *TrainingGovernanceService) loadManifest(ctx context.Context, workspaceID, manifestID string) (pgtype.UUID, *loadedTrainingManifest, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("training manifest: workspace id: %w", err)
	}
	if strings.TrimSpace(manifestID) == "" {
		return pgtype.UUID{}, nil, ErrTrainingManifestNotFound
	}
	id, err := util.ParseUUID(manifestID)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("training manifest: id: %w", err)
	}
	row, err := db.New(s.pool).GetTrainingManifest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, nil, ErrTrainingManifestNotFound
	}
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("training manifest read: %w", err)
	}
	if row.WorkspaceID != ws {
		return pgtype.UUID{}, nil, ErrTrainingWorkspaceMismatch
	}
	items, err := db.New(s.pool).ListTrainingManifestItems(ctx, id)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("training manifest items: %w", err)
	}
	loaded := &loadedTrainingManifest{
		manifestUUID: row.ManifestID, workspaceID: workspaceID, purpose: row.Purpose,
		status: row.Status, itemKind: TrainingSampleKindSegment, itemKeys: []string{},
	}
	kinds := map[string]bool{}
	for _, item := range items {
		kinds[item.ItemKind] = true
		loaded.itemKeys = append(loaded.itemKeys, item.ItemKey)
	}
	// A manifest built from graph trajectories flips the CAS kind; segment
	// manifests keep the segment kind. (Selection never mixes kinds.)
	if len(kinds) == 1 {
		for kind := range kinds {
			loaded.itemKind = kind
		}
	}
	return ws, loaded, nil
}

// requireSamplesUnfenced rechecks the CURRENT fence and reward status for
// every manifest item: a retraction (Task 8A) or a vanished reward between
// two transitions fails closed.
func (s *TrainingGovernanceService) requireSamplesUnfenced(ctx context.Context, q *db.Queries, ws pgtype.UUID, manifest *loadedTrainingManifest) error {
	if manifest.itemKind == TrainingSampleKindSegment {
		rows, err := q.ListTrainingSegmentSelectionAudit(ctx, ws)
		if err != nil {
			return fmt.Errorf("training fence recheck: %w", err)
		}
		audit := make(map[string]db.ListTrainingSegmentSelectionAuditRow, len(rows))
		for _, row := range rows {
			audit[row.SegmentID] = row
		}
		for _, key := range manifest.itemKeys {
			row, ok := audit[key]
			if !ok {
				return fmt.Errorf("%w: segment %s vanished", ErrTrainingFenced, key)
			}
			if pgBoolValue(row.Retracted) || pgTextValue(row.PublishStatus) == "retracted" {
				return fmt.Errorf("%w: segment %s retracted", ErrTrainingFenced, key)
			}
			if row.ContentStatus == "redaction_failed" || row.ContentStatus == "retracted" {
				return fmt.Errorf("%w: segment %s %s", ErrTrainingFenced, key, row.ContentStatus)
			}
			if !row.RewardAvailable {
				return fmt.Errorf("%w: segment %s", ErrTrainingRewardUnavailable, key)
			}
		}
		return nil
	}
	// Graph trajectories: the dive reward must still exist and the owner
	// fence must still be clear (reward/fence recheck without the
	// already-sampled exclusion — these samples are selected by design).
	rows, err := q.ListTrainingGraphTrajectoryFenceAudit(ctx, db.ListTrainingGraphTrajectoryFenceAuditParams{
		WorkspaceID: ws, ItemKeys: manifest.itemKeys,
	})
	if err != nil {
		return fmt.Errorf("training fence recheck: graph: %w", err)
	}
	verdict := make(map[string]db.ListTrainingGraphTrajectoryFenceAuditRow, len(rows))
	for _, row := range rows {
		verdict[row.ItemKey] = row
	}
	for _, key := range manifest.itemKeys {
		row, ok := verdict[key]
		if !ok {
			return fmt.Errorf("%w: graph trajectory %s vanished", ErrTrainingFenced, key)
		}
		if !row.Unfenced {
			return fmt.Errorf("%w: graph trajectory %s owner retracted", ErrTrainingFenced, key)
		}
		if !pgBoolValue(row.RewardAvailable) {
			return fmt.Errorf("%w: graph trajectory %s", ErrTrainingRewardUnavailable, key)
		}
	}
	return nil
}

// withTx runs fn on a transaction-scoped Queries, committing on success.
func (s *TrainingGovernanceService) withTx(ctx context.Context, fn func(tx *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("training tx begin: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	if err := fn(qtx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("training tx commit: %w", err)
	}
	return nil
}

// pgTypeBool / pgTypeInt8 / pgTypeInt4 convert optional patch fields to the
// nullable sqlc parameter types.
func pgTypeBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}

func pgTypeInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func pgTypeInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

// trainingSelectionAuditReason classifies one segment audit row for
// reporting; sorted reasons keep outputs deterministic.
func trainingSelectionAuditReason(row db.ListTrainingSegmentSelectionAuditRow) string {
	switch {
	case row.Derivative:
		return "derivative"
	case pgBoolValue(row.Retracted) || pgTextValue(row.PublishStatus) == "retracted":
		return "retracted"
	case row.ContentStatus == "redaction_failed":
		return "redaction_failed"
	case pgTextValue(row.PublishStatus) != "published":
		return "publish_" + pgTextValue(row.PublishStatus)
	case row.ContentStatus != "published":
		return "content_" + row.ContentStatus
	case row.BoundaryQuality == "approximate":
		return "approximate_backfill"
	case !row.TrainableEligible:
		return "not_trainable_eligible"
	case !row.RewardAvailable:
		return "reward_unavailable"
	case row.AlreadySampled:
		return "already_sampled"
	default:
		return "near_duplicate"
	}
}

// TrainingSelectionAudit describes why each workspace segment is or is not
// selectable right now.
type TrainingSelectionAudit struct {
	SegmentID string `json:"segment_id"`
	Reason    string `json:"reason"` // empty when the segment is currently selectable
}

// pgTextValue / pgBoolValue tolerate the nullable audit scan shapes.
func pgTextValue(v pgtype.Text) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func pgBoolValue(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

// AuditTrainingSelection classifies every workspace segment.
func (s *TrainingGovernanceService) AuditTrainingSelection(ctx context.Context, workspaceID string) ([]TrainingSelectionAudit, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("training audit: workspace id: %w", err)
	}
	rows, err := db.New(s.pool).ListTrainingSegmentSelectionAudit(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("training audit: %w", err)
	}
	out := make([]TrainingSelectionAudit, 0, len(rows))
	for _, row := range rows {
		reason := trainingSelectionAuditReason(row)
		if reason == "near_duplicate" {
			// The audit row passed every other gate; near-duplicate only
			// applies once the same content hash was already selected.
			reason = ""
		}
		out = append(out, TrainingSelectionAudit{SegmentID: row.SegmentID, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SegmentID < out[j].SegmentID })
	return out, nil
}
