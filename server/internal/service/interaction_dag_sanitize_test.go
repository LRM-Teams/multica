// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sanitizeMsg builds one task_message with every six-field value populated.
func sanitizeMsg(seq int32, mtype, tool, content, input, output string) db.TaskMessage {
	msg := db.TaskMessage{Seq: seq, Type: mtype}
	if tool != "" {
		msg.Tool = pgtype.Text{String: tool, Valid: true}
	}
	if content != "" {
		msg.Content = pgtype.Text{String: content, Valid: true}
	}
	if input != "" {
		msg.Input = []byte(input)
	}
	if output != "" {
		msg.Output = pgtype.Text{String: output, Valid: true}
	}
	return msg
}

func TestSanitizeTrajectory_RedactsCredentialsAcrossAllFields(t *testing.T) {
	msg := sanitizeMsg(1, "user", "shell",
		"deploy using AKIAIOSFODNN7EXAMPLE and the ssh key",
		`{"cmd":"export DASHBOARD_API_KEY=sk-abcdefghijklmnopqrstuvwxyz012345"}`,
		"push with ghp_16C7e42F292c6912E7710c838347Ae178B4aG")

	out, err := SanitizeTrajectory([]db.TaskMessage{msg}, DefaultSanitizerPolicy())
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	body := string(raw)
	assert.NotContains(t, body, "AKIAIOSFODNN7EXAMPLE", "AWS key id must never survive sanitization")
	assert.NotContains(t, body, "sk-abcdefghijklmnopqrstuvwxyz012345", "api key must never survive sanitization")
	assert.NotContains(t, body, "ghp_16C7e42F292c6912E7710c838347Ae178B4aG", "github token must never survive sanitization")

	sanitized := out.Messages[0]
	assert.Contains(t, sanitized.Content, "[REDACTED")
	assert.Contains(t, sanitized.Input, "[REDACTED")
	assert.Contains(t, sanitized.Output, "[REDACTED")
	// The six-field shape survives with identity intact.
	assert.Equal(t, int32(1), sanitized.Sequence)
	assert.Equal(t, "user", sanitized.Type)
	assert.Equal(t, "shell", sanitized.Tool)
}

func TestSanitizeTrajectory_RejectsBinaryPayloadsWithoutLeakingBytes(t *testing.T) {
	binary := append([]byte{0x00, 0x01, 0x02, 0xfe, 0xff}, make([]byte, 128)...)
	msg := db.TaskMessage{
		Seq: 1, Type: "assistant",
		Output: pgtype.Text{String: string(binary), Valid: true},
	}

	out, err := SanitizeTrajectory([]db.TaskMessage{msg}, DefaultSanitizerPolicy())
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)

	rejected := out.Messages[0].Output
	assert.True(t, utf8.ValidString(rejected), "sanitized fields must be valid UTF-8")
	assert.NotContains(t, rejected, "\x00", "binary bytes must never survive sanitization")
	assert.NotContains(t, rejected, "\xfe", "binary bytes must never survive sanitization")
	assert.Contains(t, rejected, "BINARY", "a rejected binary payload is replaced by an explicit marker")
	assert.Empty(t, out.ArtifactRefs, "rejected binaries are not externalized, they are dropped")
}

func TestSanitizeTrajectory_RedactsShellEnvDump(t *testing.T) {
	dump := "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n" +
		"PATH=/usr/local/bin:/usr/bin\n" +
		"DATABASE_URL=postgres://app:supersecret@db.internal:5432/prod\n"

	out, err := SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(1, "assistant", "", dump, "", ""),
	}, DefaultSanitizerPolicy())
	require.NoError(t, err)

	body := out.Messages[0].Content
	assert.NotContains(t, body, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	assert.NotContains(t, body, "supersecret")
	assert.Contains(t, body, "PATH=/usr/local/bin:/usr/bin", "non-secret env lines survive")
	assert.Contains(t, body, "[REDACTED")
}

func TestSanitizeTrajectory_ExternalizesOversizedFieldsAsDeterministicArtifactRefs(t *testing.T) {
	policy := DefaultSanitizerPolicy()
	policy.MaxFieldBytes = 32
	big := strings.Repeat("A", 100)
	msg := sanitizeMsg(1, "assistant", "", "small", "", big)

	first, err := SanitizeTrajectory([]db.TaskMessage{msg}, policy)
	require.NoError(t, err)
	require.Len(t, first.ArtifactRefs, 1, "one oversized field externalizes exactly one artifact")

	externalized := first.Messages[0].Output
	assert.NotContains(t, externalized, "AAAA", "oversized content is never inlined")
	assert.Contains(t, externalized, "artifact:sha256:", "the placeholder names its content-addressed ref")
	assert.Equal(t, "small", first.Messages[0].Content, "small fields stay inline")

	// Retry-stable: the same content always yields the same ref and hash.
	second, err := SanitizeTrajectory([]db.TaskMessage{msg}, policy)
	require.NoError(t, err)
	assert.Equal(t, first.ArtifactRefs, second.ArtifactRefs)
	assert.Equal(t, first.ContentHash, second.ContentHash)

	// Content-addressed: different bytes mean a different ref.
	other, err := SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(1, "assistant", "", "small", "", strings.Repeat("B", 100)),
	}, policy)
	require.NoError(t, err)
	assert.NotEqual(t, first.ArtifactRefs, other.ArtifactRefs)
}

func TestSanitizeTrajectory_HashIsDeterministicAcrossRuns(t *testing.T) {
	msgs := []db.TaskMessage{
		sanitizeMsg(1, "user", "", "hello", "", ""),
		sanitizeMsg(2, "assistant", "", "", "", "hi"),
	}
	first, err := SanitizeTrajectory(msgs, DefaultSanitizerPolicy())
	require.NoError(t, err)
	second, err := SanitizeTrajectory(msgs, DefaultSanitizerPolicy())
	require.NoError(t, err)
	assert.Equal(t, first.ContentHash, second.ContentHash)
	assert.Equal(t, first.SanitizerVersion, second.SanitizerVersion)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, first.ContentHash)

	changed, err := SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(1, "user", "", "hello!", "", ""),
		sanitizeMsg(2, "assistant", "", "", "", "hi"),
	}, DefaultSanitizerPolicy())
	require.NoError(t, err)
	assert.NotEqual(t, first.ContentHash, changed.ContentHash, "any content change must change the hash")

	empty, err := SanitizeTrajectory(nil, DefaultSanitizerPolicy())
	require.NoError(t, err)
	sum := sha256.Sum256([]byte("[]"))
	assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), empty.ContentHash)
}

func TestSanitizeTrajectory_RejectsDiscontiguousSequences(t *testing.T) {
	_, err := SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(1, "user", "", "a", "", ""),
		sanitizeMsg(3, "assistant", "", "", "", "b"),
	}, DefaultSanitizerPolicy())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGPublishRedaction, "a structural range failure is deterministic")

	_, err = SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(2, "user", "", "a", "", ""),
		sanitizeMsg(1, "assistant", "", "", "", "b"),
	}, DefaultSanitizerPolicy())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGPublishRedaction)
}

func TestSanitizeTrajectory_FailsClosedOnPanic(t *testing.T) {
	out, err := sanitizeFailClosed(func() (SanitizedTrajectory, error) {
		panic("boom")
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDAGPublishRedaction, "a sanitizer panic must classify as redaction, never as transient")
	assert.Empty(t, out.Messages)
	assert.Empty(t, out.ContentHash)
}

func TestSanitizeTrajectory_AppliesPolicyDefaults(t *testing.T) {
	out, err := SanitizeTrajectory([]db.TaskMessage{
		sanitizeMsg(1, "user", "", "plain", "", ""),
	}, SanitizerPolicy{})
	require.NoError(t, err)
	assert.Equal(t, interactionDAGSanitizerVersion, out.SanitizerVersion)
	assert.Equal(t, interactionDAGSanitizerVersion, DefaultSanitizerPolicy().SanitizerVersion)
	assert.Equal(t, interactionDAGPolicyVersion, DefaultSanitizerPolicy().PolicyVersion)
	assert.Positive(t, DefaultSanitizerPolicy().MaxFieldBytes)
}

// --- publisher integration (real schema, real publish transaction) ---

// setTaskMessageContent rewrites the harness task's single message with the
// given six-field values.
func setTaskMessageContent(t *testing.T, h *universalDAGPublisherHarness, task db.AgentInboxEvent, content, input, output string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		UPDATE task_message
		SET content=$1, input=$2::jsonb, output=$3, type='user'
		WHERE task_id=$4`, content, input, output, task.ID)
	require.NoError(t, err, "seed task_message content")
}

func readPublishedTrajectory(t *testing.T, h *universalDAGPublisherHarness, segmentID string) (SanitizedTrajectory, string) {
	t.Helper()
	var raw []byte
	var sanitizerVersion, policyVersion string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT trajectory::text, COALESCE(sanitizer_version,''), COALESCE(policy_version,'')
		FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).
		Scan(&raw, &sanitizerVersion, &policyVersion))
	var parsed SanitizedTrajectory
	require.NoError(t, json.Unmarshal(raw, &parsed), "published trajectory must be the sanitized payload document")
	return parsed, sanitizerVersion + "|" + policyVersion
}

func TestInteractionDAGPublisher_PublishPersistsSanitizedPayloadOnly(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task,
		"deploy using AKIAIOSFODNN7EXAMPLE now",
		`{"cmd":"export API_KEY=sk-abcdefghijklmnopqrstuvwxyz012345"}`,
		"token ghp_16C7e42F292c6912E7710c838347Ae178B4aG")
	segmentID := h.recordMessageSegment(task, 1, "sanitize-keystone")

	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	payload, versions := readPublishedTrajectory(t, h, segmentID)
	require.Len(t, payload.Messages, 1)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, payload.ContentHash)
	assert.Equal(t, interactionDAGSanitizerVersion+"|"+interactionDAGPolicyVersion, versions)
	assert.NotContains(t, payload.Messages[0].Content, "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, payload.Messages[0].Input, "sk-abcdefghijklmnopqrstuvwxyz012345")
	assert.NotContains(t, payload.Messages[0].Output, "ghp_16C7e42F292c6912E7710c838347Ae178B4aG")

	// The durable row itself carries no unredacted pipeline payload (AC 8).
	var body string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT trajectory::text FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).Scan(&body))
	assert.NotContains(t, body, "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, body, "sk-abcdefghijklmnopqrstuvwxyz012345")
	assert.NotContains(t, body, "ghp_16C7e42F292c6912E7710c838347Ae178B4aG")

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentPublished), segment.publishStatus)
	assert.Equal(t, "published", segment.contentStatus)
	assert.Positive(t, segment.publishSeq)
}

func TestInteractionDAGPublisher_RedactionFailureLeavesMetadataOnlySegment(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task, "plain content", `{"a":1}`, "plain output")
	segmentID := h.recordMessageSegment(task, 1, "redaction-metadata-only")

	publisher := NewInteractionDAGPublisher(h.pubPool, WithInteractionDAGSanitizer(
		func([]db.TaskMessage, SanitizerPolicy) (SanitizedTrajectory, error) {
			return SanitizedTrajectory{}, fmt.Errorf("schema: %w", ErrDAGPublishRedaction)
		}))
	_, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)

	status, _, _, lastError, _, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentRedactionFailed), status)
	assert.Contains(t, lastError, "redaction")
	assert.True(t, completed)

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentRedactionFailed), segment.publishStatus)
	assert.Equal(t, "redaction_failed", segment.contentStatus)

	var body string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT trajectory::text FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).Scan(&body))
	assert.Equal(t, "[]", body, "a redaction failure stays metadata-only: no payload is persisted")
	assert.NotContains(t, body, "plain content")
}

func TestInteractionDAGPublisher_OversizedOutputIsExternalizedNotPersisted(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task, "keep me inline", `{"a":1}`, strings.Repeat("B", 4096))
	segmentID := h.recordMessageSegment(task, 1, "oversized-externalized")

	policy := DefaultSanitizerPolicy()
	policy.MaxFieldBytes = 64
	published, err := NewInteractionDAGPublisher(h.pubPool, WithInteractionDAGPublishPolicy(policy)).
		PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	payload, _ := readPublishedTrajectory(t, h, segmentID)
	require.Len(t, payload.Messages, 1)
	assert.Equal(t, "keep me inline", payload.Messages[0].Content)
	assert.Contains(t, payload.Messages[0].Output, "artifact:sha256:")
	assert.NotContains(t, payload.Messages[0].Output, "BBBB")
	require.Len(t, payload.ArtifactRefs, 1)
	assert.Regexp(t, `^artifact:sha256:[0-9a-f]{64}$`, payload.ArtifactRefs[0])

	var body string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT trajectory::text FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).Scan(&body))
	assert.NotContains(t, body, "BBBB", "the oversized body never enters the durable payload")
}

// The sanitizer runs before the sink: an external-model sink must never see
// unredacted content (spec 7.1/7.3 ordering).
func TestInteractionDAGPublisher_SanitizesBeforeSink(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h, task, "key AKIAIOSFODNN7EXAMPLE", `{"a":1}`, "out")
	segmentID := h.recordMessageSegment(task, 1, "sanitize-before-sink")

	var seen SanitizedTrajectory
	sink := &capturingSink{onPublish: func(_ context.Context, _ *db.Queries, _ InteractionDAGPublishClaim, payload SanitizedTrajectory) error {
		seen = payload
		return nil
	}}
	_, err := NewInteractionDAGPublisher(h.pubPool, WithInteractionDAGPublishSink(sink)).PublishClaim(h.ctx, 10)
	require.NoError(t, err)

	require.Len(t, seen.Messages, 1)
	assert.NotContains(t, seen.Messages[0].Content, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, seen.Messages[0].Content, "[REDACTED")
	_ = segmentID
}

type capturingSink struct {
	onPublish func(ctx context.Context, qtx *db.Queries, claim InteractionDAGPublishClaim, payload SanitizedTrajectory) error
}

func (s *capturingSink) PublishSegment(ctx context.Context, qtx *db.Queries, claim InteractionDAGPublishClaim, payload SanitizedTrajectory) error {
	return s.onPublish(ctx, qtx, claim, payload)
}
