package researchrun

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Node command actions (LRM-1413 continue|fork; LRM-1408 retry|reassign).
const (
	NodeActionContinue = "continue"
	NodeActionFork     = "fork"
	NodeActionRetry    = "retry"
	NodeActionReassign = "reassign"
)

// Machine codes + message keys for 409 responses — never English engineering text.
const (
	NodeCmdCodePermissionDenied     = "research.node_command.permission_denied"
	NodeCmdCodeSessionTerminal      = "research.node_command.session_terminal"
	NodeCmdCodeNodeStale            = "research.node_command.node_stale"
	NodeCmdCodeStateVersionConflict = "research.node_command.state_version_conflict"
	NodeCmdCodeActionNotAllowed     = "research.node_command.action_not_allowed"
	NodeCmdCodeIdempotencyConflict  = "research.node_command.idempotency_conflict"
	NodeCmdCodeInvalidRequest       = "research.node_command.invalid_request"
	NodeCmdCodeRunNotRunning        = "research.node_command.run_not_running"
	NodeCmdCodeNotRetryable         = "research.node_command.not_retryable"
	NodeCmdCodeNoEligibleMember     = "research.node_command.no_eligible_member"
)

// NodeCommandDenied is a typed 409/403 denial with localized message_key.
type NodeCommandDenied struct {
	MachineCode string
	MessageKey  string
	Message     string // Chinese user-facing
	HTTPStatus  int    // 0 → 409
}

func (e *NodeCommandDenied) Error() string {
	if e == nil {
		return "research node command denied"
	}
	if e.Message != "" {
		return e.Message
	}
	return e.MachineCode
}

func denyNodeCommand(code, message string) *NodeCommandDenied {
	return &NodeCommandDenied{
		MachineCode: code,
		MessageKey:  code,
		Message:     message,
		HTTPStatus:  409,
	}
}

// DenyNodeCommand is the exported constructor for handler-layer denials.
func DenyNodeCommand(code, message string) *NodeCommandDenied {
	return denyNodeCommand(code, message)
}

// NodeCommandInput is the deep-module seam for continue|fork|retry|reassign.
type NodeCommandInput struct {
	SessionID            string
	WorkspaceID          string
	NodeID               string
	Action               string
	ClientRequestID      string
	ExpectedStateVersion *int64
	ActorType            string // user|agent
	ActorID              string
	Objective            string
	GoalPatch            string
	Strategy             string
	StrategyPatch        string
	SourceConstraints    json.RawMessage
	SourcePatch          json.RawMessage
	TargetAgentID        string // reassign: optional explicit member

	// Resolved anchor (handler fills before store call).
	AnchorKind       string // question|task|root|attempt|legacy
	AnchorQuestionID string
	AnchorTaskID     string
	AnchorTitle      string
}

// NodeCommandOutcome is returned to the HTTP layer (and stored in the audit event).
type NodeCommandOutcome struct {
	CommandID       string          `json:"command_id"`
	Action          string          `json:"action"`
	ClientRequestID string          `json:"client_request_id"`
	Replayed        bool            `json:"replayed"`
	StateVersion    int64           `json:"state_version"`
	Question        *Question       `json:"question,omitempty"`
	Task            *Task           `json:"task,omitempty"`
	Attempt         *Attempt        `json:"attempt,omitempty"`
	ParentLineage   ParentLineage   `json:"parent_lineage"`
	RetryLineage    *RetryLineage   `json:"retry_lineage,omitempty"`
	Reassign        *ReassignInfo   `json:"reassign,omitempty"`
	Assigned        *string         `json:"assigned,omitempty"`
	Queued          bool            `json:"queued"`
	Event           RunEvent        `json:"event"`
	Acceptance      json.RawMessage `json:"acceptance_criteria,omitempty"`
}

// ParentLineage mirrors LRM-1388 parent refs without rewriting branch history.
type ParentLineage struct {
	ParentQuestionID string `json:"parent_question_id,omitempty"`
	ParentTaskID     string `json:"parent_task_id,omitempty"`
	SourceNodeID     string `json:"source_node_id,omitempty"`
}

// RetryLineage records that a new attempt supersedes a prior failed/stale one
// without deleting the original attempt row.
type RetryLineage struct {
	PreviousAttemptID string `json:"previous_attempt_id,omitempty"`
	TaskID            string `json:"task_id"`
	NextAttemptNumber int    `json:"next_attempt_number,omitempty"`
}

// ReassignInfo is returned for action=reassign (from/to + selection reason).
type ReassignInfo struct {
	FromAgentID   string `json:"from_agent_id,omitempty"`
	ToAgentID     string `json:"to_agent_id"`
	Reason        string `json:"reason"`
	QueuePosition int    `json:"queue_position"`
}

func validateNodeCommandInput(in NodeCommandInput) error {
	if strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.WorkspaceID) == "" {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "会话无效，请刷新后重试")
	}
	if strings.TrimSpace(in.ClientRequestID) == "" || len(in.ClientRequestID) > 200 {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "缺少有效的请求标识，请重试")
	}
	switch in.Action {
	case NodeActionContinue, NodeActionFork:
		if in.AnchorKind == "" && in.AnchorQuestionID == "" && in.AnchorTaskID == "" {
			return denyNodeCommand(NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
		}
	case NodeActionRetry, NodeActionReassign:
		if strings.TrimSpace(in.AnchorTaskID) == "" {
			return denyNodeCommand(NodeCmdCodeNodeStale, "请从失败或停滞的任务节点发起重试或改派")
		}
	default:
		return denyNodeCommand(NodeCmdCodeActionNotAllowed, "当前节点不支持该操作")
	}
	if strings.TrimSpace(in.NodeID) == "" {
		return denyNodeCommand(NodeCmdCodeNodeStale, "节点已失效，请刷新画布后重试")
	}
	if len(in.GoalPatch) > 8<<10 || len(in.Strategy) > 8<<10 || len(in.StrategyPatch) > 8<<10 || len(in.Objective) > maxTaskObjectiveBytes {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "请求内容过长，请精简后重试")
	}
	if len(in.SourceConstraints) > 64<<10 || len(in.SourcePatch) > 64<<10 {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "来源约束过大，请精简后重试")
	}
	if len(in.SourceConstraints) > 0 && !json.Valid(in.SourceConstraints) {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "来源约束格式无效")
	}
	if len(in.SourcePatch) > 0 && !json.Valid(in.SourcePatch) {
		return denyNodeCommand(NodeCmdCodeInvalidRequest, "来源约束格式无效")
	}
	return nil
}

func strategyForNodeCommand(in NodeCommandInput) string {
	if patch := strings.TrimSpace(in.StrategyPatch); patch != "" {
		return patch
	}
	return strings.TrimSpace(in.Strategy)
}

func sourcePatchForNodeCommand(in NodeCommandInput) json.RawMessage {
	if len(in.SourcePatch) > 0 {
		return in.SourcePatch
	}
	return in.SourceConstraints
}

func nodeCommandClientKey(clientRequestID, suffix string) string {
	key := fmt.Sprintf("node-cmd:%s:%s", clientRequestID, suffix)
	if len(key) > maxClientKeyBytes {
		return key[:maxClientKeyBytes]
	}
	return key
}
