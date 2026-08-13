package protocol

import "fmt"

const (
	AgentLifecycleCommandAccepted  = "accepted"
	AgentLifecycleCommandDuplicate = "duplicate"
)

// WorkspaceRunnerAgentLifecyclePayload is the direct Raft-style command for
// one user-requested Agent restart. OperationID is both the server operation
// identity and the Runner-local duplicate receipt identity.
type WorkspaceRunnerAgentLifecyclePayload struct {
	OperationID string `json:"operationId"`
	WorkspaceID string `json:"workspaceId"`
	AgentID     string `json:"agentId"`
	RuntimeID   string `json:"runtimeId"`
	ActionKind  string `json:"actionKind"`
}

// WorkspaceRunnerAgentLifecycleAckPayload reports command acceptance before
// destructive work begins. Duplicate means the same command is already in
// flight or its terminal receipt is still cached.
type WorkspaceRunnerAgentLifecycleAckPayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
	RuntimeID   string `json:"runtimeId"`
	Outcome     string `json:"outcome"`
}

// WorkspaceRunnerAgentLifecycleResultPayload is the terminal outcome of the
// accepted command. The server operation row remains the business record;
// this frame is only its direct Runner receipt.
type WorkspaceRunnerAgentLifecycleResultPayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
	RuntimeID   string `json:"runtimeId"`
	Status      string `json:"status"`
	Step        string `json:"step,omitempty"`
	ReasonCode  string `json:"reasonCode,omitempty"`
}

func (payload WorkspaceRunnerAgentLifecyclePayload) Validate() error {
	if err := validateRequiredIDs(payload.OperationID, payload.WorkspaceID, payload.AgentID, payload.RuntimeID); err != nil {
		return err
	}
	switch payload.ActionKind {
	case "restart", "reset_session_restart", "full_reset_restart":
		return nil
	default:
		return fmt.Errorf("invalid Agent lifecycle action")
	}
}

func (payload WorkspaceRunnerAgentLifecycleAckPayload) Validate() error {
	if err := validateRequiredIDs(payload.OperationID, payload.AgentID, payload.RuntimeID); err != nil {
		return err
	}
	if payload.Outcome != AgentLifecycleCommandAccepted && payload.Outcome != AgentLifecycleCommandDuplicate {
		return fmt.Errorf("invalid Agent lifecycle acknowledgement outcome")
	}
	return nil
}

func (payload WorkspaceRunnerAgentLifecycleResultPayload) Validate() error {
	if err := validateRequiredIDs(payload.OperationID, payload.AgentID, payload.RuntimeID); err != nil {
		return err
	}
	if payload.Status != "succeeded" && payload.Status != "failed" {
		return fmt.Errorf("invalid Agent lifecycle result status")
	}
	return nil
}
