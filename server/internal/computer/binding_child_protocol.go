package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const BindingChildProtocolVersion = 1

// BindingChildBootstrap is the immutable identity a Computer host gives one
// supervised Binding child. Credentials are deliberately absent: the child
// resolves the scoped credential from the permission-restricted Binding store.
type BindingChildBootstrap struct {
	ProtocolVersion int    `json:"protocolVersion"`
	WorkspaceID     string `json:"workspaceId"`
	ComputerID      string `json:"computerId"`
	StartIdentity   string `json:"startIdentity"`
	Environment     string `json:"environment"`
	Profile         string `json:"profile,omitempty"`
	ServerBaseURL   string `json:"serverBaseUrl"`
	ServiceEndpoint string `json:"serviceEndpoint"`
	BindingsRoot    string `json:"bindingsRoot"`
	WorkspacesRoot  string `json:"workspacesRoot"`
}

func (b BindingChildBootstrap) validated() (BindingChildBootstrap, error) {
	b.WorkspaceID = strings.TrimSpace(b.WorkspaceID)
	b.ComputerID = strings.TrimSpace(b.ComputerID)
	b.StartIdentity = strings.TrimSpace(b.StartIdentity)
	b.Environment = strings.TrimSpace(b.Environment)
	b.Profile = strings.TrimSpace(b.Profile)
	b.ServerBaseURL = strings.TrimSpace(b.ServerBaseURL)
	b.ServiceEndpoint = strings.TrimSpace(b.ServiceEndpoint)
	b.BindingsRoot = strings.TrimSpace(b.BindingsRoot)
	b.WorkspacesRoot = strings.TrimSpace(b.WorkspacesRoot)
	if b.ProtocolVersion != BindingChildProtocolVersion {
		return BindingChildBootstrap{}, fmt.Errorf("unsupported Binding child protocol version %d", b.ProtocolVersion)
	}
	if b.WorkspaceID == "" {
		return BindingChildBootstrap{}, errors.New("Binding child workspace-id is required")
	}
	if strings.ContainsAny(b.WorkspaceID, "/\\") || b.WorkspaceID == "." || b.WorkspaceID == ".." {
		return BindingChildBootstrap{}, errors.New("Binding child workspace-id is not a safe local identity")
	}
	if b.ComputerID == "" {
		return BindingChildBootstrap{}, errors.New("Binding child computer-id is required")
	}
	if b.StartIdentity == "" {
		return BindingChildBootstrap{}, errors.New("Binding child start identity is required")
	}
	if b.Environment != "production" && b.Environment != "test" {
		return BindingChildBootstrap{}, fmt.Errorf("unsupported Binding child environment %q", b.Environment)
	}
	parsed, err := url.Parse(b.ServerBaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return BindingChildBootstrap{}, fmt.Errorf("Binding child server base URL is invalid")
	}
	if !validLocalControlEndpoint(b.ServiceEndpoint) {
		return BindingChildBootstrap{}, fmt.Errorf("Binding runner service endpoint is invalid")
	}
	if b.BindingsRoot == "" {
		return BindingChildBootstrap{}, errors.New("Binding child Bindings root is required")
	}
	if b.WorkspacesRoot == "" {
		return BindingChildBootstrap{}, errors.New("Binding child Workspaces root is required")
	}
	return b, nil
}

// BindingChildReady is emitted only after the child owns a live Workspace
// Runner. The host validates every identity field before publishing readiness.
type BindingChildReady struct {
	ProtocolVersion int    `json:"protocolVersion"`
	WorkspaceID     string `json:"workspaceId"`
	StartIdentity   string `json:"startIdentity"`
	PID             int    `json:"pid"`
	RunnerEndpoint  string `json:"runnerEndpoint"`
}

func ReadBindingChildBootstrap(r io.Reader) (BindingChildBootstrap, error) {
	if r == nil {
		return BindingChildBootstrap{}, errors.New("Binding child bootstrap reader is required")
	}
	var bootstrap BindingChildBootstrap
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bootstrap); err != nil {
		return BindingChildBootstrap{}, fmt.Errorf("decode Binding child bootstrap: %w", err)
	}
	return bootstrap.validated()
}

func writeBindingChildBootstrap(w io.Writer, bootstrap BindingChildBootstrap) error {
	if w == nil {
		return errors.New("Binding child bootstrap writer is required")
	}
	validated, err := bootstrap.validated()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(w).Encode(validated); err != nil {
		return fmt.Errorf("encode Binding child bootstrap: %w", err)
	}
	return nil
}

func WriteBindingChildReady(w io.Writer, ready BindingChildReady) error {
	if w == nil {
		return errors.New("Binding child ready writer is required")
	}
	if ready.ProtocolVersion != BindingChildProtocolVersion {
		return fmt.Errorf("unsupported Binding child ready protocol version %d", ready.ProtocolVersion)
	}
	if strings.TrimSpace(ready.WorkspaceID) == "" || strings.TrimSpace(ready.StartIdentity) == "" || ready.PID < 1 {
		return errors.New("Binding child ready identity is incomplete")
	}
	if !validLocalControlEndpoint(ready.RunnerEndpoint) {
		return errors.New("Binding runner Ready endpoint is invalid")
	}
	if err := json.NewEncoder(w).Encode(ready); err != nil {
		return fmt.Errorf("encode Binding child ready: %w", err)
	}
	return nil
}

func readBindingChildReady(r io.Reader) (BindingChildReady, error) {
	if r == nil {
		return BindingChildReady{}, errors.New("Binding child ready reader is required")
	}
	var ready BindingChildReady
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ready); err != nil {
		return BindingChildReady{}, fmt.Errorf("decode Binding child ready: %w", err)
	}
	return ready, nil
}
