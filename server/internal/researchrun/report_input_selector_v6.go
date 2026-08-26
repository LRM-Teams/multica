package researchrun

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/jackc/pgx/v5"
)

type v6ReportDirection struct {
	id, objective, status string
}

type v6ReportCandidate struct {
	directionID, branchID, versionID, artifactID, kind, tier, hash, producerAgentID string
	producerName                                                                    string
	catalog, brief, objective, conclusion, content                                  string
	scope, uncertainties, conflicts, openQuestions                                  json.RawMessage
	createdOrdinal                                                                  int64
}

// selectV6ReportInputsTx is the sole policy boundary for report composition.
// It chooses one current maximum per top-level research direction and then
// deduplicates cross-direction successors that represent several directions.
func selectV6ReportInputsTx(ctx context.Context, tx pgx.Tx, workspaceID, runID string) (V6ReportSelection, error) {
	rows, err := tx.Query(ctx, `WITH root AS (
		SELECT id FROM research_branch
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
		ORDER BY (client_key='root') DESC,created_at,id LIMIT 1
	)
	SELECT branch.id::text,branch.objective,branch.status
	FROM research_branch branch JOIN root ON branch.parent_branch_id=root.id
	WHERE branch.status<>'obsolete' ORDER BY branch.created_at,branch.id`, workspaceID, runID)
	if err != nil {
		return V6ReportSelection{}, err
	}
	directions := []v6ReportDirection{}
	for rows.Next() {
		var direction v6ReportDirection
		if err = rows.Scan(&direction.id, &direction.objective, &direction.status); err != nil {
			rows.Close()
			return V6ReportSelection{}, err
		}
		directions = append(directions, direction)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return V6ReportSelection{}, err
	}
	rows.Close()
	// Historical V6 runs could place all research directly on the canonical
	// root. Treat that root as one direction so they can still gain a report;
	// new runs always create explicit top-level directions.
	if len(directions) == 0 {
		var direction v6ReportDirection
		if err = tx.QueryRow(ctx, `SELECT id::text,objective,status FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
			ORDER BY (client_key='root') DESC,created_at,id LIMIT 1`, workspaceID, runID).Scan(&direction.id, &direction.objective, &direction.status); err != nil {
			return V6ReportSelection{}, err
		}
		directions = append(directions, direction)
	}

	rows, err = tx.Query(ctx, `WITH RECURSIVE root AS (
		SELECT id FROM research_branch
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
		ORDER BY (client_key='root') DESC,created_at,id LIMIT 1
	), anchors(top_branch_id,branch_id) AS (
		SELECT branch.id,branch.id FROM research_branch branch JOIN root ON branch.parent_branch_id=root.id
		WHERE branch.status<>'obsolete'
		UNION ALL
		SELECT root.id,root.id FROM root WHERE NOT EXISTS (
			SELECT 1 FROM research_branch branch WHERE branch.parent_branch_id=root.id AND branch.status<>'obsolete'
		)
	), tree(top_branch_id,branch_id) AS (
		SELECT top_branch_id,branch_id FROM anchors
		UNION ALL
		SELECT tree.top_branch_id,child.id FROM tree
		JOIN research_branch child ON child.parent_branch_id=tree.branch_id
		WHERE child.status<>'obsolete'
	), raw_candidates AS (
		SELECT tree.top_branch_id,binding.branch_id,version.id AS version_id,version.artifact_id,
			CASE WHEN result.id IS NOT NULL THEN 'result_s' ELSE 'insight' END AS kind,
			frontier.tier,version.content_hash,
			COALESCE(version.produced_by_agent_id::text,'') AS producer_agent_id,
			COALESCE(NULLIF(producer.display_name,''),producer.name,'') AS producer_name,
			frontier.added_by_event_sequence,
			COALESCE(result.catalog_summary,insight.catalog_summary) AS catalog_summary,
			COALESCE(result.brief_summary,insight.brief_summary) AS brief_summary,
			COALESCE(result.objective,insight.objective) AS objective,
			COALESCE(result.conclusion,insight.conclusion) AS conclusion,
			COALESCE(result.content,insight.content) AS content,
			COALESCE(result.scope,insight.scope,'{}'::jsonb) AS scope,
			COALESCE(result.uncertainties,insight.uncertainties,'[]'::jsonb) AS uncertainties,
			COALESCE(result.conflicts,insight.conflicts,'[]'::jsonb) AS conflicts,
			COALESCE(result.open_questions,insight.open_questions,'[]'::jsonb) AS open_questions
		FROM tree
		JOIN research_node_branch binding ON binding.session_id=$2::uuid AND binding.branch_id=tree.branch_id
		JOIN research_branch_frontier frontier ON frontier.session_id=binding.session_id
			AND frontier.branch_id=binding.branch_id
			AND frontier.node_artifact_version_id=binding.node_artifact_version_id
			AND frontier.removed_by_event_sequence IS NULL
		JOIN research_artifact_version version ON version.id=frontier.node_artifact_version_id
		JOIN research_artifact_passport passport ON passport.id=version.artifact_id AND passport.lifecycle_status='accepted'
		LEFT JOIN agent producer ON producer.workspace_id=version.workspace_id AND producer.id=version.produced_by_agent_id
		LEFT JOIN research_result_node result ON result.artifact_version_id=version.id
		LEFT JOIN research_insight_version insight ON insight.artifact_version_id=version.id
		WHERE version.workspace_id=$1::uuid AND version.session_id=$2::uuid
			AND (result.id IS NOT NULL OR insight.id IS NOT NULL)
			AND (result.id IS NULL OR result.conclusion_state NOT IN ('invalid','refuted'))
			AND (insight.id IS NULL OR insight.status NOT IN ('invalid','refuted','superseded','terminal'))
	), candidates AS (
		SELECT top_branch_id,(array_agg(branch_id ORDER BY branch_id))[1] AS branch_id,version_id,artifact_id,kind,tier,content_hash,producer_agent_id,producer_name,
			max(added_by_event_sequence) AS added_by_event_sequence,catalog_summary,brief_summary,objective,conclusion,content,
			scope,uncertainties,conflicts,open_questions
		FROM raw_candidates
		GROUP BY top_branch_id,version_id,artifact_id,kind,tier,content_hash,producer_agent_id,producer_name,catalog_summary,brief_summary,objective,conclusion,content,
			scope,uncertainties,conflicts,open_questions
	)
	SELECT top_branch_id::text,branch_id::text,version_id::text,artifact_id::text,kind,tier,content_hash,producer_agent_id,producer_name,added_by_event_sequence,
		catalog_summary,brief_summary,objective,conclusion,content,scope,uncertainties,conflicts,open_questions
	FROM candidates ORDER BY top_branch_id,added_by_event_sequence DESC,version_id`, workspaceID, runID)
	if err != nil {
		return V6ReportSelection{}, err
	}
	byDirection := map[string][]v6ReportCandidate{}
	for rows.Next() {
		var candidate v6ReportCandidate
		if err = rows.Scan(&candidate.directionID, &candidate.branchID, &candidate.versionID, &candidate.artifactID, &candidate.kind, &candidate.tier, &candidate.hash, &candidate.producerAgentID, &candidate.producerName, &candidate.createdOrdinal,
			&candidate.catalog, &candidate.brief, &candidate.objective, &candidate.conclusion, &candidate.content, &candidate.scope, &candidate.uncertainties, &candidate.conflicts, &candidate.openQuestions); err != nil {
			rows.Close()
			return V6ReportSelection{}, err
		}
		byDirection[candidate.directionID] = append(byDirection[candidate.directionID], candidate)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return V6ReportSelection{}, err
	}
	rows.Close()

	activeWorkByDirection := map[string]int{}
	rows, err = tx.Query(ctx, `WITH RECURSIVE root AS (
		SELECT id FROM research_branch
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
		ORDER BY (client_key='root') DESC,created_at,id LIMIT 1
	), anchors(top_branch_id,branch_id) AS (
		SELECT branch.id,branch.id FROM research_branch branch JOIN root ON branch.parent_branch_id=root.id
		WHERE branch.status<>'obsolete'
		UNION ALL
		SELECT root.id,root.id FROM root WHERE NOT EXISTS (
			SELECT 1 FROM research_branch branch WHERE branch.parent_branch_id=root.id AND branch.status<>'obsolete'
		)
	), tree(top_branch_id,branch_id) AS (
		SELECT top_branch_id,branch_id FROM anchors
		UNION ALL
		SELECT tree.top_branch_id,child.id FROM tree
		JOIN research_branch child ON child.parent_branch_id=tree.branch_id
		WHERE child.status<>'obsolete'
	)
	SELECT tree.top_branch_id::text,count(DISTINCT work.id)::int
	FROM tree
	JOIN research_v6_work_item_branch scope ON scope.workspace_id=$1::uuid
		AND scope.session_id=$2::uuid AND scope.branch_id=tree.branch_id
	JOIN research_work_item work ON work.workspace_id=scope.workspace_id
		AND work.session_id=scope.session_id AND work.id=scope.work_item_id
	WHERE work.kind NOT IN ('director','report','review')
		AND work.status IN ('ready','dispatching','enqueued','running','awaiting_input')
	GROUP BY tree.top_branch_id`, workspaceID, runID)
	if err != nil {
		return V6ReportSelection{}, err
	}
	for rows.Next() {
		var directionID string
		var count int
		if err = rows.Scan(&directionID, &count); err != nil {
			rows.Close()
			return V6ReportSelection{}, err
		}
		activeWorkByDirection[directionID] = count
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return V6ReportSelection{}, err
	}
	rows.Close()

	selection := V6ReportSelection{Inputs: []V6ReportInputRef{}, InputNodes: []V6NodeRef{}, Documents: []V6ReportInputDocument{}, Directions: []V6ReportDirectionCoverage{}, Maturity: "interim"}
	selected := map[string]int{}
	allFinal := len(directions) > 0
	for _, direction := range directions {
		candidates := byDirection[direction.id]
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := v6TierLevel(V6Tier(candidates[i].tier)), v6TierLevel(V6Tier(candidates[j].tier))
			if left != right {
				return left > right
			}
			if candidates[i].createdOrdinal != candidates[j].createdOrdinal {
				return candidates[i].createdOrdinal > candidates[j].createdOrdinal
			}
			return candidates[i].versionID < candidates[j].versionID
		})
		coverage := V6ReportDirectionCoverage{BranchID: direction.id, Objective: direction.objective, Status: "empty", CandidateCount: len(candidates), ActiveWorkCount: activeWorkByDirection[direction.id]}
		if len(candidates) == 0 {
			if direction.status == "terminated" || direction.status == "completed" {
				coverage.Status = "closed_without_result"
			} else {
				coverage.Status = "researching"
			}
			selection.Directions = append(selection.Directions, coverage)
			allFinal = false
			continue
		}
		chosen := candidates[0]
		maxRank := v6TierLevel(V6Tier(chosen.tier))
		maxima := 0
		for _, candidate := range candidates {
			if v6TierLevel(V6Tier(candidate.tier)) == maxRank {
				maxima++
			}
		}
		coverage.NodeArtifactVersionID = chosen.versionID
		coverage.Tier = V6Tier(chosen.tier)
		// Lower-tier frontier nodes are already represented by the chosen maximum.
		// Only same-tier competing maxima are unresolved report inputs.
		coverage.PendingCount = maxima - 1
		coverage.Status = "represented"
		role := "branch_maximum"
		if chosen.tier == string(V6TierXXL) && maxima == 1 {
			role = "branch_xxl"
		} else {
			allFinal = false
			if maxima > 1 {
				coverage.Status = "converging"
				role = "unresolved_gap"
			}
		}
		coverage.InputRole = role
		if existing, ok := selected[chosen.versionID]; ok {
			selection.Directions = append(selection.Directions, coverage)
			if role == "unresolved_gap" {
				selection.Inputs[existing].InputRole = role
			}
			continue
		}
		selected[chosen.versionID] = len(selection.Inputs)
		selection.Inputs = append(selection.Inputs, V6ReportInputRef{BranchID: chosen.branchID, NodeArtifactVersionID: chosen.versionID, InputRole: role, ContentHash: chosen.hash})
		node := V6NodeRef{Kind: chosen.kind, ID: chosen.artifactID, VersionID: chosen.versionID, Tier: V6Tier(chosen.tier), ContentHash: chosen.hash}
		selection.InputNodes = append(selection.InputNodes, node)
		selection.Documents = append(selection.Documents, V6ReportInputDocument{Node: node, ProducerAgentID: chosen.producerAgentID, ProducerName: chosen.producerName, Catalog: chosen.catalog, Brief: chosen.brief, Objective: chosen.objective, Conclusion: chosen.conclusion, Content: chosen.content, Scope: chosen.scope, Uncertainties: chosen.uncertainties, Conflicts: chosen.conflicts, OpenQuestions: chosen.openQuestions})
		selection.Directions = append(selection.Directions, coverage)
	}
	if allFinal {
		var activeWork, openDisputes int
		if err = tx.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM research_work_item work WHERE work.workspace_id=$1::uuid AND work.session_id=$2::uuid
			 AND work.kind NOT IN ('director','report','review') AND work.status IN ('ready','dispatching','enqueued','running','awaiting_input')),
			(SELECT count(*)::int FROM research_dispute dispute WHERE dispute.workspace_id=$1::uuid AND dispute.session_id=$2::uuid
			 AND dispute.status IN ('open','investigating','irreducible'))`, workspaceID, runID).Scan(&activeWork, &openDisputes); err != nil {
			return V6ReportSelection{}, err
		}
		if activeWork == 0 && openDisputes == 0 {
			selection.Maturity = "final"
		}
	}
	canonical, err := MarshalArtifactCanonicalJSON(map[string]any{"inputs": selection.Inputs, "directions": selection.Directions, "maturity": selection.Maturity})
	if err != nil {
		return V6ReportSelection{}, err
	}
	selection.Hash = ArtifactContentHashFromCanonicalJSON(canonical)
	return selection, nil
}

func v6ReportSelectionJSON(selection V6ReportSelection) json.RawMessage {
	raw, _ := json.Marshal(selection)
	return raw
}
