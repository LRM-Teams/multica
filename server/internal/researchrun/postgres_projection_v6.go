package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type v6ProjectionBuild struct {
	workspaceID, runID string
	throughSequence    int64
	nodes              []V6ProjectionNode
	edges              []V6ProjectionEdge
	density            []V6ProjectionDensityBin
	defaultVisible     map[string]bool
}

func (s *PostgresStore) ProjectionV6Snapshot(ctx context.Context, request V6ProjectionPageRequest) (V6ProjectionSnapshot, error) {
	if request.Limit == 0 {
		request.Limit = v6ProjectionDefaultPageSize
	}
	if request.Limit < 1 || request.Limit > v6ProjectionMaximumPageSize {
		return V6ProjectionSnapshot{}, ErrInvalidContract
	}
	if request.Cursor != "" {
		cursor, err := decodeV6ProjectionCursor(request.Cursor)
		if err != nil || cursor.Limit != request.Limit {
			return V6ProjectionSnapshot{}, ErrInvalidContract
		}
		if cursor.SliceKey != "default" {
			return V6ProjectionSnapshot{}, ErrInvalidContract
		}
		return s.loadV6ProjectionPage(ctx, request.WorkspaceID, request.RunID, cursor)
	}
	return s.createV6ProjectionSnapshot(ctx, request)
}

func (s *PostgresStore) createV6ProjectionSnapshot(ctx context.Context, request V6ProjectionPageRequest) (V6ProjectionSnapshot, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6ProjectionSnapshot, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "research-v6-projection:"+request.RunID); err != nil {
		return V6ProjectionSnapshot{}, err
	}
	build, err := buildCanonicalV6ProjectionTx(ctx, tx, request.WorkspaceID, request.RunID)
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	normalizeV6Projection(build.nodes, build.edges, build.density)
	projectionHash, err := hashV6Projection(build.nodes, build.edges, build.density)
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	snapshotID := uuid.NewString()
	var generation int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(generation),0)+1 FROM research_projection_snapshot WHERE session_id=$1::uuid`, request.RunID).Scan(&generation); err != nil {
		return V6ProjectionSnapshot{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_projection_snapshot(id,workspace_id,session_id,through_event_sequence,generation,expires_at,projection_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,now()+interval '15 minutes',$6)`, snapshotID, request.WorkspaceID, request.RunID, build.throughSequence, generation, projectionHash); err != nil {
		return V6ProjectionSnapshot{}, err
	}
	visibleNodes, visibleEdges := defaultV6ProjectionVisibility(build.nodes, build.edges, build.defaultVisible)
	pages := paginateV6Projection(snapshotID, request.WorkspaceID, request.RunID, build.throughSequence, projectionHash, "default", request.Limit, visibleNodes, visibleEdges, build.density)
	for index := range pages {
		payload, marshalErr := marshalV6CanonicalJSON(pages[index])
		if marshalErr != nil {
			return V6ProjectionSnapshot{}, marshalErr
		}
		cursorKey := fmt.Sprintf("page:%08d:limit:%d", index+1, request.Limit)
		if _, err = tx.Exec(ctx, `INSERT INTO research_projection_slice(workspace_id,session_id,snapshot_id,slice_key,cursor_key,node_count,edge_count,density_count,payload_hash,payload_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,'default',$4,$5,$6,$7,$8,$9)`, request.WorkspaceID, request.RunID, snapshotID, cursorKey, len(pages[index].Nodes), len(pages[index].Edges), len(pages[index].DensityBins), ArtifactContentHashFromCanonicalJSON(payload), payload); err != nil {
			return V6ProjectionSnapshot{}, err
		}
	}
	canonicalPages := paginateV6Projection(snapshotID, request.WorkspaceID, request.RunID, build.throughSequence, projectionHash, "canonical", v6ProjectionMaximumPageSize, build.nodes, build.edges, build.density)
	for index := range canonicalPages {
		payload, marshalErr := marshalV6CanonicalJSON(canonicalPages[index])
		if marshalErr != nil {
			return V6ProjectionSnapshot{}, marshalErr
		}
		cursorKey := fmt.Sprintf("page:%08d:limit:%d", index+1, v6ProjectionMaximumPageSize)
		if _, err = tx.Exec(ctx, `INSERT INTO research_projection_slice(workspace_id,session_id,snapshot_id,slice_key,cursor_key,node_count,edge_count,density_count,payload_hash,payload_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,'canonical',$4,$5,$6,$7,$8,$9)`, request.WorkspaceID, request.RunID, snapshotID, cursorKey, len(canonicalPages[index].Nodes), len(canonicalPages[index].Edges), len(canonicalPages[index].DensityBins), ArtifactContentHashFromCanonicalJSON(payload), payload); err != nil {
			return V6ProjectionSnapshot{}, err
		}
	}
	if err = s.commitResearchTx(ctx, txOpV6ProjectionSnapshot, tx); err != nil {
		return V6ProjectionSnapshot{}, err
	}
	return pages[0], nil
}

func paginateV6Projection(snapshotID, workspaceID, runID string, sequence int64, projectionHash, sliceKey string, limit int, nodes []V6ProjectionNode, edges []V6ProjectionEdge, density []V6ProjectionDensityBin) []V6ProjectionSnapshot {
	total := len(nodes) + len(edges) + len(density)
	pageCount := (total + limit - 1) / limit
	if pageCount == 0 {
		pageCount = 1
	}
	pages := make([]V6ProjectionSnapshot, pageCount)
	for page := 0; page < pageCount; page++ {
		result := V6ProjectionSnapshot{ContractKind: "projection_snapshot", SchemaVersion: 6, SnapshotID: snapshotID, WorkspaceID: workspaceID, RunID: runID, ThroughEventSequence: sequence, ProjectionHash: projectionHash, SliceKey: sliceKey, Nodes: []V6ProjectionNode{}, Edges: []V6ProjectionEdge{}, DensityBins: []V6ProjectionDensityBin{}, HasMore: page+1 < pageCount}
		start, end := page*limit, (page+1)*limit
		if end > total {
			end = total
		}
		for position := start; position < end; position++ {
			switch {
			case position < len(nodes):
				result.Nodes = append(result.Nodes, nodes[position])
			case position < len(nodes)+len(edges):
				result.Edges = append(result.Edges, edges[position-len(nodes)])
			default:
				result.DensityBins = append(result.DensityBins, density[position-len(nodes)-len(edges)])
			}
		}
		if result.HasMore {
			result.NextCursor = encodeV6ProjectionCursor(v6ProjectionCursor{SnapshotID: snapshotID, SliceKey: sliceKey, Page: page + 2, Limit: limit})
		}
		pages[page] = result
	}
	return pages
}

func defaultV6ProjectionVisibility(nodes []V6ProjectionNode, edges []V6ProjectionEdge, defaultVisible map[string]bool) ([]V6ProjectionNode, []V6ProjectionEdge) {
	visible := map[string]struct{}{}
	outNodes := make([]V6ProjectionNode, 0, len(nodes))
	for _, node := range nodes {
		if !defaultVisible[node.ID] {
			continue
		}
		visible[node.ID] = struct{}{}
		outNodes = append(outNodes, node)
	}
	outEdges := make([]V6ProjectionEdge, 0, len(edges))
	for _, edge := range edges {
		_, fromOK := visible[edge.FromNodeID]
		_, toOK := visible[edge.ToNodeID]
		if fromOK && toOK {
			outEdges = append(outEdges, edge)
		}
	}
	return outNodes, outEdges
}

func (s *PostgresStore) loadV6ProjectionPage(ctx context.Context, workspaceID, runID string, cursor v6ProjectionCursor) (V6ProjectionSnapshot, error) {
	var payload []byte
	var payloadHash string
	err := s.pool.QueryRow(ctx, `SELECT slice.payload_bytes,slice.payload_hash FROM research_projection_slice slice JOIN research_projection_snapshot snapshot ON snapshot.id=slice.snapshot_id WHERE slice.workspace_id=$1::uuid AND slice.session_id=$2::uuid AND slice.snapshot_id=$3::uuid AND slice.slice_key=$4 AND slice.cursor_key=$5 AND snapshot.expires_at>now()`, workspaceID, runID, cursor.SnapshotID, cursor.SliceKey, fmt.Sprintf("page:%08d:limit:%d", cursor.Page, cursor.Limit)).Scan(&payload, &payloadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6ProjectionSnapshot{}, ErrProjectionResyncRequired
	}
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	var result V6ProjectionSnapshot
	if ArtifactContentHashFromCanonicalJSON(payload) != payloadHash || json.Unmarshal(payload, &result) != nil {
		return V6ProjectionSnapshot{}, ErrProjectionResyncRequired
	}
	return result, nil
}

func buildCanonicalV6ProjectionTx(ctx context.Context, tx pgx.Tx, workspaceID, runID string) (v6ProjectionBuild, error) {
	build := v6ProjectionBuild{workspaceID: workspaceID, runID: runID, nodes: []V6ProjectionNode{}, edges: []V6ProjectionEdge{}, density: []V6ProjectionDensityBin{}, defaultVisible: map[string]bool{}}
	var runStatus, goal string
	var updated time.Time
	var goalVersion int
	var contractID string
	if err := tx.QueryRow(ctx, `SELECT s.status,s.goal,s.goal_version,s.updated_at,COALESCE(c.id::text,s.id::text),COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0) FROM research_session s LEFT JOIN research_contract_revision c ON c.session_id=s.id AND c.goal_version=s.goal_version WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND s.orchestrator_version='research-run-v6'`, workspaceID, runID).Scan(&runStatus, &goal, &goalVersion, &updated, &contractID, &build.throughSequence); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return build, ErrRunNotFound
		}
		return build, err
	}
	goalExecution, goalTerminal := projectionExecutionForRun(runStatus)
	goalID := v6ProjectionStableID("goal", contractID, goalVersion)
	build.nodes = append(build.nodes, V6ProjectionNode{ID: goalID, Kind: "goal", Tier: "GOAL", CanonicalRef: V6ProjectionEntityRef{Kind: "goal", ID: contractID, Revision: goalVersion}, BranchIDs: []string{}, State: V6ProjectionState{Execution: goalExecution, Conclusion: "accepted", Integration: "unmatched"}, Title: truncateProjectionText(goal, 4096), CatalogSummary: truncateProjectionText(goal, 512), Terminal: goalTerminal, Expandable: true, UpdatedAt: normalizeProjectionTime(updated)})
	build.defaultVisible[goalID] = true
	versionNodeIDs := map[string]string{}
	workNodeIDs := map[string]string{}
	agentNodeIDs := map[string]string{}
	if err := appendV6InsightProjectionTx(ctx, tx, &build, versionNodeIDs); err != nil {
		return build, err
	}
	if err := appendV6AgentProjectionTx(ctx, tx, &build, goalID, agentNodeIDs); err != nil {
		return build, err
	}
	if err := appendV6WorkProjectionTx(ctx, tx, &build, goalID, workNodeIDs, agentNodeIDs); err != nil {
		return build, err
	}
	if err := appendV6ResultProjectionTx(ctx, tx, &build, goalID, versionNodeIDs, workNodeIDs); err != nil {
		return build, err
	}
	if err := appendV6AbsorptionEdgesTx(ctx, tx, &build, versionNodeIDs); err != nil {
		return build, err
	}
	if err := appendV6DerivationEdgesTx(ctx, tx, &build, versionNodeIDs); err != nil {
		return build, err
	}
	return build, nil
}

func appendV6AgentProjectionTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, goalID string, agentNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT m.agent_id::text,
		       m.membership_generation,
		       m.state,
		       m.mission_prompt,
		       COALESCE(NULLIF(agent.display_name,''),agent.name),
		       GREATEST(m.created_at,COALESCE(m.left_at,m.created_at))
		FROM research_team_membership m
		JOIN agent ON agent.id=m.agent_id AND agent.workspace_id=m.workspace_id
		WHERE m.workspace_id=$1::uuid
		  AND m.session_id=$2::uuid
		  AND m.state IN ('idle','working','offline','retiring')
		ORDER BY m.membership_generation,m.created_at,m.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var agentID, membershipState, mission, displayName string
		var generation int
		var updated time.Time
		if err = rows.Scan(&agentID, &generation, &membershipState, &mission, &displayName, &updated); err != nil {
			return err
		}
		execution := "pending"
		if membershipState == "working" {
			execution = "running"
		} else if membershipState == "retiring" {
			execution = "cancelled"
		}
		nodeID := v6ProjectionStableID("agent", agentID, generation)
		build.nodes = append(build.nodes, V6ProjectionNode{
			ID: nodeID, Kind: "agent", Tier: "S",
			CanonicalRef: V6ProjectionEntityRef{Kind: "agent", ID: agentID, Revision: generation},
			BranchIDs:    []string{},
			State:        V6ProjectionState{Execution: execution, Conclusion: "proposed", Integration: "unmatched"},
			Title:        truncateProjectionText(displayName, 160), CatalogSummary: truncateProjectionText(mission, 512),
			Terminal: false, Expandable: false, UpdatedAt: normalizeProjectionTime(updated),
		})
		build.defaultVisible[nodeID] = true
		agentNodeIDs[agentID] = nodeID
		build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("belongs_to", nodeID, goalID), Kind: "belongs_to", FromNodeID: nodeID, ToNodeID: goalID, Canonical: true})
	}
	return rows.Err()
}

func appendV6InsightProjectionTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, versionNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT iv.id::text,iv.insight_id::text,iv.revision,iv.artifact_version_id::text,iv.tier,iv.catalog_summary,iv.status,iv.created_at,v.content_hash,COALESCE(array_agg(DISTINCT nb.branch_id::text) FILTER(WHERE nb.branch_id IS NOT NULL),'{}'),EXISTS(SELECT 1 FROM research_node_absorption a WHERE a.input_artifact_version_id=iv.artifact_version_id),(SELECT count(*)::int FROM research_node_absorption a WHERE a.successor_insight_version_id=iv.id),EXISTS(SELECT 1 FROM research_branch_frontier f WHERE f.session_id=iv.session_id AND f.node_artifact_version_id=iv.artifact_version_id AND f.removed_by_event_sequence IS NULL) OR EXISTS(SELECT 1 FROM research_branch b WHERE b.session_id=iv.session_id AND b.current_xxl_version_id=iv.id) FROM research_insight_version iv JOIN research_artifact_version v ON v.id=iv.artifact_version_id LEFT JOIN research_node_branch nb ON nb.node_artifact_version_id=iv.artifact_version_id WHERE iv.workspace_id=$1::uuid AND iv.session_id=$2::uuid GROUP BY iv.id,v.content_hash ORDER BY iv.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var versionEntityID, insightID, artifactVersionID, tier, summary, status, contentHash string
		var updated time.Time
		var revision int
		var branches []string
		var absorbed, branchTop bool
		var hiddenInputs int
		if err = rows.Scan(&versionEntityID, &insightID, &revision, &artifactVersionID, &tier, &summary, &status, &updated, &contentHash, &branches, &absorbed, &hiddenInputs, &branchTop); err != nil {
			return err
		}
		nodeID := v6ProjectionStableID("insight", insightID, revision)
		conclusion, terminal := projectionConclusionForInsight(status)
		integration := "unmatched"
		if absorbed {
			integration = "absorbed"
		}
		build.nodes = append(build.nodes, V6ProjectionNode{ID: nodeID, Kind: "insight", Tier: tier, CanonicalRef: V6ProjectionEntityRef{Kind: "insight", ID: insightID, Revision: revision, VersionID: artifactVersionID, ContentHash: contentHash}, BranchIDs: branches, State: V6ProjectionState{Execution: "succeeded", Conclusion: conclusion, Integration: integration}, CatalogSummary: summary, Absorbed: absorbed, Terminal: terminal, Expandable: absorbed || hiddenInputs > 0, HiddenChildCount: hiddenInputs, UpdatedAt: normalizeProjectionTime(updated)})
		build.defaultVisible[nodeID] = terminal || (branchTop && !absorbed)
		versionNodeIDs[artifactVersionID] = nodeID
		_ = versionEntityID
	}
	return rows.Err()
}

func appendV6WorkProjectionTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, goalID string, workNodeIDs, agentNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT w.id::text,
		       w.kind,
		       w.status,
		       COALESCE(w.reason,''),
		       COALESCE(w.terminal_reason_code,''),
		       COALESCE(w.terminal_reason_detail,''),
		       COALESCE(latest_attempt.failure_class,''),
		       COALESCE(latest_attempt.diagnostics,''),
		       GREATEST(
		         w.updated_at,
		         COALESCE(latest_attempt.updated_at,w.updated_at),
		         COALESCE(inbox.updated_at,w.updated_at),
		         COALESCE(inbox.started_at,w.updated_at),
		         COALESCE(inbox.completed_at,w.updated_at),
		         COALESCE(progress.updated_at,w.updated_at)
		       ),
		       COALESCE((
		         SELECT array_agg(scope.branch_id::text ORDER BY scope.branch_id::text)
		         FROM research_v6_work_item_branch scope
		         WHERE scope.workspace_id=w.workspace_id
		           AND scope.session_id=w.session_id
		           AND scope.work_item_id=w.id
		       ),'{}'),
		       COALESCE(w.assigned_agent_id::text,latest_attempt.assigned_agent_id::text,''),
		       COALESCE(NULLIF(agent.display_name,''),agent.name,w.kind)
		FROM research_work_item w
		LEFT JOIN LATERAL (
		  SELECT attempt.assigned_agent_id,attempt.inbox_task_id,attempt.updated_at,attempt.failure_class,attempt.diagnostics
		  FROM research_work_item_attempt attempt
		  WHERE attempt.workspace_id=w.workspace_id
		    AND attempt.session_id=w.session_id
		    AND attempt.work_item_id=w.id
		  ORDER BY attempt.attempt_number DESC
		  LIMIT 1
		) latest_attempt ON true
		LEFT JOIN agent ON agent.id=COALESCE(w.assigned_agent_id,latest_attempt.assigned_agent_id)
		LEFT JOIN agent_inbox_event inbox
		  ON inbox.id=latest_attempt.inbox_task_id
		 AND inbox.agent_id=latest_attempt.assigned_agent_id
		LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id=latest_attempt.inbox_task_id
		WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid
		ORDER BY w.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, status, reason, reasonCode, reasonDetail, attemptFailureClass, attemptDiagnostics, assignedAgentID, agentName string
		var updated time.Time
		var branches []string
		if err = rows.Scan(&id, &kind, &status, &reason, &reasonCode, &reasonDetail, &attemptFailureClass, &attemptDiagnostics, &updated, &branches, &assignedAgentID, &agentName); err != nil {
			return err
		}
		execution, terminal := projectionExecutionForWork(status)
		state := V6ProjectionState{Execution: execution, Conclusion: "proposed", Integration: "unmatched"}
		state.Termination = projectionTerminationForWork(execution, terminal, reasonCode, reasonDetail, attemptFailureClass, attemptDiagnostics)
		nodeID := v6ProjectionStableID("work_s", id, 0)
		build.nodes = append(build.nodes, V6ProjectionNode{ID: nodeID, Kind: "work_s", Tier: "S", CanonicalRef: V6ProjectionEntityRef{Kind: "work_item", ID: id}, BranchIDs: branches, State: state, Title: truncateProjectionText(firstNonEmptyV6(reason, agentName), 160), CatalogSummary: truncateProjectionText(reason, 512), Terminal: terminal, Expandable: false, UpdatedAt: normalizeProjectionTime(updated)})
		build.defaultVisible[nodeID] = kind != "director" || !terminal
		workNodeIDs[id] = nodeID
		build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("belongs_to", nodeID, goalID), Kind: "belongs_to", FromNodeID: nodeID, ToNodeID: goalID, Canonical: true})
		if agentNodeID := agentNodeIDs[assignedAgentID]; agentNodeID != "" {
			build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("assigned_to", nodeID, agentNodeID), Kind: "assigned_to", FromNodeID: nodeID, ToNodeID: agentNodeID, Canonical: true})
		}
	}
	return rows.Err()
}

func appendV6ResultProjectionTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, goalID string, versionNodeIDs, workNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT rn.id::text,rn.artifact_version_id::text,v.artifact_id::text,v.content_hash,rn.catalog_summary,rn.conclusion_state,rn.integration_state,rn.reason_code,rn.reason_detail,rn.accepted_at,a.work_item_id::text,COALESCE(array_agg(DISTINCT nb.branch_id::text) FILTER(WHERE nb.branch_id IS NOT NULL),'{}'),EXISTS(SELECT 1 FROM research_node_absorption absorb WHERE absorb.input_artifact_version_id=rn.artifact_version_id) FROM research_result_node rn JOIN research_artifact_version v ON v.id=rn.artifact_version_id JOIN research_work_item_attempt a ON a.id=rn.work_item_attempt_id LEFT JOIN research_node_branch nb ON nb.node_artifact_version_id=rn.artifact_version_id WHERE rn.workspace_id=$1::uuid AND rn.session_id=$2::uuid GROUP BY rn.id,v.id,a.work_item_id ORDER BY rn.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, versionID, artifactID, contentHash, summary, conclusion, integration, reasonCode, reasonDetail, workID string
		var updated time.Time
		var branches []string
		var absorbed bool
		if err = rows.Scan(&id, &versionID, &artifactID, &contentHash, &summary, &conclusion, &integration, &reasonCode, &reasonDetail, &updated, &workID, &branches, &absorbed); err != nil {
			return err
		}
		terminal := conclusion == "refuted" || conclusion == "invalid" || reasonCode != ""
		state := V6ProjectionState{Execution: "succeeded", Conclusion: conclusion, Integration: integration}
		if absorbed {
			state.Integration = "absorbed"
		}
		if terminal && reasonCode != "" {
			state.Termination = &V6ProjectionTermination{ReasonCode: normalizeProjectionReason(reasonCode), ReasonDetail: nonemptyProjectionReason(reasonDetail)}
		}
		nodeID := v6ProjectionStableID("result_s", id, 0)
		build.nodes = append(build.nodes, V6ProjectionNode{ID: nodeID, Kind: "result_s", Tier: "S", CanonicalRef: V6ProjectionEntityRef{Kind: "result", ID: artifactID, VersionID: versionID, ContentHash: contentHash}, BranchIDs: branches, State: state, CatalogSummary: summary, Absorbed: absorbed, Terminal: terminal, Expandable: absorbed, UpdatedAt: normalizeProjectionTime(updated)})
		build.defaultVisible[nodeID] = terminal || !absorbed
		versionNodeIDs[versionID] = nodeID
		if workNodeIDs[workID] != "" {
			workNodeID := workNodeIDs[workID]
			build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("produced_by", nodeID, workNodeID), Kind: "produced_by", FromNodeID: nodeID, ToNodeID: workNodeID, Canonical: true})
			build.defaultVisible[workNodeID] = false
			build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("collapsed_path", nodeID, goalID), Kind: "collapsed_path", FromNodeID: nodeID, ToNodeID: goalID, Canonical: false, HiddenCount: 1})
		}
	}
	return rows.Err()
}

func appendV6AbsorptionEdgesTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, versionNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT a.input_artifact_version_id::text,iv.artifact_version_id::text FROM research_node_absorption a JOIN research_insight_version iv ON iv.id=a.successor_insight_version_id WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid ORDER BY a.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var input, successor string
		if err = rows.Scan(&input, &successor); err != nil {
			return err
		}
		from, to := versionNodeIDs[input], versionNodeIDs[successor]
		if from != "" && to != "" {
			build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("absorbed_into", from, to), Kind: "absorbed_into", FromNodeID: from, ToNodeID: to, Canonical: true})
		}
	}
	return rows.Err()
}

func appendV6DerivationEdgesTx(ctx context.Context, tx pgx.Tx, build *v6ProjectionBuild, versionNodeIDs map[string]string) error {
	rows, err := tx.Query(ctx, `SELECT d.input_artifact_version_id::text,output.artifact_version_id::text FROM research_insight_derivation d JOIN research_insight_version output ON output.id=d.insight_version_id WHERE d.workspace_id=$1::uuid AND d.session_id=$2::uuid AND d.input_artifact_version_id IS NOT NULL ORDER BY d.id`, build.workspaceID, build.runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var input, output string
		if err = rows.Scan(&input, &output); err != nil {
			return err
		}
		from, to := versionNodeIDs[input], versionNodeIDs[output]
		if from != "" && to != "" {
			build.edges = append(build.edges, V6ProjectionEdge{ID: v6ProjectionEdgeID("derived_from", from, to), Kind: "derived_from", FromNodeID: from, ToNodeID: to, Canonical: true})
		}
	}
	return rows.Err()
}

func projectionExecutionForRun(status string) (string, bool) {
	switch status {
	case "completed", "archived":
		return "succeeded", true
	case "failed":
		return "failed", true
	case "cancelled":
		return "cancelled", true
	default:
		return "running", false
	}
}

func projectionExecutionForWork(status string) (string, bool) {
	switch status {
	case "pending", "ready", "enqueued", "dispatching":
		return "pending", false
	case "running", "awaiting_input":
		return "running", false
	case "done", "succeeded":
		return "succeeded", true
	case "failed":
		return "failed", true
	case "cancelled", "stale":
		return "cancelled", true
	default:
		return "lost", true
	}
}

func projectionConclusionForInsight(status string) (string, bool) {
	switch status {
	case "challenged":
		return "challenged", false
	case "refuted":
		return "refuted", true
	case "invalid":
		return "invalid", true
	case "terminal":
		return "accepted", true
	default:
		return "accepted", false
	}
}

func truncateProjectionText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func normalizeProjectionTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func normalizeProjectionReason(value string) string {
	allowed := map[string]bool{"invalid_direction": true, "dead_end": true, "no_semantic_gain": true, "duplicate": true, "out_of_scope": true, "stopped_by_user": true, "stopped_by_director": true, "resource_failure": true, "superseded": true, "other": true}
	if allowed[value] {
		return value
	}
	resourceFailures := map[string]bool{"attempt_budget_exhausted": true, "contract_rejected": true, "dispatch_failed": true, "runtime_failure": true, "runtime_unavailable": true, "task_timeout": true, "lost": true}
	if resourceFailures[value] {
		return "resource_failure"
	}
	return "other"
}

func projectionTerminationForWork(execution string, terminal bool, reasonCode, reasonDetail, failureClass, diagnostics string) *V6ProjectionTermination {
	if !terminal || (execution == "succeeded" && reasonCode == "" && failureClass == "") {
		return nil
	}
	canonicalReason := reasonCode
	if canonicalReason == "" {
		canonicalReason = failureClass
	}
	detail := reasonDetail
	if detail == "" {
		detail = diagnostics
	}
	if canonicalReason == "" {
		canonicalReason = "other"
	}
	if detail == "" {
		detail = "未记录具体失败原因。"
	}
	return &V6ProjectionTermination{
		ReasonCode:   normalizeProjectionReason(canonicalReason),
		ReasonDetail: truncateProjectionText(fmt.Sprintf("%s：%s", canonicalReason, detail), 32768),
	}
}

func nonemptyProjectionReason(value string) string {
	if value == "" {
		return "terminal canonical state"
	}
	return truncateProjectionText(value, 32768)
}
