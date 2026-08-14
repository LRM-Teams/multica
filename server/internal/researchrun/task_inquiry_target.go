package researchrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const maxTaskInquiryTargets = 4096

// TaskInquiryTarget binds an Execution Task to the canonical Inquiry entities
// it is responsible for advancing. Selective steering consumes these resolved
// identities; it must never infer branch ownership from Task prose.
type TaskInquiryTarget struct {
	TaskID   string            `json:"task_id"`
	Kind     InquiryEntityKind `json:"kind"`
	EntityID string            `json:"entity_id"`
}

type BindTaskInquiryTargetsInput struct {
	WorkspaceID          string              `json:"-"`
	SessionID            string              `json:"-"`
	AttemptID            string              `json:"attempt_id"`
	AgentID              string              `json:"agent_id"`
	IdempotencyKey       string              `json:"idempotency_key"`
	ExpectedStateVersion int64               `json:"expected_state_version"`
	Targets              []TaskInquiryTarget `json:"targets"`
}

type BindTaskInquiryTargetsResult struct {
	Event    RunEvent `json:"event"`
	Replayed bool     `json:"replayed"`
}

type taskInquiryTargetModule struct{}

func (taskInquiryTargetModule) ValidateBind(in BindTaskInquiryTargetsInput) error {
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.SessionID) == "" ||
		strings.TrimSpace(in.AttemptID) == "" || strings.TrimSpace(in.AgentID) == "" ||
		strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 512 ||
		in.ExpectedStateVersion < 1 {
		return fmt.Errorf("%w: incomplete Task Inquiry target identity", ErrInvalidContract)
	}
	if _, err := uuid.Parse(in.WorkspaceID); err != nil {
		return fmt.Errorf("%w: invalid Task Inquiry target workspace", ErrInvalidContract)
	}
	if _, err := uuid.Parse(in.SessionID); err != nil {
		return fmt.Errorf("%w: invalid Task Inquiry target session", ErrInvalidContract)
	}
	if _, err := uuid.Parse(in.AttemptID); err != nil {
		return fmt.Errorf("%w: invalid Task Inquiry target attempt", ErrInvalidContract)
	}
	if _, err := uuid.Parse(in.AgentID); err != nil {
		return fmt.Errorf("%w: invalid Task Inquiry target agent", ErrInvalidContract)
	}
	if len(in.Targets) == 0 || len(in.Targets) > maxTaskInquiryTargets {
		return fmt.Errorf("%w: Task Inquiry targets must be non-empty and bounded", ErrInvalidContract)
	}
	seen := make(map[TaskInquiryTarget]struct{}, len(in.Targets))
	for _, target := range in.Targets {
		if _, err := uuid.Parse(target.TaskID); err != nil {
			return fmt.Errorf("%w: Task Inquiry target task is not resolved", ErrInvalidContract)
		}
		if !inquiryKinds[target.Kind] {
			return fmt.Errorf("%w: unknown Task Inquiry target kind %q", ErrInvalidContract, target.Kind)
		}
		if _, err := uuid.Parse(target.EntityID); err != nil {
			return fmt.Errorf("%w: Task Inquiry target entity is not resolved", ErrInvalidContract)
		}
		if _, duplicate := seen[target]; duplicate {
			return fmt.Errorf("%w: duplicate Task Inquiry target", ErrInvalidContract)
		}
		seen[target] = struct{}{}
	}
	return nil
}

func canonicalTaskInquiryTargets(targets []TaskInquiryTarget) []TaskInquiryTarget {
	canonical := append([]TaskInquiryTarget(nil), targets...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].TaskID != canonical[j].TaskID {
			return canonical[i].TaskID < canonical[j].TaskID
		}
		if canonical[i].Kind != canonical[j].Kind {
			return canonical[i].Kind < canonical[j].Kind
		}
		return canonical[i].EntityID < canonical[j].EntityID
	})
	return canonical
}

type taskInquiryTargetsBoundPayload struct {
	AttemptID            string              `json:"attempt_id"`
	ExpectedStateVersion int64               `json:"expected_state_version"`
	Targets              []TaskInquiryTarget `json:"targets"`
}

func taskInquiryTargetsEventPayload(in BindTaskInquiryTargetsInput) taskInquiryTargetsBoundPayload {
	return taskInquiryTargetsBoundPayload{
		AttemptID:            in.AttemptID,
		ExpectedStateVersion: in.ExpectedStateVersion,
		Targets:              canonicalTaskInquiryTargets(in.Targets),
	}
}
