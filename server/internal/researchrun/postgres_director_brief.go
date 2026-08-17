package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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
		item := map[string]any{"agent_id": agentID, "membership_id": membershipID, "state": state, "mission_summary": mission, "steward_node_count": stewardCount}
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
	for rows.Next() {
		var id, parent, objective, status string
		var branchScope json.RawMessage
		var version int64
		if err = rows.Scan(&id, &parent, &objective, &branchScope, &status, &version); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		item := map[string]any{"branch": map[string]any{"id": id, "state_version": version}, "objective": objective, "scope": jsonObjectOrEmpty(branchScope), "status": status, "frontier_nodes": []any{}, "has_more": false}
		if parent != "" {
			item["parent_branch_id"] = parent
		}
		facts.Branches = append(facts.Branches, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return DirectorBriefFacts{}, err
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id::text,kind,status,COALESCE(NULLIF(target_kind,''),kind),updated_at,COALESCE(assigned_agent_id::text,'') FROM research_work_item WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND kind IN ('research','match','discussion','integration','director','report','review') ORDER BY updated_at,id LIMIT 512`, in.WorkspaceID, in.RunID)
	if err != nil {
		return DirectorBriefFacts{}, err
	}
	for rows.Next() {
		var id, kind, state, summary, agentID string
		var updated time.Time
		if err = rows.Scan(&id, &kind, &state, &summary, &updated, &agentID); err != nil {
			rows.Close()
			return DirectorBriefFacts{}, err
		}
		state = directorBriefWorkState(state)
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
	return facts, nil
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
	if stateVersion != in.ExpectedStateVersion || eventSequence != in.ThroughSequence {
		return V6DirectorCycle{}, ErrWorkItemChanged
	}
	cycle := V6DirectorCycle{ID: uuid.NewString(), RunID: in.RunID, AssignmentID: assignmentID, BriefID: brief.BriefID, BriefHash: brief.BriefHash, Generation: generation, PageCount: len(brief.Pages), StateVersion: 1, Status: "pending", WorkItemID: uuid.NewString()}
	idempotency := "director-cycle:" + in.TriggerKey
	_, err = tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,target_id,client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,assigned_agent_id,priority,max_attempts,payload_schema_id,payload,state_version,ready_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,'director','ready','director_cycle',$4::uuid,$5,$5,$6,$7,$8,$9::uuid,1,3,'director_action_proposal',jsonb_build_object('brief_id',$10,'brief_hash',$11),1,now())`, cycle.WorkItemID, in.WorkspaceID, in.RunID, cycle.ID, idempotency, goalVersion, stateVersion, eventSequence, agentID, brief.BriefID, brief.BriefHash)
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
		AND ($6='' OR a.inbox_task_id=$6::uuid) AND (($7<0 AND p.reviewed_at IS NULL) OR p.ordinal=$7) ORDER BY p.ordinal LIMIT 1`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID, ordinal).Scan(&raw, &page.PageKey, &page.PageHash, &page.BriefHash, &page.Ordinal, &page.PageCount, &page.Reviewed)
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
