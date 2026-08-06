package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrRuntimeOffline is returned by request RPCs when no connected daemon is
// watching the target runtime (the daemon is offline / not connected over WS).
var ErrRuntimeOffline = errors.New("runtime offline")

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// ClientIdentity captures the already-authenticated daemon connection scope.
type ClientIdentity struct {
	DaemonID      string
	UserID        string
	WorkspaceID   string
	RuntimeIDs    []string
	ClientVersion string
}

type client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	done     chan struct{}
	identity ClientIdentity
	runtimes map[string]struct{}

	dedupMu  sync.Mutex
	seenIDs  map[string]struct{}
	seenList []string
}

const eventDedupCapacity = 128

// markSeen records eventID as already delivered to this client. Empty event IDs
// disable dedup and are always delivered.
func (c *client) markSeen(eventID string) bool {
	if eventID == "" {
		return true
	}
	c.dedupMu.Lock()
	defer c.dedupMu.Unlock()
	if c.seenIDs == nil {
		c.seenIDs = make(map[string]struct{}, eventDedupCapacity)
	}
	if _, ok := c.seenIDs[eventID]; ok {
		return false
	}
	c.seenIDs[eventID] = struct{}{}
	c.seenList = append(c.seenList, eventID)
	if len(c.seenList) > eventDedupCapacity {
		drop := c.seenList[0]
		c.seenList = c.seenList[1:]
		delete(c.seenIDs, drop)
	}
	return true
}

// HeartbeatHandler processes a daemon:heartbeat frame. It must verify that
// runtimeID is one of identity.RuntimeIDs (the connection's authenticated
// scope) and return the ack payload to send back. Returning an error skips
// the ack and is logged at debug level.
type HeartbeatHandler func(ctx context.Context, identity ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error)

type ReminderSnapshotHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderSnapshotRequestPayload) (*protocol.ReminderSnapshotPayload, error)
type ReminderFireAttemptHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderFireAttemptPayload) (*protocol.ReminderFireResultPayload, error)
type ReminderOwnerLifecycleHandler func(ctx context.Context, identity ClientIdentity, payload protocol.DaemonAgentLifecycleRequestPayload) ([]protocol.DaemonAgentLifecycleEvent, map[string]int64, error)
type ReminderOwnerLifecycleAckHandler func(ctx context.Context, identity ClientIdentity, payload protocol.DaemonAgentLifecycleAckPayload) error
type ReminderProjectionHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderProjectionRequestPayload) ([]protocol.ReminderProjectionEvent, protocol.ReminderProjectionReplayEndPayload, error)
type ReminderProjectionAckHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderProjectionAckPayload) error

type ReminderOwnerGoneError struct {
	AgentID             string
	RuntimeID           string
	PlacementGeneration int64
}

func (e *ReminderOwnerGoneError) Error() string { return "reminder owner no longer belongs to daemon" }

// MessageKindRecorder is the optional metric hook called once per inbound
// daemon WebSocket frame. kind is the protocol message type with the
// "daemon:" prefix stripped (e.g. "heartbeat") or the literal "unknown" for
// types we don't model. A nil recorder is safely no-op'd.
type MessageKindRecorder interface {
	RecordDaemonWSMessageReceived(kind string)
}

// Hub keeps daemon WebSocket connections indexed by runtime ID. Messages are
// best-effort wakeup hints; the daemon still uses HTTP claim for correctness.
type Hub struct {
	upgrader websocket.Upgrader

	mu        sync.RWMutex
	clients   map[*client]bool
	byRuntime map[string]map[*client]bool

	hbMu                    sync.RWMutex
	onHeartbeat             HeartbeatHandler
	reminderMu              sync.RWMutex
	onReminderSnapshot      ReminderSnapshotHandler
	onReminderFire          ReminderFireAttemptHandler
	onReminderLifecycle     ReminderOwnerLifecycleHandler
	onReminderLifecycleAck  ReminderOwnerLifecycleAckHandler
	onReminderProjection    ReminderProjectionHandler
	onReminderProjectionAck ReminderProjectionAckHandler

	kindMu       sync.RWMutex
	kindRecorder MessageKindRecorder

	// In-flight server→daemon request/response RPCs, keyed by RequestID. A
	// response frame from the daemon is routed (as raw JSON) to the waiting
	// channel; the caller unmarshals into the response type it expects.
	pendMu  sync.Mutex
	pending map[string]chan json.RawMessage
}

func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			// Daemon clients authenticate with Authorization headers before the
			// upgrade. Browsers cannot set those headers through the native WS API,
			// and DaemonAuth does not accept cookies, so cookie-based CSWSH does
			// not apply to this endpoint. Re-evaluate this if DaemonAuth ever
			// grows cookie fallback.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients:   make(map[*client]bool),
		byRuntime: make(map[string]map[*client]bool),
		pending:   make(map[string]chan json.RawMessage),
	}
}

// requestDaemon pushes a request frame to the daemon watching runtimeID and
// waits (bounded by ctx) for the response frame correlated by requestID. The
// raw response payload is returned for the caller to unmarshal. Returns
// ErrRuntimeOffline when no daemon is connected for that runtime.
func (h *Hub) requestDaemon(ctx context.Context, runtimeID, requestID, msgType string, payload any) (json.RawMessage, error) {
	if h == nil {
		return nil, ErrRuntimeOffline
	}
	if requestID == "" || runtimeID == "" {
		return nil, errors.New("request_id and runtime_id required")
	}
	ch := make(chan json.RawMessage, 1)
	h.pendMu.Lock()
	h.pending[requestID] = ch
	h.pendMu.Unlock()
	defer func() {
		h.pendMu.Lock()
		delete(h.pending, requestID)
		h.pendMu.Unlock()
	}()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	frame, err := json.Marshal(protocol.Message{Type: msgType, Payload: body})
	if err != nil {
		return nil, err
	}
	// eventID "" disables dedup so the request is always delivered.
	if delivered, _ := h.notifyFrame(runtimeID, frame, ""); !delivered {
		return nil, ErrRuntimeOffline
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case raw := <-ch:
		return raw, nil
	}
}

// deliverResponse routes a daemon response (any RPC) to the waiting caller by
// request id. No-op if the request already timed out.
func (h *Hub) deliverResponse(requestID string, raw json.RawMessage) {
	if h == nil || requestID == "" {
		return
	}
	h.pendMu.Lock()
	ch := h.pending[requestID]
	h.pendMu.Unlock()
	if ch != nil {
		select {
		case ch <- raw:
		default:
		}
	}
}

// RequestWorkdirFiles pushes a list-files request to req.RuntimeID's daemon and
// waits for the correlated response. ErrRuntimeOffline if no daemon is connected.
func (h *Hub) RequestWorkdirFiles(ctx context.Context, req protocol.ListWorkdirFilesRequestPayload) (*protocol.ListWorkdirFilesResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonListFilesRequest, req)
	if err != nil {
		return nil, err
	}
	var resp protocol.ListWorkdirFilesResponsePayload
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RequestReadFile pushes a read-file request to req.RuntimeID's daemon and waits
// for the correlated response. ErrRuntimeOffline if no daemon is connected.
func (h *Hub) RequestReadFile(ctx context.Context, req protocol.ReadWorkdirFileRequestPayload) (*protocol.ReadWorkdirFileResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonReadFileRequest, req)
	if err != nil {
		return nil, err
	}
	var resp protocol.ReadWorkdirFileResponsePayload
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RequestWriteFile pushes a write-file request to req.RuntimeID's daemon and
// waits for the correlated response. ErrRuntimeOffline if no daemon is connected.
func (h *Hub) RequestWriteFile(ctx context.Context, req protocol.WriteWorkdirFileRequestPayload) (*protocol.WriteWorkdirFileResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonWriteFileRequest, req)
	if err != nil {
		return nil, err
	}
	var resp protocol.WriteWorkdirFileResponsePayload
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RequestDeleteDir pushes a delete-dir request to req.RuntimeID's daemon and
// waits for the correlated response. ErrRuntimeOffline if no daemon is connected.
func (h *Hub) RequestDeleteDir(ctx context.Context, req protocol.DeleteWorkdirDirRequestPayload) (*protocol.DeleteWorkdirDirResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonDeleteDirRequest, req)
	if err != nil {
		return nil, err
	}
	var resp protocol.DeleteWorkdirDirResponsePayload
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RequestSeedAgentContext pushes Wendy-created initial notes/memory to the
// daemon that owns req.RuntimeID. ErrRuntimeOffline if no daemon is connected.
func (h *Hub) RequestSeedAgentContext(ctx context.Context, req protocol.SeedAgentContextRequestPayload) (*protocol.SeedAgentContextResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonSeedAgentContextRequest, req)
	if err != nil {
		return nil, err
	}
	var resp protocol.SeedAgentContextResponsePayload
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetHeartbeatHandler installs the callback used for daemon:heartbeat frames.
// Wiring is done after handler construction because the handler depends on
// DB queries that aren't available when the hub is built. A nil handler
// disables WS heartbeat processing — daemons fall back to HTTP heartbeat
// transparently because their fallback timer fires whenever no ack arrives.
func (h *Hub) SetHeartbeatHandler(fn HeartbeatHandler) {
	if h == nil {
		return
	}
	h.hbMu.Lock()
	h.onHeartbeat = fn
	h.hbMu.Unlock()
}

func (h *Hub) heartbeatHandler() HeartbeatHandler {
	h.hbMu.RLock()
	defer h.hbMu.RUnlock()
	return h.onHeartbeat
}

func (h *Hub) SetReminderHandlers(snapshot ReminderSnapshotHandler, fire ReminderFireAttemptHandler, lifecycle ReminderOwnerLifecycleHandler, lifecycleAck ReminderOwnerLifecycleAckHandler) {
	if h == nil {
		return
	}
	h.reminderMu.Lock()
	h.onReminderSnapshot = snapshot
	h.onReminderFire = fire
	h.onReminderLifecycle = lifecycle
	h.onReminderLifecycleAck = lifecycleAck
	h.reminderMu.Unlock()
}

func (h *Hub) SetReminderProjectionHandlers(replay ReminderProjectionHandler, ack ReminderProjectionAckHandler) {
	h.mu.Lock()
	h.onReminderProjection = replay
	h.onReminderProjectionAck = ack
	h.mu.Unlock()
}

func (h *Hub) reminderHandlers() (ReminderSnapshotHandler, ReminderFireAttemptHandler, ReminderOwnerLifecycleHandler, ReminderOwnerLifecycleAckHandler) {
	h.reminderMu.RLock()
	defer h.reminderMu.RUnlock()
	return h.onReminderSnapshot, h.onReminderFire, h.onReminderLifecycle, h.onReminderLifecycleAck
}

func (h *Hub) reminderProjectionHandlers() (ReminderProjectionHandler, ReminderProjectionAckHandler) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.onReminderProjection, h.onReminderProjectionAck
}

// SetMessageKindRecorder installs an optional callback fired exactly once per
// inbound daemon WebSocket frame. Used by the metrics layer to count traffic
// by handler kind without hard-coupling the hub to any specific collector.
func (h *Hub) SetMessageKindRecorder(rec MessageKindRecorder) {
	if h == nil {
		return
	}
	h.kindMu.Lock()
	h.kindRecorder = rec
	h.kindMu.Unlock()
}

func (h *Hub) messageKindRecorder() MessageKindRecorder {
	if h == nil {
		return nil
	}
	h.kindMu.RLock()
	defer h.kindMu.RUnlock()
	return h.kindRecorder
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request, identity ClientIdentity) {
	if len(identity.RuntimeIDs) == 0 {
		http.Error(w, `{"error":"runtime_ids required"}`, http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("daemon websocket upgrade failed", "error", err)
		return
	}

	runtimes := make(map[string]struct{}, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		if runtimeID != "" {
			runtimes[runtimeID] = struct{}{}
		}
	}
	if len(runtimes) == 0 {
		conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"runtime_ids required"}`))
		conn.Close()
		return
	}

	c := &client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 16),
		done:     make(chan struct{}),
		identity: identity,
		runtimes: runtimes,
	}
	h.register(c)

	go c.writePump()
	go c.readPump()
}

// NotifyTaskAvailable sends a best-effort wakeup to daemons watching runtimeID.
func (h *Hub) NotifyTaskAvailable(runtimeID, taskID string) {
	h.notifyTaskAvailable(runtimeID, taskID, "")
}

func (h *Hub) NotifyReminderProjection(runtimeID string, payload protocol.ReminderProjectionEvent) {
	h.notifyReminder(runtimeID, protocol.EventReminderProjection, payload, fmt.Sprintf("reminder-projection:%s:%d", runtimeID, payload.Seq))
}

func (h *Hub) NotifyReminderOwnerRemoved(runtimeID string, payload protocol.DaemonAgentStopPayload) {
	h.notifyReminder(runtimeID, protocol.EventDaemonAgentStop, payload, "")
}

func (h *Hub) NotifyReminderOwnerAdded(runtimeID string, payload protocol.DaemonAgentStartPayload) {
	h.notifyReminder(runtimeID, protocol.EventDaemonAgentStart, payload, "")
}

func (h *Hub) notifyReminder(runtimeID, eventType string, payload any, eventID string) {
	if h == nil || runtimeID == "" {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return
	}
	h.notifyReminderFrame(runtimeID, frame, eventID)
}

func (h *Hub) notifyReminderFrame(runtimeID string, frame []byte, eventID string) {
	if h == nil || runtimeID == "" {
		return
	}
	h.notifyFrame(runtimeID, frame, eventID)
}

func (h *Hub) notifyTaskAvailable(runtimeID, taskID, eventID string) {
	if h == nil || runtimeID == "" {
		return
	}
	data, err := taskAvailableFrame(runtimeID, taskID)
	if err != nil {
		return
	}
	delivered, deduped := h.notifyFrame(runtimeID, data, eventID)
	if delivered {
		M.WakeupDeliveredHit.Add(1)
	} else if !deduped {
		M.WakeupDeliveredMiss.Add(1)
	}
}

func (h *Hub) DeliverDaemonRuntime(scopeID string, frame []byte, eventID string) {
	if h == nil {
		return
	}
	M.WakeupReceivedTotal.Add(1)
	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		slog.Debug("daemon websocket relay: invalid frame", "error", err, "scope_id", scopeID, "event_id", eventID)
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	runtimeID := ""
	switch msg.Type {
	case protocol.EventDaemonTaskAvailable:
		var payload protocol.TaskAvailablePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.RuntimeID == "" {
			slog.Debug("daemon websocket relay: invalid task_available payload", "error", err, "scope_id", scopeID, "event_id", eventID)
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		runtimeID = payload.RuntimeID
	case protocol.EventReminderUpsert, protocol.EventReminderCancel, protocol.EventDaemonAgentStart, protocol.EventDaemonAgentStop:
		runtimeID = scopeID
	default:
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	delivered, deduped := h.notifyFrame(runtimeID, frame, eventID)
	if delivered {
		M.WakeupDeliveredHit.Add(1)
	} else if !deduped {
		M.WakeupDeliveredMiss.Add(1)
	}
}

func (h *Hub) notifyFrame(runtimeID string, data []byte, eventID string) (delivered bool, deduped bool) {
	h.mu.RLock()
	clients := h.byRuntime[runtimeID]
	slow := make([]*client, 0)
	for c := range clients {
		if !c.markSeen(eventID) {
			deduped = true
			continue
		}
		select {
		case c.send <- data:
			delivered = true
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range slow {
		h.unregister(c)
		c.conn.Close()
	}
	if len(slow) > 0 {
		M.SlowEvictionsTotal.Add(int64(len(slow)))
	}
	return delivered, deduped
}

func taskAvailableFrame(runtimeID, taskID string) ([]byte, error) {
	return json.Marshal(protocol.Message{
		Type: protocol.EventDaemonTaskAvailable,
		Payload: mustMarshalRaw(protocol.TaskAvailablePayload{
			RuntimeID: runtimeID,
			TaskID:    taskID,
		}),
	})
}

func mustMarshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

func (h *Hub) RuntimeConnectionCount(runtimeID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byRuntime[runtimeID])
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = true
	for runtimeID := range c.runtimes {
		conns := h.byRuntime[runtimeID]
		if conns == nil {
			conns = make(map[*client]bool)
			h.byRuntime[runtimeID] = conns
		}
		conns[c] = true
	}
	total := len(h.clients)
	h.mu.Unlock()

	M.ConnectsTotal.Add(1)
	M.ActiveConnections.Add(1)
	slog.Info("daemon websocket connected",
		"daemon_id", c.identity.DaemonID,
		"user_id", c.identity.UserID,
		"workspace_id", c.identity.WorkspaceID,
		"runtimes", len(c.runtimes),
		"client_version", c.identity.ClientVersion,
		"total_clients", total,
	)
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if !h.clients[c] {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c)
	for runtimeID := range c.runtimes {
		if conns := h.byRuntime[runtimeID]; conns != nil {
			delete(conns, c)
			if len(conns) == 0 {
				delete(h.byRuntime, runtimeID)
			}
		}
	}
	close(c.done)
	total := len(h.clients)
	h.mu.Unlock()

	M.DisconnectsTotal.Add(1)
	M.ActiveConnections.Add(-1)
	slog.Info("daemon websocket disconnected",
		"daemon_id", c.identity.DaemonID,
		"user_id", c.identity.UserID,
		"workspace_id", c.identity.WorkspaceID,
		"runtimes", len(c.runtimes),
		"total_clients", total,
	)
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.conn.Close()
	}()

	// Heartbeats are tiny, but workdir responses can be large: a 2000-node
	// file tree and especially base64-encoded media previews (capped ~6 MiB
	// raw → ~8 MiB base64). Allow 10 MiB so a response frame isn't truncated
	// into a read error.
	c.conn.SetReadLimit(10 << 20)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("daemon websocket read error", "error", err, "daemon_id", c.identity.DaemonID)
			}
			return
		}
		c.handleFrame(raw)
	}
}

func (c *client) handleFrame(raw []byte) {
	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		slog.Debug("daemon websocket invalid frame", "error", err, "daemon_id", c.identity.DaemonID)
		if rec := c.hub.messageKindRecorder(); rec != nil {
			rec.RecordDaemonWSMessageReceived("invalid")
		}
		return
	}
	kind := strings.TrimPrefix(msg.Type, "daemon:")
	if kind == "" {
		kind = "unknown"
	}
	if rec := c.hub.messageKindRecorder(); rec != nil {
		rec.RecordDaemonWSMessageReceived(kind)
	}
	switch msg.Type {
	case protocol.EventDaemonHeartbeat:
		c.handleHeartbeatFrame(msg.Payload)
	case protocol.EventDaemonListFilesResponse,
		protocol.EventDaemonReadFileResponse,
		protocol.EventDaemonWriteFileResponse,
		protocol.EventDaemonDeleteDirResponse,
		protocol.EventDaemonSeedAgentContextResponse:
		var idOnly struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(msg.Payload, &idOnly); err == nil && idOnly.RequestID != "" {
			c.hub.deliverResponse(idOnly.RequestID, msg.Payload)
		}
	case protocol.EventReminderSnapshotRequest:
		c.handleReminderSnapshotRequest(msg.Payload)
	case protocol.EventReminderFireAttempt:
		c.handleReminderFireAttempt(msg.Payload)
	case protocol.EventDaemonAgentLifecycleReq:
		c.handleReminderOwnerLifecycleRequest(msg.Payload)
	case protocol.EventDaemonAgentLifecycleAck:
		c.handleReminderOwnerLifecycleAck(msg.Payload)
	case protocol.EventReminderProjectionReq:
		c.handleReminderProjectionRequest(msg.Payload)
	case protocol.EventReminderProjectionAck:
		c.handleReminderProjectionAck(msg.Payload)
	default:
		// Unknown app messages are intentionally ignored for forward
		// compatibility with future daemon → server message types.
	}
}

func (c *client) handleReminderSnapshotRequest(raw json.RawMessage) {
	handler, _, _, _ := c.hub.reminderHandlers()
	if handler == nil {
		return
	}
	var payload protocol.ReminderSnapshotRequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.AgentID == "" {
		slog.Debug("daemon websocket reminder snapshot invalid payload", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	snapshot, err := handler(context.Background(), c.identity, payload)
	if err != nil {
		var ownerGone *ReminderOwnerGoneError
		if errors.As(err, &ownerGone) {
			c.sendReminderFrame(protocol.EventDaemonAgentStop, protocol.DaemonAgentStopPayload{
				AgentID: ownerGone.AgentID, RuntimeID: ownerGone.RuntimeID, PlacementGeneration: ownerGone.PlacementGeneration,
			})
			return
		}
		slog.Warn("daemon websocket reminder snapshot failed", "error", err, "daemon_id", c.identity.DaemonID, "agent_id", payload.AgentID)
		_ = c.conn.Close()
		return
	}
	if snapshot == nil {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventReminderSnapshot, Payload: mustMarshalRaw(snapshot)})
	if err != nil {
		return
	}
	select {
	case c.send <- frame:
	default:
		c.conn.Close()
	}
}

func (c *client) handleReminderFireAttempt(raw json.RawMessage) {
	_, handler, _, _ := c.hub.reminderHandlers()
	if handler == nil {
		return
	}
	var payload protocol.ReminderFireAttemptPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.AgentID == "" || payload.ReminderID == "" || payload.Version < 1 {
		slog.Debug("daemon websocket reminder fire invalid payload", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	result, err := handler(context.Background(), c.identity, payload)
	if err != nil {
		var ownerGone *ReminderOwnerGoneError
		if errors.As(err, &ownerGone) {
			_ = c.sendReminderFrame(protocol.EventDaemonAgentStop, protocol.DaemonAgentStopPayload{AgentID: ownerGone.AgentID, RuntimeID: ownerGone.RuntimeID, PlacementGeneration: ownerGone.PlacementGeneration})
			return
		}
		// Task #68: the daemon now keeps a locally-retryable in-flight record
		// for a fired reminder until it sees a fire_result confirmation (see
		// reminderCache.fireAndScheduleRetryLocked), so a transient failure
		// here no longer needs the connection torn down just to force a
		// reconnect+snapshot recovery — the daemon's own retry timer resends
		// this fire_attempt shortly. Forcing a reconnect on every processing
		// hiccup made a single transient DB error pay for a full WS drop.
		slog.Warn("daemon websocket reminder fire failed; daemon local retry will resend", "error", err, "daemon_id", c.identity.DaemonID, "agent_id", payload.AgentID, "reminder_id", payload.ReminderID)
		return
	}
	if result != nil {
		_ = c.sendReminderFrame(protocol.EventReminderFireResult, result)
	}
}

func (c *client) handleReminderProjectionRequest(raw json.RawMessage) {
	handler, _ := c.hub.reminderProjectionHandlers()
	if handler == nil {
		return
	}
	var payload protocol.ReminderProjectionRequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		_ = c.conn.Close()
		return
	}
	events, end, err := handler(context.Background(), c.identity, payload)
	if err != nil {
		slog.Warn("daemon websocket reminder projection replay failed", "error", err, "daemon_id", c.identity.DaemonID)
		_ = c.conn.Close()
		return
	}
	for _, event := range events {
		if err := c.sendReminderFrame(protocol.EventReminderProjection, event); err != nil {
			return
		}
	}
	_ = c.sendReminderFrame(protocol.EventReminderProjectionEnd, end)
}

func (c *client) handleReminderProjectionAck(raw json.RawMessage) {
	_, handler := c.hub.reminderProjectionHandlers()
	if handler == nil {
		return
	}
	var payload protocol.ReminderProjectionAckPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		_ = c.conn.Close()
		return
	}
	if err := handler(context.Background(), c.identity, payload); err != nil {
		slog.Warn("daemon websocket reminder projection ack failed", "error", err, "daemon_id", c.identity.DaemonID)
		_ = c.conn.Close()
	}
}

func (c *client) handleReminderOwnerLifecycleRequest(raw json.RawMessage) {
	_, _, handler, _ := c.hub.reminderHandlers()
	if handler == nil {
		return
	}
	var payload protocol.DaemonAgentLifecycleRequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		_ = c.conn.Close()
		return
	}
	events, cursors, err := handler(context.Background(), c.identity, payload)
	if err != nil {
		slog.Warn("daemon websocket agent lifecycle replay failed", "error", err, "daemon_id", c.identity.DaemonID)
		_ = c.conn.Close()
		return
	}
	for _, event := range events {
		switch event.EventType {
		case "start":
			if err := c.sendReminderFrame(protocol.EventDaemonAgentStart, protocol.DaemonAgentStartPayload{
				AgentID: event.AgentID, RuntimeID: event.RuntimeID, WorkspaceID: event.WorkspaceID,
				PlacementGeneration: event.PlacementGeneration, LifecycleSeq: event.LifecycleSeq, Replay: true,
			}); err != nil {
				return
			}
		case "stop":
			if err := c.sendReminderFrame(protocol.EventDaemonAgentStop, protocol.DaemonAgentStopPayload{
				AgentID: event.AgentID, RuntimeID: event.RuntimeID, PlacementGeneration: event.PlacementGeneration,
				LifecycleSeq: event.LifecycleSeq, Replay: true,
			}); err != nil {
				return
			}
		}
	}
	_ = c.sendReminderFrame(protocol.EventDaemonAgentLifecycleEnd, protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: cursors})
}

func (c *client) handleReminderOwnerLifecycleAck(raw json.RawMessage) {
	_, _, _, handler := c.hub.reminderHandlers()
	if handler == nil {
		return
	}
	var payload protocol.DaemonAgentLifecycleAckPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		_ = c.conn.Close()
		return
	}
	if err := handler(context.Background(), c.identity, payload); err != nil {
		slog.Warn("daemon websocket agent lifecycle ack failed", "error", err, "daemon_id", c.identity.DaemonID)
		_ = c.conn.Close()
	}
}

func (c *client) sendReminderFrame(eventType string, payload any) error {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return err
	}
	select {
	case c.send <- frame:
		return nil
	case <-c.done:
		return errors.New("daemon websocket client disconnected")
	}
}

// handleHeartbeatFrame processes an inbound daemon:heartbeat from the daemon,
// invokes the hub's handler, and writes back a daemon:heartbeat_ack.
func (c *client) handleHeartbeatFrame(raw json.RawMessage) {
	handler := c.hub.heartbeatHandler()
	if handler == nil {
		// Server doesn't have a heartbeat handler wired — daemon will time
		// out waiting for an ack and fall back to HTTP heartbeat.
		return
	}

	var payload protocol.DaemonHeartbeatRequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		slog.Debug("daemon websocket heartbeat invalid payload", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	if payload.RuntimeID == "" {
		slog.Debug("daemon websocket heartbeat missing runtime_id", "daemon_id", c.identity.DaemonID)
		return
	}
	if _, ok := c.runtimes[payload.RuntimeID]; !ok {
		// The connection authenticated for a fixed runtime set; reject any
		// heartbeat for a runtime the client did not register for.
		slog.Warn("daemon websocket heartbeat for unauthorized runtime",
			"daemon_id", c.identity.DaemonID,
			"runtime_id", payload.RuntimeID)
		return
	}

	// Intentionally do NOT wrap this ctx with WithTimeout. The handler
	// reaches LocalSkill{List,Import}Store.PopPending, whose Redis Lua
	// claim script has side effects (ZREM + SET-running) that cannot be
	// safely un-run if the client cancels mid-script — the same invariant
	// that keeps the HTTP heartbeat from putting a per-call timeout on
	// PopPending. The natural bound is the read pump's lifetime (the conn
	// closes if the daemon goes away) plus Redis's own server-side limits.
	ack, err := handler(context.Background(), c.identity, payload)
	if err != nil {
		slog.Warn("daemon websocket heartbeat handler failed",
			"error", err,
			"daemon_id", c.identity.DaemonID,
			"runtime_id", payload.RuntimeID)
		return
	}
	if ack == nil {
		return
	}
	frame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeatAck,
		Payload: mustMarshalRaw(ack),
	})
	if err != nil {
		slog.Debug("daemon websocket heartbeat ack marshal failed", "error", err)
		return
	}
	select {
	case c.send <- frame:
	default:
		// Send buffer is full — slow client. Don't block the read pump; the
		// next writePump tick or notifyFrame eviction will clean up.
		slog.Debug("daemon websocket heartbeat ack dropped: send buffer full",
			"daemon_id", c.identity.DaemonID,
			"runtime_id", payload.RuntimeID)
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				slog.Debug("daemon websocket write error", "error", err, "daemon_id", c.identity.DaemonID)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}
	}
}
