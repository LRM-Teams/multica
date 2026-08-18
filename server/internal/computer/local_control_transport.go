package computer

import (
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ServiceControlEndpoint is the machine-owned IPC endpoint used by Binding
// children and lifecycle clients.
func ServiceControlEndpoint(residentRoot string) string {
	return localControlEndpoint(residentRoot, "service")
}

// RunnerControlEndpoint is one generation-fenced Binding runner's IPC
// endpoint. Its name is bounded even when Workspace identifiers are long.
func RunnerControlEndpoint(residentRoot string, identity BindingChildIdentity) string {
	return localControlEndpoint(residentRoot, strings.Join([]string{
		"runner", identity.WorkspaceID, strconv.FormatInt(identity.RunnerGeneration, 10), strconv.Itoa(identity.PID),
	}, "-"))
}

// ListenLocalControl binds a Unix-domain socket or Windows named pipe.
func ListenLocalControl(endpoint string) (net.Listener, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("local control endpoint is required")
	}
	return listenLocalControl(endpoint)
}

func validLocalControlEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	return validPlatformLocalControlEndpoint(endpoint)
}

func localControlClientFor(endpoint string, timeout time.Duration) (*localControlClient, string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if !validLocalControlEndpoint(endpoint) {
		return nil, "", errors.New("local control endpoint is invalid")
	}
	return &localControlClient{endpoint: endpoint, timeout: timeout}, "", nil
}

func localControlSocketDir(residentRoot string) string {
	return filepath.Join(strings.TrimSpace(residentRoot), "run")
}
