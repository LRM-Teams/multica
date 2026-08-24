package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DaemonConnection is one live /api/workspace/daemon/connect socket for one
// WorkspaceDaemonCore. workspaceSession owns commands on top of it.
type DaemonConnection struct {
	workspaceID string
	ctx         context.Context
	cancel      context.CancelFunc

	writeMu sync.Mutex
	write   func(string, any) error
	close   func()
	once    sync.Once
	closed  atomic.Bool

	deliveries *workspaceDaemonDeliveryDispatcher
}

func newDaemonConnection(workspaceID string, parent context.Context, write func(string, any) error, closeFn func()) *DaemonConnection {
	ctx, cancel := context.WithCancel(parent)
	return &DaemonConnection{
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
		write:       write,
		close:       closeFn,
	}
}

// Connected reports whether this socket is still the live DaemonCore
// connection. This is the Raft DaemonConnection.connected check.
func (connection *DaemonConnection) Connected() bool {
	return connection != nil && !connection.closed.Load()
}

func (connection *DaemonConnection) Write(eventType string, payload any) error {
	if connection == nil {
		return fmt.Errorf("DaemonConnection is unavailable")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.write(eventType, payload)
}

func (connection *DaemonConnection) Close() {
	if connection == nil {
		return
	}
	connection.once.Do(func() {
		connection.closed.Store(true)
		connection.cancel()
		if connection.close != nil {
			connection.close()
		}
	})
}

func writeDaemonConnectionFrame(conn *websocket.Conn, eventType string, payload any) error {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(workspaceDaemonWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, frame)
}

func daemonConnectionURL(baseURL, _ string) (string, error) {
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
	u.Path = strings.TrimRight(u.Path, "/") + protocol.WorkspaceDaemonConnectPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// workspaceDaemonURL keeps the previous helper name for call sites that still
// describe the WorkspaceDaemon connect URL.
func workspaceDaemonURL(baseURL, workspaceID string) (string, error) {
	return daemonConnectionURL(baseURL, workspaceID)
}
