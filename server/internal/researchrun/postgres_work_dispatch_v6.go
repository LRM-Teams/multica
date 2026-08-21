package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) PrepareV6Dispatches(ctx context.Context, limit int) (int, error) {
	prepared := 0
	for prepared < limit {
		ok, err := s.prepareNextV6Dispatch(ctx)
		if err != nil || !ok {
			return prepared, err
		}
		prepared++
	}
	return prepared, nil
}

func (s *PostgresStore) prepareNextV6Dispatch(ctx context.Context) (bool, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6DispatchPrepare, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var workspaceID, runID, workItemID, agentID, membershipID, mission, expectedSchema string
	var goalVersion int
	var stateVersion, throughSequence int64
	var payload json.RawMessage
	err = tx.QueryRow(ctx, `SELECT w.workspace_id::text,w.session_id::text,w.id::text,w.assigned_agent_id::text,m.id::text,
		COALESCE(NULLIF(w.payload->>'mission_prompt',''),m.mission_prompt),w.expected_result_schema_id,w.goal_version,s.state_version,w.input_event_sequence,w.payload
		FROM research_work_item w JOIN research_session s ON s.id=w.session_id
		JOIN research_team_membership m ON m.workspace_id=w.workspace_id AND m.session_id=w.session_id AND m.agent_id=w.assigned_agent_id
		AND m.state IN('idle','working') WHERE s.orchestrator_version='research-run-v6' AND s.status='running' AND w.status='ready' AND w.ready_at<=now()
		AND w.attempt_count<w.max_attempts ORDER BY w.priority DESC,w.ready_at,w.id FOR UPDATE OF s,w,m SKIP LOCKED LIMIT 1`).Scan(
		&workspaceID, &runID, &workItemID, &agentID, &membershipID, &mission, &expectedSchema, &goalVersion, &stateVersion, &throughSequence, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = lockRunForMutation(ctx, tx, runID, workspaceID); err != nil {
		return false, err
	}
	if expectedSchema == string(V6ContractAtomicResultSubmission) {
		if _, err = ensureV6BackingTaskTx(ctx, tx, workItemID); err != nil {
			return false, err
		}
	}
	attemptID, manifestID := uuid.NewString(), uuid.NewString()
	manifest, manifestHash, err := compileV6WorkManifestTx(ctx, tx, workspaceID, runID, workItemID, attemptID, manifestID, agentID, mission, expectedSchema, goalVersion, stateVersion, throughSequence, payload)
	if err != nil {
		return false, err
	}
	var attemptNumber int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(attempt_number),0)+1 FROM research_work_item_attempt WHERE work_item_id=$1::uuid`, workItemID).Scan(&attemptNumber); err != nil {
		return false, err
	}
	dispatchKey := "v6-dispatch:" + attemptID
	if _, err = tx.Exec(ctx, `INSERT INTO research_work_item_attempt(id,workspace_id,session_id,work_item_id,attempt_number,assigned_agent_id,membership_id,dispatch_key,manifest_id,manifest_hash,status,manifest)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8,$9::uuid,$10,'dispatching',$11::jsonb)`, attemptID, workspaceID, runID, workItemID, attemptNumber, agentID, membershipID, dispatchKey, manifestID, manifestHash, manifest); err != nil {
		return false, err
	}
	attemptHash, err := ArtifactContentHash(ArtifactKindAttempt, v6WorkAttemptArtifactContent(
		workItemID, attemptNumber, agentID, membershipID, dispatchKey, manifestID, manifestHash,
	))
	if err != nil {
		return false, err
	}
	goalVersion32 := int32(goalVersion)
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: runID, EntityID: attemptID,
		Kind: ArtifactKindAttempt, ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion: &goalVersion32, SchemaName: string(ArtifactKindAttempt),
		SchemaVersion: OrchestratorVersionV6, AccessLevel: ArtifactAccessRaw,
		HashOrigin: ArtifactHashOriginProduction, ContentHash: attemptHash,
	}); err != nil {
		return false, err
	}
	if expectedSchema == string(V6ContractAtomicResultSubmission) {
		if err = persistV6CatalogPagesTx(ctx, tx, workspaceID, runID, attemptID, throughSequence, manifest); err != nil {
			return false, err
		}
	}
	outboxPayload, err := json.Marshal(V6DispatchIntentPayload{Access: V6AttemptAccess{WorkspaceID: workspaceID, RunID: runID, WorkItemID: workItemID, AttemptID: attemptID, AgentID: agentID}, Manifest: manifest, Mission: mission})
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_v6_outbox(workspace_id,session_id,kind,idempotency_key,payload)VALUES($1::uuid,$2::uuid,'dispatch_work_item',$3,$4::jsonb)`, workspaceID, runID, dispatchKey, outboxPayload); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item SET status='dispatching',attempt_count=attempt_count+1,lease_token=$2::uuid,lease_expires_at=now()+interval '15 minutes',state_version=state_version+1,updated_at=now() WHERE id=$1::uuid`, workItemID, uuid.NewString()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_team_membership SET state='working' WHERE id=$1::uuid`, membershipID); err != nil {
		return false, err
	}
	if _, err = appendEvent(ctx, tx, workspaceID, runID, "v6_work_item_dispatch_prepared", dispatchKey, "system", "", map[string]any{"work_item_id": workItemID, "work_item_attempt_id": attemptID, "manifest_id": manifestID, "manifest_hash": manifestHash, "attempt_number": attemptNumber}); err != nil {
		return false, err
	}
	if err = s.commitResearchTx(ctx, txOpV6DispatchPrepare, tx); err != nil {
		return false, err
	}
	return true, nil
}

func v6WorkAttemptArtifactContent(workItemID string, attemptNumber int, agentID, membershipID, dispatchKey, manifestID, manifestHash string) map[string]any {
	return map[string]any{
		"work_item_id": workItemID, "attempt_number": attemptNumber,
		"assigned_agent_id": agentID, "membership_id": membershipID,
		"dispatch_key": dispatchKey, "manifest_id": manifestID,
		"manifest_hash": manifestHash,
	}
}

func persistV6CatalogPagesTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, attemptID string, throughSequence int64, manifest json.RawMessage) error {
	var frozen struct {
		BranchRefs []V6BranchRef `json:"branch_refs"`
	}
	if json.Unmarshal(manifest, &frozen) != nil {
		return ErrInvalidContract
	}
	branchIDs := make([]string, len(frozen.BranchRefs))
	for i := range frozen.BranchRefs {
		branchIDs[i] = frozen.BranchRefs[i].ID
	}
	branchScope, _ := json.Marshal(frozen.BranchRefs)
	for _, view := range []V6CatalogView{V6CatalogSameTier, V6CatalogHigherCandidates} {
		rows, err := tx.Query(ctx, `SELECT DISTINCT v.id::text,CASE WHEN rn.id IS NOT NULL THEN 'result_s' ELSE 'insight' END,
		COALESCE(rn.id::text,iv.insight_id::text),f.tier,v.content_hash,COALESCE(rn.catalog_summary,iv.catalog_summary)
		FROM research_branch_frontier f JOIN research_artifact_version v ON v.id=f.node_artifact_version_id
		LEFT JOIN research_result_node rn ON rn.artifact_version_id=v.id LEFT JOIN research_insight_version iv ON iv.artifact_version_id=v.id
		WHERE f.workspace_id=$1::uuid AND f.session_id=$2::uuid AND f.removed_by_event_sequence IS NULL
		AND (($3='same_tier' AND f.tier='S') OR ($3='higher_candidates' AND f.tier<>'S' AND f.branch_id=ANY($4::uuid[])))
		ORDER BY v.id::text`, workspaceID, runID, view, branchIDs)
		if err != nil {
			return err
		}
		items := []any{}
		for rows.Next() {
			var versionID, kind, id, tier, hash, summary string
			if err = rows.Scan(&versionID, &kind, &id, &tier, &hash, &summary); err != nil {
				rows.Close()
				return err
			}
			items = append(items, map[string]any{"kind": kind, "id": id, "version_id": versionID, "tier": tier, "content_hash": hash, "catalog_summary": summary})
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		const pageSize = 128
		pageCount := max(1, (len(items)+pageSize-1)/pageSize)
		for ordinal := 0; ordinal < pageCount; ordinal++ {
			start := ordinal * pageSize
			end := min(len(items), start+pageSize)
			page := []any{}
			if start < len(items) {
				page = items[start:end]
			}
			raw, err := marshalV6CanonicalJSON(page)
			if err != nil {
				return err
			}
			hash := ArtifactContentHashFromCanonicalJSON(raw)
			key := fmt.Sprintf("%s:%d:%d", view, throughSequence, ordinal)
			next := ""
			if ordinal+1 < pageCount {
				next = fmt.Sprintf("%s:%d:%d", view, throughSequence, ordinal+1)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO research_work_catalog_page(workspace_id,session_id,work_item_attempt_id,catalog_view,tier,branch_scope,through_event_sequence,page_key,ordinal,content_hash,next_cursor,page)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4,'S',$5::jsonb,$6,$7,$8,$9,NULLIF($10,''),$11::jsonb)`, workspaceID, runID, attemptID, view, branchScope, throughSequence, key, ordinal, hash, next, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileV6WorkManifestTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, workItemID, attemptID, manifestID, agentID, mission, expectedSchema string, goalVersion int, stateVersion, throughSequence int64, payload json.RawMessage) (json.RawMessage, string, error) {
	var goal string
	var scope, sourcePolicy json.RawMessage
	var audience, freshness, language string
	err := tx.QueryRow(ctx, `SELECT s.goal,COALESCE(c.scope,'{}'),COALESCE(c.audience,''),COALESCE(c.freshness,''),COALESCE(c.language,''),COALESCE(c.source_policy,'{}') FROM research_session s LEFT JOIN research_contract_revision c ON c.session_id=s.id AND c.goal_version=$3 WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, workspaceID, runID, goalVersion).Scan(&goal, &scope, &audience, &freshness, &language, &sourcePolicy)
	if err != nil {
		return nil, "", err
	}
	var config map[string]any
	if json.Unmarshal(payload, &config) != nil {
		config = map[string]any{}
	}
	branchRefs := []any{}
	if value, ok := config["branch_refs"].([]any); ok {
		branchRefs = value
	}
	if len(branchRefs) == 0 {
		rows, qerr := tx.Query(ctx, `SELECT DISTINCT b.id::text,b.state_version FROM research_work_item w JOIN research_discussion_input di ON di.discussion_id=w.target_id JOIN research_node_branch nb ON nb.node_artifact_version_id=di.node_artifact_version_id JOIN research_branch b ON b.id=nb.branch_id WHERE w.id=$1::uuid ORDER BY b.id::text`, workItemID)
		if qerr != nil {
			return nil, "", qerr
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var version int64
			if qerr = rows.Scan(&id, &version); qerr != nil {
				return nil, "", qerr
			}
			branchRefs = append(branchRefs, map[string]any{"id": id, "state_version": version})
		}
		if qerr = rows.Err(); qerr != nil {
			return nil, "", qerr
		}
	}
	artifacts := []any{}
	rows, err := tx.Query(ctx, `SELECT DISTINCT v.id::text,p.entity_kind,v.content_hash FROM research_work_item w JOIN research_discussion_input di ON di.discussion_id=w.target_id JOIN research_artifact_version v ON v.id=di.node_artifact_version_id JOIN research_artifact_passport p ON p.id=v.artifact_id WHERE w.id=$1::uuid ORDER BY v.id::text`, workItemID)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var id, kind, hash string
		if err = rows.Scan(&id, &kind, &hash); err != nil {
			rows.Close()
			return nil, "", err
		}
		artifacts = append(artifacts, map[string]any{"artifact_version_id": id, "kind": kind, "representation": "full", "representation_hash": hash, "use_kind": "integration_input", "reason": "Frozen Work Item input."})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, "", err
	}
	rows.Close()
	seenArtifacts := map[string]struct{}{}
	for _, item := range artifacts {
		if object, ok := item.(map[string]any); ok {
			if id, ok := object["artifact_version_id"].(string); ok {
				seenArtifacts[id] = struct{}{}
			}
		}
	}
	if configured, ok := config["artifact_version_ids"].([]any); ok {
		ids := make([]string, 0, len(configured))
		for _, value := range configured {
			if id, ok := value.(string); ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if _, seen := seenArtifacts[id]; seen {
				continue
			}
			var kind, hash string
			if err = tx.QueryRow(ctx, `SELECT p.entity_kind,v.content_hash FROM research_artifact_version v JOIN research_artifact_passport p ON p.id=v.artifact_id WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid AND p.lifecycle_status='accepted'`, workspaceID, runID, id).Scan(&kind, &hash); err != nil {
				return nil, "", err
			}
			artifacts = append(artifacts, map[string]any{"artifact_version_id": id, "kind": kind, "representation": "full", "representation_hash": hash, "use_kind": "frozen_input", "reason": "Director-authorized Work Item input."})
		}
	}
	taskSchema := any(nil)
	if value, ok := config["task_specific_schema"]; ok {
		taskSchema = value
	}
	manifestMap := map[string]any{"contract_kind": "work_manifest", "schema_version": 6, "manifest_id": manifestID, "workspace_id": workspaceID, "run_id": runID, "work_item_id": workItemID, "attempt_id": attemptID, "assigned_agent_id": agentID, "goal": map[string]any{"goal_version": goalVersion, "goal": goal, "scope": jsonObjectOrEmpty(scope), "audience": audience, "freshness": freshness, "language": language, "source_policy": jsonObjectOrEmpty(sourcePolicy)}, "branch_refs": branchRefs, "runtime_protocol_version": "research-run-v6-runtime-v1", "mission_prompt": mission, "expected_result_schema": expectedSchema, "artifacts": artifacts, "through_state_version": stateVersion, "through_event_sequence": throughSequence}
	if expectedSchema == string(V6ContractAtomicResultSubmission) {
		var taskID string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM research_task WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND work_item_id=$3::uuid`, workspaceID, runID, workItemID).Scan(&taskID); err != nil {
			return nil, "", err
		}
		manifestMap["task_id"] = taskID
		tier := "S"
		branchIDs := []string{}
		for _, rawRef := range branchRefs {
			if ref, ok := rawRef.(map[string]any); ok {
				if id, ok := ref["id"].(string); ok {
					branchIDs = append(branchIDs, id)
				}
			}
		}
		manifestMap["catalog_access"] = map[string]any{"same_tier": tier, "higher_candidate_branch_ids": branchIDs, "include_higher_candidates": true, "through_event_sequence": throughSequence, "page_size": 128}
	}
	if taskSchema != nil {
		if expectedSchema == string(V6ContractAtomicResultSubmission) {
			var payloadSchemaID string
			if err = tx.QueryRow(ctx, `SELECT payload_schema_id FROM research_work_item WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, runID, workItemID).Scan(&payloadSchemaID); err != nil {
				return nil, "", err
			}
			if payloadSchemaID == "" {
				return nil, "", ErrInvalidContract
			}
			taskSchema = map[string]any{"payload_schemas": map[string]any{payloadSchemaID: taskSchema}}
		}
		manifestMap["task_specific_schema"] = taskSchema
	}
	canonical, err := marshalV6CanonicalJSON(manifestMap)
	if err != nil {
		return nil, "", err
	}
	hash := ArtifactContentHashFromCanonicalJSON(canonical)
	manifestMap["manifest_hash"] = hash
	raw, err := json.Marshal(manifestMap)
	if err != nil {
		return nil, "", err
	}
	if _, err = DecodeV6Contract(raw, V6ContractWorkManifest, nil); err != nil {
		return nil, "", err
	}
	return raw, hash, nil
}

func (s *PostgresStore) CompleteV6DispatchOutbox(ctx context.Context, outboxID, token, inboxTaskID string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DispatchComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var workspaceID, runID string
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT o.workspace_id::text,o.session_id::text,o.payload FROM research_v6_outbox o WHERE o.id=$1::uuid AND o.lease_token=$2::uuid AND o.kind='dispatch_work_item' AND o.status='delivering' FOR UPDATE`, outboxID, token).Scan(&workspaceID, &runID, &payload)
	if err != nil {
		return err
	}
	attemptID, workItemID, err := parseV6DispatchAccessIDs(payload)
	if err != nil {
		return err
	}
	if err = lockRunForMutation(ctx, tx, runID, workspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE research_work_item_attempt SET inbox_task_id=$3::uuid,status='running',started_at=COALESCE(started_at,now()),updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$4::uuid AND status='dispatching'`, workspaceID, runID, inboxTaskID, attemptID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemLeaseLost
	}
	command, err = tx.Exec(ctx, `UPDATE research_work_item SET status='running',lease_expires_at=now()+interval '15 minutes',updated_at=now() WHERE id=$1::uuid AND status='dispatching'`, workItemID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemChanged
	}
	command, err = tx.Exec(ctx, `UPDATE research_v6_outbox SET status='delivered',result=jsonb_build_object('inbox_task_id',$2::text),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1::uuid AND lease_token=$3::uuid`, outboxID, inboxTaskID, token)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemLeaseLost
	}
	if _, err = appendEvent(ctx, tx, workspaceID, runID, "v6_work_item_dispatched", "v6-work-item-dispatched:"+attemptID, "system", "", map[string]any{"work_item_id": workItemID, "work_item_attempt_id": attemptID, "inbox_task_id": inboxTaskID}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DispatchComplete, tx)
}
