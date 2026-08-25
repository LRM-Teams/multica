package computer

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	shutdownSourceHeader     = "X-Multica-Shutdown-Source"
	shutdownActionHeader     = "X-Multica-Shutdown-Action"
	shutdownRequestPIDHeader = "X-Multica-Shutdown-Request-Pid"
)

// ShutdownRequest identifies the local process and lifecycle operation asking
// the resident Computer to exit. It is diagnostic metadata, not authorization.
type ShutdownRequest struct {
	Source     string
	Action     string
	RequestPID int
}

// ServiceProbe reports the resident Computer's health map through local IPC,
// replacing the real service probe in tests. It is the replaceable
// local dependency the Lifecycle talks through.
type ServiceProbe func(ctx context.Context, endpoint string) map[string]any

// ProbeHealth calls the local health method on the Computer service IPC and
// returns the decoded JSON body. Any failure (transport, non-JSON, or the
// endpoint simply not being up) is reported as a "stopped" status.
func ProbeHealth(ctx context.Context, endpoint string) map[string]any {
	var result map[string]any
	if err := callLocalJSON(ctx, endpoint, LocalControlServiceStatusOperation, 2*time.Second, nil, nil, &result); err != nil {
		return map[string]any{"status": "stopped"}
	}
	return result
}

// RequestMachineUpgradeStatus reads the resident's current or most recent
// upgrade phase. It is unauthenticated and read-only, like service status.
func RequestMachineUpgradeStatus(ctx context.Context, endpoint string) (MachineUpgradeStatus, error) {
	var status MachineUpgradeStatus
	err := callLocalJSON(ctx, endpoint, LocalControlUpgradeStatusOperation, 2*time.Second, nil, nil, &status)
	return status, err
}

// RequestEnvironmentSwitch asks the live resident to stop taking new work
// and waits until work admitted before that barrier finishes naturally.
func RequestEnvironmentSwitch(ctx context.Context, endpoint, controlToken string) error {
	return requestEnvironmentSwitchControl(ctx, endpoint, controlToken, "prepare")
}

// ReleaseEnvironmentSwitch reopens claims when the caller cannot commit the
// prepared switch. A successful switch shuts the resident down instead.
func ReleaseEnvironmentSwitch(ctx context.Context, endpoint, controlToken string) error {
	return requestEnvironmentSwitchControl(ctx, endpoint, controlToken, "release")
}

func requestEnvironmentSwitchControl(ctx context.Context, endpoint, controlToken, action string) error {
	return callLocalJSON(ctx, endpoint, LocalControlWorkspaceEnvironmentOperation, 0,
		map[string]string{"X-Multica-Control-Token": strings.TrimSpace(controlToken)},
		map[string]string{"action": action}, nil)
}

// Alive reports whether a health response indicates a live resident process
// on the port — either fully "running" (ready) or still "starting" (port
// bound, preflight in progress). Lifecycle commands that only need to know
// "is the Computer there" (already-running guard, restart, stop) use this,
// whereas a start's readiness wait gates on the stricter "running".
func Alive(health map[string]any) bool {
	switch health["status"] {
	case "running", "starting":
		return true
	default:
		return false
	}
}

// RequestShutdown POSTs to the resident process's /shutdown endpoint to ask
// it to exit gracefully and includes non-sensitive audit metadata. Returns an
// error if the request could not be delivered (network error, non-2xx status,
// or the endpoint predates this change).
func RequestShutdown(endpoint string, audit ShutdownRequest) error {
	headers := map[string]string{shutdownSourceHeader: strings.TrimSpace(audit.Source), shutdownActionHeader: strings.TrimSpace(audit.Action)}
	if audit.RequestPID > 0 {
		headers[shutdownRequestPIDHeader] = fmt.Sprintf("%d", audit.RequestPID)
	}
	return callLocalJSON(context.Background(), endpoint, LocalControlRestartServiceOperation, 2*time.Second, headers, nil, nil)
}
