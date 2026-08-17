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
	notePeriodBriefFolderTitle   = "工作介绍"
	notePeriodBriefSourceJournal = "machine_work_journal"
	// Keep the HTTP path snappy: Next.js proxies abort long requests as
	// socket hang up / opaque 500. Digests degrade into sources_empty.
	notePeriodBriefDigestTimeout  = 4 * time.Second
	notePeriodBriefDigestBudget   = 8 * time.Second
)

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
		"5) Put unscoped local machine work (本机未归类) in its own section — never mix it into the team narrative.\n" +
		"6) Do not list raw commits; do not invent claims without evidence.\n" +
		"7) Deliver the Brief with `multica message send --target <Message target for chat transport> --note-write --note-page-id " + folder +
		"`. The body must be only the Brief markdown. Title it like `工作介绍 " + label +
		"`. The human confirms 「新建子笔记」 under 工作介绍/ — never treat the draft Facts page as the finished Brief, and never pass the draft page id to --note-page-id."
}

type createNotePeriodBriefRequest struct {
	Window    string   `json:"window"` // day | week | month
	Date      string   `json:"date"`
	Timezone  string   `json:"timezone"`
	AgentID   string   `json:"agent_id"`
	Sources   []string `json:"sources"`
	ChannelID string   `json:"channel_id"`
}

type createNotePeriodBriefResponse struct {
	Page           NotePageResponse                `json:"page"`
	Job            NoteWorkerJobResponse           `json:"job"`
	Window         noteRetrospectiveWindowResponse `json:"window"`
	SourcesUsed    []string                        `json:"sources_used"`
	SourcesEmpty   []string                        `json:"sources_empty"`
	SourcesSkipped []string                        `json:"sources_skipped"`
	FactCount      int                             `json:"fact_count"`
}

// CreateNotePeriodBrief gathers platform Facts + Owner Work Digests for a
// window, writes a private draft note, and dispatches a Worker job with the
// period_brief instruction. Digest failures/disabled states degrade into
// sources_empty — they do not fail the whole request. No model runs here.
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
	ownedComputers, err := h.listOwnedComputerIDsInWorkspace(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Computers")
		return
	}
	if len(ownedComputers) == 0 {
		writeError(w, http.StatusForbidden, "computer owner required")
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

	workspaceRemotes, err := h.listWorkspaceGitRepoRemotes(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workspace git remotes")
		return
	}
	digestPacks, journalEmpty := h.collectOwnerWorkDigests(r.Context(), ownedComputers, window, workspaceRemotes)
	used := append([]string(nil), bundle.SourcesUsed...)
	empty := append([]string(nil), bundle.SourcesEmpty...)
	skipped := append([]string(nil), bundle.SourcesSkipped...)
	if journalEmpty {
		if !containsNoteRetrospectiveSource(empty, notePeriodBriefSourceJournal) {
			empty = append(empty, notePeriodBriefSourceJournal)
		}
	} else if !containsNoteRetrospectiveSource(used, notePeriodBriefSourceJournal) {
		used = append(used, notePeriodBriefSourceJournal)
	}

	factsText := formatNotePeriodBriefFacts(bundle.Facts)
	digestText := formatNotePeriodBriefDigests(digestPacks)
	title := fmt.Sprintf("工作介绍 %s · 底稿", window.Label)
	content := buildNotePeriodBriefDraftMarkdown(window, factsText, digestText, used, empty, skipped)

	folderID, err := h.ensureNotePeriodBriefFolder(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure period brief folder")
		return
	}
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
	job, ok := h.dispatchNotePeriodBriefWorker(w, r, workspaceID, userID, userIDString, folderID, page, agent, window.Label, strings.TrimSpace(req.ChannelID), factsText, digestText)
	if !ok {
		return
	}

	writeJSON(w, http.StatusCreated, createNotePeriodBriefResponse{
		Page: notePageToResponse(page, userID, []string{}, nil),
		Job:  job,
		Window: noteRetrospectiveWindowResponse{
			Kind:     string(window.Kind),
			Timezone: window.Timezone,
			Start:    window.Start.UTC().Format(time.RFC3339),
			End:      window.End.UTC().Format(time.RFC3339),
			Label:    window.Label,
		},
		SourcesUsed:    used,
		SourcesEmpty:   empty,
		SourcesSkipped: skipped,
		FactCount:      bundle.FactCount(),
	})
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

func (h *Handler) dispatchNotePeriodBriefWorker(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	folderID pgtype.UUID,
	page notePageRow,
	agent db.Agent,
	windowLabel, channelID, factsText, digestText string,
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
		buildNotePeriodBriefPrompt(instruction, uuidToString(page.ID), folderPageID, windowLabel, page.Title, page.Content, factsText, digestText),
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildNotePeriodBriefDraftMarkdown(
	window noteRetrospectiveWindow,
	factsText, digestText string,
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
	b.WriteString(digestText)
	return b.String()
}
