package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	Messages        []protocol.AgentMessageProjection `json:"messages"`
	HasMore         bool                              `json:"has_more"`
	Remaining       int                               `json:"remaining"`
	Status          string                            `json:"status"`
	CoverageReceipt string                            `json:"_coverage_receipt,omitempty"`
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
// Accept makes Pending responsible for a valid delivery. The Workspace Runner
// may ACK only after its deeper acceptance seam has either handed that body to
// the provider, retained it in Pending, or identified it as a duplicate.
type MessageCoordinator struct {
	mu                  sync.Mutex
	deliveryMu          sync.Mutex
	key                 InboxKey
	root                string
	boundaries          map[string]int64
	pending             map[string]map[int64]protocol.AgentMessageProjection
	accepted            map[string]struct{}
	deliver             RuntimeMessageDelivery
	activity            MessageReceivedActivity
	queueActivity       MessageQueueActivity
	deliverInboxNotice  RuntimeInboxNoticeDelivery
	noticeCoordinatorID string
	pendingGeneration   uint64
	noticeCoalesce      time.Duration
	noticeRetry         time.Duration
	noticeTimer         *time.Timer
	coverageReceipts    map[string]*coverageReceipt
	coverageNow         func() time.Time
	coverageTTL         time.Duration
	coverageCapacity    int
	closed              bool
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
	CoverageReceipt string
}

type pendingNoticeCommitState uint8

const (
	pendingNoticeCommitAwaiting pendingNoticeCommitState = iota
	pendingNoticeCommitRejected
	pendingNoticeCommitApplied
)

var (
	errRuntimeMessageDeliveryInProgress  = errors.New("runtime Message delivery is already in progress")
	errRuntimeMessageDeliveryInvalidated = errors.New("runtime Message delivery was invalidated")
	errPendingNoticeGenerationChanged    = errors.New("Pending Notice generation changed before suppression commit")
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
	return &MessageCoordinator{
		key: key, root: agentRoot, boundaries: make(map[string]int64),
		pending:  make(map[string]map[int64]protocol.AgentMessageProjection),
		accepted: make(map[string]struct{}), deliver: deliver, activity: activity,
		noticeCoordinatorID: uuid.NewString(),
		noticeCoalesce:      pendingNoticeCoalesceWindow,
		noticeRetry:         pendingNoticeRetryDelay,
		coverageReceipts:    make(map[string]*coverageReceipt),
		coverageNow:         time.Now,
		coverageTTL:         coverageReceiptDefaultTTL,
		coverageCapacity:    coverageReceiptDefaultCapacity,
	}, nil
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
	c.coverageReceipts = nil
	if c.noticeTimer != nil {
		c.noticeTimer.Stop()
		c.noticeTimer = nil
	}
}

// ContextBoundary is the minimum Credential Proxy integration.
func (c *MessageCoordinator) ContextBoundary(target string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending[target]) > 0 {
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
// its local Draft. Pending is represented by one two-phase coverage receipt:
// only the newest three concrete bodies are returned, while commit advances
// through the complete represented Pending range.
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
	hasPending := len(c.pending[target]) > 0
	c.mu.Unlock()
	if !hasPending {
		return result, nil
	}
	offer, err := c.PrepareCoverage(CoverageRequest{Kind: CoverageHold, Target: target, Limit: messageCheckMaxLimit})
	if err != nil {
		return result, err
	}
	if offer.ReceiptID == "" {
		return result, errors.New("freshness hold coverage is unavailable")
	}
	result.Held = true
	result.NewMessageCount = int64(offer.CoveredCount)
	result.LatestSeq = offer.ThroughSeq
	result.Messages = offer.Messages
	result.Omitted = int64(offer.CoveredCount - len(offer.Messages))
	result.CoverageReceipt = offer.ReceiptID
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
	created, err := c.acceptLocked(delivery)
	activity := c.queueActivity
	c.mu.Unlock()
	if err == nil && created && activity != nil {
		activity([]protocol.AgentMessageProjection{delivery.Message}, 1)
	}
	return created, err
}

func (c *MessageCoordinator) acceptLocked(delivery protocol.AgentDeliverPayload) (bool, error) {
	key := messageIdentityKey(delivery.Message)
	if _, ok := c.accepted[key]; ok {
		return false, nil
	}
	if delivery.Seq <= c.boundaries[delivery.Target] {
		return false, nil
	}
	bySequence := c.pending[delivery.Target]
	if bySequence == nil {
		bySequence = make(map[int64]protocol.AgentMessageProjection)
		c.pending[delivery.Target] = bySequence
	}
	if existing, ok := bySequence[delivery.Seq]; ok && existing.ID != delivery.Message.ID {
		return false, fmt.Errorf("target %q sequence %d maps to conflicting Messages", delivery.Target, delivery.Seq)
	}
	bySequence[delivery.Seq] = delivery.Message
	c.accepted[key] = struct{}{}
	c.pendingGeneration++
	return true, nil
}

// Flush hands all currently Pending bodies to an idle runtime in target-sequence
// order. The in-memory boundary advances before Pending is forgotten.
func (c *MessageCoordinator) Flush(ctx context.Context) error {
	_, err := c.flushWithResult(ctx, true)
	return err
}

// NotifyPendingAfterTurn schedules a content-free Notice when Pending remains
// after a resident turn ends. Raft-aligned: do not auto-deliver the next body
// batch solely because Pending exists. Body delivery stays on idle Accept→Flush
// (workspace runner) and recovery Flush; the agent may also `message check`.
func (c *MessageCoordinator) NotifyPendingAfterTurn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.pendingCountLocked() == 0 {
		return
	}
	c.schedulePendingNoticeLocked(c.noticeCoalesce)
}

// MarkRead advances exactly one target's Context Boundary after the Credential
// Proxy has returned canonical history to the Agent. It has no runtime delivery
// or Activity side effect: explicit history reading is its own boundary.
func (c *MessageCoordinator) MarkRead(target string, throughSeq int64) error {
	target = strings.TrimSpace(target)
	if target == "" || throughSeq <= 0 {
		return errors.New("message read target and positive sequence are required")
	}
	if !c.deliveryMu.TryLock() {
		return errors.New("runtime Message delivery is in progress")
	}
	defer c.deliveryMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("Message coordinator is closed")
	}
	if throughSeq > c.boundaries[target] {
		c.boundaries[target] = throughSeq
	}
	pendingChanged := false
	var removed []protocol.AgentMessageProjection
	for sequence, message := range c.pending[target] {
		if sequence <= c.boundaries[target] {
			removed = append(removed, message)
			delete(c.pending[target], sequence)
			delete(c.accepted, messageIdentityKey(message))
			pendingChanged = true
		}
	}
	if len(c.pending[target]) == 0 {
		delete(c.pending, target)
	}
	if pendingChanged {
		c.pendingGeneration++
	}
	c.emitQueueActivityLocked(removed, -1)
	return nil
}

func (c *MessageCoordinator) flush(ctx context.Context, scheduleBusyNotice bool) error {
	_, err := c.flushWithResult(ctx, scheduleBusyNotice)
	return err
}

func (c *MessageCoordinator) flushWithResult(ctx context.Context, scheduleBusyNotice bool) (bool, error) {
	if !c.deliveryMu.TryLock() {
		return false, errRuntimeMessageDeliveryInProgress
	}
	defer c.deliveryMu.Unlock()

	delivered := false
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return delivered, errors.New("Message coordinator is closed")
		}
		messages := c.pendingBatchLocked()
		if len(messages) == 0 {
			c.mu.Unlock()
			return delivered, nil
		}
		identities := make([]string, len(messages))
		for i, message := range messages {
			identities[i] = messageIdentityKey(message)
		}
		c.mu.Unlock()

		if err := c.deliver(ctx, messages); err != nil {
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

		c.mu.Lock()
		if c.closed || !c.pendingContentsCurrentLocked(messages, identities) {
			c.mu.Unlock()
			return delivered, errRuntimeMessageDeliveryInvalidated
		}
		for _, message := range messages {
			if message.Seq > c.boundaries[message.Target] {
				c.boundaries[message.Target] = message.Seq
			}
			delete(c.pending[message.Target], message.Seq)
			delete(c.accepted, messageIdentityKey(message))
			if len(c.pending[message.Target]) == 0 {
				delete(c.pending, message.Target)
			}
		}
		c.emitQueueActivityLocked(messages, -1)
		c.pendingGeneration++
		c.mu.Unlock()
	}
}

func (c *MessageCoordinator) pendingContentsCurrentLocked(messages []protocol.AgentMessageProjection, identities []string) bool {
	if len(messages) != len(identities) {
		return false
	}
	for i, message := range messages {
		current, ok := c.pending[message.Target][message.Seq]
		if !ok || messageIdentityKey(current) != identities[i] {
			return false
		}
	}
	return true
}

func (c *MessageCoordinator) schedulePendingNoticeLocked(delay time.Duration) {
	if c.closed || c.deliverInboxNotice == nil || c.noticeTimer != nil || len(c.pending) == 0 {
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
	targets := make([]string, 0, len(c.pending))
	for target := range c.pending {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	notice := agent.ResidentPendingNotice{ChangedTargets: make([]agent.ResidentPendingTarget, 0, len(targets))}
	identities := make([]string, 0)
	targetFingerprints := make(map[string]string, len(targets))
	for _, target := range targets {
		sequences := make([]int64, 0, len(c.pending[target]))
		for sequence := range c.pending[target] {
			sequences = append(sequences, sequence)
		}
		sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
		notice.ChangedTargets = append(notice.ChangedTargets, agent.ResidentPendingTarget{Target: target, PendingCount: len(sequences)})
		notice.TotalPending += len(sequences)
		targetIdentities := make([]string, 0, len(sequences))
		for _, sequence := range sequences {
			identity := messageIdentityKey(c.pending[target][sequence])
			identities = append(identities, identity)
			targetIdentities = append(targetIdentities, identity)
		}
		targetSum := sha256.Sum256([]byte(strings.Join(targetIdentities, "\x01")))
		targetFingerprints[target] = fmt.Sprintf("%x", targetSum[:])
	}
	sum := sha256.Sum256([]byte(strings.Join(identities, "\x01")))
	return InboxNoticeSnapshot{
		Notice: notice, Fingerprint: fmt.Sprintf("%x", sum[:]), TargetFingerprints: targetFingerprints,
		CoordinatorID: c.noticeCoordinatorID, PendingGeneration: c.pendingGeneration,
	}
}

func messageIdentityKey(message protocol.AgentMessageProjection) string {
	return message.ID + "\x00" + message.Target + "\x00" + fmt.Sprint(message.Seq)
}

func (c *MessageCoordinator) Boundaries() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneBoundaries(c.boundaries)
}

// PendingSnapshot returns a pure copy for aggregate Inbox inspection. Unlike
// message check/read, it creates no coverage receipt and advances no boundary.
func (c *MessageCoordinator) PendingSnapshot() []protocol.AgentMessageProjection {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.AgentMessageProjection(nil), c.pendingBatchLocked()...)
}

// Acknowledgement constructs the wire receipt after Accept succeeds. Emission
// remains the Workspace Runner acceptance seam's responsibility, after it has
// classified provider acceptance, Pending retention, or deduplication.
func (c *MessageCoordinator) Acknowledgement(delivery protocol.AgentDeliverPayload) protocol.AgentDeliverAckPayload {
	return protocol.AgentDeliverAckPayload{
		AgentID: delivery.AgentID, Seq: delivery.Seq, DeliveryID: delivery.DeliveryID, Traceparent: delivery.Traceparent,
	}
}

func (c *MessageCoordinator) pendingBatchLocked() []protocol.AgentMessageProjection {
	targets := make([]string, 0, len(c.pending))
	for target := range c.pending {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	var batch []protocol.AgentMessageProjection
	for _, target := range targets {
		sequences := make([]int64, 0, len(c.pending[target]))
		for sequence := range c.pending[target] {
			sequences = append(sequences, sequence)
		}
		sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
		for _, sequence := range sequences {
			batch = append(batch, c.pending[target][sequence])
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

func (c *MessageCoordinator) pendingCountLocked() int {
	total := 0
	for _, messages := range c.pending {
		total += len(messages)
	}
	return total
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
