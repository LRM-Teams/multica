// SPDX-License-Identifier: Apache-2.0

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

// Sanitizer identity. Bumping the version re-keys every content hash, so it
// changes only with a deliberate policy revision, never incidentally.
const (
	interactionDAGSanitizerVersion = "sixfield-redact-v1"
	interactionDAGPolicyVersion    = "redact-default-v1"
	// interactionDAGMaxFieldBytes caps one six-field value before the payload
	// is externalized as a content-addressed artifact ref.
	interactionDAGMaxFieldBytes = 64 * 1024
	// interactionDAGBinaryControlRatio is the share of control bytes (outside
	// \n, \r, \t) above which otherwise-valid UTF-8 counts as binary.
	interactionDAGBinaryControlRatio = 10
)

// SanitizedTaskMessage is the allowlisted six-field projection of one
// task_message: sequence, type, tool, content, input, output. Every field has
// passed redaction, binary rejection, and the size cap, so the struct is safe
// to persist and to hand to pipeline-external providers.
type SanitizedTaskMessage struct {
	Sequence int32  `json:"sequence"`
	Type     string `json:"type"`
	Tool     string `json:"tool"`
	Content  string `json:"content"`
	Input    string `json:"input"`
	Output   string `json:"output"`
}

// SanitizedTrajectory is the durable publish payload for one Segment. The
// content hash covers the sanitized messages only, so it is deterministic for
// a given input and policy and stable across publish retries.
type SanitizedTrajectory struct {
	Messages         []SanitizedTaskMessage `json:"messages"`
	ContentHash      string                 `json:"content_hash"`
	SanitizerVersion string                 `json:"sanitizer_version"`
	ArtifactRefs     []string               `json:"artifact_refs,omitempty"`
}

// SanitizerPolicy parameterizes the deterministic sanitizer. The zero value
// resolves to the platform defaults.
type SanitizerPolicy struct {
	SanitizerVersion string
	PolicyVersion    string
	MaxFieldBytes    int
}

// DefaultSanitizerPolicy returns the platform redaction policy (spec 7.1/7.2).
func DefaultSanitizerPolicy() SanitizerPolicy {
	return SanitizerPolicy{
		SanitizerVersion: interactionDAGSanitizerVersion,
		PolicyVersion:    interactionDAGPolicyVersion,
		MaxFieldBytes:    interactionDAGMaxFieldBytes,
	}
}

func (p SanitizerPolicy) resolved() SanitizerPolicy {
	if p.SanitizerVersion == "" {
		p.SanitizerVersion = interactionDAGSanitizerVersion
	}
	if p.PolicyVersion == "" {
		p.PolicyVersion = interactionDAGPolicyVersion
	}
	if p.MaxFieldBytes <= 0 {
		p.MaxFieldBytes = interactionDAGMaxFieldBytes
	}
	return p
}

// sanitizeFailClosed runs one sanitizer body and converts any panic into a
// redaction-class error: a panicking sanitizer must never surface as a
// retryable transient or, worse, as a success with a partial payload.
func sanitizeFailClosed(body func() (SanitizedTrajectory, error)) (out SanitizedTrajectory, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = SanitizedTrajectory{}
			err = fmt.Errorf("%w: sanitizer panic: %v", ErrDAGPublishRedaction, recovered)
		}
	}()
	return body()
}

// SanitizeTrajectory turns the canonical task_messages of one Segment range
// into the publish payload: each six-field value is redacted, binary-rejected,
// and size-capped (oversized values are externalized as content-addressed
// artifact refs). Structural failures — sequence gaps or disorder — and any
// panic fail closed with an ErrDAGPublishRedaction-class error; the caller
// then records a metadata-only redaction_failed Segment and never persists a
// body. SanitizeTrajectory never returns unredacted content.
func SanitizeTrajectory(messages []db.TaskMessage, policy SanitizerPolicy) (SanitizedTrajectory, error) {
	return sanitizeFailClosed(func() (SanitizedTrajectory, error) {
		resolved := policy.resolved()
		out := SanitizedTrajectory{
			Messages:         make([]SanitizedTaskMessage, 0, len(messages)),
			SanitizerVersion: resolved.SanitizerVersion,
		}
		previous := int32(0)
		for index, message := range messages {
			if index > 0 && message.Seq != previous+1 {
				return SanitizedTrajectory{}, fmt.Errorf(
					"%w: task_message sequence %d does not continue %d",
					ErrDAGPublishRedaction, message.Seq, previous)
			}
			previous = message.Seq
			sanitized, refs := sanitizeTaskMessageFields(message, resolved)
			out.Messages = append(out.Messages, sanitized)
			out.ArtifactRefs = append(out.ArtifactRefs, refs...)
		}
		encoded, err := json.Marshal(out.Messages)
		if err != nil {
			return SanitizedTrajectory{}, fmt.Errorf("%w: encode sanitized messages: %v", ErrDAGPublishRedaction, err)
		}
		sum := sha256.Sum256(encoded)
		out.ContentHash = "sha256:" + hex.EncodeToString(sum[:])
		return out, nil
	})
}

// sanitizeTaskMessageFields applies the per-field pipeline — binary rejection,
// deterministic redaction, then size-cap externalization — to one message's
// six allowlisted fields. Oversized fields return their artifact refs so the
// caller can surface them on the payload document.
func sanitizeTaskMessageFields(message db.TaskMessage, policy SanitizerPolicy) (SanitizedTaskMessage, []string) {
	tool := nullableText(message.Tool)
	content := nullableText(message.Content)
	output := nullableText(message.Output)
	input := string(message.Input)

	sanitized := SanitizedTaskMessage{
		Sequence: message.Seq,
		Type:     message.Type,
		Tool:     sanitizeField(tool, policy),
		Content:  sanitizeField(content, policy),
		Input:    sanitizeField(input, policy),
		Output:   sanitizeField(output, policy),
	}

	var refs []string
	for _, field := range []*string{&sanitized.Tool, &sanitized.Content, &sanitized.Input, &sanitized.Output} {
		if ref, ok := artifactRefOf(*field); ok {
			refs = append(refs, ref)
		}
	}
	return sanitized, refs
}

// sanitizeField runs one field value through the deterministic gates.
func sanitizeField(value string, policy SanitizerPolicy) string {
	if value == "" {
		return ""
	}
	if isBinaryPayload(value) {
		// Binary is rejected outright, not externalized: there is no safe
		// text projection to redact, so no artifact is created either.
		return "[BINARY REJECTED: payload is not textual]"
	}
	redacted := redact.Text(value)
	if len(redacted) <= policy.MaxFieldBytes {
		return redacted
	}
	return "[ARTIFACT EXTERNALIZED " + newArtifactRef(redacted) + "]"
}

// artifactRefOf extracts the artifact ref a field placeholder carries, if any.
func artifactRefOf(field string) (string, bool) {
	const marker = "[ARTIFACT EXTERNALIZED "
	start := strings.Index(field, marker)
	if start < 0 {
		return "", false
	}
	ref := field[start+len(marker):]
	if len(ref) == 0 || ref[len(ref)-1] != ']' {
		return "", false
	}
	return ref[:len(ref)-1], true
}

// isBinaryPayload reports whether the value cannot be treated as text: either
// invalid UTF-8 or a dominant share of control bytes.
func isBinaryPayload(value string) bool {
	if !utf8.ValidString(value) {
		return true
	}
	control := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			control++
		}
	}
	return control*interactionDAGBinaryControlRatio > len(value)
}

// newArtifactRef derives the deterministic, content-addressed reference for
// an externalized field. The hash covers the redacted projection, so the
// artifact addresses safe content and stays stable across retries.
func newArtifactRef(redacted string) string {
	sum := sha256.Sum256([]byte(redacted))
	return "artifact:sha256:" + hex.EncodeToString(sum[:])
}

// nullableText reads a nullable text column value.
func nullableText(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
