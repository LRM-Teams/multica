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
			runner, err := d.newWorkspaceRunner(workspaceID)
			if err != nil {
				if d.logger != nil {
					d.logger.Warn("Workspace Runner construction failed", "workspace_id", workspaceID, "reason", "invalid_runner_configuration", "error", err)
				}
				continue
			}
			go runner.Run(child)
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

// workspaceAgentProcessManager returns the sole lifecycle owner for one
// Workspace Runner. Managers must not cross Workspace boundaries: their queue,
// launch identities, process-cap accounting, and stale callbacks are local to
// this Runner connection scope.
func (d *Daemon) workspaceAgentProcessManager(workspaceID string) *agentProcessManager {
	d.mu.Lock()
	defer d.mu.Unlock()
	if manager := d.agentProcessManagers[workspaceID]; manager != nil {
		return manager
	}
	manager := newAgentProcessManager(d.cfg.MaxAgentProcesses, time.Now, func(transition agentLifecycleTransition) {
		if d.lifecycleDiagnostics == nil {
			return
		}
		if err := d.lifecycleDiagnostics.Record(transition); err != nil && d.logger != nil {
			// Local diagnostics are intentionally non-blocking for lifecycle.
			d.logger.Debug("agent lifecycle diagnostic write failed", "error", err)
		}
	})
	d.agentProcessManagers[workspaceID] = manager
	return manager
}

func (d *Daemon) workspaceAgentActivityProducer(workspaceID string) *agentActivityProducer {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.agentActivityProducers == nil {
		d.agentActivityProducers = make(map[string]*agentActivityProducer)
	}
	if producer := d.agentActivityProducers[workspaceID]; producer != nil {
		return producer
	}
	producer := newAgentActivityProducer(d.runnerInstanceID, time.Now, nil)
	d.agentActivityProducers[workspaceID] = producer
	return producer
}

func (runner *WorkspaceRunner) serveConnection(connection *workspaceRunnerConnection, conn *websocket.Conn) error {
	d := runner.daemon
	workspaceID := connection.workspaceID
	writeFrame := connection.Write
	failConnection := func(err error) {
		if err == nil {
			return
		}
		if d.logger != nil {
			d.logger.Debug("workspace Runner delivery writer failed", "workspace_id", workspaceID, "error", err)
		}
		connection.Close()
	}
	deliveryDispatcher := newWorkspaceRunnerDeliveryDispatcher(connection.ctx, func(deliveryCtx context.Context, delivery protocol.AgentDeliverPayload) {
		if err := d.handleWorkspaceRunnerDelivery(deliveryCtx, workspaceID, delivery, writeFrame); err != nil {
			failConnection(err)
		}
	})
	messageTransportGeneration := d.attachWorkspaceRunnerMessageTransport(workspaceID, writeFrame)
	defer d.detachWorkspaceRunnerMessageTransport(workspaceID, messageTransportGeneration)
	producer := d.workspaceAgentActivityProducer(workspaceID)
	transportGeneration, reconnectFrames := producer.AttachTransport(func(activity protocol.AgentActivityPayload) {
		if err := writeFrame(protocol.EventAgentActivity, activity); err != nil && d.logger != nil {
			d.logger.Debug("workspace runner Activity publish failed", "workspace_id", workspaceID, "error", err)
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
	d.beginMessageRecoveryWithSend(func(request protocol.AgentRecoveryRequest) error {
		return writeFrame(protocol.EventAgentRecoveryRequest, request)
	})
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
			if json.Unmarshal(message.Payload, &start) != nil || start.Validate() != nil || !d.ownsWorkspaceRunnerRuntime(workspaceID, start.RuntimeID) {
				continue
			}
			ack, err := d.workspaceAgentProcessManager(workspaceID).Start(agentProcessStartRequest{AgentID: start.AgentID, RuntimeID: start.RuntimeID, StartDispatchID: start.StartDispatchID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
			if err != nil {
				continue
			}
			status := protocol.AgentStatusPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive}
			session := protocol.AgentSessionPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID}
			if err := producer.SetManaged(status, session); err != nil {
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
			// A managed launch becomes immediately observable as online. This is
			// an actual Manager fact, not an inferred UI fallback; later provider
			// observations replace it with working or thinking snapshots.
			if err := producer.Publish(protocol.AgentActivitySnapshot{
				AgentID:          ack.AgentID,
				LaunchID:         ack.LaunchID,
				DaemonInstanceID: d.runnerInstanceID,
				ActivityKind:     protocol.ActivityKindOnline,
			}, nil); err != nil {
				return err
			}
		case protocol.EventDaemonAgentStop:
			var stop protocol.WorkspaceRunnerAgentStopPayload
			if json.Unmarshal(message.Payload, &stop) != nil || stop.Validate() != nil {
				continue
			}
			if err := d.workspaceAgentProcessManager(workspaceID).Stop(agentProcessCallback{AgentID: stop.AgentID, LaunchID: stop.LaunchID}); err != nil {
				continue
			}
			if entry, err := activityNarrativeEntry(protocol.ActivityKindOffline, "stopped", "Stopped"); err == nil {
				if err := producer.PublishForManagedAgent(stop.AgentID, d.runnerInstanceID, protocol.ActivityKindOffline, "stopped", []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
					d.logger.Debug("workspace Runner stopped Activity publish deferred", "error", err, "agent_id", stop.AgentID)
				}
			}
			producer.RemoveManaged(stop.AgentID, stop.LaunchID)
			if err := writeFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{AgentID: stop.AgentID, LaunchID: stop.LaunchID, Status: protocol.AgentStatusInactive}); err != nil {
				return err
			}
		case protocol.EventAgentDeliver:
			var delivery protocol.AgentDeliverPayload
			if json.Unmarshal(message.Payload, &delivery) != nil || delivery.AgentID == "" || delivery.Target == "" || delivery.Seq <= 0 || delivery.DeliveryID == "" || delivery.Message.ID == "" || delivery.Message.Target != delivery.Target || delivery.Message.Seq != delivery.Seq {
				continue
			}
			deliveryDispatcher.Enqueue(delivery)
		case protocol.EventAgentRecoveryPage:
			var page protocol.AgentRecoveryPage
			if json.Unmarshal(message.Payload, &page) != nil {
				continue
			}
			if err := d.handleMessageRecoveryPageWithSend(context.Background(), page, func(request protocol.AgentRecoveryRequest) error {
				return writeFrame(protocol.EventAgentRecoveryRequest, request)
			}); err != nil && d.logger != nil {
				d.logger.Warn("workspace Runner agent Message recovery failed", "error", err, "workspace_id", workspaceID, "agent_id", page.AgentID, "recovery_id", page.RecoveryID)
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

// handleWorkspaceRunnerDelivery accepts or deduplicates a Delivery, attempts
// its transport ACK, and only then performs the best-effort Runtime handoff.
// The sole returned error is an ACK writer failure, which requires the caller
// to tear down the connection so canonical redelivery can retry safely.
func (d *Daemon) handleWorkspaceRunnerDelivery(
	ctx context.Context,
	workspaceID string,
	delivery protocol.AgentDeliverPayload,
	writeFrame func(string, any) error,
) error {
	d.messageCoordinatorMu.RLock()
	runtimeID := d.messageRuntimeIDs[delivery.AgentID]
	d.messageCoordinatorMu.RUnlock()
	d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
		workspaceID, runtimeID, delivery, "runner_received", "accepted", "",
	))
	ack, err := d.acceptIdleAgentDelivery(ctx, workspaceID, delivery)
	// Restart-time delivery may recreate the coordinator and discover its
	// durable runtime during acceptance. Re-read routing identity so every
	// later checkpoint joins the same runtime even when runner_received could
	// only identify the Workspace and Agent.
	d.messageCoordinatorMu.RLock()
	runtimeID = d.messageRuntimeIDs[delivery.AgentID]
	d.messageCoordinatorMu.RUnlock()
	if err != nil {
		d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
			workspaceID, runtimeID, delivery, "coordinator_accepted", "rejected", canonicalMessageFailureReason(err),
		))
		if d.logger != nil {
			d.logger.Warn("workspace Runner agent delivery not acknowledged", "error", err, "workspace_id", workspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
		workspaceID, runtimeID, delivery, "ack_attempted", "attempted", "",
	))
	if err := writeFrame(protocol.EventAgentDeliverAck, ack); err != nil {
		d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
			workspaceID, runtimeID, delivery, "ack_sent", "failed", "runner_connection_write_failed",
		))
		return err
	}
	d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
		workspaceID, runtimeID, delivery, "ack_sent", "accepted", "",
	))
	if err := d.ensureResidentMessageRuntime(ctx, delivery.AgentID, runtimeID); err != nil {
		d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
			workspaceID, runtimeID, delivery, "runtime_handoff_attempted", "failed", canonicalMessageFailureReason(err),
		))
		if d.logger != nil {
			d.logger.Warn("workspace Runner resident Message runtime unavailable after delivery acknowledgement", "error", err, "workspace_id", workspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	if err := d.flushIdleAgentDelivery(ctx, delivery.AgentID); err != nil {
		outcome := "failed"
		if errors.Is(err, ErrCanonicalAgentRuntimeBusy) || strings.Contains(err.Error(), "freshness is unknown") {
			outcome = "deferred"
		}
		d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
			workspaceID, runtimeID, delivery, "context_boundary_persisted", outcome, canonicalMessageFailureReason(err),
		))
		if d.logger != nil {
			d.logger.Warn("workspace Runner idle agent Message handoff failed after delivery acknowledgement", "error", err, "workspace_id", workspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	d.recordRunnerDiagnostic(workspaceID, d.canonicalMessageDiagnosticEvent(
		workspaceID, runtimeID, delivery, "context_boundary_persisted", "accepted", "",
	))
	return nil
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
