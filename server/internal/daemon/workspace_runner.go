package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const workspaceRunnerWriteTimeout = 10 * time.Second

// workspaceRunnerLoop owns one WebSocket per authenticated workspace. It is
// intentionally separate from the removed runtime-multiplexed wake socket: a
// Runner survives a workspace with zero runtimes and can never receive another
// workspace's commands.
func (d *Daemon) workspaceRunnerLoop(ctx context.Context) {
	if d == nil || d.runtimeSet == nil {
		return
	}
	changes, unsub := d.runtimeSet.Subscribe()
	defer unsub()
	interval := d.workspaceRunnerReconcileEvery
	if interval <= 0 {
		interval = computer.RunnerReconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	d.reconcileWorkspaceRunners(ctx)
	for {
		select {
		case <-ctx.Done():
			d.stopWorkspaceRunners()
			return
		case <-changes:
			d.reconcileWorkspaceRunners(ctx)
		case <-ticker.C:
			d.reconcileWorkspaceRunners(ctx)
		}
	}
}

// reconcileWorkspaceRunners treats a Binding as the Runner's sole identity.
// Runtime changes merely wake reconciliation; they never replace an existing
// Runner, its connection, or its locally owned Inbox/lifecycle state.
func (d *Daemon) reconcileWorkspaceRunners(parent context.Context) {
	desired := make(map[string]struct{})
	for _, workspaceID := range d.workspaceRunnerWorkspaceIDs() {
		desired[workspaceID] = struct{}{}
	}
	type start struct {
		runner     *WorkspaceRunner
		ctx        context.Context
		cancel     context.CancelFunc
		generation int64
	}
	var starts []start
	var stops []context.CancelFunc
	d.workspaceRunnerMu.Lock()
	if d.workspaceRunners == nil {
		d.workspaceRunners = make(map[string]*WorkspaceRunner)
	}
	if d.workspaceRunnerCancels == nil {
		d.workspaceRunnerCancels = make(map[string]context.CancelFunc)
	}
	if d.workspaceRunnerRecords == nil {
		d.workspaceRunnerRecords = make(map[string]*computer.RunnerRecord)
	}
	if d.workspaceRunnerChildren == nil {
		d.workspaceRunnerChildren = make(map[string]computer.BindingChild)
	}
	now := d.runnerNow()
	for workspaceID, cancel := range d.workspaceRunnerCancels {
		if _, ok := desired[workspaceID]; !ok {
			// Desired-set removal is a graceful stop. Unlinked is only for a
			// child that vanished without Wait; using it here would degrade
			// the Binding and refuse a later re-add.
			d.recordForWorkspaceRunner(workspaceID).ObserveExit(now, computer.RunnerExitGraceful)
			d.stopStoredBindingChild(workspaceID)
			delete(d.workspaceRunnerCancels, workspaceID)
			delete(d.workspaceRunners, workspaceID)
			stops = append(stops, cancel)
		}
	}
	for workspaceID := range desired {
		if _, running := d.workspaceRunnerCancels[workspaceID]; running {
			continue
		}
		record := d.recordForWorkspaceRunner(workspaceID)
		if !record.CanSpawn(true, now) {
			continue
		}
		runner := d.workspaceRunners[workspaceID]
		if runner == nil {
			var err error
			runner, err = d.newWorkspaceRunner(workspaceID)
			if err != nil {
				if d.logger != nil {
					d.logger.Warn("Workspace Runner construction failed", "workspace_id", workspaceID, "reason", "invalid_runner_configuration", "error", err)
				}
				continue
			}
			d.workspaceRunners[workspaceID] = runner
		}
		child, cancel := context.WithCancel(parent)
		d.workspaceRunnerCancels[workspaceID] = cancel
		record.ObserveSpawn()
		starts = append(starts, start{runner: runner, ctx: child, cancel: cancel, generation: record.Generation()})
	}
	d.workspaceRunnerMu.Unlock()
	for _, cancel := range stops {
		cancel()
	}
	for _, next := range starts {
		d.superviseWorkspaceRunner(next.runner, next.ctx, next.cancel, next.generation)
	}
}

func (d *Daemon) runnerNow() time.Time {
	if d != nil && d.workspaceRunnerNow != nil {
		return d.workspaceRunnerNow()
	}
	return time.Now()
}

func (d *Daemon) recordForWorkspaceRunner(workspaceID string) *computer.RunnerRecord {
	rec := d.workspaceRunnerRecords[workspaceID]
	if rec == nil {
		rec = &computer.RunnerRecord{Lifecycle: computer.RunnerLifecycleStopped}
		d.workspaceRunnerRecords[workspaceID] = rec
	}
	return rec
}

func (d *Daemon) startBindingChild(workspaceID string) (computer.BindingChild, error) {
	if d != nil && d.workspaceRunnerSpawn != nil {
		return d.workspaceRunnerSpawn(workspaceID)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return computer.StartBindingRunner(exe, workspaceID)
}

func (d *Daemon) stopStoredBindingChild(workspaceID string) {
	if d.workspaceRunnerChildren == nil {
		return
	}
	if child := d.workspaceRunnerChildren[workspaceID]; child != nil {
		_ = child.Stop()
		delete(d.workspaceRunnerChildren, workspaceID)
	}
}

func (d *Daemon) superviseWorkspaceRunner(runner *WorkspaceRunner, ctx context.Context, cancel context.CancelFunc, generation int64) {
	workspaceID := runner.WorkspaceID()
	useProcess := d.workspaceRunnerRun == nil || d.workspaceRunnerSpawn != nil
	var child computer.BindingChild
	if useProcess {
		spawned, err := d.startBindingChild(workspaceID)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("Workspace Runner child spawn failed", "workspace_id", workspaceID, "error", err)
			}
			d.observeWorkspaceRunnerExit(workspaceID, generation, nil, computer.RunnerExitCrash)
			return
		}
		d.workspaceRunnerMu.Lock()
		d.workspaceRunnerChildren[workspaceID] = spawned
		d.workspaceRunnerMu.Unlock()
		child = spawned
	}
	go func() {
		var runCrashed atomic.Bool
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			defer func() {
				if recover() != nil {
					runCrashed.Store(true)
				}
			}()
			if d.workspaceRunnerRun != nil {
				d.workspaceRunnerRun(runner, ctx)
			} else if runner != nil {
				runner.Run(ctx)
			}
			if ctx.Err() == nil {
				runCrashed.Store(true)
			}
		}()
		class := computer.RunnerExitGraceful
		if child != nil {
			waitDone := make(chan computer.RunnerExitClass, 1)
			go func() {
				waitDone <- child.Wait()
			}()
			select {
			case waitClass := <-waitDone:
				if ctx.Err() == nil {
					class = waitClass
				}
				if cancel != nil {
					cancel()
				}
				<-runDone
			case <-runDone:
				if ctx.Err() == nil || runCrashed.Load() {
					class = computer.RunnerExitCrash
				}
				_ = child.Stop()
				<-waitDone
			}
		} else {
			<-runDone
		}
		if runCrashed.Load() {
			class = computer.RunnerExitCrash
		}
		d.observeWorkspaceRunnerExit(workspaceID, generation, child, class)
	}()
}

func (d *Daemon) observeWorkspaceRunnerExit(workspaceID string, generation int64, child computer.BindingChild, class computer.RunnerExitClass) {
	if d == nil || workspaceID == "" {
		return
	}
	d.workspaceRunnerMu.Lock()
	defer d.workspaceRunnerMu.Unlock()
	rec := d.workspaceRunnerRecords[workspaceID]
	// Raft Computer rejects stale process work as inactive_process_generation
	// when current !== process. A previous supervise must not crash, degrade,
	// or delete a later spawn — including observe(nil) after a test-only Run.
	if rec == nil || !rec.HasChild() || rec.Generation() != generation {
		return
	}
	if child != nil {
		if current := d.workspaceRunnerChildren[workspaceID]; current != nil && current != child {
			return
		}
	}
	rec.ObserveExit(d.runnerNow(), class)
	delete(d.workspaceRunnerCancels, workspaceID)
	delete(d.workspaceRunnerChildren, workspaceID)
	delete(d.workspaceRunners, workspaceID)
}

func (d *Daemon) stopWorkspaceRunners() {
	d.workspaceRunnerMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(d.workspaceRunnerCancels))
	now := d.runnerNow()
	for workspaceID, cancel := range d.workspaceRunnerCancels {
		cancels = append(cancels, cancel)
		delete(d.workspaceRunnerCancels, workspaceID)
		d.stopStoredBindingChild(workspaceID)
		if rec := d.workspaceRunnerRecords[workspaceID]; rec != nil && rec.HasChild() {
			rec.ObserveExit(now, computer.RunnerExitGraceful)
		}
	}
	d.workspaceRunnerMu.Unlock()
	for _, cancel := range cancels {
		cancel()
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

func (runner *WorkspaceRunner) serveConnection(connection *workspaceRunnerConnection, conn *websocket.Conn) error {
	workspaceID := connection.workspaceID
	writeFrame := func(eventType string, payload any) error {
		return runner.sendOnConnection(connection, eventType, payload)
	}
	failConnection := func(err error) {
		if err == nil {
			return
		}
		if runner.logger != nil {
			runner.logger.Debug("workspace Runner delivery writer failed", "workspace_id", workspaceID, "error", err)
		}
		connection.Close()
	}
	connection.deliveries = newWorkspaceRunnerDeliveryDispatcher(connection.ctx, func(deliveryCtx context.Context, delivery protocol.AgentDeliverPayload) {
		if err := runner.handleMessageDelivery(deliveryCtx, delivery, writeFrame); err != nil {
			failConnection(err)
		}
	})
	producer := runner.activity
	if producer == nil || runner.processes == nil {
		return errors.New("workspace Runner lifecycle owners are unavailable")
	}
	if runner.mixedRunActivityReplay != nil {
		runner.mixedRunActivityReplay(writeFrame)
	}
	transportGeneration, reconnectFrames := producer.AttachTransport(func(activity protocol.AgentActivityPayload) {
		if err := writeFrame(protocol.EventAgentActivity, activity); err != nil && runner.logger != nil {
			runner.logger.Debug("workspace runner Activity publish failed", "workspace_id", workspaceID, "error", err)
		}
	})
	defer producer.DetachTransport(transportGeneration)
	activityTickerDone := make(chan struct{})
	defer close(activityTickerDone)
	go func() {
		ticker := time.NewTicker(agentActivityHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-activityTickerDone:
				return
			case <-ticker.C:
				producer.Tick()
			}
		}
	}()
	for _, frame := range reconnectFrames {
		if err := writeFrame(frame.EventType, frame.Payload); err != nil {
			return err
		}
	}
	attachmentRuntimeSet := runner.attachmentRuntimeSet()
	attachmentReplay, err := runner.attachmentReplayRequest(attachmentRuntimeSet)
	if err != nil {
		return err
	}
	if err := writeFrame(protocol.EventAgentAttachmentReplayReq, attachmentReplay); err != nil {
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
			if err := writeFrame(protocol.EventWorkspaceRunnerPong, protocol.WorkspaceRunnerPongPayload{PingID: ping.PingID}); err != nil {
				return err
			}
		case protocol.EventDaemonAgentStart:
			var start protocol.WorkspaceRunnerAgentStartPayload
			if json.Unmarshal(message.Payload, &start) != nil || start.Validate() != nil || !connection.deliveries.Pause(start.AgentID, start.LaunchID) {
				continue
			}
			ack, err := runner.registerManagedAgentStart(start)
			if err != nil {
				connection.deliveries.RejectStart(start.AgentID, start.LaunchID)
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner start rejected", "workspace_id", workspaceID, "agent_id", start.AgentID, "runtime_id", start.RuntimeID, "launch_id", start.LaunchID, "reason", "start_rejected", "error", err)
				}
				failConnection(err)
				continue
			}
			if err := writeFrame(protocol.EventAgentStartAck, ack); err != nil {
				return err
			}
			connection.deliveries.Resume(start.AgentID, start.LaunchID)
			go func() {
				// Provider startup belongs to the Runner, not the socket that
				// delivered agent:start. A Machine Upgrade successor can drop
				// that control connection while Codex is still booting.
				startCtx := runner.life
				if startCtx == nil {
					startCtx = context.Background()
				}
				status, session, err := runner.completeManagedAgentStart(startCtx, start, ack)
				if err != nil {
					if runner.logger != nil {
						runner.logger.Warn("Workspace Runner provider start failed", runner.managedStartLogAttrs(start, ack.QueueState, "provider_start_failed", "failed", err)...)
					}
					if status.AgentID != "" {
						if writeErr := runner.sendOnCurrentConnection(protocol.EventAgentStatus, status); writeErr != nil && runner.logger != nil {
							runner.logger.Debug("workspace Runner start-failure status unpublished", "workspace_id", workspaceID, "agent_id", start.AgentID, "error", writeErr)
						}
					}
					return
				}
				if err := runner.sendOnCurrentConnection(protocol.EventAgentStatus, status); err != nil {
					failConnection(err)
					return
				}
				runner.publishManagedAgentStartActivity(start.AgentID, start.RuntimeID)
				if session.ProviderSessionID != "" {
					if err := runner.sendOnCurrentConnection(protocol.EventAgentSession, session); err != nil {
						failConnection(err)
						return
					}
				}
			}()
		case protocol.EventDaemonAgentStop:
			var stop protocol.WorkspaceRunnerAgentStopPayload
			if json.Unmarshal(message.Payload, &stop) != nil || stop.Validate() != nil {
				continue
			}
			status, err := runner.stopManagedAgent(stop)
			if err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner stop rejected", "workspace_id", workspaceID, "agent_id", stop.AgentID, "launch_id", stop.LaunchID, "reason", "stop_rejected", "error", err)
				}
				failConnection(err)
				continue
			}
			if err := writeFrame(protocol.EventAgentStatus, status); err != nil {
				return err
			}
		case protocol.EventAgentAttach:
			var attach protocol.WorkspaceRunnerAgentAttachPayload
			if json.Unmarshal(message.Payload, &attach) != nil {
				continue
			}
			receipt, err := runner.applyAttachmentAttach(attach)
			if err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner Attachment attach rejected", "workspace_id", workspaceID, "agent_id", attach.AgentID, "runtime_id", attach.RuntimeID, "reason", "attach_rejected", "error", err)
				}
				continue
			}
			if err := writeFrame(protocol.EventAgentAttached, receipt); err != nil {
				return err
			}
			if runner.requestReminderSnapshot != nil {
				runner.requestReminderSnapshot(attach.AgentID)
			}
		case protocol.EventAgentDetach:
			var detach protocol.WorkspaceRunnerAgentDetachPayload
			if json.Unmarshal(message.Payload, &detach) != nil {
				continue
			}
			receipt, err := runner.applyAttachmentDetach(detach)
			if err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner Attachment detach rejected", "workspace_id", workspaceID, "agent_id", detach.AgentID, "runtime_id", detach.RuntimeID, "reason", "detach_rejected", "error", err)
				}
				continue
			}
			if err := writeFrame(protocol.EventAgentDetached, receipt); err != nil {
				return err
			}
			if runner.removeDetachedReminderAgent != nil {
				if err := runner.removeDetachedReminderAgent(detach.AgentID); err != nil {
					return err
				}
			}
		case protocol.EventAgentAttachmentReplayEnd:
			var end protocol.WorkspaceRunnerAttachmentReplayEnd
			if json.Unmarshal(message.Payload, &end) != nil {
				continue
			}
			ack, err := runner.completeAttachmentReplay(attachmentRuntimeSet, end)
			if err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner Attachment replay rejected", "workspace_id", workspaceID, "reason", "invalid_replay_end", "error", err)
				}
				continue
			}
			if err := writeFrame(protocol.EventAgentAttachmentReplayAck, ack); err != nil {
				return err
			}
		case protocol.EventMixedRunActivityAck:
			var activityAck protocol.MixedRunActivityTransitionAckPayload
			if json.Unmarshal(message.Payload, &activityAck) != nil || activityAck.Validate() != nil {
				continue
			}
			if runner.mixedRunActivityAck != nil {
				if err := runner.mixedRunActivityAck(activityAck); err != nil && runner.logger != nil {
					runner.logger.Warn("persist mixed-run activity acknowledgement failed", "error", err, "workspace_id", workspaceID, "run_id", activityAck.RunID, "transition_id", activityAck.TransitionID)
				}
			}
		case protocol.EventAgentDeliver:
			var transient protocol.AgentTransientDeliverPayload
			if json.Unmarshal(message.Payload, &transient) == nil && transient.Kind != "" {
				if transient.Kind != protocol.AgentTransientDeliverKindReminder || !transient.Transient || transient.Reminder.WorkspaceID != workspaceID {
					if runner.logger != nil {
						runner.logger.Warn("transient Agent delivery rejected", "workspace_id", workspaceID, "kind", transient.Kind, "reason_code", "invalid_transient_input")
					}
					continue
				}
				if runner.handleReminderInput != nil {
					runner.handleReminderInput(connection.ctx, transient.Reminder)
				}
				continue
			}
			var delivery protocol.AgentDeliverPayload
			if json.Unmarshal(message.Payload, &delivery) != nil || delivery.AgentID == "" || delivery.Target == "" || delivery.Seq <= 0 || delivery.DeliveryID == "" || delivery.Message.ID == "" || delivery.Message.Target != delivery.Target || delivery.Message.Seq != delivery.Seq {
				continue
			}
			if !connection.deliveries.Enqueue(delivery) && runner.logger != nil {
				runner.logger.Warn("Workspace Runner Agent delivery was not queued", "workspace_id", workspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID, "seq", delivery.Seq, "reason", "connection_delivery_dispatcher_unavailable")
			}
		case protocol.EventAgentActivityProbe:
			var probe protocol.AgentActivityProbePayload
			if json.Unmarshal(message.Payload, &probe) != nil || probe.Validate() != nil {
				continue
			}
			activity, err := producer.Probe(probe)
			if err != nil {
				continue
			}
			if err := writeFrame(protocol.EventAgentActivity, activity); err != nil {
				return err
			}
		}
	}
}

func writeWorkspaceRunnerFrame(conn *websocket.Conn, eventType string, payload any) error {
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(workspaceRunnerWriteTimeout)); err != nil {
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
