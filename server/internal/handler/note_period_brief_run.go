package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// notePeriodBriefCollectorRef is durable per-collector state for one Brief run.
// PackMarkdown is the implicit collector artifact (not a Notes page). Cleared
// when the run reaches status=done after synthesis wake.
type notePeriodBriefCollectorRef struct {
	AgentID      string `json:"agent_id"`
	PackPageID   string `json:"pack_page_id,omitempty"` // legacy; unused for new runs
	JobID        string `json:"job_id"`
	ChannelID    string `json:"channel_id,omitempty"`
	RetryCount   int    `json:"retry_count"`
	WindowLabel  string `json:"window_label"`
	WindowStart  string `json:"window_start"`
	WindowEnd    string `json:"window_end"`
	PackMarkdown string `json:"pack_markdown,omitempty"`
	PackJobID    string `json:"pack_job_id,omitempty"`
}

type notePeriodBriefRunRow struct {
	ID                 pgtype.UUID
	WorkspaceID        pgtype.UUID
	OwnerUserID        pgtype.UUID
	DraftPageID        pgtype.UUID
	FolderPageID       pgtype.UUID
	SynthesizerAgentID pgtype.UUID
	WindowLabel        string
	WindowStart        time.Time
	WindowEnd          time.Time
	Timezone           string
	WindowKind         string
	ChannelID          pgtype.UUID
	FactsText          string
	SourcesUsed        []string
	SourcesEmpty       []string
	SourcesSkipped     []string
	Collectors         []notePeriodBriefCollectorRef
	Status             string
	UserFocus          string
	CollectPlan        *notePeriodBriefCollectPlan
	PlannerJobID       pgtype.UUID
	ChatSessionID      pgtype.UUID
	SourcePageID       pgtype.UUID
}

func (h *Handler) insertNotePeriodBriefRun(
	ctx context.Context,
	workspaceID, userID, draftID, folderID, synthAgentID pgtype.UUID,
	window noteRetrospectiveWindow,
	channelID, factsText string,
	used, empty, skipped []string,
	collectors []notePeriodBriefCollectorRef,
	userFocus, status string,
	chatSessionID, sourcePageID pgtype.UUID,
) error {
	raw, err := json.Marshal(collectors)
	if err != nil {
		return err
	}
	if used == nil {
		used = []string{}
	}
	if empty == nil {
		empty = []string{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	var channelArg any
	if trimmed := strings.TrimSpace(channelID); trimmed != "" {
		channelArg = parseUUID(trimmed)
	}
	if strings.TrimSpace(status) == "" {
		status = "collecting"
	}
	_, err = h.DB.Exec(ctx, `
INSERT INTO note_period_brief_run (
  workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
  window_label, window_start, window_end, timezone, window_kind,
  channel_id, facts_text, sources_used, sources_empty, sources_skipped,
  collectors, status, user_focus, chat_session_id, source_page_id
) VALUES (
  $1,$2,$3,$4,$5,
  $6,$7,$8,$9,$10,
  $11,$12,$13,$14,$15,
  $16::jsonb, $17, $18, $19, $20
)`,
		workspaceID, userID, draftID, folderID, synthAgentID,
		window.Label, window.Start.UTC(), window.End.UTC(), window.Timezone, string(window.Kind),
		channelArg, factsText, used, empty, skipped,
		raw, status, strings.TrimSpace(userFocus),
		nullableUUIDArg(chatSessionID), nullableUUIDArg(sourcePageID),
	)
	return err
}

func (h *Handler) loadNotePeriodBriefRunByDraft(
	ctx context.Context,
	workspaceID, draftPageID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	var row notePeriodBriefRunRow
	var collectorsRaw []byte
	var planRaw []byte
	var channelID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
       window_label, window_start, window_end, timezone, window_kind,
       channel_id, facts_text, sources_used, sources_empty, sources_skipped,
       collectors, status, user_focus, collect_plan, planner_job_id,
       chat_session_id, source_page_id
FROM note_period_brief_run
WHERE draft_page_id = $1 AND workspace_id = $2`, draftPageID, workspaceID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.DraftPageID, &row.FolderPageID, &row.SynthesizerAgentID,
		&row.WindowLabel, &row.WindowStart, &row.WindowEnd, &row.Timezone, &row.WindowKind,
		&channelID, &row.FactsText, &row.SourcesUsed, &row.SourcesEmpty, &row.SourcesSkipped,
		&collectorsRaw, &row.Status, &row.UserFocus, &planRaw, &row.PlannerJobID,
		&row.ChatSessionID, &row.SourcePageID,
	)
	if err != nil {
		return row, err
	}
	row.ChannelID = channelID
	if len(collectorsRaw) > 0 {
		_ = json.Unmarshal(collectorsRaw, &row.Collectors)
	}
	if len(planRaw) > 0 && string(planRaw) != "null" {
		var plan notePeriodBriefCollectPlan
		if json.Unmarshal(planRaw, &plan) == nil {
			row.CollectPlan = &plan
		}
	}
	return row, nil
}

func (h *Handler) loadNotePeriodBriefRunByID(
	ctx context.Context,
	workspaceID, userID, runID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	var row notePeriodBriefRunRow
	var collectorsRaw []byte
	var planRaw []byte
	var channelID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
       window_label, window_start, window_end, timezone, window_kind,
       channel_id, facts_text, sources_used, sources_empty, sources_skipped,
       collectors, status, user_focus, collect_plan, planner_job_id,
       chat_session_id, source_page_id
FROM note_period_brief_run
WHERE id = $1 AND workspace_id = $2 AND owner_user_id = $3`, runID, workspaceID, userID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.DraftPageID, &row.FolderPageID, &row.SynthesizerAgentID,
		&row.WindowLabel, &row.WindowStart, &row.WindowEnd, &row.Timezone, &row.WindowKind,
		&channelID, &row.FactsText, &row.SourcesUsed, &row.SourcesEmpty, &row.SourcesSkipped,
		&collectorsRaw, &row.Status, &row.UserFocus, &planRaw, &row.PlannerJobID,
		&row.ChatSessionID, &row.SourcePageID,
	)
	if err != nil {
		return row, err
	}
	row.ChannelID = channelID
	if len(collectorsRaw) > 0 {
		_ = json.Unmarshal(collectorsRaw, &row.Collectors)
	}
	if len(planRaw) > 0 && string(planRaw) != "null" {
		var plan notePeriodBriefCollectPlan
		if json.Unmarshal(planRaw, &plan) == nil {
			row.CollectPlan = &plan
		}
	}
	return row, nil
}

func (h *Handler) updateNotePeriodBriefRunPlannerJob(ctx context.Context, runID, plannerJobID pgtype.UUID) error {
	_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET planner_job_id = $1, updated_at = now()
WHERE id = $2`, plannerJobID, runID)
	return err
}

func (h *Handler) updateNotePeriodBriefRunPlan(
	ctx context.Context,
	runID pgtype.UUID,
	plan *notePeriodBriefCollectPlan,
	collectors []notePeriodBriefCollectorRef,
	status string,
) error {
	var planArg any
	if plan != nil {
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		planArg = raw
	}
	raw, err := json.Marshal(collectors)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		status = "collecting"
	}
	_, err = h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET collect_plan = $1::jsonb, collectors = $2::jsonb, status = $3, updated_at = now()
WHERE id = $4`, planArg, raw, status, runID)
	return err
}

func (h *Handler) updateNotePeriodBriefRunCollectors(
	ctx context.Context,
	runID pgtype.UUID,
	collectors []notePeriodBriefCollectorRef,
	status string,
) error {
	raw, err := json.Marshal(collectors)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET collectors = $1::jsonb, status = $2, updated_at = now()
WHERE id = $3 AND status IN ('planning', 'collecting', 'synthesizing')`, raw, status, runID)
	return err
}

func (h *Handler) updateNotePeriodBriefRunStatus(ctx context.Context, runID pgtype.UUID, status string) error {
	if strings.TrimSpace(status) == "" {
		return nil
	}
	_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET status = $1, updated_at = now()
WHERE id = $2`, status, runID)
	return err
}

// mergeNotePeriodBriefCollector patches one collector object in place so two
// machines submitting at once cannot clobber each other's pack or job_id.
func (h *Handler) mergeNotePeriodBriefCollector(
	ctx context.Context,
	runID pgtype.UUID,
	agentID string,
	patch map[string]any,
	status string,
) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("collector agent id is required")
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET collectors = (
      SELECT COALESCE(jsonb_agg(elem ORDER BY ord), '[]'::jsonb)
      FROM (
        SELECT
          CASE WHEN c->>'agent_id' = $2 THEN c || $3::jsonb ELSE c END AS elem,
          ordinality AS ord
        FROM jsonb_array_elements(collectors) WITH ORDINALITY AS t(c, ordinality)
      ) s
    ),
    status = CASE WHEN $4 <> '' THEN $4 ELSE status END,
    updated_at = now()
WHERE id = $1
  AND collectors @> jsonb_build_array(jsonb_build_object('agent_id', $2::text))`,
		runID, agentID, raw, strings.TrimSpace(status))
	return err
}

func collectorRefsFromJobs(jobs []NoteWorkerJobResponse, windowLabel, windowStart, windowEnd string) []notePeriodBriefCollectorRef {
	out := make([]notePeriodBriefCollectorRef, 0, len(jobs))
	for _, job := range jobs {
		ref := notePeriodBriefCollectorRef{
			AgentID: job.AgentID,
			// Job.PageID is the draft page (Worker ACL / notes get). Packs are
			// stored in PackMarkdown, not as child Notes pages.
			PackPageID:  "",
			JobID:       job.ID,
			RetryCount:  0,
			WindowLabel: windowLabel,
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		}
		if job.ChannelID != nil {
			ref.ChannelID = *job.ChannelID
		}
		out = append(out, ref)
	}
	return out
}

func jobsFromCollectorRefs(refs []notePeriodBriefCollectorRef, draftPageID string) []NoteWorkerJobResponse {
	out := make([]NoteWorkerJobResponse, 0, len(refs))
	draftPageID = strings.TrimSpace(draftPageID)
	for _, ref := range refs {
		pageID := draftPageID
		if pageID == "" {
			pageID = strings.TrimSpace(ref.PackPageID) // legacy runs
		}
		job := NoteWorkerJobResponse{
			ID:      ref.JobID,
			PageID:  pageID,
			AgentID: ref.AgentID,
			Status:  "dispatched",
		}
		if ref.ChannelID != "" {
			ch := ref.ChannelID
			job.ChannelID = &ch
		}
		out = append(out, job)
	}
	return out
}

func findCollectorRef(refs []notePeriodBriefCollectorRef, agentID string) (notePeriodBriefCollectorRef, int, bool) {
	for i, ref := range refs {
		if ref.AgentID == agentID {
			return ref, i, true
		}
	}
	return notePeriodBriefCollectorRef{}, -1, false
}

func clearCollectorPackMarkdown(refs []notePeriodBriefCollectorRef) []notePeriodBriefCollectorRef {
	out := make([]notePeriodBriefCollectorRef, len(refs))
	copy(out, refs)
	for i := range out {
		out[i].PackMarkdown = ""
	}
	return out
}

func formatPeriodBriefRetryHint(draftPageID string) string {
	return fmt.Sprintf(
		"Retry only retryable collectors (never permanent config/auth/key failures) with:\n"+
			"`multica notes period-brief retry-collectors --draft-page-id %s [--collector-agent-id <id>]`\n"+
			"Exactly one retry per collector. Inbox will not auto-retry. After that attempt settles, the result is final.\n"+
			"After a successful retry call, stop and wait — the platform re-wakes you when packs settle.",
		draftPageID,
	)
}
