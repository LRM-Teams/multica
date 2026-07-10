package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

const (
	JobNameMemoryL1DailyRecord   = "memory_l1_daily_record"
	JobNameMemoryL2ReviewExtract = "memory_l2_review_extract"
	JobNameMemoryL3Promote       = "memory_l3_promote"
	JobNameMemoryL4Curator       = "memory_l4_curator"
)

func MemoryCurationJobs(pool *pgxpool.Pool) []JobSpec {
	return []JobSpec{
		memoryCurationJob(pool, JobNameMemoryL1DailyRecord, memorycuration.StageL1, 1),
		memoryCurationJob(pool, JobNameMemoryL2ReviewExtract, memorycuration.StageL2, 2),
		memoryCurationJob(pool, JobNameMemoryL3Promote, memorycuration.StageL3, 3),
		memoryCurationJob(pool, JobNameMemoryL4Curator, memorycuration.StageL4, 4),
	}
}

func memoryCurationJob(pool *pgxpool.Pool, name string, stage memorycuration.Stage, beijingHour int) JobSpec {
	return JobSpec{
		Name:              name,
		Cadence:           time.Hour,
		ScheduleDelay:     5 * time.Minute,
		CatchUpMode:       CatchUpEveryPlan,
		CatchUpWindow:     48 * time.Hour,
		MaxPlansPerTick:   8,
		RunTimeout:        55 * time.Minute,
		StaleTimeout:      65 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			5 * time.Minute,
			15 * time.Minute,
			30 * time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeMemoryCurationHandler(pool, stage, beijingHour),
	}
}

func makeMemoryCurationHandler(pool *pgxpool.Pool, stage memorycuration.Stage, beijingHour int) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
		if err != nil {
			loc = time.FixedZone("CST", 8*60*60)
		}
		planLocal := in.PlanTime.In(loc)
		if planLocal.Hour() != beijingHour {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "outside_stage_hour", "stage": stage, "timezone": memorycuration.DefaultTimezone}}, nil
		}
		root := memoryCurationWorkspacesRoot()
		if root == "" {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "workspaces_root_unresolved", "stage": stage}}, nil
		}
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "workspaces_root_missing", "workspaces_root": root, "stage": stage}}, nil
			}
			return HandlerResult{}, err
		}
		planDate := planLocal.AddDate(0, 0, -1)
		var evidenceDB memorycuration.EvidenceDB
		if pool != nil {
			evidenceDB = pool
		}
		runIDs := map[string]string{}
		if pool != nil {
			rows, err := pool.Query(ctx, `
				INSERT INTO memory_curation_run (workspace_id, agent_id, stage, trigger_kind, status, date_from, date_to, started_at)
				SELECT id, NULL, $1, 'scheduled', 'running', $2, $3, now()
				  FROM workspace
				RETURNING workspace_id::text, id::text
			`, memorycuration.DBStageName(stage), planDate, planDate)
			if err != nil {
				return HandlerResult{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var workspaceID, runID string
				if err := rows.Scan(&workspaceID, &runID); err != nil {
					return HandlerResult{}, err
				}
				runIDs[workspaceID] = runID
			}
			if err := rows.Err(); err != nil {
				return HandlerResult{}, err
			}
		}
		res, err := memorycuration.NewEngine().Run(memorycuration.Options{
			Context:        ctx,
			DB:             evidenceDB,
			WorkspacesRoot: root,
			AllAgents:      true,
			Stage:          stage,
			Since:          planDate,
			Until:          planDate,
			Now:            time.Now().UTC(),
			Timezone:       memorycuration.DefaultTimezone,
		})
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		status := "succeeded"
		errText := ""
		if err != nil || len(res.Errors) > 0 {
			status = "failed"
			if err != nil {
				errText = err.Error()
			} else {
				errText = "one or more agents failed"
			}
		}
		if len(runIDs) > 0 && pool != nil {
			statsByWorkspace := map[string]memorycuration.Result{}
			for workspaceID := range runIDs {
				statsByWorkspace[workspaceID] = memorycuration.Result{
					Stage:          res.Stage,
					WorkspacesRoot: res.WorkspacesRoot,
					WorkspaceID:    workspaceID,
					DateFrom:       res.DateFrom,
					DateTo:         res.DateTo,
					DryRun:         res.DryRun,
					Force:          res.Force,
					Timezone:       res.Timezone,
				}
			}
			for _, agent := range res.AgentResults {
				stats := statsByWorkspace[agent.WorkspaceID]
				stats.WorkspaceID = agent.WorkspaceID
				stats.AgentResults = append(stats.AgentResults, agent)
				stats.AgentsScanned++
				if agent.Changed {
					stats.AgentsChanged++
				}
				stats.DailyFilesWritten += agent.DailyFilesWritten
				stats.ReviewCandidatesAdded += agent.ReviewCandidatesAdded
				stats.EntriesPromoted += agent.EntriesPromoted
				stats.SharedCandidatesAdded += agent.SharedCandidatesAdded
				stats.SharedCandidatesSynced += agent.SharedCandidatesSynced
				stats.EntriesArchived += agent.EntriesArchived
				stats.DuplicatesMerged += agent.DuplicatesMerged
				stats.ConflictsFound += agent.ConflictsFound
				stats.EvidenceCollected += agent.EvidenceCollected
				statsByWorkspace[agent.WorkspaceID] = stats
			}
			for _, agentErr := range res.Errors {
				stats := statsByWorkspace[agentErr.WorkspaceID]
				stats.WorkspaceID = agentErr.WorkspaceID
				stats.Errors = append(stats.Errors, agentErr)
				statsByWorkspace[agentErr.WorkspaceID] = stats
			}
			for workspaceID, runID := range runIDs {
				stats := statsByWorkspace[workspaceID]
				statsJSON, _ := json.Marshal(stats)
				workspaceStatus := status
				workspaceErr := errText
				if err == nil && len(stats.Errors) == 0 {
					workspaceStatus = "succeeded"
					workspaceErr = ""
				}
				if _, updateErr := pool.Exec(ctx, `
					UPDATE memory_curation_run
					   SET status = $2, stats = $3::jsonb, error = $4, finished_at = now()
					 WHERE id = $1
				`, runID, workspaceStatus, string(statsJSON), workspaceErr); updateErr != nil {
					return HandlerResult{}, updateErr
				}
			}
		}
		if err != nil {
			return HandlerResult{}, err
		}
		return HandlerResult{RowsAffected: int64(res.AgentsChanged), Result: map[string]any{
			"stage":                   stage,
			"workspaces_root":         root,
			"agents_scanned":          res.AgentsScanned,
			"agents_changed":          res.AgentsChanged,
			"daily_files_written":     res.DailyFilesWritten,
			"review_candidates_added": res.ReviewCandidatesAdded,
			"entries_promoted":        res.EntriesPromoted,
			"entries_archived":        res.EntriesArchived,
			"duplicates_merged":       res.DuplicatesMerged,
			"evidence_collected":      res.EvidenceCollected,
			"timezone":                memorycuration.DefaultTimezone,
			"errors":                  len(res.Errors),
		}}, nil
	}
}

func memoryCurationWorkspacesRoot() string { return memorycuration.DefaultWorkspacesRoot() }
