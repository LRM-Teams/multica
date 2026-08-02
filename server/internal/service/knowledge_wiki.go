package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	KnowledgeEdgeDerivedFrom = "derived_from"
	KnowledgeEdgeAbout       = "about"
	KnowledgeEdgeSharedTo    = "shared_to"
	KnowledgeEdgeSupersedes  = "supersedes"
	KnowledgeEdgeOwnedBy     = "owned_by"

	KnowledgeKindContext  = "context"
	KnowledgeKindDecision = "decision"

	KnowledgeNodeTeamKnowledge = "team_knowledge"
	KnowledgeNodeIssue         = "issue"
	KnowledgeNodeChannel       = "channel"
	KnowledgeNodeProject       = "project"
	KnowledgeNodeAgent         = "agent"
	KnowledgeNodeMember        = "member"

	knowledgeWikiMaxInjectPages = 12
	knowledgeWikiMaxHops        = 2
)

// KnowledgePromoteInput promotes an issue/channel conclusion into a CONTEXT or
// DECISION wiki page and writes the minimal edge set (LRM-1000).
type KnowledgePromoteInput struct {
	WorkspaceID   pgtype.UUID
	SourceType    string // issue | channel
	SourceID      pgtype.UUID
	TargetKind    string // context | decision
	Title         string
	Content       string
	SubjectID     pgtype.UUID // channel_id for context, project_id for decision
	SupersedesID  pgtype.UUID // optional prior page
	ActorType     string      // member | agent
	ActorID       pgtype.UUID
	SharedToAgent pgtype.UUID // optional shared_to target
}

// KnowledgePromoteResult is the promoted page plus edges created.
type KnowledgePromoteResult struct {
	Page  db.InsertTeamKnowledgeItemRow `json:"page"`
	Edges []db.TeamKnowledgeEdge        `json:"edges"`
}

// PromoteKnowledgePage creates a CONTEXT/DECISION page with derived_from + about
// (+ owned_by / supersedes / shared_to when provided).
func (s *TaskService) PromoteKnowledgePage(ctx context.Context, in KnowledgePromoteInput) (*KnowledgePromoteResult, error) {
	if s == nil || s.Queries == nil {
		return nil, fmt.Errorf("task service unavailable")
	}
	kind := strings.ToLower(strings.TrimSpace(in.TargetKind))
	if kind != KnowledgeKindContext && kind != KnowledgeKindDecision {
		return nil, fmt.Errorf("target_kind must be context or decision")
	}
	sourceType := strings.ToLower(strings.TrimSpace(in.SourceType))
	if sourceType != KnowledgeNodeIssue && sourceType != KnowledgeNodeChannel {
		return nil, fmt.Errorf("source_type must be issue or channel")
	}
	if !in.SourceID.Valid {
		return nil, fmt.Errorf("source_id is required")
	}
	title := strings.TrimSpace(in.Title)
	content := strings.TrimSpace(in.Content)
	if title == "" || content == "" {
		return nil, fmt.Errorf("title and content are required")
	}
	actorType := strings.ToLower(strings.TrimSpace(in.ActorType))
	if actorType != "member" && actorType != "agent" {
		actorType = "system"
	}

	meta := map[string]any{
		"subject_id":  util.UUIDToString(in.SubjectID),
		"scope":       kindScope(kind),
		"source_type": sourceType,
		"source_id":   util.UUIDToString(in.SourceID),
		"promoted":    true,
	}
	if kind == KnowledgeKindContext && in.SubjectID.Valid {
		meta["applies"] = map[string]any{"channel_id": util.UUIDToString(in.SubjectID)}
	}
	if kind == KnowledgeKindDecision && in.SubjectID.Valid {
		meta["applies"] = map[string]any{"project_id": util.UUIDToString(in.SubjectID)}
	}
	metaBytes, _ := json.Marshal(meta)

	page, err := s.Queries.InsertTeamKnowledgeItem(ctx, db.InsertTeamKnowledgeItemParams{
		WorkspaceID: in.WorkspaceID,
		Kind:        kind,
		Title:       title,
		Content:     content,
		Metadata:    metaBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("insert knowledge page: %w", err)
	}

	result := &KnowledgePromoteResult{Page: page, Edges: make([]db.TeamKnowledgeEdge, 0, 5)}
	emptyMeta := []byte(`{}`)

	addEdge := func(edgeType, fromKind string, fromID pgtype.UUID, toKind string, toID pgtype.UUID) {
		if !fromID.Valid || !toID.Valid {
			return
		}
		edge, err := s.Queries.InsertTeamKnowledgeEdge(ctx, db.InsertTeamKnowledgeEdgeParams{
			WorkspaceID:   in.WorkspaceID,
			EdgeType:      edgeType,
			FromKind:      fromKind,
			FromID:        fromID,
			ToKind:        toKind,
			ToID:          toID,
			Metadata:      emptyMeta,
			CreatedByType: actorType,
			CreatedByID:   in.ActorID,
		})
		if err != nil {
			slog.Warn("insert knowledge edge failed", "edge_type", edgeType, "error", err)
			return
		}
		result.Edges = append(result.Edges, edge)
	}

	// Page derived_from source episode/issue.
	addEdge(KnowledgeEdgeDerivedFrom, KnowledgeNodeTeamKnowledge, page.ID, sourceType, in.SourceID)
	// Page about subject + source.
	if in.SubjectID.Valid {
		subjectKind := KnowledgeNodeChannel
		if kind == KnowledgeKindDecision {
			subjectKind = KnowledgeNodeProject
		}
		addEdge(KnowledgeEdgeAbout, KnowledgeNodeTeamKnowledge, page.ID, subjectKind, in.SubjectID)
	}
	addEdge(KnowledgeEdgeAbout, KnowledgeNodeTeamKnowledge, page.ID, sourceType, in.SourceID)
	if in.ActorID.Valid && (actorType == "member" || actorType == "agent") {
		ownerKind := KnowledgeNodeMember
		if actorType == "agent" {
			ownerKind = KnowledgeNodeAgent
		}
		addEdge(KnowledgeEdgeOwnedBy, KnowledgeNodeTeamKnowledge, page.ID, ownerKind, in.ActorID)
	}
	if in.SharedToAgent.Valid {
		addEdge(KnowledgeEdgeSharedTo, KnowledgeNodeTeamKnowledge, page.ID, KnowledgeNodeAgent, in.SharedToAgent)
	}
	if in.SupersedesID.Valid {
		addEdge(KnowledgeEdgeSupersedes, KnowledgeNodeTeamKnowledge, page.ID, KnowledgeNodeTeamKnowledge, in.SupersedesID)
		_ = s.Queries.ArchiveTeamKnowledgeItem(ctx, db.ArchiveTeamKnowledgeItemParams{
			WorkspaceID: in.WorkspaceID,
			ID:          in.SupersedesID,
		})
	}
	return result, nil
}

func kindScope(kind string) string {
	if kind == KnowledgeKindDecision {
		return "project"
	}
	return "channel"
}

// LoadTaskRelatedKnowledgeNeighborhood returns task-related wiki pages plus
// at most maxHops undirected edge hops (LRM-1000). Hard-caps page count.
func (s *TaskService) LoadTaskRelatedKnowledgeNeighborhood(ctx context.Context, workspaceID pgtype.UUID, execution MemoryExecutionScope, maxHops int) []AgentMemoryData {
	if s == nil || s.Queries == nil || !workspaceID.Valid {
		return nil
	}
	if maxHops < 0 {
		maxHops = 0
	}
	if maxHops > knowledgeWikiMaxHops {
		maxHops = knowledgeWikiMaxHops
	}

	seeds, err := s.Queries.ListTeamKnowledgeSeedPagesForExecution(ctx, db.ListTeamKnowledgeSeedPagesForExecutionParams{
		WorkspaceID: workspaceID,
		ChannelID:   strings.TrimSpace(execution.ChannelID),
		ProjectID:   strings.TrimSpace(execution.ProjectID),
		IssueID:     strings.TrimSpace(execution.IssueID),
	})
	if err != nil {
		slog.Warn("list knowledge seed pages failed", "error", err)
		return nil
	}
	if len(seeds) == 0 {
		return nil
	}

	seen := make(map[string]db.ListTeamKnowledgeSeedPagesForExecutionRow, len(seeds))
	frontier := make([]pgtype.UUID, 0, len(seeds))
	for _, seed := range seeds {
		id := util.UUIDToString(seed.ID)
		if id == "" {
			continue
		}
		seen[id] = seed
		frontier = append(frontier, seed.ID)
	}

	for hop := 0; hop < maxHops && len(seen) < knowledgeWikiMaxInjectPages; hop++ {
		next := make([]pgtype.UUID, 0)
		for _, pageID := range frontier {
			neighbors, err := s.Queries.ListTeamKnowledgeNeighborPageIDs(ctx, db.ListTeamKnowledgeNeighborPageIDsParams{
				PageID:      pageID,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				continue
			}
			for _, neighborID := range neighbors {
				key := util.UUIDToString(neighborID)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				next = append(next, neighborID)
			}
		}
		if len(next) == 0 {
			break
		}
		pages, err := s.Queries.ListTeamKnowledgePagesByIDs(ctx, db.ListTeamKnowledgePagesByIDsParams{
			WorkspaceID: workspaceID,
			Ids:         next,
		})
		if err != nil {
			break
		}
		frontier = frontier[:0]
		for _, page := range pages {
			key := util.UUIDToString(page.ID)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			if len(seen) >= knowledgeWikiMaxInjectPages {
				break
			}
			seen[key] = db.ListTeamKnowledgeSeedPagesForExecutionRow{
				ID:        page.ID,
				Kind:      page.Kind,
				Title:     page.Title,
				Content:   page.Content,
				Metadata:  page.Metadata,
				UpdatedAt: page.UpdatedAt,
			}
			frontier = append(frontier, page.ID)
		}
	}

	out := make([]AgentMemoryData, 0, len(seen))
	for _, page := range seen {
		out = append(out, teamKnowledgeWikiMemoryData(page))
	}
	return out
}

func teamKnowledgeWikiMemoryData(item db.ListTeamKnowledgeSeedPagesForExecutionRow) AgentMemoryData {
	id := util.UUIDToString(item.ID)
	kind := strings.TrimSpace(item.Kind)
	prefix := "Team knowledge"
	switch kind {
	case KnowledgeKindContext:
		prefix = "CONTEXT"
	case KnowledgeKindDecision:
		prefix = "DECISION"
	}
	scope := "workspace"
	subjectType, subjectID := "", ""
	var meta struct {
		Scope       string `json:"scope"`
		SubjectID   string `json:"subject_id"`
		SubjectType string `json:"subject_type"`
	}
	_ = json.Unmarshal(item.Metadata, &meta)
	if s := strings.TrimSpace(meta.Scope); s != "" {
		scope = s
	}
	if id := strings.TrimSpace(meta.SubjectID); id != "" {
		subjectID = id
		subjectType = strings.TrimSpace(meta.SubjectType)
		if subjectType == "" {
			if kind == KnowledgeKindDecision {
				subjectType = "project"
			} else if kind == KnowledgeKindContext {
				subjectType = "channel"
			}
		}
	}
	return AgentMemoryData{
		ID:          id,
		Name:        prefix + " · " + strings.TrimSpace(item.Title),
		Content:     item.Content,
		Scope:       scope,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		SyncKey:     "team_knowledge:" + kind + ":" + id,
	}
}

// ListKnowledgeNeighbors returns edges touching a wiki page (for API/CLI).
func (s *TaskService) ListKnowledgeNeighbors(ctx context.Context, workspaceID, pageID pgtype.UUID) ([]db.TeamKnowledgeEdge, error) {
	if s == nil || s.Queries == nil {
		return nil, fmt.Errorf("task service unavailable")
	}
	return s.Queries.ListTeamKnowledgeEdgesForNode(ctx, db.ListTeamKnowledgeEdgesForNodeParams{
		WorkspaceID: workspaceID,
		NodeKind:    KnowledgeNodeTeamKnowledge,
		NodeID:      pageID,
	})
}
