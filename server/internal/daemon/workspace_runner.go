package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
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
	d.reconcileWorkspaceRunners(ctx)
	for {
		select {
		case <-ctx.Done():
			d.stopWorkspaceRunners()
			return
		case <-changes:
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
		runner *WorkspaceRunner
		ctx    context.Context
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
	for workspaceID, cancel := range d.workspaceRunnerCancels {
		if _, ok := desired[workspaceID]; !ok {
			delete(d.workspaceRunnerCancels, workspaceID)
			delete(d.workspaceRunners, workspaceID)
			stops = append(stops, cancel)
		}
	}
	for workspaceID := range desired {
		if _, running := d.workspaceRunnerCancels[workspaceID]; running {
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
		starts = append(starts, start{runner: runner, ctx: child})
	}
	d.workspaceRunnerMu.Unlock()
	for _, cancel := range stops {
		cancel()
	}
	for _, next := range starts {
		if d.workspaceRunnerRun != nil {
			d.workspaceRunnerRun(next.runner, next.ctx)
			continue
		}
		runner, child := next.runner, next.ctx
		go runner.Run(child)
	}
}

func (d *Daemon) stopWorkspaceRunners() {
	d.workspaceRunnerMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(d.workspaceRunnerCancels))
	for workspaceID, cancel := range d.workspaceRunnerCancels {
		cancels = append(cancels, cancel)
		delete(d.workspaceRunnerCancels, workspaceID)
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
	attachmentReplayComplete := false
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
			if json.Unmarshal(message.Payload, &start) != nil {
				continue
			}
			ack, status, session, err := runner.startManagedAgent(start)
			if err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner start rejected", "workspace_id", workspaceID, "agent_id", start.AgentID, "runtime_id", start.RuntimeID, "reason", "start_rejected", "error", err)
				}
				continue
			}
			if err := writeFrame(protocol.EventAgentStartAck, ack); err != nil {
				return err
			}
			if err := writeFrame(protocol.EventAgentStatus, status); err != nil {
				return err
			}
			if err := writeFrame(protocol.EventAgentSession, session); err != nil {
				return err
			}
		case protocol.EventDaemonAgentStop:
			var stop protocol.WorkspaceRunnerAgentStopPayload
			if json.Unmarshal(message.Payload, &stop) != nil || stop.Validate() != nil {
				continue
			}
			launch, found := runner.processes.Snapshot(stop.AgentID)
			if !found || launch.LaunchID != stop.LaunchID {
				continue
			}
			if err := runner.processes.Stop(agentProcessCallback{AgentID: stop.AgentID, LaunchID: stop.LaunchID}); err != nil {
				continue
			}
			producer.RemoveManaged(stop.AgentID, stop.LaunchID)
			if err := writeFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{AgentID: stop.AgentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil {
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
			if attachmentReplayComplete {
				runner.beginMessageRecoveryForAll(func(request protocol.AgentRecoveryRequest) error {
					return writeFrame(protocol.EventAgentRecoveryRequest, request)
				})
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
			attachmentReplayComplete = true
			runner.beginMessageRecoveryForAll(func(request protocol.AgentRecoveryRequest) error {
				return writeFrame(protocol.EventAgentRecoveryRequest, request)
			})
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
			connection.deliveries.Enqueue(delivery)
		case protocol.EventAgentRecoveryPage:
			var page protocol.AgentRecoveryPage
			if json.Unmarshal(message.Payload, &page) != nil {
				continue
			}
			if err := runner.mergeMessageRecoveryPage(page, func(request protocol.AgentRecoveryRequest) error {
				return writeFrame(protocol.EventAgentRecoveryRequest, request)
			}); err != nil && runner.logger != nil {
				runner.logger.Warn("workspace Runner Agent Message recovery failed", "error", err, "workspace_id", workspaceID, "agent_id", page.AgentID, "recovery_id", page.RecoveryID)
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
