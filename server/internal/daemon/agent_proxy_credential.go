package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/internal/turntransport"
)

const (
	AgentProxyURLEnv       = "MULTICA_AGENT_PROXY_URL"
	AgentProxyTokenFileEnv = "MULTICA_AGENT_PROXY_TOKEN_FILE"
	// AgentProxyCLIWrapperEnv carries only the launch-pinned wrapper path, not
	// credentials. The real CLI uses it to recover when a login shell rebuilds
	// PATH and would otherwise bypass the authenticated wrapper.
	AgentProxyCLIWrapperEnv       = "MULTICA_AGENT_CLI_WRAPPER"
	AgentProxyTokenHeader         = "X-Multica-Agent-Proxy-Token"
	agentProxyCredentialTokenName = "token"
)

var ErrAgentProxyCredentialInvalid = errors.New("Agent Proxy credential is invalid")

type authenticatedAgentProxy struct {
	Inbox     InboxKey
	RuntimeID string
}

type agentProxyCLITransport struct {
	daemon      *Daemon
	credential  [32]byte
	inbox       InboxKey
	runtimeID   string
	root        string
	wrapperPath string
	tokenFile   string
	closeOnce   sync.Once
	closeErr    error
}

// prepareAgentProxyCLITransport creates the process-scoped local credential
// carrier used by generated Agent CLI wrappers. Authentication state belongs
// to the Machine Service and process launch; Message coordinators never see the
// token or its file path.
func (d *Daemon) prepareAgentProxyCLITransport(
	key InboxKey,
	runtimeID, multicaBin string,
) (*agentProxyCLITransport, error) {
	if d == nil {
		return nil, errors.New("Machine Service is unavailable")
	}
	var err error
	key, err = key.normalized()
	if err != nil {
		return nil, err
	}
	runtimeID = strings.TrimSpace(runtimeID)
	multicaBin = filepath.Clean(strings.TrimSpace(multicaBin))
	if runtimeID == "" {
		return nil, errors.New("Agent Proxy runtime_id is required")
	}
	if multicaBin == "." || !filepath.IsAbs(multicaBin) {
		return nil, errors.New("Agent Proxy multica binary path must be absolute")
	}
	if d.cfg.HealthPort <= 0 {
		return nil, errors.New("Agent Proxy local port is unavailable")
	}
	token, err := newAgentProxyToken()
	if err != nil {
		return nil, fmt.Errorf("create Agent Proxy credential: %w", err)
	}
	credentialHash := sha256.Sum256([]byte(token))
	credentialID := uuid.NewString()
	root := filepath.Join(agentworkspace.Root(d.cfg.WorkspacesRoot, key.WorkspaceID, key.AgentID), "runtime", "agent-proxy", credentialID)
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Agent Proxy transport directory: %w", err)
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = os.RemoveAll(root)
		}
	}()
	for _, dir := range []string{root, binDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("protect Agent Proxy transport directory: %w", err)
		}
	}
	tokenFile := filepath.Join(root, agentProxyCredentialTokenName)
	if err := writeAgentProxyFileAtomic(tokenFile, []byte(token), 0o600); err != nil {
		return nil, fmt.Errorf("write Agent Proxy token file: %w", err)
	}
	wrapperPath := filepath.Join(binDir, turntransport.CliWrapperFilename())
	wrapper := agentProxyCLIWrapperBody(agentProxyCLIWrapperConfig{
		WorkspaceID: key.WorkspaceID,
		AgentID:     key.AgentID,
		ProxyURL:    fmt.Sprintf("http://127.0.0.1:%d", d.cfg.HealthPort),
		TokenFile:   tokenFile,
		MulticaBin:  multicaBin,
	})
	if err := writeAgentProxyFileAtomic(wrapperPath, []byte(wrapper), 0o700); err != nil {
		return nil, fmt.Errorf("write Agent Proxy CLI wrapper: %w", err)
	}

	context := authenticatedAgentProxy{Inbox: key, RuntimeID: runtimeID}
	d.agentProxyCredentialMu.Lock()
	if d.agentProxyCredentials == nil {
		d.agentProxyCredentials = make(map[[32]byte]authenticatedAgentProxy)
	}
	if _, exists := d.agentProxyCredentials[credentialHash]; exists {
		d.agentProxyCredentialMu.Unlock()
		return nil, errors.New("Agent Proxy credential collision")
	}
	d.agentProxyCredentials[credentialHash] = context
	d.agentProxyCredentialMu.Unlock()

	transport := &agentProxyCLITransport{
		daemon: d, credential: credentialHash, inbox: key, runtimeID: runtimeID,
		root: root, wrapperPath: wrapperPath, tokenFile: tokenFile,
	}
	cleanupOnFailure = false
	d.recordAgentProxyCredentialLifecycle(key, runtimeID, "agent_proxy_credential_issued", "accepted", "")
	return transport, nil
}

func (d *Daemon) authenticateAgentProxyToken(token string) (authenticatedAgentProxy, error) {
	if d == nil {
		return authenticatedAgentProxy{}, ErrAgentProxyCredentialInvalid
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return authenticatedAgentProxy{}, ErrAgentProxyCredentialInvalid
	}
	hash := sha256.Sum256([]byte(token))
	d.agentProxyCredentialMu.RLock()
	credential, ok := d.agentProxyCredentials[hash]
	d.agentProxyCredentialMu.RUnlock()
	if !ok {
		return authenticatedAgentProxy{}, ErrAgentProxyCredentialInvalid
	}
	return credential, nil
}

func (t *agentProxyCLITransport) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		if t.daemon != nil {
			t.daemon.agentProxyCredentialMu.Lock()
			delete(t.daemon.agentProxyCredentials, t.credential)
			t.daemon.agentProxyCredentialMu.Unlock()
		}
		if err := os.RemoveAll(t.root); err != nil {
			t.closeErr = fmt.Errorf("remove Agent Proxy transport: %w", err)
			if t.daemon != nil {
				t.daemon.recordAgentProxyCredentialLifecycle(t.inbox, t.runtimeID, "agent_proxy_credential_revoked", "degraded", "credential_file_cleanup_failed")
			}
			return
		}
		if t.daemon != nil {
			t.daemon.recordAgentProxyCredentialLifecycle(t.inbox, t.runtimeID, "agent_proxy_credential_revoked", "revoked", "")
		}
	})
	return t.closeErr
}

func (d *Daemon) recordAgentProxyCredentialLifecycle(key InboxKey, runtimeID, phase, outcome, reasonCode string) {
	d.recordRunnerDiagnostic(key.WorkspaceID, diagnosticlog.Event{
		Name:      diagnosticlog.EventAgentProcessStateChanged,
		Level:     diagnosticLevel(outcome),
		Component: "credential_proxy",
		Identity:  diagnosticlog.Identity{AgentID: key.AgentID, RuntimeID: runtimeID},
		Fields:    diagnosticlog.Fields{Phase: phase, Outcome: outcome, ReasonCode: reasonCode},
	})
}

type agentProxyCLIWrapperConfig struct {
	WorkspaceID string
	AgentID     string
	ProxyURL    string
	TokenFile   string
	MulticaBin  string
}

func agentProxyCLIWrapperBody(config agentProxyCLIWrapperConfig) string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\n" +
			"set \"" + AgentProxyCLIWrapperEnv + "=\"\r\n" +
			"set \"MULTICA_AGENT_ID=" + config.AgentID + "\"\r\n" +
			"set \"MULTICA_WORKSPACE_ID=" + config.WorkspaceID + "\"\r\n" +
			"set \"" + AgentProxyURLEnv + "=" + config.ProxyURL + "\"\r\n" +
			"set \"" + AgentProxyTokenFileEnv + "=" + config.TokenFile + "\"\r\n" +
			"call \"" + config.MulticaBin + "\" %*\r\n" +
			"exit /b %ERRORLEVEL%\r\n"
	}
	return "#!/bin/sh\n" +
		"unset " + AgentProxyCLIWrapperEnv + "\n" +
		"export MULTICA_AGENT_ID=" + shellQuote(config.AgentID) + "\n" +
		"export MULTICA_WORKSPACE_ID=" + shellQuote(config.WorkspaceID) + "\n" +
		"export " + AgentProxyURLEnv + "=" + shellQuote(config.ProxyURL) + "\n" +
		"export " + AgentProxyTokenFileEnv + "=" + shellQuote(config.TokenFile) + "\n" +
		"exec " + shellQuote(config.MulticaBin) + " \"$@\"\n"
}

func newAgentProxyToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "map_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func writeAgentProxyFileAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agent-proxy-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return os.Chmod(path, mode)
}
