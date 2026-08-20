// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphMemoryDiveBackendFactory constructs the agent backend for a Dive run.
// Model and provider are effective, policy-validated profile values; empty
// values inherit the Explore configuration at the application wiring layer.
type GraphMemoryDiveBackendFactory func(ctx context.Context, model, provider string) (memorygraph.AgentBackend, error)

// GraphMemoryDiveWorker drives one durable Dive job from its lease through
// grading, optional online-RL reward outboxing, and completion.
type GraphMemoryDiveWorker struct {
	pool       *pgxpool.Pool
	dive       *GraphMemoryDiveService
	rl         *GraphMemoryRLSessionService
	limits     GraphMemoryLimits
	root       string
	backendFor GraphMemoryDiveBackendFactory
}

// NewGraphMemoryDiveWorker constructs a worker. A nil RL service disables
// online-RL reward outboxing; callers use an empty root to use the normal
// MULTICA_WORKSPACES_ROOT resolution.
func NewGraphMemoryDiveWorker(pool *pgxpool.Pool, dive *GraphMemoryDiveService, rl *GraphMemoryRLSessionService, limits GraphMemoryLimits, root string, backendFor GraphMemoryDiveBackendFactory) *GraphMemoryDiveWorker {
	return &GraphMemoryDiveWorker{
		pool: pool, dive: dive, rl: rl, limits: limits, root: root, backendFor: backendFor,
	}
}

const graphMemoryDiveConservativeLeaseTTL = 15 * time.Minute

// RunOnce leases and processes at most one job. Leasing begins with a
// conservative 15-minute TTL because the workspace profile is discovered
// only after a job is claimed; configured Dive timeouts are bounded to two
// hours by storage policy and scheduler workers provide the outer guard.
func (w *GraphMemoryDiveWorker) RunOnce(ctx context.Context, workerID string) (bool, error) {
	if w == nil || w.dive == nil {
		return false, nil
	}
	job, err := w.dive.Lease(ctx, workerID, graphMemoryDiveConservativeLeaseTTL)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	query, trainingMode, runs, err := w.loadRecallRuns(ctx, job.RecallID)
	if err != nil {
		return w.retry(ctx, job, workerID, "exec", err)
	}
	cfg, provider, wRound, err := w.diveConfig(ctx, job.WorkspaceID)
	if err != nil {
		return w.retry(ctx, job, workerID, "exec", err)
	}
	dir, err := w.graphDir(job)
	if err != nil {
		return w.retry(ctx, job, workerID, "exec", err)
	}
	if w.backendFor == nil {
		return w.retry(ctx, job, workerID, "backend", errors.New("graph memory dive: backend factory is not configured"))
	}
	backend, err := w.backendFor(ctx, cfg.Model, provider)
	if err != nil {
		return w.retry(ctx, job, workerID, "backend", err)
	}

	diveCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	res, err := memorygraph.NewDiver(memorygraph.NewStore(dir), backend, cfg).Dive(diveCtx, query, job.GraphVersion, runs)
	if err != nil {
		return w.retry(ctx, job, workerID, "exec", err)
	}
	applied, err := w.dive.ApplyDiveResult(ctx, job.ID, workerID, res, wRound)
	if err != nil {
		return w.retry(ctx, job, workerID, "apply", err)
	}
	if !applied {
		return true, nil
	}

	// Reload the persisted rewards after ApplyDiveResult so the outbox carries
	// server-authoritative values, including zero for bypassed runs. The
	// outbox's trajectory uniqueness makes repeat attempts idempotent.
	if trainingMode == "online_rl" && w.rl != nil {
		if err := w.enqueueRewards(ctx, job.RecallID); err != nil {
			return w.retry(ctx, job, workerID, "reward_outbox", err)
		}
	}

	result, err := json.Marshal(res)
	if err != nil {
		return w.retry(ctx, job, workerID, "apply", fmt.Errorf("marshal dive result: %w", err))
	}
	_, err = w.dive.Complete(ctx, job.ID, workerID, res.Incomplete, result)
	if err != nil {
		return true, err
	}
	return true, nil
}

func (w *GraphMemoryDiveWorker) retry(ctx context.Context, job *GraphMemoryDiveJob, workerID, kind string, cause error) (bool, error) {
	if _, err := w.dive.Fail(ctx, job.ID, workerID, kind, RedactGraphMemoryObservability(cause.Error()), true); err != nil {
		return true, err
	}
	return true, nil
}

func (w *GraphMemoryDiveWorker) loadRecallRuns(ctx context.Context, recallID string) (string, string, []memorygraph.DiveRunInput, error) {
	rUUID, err := util.ParseUUID(recallID)
	if err != nil {
		return "", "", nil, fmt.Errorf("graph memory dive: recall id: %w", err)
	}
	var query, trainingMode string
	if err := w.pool.QueryRow(ctx, `SELECT query, training_mode FROM graph_memory_recall WHERE id = $1`, rUUID).Scan(&query, &trainingMode); err != nil {
		return "", "", nil, fmt.Errorf("graph memory dive: load recall: %w", err)
	}
	rows, err := w.pool.Query(ctx, `
		SELECT id, seed_index, status, summary, viewed_node_ids, submitted_node_ids, rounds
		FROM graph_memory_trajectory WHERE recall_id = $1 ORDER BY seed_index
	`, rUUID)
	if err != nil {
		return "", "", nil, fmt.Errorf("graph memory dive: load trajectories: %w", err)
	}
	defer rows.Close()
	var runs []memorygraph.DiveRunInput
	for rows.Next() {
		var (
			id                pgtype.UUID
			run               memorygraph.DiveRunInput
			viewed, submitted []byte
		)
		if err := rows.Scan(&id, &run.SeedIndex, &run.Status, &run.Summary, &viewed, &submitted, &run.Rounds); err != nil {
			return "", "", nil, fmt.Errorf("graph memory dive: scan trajectory: %w", err)
		}
		if err := json.Unmarshal(viewed, &run.ViewedNodeIDs); err != nil {
			return "", "", nil, fmt.Errorf("graph memory dive: decode viewed nodes: %w", err)
		}
		if len(submitted) > 0 && string(submitted) != "null" {
			if err := json.Unmarshal(submitted, &run.SubmittedNodeIDs); err != nil {
				return "", "", nil, fmt.Errorf("graph memory dive: decode submitted nodes: %w", err)
			}
		}
		run.TrajectoryID = util.UUIDToString(id)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return "", "", nil, fmt.Errorf("graph memory dive: iterate trajectories: %w", err)
	}
	return query, trainingMode, runs, nil
}

func (w *GraphMemoryDiveWorker) diveConfig(ctx context.Context, workspaceID string) (memorygraph.DiveConfig, string, float64, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return memorygraph.DiveConfig{}, "", 0, fmt.Errorf("graph memory dive: workspace id: %w", err)
	}
	t := w.limits.Defaults
	model, provider := "", ""
	profile, err := db.New(w.pool).GetGraphMemoryProfile(ctx, ws)
	if err == nil {
		t = graphMemoryTunablesFromProfile(profile)
		candidateModel := strings.TrimSpace(profile.DiveModel)
		candidateProvider := strings.TrimSpace(profile.DiveProvider)
		if candidateModel != "" && candidateProvider != "" && w.limits.ValidateDiveOverride(candidateProvider, candidateModel) == nil {
			model, provider = candidateModel, candidateProvider
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return memorygraph.DiveConfig{}, "", 0, fmt.Errorf("graph memory dive: load profile: %w", err)
	}
	return memorygraph.DiveConfig{
		MaxRounds: t.DiveMaxRounds, MaxViewedNodes: t.DiveMaxViewedNodes,
		MaxSourceFiles: t.DiveMaxSourceFiles, Timeout: time.Duration(t.DiveTimeoutSeconds) * time.Second,
		Model: model,
	}, provider, t.WRound, nil
}

func (w *GraphMemoryDiveWorker) graphDir(job *GraphMemoryDiveJob) (string, error) {
	root := w.root
	if root == "" {
		var err error
		root, err = graphMemoryWorkspacesRoot()
		if err != nil {
			return "", err
		}
	}
	dir, err := memorygraph.DirForScope(root, job.WorkspaceID, memorygraph.GraphDirKind(job.GraphKind), job.GraphOwnerID)
	if err != nil {
		return "", fmt.Errorf("graph memory dive: graph dir: %w", err)
	}
	if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{
		WorkspaceID: job.WorkspaceID, Kind: job.GraphKind, OwnerID: job.GraphOwnerID,
	}); err != nil {
		return "", fmt.Errorf("graph memory dive: %w", err)
	}
	return dir, nil
}

func (w *GraphMemoryDiveWorker) enqueueRewards(ctx context.Context, recallID string) error {
	rUUID, err := util.ParseUUID(recallID)
	if err != nil {
		return fmt.Errorf("graph memory dive: recall id: %w", err)
	}
	rows, err := w.pool.Query(ctx, `
		SELECT id, reward FROM graph_memory_trajectory
		WHERE recall_id = $1 AND status IN ('found', 'miss', 'error', 'budget', 'timeout')
		ORDER BY seed_index
	`, rUUID)
	if err != nil {
		return fmt.Errorf("graph memory dive: reread rewards: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		var reward float64
		if err := rows.Scan(&id, &reward); err != nil {
			return fmt.Errorf("graph memory dive: scan reward: %w", err)
		}
		if err := w.rl.EnqueueReward(ctx, util.UUIDToString(id), reward); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph memory dive: iterate rewards: %w", err)
	}
	return nil
}
