package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// agentWorkspaceRetentionLoop periodically destroys the durable
// .multica/agents/<agentID> workspace of agents that have been archived
// (soft-deleted) for longer than the server-owned retention window (task
// #96 / #204, default 30 days). Defaults to dry-run: MULTICA_AGENT_WORKSPACE_RETENTION_DRY_RUN=false
// is required to perform real deletions. This is a destructive job — see
// runAgentWorkspaceRetention's doc comment for the safety invariants.
func (d *Daemon) agentWorkspaceRetentionLoop(ctx context.Context) {
	if !d.cfg.AgentWorkspaceRetentionEnabled {
		d.logger.Info("agent-workspace-retention: disabled")
		return
	}
	d.logger.Info("agent-workspace-retention: started",
		"interval", d.cfg.AgentWorkspaceRetentionInterval,
		"dry_run", d.cfg.AgentWorkspaceRetentionDryRun,
	)
	if !d.cfg.AgentWorkspaceRetentionDryRun {
		d.logger.Warn("agent-workspace-retention: dry run is DISABLED — this daemon will permanently destroy archived agents' .multica/agents/<id> directories")
	}

	// Lower priority / less time-sensitive than task GC (30-day granularity
	// vs hour-scale), so a longer post-boot stagger is fine.
	if err := sleepWithContext(ctx, time.Minute); err != nil {
		return
	}
	d.runAgentWorkspaceRetention(ctx)

	ticker := time.NewTicker(d.cfg.AgentWorkspaceRetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runAgentWorkspaceRetention(ctx)
		}
	}
}

// runAgentWorkspaceRetention scans every workspace directory under
// WorkspacesRoot for .multica/agents/<id> entries and asks the server which
// of those agent IDs are archived past the retention window.
//
// Safety invariants (task #96, Parker 2026-08-02 — hard requirements, not
// suggestions):
//  1. The server's answer is never trusted as a set of paths to delete. Each
//     returned ID must also appear in THIS scan's own candidate batch (a
//     stray/forged response can't make the daemon touch a directory it
//     didn't just enumerate itself), and must resolve via
//     execenv.RemoveAgentWorkspace's own canonical-root + symlink-escape
//     checks before anything is removed.
//  2. Dry-run is the default. Only MULTICA_AGENT_WORKSPACE_RETENTION_DRY_RUN=false
//     performs a real deletion.
func (d *Daemon) runAgentWorkspaceRetention(ctx context.Context) {
	root := d.cfg.WorkspacesRoot
	wsEntries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		d.logger.Warn("agent-workspace-retention: read workspaces root failed", "error", err)
		return
	}

	for _, wsEntry := range wsEntries {
		if ctx.Err() != nil {
			return
		}
		if !wsEntry.IsDir() || gcSkipWorkspaceRootName(wsEntry.Name()) {
			continue
		}
		d.retainAgentWorkspacesForWorkspace(ctx, root, wsEntry.Name())
	}
}

func (d *Daemon) retainAgentWorkspacesForWorkspace(ctx context.Context, workspacesRoot, workspaceID string) {
	agentsDir := filepath.Join(workspacesRoot, workspaceID, managedAgentWorkspaceNamespace, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		d.logger.Warn("agent-workspace-retention: read agents dir failed", "workspace_id", workspaceID, "error", err)
		return
	}

	candidateSet := make(map[string]bool, len(entries))
	candidateIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidateIDs = append(candidateIDs, e.Name())
		candidateSet[e.Name()] = true
	}
	if len(candidateIDs) == 0 {
		return
	}

	eligible, err := d.client.CheckAgentWorkspaceRetention(ctx, workspaceID, candidateIDs)
	if err != nil {
		d.logger.Warn("agent-workspace-retention: check failed", "workspace_id", workspaceID, "error", err)
		return
	}

	for _, agentID := range eligible {
		if !candidateSet[agentID] {
			d.logger.Warn("agent-workspace-retention: server returned an agent ID outside this scan's own batch, refusing",
				"workspace_id", workspaceID, "agent_id", agentID)
			continue
		}
		d.destroyAgentWorkspace(workspaceID, agentID, workspacesRoot, filepath.Join(agentsDir, agentID))
	}
}

func (d *Daemon) destroyAgentWorkspace(workspaceID, agentID, workspacesRoot, rootDir string) {
	if d.cfg.AgentWorkspaceRetentionDryRun {
		d.logger.Info("agent-workspace-retention: DRY RUN would destroy", "workspace_id", workspaceID, "agent_id", agentID, "path", rootDir)
		return
	}

	// A 30-day-elapsed archival is treated as sufficient proof of
	// quiescence: an agent that was archived (and therefore excluded from
	// dispatch — archived agents don't receive new work) cannot have a turn
	// or provider lease survive a month. execenv.RemoveAgentWorkspace still
	// independently re-derives and checks the canonical root before
	// touching disk.
	err := execenv.RemoveAgentWorkspace(execenv.RemoveAgentWorkspaceParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		RootDir:        rootDir,
		Reason:         execenv.AgentWorkspaceRemovalAgentDeleted,
		Proof: execenv.AgentWorkspaceRemovalProof{
			NoActiveTurn:          true,
			NoActiveProviderLease: true,
		},
	})
	if err != nil {
		d.logger.Warn("agent-workspace-retention: destroy failed", "workspace_id", workspaceID, "agent_id", agentID, "error", err)
		return
	}
	d.logger.Info("agent-workspace-retention: destroyed", "workspace_id", workspaceID, "agent_id", agentID)
}
