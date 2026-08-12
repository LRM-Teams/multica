package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
		return d.handoffIdleMessageBatch(ctx, key.AgentID, runtimeID, messages)
	}, nil)
	if err != nil {
		return nil, err
	}
	coordinator.ConfigurePendingNotices(func(ctx context.Context, snapshot PendingNoticeSnapshot, commitIfCurrent PendingNoticeCommitIfCurrent) error {
		return d.canonicalRuntimes.handoffBusyNotice(ctx, key.AgentID, runtimeID, snapshot, commitIfCurrent)
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

// ensureIdleMessageCoordinatorForDelivery repairs restart-time coordinator
// loss from the daemon's durable Agent Attachment. Runtime routing remains out
// of the Message envelope; the fixed Runner Workspace scope and a matching
// Attachment are both required to recreate this receive-side projection.
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
	if runner.hasMessageInbox(agentID) {
		return nil
	}
	registry := d.attachmentRegistry()
	if registry == nil {
		return fmt.Errorf("no durable Agent Attachment for %q in Workspace %q", agentID, workspaceID)
	}
	attachment, ok := registry.Resolve(workspaceID, agentID)
	if !ok {
		return fmt.Errorf("no durable Agent Attachment for %q in Workspace %q", agentID, workspaceID)
	}
	d.mu.Lock()
	runtime, runtimeKnown := d.runtimeIndex[attachment.RuntimeID]
	d.mu.Unlock()
	if !runtimeKnown || runtime.WorkspaceID != workspaceID {
		return fmt.Errorf("durable Agent Attachment for %q is not owned by Workspace %q", agentID, workspaceID)
	}
	created, err := d.ensureIdleMessageCoordinator(workspaceID, agentID, attachment.RuntimeID)
	if err != nil {
		return fmt.Errorf("repair Agent Message coordinator: %w", err)
	}
	if created {
		runner.beginMessageRecovery(agentID)
	}
	return nil
}

// restoreResidentAgents rebuilds Inbox ownership for durable Attachments after
// a Computer process restart. Provider launches are never reconstructed here;
// the Workspace Runner receives an explicit managed start when work exists.
func (d *Daemon) restoreResidentAgents() error {
	if d == nil {
		return nil
	}
	for _, attachment := range d.currentAttachments() {
		if attachment.RuntimeID == "" || attachment.WorkspaceID == "" || attachment.AttachmentGeneration < 1 {
			continue
		}
		d.mu.Lock()
		runtime, runtimeKnown := d.runtimeIndex[attachment.RuntimeID]
		d.mu.Unlock()
		if !runtimeKnown || runtime.WorkspaceID != attachment.WorkspaceID {
			continue
		}
		agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, attachment.WorkspaceID, attachment.AgentID)
		if err := ensureMulticaAgentRoot(agentRoot); err != nil {
			return fmt.Errorf("restore Agent root %q: %w", attachment.AgentID, err)
		}
		if _, err := d.ensureIdleMessageCoordinator(attachment.WorkspaceID, attachment.AgentID, attachment.RuntimeID); err != nil {
			return fmt.Errorf("restore Agent Message coordinator %q: %w", attachment.AgentID, err)
		}
		if _, err := d.ensureWorkspaceRunner(attachment.WorkspaceID); err != nil {
			return fmt.Errorf("restore Agent Workspace Runner %q: %w", attachment.AgentID, err)
		}
	}
	return nil
}

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

func (d *Daemon) handoffIdleMessageBatch(ctx context.Context, agentID, runtimeID string, messages []protocol.AgentMessageProjection) error {
	runID, runAgentID, turnID, mixed := mixedRunMessageBatchIdentity(messages)
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	runner, _ := d.ensureWorkspaceRunner(workspaceID)
	preparedMessages, memoryTask, err := d.prepareResidentMessageBatch(ctx, agentID, runtimeID, messages)
	if err != nil {
		return err
	}
	err = d.canonicalRuntimes.handoffIdleMessages(ctx, agentID, runtimeID, preparedMessages, func() {
		runner.observeMessageLifecycle(agentID, runtimeID)
	}, func() {
		if mixed {
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:start", protocol.MixedRunActivityActiveTurn, 1)
			// US1 capture accounting tracks the accepted resident-turn batch. The
			// provider upload/capture payload remains deferred to T049.
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:start", protocol.MixedRunActivityUnfinishedCaptureBatch, 1)
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "runtime_handoff_accepted", "accepted", "")
		runner.observeMessageAccepted(agentID, runtimeID, preparedMessages)
	}, func(message agent.Message) {
		d.reportMixedRunToolActivity(agentID, runtimeID, runID, runAgentID, turnID, message)
		runner.observeResidentMessageRuntime(agentID, runtimeID, message)
	}, func(turnErr error, generation uint64) {
		if mixed {
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":active:end", protocol.MixedRunActivityActiveTurn, -1)
			d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, "turn:"+turnID+":capture:end", protocol.MixedRunActivityUnfinishedCaptureBatch, -1)
		}
		outcome, reasonCode := "completed", ""
		if turnErr != nil {
			outcome, reasonCode = "failed", "provider_turn_failed"
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "provider_finished", outcome, reasonCode)
		if turnErr == nil && d.client != nil && strings.TrimSpace(d.client.baseURL) != "" {
			reportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			d.reportAgentMemoryWrites(reportCtx, memoryTask)
			cancel()
		}
		// Raft-aligned turn end: never auto body-handoff Pending solely because
		// the prior turn finished. If Pending remains, schedule a content-free
		// Notice; body handoff waits for idle Accept→Flush, recovery Flush, or
		// agent `message check`.
		runner.notifyPendingMessagesAfterTurn(agentID)
		d.canonicalRuntimes.publishIfMessageTurnStillIdle(agentID, runtimeID, generation, func() {
			runner.observeMessageTurnCompletion(agentID, runtimeID, turnErr)
		})
	})
	if err != nil {
		outcome := "rejected"
		if errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
			outcome = "deferred"
		}
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, messages, "runtime_handoff_accepted", outcome, canonicalMessageFailureReason(err))
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

// reportMixedRunToolActivity tracks inflight tool calls of a mixed-run turn.
// User-facing Activity emission stays with the Workspace Runner; this reports
// only the durable mixed-run transition deltas.
func (d *Daemon) reportMixedRunToolActivity(agentID, runtimeID, runID, runAgentID, turnID string, message agent.Message) {
	if runID == "" || runAgentID == "" || turnID == "" || strings.TrimSpace(message.CallID) == "" {
		return
	}
	switch message.Type {
	case agent.MessageToolUse:
		d.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID,
			"turn:"+turnID+":tool:"+message.CallID+":start", protocol.MixedRunActivityInflightTool, 1)
	case agent.MessageToolResult:
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
		message.RuntimeContext = execenv.RenderTurnContext(execenv.TaskContextForEnv{
			MessageDelivery: true,
			AgentID:         agentID,
			AgentRoot:       agentRoot,
			AgentMemories:   memories,
			ChannelID:       message.ChannelID,
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
	return task
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
