package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workspaceRunnerLoop owns one WebSocket per authenticated workspace. It is
// intentionally separate from the legacy runtime-multiplexed wake socket: a
// Runner survives a workspace with zero runtimes and can never receive another
// workspace's commands.
func (d *Daemon) workspaceRunnerLoop(ctx context.Context) {
	if d == nil || d.runtimeSet == nil {
		return
	}
	changes, unsub := d.runtimeSet.Subscribe()
	defer unsub()
	for {
		workspaceIDs := d.workspaceRunnerWorkspaceIDs()
		child, cancel := context.WithCancel(ctx)
		for _, workspaceID := range workspaceIDs {
			go d.runWorkspaceRunner(child, workspaceID)
		}
		select {
		case <-ctx.Done():
			cancel()
			return
		case <-changes:
			cancel()
		}
	}
}

func (d *Daemon) workspaceRunnerWorkspaceIDs() []string {
	d.mu.Lock()
	ids := make([]string, 0, len(d.workspaces))
	for workspaceID := range d.workspaces {
		ids = append(ids, workspaceID)
	}
	d.mu.Unlock()
	sort.Strings(ids)
	return ids
}

func (d *Daemon) runWorkspaceRunner(ctx context.Context, workspaceID string) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := d.runWorkspaceRunnerConnection(ctx, workspaceID); err != nil && ctx.Err() == nil && d.logger != nil {
			d.logger.Debug("workspace runner disconnected", "workspace_id", workspaceID, "error", err)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (d *Daemon) runWorkspaceRunnerConnection(ctx context.Context, workspaceID string) error {
	if d.client == nil {
		return fmt.Errorf("daemon client unavailable")
	}
	token := d.client.WorkspaceDaemonToken(workspaceID, time.Now())
	if token == "" {
		return fmt.Errorf("workspace daemon token unavailable")
	}
	wsURL, err := workspaceRunnerURL(d.cfg.ServerBaseURL, workspaceID)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	if d.client.platform != "" {
		headers.Set("X-Client-Platform", d.client.platform)
	}
	if d.client.version != "" {
		headers.Set("X-Client-Version", d.client.version)
	}
	if d.client.os != "" {
		headers.Set("X-Client-OS", d.client.os)
	}
	dialer := taskWakeupDialer()
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()
	ready, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: marshalRaw(protocol.WorkspaceRunnerReadyPayload{WorkspaceID: workspaceID, DaemonInstanceID: d.runnerInstanceID})})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		return err
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var message protocol.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			continue
		}
		switch message.Type {
		case protocol.EventWorkspaceRunnerPing:
			var ping protocol.WorkspaceRunnerPingPayload
			if json.Unmarshal(message.Payload, &ping) != nil || ping.Validate() != nil {
				continue
			}
			if err := writeWorkspaceRunnerFrame(conn, protocol.EventWorkspaceRunnerPong, protocol.WorkspaceRunnerPongPayload{PingID: ping.PingID}); err != nil {
				return err
			}
		case protocol.EventDaemonAgentStart:
			var start protocol.WorkspaceRunnerAgentStartPayload
			if json.Unmarshal(message.Payload, &start) != nil || start.Validate() != nil || !d.ownsWorkspaceRunnerRuntime(workspaceID, start.RuntimeID) {
				continue
			}
			ack, err := d.agentProcessManager.Start(agentProcessStartRequest{AgentID: start.AgentID, RuntimeID: start.RuntimeID, StartDispatchID: start.StartDispatchID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
			if err != nil {
				continue
			}
			if err := writeWorkspaceRunnerFrame(conn, protocol.EventAgentStartAck, ack); err != nil {
				return err
			}
			if err := writeWorkspaceRunnerFrame(conn, protocol.EventAgentStatus, protocol.AgentStatusPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive}); err != nil {
				return err
			}
		}
	}
}

func (d *Daemon) ownsWorkspaceRunnerRuntime(workspaceID, runtimeID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	runtime, ok := d.runtimeIndex[runtimeID]
	return ok && runtime.WorkspaceID == workspaceID
}

func writeWorkspaceRunnerFrame(conn *websocket.Conn, eventType string, payload any) error {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, frame)
}

func workspaceRunnerURL(baseURL, workspaceID string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid daemon server URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("daemon server URL must use http, https, ws, or wss")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/daemon/ws"
	q := u.Query()
	q.Set("workspace_id", workspaceID)
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}
