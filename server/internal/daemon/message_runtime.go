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
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
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
	coordinator, err := NewMessageCoordinator(key, agentRoot, func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.deliverIdleMessageBatch(ctx, key.AgentID, runtimeID, messages)
	}, nil)
	if err != nil {
		return nil, err
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
	runner, err := d.ensureWorkspaceRunner(workspaceID)
	if err != nil {
		return false, fmt.Errorf("ensure Workspace Runner %q: %w", workspaceID, err)
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
	runner := d.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		return fmt.Errorf("Workspace Runner %q is unavailable", workspaceID)
	}
	return runner.ensureMessageInboxForDelivery(agentID)
}

// restoreResidentAgents rebuilds durable Agent roots after a Computer process
// restart. A durable root alone does not create a Workspace Runner or a Message
// coordinator; the supervised Binding child receives an explicit managed start
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
	runID, runAgentID, turnID, mixed := mixedRunMessageBatchIdentity(messages)
	workspaceID, runtimeEpoch := d.runtimeEpochForRuntime(runtimeID)
	if workspaceID == "" {
		return fmt.Errorf("runtime %q is not registered", runtimeID)
	}
	executionID := "resident-" + uuid.NewString()
	runner, _ := d.ensureWorkspaceRunner(workspaceID)
	launchID, startDispatchID := "", ""
	if runner != nil && runner.processes != nil {
		if snapshot, ok := runner.processes.Snapshot(agentID); ok {
			launchID, startDispatchID = snapshot.LaunchID, snapshot.StartDispatchID
		}
	}
	if runner != nil && (launchID == "" || startDispatchID == "") && runner.residency != nil {
		if resident, ok := runner.residency.get(agentID); ok {
			launchID = firstNonEmpty(launchID, resident.launchID)
			startDispatchID = firstNonEmpty(startDispatchID, resident.startDispatchID)
		}
	}
	preparedMessages, memoryTask, err := d.prepareResidentMessageBatch(ctx, agentID, runtimeID, messages)
	if err != nil {
		reason := canonicalMessageFailureReason(err)
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "runtime_delivery_accepted", "rejected", reason, executionID, runtimeEpoch, launchID, startDispatchID)
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "terminal_rejected", "rejected", reason, executionID, runtimeEpoch, launchID, startDispatchID)
		return err
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
	err = d.canonicalRuntimes.deliverIdleMessages(ctx, agentID, runtimeID, preparedMessages, nil, func() {
		if mixed {
			d.activateCanonicalActionTurn(agentID, canonicalActionTurn)
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:start", protocol.MixedRunActivityActiveTurn, 1)
			// Capture-batch accounting opens with the resident turn and closes
			// only after the trusted upload (or capture-gap) is acknowledged.
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:start", protocol.MixedRunActivityUnfinishedCaptureBatch, 1)
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "runtime_delivery_accepted", "accepted", "", executionID, runtimeEpoch, launchID, startDispatchID)
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "execution_started", "accepted", "", executionID, runtimeEpoch, launchID, startDispatchID)
		runner.broadcastMessageReceivedActivity(agentID, runtimeID, preparedMessages)
	}, func(message agent.Message) {
		if message.Type == agent.MessageText && message.Content != "" {
			streamedMu.Lock()
			streamedText.WriteString(message.Content)
			streamedMu.Unlock()
		}
		d.reportMixedRunToolActivity(agentID, runtimeID, runID, runAgentID, turnID, canonicalActionTurn, message)
		runner.observeResidentMessageRuntime(agentID, runtimeID, message)
	}, func(turnErr error, generation uint64, capture *agent.ResidentTurnCapture) {
		if mixed {
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:end", protocol.MixedRunActivityActiveTurn, -1)
			if d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, turnID, canonicalActionTurn, turnErr, capture) {
				d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:end", protocol.MixedRunActivityUnfinishedCaptureBatch, -1)
			}
		}
		outcome, reasonCode := "completed", ""
		if turnErr != nil {
			outcome, reasonCode = "failed", "provider_turn_failed"
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "provider_finished", outcome, reasonCode, executionID, runtimeEpoch, launchID, startDispatchID)
		terminalPhase, terminalOutcome, terminalReason := "terminal_accepted", "accepted", ""
		if turnErr != nil {
			terminalPhase, terminalOutcome, terminalReason = "terminal_rejected", "rejected", reasonCode
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, terminalPhase, terminalOutcome, terminalReason, executionID, runtimeEpoch, launchID, startDispatchID)
		if turnErr == nil && d.client != nil && strings.TrimSpace(d.client.baseURL) != "" {
			reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			d.reportAgentMemoryWrites(reportCtx, memoryTask)
			if sessionID, ok := standaloneChatSessionIDFromMessages(preparedMessages); ok {
				streamedMu.Lock()
				streamed := streamedText.String()
				streamedMu.Unlock()
				reply := standaloneAssistantReplyText(capture, streamed)
				if reply != "" {
					if err := d.client.ReportStandaloneChatReply(reportCtx, sessionID, reply, runtimeID); err != nil && d.logger != nil {
						d.logger.Warn("standalone chat reply writeback failed", "session_id", sessionID, "error", err)
					}
				} else if d.logger != nil {
					d.logger.Warn("standalone chat reply missing after successful turn", "session_id", sessionID, "has_capture", capture != nil)
				}
			}
			cancel()
		}
		// Raft-aligned turn end: never auto-deliver Pending bodies solely because
		// the prior turn finished. If Pending remains, schedule a content-free
		// Notice; body delivery waits for idle Accept→Flush, recovery Flush, or
		// agent `message check`.
		runner.notifyPendingMessagesAfterTurn(agentID)
		d.canonicalRuntimes.publishIfMessageTurnStillIdle(agentID, runtimeID, generation, func() {
			runner.observeMessageTurnCompletion(agentID, runtimeID, turnErr)
		})
	})
	if err != nil {
		if mixed {
			d.endCanonicalActionTurn(agentID, canonicalActionTurn)
		}
		outcome := "rejected"
		if errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
			outcome = "deferred"
		}
		diagnosticExecutionID := executionID
		if outcome == "deferred" {
			diagnosticExecutionID = ""
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "runtime_delivery_accepted", outcome, canonicalMessageFailureReason(err), diagnosticExecutionID, runtimeEpoch, launchID, startDispatchID)
		if outcome == "rejected" {
			d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "terminal_rejected", "rejected", canonicalMessageFailureReason(err), diagnosticExecutionID, runtimeEpoch, launchID, startDispatchID)
		}
	}
	if err != nil && !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		// Setup and native-acceptance failures happen before a completion
		// receipt exists, so the onComplete path above cannot publish them.
		// Project the failure explicitly instead of leaving it only in daemon
		// logs while the user waits for an Agent response that cannot arrive.
		runner.observeMessageTurnCompletion(agentID, runtimeID, err)
	}
	return err
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
	credential, ok := readCachedAgentCredential(d.cfg, workspaceID, runtimeID, agentID, time.Now())
	if !ok {
		if d.logger != nil {
			d.logger.Warn("mixed-run capture credential unavailable", "run_id", runID, "run_agent_id", runAgentID)
		}
		return false
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
// User-facing Activity emission stays with the Workspace Runner; this reports
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
	for _, message := range messages {
		messageTask := residentMessageMemoryTask(workspaceID, agentID, runtimeID, []protocol.AgentMessageProjection{message})
		memories, _ := prepareExecutionMemory(agentRoot, messageTask, convertResidentMessageMemoriesForEnv(message.Memories))
		// Graph reviewer (design §1 memory_type=graph): same replacement
		// contract as runTask — graph recall wins on success, legacy stands
		// on miss or error.
		if graphMemories := d.graphExecutionMemories(ctx, messageTask, d.logger); graphMemories != nil {
			memories = graphMemories
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
		prepared = append(prepared, message)
	}
	return prepared, residentMessageMemoryTask(workspaceID, agentID, runtimeID, messages), nil
}

func residentMessageMemoryTask(workspaceID, agentID, runtimeID string, messages []protocol.AgentMessageProjection) Task {
	task := Task{WorkspaceID: workspaceID, AgentID: agentID, RuntimeID: runtimeID}
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
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
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
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return MessageCheckResult{}, errors.New("Message coordinator is unavailable")
	}
	offer, err := runner.prepareMessageCoverage(agentID, CoverageRequest{Kind: CoverageCheck, Limit: messageCheckDefaultLimit})
	if err != nil {
		return MessageCheckResult{}, err
	}
	status := messageCheckStatusComplete
	if offer.HasMore {
		status = messageCheckStatusMore
	}
	return MessageCheckResult{
		Messages: offer.Messages, HasMore: offer.HasMore, Remaining: offer.Remaining,
		Status: status, CoverageReceipt: offer.ReceiptID,
	}, nil
}

// PrepareMessageRead stages only server-validated canonical bodies. The
// boundary remains unchanged until the CLI writes the visible JSON and commits
// the returned local receipt.
func (p *CredentialProxy) PrepareMessageRead(
	agentID, target string,
	throughSeq int64,
	messages []protocol.AgentMessageProjection,
) (CoverageOffer, error) {
	if p == nil || p.daemon == nil {
		return CoverageOffer{}, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return CoverageOffer{}, errors.New("Message coordinator is unavailable")
	}
	if throughSeq == 0 && len(messages) == 0 {
		return CoverageOffer{Messages: []protocol.AgentMessageProjection{}}, nil
	}
	return runner.prepareMessageCoverage(agentID, CoverageRequest{
		Kind: CoverageRead, Target: target, ThroughSeq: throughSeq, Messages: messages,
	})
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
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return 0, errors.New("Message coordinator is unavailable")
	}
	return runner.messageSendBoundarySnapshot(agentID, target)
}

func (p *CredentialProxy) PreflightMessageSend(agentID, target string) (MessageSendFreshness, error) {
	if p == nil || p.daemon == nil {
		return MessageSendFreshness{}, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return MessageSendFreshness{}, errors.New("Message coordinator is unavailable")
	}
	return runner.preflightMessageSend(agentID, target)
}

func (p *CredentialProxy) PrepareHeldMessageContext(agentID, target string, throughSeq int64, messages []protocol.AgentMessageProjection) (CoverageOffer, error) {
	if p == nil || p.daemon == nil {
		return CoverageOffer{}, errors.New("Credential Proxy is unavailable")
	}
	runner, err := p.daemon.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return CoverageOffer{}, errors.New("Message coordinator is unavailable")
	}
	return runner.prepareMessageCoverage(agentID, CoverageRequest{
		Kind: CoverageHold, Target: target, ThroughSeq: throughSeq, Messages: messages,
	})
}
