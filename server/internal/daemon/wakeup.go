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
	defer func() { _ = conn.Close() }()

	d.setWSConnState("open")
	d.logger.Info("task wakeup websocket connected",
		"runtimes", len(runtimeIDs),
		"conn_state", d.getWSConnState(),
		"inbound_watchdog", d.inboundWatchdogInterval(),
	)
	signalTaskWakeup(taskWakeups, "")

	// Serialize all task-wakeup, Reminder, file RPC, and liveness writes through
	// one channel because gorilla/websocket permits only one concurrent writer.
	writeBufSize := 16
	if 2*len(runtimeIDs) > writeBufSize {
		writeBufSize = 2 * len(runtimeIDs)
	}
	writes := make(chan []byte, writeBufSize)
	writerDone := make(chan struct{})
	go d.runWSWriter(conn, writes, writerDone)
	d.setReminderWS(writes, writerDone, conn.Close)
	if !d.startReminderProjectionReplay() {
		return errors.New("queue initial reminder projection replay")
	}

	watchdog := newInboundWatchdogState(time.Now())
	var watchdogTimedOut atomic.Bool
	watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
	wdDone := make(chan struct{})
	watchdogErrCh := make(chan error, 1)
	go func() {
		defer close(wdDone)
		if err := d.runInboundWatchdog(watchdogCtx, conn, watchdog, writes, &watchdogTimedOut); err != nil {
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

	// Stop the watchdog before closing the serialized writer so no producer
	// can race a write into the closed channel during connection teardown.
	defer func() {
		cancelWatchdog()
		<-wdDone
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
func (d *Daemon) runInboundWatchdog(ctx context.Context, conn *websocket.Conn, state *inboundWatchdogState, writes chan<- []byte, timedOut *atomic.Bool) error {
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
				frame, err := json.Marshal(protocol.Message{Type: protocol.EventDaemonLivenessProbe})
				if err != nil {
					continue
				}
				select {
				case writes <- frame:
				case <-ctx.Done():
					return nil
				default:
					_ = conn.Close()
					return errors.New("liveness probe writer backlog")
				}
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
	attachmentChanged := false
	registry := d.attachmentRegistry()
	if registry != nil {
		for _, runtimeSet := range d.attachmentRuntimeSets() {
			changes, err := registry.Reconcile(runtimeSet)
			if err != nil {
				return err
			}
			attachmentChanged = attachmentChanged || len(changes) > 0
		}
	}
	cacheChanged := false
	if d.reminderCache != nil {
		retiredOwners := []string(nil)
		if registry != nil {
			retiredOwners = registry.DetachedAgentIDs()
		}
		var err error
		cacheChanged, err = d.reminderCache.reconcileRuntimeSet(allowed, retiredOwners)
		if err != nil {
			return err
		}
		if !attachmentChanged && !cacheChanged {
			d.reminderCache.resume()
		}
	}
	return nil
}

// runWSWriter funnels daemon-initiated data and liveness frames into a single
// goroutine. gorilla/websocket
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

func (d *Daemon) controlPlaneHeartbeatPayload(runtimeID string) protocol.DaemonHeartbeatRequestPayload {
	return protocol.DaemonHeartbeatRequestPayload{
		RuntimeID:                 runtimeID,
		ComputerGeneration:        d.cfg.ComputerGeneration,
		SupportsBatchImport:       true,
		SupportsMemoryCuration:    true,
		ActiveMemoryCurationRunID: d.activeMemoryCurationRun(runtimeID),
	}
}

func marshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// handleWSHeartbeatAck dispatches one heartbeat acknowledgement received by
// the current Workspace Runner control plane.
//
// A RuntimeGone=true acknowledgement means the runtime row was deleted
// server-side and routes through the same self-heal entry point as polling.
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
	d.handleHeartbeatActions(ctx, ack.RuntimeID, ack)
}

func (d *Daemon) handleWorkspaceRunnerControlAck(ctx context.Context, ack *HeartbeatResponse) {
	if d == nil || ack == nil {
		return
	}
	if d.bindingHostControl == nil {
		d.handleWSHeartbeatAck(ctx, ack)
		return
	}
	local := *ack
	local.PendingUpdate = nil
	local.PendingMachineUpgrade = nil
	local.PendingRestart = nil
	local.ReleaseManifestBaseURL = ""
	d.handleWSHeartbeatAck(ctx, &local)

	machine := HeartbeatResponse{
		RuntimeID:              ack.RuntimeID,
		Status:                 ack.Status,
		PendingUpdate:          ack.PendingUpdate,
		PendingMachineUpgrade:  ack.PendingMachineUpgrade,
		PendingRestart:         ack.PendingRestart,
		ReleaseManifestBaseURL: ack.ReleaseManifestBaseURL,
	}
	if machine.PendingUpdate == nil && machine.PendingMachineUpgrade == nil && machine.PendingRestart == nil && machine.ReleaseManifestBaseURL == "" {
		return
	}
	forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.bindingHostControl.forwardMachineActions(forwardCtx, machine); err != nil && d.logger != nil {
		d.logger.Warn("forward Binding child machine action to Host failed", "runtime_id", ack.RuntimeID, "reason", "host_unavailable")
	}
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
			if err := d.handleReminderFireResult(payload); err != nil {
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
		case protocol.EventDaemonPreparePiRunRequest:
			var req protocol.PreparePiRunRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("prepare Pi run request invalid payload", "error", err)
				continue
			}
			d.handlePreparePiRunRequest(context.Background(), req, writes)
		case protocol.EventDaemonRevokePiRunRequest:
			var req protocol.RevokePiRunRequestPayload
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				d.logger.Debug("revoke Pi run request invalid payload", "error", err)
				continue
			}
			d.handleRevokePiRunRequest(req, writes)
		}
	}
}

func (d *Daemon) handleReminderSnapshot(payload protocol.ReminderSnapshotPayload) error {
	if d == nil || d.attachmentRegistry() == nil {
		return nil
	}
	attachment, ok := d.currentAttachmentForRuntimeAgent(payload.RuntimeID, payload.AgentID)
	if !ok || int64(attachment.AttachmentGeneration) != payload.PlacementGeneration {
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
	if d == nil || d.reminderCache == nil || d.attachmentRegistry() == nil {
		return nil
	}
	attachment, ok := d.currentAttachmentForRuntimeAgent(payload.RuntimeID, payload.AgentID)
	if !ok || int64(attachment.AttachmentGeneration) != payload.PlacementGeneration {
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

func (d *Daemon) handleReminderFireResult(payload protocol.ReminderFireResultPayload) error {
	if d != nil && d.reminderCache != nil && payload.Ack.AgentID != "" && payload.Ack.ReminderID != "" && payload.Ack.Version > 0 {
		d.reminderCache.ackFireReceipt(reminderDueIdentity{
			OwnerAgentID: payload.Ack.AgentID,
			ReminderID:   payload.Ack.ReminderID,
			Version:      payload.Ack.Version,
		})
	}
	return d.handleReminderProjection(payload.Projection)
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
	// serialized writer. The writer deadline closes genuinely broken network
	// connections and unblocks this wait through reminderWSDone.
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

func (d *Daemon) requestReminderSnapshot(workspaceID, agentID string) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(agentID) == "" {
		return
	}
	registry := d.attachmentRegistry()
	if registry == nil {
		return
	}
	attachment, ok := registry.Resolve(workspaceID, agentID)
	if !ok {
		return
	}
	d.reminderGateMu.Lock()
	if !d.reminderReplayComplete || d.reminderProjectionReplayInFlight {
		if d.reminderPendingSnapshots == nil {
			d.reminderPendingSnapshots = make(map[string]struct{})
		}
		d.reminderPendingSnapshots[agentID] = struct{}{}
		d.reminderGateMu.Unlock()
		return
	}
	d.reminderGateMu.Unlock()
	d.requestReminderSnapshotNow(attachment)
}

func (d *Daemon) requestReminderSnapshotNow(attachment AgentAttachment) bool {
	d.mu.Lock()
	runtime, authorized := d.runtimeIndex[attachment.RuntimeID]
	d.mu.Unlock()
	if !authorized || runtime.WorkspaceID != attachment.WorkspaceID {
		return false
	}
	return d.queueReminderFrame(protocol.EventReminderSnapshotRequest, protocol.ReminderSnapshotRequestPayload{
		AgentID: attachment.AgentID, RuntimeID: attachment.RuntimeID, PlacementGeneration: int64(attachment.AttachmentGeneration),
	})
}

func (d *Daemon) requestReminderSnapshots() {
	for _, attachment := range d.currentAttachments() {
		d.requestReminderSnapshot(attachment.WorkspaceID, attachment.AgentID)
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
	if d.attachmentRegistry() != nil {
		storedResidencies := d.reminderRuntimeResidencies()
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
	ready := d.reminderReplayComplete && !d.reminderProjectionReplayInFlight
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
	localResidencies := d.reminderRuntimeResidencies()
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
		for _, attachment := range d.currentAttachments() {
			d.reminderPendingSnapshots[attachment.AgentID] = struct{}{}
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
		attachment, ok := d.currentAttachmentForAgent(agentID)
		if !ok {
			continue
		}
		if !d.requestReminderSnapshotNow(attachment) {
			return errors.New("queue reminder snapshot after replay")
		}
		d.reminderGateMu.Lock()
		delete(d.reminderPendingSnapshots, agentID)
		d.reminderGateMu.Unlock()
	}
	return nil
}

func (d *Daemon) onReminderTimer(job protocol.ReminderTimerJob) bool {
	attachment, ok := d.currentAttachmentForAgent(job.OwnerAgentID)
	if !ok {
		// Raft 1.0.16: a due fact stays local and retries until the owner wake
		// can be enqueued. Do not tell the server a FIRED occurrence exists.
		if d.logger != nil {
			d.logger.Warn("reminder timer fired for an owner missing from current Agent Attachment set; retrying until owner wake can be enqueued",
				"reminder_id", job.ReminderID, "agent_id", job.OwnerAgentID, "version", job.Version)
		}
		return false
	}
	return d.queueReminderFrame(protocol.EventReminderFireAttempt, protocol.ReminderFireAttemptPayload{
		AgentID:             job.OwnerAgentID,
		RuntimeID:           attachment.RuntimeID,
		PlacementGeneration: int64(attachment.AttachmentGeneration),
		ReminderID:          job.ReminderID,
		Version:             job.Version,
		FiredAtClient:       time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (d *Daemon) queueReminderFireReceipt(receipt reminderDueReceipt) bool {
	attachment, ok := d.currentAttachmentForAgent(receipt.Job.OwnerAgentID)
	if !ok {
		return false
	}
	return d.queueReminderFrame(protocol.EventReminderFireAttempt, protocol.ReminderFireAttemptPayload{
		AgentID:             receipt.Job.OwnerAgentID,
		RuntimeID:           attachment.RuntimeID,
		PlacementGeneration: int64(attachment.AttachmentGeneration),
		ReminderID:          receipt.Job.ReminderID,
		Version:             receipt.Job.Version,
		FiredAtClient:       receipt.FiredAtClient,
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
