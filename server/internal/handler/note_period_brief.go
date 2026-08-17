package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	notePeriodBriefFolderTitle     = "工作介绍"
	notePeriodBriefSourceJournal   = "machine_work_journal" // legacy label; unused on Brief path after K1-T2
	notePeriodBriefSourceCollectors = "period_work_collectors"
	// Legacy Host Digest helpers retained until K2-T2 deletes them; keep
	// constants so unused functions still compile.
	notePeriodBriefDigestTimeout = 4 * time.Second
	notePeriodBriefDigestBudget  = 8 * time.Second
	// Keep the HTTP path snappy: Next.js proxies abort long requests as
	// socket hang up / opaque 500. Collector wait degrades into sources_empty.
	notePeriodBriefCollectorPollEvery  = 400 * time.Millisecond
	notePeriodBriefCollectorStubMarker = "Stub awaiting Agent pack"
)

// Overridable in tests so suite does not sleep the full production budget.
var notePeriodBriefCollectorWaitBudget = 6 * time.Second

// Stable English instruction locked to the period_brief playbook (J3-T1 / J3-T4).
// folderPageID is the private 工作介绍/ folder — Brief lands as a child via human confirm.
func notePeriodBriefInstruction(folderPageID, windowLabel string) string {
	folder := strings.TrimSpace(folderPageID)
	label := strings.TrimSpace(windowLabel)
	if label == "" {
		label = "period"
	}
	return "Write a Period Work Brief for a manager or colleague — a reporting narrative, not a collaboration wrap-up and not slide deck copy.\n" +
		"1) Open with one clear claim about the period.\n" +
		"2) Give 3–7 main threads; each thread has at most 3 bullets and should cite Issue/PR/repo-path evidence when available.\n" +
		"3) Call out delegated leverage (what agents or teammates carried).\n" +
		"4) State what remains unfinished.\n" +
		"5) Put unscoped machine work from collector packs (本机未归类) in its own section — never mix it into the team narrative.\n" +
		"6) Do not list raw commits; do not invent claims without evidence in Facts or collector packs.\n" +
		"7) Deliver the Brief with `multica message send --target <Message target for chat transport> --note-write --note-page-id " + folder +
		"`. The body must be only the Brief markdown. Title it like `工作介绍 " + label +
		"`. The human confirms 「新建子笔记」 under 工作介绍/ — never treat the draft Facts page as the finished Brief, and never pass the draft page id to --note-page-id."
}

// notePeriodBriefCollectorInstruction tells a runtime Agent to gather OS work
// into a structured pack page (ADR 0019). packPageID is the --note-write target.
func notePeriodBriefCollectorInstruction(packPageID, windowLabel, windowStart, windowEnd string) string {
	pack := strings.TrimSpace(packPageID)
	label := strings.TrimSpace(windowLabel)
	if label == "" {
		label = "period"
	}
	start := strings.TrimSpace(windowStart)
	end := strings.TrimSpace(windowEnd)
	rangeHint := label
	if start != "" && end != "" {
		rangeHint = label + " (" + start + " → " + end + ")"
	}
	return "Collect recent work on the OS where this runtime runs for " + rangeHint + " into a structured Period Work collector pack.\n" +
		"Scope: whole-machine HOME for local runtimes; the cloud runtime environment for cloud. Prefer git status, recent commits, dirty trees, and project dirs you can see.\n" +
		"Allowed detail: short diffs, file summaries, and key snippets when needed to explain work. Prefer bounded excerpts over whole files.\n" +
		"Forbidden: keymouse, screenshots, clipboard, browser history, full-repo dumps, secrets (.env / .ssh / keys / credentials), and runtime diagnostics noise.\n" +
		"Denylist paths (skip): .ssh, .gnupg, .aws, .env / .env.*, credential stores, and similar secret roots.\n" +
		"Do NOT write the final Period Work Brief — that is the synthesizer's job.\n" +
		"Pack markdown shape (required headings):\n" +
		"# 采集包 " + label + "\n" +
		"## Runtime\n" +
		"- mode / hostname or cloud env (best effort)\n" +
		"## Repos / roots\n" +
		"- path — short summary of what changed in the window\n" +
		"## Highlights\n" +
		"- claim with optional short diff or snippet\n" +
		"## Unscoped / unclear\n" +
		"- leftover traces that do not map cleanly\n" +
		"Deliver with `multica message send --target <Message target for chat transport> --note-write --note-page-id " + pack +
		"`. Body = pack markdown only. Title it like `采集包 " + label + "`."
}

func notePeriodBriefCollectorPackStub(windowLabel, agentLabel string) string {
	label := strings.TrimSpace(windowLabel)
	if label == "" {
		label = "period"
	}
	who := strings.TrimSpace(agentLabel)
	if who == "" {
		who = "collector"
	}
	return "# 采集包 " + label + "\n\n" +
		"Collector: " + who + "\n\n" +
		notePeriodBriefCollectorStubMarker + " via `--note-write`. Replace this body with structured OS work traces " +
		"(repos/roots, highlights with short diffs/snippets, unscoped leftovers). Do not write the final Brief here.\n"
}

type createNotePeriodBriefRequest struct {
	Window            string   `json:"window"` // day | week | month
	Date              string   `json:"date"`
	Timezone          string   `json:"timezone"`
	AgentID           string   `json:"agent_id"`
	CollectorAgentIDs []string `json:"collector_agent_ids"`
	Sources           []string `json:"sources"`
	ChannelID         string   `json:"channel_id"`
}

type createNotePeriodBriefResponse struct {
	Page              NotePageResponse                `json:"page"`
	Job               NoteWorkerJobResponse           `json:"job"`
	Window            noteRetrospectiveWindowResponse `json:"window"`
	SourcesUsed       []string                        `json:"sources_used"`
	SourcesEmpty      []string                        `json:"sources_empty"`
	SourcesSkipped    []string                        `json:"sources_skipped"`
	FactCount         int                             `json:"fact_count"`
	CollectorAgentIDs []string                        `json:"collector_agent_ids"`
	CollectorJobs     []NoteWorkerJobResponse         `json:"collector_jobs"`
}

// CreateNotePeriodBrief gathers platform Facts, dispatches collector Agents to
// gather OS work packs, waits briefly for packs (timeout → empty degrade), then
// wakes the synthesizer with Facts + collector packs. No Host Digest. No model
// runs here.
func (h *Handler) CreateNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	var req createNotePeriodBriefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	collectorIDs, ok := h.parsePeriodBriefCollectorAgentIDs(w, r.Context(), workspaceID, req.CollectorAgentIDs)
	if !ok {
		return
	}

	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = h.resolveViewingTZ(r)
	}
	window, err := resolveNoteRetrospectiveWindow(noteRetrospectiveWindowKind(strings.TrimSpace(req.Window)), req.Date, tz, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sources := req.Sources
	if len(sources) == 0 {
		sources = append([]string(nil), notePeriodWorkDefaultSources...)
	}
	bundle, err := h.loadNoteRetrospectiveFactsBundle(r.Context(), workspaceID, userID, window.Start, window.End, sources)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load retrospective facts")
		return
	}

	folderID, err := h.ensureNotePeriodBriefFolder(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure period brief folder")
		return
	}

	windowStart := window.Start.UTC().Format(time.RFC3339)
	windowEnd := window.End.UTC().Format(time.RFC3339)
	collectorJobs := make([]NoteWorkerJobResponse, 0, len(collectorIDs))
	for _, collectorID := range collectorIDs {
		collectorUUID := parseUUID(collectorID)
		collector, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          collectorUUID,
			WorkspaceID: workspaceID,
		})
		if err != nil || collector.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, "collector agent not found: "+collectorID)
			return
		}
		job, ok := h.dispatchNotePeriodBriefCollector(
			w, r, workspaceID, userID, userIDString, folderID, collector, window.Label, windowStart, windowEnd,
		)
		if !ok {
			return
		}
		collectorJobs = append(collectorJobs, job)
	}

	packResults := h.awaitPeriodBriefCollectorPacks(r.Context(), workspaceID, userID, collectorJobs)
	packsText := formatNotePeriodBriefPacks(packResults)
	factsText := formatNotePeriodBriefFacts(bundle.Facts)

	used := append([]string(nil), bundle.SourcesUsed...)
	empty := append([]string(nil), bundle.SourcesEmpty...)
	skipped := append([]string(nil), bundle.SourcesSkipped...)
	packsReady := 0
	for _, pack := range packResults {
		if pack.Status == "ready" {
			packsReady++
		}
	}
	if packsReady > 0 {
		if !containsNoteRetrospectiveSource(used, notePeriodBriefSourceCollectors) {
			used = append(used, notePeriodBriefSourceCollectors)
		}
	} else if !containsNoteRetrospectiveSource(empty, notePeriodBriefSourceCollectors) {
		empty = append(empty, notePeriodBriefSourceCollectors)
	}

	title := fmt.Sprintf("工作介绍 %s · 底稿", window.Label)
	content := buildNotePeriodBriefDraftMarkdown(window, factsText, packsText, used, empty, skipped)
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, folderID, userID, normalizeNoteTitle(title), content))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create period brief draft note")
		return
	}

	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	job, ok := h.dispatchNotePeriodBriefWorker(w, r, workspaceID, userID, userIDString, folderID, page, agent, window.Label, strings.TrimSpace(req.ChannelID), factsText, packsText)
	if !ok {
		return
	}

	writeJSON(w, http.StatusCreated, createNotePeriodBriefResponse{
		Page: notePageToResponse(page, userID, []string{}, nil),
		Job:  job,
		Window: noteRetrospectiveWindowResponse{
			Kind:     string(window.Kind),
			Timezone: window.Timezone,
			Start:    windowStart,
			End:      windowEnd,
			Label:    window.Label,
		},
		SourcesUsed:       used,
		SourcesEmpty:      empty,
		SourcesSkipped:    skipped,
		FactCount:         bundle.FactCount(),
		CollectorAgentIDs: collectorIDs,
		CollectorJobs:     collectorJobs,
	})
}

// parsePeriodBriefCollectorAgentIDs requires at least one non-archived Agent in
// the workspace. Order is preserved; duplicates are dropped.
func (h *Handler) parsePeriodBriefCollectorAgentIDs(
	w http.ResponseWriter,
	ctx context.Context,
	workspaceID pgtype.UUID,
	raw []string,
) ([]string, bool) {
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "collector_agent_ids is required")
		return nil, false
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		agentUUID, ok := parseUUIDOrBadRequest(w, trimmed, "collector_agent_ids")
		if !ok {
			return nil, false
		}
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          agentUUID,
			WorkspaceID: workspaceID,
		})
		if err != nil || agent.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, "collector agent not found: "+trimmed)
			return nil, false
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		writeError(w, http.StatusBadRequest, "collector_agent_ids is required")
		return nil, false
	}
	return out, true
}

type notePeriodBriefDigestPack struct {
	ComputerID string
	Disabled   bool
	FetchError string
	Repos      []scopedWorkDigestRepo
}

func (h *Handler) listOwnedComputerIDsInWorkspace(ctx context.Context, workspaceID, userID pgtype.UUID) ([]string, error) {
	rows, err := h.DB.Query(ctx, `
SELECT DISTINCT b.daemon_id
FROM computer_workspace_bindings b
JOIN computer_identity_owner o ON o.daemon_id = b.daemon_id
WHERE b.workspace_id = $1
  AND b.active = TRUE
  AND b.revoked_at IS NULL
  AND o.user_id = $2
ORDER BY b.daemon_id`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var daemonID string
		if err := rows.Scan(&daemonID); err != nil {
			return nil, err
		}
		out = append(out, daemonID)
	}
	return out, rows.Err()
}

func (h *Handler) collectOwnerWorkDigests(
	ctx context.Context,
	computerIDs []string,
	window noteRetrospectiveWindow,
	workspaceRemotes []string,
) ([]notePeriodBriefDigestPack, bool) {
	out := make([]notePeriodBriefDigestPack, len(computerIDs))
	if len(computerIDs) == 0 {
		return out, true
	}
	budgetCtx, budgetCancel := context.WithTimeout(ctx, notePeriodBriefDigestBudget)
	defer budgetCancel()

	var wg sync.WaitGroup
	for i, computerID := range computerIDs {
		wg.Add(1)
		go func(i int, computerID string) {
			defer wg.Done()
			digestCtx, cancel := context.WithTimeout(budgetCtx, notePeriodBriefDigestTimeout)
			defer cancel()
			digest, err := h.fetchComputerWorkDigest(digestCtx, computerID, protocol.ComputerWorkDigestPayload{
				RequestID: uuid.NewString(),
				Start:     window.Start,
				End:       window.End,
			})
			pack := notePeriodBriefDigestPack{ComputerID: computerID}
			if err != nil {
				pack.FetchError = workDigestCollectError(err)
				out[i] = pack
				return
			}
			pack.Disabled = digest.Disabled
			pack.Repos = scopeWorkDigestRepos(digest.Repos, workspaceRemotes)
			out[i] = pack
		}(i, computerID)
	}
	wg.Wait()

	journalEmpty := true
	for _, pack := range out {
		if pack.FetchError == "" && !pack.Disabled && len(pack.Repos) > 0 {
			journalEmpty = false
			break
		}
	}
	return out, journalEmpty
}

func workDigestCollectError(err error) string {
	if err == nil {
		return ""
	}
	if errorsIsComputerOffline(err) {
		return "computer_offline"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "computer_work_digest_timeout"
	}
	return err.Error()
}

func errorsIsComputerOffline(err error) bool {
	return errors.Is(err, daemonws.ErrComputerOffline)
}

func (h *Handler) ensureNotePeriodBriefFolder(ctx context.Context, workspaceID, userID pgtype.UUID) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id FROM note_page
WHERE workspace_id = $1
  AND owner_user_id = $2
  AND parent_id IS NULL
  AND deleted_at IS NULL
  AND title = $3
ORDER BY created_at ASC
LIMIT 1`, workspaceID, userID, notePeriodBriefFolderTitle).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, err
	}
	page, err := scanNotePage(h.DB.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, NULL, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, userID, notePeriodBriefFolderTitle))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return page.ID, nil
}

func (h *Handler) dispatchNotePeriodBriefCollector(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	folderID pgtype.UUID,
	agent db.Agent,
	windowLabel, windowStart, windowEnd string,
) (NoteWorkerJobResponse, bool) {
	agentLabel := strings.TrimSpace(agent.DisplayName)
	if agentLabel == "" {
		agentLabel = strings.TrimSpace(agent.Name)
	}
	if agentLabel == "" {
		agentLabel = uuidToString(agent.ID)
	}
	packTitle := normalizeNoteTitle(fmt.Sprintf("采集包 %s · %s", windowLabel, agentLabel))
	packContent := notePeriodBriefCollectorPackStub(windowLabel, agentLabel)
	packPage, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, folderID, userID, packTitle, packContent))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collector pack note")
		return NoteWorkerJobResponse{}, false
	}

	ch, ok := h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, "")
	if !ok {
		return NoteWorkerJobResponse{}, false
	}

	packPageID := uuidToString(packPage.ID)
	instruction := notePeriodBriefCollectorInstruction(packPageID, windowLabel, windowStart, windowEnd)

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, workspaceID, packPage.ID, userID, agent.ID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collector Worker job")
		return NoteWorkerJobResponse{}, false
	}

	visibleContent, parts, err := h.buildNoteWorkerChannelMessage(r.Context(), ch, agent, packPage, instruction)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return NoteWorkerJobResponse{}, false
	}
	authorName := h.channelAuthorName(r.Context(), userIDString)
	threadID := uuid.NewString()
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:   parseUUID(ch.ID),
		WorkspaceID: workspaceID,
		AuthorID:    userID,
		AuthorName:  authorName,
		Content:     visibleContent,
		Parts:       parts,
		ThreadID:    &threadID,
	}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post collector Worker message")
		return NoteWorkerJobResponse{}, false
	}
	msg := result.Message
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(ch.ID))
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), uuidToString(workspaceID), parseUUID(ch.ID))
	}
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(r.Context(), uuidToString(workspaceID), ch.ID))
	h.publishToUsers(protocol.EventChannelMessage, uuidToString(workspaceID), "member", userIDString, recipientIDs, msg)

	workerPrompt := wrapNoteWorkerChannelWakePrompt(
		buildNotePeriodBriefCollectorPrompt(
			instruction, packPageID, windowLabel, windowStart, windowEnd, packPage.Title, packPage.Content,
		),
		h.agentMessageTargetForPrompt(r.Context(), ch, msg),
	)
	task, err := h.enqueueChannelAgentPrompt(
		r.Context(), ch, agent, msg, userID, workerPrompt,
		"note worker", true, protocol.AgentInboxReasonNoteWorker, channelDirectedWakePriority,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue collector Worker job: "+err.Error())
		return NoteWorkerJobResponse{}, false
	}
	mergedContext, err := service.WithNoteBrief(task.Context, service.NoteBrief{
		Version: 1,
		PageID:  packPageID,
		Title:   packPage.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach collector note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE agent_inbox_event SET context = $1::jsonb WHERE id = $2`, mergedContext, task.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist collector note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE note_worker_job
SET task_id = $1, channel_message_id = $2, status = 'dispatched', updated_at = now()
WHERE id = $3`, task.ID, parseUUID(msg.ID), jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link collector Worker task")
		return NoteWorkerJobResponse{}, false
	}
	resp, err := h.noteWorkerJobResponse(r.Context(), workspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collector Worker job")
		return NoteWorkerJobResponse{}, false
	}
	return resp, true
}

func (h *Handler) dispatchNotePeriodBriefWorker(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	folderID pgtype.UUID,
	page notePageRow,
	agent db.Agent,
	windowLabel, channelID, factsText, packsText string,
) (NoteWorkerJobResponse, bool) {
	ch, ok := h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, channelID)
	if !ok {
		return NoteWorkerJobResponse{}, false
	}

	folderPageID := uuidToString(folderID)
	instruction := notePeriodBriefInstruction(folderPageID, windowLabel)

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, workspaceID, page.ID, userID, agent.ID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note Worker job")
		return NoteWorkerJobResponse{}, false
	}

	visibleContent, parts, err := h.buildNotePeriodBriefChannelMessage(r.Context(), ch, agent, folderID, page, instruction)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return NoteWorkerJobResponse{}, false
	}
	authorName := h.channelAuthorName(r.Context(), userIDString)
	threadID := uuid.NewString()
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:   parseUUID(ch.ID),
		WorkspaceID: workspaceID,
		AuthorID:    userID,
		AuthorName:  authorName,
		Content:     visibleContent,
		Parts:       parts,
		ThreadID:    &threadID,
	}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post note Worker message")
		return NoteWorkerJobResponse{}, false
	}
	msg := result.Message
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(ch.ID))
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), uuidToString(workspaceID), parseUUID(ch.ID))
	}
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(r.Context(), uuidToString(workspaceID), ch.ID))
	h.publishToUsers(protocol.EventChannelMessage, uuidToString(workspaceID), "member", userIDString, recipientIDs, msg)

	workerPrompt := wrapNoteWorkerChannelWakePrompt(
		buildNotePeriodBriefPrompt(instruction, uuidToString(page.ID), folderPageID, windowLabel, page.Title, page.Content, factsText, packsText),
		h.agentMessageTargetForPrompt(r.Context(), ch, msg),
	)
	task, err := h.enqueueChannelAgentPrompt(
		r.Context(), ch, agent, msg, userID, workerPrompt,
		"note worker", true, protocol.AgentInboxReasonNoteWorker, channelDirectedWakePriority,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue note Worker job: "+err.Error())
		return NoteWorkerJobResponse{}, false
	}
	mergedContext, err := service.WithNoteBrief(task.Context, service.NoteBrief{
		Version: 1,
		PageID:  uuidToString(page.ID),
		Title:   page.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE agent_inbox_event SET context = $1::jsonb WHERE id = $2`, mergedContext, task.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE note_worker_job
SET task_id = $1, channel_message_id = $2, status = 'dispatched', updated_at = now()
WHERE id = $3`, task.ID, parseUUID(msg.ID), jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link note Worker task")
		return NoteWorkerJobResponse{}, false
	}
	resp, err := h.noteWorkerJobResponse(r.Context(), workspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note Worker job")
		return NoteWorkerJobResponse{}, false
	}
	return resp, true
}

// buildNotePeriodBriefChannelMessage posts a note_brief sticky on the 工作介绍/
// folder (write target for Create child) while snapshotting the draft body for
// human context. Task NoteBrief context still points at the draft for notes get.
func (h *Handler) buildNotePeriodBriefChannelMessage(
	ctx context.Context,
	ch ChannelResponse,
	agent db.Agent,
	folderID pgtype.UUID,
	draft notePageRow,
	instruction string,
) (string, []protocol.MessagePart, error) {
	title := strings.TrimSpace(draft.Title)
	if title == "" {
		title = notePeriodBriefFolderTitle
	}
	body := strings.TrimSpace(instruction)
	if ch.Kind == "group" {
		handle := strings.TrimSpace(agent.Name)
		if handle == "" {
			handle = uuidToString(agent.ID)
		}
		body = "@" + handle + " " + body
	}
	brief := protocol.MessagePart{
		Type:  protocol.MessagePartTypeNoteBrief,
		RefID: uuidToString(folderID),
		Label: title,
		Text:  draft.Content,
	}
	content, parts, err := messageparts.Normalize(body, []protocol.MessagePart{brief})
	if err != nil {
		return "", nil, err
	}
	content, parts, err = h.enrichChannelMessageMentions(ctx, ch, content, parts)
	if err != nil {
		return "", nil, err
	}
	return content, parts, nil
}

func formatNotePeriodBriefFacts(facts noteRetrospectiveFacts) string {
	var b strings.Builder
	b.WriteString("## Platform Facts\n")
	if len(facts.Issues) == 0 && len(facts.Notes) == 0 && len(facts.Runs) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	if len(facts.Issues) > 0 {
		b.WriteString("\n### Issues\n")
		for _, fact := range facts.Issues {
			fmt.Fprintf(&b, "- [%s] %s %s (%s)", fact.Identifier, fact.Action, fact.Title, fact.Attribution)
			if len(fact.PullRequests) > 0 {
				b.WriteString(" PRs:")
				for _, pr := range fact.PullRequests {
					fmt.Fprintf(&b, " #%d %s %s", pr.Number, pr.State, pr.URL)
				}
			}
			b.WriteByte('\n')
		}
	}
	if len(facts.Notes) > 0 {
		b.WriteString("\n### Touched notes\n")
		for _, fact := range facts.Notes {
			fmt.Fprintf(&b, "- %s (%s)\n", fact.Title, fact.PageID)
		}
	}
	if len(facts.Runs) > 0 {
		b.WriteString("\n### Agent runs\n")
		for _, fact := range facts.Runs {
			fmt.Fprintf(&b, "- %s: %s\n", fact.AgentName, fact.Summary)
		}
	}
	return b.String()
}

func formatNotePeriodBriefDigests(packs []notePeriodBriefDigestPack) string {
	var b strings.Builder
	b.WriteString("## Machine Work Digest\n")
	if len(packs) == 0 {
		b.WriteString("disabled: true\n(no owned Computers)\n")
		return b.String()
	}
	for _, pack := range packs {
		fmt.Fprintf(&b, "\n### Computer %s\n", pack.ComputerID)
		if pack.FetchError != "" {
			fmt.Fprintf(&b, "fetch_error: %s\n", pack.FetchError)
			continue
		}
		fmt.Fprintf(&b, "disabled: %t\n", pack.Disabled)
		if len(pack.Repos) == 0 {
			b.WriteString("repos: []\n")
			continue
		}
		for _, repo := range pack.Repos {
			fmt.Fprintf(&b, "- root: %s\n  scope: %s\n", repo.Root, repo.Scope)
			if len(repo.Remotes) > 0 {
				fmt.Fprintf(&b, "  remotes: %s\n", strings.Join(repo.Remotes, ", "))
			}
			fmt.Fprintf(&b, "  commits: %d dirty: %d\n", len(repo.Commits), len(repo.Dirty))
			for _, commit := range repo.Commits {
				fmt.Fprintf(&b, "  - %s %s (%s)\n", commit.Hash[:minInt(8, len(commit.Hash))], commit.Subject, commit.Author)
			}
			for _, dirty := range repo.Dirty {
				fmt.Fprintf(&b, "  - dirty %s %s\n", dirty.Status, dirty.Path)
			}
		}
	}
	return b.String()
}

type notePeriodBriefPackResult struct {
	AgentID string
	PageID  string
	Title   string
	Content string
	Status  string // ready | empty | failed
}

// awaitPeriodBriefCollectorPacks polls pack pages / job status until ready or
// the wait budget elapses. Timeouts degrade to empty — they never fail the
// whole Period Brief request.
func (h *Handler) awaitPeriodBriefCollectorPacks(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	jobs []NoteWorkerJobResponse,
) []notePeriodBriefPackResult {
	out := make([]notePeriodBriefPackResult, len(jobs))
	for i, job := range jobs {
		out[i] = notePeriodBriefPackResult{
			AgentID: job.AgentID,
			PageID:  job.PageID,
			Status:  "empty",
		}
	}
	if len(jobs) == 0 {
		return out
	}
	deadline := time.Now().Add(notePeriodBriefCollectorWaitBudget)
	for {
		allSettled := true
		for i, job := range jobs {
			if out[i].Status == "ready" || out[i].Status == "failed" {
				continue
			}
			page, err := scanNotePage(h.DB.QueryRow(ctx, `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
				parseUUID(job.PageID), workspaceID))
			if err != nil {
				allSettled = false
				continue
			}
			out[i].Title = page.Title
			out[i].Content = page.Content

			projected, _ := h.noteWorkerJobResponse(ctx, workspaceID, userID, parseUUID(job.ID))
			status := projected.Status
			if status == "" {
				status = job.Status
			}
			stub := strings.Contains(page.Content, notePeriodBriefCollectorStubMarker)
			switch {
			case !stub && strings.TrimSpace(page.Content) != "":
				out[i].Status = "ready"
			case status == "failed" || status == "cancelled":
				out[i].Status = "failed"
			case status == "completed" && stub:
				out[i].Status = "empty"
			default:
				allSettled = false
			}
		}
		if allSettled || time.Now().After(deadline) {
			break
		}
		timer := time.NewTimer(notePeriodBriefCollectorPollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out
		case <-timer.C:
		}
	}
	return out
}

func formatNotePeriodBriefPacks(packs []notePeriodBriefPackResult) string {
	var b strings.Builder
	b.WriteString("## Collector packs\n")
	if len(packs) == 0 {
		b.WriteString("status: empty\n(no collectors)\n")
		return b.String()
	}
	for _, pack := range packs {
		fmt.Fprintf(&b, "\n### Collector %s\n", pack.AgentID)
		fmt.Fprintf(&b, "page_id: %s\n", pack.PageID)
		fmt.Fprintf(&b, "status: %s\n", pack.Status)
		if pack.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", pack.Title)
		}
		switch pack.Status {
		case "ready":
			b.WriteString(strings.TrimSpace(pack.Content))
			b.WriteByte('\n')
		case "failed":
			b.WriteString("(collector job failed — treat as empty)\n")
		default:
			b.WriteString("(empty — pack still stub or timed out; do not invent OS work)\n")
		}
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildNotePeriodBriefDraftMarkdown(
	window noteRetrospectiveWindow,
	factsText, packsText string,
	used, empty, skipped []string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 工作介绍底稿 %s\n\n", window.Label)
	fmt.Fprintf(&b, "- 窗口：%s → %s (%s)\n", window.Start.UTC().Format(time.RFC3339), window.End.UTC().Format(time.RFC3339), window.Timezone)
	if len(used) > 0 {
		fmt.Fprintf(&b, "- 已用：%s\n", strings.Join(used, ", "))
	}
	if len(empty) > 0 {
		fmt.Fprintf(&b, "- 空源：%s\n", strings.Join(empty, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "- 已跳过：%s\n", strings.Join(skipped, ", "))
	}
	b.WriteString("\n> 这是合成底稿，不是给领导看的 Period Work Brief。Agent 应另写 Brief 页。\n\n")
	b.WriteString(factsText)
	b.WriteByte('\n')
	b.WriteString(packsText)
	return b.String()
}
