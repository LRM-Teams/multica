package researchrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const v6MinimumParallelResearchWorkers = 3

type v6DirectorPreflightFacts struct {
	maxParallelTasks    int
	workerCount         int
	pendingAgentCount   int
	activeAtomicWork    int
	resultCount         int
	unresolvedQuestions int
	proposedAgentCount  int
	proposedAtomicWork  int
}

func (s *PostgresStore) preflightV6DirectorProposal(ctx context.Context, proposal v6DirectorProposal) error {
	branchIDs := make([]uuid.UUID, 0)
	assigneeIDs := make([]uuid.UUID, 0)
	assigneeSeen := map[uuid.UUID]struct{}{}
	proposedAgentCount := 0
	proposedAtomicWork := 0
	for _, action := range proposal.Actions {
		switch action.Kind {
		case "create_agent":
			proposedAgentCount++
		case "create_work_item", "create_task":
			var payload v6CreateWorkActionPayload
			if json.Unmarshal(action.Payload, &payload) != nil {
				return fmt.Errorf("%w: 无法解析 Work 创建载荷", ErrInvalidContract)
			}
			if V6ContractKind(payload.ExpectedResultSchemaID) != V6ContractAtomicResultSubmission {
				continue
			}
			proposedAtomicWork++
			agentID, err := uuid.Parse(payload.AssigneeAgentID)
			if err != nil {
				return fmt.Errorf("%w: atomic Work 的 assignee_agent_id 无效", ErrInvalidContract)
			}
			if _, duplicate := assigneeSeen[agentID]; duplicate {
				return fmt.Errorf("%w: 多个独立 atomic Work 不能分配给同一个 Agent", ErrInvalidContract)
			}
			assigneeSeen[agentID] = struct{}{}
			assigneeIDs = append(assigneeIDs, agentID)
			for _, rawBranchID := range payload.BranchIDs {
				branchID, parseErr := uuid.Parse(rawBranchID)
				if parseErr != nil {
					return fmt.Errorf("%w: atomic Work 引用了无效的 branch_id", ErrInvalidContract)
				}
				branchIDs = append(branchIDs, branchID)
			}
		}
	}

	var facts v6DirectorPreflightFacts
	facts.proposedAgentCount = proposedAgentCount
	facts.proposedAtomicWork = proposedAtomicWork
	err := s.pool.QueryRow(ctx, `SELECT
		COALESCE((s.run_config->>'max_parallel_tasks')::int,5),
		(SELECT count(*)::int FROM research_team_membership m
			WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id AND m.state IN ('idle','working')
			AND NOT EXISTS (SELECT 1 FROM research_director_assignment director
				WHERE director.workspace_id=m.workspace_id AND director.session_id=m.session_id
				AND director.status='active' AND director.director_agent_id=m.agent_id)),
		(SELECT count(*)::int FROM research_v6_outbox pending
			WHERE pending.workspace_id=s.workspace_id AND pending.session_id=s.id
			AND pending.kind='create_agent' AND pending.status IN ('pending','delivering')),
		(SELECT count(*)::int FROM research_work_item active
			WHERE active.workspace_id=s.workspace_id AND active.session_id=s.id AND active.kind='research'
			AND active.status IN ('ready','dispatching','enqueued','running','awaiting_input')),
		(SELECT count(*)::int FROM research_result_node result
			WHERE result.workspace_id=s.workspace_id AND result.session_id=s.id
			AND result.conclusion_state NOT IN ('invalid','refuted')
			AND NOT EXISTS (SELECT 1 FROM research_node_absorption absorbed
				WHERE absorbed.workspace_id=result.workspace_id AND absorbed.session_id=result.session_id
				AND absorbed.input_artifact_version_id=result.artifact_version_id)),
		(SELECT COALESCE(sum(jsonb_array_length(COALESCE(result.open_questions,'[]'::jsonb))),0)::int
			FROM research_result_node result
			WHERE result.workspace_id=s.workspace_id AND result.session_id=s.id
			AND result.conclusion_state NOT IN ('invalid','refuted')
			AND NOT EXISTS (SELECT 1 FROM research_node_absorption absorbed
				WHERE absorbed.workspace_id=result.workspace_id AND absorbed.session_id=result.session_id
				AND absorbed.input_artifact_version_id=result.artifact_version_id))
		FROM research_session s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(
		&facts.maxParallelTasks, &facts.workerCount, &facts.pendingAgentCount, &facts.activeAtomicWork,
		&facts.resultCount, &facts.unresolvedQuestions)
	if err != nil {
		return err
	}
	if len(branchIDs) > 0 {
		var existing int
		if err = s.pool.QueryRow(ctx, `SELECT count(DISTINCT id)::int FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=ANY($3::uuid[])`,
			proposal.WorkspaceID, proposal.RunID, branchIDs).Scan(&existing); err != nil {
			return err
		}
		unique := map[uuid.UUID]struct{}{}
		for _, branchID := range branchIDs {
			unique[branchID] = struct{}{}
		}
		if existing != len(unique) {
			return fmt.Errorf("%w: Work 引用了不属于当前 Run 的 branch_id", ErrInvalidContract)
		}
	}
	if len(assigneeIDs) > 0 {
		var eligible, busy int
		if err = s.pool.QueryRow(ctx, `SELECT
			count(DISTINCT m.agent_id)::int,
			count(DISTINCT active.assigned_agent_id) FILTER (WHERE active.id IS NOT NULL)::int
			FROM research_team_membership m
			LEFT JOIN research_work_item active ON active.workspace_id=m.workspace_id AND active.session_id=m.session_id
				AND active.assigned_agent_id=m.agent_id
				AND active.status IN ('ready','dispatching','enqueued','running','awaiting_input')
			WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.agent_id=ANY($3::uuid[])
			AND m.state IN ('idle','working','offline','retiring')`, proposal.WorkspaceID, proposal.RunID, assigneeIDs).Scan(&eligible, &busy); err != nil {
			return err
		}
		if eligible != len(assigneeSeen) {
			return fmt.Errorf("%w: atomic Work 必须分配给当前 Run 的有效 Agent", ErrInvalidContract)
		}
		if busy > 0 {
			return fmt.Errorf("%w: atomic Work 的 Agent 已有活动任务，必须改派给不同 Agent", ErrInvalidContract)
		}
	}
	return validateV6ParallelResearchPlan(facts)
}

func validateV6ParallelResearchPlan(facts v6DirectorPreflightFacts) error {
	if facts.proposedAgentCount == 0 && facts.proposedAtomicWork == 0 && facts.unresolvedQuestions == 0 {
		return nil
	}
	parallelTarget := v6MinimumParallelResearchWorkers
	if facts.maxParallelTasks < parallelTarget {
		parallelTarget = facts.maxParallelTasks
	}
	if parallelTarget < 1 {
		parallelTarget = 1
	}
	requiredWork := 0
	if facts.unresolvedQuestions > 0 {
		requiredWork = facts.unresolvedQuestions
		if requiredWork > facts.maxParallelTasks {
			requiredWork = facts.maxParallelTasks
		}
	}
	if remaining := parallelTarget - facts.resultCount; remaining > requiredWork {
		requiredWork = remaining
	}
	if requiredWork <= 0 {
		return nil
	}
	if facts.workerCount+facts.pendingAgentCount+facts.proposedAgentCount < requiredWork {
		return fmt.Errorf("%w: 当前研究至少需要 %d 个不同的 run-scoped Agent 并发覆盖独立方向或待回答问题", ErrInvalidContract, requiredWork)
	}
	if facts.pendingAgentCount+facts.proposedAgentCount > 0 {
		return nil
	}
	if facts.activeAtomicWork+facts.proposedAtomicWork < requiredWork {
		return fmt.Errorf("%w: 当前轮次至少需要 %d 个分配给不同 Agent 的 atomic Work，不能把独立方向串行塞进一个宽任务", ErrInvalidContract, requiredWork)
	}
	return nil
}
