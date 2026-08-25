package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const WorkspaceDaemonProtocolVersion = 1

// WorkspaceDaemonBootstrap is the DaemonCore-owned launch config for one
// WorkspaceDaemon process. It has no process-instance ticket: DaemonCore only
// passes launch config, and the process generates daemonInstanceId and reports
// it on Ready. Credentials are absent: the child resolves the scoped
// credential from the permission-restricted Workspace binding store.
type WorkspaceDaemonBootstrap struct {
	ProtocolVersion int    `json:"protocolVersion"`
	WorkspaceID     string `json:"workspaceId"`
	ComputerID      string `json:"computerId"`
	Environment     string `json:"environment"`
	Profile         string `json:"profile,omitempty"`
	ServerBaseURL   string `json:"serverBaseUrl"`
	ServiceEndpoint string `json:"serviceEndpoint"`
	BindingsRoot    string `json:"bindingsRoot"`
	WorkspacesRoot  string `json:"workspacesRoot"`
}

func (b WorkspaceDaemonBootstrap) validated() (WorkspaceDaemonBootstrap, error) {
	b.WorkspaceID = strings.TrimSpace(b.WorkspaceID)
	b.ComputerID = strings.TrimSpace(b.ComputerID)
	b.Environment = strings.TrimSpace(b.Environment)
	b.Profile = strings.TrimSpace(b.Profile)
	b.ServerBaseURL = strings.TrimSpace(b.ServerBaseURL)
	b.ServiceEndpoint = strings.TrimSpace(b.ServiceEndpoint)
	b.BindingsRoot = strings.TrimSpace(b.BindingsRoot)
	b.WorkspacesRoot = strings.TrimSpace(b.WorkspacesRoot)
	if b.ProtocolVersion != WorkspaceDaemonProtocolVersion {
		return WorkspaceDaemonBootstrap{}, fmt.Errorf("unsupported WorkspaceDaemon protocol version %d", b.ProtocolVersion)
	}
	if b.WorkspaceID == "" {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon workspace-id is required")
	}
	if strings.ContainsAny(b.WorkspaceID, "/\\") || b.WorkspaceID == "." || b.WorkspaceID == ".." {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon workspace-id is not a safe local identity")
	}
	if b.ComputerID == "" {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon computer-id is required")
	}
	if b.Environment != "production" && b.Environment != "test" {
		return WorkspaceDaemonBootstrap{}, fmt.Errorf("unsupported WorkspaceDaemon environment %q", b.Environment)
	}
	parsed, err := url.Parse(b.ServerBaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return WorkspaceDaemonBootstrap{}, fmt.Errorf("WorkspaceDaemon server base URL is invalid")
	}
	if !validLocalControlEndpoint(b.ServiceEndpoint) {
		return WorkspaceDaemonBootstrap{}, fmt.Errorf("WorkspaceDaemon Computer endpoint is invalid")
	}
	if b.BindingsRoot == "" {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon Bindings root is required")
	}
	if b.WorkspacesRoot == "" {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon Workspaces root is required")
	}
	return b, nil
}

// WorkspaceDaemonReady is emitted after the process owns a live Workspace
// connection. DaemonCore records its generated daemonInstanceId only after
// matching this Ready message to the process handle it spawned.
type WorkspaceDaemonReady struct {
	ProtocolVersion  int    `json:"protocolVersion"`
	WorkspaceID      string `json:"workspaceId"`
	DaemonInstanceID string `json:"daemonInstanceId"`
	PID              int    `json:"pid"`
	RunnerEndpoint   string `json:"runnerEndpoint"`
}

func ReadWorkspaceDaemonBootstrap(r io.Reader) (WorkspaceDaemonBootstrap, error) {
	if r == nil {
		return WorkspaceDaemonBootstrap{}, errors.New("WorkspaceDaemon bootstrap reader is required")
	}
	var bootstrap WorkspaceDaemonBootstrap
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bootstrap); err != nil {
		return WorkspaceDaemonBootstrap{}, fmt.Errorf("decode WorkspaceDaemon bootstrap: %w", err)
	}
	return bootstrap.validated()
}

func writeWorkspaceDaemonBootstrap(w io.Writer, bootstrap WorkspaceDaemonBootstrap) error {
	if w == nil {
		return errors.New("WorkspaceDaemon bootstrap writer is required")
	}
	validated, err := bootstrap.validated()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(w).Encode(validated); err != nil {
		return fmt.Errorf("encode WorkspaceDaemon bootstrap: %w", err)
	}
	return nil
}

func WriteWorkspaceDaemonReady(w io.Writer, ready WorkspaceDaemonReady) error {
	if w == nil {
		return errors.New("WorkspaceDaemon ready writer is required")
	}
	if ready.ProtocolVersion != WorkspaceDaemonProtocolVersion {
		return fmt.Errorf("unsupported WorkspaceDaemon ready protocol version %d", ready.ProtocolVersion)
	}
	if strings.TrimSpace(ready.WorkspaceID) == "" || strings.TrimSpace(ready.DaemonInstanceID) == "" || ready.PID < 1 {
		return errors.New("WorkspaceDaemon ready identity is incomplete")
	}
	if !validLocalControlEndpoint(ready.RunnerEndpoint) {
		return errors.New("WorkspaceDaemon Ready endpoint is invalid")
	}
	if err := json.NewEncoder(w).Encode(ready); err != nil {
		return fmt.Errorf("encode WorkspaceDaemon ready: %w", err)
	}
	return nil
}

func readWorkspaceDaemonReady(r io.Reader) (WorkspaceDaemonReady, error) {
	if r == nil {
		return WorkspaceDaemonReady{}, errors.New("WorkspaceDaemon ready reader is required")
	}
	var ready WorkspaceDaemonReady
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ready); err != nil {
		return WorkspaceDaemonReady{}, fmt.Errorf("decode WorkspaceDaemon ready: %w", err)
	}
	return ready, nil
}
