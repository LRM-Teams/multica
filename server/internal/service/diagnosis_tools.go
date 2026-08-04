// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Budget constants mirroring evolution_review_provider.go:
// - maxDiagnosisMessageBytes mirrors maxEvolutionReviewFileBytes (8KB)
// - maxDiagnosisSegmentBudgetBytes mirrors maxEvolutionReviewContentBudgetBytes (24KB)
// - maxDiagnosisSegmentTurns mirrors maxEvolutionReviewListItems (20)
const (
	maxDiagnosisMessageBytes       = 8 * 1024
	maxDiagnosisSegmentBudgetBytes = 24 * 1024
	maxDiagnosisSegmentTurns       = 20
)

// SegmentRow represents a segment in the DAG
type SegmentRow struct {
	SegmentID  string
	AgentRunID string
	StartSeq   int32
	EndSeq     int32
}

// EdgeRow represents an edge between two segments
type EdgeRow struct {
	SrcSegmentID string
	DstSegmentID string
	Type         string
}

// MessageRow represents a truncated task message with truncation indicator
type MessageRow struct {
	Seq       int32
	Type      string
	Content   string
	Truncated bool
}

// TaskContext contains minimal task context (goal/gold when available)
type TaskContext struct {
	Goal        string
	GoldContext string
}

// GetInteractionDAG returns the interaction DAG segments and edges for a project,
// enforcing workspace scoping.
func GetInteractionDAG(
	ctx context.Context,
	store InteractionDAGStore,
	projectStore MessageStore,
	workspaceID pgtype.UUID,
	projectID pgtype.UUID,
) ([]SegmentRow, []EdgeRow, error) {
	// Verify the project belongs to the workspace
	_, err := projectStore.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, pgx.ErrNoRows
		}
		return nil, nil, err
	}

	projectIDStr := projectID.String()

	segments, err := store.ListInteractionDAGSegmentsForProject(ctx, projectIDStr)
	if err != nil {
		return nil, nil, err
	}

	edges, err := store.ListInteractionDAGEdgesForProject(ctx, projectIDStr)
	if err != nil {
		return nil, nil, err
	}

	// Convert to our row types
	segmentRows := make([]SegmentRow, 0, len(segments))
	for _, seg := range segments {
		segmentRows = append(segmentRows, SegmentRow{
			SegmentID:  seg.SegmentID,
			AgentRunID: seg.AgentRunID,
			StartSeq:   seg.StartSeq,
			EndSeq:     seg.EndSeq,
		})
	}

	edgeRows := make([]EdgeRow, 0, len(edges))
	for _, edge := range edges {
		edgeRows = append(edgeRows, EdgeRow{
			SrcSegmentID: edge.SrcSegmentID,
			DstSegmentID: edge.DstSegmentID,
			Type:         edge.Type,
		})
	}

	return segmentRows, edgeRows, nil
}

// GetSegmentMessages returns task messages for a segment, truncating to stay within budget,
// and enforcing workspace scoping.
func GetSegmentMessages(
	ctx context.Context,
	store InteractionDAGStore,
	messageStore MessageStore,
	workspaceID pgtype.UUID,
	segmentID string,
) ([]MessageRow, error) {
	// Get the segment
	segment, err := store.GetInteractionDAGSegmentByID(ctx, segmentID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}

	// Parse project ID from segment and verify it belongs to the workspace
	var projectID pgtype.UUID
	err = projectID.Scan(segment.ProjectID)
	if err != nil {
		return nil, err
	}

	_, err = messageStore.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}

	// Get messages for the task in the segment's seq range
	messages, err := messageStore.MessagesForTaskInRange(ctx, segment.AgentRunID, segment.StartSeq, segment.EndSeq)
	if err != nil {
		return nil, err
	}

	// Convert and truncate messages
	result := make([]MessageRow, 0, len(messages))
	totalBytes := 0
	turnCount := 0

	for _, msg := range messages {
		// Check if adding this message would exceed the turn budget
		if turnCount >= maxDiagnosisSegmentTurns {
			break
		}

		content := ""
		if msg.Content.Valid {
			content = msg.Content.String
		}

		truncated := false
		if len(content) > maxDiagnosisMessageBytes {
			content = truncateUTF8Bytes(content, maxDiagnosisMessageBytes)
			truncated = true
		}

		// Check if adding this message would exceed the total budget
		if totalBytes+len(content) > maxDiagnosisSegmentBudgetBytes {
			// Can't add this message without exceeding budget
			break
		}

		result = append(result, MessageRow{
			Seq:       msg.Seq,
			Type:      msg.Type,
			Content:   content,
			Truncated: truncated,
		})
		totalBytes += len(content)
		turnCount++
	}

	return result, nil
}

// GetTaskContext returns task context (goal/gold) for a task, enforcing workspace scoping.
// Goal is mapped from issue.description if present, otherwise issue.title.
// GoldContext is mapped from issue.acceptance_criteria if present, otherwise empty string.
func GetTaskContext(
	ctx context.Context,
	messageStore MessageStore,
	workspaceID pgtype.UUID,
	taskID string,
) (TaskContext, error) {
	issue, err := messageStore.GetIssueForTask(ctx, taskID)
	if err != nil {
		return TaskContext{}, err
	}

	// Enforce workspace scoping
	if issue.WorkspaceID != workspaceID {
		return TaskContext{}, pgx.ErrNoRows
	}

	// Map fields to TaskContext
	goal := ""
	if issue.Description.Valid && issue.Description.String != "" {
		goal = issue.Description.String
	} else {
		goal = issue.Title
	}

	goldContext := ""
	if len(issue.AcceptanceCriteria) > 0 {
		// acceptance_criteria is JSONB, convert to string
		goldContext = string(issue.AcceptanceCriteria)
	}

	return TaskContext{
		Goal:        goal,
		GoldContext: goldContext,
	}, nil
}

// ── Cursor-paginated segment messages (Task 2) ──

// DiagnosisMessagePager is the narrow query surface for paging segment messages.
// *db.Queries satisfies it in production.
type DiagnosisMessagePager interface {
	PageTaskMessagesInRange(ctx context.Context, arg db.PageTaskMessagesInRangeParams) ([]db.TaskMessage, error)
	CountTaskMessagesInRange(ctx context.Context, arg db.CountTaskMessagesInRangeParams) (int32, error)
}

var _ DiagnosisMessagePager = (*db.Queries)(nil)

// diagnosisCursorPayload is the HMAC-signed internal payload carried in opaque
// page cursors. The caller must never trust fields from cursor without verifying
// the HMAC.
type diagnosisCursorPayload struct {
	RunID     string `json:"r"`
	SegmentID string `json:"s"`
	LastSeq   int32  `json:"q"`
	LastID    string `json:"i"`
	PageSeq   int    `json:"p"`
}

// DiagnosisMessage is a single message surfaced to the diagnosis agent.
type DiagnosisMessage struct {
	Seq       int32  `json:"seq"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// SegmentMessagePage is one page of diagnosis segment messages.
type SegmentMessagePage struct {
	Messages      []DiagnosisMessage `json:"messages"`
	NextCursor    string             `json:"next_cursor"`
	FetchedCount  int                `json:"fetched_count"`
	ExpectedCount int                `json:"expected_count"`
	Complete      bool               `json:"complete"`
}

// encodeDiagnosisCursor signs and encodes a cursor payload. The key must be
// stable for the lifetime of one tool-server session.
func encodeDiagnosisCursor(payload diagnosisCursorPayload, key []byte) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode diagnosis cursor: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	sig := mac.Sum(nil)
	raw := make([]byte, 0, len(data)+sha256.Size)
	raw = append(raw, data...)
	raw = append(raw, sig...)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeDiagnosisCursor verifies and decodes an opaque cursor. Returns an error
// when the HMAC mismatches, the payload is malformed, or required fields are
// missing.
func decodeDiagnosisCursor(encoded string, key []byte) (diagnosisCursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return diagnosisCursorPayload{}, fmt.Errorf("decode diagnosis cursor: %w", err)
	}
	if len(raw) < sha256.Size {
		return diagnosisCursorPayload{}, fmt.Errorf("decode diagnosis cursor: payload too short")
	}
	data := raw[:len(raw)-sha256.Size]
	sig := raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return diagnosisCursorPayload{}, fmt.Errorf("decode diagnosis cursor: signature mismatch")
	}
	var payload diagnosisCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return diagnosisCursorPayload{}, fmt.Errorf("decode diagnosis cursor: %w", err)
	}
	if payload.SegmentID == "" {
		return diagnosisCursorPayload{}, fmt.Errorf("decode diagnosis cursor: missing segment_id")
	}
	return payload, nil
}

// diagnosisPagerKey is a per-session HMAC key for signing cursors. It will be
// supplied by DiagnosisToolServer at construction time; tests pass their own.
var diagnosisPagerKey func() []byte

// SetDiagnosisPagerKey installs the per-session cursor-signing key getter.
func SetDiagnosisPagerKey(getter func() []byte) { diagnosisPagerKey = getter }

// GetSegmentMessagePage returns one page of messages for a segment using
// keyset pagination. An empty cursor starts at the first message; subsequent
// pages pass the NextCursor from the prior response. The page is bounded by
// maxDiagnosisSegmentTurns (turns) and maxDiagnosisSegmentBudgetBytes (bytes);
// a single message larger than the byte budget is returned alone rather than
// split. Returns the page, the next opaque cursor, and whether all remaining
// messages have been returned.
func GetSegmentMessagePage(ctx context.Context, pager DiagnosisMessagePager, taskID, segmentID string, startSeq, endSeq int32, encodedCursor string) (SegmentMessagePage, error) {
	var key []byte
	if diagnosisPagerKey != nil {
		key = diagnosisPagerKey()
	}
	return GetSegmentMessagePageWithKey(ctx, pager, key, taskID, segmentID, startSeq, endSeq, encodedCursor)
}

// DiagnosisRunCursorKey derives the cursor-signing key for the network run
// API from the run's persisted capability token hash. Deriving (rather than
// storing) keeps cursors verifiable by any API replica for the run's
// lifetime without new secret material; an empty hash yields an unusable
// key because such runs have no capability token at all.
func DiagnosisRunCursorKey(capabilityTokenHash string) []byte {
	sum := sha256.Sum256([]byte("multica-diagnosis-run-cursor-v1:" + capabilityTokenHash))
	return sum[:]
}

// GetSegmentMessagePageWithKey is GetSegmentMessagePage with an explicit
// cursor-signing key. A nil key fails cursor decode/encode exactly like an
// uninitialised session key.
func GetSegmentMessagePageWithKey(ctx context.Context, pager DiagnosisMessagePager, key []byte, taskID, segmentID string, startSeq, endSeq int32, encodedCursor string) (SegmentMessagePage, error) {
	var lastSeq int32
	var lastID pgtype.UUID
	pageSeq := 0
	accumulated := 0

	if encodedCursor != "" {
		if key == nil {
			return SegmentMessagePage{}, fmt.Errorf("diagnosis cursor key not initialised")
		}
		payload, err := decodeDiagnosisCursor(encodedCursor, key)
		if err != nil {
			return SegmentMessagePage{}, err
		}
		if payload.SegmentID != segmentID {
			return SegmentMessagePage{}, fmt.Errorf("cursor segment mismatch: %s != %s", payload.SegmentID, segmentID)
		}
		lastSeq = payload.LastSeq
		if payload.LastID != "" {
			if err := lastID.Scan(payload.LastID); err != nil {
				return SegmentMessagePage{}, fmt.Errorf("cursor last_id: %w", err)
			}
		}
		pageSeq = payload.PageSeq
		accumulated = pageSeq * maxDiagnosisSegmentTurns
	}

	// Count total messages in range (no cursor filter).
	expected, err := pager.CountTaskMessagesInRange(ctx, db.CountTaskMessagesInRangeParams{
		TaskID:   taskID,
		StartSeq: startSeq,
		EndSeq:   endSeq,
	})
	if err != nil {
		return SegmentMessagePage{}, err
	}

	rows, err := pager.PageTaskMessagesInRange(ctx, db.PageTaskMessagesInRangeParams{
		TaskID:   taskID,
		StartSeq: startSeq,
		EndSeq:   endSeq,
		LastSeq:  lastSeq,
		LastID:   lastID,
		Limit:    maxDiagnosisSegmentTurns,
	})
	if err != nil {
		return SegmentMessagePage{}, err
	}

	msgs := make([]DiagnosisMessage, 0, len(rows))
	totalBytes := 0
	fetched := 0
	var lastRowSeq int32
	var lastRowID pgtype.UUID

	for _, row := range rows {
		content := ""
		if row.Content.Valid {
			content = row.Content.String
		}
		contentBytes := len(content)

		// If we already have messages and adding this one exceeds byte budget,
		// stop here (but never split a message).
		if len(msgs) > 0 && totalBytes+contentBytes > maxDiagnosisSegmentBudgetBytes {
			break
		}

		truncated := false
		if contentBytes > maxDiagnosisMessageBytes {
			content = truncateUTF8Bytes(content, maxDiagnosisMessageBytes)
			truncated = true
			contentBytes = len(content)
		}

		msgs = append(msgs, DiagnosisMessage{
			Seq:       row.Seq,
			Type:      row.Type,
			Content:   content,
			Truncated: truncated,
		})
		totalBytes += contentBytes
		fetched++
		lastRowSeq = row.Seq
		lastRowID = row.ID
	}

	accumulated += fetched
	// TODO(agent): latent bug (spec 005 T021 finding) — when the byte budget
	// cuts a page short (fewer than maxDiagnosisSegmentTurns messages
	// delivered), pageSeq*maxDiagnosisSegmentTurns over-counts prior pages, so
	// accumulated/FetchedCount is inflated and `complete` (and downstream
	// finish-segment coverage) can trigger before all messages are delivered.
	// Also, a tail cut inside the final sub-limit query (len(rows) < limit)
	// drops the remaining rows silently. Both transports share this behavior;
	// fix in a follow-up (track fetched per page instead of deriving it).
	complete := accumulated >= int(expected) || len(rows) < maxDiagnosisSegmentTurns

	nextCursor := ""
	if !complete {
		if key == nil {
			return SegmentMessagePage{}, fmt.Errorf("diagnosis cursor key not initialised")
		}
		lastIDStr := ""
		if lastRowID.Valid {
			lastIDStr = fmt.Sprintf("%x", lastRowID.Bytes)
		}
		encoded, err := encodeDiagnosisCursor(diagnosisCursorPayload{
			RunID:     "", // run_id is validated at the server layer, not cursor layer
			SegmentID: segmentID,
			LastSeq:   lastRowSeq,
			LastID:    lastIDStr,
			PageSeq:   pageSeq + 1,
		}, key)
		if err != nil {
			return SegmentMessagePage{}, err
		}
		nextCursor = encoded
	}

	return SegmentMessagePage{
		Messages:      msgs,
		NextCursor:    nextCursor,
		FetchedCount:  accumulated,
		ExpectedCount: int(expected),
		Complete:      complete,
	}, nil
}
