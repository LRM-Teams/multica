package daemon

import (
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// resetManagedAgentWorkspace owns the WorkspaceDaemon-local destructive half of Raft's
// agent:reset-workspace command. It deliberately refuses to mutate the Agent
// root until the exact managed launch and provider process are both gone.
func (runner *WorkspaceDaemon) resetManagedAgentWorkspace(payload protocol.AgentWorkspaceResetPayload) protocol.AgentWorkspaceResetResultPayload {
	result := protocol.AgentWorkspaceResetResultPayload{
		OperationID: payload.OperationID,
		AgentID:     payload.AgentID,
		Status:      protocol.AgentResetWorkspaceFailed,
	}
	fail := func(reason string) protocol.AgentWorkspaceResetResultPayload {
		result.ReasonCode = reason
		return result
	}
	if runner == nil || runner.processes == nil || runner.runtimes == nil {
		return fail("reset_dependencies_unavailable")
	}
	if err := payload.Validate(); err != nil {
		return fail("invalid_reset_command")
	}
	if _, running := runner.processes.Snapshot(payload.AgentID); running || runner.runtimes.agentHasLiveRuntime(payload.AgentID) {
		return fail("agent_still_running")
	}
	layout, err := execenv.ResolveAgentWorkspaceLayout(runner.workspacesRoot, runner.config.WorkspaceID, payload.AgentID)
	if err != nil {
		return fail("resolve_workspace_failed")
	}
	if err := execenv.RemoveAgentWorkspace(execenv.RemoveAgentWorkspaceParams{
		WorkspacesRoot: runner.workspacesRoot,
		WorkspaceID:    runner.config.WorkspaceID,
		AgentID:        payload.AgentID,
		AgentRoot:      layout.AgentRoot,
		Reason:         execenv.AgentWorkspaceRemovalFullReset,
	}); err != nil {
		return fail("remove_workspace_failed")
	}
	if _, err := execenv.ProvisionAgentWorkspace(runner.workspacesRoot, runner.config.WorkspaceID, payload.AgentID, runner.logger); err != nil {
		return fail("provision_workspace_failed")
	}
	result.Status = protocol.AgentResetWorkspaceSucceeded
	result.ReasonCode = ""
	return result
}
