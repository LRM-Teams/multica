package handler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const goalControllerEventBatch = 100

type goalControllerWake struct {
	channel ChannelResponse
	agent   db.Agent
	trigger ChannelMessageResponse
	result  channelAgentPromptTxResult
}

// DispatchGoalControllerEvents coalesces durable Goal state changes into a
// single directed manager Run per active Goal. A live Run is fenced by the
// agent inbox delivery/ack state, so a busy manager gets one notice and can
// drain later without losing the underlying Goal events.
func (h *Handler) DispatchGoalControllerEvents(ctx context.Context, limit int) (int, error) {
	if h == nil || h.DB == nil || h.TxStarter == nil || limit <= 0 {
		return 0, nil
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE goal_controller_event event
		SET status='cancelled', updated_at=now(), last_error='goal_not_active'
		FROM channel_goal goal
		WHERE event.workspace_id=goal.workspace_id AND event.goal_id=goal.id
		  AND event.status='pending' AND goal.status <> 'active'`); err != nil {
		return 0, fmt.Errorf("cancel inactive Goal controller events: %w", err)
	}

	dispatched := 0
	attempted := 0
	for dispatched < limit && attempted < limit*4 {
		wake, ok, err := h.dispatchOneGoalController(ctx)
		if err != nil {
			return dispatched, err
		}
		if !ok {
			break
		}
		attempted++
		if wake == nil {
			continue
		}
		dispatched++
		if !wake.result.Coalesced {
			h.recordChannelAgentPromptWake(ctx, wake.channel, wake.agent, wake.trigger, protocol.AgentInboxReasonGoalController, wake.result)
		}
	}
	return dispatched, nil
}

func (h *Handler) dispatchOneGoalController(ctx context.Context) (*goalControllerWake, bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var workspaceID, goalID, channelID pgtype.UUID
	var goalTitle, channelName, channelKind, creatorType string
	var creatorID pgtype.UUID
	var goalVersion int64
	err = tx.QueryRow(ctx, `
		SELECT event.workspace_id, event.goal_id, goal.channel_id, goal.title, goal.version,
		       channel.name, channel.kind, goal.created_by_type, goal.created_by_id
		FROM goal_controller_event event
		JOIN channel_goal goal
		  ON goal.workspace_id=event.workspace_id AND goal.id=event.goal_id
		JOIN channel ON channel.id=goal.channel_id
		WHERE event.status='pending' AND event.available_at <= now()
		  AND goal.status='active' AND channel.archived_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM goal_controller_event prior
		    JOIN agent_inbox_event run ON run.id=prior.run_id
		    WHERE prior.goal_id=event.goal_id AND prior.status='dispatched'
		      AND run.terminal_outcome IS NULL
		      AND run.status IN ('pending','draining','failed')
		  )
		ORDER BY event.available_at, event.created_at, event.id
		FOR UPDATE OF event SKIP LOCKED
		LIMIT 1`).Scan(&workspaceID, &goalID, &channelID, &goalTitle, &goalVersion, &channelName, &channelKind, &creatorType, &creatorID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("select Goal controller event: %w", err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "goal-controller:"+uuidToString(goalID)); err != nil {
		return nil, false, fmt.Errorf("lock Goal controller: %w", err)
	}

	var live bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM goal_controller_event event
		  JOIN agent_inbox_event run ON run.id=event.run_id
		  WHERE event.goal_id=$1 AND event.status='dispatched'
		    AND run.terminal_outcome IS NULL
		    AND run.status IN ('pending','draining','failed')
		)`, goalID).Scan(&live); err != nil {
		return nil, false, fmt.Errorf("check live Goal controller Run: %w", err)
	}
	if live {
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	manager, err := goalControllerManager(ctx, tx, workspaceID, channelID, creatorType, creatorID)
	if err != nil {
		if err != pgx.ErrNoRows {
			return nil, false, fmt.Errorf("select Goal manager: %w", err)
		}
		if _, updateErr := tx.Exec(ctx, `
			UPDATE goal_controller_event
			SET available_at=now()+interval '30 seconds', attempt_count=attempt_count+1,
			    last_error='eligible_goal_manager_unavailable', updated_at=now()
			WHERE goal_id=$1 AND status='pending'`, goalID); updateErr != nil {
			return nil, false, updateErr
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT id,event_kind,source_kind,source_id
		FROM goal_controller_event
		WHERE goal_id=$1 AND status='pending' AND available_at <= now()
		ORDER BY created_at,id
		FOR UPDATE
		LIMIT $2`, goalID, goalControllerEventBatch)
	if err != nil {
		return nil, false, fmt.Errorf("load Goal controller batch: %w", err)
	}
	var eventIDs []uuid.UUID
	kindCounts := map[string]int{}
	var sources []string
	for rows.Next() {
		var id uuid.UUID
		var kind, sourceKind string
		var sourceID pgtype.UUID
		if err = rows.Scan(&id, &kind, &sourceKind, &sourceID); err != nil {
			rows.Close()
			return nil, false, err
		}
		eventIDs = append(eventIDs, id)
		kindCounts[kind]++
		if sourceID.Valid && len(sources) < 20 {
			sources = append(sources, sourceKind+":"+uuidToString(sourceID))
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(eventIDs) == 0 {
		if err = tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	// Issues stamped under an older Goal version are the re-validation queue a
	// mid-flight Goal update creates: surface the count so the manager checks
	// them against the current requirements instead of trusting stale scope.
	var staleIssues int
	if err = tx.QueryRow(ctx, `
		SELECT count(*) FROM issue
		WHERE workspace_id=$1 AND channel_goal_id=$2
		  AND status NOT IN ('done','cancelled')
		  AND goal_version_at_creation IS NOT NULL
		  AND goal_version_at_creation < $3`, workspaceID, goalID, goalVersion).Scan(&staleIssues); err != nil {
		return nil, false, fmt.Errorf("count stale Goal Issues: %w", err)
	}

	channel := ChannelResponse{ID: uuidToString(channelID), WorkspaceID: uuidToString(workspaceID), Name: channelName, Kind: channelKind}
	prompt := buildGoalControllerPrompt(uuidToString(goalID), goalTitle, goalVersion, staleIssues, kindCounts, sources)
	trigger := ChannelMessageResponse{
		ChannelID: channel.ID, WorkspaceID: channel.WorkspaceID, Type: "system",
		Content: prompt, Source: protocol.AgentInboxReasonGoalController,
	}
	qtx := db.New(tx)
	result, err := h.enqueueChannelAgentPromptWithTx(ctx, qtx, tx, channel, manager, trigger, manager.OwnerID, prompt, protocol.AgentInboxReasonGoalController, channelDirectedWakePriority)
	if err != nil {
		return nil, false, fmt.Errorf("enqueue Goal controller Run: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE goal_controller_event
		SET status='dispatched',run_id=$2,attempt_count=attempt_count+1,
		    last_error='',updated_at=now()
		WHERE id=ANY($1::uuid[])`, eventIDs, result.Event.ID); err != nil {
		return nil, false, fmt.Errorf("mark Goal controller events dispatched: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &goalControllerWake{channel: channel, agent: manager, trigger: trigger, result: result}, true, nil
}

func goalControllerManager(ctx context.Context, tx pgx.Tx, workspaceID, channelID pgtype.UUID, creatorType string, creatorID pgtype.UUID) (db.Agent, error) {
	var agent db.Agent
	err := tx.QueryRow(ctx, `
		SELECT a.id,a.workspace_id,a.name,a.avatar_url,a.runtime_mode,a.runtime_config,a.status,
		       a.owner_id,a.created_at,a.updated_at,a.description,a.runtime_id,
		       a.instructions,a.archived_at,a.display_name,a.model,a.thinking_level
		FROM channel_member member
		JOIN agent a ON member.member_type='agent' AND a.id=member.member_id
		WHERE member.workspace_id=$1 AND member.channel_id=$2
		  AND member.role IN ('owner','manager')
		  AND a.archived_at IS NULL AND a.runtime_id IS NOT NULL
		ORDER BY CASE WHEN $3='agent' AND a.id=$4 THEN 0
		              WHEN member.role='owner' THEN 1 ELSE 2 END,
		         member.created_at,a.id
		LIMIT 1`, workspaceID, channelID, creatorType, nullableUUID(creatorID)).Scan(
		&agent.ID, &agent.WorkspaceID, &agent.Name, &agent.AvatarUrl, &agent.RuntimeMode,
		&agent.RuntimeConfig, &agent.Status, &agent.OwnerID, &agent.CreatedAt, &agent.UpdatedAt,
		&agent.Description, &agent.RuntimeID, &agent.Instructions, &agent.ArchivedAt,
		&agent.DisplayName, &agent.Model, &agent.ThinkingLevel,
	)
	return agent, err
}

func buildGoalControllerPrompt(goalID, title string, goalVersion int64, staleIssues int, kindCounts map[string]int, sources []string) string {
	kinds := make([]string, 0, len(kindCounts))
	for kind := range kindCounts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, kindCounts[kind]))
	}
	staleNotice := ""
	if staleIssues > 0 {
		staleNotice = fmt.Sprintf(
			"The Goal requirements are now at version %d and %d open Issue(s) were created under an older version: re-validate their scope, acceptance criteria, and dependencies against the current Goal before dispatching or completing them, and cancel any made obsolete by the change. ",
			goalVersion, staleIssues,
		)
	}
	return fmt.Sprintf(
		"Goal controller reconciliation for `%s` (%s). Durable events: %s. Sources: %s. "+
			"Inspect the current Goal and all Issues scoped by channel_goal_id. If the Goal is not yet decomposed, create a parent/child Issue DAG with explicit dependencies, one accountable assignee per leaf, and non-empty acceptance criteria. "+
			"%s"+
			"Do not wake workers through chat: canonical Issue execution dispatches their Runs. Reconcile completed/failed Runs, unblock only dependency-ready Issues, update Goal progress and blockers, and complete the Goal only when every required Issue and success criterion has evidence. Avoid recreating equivalent Issues.",
		goalID, strings.TrimSpace(title), strings.Join(parts, ", "), strings.Join(sources, ", "), staleNotice,
	)
}
