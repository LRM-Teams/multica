package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const v6MinimumParallelResearchWorkers = 3

type v6DirectorPreflightFacts struct {
	maxParallelTasks     int
	workerCount          int
	pendingAgentCount    int
	activeAtomicWork     int
	resultCount          int
	unresolvedQuestions  int
	escalatedDiscussions int
	proposedAgentCount   int
	proposedAtomicWork   int
	proposedConvergence  int
	proposedBranches     int
	proposedWorkBranches int
	proposedReports      int
	reportOnly           bool
	childBranches        int
	topLevelBranches     int
	proposedTopLevel     int
	convergenceReady     bool
	openConvergence      int
}

type v6ProposedReport struct {
	AssigneeAgentID string             `json:"assignee_agent_id"`
	Inputs          []V6ReportInputRef `json:"inputs"`
}

func (s *PostgresStore) preflightV6DirectorProposal(ctx context.Context, proposal v6DirectorProposal) error {
	branchIDs := make([]uuid.UUID, 0)
	assigneeIDs := make([]uuid.UUID, 0)
	assigneeSeen := map[uuid.UUID]struct{}{}
	proposedAgentCount := 0
	proposedAtomicWork := 0
	proposedConvergence := 0
	proposedReports := 0
	reportPayloads := make([]v6ProposedReport, 0, 1)
	proposedBranches := 0
	proposedBranchParents := make([]uuid.UUID, 0)
	proposedWorkBranchIDs := map[uuid.UUID]struct{}{}
	for _, action := range proposal.Actions {
		switch action.Kind {
		case "create_agent":
			proposedAgentCount++
		case "open_discussion", "create_integration":
			proposedConvergence++
		case "create_report":
			factsPayload := v6ProposedReport{}
			if json.Unmarshal(action.Payload, &factsPayload) != nil || strings.TrimSpace(factsPayload.AssigneeAgentID) == "" {
				return fmt.Errorf("%w: create_report 缺少报告老板", ErrInvalidContract)
			}
			proposedReports++
			reportPayloads = append(reportPayloads, factsPayload)
		case "create_branch":
			var payload struct {
				ParentBranchID string `json:"parent_branch_id"`
			}
			if json.Unmarshal(action.Payload, &payload) != nil || strings.TrimSpace(payload.ParentBranchID) == "" {
				return fmt.Errorf("%w: 独立研究方向必须以根 Branch 为 parent_branch_id", ErrInvalidContract)
			}
			parentID, err := uuid.Parse(payload.ParentBranchID)
			if err != nil {
				return fmt.Errorf("%w: create_branch 的 parent_branch_id 无效", ErrInvalidContract)
			}
			proposedBranchParents = append(proposedBranchParents, parentID)
			proposedBranches++
		case "create_work_item", "create_task":
			var payload v6CreateWorkActionPayload
			if json.Unmarshal(action.Payload, &payload) != nil {
				return fmt.Errorf("%w: 无法解析 Work 创建载荷", ErrInvalidContract)
			}
			if V6ContractKind(payload.ExpectedResultSchemaID) != V6ContractAtomicResultSubmission {
				continue
			}
			if len(payload.BranchIDs) != 1 {
				return fmt.Errorf("%w: 每个 atomic Work 必须且只能绑定一个非根子 Branch", ErrInvalidContract)
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
				proposedWorkBranchIDs[branchID] = struct{}{}
			}
		}
	}

	var facts v6DirectorPreflightFacts
	facts.proposedAgentCount = proposedAgentCount
	facts.proposedAtomicWork = proposedAtomicWork
	facts.proposedConvergence = proposedConvergence
	facts.proposedBranches = proposedBranches
	facts.proposedReports = proposedReports
	facts.reportOnly = proposedReports > 0 && proposedReports == len(proposal.Actions)
	facts.proposedWorkBranches = len(proposedWorkBranchIDs)
	err := s.pool.QueryRow(ctx, `SELECT
		COALESCE((s.run_config->>'max_parallel_tasks')::int,5),
		(SELECT count(*)::int FROM research_team_membership m
			WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id AND m.state IN ('idle','working')
			AND m.role='researcher'
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
				AND absorbed.input_artifact_version_id=result.artifact_version_id)),
		(SELECT count(*)::int FROM research_discussion unresolved_question
			WHERE unresolved_question.workspace_id=s.workspace_id AND unresolved_question.session_id=s.id
			  AND unresolved_question.status='escalated'),
		(SELECT count(*)::int FROM research_branch branch
			WHERE branch.workspace_id=s.workspace_id AND branch.session_id=s.id
			AND branch.parent_branch_id IS NOT NULL AND branch.status IN ('proposed','active')),
		EXISTS(
			SELECT candidate.tier
			FROM (
				SELECT frontier.node_artifact_version_id, 'S'::text AS tier
				FROM research_branch_frontier frontier
				JOIN research_result_node result ON result.workspace_id=frontier.workspace_id
					AND result.session_id=frontier.session_id
					AND result.artifact_version_id=frontier.node_artifact_version_id
				WHERE frontier.workspace_id=s.workspace_id AND frontier.session_id=s.id
				  AND frontier.removed_by_event_sequence IS NULL
				  AND result.conclusion_state NOT IN ('invalid','refuted')
				UNION ALL
				SELECT frontier.node_artifact_version_id, insight.tier
				FROM research_branch_frontier frontier
				JOIN research_insight_version insight ON insight.workspace_id=frontier.workspace_id
					AND insight.session_id=frontier.session_id
					AND insight.artifact_version_id=frontier.node_artifact_version_id
				WHERE frontier.workspace_id=s.workspace_id AND frontier.session_id=s.id
				  AND frontier.removed_by_event_sequence IS NULL
				  AND insight.status NOT IN ('invalid','refuted','superseded','terminal')
			) candidate
			GROUP BY candidate.tier
			HAVING count(DISTINCT candidate.node_artifact_version_id) >= 2
		),
		((SELECT count(*)::int FROM research_work_item convergence
			WHERE convergence.workspace_id=s.workspace_id AND convergence.session_id=s.id
			  AND convergence.kind IN ('discussion','integration')
			  AND convergence.status IN ('ready','dispatching','enqueued','running','awaiting_input'))
		 + (SELECT count(*)::int FROM research_discussion unresolved
			WHERE unresolved.workspace_id=s.workspace_id AND unresolved.session_id=s.id
			  AND unresolved.status='escalated'))
		FROM research_session s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, proposal.WorkspaceID, proposal.RunID).Scan(
		&facts.maxParallelTasks, &facts.workerCount, &facts.pendingAgentCount, &facts.activeAtomicWork,
		&facts.resultCount, &facts.unresolvedQuestions, &facts.escalatedDiscussions, &facts.childBranches,
		&facts.convergenceReady, &facts.openConvergence)
	if err != nil {
		return err
	}
	facts.unresolvedQuestions = requiredV6FollowupCount(facts.unresolvedQuestions, facts.escalatedDiscussions)
	if len(proposedBranchParents) > 0 {
		uniqueParents := map[uuid.UUID]struct{}{}
		for _, parentID := range proposedBranchParents {
			uniqueParents[parentID] = struct{}{}
		}
		var existingParents int
		if err = s.pool.QueryRow(ctx, `SELECT count(*)::int FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=ANY($3::uuid[])`,
			proposal.WorkspaceID, proposal.RunID, proposedBranchParents).Scan(&existingParents); err != nil {
			return err
		}
		if existingParents != len(uniqueParents) {
			return fmt.Errorf("%w: create_branch 的 parent_branch_id 必须属于当前 Run", ErrInvalidContract)
		}
		var rootBranchID uuid.UUID
		if err = s.pool.QueryRow(ctx, `SELECT id FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
			ORDER BY (client_key='root') DESC,created_at,id LIMIT 1`, proposal.WorkspaceID, proposal.RunID).Scan(&rootBranchID); err != nil {
			return err
		}
		if err = s.pool.QueryRow(ctx, `SELECT count(*)::int FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id=$3::uuid
			AND status IN ('proposed','active')`, proposal.WorkspaceID, proposal.RunID, rootBranchID).Scan(&facts.topLevelBranches); err != nil {
			return err
		}
		for _, parentID := range proposedBranchParents {
			if parentID == rootBranchID {
				facts.proposedTopLevel++
			}
		}
		if facts.resultCount == 0 && facts.childBranches == 0 {
			for _, parentID := range proposedBranchParents {
				if parentID != rootBranchID {
					return fmt.Errorf("%w: 首轮独立方向必须直接创建在根 Branch 下", ErrInvalidContract)
				}
			}
		}
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
			AND m.role='researcher' AND m.state IN ('idle','working','offline','retiring')`, proposal.WorkspaceID, proposal.RunID, assigneeIDs).Scan(&eligible, &busy); err != nil {
			return err
		}
		if eligible != len(assigneeSeen) {
			return fmt.Errorf("%w: atomic Work 必须分配给当前 Run 的有效 Agent", ErrInvalidContract)
		}
		if busy > 0 {
			return fmt.Errorf("%w: atomic Work 的 Agent 已有活动任务，必须改派给不同 Agent", ErrInvalidContract)
		}
	}
	reportPlan, err := s.loadV6ReportPlanFact(ctx, proposal.WorkspaceID, proposal.RunID)
	if err != nil {
		return err
	}
	needsReport, _ := reportPlan["needs_refresh"].(bool)
	if needsReport && facts.proposedReports == 0 {
		return fmt.Errorf("%w: 当前方向最大节点已变化，必须同时派发报告老板更新阶段性报告", ErrInvalidContract)
	}
	if !needsReport && facts.proposedReports > 0 {
		return fmt.Errorf("%w: 当前报告输入没有变化，不得重复创建相同修订", ErrInvalidContract)
	}
	if facts.proposedReports > 1 {
		return fmt.Errorf("%w: 同一时刻只能由报告老板维护一个报告修订", ErrInvalidContract)
	}
	if needsReport {
		reporterAgentID, _ := reportPlan["reporter_agent_id"].(string)
		expectedInputs, _ := reportPlan["inputs"].([]V6ReportInputRef)
		if len(reportPayloads) != 1 || reportPayloads[0].AssigneeAgentID != reporterAgentID || !sameV6ReportInputs(expectedInputs, reportPayloads[0].Inputs) {
			return fmt.Errorf("%w: create_report 必须原样复制 report_plan 的报告老板和冻结输入", ErrInvalidContract)
		}
	}
	return validateV6ParallelResearchPlan(facts)
}

func requiredV6FollowupCount(openQuestions, escalatedDiscussions int) int {
	if escalatedDiscussions > openQuestions {
		return escalatedDiscussions
	}
	return openQuestions
}

func validateV6ParallelResearchPlan(facts v6DirectorPreflightFacts) error {
	parallelTarget := v6MinimumParallelResearchWorkers
	if facts.maxParallelTasks < parallelTarget {
		parallelTarget = facts.maxParallelTasks
	}
	if parallelTarget < 1 {
		parallelTarget = 1
	}
	if facts.resultCount == 0 && facts.proposedAgentCount > 0 && facts.childBranches+facts.proposedBranches < parallelTarget {
		return fmt.Errorf("%w: 首轮必须同时创建至少 %d 个非根子 Branch，不能把全部研究堆进根 Branch", ErrInvalidContract, parallelTarget)
	}
	if facts.proposedAtomicWork > 0 && facts.childBranches == 0 {
		return fmt.Errorf("%w: 先为独立调研方向创建子 Branch；atomic Work 不得全部堆进根 Branch", ErrInvalidContract)
	}
	if facts.proposedTopLevel > 0 && facts.topLevelBranches+facts.proposedTopLevel > facts.maxParallelTasks {
		return fmt.Errorf("%w: 一级研究方向最多 %d 个；请复用已有方向，或把细分问题创建为相关方向的子 Branch", ErrInvalidContract, facts.maxParallelTasks)
	}
	if facts.resultCount == 0 && facts.proposedAtomicWork > 0 && facts.proposedWorkBranches < parallelTarget {
		return fmt.Errorf("%w: 首轮 atomic Work 必须覆盖至少 %d 个不同的子 Branch", ErrInvalidContract, parallelTarget)
	}
	if facts.convergenceReady && facts.openConvergence == 0 && facts.proposedConvergence == 0 {
		return fmt.Errorf("%w: 当前 Frontier 已有可合并的同层节点，必须先创建 integration Discussion，推进 S→M→L→XL→XXL 收敛", ErrInvalidContract)
	}
	if facts.convergenceReady && facts.proposedConvergence > 0 && facts.proposedAtomicWork > 0 {
		return fmt.Errorf("%w: 当前轮次必须先完成同层节点收敛，不能同时继续堆积 atomic Work", ErrInvalidContract)
	}
	if facts.proposedConvergence > 0 {
		return nil
	}
	// A report refresh is a productive maintenance cycle. Accept it on its own so
	// the resulting report event can trigger a fresh cycle for unresolved research
	// instead of rejecting the whole proposal and starving both operations.
	if facts.reportOnly {
		return nil
	}
	if facts.proposedAgentCount == 0 && facts.proposedAtomicWork == 0 && facts.unresolvedQuestions == 0 {
		return nil
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
	if facts.workerCount < requiredWork && facts.pendingAgentCount+facts.proposedAgentCount > 0 {
		if facts.proposedAtomicWork > 0 {
			return fmt.Errorf("%w: Agent 创建是异步的；先等待全部成员 joined，再在后续轮次并行派工", ErrInvalidContract)
		}
		return nil
	}
	if facts.activeAtomicWork+facts.proposedAtomicWork < requiredWork {
		return fmt.Errorf("%w: 当前轮次至少需要 %d 个分配给不同 Agent 的 atomic Work，不能把独立方向串行塞进一个宽任务", ErrInvalidContract, requiredWork)
	}
	return nil
}
