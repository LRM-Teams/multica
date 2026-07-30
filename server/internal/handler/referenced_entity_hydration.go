package handler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/promptcontext"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type referencedEntitySource struct {
	Content string
	Parts   []protocol.MessagePart
}

type referencedEntityHydration struct {
	Snapshots    []protocol.ReferencedEntitySnapshot
	OmittedCount int
}

// hydrateReferencedEntities resolves canonical mention links into bounded
// read-only prompt data. Every lookup is scoped to the task workspace and the
// actual initiating actor; malformed, foreign, archived, or inaccessible
// references are omitted rather than partially disclosed.
func (h *Handler) hydrateReferencedEntities(
	ctx context.Context,
	workspaceID, actorType, actorID string,
	sources ...referencedEntitySource,
) referencedEntityHydration {
	if h == nil || h.Queries == nil {
		return referencedEntityHydration{}
	}
	if actorType == "user" {
		actorType = "member"
	}
	if !h.isWorkspaceEntity(ctx, actorType, actorID, workspaceID) {
		return referencedEntityHydration{}
	}

	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return referencedEntityHydration{}
	}
	workspace, err := h.Queries.GetWorkspace(ctx, workspaceUUID)
	if err != nil {
		return referencedEntityHydration{}
	}
	prefix := strings.TrimSpace(workspace.IssuePrefix)
	if prefix == "" {
		prefix = generateIssuePrefix(workspace.Name)
	}

	var result referencedEntityHydration
	seen := make(map[string]struct{})
	for _, source := range sources {
		for _, mention := range referencedEntityMentions(source) {
			if mention.Type != "issue" && mention.Type != "agent" {
				continue
			}
			key := mention.Type + ":" + mention.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			entityID, err := util.ParseUUID(mention.ID)
			if err != nil {
				continue
			}
			if len(result.Snapshots) >= promptcontext.MaxReferencedEntities {
				// The reference count is already visible in the source body.
				// Do not perform any more entity lookups after the prompt cap.
				result.OmittedCount++
				continue
			}

			var (
				snapshot protocol.ReferencedEntitySnapshot
				ok       bool
			)
			switch mention.Type {
			case "issue":
				snapshot, ok = h.hydrateIssueReference(ctx, workspaceUUID, workspaceID, actorType, actorID, prefix, entityID)
			case "agent":
				snapshot, ok = h.hydrateAgentReference(ctx, workspaceUUID, entityID)
			}
			if !ok {
				continue
			}
			result.Snapshots = append(result.Snapshots, snapshot)
		}
	}
	return result
}

func referencedEntityMentions(source referencedEntitySource) []util.Mention {
	var mentions []util.Mention
	seen := make(map[string]struct{})
	appendMention := func(mention util.Mention) {
		if mention.Type != "issue" && mention.Type != "agent" {
			return
		}
		key := mention.Type + ":" + mention.ID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		mentions = append(mentions, mention)
	}

	// Structured parts are the canonical channel/chat representation. Enrichers
	// may append references by type, so sort anchored parts by their visible
	// UTF-16 start rather than trusting slice order.
	parts := append([]protocol.MessagePart(nil), source.Parts...)
	sort.SliceStable(parts, func(i, j int) bool {
		left := parts[i].ContentStartUTF16
		right := parts[j].ContentStartUTF16
		switch {
		case left == nil:
			return false
		case right == nil:
			return true
		default:
			return *left < *right
		}
	})
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeReference || part.RefID == "" {
			continue
		}
		switch {
		case part.RefType == "issue-ref" && (part.RefSubType == "" || part.RefSubType == "issue"):
			appendMention(util.Mention{Type: "issue", ID: part.RefID})
		case part.RefType == "mention" && part.RefSubType == "agent":
			appendMention(util.Mention{Type: "agent", ID: part.RefID})
		}
	}
	// Comments and older internal writers still carry canonical mention links
	// in markdown content. Parsing is read-only and never rewrites the body.
	for _, mention := range util.ParseMentions(source.Content) {
		appendMention(mention)
	}
	return mentions
}

func (h *Handler) hydrateIssueReference(
	ctx context.Context,
	workspaceUUID pgtype.UUID,
	workspaceID, actorType, actorID, prefix string,
	issueID pgtype.UUID,
) (protocol.ReferencedEntitySnapshot, bool) {
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issueID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		return protocol.ReferencedEntitySnapshot{}, false
	}

	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	fields := []string{
		fmt.Sprintf("issue %s: %s", identifier, issue.Title),
		"status: " + issue.Status,
	}
	if assignee, ok := h.referencedIssueAssignee(ctx, issue, workspaceID); ok {
		fields = append(fields, "assignee: "+assignee)
	}
	fields = append(fields, "priority: "+issue.Priority)

	prLabels := make([]string, 0)
	if prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID); err == nil {
		prLabels = make([]string, 0, len(prs))
		for _, pr := range prs {
			state := strings.TrimSpace(pr.State)
			if pr.MergedAt.Valid {
				state = "merged"
			}
			if state == "" {
				state = "unknown"
			}
			prLabels = append(prLabels, fmt.Sprintf("#%d %s", pr.PrNumber, state))
		}
	}
	if len(prLabels) == 0 {
		fields = append(fields, "PRs: none")
	} else {
		fields = append(fields, "PRs: "+strings.Join(prLabels, ", "))
	}

	return promptcontext.NewReferencedEntitySnapshot(
		"issue",
		util.UUIDToString(issue.ID),
		strings.Join(fields, " / "),
	)
}

func (h *Handler) hydrateAgentReference(
	ctx context.Context,
	workspaceUUID pgtype.UUID,
	agentID pgtype.UUID,
) (protocol.ReferencedEntitySnapshot, bool) {
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil || agent.ArchivedAt.Valid {
		return protocol.ReferencedEntitySnapshot{}, false
	}
	role := strings.TrimSpace(agent.Description)
	if role == "" {
		role = "not specified"
	}
	return promptcontext.NewReferencedEntitySnapshot(
		"agent",
		util.UUIDToString(agent.ID),
		fmt.Sprintf("agent %s: role: %s", agentDisplayName(agent), role),
	)
}

func (h *Handler) referencedIssueAssignee(
	ctx context.Context,
	issue db.Issue,
	workspaceID string,
) (string, bool) {
	if !issue.AssigneeID.Valid || !issue.AssigneeType.Valid {
		return "unassigned", true
	}

	switch issue.AssigneeType.String {
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil || agent.ArchivedAt.Valid {
			return "", false
		}
		return agentDisplayName(agent), true
	case "member", "user":
		userID := util.UUIDToString(issue.AssigneeID)
		if _, err := h.getWorkspaceMember(ctx, userID, workspaceID); err != nil {
			return "", false
		}
		user, err := h.Queries.GetUser(ctx, issue.AssigneeID)
		if err != nil {
			return "", false
		}
		return userDisplayName(user), true
	case "squad":
		squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return "", false
		}
		return squad.Name, true
	default:
		return "", false
	}
}
