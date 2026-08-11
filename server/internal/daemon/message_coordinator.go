package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// consumedSeqsFileName is intentionally the only durable receive-side state.
// Pending Message bodies remain a rebuildable in-memory projection.
const consumedSeqsFileName = "consumed-seqs.json"

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
}

// RuntimeMessageHandoff is the explicit boundary at which concrete Message
// bodies enter an Agent runtime. Returning nil means the runtime accepted the
// full batch; only then may the Context Boundary advance.
type RuntimeMessageHandoff func(context.Context, []protocol.AgentMessageProjection) error

// MessageReceivedActivity observes one successful daemon-to-runtime body batch.
// It is deliberately best effort and never participates in delivery state.
type MessageReceivedActivity func([]protocol.AgentMessageProjection)

// PendingNoticeSnapshot is the content-free projection of current Pending.
// The runtime seam owns same-session suppression because only it knows when a
// provider session is replaced.
type PendingNoticeSnapshot struct {
	Notice             agent.ResidentPendingNotice
	Fingerprint        string
	TargetFingerprints map[string]string
}

// RuntimePendingNoticeHandoff writes one content-free Pending Notice into a
// busy runtime session.
type RuntimePendingNoticeHandoff func(context.Context, PendingNoticeSnapshot) error

// MessageCoordinator owns the receive-side state for one Workspace/Agent root.
// Its callers send agent:deliver:ack only after Accept returns successfully.
type MessageCoordinator struct {
	mu                sync.Mutex
	boundaryCommitMu  sync.Mutex
	root              string
	boundaries        map[string]int64
	pending           map[string]map[int64]protocol.AgentMessageProjection
	accepted          map[string]struct{}
	handoff           RuntimeMessageHandoff
	activity          MessageReceivedActivity
	recovery          messageRecoveryState
	handoffGeneration uint64
	activeHandoff     *runtimeMessageHandoffToken
	boundaryHealthy   bool
	writeBoundaries   func(string, map[string]int64) error
	noticeHandoff     RuntimePendingNoticeHandoff
	noticeCoalesce    time.Duration
	noticeRetry       time.Duration
	noticeTimer       *time.Timer
	closed            bool
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
}

type runtimeMessageHandoffToken struct {
	generation         uint64
	identities         []string
	messages           []protocol.AgentMessageProjection
	proposedBoundaries map[string]int64
	runtimeAccepted    bool
	commitInProgress   bool
	activityEmitted    bool
}

type messageRecoveryStatus string

type messageRecoveryState struct {
	status        messageRecoveryStatus
	agentID       string
	recoveryID    string
	snapshotID    string
	highWatermark string
	nextCursor    string
}

const (
	messageRecoveryUnknown    messageRecoveryStatus = "unknown"
	messageRecoveryRecovering messageRecoveryStatus = "recovering"
	messageRecoveryReady      messageRecoveryStatus = "ready"
	messageRecoveryFailed     messageRecoveryStatus = "failed"
)

var (
	errRuntimeMessageHandoffInProgress  = errors.New("runtime Message handoff is already in progress")
	errRuntimeMessageHandoffInvalidated = errors.New("runtime Message handoff token was invalidated")
)

func NewMessageCoordinator(agentRoot string, handoff RuntimeMessageHandoff, activity MessageReceivedActivity) (*MessageCoordinator, error) {
	if strings.TrimSpace(agentRoot) == "" {
		return nil, errors.New("agent root is required")
	}
	if handoff == nil {
		return nil, errors.New("runtime message handoff is required")
	}
	boundaries, healthy, err := loadConsumedSeqs(filepath.Join(agentRoot, consumedSeqsFileName))
	if err != nil {
		return nil, err
	}
	return &MessageCoordinator{
		root: agentRoot, boundaries: boundaries,
		pending:  make(map[string]map[int64]protocol.AgentMessageProjection),
		accepted: make(map[string]struct{}), handoff: handoff, activity: activity,
		recovery:        messageRecoveryState{status: messageRecoveryUnknown},
		boundaryHealthy: healthy,
		writeBoundaries: writeConsumedSeqs,
		noticeCoalesce:  pendingNoticeCoalesceWindow,
		noticeRetry:     pendingNoticeRetryDelay,
	}, nil
}

// ConfigurePendingNotices installs the busy-runtime Notice seam. Durations are
// configurable for deterministic acceptance tests; production uses 3s/15s.
func (c *MessageCoordinator) ConfigurePendingNotices(handoff RuntimePendingNoticeHandoff, coalesce, retry time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noticeHandoff = handoff
	if coalesce > 0 {
		c.noticeCoalesce = coalesce
	}
	if retry > 0 {
		c.noticeRetry = retry
	}
}

// Close stops future Notice attempts. Pending remains rebuildable from the
// canonical service and is intentionally not persisted during shutdown.
func (c *MessageCoordinator) Close() {
	c.boundaryCommitMu.Lock()
	defer c.boundaryCommitMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.handoffGeneration++
	c.activeHandoff = nil
	if c.noticeTimer != nil {
		c.noticeTimer.Stop()
		c.noticeTimer = nil
	}
}

// BeginRecovery resets freshness on startup and every websocket reconnect.
func (c *MessageCoordinator) BeginRecovery(agentID string, limit int) protocol.AgentRecoveryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	recoveryID := uuid.NewString()
	c.recovery = messageRecoveryState{status: messageRecoveryRecovering, agentID: agentID, recoveryID: recoveryID}
	return protocol.AgentRecoveryRequest{AgentID: agentID, RecoveryID: recoveryID, Boundaries: cloneBoundaries(c.boundaries), Limit: limit}
}

// RecoveryRequest returns the next stateless page request.
func (c *MessageCoordinator) RecoveryRequest(agentID string, limit int) protocol.AgentRecoveryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	return protocol.AgentRecoveryRequest{AgentID: agentID, RecoveryID: c.recovery.recoveryID, Boundaries: cloneBoundaries(c.boundaries), SnapshotID: c.recovery.snapshotID, Cursor: c.recovery.nextCursor, Limit: limit}
}

// MergeRecoveryPage validates the snapshot fence and merges recovered Messages
// with concurrent live Deliveries by canonical identity and target sequence.
func (c *MessageCoordinator) MergeRecoveryPage(page protocol.AgentRecoveryPage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fail := func(err error) error {
		c.recovery.status = messageRecoveryFailed
		return err
	}
	if page.AgentID == "" || page.AgentID != c.recovery.agentID {
		return fail(errors.New("recovery page agent does not match request"))
	}
	if page.RecoveryID == "" || page.RecoveryID != c.recovery.recoveryID {
		// A page from the previous websocket generation may arrive after a
		// reconnect. Ignore it without poisoning the active recovery attempt.
		return errors.New("recovery page id does not match active recovery")
	}
	if c.recovery.status == messageRecoveryFailed {
		return errors.New("recovery attempt has failed; start a new recovery")
	}
	if page.SnapshotID == "" || page.HighWatermark == "" {
		return fail(errors.New("recovery page missing snapshot fence"))
	}
	if c.recovery.snapshotID != "" && (page.SnapshotID != c.recovery.snapshotID || page.HighWatermark != c.recovery.highWatermark) {
		return fail(errors.New("recovery snapshot fence changed between pages"))
	}
	if page.HasMore && page.NextCursor == "" {
		return fail(errors.New("recovery page has_more without next cursor"))
	}
	if page.HasMore && page.NextCursor == c.recovery.nextCursor {
		return fail(errors.New("recovery cursor did not advance"))
	}
	if !page.HasMore && page.NextCursor != "" {
		return fail(errors.New("terminal recovery page has next cursor"))
	}
	c.recovery.snapshotID = page.SnapshotID
	c.recovery.highWatermark = page.HighWatermark
	for _, message := range page.Messages {
		delivery := protocol.AgentDeliverPayload{AgentID: page.AgentID, Target: message.Target, Seq: message.Seq, DeliveryID: "recovery:" + page.SnapshotID + ":" + message.ID, Message: message}
		if _, err := c.acceptLocked(delivery); err != nil {
			return fail(err)
		}
	}
	c.recovery.nextCursor = page.NextCursor
	if page.HasMore {
		c.recovery.status = messageRecoveryRecovering
	} else {
		// The terminal snapshot fence establishes canonical freshness. Recovered
		// bodies remain Pending until a separate runtime handoff, message check,
		// or send freshness hold advances their Context Boundaries.
		c.recovery.status = messageRecoveryReady
		// A complete snapshot rebuilds conservative coverage even when the file
		// was previously missing or malformed.
		c.boundaryHealthy = true
	}
	return nil
}

func (c *MessageCoordinator) FailRecovery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recovery.status = messageRecoveryFailed
}

func (c *MessageCoordinator) FreshnessKnown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recovery.status == messageRecoveryReady
}

// ContextBoundary is the minimum Credential Proxy integration. Freshness-
// sensitive sends fail closed until the recovery fence completes.
func (c *MessageCoordinator) ContextBoundary(target string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recovery.status != messageRecoveryReady || !c.boundaryHealthy || len(c.pending[target]) > 0 {
		return 0, false
	}
	return c.boundaries[target], true
}

// SendBoundarySnapshot returns the last durable boundary even while freshness
// is unknown.  It is used only to record an attempted Draft before a network
// operation; PreflightMessageSend below is still the authority that decides
// whether the attempt may proceed.
func (c *MessageCoordinator) SendBoundarySnapshot(target string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundaries[strings.TrimSpace(target)]
}

// PreflightMessageSend checks one canonical target after the caller has saved
// its local Draft.  Pending is accepted as held context atomically: durable
// coverage advances through every Pending Message, while only the newest three
// concrete bodies are returned to the Agent.  This keeps omitted canonical
// history available to an explicit read without allowing the same Draft to be
// held forever on the same range.
func (c *MessageCoordinator) PreflightMessageSend(target string) (MessageSendFreshness, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return MessageSendFreshness{}, errors.New("canonical Message target is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return MessageSendFreshness{}, errors.New("Message coordinator is closed")
	}
	result := MessageSendFreshness{SeenUpToSeq: c.boundaries[target]}
	if c.recovery.status != messageRecoveryReady || !c.boundaryHealthy {
		return result, errors.New("Message freshness is unknown")
	}
	if c.activeHandoff != nil {
		return result, errors.New("runtime Message handoff boundary is unsettled")
	}
	bySequence := c.pending[target]
	if len(bySequence) == 0 {
		return result, nil
	}

	sequences := make([]int64, 0, len(bySequence))
	for sequence := range bySequence {
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	result.Held = true
	result.NewMessageCount = int64(len(sequences))
	result.LatestSeq = sequences[len(sequences)-1]
	shownFrom := len(sequences) - messageCheckMaxLimit
	if shownFrom < 0 {
		shownFrom = 0
	}
	result.Messages = make([]protocol.AgentMessageProjection, 0, len(sequences)-shownFrom)
	for _, sequence := range sequences[shownFrom:] {
		result.Messages = append(result.Messages, bySequence[sequence])
	}
	result.Omitted = int64(shownFrom)

	next := cloneBoundaries(c.boundaries)
	if result.LatestSeq > next[target] {
		next[target] = result.LatestSeq
	}
	if err := c.writeBoundaries(filepath.Join(c.root, consumedSeqsFileName), next); err != nil {
		c.boundaryHealthy = false
		return MessageSendFreshness{}, fmt.Errorf("persist Context Boundary after freshness hold: %w", err)
	}
	c.boundaries = next
	c.boundaryHealthy = true
	for _, sequence := range sequences {
		message := bySequence[sequence]
		delete(bySequence, sequence)
		delete(c.accepted, messageIdentityKey(message))
	}
	delete(c.pending, target)
	return result, nil
}

// AcceptHeldContext advances local coverage after the canonical Server wins a
// final send-race check.  The Server's latest sequence is the complete held
// range even when its response contains only a bounded projection.
func (c *MessageCoordinator) AcceptHeldContext(target string, throughSeq int64) error {
	return c.MarkRead(target, throughSeq)
}

// Accept installs a Delivery into Pending. It returns false for a duplicate;
// both a new acceptance and a duplicate are valid reasons for the caller to
// acknowledge the Delivery, but neither changes Context Boundary state.
func (c *MessageCoordinator) Accept(_ context.Context, delivery protocol.AgentDeliverPayload) (bool, error) {
	if err := validateAgentDelivery(delivery); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acceptLocked(delivery)
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
	return true, nil
}

// Flush hands all currently Pending bodies to an idle runtime in target-sequence
// order. The boundary file is atomically replaced before Pending is forgotten.
func (c *MessageCoordinator) Flush(ctx context.Context) error {
	_, err := c.flushWithResult(ctx, true)
	return err
}

// NotifyPendingAfterTurn schedules a content-free Notice when Pending remains
// after a resident turn ends. Raft-aligned: do not auto body-handoff the next
// batch solely because Pending exists. Body handoff stays on idle Accept→Flush
// (workspace runner) and recovery Flush; the agent may also `message check`.
func (c *MessageCoordinator) NotifyPendingAfterTurn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.pendingCountLocked() == 0 {
		return
	}
	c.schedulePendingNoticeLocked(c.noticeCoalesce)
}

// Check non-blockingly moves one bounded Pending window into the current
// Agent turn. It persists the Context Boundary before removing Pending and
// never emits the daemon-to-runtime Message received Activity.
func (c *MessageCoordinator) Check(limit int) (MessageCheckResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return MessageCheckResult{}, errors.New("Message coordinator is closed")
	}
	if c.recovery.status != messageRecoveryReady {
		return MessageCheckResult{}, errors.New("Message freshness is unknown until recovery completes")
	}
	if c.activeHandoff != nil {
		return MessageCheckResult{}, errors.New("runtime Message handoff boundary is unsettled")
	}
	if limit <= 0 {
		limit = messageCheckDefaultLimit
	}
	if limit > messageCheckMaxLimit {
		limit = messageCheckMaxLimit
	}
	batch := c.pendingBatchLocked()
	if len(batch) > limit {
		batch = batch[:limit]
	}
	if batch == nil {
		batch = []protocol.AgentMessageProjection{}
	}
	remaining := c.pendingCountLocked() - len(batch)
	result := MessageCheckResult{
		Messages: batch, HasMore: remaining > 0, Remaining: remaining, Status: messageCheckStatusComplete,
	}
	if result.HasMore {
		result.Status = messageCheckStatusMore
	}
	if len(batch) == 0 {
		return result, nil
	}
	next := cloneBoundaries(c.boundaries)
	for _, message := range batch {
		if message.Seq > next[message.Target] {
			next[message.Target] = message.Seq
		}
	}
	if err := c.writeBoundaries(filepath.Join(c.root, consumedSeqsFileName), next); err != nil {
		c.boundaryHealthy = false
		return MessageCheckResult{}, fmt.Errorf("persist Context Boundary after message check: %w", err)
	}
	c.boundaries = next
	c.boundaryHealthy = true
	for _, message := range batch {
		delete(c.pending[message.Target], message.Seq)
		delete(c.accepted, messageIdentityKey(message))
		if len(c.pending[message.Target]) == 0 {
			delete(c.pending, message.Target)
		}
	}
	return result, nil
}

// MarkRead advances exactly one target's Context Boundary after the Credential
// Proxy has returned canonical history to the Agent. It has no runtime handoff
// or Activity side effect: explicit history reading is its own boundary.
func (c *MessageCoordinator) MarkRead(target string, throughSeq int64) error {
	target = strings.TrimSpace(target)
	if target == "" || throughSeq <= 0 {
		return errors.New("message read target and positive sequence are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("Message coordinator is closed")
	}
	if c.activeHandoff != nil {
		return errors.New("runtime Message handoff boundary is unsettled")
	}
	next := cloneBoundaries(c.boundaries)
	if throughSeq > next[target] {
		next[target] = throughSeq
		if err := c.writeBoundaries(filepath.Join(c.root, consumedSeqsFileName), next); err != nil {
			c.boundaryHealthy = false
			return fmt.Errorf("persist Context Boundary after message read: %w", err)
		}
		c.boundaries = next
		c.boundaryHealthy = true
	}
	for sequence, message := range c.pending[target] {
		if sequence <= c.boundaries[target] {
			delete(c.pending[target], sequence)
			delete(c.accepted, messageIdentityKey(message))
		}
	}
	if len(c.pending[target]) == 0 {
		delete(c.pending, target)
	}
	return nil
}

func (c *MessageCoordinator) flush(ctx context.Context, scheduleBusyNotice bool) error {
	_, err := c.flushWithResult(ctx, scheduleBusyNotice)
	return err
}

func (c *MessageCoordinator) flushWithResult(ctx context.Context, scheduleBusyNotice bool) (bool, error) {
	handedOff := false
	for {
		token, needsRuntime, err := c.reserveRuntimeMessageHandoff()
		if err != nil {
			return handedOff, err
		}
		if token == nil {
			return handedOff, nil
		}

		var handoffErr error
		if needsRuntime {
			handoffErr = c.handoff(ctx, token.messages)
		}

		c.mu.Lock()
		if !c.runtimeMessageHandoffCurrentLocked(token) || c.closed {
			c.mu.Unlock()
			return handedOff, errRuntimeMessageHandoffInvalidated
		}
		if handoffErr != nil {
			c.activeHandoff = nil
			if scheduleBusyNotice && errors.Is(handoffErr, ErrCanonicalAgentRuntimeBusy) {
				c.schedulePendingNoticeLocked(c.noticeCoalesce)
			}
			c.mu.Unlock()
			return handedOff, fmt.Errorf("runtime Message handoff: %w", handoffErr)
		}
		if needsRuntime {
			token.runtimeAccepted = true
			handedOff = true
		}
		if !token.runtimeAccepted || !c.runtimeMessageHandoffContentsCurrentLocked(token) {
			c.activeHandoff = nil
			c.mu.Unlock()
			return handedOff, errRuntimeMessageHandoffInvalidated
		}
		if token.commitInProgress {
			c.mu.Unlock()
			return handedOff, errRuntimeMessageHandoffInProgress
		}
		token.commitInProgress = true
		c.mu.Unlock()
		if err := c.commitRuntimeMessageHandoff(token); err != nil {
			return handedOff, err
		}
	}
}

// reserveRuntimeMessageHandoff snapshots one immutable Pending batch and its
// proposed boundaries. It performs no Runtime or filesystem I/O and never
// consumes Pending; the caller must release the state lock before handoff.
func (c *MessageCoordinator) reserveRuntimeMessageHandoff() (*runtimeMessageHandoffToken, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false, errors.New("Message coordinator is closed")
	}
	if c.recovery.status != messageRecoveryReady {
		return nil, false, errors.New("Message freshness is unknown until recovery completes")
	}
	if c.activeHandoff != nil {
		if c.activeHandoff.runtimeAccepted && !c.activeHandoff.commitInProgress {
			return c.activeHandoff, false, nil
		}
		return nil, false, errRuntimeMessageHandoffInProgress
	}
	batch := c.pendingBatchLocked()
	if len(batch) == 0 {
		return nil, false, nil
	}
	c.handoffGeneration++
	identities := make([]string, len(batch))
	proposedBoundaries := cloneBoundaries(c.boundaries)
	for index, message := range batch {
		identities[index] = messageIdentityKey(message)
		if message.Seq > proposedBoundaries[message.Target] {
			proposedBoundaries[message.Target] = message.Seq
		}
	}
	token := &runtimeMessageHandoffToken{
		generation:         c.handoffGeneration,
		identities:         identities,
		messages:           append([]protocol.AgentMessageProjection(nil), batch...),
		proposedBoundaries: proposedBoundaries,
	}
	c.activeHandoff = token
	return token, true, nil
}

func (c *MessageCoordinator) runtimeMessageHandoffCurrentLocked(token *runtimeMessageHandoffToken) bool {
	return token != nil && c.activeHandoff == token && c.handoffGeneration == token.generation
}

func (c *MessageCoordinator) runtimeMessageHandoffContentsCurrentLocked(token *runtimeMessageHandoffToken) bool {
	if !c.runtimeMessageHandoffCurrentLocked(token) || len(token.identities) != len(token.messages) {
		return false
	}
	for index, message := range token.messages {
		current, ok := c.pending[message.Target][message.Seq]
		if !ok || messageIdentityKey(current) != token.identities[index] {
			return false
		}
	}
	return true
}

// commitRuntimeMessageHandoff serializes native-acceptance commits without
// holding the main coordinator mutex across Activity or filesystem I/O. Close
// takes the same commit mutex, so no stale writer can outlive replacement.
func (c *MessageCoordinator) commitRuntimeMessageHandoff(token *runtimeMessageHandoffToken) error {
	c.mu.Lock()
	if c.closed || !c.runtimeMessageHandoffCurrentLocked(token) || !token.runtimeAccepted || !token.commitInProgress || !c.runtimeMessageHandoffContentsCurrentLocked(token) {
		if c.runtimeMessageHandoffCurrentLocked(token) {
			token.commitInProgress = false
			c.activeHandoff = nil
		}
		c.mu.Unlock()
		return errRuntimeMessageHandoffInvalidated
	}
	activity := c.activity
	emitActivity := activity != nil && !token.activityEmitted
	if emitActivity {
		token.activityEmitted = true
	}
	messages := append([]protocol.AgentMessageProjection(nil), token.messages...)
	proposedBoundaries := cloneBoundaries(token.proposedBoundaries)
	boundaryPath := filepath.Join(c.root, consumedSeqsFileName)
	c.mu.Unlock()

	if emitActivity {
		activity(messages)
	}

	c.boundaryCommitMu.Lock()
	defer c.boundaryCommitMu.Unlock()
	c.mu.Lock()
	if c.closed || !c.runtimeMessageHandoffCurrentLocked(token) || !token.commitInProgress || !c.runtimeMessageHandoffContentsCurrentLocked(token) {
		if c.runtimeMessageHandoffCurrentLocked(token) {
			token.commitInProgress = false
			c.activeHandoff = nil
		}
		c.mu.Unlock()
		return errRuntimeMessageHandoffInvalidated
	}
	c.mu.Unlock()

	writeErr := c.writeBoundaries(boundaryPath, proposedBoundaries)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.runtimeMessageHandoffCurrentLocked(token) || !token.commitInProgress || !c.runtimeMessageHandoffContentsCurrentLocked(token) {
		if c.runtimeMessageHandoffCurrentLocked(token) {
			token.commitInProgress = false
			c.activeHandoff = nil
		}
		return errRuntimeMessageHandoffInvalidated
	}
	token.commitInProgress = false
	if writeErr != nil {
		// The native Runtime already accepted this exact token. Keep it as an
		// in-process coverage receipt so retry performs only the durable commit.
		// Restart may conservatively replay because the file proves no coverage.
		c.boundaryHealthy = false
		return fmt.Errorf("persist Context Boundary after runtime handoff: %w", writeErr)
	}
	c.boundaries = proposedBoundaries
	c.boundaryHealthy = true
	for _, message := range token.messages {
		delete(c.pending[message.Target], message.Seq)
		delete(c.accepted, messageIdentityKey(message))
		if len(c.pending[message.Target]) == 0 {
			delete(c.pending, message.Target)
		}
	}
	c.activeHandoff = nil
	return nil
}

func (c *MessageCoordinator) schedulePendingNoticeLocked(delay time.Duration) {
	if c.closed || c.noticeHandoff == nil || c.noticeTimer != nil || len(c.pending) == 0 {
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
	// Raft-aligned: Notice path is content-free only. Do not body-handoff from
	// the notice timer (that recreated automatic second turns after busy).
	if err := c.deliverPendingNotice(ctx); err != nil {
		c.mu.Lock()
		c.schedulePendingNoticeLocked(c.noticeRetry)
		c.mu.Unlock()
	}
}

func (c *MessageCoordinator) deliverPendingNotice(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.noticeHandoff == nil {
		return errors.New("Pending Notice handoff is unavailable")
	}
	snapshot := c.pendingNoticeLocked()
	if snapshot.Notice.TotalPending == 0 {
		return nil
	}
	return c.noticeHandoff(ctx, snapshot)
}

func (c *MessageCoordinator) pendingNoticeLocked() PendingNoticeSnapshot {
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
	return PendingNoticeSnapshot{
		Notice: notice, Fingerprint: fmt.Sprintf("%x", sum[:]), TargetFingerprints: targetFingerprints,
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

// Acknowledgement returns the wire receipt permitted after Accept succeeds.
// It intentionally has no boundary, runtime, or execution fields.
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

func loadConsumedSeqs(path string) (map[string]int64, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read consumed sequences: %w", err)
	}
	boundaries := map[string]int64{}
	if err := json.Unmarshal(data, &boundaries); err != nil {
		// Corruption is unknown coverage, never permission to skip context.
		return map[string]int64{}, false, nil
	}
	for target, sequence := range boundaries {
		if strings.TrimSpace(target) == "" || sequence < 0 {
			return map[string]int64{}, false, nil
		}
	}
	return boundaries, true, nil
}

func writeConsumedSeqs(path string, boundaries map[string]int64) error {
	data, err := json.Marshal(boundaries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".consumed-seqs-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func cloneBoundaries(boundaries map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(boundaries))
	for target, sequence := range boundaries {
		copy[target] = sequence
	}
	return copy
}
