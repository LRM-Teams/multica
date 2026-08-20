package researchrun

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ProjectionV6NodeDetail(ctx context.Context, workspaceID, runID, nodeID, view string) (V6ProjectionNodeDetail, error) {
	if view == "" {
		view = "brief"
	}
	if view != "brief" && view != "full" && view != "history" {
		return V6ProjectionNodeDetail{}, ErrInvalidContract
	}
	first, err := s.createV6ProjectionSnapshot(ctx, V6ProjectionPageRequest{WorkspaceID: workspaceID, RunID: runID, Limit: v6ProjectionMaximumPageSize})
	if err != nil {
		return V6ProjectionNodeDetail{}, err
	}
	nodes, edges, _, sequence, projectionHash, err := s.loadPinnedCanonicalV6Projection(ctx, workspaceID, runID, first.SnapshotID)
	if err != nil {
		return V6ProjectionNodeDetail{}, err
	}
	detail := V6ProjectionNodeDetail{SnapshotID: first.SnapshotID, ThroughEventSequence: sequence, ProjectionHash: projectionHash, View: view, Incoming: []V6ProjectionEdge{}, Outgoing: []V6ProjectionEdge{}, HistoryRefs: []V6ProjectionEntityRef{}, AgentRefs: []V6ProjectionEntityRef{}, WorkItemRefs: []V6ProjectionEntityRef{}, AttemptRefs: []V6ProjectionEntityRef{}, EvidenceRefs: []V6ProjectionEntityRef{}, DiscussionRefs: []V6ProjectionEntityRef{}, ReportRefs: []V6ProjectionEntityRef{}}
	found := false
	for _, node := range nodes {
		if node.ID == nodeID {
			detail.Node = node
			found = true
			break
		}
	}
	if !found {
		return V6ProjectionNodeDetail{}, pgx.ErrNoRows
	}
	for _, edge := range edges {
		if edge.FromNodeID == nodeID {
			detail.Outgoing = append(detail.Outgoing, edge)
		}
		if edge.ToNodeID == nodeID {
			detail.Incoming = append(detail.Incoming, edge)
		}
	}
	versionID := detail.Node.CanonicalRef.VersionID
	if detail.Node.Kind == "work_s" {
		detail.WorkItemRefs = append(detail.WorkItemRefs, V6ProjectionEntityRef{Kind: "work_item", ID: detail.Node.CanonicalRef.ID})
		rows, queryErr := s.pool.Query(ctx, `SELECT a.id::text,a.assigned_agent_id::text FROM research_work_item_attempt a WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid ORDER BY a.attempt_number`, workspaceID, runID, detail.Node.CanonicalRef.ID)
		if queryErr != nil {
			return V6ProjectionNodeDetail{}, queryErr
		}
		for rows.Next() {
			var attemptID, agentID string
			if queryErr = rows.Scan(&attemptID, &agentID); queryErr != nil {
				rows.Close()
				return V6ProjectionNodeDetail{}, queryErr
			}
			detail.AttemptRefs = append(detail.AttemptRefs, V6ProjectionEntityRef{Kind: "attempt", ID: attemptID})
			detail.AgentRefs = appendUniqueV6ProjectionRef(detail.AgentRefs, V6ProjectionEntityRef{Kind: "agent", ID: agentID})
		}
		rows.Close()
	}
	if detail.Node.Kind == "result_s" && versionID != "" {
		var attemptID, workID, agentID string
		if queryErr := s.pool.QueryRow(ctx, `SELECT a.id::text,a.work_item_id::text,a.assigned_agent_id::text FROM research_result_node rn JOIN research_work_item_attempt a ON a.id=rn.work_item_attempt_id WHERE rn.workspace_id=$1::uuid AND rn.session_id=$2::uuid AND rn.artifact_version_id=$3::uuid`, workspaceID, runID, versionID).Scan(&attemptID, &workID, &agentID); queryErr != nil {
			return V6ProjectionNodeDetail{}, queryErr
		}
		detail.AttemptRefs = append(detail.AttemptRefs, V6ProjectionEntityRef{Kind: "attempt", ID: attemptID})
		detail.WorkItemRefs = append(detail.WorkItemRefs, V6ProjectionEntityRef{Kind: "work_item", ID: workID})
		detail.AgentRefs = append(detail.AgentRefs, V6ProjectionEntityRef{Kind: "agent", ID: agentID})
	}
	if versionID != "" {
		rows, queryErr := s.pool.Query(ctx, `SELECT DISTINCT d.id::text FROM research_discussion d JOIN research_discussion_input i ON i.discussion_id=d.id WHERE d.workspace_id=$1::uuid AND d.session_id=$2::uuid AND i.node_artifact_version_id=$3::uuid ORDER BY d.id::text`, workspaceID, runID, versionID)
		if queryErr != nil {
			return V6ProjectionNodeDetail{}, queryErr
		}
		for rows.Next() {
			var id string
			if queryErr = rows.Scan(&id); queryErr != nil {
				rows.Close()
				return V6ProjectionNodeDetail{}, queryErr
			}
			detail.DiscussionRefs = append(detail.DiscussionRefs, V6ProjectionEntityRef{Kind: "discussion", ID: id})
		}
		rows.Close()
		rows, queryErr = s.pool.Query(ctx, `SELECT DISTINCT r.id::text,r.revision,COALESCE(r.package_hash,'') FROM research_report r JOIN research_report_input i ON i.report_id=r.id AND i.report_revision=r.revision WHERE r.workspace_id=$1::uuid AND r.session_id=$2::uuid AND i.node_artifact_version_id=$3::uuid ORDER BY r.revision,r.id::text`, workspaceID, runID, versionID)
		if queryErr != nil {
			return V6ProjectionNodeDetail{}, queryErr
		}
		for rows.Next() {
			var id, hash string
			var revision int
			if queryErr = rows.Scan(&id, &revision, &hash); queryErr != nil {
				rows.Close()
				return V6ProjectionNodeDetail{}, queryErr
			}
			detail.ReportRefs = append(detail.ReportRefs, V6ProjectionEntityRef{Kind: "report", ID: id, Revision: revision, ContentHash: hash})
		}
		rows.Close()
	}
	if (view == "history" || view == "full") && detail.Node.Kind == "insight" {
		rows, queryErr := s.pool.Query(ctx, `SELECT iv.insight_id::text,iv.revision,iv.artifact_version_id::text,v.content_hash FROM research_insight_version iv JOIN research_artifact_version v ON v.id=iv.artifact_version_id WHERE iv.workspace_id=$1::uuid AND iv.session_id=$2::uuid AND iv.insight_id=$3::uuid ORDER BY iv.revision`, workspaceID, runID, detail.Node.CanonicalRef.ID)
		if queryErr != nil {
			return V6ProjectionNodeDetail{}, queryErr
		}
		for rows.Next() {
			var id, version, hash string
			var revision int
			if queryErr = rows.Scan(&id, &revision, &version, &hash); queryErr != nil {
				rows.Close()
				return V6ProjectionNodeDetail{}, queryErr
			}
			detail.HistoryRefs = append(detail.HistoryRefs, V6ProjectionEntityRef{Kind: "insight", ID: id, Revision: revision, VersionID: version, ContentHash: hash})
		}
		rows.Close()
	}
	sort.Slice(detail.Incoming, func(i, j int) bool { return detail.Incoming[i].ID < detail.Incoming[j].ID })
	sort.Slice(detail.Outgoing, func(i, j int) bool { return detail.Outgoing[i].ID < detail.Outgoing[j].ID })
	return detail, nil
}

func appendUniqueV6ProjectionRef(values []V6ProjectionEntityRef, candidate V6ProjectionEntityRef) []V6ProjectionEntityRef {
	for _, value := range values {
		if value.Kind == candidate.Kind && value.ID == candidate.ID {
			return values
		}
	}
	return append(values, candidate)
}
