package daemon

import (
	"crypto/sha256"
	"testing"
)

func registerTestAgentProxyServerCredential(t *testing.T, d *Daemon, workspaceID, runtimeID, agentID, token string) {
	t.Helper()
	hash := sha256.Sum256([]byte(t.Name() + "\x00" + workspaceID + "\x00" + runtimeID + "\x00" + agentID))
	d.agentProxyCredentialMu.Lock()
	if d.agentProxyCredentials == nil {
		d.agentProxyCredentials = make(map[[32]byte]authenticatedAgentProxy)
	}
	d.agentProxyCredentials[hash] = authenticatedAgentProxy{
		Inbox:     InboxKey{WorkspaceID: workspaceID, AgentID: agentID},
		RuntimeID: runtimeID,
		LaunchID:  t.Name(),
		ServerCredential: cachedAgentCredential{
			CredentialID: "test-credential", WorkspaceID: workspaceID, RuntimeID: runtimeID,
			AgentID: agentID, Token: token,
		},
	}
	d.agentProxyCredentialMu.Unlock()
	t.Cleanup(func() {
		d.agentProxyCredentialMu.Lock()
		delete(d.agentProxyCredentials, hash)
		d.agentProxyCredentialMu.Unlock()
	})
}
