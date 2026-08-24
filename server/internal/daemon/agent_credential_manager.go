package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"golang.org/x/sync/singleflight"
)

type agentCredentialMode uint8

const (
	agentCredentialCacheFirst agentCredentialMode = iota
	agentCredentialRevalidate
)

type agentCredentialKey struct {
	WorkspaceID string
	RuntimeID   string
	AgentID     string
}

type agentCredentialManager struct {
	daemon  *Daemon
	ensures singleflight.Group
}

func (m *agentCredentialManager) Get(ctx context.Context, key agentCredentialKey, mode agentCredentialMode) (cachedAgentCredential, error) {
	return m.get(ctx, key, mode, nil)
}

func (m *agentCredentialManager) get(ctx context.Context, key agentCredentialKey, mode agentCredentialMode, taskLog *slog.Logger) (cachedAgentCredential, error) {
	if m == nil || m.daemon == nil {
		return cachedAgentCredential{}, errors.New("Agent credential manager is unavailable")
	}
	key.WorkspaceID = strings.TrimSpace(key.WorkspaceID)
	key.RuntimeID = strings.TrimSpace(key.RuntimeID)
	key.AgentID = strings.TrimSpace(key.AgentID)
	if key.WorkspaceID == "" || key.RuntimeID == "" || key.AgentID == "" {
		return cachedAgentCredential{}, errors.New("workspace_id, runtime_id, and agent_id are required")
	}
	if mode == agentCredentialCacheFirst {
		if credential, ok := readCachedAgentCredential(m.daemon.cfg, key.WorkspaceID, key.RuntimeID, key.AgentID, time.Now()); ok {
			return credential, nil
		}
	}
	if m.daemon.client == nil {
		return cachedAgentCredential{}, errors.New("Agent credential manager is unavailable")
	}

	result := m.ensures.DoChan(key.WorkspaceID+"\x00"+key.RuntimeID+"\x00"+key.AgentID, func() (any, error) {
		ensureCtx, cancel := cli.APIContext(m.daemon.recoveryContext())
		defer cancel()
		return m.daemon.ensureAgentCredentialOnce(ensureCtx, key.WorkspaceID, key.RuntimeID, key.AgentID, taskLog)
	})

	select {
	case <-ctx.Done():
		return cachedAgentCredential{}, ctx.Err()
	case outcome := <-result:
		if outcome.Err != nil {
			return cachedAgentCredential{}, outcome.Err
		}
		credential, ok := outcome.Val.(cachedAgentCredential)
		if !ok {
			return cachedAgentCredential{}, errors.New("invalid Agent credential manager result")
		}
		return credential, nil
	}
}

func (d *Daemon) credentialManager() *agentCredentialManager {
	d.agentCredentialManagerOnce.Do(func() {
		d.agentCredentialManager = &agentCredentialManager{daemon: d}
	})
	return d.agentCredentialManager
}

func (d *Daemon) messageAgentCredential(ctx context.Context, workspaceID, agentID string) (cachedAgentCredential, error) {
	runner := d.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		return cachedAgentCredential{}, errors.New("Agent message runtime is unavailable")
	}
	runtimeID := runner.messageRuntimeID(agentID)
	if strings.TrimSpace(runtimeID) == "" {
		return cachedAgentCredential{}, errors.New("Agent message runtime is unavailable")
	}
	credential, err := d.credentialManager().Get(ctx, agentCredentialKey{
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		AgentID:     agentID,
	}, agentCredentialCacheFirst)
	if err != nil {
		return cachedAgentCredential{}, fmt.Errorf("Agent credential is unavailable: %w", err)
	}
	return credential, nil
}
