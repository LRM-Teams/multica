package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// HealthProbe reports the resident Computer's health map for a loopback
// health port, replacing the real HTTP probe in tests. It is the replaceable
// local dependency the Lifecycle talks through.
type HealthProbe func(ctx context.Context, port int) map[string]any

// ProbeHealth calls the local health endpoint on the given loopback port and
// returns the decoded JSON body. Any failure (transport, non-JSON, or the
// endpoint simply not being up) is reported as a "stopped" status.
func ProbeHealth(ctx context.Context, port int) map[string]any {
	addr := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return map[string]any{"status": "stopped"}
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return map[string]any{"status": "stopped"}
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return map[string]any{"status": "stopped"}
	}
	return result
}

// RequestEnvironmentSwitch asks the live resident to stop taking new work
// and waits until work admitted before that barrier finishes naturally.
func RequestEnvironmentSwitch(ctx context.Context, port int, controlToken string) error {
	return requestEnvironmentSwitchControl(ctx, port, controlToken, "prepare")
}

// ReleaseEnvironmentSwitch reopens claims when the caller cannot commit the
// prepared switch. A successful switch shuts the resident down instead.
func ReleaseEnvironmentSwitch(ctx context.Context, port int, controlToken string) error {
	return requestEnvironmentSwitchControl(ctx, port, controlToken, "release")
}

func requestEnvironmentSwitchControl(ctx context.Context, port int, controlToken, action string) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/environment-switch/%s", port, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Multica-Control-Token", strings.TrimSpace(controlToken))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("environment switch control returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return nil
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
func RequestShutdown(port int, audit ShutdownRequest) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/shutdown", port)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set(shutdownSourceHeader, strings.TrimSpace(audit.Source))
	req.Header.Set(shutdownActionHeader, strings.TrimSpace(audit.Action))
	if audit.RequestPID > 0 {
		req.Header.Set(shutdownRequestPIDHeader, strconv.Itoa(audit.RequestPID))
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
