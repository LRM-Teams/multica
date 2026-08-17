package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/service"
)

const memoryCurationBackfillMaxDays = 30

type memoryCurationBackfillRequest struct {
	Since  string `json:"since"`
	Until  string `json:"until"`
	DryRun bool   `json:"dry_run"`
}

type memoryCurationBackfillDayPlan struct {
	Date           string   `json:"date"`
	Stage          string   `json:"stage"`
	TargetAgentIDs []string `json:"target_agent_ids"`
	RunID          string   `json:"run_id,omitempty"`
	Status         string   `json:"status,omitempty"`
}

type memoryCurationBackfillSkip struct {
	Date   string `json:"date"`
	Reason string `json:"reason"`
}

type memoryCurationBackfillResponse struct {
	Since      string                          `json:"since"`
	Until      string                          `json:"until"`
	DryRun     bool                            `json:"dry_run"`
	Queued     []memoryCurationBackfillDayPlan `json:"queued"`
	Skipped    []memoryCurationBackfillSkip    `json:"skipped"`
	QueuedDays int                             `json:"queued_days"`
	SkipDays   int                             `json:"skip_days"`
}

func (h *Handler) PreviewMemoryCurationBackfill(w http.ResponseWriter, r *http.Request) {
	h.handleMemoryCurationBackfillWithRequest(w, r, memoryCurationBackfillRequest{
		Since: r.URL.Query().Get("since"),
		Until: r.URL.Query().Get("until"),
	}, true)
}

func (h *Handler) StartMemoryCurationBackfill(w http.ResponseWriter, r *http.Request) {
	var req memoryCurationBackfillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.handleMemoryCurationBackfillWithRequest(w, r, req, req.DryRun)
}

func (h *Handler) handleMemoryCurationBackfillWithRequest(w http.ResponseWriter, r *http.Request, req memoryCurationBackfillRequest, dryRun bool) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run team curation")
		return
	}
	if h.graphMemoryTypeForWorkspace(r.Context(), parseUUID(workspaceID)) == "graph" {
		writeLegacyCurationNotApplicable(w)
		return
	}
	profile, err := h.loadMemoryCuratorProfile(r, workspaceID, uuidToString(member.UserID))
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusConflict, "configure a memory curator profile before running curation")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory curator profile")
		return
	}
	if !profile.SelfReviewEnabled && !profile.TeamCurationEnabled {
		writeError(w, http.StatusConflict, "enable agent self-review or team curation before backfill")
		return
	}
	runStatus, err := h.memoryCuratorRunStatus(r.Context(), profile)
	if err != nil {
		if errors.Is(err, errInvalidMemoryCuratorProfile) {
			writeError(w, http.StatusConflict, "memory curator profile is no longer valid; choose a runtime and curator agent again")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to validate memory curator profile")
		return
	}

	since, until, err := resolveMemoryCurationBackfillRange(req.Since, req.Until, profile.Timezone, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	succeeded, err := h.loadSucceededMemoryCurationDays(r.Context(), workspaceID, profile.ID, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load succeeded curation days")
		return
	}

	resp := memoryCurationBackfillResponse{
		Since:   formatDateUTC(since),
		Until:   formatDateUTC(until),
		DryRun:  dryRun,
		Queued:  []memoryCurationBackfillDayPlan{},
		Skipped: []memoryCurationBackfillSkip{},
	}

	for day := since; !day.After(until); day = day.AddDate(0, 0, 1) {
		date := formatDateUTC(day)
		agentIDs, err := h.resolveActiveMemoryCurationTargetAgentIDs(r.Context(), profile, day)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve active curator targets")
			return
		}
		if len(agentIDs) == 0 {
			resp.Skipped = append(resp.Skipped, memoryCurationBackfillSkip{Date: date, Reason: "no_activity"})
			continue
		}

		needSelf := profile.SelfReviewEnabled && !succeeded.hasSelf(date)
		needTeam := profile.TeamCurationEnabled && !succeeded.hasTeam(date)
		if !needSelf && !needTeam {
			resp.Skipped = append(resp.Skipped, memoryCurationBackfillSkip{Date: date, Reason: "already_succeeded"})
			continue
		}

		var stage memorycuration.Stage
		switch {
		case needSelf && needTeam:
			stage = memorycuration.StageAll
		case needSelf:
			stage = memorycuration.StageAgentSelfReview
		default:
			stage = memorycuration.StageTeamCuration
		}
		plan := memoryCurationBackfillDayPlan{
			Date:           date,
			Stage:          memorycuration.DBStageName(stage),
			TargetAgentIDs: agentIDs,
		}
		if dryRun {
			resp.Queued = append(resp.Queued, plan)
			continue
		}
		runID, status, enqueueErr := h.enqueueMemoryCurationRun(r.Context(), enqueueMemoryCurationRunParams{
			WorkspaceID: workspaceID,
			MemberID:    uuidToString(member.ID),
			Profile:     profile,
			Stage:       stage,
			TriggerKind: "backfill",
			RunStatus:   runStatus,
			Since:       day,
			Until:       day,
			AgentIDs:    agentIDs,
			DryRun:      false,
			Force:       false,
		})
		if enqueueErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to create curation run")
			return
		}
		plan.RunID = runID
		plan.Status = status
		resp.Queued = append(resp.Queued, plan)
	}

	resp.QueuedDays = len(resp.Queued)
	resp.SkipDays = len(resp.Skipped)
	statusCode := http.StatusOK
	if !dryRun && len(resp.Queued) > 0 {
		statusCode = http.StatusAccepted
	}
	writeJSON(w, statusCode, resp)
}

type enqueueMemoryCurationRunParams struct {
	WorkspaceID string
	MemberID    string
	Profile     memoryCuratorProfileResponse
	Stage       memorycuration.Stage
	TriggerKind string
	RunStatus   string
	Since       time.Time
	Until       time.Time
	AgentIDs    []string
	DryRun      bool
	Force       bool
}

func (h *Handler) enqueueMemoryCurationRun(ctx context.Context, params enqueueMemoryCurationRunParams) (runID, status string, err error) {
	dbStage := memorycuration.DBStageName(params.Stage)
	trigger := params.TriggerKind
	if trigger == "" {
		trigger = "manual"
	}
	var agentForRun any
	if len(params.AgentIDs) == 1 {
		agentForRun = parseUUID(params.AgentIDs[0])
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, agent_id, stage, trigger_kind, status, date_from, date_to,
		  dry_run, force, requested_by, profile_id, owner_user_id, runtime_id,
		  curator_agent_id, curator_model, curator_mode, confidence_threshold,
		  config_version, target_agent_ids, execution_owner
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19::uuid[],'daemon')
		RETURNING id::text
	`, params.WorkspaceID, agentForRun, dbStage, trigger, params.RunStatus, params.Since, params.Until,
		params.DryRun, params.Force, params.MemberID, params.Profile.ID, params.Profile.UserID, params.Profile.RuntimeID,
		params.Profile.CuratorAgentID, params.Profile.ModelOverride, params.Profile.Mode, params.Profile.ConfidenceThreshold,
		params.Profile.ConfigVersion, params.AgentIDs).Scan(&runID); err != nil {
		return "", "", err
	}
	if memoryCurationStageIncludesSelfReview(params.Stage) {
		if err := insertMemoryCurationAgentRuns(ctx, tx, runID, params.WorkspaceID, params.Profile.RuntimeID, params.AgentIDs); err != nil {
			return "", "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return runID, params.RunStatus, nil
}

func memoryCurationStageIncludesSelfReview(stage memorycuration.Stage) bool {
	return stage == memorycuration.StageAgentSelfReview || stage == memorycuration.StageAll
}

// memoryCurationRuntimeStaleSecs is the heartbeat-freshness threshold used
// to decide whether a runtime is reachable enough to queue curation work
// against (task #53). Sourced from service.AgentHealthStaleThreshold — the
// same threshold the sweeper/handler packages already use — passed as a
// query parameter rather than hardcoded, so there is exactly one place this
// number lives.
var memoryCurationRuntimeStaleSecs = service.AgentHealthStaleThreshold.Seconds()

func insertMemoryCurationAgentRuns(ctx context.Context, exec dbExecutor, runID, workspaceID, runtimeID string, agentIDs []string) error {
	for _, agentID := range agentIDs {
		// Task #53: was `rt.status = 'online'`, trusting the raw column
		// directly — that column can read "online" for up to ~180s after
		// the runtime actually went silent (sweeper lag), which would
		// queue self-review work against a runtime that's actually
		// unreachable instead of correctly marking it 'skipped'. Keyed off
		// last_seen_at freshness instead; NULL last_seen_at (never
		// heartbeated) or no matching runtime row both fall through to the
		// same 'skipped' outcome the raw check already had for those cases.
		if _, err := exec.Exec(ctx, `
			INSERT INTO memory_curation_agent_run (
			  parent_run_id, workspace_id, agent_id, runtime_id, stage, status, error, finished_at
			)
			SELECT $1::uuid, $2::uuid, a.id, COALESCE(a.runtime_id, NULLIF($4,'')::uuid), 'agent_self_review',
			       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $5::double precision) THEN 'queued' ELSE 'skipped' END,
			       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $5::double precision) THEN '' ELSE 'runtime offline; skipped' END,
			       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $5::double precision) THEN NULL ELSE now() END
			  FROM agent a
			  LEFT JOIN agent_runtime rt ON rt.id = COALESCE(a.runtime_id, NULLIF($4,'')::uuid)
			 WHERE a.id = $3::uuid AND a.workspace_id = $2::uuid
			ON CONFLICT (parent_run_id, agent_id, stage) DO UPDATE SET
			  runtime_id = EXCLUDED.runtime_id,
			  status = CASE WHEN memory_curation_agent_run.status IN ('queued','waiting_runtime') THEN EXCLUDED.status ELSE memory_curation_agent_run.status END,
			  error = CASE WHEN memory_curation_agent_run.status IN ('queued','waiting_runtime') THEN EXCLUDED.error ELSE memory_curation_agent_run.error END,
			  finished_at = CASE WHEN memory_curation_agent_run.status IN ('queued','waiting_runtime') THEN EXCLUDED.finished_at ELSE memory_curation_agent_run.finished_at END,
			  updated_at = now()
		`, runID, workspaceID, agentID, runtimeID, memoryCurationRuntimeStaleSecs); err != nil {
			return err
		}
	}
	return nil
}

type succeededCurationDays struct {
	self map[string]struct{}
	team map[string]struct{}
}

func (s succeededCurationDays) hasSelf(date string) bool {
	_, ok := s.self[date]
	return ok
}

func (s succeededCurationDays) hasTeam(date string) bool {
	_, ok := s.team[date]
	return ok
}

func (h *Handler) loadSucceededMemoryCurationDays(ctx context.Context, workspaceID, profileID string, since, until time.Time) (succeededCurationDays, error) {
	out := succeededCurationDays{
		self: map[string]struct{}{},
		team: map[string]struct{}{},
	}
	rows, err := h.DB.Query(ctx, `
		SELECT date_from::text, stage
		  FROM memory_curation_run
		 WHERE workspace_id = $1
		   AND status = 'succeeded'
		   AND date_from >= $2::date
		   AND date_from <= $3::date
		   AND stage IN ('agent_self_review', 'team_curation', 'all')
		   AND ($4 = '' OR profile_id::text = $4)
	`, workspaceID, since, until, profileID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var date, stage string
		if err := rows.Scan(&date, &stage); err != nil {
			return out, err
		}
		date = firstDateToken(date)
		switch stage {
		case "agent_self_review", "all":
			out.self[date] = struct{}{}
		}
		switch stage {
		case "team_curation", "all":
			out.team[date] = struct{}{}
		}
	}
	return out, rows.Err()
}

func resolveMemoryCurationBackfillRange(sinceRaw, untilRaw, timezone string, now time.Time) (since, until time.Time, err error) {
	loc := time.UTC
	if timezone != "" {
		if loaded, loadErr := time.LoadLocation(timezone); loadErr == nil {
			loc = loaded
		}
	}
	localNow := now.In(loc)
	until = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	since = until.AddDate(0, 0, -(memoryCurationBackfillMaxDays - 1))

	if untilRaw != "" {
		until, err = time.Parse("2006-01-02", untilRaw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid until date")
		}
	}
	if sinceRaw != "" {
		since, err = time.Parse("2006-01-02", sinceRaw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid since date")
		}
	}
	if until.Before(since) {
		return time.Time{}, time.Time{}, errors.New("until must be on or after since")
	}
	// Inclusive day count must be <= 30.
	if since.AddDate(0, 0, memoryCurationBackfillMaxDays-1).Before(until) {
		return time.Time{}, time.Time{}, errors.New("backfill range cannot exceed 30 days")
	}
	return since, until, nil
}

func formatDateUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// defaultMemoryCurationPlanDay matches the nightly scheduler: plan date is
// yesterday in the curator profile timezone, stored as a UTC calendar date.
func defaultMemoryCurationPlanDay(timezone string, now time.Time) (since, until time.Time) {
	loc := time.UTC
	if timezone != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			loc = loaded
		} else if loaded, err := time.LoadLocation(memorycuration.DefaultTimezone); err == nil {
			loc = loaded
		}
	} else if loaded, err := time.LoadLocation(memorycuration.DefaultTimezone); err == nil {
		loc = loaded
	}
	localNow := now.In(loc)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	return day, day
}

func firstDateToken(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}
