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
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errRuntimeSetChanged = errors.New("runtime set changed")

type taskWakeup struct {
	runtimeID string
}

func (d *Daemon) taskWakeupLoop(ctx context.Context, taskWakeups chan<- taskWakeup) {
	backoff := time.Second
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	for {
		runtimeIDs := d.allRuntimeIDs()
		if len(runtimeIDs) == 0 {
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
			backoff = time.Second
			continue
		}
		if err != nil {
			d.logger.Debug("task wakeup websocket unavailable; polling fallback remains active", "error", err, "retry_in", backoff)
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
	wsURL, err := taskWakeupURL(d.cfg.ServerBaseURL, runtimeIDs)
	if err != nil {
		return err
	}

	headers := http.Header{}
	if token := d.client.Token(); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	if d.client.platform != "" {
		headers.Set("X-Client-Platform", d.client.platform)
	}
	if d.client.version != "" {
		headers.Set("X-Client-Version", d.client.version)
	}
	if d.client.os != "" {
		headers.Set("X-Client-OS", d.client.os)
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()
	// HTTP heartbeats resume the moment WS detaches so the freshness window
	// from a previous connection cannot keep them silenced past disconnect.
	defer d.clearWSHeartbeatAcks()

	d.logger.Info("task wakeup websocket connected", "runtimes", len(runtimeIDs))
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.readTaskWakeupMessages(conn, taskWakeups, writes)
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
	case err := <-errCh:
		return err
	}
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
		case <-ticker.C:
			d.sendWSHeartbeats(ctx, runtimeIDs, writes)
		}
	}
}

func (d *Daemon) sendWSHeartbeats(ctx context.Context, runtimeIDs []string, writes chan<- []byte) {
	for _, rid := range runtimeIDs {
		if ctx.Err() != nil {
			return
		}
		frame, err := json.Marshal(protocol.Message{
			Type: protocol.EventDaemonHeartbeat,
			Payload: marshalRaw(protocol.DaemonHeartbeatRequestPayload{
				RuntimeID:                 rid,
				SupportsBatchImport:       true,
				SupportsMemoryCuration:    true,
				ActiveMemoryCurationRunID: d.activeMemoryCurationRun(rid),
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
	d.requestReminderProjectionReplay()
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

func (d *Daemon) readTaskWakeupMessages(conn *websocket.Conn, taskWakeups chan<- taskWakeup, writes chan<- []byte) error {
	conn.SetReadLimit(taskWakeupReadLimit)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
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
			if err := d.handleDaemonAgentStart(payload); err != nil {
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
		case protocol.EventDaemonListFilesRequest:
			var req protocol.ListWorkdirFilesRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("list files request invalid payload", "error", err)
				continue
			}
			d.handleListFilesRequest(req, writes)
		case protocol.EventDaemonReadFileRequest:
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
		return d.ackReminderProjectionCursors(d.reminderCache.projectionCursors())
	}
	if _, err := d.reminderCache.applyProjection(payload); err != nil {
		if errors.Is(err, errReminderProjectionGap) {
			d.requestReminderProjectionReplay()
			return nil
		}
		return err
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
	if d == nil || d.reminderAgents == nil || !d.workspaceReminderCapabilityEnabled(payload.WorkspaceID) {
		return nil
	}
	d.mu.Lock()
	_, runtimeKnown := d.runtimeIndex[payload.RuntimeID]
	d.mu.Unlock()
	if !runtimeKnown {
		d.logger.Warn("daemon agent start rejected unknown local runtime", "agent_id", payload.AgentID, "runtime_id", payload.RuntimeID)
		return nil
	}
	changed, accepted, err := d.reminderAgents.applyStart(payload.AgentID, payload.RuntimeID, payload.WorkspaceID, payload.PlacementGeneration)
	if err != nil {
		return err
	}
	if accepted && changed && !payload.Replay {
		d.requestReminderSnapshot(payload.AgentID)
	}
	return nil
}

func (d *Daemon) handleDaemonAgentLifecycleReplayEnd(payload protocol.DaemonAgentLifecycleReplayEndPayload) error {
	if d == nil || d.reminderAgents == nil {
		return nil
	}
	if err := d.reminderAgents.advanceLifecycleCursors(payload.RuntimeCursors); err != nil {
		return err
	}
	if !d.queueReminderFrame(protocol.EventDaemonAgentLifecycleAck, protocol.DaemonAgentLifecycleAckPayload{RuntimeCursors: payload.RuntimeCursors}) {
		return errors.New("queue reminder lifecycle ack")
	}
	if !d.startReminderProjectionReplay() {
		return errors.New("queue reminder projection replay")
	}
	return nil
}

func (d *Daemon) setReminderWS(writes chan<- []byte, done <-chan struct{}, closeFn func() error) {
	d.reminderWSMu.Lock()
	d.reminderWrites = writes
	d.reminderWSDone = done
	d.reminderClose = closeFn
	d.reminderWSMu.Unlock()
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = false
	d.reminderProjectionReplayInFlight = false
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
	d.reminderReplayComplete = false
	d.reminderProjectionReplayInFlight = false
	d.reminderGateMu.Unlock()
}

func (d *Daemon) queueReminderFrame(eventType string, payload any) bool {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
	if err != nil {
		d.logger.Warn("reminder websocket marshal failed", "type", eventType, "error", err)
		return false
	}
	d.reminderWSMu.RLock()
	defer d.reminderWSMu.RUnlock()
	if d.reminderWrites == nil {
		return false
	}
	select {
	case d.reminderWrites <- frame:
		return true
	case <-d.reminderWSDone:
		return false
	default:
		if d.reminderClose != nil {
			go d.reminderClose()
		}
		d.logger.Warn("reminder websocket writer backlog; reconnecting for snapshot recovery", "type", eventType)
		return false
	}
}

func (d *Daemon) requestReminderSnapshot(agentID string) {
	if strings.TrimSpace(agentID) == "" || !d.reminderCapabilityEnabled() {
		return
	}
	owner, ok := d.reminderAgents.get(agentID)
	if !ok {
		return
	}
	d.reminderGateMu.Lock()
	if !d.reminderReplayComplete {
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
	return d.queueReminderFrame(protocol.EventReminderSnapshotRequest, protocol.ReminderSnapshotRequestPayload{
		AgentID: agentID, RuntimeID: owner.RuntimeID, PlacementGeneration: owner.PlacementGeneration,
	})
}

func (d *Daemon) requestAgentLifecycleReplay() {
	if !d.reminderCapabilityEnabled() {
		return
	}
	cursors := map[string]int64{}
	if d.reminderAgents != nil {
		cursors = d.reminderAgents.lifecycleCursors()
	}
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		if _, ok := cursors[runtimeID]; !ok {
			cursors[runtimeID] = 0
		}
	}
	d.mu.Unlock()
	d.queueReminderFrame(protocol.EventDaemonAgentLifecycleReq, protocol.DaemonAgentLifecycleRequestPayload{RuntimeCursors: cursors})
}

func (d *Daemon) requestReminderSnapshots() {
	if d.reminderAgents == nil || !d.reminderCapabilityEnabled() {
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
	storedCursors := map[string]int64{}
	if d.reminderCache != nil {
		storedCursors = d.reminderCache.projectionCursors()
	}
	cursors := map[string]int64{}
	residencies := map[string][]protocol.ReminderRuntimeResidency{}
	if d.reminderAgents != nil {
		residencies = d.reminderAgents.runtimeResidencies()
	}
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		cursors[runtimeID] = storedCursors[runtimeID]
	}
	d.mu.Unlock()
	if !d.queueReminderFrame(protocol.EventReminderProjectionReq, protocol.ReminderProjectionRequestPayload{
		RuntimeCursors: cursors, RuntimeResidencies: residencies,
	}) {
		d.reminderGateMu.Lock()
		d.reminderProjectionReplayInFlight = false
		d.reminderGateMu.Unlock()
		return false
	}
	return true
}

func (d *Daemon) requestReminderProjectionReplay() {
	d.reminderGateMu.Lock()
	ready := d.reminderReplayComplete && !d.reminderProjectionReplayInFlight
	d.reminderGateMu.Unlock()
	if ready {
		d.startReminderProjectionReplay()
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
			d.reminderPendingSnapshots[agentID] = struct{}{}
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

func (d *Daemon) reminderCapabilityEnabled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ws := range d.workspaces {
		for _, capability := range ws.serverCapabilities {
			if capability == protocol.DaemonCapabilityReminderVersionedCache {
				return true
			}
		}
	}
	return false
}

func (d *Daemon) workspaceReminderCapabilityEnabled(workspaceID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	ws := d.workspaces[workspaceID]
	if ws == nil {
		return false
	}
	for _, capability := range ws.serverCapabilities {
		if capability == protocol.DaemonCapabilityReminderVersionedCache {
			return true
		}
	}
	return false
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
