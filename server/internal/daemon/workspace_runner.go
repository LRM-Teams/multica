package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const workspaceRunnerWriteTimeout = 10 * time.Second

func (runner *WorkspaceRunner) serveConnection(connection *DaemonConnection, conn *websocket.Conn) error {
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
	transportGeneration, _ := producer.AttachTransport(func(activity protocol.AgentActivityPayload) {
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
	var controlStarted bool
	var stopControl context.CancelFunc
	var controlDone chan struct{}
	startControl := func() {
		if controlStarted {
			return
		}
		controlStarted = true
		controlCtx, cancelControl := context.WithCancel(connection.ctx)
		stopControl = cancelControl
		controlDone = make(chan struct{})
		go func() {
			defer close(controlDone)
			runner.runControlPlaneHeartbeats(controlCtx, connection)
		}()
	}
	defer func() {
		if controlStarted {
			stopControl()
			<-controlDone
		}
	}()
	startControl()
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
		case protocol.EventComputerWorkDigest:
			var command protocol.ComputerWorkDigestPayload
			if json.Unmarshal(message.Payload, &command) != nil || command.Validate() != nil {
				continue
			}
			done := protocol.ComputerWorkDigestDonePayload{RequestID: command.RequestID}
			if runner.handleComputerWorkDigest == nil {
				done.Error = "work journal host unavailable"
			} else if digest, err := runner.handleComputerWorkDigest(connection.ctx, command); err != nil {
				done.Error = err.Error()
			} else {
				copyDigest := digest
				done.OK = true
				done.Digest = &copyDigest
			}
			if err := writeFrame(protocol.EventComputerWorkDigestDone, done); err != nil {
				return err
			}
		case protocol.EventComputerWorkJournal:
			var command protocol.ComputerWorkJournalPayload
			if json.Unmarshal(message.Payload, &command) != nil || command.Validate() != nil {
				continue
			}
			done := protocol.ComputerWorkJournalDonePayload{RequestID: command.RequestID, Enabled: command.Enabled}
			if runner.handleComputerWorkJournal == nil {
				done.Error = "work journal host unavailable"
			} else if enabled, err := runner.handleComputerWorkJournal(connection.ctx, command); err != nil {
				done.Error = err.Error()
			} else {
				done.OK = true
				done.Enabled = enabled
			}
			if err := writeFrame(protocol.EventComputerWorkJournalDone, done); err != nil {
				return err
			}
		case protocol.EventComputerUpgrade, protocol.EventComputerRestart:
			var command protocol.ComputerUpgradePayload
			if message.Type == protocol.EventComputerRestart {
				var restart protocol.ComputerRestartPayload
				if json.Unmarshal(message.Payload, &restart) != nil || restart.Validate() != nil {
					continue
				}
				command = protocol.ComputerUpgradePayload{RequestID: restart.Operation()}
			} else if json.Unmarshal(message.Payload, &command) != nil || command.Validate() != nil {
				continue
			}
			if runner.handleComputerControl == nil {
				if runner.logger != nil {
					runner.logger.Info("ignoring Computer control; Host callback is unavailable", "workspace_id", workspaceID, "action", message.Type)
				}
				continue
			}
			if err := runner.handleComputerControl(connection.ctx, message.Type, command); err != nil {
				if runner.logger != nil {
					runner.logger.Warn("forward Computer control to Host failed", "workspace_id", workspaceID, "action", message.Type, "request_id", command.RequestID, "error", err)
				}
				if message.Type == protocol.EventComputerUpgrade && errors.Is(err, computer.ErrComputerControlBusy) {
					_ = writeFrame(protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{
						RequestID: command.RequestID, OK: false, Error: "control_busy",
					})
				}
			}
		case protocol.EventDaemonHeartbeatAck:
			var ack HeartbeatResponse
			if json.Unmarshal(message.Payload, &ack) != nil || ack.RuntimeID == "" || !runner.ownsRuntime(ack.RuntimeID) {
				continue
			}
			if runner.controlHeartbeatAck != nil {
				lifetime := runner.life
				if lifetime == nil {
					lifetime = context.Background()
				}
				runner.controlHeartbeatAck(lifetime, &ack)
			}
		case protocol.EventDaemonAgentStart:
			var start protocol.WorkspaceRunnerAgentStartPayload
			if json.Unmarshal(message.Payload, &start) != nil || start.Validate() != nil || !connection.deliveries.Pause(start.AgentID, start.LaunchID) {
				continue
			}
			ack, replayed, releaseStartupPublication, _, startupPublished, err := runner.acceptManagedAgentStart(start, failConnection)
			if err != nil {
				connection.deliveries.RejectStart(start.AgentID, start.LaunchID)
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner start rejected", "workspace_id", workspaceID, "agent_id", start.AgentID, "runtime_id", start.RuntimeID, "launch_id", start.LaunchID, "reason", "start_rejected", "error", err)
				}
				failConnection(err)
				continue
			}
			writeErr := writeFrame(protocol.EventAgentStartAck, ack)
			if releaseStartupPublication != nil {
				// Provider startup is already owned before the ACK write. Publication
				// waits only to preserve ACK-before-status ordering; a failed write
				// still releases the owner so reconnect/stop cannot strand Starting.
				releaseStartupPublication()
			}
			if writeErr != nil {
				return writeErr
			}
			go func() {
				published := startupPublished != nil && <-startupPublished
				if published && replayed {
					published = runner.replayManagedAgentStartPublication(start, failConnection)
				}
				if published {
					connection.deliveries.Resume(start.AgentID, start.LaunchID)
				} else {
					connection.deliveries.RejectStart(start.AgentID, start.LaunchID)
				}
			}()
		case protocol.EventDaemonAgentStop:
			var stop protocol.WorkspaceRunnerAgentStopPayload
			if json.Unmarshal(message.Payload, &stop) != nil || stop.Validate() != nil {
				continue
			}
			if err := runner.stopManagedAgent(connection.ctx, stop, func() {
				connection.deliveries.FenceStop(stop.AgentID, stop.LaunchID)
			}, writeFrame); err != nil {
				if runner.logger != nil {
					runner.logger.Warn("Workspace Runner stop rejected", "workspace_id", workspaceID, "agent_id", stop.AgentID, "launch_id", stop.LaunchID, "reason", "stop_rejected", "error", err)
				}
				failConnection(err)
				continue
			}
		case protocol.EventDaemonAgentResetWorkspace:
			var reset protocol.WorkspaceRunnerAgentResetWorkspacePayload
			if json.Unmarshal(message.Payload, &reset) != nil || reset.Validate() != nil {
				continue
			}
			result := runner.resetManagedAgentWorkspace(reset)
			if err := writeFrame(protocol.EventAgentResetWorkspaceResult, result); err != nil {
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

func (runner *WorkspaceRunner) ownsRuntime(runtimeID string) bool {
	if runner == nil || runner.runtimeIDs == nil || runtimeID == "" {
		return false
	}
	for _, current := range runner.runtimeIDs() {
		if current == runtimeID {
			return true
		}
	}
	return false
}

func (runner *WorkspaceRunner) runControlPlaneHeartbeats(ctx context.Context, connection *DaemonConnection) {
	if runner == nil || runner.controlHeartbeatPayload == nil {
		return
	}
	interval := runner.controlHeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	var changes <-chan struct{}
	unsubscribe := func() {}
	if runner.controlHeartbeatChanges != nil {
		changes, unsubscribe = runner.controlHeartbeatChanges()
	}
	defer unsubscribe()
	send := func() bool {
		if runner.runtimeIDs == nil {
			return true
		}
		for _, runtimeID := range runner.runtimeIDs() {
			if ctx.Err() != nil {
				return false
			}
			if err := runner.sendOnConnection(connection, protocol.EventDaemonHeartbeat, runner.controlHeartbeatPayload(runtimeID)); err != nil {
				connection.Close()
				return false
			}
		}
		return true
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			if !send() {
				return
			}
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}
