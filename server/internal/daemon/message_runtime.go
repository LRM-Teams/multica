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

func (d *Daemon) ensureIdleMessageCoordinator(agentID, runtimeID, agentRoot string) (bool, error) {
	if d == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(runtimeID) == "" {
		return false, errors.New("agent and runtime ids are required")
	}
	d.messageCoordinatorMu.Lock()
	defer d.messageCoordinatorMu.Unlock()
	if existing := d.messageCoordinators[agentID]; existing != nil {
		if d.messageRuntimeIDs[agentID] == "" || d.messageRuntimeIDs[agentID] == runtimeID {
			return false, nil
		}
		existing.Close()
	}
	coordinator, err := NewMessageCoordinator(agentRoot, func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.handoffIdleMessageBatch(ctx, agentID, runtimeID, messages)
	}, nil)
	if err != nil {
		return false, err
	}
	coordinator.ConfigurePendingNotices(func(ctx context.Context, snapshot PendingNoticeSnapshot) error {
		return d.canonicalRuntimes.handoffBusyNotice(ctx, agentID, runtimeID, snapshot)
	}, 0, 0)
	if d.messageCoordinators == nil {
		d.messageCoordinators = make(map[string]*MessageCoordinator)
	}
	if d.messageRuntimeIDs == nil {
		d.messageRuntimeIDs = make(map[string]string)
	}
	d.messageCoordinators[agentID] = coordinator
	d.messageRuntimeIDs[agentID] = runtimeID
	return true, nil
}

// ensureIdleMessageCoordinatorForDelivery repairs restart-time coordinator
// loss from the daemon's durable Agent placement. Runtime routing remains out
// of the Message envelope; only an already-authorized local residency may
// recreate this receive-side projection.
func (d *Daemon) ensureIdleMessageCoordinatorForDelivery(agentID string) error {
	if d == nil || strings.TrimSpace(agentID) == "" {
		return errors.New("agent id is required")
	}
	d.messageCoordinatorMu.RLock()
	existing := d.messageCoordinators[agentID]
	d.messageCoordinatorMu.RUnlock()
	if existing != nil {
		return nil
	}
	if d.reminderAgents == nil {
		return fmt.Errorf("no durable Agent placement for %q", agentID)
	}
	residency, ok := d.reminderAgents.get(agentID)
	if !ok || residency.RuntimeID == "" || residency.WorkspaceID == "" || residency.PlacementGeneration < 1 {
		return fmt.Errorf("no durable Agent placement for %q", agentID)
	}
	d.mu.Lock()
	runtime, runtimeKnown := d.runtimeIndex[residency.RuntimeID]
	d.mu.Unlock()
	if !runtimeKnown || runtime.WorkspaceID != residency.WorkspaceID {
		return fmt.Errorf("durable Agent placement for %q is not owned by this daemon", agentID)
	}
	agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, residency.WorkspaceID, agentID)
	if err := ensureMulticaAgentRoot(agentRoot); err != nil {
		return fmt.Errorf("create Agent root for Message coordinator: %w", err)
	}
	created, err := d.ensureIdleMessageCoordinator(agentID, residency.RuntimeID, agentRoot)
	if err != nil {
		return fmt.Errorf("repair Agent Message coordinator: %w", err)
	}
	if created {
		d.beginAgentMessageRecovery(agentID)
	}
	return nil
}

func (d *Daemon) handoffIdleMessageBatch(ctx context.Context, agentID, runtimeID string, messages []protocol.AgentMessageProjection) error {
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	preparedMessages, memoryTask, err := d.prepareResidentMessageBatch(ctx, agentID, runtimeID, messages)
	if err != nil {
		return err
	}
	err = d.canonicalRuntimes.handoffIdleMessages(ctx, agentID, runtimeID, preparedMessages, func() {
		d.emitMessageLifecycleActivity(agentID, runtimeID, protocol.ActivityKindWorking, "starting", "Starting")
	}, func() {
		d.recordResidentMessageBatch(workspaceID, runtimeID, agentID, preparedMessages, "runtime_handoff_accepted", "accepted", "")
		d.emitMessageReceivedActivity(agentID, runtimeID, preparedMessages)
	}, func(message agent.Message) {
		d.emitResidentMessageRuntimeActivity(agentID, runtimeID, message)
	}, func(turnErr error, generation uint64) {
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
		d.messageCoordinatorMu.RLock()
		coordinator := d.messageCoordinators[agentID]
		d.messageCoordinatorMu.RUnlock()
		if coordinator != nil {
			coordinator.NotifyPendingAfterTurn()
		}
		d.canonicalRuntimes.publishIfMessageTurnStillIdle(agentID, runtimeID, generation, func() {
			d.emitMessageTurnCompletionActivity(agentID, runtimeID, turnErr)
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
		d.emitMessageTurnCompletionActivity(agentID, runtimeID, err)
	}
	return err
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

func (d *Daemon) emitMessageLifecycleActivity(agentID, runtimeID, activityKind, detailKind, narrative string) {
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID == "" {
		return
	}
	entry, err := activityNarrativeEntry(activityKind, detailKind, narrative)
	if err != nil {
		return
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	if err := producer.PublishForManagedAgent(agentID, d.runnerInstanceID, activityKind, detailKind, []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
		d.logger.Debug("workspace Runner Message lifecycle Activity publish deferred", "error", err, "agent_id", agentID, "runtime_id", runtimeID)
	}
}

func (d *Daemon) emitResidentMessageRuntimeActivity(agentID, runtimeID string, message agent.Message) {
	if message.Type == agent.MessageDiagnostic {
		d.emitResidentRuntimeDiagnostic(agentID, runtimeID, message)
		return
	}
	activityKind, detailKind, narrative := "", "", ""
	switch message.Type {
	case agent.MessageThinking:
		// Thinking is a B-chain state (snapshot activity_kind), not an
		// A-chain timeline event. An empty narrative keeps it from emitting
		// an entry, so bursts of thinking never flood the activity timeline
		// (raft-aligned; see workspace_runner_activity).
		activityKind = protocol.ActivityKindThinking
	case agent.MessageToolUse:
		activityKind = protocol.ActivityKindWorking
		detailKind, narrative = toolActivityFact(message.Tool, message.Input)
	case agent.MessageCompactionStarted:
		activityKind, detailKind, narrative = protocol.ActivityKindWorking, "compacting_context", "Compacting context"
	case agent.MessageCompactionFinished:
		activityKind, detailKind, narrative = protocol.ActivityKindOnline, "idle", "Context compaction finished"
	case agent.MessageError:
		activityKind, detailKind, narrative = protocol.ActivityKindError, "runtime_error", runtimeErrorNarrative(message.Content)
	}
	if activityKind == "" {
		return
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID == "" {
		return
	}
	var entries []protocol.AgentActivityEntry
	if narrative != "" {
		entry, err := activityNarrativeEntry(activityKind, detailKind, narrative)
		if err != nil {
			return
		}
		entries = []protocol.AgentActivityEntry{entry}
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	if err := producer.PublishForManagedAgent(agentID, d.runnerInstanceID, activityKind, detailKind, entries); err != nil && d.logger != nil {
		d.logger.Debug("workspace Runner resident runtime Activity publish deferred", "error", err, "agent_id", agentID, "runtime_id", runtimeID)
	}
}

func (d *Daemon) emitResidentRuntimeDiagnostic(agentID, runtimeID string, message agent.Message) {
	if message.Level != "warning" || strings.TrimSpace(message.Title) == "" || strings.TrimSpace(message.Content) == "" {
		return
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID == "" {
		return
	}
	entry, err := activitySystemEntry(message.Title, message.Content)
	if err != nil {
		return
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	if err := producer.PublishEntryForManagedAgent(agentID, d.runnerInstanceID, []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
		d.logger.Debug("workspace Runner runtime diagnostic Activity publish deferred", "error", err, "agent_id", agentID, "runtime_id", runtimeID)
	}
}

func (d *Daemon) emitMessageTurnCompletionActivity(agentID, runtimeID string, turnErr error) {
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID == "" {
		return
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	activityKind, detailKind, narrative := protocol.ActivityKindOnline, "idle", "Idle"
	if turnErr != nil {
		activityKind, detailKind, narrative = protocol.ActivityKindError, "runtime_error", runtimeErrorNarrative(turnErr.Error())
	}
	entry, err := activityNarrativeEntry(activityKind, detailKind, narrative)
	if err != nil {
		return
	}
	if err := producer.PublishForManagedAgent(agentID, d.runnerInstanceID, activityKind, detailKind, []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
		d.logger.Debug("workspace Runner Message completion Activity publish deferred", "error", err, "agent_id", agentID)
	}
}

func runtimeErrorNarrative(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "Runtime error"
	}
	return truncateRunes(reason, 400)
}

func (d *Daemon) emitMessageReceivedActivity(agentID, runtimeID string, messages []protocol.AgentMessageProjection) {
	if len(messages) == 0 {
		return
	}
	targetSet := make(map[string]struct{})
	identity := make([]string, 0, len(messages))
	for _, message := range messages {
		targetSet[message.Target] = struct{}{}
		identity = append(identity, message.ID+"\x00"+message.Target+"\x00"+strconv.FormatInt(message.Seq, 10))
	}
	sort.Strings(identity)
	sum := sha256.Sum256([]byte(strings.Join(identity, "\x01")))
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID != "" {
		producer := d.workspaceAgentActivityProducer(workspaceID)
		status, session, created, manageErr := producer.EnsureManagedAgent(agentID)
		if manageErr == nil && created {
			// Status must cross the same serialized Runner writer before Activity
			// so the server can fence the new launch authoritatively.
			d.sendWorkspaceRunnerAgentFrame(agentID, protocol.EventAgentStatus, status)
			d.sendWorkspaceRunnerAgentFrame(agentID, protocol.EventAgentSession, session)
		}
		entry, err := activityNarrativeEntry(protocol.ActivityKindWorking, "message_received", "Message received")
		if err == nil {
			if err := producer.PublishForManagedAgent(agentID, d.runnerInstanceID, protocol.ActivityKindWorking, "message_received", []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
				d.logger.Debug("workspace Runner Message Activity publish deferred", "error", err, "agent_id", agentID)
			}
		}
	}
	payload := protocol.AgentMessageHandoffPayload{
		AgentID: agentID, RuntimeID: runtimeID, HandoffID: hex.EncodeToString(sum[:]), Count: len(messages), Targets: targets,
	}
	// Handoff is an observation after concrete bodies crossed the local
	// boundary. It belongs to the same Workspace Runner as delivery/recovery;
	// if the Runner is absent we intentionally drop this best-effort fact rather
	// than revive the retired runtime socket path.
	d.sendWorkspaceRunnerAgentFrame(agentID, protocol.EventAgentMessageHandoff, payload)
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
	p.daemon.messageCoordinatorMu.RLock()
	defer p.daemon.messageCoordinatorMu.RUnlock()
	coordinator := p.daemon.messageCoordinators[agentID]
	if coordinator == nil {
		return 0, errors.New("Message coordinator is unavailable")
	}
	seq, known := coordinator.ContextBoundary(target)
	if !known {
		return 0, errors.New("Message freshness is unknown")
	}
	return seq, nil
}

func (p *CredentialProxy) CheckMessages(agentID string) (MessageCheckResult, error) {
	if p == nil || p.daemon == nil {
		return MessageCheckResult{}, errors.New("Credential Proxy is unavailable")
	}
	p.daemon.messageCoordinatorMu.RLock()
	defer p.daemon.messageCoordinatorMu.RUnlock()
	coordinator := p.daemon.messageCoordinators[strings.TrimSpace(agentID)]
	if coordinator == nil {
		return MessageCheckResult{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.Check(messageCheckDefaultLimit)
}

// RecordMessageRead consumes successful canonical history through one target's
// local Context Boundary. The server response is the authority for both the
// canonical target and its maximum returned sequence; the Agent never supplies
// a cursor directly.
func (p *CredentialProxy) RecordMessageRead(agentID, target string, throughSeq int64) error {
	if p == nil || p.daemon == nil {
		return errors.New("Credential Proxy is unavailable")
	}
	p.daemon.messageCoordinatorMu.RLock()
	defer p.daemon.messageCoordinatorMu.RUnlock()
	coordinator := p.daemon.messageCoordinators[strings.TrimSpace(agentID)]
	if coordinator == nil {
		return errors.New("Message coordinator is unavailable")
	}
	return coordinator.MarkRead(target, throughSeq)
}

func (p *CredentialProxy) messageCoordinator(agentID string) (*MessageCoordinator, error) {
	if p == nil || p.daemon == nil {
		return nil, errors.New("Credential Proxy is unavailable")
	}
	p.daemon.messageCoordinatorMu.RLock()
	coordinator := p.daemon.messageCoordinators[strings.TrimSpace(agentID)]
	p.daemon.messageCoordinatorMu.RUnlock()
	if coordinator == nil {
		return nil, errors.New("Message coordinator is unavailable")
	}
	return coordinator, nil
}

func (p *CredentialProxy) SaveNormalMessageDraft(agentID string, draft messageDraft, now time.Time) (messageDraft, error) {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return messageDraft{}, err
	}
	return coordinator.SaveNormalMessageDraft(draft, now)
}

func (p *CredentialProxy) LoadMessageDraft(agentID, target string, now time.Time) (messageDraft, bool, error) {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return messageDraft{}, false, err
	}
	return coordinator.LoadMessageDraft(target, now)
}

func (p *CredentialProxy) RefreshMessageDraft(agentID, target, clientMessageID, contextTarget string, seenUpToSeq int64, now time.Time) (messageDraft, error) {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return messageDraft{}, err
	}
	return coordinator.RefreshMessageDraft(target, clientMessageID, contextTarget, seenUpToSeq, now)
}

func (p *CredentialProxy) ClearMessageDraft(agentID, target, clientMessageID string) error {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return err
	}
	return coordinator.ClearMessageDraft(target, clientMessageID)
}

func (p *CredentialProxy) MessageSendBoundarySnapshot(agentID, target string) (int64, error) {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return 0, err
	}
	return coordinator.SendBoundarySnapshot(target), nil
}

func (p *CredentialProxy) PreflightMessageSend(agentID, target string) (MessageSendFreshness, error) {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return MessageSendFreshness{}, err
	}
	return coordinator.PreflightMessageSend(target)
}

func (p *CredentialProxy) AcceptHeldMessageContext(agentID, target string, throughSeq int64) error {
	coordinator, err := p.messageCoordinator(agentID)
	if err != nil {
		return err
	}
	return coordinator.AcceptHeldContext(target, throughSeq)
}
