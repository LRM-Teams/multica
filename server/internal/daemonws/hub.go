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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrRuntimeOffline is returned by request RPCs when no connected daemon is
// watching the target runtime (the daemon is offline / not connected over WS).
var ErrRuntimeOffline = errors.New("runtime offline")

const (
	writeWait                  = 10 * time.Second
	pongWait                   = 60 * time.Second
	pingPeriod                 = (pongWait * 9) / 10
	agentDeliveryRetryInterval = time.Second
	runnerInboundWatchdog      = 70 * time.Second
	runnerPingInterval         = 20 * time.Second
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

	// Set only after a valid `ready` frame. Until then a connection remains on
	// the legacy runtime-multiplexed path and cannot receive Runner commands.
	runnerDaemonInstanceID string
	runnerCapabilities     map[string]struct{}
	runnerLastInbound      atomic.Int64
	runnerWatchdogOnce     sync.Once

	dedupMu  sync.Mutex
	seenIDs  map[string]struct{}
	seenList []string
}

func (c *client) supportsRunnerCapability(capability string) bool {
	if c == nil || capability == "" {
		return false
	}
	_, supported := c.runnerCapabilities[capability]
	return supported
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
type ReminderProjectionHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderProjectionRequestPayload) ([]protocol.ReminderProjectionEvent, protocol.ReminderProjectionReplayEndPayload, error)
type ReminderProjectionAckHandler func(ctx context.Context, identity ClientIdentity, payload protocol.ReminderProjectionAckPayload) error
type AgentDeliveryAckHandler func(ctx context.Context, identity ClientIdentity, payload protocol.AgentDeliverAckPayload) error
type AgentMessageHandoffHandler func(ctx context.Context, identity ClientIdentity, payload protocol.AgentMessageHandoffPayload) error

// WorkspaceRunnerHandler receives only frames from the current ready
// connection for one daemon and Workspace. It owns no Activity semantics;
// later intake tickets install typed handlers behind this fenced boundary.
type WorkspaceRunnerHandler func(ctx context.Context, identity ClientIdentity, daemonInstanceID, eventType string, payload json.RawMessage) error
type WorkspaceRunnerDisconnectHandler func(ctx context.Context, identity ClientIdentity, daemonInstanceID string) error

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
	byRunner  map[workspaceRunnerKey]*client

	hbMu                        sync.RWMutex
	onHeartbeat                 HeartbeatHandler
	reminderMu                  sync.RWMutex
	onReminderSnapshot          ReminderSnapshotHandler
	onReminderFire              ReminderFireAttemptHandler
	onReminderProjection        ReminderProjectionHandler
	onReminderProjectionAck     ReminderProjectionAckHandler
	deliveryMu                  sync.RWMutex
	onAgentDeliveryAck          AgentDeliveryAckHandler
	onAgentMessageHandoff       AgentMessageHandoffHandler
	runnerMu                    sync.RWMutex
	onWorkspaceRunner           WorkspaceRunnerHandler
	onWorkspaceRunnerDisconnect WorkspaceRunnerDisconnectHandler

	agentDeliveryMu            sync.Mutex
	pendingAgentDeliveries     map[string]*pendingAgentDelivery
	scheduleAgentDeliveryRetry func(time.Duration, func())

	kindMu       sync.RWMutex
	kindRecorder MessageKindRecorder

	// In-flight server→daemon request/response RPCs, keyed by RequestID. A
	// response frame from the daemon is routed (as raw JSON) to the waiting
	// channel; the caller unmarshals into the response type it expects.
	pendMu  sync.Mutex
	pending map[string]chan json.RawMessage
}

type workspaceRunnerKey struct {
	daemonID    string
	workspaceID string
}

type pendingAgentDelivery struct {
	runtimeID      string
	workspaceID    string
	daemonID       string
	payload        protocol.AgentDeliverPayload
	frame          []byte
	retryScheduled bool
}

// SetAgentDeliveryAckHandler installs the server-side receipt consumer for
// agent:deliver:ack frames. The handler owns authorization and idempotency;
// the hub only authenticates the daemon websocket identity.
func (h *Hub) SetAgentDeliveryAckHandler(fn AgentDeliveryAckHandler) {
	if h == nil {
		return
	}
	h.deliveryMu.Lock()
	h.onAgentDeliveryAck = fn
	h.deliveryMu.Unlock()
}

func (h *Hub) SetAgentMessageHandoffHandler(fn AgentMessageHandoffHandler) {
	if h == nil {
		return
	}
	h.deliveryMu.Lock()
	h.onAgentMessageHandoff = fn
	h.deliveryMu.Unlock()
}

// SetWorkspaceRunnerHandler installs the active Runner inbound boundary.
// Runner frames stay separate from the removed legacy routing and Activity
// contracts.
func (h *Hub) SetWorkspaceRunnerHandler(fn WorkspaceRunnerHandler) {
	if h == nil {
		return
	}
	h.runnerMu.Lock()
	h.onWorkspaceRunner = fn
	h.runnerMu.Unlock()
}

// SetWorkspaceRunnerDisconnectHandler installs the lifecycle boundary used by
// server-owned Agent Presence. The callback fires only when the disconnected
// socket still owns the current ready daemon/workspace Runner slot.
func (h *Hub) SetWorkspaceRunnerDisconnectHandler(fn WorkspaceRunnerDisconnectHandler) {
	if h == nil {
		return
	}
	h.runnerMu.Lock()
	h.onWorkspaceRunnerDisconnect = fn
	h.runnerMu.Unlock()
}

func (h *Hub) workspaceRunnerHandler() WorkspaceRunnerHandler {
	if h == nil {
		return nil
	}
	h.runnerMu.RLock()
	defer h.runnerMu.RUnlock()
	return h.onWorkspaceRunner
}

func (h *Hub) workspaceRunnerDisconnectHandler() WorkspaceRunnerDisconnectHandler {
	if h == nil {
		return nil
	}
	h.runnerMu.RLock()
	defer h.runnerMu.RUnlock()
	return h.onWorkspaceRunnerDisconnect
}

func (h *Hub) agentDeliveryAckHandler() AgentDeliveryAckHandler {
	h.deliveryMu.RLock()
	defer h.deliveryMu.RUnlock()
	return h.onAgentDeliveryAck
}

func (h *Hub) agentMessageHandoffHandler() AgentMessageHandoffHandler {
	h.deliveryMu.RLock()
	defer h.deliveryMu.RUnlock()
	return h.onAgentMessageHandoff
}

// NotifyAgentDelivery sends one canonical Message delivery to a daemon runtime.
// The runtime route is deliberately outside the envelope: it is transport
// placement, not Message or Context Boundary state.
func (h *Hub) NotifyAgentDelivery(runtimeID string, payload protocol.AgentDeliverPayload) bool {
	return h.notifyAgentDelivery(runtimeID, payload, "")
}

func (h *Hub) notifyAgentDelivery(runtimeID string, payload protocol.AgentDeliverPayload, eventID string) bool {
	if h == nil || runtimeID == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	if !h.stageAgentDelivery(runtimeID, payload, frame) {
		return false
	}
	// A retry deliberately reuses delivery_id. Hub-level event dedup would
	// suppress that retry before the coordinator can acknowledge it again.
	delivered, _ := h.notifyFrame(runtimeID, frame, eventID)
	if delivered {
		h.activateAgentDeliveryRetry(payload.DeliveryID)
	} else {
		h.dropAgentDelivery(payload.DeliveryID)
	}
	return delivered
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
		clients:                make(map[*client]bool),
		byRuntime:              make(map[string]map[*client]bool),
		byRunner:               make(map[workspaceRunnerKey]*client),
		pending:                make(map[string]chan json.RawMessage),
		pendingAgentDeliveries: make(map[string]*pendingAgentDelivery),
		scheduleAgentDeliveryRetry: func(delay time.Duration, retry func()) {
			time.AfterFunc(delay, retry)
		},
	}
}

func (h *Hub) stageAgentDelivery(runtimeID string, payload protocol.AgentDeliverPayload, frame []byte) bool {
	if h == nil || strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(payload.DeliveryID) == "" {
		return false
	}
	h.agentDeliveryMu.Lock()
	defer h.agentDeliveryMu.Unlock()
	if existing := h.pendingAgentDeliveries[payload.DeliveryID]; existing != nil {
		return existing.runtimeID == runtimeID &&
			existing.payload.AgentID == payload.AgentID &&
			existing.payload.Seq == payload.Seq
	}
	h.pendingAgentDeliveries[payload.DeliveryID] = &pendingAgentDelivery{
		runtimeID: runtimeID,
		payload:   payload,
		frame:     append([]byte(nil), frame...),
	}
	return true
}

func (h *Hub) stageWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload, frame []byte) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(daemonID) == "" || strings.TrimSpace(payload.DeliveryID) == "" {
		return false
	}
	h.agentDeliveryMu.Lock()
	defer h.agentDeliveryMu.Unlock()
	if existing := h.pendingAgentDeliveries[payload.DeliveryID]; existing != nil {
		return existing.workspaceID == workspaceID && existing.daemonID == daemonID &&
			existing.payload.AgentID == payload.AgentID && existing.payload.Seq == payload.Seq
	}
	h.pendingAgentDeliveries[payload.DeliveryID] = &pendingAgentDelivery{
		workspaceID: workspaceID,
		daemonID:    daemonID,
		payload:     payload,
		frame:       append([]byte(nil), frame...),
	}
	return true
}

func (h *Hub) activateAgentDeliveryRetry(deliveryID string) {
	if h == nil {
		return
	}
	h.agentDeliveryMu.Lock()
	pending := h.pendingAgentDeliveries[deliveryID]
	if pending == nil || pending.retryScheduled {
		h.agentDeliveryMu.Unlock()
		return
	}
	pending.retryScheduled = true
	schedule := h.scheduleAgentDeliveryRetry
	h.agentDeliveryMu.Unlock()
	if schedule != nil {
		schedule(agentDeliveryRetryInterval, func() { h.retryAgentDelivery(deliveryID) })
	}
}

func (h *Hub) retryAgentDelivery(deliveryID string) {
	if h == nil {
		return
	}
	h.agentDeliveryMu.Lock()
	pending := h.pendingAgentDeliveries[deliveryID]
	if pending == nil {
		h.agentDeliveryMu.Unlock()
		return
	}
	pending.retryScheduled = false
	runtimeID := pending.runtimeID
	workspaceID := pending.workspaceID
	daemonID := pending.daemonID
	frame := append([]byte(nil), pending.frame...)
	h.agentDeliveryMu.Unlock()

	delivered := false
	if runtimeID != "" {
		delivered, _ = h.notifyFrame(runtimeID, frame, "")
	} else {
		delivered = h.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	}
	if !delivered {
		// Once the live connection is gone, reconnect recovery is the durable
		// correctness path. Do not retain an unbounded in-memory retry ledger.
		h.dropAgentDelivery(deliveryID)
		return
	}
	h.activateAgentDeliveryRetry(deliveryID)
}

func (h *Hub) acknowledgeAgentDelivery(c *client, ack protocol.AgentDeliverAckPayload) {
	if h == nil || c == nil {
		return
	}
	h.agentDeliveryMu.Lock()
	defer h.agentDeliveryMu.Unlock()
	pending := h.pendingAgentDeliveries[ack.DeliveryID]
	if pending == nil || pending.payload.AgentID != ack.AgentID || pending.payload.Seq != ack.Seq {
		return
	}
	if pending.runtimeID != "" {
		if _, authorizedRuntime := c.runtimes[pending.runtimeID]; !authorizedRuntime {
			return
		}
	} else if pending.workspaceID != c.identity.WorkspaceID || pending.daemonID != c.identity.DaemonID || !h.isCurrentWorkspaceRunner(c) {
		return
	}
	delete(h.pendingAgentDeliveries, ack.DeliveryID)
}

func (h *Hub) dropAgentDelivery(deliveryID string) {
	if h == nil {
		return
	}
	h.agentDeliveryMu.Lock()
	delete(h.pendingAgentDeliveries, deliveryID)
	h.agentDeliveryMu.Unlock()
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
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventAgentWorkspaceList, req)
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
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventAgentWorkspaceRead, req)
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
// disables heartbeat processing; capable Workspace Runners reconnect until
// they reach a Server that supports their control plane.
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

func (h *Hub) SetReminderHandlers(snapshot ReminderSnapshotHandler, fire ReminderFireAttemptHandler) {
	if h == nil {
		return
	}
	h.reminderMu.Lock()
	h.onReminderSnapshot = snapshot
	h.onReminderFire = fire
	h.reminderMu.Unlock()
}

func (h *Hub) SetReminderProjectionHandlers(replay ReminderProjectionHandler, ack ReminderProjectionAckHandler) {
	h.mu.Lock()
	h.onReminderProjection = replay
	h.onReminderProjectionAck = ack
	h.mu.Unlock()
}

func (h *Hub) reminderHandlers() (ReminderSnapshotHandler, ReminderFireAttemptHandler) {
	h.reminderMu.RLock()
	defer h.reminderMu.RUnlock()
	return h.onReminderSnapshot, h.onReminderFire
}

func (h *Hub) RequestPreparePiRun(ctx context.Context, req protocol.PreparePiRunRequestPayload) (*protocol.PreparePiRunResponsePayload, error) {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonPreparePiRunRequest, req)
	if err != nil {
		return nil, err
	}
	var response protocol.PreparePiRunResponsePayload
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if strings.TrimSpace(response.SessionID) == "" || strings.TrimSpace(response.CaptureBoundary) == "" {
		return nil, errors.New("daemon returned incomplete Pi run binding")
	}
	return &response, nil
}

func (h *Hub) RequestRevokePiRun(ctx context.Context, req protocol.RevokePiRunRequestPayload) error {
	raw, err := h.requestDaemon(ctx, req.RuntimeID, req.RequestID, protocol.EventDaemonRevokePiRunRequest, req)
	if err != nil {
		return err
	}
	var response protocol.RevokePiRunResponsePayload
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
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
	hasRunnerScope := strings.TrimSpace(identity.DaemonID) != "" && strings.TrimSpace(identity.WorkspaceID) != ""
	if len(identity.RuntimeIDs) == 0 && !hasRunnerScope {
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
	if len(runtimes) == 0 && !hasRunnerScope {
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

func (h *Hub) NotifyAgentAttachmentRemoved(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentDetachPayload) {
	h.notifyWorkspaceRunnerCommand(workspaceID, daemonID, protocol.EventAgentDetach, payload)
}

func (h *Hub) NotifyAgentAttachmentAdded(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentAttachPayload) {
	h.notifyWorkspaceRunnerCommand(workspaceID, daemonID, protocol.EventAgentAttach, payload)
}

func (h *Hub) notifyWorkspaceRunnerCommand(workspaceID, daemonID, eventType string, payload any) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(daemonID) == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	return h.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
}

// NotifyReminderOwnerInput attempts one in-memory Workspace Runner write. It
// does not enter pendingAgentDeliveries and deliberately has no retry or ACK.
func (h *Hub) NotifyReminderOwnerInput(workspaceID, daemonID string, payload protocol.ReminderOwnerInputPayload) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(daemonID) == "" {
		return false
	}
	input := protocol.AgentTransientDeliverPayload{
		Kind: protocol.AgentTransientDeliverKindReminder, Transient: true, Reminder: payload,
	}
	delivered := h.notifyWorkspaceRunnerCommand(workspaceID, daemonID, protocol.EventAgentDeliver, input)
	if !delivered {
		slog.Info("transient Reminder owner input", "outcome", "transport_lost", "workspace_id", workspaceID, "daemon_id", daemonID, "runtime_id", payload.RuntimeID, "agent_id", payload.AgentID, "reminder_id", payload.ReminderID, "version", payload.Version)
	}
	return delivered
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
	var agentDelivery *protocol.AgentDeliverPayload
	switch msg.Type {
	case protocol.EventDaemonTaskAvailable:
		var payload protocol.TaskAvailablePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.RuntimeID == "" {
			slog.Debug("daemon websocket relay: invalid task_available payload", "error", err, "scope_id", scopeID, "event_id", eventID)
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		runtimeID = payload.RuntimeID
	case protocol.EventReminderUpsert, protocol.EventReminderCancel:
		runtimeID = scopeID
	case protocol.EventAgentDeliver:
		var payload protocol.AgentDeliverPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.DeliveryID == "" {
			slog.Debug("daemon websocket relay: invalid agent delivery payload", "error", err, "scope_id", scopeID, "event_id", eventID)
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		runtimeID = scopeID
		agentDelivery = &payload
	default:
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	if agentDelivery != nil && !h.stageAgentDelivery(runtimeID, *agentDelivery, frame) {
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	delivered, deduped := h.notifyFrame(runtimeID, frame, eventID)
	if agentDelivery != nil {
		if delivered {
			h.activateAgentDeliveryRetry(agentDelivery.DeliveryID)
		} else if !deduped {
			h.dropAgentDelivery(agentDelivery.DeliveryID)
		}
	}
	if delivered {
		M.WakeupDeliveredHit.Add(1)
	} else if !deduped {
		M.WakeupDeliveredMiss.Add(1)
	}
}

// DeliverDaemonWorkspaceRunner consumes a relayed Runner-scoped Message or
// Attachment command. The relay scope is an address only; payload
// authorization and receipts still happen on the current fenced connection.
func (h *Hub) DeliverDaemonWorkspaceRunner(scopeID string, frame []byte, eventID string) {
	if h == nil {
		return
	}
	daemonID, workspaceID, ok := parseWorkspaceRunnerRelayScopeID(scopeID)
	if !ok {
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	delivered := false
	switch msg.Type {
	case protocol.EventAgentDeliver:
		var payload protocol.AgentDeliverPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || strings.TrimSpace(payload.DeliveryID) == "" {
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		delivered = h.notifyWorkspaceAgentDelivery(workspaceID, daemonID, payload, frame, eventID)
	case protocol.EventAgentAttach:
		var payload protocol.WorkspaceRunnerAgentAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		delivered = h.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	case protocol.EventAgentDetach:
		var payload protocol.WorkspaceRunnerAgentDetachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			M.WakeupDeliveredMiss.Add(1)
			return
		}
		delivered = h.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	default:
		M.WakeupDeliveredMiss.Add(1)
		return
	}
	if delivered {
		M.WakeupDeliveredHit.Add(1)
	} else {
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

// WorkspaceRunnerConnectionCount is zero or one: a ready connection is owned
// by the daemon and Workspace pair, not by each provider registration.
func (h *Hub) WorkspaceRunnerConnectionCount(daemonID, workspaceID string) int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.byRunner[workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}] == nil {
		return 0
	}
	return 1
}

// IsCurrentWorkspaceRunner is the live connection half of Agent Presence.
// Matching the daemon instance prevents a launch owned by a replaced process
// from borrowing the replacement socket's liveness.
func (h *Hub) IsCurrentWorkspaceRunner(daemonID, workspaceID, daemonInstanceID string) bool {
	if h == nil || daemonID == "" || workspaceID == "" || daemonInstanceID == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.byRunner[workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}]
	return c != nil && c.runnerDaemonInstanceID == daemonInstanceID
}

// WorkspaceRunnerSupportsCapability reports only the active ready connection's
// declared capabilities. A replaced Runner cannot lend its protocol support to
// its successor.
func (h *Hub) WorkspaceRunnerSupportsCapability(daemonID, workspaceID, capability string) bool {
	if h == nil || strings.TrimSpace(daemonID) == "" || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(capability) == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.byRunner[workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}]
	if c == nil {
		return false
	}
	_, supported := c.runnerCapabilities[capability]
	return supported
}

// WorkspaceRunnerIdentity returns a copy of the current ready connection's
// authenticated scope. Mutation handlers use it to run the same reconcile as
// ready/reconnect immediately after an Agent placement changes.
func (h *Hub) WorkspaceRunnerIdentity(daemonID, workspaceID string) (ClientIdentity, bool) {
	if h == nil || strings.TrimSpace(daemonID) == "" || strings.TrimSpace(workspaceID) == "" {
		return ClientIdentity{}, false
	}
	h.mu.RLock()
	c := h.byRunner[workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}]
	if c == nil || c.runnerDaemonInstanceID == "" {
		h.mu.RUnlock()
		return ClientIdentity{}, false
	}
	identity := c.identity
	identity.RuntimeIDs = append([]string(nil), c.identity.RuntimeIDs...)
	h.mu.RUnlock()
	return identity, true
}

// NotifyWorkspaceRunner routes a command only to the current ready connection
// for the exact daemon and Workspace. It is deliberately separate from the
// removed legacy runtime fan-out route.
func (h *Hub) NotifyWorkspaceRunner(daemonID, workspaceID, eventType string, payload any) bool {
	if h == nil || strings.TrimSpace(daemonID) == "" || strings.TrimSpace(workspaceID) == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	key := workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}
	h.mu.RLock()
	c := h.byRunner[key]
	if c == nil {
		h.mu.RUnlock()
		return false
	}
	select {
	case c.send <- frame:
		h.mu.RUnlock()
		return true
	default:
		h.mu.RUnlock()
		h.unregister(c)
		_ = c.conn.Close()
		return false
	}
}

// NotifyWorkspaceAgentDelivery sends an at-least-once canonical Message to
// the sole current Runner for one daemon/workspace pair.
func (h *Hub) NotifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(daemonID) == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	return h.notifyWorkspaceAgentDelivery(workspaceID, daemonID, payload, frame, "")
}

func (h *Hub) notifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload, frame []byte, _ string) bool {
	if h == nil || !h.stageWorkspaceAgentDelivery(workspaceID, daemonID, payload, frame) {
		return false
	}
	delivered := h.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	if delivered {
		h.activateAgentDeliveryRetry(payload.DeliveryID)
	} else {
		h.dropAgentDelivery(payload.DeliveryID)
	}
	return delivered
}

func (h *Hub) notifyWorkspaceRunnerFrame(daemonID, workspaceID string, frame []byte) bool {
	key := workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}
	h.mu.RLock()
	c := h.byRunner[key]
	if c == nil {
		h.mu.RUnlock()
		return false
	}
	select {
	case c.send <- frame:
		h.mu.RUnlock()
		return true
	default:
		h.mu.RUnlock()
		h.unregister(c)
		_ = c.conn.Close()
		return false
	}
}

// CloseWorkspaceRunner closes only the still-current Runner for the supplied
// daemon instance. A stale-probe timeout must not disconnect a replacement
// connection that became ready after the probe was issued.
func (h *Hub) CloseWorkspaceRunner(daemonID, workspaceID, daemonInstanceID string) bool {
	if h == nil || strings.TrimSpace(daemonID) == "" || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(daemonInstanceID) == "" {
		return false
	}
	key := workspaceRunnerKey{daemonID: daemonID, workspaceID: workspaceID}
	h.mu.RLock()
	c := h.byRunner[key]
	current := c != nil && c.runnerDaemonInstanceID == daemonInstanceID
	h.mu.RUnlock()
	if !current {
		return false
	}
	h.unregister(c)
	_ = c.conn.Close()
	return true
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

func (h *Hub) readyWorkspaceRunner(c *client, ready protocol.WorkspaceRunnerReadyPayload) bool {
	if c == nil || ready.Validate() != nil || ready.WorkspaceID != c.identity.WorkspaceID {
		return false
	}
	key := workspaceRunnerKey{daemonID: c.identity.DaemonID, workspaceID: c.identity.WorkspaceID}
	h.mu.Lock()
	previous := h.byRunner[key]
	c.runnerDaemonInstanceID = ready.DaemonInstanceID
	c.runnerCapabilities = make(map[string]struct{}, len(ready.ActiveCapabilities))
	for _, capability := range ready.ActiveCapabilities {
		c.runnerCapabilities[capability] = struct{}{}
	}
	c.runnerLastInbound.Store(time.Now().UnixNano())
	h.byRunner[key] = c
	h.mu.Unlock()
	if previous != nil && previous != c {
		// The map points to c before the old connection is closed, so old
		// unregister cannot erase the current Runner ownership.
		h.unregister(previous)
		_ = previous.conn.Close()
	}
	c.startRunnerWatchdog()
	return true
}

func (h *Hub) isCurrentWorkspaceRunner(c *client) bool {
	if c == nil || c.runnerDaemonInstanceID == "" {
		return false
	}
	key := workspaceRunnerKey{daemonID: c.identity.DaemonID, workspaceID: c.identity.WorkspaceID}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.byRunner[key] == c
}

func (h *Hub) dispatchWorkspaceRunnerFrame(c *client, eventType string, payload json.RawMessage) {
	if !h.isCurrentWorkspaceRunner(c) {
		return
	}
	c.runnerLastInbound.Store(time.Now().UnixNano())
	handler := h.workspaceRunnerHandler()
	if handler == nil {
		return
	}
	if err := handler(context.Background(), c.identity, c.runnerDaemonInstanceID, eventType, payload); err != nil {
		slog.Warn("workspace runner frame rejected", "error", err, "daemon_id", c.identity.DaemonID, "workspace_id", c.identity.WorkspaceID, "event_type", eventType)
	}
}

func (c *client) startRunnerWatchdog() {
	if c == nil {
		return
	}
	c.runnerWatchdogOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(runnerPingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-c.done:
					return
				case <-ticker.C:
					lastInbound := time.Unix(0, c.runnerLastInbound.Load())
					if time.Since(lastInbound) > runnerInboundWatchdog {
						_ = c.conn.Close()
						return
					}
					pingID := fmt.Sprintf("runner-%d", time.Now().UnixNano())
					frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerPing, Payload: mustMarshalRaw(protocol.WorkspaceRunnerPingPayload{PingID: pingID})})
					if err != nil {
						continue
					}
					select {
					case c.send <- frame:
					case <-c.done:
						return
					default:
						_ = c.conn.Close()
						return
					}
				}
			}
		}()
	})
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
	wasCurrentRunner := false
	if c.runnerDaemonInstanceID != "" {
		key := workspaceRunnerKey{daemonID: c.identity.DaemonID, workspaceID: c.identity.WorkspaceID}
		if h.byRunner[key] == c {
			wasCurrentRunner = true
			delete(h.byRunner, key)
		}
	}
	close(c.done)
	total := len(h.clients)
	h.mu.Unlock()

	if wasCurrentRunner {
		if handler := h.workspaceRunnerDisconnectHandler(); handler != nil {
			if err := handler(context.Background(), c.identity, c.runnerDaemonInstanceID); err != nil {
				slog.Warn("workspace runner disconnect rejected", "error", err, "daemon_id", c.identity.DaemonID, "workspace_id", c.identity.WorkspaceID, "daemon_instance_id", c.runnerDaemonInstanceID)
			}
		}
	}

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
	case protocol.EventWorkspaceRunnerReady:
		var payload protocol.WorkspaceRunnerReadyPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || !c.hub.readyWorkspaceRunner(c, payload) {
			slog.Warn("workspace runner ready rejected", "daemon_id", c.identity.DaemonID, "workspace_id", c.identity.WorkspaceID)
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventWorkspaceRunnerPong:
		var payload protocol.WorkspaceRunnerPongPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentStartAck:
		var payload protocol.AgentStartAckPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentStatus:
		var payload protocol.AgentStatusPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentSession:
		var payload protocol.AgentSessionPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentActivity:
		var payload protocol.AgentActivityPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentAttachmentReplayReq:
		var payload protocol.WorkspaceRunnerAttachmentReplayRequest
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentAttached:
		var payload protocol.WorkspaceRunnerAgentAttachedPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentDetached:
		var payload protocol.WorkspaceRunnerAgentDetachedPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventAgentAttachmentReplayAck:
		var payload protocol.WorkspaceRunnerAttachmentReplayAck
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		c.hub.dispatchWorkspaceRunnerFrame(c, msg.Type, msg.Payload)
	case protocol.EventMixedRunActivityTransition:
		var payload protocol.MixedRunActivityTransitionPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Validate() != nil {
			return
		}
		if !c.hub.isCurrentWorkspaceRunner(c) {
			return
		}
		c.runnerLastInbound.Store(time.Now().UnixNano())
		handler := c.hub.workspaceRunnerHandler()
		if handler == nil {
			return
		}
		if err := handler(context.Background(), c.identity, c.runnerDaemonInstanceID, msg.Type, msg.Payload); err != nil {
			slog.Warn("workspace runner frame rejected", "error", err, "daemon_id", c.identity.DaemonID, "workspace_id", c.identity.WorkspaceID, "event_type", msg.Type)
			return
		}
		ack := protocol.MixedRunActivityTransitionAckPayload{RunID: payload.RunID, TransitionID: payload.TransitionID}
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventMixedRunActivityAck, Payload: mustMarshalRaw(ack)})
		if err != nil {
			return
		}
		select {
		case c.send <- frame:
		default:
			c.hub.unregister(c)
			_ = c.conn.Close()
		}
	case protocol.EventDaemonHeartbeat:
		c.handleHeartbeatFrame(msg.Payload)
	case protocol.EventDaemonLivenessProbe:
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventDaemonLivenessAck})
		if err != nil {
			return
		}
		select {
		case c.send <- frame:
		default:
			c.hub.unregister(c)
			_ = c.conn.Close()
		}
	case protocol.EventAgentWorkspaceFileTree,
		protocol.EventAgentWorkspaceFileContent,
		protocol.EventDaemonWriteFileResponse,
		protocol.EventDaemonDeleteDirResponse,
		protocol.EventDaemonSeedAgentContextResponse,
		protocol.EventDaemonPreparePiRunResponse,
		protocol.EventDaemonRevokePiRunResponse:
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
	case protocol.EventReminderProjectionReq:
		c.handleReminderProjectionRequest(msg.Payload)
	case protocol.EventReminderProjectionAck:
		c.handleReminderProjectionAck(msg.Payload)
	case protocol.EventAgentDeliverAck:
		handler := c.hub.agentDeliveryAckHandler()
		if handler == nil {
			return
		}
		var payload protocol.AgentDeliverAckPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if err := handler(context.Background(), c.identity, payload); err != nil {
			slog.Warn("agent delivery acknowledgement rejected", "error", err, "daemon_id", c.identity.DaemonID, "agent_id", payload.AgentID, "delivery_id", payload.DeliveryID)
			return
		}
		c.hub.acknowledgeAgentDelivery(c, payload)
	case protocol.EventAgentMessageHandoff:
		handler := c.hub.agentMessageHandoffHandler()
		if handler == nil {
			return
		}
		var payload protocol.AgentMessageHandoffPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if err := handler(context.Background(), c.identity, payload); err != nil {
			slog.Warn("agent Message handoff Activity rejected", "error", err, "daemon_id", c.identity.DaemonID, "agent_id", payload.AgentID, "handoff_id", payload.HandoffID)
		}
	default:
		// Unknown app messages are intentionally ignored for forward
		// compatibility with future daemon → server message types.
	}
}

func (c *client) handleReminderSnapshotRequest(raw json.RawMessage) {
	handler, _ := c.hub.reminderHandlers()
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
	_, handler := c.hub.reminderHandlers()
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
	_, legacyRuntimeAuthorized := c.runtimes[payload.RuntimeID]
	runnerControlAuthorized := c.hub.isCurrentWorkspaceRunner(c) &&
		c.supportsRunnerCapability(protocol.DaemonCapabilityWorkspaceRunnerControlPlane)
	if !legacyRuntimeAuthorized && !runnerControlAuthorized {
		// Legacy connections authenticate a fixed Runtime set. A current ready
		// Workspace Runner instead uses dynamic DB authorization in the handler,
		// because Runtime membership is mutable and not part of Runner identity.
		slog.Warn("daemon websocket heartbeat for unauthorized runtime",
			"daemon_id", c.identity.DaemonID,
			"runtime_id", payload.RuntimeID)
		return
	}
	if runnerControlAuthorized {
		c.runnerLastInbound.Store(time.Now().UnixNano())
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
