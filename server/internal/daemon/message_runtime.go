package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/memorysignal"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) openMessageCoordinator(key InboxKey, runtimeID string) (*MessageCoordinator, error) {
	key, err := key.normalized()
	if err != nil {
		return nil, err
	}
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return nil, errors.New("Message coordinator Runtime identity is required")
	}
	agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, key.WorkspaceID, key.AgentID)
	if err := ensureMulticaAgentRoot(agentRoot); err != nil {
		return nil, fmt.Errorf("create Agent root for Message coordinator: %w", err)
	}
	var coordinator *MessageCoordinator
	coordinator, err = NewMessageCoordinator(key, agentRoot, func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.deliverIdleMessageBatchWithCoordinator(ctx, key.AgentID, runtimeID, messages, coordinator)
	}, nil)
	if err != nil {
		return nil, err
	}
	store, err := d.agentAppInboxes.Store(key.AgentID)
	if err == nil {
		if err := store.Restore(); err != nil {
			return nil, fmt.Errorf("restore Agent App Inbox: %w", err)
		}
		coordinator.SetInboxStore(store)
	}
	coordinator.ConfigurePendingNotices(func(ctx context.Context, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		return d.canonicalRuntimes.deliverBusyInboxNotice(ctx, key.AgentID, runtimeID, snapshot, commitIfCurrent)
	}, 0, 0)
	coordinator.ConfigureQueueActivity(func(messages []protocol.AgentMessageProjection, delta int) {
		d.reportMixedRunMessageQueueActivity(key.AgentID, runtimeID, messages, delta)
	})
	return coordinator, nil
}

func (d *Daemon) ensureIdleMessageCoordinator(workspaceID, agentID, runtimeID string) (bool, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if d == nil || workspaceID == "" || agentID == "" || runtimeID == "" {
		return false, errors.New("Workspace, Agent, and Runtime ids are required")
	}
	runner, err := d.ensureWorkspaceDaemon(workspaceID)
	if err != nil {
		return false, fmt.Errorf("ensure WorkspaceDaemon %q: %w", workspaceID, err)
	}
	return runner.ensureMessageInbox(agentID, runtimeID)
}

// ensureIdleMessageCoordinatorForDelivery repairs a missing coordinator only
// from the Agent Process Manager's accepted agent:start. Message delivery never
// invents placement and never consults a second ownership registry.
func (d *Daemon) ensureIdleMessageCoordinatorForDelivery(workspaceID, agentID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	if d == nil || workspaceID == "" || agentID == "" {
		return errors.New("workspace and agent ids are required")
	}
	runner := d.currentWorkspaceDaemon(workspaceID)
	if runner == nil {
		return fmt.Errorf("WorkspaceDaemon %q is unavailable", workspaceID)
	}
	return runner.ensureMessageInboxForDelivery(agentID)
}

// restoreResidentAgents rebuilds durable Agent roots after a Computer process
// restart. A durable root alone does not create a WorkspaceDaemon or a Message
// coordinator; the supervised WorkspaceDaemon receives an explicit managed start
// when work exists.

func mixedRunMessageBatchIdentity(messages []protocol.AgentMessageProjection) (string, string, string, bool) {
	if len(messages) == 0 || messages[0].RunID == "" || messages[0].RunAgentID == "" {
		return "", "", "", false
	}
	runID, runAgentID := messages[0].RunID, messages[0].RunAgentID
	identities := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.RunID != runID || message.RunAgentID != runAgentID {
			return "", "", "", false
		}
		identities = append(identities, message.ID+"\x00"+message.Target+"\x00"+strconv.FormatInt(message.Seq, 10))
	}
	sort.Strings(identities)
	sum := sha256.Sum256([]byte(runID + "\x00" + runAgentID + "\x01" + strings.Join(identities, "\x01")))
	return runID, runAgentID, hex.EncodeToString(sum[:]), true
}

func (d *Daemon) deliverIdleMessageBatch(ctx context.Context, agentID, runtimeID string, messages []protocol.AgentMessageProjection) error {
	return d.deliverIdleMessageBatchWithCoordinator(ctx, agentID, runtimeID, messages, nil)
}

func (d *Daemon) deliverIdleMessageBatchWithCoordinator(ctx context.Context, agentID, runtimeID string, messages []protocol.AgentMessageProjection, coordinator *MessageCoordinator) error {
	runID, runAgentID, turnID, mixed := mixedRunMessageBatchIdentity(messages)
	directedRunID, directedTurnID := runID, turnID
	if directedRunID == "" {
		directedRunID = uuid.NewString()
	}
	if directedTurnID == "" {
		directedTurnID = uuid.NewString()
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	runner, _ := d.ensureWorkspaceDaemon(workspaceID)
	coordinatorOK := coordinator != nil
	preparedMessages, memoryTask, err := d.prepareResidentMessageBatch(ctx, agentID, runtimeID, messages)
	if err != nil {
		return err
	}
	graphMemoryChannelID := ""
	for _, message := range preparedMessages {
		if message.GraphMemoryTools {
			if graphMemoryChannelID == "" {
				graphMemoryChannelID = message.ChannelID
			} else if graphMemoryChannelID != message.ChannelID {
				graphMemoryChannelID = ""
				break
			}
		}
	}
	var graphMemoryStatsBefore *agent.RuntimeTokenStats
	if graphMemoryChannelID != "" {
		graphMemoryStatsBefore, _ = d.canonicalRuntimes.runtimeStats(ctx, agentID, runtimeID)
	}
	var canonicalActionTurn canonicalActionTurnToken
	if mixed {
		canonicalActionTurn = d.allocateCanonicalActionTurnToken()
	}
	// Standalone chat writeback prefers resident capture text, but capture is
	// only populated for mixed-run (RunID-bound) Pi sessions. Bubble / FAB
	// turns often have no RunID — accumulate streamed MessageText deltas so
	// the reply still persists to chat_message (otherwise UI stays on 排队中).
	var streamedMu sync.Mutex
	var streamedText strings.Builder
	// Resident turns bypass the issue-task drain loop, so friction is tracked
	// here per delivery batch (friction-gated memory spec).
	var frictionMu sync.Mutex
	frictionTracker := memorysignal.NewFrictionTracker()
	processCallback, managedProcess := d.canonicalRuntimes.managedProcessCallback(agentID, runtimeID)
	err = d.canonicalRuntimes.deliverIdleMessages(ctx, agentID, runtimeID, preparedMessages, nil, func() {
		if bindErr := d.canonicalRuntimes.bindDirectedTurn(agentID, runtimeID, directedRunID, directedTurnID); bindErr != nil && d.logger != nil {
			d.logger.Warn("resident directed turn binding failed", "agent_id", agentID, "runtime_id", runtimeID, "error", bindErr)
		}
		if mixed {
			d.activateCanonicalActionTurn(agentID, canonicalActionTurn)
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:start", protocol.MixedRunActivityActiveTurn, 1)
			// Capture-batch accounting opens with the resident turn and closes
			// only after the trusted upload (or capture-gap) is acknowledged.
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:start", protocol.MixedRunActivityUnfinishedCaptureBatch, 1)
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "runtime_delivery_accepted", "accepted", "")
		runner.broadcastMessageReceivedActivity(agentID, runtimeID, preparedMessages)
	}, func(message agent.Message) {
		if message.Type == agent.MessageText && message.Content != "" {
			streamedMu.Lock()
			streamedText.WriteString(message.Content)
			streamedMu.Unlock()
		}
		frictionMu.Lock()
		switch message.Type {
		case agent.MessageToolUse:
			frictionTracker.ObserveToolUse(message.Tool, frictionToolInputHash(message.Input))
		case agent.MessageError:
			frictionTracker.ObserveError()
		case agent.MessageText, agent.MessageThinking:
			if message.Content != "" {
				frictionTracker.ObserveProgress()
			}
		}
		frictionMu.Unlock()
		d.reportMixedRunToolActivity(agentID, runtimeID, runID, runAgentID, turnID, canonicalActionTurn, message)
		if managedProcess {
			runner.observeResidentMessageRuntimeForProcess(processCallback, runtimeID, message)
		} else {
			runner.observeResidentMessageRuntime(agentID, runtimeID, message)
		}
	}, func(turnErr error, generation uint64, capture *agent.ResidentTurnCapture) {
		timeout, timedOut := asResidentTurnCompletionTimeout(turnErr)
		if timedOut {
			// Publish the old process's terminal fact before replacing its local
			// AgentInstanceID. WorkspaceDaemon transport writes are bounded, so
			// this preserves terminal→Starting/Idle ordering without an unbounded
			// dependency on Activity delivery.
			if managedProcess {
				runner.observeMessageTurnCompletionForProcess(processCallback, runtimeID, turnErr)
			} else {
				runner.observeMessageTurnCompletion(agentID, runtimeID, turnErr)
			}
			if timeout.RestartSafe && managedProcess {
				go runner.restartManagedAgentAfterTurnTimeout(processCallback, runtimeID)
			}
		}
		d.canonicalRuntimes.clearDirectedTurn(agentID, runtimeID, directedRunID, directedTurnID)
		if mixed {
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:end", protocol.MixedRunActivityActiveTurn, -1)
			if d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, turnID, canonicalActionTurn, turnErr, capture) {
				d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:end", protocol.MixedRunActivityUnfinishedCaptureBatch, -1)
			}
		}
		outcome, reasonCode := "completed", ""
		if turnErr != nil {
			outcome, reasonCode = "failed", "provider_turn_failed"
			if _, timedOut := asResidentTurnCompletionTimeout(turnErr); timedOut {
				reasonCode = "provider_turn_timeout"
			}
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "provider_finished", outcome, reasonCode)
		if graphMemoryChannelID != "" {
			checkpointCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			d.autoCheckpointGraphMemoryRun(checkpointCtx, workspaceID, agentID, graphMemoryChannelID, preparedMessages)
			cancel()
		}
		if turnErr != nil && isRetryableResidentProviderFailure(turnErr) && coordinatorOK {
			// The provider can die after native acceptance (notably while writing
			// turn/start to a stale stdin). The coordinator has already advanced
			// its local boundary, so put the exact batch back before replacing the
			// provider. Do not write a failure assistant row for this transport
			// failure; the replacement owns the single user-visible reply.
			go runner.retryQueuedMessageAfterProviderFailure(coordinator, agentID, runtimeID)
			return
		}
		if d.client != nil && strings.TrimSpace(d.client.baseURL) != "" {
			reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if turnErr == nil {
				frictionMu.Lock()
				frictionVector := frictionTracker.Vector()
				frictionMu.Unlock()
				d.reportAgentMemoryWrites(reportCtx, memoryTask, frictionVector)
			}
			if sessionID, ok := standaloneChatSessionIDFromMessages(preparedMessages); ok {
				streamedMu.Lock()
				streamed := streamedText.String()
				streamedMu.Unlock()
				d.writebackStandaloneChatTurn(reportCtx, sessionID, runtimeID, turnErr, capture, streamed)
			}
			if graphMemoryChannelID != "" {
				if after, statsErr := d.canonicalRuntimes.runtimeStats(reportCtx, agentID, runtimeID); statsErr == nil {
					d.reportGraphMemoryAgentUsage(reportCtx, workspaceID, agentID, graphMemoryChannelID, graphMemoryStatsBefore, after)
				}
			}
			cancel()
		}
		// Raft-aligned turn end: never auto-deliver Pending bodies solely because
		// the prior turn finished. If Pending remains, schedule a content-free
		// Notice; body delivery waits for idle Accept→Flush, recovery Flush, or
		// agent `message check`.
		runner.notifyPendingMessagesAfterTurn(agentID)
		if runner != nil && runner.notifyAppInbox != nil {
			_ = runner.notifyAppInbox(context.Background(), agentID, runtimeID)
		}
		if !timedOut {
			d.canonicalRuntimes.publishIfMessageTurnStillIdle(agentID, runtimeID, generation, func() {
				if managedProcess {
					runner.observeMessageTurnCompletionForProcess(processCallback, runtimeID, turnErr)
				} else {
					runner.observeMessageTurnCompletion(agentID, runtimeID, turnErr)
				}
			})
		}
	})
	if err != nil {
		if mixed {
			d.endCanonicalActionTurn(agentID, canonicalActionTurn)
		}
		outcome := "rejected"
		if errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
			outcome = "deferred"
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "runtime_delivery_accepted", outcome, canonicalMessageFailureReason(err))
	}
	if err != nil && !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		// Setup and native-acceptance failures happen before a completion
		// receipt exists, so the onComplete path above cannot publish them.
		// Project the failure explicitly instead of leaving it only in daemon
		// logs while the user waits for an Agent response that cannot arrive.
		if managedProcess {
			runner.observeMessageTurnCompletionForProcess(processCallback, runtimeID, err)
		} else {
			runner.observeMessageTurnCompletion(agentID, runtimeID, err)
		}
		// Same for standalone bubbles: without an assistant row the UI stays on
		// 排队中 forever after provider timeout / accept failure.
		if sessionID, ok := standaloneChatSessionIDFromMessages(messages); ok && d.client != nil && strings.TrimSpace(d.client.baseURL) != "" {
			reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			d.writebackStandaloneChatTurn(reportCtx, sessionID, runtimeID, err, nil, "")
			cancel()
		}
	}
	return err
}

func graphMemoryUsageDelta(before, after *agent.RuntimeTokenStats) (int64, int64) {
	if before == nil || after == nil {
		return 0, 0
	}
	inputTokens, outputTokens := after.InputTokens, after.OutputTokens
	inputTokens -= before.InputTokens
	outputTokens -= before.OutputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	return inputTokens, outputTokens
}

func (d *Daemon) reportGraphMemoryAgentUsage(ctx context.Context, workspaceID, agentID, channelID string, before, after *agent.RuntimeTokenStats) {
	if d == nil || after == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	inputTokens, outputTokens := graphMemoryUsageDelta(before, after)
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	credential, err := d.messageAgentCredential(ctx, workspaceID, agentID)
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("graph memory agent usage credential unavailable", "agent_id", agentID, "channel_id", channelID, "error", err)
		}
		return
	}
	client := cli.NewAPIClient(d.cfg.ServerBaseURL, workspaceID, credential.Token)
	client.AgentID = agentID
	var response map[string]any
	if err := client.PostJSON(ctx, "/api/agent/channels/"+channelID+"/graph-memory-usage", map[string]int64{
		"input_tokens": inputTokens, "output_tokens": outputTokens,
	}, &response); err != nil && d.logger != nil {
		d.logger.Warn("graph memory agent usage report failed", "agent_id", agentID, "channel_id", channelID, "error", err)
	}
}

// reportResidentTurnCapture performs durable server-side accounting before the
// daemon releases the matching unfinished-capture counter. A failed upload is
// deliberately left unfinished; a successful server gap report is the only
// alternate terminal outcome.
func (d *Daemon) reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, activityTurnID string, turnToken canonicalActionTurnToken, turnErr error, capture *agent.ResidentTurnCapture) bool {
	drained := d.endCanonicalActionTurn(agentID, turnToken)
	if d.client == nil || strings.TrimSpace(d.client.baseURL) == "" {
		return false
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	credential, ok := d.activeAgentProxyServerCredential(workspaceID, runtimeID, agentID)
	if !ok {
		if d.logger != nil {
			d.logger.Warn("mixed-run capture credential unavailable", "run_id", runID, "run_agent_id", runAgentID)
		}
		return false
	}
	gapReason := "capture_unavailable"
	if turnErr != nil {
		gapReason = "provider_turn_failed"
	}
	if drained.Overflow {
		gapReason = "canonical_action_overflow"
	}
	if drained.Ambiguous {
		gapReason = "canonical_action_context_ambiguous"
	}
	if turnErr == nil && !drained.Overflow && !drained.Ambiguous && capture != nil && capture.Complete {
		payload := protocol.TurnCaptureUpload{
			AgentID: agentID, RuntimeID: runtimeID, CaptureBatchID: capture.CaptureBatchID,
			Turn: protocol.TurnCaptureTurn{
				TurnID: capture.TurnID, RunAgentID: capture.RunAgentID, PiSessionID: capture.PiSessionID,
				CaptureBoundary: capture.CaptureBoundary, TurnOrdinal: capture.TurnOrdinal, Status: "settled",
				StartedAt: capture.StartedAt.UTC().Format(time.RFC3339Nano), CompletedAt: capture.CompletedAt.UTC().Format(time.RFC3339Nano),
			},
			PayloadHash: capture.PayloadHash,
		}
		for _, call := range capture.ProviderCalls {
			payload.ProviderCalls = append(payload.ProviderCalls, protocol.TurnCaptureProviderCall{
				CallID: call.CallID, CallOrdinal: call.CallOrdinal, Provider: call.Provider, Model: call.Model, APIKind: call.APIKind,
				RawProviderRequest: call.RawProviderRequest, FinalAssistantMessage: call.FinalAssistantMessage,
				Status: call.Status, StopReason: call.StopReason, ResponseComplete: call.ResponseComplete,
				RequestHash: call.RequestHash, ResponseHash: call.ResponseHash,
				StartedAt: call.StartedAt.UTC().Format(time.RFC3339Nano), CompletedAt: call.CompletedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		payload.VisibleActions = resolvedTurnCaptureVisibleActions(capture.ProviderCalls, drained.Associations)
		response, err := d.client.UploadTurnCapture(reportCtx, runID, payload, credential.Token)
		if err == nil && response.Accepted {
			return true
		}
		if d.logger != nil {
			d.logger.Warn("mixed-run capture upload failed", "run_id", runID, "run_agent_id", runAgentID, "error", err)
		}
		gapReason = "capture_upload_rejected"
	}
	gapTurnID, turnOrdinal, boundary := mixedRunCaptureGapIdentity(runAgentID, activityTurnID, capture)
	response, err := d.client.ReportTurnCaptureGap(reportCtx, runID, protocol.TurnCaptureGapReport{
		AgentID: agentID, RuntimeID: runtimeID, RunAgentID: runAgentID, TurnID: gapTurnID, TurnOrdinal: turnOrdinal,
		CaptureBoundary: boundary, Reason: gapReason, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, credential.Token)
	if err == nil && response.Accepted {
		return true
	}
	if d.logger != nil {
		d.logger.Warn("mixed-run capture gap report failed", "run_id", runID, "run_agent_id", runAgentID, "error", err)
	}
	return false
}

// resolvedTurnCaptureVisibleActions binds daemon-observed tool outcomes only
// when Pi's typed final assistant blocks identify exactly one provider call.
// Unresolved and ambiguous tool IDs fail closed.
func resolvedTurnCaptureVisibleActions(calls []agent.ResidentProviderCallCapture, associations []CanonicalActionAssociation) []protocol.TurnCaptureVisibleAction {
	toolProviders := make(map[string][]string)
	for _, call := range calls {
		var message struct {
			Role   string `json:"role"`
			Blocks []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"blocks"`
		}
		if json.Unmarshal(call.FinalAssistantMessage, &message) != nil || message.Role != "assistant" {
			continue
		}
		seen := make(map[string]struct{})
		for _, block := range message.Blocks {
			if block.Type != "toolCall" || strings.TrimSpace(block.ID) == "" {
				continue
			}
			if _, duplicate := seen[block.ID]; duplicate {
				continue
			}
			seen[block.ID] = struct{}{}
			toolProviders[block.ID] = append(toolProviders[block.ID], call.CallID)
		}
	}
	visible := make([]protocol.TurnCaptureVisibleAction, 0, len(associations))
	for index, association := range associations {
		providerIDs := toolProviders[association.ToolCallID]
		if len(providerIDs) != 1 || association.SucceededAt.IsZero() {
			continue
		}
		visible = append(visible, protocol.TurnCaptureVisibleAction{
			Kind: association.Kind, CanonicalID: association.CanonicalID,
			ProducerCallID: providerIDs[0], ActionOrdinal: int64(index + 1),
			SucceededAt: association.SucceededAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return visible
}

func mixedRunCaptureGapIdentity(runAgentID, activityTurnID string, capture *agent.ResidentTurnCapture) (string, int64, string) {
	if capture != nil {
		return capture.TurnID, capture.TurnOrdinal, capture.CaptureBoundary
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(runAgentID+":capture-gap:"+activityTurnID)).String(), 0, ""
}

// reportMixedRunToolActivity tracks inflight tool calls of a mixed-run turn.
// User-facing Activity emission stays with the WorkspaceDaemon; this reports
// only the durable mixed-run transition deltas.
func (d *Daemon) reportMixedRunToolActivity(agentID, runtimeID, runID, runAgentID, turnID string, turnToken canonicalActionTurnToken, message agent.Message) {
	if runID == "" || runAgentID == "" || turnID == "" || strings.TrimSpace(message.CallID) == "" {
		return
	}
	switch message.Type {
	case agent.MessageToolUse:
		d.SetActiveProviderToolContext(ActiveProviderToolContext{
			AgentID: agentID, CallID: message.CallID, ToolCallID: message.CallID, TurnToken: turnToken,
		})
		d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID,
			"turn:"+turnID+":tool:"+message.CallID+":start", protocol.MixedRunActivityInflightTool, 1)
	case agent.MessageToolResult:
		d.clearActiveProviderToolContext(agentID, turnToken, message.CallID)
		d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID,
			"turn:"+turnID+":tool:"+message.CallID+":end", protocol.MixedRunActivityInflightTool, -1)
	}
}

// prepareResidentMessageBatch hydrates the portable memory center once, then
// renders identity and memory independently for every Message. Keeping the
// overlay per item prevents a mixed-user batch from sharing personal memory.
func (d *Daemon) prepareResidentMessageBatch(ctx context.Context, agentID, runtimeID string, messages []protocol.AgentMessageProjection) ([]protocol.AgentMessageProjection, Task, error) {
	d.mu.Lock()
	workspaceID := strings.TrimSpace(d.runtimeIndex[runtimeID].WorkspaceID)
	d.mu.Unlock()
	if workspaceID == "" {
		return nil, Task{}, errors.New("runtime workspace is unavailable for resident Message memory")
	}
	agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, workspaceID, agentID)
	if d.client != nil && strings.TrimSpace(d.client.baseURL) != "" {
		d.hydrateAgentMemoryCenter(ctx, workspaceID, agentID, runtimeID, agentRoot)
	}

	prepared := make([]protocol.AgentMessageProjection, 0, len(messages))
	sessionKey := residentTurnScopeSessionKey(agentID, runtimeID)
	graphRecallMemo := map[string]graphRecallResult{}
	for _, message := range messages {
		messageTask := residentMessageMemoryTask(workspaceID, agentID, runtimeID, []protocol.AgentMessageProjection{message})
		if profile, ok := d.graphProfileForWorkspace(workspaceID); ok {
			messageTask.MemoryType = profile.memoryType
			messageTask.ExploreAgents = profile.exploreAgents
			messageTask.ExploreMaxRounds = profile.exploreMaxRounds
		}
		serverMemories := convertResidentMessageMemoriesForEnv(message.Memories)
		var memories []execenv.MemoryContextForEnv
		if effectiveMemoryType(d.cfg.MemoryType, messageTask.MemoryType) == MemoryTypeGraph {
			// Same merge contract as runTask (spec §8): legacy user/agent
			// retained, graph blob appended, no legacy project/channel/daily.
			// Agent-scope rows stay out of per-message context.
			graphCurrent, graphResearch := d.memoizedGraphExecutionMemories(ctx, messageTask, graphRecallMemo, d.logger)
			combined := mergeGraphModeExecutionMemory(
				agentRoot, messageTask, serverMemories, graphCurrent, graphResearch,
			)
			memories = withoutAgentScopeMemories(combined)
		} else {
			memories, _ = prepareTurnScopeMemory(agentRoot, messageTask, serverMemories)
		}
		if d.turnScopeMemory != nil {
			memories = d.turnScopeMemory.selectForInject(sessionKey, memories, false)
			d.turnScopeMemory.markInjected(sessionKey, memories)
		}
		chatSessionID, _ := standaloneChatSessionID(message.Target)
		message.RuntimeContext = execenv.RenderTurnContext(execenv.TaskContextForEnv{
			MessageDelivery: true,
			AgentID:         agentID,
			AgentRoot:       agentRoot,
			AgentMemories:   memories,
			ChannelID:       message.ChannelID,
			ChatSessionID:   chatSessionID,
			ProjectID:       message.ProjectID,
			InitiatorType:   message.InitiatorType,
			InitiatorID:     message.InitiatorID,
			InitiatorName:   message.InitiatorName,
		})
		if message.GraphMemoryTools {
			if d.memoryExploreV2Negotiated(workspaceID) {
				message.RuntimeContext += graphMemoryAgentToolContextV2(message)
			} else {
				message.RuntimeContext += graphMemoryAgentToolContext(message)
			}
		}
		prepared = append(prepared, message)
	}
	return prepared, residentMessageMemoryTask(workspaceID, agentID, runtimeID, messages), nil
}

func graphMemoryAgentToolContext(message protocol.AgentMessageProjection) string {
	channelID := strings.TrimSpace(message.ChannelID)
	messageID := strings.TrimSpace(message.ID)
	if channelID == "" || messageID == "" {
		return ""
	}
	return fmt.Sprintf(`

## Managed Graph Memory tools
You are the managed Memory Agent for this channel. Run every Graph Memory operation with the multica graph-memory CLI from bash; it authenticates through the daemon credential proxy. Operations: multica graph-memory start, explore, redirect, submit, checkpoint.
  multica graph-memory start --channel %q --query "<the user request>" --idempotency-key "%s-start"
  multica graph-memory explore --channel %q --trajectory <trajectory_id> --node-ids <id1,id2> --idempotency-key "%s-explore"
  multica graph-memory redirect --channel %q --trajectory <trajectory_id> --query "<revised query>" --steering-message <message-id> --idempotency-key "%s-redirect"
  multica graph-memory submit --channel %q --trajectory <trajectory_id> --summary "<cited summary>" --node-ids <cited ids> --idempotency-key "%s-submit"
  multica graph-memory checkpoint --channel %q --trajectory <trajectory_id> --idempotency-key "%s-checkpoint"
The gateway prints its JSON response on stdout. Graph scope, version, run fencing, and trajectory authorization remain server-owned; never invent or override them. Start with the user request as query; use only the returned trajectory and nodes/refs in later calls. Explore is how you view a node: submit rejects citations for nodes this trajectory has not explored, so every id in submit's --node-ids (start's seed nodes included) must first be passed to explore. Submit or checkpoint at most once; a rejected, fenced, quota, or disabled response is terminal for this turn — checkpoint if still available and do not bypass the gateway. The daemon automatically checkpoints an active run when this turn ends without a successful submit.
`, channelID, messageID, channelID, messageID, channelID, messageID, channelID, messageID, channelID, messageID)
}

// graphMemoryAgentToolContextV2 teaches the same five operations under the
// negotiated generation-2 payload contract (plan Task 12 Step 3): the
// canonical Interaction DAG surface with structured MemoryRef objects, plans
// and seeds. It is rendered only when the server echoed the
// memory_explore_v2 capability for this workspace at registration.
func graphMemoryAgentToolContextV2(message protocol.AgentMessageProjection) string {
	channelID := strings.TrimSpace(message.ChannelID)
	messageID := strings.TrimSpace(message.ID)
	if channelID == "" || messageID == "" {
		return ""
	}
	return fmt.Sprintf(`

## Managed Graph Memory tools (protocol generation 2)
You are the managed Memory Agent for this channel. Run every Graph Memory operation with the multica graph-memory CLI from bash; it authenticates through the daemon credential proxy. Operations: multica graph-memory start, explore, redirect, submit, checkpoint.
  multica graph-memory start --channel %q --query "<the user request>" --idempotency-key "%s-start"
  multica graph-memory explore --channel %q --trajectory <trajectory_id> --node-ids <id1,id2> --idempotency-key "%s-explore"
  multica graph-memory redirect --channel %q --trajectory <trajectory_id> --query "<revised query>" --steering-message <message-id> --idempotency-key "%s-redirect"
  multica graph-memory submit --channel %q --trajectory <trajectory_id> --summary "<cited summary>" --node-ids <cited ids> --idempotency-key "%s-submit"
  multica graph-memory checkpoint --channel %q --trajectory <trajectory_id> --idempotency-key "%s-checkpoint"
The gateway prints its JSON response on stdout. The server owns the graph scope, plan, watermarks, run fencing, and trajectory authorization; never invent or override them. Start returns a plan and authorized seeds; explore only refs received from those seeds or earlier exploration. Explore is also how you view a node: submit rejects citations for nodes this trajectory has not explored, so every id in submit's --node-ids (seed refs included) must first be passed to explore. Submit or checkpoint at most once; a rejected, fenced, quota, or disabled response is terminal for this turn — checkpoint if still available and do not bypass the gateway. The daemon automatically checkpoints an active run when this turn ends without a successful submit.
`, channelID, messageID, channelID, messageID, channelID, messageID, channelID, messageID, channelID, messageID)
}

// memoryExploreV2Negotiated reports whether the server echoed the
// memory_explore_v2 capability for this workspace at registration — the
// daemon-side half of the bidirectional negotiation. The server still
// re-checks its own gate on every call; this only selects the prompt.
func (d *Daemon) memoryExploreV2Negotiated(workspaceID string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ws, ok := d.workspaces[strings.TrimSpace(workspaceID)]
	if !ok {
		return false
	}
	for _, capability := range ws.serverCapabilities {
		if capability == protocol.DaemonCapabilityMemoryExploreV2 {
			return true
		}
	}
	return false
}

func residentMessageMemoryTask(workspaceID, agentID, runtimeID string, messages []protocol.AgentMessageProjection) Task {
	task := Task{WorkspaceID: workspaceID, AgentID: agentID, RuntimeID: runtimeID}
	if len(messages) == 1 {
		task.GraphMemoryTools = messages[0].GraphMemoryTools
	}
	var trigger strings.Builder
	var initiatorType, initiatorID, initiatorName string
	for i, message := range messages {
		if i == 0 {
			task.ID = message.ID
			task.ChannelID = message.ChannelID
			task.ChannelKind = message.ChannelKind
			task.ProjectID = message.ProjectID
			initiatorType, initiatorID, initiatorName = message.InitiatorType, message.InitiatorID, message.InitiatorName
		} else if message.InitiatorType != initiatorType || message.InitiatorID != initiatorID {
			initiatorType, initiatorID, initiatorName = "", "", ""
		}
		if trigger.Len() > 0 {
			trigger.WriteByte('\n')
		}
		trigger.WriteString(message.Content)
	}
	task.ChatMessage = trigger.String()
	task.InitiatorType, task.InitiatorID, task.InitiatorName = initiatorType, initiatorID, initiatorName
	// The memory gate identifies group chat by a non-empty chat session. A
	// canonical Message target is the stable surface boundary for this path.
	if strings.EqualFold(task.ChannelKind, "group") && len(messages) > 0 {
		task.ChatSessionID = messages[0].Target
	}
	if sessionID, ok := standaloneChatSessionIDFromMessages(messages); ok {
		task.ChatSessionID = sessionID
	}
	return task
}

func standaloneChatSessionID(target string) (string, bool) {
	const prefix = "chat:"
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(target, prefix))
	return sessionID, sessionID != ""
}

func standaloneChatSessionIDFromMessages(messages []protocol.AgentMessageProjection) (string, bool) {
	for _, message := range messages {
		if sessionID, ok := standaloneChatSessionID(message.Target); ok {
			return sessionID, true
		}
	}
	return "", false
}

func standaloneAssistantTextFromCapture(capture *agent.ResidentTurnCapture) string {
	if capture == nil {
		return ""
	}
	for i := len(capture.ProviderCalls) - 1; i >= 0; i-- {
		if text := standaloneAssistantTextFromJSON(capture.ProviderCalls[i].FinalAssistantMessage); text != "" {
			return text
		}
	}
	return ""
}

// standaloneAssistantReplyText prefers capture final-assistant text (mixed-run
// path). When capture is empty — typical for standalone bubble/FAB turns
// without a RunID — fall back to concatenated MessageText deltas from the turn.
func standaloneAssistantReplyText(capture *agent.ResidentTurnCapture, streamed string) string {
	if reply := standaloneAssistantTextFromCapture(capture); reply != "" {
		return reply
	}
	return strings.TrimSpace(streamed)
}

// standaloneAssistantFailureReply is written to chat_message when a standalone
// bubble turn fails so the UI leaves 排队中 instead of waiting forever.
func standaloneAssistantFailureReply(err error) string {
	detail := "unknown error"
	if err != nil {
		if msg := strings.TrimSpace(err.Error()); msg != "" {
			detail = msg
		}
	}
	return "I could not complete that reply (" + detail + "). Please try again."
}

func isRetryableResidentProviderFailure(err error) bool {
	if err == nil {
		return false
	}
	detail := strings.ToLower(err.Error())
	for _, marker := range []string{"broken pipe", "closed pipe", "use of closed network connection", "process exited", "app-server process exited", "unexpected eof"} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func (d *Daemon) writebackStandaloneChatTurn(ctx context.Context, sessionID, runtimeID string, turnErr error, capture *agent.ResidentTurnCapture, streamed string) {
	if d == nil || d.client == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	reply := ""
	if turnErr != nil {
		reply = standaloneAssistantFailureReply(turnErr)
	} else {
		reply = standaloneAssistantReplyText(capture, streamed)
		if reply == "" {
			if d.logger != nil {
				d.logger.Warn("standalone chat reply missing after successful turn", "session_id", sessionID, "has_capture", capture != nil)
			}
			return
		}
	}
	if err := d.client.ReportStandaloneChatReply(ctx, sessionID, reply, runtimeID); err != nil && d.logger != nil {
		d.logger.Warn("standalone chat reply writeback failed", "session_id", sessionID, "error", err, "failed_turn", turnErr != nil)
	}
}

func standaloneAssistantTextFromJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var payload struct {
		Text   string `json:"text"`
		Blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if text := strings.TrimSpace(payload.Text); text != "" {
		return text
	}
	var parts []string
	for _, block := range payload.Blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func convertResidentMessageMemoriesForEnv(memories []protocol.AgentMessageMemoryProjection) []execenv.MemoryContextForEnv {
	if len(memories) == 0 {
		return nil
	}
	result := make([]execenv.MemoryContextForEnv, 0, len(memories))
	for _, memory := range memories {
		result = append(result, execenv.MemoryContextForEnv{
			Name: memory.Name, Content: memory.Content, Scope: memory.Scope,
			SubjectType: memory.SubjectType, SubjectID: memory.SubjectID,
		})
	}
	return result
}

// CredentialProxy is the machine-local freshness boundary. The first repair
// slice exposes only the internally derived target sequence; it deliberately
// does not accept an Agent-supplied cursor.
type CredentialProxy struct{ daemon *Daemon }

func (d *Daemon) CredentialProxy() *CredentialProxy { return &CredentialProxy{daemon: d} }

func (p *CredentialProxy) SeenUpToSeq(agentID, target string) (int64, error) {
	if p == nil || p.daemon == nil {
		return 0, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return 0, errors.New("Message coordinator is unavailable")
	}
	seq, known, err := runner.messageContextBoundary(agentID, target)
	if err != nil {
		return 0, err
	}
	if !known {
		return 0, errors.New("Message freshness is unknown")
	}
	return seq, nil
}

func (p *CredentialProxy) CheckMessages(agentID string) (MessageCheckResult, error) {
	if p == nil || p.daemon == nil {
		return MessageCheckResult{}, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return MessageCheckResult{}, errors.New("Message coordinator is unavailable")
	}
	messages := runner.agentInboxPendingSnapshot(agentID)
	if len(messages) > messageCheckMaxLimit {
		messages = messages[:messageCheckMaxLimit]
	}
	status := messageCheckStatusComplete
	if len(runner.agentInboxPendingSnapshot(agentID)) > len(messages) {
		status = messageCheckStatusMore
	}
	return MessageCheckResult{
		Messages: messages, HasMore: status == messageCheckStatusMore, Remaining: len(runner.agentInboxPendingSnapshot(agentID)) - len(messages),
		Status: status, Revision: coordinatorRevision(runner, agentID),
	}, nil
}

// PrepareMessageRead validates server-provided canonical bodies and returns
// them without retiring the inbox. Retirement requires an explicit ACK with
// the revision returned by the check/read boundary.
func (p *CredentialProxy) PrepareMessageRead(
	agentID, target string,
	throughSeq int64,
	messages []protocol.AgentMessageProjection,
) ([]protocol.AgentMessageProjection, error) {
	if p == nil || p.daemon == nil {
		return nil, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return nil, errors.New("Message coordinator is unavailable")
	}
	if throughSeq == 0 && len(messages) == 0 {
		return []protocol.AgentMessageProjection{}, nil
	}
	for _, message := range messages {
		if message.Target != target || message.Seq > throughSeq {
			return nil, errors.New("read message does not match inbox target and sequence")
		}
	}
	return runner.projectAgentVisibleMessages(agentID, messages, true, false)
}

func (p *CredentialProxy) messageDraftStore() (*MessageDraftStore, error) {
	if p == nil || p.daemon == nil || p.daemon.messageDraftStore == nil {
		return nil, errors.New("Message Draft store is unavailable")
	}
	return p.daemon.messageDraftStore, nil
}

func (p *CredentialProxy) SaveNormalMessageDraft(workspaceID, agentID string, draft MessageDraft, now time.Time) (MessageDraft, error) {
	store, err := p.messageDraftStore()
	if err != nil {
		return MessageDraft{}, err
	}
	key := DraftKey{WorkspaceID: workspaceID, AgentID: agentID, Target: strings.TrimSpace(draft.Target)}
	saved, err := store.saveAt(key, draft, now)
	if err != nil {
		return MessageDraft{}, err
	}
	return saved, nil
}

func (p *CredentialProxy) LoadMessageDraft(workspaceID, agentID, target string, now time.Time) (MessageDraft, bool, error) {
	store, err := p.messageDraftStore()
	if err != nil {
		return MessageDraft{}, false, err
	}
	draft, found, err := store.Load(DraftKey{WorkspaceID: workspaceID, AgentID: agentID, Target: strings.TrimSpace(target)}, now)
	if err != nil || !found {
		return MessageDraft{}, found, err
	}
	return draft, true, nil
}

func (p *CredentialProxy) UpdateMessageDraftBoundary(workspaceID, agentID, target, idempotencyKey, contextTarget string, seenUpToSeq int64, now time.Time) (MessageDraft, error) {
	store, err := p.messageDraftStore()
	if err != nil {
		return MessageDraft{}, err
	}
	key := DraftKey{WorkspaceID: workspaceID, AgentID: agentID, Target: strings.TrimSpace(target)}
	draft, err := store.updateBoundaryAt(key, idempotencyKey, contextTarget, seenUpToSeq, now)
	if err != nil {
		return MessageDraft{}, err
	}
	return draft, nil
}

func (p *CredentialProxy) RecordMessageDraftHold(workspaceID, agentID, target, idempotencyKey, contextTarget string, seenUpToSeq int64, now time.Time) (MessageDraft, error) {
	store, err := p.messageDraftStore()
	if err != nil {
		return MessageDraft{}, err
	}
	key := DraftKey{WorkspaceID: workspaceID, AgentID: agentID, Target: strings.TrimSpace(target)}
	draft, err := store.recordHoldAt(key, idempotencyKey, contextTarget, seenUpToSeq, now)
	if err != nil {
		return MessageDraft{}, err
	}
	return draft, nil
}

func (p *CredentialProxy) ClearMessageDraft(workspaceID, agentID, target, clientMessageID string) error {
	store, err := p.messageDraftStore()
	if err != nil {
		return err
	}
	return store.Clear(DraftKey{WorkspaceID: workspaceID, AgentID: agentID, Target: strings.TrimSpace(target)}, clientMessageID)
}

func (p *CredentialProxy) MessageSendBoundarySnapshot(agentID, target string) (int64, error) {
	if p == nil || p.daemon == nil {
		return 0, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return 0, errors.New("Message coordinator is unavailable")
	}
	return runner.messageSendBoundarySnapshot(agentID, target)
}

func (p *CredentialProxy) PreflightMessageSend(agentID, target string) (MessageSendFreshness, error) {
	if p == nil || p.daemon == nil {
		return MessageSendFreshness{}, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return MessageSendFreshness{}, errors.New("Message coordinator is unavailable")
	}
	return runner.preflightMessageSend(agentID, target)
}

// autoCheckpointGraphMemoryRun closes an unfinished managed-memory trajectory
// after a resident turn. Checkpoint is lifecycle cleanup, not a model choice:
// the model may submit a final result, while any remaining active run is
// checkpointed through the same agent-authorized gateway route.
func (d *Daemon) autoCheckpointGraphMemoryRun(ctx context.Context, workspaceID, agentID, channelID string, messages []protocol.AgentMessageProjection) {
	if d == nil || strings.TrimSpace(channelID) == "" || d.client == nil || strings.TrimSpace(d.client.baseURL) == "" {
		return
	}
	credential, err := d.messageAgentCredential(ctx, workspaceID, agentID)
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("graph memory auto-checkpoint credential unavailable", "agent_id", agentID, "channel_id", channelID, "error", err)
		}
		return
	}
	client := cli.NewAPIClient(d.cfg.ServerBaseURL, workspaceID, credential.Token)
	client.AgentID = agentID
	var response map[string]any
	err = client.PostJSON(ctx, "/api/agent/channels/"+channelID+"/graph-memory/checkpoint", map[string]string{
		"idempotency_key": graphMemoryAutoCheckpointKey(messages),
	}, &response)
	if err != nil {
		// A successful submit clears the active claim before this cleanup runs;
		// the resulting conflict is an expected no-op. Keep it diagnostic rather
		// than turning an already-completed agent reply into a turn failure.
		if d.logger != nil {
			d.logger.Debug("graph memory auto-checkpoint skipped", "agent_id", agentID, "channel_id", channelID, "error", err)
		}
		return
	}
	if d.logger != nil {
		d.logger.Info("graph memory auto-checkpoint completed", "agent_id", agentID, "channel_id", channelID)
	}
}

func graphMemoryAutoCheckpointKey(messages []protocol.AgentMessageProjection) string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if id := strings.TrimSpace(message.ID); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return "auto-checkpoint:" + hex.EncodeToString(sum[:])
}
