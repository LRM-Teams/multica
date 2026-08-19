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
type notePeriodBriefCollectorRef struct {
	AgentID     string `json:"agent_id"`
	PackPageID  string `json:"pack_page_id"`
	JobID       string `json:"job_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	RetryCount  int    `json:"retry_count"`
	WindowLabel string `json:"window_label"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
}

type notePeriodBriefRunRow struct {
	ID                  pgtype.UUID
	WorkspaceID         pgtype.UUID
	OwnerUserID         pgtype.UUID
	DraftPageID         pgtype.UUID
	FolderPageID        pgtype.UUID
	SynthesizerAgentID  pgtype.UUID
	WindowLabel         string
	WindowStart         time.Time
	WindowEnd           time.Time
	Timezone            string
	WindowKind          string
	ChannelID           pgtype.UUID
	FactsText           string
	SourcesUsed         []string
	SourcesEmpty        []string
	SourcesSkipped      []string
	Collectors          []notePeriodBriefCollectorRef
	Status              string
}

func (h *Handler) insertNotePeriodBriefRun(
	ctx context.Context,
	workspaceID, userID, draftID, folderID, synthAgentID pgtype.UUID,
	window noteRetrospectiveWindow,
	channelID, factsText string,
	used, empty, skipped []string,
	collectors []notePeriodBriefCollectorRef,
) error {
	raw, err := json.Marshal(collectors)
	if err != nil {
		return err
	}
	var channelArg any
	if trimmed := strings.TrimSpace(channelID); trimmed != "" {
		channelArg = parseUUID(trimmed)
	}
	_, err = h.DB.Exec(ctx, `
INSERT INTO note_period_brief_run (
  workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
  window_label, window_start, window_end, timezone, window_kind,
  channel_id, facts_text, sources_used, sources_empty, sources_skipped,
  collectors, status
) VALUES (
  $1,$2,$3,$4,$5,
  $6,$7,$8,$9,$10,
  $11,$12,$13,$14,$15,
  $16::jsonb, 'collecting'
)`,
		workspaceID, userID, draftID, folderID, synthAgentID,
		window.Label, window.Start.UTC(), window.End.UTC(), window.Timezone, string(window.Kind),
		channelArg, factsText, used, empty, skipped,
		raw,
	)
	return err
}

func (h *Handler) loadNotePeriodBriefRunByDraft(
	ctx context.Context,
	workspaceID, draftPageID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	var row notePeriodBriefRunRow
	var collectorsRaw []byte
	var channelID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
       window_label, window_start, window_end, timezone, window_kind,
       channel_id, facts_text, sources_used, sources_empty, sources_skipped,
       collectors, status
FROM note_period_brief_run
WHERE draft_page_id = $1 AND workspace_id = $2`, draftPageID, workspaceID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.DraftPageID, &row.FolderPageID, &row.SynthesizerAgentID,
		&row.WindowLabel, &row.WindowStart, &row.WindowEnd, &row.Timezone, &row.WindowKind,
		&channelID, &row.FactsText, &row.SourcesUsed, &row.SourcesEmpty, &row.SourcesSkipped,
		&collectorsRaw, &row.Status,
	)
	if err != nil {
		return row, err
	}
	row.ChannelID = channelID
	if len(collectorsRaw) > 0 {
		_ = json.Unmarshal(collectorsRaw, &row.Collectors)
	}
	return row, nil
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
WHERE id = $3`, raw, status, runID)
	return err
}

func collectorRefsFromJobs(jobs []NoteWorkerJobResponse, windowLabel, windowStart, windowEnd string) []notePeriodBriefCollectorRef {
	out := make([]notePeriodBriefCollectorRef, 0, len(jobs))
	for _, job := range jobs {
		ref := notePeriodBriefCollectorRef{
			AgentID:     job.AgentID,
			PackPageID:  job.PageID,
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

func jobsFromCollectorRefs(refs []notePeriodBriefCollectorRef) []NoteWorkerJobResponse {
	out := make([]NoteWorkerJobResponse, 0, len(refs))
	for _, ref := range refs {
		job := NoteWorkerJobResponse{
			ID:      ref.JobID,
			PageID:  ref.PackPageID,
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

func formatPeriodBriefRetryHint(draftPageID string) string {
	return fmt.Sprintf(
		"Retry only retryable collectors (never permanent config/auth/key failures) with:\n"+
			"`multica notes period-brief retry-collectors --draft-page-id %s [--collector-agent-id <id>]`\n"+
			"Max %d retries per collector. Platform rejects permanent failures and over-cap retries.\n"+
			"After a successful retry call, stop and wait — the platform re-wakes you when packs settle.",
		draftPageID, notePeriodBriefCollectorMaxRetries,
	)
}
