package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	pendingNoticeCoalesceWindow = 3 * time.Second
	pendingNoticeRetryDelay     = 15 * time.Second
	pendingNoticeWriteTimeout   = 5 * time.Second
	messageCheckDefaultLimit    = 3
	messageCheckMaxLimit        = 3
	messageCheckStatusComplete  = "complete"
	messageCheckStatusMore      = "more"
)

// MessageCheckResult is the bounded concrete-body projection returned through
// the machine-local Credential Proxy.
type MessageCheckResult struct {
	Messages  []protocol.AgentMessageProjection `json:"messages"`
	HasMore   bool                              `json:"has_more"`
	Remaining int                               `json:"remaining"`
	Status    string                            `json:"status"`
	Revision  uint64                            `json:"revision"`
}

// RuntimeMessageDelivery is the boundary at which concrete Message
// bodies enter an Agent runtime. Returning nil means the runtime accepted the
// full batch; only then may the Context Boundary advance.
type RuntimeMessageDelivery func(context.Context, []protocol.AgentMessageProjection) error

// MessageReceivedActivity observes one successful daemon-to-runtime body batch.
// It is deliberately best effort and never participates in delivery state.
type MessageReceivedActivity func([]protocol.AgentMessageProjection)

// MessageQueueActivity observes messages entering (+1) or leaving (-1) the
// coordinator Pending set. Callers must use stable per-message transition IDs.
type MessageQueueActivity func([]protocol.AgentMessageProjection, int)

// InboxNoticeSnapshot is the content-free projection of current Pending.
// The runtime seam owns same-session suppression because only it knows when a
// provider session is replaced.
type InboxNoticeSnapshot struct {
	Notice             agent.ResidentPendingNotice
	Fingerprint        string
	TargetFingerprints map[string]string
	TargetKeys         []string
	CoordinatorID      string
	PendingGeneration  uint64
}

// InboxNoticeCommitIfCurrent atomically checks the coordinator's Pending
// generation and runs a short in-memory Runtime-session fingerprint commit.
type InboxNoticeCommitIfCurrent func(func()) bool

// RuntimeInboxNoticeDelivery writes one content-free Inbox Notice into a
// busy runtime session, then finalizes session suppression through the supplied
// generation fence. It must invoke commitIfCurrent before returning nil.
type RuntimeInboxNoticeDelivery func(context.Context, InboxNoticeSnapshot, InboxNoticeCommitIfCurrent) error

// InboxKey is the fixed local identity of one Workspace/Agent coordinator.
type InboxKey struct {
	WorkspaceID string
	AgentID     string
}

func (k InboxKey) normalized() (InboxKey, error) {
	k.WorkspaceID = strings.TrimSpace(k.WorkspaceID)
	k.AgentID = strings.TrimSpace(k.AgentID)
	if k.WorkspaceID == "" || k.AgentID == "" {
		return InboxKey{}, errors.New("Inbox Workspace and Agent identity are required")
	}
	return k, nil
}

// MessageCoordinator owns the receive-side state for one Workspace/Agent root.
// Accept makes Pending responsible for a valid delivery. The WorkspaceDaemon
// may ACK only after its deeper acceptance seam has either handed that body to
// the provider, retained it in Pending, or identified it as a duplicate.
type MessageCoordinator struct {
	mu                  sync.Mutex
	key                 InboxKey
	root                string
	boundaries          map[string]int64
	consumedMessageIDs  map[string]map[string]struct{}
	flushOwner          atomic.Bool
	deliver             RuntimeMessageDelivery
	activity            MessageReceivedActivity
	queueActivity       MessageQueueActivity
	deliverInboxNotice  RuntimeInboxNoticeDelivery
	noticeCoordinatorID string
	pendingGeneration   uint64
	noticeCoalesce      time.Duration
	noticeRetry         time.Duration
	noticeTimer         *time.Timer
	closed              bool
	providerRetryState  providerDeliveryRetryState
	inboxStore          *AgentAppInboxStore
}

// SetInboxStore binds the coordinator to the daemon's single per-agent inbox
// store. The daemon installs this before exposing the coordinator.
func (c *MessageCoordinator) SetInboxStore(store *AgentAppInboxStore) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inboxStore = store
}

// MessageSendFreshness is the local preflight result for one target.  The
// coordinator is the only owner of the Context Boundary, so callers never
// provide a cursor or decide whether Pending is safe to skip.
type MessageSendFreshness struct {
	SeenUpToSeq     int64
	LatestSeq       int64
	NewMessageCount int64
	Messages        []protocol.AgentMessageProjection
	Omitted         int64
	Held            bool
	Revision        uint64
}

type pendingNoticeCommitState uint8

type providerDeliveryRetryState string

const (
	providerDeliveryRetryIdle      providerDeliveryRetryState = "idle"
	providerDeliveryRetryQueued    providerDeliveryRetryState = "queued"
	providerDeliveryRetryReplacing providerDeliveryRetryState = "replacing"
	providerDeliveryRetryFlushing  providerDeliveryRetryState = "flushing"
	providerDeliveryRetrySucceeded providerDeliveryRetryState = "succeeded"
	providerDeliveryRetryFailed    providerDeliveryRetryState = "failed"
)

const (
	pendingNoticeCommitAwaiting pendingNoticeCommitState = iota
	pendingNoticeCommitRejected
	pendingNoticeCommitApplied
)

var (
	errRuntimeMessageDeliveryInProgress  = errors.New("runtime Message delivery is already in progress")
	errRuntimeMessageDeliveryInvalidated = errors.New("runtime Message delivery was invalidated")
	errPendingNoticeGenerationChanged    = errors.New("Pending Notice generation changed before suppression commit")
	errStaleInboxRevision                = errors.New("stale inbox revision")
)

func NewMessageCoordinator(key InboxKey, agentRoot string, deliver RuntimeMessageDelivery, activity MessageReceivedActivity) (*MessageCoordinator, error) {
	key, err := key.normalized()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(agentRoot) == "" {
		return nil, errors.New("agent root is required")
	}
	if deliver == nil {
		return nil, errors.New("runtime message delivery is required")
	}
	coordinator := &MessageCoordinator{
		key: key, root: agentRoot, boundaries: make(map[string]int64),
		consumedMessageIDs: make(map[string]map[string]struct{}),
		deliver:            deliver, activity: activity,
		noticeCoordinatorID: uuid.NewString(),
		noticeCoalesce:      pendingNoticeCoalesceWindow,
		noticeRetry:         pendingNoticeRetryDelay,
		providerRetryState:  providerDeliveryRetryIdle,
	}
	coordinator.inboxStore = newAgentAppInboxStore(key.AgentID, filepath.Join(agentRoot, "app-inbox", "state.json"))
	if err := coordinator.inboxStore.Restore(); err != nil {
		return nil, fmt.Errorf("restore Agent App Inbox: %w", err)
	}
	return coordinator, nil
}

// projectVisibleMessages applies the Agent-facing visibility rule without
// changing the durable Inbox item. A message already covered by the local
// boundary or consumed by ID retains its internal sequence; a first-time
// message is copied with its sequence removed.
func (c *MessageCoordinator) projectVisibleMessages(messages []protocol.AgentMessageProjection, consume, advanceBoundary bool) []protocol.AgentMessageProjection {
	if c == nil || len(messages) == 0 {
		return messages
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	projected := make([]protocol.AgentMessageProjection, 0, len(messages))
	for _, message := range messages {
		seen := c.messageVisibleLocked(message)
		if !seen {
			message.Seq = 0
		}
		projected = append(projected, message)
	}
	if consume {
		c.consumeVisibleMessagesLocked(messages, advanceBoundary)
	}
	return projected
}

func (c *MessageCoordinator) messageVisibleLocked(message protocol.AgentMessageProjection) bool {
	if boundary := c.boundaries[message.Target]; message.Seq > 0 && boundary >= message.Seq {
		return true
	}
	return c.consumedMessageIDs[message.Target] != nil && func() bool {
		_, ok := c.consumedMessageIDs[message.Target][message.ID]
		return ok
	}()
}

func (c *MessageCoordinator) consumeVisibleMessagesLocked(messages []protocol.AgentMessageProjection, advanceBoundary bool) {
	for _, message := range messages {
		if message.Target == "" || message.ID == "" {
			continue
		}
		ids := c.consumedMessageIDs[message.Target]
		if ids == nil {
			ids = make(map[string]struct{})
			c.consumedMessageIDs[message.Target] = ids
		}
		ids[message.ID] = struct{}{}
		if advanceBoundary && message.Seq > c.boundaries[message.Target] {
			c.boundaries[message.Target] = message.Seq
		}
	}
}

// ConfigurePendingNotices installs the busy-runtime Notice seam. Durations are
// configurable for deterministic acceptance tests; production uses 3s/15s.
func (c *MessageCoordinator) ConfigurePendingNotices(deliver RuntimeInboxNoticeDelivery, coalesce, retry time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deliverInboxNotice = deliver
	if coalesce > 0 {
		c.noticeCoalesce = coalesce
	}
	if retry > 0 {
		c.noticeRetry = retry
	}
}

// ConfigureQueueActivity installs the observer for Pending-set transitions.
func (c *MessageCoordinator) ConfigureQueueActivity(activity MessageQueueActivity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queueActivity = activity
}

// Close stops future Notice attempts. Pending remains rebuildable from the
// canonical service and is intentionally not persisted during shutdown.
func (c *MessageCoordinator) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.noticeTimer != nil {
		c.noticeTimer.Stop()
		c.noticeTimer = nil
	}
}

// ContextBoundary is the minimum Credential Proxy integration.
func (c *MessageCoordinator) ContextBoundary(target string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasMessageItemsForTarget(target) {
		return 0, false
	}
	return c.boundaries[target], true
}

// SendBoundarySnapshot returns the current in-memory boundary. It is used only
// to record an attempted Draft before a network operation; PreflightMessageSend
// below is still the authority that decides whether the attempt may proceed.
func (c *MessageCoordinator) SendBoundarySnapshot(target string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundaries[strings.TrimSpace(target)]
}

// PreflightMessageSend checks one canonical target after the caller has saved
// its local Draft. Pending is represented by the coordinator's current inbox
// revision; callers must acknowledge that revision after presenting the body.
func (c *MessageCoordinator) PreflightMessageSend(target string) (MessageSendFreshness, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return MessageSendFreshness{}, errors.New("canonical Message target is required")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return MessageSendFreshness{}, errors.New("Message coordinator is closed")
	}
	result := MessageSendFreshness{SeenUpToSeq: c.boundaries[target]}
	hasPending := c.hasMessageItemsForTarget(target)
	c.mu.Unlock()
	if !hasPending {
		return result, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	allMessages := c.messageItemsForTarget(target)
	covered := allMessages
	if len(covered) > messageCheckMaxLimit {
		covered = covered[len(covered)-messageCheckMaxLimit:]
	}
	result.Held = true
	result.NewMessageCount = int64(len(allMessages))
	if len(covered) > 0 {
		result.LatestSeq = covered[len(covered)-1].Seq
	}
	result.Messages = covered
	result.Omitted = 0
	result.Revision = c.pendingGeneration
	return result, nil
}

// Accept installs a Delivery into Pending. It returns false for a duplicate;
// both a new acceptance and a duplicate are valid reasons for the caller to
// acknowledge the Delivery, but neither changes Context Boundary state.
func (c *MessageCoordinator) Accept(_ context.Context, delivery protocol.AgentDeliverPayload) (bool, error) {
	if err := validateAgentDelivery(delivery); err != nil {
		return false, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false, errors.New("Message coordinator is closed")
	}
	if c.inboxStore == nil {
		return false, errors.New("agent app inbox store is unavailable")
	}
	_, created, err := c.inboxStore.MintMessage(delivery.Message)
	if err != nil {
		return false, fmt.Errorf("persist message inbox item: %w", err)
	}
	activity := c.queueActivity
	if created {
		c.mu.Lock()
		c.pendingGeneration++
		c.mu.Unlock()
	}
	if err == nil && created && activity != nil {
		activity([]protocol.AgentMessageProjection{delivery.Message}, 1)
	}
	return created, err
}

// Flush hands all currently Pending bodies to an idle runtime in target-sequence
// order. The in-memory boundary advances before Pending is forgotten.
func (c *MessageCoordinator) Flush(ctx context.Context) error {
	_, err := c.flushWithResult(ctx, true)
	return err
}

// flushWithResultLocked runs a delivery while the coordinator's operation
// owner is held. Callers that already serialize recovery with other delivery
// operations use this form to keep restoration and the replacement Flush
// indivisible.
func (c *MessageCoordinator) flushWithResultLocked(ctx context.Context, scheduleBusyNotice bool) error {
	_, err := c.flushWithResultBody(ctx, scheduleBusyNotice)
	return err
}

// beginProviderDeliveryRetry records the explicit replacement lifecycle and restores
// the failed batch. Only one replacement may own the batch at a time.
func (c *MessageCoordinator) beginProviderDeliveryRetry(messages []protocol.AgentMessageProjection) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(messages) == 0 || c.providerRetryState == providerDeliveryRetryQueued || c.providerRetryState == providerDeliveryRetryReplacing || c.providerRetryState == providerDeliveryRetryFlushing {
		return false
	}
	firstPendingSeq := make(map[string]int64)
	for _, message := range messages {
		if message.Target == "" || message.Seq <= 0 {
			continue
		}
		if first, ok := firstPendingSeq[message.Target]; !ok || message.Seq < first {
			firstPendingSeq[message.Target] = message.Seq
		}
	}
	if len(firstPendingSeq) == 0 {
		return false
	}
	for target, first := range firstPendingSeq {
		if c.boundaries[target] >= first {
			c.boundaries[target] = first - 1
		}
	}
	c.pendingGeneration++
	c.providerRetryState = providerDeliveryRetryQueued
	return true
}

func (c *MessageCoordinator) advanceProviderDeliveryRetry(from, to providerDeliveryRetryState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.providerRetryState != from {
		return false
	}
	c.providerRetryState = to
	return true
}

func (c *MessageCoordinator) finishProviderDeliveryRetry(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if success {
		c.providerRetryState = providerDeliveryRetrySucceeded
	} else {
		c.providerRetryState = providerDeliveryRetryFailed
	}
}

// NotifyPendingAfterTurn schedules a content-free Notice when Pending remains
// after a resident turn ends. Raft-aligned: do not auto-deliver the next body
// batch solely because Pending exists. Body delivery stays on idle Accept→Flush
// (workspace daemon) and recovery Flush; the agent may also `message check`.
func (c *MessageCoordinator) NotifyPendingAfterTurn() {
	if c == nil || c.inboxStore == nil || len(c.inboxStore.ListMessageItems()) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.schedulePendingNoticeLocked(c.noticeCoalesce)
}

func (c *MessageCoordinator) AckMessage(messageID string, seq int64) bool {
	if c == nil || c.inboxStore == nil {
		return false
	}
	return c.inboxStore.Ack("message:" + messageID + ":" + fmt.Sprint(seq))
}

func (c *MessageCoordinator) flush(ctx context.Context, scheduleBusyNotice bool) error {
	_, err := c.flushWithResult(ctx, scheduleBusyNotice)
	return err
}

func (c *MessageCoordinator) flushWithResult(ctx context.Context, scheduleBusyNotice bool) (bool, error) {
	if !c.flushOwner.CompareAndSwap(false, true) {
		return false, errRuntimeMessageDeliveryInProgress
	}
	defer c.flushOwner.Store(false)
	return c.flushWithResultBody(ctx, scheduleBusyNotice)
}

func (c *MessageCoordinator) flushWithResultBody(ctx context.Context, scheduleBusyNotice bool) (bool, error) {
	delivered := false
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return delivered, errors.New("Message coordinator is closed")
		}
		var messages []protocol.AgentMessageProjection
		var inboxItems []AgentAppInboxItem
		if c.inboxStore != nil {
			inboxItems = c.inboxStore.ListMessageItems()
			for _, item := range inboxItems {
				messages = append(messages, *item.Message)
			}
		}
		if len(messages) == 0 {
			c.mu.Unlock()
			return delivered, nil
		}
		identities := make([]string, len(messages))
		for i, message := range messages {
			identities[i] = messageIdentityKey(message)
		}
		c.mu.Unlock()

		projectedMessages := c.projectVisibleMessages(messages, false, false)
		if err := c.deliver(ctx, projectedMessages); err != nil {
			c.mu.Lock()
			if scheduleBusyNotice && errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
				c.schedulePendingNoticeLocked(c.noticeCoalesce)
			}
			c.mu.Unlock()
			return delivered, fmt.Errorf("runtime Message delivery: %w", err)
		}
		delivered = true
		if c.activity != nil {
			c.activity(messages)
		}

		if c.closed {
			return delivered, errRuntimeMessageDeliveryInvalidated
		}
		for _, item := range inboxItems {
			if !c.inboxStore.Ack(item.ItemID) {
				return delivered, errRuntimeMessageDeliveryInvalidated
			}
		}
		c.mu.Lock()
		c.consumeVisibleMessagesLocked(messages, false)
		c.emitQueueActivityLocked(messages, -1)
		for _, message := range messages {
			if message.Seq > c.boundaries[message.Target] {
				c.boundaries[message.Target] = message.Seq
			}
		}
		c.pendingGeneration++
		c.mu.Unlock()
	}
}

func (c *MessageCoordinator) pendingContentsCurrentLocked(messages []protocol.AgentMessageProjection, identities []string) bool {
	return len(messages) == len(identities)
}

func (c *MessageCoordinator) schedulePendingNoticeLocked(delay time.Duration) {
	if c.closed || c.deliverInboxNotice == nil || c.noticeTimer != nil || c.inboxStore == nil || len(c.inboxStore.ListMessageItems()) == 0 {
		return
	}
	if delay <= 0 {
		delay = c.noticeCoalesce
	}
	c.noticeTimer = time.AfterFunc(delay, c.runPendingNoticeAttempt)
}

func (c *MessageCoordinator) runPendingNoticeAttempt() {
	c.mu.Lock()
	c.noticeTimer = nil
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), pendingNoticeWriteTimeout)
	defer cancel()
	// Raft-aligned: Notice path is content-free only. Do not deliver bodies from
	// the notice timer (that recreated automatic second turns after busy).
	if err := c.deliverPendingNotice(ctx); err != nil {
		c.mu.Lock()
		c.schedulePendingNoticeLocked(c.noticeRetry)
		c.mu.Unlock()
	}
}

func (c *MessageCoordinator) deliverPendingNotice(ctx context.Context) error {
	c.mu.Lock()
	if c.closed || c.deliverInboxNotice == nil {
		c.mu.Unlock()
		return errors.New("Inbox Notice delivery is unavailable")
	}
	snapshot := c.pendingNoticeLocked()
	deliver := c.deliverInboxNotice
	c.mu.Unlock()
	if snapshot.Notice.TotalPending == 0 {
		return nil
	}
	commitState := pendingNoticeCommitAwaiting
	commitIfCurrent := func(commit func()) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if commitState != pendingNoticeCommitAwaiting || commit == nil || c.closed || c.noticeCoordinatorID != snapshot.CoordinatorID || c.pendingGeneration != snapshot.PendingGeneration {
			commitState = pendingNoticeCommitRejected
			return false
		}
		commit()
		commitState = pendingNoticeCommitApplied
		return true
	}
	deliveryErr := deliver(ctx, snapshot, commitIfCurrent)
	c.mu.Lock()
	defer c.mu.Unlock()
	if deliveryErr != nil {
		if commitState == pendingNoticeCommitAwaiting {
			commitState = pendingNoticeCommitRejected
		}
		return deliveryErr
	}
	if commitState != pendingNoticeCommitApplied {
		commitState = pendingNoticeCommitRejected
		return errPendingNoticeGenerationChanged
	}
	return nil
}

func (c *MessageCoordinator) pendingNoticeLocked() InboxNoticeSnapshot {
	items := c.inboxStore.ListMessageItems()
	byTarget := make(map[string][]protocol.AgentMessageProjection)
	targets := make([]string, 0)
	for _, item := range items {
		byTarget[item.Message.Target] = append(byTarget[item.Message.Target], *item.Message)
	}
	for target := range byTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	notice := agent.ResidentPendingNotice{ChangedTargets: make([]agent.ResidentPendingTarget, 0, len(targets))}
	identities := make([]string, 0)
	targetFingerprints := make(map[string]string, len(targets))
	targetKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		messages := byTarget[target]
		sequences := make([]int64, 0, len(messages))
		for _, message := range messages {
			sequences = append(sequences, message.Seq)
		}
		sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
		replyTarget := ""
		if len(sequences) > 0 {
			for _, message := range messages {
				if message.Seq == sequences[0] {
					replyTarget = message.ReplyTarget
					break
				}
			}
		}
		notice.ChangedTargets = append(notice.ChangedTargets, agent.ResidentPendingTarget{Target: replyTarget, PendingCount: len(sequences)})
		targetKeys = append(targetKeys, target)
		notice.TotalPending += len(sequences)
		targetIdentities := make([]string, 0, len(sequences))
		for _, sequence := range sequences {
			identity := ""
			for _, message := range messages {
				if message.Seq == sequence {
					identity = messageIdentityKey(message)
					break
				}
			}
			identities = append(identities, identity)
			targetIdentities = append(targetIdentities, identity)
		}
		targetSum := sha256.Sum256([]byte(strings.Join(targetIdentities, "\x01")))
		targetFingerprints[target] = fmt.Sprintf("%x", targetSum[:])
	}
	sum := sha256.Sum256([]byte(strings.Join(identities, "\x01")))
	return InboxNoticeSnapshot{
		Notice: notice, Fingerprint: fmt.Sprintf("%x", sum[:]), TargetFingerprints: targetFingerprints, TargetKeys: targetKeys,
		CoordinatorID: c.noticeCoordinatorID, PendingGeneration: c.pendingGeneration,
	}
}

func messageIdentityKey(message protocol.AgentMessageProjection) string {
	return message.ID + "\x00" + message.Target + "\x00" + fmt.Sprint(message.Seq)
}

func pendingForCoverageTarget(bySequence map[int64]protocol.AgentMessageProjection) []protocol.AgentMessageProjection {
	sequences := make([]int64, 0, len(bySequence))
	for sequence := range bySequence {
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	result := make([]protocol.AgentMessageProjection, 0, len(sequences))
	for _, sequence := range sequences {
		result = append(result, bySequence[sequence])
	}
	return result
}

func (c *MessageCoordinator) Boundaries() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneBoundaries(c.boundaries)
}

// MessageItemsSnapshot exposes the durable inbox projection for recovery and
// transport code. It deliberately does not expose coordinator internals.
func (c *MessageCoordinator) MessageItemsSnapshot() []AgentAppInboxItem {
	if c == nil || c.inboxStore == nil {
		return nil
	}
	return c.inboxStore.ListMessageItems()
}

// Acknowledgement constructs the wire receipt after Accept succeeds. Emission
// remains the WorkspaceDaemon acceptance seam's responsibility, after it has
// classified provider acceptance, Pending retention, or deduplication.
func (c *MessageCoordinator) Acknowledgement(delivery protocol.AgentDeliverPayload) protocol.AgentDeliverAckPayload {
	return protocol.AgentDeliverAckPayload{
		AgentID: delivery.AgentID, Seq: delivery.Seq, DeliveryID: delivery.DeliveryID, Traceparent: delivery.Traceparent,
	}
}

func (c *MessageCoordinator) pendingBatchLocked() []protocol.AgentMessageProjection {
	items := c.MessageItemsSnapshot()
	batch := make([]protocol.AgentMessageProjection, 0, len(items))
	for _, item := range items {
		if item.Message != nil {
			batch = append(batch, *item.Message)
		}
	}
	return batch
}

func (c *MessageCoordinator) emitQueueActivityLocked(messages []protocol.AgentMessageProjection, delta int) {
	if len(messages) > 0 && c.queueActivity != nil {
		c.queueActivity(messages, delta)
	}
}

// PendingCount reports how many Messages are queued for the resident runtime.
func (c *MessageCoordinator) PendingCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingCountLocked()
}

func (c *MessageCoordinator) InboxRevision() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingGeneration
}

func (c *MessageCoordinator) pendingCountLocked() int {
	return len(c.MessageItemsSnapshot())
}

func (c *MessageCoordinator) hasMessageItemsForTarget(target string) bool {
	return len(c.messageItemsForTarget(target)) > 0
}

func (c *MessageCoordinator) messageItemsForTarget(target string) []protocol.AgentMessageProjection {
	items := c.MessageItemsSnapshot()
	result := make([]protocol.AgentMessageProjection, 0, len(items))
	for _, item := range items {
		if item.Message != nil && item.Message.Target == target {
			result = append(result, *item.Message)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Seq < result[j].Seq })
	return result
}

func validateAgentDelivery(delivery protocol.AgentDeliverPayload) error {
	if strings.TrimSpace(delivery.AgentID) == "" || strings.TrimSpace(delivery.DeliveryID) == "" {
		return errors.New("agent_id and delivery_id are required")
	}
	if strings.TrimSpace(delivery.Target) == "" || delivery.Seq <= 0 {
		return errors.New("target and positive seq are required")
	}
	if strings.TrimSpace(delivery.Message.ID) == "" {
		return errors.New("Message id is required")
	}
	if delivery.Message.Target != delivery.Target || delivery.Message.Seq != delivery.Seq {
		return errors.New("Message target and seq must match delivery envelope")
	}
	return nil
}

func cloneBoundaries(boundaries map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(boundaries))
	for target, sequence := range boundaries {
		copy[target] = sequence
	}
	return copy
}
