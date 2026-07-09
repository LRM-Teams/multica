package scheduler

import (
	"context"
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
