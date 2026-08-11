package daemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	coverageReceiptDefaultTTL      = 5 * time.Minute
	coverageReceiptDefaultCapacity = 128
)

// CoverageKind identifies the concrete local context boundary represented by
// a receipt. Search, resolve, react, Delivery ACK, and Notice are intentionally
// absent because they never cover Message bodies.
type CoverageKind string

const (
	CoverageCheck CoverageKind = "check"
	CoverageRead  CoverageKind = "read"
	CoverageHold  CoverageKind = "hold"
)

var (
	ErrCoverageRequestInvalid    = errors.New("coverage request is invalid")
	ErrCoverageReceiptInvalid    = errors.New("coverage receipt is invalid")
	ErrCoverageReceiptExpired    = errors.New("coverage receipt expired")
	ErrCoverageReceiptInProgress = errors.New("coverage receipt commit is already in progress")
	ErrCoverageReceiptCapacity   = errors.New("coverage receipt capacity is unavailable")
)

// CoverageRequest describes the exact bodies represented by one local output.
// Check and hold select from Pending; read supplies server-validated Messages.
type CoverageRequest struct {
	Kind       CoverageKind
	Target     string
	ThroughSeq int64
	Limit      int
	Messages   []protocol.AgentMessageProjection
}

// CoverageOffer is returned to the machine-local Credential Proxy. ReceiptID
// is an opaque capability and must not become a public or service cursor.
type CoverageOffer struct {
	ReceiptID    string
	Messages     []protocol.AgentMessageProjection
	HasMore      bool
	Remaining    int
	CoveredCount int
	ThroughSeq   int64
}

type coverageReceiptPhase uint8

const (
	coverageReceiptPrepared coverageReceiptPhase = iota
	coverageReceiptCommitting
	coverageReceiptCommitted
)

type coverageReceipt struct {
	id                 string
	key                InboxKey
	requiresPending    bool
	coveredIdentities  map[string]map[int64]string
	proposedBoundaries map[string]int64
	createdAt          time.Time
	expiresAt          time.Time
	phase              coverageReceiptPhase
}

func (c *MessageCoordinator) ownsCoverageReceipt(receiptID string) bool {
	if c == nil {
		return false
	}
	receiptID = strings.TrimSpace(receiptID)
	c.mu.Lock()
	defer c.mu.Unlock()
	receipt := c.coverageReceipts[receiptID]
	return !c.closed && receipt != nil && receipt.id == receiptID && receipt.key == c.key
}

// PrepareCoverage reserves one bounded, expiring receipt without changing the
// durable Context Boundary or removing Pending.
func (c *MessageCoordinator) PrepareCoverage(request CoverageRequest) (CoverageOffer, error) {
	if c == nil {
		return CoverageOffer{}, fmt.Errorf("%w: Inbox coordinator is unavailable", ErrCoverageRequestInvalid)
	}
	request.Target = strings.TrimSpace(request.Target)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return CoverageOffer{}, fmt.Errorf("%w: Inbox coordinator is closed", ErrCoverageRequestInvalid)
	}
	if request.Kind != CoverageRead && c.recovery.status != messageRecoveryReady {
		return CoverageOffer{}, fmt.Errorf("%w: Message freshness is unknown", ErrCoverageRequestInvalid)
	}
	if request.Kind == CoverageHold && !c.boundaryHealthy {
		return CoverageOffer{}, fmt.Errorf("%w: Context Boundary health is unknown", ErrCoverageRequestInvalid)
	}
	if c.activeHandoff != nil {
		return CoverageOffer{}, fmt.Errorf("%w: runtime Message handoff boundary is unsettled", ErrCoverageRequestInvalid)
	}

	covered, presented, hasMore, err := c.prepareCoverageMessagesLocked(request)
	if err != nil {
		return CoverageOffer{}, err
	}
	if len(covered) == 0 {
		return CoverageOffer{Messages: []protocol.AgentMessageProjection{}}, nil
	}
	remaining := 0
	if request.Kind == CoverageCheck {
		remaining = c.pendingCountLocked() - len(covered)
	}
	coveredIdentities, proposedBoundaries, err := buildCoverageIdentity(covered, request)
	if err != nil {
		return CoverageOffer{}, err
	}

	now := c.coverageNow()
	c.expireCoverageReceiptsLocked(now)
	if err := c.reserveCoverageReceiptCapacityLocked(); err != nil {
		return CoverageOffer{}, err
	}
	id := uuid.NewString()
	c.coverageReceipts[id] = &coverageReceipt{
		id:                 id,
		key:                c.key,
		requiresPending:    request.Kind != CoverageRead && !(request.Kind == CoverageHold && len(request.Messages) > 0),
		coveredIdentities:  coveredIdentities,
		proposedBoundaries: proposedBoundaries,
		createdAt:          now,
		expiresAt:          now.Add(c.coverageTTL),
		phase:              coverageReceiptPrepared,
	}
	return CoverageOffer{
		ReceiptID:    id,
		Messages:     cloneCoverageMessages(presented),
		HasMore:      hasMore,
		Remaining:    remaining,
		CoveredCount: len(covered),
		ThroughSeq:   proposedBoundaries[request.Target],
	}, nil
}

func (c *MessageCoordinator) prepareCoverageMessagesLocked(request CoverageRequest) ([]protocol.AgentMessageProjection, []protocol.AgentMessageProjection, bool, error) {
	switch request.Kind {
	case CoverageCheck:
		if request.Target != "" || request.ThroughSeq != 0 || len(request.Messages) != 0 {
			return nil, nil, false, fmt.Errorf("%w: check coverage selects directly from Pending", ErrCoverageRequestInvalid)
		}
		limit := normalizeCoverageLimit(request.Limit)
		pending := c.pendingBatchLocked()
		if len(pending) == 0 {
			return nil, nil, false, nil
		}
		covered := pending
		if len(covered) > limit {
			covered = covered[:limit]
		}
		return cloneCoverageMessages(covered), cloneCoverageMessages(covered), len(pending) > len(covered), nil

	case CoverageHold:
		if request.Target == "" {
			return nil, nil, false, fmt.Errorf("%w: hold coverage requires one target", ErrCoverageRequestInvalid)
		}
		if len(request.Messages) > 0 || request.ThroughSeq != 0 {
			if request.ThroughSeq <= 0 || len(request.Messages) == 0 || len(request.Messages) > messageCheckMaxLimit || request.Limit != 0 {
				return nil, nil, false, fmt.Errorf("%w: service hold coverage requires a positive through sequence and bounded Messages", ErrCoverageRequestInvalid)
			}
			return cloneCoverageMessages(request.Messages), cloneCoverageMessages(request.Messages), false, nil
		}
		covered := pendingForCoverageTarget(c.pending[request.Target])
		if len(covered) == 0 {
			return nil, nil, false, fmt.Errorf("%w: hold target has no Pending Messages", ErrCoverageRequestInvalid)
		}
		limit := normalizeCoverageLimit(request.Limit)
		presented := covered
		if len(presented) > limit {
			presented = presented[len(presented)-limit:]
		}
		return covered, cloneCoverageMessages(presented), false, nil

	case CoverageRead:
		if request.Target == "" || request.ThroughSeq <= 0 || len(request.Messages) == 0 {
			return nil, nil, false, fmt.Errorf("%w: read coverage requires target, positive through sequence, and Messages", ErrCoverageRequestInvalid)
		}
		return cloneCoverageMessages(request.Messages), cloneCoverageMessages(request.Messages), false, nil

	default:
		return nil, nil, false, fmt.Errorf("%w: unsupported kind %q", ErrCoverageRequestInvalid, request.Kind)
	}
}

// CommitCoverage durably advances the represented target boundaries and then
// removes only the exact still-matching Pending identities. Duplicate commit
// of the same live receipt is idempotent.
func (c *MessageCoordinator) CommitCoverage(receiptID string) error {
	if c == nil {
		return ErrCoverageReceiptInvalid
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return ErrCoverageReceiptInvalid
	}

	c.boundaryCommitMu.Lock()
	defer c.boundaryCommitMu.Unlock()

	c.mu.Lock()
	receipt := c.coverageReceipts[receiptID]
	if receipt == nil || receipt.id != receiptID || receipt.key != c.key {
		c.mu.Unlock()
		return ErrCoverageReceiptInvalid
	}
	if !c.coverageNow().Before(receipt.expiresAt) {
		delete(c.coverageReceipts, receiptID)
		c.mu.Unlock()
		return ErrCoverageReceiptExpired
	}
	if receipt.phase == coverageReceiptCommitted {
		c.mu.Unlock()
		return nil
	}
	if c.closed {
		c.mu.Unlock()
		return ErrCoverageReceiptInvalid
	}
	if receipt.phase != coverageReceiptPrepared {
		c.mu.Unlock()
		return ErrCoverageReceiptInProgress
	}
	if !c.coverageReceiptContentsCurrentLocked(receipt) {
		delete(c.coverageReceipts, receiptID)
		c.mu.Unlock()
		return ErrCoverageReceiptInvalid
	}
	next := cloneBoundaries(c.boundaries)
	for target, sequence := range receipt.proposedBoundaries {
		if sequence > next[target] {
			next[target] = sequence
		}
	}
	receipt.phase = coverageReceiptCommitting
	boundaryPath := filepath.Join(c.root, consumedSeqsFileName)
	c.mu.Unlock()

	writeErr := c.writeBoundaries(boundaryPath, next)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.coverageReceipts[receiptID] != receipt || receipt.phase != coverageReceiptCommitting || c.closed {
		return ErrCoverageReceiptInvalid
	}
	if writeErr != nil {
		receipt.phase = coverageReceiptPrepared
		c.boundaryHealthy = false
		return fmt.Errorf("persist Context Boundary after coverage commit: %w", writeErr)
	}
	c.boundaries = next
	c.boundaryHealthy = true
	pendingChanged := false
	for target, bySequence := range receipt.coveredIdentities {
		for sequence, identity := range bySequence {
			message, found := c.pending[target][sequence]
			if !found || messageIdentityKey(message) != identity {
				continue
			}
			delete(c.pending[target], sequence)
			delete(c.accepted, identity)
			pendingChanged = true
		}
		if len(c.pending[target]) == 0 {
			delete(c.pending, target)
		}
	}
	if pendingChanged {
		c.pendingGeneration++
	}
	receipt.phase = coverageReceiptCommitted
	return nil
}

func (c *MessageCoordinator) coverageReceiptContentsCurrentLocked(receipt *coverageReceipt) bool {
	for target, bySequence := range receipt.coveredIdentities {
		for sequence, identity := range bySequence {
			message, found := c.pending[target][sequence]
			if found && messageIdentityKey(message) != identity {
				return false
			}
			if !found && receipt.requiresPending && c.boundaries[target] < sequence {
				return false
			}
		}
	}
	return true
}

func (c *MessageCoordinator) expireCoverageReceiptsLocked(now time.Time) {
	for id, receipt := range c.coverageReceipts {
		if receipt.phase != coverageReceiptCommitting && !now.Before(receipt.expiresAt) {
			delete(c.coverageReceipts, id)
		}
	}
}

func (c *MessageCoordinator) reserveCoverageReceiptCapacityLocked() error {
	capacity := c.coverageCapacity
	if capacity < 1 {
		return ErrCoverageReceiptCapacity
	}
	for len(c.coverageReceipts) >= capacity {
		var oldest *coverageReceipt
		for _, receipt := range c.coverageReceipts {
			if receipt.phase == coverageReceiptCommitting {
				continue
			}
			if oldest == nil || coverageReceiptEvictionLess(receipt, oldest) {
				oldest = receipt
			}
		}
		if oldest == nil {
			return ErrCoverageReceiptCapacity
		}
		delete(c.coverageReceipts, oldest.id)
	}
	return nil
}

func coverageReceiptEvictionLess(candidate, current *coverageReceipt) bool {
	if candidate.phase != current.phase {
		return candidate.phase == coverageReceiptPrepared
	}
	return candidate.createdAt.Before(current.createdAt) || (candidate.createdAt.Equal(current.createdAt) && candidate.id < current.id)
}

func buildCoverageIdentity(messages []protocol.AgentMessageProjection, request CoverageRequest) (map[string]map[int64]string, map[string]int64, error) {
	covered := make(map[string]map[int64]string)
	boundaries := make(map[string]int64)
	for _, message := range messages {
		target := strings.TrimSpace(message.Target)
		if target == "" || message.Seq <= 0 || strings.TrimSpace(message.ID) == "" {
			return nil, nil, fmt.Errorf("%w: covered Message identity is incomplete", ErrCoverageRequestInvalid)
		}
		if request.Target != "" && target != request.Target {
			return nil, nil, fmt.Errorf("%w: covered Message target %q does not match %q", ErrCoverageRequestInvalid, target, request.Target)
		}
		if covered[target] == nil {
			covered[target] = make(map[int64]string)
		}
		identity := messageIdentityKey(message)
		if existing, found := covered[target][message.Seq]; found && existing != identity {
			return nil, nil, fmt.Errorf("%w: target %q sequence %d has conflicting identities", ErrCoverageRequestInvalid, target, message.Seq)
		}
		covered[target][message.Seq] = identity
		if message.Seq > boundaries[target] {
			boundaries[target] = message.Seq
		}
	}
	if (request.Kind == CoverageRead || (request.Kind == CoverageHold && len(request.Messages) > 0)) && boundaries[request.Target] != request.ThroughSeq {
		return nil, nil, fmt.Errorf("%w: through sequence does not match covered Messages", ErrCoverageRequestInvalid)
	}
	return covered, boundaries, nil
}

func normalizeCoverageLimit(limit int) int {
	if limit <= 0 {
		return messageCheckDefaultLimit
	}
	if limit > messageCheckMaxLimit {
		return messageCheckMaxLimit
	}
	return limit
}

func pendingForCoverageTarget(bySequence map[int64]protocol.AgentMessageProjection) []protocol.AgentMessageProjection {
	sequences := make([]int64, 0, len(bySequence))
	for sequence := range bySequence {
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	messages := make([]protocol.AgentMessageProjection, 0, len(sequences))
	for _, sequence := range sequences {
		messages = append(messages, bySequence[sequence])
	}
	return messages
}

func cloneCoverageMessages(messages []protocol.AgentMessageProjection) []protocol.AgentMessageProjection {
	if len(messages) == 0 {
		return []protocol.AgentMessageProjection{}
	}
	return append([]protocol.AgentMessageProjection(nil), messages...)
}
