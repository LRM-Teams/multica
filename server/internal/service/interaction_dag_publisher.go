// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SegmentPublishStatus enumerates the publish lifecycle shared by
// interaction_dag_segment.publish_status and the outbox row status. The
// transitions are enforced database-side by migration 454.
type SegmentPublishStatus string

const (
	SegmentPending         SegmentPublishStatus = "pending"
	SegmentProcessing      SegmentPublishStatus = "processing"
	SegmentPublished       SegmentPublishStatus = "published"
	SegmentRedactionFailed SegmentPublishStatus = "redaction_failed"
	SegmentRejectedScope   SegmentPublishStatus = "rejected_scope"
	SegmentDeadLetter      SegmentPublishStatus = "dead_letter"
	SegmentRetracted       SegmentPublishStatus = "retracted"
	SegmentRetry           SegmentPublishStatus = "retry"
)

// Publish failure sentinels. They classify the outcome of a sink invocation;
// callers wrap them with %w so classification survives wrapping.
var (
	// ErrDAGPublishTransient marks provider/storage failures that are safe to
	// retry under the exponential backoff policy.
	ErrDAGPublishTransient = errors.New("universal DAG publish transient failure")
	// ErrDAGPublishRedaction marks deterministic sanitizer/schema failures.
	// They never consume a retry.
	ErrDAGPublishRedaction = errors.New("universal DAG publish redaction failure")
	// ErrDAGPublishScope marks authorization/scope violations.
	ErrDAGPublishScope = errors.New("universal DAG publish scope rejection")
	// ErrDAGPublishReplayTerminal is returned when replay targets a terminal
	// outbox row. Migration 454 makes terminal (segment, outbox) pairs
	// immutable, so recovery requires a fresh canonical generation through the
	// boundary recorder rather than an in-place replay.
	ErrDAGPublishReplayTerminal = errors.New("universal DAG publish replay target is terminal")
	// ErrDAGPublishReplayLeased is returned when replay targets a row that is
	// actively leased by another worker.
	ErrDAGPublishReplayLeased = errors.New("universal DAG publish replay target is leased")
)

const (
	// interactionDAGPublishMaxAttempts is the retry-policy cap from spec 7.2:
	// at most ten exponential-backoff attempts within a 24h window.
	interactionDAGPublishMaxAttempts = 10
	// interactionDAGPublishBackoffBase is the first backoff interval; each
	// further retry doubles it (1m, 2m, ... 512m; cumulative ~17h < 24h).
	interactionDAGPublishBackoffBase = time.Minute
	// interactionDAGPublishLeaseTTL bounds one claim; expired leases are
	// reclaimed by the next claim without consuming an attempt.
	interactionDAGPublishLeaseTTL = 5 * time.Minute
	// interactionDAGPublishLastErrorCap keeps last_error bounded; it never
	// carries message content, only classified error classes.
	interactionDAGPublishLastErrorCap = 500
)

// InteractionDAGPublishClaim is one leased publish request handed to a sink.
// Tasks 6+ extend the publish transaction by loading the canonical
// task_messages range [StartSeq, EndSeq] through the supplied queries; the
// *_at_event scope fields are the Segment's frozen event-time facts and are
// the only scope the atom projection may inherit (spec 8.2).
type InteractionDAGPublishClaim struct {
	WorkspaceID                    string
	SegmentID                      string
	RequestHash                    string
	Attempts                       int32
	AgentRunID                     string
	Generation                     int64
	StartSeq                       int32
	EndSeq                         int32
	CloseActionKind                string
	MemoryTypeAtEvent              string
	GraphProjectionEligibleAtEvent bool
	Derivative                     bool
	TrainableEligible              bool
	ChannelIDAtEvent               string
	ProjectIDAtEvent               string
	RouteGenerationAtEvent         int64
}

// PublishSink runs inside the publish outcome transaction, after the payload
// has been sanitized. Everything it writes commits atomically with the publish
// lifecycle transitions, so a sink error can never leave a partial payload
// behind, and it never sees unredacted content (spec 7.1/7.3 ordering).
type PublishSink interface {
	PublishSegment(ctx context.Context, qtx *db.Queries, claim InteractionDAGPublishClaim, payload SanitizedTrajectory) error
}

// InteractionDAGPublishHealth aggregates outbox counters for Workspace
// health reporting (publish backlog, redaction failure, DLQ).
type InteractionDAGPublishHealth struct {
	Pending         int64 `json:"pending"`
	Leased          int64 `json:"leased"`
	StaleLeased     int64 `json:"stale_leased"`
	Retry           int64 `json:"retry"`
	Published       int64 `json:"published"`
	RedactionFailed int64 `json:"redaction_failed"`
	RejectedScope   int64 `json:"rejected_scope"`
	DeadLetter      int64 `json:"dead_letter"`
	Retracted       int64 `json:"retracted"`
	// Backlog counts every non-terminal row: pending, retry, and processing.
	Backlog int64 `json:"backlog"`
}

// InteractionDAGPublisher drains interaction_dag_publish_outbox. Each claim is
// a lease (FOR UPDATE SKIP LOCKED); the outcome transaction re-verifies lease
// ownership before transitioning, so a stolen lease discards stale outcomes.
type InteractionDAGPublisher struct {
	pool         *pgxpool.Pool
	workerID     string
	leaseTTL     time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	sink         PublishSink
	policy       SanitizerPolicy
	sanitize     interactionDAGSanitizeFunc
	atomizer     *GraphMemoryAtomizer
	publishClock func() time.Time
}

// interactionDAGSanitizeFunc is the sanitizer seam; tests override it to drive
// deterministic failures through the real publish path.
type interactionDAGSanitizeFunc func(messages []db.TaskMessage, policy SanitizerPolicy) (SanitizedTrajectory, error)

// InteractionDAGPublisherOption customizes publisher construction.
type InteractionDAGPublisherOption func(*InteractionDAGPublisher)

// WithInteractionDAGPublishSink overrides the payload sink.
func WithInteractionDAGPublishSink(sink PublishSink) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) { p.sink = sink }
}

// WithInteractionDAGWorkerID pins the lease owner identity.
func WithInteractionDAGWorkerID(workerID string) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) {
		if workerID != "" {
			p.workerID = workerID
		}
	}
}

// WithInteractionDAGPublishClock overrides the scheduling clock for tests.
func WithInteractionDAGPublishClock(clock func() time.Time) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) {
		if clock != nil {
			p.publishClock = clock
		}
	}
}

func defaultInteractionDAGWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("dag-publish-%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
}

// WithInteractionDAGPublishPolicy overrides the sanitizer policy (e.g. a
// smaller size cap in tests).
func WithInteractionDAGPublishPolicy(policy SanitizerPolicy) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) { p.policy = policy }
}

// WithInteractionDAGSanitizer overrides the sanitize seam.
func WithInteractionDAGSanitizer(fn interactionDAGSanitizeFunc) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) {
		if fn != nil {
			p.sanitize = fn
		}
	}
}

// WithInteractionDAGAtomizer overrides the memory-atom extraction seam (e.g.
// an LLM proposer resolved through the Task 4A policy resolver).
func WithInteractionDAGAtomizer(atomizer *GraphMemoryAtomizer) InteractionDAGPublisherOption {
	return func(p *InteractionDAGPublisher) {
		if atomizer != nil {
			p.atomizer = atomizer
		}
	}
}

// NewInteractionDAGPublisher constructs a publisher over the canonical
// migration 454 outbox. The sanitize stage always runs; a nil sink performs
// no pipeline-external work.
func NewInteractionDAGPublisher(pool *pgxpool.Pool, options ...InteractionDAGPublisherOption) *InteractionDAGPublisher {
	p := &InteractionDAGPublisher{
		pool:         pool,
		workerID:     defaultInteractionDAGWorkerID(),
		leaseTTL:     interactionDAGPublishLeaseTTL,
		maxAttempts:  interactionDAGPublishMaxAttempts,
		backoffBase:  interactionDAGPublishBackoffBase,
		policy:       DefaultSanitizerPolicy(),
		sanitize:     SanitizeTrajectory,
		atomizer:     NewGraphMemoryAtomizer(nil),
		publishClock: time.Now,
	}
	for _, option := range options {
		option(p)
	}
	return p
}

// PublishClaim leases and drives at most limit outbox rows to their next
// lifecycle state. It returns how many rows were claimed and processed; rows
// that end in retry or a terminal failure state still count as processed.
func (p *InteractionDAGPublisher) PublishClaim(ctx context.Context, limit int) (int, error) {
	if p == nil || p.pool == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 1
	}
	now := p.publishClock().UTC()
	claims, err := p.claim(ctx, int32(limit), now)
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		if err := p.process(ctx, claim, now); err != nil {
			return len(claims), err
		}
	}
	return len(claims), nil
}

func (p *InteractionDAGPublisher) claim(ctx context.Context, limit int32, now time.Time) ([]InteractionDAGPublishClaim, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin publish claim: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	rows, err := qtx.ClaimUniversalDAGPublishOutbox(ctx, db.ClaimUniversalDAGPublishOutboxParams{
		MaxRows:        limit,
		LeaseOwner:     pgtype.Text{String: p.workerID, Valid: true},
		LeaseExpiresAt: pgtype.Timestamptz{Time: now.Add(p.leaseTTL), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("claim publish outbox: %w", err)
	}
	claims := make([]InteractionDAGPublishClaim, 0, len(rows))
	for _, row := range rows {
		// The Segment publish lifecycle moves in the same transaction as the
		// outbox lease; zero rows affected means this claim stole a stale
		// lease onto a Segment that is already processing.
		if _, err := qtx.MarkUniversalDAGSegmentPublishProcessing(ctx, db.MarkUniversalDAGSegmentPublishProcessingParams{
			WorkspaceID: row.WorkspaceID, SegmentID: row.SegmentID,
		}); err != nil {
			return nil, fmt.Errorf("mark segment processing %s: %w", row.SegmentID, err)
		}
		claims = append(claims, InteractionDAGPublishClaim{
			WorkspaceID:                    row.WorkspaceID.String(),
			SegmentID:                      row.SegmentID,
			RequestHash:                    row.RequestHash,
			Attempts:                       row.Attempts,
			AgentRunID:                     row.AgentRunID.String(),
			Generation:                     row.Generation,
			StartSeq:                       row.StartSeq,
			EndSeq:                         row.EndSeq,
			CloseActionKind:                row.CloseActionKind.String,
			MemoryTypeAtEvent:              row.MemoryTypeAtEvent,
			GraphProjectionEligibleAtEvent: row.GraphProjectionEligibleAtEvent,
			Derivative:                     row.Derivative,
			TrainableEligible:              row.TrainableEligible,
			ChannelIDAtEvent:               row.ChannelIDAtEvent.String(),
			ProjectIDAtEvent:               row.ProjectIDAtEvent.String(),
			RouteGenerationAtEvent:         row.RouteGenerationAtEvent.Int64,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit publish claim: %w", err)
	}
	return claims, nil
}

func (p *InteractionDAGPublisher) process(ctx context.Context, claim InteractionDAGPublishClaim, now time.Time) error {
	workspaceID, err := util.ParseUUID(claim.WorkspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace %s: %w", claim.WorkspaceID, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publish outcome %s: %w", claim.SegmentID, err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)

	row, err := qtx.GetUniversalDAGPublishOutboxForUpdate(ctx, db.GetUniversalDAGPublishOutboxForUpdateParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock outbox row %s: %w", claim.SegmentID, err)
	}
	if !p.ownsLease(row, now) {
		// The lease was stolen after TTL; the current owner re-ran the work,
		// so this outcome is discarded without touching the row.
		return nil
	}

	// The payload is sanitized before anything pipeline-external runs and
	// before any body exists: a redaction failure leaves a metadata-only
	// redaction_failed Segment (spec 7.1/7.2).
	payload, trajectory, err := p.preparePayload(ctx, qtx, claim)
	if err != nil {
		tx.Rollback(ctx)
		return p.fail(ctx, workspaceID, claim, err, now)
	}

	// Atoms are extracted from the sanitized payload before the sink runs, so
	// extraction never sees unredacted content. Scope and eligibility come from
	// the claim's frozen event-time facts (spec 8.2).
	atoms, err := p.atomizer.ExtractAtoms(ctx, p.atomizerSegment(claim), payload)
	if err != nil {
		tx.Rollback(ctx)
		return p.fail(ctx, workspaceID, claim, err, now)
	}

	if p.sink != nil {
		if err := p.sink.PublishSegment(ctx, qtx, claim, payload); err != nil {
			tx.Rollback(ctx)
			return p.fail(ctx, workspaceID, claim, err, now)
		}
	}

	// publish_seq is allocated inside the publish transaction and only after
	// the sink succeeded, so the read path never observes a sequence for
	// content that failed to publish (spec AC: sequence after commit). The
	// sanitized payload becomes durable in this same statement.
	if err := qtx.LockUniversalDAGPublishSequence(ctx, pgtype.Text{String: claim.WorkspaceID, Valid: true}); err != nil {
		return fmt.Errorf("lock publish sequence %s: %w", claim.SegmentID, err)
	}
	nextSeq, err := qtx.NextUniversalDAGPublishSeq(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("allocate publish sequence %s: %w", claim.SegmentID, err)
	}
	if _, err := qtx.PublishUniversalDAGPublishOutbox(ctx, db.PublishUniversalDAGPublishOutboxParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	}); err != nil {
		return fmt.Errorf("publish outbox row %s: %w", claim.SegmentID, err)
	}
	if _, err := qtx.PublishUniversalDAGSegment(ctx, db.PublishUniversalDAGSegmentParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
		PublishSeq:       pgtype.Int8{Int64: int64(nextSeq), Valid: true},
		Trajectory:       trajectory,
		SanitizerVersion: payload.SanitizerVersion,
		PolicyVersion:    p.policy.resolved().PolicyVersion,
	}); err != nil {
		return fmt.Errorf("publish segment %s: %w", claim.SegmentID, err)
	}
	// Atoms and the durable graph projection request commit in this same
	// transaction, after the Segment became readable as published: if any
	// write fails, none of payload, atoms, or the request become visible
	// (Task 7 single-publish-transaction rule). A segment with zero atoms
	// enqueues no request — Task 8 only ever claims requests, never scans.
	if err := p.persistAtoms(ctx, qtx, workspaceID, claim, atoms, int64(nextSeq)); err != nil {
		return fmt.Errorf("persist memory atoms %s: %w", claim.SegmentID, err)
	}
	// Task 8A: the same transaction maintains the retraction fence — a guard
	// row for the segment's canonical task_output source, and the reverse
	// provenance from that source to every atom it produced. A later
	// retraction of the task quarantines exactly this closure.
	if err := p.persistFence(ctx, qtx, workspaceID, claim, atoms); err != nil {
		return fmt.Errorf("persist memory fence %s: %w", claim.SegmentID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publish outcome %s: %w", claim.SegmentID, err)
	}
	return nil
}

// persistFence upserts the source guard and atom provenance rows so the
// retraction fence covers future publishes without a backfill.
func (p *InteractionDAGPublisher) persistFence(
	ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID,
	claim InteractionDAGPublishClaim, atoms []memorygraph.Atom,
) error {
	if err := qtx.UpsertUniversalDAGSourceGuard(ctx, db.UpsertUniversalDAGSourceGuardParams{
		WorkspaceID: workspaceID, SourceID: claim.AgentRunID,
	}); err != nil {
		return fmt.Errorf("upsert source guard: %w", err)
	}
	if len(atoms) == 0 {
		return nil
	}
	ids := make([]string, 0, len(atoms))
	for _, atom := range atoms {
		ids = append(ids, atom.AtomID)
	}
	if _, err := qtx.UpsertUniversalDAGAtomProvenance(ctx, db.UpsertUniversalDAGAtomProvenanceParams{
		WorkspaceID: workspaceID, SourceID: claim.AgentRunID, AtomIds: ids,
	}); err != nil {
		return fmt.Errorf("upsert atom provenance: %w", err)
	}
	return nil
}

// atomizerSegment projects the claim's frozen event-time facts into the
// atomizer input shape.
func (p *InteractionDAGPublisher) atomizerSegment(claim InteractionDAGPublishClaim) AtomizerSegment {
	return AtomizerSegment{
		SegmentID:               claim.SegmentID,
		StartSeq:                claim.StartSeq,
		EndSeq:                  claim.EndSeq,
		MemoryTypeAtEvent:       claim.MemoryTypeAtEvent,
		GraphProjectionEligible: claim.GraphProjectionEligibleAtEvent,
		Derivative:              claim.Derivative,
		CloseActionKind:         claim.CloseActionKind,
		ChannelID:               claim.ChannelIDAtEvent,
		ProjectID:               claim.ProjectIDAtEvent,
	}
}

// persistAtoms writes the segment's atoms and, when any exist, the durable
// graph projection request. Atom identity is content-addressed and the
// projection request keyed by segment, so a re-publish after a stolen lease
// converges through ON CONFLICT DO NOTHING.
func (p *InteractionDAGPublisher) persistAtoms(
	ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID,
	claim InteractionDAGPublishClaim, atoms []memorygraph.Atom, publishSeq int64,
) error {
	if len(atoms) == 0 {
		return nil
	}
	var channelID, projectID pgtype.UUID
	if claim.ChannelIDAtEvent != "" {
		parsed, err := util.ParseUUID(claim.ChannelIDAtEvent)
		if err != nil {
			return fmt.Errorf("%w: atomize channel scope %s: %v", ErrDAGPublishScope, claim.SegmentID, err)
		}
		channelID = parsed
	}
	if claim.ProjectIDAtEvent != "" {
		parsed, err := util.ParseUUID(claim.ProjectIDAtEvent)
		if err != nil {
			return fmt.Errorf("%w: atomize project scope %s: %v", ErrDAGPublishScope, claim.SegmentID, err)
		}
		projectID = parsed
	}
	ids := make([]string, 0, len(atoms))
	for _, atom := range atoms {
		// The scope check in migration 466 makes visibility exclusive: a
		// channel atom carries only channel_id, a project atom only
		// project_id, whatever the segment's other scope column holds.
		atomChannel, atomProject := channelID, projectID
		if atom.Visibility == "channel" {
			atomProject = pgtype.UUID{}
		} else if atom.Visibility == "project" {
			atomChannel = pgtype.UUID{}
		}
		var artifactRef pgtype.Text
		if atom.ArtifactRef != "" {
			artifactRef = pgtype.Text{String: atom.ArtifactRef, Valid: true}
		}
		if _, err := qtx.InsertGraphMemoryAtom(ctx, db.InsertGraphMemoryAtomParams{
			WorkspaceID:       workspaceID,
			AtomID:            atom.AtomID,
			SegmentID:         atom.SegmentID,
			Body:              atom.Body,
			Kind:              atom.Kind,
			SourceMessageSeqs: atom.SourceMessageSeqs,
			SourceTool:        atom.SourceTool,
			ToolTrustClass:    atom.ToolTrustClass,
			ContentHash:       atom.ContentHash,
			ArtifactRef:       artifactRef,
			Visibility:        atom.Visibility,
			ChannelID:         atomChannel,
			ProjectID:         atomProject,
			PublishSeq:        publishSeq,
		}); err != nil {
			return fmt.Errorf("insert atom %s: %w", atom.AtomID, err)
		}
		ids = append(ids, atom.AtomID)
	}
	var routeGeneration pgtype.Int8
	if claim.RouteGenerationAtEvent > 0 {
		routeGeneration = pgtype.Int8{Int64: claim.RouteGenerationAtEvent, Valid: true}
	}
	if _, err := qtx.EnqueueGraphMemoryProjection(ctx, db.EnqueueGraphMemoryProjectionParams{
		WorkspaceID:     workspaceID,
		SegmentID:       claim.SegmentID,
		RequestHash:     projectionRequestHash(claim.SegmentID, publishSeq, ids),
		RouteGeneration: routeGeneration,
	}); err != nil {
		return fmt.Errorf("enqueue projection request: %w", err)
	}
	return nil
}

// projectionRequestHash derives the deterministic, content-free identity of
// one projection request: the segment, the allocated publish sequence, and
// the exact atom set it must project.
func projectionRequestHash(segmentID string, publishSeq int64, atomIDs []string) string {
	sorted := append([]string(nil), atomIDs...)
	sort.Strings(sorted)
	parts := make([]string, 0, len(sorted)+2)
	parts = append(parts, segmentID, strconv.FormatInt(publishSeq, 10))
	parts = append(parts, sorted...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// preparePayload loads the claim's canonical task_messages range and runs the
// deterministic sanitizer. Metadata-only closures carry no messages and stay
// empty. The returned trajectory document is what the publish statement
// persists; it never contains unredacted content.
func (p *InteractionDAGPublisher) preparePayload(
	ctx context.Context, qtx *db.Queries, claim InteractionDAGPublishClaim,
) (SanitizedTrajectory, []byte, error) {
	if claim.CloseActionKind == string(DAGCloseMetadataOnly) {
		empty := SanitizedTrajectory{
			Messages:         []SanitizedTaskMessage{},
			SanitizerVersion: p.policy.resolved().SanitizerVersion,
		}
		return empty, []byte("[]"), nil
	}
	messages, err := qtx.MessagesForTaskInRange(ctx, db.MessagesForTaskInRangeParams{
		TaskID: claim.AgentRunID, StartSeq: claim.StartSeq, EndSeq: claim.EndSeq,
	})
	if err != nil {
		return SanitizedTrajectory{}, nil, fmt.Errorf("load task_messages %s [%d,%d]: %w",
			claim.SegmentID, claim.StartSeq, claim.EndSeq, err)
	}
	if int32(len(messages)) != claim.EndSeq-claim.StartSeq+1 ||
		len(messages) == 0 ||
		messages[0].Seq != claim.StartSeq ||
		messages[len(messages)-1].Seq != claim.EndSeq {
		return SanitizedTrajectory{}, nil, fmt.Errorf(
			"%w: task_messages range [%d,%d] of segment %s is not exactly covered",
			ErrDAGPublishRedaction, claim.StartSeq, claim.EndSeq, claim.SegmentID)
	}
	payload, err := p.sanitize(messages, p.policy)
	if err != nil {
		return SanitizedTrajectory{}, nil, fmt.Errorf("sanitize segment %s: %w", claim.SegmentID, err)
	}
	trajectory, err := json.Marshal(payload)
	if err != nil {
		return SanitizedTrajectory{}, nil, fmt.Errorf("%w: encode payload %s: %v", ErrDAGPublishRedaction, claim.SegmentID, err)
	}
	return payload, trajectory, nil
}

func (p *InteractionDAGPublisher) ownsLease(row db.InteractionDagPublishOutbox, now time.Time) bool {
	return row.Status == string(SegmentProcessing) &&
		row.LeaseOwner.Valid && row.LeaseOwner.String == p.workerID &&
		row.LeaseExpiresAt.Valid && row.LeaseExpiresAt.Time.After(now)
}

// fail applies the classified failure outcome in a fresh transaction. The row
// is re-locked and lease ownership re-verified, so a stolen lease discards
// this failure instead of overriding the current owner's outcome.
func (p *InteractionDAGPublisher) fail(ctx context.Context, workspaceID pgtype.UUID, claim InteractionDAGPublishClaim, cause error, now time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publish failure %s: %w", claim.SegmentID, err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	row, err := qtx.GetUniversalDAGPublishOutboxForUpdate(ctx, db.GetUniversalDAGPublishOutboxForUpdateParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock outbox row for failure %s: %w", claim.SegmentID, err)
	}
	if !p.ownsLease(row, now) {
		return nil
	}
	lastError := classifyPublishError(cause)

	switch {
	case errors.Is(cause, ErrDAGPublishRedaction):
		return p.applyTerminalFailure(ctx, tx, qtx, workspaceID, claim, string(SegmentRedactionFailed), lastError)
	case errors.Is(cause, ErrDAGPublishScope):
		return p.applyTerminalFailure(ctx, tx, qtx, workspaceID, claim, string(SegmentRejectedScope), lastError)
	default:
		if row.Attempts >= int32(p.maxAttempts) {
			return p.applyTerminalFailure(ctx, tx, qtx, workspaceID, claim, string(SegmentDeadLetter), lastError)
		}
		backoff := p.backoff(int(row.Attempts) + 1)
		if _, err := qtx.RetryUniversalDAGPublishOutbox(ctx, db.RetryUniversalDAGPublishOutboxParams{
			NextAttemptAt:   pgtype.Timestamptz{Time: now.Add(backoff), Valid: true},
			LastError:       pgtype.Text{String: lastError, Valid: true},
			WorkspaceID:     workspaceID,
			SegmentID:       claim.SegmentID,
			CurrentAttempts: row.Attempts,
		}); err != nil {
			return fmt.Errorf("retry outbox row %s: %w", claim.SegmentID, err)
		}
		if _, err := qtx.RetryUniversalDAGSegment(ctx, db.RetryUniversalDAGSegmentParams{
			WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
		}); err != nil {
			return fmt.Errorf("retry segment %s: %w", claim.SegmentID, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit publish failure %s: %w", claim.SegmentID, err)
		}
		return nil
	}
}

func (p *InteractionDAGPublisher) applyTerminalFailure(
	ctx context.Context, tx pgx.Tx, qtx *db.Queries, workspaceID pgtype.UUID,
	claim InteractionDAGPublishClaim, status, lastError string,
) error {
	if _, err := qtx.FailUniversalDAGPublishOutbox(ctx, db.FailUniversalDAGPublishOutboxParams{
		TerminalStatus: status,
		LastError:      pgtype.Text{String: lastError, Valid: true},
		WorkspaceID:    workspaceID, SegmentID: claim.SegmentID,
	}); err != nil {
		return fmt.Errorf("fail outbox row %s: %w", claim.SegmentID, err)
	}
	if _, err := qtx.FailUniversalDAGSegment(ctx, db.FailUniversalDAGSegmentParams{
		TerminalStatus: status,
		WorkspaceID:    workspaceID, SegmentID: claim.SegmentID,
	}); err != nil {
		return fmt.Errorf("fail segment %s: %w", claim.SegmentID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit terminal failure %s: %w", claim.SegmentID, err)
	}
	return nil
}

// backoff returns the wait before attempt n (1-based). The cap keeps the
// cumulative retry window inside the 24h policy bound.
func (p *InteractionDAGPublisher) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > p.maxAttempts {
		attempt = p.maxAttempts
	}
	return p.backoffBase * time.Duration(1<<uint(attempt-1))
}

// classifyPublishError reduces an error to a bounded, content-free summary
// for the outbox last_error column.
func classifyPublishError(err error) string {
	class := "transient"
	switch {
	case errors.Is(err, ErrDAGPublishRedaction):
		class = "redaction"
	case errors.Is(err, ErrDAGPublishScope):
		class = "scope"
	}
	detail := err.Error()
	if len(detail) > interactionDAGPublishLastErrorCap {
		detail = detail[:interactionDAGPublishLastErrorCap]
	}
	return class + ": " + detail
}

// ReplayDeadLetter requeues one outbox row for immediate processing. Pending
// and retrying rows converge on a claimable state (idempotent); terminal rows
// fail closed because migration 454 makes terminal pairs immutable; rows
// leased by a live worker are rejected.
func (p *InteractionDAGPublisher) ReplayDeadLetter(ctx context.Context, workspaceID, segmentID string) error {
	if p == nil || p.pool == nil {
		return errors.New("universal DAG publisher is not configured")
	}
	workspace, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace %s: %w", workspaceID, err)
	}
	queries := db.New(p.pool)
	row, err := queries.GetUniversalDAGPublishOutboxStatus(ctx, db.GetUniversalDAGPublishOutboxStatusParams{
		WorkspaceID: workspace, SegmentID: segmentID,
	})
	if err != nil {
		return fmt.Errorf("read publish outbox row %s: %w", segmentID, err)
	}
	switch row {
	case string(SegmentPending):
		return nil
	case string(SegmentRetry):
		if _, err := queries.RequeueUniversalDAGPublishOutbox(ctx, db.RequeueUniversalDAGPublishOutboxParams{
			WorkspaceID: workspace, SegmentID: segmentID,
		}); err != nil {
			return fmt.Errorf("requeue publish outbox row %s: %w", segmentID, err)
		}
		return nil
	case string(SegmentProcessing):
		// An expired lease is already reclaimable by the next claim; a live
		// lease belongs to the worker currently holding it.
		full, err := queries.GetUniversalDAGPublishOutboxForUpdate(ctx, db.GetUniversalDAGPublishOutboxForUpdateParams{
			WorkspaceID: workspace, SegmentID: segmentID,
		})
		if err != nil {
			return fmt.Errorf("read leased publish outbox row %s: %w", segmentID, err)
		}
		if full.LeaseExpiresAt.Valid && full.LeaseExpiresAt.Time.After(p.publishClock().UTC()) {
			return fmt.Errorf("%w: %s", ErrDAGPublishReplayLeased, segmentID)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s is %s", ErrDAGPublishReplayTerminal, segmentID, row)
	}
}

// PublishHealth aggregates outbox counters for Workspace health. The backlog
// covers every row that still owes work.
func (p *InteractionDAGPublisher) PublishHealth(ctx context.Context) (InteractionDAGPublishHealth, error) {
	if p == nil || p.pool == nil {
		return InteractionDAGPublishHealth{}, errors.New("universal DAG publisher is not configured")
	}
	row, err := db.New(p.pool).UniversalDAGPublishHealth(ctx)
	if err != nil {
		return InteractionDAGPublishHealth{}, fmt.Errorf("aggregate publish health: %w", err)
	}
	return InteractionDAGPublishHealth{
		Pending:         row.PendingCount,
		Leased:          row.LeasedCount,
		StaleLeased:     row.StaleLeasedCount,
		Retry:           row.RetryCount,
		Published:       row.PublishedCount,
		RedactionFailed: row.RedactionFailedCount,
		RejectedScope:   row.RejectedScopeCount,
		DeadLetter:      row.DeadLetterCount,
		Retracted:       row.RetractedCount,
		Backlog:         row.PendingCount + row.RetryCount + row.LeasedCount + row.StaleLeasedCount,
	}, nil
}
