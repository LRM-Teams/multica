package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) buildResearchHandoffSummary(ctx context.Context, workspaceID pgtype.UUID, session db.ResearchSession) string {
	var b strings.Builder
	b.WriteString("# Research handoff\n\n")
	b.WriteString("## Goal\n")
	b.WriteString(session.Goal)
	b.WriteString("\n\n")

	if report, err := h.Queries.GetLatestResearchReport(ctx, db.GetLatestResearchReportParams{
		SessionID: session.ID, WorkspaceID: workspaceID,
	}); err == nil && report.ContentMd != "" {
		b.WriteString("## Report\n")
		b.WriteString(report.ContentMd)
		b.WriteString("\n\n")
	}

	sources, _ := h.Queries.ListResearchSources(ctx, db.ListResearchSourcesParams{
		SessionID: session.ID, WorkspaceID: workspaceID,
	})
	if len(sources) > 0 {
		b.WriteString("## High-weight sources\n")
		for _, s := range sources {
			if s.CredibilityWeight < 0.6 {
				continue
			}
			fmt.Fprintf(&b, "- [%.2f] %s %s\n", s.CredibilityWeight, s.Title, s.Url)
		}
		b.WriteString("\n")
	}

	nodes, _ := h.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{
		SessionID: session.ID, WorkspaceID: workspaceID,
	})
	b.WriteString("## Wrong turns / pivots\n")
	for _, n := range nodes {
		if n.NodeType == "pivot" || n.NodeType == "dead_end" || n.NodeType == "refuted" {
			fmt.Fprintf(&b, "- (%s) %s — %s\n", n.NodeType, n.Title, n.Summary)
		}
	}
	return b.String()
}

func (h *Handler) createOrdinaryGroupChannelForResearch(r *http.Request, workspaceID, userID pgtype.UUID, name, description string) (pgtype.UUID, error) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer tx.Rollback(r.Context())

	var descPtr *string
	if description != "" {
		descPtr = &description
	}
	channelIDStr, err := createOrdinaryGroupWithOwnerTx(
		r.Context(), tx,
		workspaceID, userID,
		name, descPtr, nil, pgtype.UUID{},
	)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return pgtype.UUID{}, err
	}
	return parseUUID(channelIDStr), nil
}
