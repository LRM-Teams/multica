package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type researchProcessEvent struct {
	Op      string
	Title   string
	Body    string
	ActorID pgtype.UUID
	Meta    map[string]any
}

func (h *Handler) publishResearchGraph(workspaceID string, actorType, actorID string, sessionID pgtype.UUID, node db.ResearchGraphNode, edge *db.ResearchGraphEdge) {
	var edgeResp any
	if edge != nil {
		er := mapEdges([]db.ResearchGraphEdge{*edge})[0]
		edgeResp = er
	}
	h.publish(protocol.EventResearchSessionGraphUpdated, workspaceID, actorType, actorID, map[string]any{
		"session_id": uuidToString(sessionID),
		"node":       mapGraphNodeWithEdge(node, edge),
		"edge":       edgeResp,
	})
	// V6 is a separate, run-scoped transport. Keep the legacy event intact while
	// new clients receive an explicit envelope and can reject cross-run frames.
	if h.ResearchRun != nil {
		runID := uuidToString(sessionID)
		if _, err := h.ResearchRun.Snapshot(context.Background(), runID, workspaceID); err == nil {
			var sequence int64
			if err = h.DB.QueryRow(context.Background(), `SELECT COALESCE(max(sequence),0) FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, workspaceID, runID).Scan(&sequence); err == nil {
				// This compatibility callback has no committed Run Event payload and
				// therefore no trustworthy prior graph baseline for tombstones. Tell
				// V6 clients to reload instead of publishing a lossy synthetic delta.
				h.publish(protocol.EventResearchProjectionV6Delta, workspaceID, actorType, actorID,
					researchV6RealtimeResyncEnvelope{RunID: runID, ResyncRequired: true, ThroughSequence: sequence})
			}
		}
	}
}

func (h *Handler) emitResearchProcessCard(
	ctx context.Context,
	workspaceID string,
	wsUUID, sessionID pgtype.UUID,
	actorType, actorID string,
	ev researchProcessEvent,
) {
	meta := ev.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	meta["op"] = ev.Op
	if ev.Title != "" {
		meta["title"] = ev.Title
	}
	if ev.ActorID.Valid {
		meta["actor_agent_id"] = uuidToString(ev.ActorID)
	}
	cardKind := "process"
	body := ev.Body
	if body == "" {
		body = ev.Title
	}
	msg, err := h.createResearchMessageWithPassport(ctx, db.CreateResearchMessageParams{
		WorkspaceID:   wsUUID,
		SessionID:     sessionID,
		SenderType:    "system",
		SenderID:      pgtype.UUID{},
		TargetAgentID: pgtype.UUID{},
		Body:          body,
		CardKind:      cardKind,
		Meta:          marshalJSONRaw(meta),
	})
	if err != nil {
		slog.Warn("research process card failed", "session_id", uuidToString(sessionID), "op", ev.Op, "error", err)
		return
	}
	h.publish(protocol.EventResearchSessionMessage, workspaceID, actorType, actorID, map[string]any{
		"session_id": uuidToString(sessionID),
		"message":    mapMessages([]db.ResearchMessage{msg})[0],
	})
}

func (h *Handler) createResearchGraphNodeWithPassport(
	ctx context.Context,
	wsUUID, sessionID pgtype.UUID,
	params db.CreateResearchGraphNodeParams,
	fromNodeID pgtype.UUID,
	edgeType string,
) (db.ResearchGraphNode, *db.ResearchGraphEdge, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.ResearchGraphNode{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := h.Queries.WithTx(tx)
	node, err := qtx.CreateResearchGraphNode(ctx, params)
	if err != nil {
		return db.ResearchGraphNode{}, nil, err
	}
	if err := ensureGraphNodePassportTx(ctx, tx, wsUUID, sessionID, node.ID); err != nil {
		return db.ResearchGraphNode{}, nil, err
	}

	var edge *db.ResearchGraphEdge
	if fromNodeID.Valid {
		if edgeType == "" {
			edgeType = "leads_to"
		}
		e, eerr := qtx.CreateResearchGraphEdge(ctx, db.CreateResearchGraphEdgeParams{
			WorkspaceID: wsUUID,
			SessionID:   sessionID,
			FromNodeID:  fromNodeID,
			ToNodeID:    node.ID,
			EdgeType:    edgeType,
		})
		if eerr != nil {
			return db.ResearchGraphNode{}, nil, eerr
		}
		if err := ensureGraphEdgePassportTx(ctx, tx, wsUUID, sessionID, e.ID); err != nil {
			return db.ResearchGraphNode{}, nil, err
		}
		edge = &e
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ResearchGraphNode{}, nil, err
	}
	return node, edge, nil
}

func (h *Handler) createResearchGraphNodePublished(
	ctx context.Context,
	workspaceID string,
	wsUUID, sessionID pgtype.UUID,
	actorType, actorID string,
	params db.CreateResearchGraphNodeParams,
	fromNodeID pgtype.UUID,
	edgeType string,
) (db.ResearchGraphNode, *db.ResearchGraphEdge, error) {
	node, edge, err := h.createResearchGraphNodeWithPassport(ctx, wsUUID, sessionID, params, fromNodeID, edgeType)
	if err != nil {
		return db.ResearchGraphNode{}, nil, err
	}
	h.publishResearchGraph(workspaceID, actorType, actorID, sessionID, node, edge)
	return node, edge, nil
}

func researchMemberLabel(m db.ResearchFleetMember, name, display string) string {
	if display != "" {
		return display
	}
	if name != "" {
		return name
	}
	return m.Role
}

func researchRoleKickoffBrief(role string) string {
	switch role {
	case "lead":
		return "统筹 S1 作战图，向团员分派探查任务"
	case "scout":
		return "待命：准备多源入口检索（官网/文档/仓库/论坛）"
	case "reader":
		return "待命：准备深读与摘录高权重来源"
	case "validator":
		return "待命：准备冲突检测与交叉验证"
	case "reporter":
		return "待命：准备维护来源图谱与交付报告"
	default:
		return fmt.Sprintf("待命：角色 %s", role)
	}
}
