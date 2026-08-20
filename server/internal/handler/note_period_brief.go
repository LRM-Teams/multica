package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	notePeriodBriefFolderTitle        = "工作介绍"
	notePeriodBriefSourceCollectors   = "period_work_collectors"
	notePeriodBriefCollectorPollEvery = 1 * time.Second
)

// Absolute safety ceiling for the background wait loop. Active collectors are
// waited on until ready / failed / cancelled / empty (completed without pack).
// Hitting this ceiling marks remaining runners as stalled — never silent empty.
// Overridable in tests.
var notePeriodBriefCollectorMaxWait = 2 * time.Hour

// When true (production default), CreateNotePeriodBrief returns after dispatching
// collectors and finishes synthesis in a background goroutine. Waiting inline
// caused Next.js rewrite proxies to abort with opaque HTTP 500.
// Tests set this false so the response includes the synthesizer job.
var notePeriodBriefFinishInBackground = true

// Stable English instruction locked to the period_brief playbook (J3-T1 / J3-T4).
// folderPageID is the private 工作介绍/ folder — Brief lands as a child via human confirm.
// draftPageID is the Facts+packs draft; synthesizer uses it for status / retry.
func notePeriodBriefInstruction(folderPageID, draftPageID, windowLabel string) string {
	folder := strings.TrimSpace(folderPageID)
	draft := strings.TrimSpace(draftPageID)
	label := strings.TrimSpace(windowLabel)
	if label == "" {
		label = "period"
	}
	retryHint := ""
	if draft != "" {
		retryHint = "\n10) Collector status board: each pack has status + retryable. " +
			"Permanent failures (missing API key / model config / auth / quota / blocked) → abandon that collector; do not retry. " +
			"Transient failures (runtime offline, network, capacity, empty pack, stalled) → retry with the narrow CLI below, at most " +
			fmt.Sprintf("%d", notePeriodBriefCollectorMaxRetries) + " retries per collector. " +
			"After retry, wait for the platform re-wake — do not invent OS work.\n" +
			formatPeriodBriefRetryHint(draft)
	}
	return "Write a Period Work Brief for **other people to read** (manager / colleague) — polished reporting narrative with clear structure. Not a pack dump, not a standup wrap-up, not slide-deck copy, not an engineering evidence log. Follow skill `multica-period-work-brief` for section shape, titles, and diagrams.\n" +
		"0) STRICT TIME WINDOW: narrate only work that falls inside the wake window (Facts timestamps + collector pack claims dated in that range). Do not pull in earlier/later history to \"complete the story\". If a pack mentions out-of-window commits, ignore them.\n" +
		"1) Fixed top-level sections (English headings): `## Summary`, then optionally `## Technique`, `## Achievements`, `## Research` — omit any of Technique/Achievements/Research entirely when Facts+packs have no related work (do not write empty placeholders).\n" +
		"2) `## Summary` is the overview and is always required. It has exactly two subsections: `### Work Summary` and `### Next Steps`.\n" +
		"3) `### Work Summary` is the priority section. **Start from collector ## Work groups.** Each Work group becomes one main titled thread; nest different work inside that group as nested sub-points / sub-bullets. Default trust: same-repo/project groups and cross-repo groups the collector marked related. Merge groups across collectors only when they share the same initiative identity. Never invent a merge of unrelated groups; never split one collector group by calendar. **Group by initiative identity / outcome / Issue — never by calendar order.** Unrelated initiatives must never share a sentence. Each thread: human title + 1–2 sentence claim + nested bullets about decisions, impact, and remaining risk — written so a non-author can understand. Optional Issue/PR identifiers only as human references (e.g. MUL-123), never as forensic proof.\n" +
		"4) **No evidence layer in the Brief.** Facts and collector packs are private source material only. Do not paste commit hashes, diffs, file snippets, `evidence:` labels, Runtime/Repos dumps, dirty-path lists, or wording like「证据」. Do not explain how you verified a claim.\n" +
		"5) `### Next Steps` may infer plausible follow-ups from current in-window work and unfinished threads; label speculation honestly; do not invent facts from empty/failed collectors.\n" +
		"6) Titles and body use reporting language (what changed / why it matters to others). Never use a filesystem path, repo folder, or package directory (`packages/…`, `/home/…`, a branch name alone) as a heading. Prefer zero paths in the Brief; if a product name needs grounding, use plain language, not a path bullet.\n" +
		"7) If a ready collector pack has a Mermaid diagram that clarifies a flow for the reader, copy that mermaid fence next to that work (tighten labels for readability). Do not drop useful diagrams; if packs overlap, keep the clearest one. Do not invent topology. Diagrams are for intuition, not for dumping graph evidence.\n" +
		"8) Call out delegated leverage (what agents or teammates carried) inside Work Summary when relevant — still in plain reporting language.\n" +
		"9) Deliver the Brief with `multica message send --target <Message target for chat transport> --note-write --note-page-id " + folder +
		"`. The body must be only the Brief markdown. Title it like `工作介绍 " + label +
		"`. The human confirms 「新建子笔记」 under 工作介绍/ — never treat the draft Facts page as the finished Brief, and never pass the draft page id to --note-page-id." +
		retryHint
}

// notePeriodBriefCollectorInstruction tells a runtime Agent to gather OS work
// into a structured pack stored on the Period Brief run (not a Notes page).
// draftPageID is the submit-pack / notes-get target.
func notePeriodBriefCollectorInstruction(draftPageID, windowLabel, windowStart, windowEnd string) string {
	draft := strings.TrimSpace(draftPageID)
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
		"OWN COMPUTER ONLY: harvest only this bound Computer. Do not collect from another member's laptop/cloud box.\n" +
		"STRICT TIME WINDOW: only include commits, file changes, and claims whose activity falls inside start→end from the wake `<window>` partition (RFC3339, half-open: include start, exclude end). Drop anything outside that range — do not widen to \"recent\" or \"this week\" on your own.\n" +
		"Follow the built-in skill `multica-period-work-collect` (read SKILL.md and `references/collect-recipes.md`) before collecting — use its shell recipes with the wake `$START` / `$END`.\n" +
		"Scope: whole-machine HOME for local runtimes; the cloud runtime environment for cloud. Prefer git status, commits in-window, dirty trees with in-window mtimes, and project dirs you can see.\n" +
		"PRELIMINARY GROUPING (required): after harvesting evidence, build `## Work groups`. Default: one group per git repo / project root. If work in different repos, files, or surfaces shares one outcome/initiative, put them in **one** group and state why. Unrelated work stays in separate groups — never glue by calendar. Completeness first — Highlights and Repos stay as the evidence layer; Work groups organize them. Every Highlight belongs to exactly one group; every group claim must be in-window.\n" +
		"When a multi-step flow needs it, add Mermaid diagrams under Diagrams.\n" +
		"Allowed detail: short diffs, file summaries, and key snippets when needed to explain work. Prefer bounded excerpts over whole files.\n" +
		"Forbidden: keymouse, screenshots, clipboard, browser history, full-repo dumps, secrets (.env / .ssh / keys / credentials), Host Digest APIs, and runtime diagnostics noise.\n" +
		"Denylist paths (skip): .ssh, .gnupg, .aws, .env / .env.*, credential stores, and similar secret roots.\n" +
		"Do NOT write the final Period Work Brief — that is the synthesizer's job.\n" +
		"Do NOT use `--note-write` for this pack — packs are ephemeral run artifacts, not Notes pages.\n" +
		"Pack markdown shape (required headings):\n" +
		"# 采集包 " + label + "\n" +
		"## Runtime\n" +
		"- mode / hostname or cloud env (best effort)\n" +
		"- window: start → end (copy from wake)\n" +
		"## Repos / roots\n" +
		"- path — short summary of what changed **in the window**\n" +
		"## Highlights\n" +
		"- claim with optional short diff or snippet (in-window only)\n" +
		"## Work groups\n" +
		"### <group title — project or related initiative>\n" +
		"- why: same repo/project | related outcome across …\n" +
		"- repos/paths: …\n" +
		"- items: nested bullets of in-window work in this group (cite Highlights)\n" +
		"## Diagrams\n" +
		"- optional Mermaid blocks when flow/dependency/state needs full local context; omit section if unused\n" +
		"## Unscoped / unclear\n" +
		"- leftover traces that do not map cleanly (still in-window if dated)\n" +
		"Deliver with `multica notes period-brief submit-pack --draft-page-id " + draft +
		"`. Body = pack markdown only (stdin or JSON `{\"markdown\":\"...\"}`)."
}

type createNotePeriodBriefRequest struct {
	Window            string   `json:"window"` // day | week | month | custom
	Date              string   `json:"date"`
	StartDate         string   `json:"start_date"` // custom: inclusive YYYY-MM-DD in timezone
	EndDate           string   `json:"end_date"`   // custom: inclusive YYYY-MM-DD in timezone
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
// gather OS work packs, waits until collectors settle (status-driven; stalled
// only at an absolute safety ceiling), then wakes the synthesizer with Facts +
// a collector status board. No Host Digest. No model runs here. In production
// the wait+synthesis runs in the background so the HTTP response is not killed
// by reverse-proxy timeouts.
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
	collectorIDs, ok := h.parsePeriodBriefCollectorAgentIDs(w, r.Context(), workspaceID, userID, req.CollectorAgentIDs)
	if !ok {
		return
	}

	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = h.resolveViewingTZ(r)
	}
	window, err := resolveNotePeriodBriefWindow(
		noteRetrospectiveWindowKind(strings.TrimSpace(req.Window)),
		req.Date, req.StartDate, req.EndDate, tz, time.Now(),
	)
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
	factsText := formatNotePeriodBriefFacts(bundle.Facts)
	windowInfo := noteRetrospectiveWindowResponse{
		Kind:     string(window.Kind),
		Timezone: window.Timezone,
		Start:    windowStart,
		End:      windowEnd,
		Label:    window.Label,
	}
	channelID := strings.TrimSpace(req.ChannelID)
	used := append([]string(nil), bundle.SourcesUsed...)
	empty := append([]string(nil), bundle.SourcesEmpty...)
	skipped := append([]string(nil), bundle.SourcesSkipped...)

	// Draft first so collectors can submit-pack onto the run without creating
	// Notes「采集包」pages.
	pendingPacks := make([]notePeriodBriefPackResult, len(collectorIDs))
	for i, id := range collectorIDs {
		pendingPacks[i] = notePeriodBriefPackResult{
			AgentID: id,
			Status:  "pending",
		}
	}
	packsText := formatNotePeriodBriefPacks(pendingPacks)
	title := fmt.Sprintf("工作介绍 %s · 底稿", window.Label)
	content := buildNotePeriodBriefDraftMarkdown(window, factsText, packsText, used, empty, skipped)
	content = strings.Replace(content, "> 这是合成底稿，不是给领导看的 Period Work Brief。Agent 应另写 Brief 页。\n",
		"> 采集员进行中：服务端会等采集完成后再唤醒整理 Agent。这是合成底稿，不是最终 Brief。\n", 1)
	draft, err := scanNotePage(h.DB.QueryRow(r.Context(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, folderID, userID, normalizeNoteTitle(title), content))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create period brief draft note")
		return
	}

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
			w, r, workspaceID, userID, userIDString, draft, collector, window.Label, windowStart, windowEnd,
		)
		if !ok {
			return
		}
		collectorJobs = append(collectorJobs, job)
	}

	refs := collectorRefsFromJobs(collectorJobs, window.Label, windowStart, windowEnd)
	if err := h.insertNotePeriodBriefRun(r.Context(), workspaceID, userID, draft.ID, folderID, agentID, window, channelID, factsText, used, empty, skipped, refs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create period brief run: "+err.Error())
		return
	}

	if notePeriodBriefFinishInBackground {
		primaryJob := collectorJobs[0]
		writeJSON(w, http.StatusCreated, createNotePeriodBriefResponse{
			Page:              notePageToResponse(draft, userID, []string{}, nil),
			Job:               primaryJob,
			Window:            windowInfo,
			SourcesUsed:       used,
			SourcesEmpty:      empty,
			SourcesSkipped:    skipped,
			FactCount:         bundle.FactCount(),
			CollectorAgentIDs: collectorIDs,
			CollectorJobs:     collectorJobs,
		})
		bg := context.WithoutCancel(r.Context())
		go h.finishNotePeriodBriefAfterCollectors(bg, workspaceID, userID, userIDString, agentID, folderID, draft, window, channelID, factsText, collectorJobs, used, empty, skipped)
		return
	}

	packResults := h.awaitPeriodBriefCollectorPacks(r.Context(), workspaceID, userID, draft.ID, collectorJobs)
	page, job, usedOut, emptyOut, skippedOut, ok := h.synthesizeNotePeriodBrief(
		w, r, workspaceID, userID, userIDString, agentID, folderID, draft, window, channelID, factsText, packResults, used, empty, skipped,
	)
	if !ok {
		return
	}
	if run, loadErr := h.loadNotePeriodBriefRunByDraft(r.Context(), workspaceID, page.ID); loadErr == nil {
		_ = h.updateNotePeriodBriefRunCollectors(r.Context(), run.ID, clearCollectorPackMarkdown(run.Collectors), "done")
	}
	writeJSON(w, http.StatusCreated, createNotePeriodBriefResponse{
		Page:              notePageToResponse(page, userID, []string{}, nil),
		Job:               job,
		Window:            windowInfo,
		SourcesUsed:       usedOut,
		SourcesEmpty:      emptyOut,
		SourcesSkipped:    skippedOut,
		FactCount:         bundle.FactCount(),
		CollectorAgentIDs: collectorIDs,
		CollectorJobs:     collectorJobs,
	})
}

func (h *Handler) finishNotePeriodBriefAfterCollectors(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	agentID, folderID pgtype.UUID,
	draft notePageRow,
	window noteRetrospectiveWindow,
	channelID, factsText string,
	collectorJobs []NoteWorkerJobResponse,
	used, empty, skipped []string,
) {
	// Wait until collectors settle; absolute ceiling only marks stalled.
	ctx, cancel := context.WithTimeout(ctx, notePeriodBriefCollectorMaxWait+time.Minute)
	defer cancel()

	packResults := h.awaitPeriodBriefCollectorPacks(ctx, workspaceID, userID, draft.ID, collectorJobs)
	if run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draft.ID); err == nil {
		for i := range packResults {
			if ref, _, ok := findCollectorRef(run.Collectors, packResults[i].AgentID); ok {
				packResults[i].RetryCount = ref.RetryCount
			}
		}
	}
	packsText := formatNotePeriodBriefPacks(packResults)
	packsReady := 0
	for _, pack := range packResults {
		if pack.Status == "ready" {
			packsReady++
		}
	}
	usedOut := append([]string(nil), used...)
	emptyOut := append([]string(nil), empty...)
	if packsReady > 0 {
		if !containsNoteRetrospectiveSource(usedOut, notePeriodBriefSourceCollectors) {
			usedOut = append(usedOut, notePeriodBriefSourceCollectors)
		}
		emptyOut = filterNoteRetrospectiveSource(emptyOut, notePeriodBriefSourceCollectors)
	} else if !containsNoteRetrospectiveSource(emptyOut, notePeriodBriefSourceCollectors) {
		emptyOut = append(emptyOut, notePeriodBriefSourceCollectors)
	}
	content := buildNotePeriodBriefDraftMarkdown(window, factsText, packsText, usedOut, emptyOut, skipped)
	_, _ = h.DB.Exec(ctx, `
UPDATE note_page SET content = $1, updated_at = now(), updated_by = $2 WHERE id = $3 AND workspace_id = $4`,
		content, userID, draft.ID, workspaceID)
	draft.Content = content

	if run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draft.ID); err == nil {
		_ = h.updateNotePeriodBriefRunCollectors(ctx, run.ID, run.Collectors, "synthesizing")
	}

	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		return
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/notes/period-briefs", nil)
	_, _ = h.dispatchNotePeriodBriefWorker(rec, req, workspaceID, userID, userIDString, folderID, draft, agent, window.Label, channelID, factsText, packsText)
	if run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draft.ID); err == nil {
		// Purge ephemeral pack artifacts once the synthesizer has been woken
		// with packsText in the prompt.
		_ = h.updateNotePeriodBriefRunCollectors(ctx, run.ID, clearCollectorPackMarkdown(run.Collectors), "done")
	}
}

func filterNoteRetrospectiveSource(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s == drop {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (h *Handler) synthesizeNotePeriodBrief(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	agentID, folderID pgtype.UUID,
	draft notePageRow,
	window noteRetrospectiveWindow,
	channelID, factsText string,
	packResults []notePeriodBriefPackResult,
	usedIn, emptyIn, skippedIn []string,
) (notePageRow, NoteWorkerJobResponse, []string, []string, []string, bool) {
	packsText := formatNotePeriodBriefPacks(packResults)
	used := append([]string(nil), usedIn...)
	empty := append([]string(nil), emptyIn...)
	skipped := append([]string(nil), skippedIn...)
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
		empty = filterNoteRetrospectiveSource(empty, notePeriodBriefSourceCollectors)
	} else if !containsNoteRetrospectiveSource(empty, notePeriodBriefSourceCollectors) {
		empty = append(empty, notePeriodBriefSourceCollectors)
	}

	content := buildNotePeriodBriefDraftMarkdown(window, factsText, packsText, used, empty, skipped)
	_, err := h.DB.Exec(r.Context(), `
UPDATE note_page SET content = $1, updated_at = now(), updated_by = $2 WHERE id = $3 AND workspace_id = $4`,
		content, userID, draft.ID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update period brief draft note")
		return notePageRow{}, NoteWorkerJobResponse{}, nil, nil, nil, false
	}
	draft.Content = content

	if run, loadErr := h.loadNotePeriodBriefRunByDraft(r.Context(), workspaceID, draft.ID); loadErr == nil {
		_ = h.updateNotePeriodBriefRunCollectors(r.Context(), run.ID, run.Collectors, "synthesizing")
	}

	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return notePageRow{}, NoteWorkerJobResponse{}, nil, nil, nil, false
	}
	job, ok := h.dispatchNotePeriodBriefWorker(w, r, workspaceID, userID, userIDString, folderID, draft, agent, window.Label, channelID, factsText, packsText)
	if !ok {
		return notePageRow{}, NoteWorkerJobResponse{}, nil, nil, nil, false
	}
	return draft, job, used, empty, skipped, true
}

// parsePeriodBriefCollectorAgentIDs requires at least one non-archived Period
// Work collector whose bound Computer is owned by the caller. Public runtimes
// and workspace admin role do not grant collection on someone else's machine.
func (h *Handler) parsePeriodBriefCollectorAgentIDs(
	w http.ResponseWriter,
	ctx context.Context,
	workspaceID, callerUserID pgtype.UUID,
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
		if !isPeriodBriefCollectorAgentName(agent.Name) {
			writeError(w, http.StatusBadRequest, "collector agent must be a Period Work collector: "+trimmed)
			return nil, false
		}
		if !agent.RuntimeID.Valid {
			writeError(w, http.StatusBadRequest, "collector agent has no bound computer: "+trimmed)
			return nil, false
		}
		rt, err := h.Queries.GetAgentRuntime(ctx, agent.RuntimeID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "collector agent runtime not found: "+trimmed)
			return nil, false
		}
		if uuidToString(rt.WorkspaceID) != uuidToString(workspaceID) {
			writeError(w, http.StatusBadRequest, "collector agent runtime not in workspace: "+trimmed)
			return nil, false
		}
		rtOwnerID, ownerErr := h.resolveRuntimeOwnerQuery(ctx, rt)
		if ownerErr != nil || uuidToString(rtOwnerID) != uuidToString(callerUserID) {
			writeError(w, http.StatusBadRequest, "collector agent must be on a computer you own: "+trimmed)
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
	draft notePageRow,
	agent db.Agent,
	windowLabel, windowStart, windowEnd string,
) (NoteWorkerJobResponse, bool) {
	ch, ok := h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, "")
	if !ok {
		return NoteWorkerJobResponse{}, false
	}

	draftPageID := uuidToString(draft.ID)
	instruction := notePeriodBriefCollectorInstruction(draftPageID, windowLabel, windowStart, windowEnd)

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, workspaceID, draft.ID, userID, agent.ID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collector Worker job")
		return NoteWorkerJobResponse{}, false
	}

	visibleContent, parts, err := h.buildNoteWorkerChannelMessage(r.Context(), ch, agent, draft, instruction)
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
			instruction, draftPageID, windowLabel, windowStart, windowEnd, draft.Title, "",
		),
		h.agentMessageThreadTargetForPrompt(r.Context(), ch, msg),
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
		PageID:  draftPageID,
		Title:   draft.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach collector note brief")
		return NoteWorkerJobResponse{}, false
	}
	if err := h.persistPeriodBriefNoteBriefContext(r.Context(), task.ID, mergedContext); err != nil {
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
	instruction := notePeriodBriefInstruction(folderPageID, uuidToString(page.ID), windowLabel)

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
		h.agentMessageThreadTargetForPrompt(r.Context(), ch, msg),
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
	if err := h.persistPeriodBriefNoteBriefContext(r.Context(), task.ID, mergedContext); err != nil {
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

type notePeriodBriefPackResult struct {
	AgentID     string
	PageID      string
	Title       string
	Content     string
	Status      string // ready | empty | failed | cancelled | stalled | running
	Retryable   bool
	AbandonWhy  string
	Detail      string
	FailureKind string
	RetryCount  int
}

// awaitPeriodBriefCollectorPacks waits until each collector settles (ready /
// failed / cancelled / empty). Ready packs come from run.collectors[].pack_markdown
// (submit-pack). Hitting notePeriodBriefCollectorMaxWait marks still-running
// collectors as stalled — never silent empty.
func (h *Handler) awaitPeriodBriefCollectorPacks(
	ctx context.Context,
	workspaceID, userID, draftPageID pgtype.UUID,
	jobs []NoteWorkerJobResponse,
) []notePeriodBriefPackResult {
	out := make([]notePeriodBriefPackResult, len(jobs))
	for i, job := range jobs {
		out[i] = notePeriodBriefPackResult{
			AgentID: job.AgentID,
			PageID:  uuidToString(draftPageID),
			Status:  "running",
		}
	}
	if len(jobs) == 0 {
		return out
	}
	deadline := time.Now().Add(notePeriodBriefCollectorMaxWait)
	for {
		pastCeiling := time.Now().After(deadline)
		allSettled := true
		run, runErr := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draftPageID)
		for i, job := range jobs {
			if isPeriodBriefPackSettled(out[i].Status) {
				continue
			}
			out[i].Content = ""
			out[i].Title = ""

			projected, _ := h.noteWorkerJobResponse(ctx, workspaceID, userID, parseUUID(job.ID))
			status := projected.Status
			if status == "" {
				status = job.Status
			}
			failReason := ""
			if projected.FailureReason != nil {
				failReason = strings.TrimSpace(*projected.FailureReason)
			}
			packReady := false
			if runErr == nil {
				if ref, _, ok := findCollectorRef(run.Collectors, job.AgentID); ok {
					if md := strings.TrimSpace(ref.PackMarkdown); md != "" {
						out[i].Content = md
						out[i].Title = normalizeNoteTitle("采集包 " + ref.WindowLabel)
						packReady = true
					}
				}
			}

			timedOut := pastCeiling && !packReady &&
				(status == "" || status == "pending" || status == "dispatched" || status == "running")
			d := classifyPeriodBriefCollectorOutcome(status, failReason, failReason, packReady, timedOut)
			out[i].Status = d.Status
			out[i].Retryable = d.Retryable
			out[i].AbandonWhy = d.AbandonWhy
			out[i].Detail = d.Detail
			out[i].FailureKind = d.FailureKind
			if !isPeriodBriefPackSettled(out[i].Status) {
				allSettled = false
			}
		}
		if allSettled || pastCeiling {
			if pastCeiling {
				for i := range out {
					if out[i].Status == "running" || out[i].Status == "pending" {
						d := classifyPeriodBriefCollectorOutcome("running", out[i].Detail, out[i].Detail, false, true)
						out[i].Status = d.Status
						out[i].Retryable = d.Retryable
						out[i].AbandonWhy = d.AbandonWhy
						out[i].Detail = d.Detail
						out[i].FailureKind = d.FailureKind
					}
				}
			}
			break
		}
		timer := time.NewTimer(notePeriodBriefCollectorPollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			for i := range out {
				if !isPeriodBriefPackSettled(out[i].Status) {
					d := classifyPeriodBriefCollectorOutcome("running", out[i].Detail, "wait cancelled", false, true)
					out[i].Status = d.Status
					out[i].Retryable = d.Retryable
					out[i].AbandonWhy = d.AbandonWhy
					out[i].Detail = d.Detail
					out[i].FailureKind = d.FailureKind
				}
			}
			return out
		case <-timer.C:
		}
	}
	return out
}

// persistPeriodBriefNoteBriefContext writes the Note Worker brief onto the
// inbox event and marks the wake force_fresh_session. Period Brief collect /
// synth / retry are one-shot prompts — they must not resume a poisoned Pi
// conversation from a prior turn (input[n].status 400).
func (h *Handler) persistPeriodBriefNoteBriefContext(ctx context.Context, taskID pgtype.UUID, mergedContext []byte) error {
	_, err := h.DB.Exec(ctx, `
UPDATE agent_inbox_event
SET context = $1::jsonb, force_fresh_session = true, updated_at = now()
WHERE id = $2`, mergedContext, taskID)
	return err
}

func isPeriodBriefPackSettled(status string) bool {
	switch status {
	case "ready", "empty", "failed", "cancelled", "stalled":
		return true
	default:
		return false
	}
}

// loadCollectorPackNoteWriteProposal returns the latest agent message body that
// proposed --note-write onto the pack page. Used when the pack page is still
// empty (job running, completed, or failed after write) so synthesis does not
// require the human to accept the writeback into note_page first.
func (h *Handler) loadCollectorPackNoteWriteProposal(ctx context.Context, channelID, packPageID string) string {
	channelID = strings.TrimSpace(channelID)
	packPageID = strings.TrimSpace(packPageID)
	if channelID == "" || packPageID == "" {
		return ""
	}
	var content string
	err := h.DB.QueryRow(ctx, `
SELECT m.content
FROM channel_message m
WHERE m.channel_id = $1
  AND m.deleted_at IS NULL
  AND m.author_type = 'agent'
  AND length(trim(m.content)) > 0
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) part
    WHERE part->>'type' = 'note_write'
      AND part->>'ref_id' = $2
  )
ORDER BY m.created_at DESC
LIMIT 1`, parseUUID(channelID), packPageID).Scan(&content)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

func formatNotePeriodBriefPacks(packs []notePeriodBriefPackResult) string {
	var b strings.Builder
	b.WriteString("## Collector packs\n")
	b.WriteString("Platform waited until each collector settled (ready/failed/cancelled/empty/stalled).\n")
	b.WriteString("retryable=true → you may call the narrow retry CLI (max retries enforced server-side).\n")
	b.WriteString("retryable=false permanent → abandon that collector; do not invent its OS work.\n")
	if len(packs) == 0 {
		b.WriteString("status: empty\n(no collectors)\n")
		return b.String()
	}
	for _, pack := range packs {
		fmt.Fprintf(&b, "\n### Collector %s\n", pack.AgentID)
		fmt.Fprintf(&b, "draft_page_id: %s\n", pack.PageID)
		fmt.Fprintf(&b, "status: %s\n", pack.Status)
		fmt.Fprintf(&b, "retryable: %t\n", pack.Retryable)
		fmt.Fprintf(&b, "retry_count: %d / max %d\n", pack.RetryCount, notePeriodBriefCollectorMaxRetries)
		if pack.FailureKind != "" {
			fmt.Fprintf(&b, "failure_kind: %s\n", pack.FailureKind)
		}
		if pack.AbandonWhy != "" {
			fmt.Fprintf(&b, "abandon_why: %s\n", pack.AbandonWhy)
		}
		if pack.Detail != "" {
			fmt.Fprintf(&b, "detail: %s\n", pack.Detail)
		}
		if pack.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", pack.Title)
		}
		switch pack.Status {
		case "ready":
			b.WriteString(strings.TrimSpace(pack.Content))
			b.WriteByte('\n')
		case "failed":
			b.WriteString("调用采集 Agent 失败了 — do not treat this collector as Brief evidence; do not invent OS work.\n")
			if pack.Retryable {
				b.WriteString("(retryable — consider narrow retry)\n")
			} else {
				b.WriteString("(permanent — abandon this collector)\n")
			}
		case "cancelled":
			b.WriteString("调用采集 Agent 失败了（已取消）— abandon; do not invent OS work.\n")
		case "stalled":
			b.WriteString("调用采集 Agent 失败了（超时未交付）— do not invent OS work.\n")
		case "running", "pending":
			b.WriteString("(still running — platform should not have woken you yet)\n")
		default:
			b.WriteString("调用采集 Agent 失败了（未交付采集包）— retryable; do not invent OS work.\n")
		}
	}
	return b.String()
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
