package protocol

import "fmt"

// Workspace Runner Attachment frames carry no Workspace identity. The
// authenticated Runner connection is the sole Workspace authority.
type agentAttachmentPayload struct {
	AgentID              string `json:"agentId"`
	RuntimeID            string `json:"runtimeId"`
	AttachmentGeneration int64  `json:"attachmentGeneration"`
	LifecycleSeq         int64  `json:"lifecycleSeq"`
}

// WorkspaceRunnerAgentAttachPayload assigns or moves durable local
// responsibility for an Agent. It does not request a managed launch.
type WorkspaceRunnerAgentAttachPayload agentAttachmentPayload

// WorkspaceRunnerAgentAttachedPayload is the durable acceptance receipt.
type WorkspaceRunnerAgentAttachedPayload agentAttachmentPayload

// WorkspaceRunnerAgentDetachPayload relinquishes durable local responsibility
// independently from stopping any active managed launch.
type WorkspaceRunnerAgentDetachPayload agentAttachmentPayload

// WorkspaceRunnerAgentDetachedPayload is the durable receipt for the
// correlated detach command.
type WorkspaceRunnerAgentDetachedPayload agentAttachmentPayload

// WorkspaceRunnerAttachmentReplayRequest and Ack advance only cursors for
// Runtimes authenticated on this Runner's fixed Workspace connection.
type WorkspaceRunnerAttachmentReplayRequest struct {
	RuntimeCursors map[string]int64 `json:"runtimeCursors"`
}

type WorkspaceRunnerAttachmentReplayEnd struct {
	RuntimeCursors map[string]int64 `json:"runtimeCursors"`
}

type WorkspaceRunnerAttachmentReplayAck struct {
	RuntimeCursors map[string]int64 `json:"runtimeCursors"`
}

func (payload WorkspaceRunnerAgentAttachPayload) Validate() error {
	return validateAgentAttachmentPayload(agentAttachmentPayload(payload))
}

func (payload WorkspaceRunnerAgentAttachedPayload) Validate() error {
	return validateAgentAttachmentPayload(agentAttachmentPayload(payload))
}

func (payload WorkspaceRunnerAgentDetachPayload) Validate() error {
	return validateAgentAttachmentPayload(agentAttachmentPayload(payload))
}

func (payload WorkspaceRunnerAgentDetachedPayload) Validate() error {
	return validateAgentAttachmentPayload(agentAttachmentPayload(payload))
}

func (payload WorkspaceRunnerAttachmentReplayRequest) Validate() error {
	return validateAttachmentReplayCursors(payload.RuntimeCursors)
}

func (payload WorkspaceRunnerAttachmentReplayEnd) Validate() error {
	return validateAttachmentReplayCursors(payload.RuntimeCursors)
}

func (payload WorkspaceRunnerAttachmentReplayAck) Validate() error {
	return validateAttachmentReplayCursors(payload.RuntimeCursors)
}

func validateAttachmentReplayCursors(cursors map[string]int64) error {
	for runtimeID, cursor := range cursors {
		if err := validateRequiredIDs(runtimeID); err != nil || cursor < 0 {
			return fmt.Errorf("invalid Attachment replay cursor")
		}
	}
	return nil
}

func validateAgentAttachmentPayload(payload agentAttachmentPayload) error {
	if err := validateRequiredIDs(payload.AgentID, payload.RuntimeID); err != nil {
		return err
	}
	if payload.AttachmentGeneration <= 0 {
		return fmt.Errorf("attachment generation must be positive")
	}
	if payload.LifecycleSeq <= 0 {
		return fmt.Errorf("attachment lifecycle sequence must be positive")
	}
	return nil
}
