package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issueguard"
	"github.com/multica-ai/multica/server/internal/issueposition"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// IssueService is the single service-layer entry point for creating issues.
// Both the HTTP `POST /issues` handler and the future Lark `/issue` command
// call into Create so that duplicate guard, issue numbering, attachment
// linking, broadcast, analytics, and agent enqueue stay aligned. The
// service deliberately does NOT depend on http.Request — callers parse
// their own transport and pass a fully-resolved IssueCreateParams.
type IssueService struct {
	Queries   *db.Queries
	TxStarter TxStarter
	Bus       *events.Bus
	Analytics analytics.Client
	// Metrics is the shared business-metrics collector. Wired by
	// cmd/server/router.go after construction; nil in tests / self-hosted
	// without the metrics listener — obsmetrics.RecordEvent treats a nil
	// Metrics as "PostHog only", so leaving it unset is safe.
	Metrics     *obsmetrics.BusinessMetrics
	TaskService *TaskService
}

func NewIssueService(q *db.Queries, tx TxStarter, bus *events.Bus, ac analytics.Client, ts *TaskService) *IssueService {
	return &IssueService{
		Queries:     q,
		TxStarter:   tx,
		Bus:         bus,
		Analytics:   ac,
		TaskService: ts,
	}
}

// IssueCreateParams carries the already-validated, already-resolved inputs
// to IssueService.Create. The handler owns the parsing step that turns its
// request payload into this struct; the service stays transport-agnostic.
type IssueCreateParams struct {
	WorkspaceID        pgtype.UUID
	Title              string
	Description        pgtype.Text
	Status             string
	Priority           string
	AssigneeType       pgtype.Text
	AssigneeID         pgtype.UUID
	CreatorType        string // "agent" or "member"
	CreatorID          pgtype.UUID
	ParentIssueID      pgtype.UUID
	ProjectID          pgtype.UUID
	StartDate          pgtype.Date
	DueDate            pgtype.Date
	OriginType         pgtype.Text
	OriginID           pgtype.UUID
	SourceChannelID    pgtype.UUID
	SourceMessageID    pgtype.UUID
	AcceptanceCriteria []string
	AttachmentIDs      []pgtype.UUID
	AllowDuplicate     bool
}

// IssueCreateOpts groups optional knobs for IssueService.Create. Most
// callers leave it zero-valued.
type IssueCreateOpts struct {
	// BroadcastPayload, if non-nil, is invoked after the issue row is
	// created and attachments are linked. Its return value is sent as
	// the EventIssueCreated payload via the event bus. The HTTP handler
	// uses this hook to inject its IssueResponse without forcing this
	// package to depend on handler-layer types. If nil, the service
	// emits a minimal `{"issue_id": <uuid>}` payload — enough for cache
	// invalidation, but front-ends that expect the full response shape
	// must provide BroadcastPayload.
	BroadcastPayload func(issue db.Issue, attachments []db.Attachment) map[string]any

	// ActorID overrides the actor ID used for broadcast + analytics
	// when it differs from the creator on the row. Agent-created issues
	// use the agent UUID here (the creator_id column is the daemon
	// owner). Empty falls back to CreatorID.
	ActorID string

	// AnalyticsAgentID is the agent associated with the issue for
	// analytics purposes (assignee agent or, for agent-created issues,
	// the creator agent). Resolved by the caller because it depends on
	// transport context.
	AnalyticsAgentID string

	// Platform tags the IssueCreated analytics + business-metrics event
	// with the client surface the request came in on (web / desktop /
	// daemon / lark / autopilot). Derived from middleware's client
	// metadata at the handler layer.
	Platform string
}

// ErrActiveDuplicate signals that the duplicate guard found an active
// issue with the same (workspace, project, parent, title) tuple and
// AllowDuplicate was false. The IssueCreateResult.DuplicateIssue field is
// populated when this error is returned so callers can render the
// conflict (HTTP 409, Lark card, etc.).
var ErrActiveDuplicate = errors.New("active duplicate issue exists")

// ErrParentIssueNotFound signals that the supplied ParentIssueID does
// not exist in the issue's workspace. The service refuses to create
// orphaned or cross-workspace child issues; callers translate this into
// their transport's 400 / Lark card error.
var ErrParentIssueNotFound = errors.New("parent issue not found in this workspace")

// ErrProjectNotFound signals that the supplied ProjectID does not exist
// in the issue's workspace. Cross-workspace project IDs are rejected
// here so every create entry (HTTP `POST /issues`, Lark `/issue`, future
// MCP / API key callers) enforces the same workspace boundary without
// having to remember it. Callers translate this into 400.
var ErrProjectNotFound = errors.New("project not found in this workspace")

// ErrAttachmentNotFound signals that at least one requested attachment is
// absent or belongs to another workspace. Already-bound rows are cloned onto
// the new issue (LRM-731) so carrier sub-issues cannot block main-issue
// reference material. Issue creation is rejected atomically instead of
// succeeding without the caller's reference material.
var ErrAttachmentNotFound = errors.New("attachment not available for issue creation")

// IssueCreateResult is the typed return from IssueService.Create.
//
//   - On the happy path: Issue is the new row, Attachments lists the
//     linked attachments (may be empty), DuplicateIssue is nil.
//   - On ErrActiveDuplicate: DuplicateIssue is the row that blocked the
//     create; Issue and Attachments are zero.
type IssueCreateResult struct {
	Issue          db.Issue
	Attachments    []db.Attachment
	DuplicateIssue *db.Issue
}

// Create runs the full issue-creation pipeline atomically end-to-end:
//
//  1. Begin transaction.
//  2. Lock and validate requested attachments in the workspace.
//  3. Resolve & validate parent / project belong to the same workspace.
//  4. Lock & check the duplicate guard.
//  5. Increment the workspace issue counter.
//  6. Insert the issue row, acceptance criteria, source anchor, and attachments.
//  7. Commit.
//  8. Publish EventIssueCreated to the bus (payload via opts.BroadcastPayload).
//  9. Capture the IssueCreated analytics event.
//  10. Enqueue an agent task when the issue is assigned and not in `backlog`.
//
// Validation that lives in the service (parent existence, project
// workspace membership, parent → project back-fill) is enforced here so
// every create entry — HTTP `POST /issues`, Lark `/issue`, future
// MCP/API-key callers — shares the same workspace boundary semantics.
// Caller-owned validation is limited to transport-shaped checks: title
// required, RFC3339 date format, assignee pair sanity.
func (s *IssueService) Create(ctx context.Context, p IssueCreateParams, opts IssueCreateOpts) (IssueCreateResult, error) {
	acceptanceCriteria := p.AcceptanceCriteria
	if acceptanceCriteria == nil {
		acceptanceCriteria = []string{}
	}
	acceptanceCriteriaJSON, err := json.Marshal(acceptanceCriteria)
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("marshal acceptance criteria: %w", err)
	}
	for _, id := range p.AttachmentIDs {
		if !id.Valid {
			return IssueCreateResult{}, ErrAttachmentNotFound
		}
	}
	attachmentIDs := uniqueUUIDs(p.AttachmentIDs)

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)
	var unboundAttachmentIDs []pgtype.UUID
	var cloneAttachmentIDs []pgtype.UUID
	if len(attachmentIDs) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT id, issue_id
			FROM attachment
			WHERE workspace_id = $1
			  AND id = ANY($2::uuid[])
			FOR UPDATE
		`, p.WorkspaceID, attachmentIDs)
		if err != nil {
			return IssueCreateResult{}, fmt.Errorf("lock issue attachments: %w", err)
		}
		found := make(map[string]struct{}, len(attachmentIDs))
		for rows.Next() {
			var id, issueID pgtype.UUID
			if err := rows.Scan(&id, &issueID); err != nil {
				rows.Close()
				return IssueCreateResult{}, fmt.Errorf("scan issue attachment: %w", err)
			}
			found[util.UUIDToString(id)] = struct{}{}
			if issueID.Valid {
				cloneAttachmentIDs = append(cloneAttachmentIDs, id)
			} else {
				unboundAttachmentIDs = append(unboundAttachmentIDs, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return IssueCreateResult{}, fmt.Errorf("list issue attachments: %w", err)
		}
		if len(found) != len(attachmentIDs) {
			return IssueCreateResult{}, ErrAttachmentNotFound
		}
	}

	// Resolve and validate parent / project before reading from the
	// duplicate guard so a forged parent or project ID is rejected
	// before we touch the issue counter. Both checks scope by
	// WorkspaceID — there is no path from this service to a row in a
	// foreign workspace.
	projectID := p.ProjectID
	if p.ParentIssueID.Valid {
		parent, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID:          p.ParentIssueID,
			WorkspaceID: p.WorkspaceID,
		})
		if err != nil || !parent.ID.Valid {
			return IssueCreateResult{}, ErrParentIssueNotFound
		}
		// Back-fill project from parent when the caller did not pin
		// one explicitly. Matches the long-standing HTTP behavior: a
		// sub-issue inherits its parent's project unless overridden.
		if !projectID.Valid {
			projectID = parent.ProjectID
		}
	}
	if projectID.Valid {
		if _, err := qtx.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          projectID,
			WorkspaceID: p.WorkspaceID,
		}); err != nil {
			return IssueCreateResult{}, ErrProjectNotFound
		}
	}

	duplicate, found, err := issueguard.LockAndFindActiveDuplicate(ctx, qtx, p.WorkspaceID, projectID, p.ParentIssueID, p.Title, p.AllowDuplicate)
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("duplicate guard: %w", err)
	}
	if found {
		dup := duplicate
		return IssueCreateResult{DuplicateIssue: &dup}, ErrActiveDuplicate
	}

	issueNumber, err := qtx.IncrementIssueCounter(ctx, p.WorkspaceID)
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("increment counter: %w", err)
	}

	// New issues sort to the top of their (workspace, status) column for
	// manual ordering. Computed inside the tx, after IncrementIssueCounter
	// has already taken the workspace row lock, so two concurrent creates
	// in the same workspace see each other's positions and don't both
	// land on the same min-1 slot. Concurrent manual reorder via
	// UpdateIssue(position) does NOT take this lock, so a create racing
	// a reorder is still allowed to collide on position — manual ordering
	// is best-effort and the UI tolerates equal positions by falling back
	// to the secondary ORDER BY key.
	newPosition, err := issueposition.NextTopPosition(ctx, tx, p.WorkspaceID, p.Status)
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("next top position: %w", err)
	}

	var issue db.Issue
	if p.OriginType.Valid {
		issue, err = qtx.CreateIssueWithOrigin(ctx, db.CreateIssueWithOriginParams{
			WorkspaceID:        p.WorkspaceID,
			Title:              p.Title,
			Description:        p.Description,
			Status:             p.Status,
			Priority:           p.Priority,
			AssigneeType:       p.AssigneeType,
			AssigneeID:         p.AssigneeID,
			CreatorType:        p.CreatorType,
			CreatorID:          p.CreatorID,
			ParentIssueID:      p.ParentIssueID,
			Position:           newPosition,
			StartDate:          p.StartDate,
			DueDate:            p.DueDate,
			Number:             issueNumber,
			ProjectID:          projectID,
			OriginType:         p.OriginType,
			OriginID:           p.OriginID,
			AcceptanceCriteria: acceptanceCriteriaJSON,
		})
	} else {
		issue, err = qtx.CreateIssue(ctx, db.CreateIssueParams{
			WorkspaceID:        p.WorkspaceID,
			Title:              p.Title,
			Description:        p.Description,
			Status:             p.Status,
			Priority:           p.Priority,
			AssigneeType:       p.AssigneeType,
			AssigneeID:         p.AssigneeID,
			CreatorType:        p.CreatorType,
			CreatorID:          p.CreatorID,
			ParentIssueID:      p.ParentIssueID,
			Position:           newPosition,
			StartDate:          p.StartDate,
			DueDate:            p.DueDate,
			Number:             issueNumber,
			ProjectID:          projectID,
			AcceptanceCriteria: acceptanceCriteriaJSON,
		})
	}
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("create issue: %w", err)
	}
	if p.SourceChannelID.Valid {
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, $4)`, issue.ID, p.WorkspaceID, p.SourceChannelID, p.SourceMessageID); err != nil {
			return IssueCreateResult{}, fmt.Errorf("persist issue source message: %w", err)
		}
	}
	if len(unboundAttachmentIDs) > 0 {
		if err := qtx.LinkAttachmentsToIssue(ctx, db.LinkAttachmentsToIssueParams{
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
			Column3:     unboundAttachmentIDs,
		}); err != nil {
			return IssueCreateResult{}, fmt.Errorf("link issue attachments: %w", err)
		}
	}
	for _, srcID := range cloneAttachmentIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachment (
			  workspace_id, issue_id, comment_id, chat_session_id, channel_id,
			  uploader_type, uploader_id, filename, url, content_type, size_bytes
			)
			SELECT
			  workspace_id, $1, NULL, NULL, channel_id,
			  uploader_type, uploader_id, filename, url, content_type, size_bytes
			FROM attachment
			WHERE id = $2 AND workspace_id = $3`,
			issue.ID, srcID, issue.WorkspaceID,
		); err != nil {
			return IssueCreateResult{}, fmt.Errorf("clone issue attachment: %w", err)
		}
	}
	attachments, err := qtx.ListAttachmentsByIssue(ctx, db.ListAttachmentsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return IssueCreateResult{}, fmt.Errorf("list linked issue attachments: %w", err)
	}
	if len(attachmentIDs) > 0 && len(attachments) < len(attachmentIDs) {
		return IssueCreateResult{}, fmt.Errorf("link issue attachments: expected %d bound, got %d", len(attachmentIDs), len(attachments))
	}

	if err := tx.Commit(ctx); err != nil {
		return IssueCreateResult{}, fmt.Errorf("commit: %w", err)
	}

	actorID := opts.ActorID
	if actorID == "" {
		actorID = util.UUIDToString(issue.CreatorID)
	}

	s.publishIssueCreated(issue, attachments, p.CreatorType, actorID, opts)
	s.captureCreatedAnalytics(issue, p.CreatorType, actorID, opts)
	s.maybeEnqueueOnAssign(ctx, issue, p.CreatorType, actorID)

	return IssueCreateResult{Issue: issue, Attachments: attachments}, nil
}

func uniqueUUIDs(ids []pgtype.UUID) []pgtype.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		key := util.UUIDToString(id)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *IssueService) publishIssueCreated(issue db.Issue, attachments []db.Attachment, creatorType, actorID string, opts IssueCreateOpts) {
	if s.Bus == nil {
		return
	}
	var payload map[string]any
	if opts.BroadcastPayload != nil {
		payload = opts.BroadcastPayload(issue, attachments)
	} else {
		// Minimal fallback so cache invalidations still fire even if the
		// caller forgot to supply a builder. Front-ends that expect the
		// full IssueResponse must pass BroadcastPayload.
		payload = map[string]any{"issue_id": util.UUIDToString(issue.ID)}
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   creatorType,
		ActorID:     actorID,
		Payload:     payload,
	})
}

func (s *IssueService) captureCreatedAnalytics(issue db.Issue, creatorType, actorID string, opts IssueCreateOpts) {
	if s.Analytics == nil {
		return
	}
	source, taskID, autopilotRunID := classifyOrigin(issue, opts)
	analyticsActorID := actorID
	if creatorType == "agent" {
		analyticsActorID = "agent:" + actorID
	}
	obsmetrics.RecordEvent(s.Analytics, s.Metrics, analytics.IssueCreated(
		analyticsActorID,
		util.UUIDToString(issue.WorkspaceID),
		util.UUIDToString(issue.ID),
		opts.AnalyticsAgentID,
		taskID,
		autopilotRunID,
		source,
		opts.Platform,
	))
}

// classifyOrigin maps the issue's origin_type / origin_id columns into the
// analytics source labels. Unknown origin_type falls back to SourceManual
// with the warning logged — analytics drift is preferable to dropping the
// event entirely.
func classifyOrigin(issue db.Issue, opts IssueCreateOpts) (source, taskID, autopilotRunID string) {
	source = analytics.SourceManual
	if !issue.OriginType.Valid {
		return source, "", ""
	}
	originID := util.UUIDToString(issue.OriginID)
	switch issue.OriginType.String {
	case "quick_create":
		return analytics.SourceManual, originID, ""
	case "autopilot":
		return analytics.SourceAutopilot, "", originID
	default:
		slog.Warn("analytics: unknown issue origin type",
			"origin_type", issue.OriginType.String,
			"issue_id", util.UUIDToString(issue.ID),
		)
		return analytics.SourceManual, "", ""
	}
}

func (s *IssueService) maybeEnqueueOnAssign(ctx context.Context, issue db.Issue, creatorType, actorID string) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return
	}
	if s.shouldEnqueueAgentTask(ctx, issue) {
		if _, err := s.TaskService.EnqueueTaskForIssue(ctx, issue); err != nil {
			slog.Warn("enqueue agent task on create failed",
				"issue_id", util.UUIDToString(issue.ID),
				"error", err)
		}
	}
}

// shouldEnqueueAgentTask returns true when an issue create or assignment
// should trigger the assigned agent. Backlog issues are skipped — backlog
// acts as a parking lot for pre-assigning without immediate execution.
// Mirrors handler.shouldEnqueueAgentTask; kept here to make the service
// self-contained, since both code paths must move together.
func (s *IssueService) shouldEnqueueAgentTask(ctx context.Context, issue db.Issue) bool {
	if issue.Status == "backlog" {
		return false
	}
	return s.isAgentAssigneeReady(ctx, issue)
}

func (s *IssueService) isAgentAssigneeReady(ctx context.Context, issue db.Issue) bool {
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return false
	}
	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid {
		return false
	}
	return true
}
