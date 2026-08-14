package researchrun

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// selectiveSteeringModule computes the exact mutation scope for a typed V6
// steering operation. It does not interpret prose and never selects accepted
// Evidence for deletion; the transaction adapter applies this plan to
// canonical Inquiry and Execution state.
type selectiveSteeringModule struct{}

type selectiveSteeringRequest struct {
	ExpectedStateVersion int64
	FullReplan           bool
	AffectedBranchIDs    []string
	AllowRunningFinish   bool
}

type steeringBranchState struct {
	ID       string
	ParentID string
	Status   string
}

type steeringTaskState struct {
	ID        string
	Status    TaskStatus
	BranchIDs []string
}

type selectiveSteeringState struct {
	StateVersion int64
	Branches     []steeringBranchState
	Tasks        []steeringTaskState
}

type selectiveSteeringPlan struct {
	ImpactedBranchIDs      []string
	ObsoleteBranchIDs      []string
	ObsoleteTaskIDs        []string
	CancelRunningTaskIDs   []string
	RetainedRunningTaskIDs []string
}

func (selectiveSteeringModule) Plan(request selectiveSteeringRequest, state selectiveSteeringState) (selectiveSteeringPlan, error) {
	if request.ExpectedStateVersion <= 0 || request.ExpectedStateVersion != state.StateVersion {
		return selectiveSteeringPlan{}, fmt.Errorf("%w: steering state version changed", ErrControlTargetChanged)
	}
	branches := make(map[string]steeringBranchState, len(state.Branches))
	children := make(map[string][]string, len(state.Branches))
	for _, branch := range state.Branches {
		if _, err := uuid.Parse(branch.ID); err != nil {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: steering branch ID is invalid", ErrInvalidContract)
		}
		if _, duplicate := branches[branch.ID]; duplicate {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: duplicate steering branch", ErrInvalidContract)
		}
		switch branch.Status {
		case "proposed", "active", "paused", "completed", "terminated", "obsolete":
		default:
			return selectiveSteeringPlan{}, fmt.Errorf("%w: invalid steering branch status %q", ErrInvalidContract, branch.Status)
		}
		if branch.ParentID != "" {
			if _, err := uuid.Parse(branch.ParentID); err != nil || branch.ParentID == branch.ID {
				return selectiveSteeringPlan{}, fmt.Errorf("%w: invalid steering branch parent", ErrInvalidContract)
			}
			children[branch.ParentID] = append(children[branch.ParentID], branch.ID)
		}
		branches[branch.ID] = branch
	}
	for _, branch := range state.Branches {
		if branch.ParentID != "" {
			if _, ok := branches[branch.ParentID]; !ok {
				return selectiveSteeringPlan{}, fmt.Errorf("%w: steering branch parent is outside the Run", ErrInvalidContract)
			}
		}
	}
	if hasSteeringBranchCycle(branches) {
		return selectiveSteeringPlan{}, fmt.Errorf("%w: steering branch hierarchy contains a cycle", ErrInvalidContract)
	}

	affected := make(map[string]struct{}, len(branches))
	if request.FullReplan {
		for id := range branches {
			affected[id] = struct{}{}
		}
	} else {
		if len(request.AffectedBranchIDs) == 0 {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: selective steering requires an affected branch", ErrInvalidContract)
		}
		for _, root := range request.AffectedBranchIDs {
			if _, ok := branches[root]; !ok {
				return selectiveSteeringPlan{}, fmt.Errorf("%w: affected steering branch is outside the Run", ErrInvalidContract)
			}
			collectSteeringBranchDescendants(root, children, affected)
		}
	}

	seenTasks := make(map[string]struct{}, len(state.Tasks))
	plan := selectiveSteeringPlan{ImpactedBranchIDs: sortedSteeringSet(affected)}
	for branchID := range affected {
		switch branches[branchID].Status {
		case "proposed", "active", "paused":
			plan.ObsoleteBranchIDs = append(plan.ObsoleteBranchIDs, branchID)
		}
	}
	sort.Strings(plan.ObsoleteBranchIDs)
	for _, task := range state.Tasks {
		if _, err := uuid.Parse(task.ID); err != nil {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: steering task ID is invalid", ErrInvalidContract)
		}
		if _, duplicate := seenTasks[task.ID]; duplicate {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: duplicate steering task", ErrInvalidContract)
		}
		seenTasks[task.ID] = struct{}{}
		if !validSteeringTaskStatus(task.Status) {
			return selectiveSteeringPlan{}, fmt.Errorf("%w: invalid steering task status %q", ErrInvalidContract, task.Status)
		}
		seenTaskBranches := map[string]struct{}{}
		for _, branchID := range task.BranchIDs {
			if _, ok := branches[branchID]; !ok {
				return selectiveSteeringPlan{}, fmt.Errorf("%w: task references branch outside the Run", ErrInvalidContract)
			}
			if _, duplicate := seenTaskBranches[branchID]; duplicate {
				return selectiveSteeringPlan{}, fmt.Errorf("%w: task repeats a branch reference", ErrInvalidContract)
			}
			seenTaskBranches[branchID] = struct{}{}
		}
		if !request.FullReplan && !intersectsSteeringBranches(task.BranchIDs, affected) {
			continue
		}
		switch task.Status {
		case TaskStatusPending, TaskStatusReady:
			plan.ObsoleteTaskIDs = append(plan.ObsoleteTaskIDs, task.ID)
		case TaskStatusDispatching, TaskStatusRunning:
			if request.AllowRunningFinish {
				plan.RetainedRunningTaskIDs = append(plan.RetainedRunningTaskIDs, task.ID)
			} else {
				plan.CancelRunningTaskIDs = append(plan.CancelRunningTaskIDs, task.ID)
			}
		}
	}
	sort.Strings(plan.ObsoleteTaskIDs)
	sort.Strings(plan.CancelRunningTaskIDs)
	sort.Strings(plan.RetainedRunningTaskIDs)
	return plan, nil
}

func collectSteeringBranchDescendants(root string, children map[string][]string, affected map[string]struct{}) {
	if _, seen := affected[root]; seen {
		return
	}
	affected[root] = struct{}{}
	for _, child := range children[root] {
		collectSteeringBranchDescendants(child, children, affected)
	}
}

func intersectsSteeringBranches(branchIDs []string, affected map[string]struct{}) bool {
	for _, branchID := range branchIDs {
		if _, ok := affected[branchID]; ok {
			return true
		}
	}
	return false
}

func sortedSteeringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validSteeringTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskStatusPending, TaskStatusReady, TaskStatusDispatching, TaskStatusRunning,
		TaskStatusSucceeded, TaskStatusFailed, TaskStatusBlocked, TaskStatusObsolete, TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func hasSteeringBranchCycle(branches map[string]steeringBranchState) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		if parent := branches[id].ParentID; parent != "" && visit(parent) {
			return true
		}
		state[id] = 2
		return false
	}
	for id := range branches {
		if visit(id) {
			return true
		}
	}
	return false
}
