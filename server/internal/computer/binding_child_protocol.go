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
	ProtocolVersion    int    `json:"protocol_version"`
	WorkspaceID        string `json:"workspace_id"`
	ComputerID         string `json:"computer_id"`
	ComputerGeneration int64  `json:"computer_generation"`
	RunnerGeneration   int64  `json:"runner_generation"`
	Environment        string `json:"environment"`
	Profile            string `json:"profile,omitempty"`
	ServerBaseURL      string `json:"server_base_url"`
	HostControlURL     string `json:"host_control_url"`
	BindingsRoot       string `json:"bindings_root"`
	WorkspacesRoot     string `json:"workspaces_root"`
	// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source.
	PreviousPackageUpgradeBootstrap bool `json:"previous_package_upgrade_bootstrap,omitempty"`
}

func (b BindingChildBootstrap) validated() (BindingChildBootstrap, error) {
	b.WorkspaceID = strings.TrimSpace(b.WorkspaceID)
	b.ComputerID = strings.TrimSpace(b.ComputerID)
	b.Environment = strings.TrimSpace(b.Environment)
	b.Profile = strings.TrimSpace(b.Profile)
	b.ServerBaseURL = strings.TrimSpace(b.ServerBaseURL)
	b.HostControlURL = strings.TrimSpace(b.HostControlURL)
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
	if b.ComputerGeneration < 1 {
		return BindingChildBootstrap{}, errors.New("Binding child Computer generation is required")
	}
	if b.RunnerGeneration < 1 {
		return BindingChildBootstrap{}, errors.New("Binding child Runner generation is required")
	}
	if b.Environment != "production" && b.Environment != "test" {
		return BindingChildBootstrap{}, fmt.Errorf("unsupported Binding child environment %q", b.Environment)
	}
	parsed, err := url.Parse(b.ServerBaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return BindingChildBootstrap{}, fmt.Errorf("Binding child server base URL is invalid")
	}
	hostControl, err := url.Parse(b.HostControlURL)
	if err != nil || hostControl.Scheme != "http" || hostControl.Hostname() != "127.0.0.1" || hostControl.Port() == "" {
		return BindingChildBootstrap{}, fmt.Errorf("Binding child Host control URL is invalid")
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
	ProtocolVersion  int    `json:"protocol_version"`
	WorkspaceID      string `json:"workspace_id"`
	RunnerGeneration int64  `json:"runner_generation"`
	PID              int    `json:"pid"`
	ControlURL       string `json:"control_url"`
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
	if strings.TrimSpace(ready.WorkspaceID) == "" || ready.RunnerGeneration < 1 || ready.PID < 1 {
		return errors.New("Binding child ready identity is incomplete")
	}
	if !validBindingChildControlURL(ready.ControlURL) {
		return errors.New("Binding child Ready control URL is invalid")
	}
	if err := json.NewEncoder(w).Encode(ready); err != nil {
		return fmt.Errorf("encode Binding child ready: %w", err)
	}
	return nil
}

func validBindingChildControlURL(raw string) bool {
	controlURL, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && controlURL.Scheme == "http" && controlURL.Hostname() == "127.0.0.1" && controlURL.Port() != ""
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
