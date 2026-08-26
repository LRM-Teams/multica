package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) LoadDirectorBriefFacts(ctx context.Context, in StartV6DirectorCycleInput) (DirectorBriefFacts, error) {
	facts := DirectorBriefFacts{WorkspaceID: in.WorkspaceID, RunID: in.RunID, Team: []any{}, Branches: []any{}, TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{}, UnresolvedDisputes: []any{}, Steering: []any{}, DirectorState: "available"}
	var goal string
	var scope, sourcePolicy json.RawMessage
	var audience, freshness, language string
	var goalVersion int
	var assignmentStatus string
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,a.generation,a.status,s.state_version,COALESCE((SELECT max(sequence) FROM research_run_event e WHERE e.session_id=s.id),0),s.goal_version,s.goal,
		COALESCE(c.scope,'{}'::jsonb),COALESCE(c.audience,''),COALESCE(c.freshness,''),COALESCE(c.language,''),COALESCE(c.source_policy,'{}'::jsonb)
		FROM research_session s JOIN research_director_assignment a ON a.id=s.current_director_assignment_id
		LEFT JOIN research_contract_revision c ON c.session_id=s.id AND c.goal_version=s.goal_version
		WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND s.orchestrator_version='research-run-v6' AND a.status IN ('active','unavailable')`, in.WorkspaceID, in.RunID).Scan(&facts.AssignmentID, &facts.DirectorGeneration, &assignmentStatus, &facts.StateVersion, &facts.ThroughSequence, &goalVersion, &goal, &scope, &audience, &freshness, &language, &sourcePolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return DirectorBriefFacts{}, ErrV6DirectorUnavailable
	}
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	if facts.StateVersion != in.ExpectedStateVersion {
		return DirectorBriefFacts{}, ErrWorkItemChanged
	}
	if assignmentStatus != "active" {
		facts.DirectorState = "awaiting_director"
	}
	facts.Goal = map[string]any{"goal_version": goalVersion, "goal": goal, "scope": jsonObjectOrEmpty(scope), "audience": audience, "freshness": freshness, "language": language, "source_policy": jsonObjectOrEmpty(sourcePolicy)}
	rows, err := s.pool.Query(ctx, `SELECT m.agent_id::text,m.id::text,m.state,left(m.mission_prompt,4096),
		COALESCE((SELECT w.id::text FROM research_work_item w WHERE w.session_id=m.session_id AND w.assigned_agent_id=m.agent_id AND w.status IN ('ready','dispatching','running','awaiting_input') ORDER BY w.priority DESC,w.created_at LIMIT 1),''),
		(SELECT count(*)::int FROM research_node_steward_assignment n WHERE n.session_id=m.session_id AND n.agent_id=m.agent_id AND n.status='active')
		FROM research_team_membership m WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid ORDER BY m.joined_at,m.id`, in.WorkspaceID, in.RunID)
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	for rows.Next() {
		var agentID, membershipID, state, mission, activeWork string
		var stewardCount int
		if err = rows.Scan(&agentID, &membershipID, &state, &mission, &activeWork, &stewardCount); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		item := map[string]any{"agent_id": agentID, "membership_id": membershipID, "state": state, "mission_summary": truncateV6BriefText(mission, 512), "steward_node_count": stewardCount}
		if activeWork != "" {
			item["active_work_item_id"] = activeWork
		}
		facts.Team = append(facts.Team, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return DirectorBriefFacts{}, err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id::text,COALESCE(parent_branch_id::text,''),objective,scope,status,state_version FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY created_at,id`, in.WorkspaceID, in.RunID)
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	type branchBrief struct {
		id, parent, objective, status string
		scope                         json.RawMessage
		version                       int64
	}
	branches := []branchBrief{}
	for rows.Next() {
		var branch branchBrief
		if err = rows.Scan(&branch.id, &branch.parent, &branch.objective, &branch.scope, &branch.status, &branch.version); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		branches = append(branches, branch)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return DirectorBriefFacts{}, err
	}
	rows.Close()
	for _, branch := range branches {
		item := map[string]any{"branch": map[string]any{"id": branch.id, "state_version": branch.version}, "objective": branch.objective, "scope": jsonObjectOrEmpty(branch.scope), "status": branch.status, "frontier_nodes": []any{}, "has_more": false}
		frontier, hasMore, frontierErr := s.loadV6BranchFrontierBrief(ctx, in.WorkspaceID, in.RunID, branch.id)
		if frontierErr != nil {
			return DirectorBriefFacts{}, frontierErr
		}
		item["frontier_nodes"] = frontier
		item["has_more"] = hasMore
		if hasMore {
			item["next_cursor"] = "64"
		}
		if branch.parent != "" {
			item["parent_branch_id"] = branch.parent
		}
		facts.Branches = append(facts.Branches, item)
	}
	rows, err = s.pool.Query(ctx, `SELECT w.id::text,w.kind,w.status,
		COALESCE(NULLIF(left(w.reason,512),''),NULLIF(w.target_kind,''),w.kind),w.updated_at,
		COALESCE(w.assigned_agent_id::text,''),w.attempt_count,w.max_attempts,
		COALESCE(latest_attempt.status,''),COALESCE(latest_attempt.failure_class,''),COALESCE(left(latest_attempt.diagnostics,32768),''),
		COALESCE(w.terminal_reason_code,''),COALESCE(left(w.terminal_reason_detail,32768),''),
		COALESCE((SELECT array_agg(b.branch_id::text ORDER BY b.branch_id) FROM research_v6_work_item_branch b WHERE b.workspace_id=w.workspace_id AND b.session_id=w.session_id AND b.work_item_id=w.id),'{}')
		FROM research_work_item w
		LEFT JOIN LATERAL (
			SELECT attempt.status,attempt.failure_class,attempt.diagnostics
			FROM research_work_item_attempt attempt
			WHERE attempt.workspace_id=w.workspace_id AND attempt.session_id=w.session_id AND attempt.work_item_id=w.id
			ORDER BY attempt.attempt_number DESC LIMIT 1
		) latest_attempt ON true
		WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.kind IN ('research','match','discussion','integration','director','report','review') ORDER BY w.updated_at,w.id LIMIT 512`, in.WorkspaceID, in.RunID)
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	for rows.Next() {
		var id, kind, state, summary, agentID, attemptState, failureClass, failureDiagnostics, terminalReasonCode, terminalReasonDetail string
		var attemptCount, maxAttempts int
		var branchIDs []string
		var updated time.Time
		if err = rows.Scan(&id, &kind, &state, &summary, &updated, &agentID, &attemptCount, &maxAttempts, &attemptState, &failureClass, &failureDiagnostics, &terminalReasonCode, &terminalReasonDetail, &branchIDs); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		state = directorBriefWorkState(state)
		summary = directorBriefWorkSummary(summary, state, attemptCount, maxAttempts, attemptState, failureClass, failureDiagnostics, terminalReasonCode, terminalReasonDetail)
		item := map[string]any{"id": id, "kind": kind, "state": state, "summary": summary, "updated_at": updated.UTC().Format(time.RFC3339Nano)}
		if agentID != "" {
			item["assigned_agent_id"] = agentID
		}
		facts.WorkItems = append(facts.WorkItems, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return DirectorBriefFacts{}, err
	}
	rows.Close()
	if err = s.loadV6DirectorControlFacts(ctx, in.WorkspaceID, in.RunID, &facts); err != nil {
		return DirectorBriefFacts{}, err
	}
	rows, err = s.pool.Query(ctx, `SELECT t.event_sequence,m.body,t.selected_refs
		FROM research_v6_steering_trigger t JOIN research_message m ON m.id=t.research_message_id
		LEFT JOIN research_steering_assessment a ON a.research_message_id=t.research_message_id
		WHERE t.workspace_id=$1::uuid AND t.session_id=$2::uuid AND a.id IS NULL
		ORDER BY t.event_sequence,t.id LIMIT 64`, in.WorkspaceID, in.RunID)
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	for rows.Next() {
		var sequence int64
		var body string
		var selected json.RawMessage
		if err = rows.Scan(&sequence, &body, &selected); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		var refs []map[string]any
		_ = json.Unmarshal(selected, &refs)
		briefRefs := make([]any, 0, len(refs))
		for _, ref := range refs {
			briefRef := map[string]any{"kind": ref["kind"], "id": ref["entity_id"], "revision": ref["revision"], "content_hash": ref["content_hash"]}
			briefRefs = append(briefRefs, briefRef)
		}
		facts.Steering = append(facts.Steering, map[string]any{"from_sequence": sequence, "through_sequence": sequence,
			"summary": truncateV6BriefText(body, 4096), "affected_refs": briefRefs})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return DirectorBriefFacts{}, err
	}
	rows.Close()
	return facts, nil
}

func directorBriefWorkSummary(mission, state string, attemptCount, maxAttempts int, attemptState, failureClass, failureDiagnostics, terminalReasonCode, terminalReasonDetail string) string {
	parts := []string{strings.TrimSpace(mission)}
	if state == "failed" || failureClass != "" || terminalReasonCode != "" {
		parts = append(parts, fmt.Sprintf("尝试 %d/%d", attemptCount, maxAttempts))
		if attemptState != "" {
			parts = append(parts, "最近尝试状态 "+attemptState)
		}
		if failureClass != "" {
			parts = append(parts, "失败分类 "+failureClass)
		}
		if failureDiagnostics != "" {
			parts = append(parts, "失败诊断 "+failureDiagnostics)
		}
		if terminalReasonCode != "" {
			parts = append(parts, "终止原因 "+terminalReasonCode)
		}
		if terminalReasonDetail != "" && terminalReasonDetail != failureDiagnostics {
			parts = append(parts, terminalReasonDetail)
		}
	}
	return truncateV6BriefText(strings.Join(parts, "；"), 512)
}

func (s *PostgresStore) loadV6BranchFrontierBrief(ctx context.Context, workspaceID, runID, branchID string) ([]any, bool, error) {
	rows, err := s.pool.Query(ctx, `WITH frontier_content AS (
		SELECT rn.artifact_version_id,'result_s'::text AS node_kind,'S'::text AS tier,rn.catalog_summary,rn.brief_summary,rn.open_questions,
			rn.conclusion_state,
			CASE rn.integration_state WHEN 'candidate' THEN 'candidate' WHEN 'discussing' THEN 'discussing' WHEN 'excluded' THEN 'excluded' WHEN 'absorbed' THEN 'excluded' ELSE 'unmatched' END AS integration_state,
			rn.accepted_at AS content_created_at,rn.id AS content_id
		FROM research_result_node rn WHERE rn.workspace_id=$1::uuid AND rn.session_id=$2::uuid
		UNION ALL
		SELECT iv.artifact_version_id,'insight'::text,iv.tier,iv.catalog_summary,iv.brief_summary,iv.open_questions,
			CASE iv.status WHEN 'accepted' THEN 'accepted' WHEN 'challenged' THEN 'challenged' WHEN 'refuted' THEN 'refuted' ELSE 'invalid' END,
			CASE WHEN iv.status IN ('refuted','invalid','terminal') THEN 'excluded' WHEN iv.discussion_id IS NOT NULL THEN 'discussing' WHEN iv.integration_round_id IS NOT NULL THEN 'candidate' ELSE 'unmatched' END,
			iv.created_at,iv.id
		FROM research_insight_version iv WHERE iv.workspace_id=$1::uuid AND iv.session_id=$2::uuid
	)
		SELECT v.id::text,v.artifact_id::text,v.content_hash,content.node_kind,content.tier,content.catalog_summary,content.brief_summary,content.open_questions,
		COALESCE((SELECT steward.agent_id::text FROM research_node_steward_assignment steward WHERE steward.session_id=f.session_id AND steward.node_artifact_version_id=f.node_artifact_version_id AND steward.status='active' ORDER BY steward.generation DESC LIMIT 1),
		         (SELECT assignment.director_agent_id::text FROM research_session session JOIN research_director_assignment assignment ON assignment.id=session.current_director_assignment_id WHERE session.id=f.session_id)),
		content.conclusion_state,content.integration_state,
		COALESCE((SELECT array_agg(DISTINCT binding.branch_id::text ORDER BY binding.branch_id::text) FROM research_node_branch binding WHERE binding.session_id=f.session_id AND binding.node_artifact_version_id=f.node_artifact_version_id),'{}')
		FROM research_branch_frontier f JOIN research_artifact_version v ON v.id=f.node_artifact_version_id
		JOIN frontier_content content ON content.artifact_version_id=v.id
		WHERE f.workspace_id=$1::uuid AND f.session_id=$2::uuid AND f.branch_id=$3::uuid AND f.removed_by_event_sequence IS NULL
		ORDER BY CASE content.tier WHEN 'XXL' THEN 5 WHEN 'XL' THEN 4 WHEN 'L' THEN 3 WHEN 'M' THEN 2 ELSE 1 END DESC,content.content_created_at DESC,content.content_id LIMIT 65`, workspaceID, runID, branchID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	frontier := []any{}
	for rows.Next() {
		var versionID, artifactID, contentHash, nodeKind, tier, catalog, brief, steward, conclusion, integration string
		var openQuestions json.RawMessage
		var branchIDs []string
		if err = rows.Scan(&versionID, &artifactID, &contentHash, &nodeKind, &tier, &catalog, &brief, &openQuestions, &steward, &conclusion, &integration, &branchIDs); err != nil {
			return nil, false, err
		}
		if len(frontier) == 64 {
			return frontier, true, nil
		}
		frontier = append(frontier, map[string]any{
			"node":            map[string]any{"kind": nodeKind, "id": artifactID, "version_id": versionID, "tier": tier, "content_hash": contentHash},
			"catalog_summary": catalog, "brief_summary": directorBriefFrontierSummary(brief, openQuestions), "steward_agent_id": steward, "branch_ids": branchIDs,
			"conclusion_state": conclusion, "integration_state": integration,
		})
	}
	return frontier, false, rows.Err()
}

const directorBriefOpenQuestionsMarker = "待回答问题："

func directorBriefFrontierSummary(summary string, rawOpenQuestions json.RawMessage) string {
	base := truncateV6BriefText(summary, 32768)
	var questions []string
	if json.Unmarshal(rawOpenQuestions, &questions) != nil {
		return base
	}
	clean := make([]string, 0, len(questions))
	for _, question := range questions {
		if question = strings.TrimSpace(question); question != "" {
			clean = append(clean, question)
		}
	}
	if len(clean) == 0 {
		return base
	}
	questionText := truncateV6BriefText(strings.Join(clean, "\n- "), 8192)
	questionBlock := directorBriefOpenQuestionsMarker + "\n- " + questionText
	baseLimit := 32768 - len([]rune(questionBlock)) - 2
	if baseLimit < 1 {
		return truncateV6BriefText(questionBlock, 32768)
	}
	return truncateV6BriefText(base, baseLimit) + "\n\n" + questionBlock
}

func (s *PostgresStore) loadV6DirectorControlFacts(ctx context.Context, workspaceID, runID string, facts *DirectorBriefFacts) error {
	rows, err := s.pool.Query(ctx, `SELECT discussion.id::text,discussion.kind,discussion.status,
		left(format('讨论类型：%s；输入版本：%s；最新投票：%s；可见结论：%s',
			discussion.kind,COALESCE(inputs.summary,'无'),COALESCE(votes.summary,'无'),COALESCE(turns.summary,'无')),8192),
		discussion.updated_at
		FROM research_discussion discussion
		LEFT JOIN LATERAL (
			SELECT string_agg(input.node_artifact_version_id::text,', ' ORDER BY input.ordinal) AS summary
			FROM research_discussion_input input
			WHERE input.workspace_id=discussion.workspace_id AND input.session_id=discussion.session_id
			  AND input.discussion_id=discussion.id
		) inputs ON true
		LEFT JOIN LATERAL (
			SELECT string_agg(latest.vote||'：'||left(latest.reason,512),' | ' ORDER BY latest.agent_id::text) AS summary
			FROM (
				SELECT DISTINCT ON (vote.agent_id) vote.agent_id,vote.vote,vote.reason
				FROM research_discussion_vote vote
				WHERE vote.workspace_id=discussion.workspace_id AND vote.session_id=discussion.session_id
				  AND vote.discussion_id=discussion.id AND vote.discussion_revision=discussion.revision
				ORDER BY vote.agent_id,vote.created_at DESC,vote.id DESC
			) latest
		) votes ON true
		LEFT JOIN LATERAL (
			SELECT string_agg(left(turn.visible_message,1024),' | ' ORDER BY turn.ordinal) AS summary
			FROM research_discussion_turn turn
			WHERE turn.workspace_id=discussion.workspace_id AND turn.session_id=discussion.session_id
			  AND turn.discussion_id=discussion.id AND turn.discussion_revision=discussion.revision
		) turns ON true
		WHERE discussion.workspace_id=$1::uuid AND discussion.session_id=$2::uuid
		ORDER BY discussion.updated_at DESC,discussion.id LIMIT 256`, workspaceID, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind, state, summary string
		var updated time.Time
		if err = rows.Scan(&id, &kind, &state, &summary, &updated); err != nil {
			rows.Close()
			return err
		}
		facts.Discussions = append(facts.Discussions, map[string]any{"id": id, "kind": "discussion", "state": directorBriefControlState(state), "summary": summary, "updated_at": updated.UTC().Format(time.RFC3339Nano)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id::text,status,COALESCE(NULLIF(title,''),'Research report'),updated_at FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY revision DESC,id LIMIT 128`, workspaceID, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, state, summary string
		var updated time.Time
		if err = rows.Scan(&id, &state, &summary, &updated); err != nil {
			rows.Close()
			return err
		}
		facts.Reports = append(facts.Reports, map[string]any{"id": id, "kind": "report", "state": directorBriefControlState(state), "summary": summary, "updated_at": updated.UTC().Format(time.RFC3339Nano)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id::text,status,resolution_request,updated_at FROM research_dispute WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND status IN ('open','investigating','irreducible') ORDER BY severity DESC,materiality DESC,updated_at DESC LIMIT 256`, workspaceID, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, state, summary string
		var updated time.Time
		if err = rows.Scan(&id, &state, &summary, &updated); err != nil {
			rows.Close()
			return err
		}
		facts.UnresolvedDisputes = append(facts.UnresolvedDisputes, map[string]any{"id": id, "kind": "dispute", "state": directorBriefControlState(state), "summary": truncateV6BriefText(summary, 512), "updated_at": updated.UTC().Format(time.RFC3339Nano)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT nb.branch_id::text,COALESCE(NULLIF(rn.reason_code,''),'other'),left(COALESCE(NULLIF(rn.reason_detail,''),rn.catalog_summary),512),count(*)::int
		FROM research_result_node rn JOIN research_node_branch nb ON nb.session_id=rn.session_id AND nb.node_artifact_version_id=rn.artifact_version_id
		WHERE rn.workspace_id=$1::uuid AND rn.session_id=$2::uuid AND (rn.reason_code<>'' OR rn.conclusion_state IN ('refuted','invalid'))
		GROUP BY nb.branch_id,COALESCE(NULLIF(rn.reason_code,''),'other'),left(COALESCE(NULLIF(rn.reason_detail,''),rn.catalog_summary),512) ORDER BY nb.branch_id LIMIT 256`, workspaceID, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var branchID, reason, summary string
		var count int
		if err = rows.Scan(&branchID, &reason, &summary, &count); err != nil {
			rows.Close()
			return err
		}
		facts.TerminalSummaries = append(facts.TerminalSummaries, map[string]any{"branch_id": branchID, "reason_code": normalizeProjectionReason(reason), "summary": summary, "count": count})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func directorBriefControlState(state string) string {
	switch state {
	case "active", "draft", "proposed", "open", "investigating":
		return "running"
	case "completed", "published", "consensus_accept", "consensus_reject":
		return "succeeded"
	case "needs_research", "needs_revision", "uncertain", "escalated", "irreducible":
		return "awaiting_input"
	case "technical_failure":
		return "failed"
	case "stale_input", "obsolete":
		return "stale"
	default:
		return "pending"
	}
}

func truncateV6BriefText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func jsonObjectOrEmpty(raw json.RawMessage) any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return map[string]any{}
	}
	return value
}
func directorBriefWorkState(state string) string {
	switch state {
	case "dispatching", "enqueued":
		return "running"
	case "done":
		return "succeeded"
	default:
		return state
	}
}

func (s *PostgresStore) PersistDirectorCycle(ctx context.Context, in StartV6DirectorCycleInput, brief CompiledDirectorBrief) (V6DirectorCycle, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorCycleCreate, pgx.TxOptions{})
	if err != nil {
		return V6DirectorCycle{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6DirectorCycle{}, err
	}
	var assignmentID, agentID, membershipID string
	var generation, goalVersion int
	var stateVersion, eventSequence int64
	err = tx.QueryRow(ctx, `SELECT a.id::text,a.director_agent_id::text,a.generation,s.goal_version,s.state_version,COALESCE((SELECT max(sequence) FROM research_run_event e WHERE e.session_id=s.id),0),
		(SELECT id::text FROM research_team_membership m WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id AND m.agent_id=a.director_agent_id AND m.state IN ('idle','working','offline','retiring') ORDER BY membership_generation DESC LIMIT 1)
		FROM research_session s JOIN research_director_assignment a ON a.id=s.current_director_assignment_id WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND a.status='active'`, in.WorkspaceID, in.RunID).Scan(&assignmentID, &agentID, &generation, &goalVersion, &stateVersion, &eventSequence, &membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6DirectorCycle{}, ErrV6DirectorUnavailable
	}
	if err != nil {
		return V6DirectorCycle{}, err
	}
	idempotency := "director-cycle:" + in.TriggerKey
	var replay V6DirectorCycle
	err = tx.QueryRow(ctx, `SELECT c.id::text,c.session_id::text,c.director_assignment_id::text,c.brief_id::text,c.brief_hash,
		c.work_item_id::text,c.director_generation,c.page_count,c.state_version,c.status
		FROM research_director_cycle c JOIN research_work_item w ON w.id=c.work_item_id
		WHERE c.workspace_id=$1::uuid AND c.session_id=$2::uuid AND w.client_key=$3`, in.WorkspaceID, in.RunID, idempotency).
		Scan(&replay.ID, &replay.RunID, &replay.AssignmentID, &replay.BriefID, &replay.BriefHash, &replay.WorkItemID,
			&replay.Generation, &replay.PageCount, &replay.StateVersion, &replay.Status)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6DirectorCycle{}, err
	}
	if stateVersion != in.ExpectedStateVersion || eventSequence != in.ThroughSequence {
		return V6DirectorCycle{}, ErrWorkItemChanged
	}
	cycle := V6DirectorCycle{ID: uuid.NewString(), RunID: in.RunID, AssignmentID: assignmentID, BriefID: brief.BriefID, BriefHash: brief.BriefHash, Generation: generation, PageCount: len(brief.Pages), StateVersion: 1, Status: "pending", WorkItemID: uuid.NewString()}
	directorPayload, err := json.Marshal(map[string]any{"brief_id": brief.BriefID, "brief_hash": brief.BriefHash,
		"mission_prompt":       "研读最新调研简报，规划并派发下一步调研工作。",
		"task_specific_schema": map[string]any{"payload_schemas": v6DirectorActionPayloadSchemas()}})
	if err != nil {
		return V6DirectorCycle{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,target_id,client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,assigned_agent_id,priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,'director','ready','director_cycle',$4::uuid,$5,$5,$6,$7,$8,$9::uuid,1,3,'director.action.registry.v1','director_action_proposal',$10::jsonb,1,now())`, cycle.WorkItemID, in.WorkspaceID, in.RunID, cycle.ID, idempotency, goalVersion, stateVersion, eventSequence, agentID, directorPayload)
	if err != nil {
		return V6DirectorCycle{}, err
	}
	manifest := make([]map[string]any, len(brief.Pages))
	for i := range brief.Pages {
		manifest[i] = map[string]any{"page_key": brief.PageKeys[i], "page_hash": brief.PageHashes[i]}
	}
	manifestRaw, _ := json.Marshal(manifest)
	_, err = tx.Exec(ctx, `INSERT INTO research_director_cycle(id,workspace_id,session_id,director_assignment_id,director_generation,work_item_id,trigger_from_sequence,trigger_through_sequence,brief_id,brief_hash,status,page_count,brief_manifest)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9::uuid,$10,'pending',$11,$12::jsonb)`, cycle.ID, in.WorkspaceID, in.RunID, assignmentID, generation, cycle.WorkItemID, in.FromSequence, in.ThroughSequence, brief.BriefID, brief.BriefHash, len(brief.Pages), manifestRaw)
	if err != nil {
		return V6DirectorCycle{}, err
	}
	for i, raw := range brief.Pages {
		_, err = tx.Exec(ctx, `INSERT INTO research_director_brief_page(id,workspace_id,session_id,director_cycle_id,page_kind,brief_id,brief_hash,page_key,ordinal,through_event_sequence,content_bytes,content_hash)
		VALUES(gen_random_uuid(),$1::uuid,$2::uuid,$3::uuid,'overview',$4::uuid,$5,$6,$7,$8,$9,$10)`, in.WorkspaceID, in.RunID, cycle.ID, brief.BriefID, brief.BriefHash, brief.PageKeys[i], i, in.ThroughSequence, []byte(raw), brief.PageHashes[i])
		if err != nil {
			return V6DirectorCycle{}, err
		}
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_director_cycle_created", idempotency, "system", "", map[string]any{"cycle_id": cycle.ID, "work_item_id": cycle.WorkItemID, "brief_id": brief.BriefID, "brief_hash": brief.BriefHash, "page_count": len(brief.Pages)}); err != nil {
		return V6DirectorCycle{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6DirectorCycleCreate, tx); err != nil {
		return V6DirectorCycle{}, err
	}
	_ = membershipID
	return cycle, nil
}

func v6DirectorActionPayloadSchemas() map[string]any {
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 32768}
	ref := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "id"}, "properties": map[string]any{
		"kind": map[string]any{"type": "string"}, "id": map[string]any{"type": "string", "format": "uuid"},
		"expected_state_version": map[string]any{"type": "integer", "minimum": 0}, "disposition": map[string]any{"type": "string"}, "reason": text}}
	uuidValue := map[string]any{"type": "string", "format": "uuid"}
	hashValue := map[string]any{"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"}
	jsonObject := map[string]any{"type": "object"}
	nonEmptyJSONObject := map[string]any{"type": "object", "minProperties": 1}
	atomicWorkConfig := map[string]any{"type": "object", "required": []string{"task_specific_schema"}, "properties": map[string]any{
		"task_specific_schema": nonEmptyJSONObject}}
	nodeRef := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "id", "version_id", "tier", "content_hash"}, "properties": map[string]any{
		"kind": map[string]any{"enum": []string{"result_s", "insight"}}, "id": uuidValue, "version_id": uuidValue,
		"tier": map[string]any{"enum": []string{"S", "M", "L", "XL", "XXL"}}, "content_hash": hashValue}}
	branchRef := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "state_version"}, "properties": map[string]any{
		"id": uuidValue, "state_version": map[string]any{"type": "integer", "minimum": 0}}}
	return map[string]any{
		"no_op.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"reason"}, "properties": map[string]any{"message_id": map[string]any{"type": "string", "format": "uuid"}, "reason": text}},
		"steering_assessment.v1": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"message_id", "assessment_kind", "interpretation", "reason", "impacts"}, "properties": map[string]any{
				"message_id": map[string]any{"type": "string", "format": "uuid"}, "assessment_kind": map[string]any{"enum": []string{"no_op", "local_change", "goal_revision", "full_reassessment"}},
				"interpretation": text, "reason": text, "impacts": map[string]any{"type": "array", "maxItems": 512, "items": ref},
				"revised_goal": text, "revised_scope": map[string]any{"type": "object"}, "revised_audience": map[string]any{"type": "string"},
				"revised_freshness": map[string]any{"type": "string"}, "revised_language": map[string]any{"type": "string"},
				"revised_source_policy": map[string]any{"type": "object"}, "revised_limits": map[string]any{"type": "object"}}},
		"agent.create.v1": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"name", "capability", "mission_prompt", "model_config", "tool_config", "permission_config"}, "properties": map[string]any{
				"name": text, "capability": text, "mission_prompt": text, "capacity_reason": text,
				"model_config": jsonObject, "tool_config": jsonObject, "permission_config": jsonObject}},
		"work.create.v1": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"kind", "assignee_agent_id", "mission", "expected_result_schema_id", "payload_schema_id", "payload", "priority", "max_attempts", "branch_ids"}, "properties": map[string]any{
				"kind": map[string]any{"const": "research"}, "assignee_agent_id": uuidValue, "mission": text,
				"expected_result_schema_id": map[string]any{"const": "atomic_result_submission"}, "payload_schema_id": map[string]any{"type": "string", "pattern": "^research\\.[A-Za-z0-9._-]+$"}, "payload": atomicWorkConfig,
				"priority": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"branch_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 1, "uniqueItems": true, "items": uuidValue}}},
		"collaboration.create.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"assignee_agent_id", "mission", "expected_result_schema_id", "payload_schema_id", "payload", "priority", "max_attempts"}, "properties": map[string]any{
			"kind": map[string]any{"type": "string"}, "assignee_agent_id": uuidValue, "mission": text, "expected_result_schema_id": map[string]any{"type": "string"}, "payload_schema_id": map[string]any{"type": "string"}, "payload": jsonObject,
			"priority": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "branch_ids": map[string]any{"type": "array", "maxItems": 128, "items": uuidValue}}},
		"integration.create.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"inputs", "branch_refs"}, "properties": map[string]any{
			"inputs":      map[string]any{"type": "array", "minItems": 2, "maxItems": 256, "items": nodeRef},
			"branch_refs": map[string]any{"type": "array", "minItems": 1, "maxItems": 128, "items": branchRef}}},
		"branch.create.v1": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"objective", "scope", "budget_share"}, "properties": map[string]any{
				"objective": text, "scope": jsonObject, "budget_share": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "parent_branch_id": uuidValue}},
		"report.create.v1": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"assignee_agent_id", "title", "inputs"}, "properties": map[string]any{
				"assignee_agent_id": uuidValue, "title": text, "inputs": map[string]any{"type": "array", "minItems": 1, "maxItems": 1024, "items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"branch_id", "node_artifact_version_id", "input_role", "content_hash"}, "properties": map[string]any{
						"branch_id": uuidValue, "node_artifact_version_id": uuidValue, "input_role": map[string]any{"enum": []string{"branch_xxl", "branch_maximum", "unresolved_gap"}}, "content_hash": hashValue}}}}},
		"target.action.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"target_id"}, "properties": map[string]any{
			"target_id": uuidValue, "assignee_agent_id": uuidValue, "mission": text, "scope": jsonObject, "reason": text}},
		"agent.action.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"agent_id"}, "properties": map[string]any{
			"agent_id": uuidValue, "mission_prompt": text, "model_config": jsonObject, "tool_config": jsonObject, "permission_config": jsonObject, "reason": text}},
		"run.action.v1":    map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"reason": text}},
		"report.review.v1": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"report_id", "expected_revision", "reason"}, "properties": map[string]any{"report_id": uuidValue, "expected_revision": map[string]any{"type": "integer", "minimum": 1}, "reason": text}},
		"goal.revise.v1":   map[string]any{"type": "object", "additionalProperties": false, "required": []string{"goal", "scope"}, "properties": map[string]any{"goal": text, "scope": jsonObject, "audience": text, "freshness": text, "language": text, "source_policy": jsonObject}},
	}
}

func (s *PostgresStore) LoadDirectorBriefPage(ctx context.Context, access V6AttemptAccess, cursor string) (V6DirectorBriefPage, error) {
	var page V6DirectorBriefPage
	var raw []byte
	ordinal := -1
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return page, ErrInvalidContract
		}
		ordinal = parsed
	}
	err := s.pool.QueryRow(ctx, `SELECT p.content_bytes,p.page_key,p.content_hash,p.brief_hash,p.ordinal,c.page_count,p.reviewed_at IS NOT NULL
		FROM research_work_item_attempt a JOIN research_director_cycle c ON c.work_item_id=a.work_item_id
		JOIN research_director_brief_page p ON p.director_cycle_id=c.id JOIN research_team_membership m ON m.id=a.membership_id
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid
		AND ($6='' OR a.inbox_task_id=$6::uuid) AND ($7<0 OR p.ordinal=$7)
		ORDER BY (p.reviewed_at IS NOT NULL),p.ordinal LIMIT 1`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID, ordinal).Scan(&raw, &page.PageKey, &page.PageHash, &page.BriefHash, &page.Ordinal, &page.PageCount, &page.Reviewed)
	if errors.Is(err, pgx.ErrNoRows) {
		return page, ErrRunNotFound
	}
	page.Bytes = json.RawMessage(raw)
	return page, err
}

func (s *PostgresStore) AcknowledgeDirectorBriefPage(ctx context.Context, in AcknowledgeV6DirectorBriefInput) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorBriefAck, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE research_director_brief_page p SET reviewed_at=COALESCE(reviewed_at,now()),review_request_id=COALESCE(review_request_id,$10::uuid)
		FROM research_director_cycle c,research_work_item_attempt a,research_team_membership m WHERE p.director_cycle_id=c.id AND c.work_item_id=a.work_item_id AND a.membership_id=m.id
		AND p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid
		AND p.brief_id=$6::uuid AND p.brief_hash=$7 AND p.page_key=$8 AND p.content_hash=$9 AND ($11='' OR a.inbox_task_id=$11::uuid)`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID, in.AgentID, in.BriefID, in.BriefHash, in.PageKey, in.PageHash, in.ClientRequestID, in.InboxTaskID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrResultConflict
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_director_brief_page_acknowledged", "v6-brief-ack:"+in.ClientRequestID, "director", in.AgentID, map[string]any{"brief_id": in.BriefID, "page_key": in.PageKey, "page_hash": in.PageHash}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorBriefAck, tx)
}
