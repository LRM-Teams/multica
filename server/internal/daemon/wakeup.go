package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errRuntimeSetChanged = errors.New("runtime set changed")

// errInboundWatchdogTimeout is returned when the daemon-ws connection is
// closed after a full probe→silence cycle with no server frames.
var errInboundWatchdogTimeout = errors.New("inbound watchdog timeout")

type taskWakeup struct {
	runtimeID string
}

func (d *Daemon) taskWakeupLoop(ctx context.Context, taskWakeups chan<- taskWakeup) {
	backoff := time.Second
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()
	// Loop owns backoff/closed visibility: connection only reports connecting/open
	// (and closed after its local resources are torn down). Transient errors set
	// backoff for the entire sleep window; loop exit is the only durable closed.
	defer d.setWSConnState("closed")

	for {
		runtimeIDs := d.allRuntimeIDs()
		if len(runtimeIDs) == 0 {
			// No local runtimes: not connected and not in retry backoff.
			d.setWSConnState("closed")
			if err := sleepWithContextOrRuntimeChange(ctx, 5*time.Second, runtimeSetCh); err != nil {
				return
			}
			continue
		}

		err := d.runTaskWakeupConnection(ctx, runtimeIDs, taskWakeups, runtimeSetCh)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errRuntimeSetChanged) {
			// Fast re-dial: no backoff sleep, no fake backoff state.
			backoff = time.Second
			continue
		}
		if err != nil {
			d.setWSConnState("backoff")
			// PR A only stamps the watchdog sentinel by name. Other failures
			// keep a neutral log field — PR B owns planned/transient/terminal
			// classification and must not inherit a premature "transient" label.
			logArgs := []any{
				"error", err,
				"retry_in", backoff,
				"conn_state", "backoff",
			}
			if errors.Is(err, errInboundWatchdogTimeout) {
				logArgs = append(logArgs, "reason", "watchdog_timeout")
			}
			d.logger.Debug("task wakeup websocket unavailable; polling fallback remains active", logArgs...)
		}

		if err := sleepWithContextOrRuntimeChange(ctx, jitterDuration(backoff), runtimeSetCh); err != nil {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func jitterDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := d / 5
	if spread <= 0 {
		return d
	}
	delta := time.Duration(rand.Int63n(int64(spread)*2+1)) - spread
	return d + delta
}

func (d *Daemon) runTaskWakeupConnection(ctx context.Context, runtimeIDs []string, taskWakeups chan<- taskWakeup, runtimeSetCh <-chan struct{}) error {
	// Connection owns connecting/open only. Outer taskWakeupLoop owns backoff
	// across retry sleep and final closed on loop exit.
	d.setWSConnState("connecting")
	if err := d.reconcileReminderRuntimeSet(runtimeIDs); err != nil {
		return fmt.Errorf("reconcile reminder runtime set: %w", err)
	}
	wsURL, err := taskWakeupURL(d.cfg.ServerBaseURL, runtimeIDs)
	if err != nil {
		return err
	}

	headers := http.Header{}
	if token := d.client.Token(); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	d.client.addIdentityHeaders(headers)

	dialer := taskWakeupDialer()
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	// HTTP heartbeats resume the moment WS detaches so the freshness window
	// from a previous connection cannot keep them silenced past disconnect.
	defer func() {
		_ = conn.Close()
		d.clearWSHeartbeatAcks()
	}()

	d.setWSConnState("open")
	d.logger.Info("task wakeup websocket connected",
		"runtimes", len(runtimeIDs),
		"conn_state", d.getWSConnState(),
		"inbound_watchdog", d.inboundWatchdogInterval(),
	)
	signalTaskWakeup(taskWakeups, "")

	// Serialize all writes through a single channel: the gorilla/websocket
	// Conn does not allow concurrent WriteMessage calls, and the heartbeat
	// sender now coexists with future server-initiated writes. The buffer
	// is sized to fit a full per-runtime heartbeat batch plus headroom; a
	// fixed 8-slot queue would silently drop heartbeats once a daemon
	// watched more than ~8 runtimes (typical when one machine connects to
	// several workspaces), even when the network was healthy.
	writeBufSize := 16
	if 2*len(runtimeIDs) > writeBufSize {
		writeBufSize = 2 * len(runtimeIDs)
	}
	writes := make(chan []byte, writeBufSize)
	writerDone := make(chan struct{})
	go d.runWSWriter(conn, writes, writerDone)
	d.setReminderWS(writes, writerDone, conn.Close)
	d.requestAgentLifecycleReplay()

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		d.runWSHeartbeatSender(heartbeatCtx, runtimeIDs, writes)
	}()

	watchdog := newInboundWatchdogState(time.Now())
	var watchdogTimedOut atomic.Bool
	watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
	wdDone := make(chan struct{})
	watchdogErrCh := make(chan error, 1)
	go func() {
		defer close(wdDone)
		if err := d.runInboundWatchdog(watchdogCtx, conn, watchdog, runtimeIDs, writes, &watchdogTimedOut); err != nil {
			// One-shot result so the outer select can return the sentinel
			// instead of treating a side-effect Close as a generic network error.
			select {
			case watchdogErrCh <- err:
			default:
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.readTaskWakeupMessages(conn, taskWakeups, writes, watchdog)
	}()

	// Defer cleanup must shut goroutines down in this order:
	//   1. cancel the heartbeat sender's ctx
	//   2. wait for the sender to actually return — only then is it safe
	//      to close the writes channel without a "send on closed channel"
	//      panic from sendWSHeartbeats
	//   3. close writes; the writer drains and exits
	//   4. wait for the writer to finish so it doesn't outlive the conn
	//
	// LIFO defer order would close writes before the sender stops, so the
	// teardown is folded into a single deferred function instead.
	defer func() {
		cancelWatchdog()
		<-wdDone
		cancelHeartbeat()
		<-hbDone
		d.clearReminderWS(writes)
		close(writes)
		<-writerDone
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runtimeSetCh:
		return errRuntimeSetChanged
	case err := <-watchdogErrCh:
		return err
	case err := <-errCh:
		// Prefer watchdog sentinel when Close raced the reader.
		if watchdogTimedOut.Load() {
			return errInboundWatchdogTimeout
		}
		select {
		case wdErr := <-watchdogErrCh:
			return wdErr
		default:
		}
		return err
	}
}

func taskWakeupDialer() websocket.Dialer {
	return websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
}

func (d *Daemon) setWSConnState(state string) {
	if d == nil {
		return
	}
	d.wsConnStateMu.Lock()
	prev := d.wsConnState
	d.wsConnState = state
	d.wsConnStateMu.Unlock()
	if prev != state && d.logger != nil {
		d.logger.Info("task wakeup conn state", "conn_state", state, "prev_conn_state", prev)
	}
}

func (d *Daemon) getWSConnState() string {
	if d == nil {
		return ""
	}
	d.wsConnStateMu.RLock()
	defer d.wsConnStateMu.RUnlock()
	return d.wsConnState
}

func (d *Daemon) inboundWatchdogInterval() time.Duration {
	if d == nil {
		return DefaultInboundWatchdog
	}
	// 0 = disabled (env MULTICA_DAEMON_INBOUND_WATCHDOG=0). Negative → default.
	if d.cfg.InboundWatchdog < 0 {
		return DefaultInboundWatchdog
	}
	return d.cfg.InboundWatchdog
}

// runInboundWatchdog implements Raft-aligned two-phase silence detection on
// the task-wakeup WebSocket: probe after one interval of no inbound frames,
// terminate (force reconnect) after a second full interval still silent.
// On terminate it closes conn and returns errInboundWatchdogTimeout so the
// connection loop can surface the cause (not a bare network close error).
func (d *Daemon) runInboundWatchdog(ctx context.Context, conn *websocket.Conn, state *inboundWatchdogState, runtimeIDs []string, writes chan<- []byte, timedOut *atomic.Bool) error {
	interval := d.cfg.InboundWatchdog
	if interval < 0 {
		interval = DefaultInboundWatchdog
	}
	if interval == 0 {
		<-ctx.Done()
		return nil
	}
	// Tick frequently enough that we fire within ~1s of the threshold in tests
	// with shortened intervals, without busy-looping in production.
	tickEvery := interval / 10
	if tickEvery > time.Second {
		tickEvery = time.Second
	}
	if tickEvery < 20*time.Millisecond {
		tickEvery = 20 * time.Millisecond
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			switch state.tick(time.Now(), interval) {
			case inboundWatchdogProbe:
				d.logger.Info("task wakeup inbound probe sent",
					"reason", "inbound_probe",
					"inbound_watchdog", interval,
					"conn_state", d.getWSConnState(),
				)
				// Reuse the existing heartbeat frame as the liveness probe so
				// upgraded servers reply with daemon:heartbeat_ack (marks inbound).
				d.sendWSHeartbeats(ctx, runtimeIDs, writes)
			case inboundWatchdogTerminate:
				d.logger.Warn("task wakeup watchdog timeout; reconnecting",
					"reason", "watchdog_timeout",
					"inbound_watchdog", interval,
					"conn_state", "backoff",
				)
				// Mark before Close so a racing reader maps closed-network to the sentinel.
				if timedOut != nil {
					timedOut.Store(true)
				}
				_ = conn.Close()
				return errInboundWatchdogTimeout
			}
		}
	}
}

func (d *Daemon) reconcileReminderRuntimeSet(runtimeIDs []string) error {
	allowed := make(map[string]bool, len(runtimeIDs))
	for _, runtimeID := range runtimeIDs {
		if runtimeID != "" {
			allowed[runtimeID] = true
		}
	}
	if d.reminderCache != nil {
		d.reminderCache.suspend()
	}
	managerChanged := false
	if d.reminderAgents != nil {
		var err error
		managerChanged, err = d.reminderAgents.reconcileRuntimeSet(allowed)
		if err != nil {
			return err
		}
	}
	cacheChanged := false
	if d.reminderCache != nil {
		retiredOwners := []string(nil)
		if d.reminderAgents != nil {
			retiredOwners = d.reminderAgents.retiredAgentIDs()
		}
		var err error
		cacheChanged, err = d.reminderCache.reconcileRuntimeSet(allowed, retiredOwners)
		if err != nil {
			return err
		}
		if !managerChanged && !cacheChanged {
			d.reminderCache.resume()
		}
	}
	return nil
}

// runWSWriter funnels writes from the heartbeat sender (and any future
// daemon-initiated message) into a single goroutine. gorilla/websocket
// requires that all WriteMessage calls happen from the same goroutine.
func (d *Daemon) runWSWriter(conn *websocket.Conn, writes <-chan []byte, done chan<- struct{}) {
	defer close(done)
	for frame := range writes {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			d.logger.Debug("task wakeup websocket write failed", "error", err)
			conn.Close()
			// Drain remaining frames so the producers don't block forever
			// while waiting for runTaskWakeupConnection to close the channel.
			for range writes {
			}
			return
		}
	}
}

// runWSHeartbeatSender emits a daemon:heartbeat per runtime every
// HeartbeatInterval. The first batch fires immediately so the server learns
// the connection identity without waiting a full interval. Frames are queued
// to the writer; if the queue is full the heartbeat is dropped (the
// freshness window is short enough that one missed beat just means HTTP will
// pick it up next tick).
func (d *Daemon) runWSHeartbeatSender(ctx context.Context, runtimeIDs []string, writes chan<- []byte) {
	d.sendWSHeartbeats(ctx, runtimeIDs, writes)
	var updateChanged <-chan struct{}
	unsubscribe := func() {}
	if d.updateObservation != nil {
		updateChanged, unsubscribe = d.updateObservation.Subscribe()
	}
	defer unsubscribe()
	interval := d.cfg.HeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-updateChanged:
			d.sendWSHeartbeats(ctx, runtimeIDs, writes)
		case <-ticker.C:
			d.sendWSHeartbeats(ctx, runtimeIDs, writes)
		}
	}
}

func (d *Daemon) sendWSHeartbeats(ctx context.Context, runtimeIDs []string, writes chan<- []byte) {
	var observation *protocol.DaemonUpdateObservation
	if d.updateObservation != nil {
		observation = d.updateObservation.PublishedSnapshot()
	}
	for _, rid := range runtimeIDs {
		if ctx.Err() != nil {
			return
		}
		frame, err := json.Marshal(protocol.Message{
			Type: protocol.EventDaemonHeartbeat,
			Payload: marshalRaw(protocol.DaemonHeartbeatRequestPayload{
				RuntimeID:                 rid,
				ComputerGeneration:        d.cfg.ComputerGeneration,
				SupportsBatchImport:       true,
				SupportsMemoryCuration:    true,
				ActiveMemoryCurationRunID: d.activeMemoryCurationRun(rid),
				UpdateObservation:         observation,
			}),
		})
		if err != nil {
			d.logger.Debug("ws heartbeat marshal failed", "error", err, "runtime_id", rid)
			continue
		}
		select {
		case writes <- frame:
		case <-ctx.Done():
			return
		default:
			// Writer is backed up; drop this beat. HTTP heartbeat will resume
			// on its next tick once the freshness window expires.
			d.logger.Debug("ws heartbeat dropped: writer backlog", "runtime_id", rid)
		}
	}
	d.requestAgentLifecycleReplay()
}

func marshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// handleWSHeartbeatAck dispatches one heartbeat_ack received over the WS
// task-wakeup connection. Extracted from readTaskWakeupMessages so tests can
// exercise the branching logic without a real WebSocket.
//
// A RuntimeGone=true ack is the WebSocket twin of an HTTP 404 "runtime not
// found": it tells the daemon the runtime row was deleted server-side. We
// route it through the same self-heal entry point as the HTTP path and do
// NOT record a heartbeat freshness mark — pretending the runtime is alive
// would let HTTP keep skipping its own heartbeat against the dead UUID.
//
// handleRuntimeGone uses the daemon root context for its register call, so
// this function can safely pass any caller context here.
func (d *Daemon) handleWSHeartbeatAck(ctx context.Context, ack *HeartbeatResponse) {
	if ack == nil || ack.RuntimeID == "" {
		return
	}
	if ack.RuntimeGone {
		go d.handleRuntimeGone(ack.RuntimeID)
		return
	}
	d.recordWSHeartbeatAck(ack.RuntimeID)
	d.handleHeartbeatActions(ctx, ack.RuntimeID, ack)
}

// taskWakeupReadLimit must stay aligned with daemonws hub SetReadLimit.
// Heartbeat acks can include pending_memory_curation with DB evidence bundles
// that exceed the old 64KiB client limit and abort the socket with
// "websocket: read limit exceeded", leaving server-side claimed runs as zombies.
const taskWakeupReadLimit = 10 << 20

func (d *Daemon) readTaskWakeupMessages(conn *websocket.Conn, taskWakeups chan<- taskWakeup, writes chan<- []byte, watchdog *inboundWatchdogState) error {
	conn.SetReadLimit(taskWakeupReadLimit)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if watchdog != nil {
			watchdog.onInbound(time.Now())
		}
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			d.logger.Debug("task wakeup websocket invalid message", "error", err)
			continue
		}
		switch msg.Type {
		case protocol.EventDaemonTaskAvailable:
			var payload protocol.TaskAvailablePayload
			if len(msg.Payload) > 0 {
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					d.logger.Debug("task wakeup websocket invalid payload", "error", err)
					continue
				}
			}
			if payload.RuntimeID != "" {
				d.logger.Debug("task wakeup received", "runtime_id", payload.RuntimeID, "task_id", payload.TaskID)
			}
			signalTaskWakeup(taskWakeups, payload.RuntimeID)
		case protocol.EventDaemonHeartbeatAck:
			var ack HeartbeatResponse
			if err := json.Unmarshal(msg.Payload, &ack); err != nil {
				d.logger.Debug("ws heartbeat ack invalid payload", "error", err)
				continue
			}
			d.handleWSHeartbeatAck(context.Background(), &ack)
		case protocol.EventReminderProjection:
			var payload protocol.ReminderProjectionEvent
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				d.logger.Debug("reminder projection invalid payload", "error", err)
				continue
			}
			if err := d.handleReminderProjection(payload); err != nil {
				return err
			}
		case protocol.EventReminderFireResult:
			var payload protocol.ReminderFireResultPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				d.logger.Debug("reminder fire result invalid payload", "error", err)
				continue
			}
			if err := d.handleReminderProjection(payload.Projection); err != nil {
				return err
			}
		case protocol.EventReminderOwnerInput:
			var payload protocol.ReminderOwnerInputPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				d.logger.Warn("transient Reminder owner input", "outcome", string(reminderOwnerInputRejected), "reason_code", "invalid_json")
				continue
			}
			d.handleReminderOwnerInput(context.Background(), payload)
		case protocol.EventReminderSnapshot:
			var payload protocol.ReminderSnapshotPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				d.logger.Debug("reminder snapshot invalid payload", "error", err)
				continue
			}
			if err := d.handleReminderSnapshot(payload); err != nil {
				return err
			}
		case protocol.EventDaemonAgentStop:
			var payload protocol.DaemonAgentStopPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil || strings.TrimSpace(payload.AgentID) == "" {
				d.logger.Debug("daemon agent stop invalid payload", "error", err)
				continue
			}
			if err := d.handleDaemonAgentStop(payload); err != nil {
				d.logger.Warn("persist daemon agent stop failed; reconnecting", "error", err, "agent_id", payload.AgentID)
				return err
			}
		case protocol.EventDaemonAgentStart:
			var payload protocol.DaemonAgentStartPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil || strings.TrimSpace(payload.AgentID) == "" || strings.TrimSpace(payload.RuntimeID) == "" || strings.TrimSpace(payload.WorkspaceID) == "" {
				d.logger.Debug("daemon agent start invalid payload", "error", err)
				continue
			}
			if err := d.handleDaemonAgentStartFrame(payload); err != nil {
				d.logger.Warn("persist daemon agent start failed; reconnecting", "error", err, "agent_id", payload.AgentID)
				return err
			}
		case protocol.EventDaemonAgentLifecycleEnd:
			var payload protocol.DaemonAgentLifecycleReplayEndPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				d.logger.Debug("daemon agent lifecycle replay end invalid payload", "error", err)
				continue
			}
			if err := d.handleDaemonAgentLifecycleReplayEnd(payload); err != nil {
				d.logger.Warn("persist daemon lifecycle cursor failed; reconnecting", "error", err)
				return err
			}
		case protocol.EventReminderProjectionEnd:
			var payload protocol.ReminderProjectionReplayEndPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return err
			}
			if err := d.handleReminderProjectionReplayEnd(payload); err != nil {
				return err
			}
		case protocol.EventAgentWorkspaceList:
			var req protocol.ListWorkdirFilesRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("list files request invalid payload", "error", err)
				continue
			}
			d.handleListFilesRequest(req, writes)
		case protocol.EventAgentWorkspaceRead:
			var req protocol.ReadWorkdirFileRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("read file request invalid payload", "error", err)
				continue
			}
			d.handleReadFileRequest(req, writes)
		case protocol.EventDaemonWriteFileRequest:
			var req protocol.WriteWorkdirFileRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("write file request invalid payload", "error", err)
				continue
			}
			d.handleWriteFileRequest(req, writes)
		case protocol.EventDaemonDeleteDirRequest:
			var req protocol.DeleteWorkdirDirRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("delete dir request invalid payload", "error", err)
				continue
			}
			d.handleDeleteDirRequest(req, writes)
		case protocol.EventDaemonSeedAgentContextRequest:
			var req protocol.SeedAgentContextRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("seed agent context request invalid payload", "error", err)
				continue
			}
			d.handleSeedAgentContextRequest(req, writes)
		}
	}
}

func (d *Daemon) handleReminderSnapshot(payload protocol.ReminderSnapshotPayload) error {
	if d == nil || d.reminderAgents == nil {
		return nil
	}
	owner, ok := d.reminderAgents.get(payload.AgentID)
	if !ok || owner.RuntimeID != payload.RuntimeID || owner.PlacementGeneration != payload.PlacementGeneration {
		if d.logger != nil {
			d.logger.Warn("reminder snapshot rejected unknown local owner", "agent_id", payload.AgentID)
		}
		return nil
	}
	if d.reminderCache != nil {
		if _, err := d.reminderCache.snapshot(payload.RuntimeID, payload.AgentID, payload.ProjectionWatermark, payload.Reminders); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) handleReminderProjection(payload protocol.ReminderProjectionEvent) error {
	if d == nil || d.reminderCache == nil || d.reminderAgents == nil {
		return nil
	}
	owner, ok := d.reminderAgents.get(payload.AgentID)
	if !ok || owner.RuntimeID != payload.RuntimeID || owner.PlacementGeneration != payload.PlacementGeneration {
		if err := d.reminderCache.advanceProjectionCursor(payload.RuntimeID, payload.PrevSeq, payload.Seq); err != nil {
			if errors.Is(err, errReminderProjectionGap) {
				d.requestReminderProjectionReplay()
				return nil
			}
			return err
		}
		d.reminderGateMu.Lock()
		replaying := d.reminderProjectionReplayInFlight
		d.reminderGateMu.Unlock()
		if replaying {
			return nil
		}
		return d.ackReminderProjectionCursors(d.reminderCache.projectionCursors())
	}
	if _, err := d.reminderCache.applyProjection(payload); err != nil {
		if errors.Is(err, errReminderProjectionGap) {
			d.requestReminderProjectionReplay()
			return nil
		}
		return err
	}
	d.reminderGateMu.Lock()
	replaying := d.reminderProjectionReplayInFlight
	d.reminderGateMu.Unlock()
	if replaying {
		return nil
	}
	return d.ackReminderProjectionCursors(d.reminderCache.projectionCursors())
}

func (d *Daemon) handleDaemonAgentStop(payload protocol.DaemonAgentStopPayload) error {
	if d == nil || strings.TrimSpace(payload.AgentID) == "" || strings.TrimSpace(payload.RuntimeID) == "" || payload.PlacementGeneration < 1 {
		return nil
	}
	return d.removeReminderAgent(payload.AgentID, payload.RuntimeID, payload.PlacementGeneration)
}

func (d *Daemon) handleDaemonAgentStart(payload protocol.DaemonAgentStartPayload) error {
	_, err := d.applyDaemonAgentStart(payload)
	return err
}

// handleDaemonAgentStartFrame starts Message recovery only when this lifecycle
// frame creates or replaces the local Message coordinator. Replayed Task
// lifecycle state must not restart recovery for every existing Agent: the
// Workspace Runner connection owns reconnect recovery for chat.
func (d *Daemon) handleDaemonAgentStartFrame(payload protocol.DaemonAgentStartPayload) error {
	created, err := d.applyDaemonAgentStart(payload)
	if err != nil {
		return err
	}
	if created {
		d.beginAgentMessageRecovery(payload.AgentID)
	}
	return nil
}

func (d *Daemon) applyDaemonAgentStart(payload protocol.DaemonAgentStartPayload) (bool, error) {
	if d == nil || d.reminderAgents == nil {
		return false, nil
	}
	// A server-originated owner frame is the capability proof. The registration
	// response may predate a server upgrade, so only local runtime/workspace and
	// placement-generation checks may reject this frame.
	d.mu.Lock()
	runtime, runtimeKnown := d.runtimeIndex[payload.RuntimeID]
	d.mu.Unlock()
	if !runtimeKnown || runtime.WorkspaceID != payload.WorkspaceID {
		d.logger.Warn("daemon agent start rejected outside local runtime", "agent_id", payload.AgentID, "runtime_id", payload.RuntimeID, "workspace_id", payload.WorkspaceID)
		return false, nil
	}
	changed, accepted, err := d.reminderAgents.applyStart(payload.AgentID, payload.RuntimeID, payload.WorkspaceID, payload.PlacementGeneration)
	if err != nil {
		return false, err
	}
	coordinatorCreated := false
	if accepted {
		agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, payload.WorkspaceID, payload.AgentID)
		if err := ensureMulticaAgentRoot(agentRoot); err != nil {
			return false, fmt.Errorf("create Agent root for Message coordinator: %w", err)
		}
		coordinatorCreated, err = d.ensureIdleMessageCoordinator(payload.AgentID, payload.RuntimeID, agentRoot)
		if err != nil {
			return false, fmt.Errorf("register Agent Message coordinator: %w", err)
		}
		if err := d.ensureWorkspaceRunnerManagedAgent(payload.WorkspaceID, payload.AgentID); err != nil {
			return false, fmt.Errorf("register Agent Workspace Runner lifecycle: %w", err)
		}
	}
	if accepted && changed && !payload.Replay {
		d.requestReminderSnapshot(payload.AgentID)
	} else if accepted && changed {
		d.reminderGateMu.Lock()
		if d.reminderPendingSnapshots == nil {
			d.reminderPendingSnapshots = make(map[string]struct{})
		}
		d.reminderPendingSnapshots[payload.AgentID] = struct{}{}
		d.reminderGateMu.Unlock()
	}
	return coordinatorCreated, nil
}

// ensureWorkspaceRunnerManagedAgent makes durable local Agent residency
// visible through the same Runner lifecycle projection used by Presence. The
// producer retains the launch when the Runner transport is still connecting;
// AttachTransport replays it on that connection instead of inventing a second
// launch identity.
func (d *Daemon) ensureWorkspaceRunnerManagedAgent(workspaceID, agentID string) error {
	producer := d.workspaceAgentActivityProducer(workspaceID)
	status, session, created, err := producer.EnsureManagedAgent(agentID)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	d.sendWorkspaceRunnerAgentFrame(agentID, protocol.EventAgentStatus, status)
	d.sendWorkspaceRunnerAgentFrame(agentID, protocol.EventAgentSession, session)
	return nil
}

func (d *Daemon) handleDaemonAgentLifecycleReplayEnd(payload protocol.DaemonAgentLifecycleReplayEndPayload) error {
	if d == nil || d.reminderAgents == nil {
		return nil
	}
	d.mu.Lock()
	for runtimeID := range payload.RuntimeCursors {
		if _, ok := d.runtimeIndex[runtimeID]; !ok {
			d.mu.Unlock()
			return fmt.Errorf("agent lifecycle replay runtime outside current daemon set %s", runtimeID)
		}
	}
	d.mu.Unlock()
	if err := d.reminderAgents.advanceLifecycleCursors(payload.RuntimeCursors); err != nil {
		return err
	}
	if !d.queueReminderFrame(protocol.EventDaemonAgentLifecycleAck, protocol.DaemonAgentLifecycleAckPayload{RuntimeCursors: payload.RuntimeCursors}) {
		return errors.New("queue reminder lifecycle ack")
	}
	if !d.startReminderProjectionReplay() {
		return errors.New("queue reminder projection replay")
	}
	d.reminderGateMu.Lock()
	d.reminderLifecycleReplayInFlight = false
	d.reminderGateMu.Unlock()
	return nil
}

func (d *Daemon) setReminderWS(writes chan<- []byte, done <-chan struct{}, closeFn func() error) {
	d.reminderWSMu.Lock()
	d.reminderWrites = writes
	d.reminderWSDone = done
	d.reminderClose = closeFn
	d.reminderWSMu.Unlock()
	d.reminderGateMu.Lock()
	d.reminderLifecycleReplayInFlight = false
	d.reminderReplayComplete = false
	d.reminderProjectionReplayInFlight = false
	d.reminderProjectionReplayPending = false
	if d.reminderPendingSnapshots == nil {
		d.reminderPendingSnapshots = make(map[string]struct{})
	}
	d.reminderGateMu.Unlock()
	if d.reminderCache != nil {
		d.reminderCache.beginConnection()
	}
}

func (d *Daemon) clearReminderWS(writes chan<- []byte) {
	d.reminderWSMu.Lock()
	if d.reminderWrites == writes {
		d.reminderWrites = nil
		d.reminderWSDone = nil
		d.reminderClose = nil
	}
	d.reminderWSMu.Unlock()
	d.reminderGateMu.Lock()
	d.reminderLifecycleReplayInFlight = false
	d.reminderReplayComplete = false
	d.reminderProjectionReplayInFlight = false
	d.reminderProjectionReplayPending = false
	d.reminderGateMu.Unlock()
}

func (d *Daemon) queueTaskWakeupFrame(writes chan<- []byte, eventType string, payload any) error {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
	if err != nil {
		return fmt.Errorf("marshal %s frame: %w", eventType, err)
	}
	d.reminderWSMu.RLock()
	defer d.reminderWSMu.RUnlock()
	if d.reminderWrites == nil || d.reminderWSDone == nil {
		return errors.New("task wakeup websocket is unavailable")
	}
	if writes != nil && d.reminderWrites != writes {
		return errors.New("task wakeup websocket generation changed")
	}
	// Raft-style transport contract: application frames wait for the single
	// serialized writer. Only reconstructible signals such as heartbeat may be
	// dropped under local backpressure. The writer deadline closes genuinely
	// broken network connections and unblocks this wait through reminderWSDone.
	select {
	case d.reminderWrites <- frame:
		return nil
	case <-d.reminderWSDone:
		return errors.New("task wakeup websocket writer stopped")
	}
}

func (d *Daemon) queueReminderFrame(eventType string, payload any) bool {
	if err := d.queueTaskWakeupFrame(nil, eventType, payload); err != nil {
		d.logger.Warn("reminder websocket frame not queued", "type", eventType, "error", err)
		return false
	}
	return true
}

func (d *Daemon) requestReminderSnapshot(agentID string) {
	if strings.TrimSpace(agentID) == "" {
		return
	}
	owner, ok := d.reminderAgents.get(agentID)
	if !ok {
		return
	}
	d.reminderGateMu.Lock()
	if !d.reminderReplayComplete || d.reminderLifecycleReplayInFlight || d.reminderProjectionReplayInFlight {
		if d.reminderPendingSnapshots == nil {
			d.reminderPendingSnapshots = make(map[string]struct{})
		}
		d.reminderPendingSnapshots[agentID] = struct{}{}
		d.reminderGateMu.Unlock()
		return
	}
	d.reminderGateMu.Unlock()
	d.requestReminderSnapshotNow(agentID, owner)
}

func (d *Daemon) requestReminderSnapshotNow(agentID string, owner reminderAgentResidency) bool {
	d.mu.Lock()
	_, authorized := d.runtimeIndex[owner.RuntimeID]
	d.mu.Unlock()
	if !authorized {
		return false
	}
	return d.queueReminderFrame(protocol.EventReminderSnapshotRequest, protocol.ReminderSnapshotRequestPayload{
		AgentID: agentID, RuntimeID: owner.RuntimeID, PlacementGeneration: owner.PlacementGeneration,
	})
}

func (d *Daemon) requestAgentLifecycleReplay() bool {
	// This is an attach-time protocol probe. Old servers intentionally ignore
	// unknown application frames, while upgraded servers can recover a daemon
	// whose cached registration capabilities are stale.
	d.reminderGateMu.Lock()
	if d.reminderLifecycleReplayInFlight || d.reminderProjectionReplayInFlight {
		d.reminderGateMu.Unlock()
		return true
	}
	d.reminderLifecycleReplayInFlight = true
	d.reminderGateMu.Unlock()
	storedCursors := map[string]int64{}
	if d.reminderAgents != nil {
		storedCursors = d.reminderAgents.lifecycleCursors()
	}
	cursors := map[string]int64{}
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		cursors[runtimeID] = storedCursors[runtimeID]
	}
	d.mu.Unlock()
	if !d.queueReminderFrame(protocol.EventDaemonAgentLifecycleReq, protocol.DaemonAgentLifecycleRequestPayload{RuntimeCursors: cursors}) {
		d.reminderGateMu.Lock()
		d.reminderLifecycleReplayInFlight = false
		d.reminderGateMu.Unlock()
		return false
	}
	return true
}

func (d *Daemon) requestReminderSnapshots() {
	if d.reminderAgents == nil {
		return
	}
	for _, agentID := range d.reminderAgents.residentAgentIDs() {
		d.requestReminderSnapshot(agentID)
	}
}

func (d *Daemon) startReminderProjectionReplay() bool {
	d.reminderGateMu.Lock()
	if d.reminderProjectionReplayInFlight {
		d.reminderGateMu.Unlock()
		return true
	}
	d.reminderProjectionReplayInFlight = true
	d.reminderGateMu.Unlock()
	runtimeIDs := make([]string, 0)
	residencies := map[string][]protocol.ReminderRuntimeResidency{}
	if d.reminderAgents != nil {
		storedResidencies := d.reminderAgents.runtimeResidencies()
		d.mu.Lock()
		for runtimeID := range d.runtimeIndex {
			if owners := storedResidencies[runtimeID]; len(owners) > 0 {
				residencies[runtimeID] = owners
			}
		}
		d.mu.Unlock()
	}
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		runtimeIDs = append(runtimeIDs, runtimeID)
	}
	d.mu.Unlock()
	sort.Strings(runtimeIDs)
	cursors := make(map[string]int64, len(runtimeIDs))
	for _, runtimeID := range runtimeIDs {
		cursors[runtimeID] = 0
	}
	resetRequired := make(map[string]bool)
	if d.reminderCache != nil {
		var err error
		cursors, resetRequired, err = d.reminderCache.projectionReplayState(runtimeIDs)
		if err != nil {
			d.reminderGateMu.Lock()
			d.reminderProjectionReplayInFlight = false
			d.reminderGateMu.Unlock()
			if d.logger != nil {
				d.logger.Warn("persist reminder recovery request failed", "error", err)
			}
			return false
		}
	}
	if !d.queueReminderFrame(protocol.EventReminderProjectionReq, protocol.ReminderProjectionRequestPayload{
		RuntimeCursors: cursors, RuntimeResidencies: residencies, RuntimeResetRequired: resetRequired,
	}) {
		d.reminderGateMu.Lock()
		d.reminderProjectionReplayInFlight = false
		d.reminderGateMu.Unlock()
		return false
	}
	return true
}

func (d *Daemon) requestReminderProjectionReplay() {
	if d.reminderCache != nil {
		d.reminderCache.suspend()
	}
	d.reminderGateMu.Lock()
	ready := d.reminderReplayComplete && !d.reminderLifecycleReplayInFlight && !d.reminderProjectionReplayInFlight
	d.reminderReplayComplete = false
	if !ready {
		d.reminderProjectionReplayPending = true
		d.reminderGateMu.Unlock()
		return
	}
	d.reminderGateMu.Unlock()
	if !d.startReminderProjectionReplay() {
		d.reminderGateMu.Lock()
		d.reminderProjectionReplayPending = true
		d.reminderGateMu.Unlock()
	}
}

func (d *Daemon) ackReminderProjectionCursors(cursors map[string]int64) error {
	filtered := make(map[string]int64)
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		filtered[runtimeID] = cursors[runtimeID]
	}
	d.mu.Unlock()
	if !d.queueReminderFrame(protocol.EventReminderProjectionAck, protocol.ReminderProjectionAckPayload{RuntimeCursors: filtered}) {
		return errors.New("queue reminder projection ack")
	}
	return nil
}

func (d *Daemon) handleReminderProjectionReplayEnd(payload protocol.ReminderProjectionReplayEndPayload) error {
	if d.reminderCache == nil {
		return nil
	}
	localResidencies := d.reminderAgents.runtimeResidencies()
	d.mu.Lock()
	for runtimeID := range payload.RuntimeCursors {
		if _, ok := d.runtimeIndex[runtimeID]; !ok {
			d.mu.Unlock()
			return fmt.Errorf("reminder projection runtime outside current daemon set %s", runtimeID)
		}
	}
	for runtimeID := range localResidencies {
		if _, ok := d.runtimeIndex[runtimeID]; !ok {
			delete(localResidencies, runtimeID)
		}
	}
	d.mu.Unlock()
	for runtimeID := range d.reminderCache.requiredRuntimeResets() {
		if _, ok := payload.RuntimeResets[runtimeID]; !ok {
			return fmt.Errorf("reminder runtime reset required but omitted %s", runtimeID)
		}
	}
	for runtimeID, reset := range payload.RuntimeResets {
		end, ok := payload.RuntimeCursors[runtimeID]
		if !ok || end != reset.ProjectionWatermark {
			return fmt.Errorf("invalid reminder runtime reset watermark %s", runtimeID)
		}
		local := make(map[string]int64, len(localResidencies[runtimeID]))
		for _, owner := range localResidencies[runtimeID] {
			local[owner.AgentID] = owner.PlacementGeneration
		}
		if len(reset.Owners) != len(local) {
			return fmt.Errorf("reminder runtime reset owner set mismatch %s", runtimeID)
		}
		for _, owner := range reset.Owners {
			generation, exists := local[owner.AgentID]
			if !exists || (!owner.Terminal && owner.PlacementGeneration != generation) || (owner.Terminal && owner.PlacementGeneration < generation) {
				return fmt.Errorf("reminder runtime reset owner mismatch %s:%s", runtimeID, owner.AgentID)
			}
			delete(local, owner.AgentID)
		}
		if len(local) != 0 {
			return fmt.Errorf("reminder runtime reset omitted local owners %s", runtimeID)
		}
		if err := d.reminderCache.markRuntimeReset(runtimeID); err != nil {
			return err
		}
		if err := d.reminderCache.resetRuntime(runtimeID, reset); err != nil {
			return err
		}
		for _, owner := range reset.Owners {
			if !owner.Terminal {
				continue
			}
			if _, _, err := d.reminderAgents.applyStop(owner.AgentID, runtimeID, owner.PlacementGeneration); err != nil {
				return err
			}
		}
	}
	cursors := d.reminderCache.projectionCursors()
	for runtimeID, end := range payload.RuntimeCursors {
		if cursors[runtimeID] < end {
			return fmt.Errorf("reminder projection replay ended before cursor %s:%d", runtimeID, end)
		}
	}
	d.reminderGateMu.Lock()
	if d.reminderProjectionReplayPending {
		d.reminderProjectionReplayPending = false
		d.reminderProjectionReplayInFlight = false
		d.reminderGateMu.Unlock()
		if !d.startReminderProjectionReplay() {
			return errors.New("queue successor reminder projection replay")
		}
		return nil
	}
	d.reminderGateMu.Unlock()
	if err := d.ackReminderProjectionCursors(cursors); err != nil {
		return err
	}
	d.reminderGateMu.Lock()
	initialReplay := !d.reminderReplayComplete
	d.reminderProjectionReplayInFlight = false
	d.reminderReplayComplete = true
	if d.reminderPendingSnapshots == nil {
		d.reminderPendingSnapshots = make(map[string]struct{})
	}
	if initialReplay {
		for _, agentID := range d.reminderAgents.residentAgentIDs() {
			owner, ok := d.reminderAgents.get(agentID)
			if !ok {
				continue
			}
			d.mu.Lock()
			_, authorized := d.runtimeIndex[owner.RuntimeID]
			d.mu.Unlock()
			if authorized {
				d.reminderPendingSnapshots[agentID] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(d.reminderPendingSnapshots))
	for agentID := range d.reminderPendingSnapshots {
		ids = append(ids, agentID)
	}
	d.reminderGateMu.Unlock()
	d.reminderCache.resume()
	sort.Strings(ids)
	for _, agentID := range ids {
		owner, ok := d.reminderAgents.get(agentID)
		if !ok {
			continue
		}
		if !d.requestReminderSnapshotNow(agentID, owner) {
			return errors.New("queue reminder snapshot after replay")
		}
		d.reminderGateMu.Lock()
		delete(d.reminderPendingSnapshots, agentID)
		d.reminderGateMu.Unlock()
	}
	return nil
}

func (d *Daemon) onReminderTimer(job protocol.ReminderTimerJob) {
	owner, ok := d.reminderAgents.get(job.OwnerAgentID)
	if !ok {
		// Task #69: this used to be a silent return — a due reminder whose
		// owner isn't (yet, or anymore) in the local residency map produced
		// zero trace: no fire_attempt sent, no error, no reconnect forced.
		// A perfectly healthy WS connection (heartbeat alive, projection
		// cursor advancing) would show no symptom at all while this
		// specific reminder just never fires. Owner-not-present-yet is a
		// legitimate transient state (e.g. an agent lifecycle race), so
		// this does not fail closed here — reminderCache's local retry
		// (task #68's fireAndScheduleRetryLocked) already re-invokes this
		// function on a schedule, so a transient gap self-heals the moment
		// the owner registers; logging is what makes a *persistent* gap
		// (owner genuinely never resolves) visible instead of invisible.
		if d.logger != nil {
			d.logger.Warn("reminder timer fired for an owner missing from local residency map; local retry will re-check",
				"reminder_id", job.ReminderID, "agent_id", job.OwnerAgentID, "version", job.Version)
		}
		return
	}
	d.queueReminderFrame(protocol.EventReminderFireAttempt, protocol.ReminderFireAttemptPayload{
		AgentID:             job.OwnerAgentID,
		RuntimeID:           owner.RuntimeID,
		PlacementGeneration: owner.PlacementGeneration,
		ReminderID:          job.ReminderID,
		Version:             job.Version,
		FiredAtClient:       time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func signalTaskWakeup(taskWakeups chan<- taskWakeup, runtimeID string) {
	select {
	case taskWakeups <- taskWakeup{runtimeID: runtimeID}:
	default:
	}
}

func taskWakeupURL(baseURL string, runtimeIDs []string) (string, error) {
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
	u.RawPath = ""
	q := u.Query()
	ids := append([]string(nil), runtimeIDs...)
	sort.Strings(ids)
	q.Set("runtime_ids", strings.Join(ids, ","))
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func sleepWithContextOrRuntimeChange(ctx context.Context, d time.Duration, runtimeSetCh <-chan struct{}) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runtimeSetCh:
		return nil
	case <-timer.C:
		return nil
	}
}
